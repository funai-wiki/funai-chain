package p2p

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/cometbft/cometbft/crypto/secp256k1"
	pubsub "github.com/libp2p/go-libp2p-pubsub"

	p2phost "github.com/funai-wiki/funai-chain/p2p/host"
	"github.com/funai-wiki/funai-chain/p2p/leader"
	p2ptypes "github.com/funai-wiki/funai-chain/p2p/types"
	"github.com/funai-wiki/funai-chain/p2p/worker"
	vrftypes "github.com/funai-wiki/funai-chain/x/vrf/types"
)

// startDispatchLoops launches message dispatch goroutines for all subscribed topics.
// B1: This is the missing pubsub message distribution loop that routes incoming P2P
// messages to the appropriate handler (Leader, Worker, Verifier, Proposer).
func (n *Node) startDispatchLoops(ctx context.Context) error {
	for _, modelId := range n.Config.ModelIds {
		// Model topic: InferRequest, AssignTask, InferReceipt
		modelTopic := p2phost.ModelTopic(modelId)
		modelSub, err := n.Host.Subscribe(modelTopic)
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", modelTopic, err)
		}
		go n.dispatchModelMessages(ctx, modelId, modelSub)

		// Verify result topic: VerifyResult from verifiers -> Proposer
		verifyResultTopic := fmt.Sprintf("%s/verify", modelTopic)
		verifyResultSub, err := n.Host.Subscribe(verifyResultTopic)
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", verifyResultTopic, err)
		}
		go n.dispatchVerifyResults(ctx, verifyResultSub)
	}

	// Per-node verify topic: VerifyPayload directed to this node's verifier
	if n.Config.WorkerAddr != "" {
		verifyTopic := fmt.Sprintf("/funai/verify/%s", n.Config.WorkerAddr)
		verifySub, err := n.Host.Subscribe(verifyTopic)
		if err != nil {
			return fmt.Errorf("subscribe %s: %w", verifyTopic, err)
		}
		go n.dispatchVerifyPayloads(ctx, verifySub)
	}

	// Settlement topic: AuditRequest / SecondVerificationResponse
	settleSub, err := n.Host.Subscribe(p2phost.SettlementTopic)
	if err != nil {
		return fmt.Errorf("subscribe settlement: %w", err)
	}
	go n.dispatchSettlementMessages(ctx, settleSub)

	log.Printf("  Dispatch loops started for %d model(s)", len(n.Config.ModelIds))
	return nil
}

// ── Topic Dispatch Loops ────────────────────────────────────────────────────

// dispatchModelMessages reads from a model topic and routes by message type.
// Discrimination uses unique JSON field presence: leader_sig → AssignTask,
// result_hash → InferReceipt, user_signature → InferRequest.
func (n *Node) dispatchModelMessages(ctx context.Context, modelId string, sub *pubsub.Subscription) {
	for {
		msg, err := sub.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("dispatch[model/%s]: %v", modelId, err)
			return
		}

		if msg.ReceivedFrom == n.Host.ID() {
			continue
		}

		data := n.DecryptMessage(ctx, msg.Data)

		var raw map[string]json.RawMessage
		if json.Unmarshal(data, &raw) != nil {
			continue
		}

		if _, ok := raw["leader_sig"]; ok {
			var task p2ptypes.AssignTask
			if json.Unmarshal(data, &task) == nil && len(task.TaskId) > 0 {
				go n.handleAssignTask(ctx, &task)
			}
		} else if _, ok := raw["target_verifier"]; ok {
			// VerifyPayload routed via model topic — must check before result_hash
			// since VerifyPayload also contains result_hash
			var payload worker.VerifyPayload
			if json.Unmarshal(data, &payload) == nil && len(payload.TaskId) > 0 {
				if payload.TargetVerifier == "" || payload.TargetVerifier == n.Config.WorkerAddr {
					go n.handleVerifyPayload(ctx, &payload)
				}
			}
		} else if _, ok := raw["result_hash"]; ok {
			var receipt p2ptypes.InferReceipt
			if json.Unmarshal(data, &receipt) == nil && len(receipt.TaskId) > 0 {
				n.handleInferReceipt(&receipt)
			}
		} else if _, ok := raw["user_signature"]; ok {
			var req p2ptypes.InferRequest
			if json.Unmarshal(data, &req) == nil && len(req.UserPubkey) > 0 {
				go n.handleInferRequest(ctx, &req)
			}
		}
	}
}

// dispatchVerifyPayloads reads VerifyPayload messages directed to this node's verifier.
func (n *Node) dispatchVerifyPayloads(ctx context.Context, sub *pubsub.Subscription) {
	for {
		msg, err := sub.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("dispatch[verify-payload]: %v", err)
			return
		}

		if msg.ReceivedFrom == n.Host.ID() {
			continue
		}

		data := n.DecryptMessage(ctx, msg.Data)

		var payload worker.VerifyPayload
		if json.Unmarshal(data, &payload) == nil && len(payload.TaskId) > 0 {
			go n.handleVerifyPayload(ctx, &payload)
		}
	}
}

// dispatchVerifyResults reads VerifyResult messages and feeds them to the Proposer.
func (n *Node) dispatchVerifyResults(ctx context.Context, sub *pubsub.Subscription) {
	for {
		msg, err := sub.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("dispatch[verify-result]: %v", err)
			return
		}

		if msg.ReceivedFrom == n.Host.ID() {
			continue
		}

		data := n.DecryptMessage(ctx, msg.Data) // KT: decrypt consistency fix

		var result p2ptypes.VerifyResult
		if json.Unmarshal(data, &result) == nil && len(result.TaskId) > 0 {
			n.handleVerifyResult(&result)
		}
	}
}

// dispatchSettlementMessages reads AuditRequest/SecondVerificationResponse from the settlement topic.
func (n *Node) dispatchSettlementMessages(ctx context.Context, sub *pubsub.Subscription) {
	for {
		msg, err := sub.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("dispatch[settlement]: %v", err)
			return
		}

		if msg.ReceivedFrom == n.Host.ID() {
			continue
		}

		data := n.DecryptMessage(ctx, msg.Data) // KT: decrypt consistency fix

		var raw map[string]json.RawMessage
		if json.Unmarshal(data, &raw) != nil {
			continue
		}

		if _, ok := raw["second_verifier_addr"]; ok {
			var resp p2ptypes.SecondVerificationResponse
			if json.Unmarshal(data, &resp) == nil {
				n.handleSecondVerificationResponse(&resp)
			}
		} else if _, ok := raw["proposer_sig"]; ok {
			var req p2ptypes.AuditRequest
			if json.Unmarshal(data, &req) == nil {
				go n.handleAuditRequest(ctx, &req)
			}
		}
	}
}

// ── Message Handlers ────────────────────────────────────────────────────────

func (n *Node) handleInferRequest(ctx context.Context, req *p2ptypes.InferRequest) {
	modelId := string(req.ModelId)
	l, ok := n.Leaders[modelId]
	if !ok {
		return
	}

	var blockHash []byte
	if n.Chain != nil {
		bh, _, err := n.Chain.GetLatestBlockHash(ctx)
		if err == nil {
			blockHash = bh
		}
	}

	workerAddr, err := l.HandleRequest(ctx, req, blockHash)
	if err != nil {
		log.Printf("dispatch: leader.HandleRequest: %v", err)
		return
	}

	n.Proposer.AddRequest(req)

	taskId := req.TaskId()
	log.Printf("dispatch: task %s dispatched to %s", shortHex(taskId), workerAddr)
}

func (n *Node) handleAssignTask(ctx context.Context, task *p2ptypes.AssignTask) {
	if n.Worker == nil {
		return
	}

	// Convert cached workers to VerifierCandidate for verifier dispatch
	n.cachedWorkersMu.RLock()
	var verifiers []worker.VerifierCandidate
	for _, w := range n.cachedWorkers {
		verifiers = append(verifiers, worker.VerifierCandidate{
			Address: w.Address,
			Pubkey:  w.Pubkey,
			Stake:   w.Stake,
		})
	}
	n.cachedWorkersMu.RUnlock()

	receipt, err := n.Worker.HandleTask(ctx, task, task.DispatchBlockHash, verifiers, task.UserSeed, "")
	if err != nil {
		log.Printf("dispatch: worker.HandleTask: %v", err)
		return
	}

	log.Printf("dispatch: task %s completed, result=%s", shortHex(task.TaskId), shortHex(receipt.ResultHash))
}

func (n *Node) handleInferReceipt(receipt *p2ptypes.InferReceipt) {
	n.Proposer.AddReceipt(receipt)

	workerAddr := hex.EncodeToString(receipt.WorkerPubkey)
	for _, l := range n.Leaders {
		l.HandleReceiptBusyRelease(receipt.WorkerPubkey, workerAddr, receipt.TaskId)
	}

	log.Printf("dispatch: receipt for task %s from %s", shortHex(receipt.TaskId), shortHex(receipt.WorkerPubkey))
}

func (n *Node) handleVerifyPayload(ctx context.Context, payload *worker.VerifyPayload) {
	if n.Verifier == nil {
		return
	}

	result, err := n.Verifier.HandleVerifyRequest(ctx, payload)
	if err != nil {
		log.Printf("dispatch: verifier.HandleVerifyRequest: %v", err)
		return
	}

	// KT: determine model_id for broadcast topic from payload (multi-model fix)
	modelId := string(payload.ModelId)
	if modelId == "" {
		for _, mid := range n.Config.ModelIds {
			modelId = mid
			break
		}
	}
	if err := n.Verifier.BroadcastResult(ctx, result, modelId); err != nil {
		log.Printf("dispatch: verifier.BroadcastResult: %v", err)
	}

	log.Printf("dispatch: verified task %s pass=%v logits=%d/5", shortHex(payload.TaskId), result.Pass, result.LogitsMatch)
}

func (n *Node) handleVerifyResult(result *p2ptypes.VerifyResult) {
	if err := n.Proposer.AddVerifyResult(result); err != nil {
		// Expected for tasks not in our pending pool
		return
	}
	log.Printf("dispatch: verify result for task %s pass=%v", shortHex(result.TaskId), result.Pass)
}

func (n *Node) handleAuditRequest(ctx context.Context, req *p2ptypes.AuditRequest) {
	if n.Verifier == nil {
		return
	}

	// Re-use verifier's teacher forcing logic for audit
	payload := &worker.VerifyPayload{
		TaskId:           req.TaskId,
		Prompt:           req.Prompt,
		Output:           req.Output,
		WorkerLogits:     req.WorkerLogits,
		Temperature:      float32(req.Temperature) / 10000.0,
		FinalSeed:        req.FinalSeed,
		SampledTokens:    req.SampledTokens,
		InputTokenCount:  req.InputTokenCount,
		OutputTokenCount: req.OutputTokenCount,
		WorkerPubkey:     req.WorkerPubkey,
	}

	result, err := n.Verifier.HandleVerifyRequest(ctx, payload)
	if err != nil {
		log.Printf("dispatch: audit verify: %v", err)
		return
	}

	resp := p2ptypes.SecondVerificationResponse{
		TaskId:               req.TaskId,
		Pass:                 result.Pass,
		SecondVerifierAddr:   n.Verifier.Pubkey,
		LogitsHash:           result.LogitsHash,
		VerifiedInputTokens:  result.VerifiedInputTokens,
		VerifiedOutputTokens: result.VerifiedOutputTokens,
	}
	// KT: sign SecondVerificationResponse to prevent forgery
	if len(n.Config.WorkerPrivKey) == 32 {
		msgHash := sha256.Sum256(resp.SignBytes())
		privKey := secp256k1.PrivKey(n.Config.WorkerPrivKey)
		if sig, err := privKey.Sign(msgHash[:]); err == nil {
			resp.Signature = sig
		}
	}
	data, _ := json.Marshal(resp)
	_ = n.Host.Publish(ctx, p2phost.SettlementTopic, data)

	log.Printf("dispatch: audit response for task %s pass=%v", shortHex(req.TaskId), result.Pass)
}

func (n *Node) handleSecondVerificationResponse(resp *p2ptypes.SecondVerificationResponse) {
	// KT: verify second_verifier signature to prevent forged audit results
	if len(resp.Signature) == 0 || len(resp.SecondVerifierAddr) == 0 {
		log.Printf("dispatch: reject unsigned audit response for task %s", shortHex(resp.TaskId))
		return
	}
	second_verifierPubkey, err := n.Chain.GetWorkerPubkey(string(resp.SecondVerifierAddr))
	if err != nil || len(second_verifierPubkey) != 33 {
		log.Printf("dispatch: reject audit response, cannot verify second_verifier %s: %v", shortHex(resp.SecondVerifierAddr), err)
		return
	}
	msgHash := sha256.Sum256(resp.SignBytes())
	pk := secp256k1.PubKey(second_verifierPubkey)
	if !pk.VerifySignature(msgHash[:], resp.Signature) {
		log.Printf("dispatch: reject audit response with invalid signature for task %s", shortHex(resp.TaskId))
		return
	}

	complete, pass := n.Proposer.CollectSecondVerificationResponse(resp)
	if complete {
		log.Printf("dispatch: audit complete for task %s pass=%v", shortHex(resp.TaskId), pass)
	}
}

// ── Worker List Refresh ─────────────────────────────────────────────────────

// refreshWorkerList periodically queries the chain for active workers and updates
// all subsystems (Leader, Verifier, Proposer) with fresh VRF ranking data.
func (n *Node) refreshWorkerList(ctx context.Context) {
	// Immediate refresh on start
	n.doRefreshWorkerList(ctx)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.doRefreshWorkerList(ctx)
		}
	}
}

func (n *Node) doRefreshWorkerList(ctx context.Context) {
	if n.Chain == nil {
		return
	}

	entries, err := n.Chain.GetActiveWorkers(ctx)
	if err != nil {
		// Non-fatal: chain may not be ready yet during startup
		log.Printf("dispatch: refresh workers: %v", err)
	}

	var leaderInfos []leader.WorkerInfo
	var rankedWorkers []vrftypes.RankedWorker
	for _, e := range entries {
		stake, _ := sdkmath.NewIntFromString(e.Stake)
		if stake.IsZero() {
			stake = sdkmath.NewInt(1)
		}
		pubkey := decodePubkey(e.Pubkey)

		maxTasks := e.MaxConcurrentTasks
		if maxTasks == 0 {
			maxTasks = 1
		}
		leaderInfos = append(leaderInfos, leader.WorkerInfo{
			Address:            e.Address,
			Pubkey:             pubkey,
			Stake:              stake,
			MaxConcurrentTasks: maxTasks,
		})
		rep := float64(1.0)
		if e.ReputationScore > 0 {
			rep = float64(e.ReputationScore) / 10000.0
		}
		rankedWorkers = append(rankedWorkers, vrftypes.RankedWorker{
			Address:      e.Address,
			Pubkey:       pubkey,
			Stake:        stake,
			Reputation:   rep,
			AvgLatencyMs: e.AvgLatencyMs,
		})
	}

	// Fallback: if no workers found from chain query but this node is configured as a worker,
	// add self to ensure the P2P layer can function during cold start / API unavailability.
	if len(rankedWorkers) == 0 && n.Config.WorkerAddr != "" && len(n.Config.WorkerPubkey) > 0 {
		selfStake := sdkmath.NewInt(1)
		if stakeInfo, err := n.Chain.GetWorkerStake(n.Config.WorkerAddr); err == nil && stakeInfo.IsPositive() {
			selfStake = stakeInfo
		}
		leaderInfos = append(leaderInfos, leader.WorkerInfo{
			Address: n.Config.WorkerAddr,
			Pubkey:  n.Config.WorkerPubkey,
			Stake:   selfStake,
		})
		rankedWorkers = append(rankedWorkers, vrftypes.RankedWorker{
			Address: n.Config.WorkerAddr,
			Pubkey:  n.Config.WorkerPubkey,
			Stake:   selfStake,
		})
		log.Printf("dispatch: added self as fallback worker (%s)", n.Config.WorkerAddr)
	}

	for _, l := range n.Leaders {
		l.SetWorkers(leaderInfos)
	}
	n.Verifier.SetActiveWorkers(rankedWorkers)
	n.Proposer.SetActiveWorkers(rankedWorkers)

	// P0-5: set current leader pubkey on Worker for AssignTask signature verification.
	// Determine the actual VRF-elected leader for each model and set the pubkey.
	// For now, Leader signs with its own key; Worker can verify once it knows who the leader is.
	// TODO: implement proper VRF leader election lookup to set correct leader pubkey.
	// Currently the check is skipped when CurrentLeaderPubkey is not set (safe default).

	n.cachedWorkersMu.Lock()
	n.cachedWorkers = rankedWorkers
	n.cachedWorkersMu.Unlock()

	if len(rankedWorkers) > 0 {
		log.Printf("dispatch: refreshed %d workers", len(rankedWorkers))
	}
}

// ── Batch Settlement Loop ──────────────────────────────────────────────────

// startBatchLoop periodically processes pending tasks and submits BatchSettlement to the chain.
// C1: This is the missing link that wires Proposer.ProcessPending → BuildBatch → chain broadcast.
func (n *Node) startBatchLoop(ctx context.Context) {
	interval := n.Config.BatchInterval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			n.doBatchSettlement(ctx)
		}
	}
}

func (n *Node) doBatchSettlement(ctx context.Context) {
	if n.Chain == nil || n.Proposer == nil {
		return
	}

	// Get latest block hash for VRF audit check
	blockHash, _, err := n.Chain.GetLatestBlockHash(ctx)
	if err != nil {
		return // chain not ready, skip this tick
	}

	// Process pending tasks: moves CLEARED to batch queue, returns audit dispatches
	processed, audits := n.Proposer.ProcessPending(ctx, blockHash)

	// C2: Dispatch audit requests for tasks that triggered VRF audit
	for _, ad := range audits {
		data, err := json.Marshal(ad.Request)
		if err != nil {
			continue
		}
		_ = n.Host.Publish(ctx, p2phost.SettlementTopic, data)
		log.Printf("dispatch: audit dispatched for task %s", shortHex(ad.TaskId))
	}

	// Build batch from cleared tasks
	msg := n.Proposer.BuildBatch()
	if msg == nil {
		return // nothing to settle
	}

	if n.Config.WorkerAddr == "" || len(n.Config.WorkerPrivKey) == 0 {
		log.Printf("dispatch: batch ready (%d entries) but no signing key configured", len(msg.Entries))
		return
	}

	hash, err := n.Chain.BroadcastSettlement(ctx, msg, n.Config.WorkerPrivKey, n.Config.WorkerAddr, n.Config.ChainID)
	if err != nil {
		log.Printf("dispatch: BatchSettlement broadcast failed (entries retained for retry): %v", err)
		return // entries stay in Proposer.clearedTasks, will retry next tick
	}

	// Only clear after successful broadcast — prevents data loss on failure
	n.Proposer.CommitBatch()

	log.Printf("dispatch: BatchSettlement submitted tx=%s entries=%d (processed=%d audits=%d)",
		hash, len(msg.Entries), processed, len(audits))
}

// ── Helpers ─────────────────────────────────────────────────────────────────

func shortHex(b []byte) string {
	if len(b) > 8 {
		return fmt.Sprintf("%x..", b[:8])
	}
	return fmt.Sprintf("%x", b)
}

// decodePubkey decodes a pubkey string that may be base64 (Cosmos SDK default)
// or hex-encoded. Returns nil if both fail.
func decodePubkey(s string) []byte {
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == 33 {
		return b
	}
	if b, err := hex.DecodeString(s); err == nil && len(b) == 33 {
		return b
	}
	return nil
}
