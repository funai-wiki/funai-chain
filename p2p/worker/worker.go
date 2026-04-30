package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
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
	Address            string
	Pubkey             []byte
	PrivKey            []byte // S6: private key for signing InferReceipts
	ModelIds           []string
	Host               *p2phost.Host
	Engine             inference.Engine
	ChainClient        *chain.Client
	OutputObserver     OutputObserver
	activeTasks        sync.Map // P2-3: task_id dedup to prevent processing duplicate dispatches
	rebroadcastCancels sync.Map // P2-8: task_key → context.CancelFunc for rebroadcast termination
	// S1: concurrent inference control
	activeInferenceTasks atomic.Uint32
	maxConcurrentTasks   uint32

	// S4: per-model set of accepted Leader pubkeys/addresses for AssignTask signature
	// verification. Stores the top-3 VRF-ranked candidates so that:
	//   - Normal dispatch by rank#1 is accepted.
	//   - Leader failover to rank#2/#3 (§6.2) is accepted without waiting for next refresh.
	//   - Epoch transitions briefly tolerate both old and new leaders.
	// A nil/empty set for a model means "not yet known" — during cold start before the
	// first chain refresh, HandleTask treats this as bootstrap and permits the task.
	leadersMu      sync.RWMutex
	leaderPubkeys  map[string][][]byte // modelId → up to 3 pubkeys
	leaderAddrs    map[string][]string // modelId → up to 3 addresses (for logging / M12)
	leadersSeeded  bool                // true once SetLeadersForModel has been called at least once

	// Audit KT §5: latency tracking for self-assessment. EMA of inference duration
	// (engine call → receipt), matching what the Worker reports on-chain via
	// InferReceipt.InferenceLatencyMs. The field was previously named
	// avgFirstTokenMs, but the measurement is and always was total inference time —
	// measuring real TTFT would require per-token streaming instrumentation that
	// doesn't exist for the deterministic (temperature > 0) path. Renamed to match
	// reality and avoid driving spec decisions off a misnamed signal.
	avgInferenceMs atomic.Uint32 // EMA of InferenceLatencyMs (ms)

	// Test-only: when true, the Worker deliberately corrupts the signed
	// receipt's ResultHash so it disagrees with the content actually streamed
	// out. This emulates a malicious Worker and is used by
	// scripts/e2e-mock-fraud.sh to exercise the SDK's M7 fraud-detection
	// + MsgFraudProof submission path end-to-end without depending on a real
	// attacker. Must never be set by a production binary — a live Worker with
	// this flag would get slashed on chain on every task. Operators see a loud
	// startup warning from cmd/funai-node when FUNAI_TEST_CORRUPT_RECEIPT=1
	// is set.
	testCorruptReceipt bool
}

// SetLeadersForModel updates the accepted top-3 VRF-ranked Leader pubkeys / addresses
// for the given model. Called by dispatch.go after each worker-list refresh.
// Passing an empty list clears the entry (leaves the model in "unknown" state).
func (w *Worker) SetLeadersForModel(modelId string, addrs []string, pubkeys [][]byte) {
	w.leadersMu.Lock()
	defer w.leadersMu.Unlock()
	if w.leaderPubkeys == nil {
		w.leaderPubkeys = make(map[string][][]byte)
		w.leaderAddrs = make(map[string][]string)
	}
	if len(pubkeys) == 0 {
		delete(w.leaderPubkeys, modelId)
		delete(w.leaderAddrs, modelId)
		return
	}
	w.leaderPubkeys[modelId] = append([][]byte(nil), pubkeys...)
	w.leaderAddrs[modelId] = append([]string(nil), addrs...)
	w.leadersSeeded = true
}

// isLeaderAuthorized returns true if any of the top-3 accepted Leader pubkeys for the
// model validly signs the given AssignTask. The second return value `bootstrap` is true
// if no leader set has ever been seeded (cold-start) — the caller should treat this as
// a permit-with-warning rather than a hard reject.
func (w *Worker) isLeaderAuthorized(task *p2ptypes.AssignTask) (authorized bool, bootstrap bool) {
	w.leadersMu.RLock()
	defer w.leadersMu.RUnlock()
	if !w.leadersSeeded {
		return false, true
	}
	pubkeys := w.leaderPubkeys[string(task.ModelId)]
	if len(pubkeys) == 0 {
		// Leaders known for some models but not this one — do NOT treat as bootstrap.
		return false, false
	}
	for _, pk := range pubkeys {
		if verifyAssignTaskSigWith(task, pk) {
			return true, false
		}
	}
	return false, false
}

// KnownLeaderAddrsForModel returns the current accepted Leader address list (for logging).
func (w *Worker) KnownLeaderAddrsForModel(modelId string) []string {
	w.leadersMu.RLock()
	defer w.leadersMu.RUnlock()
	addrs := w.leaderAddrs[modelId]
	if len(addrs) == 0 {
		return nil
	}
	return append([]string(nil), addrs...)
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

// SetTestCorruptReceipt enables the Worker-side fraud injection used by
// scripts/e2e-mock-fraud.sh. See Worker.testCorruptReceipt for the safety
// note — never call this from production code paths.
func (w *Worker) SetTestCorruptReceipt(enabled bool) {
	w.testCorruptReceipt = enabled
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

	// S4: the AssignTask MUST carry a Leader signature that verifies against one of the
	// top-3 VRF-ranked Leader pubkeys we know for this model. This is the only thing
	// standing between a random network peer and the ability to forge dispatch for this
	// Worker (spoofing prompt, fee, billing, task_id, etc.). Semantics:
	//   - Bootstrap (no leader set has ever been seeded): permit, but log loudly so
	//     operators notice if refresh is failing in production.
	//   - Leader set known for other models but empty for this model: hard FAIL (unknown
	//     Leader for this task's model).
	//   - Sig missing or doesn't match any of the top-3 known Leader pubkeys: hard FAIL.
	// The legacy M12 `leaderAddr` string is unreliable (dispatch always passes "") so we
	// authenticate purely from the signature.
	authorized, bootstrap := w.isLeaderAuthorized(task)
	if bootstrap {
		fmt.Printf("S4: leader set not yet seeded — permitting task %x during cold start\n", task.TaskId[:8])
	} else if !authorized {
		w.sendAcceptTask(ctx, task.TaskId, false)
		known := w.KnownLeaderAddrsForModel(string(task.ModelId))
		return nil, fmt.Errorf("S4: AssignTask for task %x is not signed by any of the top-3 accepted leaders for model %s (known=%v)", task.TaskId[:8], task.ModelId, known)
	}
	_ = leaderAddr // M12 legacy parameter; retained for call-site stability but no longer used

	// Audit KT §4/§5: reject if we cannot meet the user's latency requirement.
	// avgInferenceMs is total inference time (engine call → receipt). We compare it
	// to MaxLatencyMs as an upper-bound proxy for TTFT — if total inference is
	// already over the budget, TTFT is guaranteed to be too. Under-approximates TTFT
	// (over-rejects some valid tasks) but never accepts a task we cannot meet.
	if task.MaxLatencyMs > 0 {
		avgMs := w.avgInferenceMs.Load()
		if avgMs > 0 && avgMs > task.MaxLatencyMs {
			w.sendAcceptTask(ctx, task.TaskId, false)
			return nil, fmt.Errorf("cannot meet latency requirement: avg %dms > max %dms", avgMs, task.MaxLatencyMs)
		}
		// If GPU concurrency is high, estimated latency scales linearly.
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

	// Audit KT §5: update inference-duration EMA for self-assessment. The same
	// inferMs value is embedded into the outgoing InferReceipt below so the chain
	// can update the Worker's on-chain AvgLatencyMs from a Worker-signed sample
	// rather than a noisy proposer wall-clock measurement.
	inferMs := uint32(time.Since(inferStart).Milliseconds())
	if inferMs > 0 {
		prev := w.avgInferenceMs.Load()
		if prev == 0 {
			w.avgInferenceMs.Store(inferMs)
		} else {
			// Exponential moving average: new = 0.8*old + 0.2*sample
			updated := (prev*4 + inferMs) / 5
			w.avgInferenceMs.Store(updated)
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
		TaskId:             task.TaskId,
		WorkerPubkey:       w.Pubkey,
		WorkerLogits:       logProbs,
		ResultHash:         resultHash[:],
		FinalSeed:           finalSeedHash[:],
		SampledTokens:      tokenIDs,
		InputTokenCount:    inputTokenCount,
		OutputTokenCount:   outputTokenCount,
		InferenceLatencyMs: inferMs,
	}

	// Test-only fraud injection: tamper the receipt's ResultHash so it no
	// longer matches the content that was already streamed out. The SDK's
	// M7 check will catch this and submit a MsgFraudProof; on chain, the
	// settlement keeper will verify the contradiction between the Worker's
	// ContentSig (over real content) and the Worker's ReceiptSig (over the
	// tampered hash) and slash. See comment on Worker.testCorruptReceipt
	// for guard rails.
	if w.testCorruptReceipt {
		tampered := make([]byte, len(resultHash))
		copy(tampered, resultHash[:])
		tampered[0] ^= 0xFF
		receipt.ResultHash = tampered
		log.Printf("WORKER TEST-ONLY: tampered ResultHash for task %x to exercise fraud-proof path", task.TaskId[:8])
	}

	// S6: Worker must sign the InferReceipt as proof of work
	receipt.WorkerSig = w.signReceipt(receipt)

	receiptData, _ := json.Marshal(receipt)
	topic := p2phost.ModelTopic(string(task.ModelId))
	_ = w.Host.Publish(ctx, topic, receiptData)
	// Also publish on the per-task topic that the SDK subscribes to for its
	// M7 fraud-detection check (sdk/client.go:266). Before this double-publish
	// the SDK never received receipts — its receipt subscription was on
	// /funai/receipt/<taskId> but Worker only published to /funai/model/<modelId>
	// so the entire M7 `result_hash mismatch → submit MsgFraudProof` path was
	// silently dead. Dispatching to a narrow per-task topic lets the SDK keep
	// a clean scoped subscription without filtering every model-topic message.
	receiptTopic := fmt.Sprintf("/funai/receipt/%x", task.TaskId)
	_ = w.Host.Publish(ctx, receiptTopic, receiptData)

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
			TaskId:             task.TaskId,
			Prompt:             task.Prompt,
			Output:             completeOutput,
			WorkerLogits:       logProbs,
			ResultHash:         resultHash[:],
			Temperature:        temperature,
			TopP:               topP,
			FinalSeed:          finalSeedHash[:],
			SampledTokens:      tokenIDs,
			InputTokenCount:    inputTokenCount,
			OutputTokenCount:   outputTokenCount,
			InferenceLatencyMs: inferMs,
			UserSeed:           userSeed,
			DispatchBlockHash:  blockHash,
			WorkerAddress:      w.Address,
			WorkerPubkey:       w.Pubkey,
			WorkerSig:          receipt.WorkerSig,
			ModelId:            task.ModelId, // KT: for multi-model topic routing
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
// S6 §7.3, §10.2: proves "Worker actually did the inference".
// Delegates the canonical byte layout to InferReceipt.SignBytes so the signer,
// the verifier (p2p/verifier), and the SDK all read from one definition.
func (w *Worker) signReceipt(receipt *p2ptypes.InferReceipt) []byte {
	if len(w.PrivKey) != 32 {
		return nil
	}
	var privKey secp256k1.PrivKey = w.PrivKey
	sig, err := privKey.Sign(receipt.SignBytes())
	if err != nil {
		return nil
	}
	return sig
}

// sendAcceptTask publishes an AcceptTask response back to the Leader.
// P2-6 §6.2: "Worker must reply accept/reject within 1 second"
//
// KT non-state-machine Issue A: sign BOTH accept and reject (pre-fix only
// the accepted path was signed, leaving forged rejects undetectable), and
// sign the canonical SignBytes bound to (taskId, worker pubkey, accepted)
// rather than just taskId. The pubkey is included in the message so Leader
// can verify without an out-of-band lookup.
func (w *Worker) sendAcceptTask(ctx context.Context, taskId []byte, accepted bool) {
	accept := p2ptypes.AcceptTask{
		TaskId:   taskId,
		Accepted: accepted,
	}
	if len(w.PrivKey) == 32 {
		privKey := secp256k1.PrivKey(w.PrivKey)
		accept.WorkerPubkey = privKey.PubKey().Bytes()
		sig, err := privKey.Sign(accept.SignBytes())
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
	// Audit KT §5: Worker's signed inference duration; required in the payload so
	// the Verifier can reconstruct InferReceipt.SignBytes and validate WorkerSig.
	InferenceLatencyMs uint32     `json:"inference_latency_ms,omitempty"`
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
// verifyAssignTaskSigWith verifies the AssignTask's LeaderSig against the given pubkey.
// Returns false on any malformed input (empty sig, non-33-byte pubkey, etc.).
// The canonical digest lives on AssignTask.SigDigest() in p2p/types.
func verifyAssignTaskSigWith(task *p2ptypes.AssignTask, leaderPubkey []byte) bool {
	if len(task.LeaderSig) == 0 || len(leaderPubkey) != 33 {
		return false
	}
	digest := task.SigDigest()
	pubKey := secp256k1.PubKey(leaderPubkey)
	return pubKey.VerifySignature(digest[:], task.LeaderSig)
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
