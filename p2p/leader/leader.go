package leader

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	stdmath "math"
	"sync"
	"sync/atomic"
	"time"

	"cosmossdk.io/math"

	"github.com/cometbft/cometbft/crypto/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/p2p/chain"
	p2phost "github.com/funai-wiki/funai-chain/p2p/host"
	p2ptypes "github.com/funai-wiki/funai-chain/p2p/types"
	vrftypes "github.com/funai-wiki/funai-chain/x/vrf/types"
)

const (
	acceptTimeout      = 1 * time.Second
	addressRateLimit   = 10 // requests per second per address
	rateLimitResetSecs = 1

	// P2-2: mempool batch processing interval
	mempoolTickInterval = 100 * time.Millisecond

	// P2-3: auto-split threshold (TPS per sub-leader)
	autoSplitTPSThreshold = 500

	// P2-3: TPS tracking window
	tpsTrackingWindow = 10 * time.Second

	// M2: Leader epoch duration (§6.2: every model_id 30s rotation)
	LeaderEpochDuration = 30 * time.Second
)

// mempoolRequest wraps an incoming InferRequest with dispatch context.
type mempoolRequest struct {
	Req       *p2ptypes.InferRequest
	BlockHash []byte
	ResultCh  chan mempoolResult
}

type mempoolResult struct {
	WorkerAddr string
	Err        error
}

// Leader handles task dispatch for a model_id.
// V5.2 §6.2: VRF rank #1 → assign → 1s timeout → fallback up to 3.
// S1/S2: uses per-worker concurrency counters instead of boolean busy flag.
type Leader struct {
	ModelId     string
	Host        *p2phost.Host
	ChainClient *chain.Client
	PrivKey     []byte // P2-8: Leader's private key for signing AssignTask
	Workers     []WorkerInfo
	// S1: per-worker inference concurrency tracking (replaces BusyWorkers boolean)
	activeInferenceTasks map[string]uint32
	// S2: per-worker verification concurrency tracking (independent of inference)
	activeVerifyTasks map[string]uint32
	mu                sync.RWMutex

	// S9: per-task shadow balance tracking with expiry
	pendingFees map[string][]PendingEntry // userAddr → per-task entries

	// S8: rate limiter
	rateCounts  map[string]int
	rateResetAt time.Time

	// P2-2: mempool for batching incoming requests
	mempool chan mempoolRequest
	stopCh  chan struct{}
	stopped atomic.Bool

	// P2-3: TPS tracking for auto-split
	requestTimestamps []time.Time
	tpsMu             sync.Mutex
	splitN            int // current number of sub-topics (1 = no split)

	// M2: epoch rotation — Leader re-election every 30s (§6.2)
	epochStart time.Time

	// P2-4: Leader self-address for VRF legitimacy check
	SelfAddress string
	SelfPubkey  []byte
}

// PendingEntry tracks one per-token task's frozen fee for shadow balance (S9 §2.3).
type PendingEntry struct {
	TaskId      []byte
	MaxFee      uint64
	ExpireBlock uint64
}

// WorkerInfo holds a worker's info for VRF ranking.
type WorkerInfo struct {
	Address             string
	Pubkey              []byte
	Stake               math.Int
	MaxConcurrentTasks  uint32 // S1: inference concurrency limit (default 1)
	MaxConcurrentVerify uint32 // S2: verification concurrency limit (default 2)
}

func New(modelId string, privKey []byte, address string, pubkey []byte, host *p2phost.Host, chainClient *chain.Client) *Leader {
	l := &Leader{
		ModelId:              modelId,
		PrivKey:              privKey,
		SelfAddress:          address,
		SelfPubkey:           pubkey,
		Host:                 host,
		ChainClient:          chainClient,
		activeInferenceTasks: make(map[string]uint32),
		activeVerifyTasks:    make(map[string]uint32),
		pendingFees:          make(map[string][]PendingEntry),
		rateCounts:           make(map[string]int),
		rateResetAt:          time.Now().Add(rateLimitResetSecs * time.Second),
		mempool:              make(chan mempoolRequest, 4096),
		stopCh:               make(chan struct{}),
		splitN:               1,
		epochStart:           time.Now(),
	}
	go l.mempoolLoop()
	return l
}

// Stop gracefully shuts down the mempool processing loop.
func (l *Leader) Stop() {
	if l.stopped.CompareAndSwap(false, true) {
		close(l.stopCh)
	}
}

// mempoolLoop processes batched requests every 100ms (P2-2).
func (l *Leader) mempoolLoop() {
	ticker := time.NewTicker(mempoolTickInterval)
	defer ticker.Stop()

	for {
		select {
		case <-l.stopCh:
			return
		case <-ticker.C:
			l.drainMempool()
		}
	}
}

// drainMempool collects all pending requests and dispatches them.
func (l *Leader) drainMempool() {
	var batch []mempoolRequest
	for {
		select {
		case req := <-l.mempool:
			batch = append(batch, req)
		default:
			goto dispatch
		}
	}
dispatch:
	for _, mr := range batch {
		addr, err := l.dispatchSingle(context.Background(), mr.Req, mr.BlockHash)
		mr.ResultCh <- mempoolResult{WorkerAddr: addr, Err: err}
	}

	// P2-3: update TPS tracking
	if len(batch) > 0 {
		l.trackTPS(len(batch))
	}
}

// trackTPS records request timestamps and recalculates auto-split N.
func (l *Leader) trackTPS(count int) {
	l.tpsMu.Lock()
	defer l.tpsMu.Unlock()

	now := time.Now()
	for i := 0; i < count; i++ {
		l.requestTimestamps = append(l.requestTimestamps, now)
	}

	// Prune old timestamps outside the tracking window
	cutoff := now.Add(-tpsTrackingWindow)
	start := 0
	for start < len(l.requestTimestamps) && l.requestTimestamps[start].Before(cutoff) {
		start++
	}
	l.requestTimestamps = l.requestTimestamps[start:]

	// Calculate recent TPS
	if len(l.requestTimestamps) > 0 {
		recentTPS := float64(len(l.requestTimestamps)) / tpsTrackingWindow.Seconds()
		newN := int(stdmath.Ceil(recentTPS / autoSplitTPSThreshold))
		if newN < 1 {
			newN = 1
		}
		l.splitN = newN
	}
}

// GetSubTopicIndex returns which sub-topic a task should be routed to (P2-3).
// sub_topic_id = hash(task_id) % N
func (l *Leader) GetSubTopicIndex(taskId []byte) int {
	l.tpsMu.Lock()
	n := l.splitN
	l.tpsMu.Unlock()
	if n <= 1 {
		return 0
	}
	h := binary.BigEndian.Uint32(taskId[:4])
	return int(h % uint32(n))
}

// SetWorkers updates the worker list (from chain query).
func (l *Leader) SetWorkers(workers []WorkerInfo) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Workers = workers
}

// HandleRequest enqueues an InferRequest into the mempool (P2-2).
// Pre-validates signature, prompt hash, rate limit, and balance before enqueuing.
func (l *Leader) HandleRequest(ctx context.Context, req *p2ptypes.InferRequest, blockHash []byte) (string, error) {
	// §23: reject temperature > 20000 (temperature_max = 20000, i.e. 2.0)
	if req.Temperature > 20000 {
		return "", fmt.Errorf("temperature %d exceeds max 20000", req.Temperature)
	}
	// TopP validation: 0 or 10000 = disabled, 1-9999 = nucleus sampling
	if req.TopP > 10000 {
		return "", fmt.Errorf("top_p %d exceeds max 10000", req.TopP)
	}

	// S8: verify request signature
	if !verifyInferRequestSignature(req) {
		return "", fmt.Errorf("invalid request signature")
	}

	// M5: verify prompt_hash matches SHA256(prompt)
	promptHash := sha256.Sum256([]byte(req.Prompt))
	if !bytes.Equal(promptHash[:], req.PromptHash) {
		return "", fmt.Errorf("prompt_hash mismatch")
	}

	userAddr := hex.EncodeToString(req.UserPubkey)

	// S8: rate limiting (per-address)
	if !l.checkRateLimit(userAddr) {
		return "", fmt.Errorf("rate limit exceeded for %s", userAddr)
	}

	// S9: validate fee mode (per-request vs per-token)
	if err := req.ValidateFeeMode(); err != nil {
		return "", err
	}

	// S8 + M10: balance check with local overspend tracking
	// S9: use EffectiveFee (MaxFee for per-token, Fee for per-request)
	if l.ChainClient != nil {
		// Derive bech32 address from secp256k1 pubkey for chain REST query
		bech32Addr := pubkeyToBech32(req.UserPubkey)
		if err := l.checkBalanceWithPending(ctx, userAddr, bech32Addr, req.EffectiveFee()); err != nil {
			return "", err
		}
	}

	// P2-2: enqueue into mempool for batch processing
	resultCh := make(chan mempoolResult, 1)
	mr := mempoolRequest{
		Req:       req,
		BlockHash: blockHash,
		ResultCh:  resultCh,
	}

	select {
	case l.mempool <- mr:
	default:
		return "", fmt.Errorf("mempool full, try again later")
	}

	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case result := <-resultCh:
		return result.WorkerAddr, result.Err
	}
}

// dispatchSingle handles the actual VRF ranking and worker assignment for one request.
func (l *Leader) dispatchSingle(ctx context.Context, req *p2ptypes.InferRequest, blockHash []byte) (string, error) {
	userAddr := hex.EncodeToString(req.UserPubkey)

	l.mu.Lock()
	defer l.mu.Unlock()

	taskId := req.TaskId()
	seed := append(taskId, blockHash...)

	var ranked []vrftypes.RankedWorker
	for _, w := range l.Workers {
		// S1: skip workers that have reached their inference concurrency limit
		maxTasks := w.MaxConcurrentTasks
		if maxTasks == 0 {
			maxTasks = 1 // default for unset/legacy workers
		}
		if l.activeInferenceTasks[w.Address] >= maxTasks {
			continue
		}
		stake := w.Stake
		if stake.IsZero() {
			stake = math.NewInt(1)
		}
		ranked = append(ranked, vrftypes.RankedWorker{
			Address: w.Address,
			Pubkey:  w.Pubkey,
			Stake:   stake,
		})
	}

	if len(ranked) == 0 {
		return "", fmt.Errorf("no available workers for model %s", l.ModelId)
	}

	ranked = vrftypes.RankWorkers(seed, ranked, vrftypes.AlphaDispatch)

	// M5: try top 3 workers with 1s accept timeout each
	maxFallback := 3
	if maxFallback > len(ranked) {
		maxFallback = len(ranked)
	}

	// P2-3: determine sub-topic for high-TPS routing
	subIdx := l.GetSubTopicIndex(taskId)
	_ = subIdx // used in topic routing when splitN > 1

	for i := 0; i < maxFallback; i++ {
		w := ranked[i]

		assign := p2ptypes.AssignTask{
			TaskId:            taskId,
			ModelId:           req.ModelId,
			Prompt:            req.Prompt,
			Fee:               req.MaxFee,
			UserAddr:          req.UserPubkey,
			Temperature:       req.Temperature,
			TopP:              req.TopP,
			UserSeed:          req.UserSeed,
			DispatchBlockHash: blockHash,
			FeePerInputToken:  req.FeePerInputToken,
			FeePerOutputToken: req.FeePerOutputToken,
			MaxFee:            req.MaxFee,
			MaxLatencyMs:      req.MaxLatencyMs,
			StreamMode:        req.StreamMode,
		}
		// Forward max_tokens from InferRequest; S9 per-token mode may override with budget-derived limit
		if req.MaxTokens > 0 {
			assign.MaxTokens = req.MaxTokens
		}
		if req.IsPerToken() && req.FeePerOutputToken > 0 {
			budgetTokens := uint32(req.MaxFee / req.FeePerOutputToken)
			if assign.MaxTokens == 0 || budgetTokens < assign.MaxTokens {
				assign.MaxTokens = budgetTokens
			}
		}
		// P1-5: Sign complete AssignTask fields including temperature/seed/blockHash
		// S9: also covers per-token billing fields to prevent MITM tampering
		if len(l.PrivKey) == 32 {
			sigData := sha256.New()
			sigData.Write(taskId)
			sigData.Write(req.ModelId)
			sigData.Write([]byte(req.Prompt))
			feeBuf := make([]byte, 8)
			binary.BigEndian.PutUint64(feeBuf, req.MaxFee)
			sigData.Write(feeBuf)
			sigData.Write(req.UserPubkey)
			tempBuf := make([]byte, 2)
			binary.BigEndian.PutUint16(tempBuf, req.Temperature)
			sigData.Write(tempBuf)
			sigData.Write(req.UserSeed)
			sigData.Write(blockHash)
			fipBuf := make([]byte, 8)
			binary.BigEndian.PutUint64(fipBuf, req.FeePerInputToken)
			sigData.Write(fipBuf)
			fopBuf := make([]byte, 8)
			binary.BigEndian.PutUint64(fopBuf, req.FeePerOutputToken)
			sigData.Write(fopBuf)
			mfBuf := make([]byte, 8)
			binary.BigEndian.PutUint64(mfBuf, req.MaxFee)
			sigData.Write(mfBuf)
			mtBuf := make([]byte, 4)
			binary.BigEndian.PutUint32(mtBuf, assign.MaxTokens)
			sigData.Write(mtBuf)
			h := sigData.Sum(nil)
			msgHash := sha256.Sum256(h)
			privKey := secp256k1.PrivKey(l.PrivKey)
			sig, err := privKey.Sign(msgHash[:])
			if err == nil {
				assign.LeaderSig = sig
			}
		}
		assignData, _ := json.Marshal(assign)

		topic := p2phost.ModelTopic(l.ModelId)
		if err := l.Host.Publish(ctx, topic, assignData); err != nil {
			continue
		}

		// M5: wait for AcceptTask response with 1s timeout
		accepted := l.waitForAccept(ctx, taskId, w.Address)
		if !accepted {
			continue
		}

		l.activeInferenceTasks[w.Address]++
		l.pendingFees[userAddr] = append(l.pendingFees[userAddr], PendingEntry{
			TaskId:      taskId,
			MaxFee:      req.EffectiveFee(),
			ExpireBlock: req.ExpireBlock,
		})

		return w.Address, nil
	}

	return "", fmt.Errorf("all workers rejected for model %s", l.ModelId)
}

// waitForAccept waits for a Worker's AcceptTask response.
// Returns true if accepted within timeout, false otherwise.
func (l *Leader) waitForAccept(ctx context.Context, taskId []byte, workerAddr string) bool {
	acceptTopic := fmt.Sprintf("/funai/accept/%x", taskId)
	sub, err := l.Host.Subscribe(acceptTopic)
	if err != nil {
		return true // if can't subscribe, optimistically accept
	}

	// Use a timeout context to block on sub.Next instead of busy-waiting with default case
	timeoutCtx, cancel := context.WithTimeout(ctx, acceptTimeout)
	defer cancel()

	for {
		msg, err := sub.Next(timeoutCtx)
		if err != nil {
			// Timeout or context cancellation
			return false
		}
		var accept p2ptypes.AcceptTask
		if err := json.Unmarshal(msg.Data, &accept); err != nil {
			continue
		}
		return accept.Accepted
	}
}

// verifyInferRequestSignature validates the user's secp256k1 signature.
// S7/S8: cryptographic verification prevents spam with forged requests.
func verifyInferRequestSignature(req *p2ptypes.InferRequest) bool {
	if len(req.UserSignature) == 0 || len(req.UserPubkey) == 0 {
		return false
	}
	signBytes := req.SignBytes()
	msgHash := sha256.Sum256(signBytes)

	var pubKey secp256k1.PubKey = req.UserPubkey
	return pubKey.VerifySignature(msgHash[:], req.UserSignature)
}

// checkRateLimit enforces per-address rate limiting.
// S8: 10 requests/second/address.
// P3-3: must be concurrency-safe (called outside l.mu in HandleRequest).
func (l *Leader) checkRateLimit(userAddr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.After(l.rateResetAt) {
		l.rateCounts = make(map[string]int)
		l.rateResetAt = now.Add(rateLimitResetSecs * time.Second)
	}
	l.rateCounts[userAddr]++
	return l.rateCounts[userAddr] <= addressRateLimit
}

// checkBalanceWithPending verifies on-chain balance minus local pending fees.
// S9 §2.3: shadow balance with per-task entries and expiry cleanup.
func (l *Leader) checkBalanceWithPending(ctx context.Context, userAddrHex, userAddr string, fee uint64) error {
	balance, err := l.ChainClient.GetInferenceBalance(ctx, userAddr)
	if err != nil {
		return fmt.Errorf("query balance: %w", err)
	}
	l.cleanExpiredPending()
	var totalPending uint64
	for _, entry := range l.pendingFees[userAddrHex] {
		totalPending += entry.MaxFee
	}
	if balance <= totalPending || balance-totalPending < fee {
		return fmt.Errorf("insufficient available balance: on-chain %d - pending %d < fee %d",
			balance, totalPending, fee)
	}
	return nil
}

// ReleaseBusy decrements a worker's inference task count and removes pending entry by taskId.
func (l *Leader) ReleaseBusy(workerAddr string, userAddr string, taskId []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.activeInferenceTasks[workerAddr] > 0 {
		l.activeInferenceTasks[workerAddr]--
	}
	l.removePendingEntry(userAddr, taskId)
}

// HandleReceiptBusyRelease processes an InferReceipt observed on P2P to release one inference slot.
func (l *Leader) HandleReceiptBusyRelease(workerPubkey []byte, userAddr string, taskId []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()

	workerAddr := hex.EncodeToString(workerPubkey)
	if l.activeInferenceTasks[workerAddr] > 0 {
		l.activeInferenceTasks[workerAddr]--
		l.removePendingEntry(userAddr, taskId)
	}
}

// removePendingEntry removes a specific task from the user's pending list.
func (l *Leader) removePendingEntry(userAddr string, taskId []byte) {
	entries := l.pendingFees[userAddr]
	for i, e := range entries {
		if bytes.Equal(e.TaskId, taskId) {
			l.pendingFees[userAddr] = append(entries[:i], entries[i+1:]...)
			if len(l.pendingFees[userAddr]) == 0 {
				delete(l.pendingFees, userAddr)
			}
			return
		}
	}
}

// cleanExpiredPending removes entries past their ExpireBlock (S9 §2.3).
func (l *Leader) cleanExpiredPending() {
	currentBlock := uint64(time.Now().Unix() / 5) // approximate block height
	for user, entries := range l.pendingFees {
		kept := entries[:0]
		for _, e := range entries {
			if e.ExpireBlock == 0 || currentBlock <= e.ExpireBlock {
				kept = append(kept, e)
			}
		}
		if len(kept) == 0 {
			delete(l.pendingFees, user)
		} else {
			l.pendingFees[user] = kept
		}
	}
}

// IsEpochExpired returns true if the current leader epoch (30s) has elapsed (M2 §6.2).
// The caller (node) should trigger VRF re-election when this returns true.
func (l *Leader) IsEpochExpired() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return time.Since(l.epochStart) >= LeaderEpochDuration
}

// ResetEpoch starts a new leader epoch (called after re-election).
func (l *Leader) ResetEpoch() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.epochStart = time.Now()
}

// CleanupStaleBusy resets all concurrency counters (called on leader epoch expiry or stale detection).
// S1: counters are rebuilt from Worker state via LeaderSync or reset to zero.
func (l *Leader) CleanupStaleBusy(maxDuration time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Reset all counters — stale state is worse than undercount.
	// New tasks will re-increment naturally.
	l.activeInferenceTasks = make(map[string]uint32)
	l.activeVerifyTasks = make(map[string]uint32)
}

// ReleaseVerify decrements a worker's verify task count (S2).
func (l *Leader) ReleaseVerify(workerAddr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.activeVerifyTasks[workerAddr] > 0 {
		l.activeVerifyTasks[workerAddr]--
	}
}

// AcquireVerify increments a worker's verify task count (S2).
func (l *Leader) AcquireVerify(workerAddr string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.activeVerifyTasks[workerAddr]++
}

// IsVerifyAvailable returns true if the worker can accept more verify tasks (S2).
// Inference-busy workers are NOT excluded — only verify-full workers are.
func (l *Leader) IsVerifyAvailable(workerAddr string, maxVerify uint32) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if maxVerify == 0 {
		maxVerify = 2 // default
	}
	return l.activeVerifyTasks[workerAddr] < maxVerify
}

// CurrentSplitN returns the current auto-split factor (P2-3).
func (l *Leader) CurrentSplitN() int {
	l.tpsMu.Lock()
	defer l.tpsMu.Unlock()
	return l.splitN
}

// ---- P2-4: Leader Failover Monitor ----

const (
	// LeaderFailoverTimeout is 1.5s — if no leader activity observed, switch to rank#2.
	LeaderFailoverTimeout = 1500 * time.Millisecond
)

// LeaderMonitor monitors leader activity and triggers failover.
// Runs on each Worker node to detect leader liveness.
type LeaderMonitor struct {
	lastActivity atomic.Int64 // unix nano of last observed leader activity
	currentRank  int          // VRF rank of current leader (0 = primary)
	maxRank      int          // max failover rank (typically 2 for rank#2)
	mu           sync.Mutex
	onFailover   func(newRank int) // callback when failover triggers
}

// NewLeaderMonitor creates a leader activity monitor (P2-4).
func NewLeaderMonitor(onFailover func(newRank int)) *LeaderMonitor {
	m := &LeaderMonitor{
		currentRank: 0,
		maxRank:     2,
		onFailover:  onFailover,
	}
	m.lastActivity.Store(time.Now().UnixNano())
	return m
}

// RecordActivity updates the last observed leader activity timestamp.
func (m *LeaderMonitor) RecordActivity() {
	m.lastActivity.Store(time.Now().UnixNano())
}

// CurrentRank returns the current leader rank being used.
func (m *LeaderMonitor) CurrentRank() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.currentRank
}

// Run starts monitoring leader activity. Call in a goroutine.
// Checks every 500ms whether leader has been silent for >1.5s.
func (m *LeaderMonitor) Run(ctx context.Context) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			lastNano := m.lastActivity.Load()
			elapsed := time.Since(time.Unix(0, lastNano))
			if elapsed > LeaderFailoverTimeout {
				m.mu.Lock()
				if m.currentRank < m.maxRank {
					m.currentRank++
					rank := m.currentRank
					m.mu.Unlock()
					if m.onFailover != nil {
						m.onFailover(rank)
					}
					// Reset activity timer after failover
					m.lastActivity.Store(time.Now().UnixNano())
				} else {
					m.mu.Unlock()
				}
			}
		}
	}
}

// ResetToRank0 resets the monitor to use the primary leader.
func (m *LeaderMonitor) ResetToRank0() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.currentRank = 0
	m.lastActivity.Store(time.Now().UnixNano())
}

// pubkeyToBech32 converts a compressed secp256k1 public key to a bech32 "funai1..." address.
func pubkeyToBech32(pubkeyBytes []byte) string {
	if len(pubkeyBytes) != 33 {
		return hex.EncodeToString(pubkeyBytes)
	}
	pk := secp256k1.PubKey(pubkeyBytes)
	addr := sdk.AccAddress(pk.Address())
	return addr.String()
}
