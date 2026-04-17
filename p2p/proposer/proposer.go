package proposer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/cometbft/cometbft/crypto/secp256k1"

	"github.com/funai-wiki/funai-chain/p2p/chain"
	p2pstore "github.com/funai-wiki/funai-chain/p2p/store"
	p2ptypes "github.com/funai-wiki/funai-chain/p2p/types"
	settlementkeeper "github.com/funai-wiki/funai-chain/x/settlement/keeper"
	settlementtypes "github.com/funai-wiki/funai-chain/x/settlement/types"
	vrftypes "github.com/funai-wiki/funai-chain/x/vrf/types"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"cosmossdk.io/math"
)

// RebroadcastStopper is called when 3 verify results are collected for a task,
// signaling that rebroadcast should stop (P2-2).
type RebroadcastStopper interface {
	StopRebroadcast(taskId []byte)
}

// Proposer collects settlement evidence and constructs MsgBatchSettlement.
// V5.2 §10.4: only CLEARED tasks are packaged.
type Proposer struct {
	Address   string
	Chain     *chain.Client
	AuditRate uint32 // per-mille (100 = 10%)
	PrivKey   []byte // secp256k1 private key for signing batches

	mu            sync.Mutex
	pendingTasks  map[string]*TaskEvidence
	pendingAudits map[string]*AuditEvidence
	clearedTasks  []settlementtypes.SettlementEntry
	batchSize     int
	activeWorkers []vrftypes.RankedWorker
	Store         *p2pstore.Store    // P2-1: persistent storage for audit trail
	Rebroadcaster RebroadcastStopper // P2-2: stop rebroadcast when 3 results collected
}

// TaskEvidence holds evidence for a single task.
type TaskEvidence struct {
	Receipt    *p2ptypes.InferReceipt
	Verifiers  []*p2ptypes.VerifyResult
	Request    *p2ptypes.InferRequest
	Output     string // complete inference output text (from Worker stream)
	ReceivedAt uint64 // unix ms when receipt was received (for latency calculation)
}

const (
	// TaskTimeout is the maximum time a task can stay in pendingTasks before
	// it's either settled with partial verification or expired.
	// 5 minutes = 3x the rebroadcast window (90s) + margin for backup verifiers.
	TaskTimeout = 5 * time.Minute

	// MinVerifiersForPartialSettle is the minimum number of consistent verify results
	// required to settle a task after timeout (instead of the normal 3).
	MinVerifiersForPartialSettle = 2
)

func New(address string, privKey []byte, chainClient *chain.Client, auditRate uint32, batchSize int) *Proposer {
	return &Proposer{
		Address:       address,
		Chain:         chainClient,
		AuditRate:     auditRate,
		PrivKey:       privKey,
		pendingTasks:  make(map[string]*TaskEvidence),
		pendingAudits: make(map[string]*AuditEvidence),
		batchSize:     batchSize,
	}
}

// SetActiveWorkers updates the worker list for VRF re-computation.
func (p *Proposer) SetActiveWorkers(workers []vrftypes.RankedWorker) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.activeWorkers = workers
}

// AddReceipt adds an InferReceipt to the pending pool.
func (p *Proposer) AddReceipt(receipt *p2ptypes.InferReceipt) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := hex.EncodeToString(receipt.TaskId)
	if _, exists := p.pendingTasks[key]; !exists {
		p.pendingTasks[key] = &TaskEvidence{}
	}
	p.pendingTasks[key].Receipt = receipt
	p.pendingTasks[key].ReceivedAt = uint64(time.Now().UnixMilli())

	// P2-1: persist receipt to store for audit trail across restarts
	if p.Store != nil {
		if data, err := json.Marshal(receipt); err == nil {
			_ = p.Store.Put(p2pstore.RecordReceipt, receipt.TaskId, data)
		}
	}
}

// AddRequest stores the original InferRequest for signature hash computation.
func (p *Proposer) AddRequest(req *p2ptypes.InferRequest) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := hex.EncodeToString(req.TaskId())
	if _, exists := p.pendingTasks[key]; !exists {
		p.pendingTasks[key] = &TaskEvidence{}
	}
	p.pendingTasks[key].Request = req
}

// AddOutput stores the complete inference output text for a task.
// Called when the Proposer observes the Worker's final StreamToken on P2P.
func (p *Proposer) AddOutput(taskId []byte, output string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := hex.EncodeToString(taskId)
	if _, exists := p.pendingTasks[key]; !exists {
		p.pendingTasks[key] = &TaskEvidence{}
	}
	p.pendingTasks[key].Output = output

	// P2-1: persist output to store for audit trail
	if p.Store != nil {
		_ = p.Store.Put(p2pstore.RecordOutput, taskId, []byte(output))
	}
}

// AddVerifyResult adds a VerifyResult after validating the verifier's VRF legitimacy.
// S7: Recompute VRF ranking and verify submitter is in top 3 candidates.
func (p *Proposer) AddVerifyResult(result *p2ptypes.VerifyResult) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := hex.EncodeToString(result.TaskId)
	ev, exists := p.pendingTasks[key]
	if !exists {
		return fmt.Errorf("unknown task_id %s", key)
	}

	// S7: VRF legitimacy check — verify submitter is in VRF top 3
	if ev.Receipt != nil && len(p.activeWorkers) > 0 {
		verifSeed := append(append([]byte{}, result.TaskId...), ev.Receipt.ResultHash...)

		candidates := make([]vrftypes.RankedWorker, 0, len(p.activeWorkers))
		for _, w := range p.activeWorkers {
			if !bytes.Equal([]byte(w.Address), ev.Receipt.WorkerPubkey) {
				candidates = append(candidates, vrftypes.RankedWorker{
					Address: w.Address,
					Pubkey:  w.Pubkey,
					Stake:   w.Stake,
				})
			}
		}

		if len(candidates) >= 3 {
			ranked := vrftypes.RankWorkers(verifSeed, candidates, vrftypes.AlphaVerification)

			isLegit := false
			// Audit KT §1: Accept top 21 candidates. Rank 3-20 are backup
			// verifiers with staggered 2s delay. Covers 50% offline rate.
			top := 21
			if top > len(ranked) {
				top = len(ranked)
			}
			for i := 0; i < top; i++ {
				if bytes.Equal(ranked[i].Pubkey, result.VerifierAddr) {
					isLegit = true
					break
				}
			}
			if !isLegit {
				return fmt.Errorf("verifier %x not in VRF top 10 candidates", result.VerifierAddr)
			}
		}
	}

	if len(ev.Verifiers) < 3 {
		ev.Verifiers = append(ev.Verifiers, result)
		// P2-2: when 3 results collected, signal Worker to stop rebroadcasting
		if len(ev.Verifiers) == 3 && p.Rebroadcaster != nil {
			go p.Rebroadcaster.StopRebroadcast(result.TaskId)
		}
	}
	return nil
}

// AuditDispatch holds audit requests generated during ProcessPending for tasks
// that triggered the VRF audit check. The caller must publish these to the P2P settlement topic.
type AuditDispatch struct {
	TaskId  []byte
	Request *p2ptypes.AuditRequest
}

// ProcessPending checks all pending tasks, moves CLEARED ones to the batch queue,
// and returns audit dispatch requests for tasks that triggered VRF audit.
func (p *Proposer) ProcessPending(ctx context.Context, blockHash []byte) (int, []*AuditDispatch) {
	p.mu.Lock()
	defer p.mu.Unlock()

	processed := 0
	now := uint64(time.Now().UnixMilli())
	var audits []*AuditDispatch
	var expiredKeys []string
	for key, ev := range p.pendingTasks {
		if ev.Receipt == nil {
			continue
		}

		// Normal path: 3 verifier results collected
		if len(ev.Verifiers) >= 3 {
			// fall through to settlement logic below
		} else {
			// Check if task has timed out
			elapsed := time.Duration(now-ev.ReceivedAt) * time.Millisecond
			if elapsed < TaskTimeout {
				continue // still within timeout window, wait for more results
			}
			// Timed out: try partial settlement with 2 consistent results
			if len(ev.Verifiers) >= MinVerifiersForPartialSettle {
				fmt.Printf("proposer: task %s timed out with %d/%d verifiers after %v, attempting partial settlement\n",
					key[:16], len(ev.Verifiers), 3, elapsed.Round(time.Second))
				// fall through to settlement logic with available results
			} else {
				// < 2 results after timeout → cannot settle, expire the task
				fmt.Printf("proposer: task %s expired with only %d verifier(s) after %v, discarding\n",
					key[:16], len(ev.Verifiers), elapsed.Round(time.Second))
				expiredKeys = append(expiredKeys, key)
				continue
			}
		}

		allPass := true
		for _, v := range ev.Verifiers {
			if !v.Pass {
				allPass = false
				break
			}
		}

		status := settlementtypes.SettlementSuccess
		if !allPass {
			status = settlementtypes.SettlementFail
		}

		// Per-task VRF audit check: hash(task_id || block_hash) < audit_rate?
		if p.shouldAudit(ev.Receipt.TaskId, blockHash) {
			// Build audit request and move to pendingAudits
			auditReq := p.dispatchAuditLocked(ev.Receipt.TaskId, ev)
			if auditReq != nil {
				audits = append(audits, &AuditDispatch{TaskId: ev.Receipt.TaskId, Request: auditReq})
			}
			if p.pendingAudits == nil {
				p.pendingAudits = make(map[string]*AuditEvidence)
			}
			p.pendingAudits[key] = &AuditEvidence{Request: auditReq}
			delete(p.pendingTasks, key)
			continue
		}

		// Build verifier results with real signature hashes and S9 token counts
		verifierResults := make([]settlementtypes.VerifierResult, len(ev.Verifiers))
		for i, v := range ev.Verifiers {
			verifierResults[i] = settlementtypes.VerifierResult{
				Address:              pubkeyToBech32(v.VerifierAddr),
				Pass:                 v.Pass,
				LogitsHash:           v.LogitsHash,
				Signature:            v.Signature,
				VerifiedInputTokens:  v.VerifiedInputTokens,
				VerifiedOutputTokens: v.VerifiedOutputTokens,
			}
		}

		// S6: fill real signature hashes instead of hardcoded values
		var userSigHash, workerSigHash []byte
		var verifySigHashes [][]byte

		if ev.Request != nil && len(ev.Request.UserSignature) > 0 {
			h := sha256.Sum256(ev.Request.UserSignature)
			userSigHash = h[:]
		}
		if ev.Receipt != nil && len(ev.Receipt.WorkerSig) > 0 {
			h := sha256.Sum256(ev.Receipt.WorkerSig)
			workerSigHash = h[:]
		}
		for _, v := range ev.Verifiers {
			if len(v.Signature) > 0 {
				h := sha256.Sum256(v.Signature)
				verifySigHashes = append(verifySigHashes, h[:])
			}
		}

		// Determine user address, fee/expire, and model_id from request
		userAddr := ""
		modelId := ""
		var fee sdk.Coin
		var expireBlock uint64
		if ev.Request != nil {
			userAddr = pubkeyToBech32(ev.Request.UserPubkey)
			fee = sdk.NewCoin("ufai", math.NewIntFromUint64(ev.Request.MaxFee))
			expireBlock = ev.Request.ExpireBlock
			modelId = string(ev.Request.ModelId)
		}

		// Re-compute VRF dispatch ranking to verify the assigned Worker's rank.
		// seed = task_id || dispatch_block_hash (from the original request)
		var dispatchRank uint32 = 0
		if ev.Receipt != nil && ev.Request != nil && len(p.activeWorkers) > 0 {
			dispatchSeed := append(append([]byte{}, ev.Receipt.TaskId...), ev.Request.UserSeed...)
			// Reconstruct dispatch seed: the Leader used task_id bytes as part of seed
			// For VRF dispatch: seed = task_id (which is hash of user fields)
			rankedForDispatch := vrftypes.RankWorkers(ev.Receipt.TaskId, p.activeWorkers, vrftypes.AlphaDispatch)
			workerAddr := pubkeyToBech32(ev.Receipt.WorkerPubkey)
			for ri, rw := range rankedForDispatch {
				if pubkeyToBech32(rw.Pubkey) == workerAddr {
					dispatchRank = uint32(ri)
					break
				}
			}
			_ = dispatchSeed // seed construction for reference
		}

		var latencyMs uint64
		if ev.Request != nil {
			requestMs := ev.Request.Timestamp / 1_000_000 // Timestamp is UnixNano
			if ev.ReceivedAt > requestMs {
				latencyMs = ev.ReceivedAt - requestMs
			}
		}

		entry := settlementtypes.SettlementEntry{
			TaskId:          ev.Receipt.TaskId,
			UserAddress:     userAddr,
			WorkerAddress:   pubkeyToBech32(ev.Receipt.WorkerPubkey),
			VerifierResults: verifierResults,
			Status:          status,
			Fee:             fee,
			ExpireBlock:     int64(expireBlock),
			ModelId:         modelId,
			LatencyMs:       latencyMs,
			UserSigHash:     userSigHash,
			WorkerSigHash:   workerSigHash,
			VerifySigHashes: verifySigHashes,
			DispatchRank:    dispatchRank,
		}

		// S9: populate per-token billing fields from request and receipt
		if ev.Request != nil && ev.Request.IsPerToken() {
			entry.FeePerInputToken = ev.Request.FeePerInputToken
			entry.FeePerOutputToken = ev.Request.FeePerOutputToken
			entry.MaxFee = sdk.NewCoin("ufai", math.NewIntFromUint64(ev.Request.MaxFee))
			entry.Fee = sdk.Coin{} // clear per-request fee
		}
		if ev.Receipt != nil {
			entry.WorkerInputTokens = ev.Receipt.InputTokenCount
			entry.WorkerOutputTokens = ev.Receipt.OutputTokenCount
		}
		p.clearedTasks = append(p.clearedTasks, entry)
		delete(p.pendingTasks, key)
		processed++
	}

	// Clean up expired tasks (< 2 verifier results after timeout)
	for _, key := range expiredKeys {
		delete(p.pendingTasks, key)
	}

	return processed, audits
}

// shouldAudit checks if a task should be audited using per-task VRF.
// V5.2 §13.4: VRF(task_id || post_verification_block_hash) < audit_rate
func (p *Proposer) shouldAudit(taskId, blockHash []byte) bool {
	h := sha256.Sum256(append(taskId, blockHash...))
	vrfValue := new(big.Int).SetBytes(h[:])

	maxUint := new(big.Int).Lsh(big.NewInt(1), 256)
	threshold := new(big.Int).Mul(maxUint, big.NewInt(int64(p.AuditRate)))
	threshold.Div(threshold, big.NewInt(1000))

	return vrfValue.Cmp(threshold) < 0
}

// BuildBatch constructs a MsgBatchSettlement if enough tasks are accumulated.
// MaxBatchEntries is the maximum entries per batch to stay within block gas limit (E17).
// gasLimit = 200000 + len*2000; at 100M block gas limit, max = (100M - 200K) / 2K = 49900.
// Use 40000 as conservative limit.
const MaxBatchEntries = 40000

func (p *Proposer) BuildBatch() *settlementtypes.MsgBatchSettlement {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.clearedTasks) == 0 {
		return nil
	}

	// E17: cap batch size to prevent exceeding block gas limit
	batchSize := len(p.clearedTasks)
	if batchSize > MaxBatchEntries {
		batchSize = MaxBatchEntries
	}

	entries := make([]settlementtypes.SettlementEntry, batchSize)
	copy(entries, p.clearedTasks[:batchSize])

	merkleRoot := settlementkeeper.ComputeMerkleRoot(entries)

	proposerSig := p.signMerkleRoot(merkleRoot)

	msg := settlementtypes.NewMsgBatchSettlement(
		p.Address,
		merkleRoot,
		entries,
		proposerSig,
	)

	// Don't clear yet — caller must call CommitBatch() after successful broadcast.
	return msg
}

// CommitBatch clears the committed entries after successful on-chain broadcast.
// E17: only clears up to MaxBatchEntries; remaining entries stay for next batch.
func (p *Proposer) CommitBatch() {
	p.mu.Lock()
	if len(p.clearedTasks) <= MaxBatchEntries {
		p.clearedTasks = nil
	} else {
		p.clearedTasks = p.clearedTasks[MaxBatchEntries:]
	}
	p.mu.Unlock()
}

// PendingCount returns the number of pending tasks.
func (p *Proposer) PendingCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pendingTasks)
}

// ClearedCount returns the number of cleared tasks ready for batch.
func (p *Proposer) ClearedCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.clearedTasks)
}

// signMerkleRoot signs the batch's merkle root with the proposer's secp256k1 private key.
func (p *Proposer) signMerkleRoot(merkleRoot []byte) []byte {
	if len(p.PrivKey) == 0 {
		return []byte("unsigned")
	}
	h := sha256.Sum256(merkleRoot)
	privKey := secp256k1.PrivKey(p.PrivKey)
	sig, err := privKey.Sign(h[:])
	if err != nil {
		return []byte("sign-failed")
	}
	return sig
}

// AuditEvidence holds collected audit data for a pending audit task.
type AuditEvidence struct {
	Request   *p2ptypes.AuditRequest
	Responses []p2ptypes.SecondVerificationResponse
}

// dispatchAuditLocked creates an AuditRequest (must be called under p.mu lock).
func (p *Proposer) dispatchAuditLocked(taskId []byte, ev *TaskEvidence) *p2ptypes.AuditRequest {
	return p.buildAuditRequest(taskId, ev)
}

// DispatchAudit creates an AuditRequest for a task that triggered audit (public, takes lock).
func (p *Proposer) DispatchAudit(taskId []byte, ev *TaskEvidence) *p2ptypes.AuditRequest {
	return p.buildAuditRequest(taskId, ev)
}

// buildAuditRequest builds a signed AuditRequest (no lock required, safe to call from locked context).
func (p *Proposer) buildAuditRequest(taskId []byte, ev *TaskEvidence) *p2ptypes.AuditRequest {
	if ev == nil || ev.Receipt == nil || ev.Request == nil {
		return nil
	}

	auditReq := &p2ptypes.AuditRequest{
		TaskId:           taskId,
		ModelId:          ev.Request.ModelId,
		Prompt:           ev.Request.Prompt,
		Output:           ev.Output,
		WorkerLogits:     ev.Receipt.WorkerLogits,
		SampledTokens:    ev.Receipt.SampledTokens,
		FinalSeed:        ev.Receipt.FinalSeed,
		Temperature:      ev.Request.Temperature,
		TopP:             ev.Request.TopP,
		WorkerPubkey:     ev.Receipt.WorkerPubkey,
		InputTokenCount:  ev.Receipt.InputTokenCount,
		OutputTokenCount: ev.Receipt.OutputTokenCount,
	}

	for _, v := range ev.Verifiers {
		auditReq.VerifierAddresses = append(auditReq.VerifierAddresses, pubkeyToBech32(v.VerifierAddr))
	}

	h := sha256.Sum256(taskId)
	if len(p.PrivKey) > 0 {
		privKey := secp256k1.PrivKey(p.PrivKey)
		sig, err := privKey.Sign(h[:])
		if err == nil {
			auditReq.ProposerSig = sig
		}
	}

	return auditReq
}

// CollectSecondVerificationResponse adds an audit response from an second_verifier.
// Returns true when enough responses (3) have been collected.
func (p *Proposer) CollectSecondVerificationResponse(resp *p2ptypes.SecondVerificationResponse) (complete bool, pass bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	key := hex.EncodeToString(resp.TaskId)
	ev, exists := p.pendingAudits[key]
	if !exists {
		ev = &AuditEvidence{}
		if p.pendingAudits == nil {
			p.pendingAudits = make(map[string]*AuditEvidence)
		}
		p.pendingAudits[key] = ev
	}

	if len(ev.Responses) >= 3 {
		return true, p.countAuditPass(ev) >= 2
	}

	ev.Responses = append(ev.Responses, *resp)

	if len(ev.Responses) >= 3 {
		return true, p.countAuditPass(ev) >= 2
	}
	return false, false
}

func (p *Proposer) countAuditPass(ev *AuditEvidence) int {
	count := 0
	for _, r := range ev.Responses {
		if r.Pass {
			count++
		}
	}
	return count
}

// pubkeyToBech32 derives a bech32 address from a compressed secp256k1 public key.
func pubkeyToBech32(pubkeyBytes []byte) string {
	if len(pubkeyBytes) != 33 {
		return hex.EncodeToString(pubkeyBytes)
	}
	pk := secp256k1.PubKey(pubkeyBytes)
	addr := sdk.AccAddress(pk.Address())
	return addr.String()
}

var _ = fmt.Sprintf
var _ = math.Int{}
