package worker

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	gomath "math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdkmath "cosmossdk.io/math"

	"github.com/funai-wiki/funai-chain/p2p/chain"
	p2phost "github.com/funai-wiki/funai-chain/p2p/host"
	"github.com/funai-wiki/funai-chain/p2p/inference"
	p2ptypes "github.com/funai-wiki/funai-chain/p2p/types"
	vrftypes "github.com/funai-wiki/funai-chain/x/vrf/types"

	"github.com/cometbft/cometbft/crypto/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// OutputObserver is notified when a Worker completes inference and has the full output.
type OutputObserver interface {
	AddOutput(taskId []byte, output string)
}

// Worker handles inference execution and verification dispatch.
type Worker struct {
	Address             string
	Pubkey              []byte
	PrivKey             []byte // S6: private key for signing InferReceipts
	ModelIds            []string
	Host                *p2phost.Host
	Engine              inference.Engine
	ChainClient         *chain.Client
	CurrentLeader       string // M12: expected leader address for legitimacy check
	CurrentLeaderPubkey []byte // S4: leader's secp256k1 pubkey for AssignTask signature verification
	OutputObserver      OutputObserver
	activeTasks         sync.Map // P2-3: task_id dedup to prevent processing duplicate dispatches
	rebroadcastCancels  sync.Map // P2-8: task_key → context.CancelFunc for rebroadcast termination
	// S1: concurrent inference control
	activeInferenceTasks atomic.Uint32
	maxConcurrentTasks   uint32

	// Audit KT §4: latency tracking for self-assessment
	avgFirstTokenMs atomic.Uint32 // exponential moving average of first-token latency (ms)
}

// SetCurrentLeader updates the expected leader address and pubkey (from VRF rotation).
func (w *Worker) SetCurrentLeader(leader string) {
	w.CurrentLeader = leader
}

// SetCurrentLeaderPubkey updates the leader's pubkey for S4 AssignTask signature verification.
func (w *Worker) SetCurrentLeaderPubkey(pubkey []byte) {
	w.CurrentLeaderPubkey = pubkey
}

// InferenceEngine is the interface for running inference.
type InferenceEngine interface {
	Complete(ctx context.Context, prompt string, maxTokens int, temperature float32, seed *int64) (*inference.InferenceResult, error)
	Stream(ctx context.Context, prompt string, maxTokens int, temperature float32, seed *int64) (<-chan inference.StreamToken, <-chan error)
}

func New(address string, pubkey []byte, privKey []byte, modelIds []string, host *p2phost.Host, engine inference.Engine, chainClient *chain.Client) *Worker {
	return &Worker{
		Address:            address,
		Pubkey:             pubkey,
		PrivKey:            privKey,
		ModelIds:           modelIds,
		Host:               host,
		Engine:             engine,
		ChainClient:        chainClient,
		maxConcurrentTasks: 1, // S1: default, can be updated via SetMaxConcurrentTasks
	}
}

// SetMaxConcurrentTasks updates the inference concurrency limit (S1).
// Called when chain state is queried for the Worker's registered max_concurrent_tasks.
func (w *Worker) SetMaxConcurrentTasks(max uint32) {
	if max == 0 {
		max = 1
	}
	w.maxConcurrentTasks = max
}

// CheckUserBalance verifies that the user has sufficient on-chain balance.
// M9: Worker must verify balance before accepting to avoid wasting GPU.
func (w *Worker) CheckUserBalance(ctx context.Context, userAddr string, fee uint64) error {
	if w.ChainClient == nil {
		return nil
	}
	balance, err := w.ChainClient.GetInferenceBalance(ctx, userAddr)
	if err != nil {
		return fmt.Errorf("query balance: %w", err)
	}
	safetyFactor := uint64(3)
	if balance < fee*safetyFactor {
		return fmt.Errorf("insufficient balance: %d < %d*%d", balance, fee, safetyFactor)
	}
	return nil
}

// HandleTask processes an assigned task: run inference, stream to user, dispatch to verifiers.
// M12: leaderAddr is validated against the expected current leader.
// Temperature is uint16 matching AssignTask (0=argmax, 10000=1.0); converted to float32 internally.
func (w *Worker) HandleTask(ctx context.Context, task *p2ptypes.AssignTask, blockHash []byte, verifierWorkers []VerifierCandidate, userSeed []byte, leaderAddr string) (*p2ptypes.InferReceipt, error) {
	// G5: check TGI health before accepting — reject if GPU OOM or TGI crashed
	if w.Engine != nil && !w.Engine.IsHealthy(ctx) {
		w.sendAcceptTask(ctx, task.TaskId, false)
		return nil, fmt.Errorf("G5: TGI unhealthy, rejecting task to prevent OOM")
	}

	// S1: enforce inference concurrency limit
	if w.activeInferenceTasks.Load() >= uint32(w.maxConcurrentTasks) {
		w.sendAcceptTask(ctx, task.TaskId, false)
		return nil, fmt.Errorf("inference concurrency limit reached (%d/%d)", w.activeInferenceTasks.Load(), w.maxConcurrentTasks)
	}
	w.activeInferenceTasks.Add(1)
	defer w.activeInferenceTasks.Add(^uint32(0)) // decrement

	temperature := float32(task.Temperature) / 10000.0
	topP := float32(task.TopP) / 10000.0
	// P2-3: reject duplicate task dispatch
	taskKey := fmt.Sprintf("%x", task.TaskId)
	if _, loaded := w.activeTasks.LoadOrStore(taskKey, true); loaded {
		return nil, fmt.Errorf("duplicate task %s, already processing", taskKey)
	}
	defer w.activeTasks.Delete(taskKey)

	// M12: verify sender is the legitimate leader
	if w.CurrentLeader != "" && leaderAddr != "" && leaderAddr != w.CurrentLeader {
		// P2-6: send reject AcceptTask
		w.sendAcceptTask(ctx, task.TaskId, false)
		return nil, fmt.Errorf("task from non-leader %s, expected %s", leaderAddr, w.CurrentLeader)
	}

	// S4: verify AssignTask signature to prevent MITM tampering of prompt/fee/billing fields
	if len(task.LeaderSig) > 0 && len(w.CurrentLeaderPubkey) > 0 {
		if !w.verifyAssignTaskSig(task) {
			w.sendAcceptTask(ctx, task.TaskId, false)
			return nil, fmt.Errorf("S4: invalid AssignTask signature from leader")
		}
	}

	// Audit KT §4: reject if we cannot meet latency requirement
	if task.MaxLatencyMs > 0 {
		avgMs := w.avgFirstTokenMs.Load()
		// If we have latency history and it exceeds requirement, reject honestly (no reputation penalty)
		if avgMs > 0 && avgMs > task.MaxLatencyMs {
			w.sendAcceptTask(ctx, task.TaskId, false)
			return nil, fmt.Errorf("cannot meet latency requirement: avg %dms > max %dms", avgMs, task.MaxLatencyMs)
		}
		// If GPU concurrency is high, estimated latency scales linearly
		activeTasks := w.activeInferenceTasks.Load()
		if activeTasks > 0 && avgMs > 0 {
			estimatedMs := avgMs * (activeTasks + 1)
			if estimatedMs > task.MaxLatencyMs {
				w.sendAcceptTask(ctx, task.TaskId, false)
				return nil, fmt.Errorf("estimated latency %dms (avg %d × %d tasks) > max %dms",
					estimatedMs, avgMs, activeTasks+1, task.MaxLatencyMs)
			}
		}
	}

	// M9: check user balance before running inference
	// S9: use EffectiveFee (MaxFee for per-token, Fee for per-request)
	userBech32 := pubkeyToBech32(task.UserAddr)
	if err := w.CheckUserBalance(ctx, userBech32, task.EffectiveFee()); err != nil {
		w.sendAcceptTask(ctx, task.TaskId, false)
		return nil, fmt.Errorf("balance check: %w", err)
	}

	// P2-6: send accept AcceptTask response
	w.sendAcceptTask(ctx, task.TaskId, true)

	// Safety timeout: prevent single task from holding the inference slot indefinitely
	inferCtx, inferCancel := context.WithTimeout(ctx, 120*time.Second)
	defer inferCancel()
	ctx = inferCtx

	// Compute final_seed = SHA256(user_seed || dispatch_block_hash || task_id)
	seedInput := append(append([]byte{}, userSeed...), blockHash...)
	seedInput = append(seedInput, task.TaskId...)
	finalSeedHash := sha256.Sum256(seedInput)

	// S9: determine max output tokens — use task.MaxTokens if set, else default 2048
	maxOutputTokens := 2048
	if task.MaxTokens > 0 {
		maxOutputTokens = int(task.MaxTokens)
	}

	// S9: for per-token mode, tokenize prompt to get input token count for budget calculation
	var promptInputTokens int
	if task.IsPerToken() {
		tokens, err := w.Engine.Tokenize(ctx, task.Prompt)
		if err != nil {
			log.Printf("Worker: tokenize prompt failed (non-fatal): %v", err)
		} else {
			promptInputTokens = len(tokens)
		}
		// Refine maxOutputTokens based on actual input cost
		inputCost := uint64(promptInputTokens) * task.FeePerInputToken
		if inputCost < task.MaxFee {
			remaining := task.MaxFee - inputCost
			budgetTokens := remaining / task.FeePerOutputToken
			// Use 95% of budget to match Leader's StopSignal threshold
			budgetTokens = budgetTokens * 95 / 100
			if int(budgetTokens) < maxOutputTokens {
				maxOutputTokens = int(budgetTokens)
			}
		}
	}

	responseTopic := fmt.Sprintf("/funai/response/%x", task.TaskId)
	var completeOutput string
	var result *inference.InferenceResult
	inferStart := time.Now()

	if temperature > 0 {
		var err error
		// S9: use budget-aware generation for per-token mode
		if task.IsPerToken() {
			inputToks := uint32(promptInputTokens)
			result, err = w.Engine.DeterministicGenerateWithBudget(ctx, task.Prompt, maxOutputTokens, temperature, finalSeedHash[:], func(outputTokens uint32) bool {
				return shouldStopGeneration(task, inputToks, outputTokens)
			})
		} else {
			result, err = w.Engine.DeterministicGenerate(ctx, task.Prompt, maxOutputTokens, temperature, finalSeedHash[:])
		}
		if err != nil {
			return nil, fmt.Errorf("deterministic inference: %w", err)
		}
		completeOutput = result.Output

		for i, token := range result.Tokens {
			st := p2ptypes.StreamToken{
				TaskId:  task.TaskId,
				Token:   token.Text,
				Index:   uint32(i),
				IsFinal: i == len(result.Tokens)-1,
			}
			if st.IsFinal && len(w.PrivKey) == 32 {
				contentHash := sha256.Sum256([]byte(completeOutput))
				privKey := secp256k1.PrivKey(w.PrivKey)
				sig, err := privKey.Sign(contentHash[:])
				if err == nil {
					st.ContentSig = sig
				}
			}
			stData, _ := json.Marshal(st)
			_ = w.Host.Publish(ctx, responseTopic, stData)
		}
	} else {
		streamCh, errCh := w.Engine.Stream(ctx, task.Prompt, maxOutputTokens, 0, topP, nil)
		var fullOutput strings.Builder
		var tokenCount int

		for token := range streamCh {
			fullOutput.WriteString(token.Text)
			tokenCount++

			// S9 §2.4: check per-token budget limit
			budgetStop := shouldStopGeneration(task, uint32(promptInputTokens), uint32(tokenCount))
			isFinal := token.IsFinal || budgetStop

			st := p2ptypes.StreamToken{
				TaskId:  task.TaskId,
				Token:   token.Text,
				Index:   token.Index,
				IsFinal: isFinal,
			}
			if isFinal && len(w.PrivKey) == 32 {
				contentHash := sha256.Sum256([]byte(fullOutput.String()))
				privKey := secp256k1.PrivKey(w.PrivKey)
				sig, err := privKey.Sign(contentHash[:])
				if err == nil {
					st.ContentSig = sig
				}
			}
			stData, _ := json.Marshal(st)
			_ = w.Host.Publish(ctx, responseTopic, stData)

			if budgetStop {
				break
			}
		}

		select {
		case err := <-errCh:
			if err != nil {
				return nil, fmt.Errorf("inference stream: %w", err)
			}
		default:
		}

		completeOutput = fullOutput.String()

		var err error
		result, err = w.Engine.TeacherForce(ctx, task.Prompt, completeOutput, tokenCount)
		if err != nil {
			return nil, fmt.Errorf("teacher force for logits: %w", err)
		}
	}

	// Audit KT §4: update first-token latency EMA for self-assessment
	inferMs := uint32(time.Since(inferStart).Milliseconds())
	if inferMs > 0 {
		prev := w.avgFirstTokenMs.Load()
		if prev == 0 {
			w.avgFirstTokenMs.Store(inferMs)
		} else {
			// Exponential moving average: new = 0.8*old + 0.2*sample
			updated := (prev*4 + inferMs) / 5
			w.avgFirstTokenMs.Store(updated)
		}
	}

	if w.OutputObserver != nil {
		w.OutputObserver.AddOutput(task.TaskId, completeOutput)
	}

	resultHash := sha256.Sum256([]byte(completeOutput))

	// S2: VRF-based random position selection (positions depend on resultHash,
	// so worker cannot predict them before completing full output).
	positions := inference.SelectLogitsPositions(task.TaskId, resultHash[:], result.TokenCount)
	logProbs, tokenIDs := inference.ExtractLogitsAtPositions(result.Tokens, positions)

	// S9: input token count (from prompt tokenization above) and output token count
	inputTokenCount := uint32(promptInputTokens)
	if inputTokenCount == 0 && result != nil && result.InputTokenCount > 0 {
		inputTokenCount = uint32(result.InputTokenCount)
	}
	outputTokenCount := uint32(0)
	if result != nil {
		outputTokenCount = uint32(result.TokenCount)
	}

	receipt := &p2ptypes.InferReceipt{
		TaskId:           task.TaskId,
		WorkerPubkey:     w.Pubkey,
		WorkerLogits:     logProbs,
		ResultHash:       resultHash[:],
		FinalSeed:        finalSeedHash[:],
		SampledTokens:    tokenIDs,
		InputTokenCount:  inputTokenCount,
		OutputTokenCount: outputTokenCount,
	}

	// S6: Worker must sign the InferReceipt as proof of work
	receipt.WorkerSig = w.signReceipt(receipt)

	receiptData, _ := json.Marshal(receipt)
	topic := p2phost.ModelTopic(string(task.ModelId))
	_ = w.Host.Publish(ctx, topic, receiptData)

	// Select verifiers using VRF (alpha=0.5)
	verifSeed := append(append([]byte{}, task.TaskId...), resultHash[:]...)
	var ranked []vrftypes.RankedWorker
	for _, vc := range verifierWorkers {
		if vc.Address == w.Address {
			continue
		}
		stake := vc.Stake
		if stake.IsZero() {
			stake = sdkmath.NewInt(1)
		}
		ranked = append(ranked, vrftypes.RankedWorker{
			Address: vc.Address,
			Pubkey:  vc.Pubkey,
			Stake:   stake,
		})
	}

	log.Printf("worker: verify dispatch: %d candidates (need >=3), task=%x", len(ranked), task.TaskId[:8])
	if len(ranked) >= 3 {
		ranked = vrftypes.RankWorkers(verifSeed, ranked, vrftypes.AlphaVerification)

		verifyPayload := VerifyPayload{
			TaskId:            task.TaskId,
			Prompt:            task.Prompt,
			Output:            completeOutput,
			WorkerLogits:      logProbs,
			ResultHash:        resultHash[:],
			Temperature:       temperature,
			TopP:              topP,
			FinalSeed:         finalSeedHash[:],
			SampledTokens:     tokenIDs,
			InputTokenCount:   inputTokenCount,
			OutputTokenCount:  outputTokenCount,
			UserSeed:          userSeed,
			DispatchBlockHash: blockHash,
			WorkerAddress:     w.Address,
			WorkerPubkey:      w.Pubkey,
			WorkerSig:         receipt.WorkerSig,
			ModelId:           task.ModelId, // KT: for multi-model topic routing
		}
		// Publish verify payload to each target verifier via model topic
		// (all nodes subscribe to model topic; verifier filters by TargetVerifier)
		modelTopic := p2phost.ModelTopic(string(task.ModelId))
		verifyTopics := []string{modelTopic} // single topic, all nodes receive
		for i := 0; i < 3 && i < len(ranked); i++ {
			vp := verifyPayload
			vp.TargetVerifier = ranked[i].Address
			payloadBytes, _ := json.Marshal(vp)
			_ = w.Host.Publish(ctx, modelTopic, payloadBytes)
		}
		log.Printf("worker: published verify payloads to %s for %d verifiers", modelTopic, min(3, len(ranked)))
		payloadData, _ := json.Marshal(verifyPayload) // for rebroadcast

		// §23 rebroadcast_interval = 30s: rebroadcast verification requests if results not collected.
		// P2-8: create a cancellable context for rebroadcast so it can be stopped
		// when verification completes. Store cancel func for external signaling.
		rebroadcastCtx, rebroadcastCancel := context.WithCancel(ctx)
		taskKey := fmt.Sprintf("%x", task.TaskId)
		w.rebroadcastCancels.Store(taskKey, rebroadcastCancel)
		go w.rebroadcastVerifyPayload(rebroadcastCtx, task.TaskId, verifyTopics, payloadData)
	}

	return receipt, nil
}

// rebroadcastVerifyPayload re-publishes verification requests every 30s (§23 rebroadcast_interval).
// Stops after 3 rebroadcasts (90s total) or context cancellation (P2-8: early stop on verification complete).
func (w *Worker) rebroadcastVerifyPayload(ctx context.Context, taskId []byte, topics []string, payload []byte) {
	const rebroadcastInterval = 30 * time.Second
	const maxRebroadcasts = 3

	taskKey := fmt.Sprintf("%x", taskId)
	defer w.rebroadcastCancels.Delete(taskKey)

	ticker := time.NewTicker(rebroadcastInterval)
	defer ticker.Stop()

	for i := 0; i < maxRebroadcasts; i++ {
		select {
		case <-ctx.Done():
			log.Printf("Worker: rebroadcast cancelled for task %x", taskId[:8])
			return
		case <-ticker.C:
			log.Printf("Worker: rebroadcast verify payload for task %x (attempt %d/%d)", taskId[:8], i+1, maxRebroadcasts)
			for _, topic := range topics {
				_ = w.Host.Publish(ctx, topic, payload)
			}
		}
	}
}

// StopRebroadcast stops rebroadcasting verification requests for a task.
// Called when 3 verify results have been collected, per P2-8.
func (w *Worker) StopRebroadcast(taskId []byte) {
	taskKey := fmt.Sprintf("%x", taskId)
	if cancelFn, ok := w.rebroadcastCancels.LoadAndDelete(taskKey); ok {
		if cancel, ok := cancelFn.(context.CancelFunc); ok {
			cancel()
		}
	}
}

// signReceipt produces a secp256k1 signature over the InferReceipt canonical bytes.
// S6 §7.3, §10.2: the signature proves "Worker actually did the inference".
// S9: also covers InputTokenCount and OutputTokenCount.
func (w *Worker) signReceipt(receipt *p2ptypes.InferReceipt) []byte {
	h := sha256.New()
	h.Write(receipt.TaskId)
	h.Write(receipt.WorkerPubkey)
	h.Write(receipt.ResultHash)
	h.Write(receipt.FinalSeed)
	for i := 0; i < 5; i++ {
		bits := gomath.Float32bits(receipt.WorkerLogits[i])
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, bits)
		h.Write(buf)
	}
	for i := 0; i < 5; i++ {
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, receipt.SampledTokens[i])
		h.Write(buf)
	}
	// S9: include token counts in signature
	itcBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(itcBuf, receipt.InputTokenCount)
	h.Write(itcBuf)
	otcBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(otcBuf, receipt.OutputTokenCount)
	h.Write(otcBuf)
	msgHash := h.Sum(nil)

	if len(w.PrivKey) == 32 {
		var privKey secp256k1.PrivKey = w.PrivKey
		sig, err := privKey.Sign(msgHash)
		if err != nil {
			return nil
		}
		return sig
	}
	return nil
}

// sendAcceptTask publishes an AcceptTask response back to the Leader.
// P2-6 §6.2: "Worker must reply accept/reject within 1 second"
func (w *Worker) sendAcceptTask(ctx context.Context, taskId []byte, accepted bool) {
	accept := p2ptypes.AcceptTask{
		TaskId:   taskId,
		Accepted: accepted,
	}
	if accepted && len(w.PrivKey) == 32 {
		privKey := secp256k1.PrivKey(w.PrivKey)
		sig, err := privKey.Sign(taskId)
		if err == nil {
			accept.WorkerSig = sig
		}
	}
	data, _ := json.Marshal(accept)
	topic := fmt.Sprintf("/funai/accept/%x", taskId)
	_ = w.Host.Publish(ctx, topic, data)
}

// VerifierCandidate holds info for VRF verifier selection.
type VerifierCandidate struct {
	Address string
	Pubkey  []byte
	Stake   sdkmath.Int
}

// VerifyPayload is sent from Worker to Verifiers.
type VerifyPayload struct {
	TaskId            []byte     `json:"task_id"`
	Prompt            string     `json:"prompt"`
	Output            string     `json:"output"`
	WorkerLogits      [5]float32 `json:"worker_logits"`
	ResultHash        []byte     `json:"result_hash"`
	Temperature       float32    `json:"temperature"`
	TopP              float32    `json:"top_p"`
	FinalSeed         []byte     `json:"final_seed"`
	SampledTokens     [5]uint32  `json:"sampled_tokens"`
	InputTokenCount   uint32     `json:"input_token_count"`
	OutputTokenCount  uint32     `json:"output_token_count"`
	UserSeed          []byte     `json:"user_seed"`                 // S4: for final_seed verification
	DispatchBlockHash []byte     `json:"dispatch_block_hash"`       // S4: for final_seed verification
	WorkerAddress     string     `json:"worker_address"`            // M3: for VRF self-check
	WorkerPubkey      []byte     `json:"worker_pubkey"`             // P1-4: for signature verification
	WorkerSig         []byte     `json:"worker_sig"`                // P1-4: InferReceipt signature
	ModelId           []byte     `json:"model_id,omitempty"`        // KT: for multi-model topic routing
	TargetVerifier    string     `json:"target_verifier,omitempty"` // Intended verifier address for filtering
}

// shouldStopGeneration checks if the Worker should stop generating tokens due to per-token budget limit.
// S9 §2.4: Worker computes running_cost after each token and stops at 95% of max_fee.
// Returns false for per-request mode (no truncation).
func shouldStopGeneration(task *p2ptypes.AssignTask, inputTokenCount uint32, currentOutputTokens uint32) bool {
	if task.FeePerInputToken == 0 || task.FeePerOutputToken == 0 {
		return false // per-request mode
	}
	inputCost := uint64(inputTokenCount) * task.FeePerInputToken
	outputCost := uint64(currentOutputTokens) * task.FeePerOutputToken
	runningCost := inputCost + outputCost

	budgetLimit := task.MaxFee * 95 / 100
	// Ensure at least 1 output token can be generated
	minBudget := inputCost + task.FeePerOutputToken
	if budgetLimit < minBudget {
		budgetLimit = minBudget
	}
	if budgetLimit > task.MaxFee {
		budgetLimit = task.MaxFee
	}
	return runningCost >= budgetLimit
}

// verifyAssignTaskSig verifies the leader's signature on an AssignTask (S4).
// Replicates the signing logic from leader.go:362-391.
func (w *Worker) verifyAssignTaskSig(task *p2ptypes.AssignTask) bool {
	sigData := sha256.New()
	sigData.Write(task.TaskId)
	sigData.Write(task.ModelId)
	sigData.Write([]byte(task.Prompt))
	feeBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(feeBuf, task.MaxFee)
	sigData.Write(feeBuf)
	sigData.Write(task.UserAddr)
	tempBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(tempBuf, task.Temperature)
	sigData.Write(tempBuf)
	sigData.Write(task.UserSeed)
	sigData.Write(task.DispatchBlockHash)
	fipBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(fipBuf, task.FeePerInputToken)
	sigData.Write(fipBuf)
	fopBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(fopBuf, task.FeePerOutputToken)
	sigData.Write(fopBuf)
	mfBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(mfBuf, task.MaxFee)
	sigData.Write(mfBuf)
	mtBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(mtBuf, task.MaxTokens)
	sigData.Write(mtBuf)
	h := sigData.Sum(nil)
	msgHash := sha256.Sum256(h)

	pubKey := secp256k1.PubKey(w.CurrentLeaderPubkey)
	return pubKey.VerifySignature(msgHash[:], task.LeaderSig)
}

// pubkeyToBech32 converts a compressed secp256k1 public key to a bech32 "funai1..." address.
func pubkeyToBech32(pubkeyBytes []byte) string {
	if len(pubkeyBytes) != 33 {
		return string(pubkeyBytes) // fallback for non-standard keys
	}
	pk := secp256k1.PubKey(pubkeyBytes)
	addr := sdk.AccAddress(pk.Address())
	return addr.String()
}
