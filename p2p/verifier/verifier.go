package verifier

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	gomath "math"
	"sort"
	"time"

	sdkmath "cosmossdk.io/math"
	"github.com/cometbft/cometbft/crypto/secp256k1"

	p2phost "github.com/funai-wiki/funai-chain/p2p/host"
	"github.com/funai-wiki/funai-chain/p2p/inference"
	p2ptypes "github.com/funai-wiki/funai-chain/p2p/types"
	"github.com/funai-wiki/funai-chain/p2p/worker"
	vrftypes "github.com/funai-wiki/funai-chain/x/vrf/types"

	"golang.org/x/crypto/chacha20"
)

// Verifier runs teacher forcing and logits/sampling comparison.
// V5.2 §9.1-9.2: combined mismatch (logits + sampling) <= 2 = PASS.
type Verifier struct {
	Address string
	Pubkey  []byte
	PrivKey []byte // P0-5: private key for signing VerifyResult
	Stake   sdkmath.Int
	Host    *p2phost.Host
	Engine  inference.Engine
	Epsilon float32

	// M3: worker list for VRF self-check
	ActiveWorkers []vrftypes.RankedWorker
}

func New(address string, pubkey, privKey []byte, stake sdkmath.Int, host *p2phost.Host, engine inference.Engine, epsilon float32) *Verifier {
	return &Verifier{
		Address: address,
		Pubkey:  pubkey,
		PrivKey: privKey,
		Stake:   stake,
		Host:    host,
		Engine:  engine,
		Epsilon: epsilon,
	}
}

// SetActiveWorkers updates the worker list for VRF self-verification.
func (v *Verifier) SetActiveWorkers(workers []vrftypes.RankedWorker) {
	v.ActiveWorkers = workers
}

// HandleVerifyRequest processes a verification request from the executing Worker.
// P2-5: backup verifiers (rank 4-10) wait 2s before processing to let primary verifiers respond first.
func (v *Verifier) HandleVerifyRequest(ctx context.Context, payload *worker.VerifyPayload) (*p2ptypes.VerifyResult, error) {
	// M3: VRF self-check — confirm we are actually eligible as a verifier
	rank := v.vrfRank(payload)
	if rank < 0 {
		return nil, fmt.Errorf("not eligible as verifier for task %x (VRF self-check failed)", payload.TaskId)
	}

	// P2-5 §9.1: backup verifiers (rank >= 3) wait 2s before running teacher forcing
	if rank >= 3 {
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	var zeroLogits [5]float32

	// P1-4: Verify Worker's InferReceipt signature to prevent MITM tampering.
	if !v.verifyWorkerPayloadSignature(payload) {
		return v.makeResult(payload.TaskId, false, 0, 0, zeroLogits, 0, 0), nil
	}

	// S5 step 1: SHA256(complete output) == InferReceipt.result_hash
	outputHash := sha256.Sum256([]byte(payload.Output))
	if !bytes.Equal(outputHash[:], payload.ResultHash) {
		return v.makeResult(payload.TaskId, false, 0, 0, zeroLogits, 0, 0), nil
	}

	// S4: verify final_seed = SHA256(user_seed || dispatch_block_hash || task_id)
	if len(payload.UserSeed) > 0 || len(payload.DispatchBlockHash) > 0 {
		expectedSeed := computeFinalSeed(payload.UserSeed, payload.DispatchBlockHash, payload.TaskId)
		if !bytes.Equal(expectedSeed, payload.FinalSeed) {
			return v.makeResult(payload.TaskId, false, 0, 0, zeroLogits, 0, 0), nil
		}
	}

	// S9 §3.2: tokenize prompt to get verified input token count
	var verifiedInputTokens uint32
	if promptTokens, tErr := v.Engine.Tokenize(ctx, payload.Prompt); tErr == nil {
		verifiedInputTokens = uint32(len(promptTokens))
	}

	// Teacher forcing via TGI decoder_input_details
	result, err := v.Engine.TeacherForce(ctx, payload.Prompt, payload.Output, int(payload.OutputTokenCount))
	if err != nil {
		return nil, fmt.Errorf("teacher forcing: %w", err)
	}
	fmt.Printf("verifier: teacher force for task %x returned %d tokens\n", payload.TaskId[:8], result.TokenCount)

	// S9 §3.2: verified output token count from teacher forcing
	verifiedOutputTokens := uint32(result.TokenCount)

	// Guard: if teacher forcing returned 0 tokens, cannot verify.
	if result.TokenCount == 0 {
		fmt.Printf("verifier: teacher force returned 0 tokens for task %x, cannot verify\n", payload.TaskId[:8])
		return v.makeResult(payload.TaskId, false, 0, 0, zeroLogits, verifiedInputTokens, 0), nil
	}

	// VRF-based position selection (same deterministic positions as worker)
	positions := inference.SelectLogitsPositions(payload.TaskId, payload.ResultHash, result.TokenCount)
	verifierLogits, _ := inference.ExtractLogitsAtPositions(result.Tokens, positions)

	// S1: extract top-K logprobs at VRF positions for ChaCha20 sampling
	topK := inference.ExtractTopKAtPositions(result.Tokens, positions)

	// S5 step 2: verify sampled_tokens[p] == complete_output[positions[p]]
	// P1-3: use real tokenizer instead of whitespace splitting
	if payload.Temperature > 0 {
		outputTokens, err := v.tokenizeOutput(ctx, payload.Output)
		if err == nil {
			for i := 0; i < 5; i++ {
				pos := positions[i]
				if pos < len(outputTokens) && outputTokens[pos] != payload.SampledTokens[i] {
					return v.makeResult(payload.TaskId, false, 0, 0, zeroLogits, verifiedInputTokens, verifiedOutputTokens), nil
				}
			}
		}
	}

	// E14: guard against all-zero / near-constant logits collusion attack.
	// If Worker returns degenerate logits (all zero or variance below noise floor),
	// CompareLogits would match any equally-degenerate fake from a colluding verifier.
	// Reject before the comparison so degenerate-vs-degenerate cannot produce a PASS.
	if isLogitsDegenerate(payload.WorkerLogits) {
		return v.makeResult(payload.TaskId, false, 0, 0, zeroLogits, verifiedInputTokens, verifiedOutputTokens), nil
	}
	// Symmetric check: if our own teacher-forcing output is degenerate, the local TGI
	// likely failed. Submitting PASS based on such a comparison is unsafe — return FAIL
	// and let 2nd/3rd-tier verifiers decide.
	if isLogitsDegenerate(verifierLogits) {
		return v.makeResult(payload.TaskId, false, 0, 0, zeroLogits, verifiedInputTokens, verifiedOutputTokens), nil
	}

	logitsMatch := inference.CompareLogits(payload.WorkerLogits, verifierLogits, v.Epsilon)

	// Sampling verification when temperature > 0
	samplingMatch := uint8(5)
	samplingExempt := uint8(0)
	if payload.Temperature > 0 && len(payload.FinalSeed) > 0 {
		samplingMatch, samplingExempt = v.verifySampling(payload, verifierLogits, positions, topK)
	}

	// V5.2 §9.2: combined judgment
	// M1: temp=0 requires logitsMatch >= 4 (spec: 4/5 match = PASS)
	// temp>0: total mismatch <= 2 = PASS (logits + sampling combined)
	var pass bool
	if payload.Temperature == 0 {
		pass = logitsMatch >= 4
	} else {
		totalMismatch := (5 - logitsMatch) + (5 - samplingMatch - samplingExempt)
		pass = totalMismatch <= 2
	}

	return v.makeResult(payload.TaskId, pass, logitsMatch, samplingMatch, verifierLogits, verifiedInputTokens, verifiedOutputTokens), nil
}

// vrfRank returns the VRF rank of this verifier for the given task (-1 if not eligible).
// M3 §9.4 step 3: local VRF self-check before running teacher forcing.
func (v *Verifier) vrfRank(payload *worker.VerifyPayload) int {
	if len(v.ActiveWorkers) == 0 {
		return 0 // during bootstrap, assume rank 0 (primary)
	}

	verifSeed := append(append([]byte{}, payload.TaskId...), payload.ResultHash...)

	var candidates []vrftypes.RankedWorker
	for _, w := range v.ActiveWorkers {
		if w.Address == payload.WorkerAddress {
			continue
		}
		candidates = append(candidates, w)
	}
	if len(candidates) < 3 {
		return 0
	}

	ranked := vrftypes.RankWorkers(verifSeed, candidates, vrftypes.AlphaVerification)

	top := 21 // Audit KT §1: rank 21 covers 50% offline with <0.02% failure
	if top > len(ranked) {
		top = len(ranked)
	}
	for i := 0; i < top; i++ {
		if bytes.Equal(ranked[i].Pubkey, v.Pubkey) {
			return i
		}
	}
	return -1 // not eligible
}

// tokenizeOutput calls the TGI tokenizer to get actual BPE/SentencePiece token IDs.
// P1-3: replaces whitespace splitting with real tokenizer for correct sampled_tokens verification.
func (v *Verifier) tokenizeOutput(ctx context.Context, output string) ([]uint32, error) {
	tokens, err := v.Engine.Tokenize(ctx, output)
	if err != nil {
		return nil, fmt.Errorf("tokenize output: %w", err)
	}
	ids := make([]uint32, len(tokens))
	for i, t := range tokens {
		ids[i] = uint32(t.ID)
	}
	return ids, nil
}

// verifySampling verifies deterministic ChaCha20 sampling at VRF positions.
// V5.2 §8.3-8.4: each position uses independent ChaCha20 instance.
// S1: uses top-K logprobs for softmax → CDF → ChaCha20 sampling.
func (v *Verifier) verifySampling(payload *worker.VerifyPayload, verifierLogits [5]float32, positions [5]int, topK [5][]inference.TopTokenInfo) (matches uint8, exemptions uint8) {
	for i := 0; i < 5; i++ {
		result := chacha20Sample(topK[i], payload.Temperature, payload.TopP, payload.FinalSeed, uint64(positions[i]))

		if result.Insufficient {
			// P1-1: CDF coverage insufficient (high temperature + top-K truncation)
			// Position is mathematically unverifiable → exempt
			exemptions++
			continue
		}

		if result.TokenID == payload.SampledTokens[i] {
			matches++
		} else {
			logitsDiff := float32(gomath.Abs(float64(payload.WorkerLogits[i] - verifierLogits[i])))
			if logitsDiff >= v.Epsilon/2 {
				exemptions++
			}
			// else: logits nearly identical but different token → cheating suspicion → mismatch
		}
	}
	return matches, exemptions
}

// expf32 implements float32 exp using the same algorithm as C's expf.
// P1-1: Go has no float32 exp; math.Exp(float64) truncated to float32 differs from C expf.
// Uses Cephes-style range reduction + polynomial approximation for cross-implementation consistency.
func expf32(x float32) float32 {
	if x > 88.72 {
		return float32(gomath.Inf(1))
	}
	if x < -87.33 {
		return 0
	}

	// Range reduction: x = k*ln(2) + r, |r| <= 0.5*ln(2)
	const ln2 = float32(0.6931471805599453)
	const ln2inv = float32(1.4426950408889634)
	k := float32(gomath.Round(float64(x * ln2inv)))
	r := x - k*ln2

	// Polynomial approximation for exp(r), |r| <= 0.347
	// Coefficients from Cephes library (float32 precision)
	r2 := r * r
	p := float32(1.0) + r + r2*float32(0.5) +
		r2*r*float32(0.16666667) +
		r2*r2*float32(0.041666668) +
		r2*r2*r*float32(0.008333334) +
		r2*r2*r2*float32(0.001388889)

	// Reconstruct: exp(x) = 2^k * exp(r)
	bits := gomath.Float32bits(p)
	bits += uint32(k) << 23
	return gomath.Float32frombits(bits)
}

// CDF coverage threshold: if top-K cumulative probability >= this value,
// the truncation is mathematically safe for sampling verification.
// At 99.99% coverage, the probability of a legitimate sample falling outside
// top-K is < 0.01%, which is negligible compared to the ε tolerance.
const cdfCoverageThreshold = float32(0.9999)

// chacha20SampleResult holds the sampling result and coverage information.
type chacha20SampleResult struct {
	TokenID      uint32
	CDFCoverage  float32 // cumulative probability mass of top-K tokens
	Insufficient bool    // true if CDF coverage < threshold (position unverifiable)
}

// chacha20Sample performs deterministic sampling using ChaCha20 PRNG (RFC 8439).
// V5.2 §8.3: all intermediate calculations use float32 for cross-implementation consistency.
// topP: nucleus sampling threshold (0 or >=1.0 means disabled). When enabled, tokens are
// sorted by probability descending, and only the smallest set whose cumulative probability
// exceeds topP is kept. This happens after softmax but before CDF construction.
// Returns sampling result with CDF coverage info for truncation-safety verification.
func chacha20Sample(topKLogits []inference.TopTokenInfo, temperature float32, topP float32, finalSeed []byte, tokenPosition uint64) chacha20SampleResult {
	if temperature <= 0 || len(topKLogits) == 0 {
		if len(topKLogits) > 0 {
			return chacha20SampleResult{TokenID: uint32(topKLogits[0].ID), CDFCoverage: 1.0}
		}
		return chacha20SampleResult{TokenID: 0, CDFCoverage: 1.0}
	}

	// 1. Temperature scaling (float32 per spec §8.3)
	// TGI returns logprobs (= logit - logsumexp). Since softmax is translation-invariant
	// (softmax(x - c) = softmax(x)), softmax(logprob/T) = softmax(logit/T) for any
	// shared constant c. We use logprobs directly as "raw logits" for the scaling step.
	scaled := make([]float32, len(topKLogits))
	for i, t := range topKLogits {
		scaled[i] = t.Logprob / temperature
	}

	// V5.2 §8.3: sort by token_id ascending before softmax + CDF accumulation.
	// float32 addition is not commutative, so order matters for determinism.
	type tokenProb struct {
		id     uint32
		scaled float32
	}
	sorted := make([]tokenProb, len(topKLogits))
	for i, t := range topKLogits {
		sorted[i] = tokenProb{id: uint32(t.ID), scaled: scaled[i]}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].id < sorted[j].id })

	// 2. Softmax (numerically stable: subtract max, float32)
	maxLogit := sorted[0].scaled
	for _, s := range sorted[1:] {
		if s.scaled > maxLogit {
			maxLogit = s.scaled
		}
	}
	sumExp := float32(0)
	probs := make([]float32, len(sorted))
	for i, s := range sorted {
		probs[i] = expf32(s.scaled - maxLogit)
		sumExp += probs[i]
	}
	for i := range probs {
		probs[i] /= sumExp
	}

	// 2b. Top-p (nucleus) filtering: keep smallest set of tokens whose cumulative
	// probability >= topP. Sort by probability descending, accumulate, then mask out
	// tokens beyond the threshold. topP <= 0 or >= 1.0 means disabled.
	if topP > 0 && topP < 1.0 {
		// Build index sorted by probability descending
		type idxProb struct {
			idx  int
			prob float32
		}
		byProb := make([]idxProb, len(sorted))
		for i := range sorted {
			byProb[i] = idxProb{idx: i, prob: probs[i]}
		}
		sort.Slice(byProb, func(a, b int) bool { return byProb[a].prob > byProb[b].prob })

		// Accumulate until topP threshold is reached
		cumulative := float32(0)
		keep := make([]bool, len(sorted))
		for _, ip := range byProb {
			keep[ip.idx] = true
			cumulative += ip.prob
			if cumulative >= topP {
				break
			}
		}

		// Zero out excluded tokens and renormalize
		newSum := float32(0)
		for i := range probs {
			if !keep[i] {
				probs[i] = 0
			}
			newSum += probs[i]
		}
		if newSum > 0 {
			for i := range probs {
				probs[i] /= newSum
			}
		}
	}

	// 3. CDF (cumulative distribution, float32) — token_id ascending order
	cdf := make([]float32, len(probs))
	cdf[0] = probs[0]
	for i := 1; i < len(probs); i++ {
		cdf[i] = cdf[i-1] + probs[i]
	}

	// P1-1: CDF coverage check — if top-K doesn't cover enough probability mass
	// (typically at high temperature), this position is mathematically unverifiable
	// via truncated top-K. Mark as insufficient rather than risking false FAIL.
	coverage := cdf[len(cdf)-1]
	if coverage < cdfCoverageThreshold {
		return chacha20SampleResult{
			TokenID:      uint32(topKLogits[0].ID),
			CDFCoverage:  coverage,
			Insufficient: true,
		}
	}

	// 4. ChaCha20 random number
	key := make([]byte, 32)
	if len(finalSeed) >= 32 {
		copy(key, finalSeed[:32])
	} else {
		copy(key, finalSeed)
	}

	nonce := make([]byte, 12)
	binary.LittleEndian.PutUint64(nonce, tokenPosition)

	cipher, err := chacha20.NewUnauthenticatedCipher(key, nonce)
	if err != nil {
		return chacha20SampleResult{TokenID: uint32(topKLogits[0].ID), CDFCoverage: coverage}
	}

	buf := make([]byte, 8)
	cipher.XORKeyStream(buf, buf)
	randUint64 := binary.LittleEndian.Uint64(buf)
	randFloat := float64(randUint64) / 18446744073709551616.0 // 2^64, per spec §8.3

	// 5. CDF lookup — spec §8.3: if (float64)(cumsum) > u (token_id ascending order)
	for i, c := range cdf {
		if float64(c) > randFloat {
			return chacha20SampleResult{TokenID: sorted[i].id, CDFCoverage: coverage}
		}
	}
	return chacha20SampleResult{TokenID: sorted[len(sorted)-1].id, CDFCoverage: coverage}
}

// verifyWorkerPayloadSignature verifies the Worker's InferReceipt signature
// to ensure the payload was not tampered with by a MITM.
// P1-4 §9.4 step 1: verify worker_sig over the canonical receipt fields.
func (v *Verifier) verifyWorkerPayloadSignature(payload *worker.VerifyPayload) bool {
	if len(payload.WorkerSig) == 0 || len(payload.WorkerPubkey) == 0 {
		return false
	}

	// Reconstruct the InferReceipt SignBytes hash
	h := sha256.New()
	h.Write(payload.TaskId)
	h.Write(payload.WorkerPubkey)
	h.Write(payload.ResultHash)
	h.Write(payload.FinalSeed)
	for i := 0; i < 5; i++ {
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, gomath.Float32bits(payload.WorkerLogits[i]))
		h.Write(buf)
	}
	for i := 0; i < 5; i++ {
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, payload.SampledTokens[i])
		h.Write(buf)
	}
	// S9: include token counts in signature
	itcBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(itcBuf, payload.InputTokenCount)
	h.Write(itcBuf)
	otcBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(otcBuf, payload.OutputTokenCount)
	h.Write(otcBuf)
	msgHash := h.Sum(nil)

	var pubKey secp256k1.PubKey = payload.WorkerPubkey
	return pubKey.VerifySignature(msgHash, payload.WorkerSig)
}

// E14: degenerateLogitsVarThreshold bounds the minimum acceptable variance across
// the 5 VRF-selected logit positions. Real top-k logits across independent token
// positions in a healthy 8B model span several units with variance typically O(1)
// or larger; variance below this threshold indicates either a TGI malfunction or
// a collusion attack returning (near-)constant values to trivially match.
const degenerateLogitsVarThreshold = float32(1e-6)

// isLogitsDegenerate reports whether a 5-position logits sample is unsafe to
// feed into CompareLogits. Two degeneracies are rejected:
//   - all-zero: attacker returns [0,0,0,0,0] which trivially matches a colluding
//     verifier's equally-zero fake.
//   - low-variance: attacker returns [c, c+ε₁, c+ε₂, ...] with εᵢ near float32
//     noise floor, bypassing a naive all-zero guard while still being trivial
//     to match.
//
// A legitimate sample from 5 independent token positions will always have
// variance several orders of magnitude above the threshold.
func isLogitsDegenerate(logits [5]float32) bool {
	allZero := true
	for _, v := range logits {
		if v != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		return true
	}

	var mean float32
	for _, v := range logits {
		mean += v
	}
	mean /= 5

	var variance float32
	for _, v := range logits {
		d := v - mean
		variance += d * d
	}
	variance /= 5

	return variance < degenerateLogitsVarThreshold
}

func computeFinalSeed(userSeed, dispatchBlockHash, taskId []byte) []byte {
	h := sha256.New()
	h.Write(userSeed)
	h.Write(dispatchBlockHash)
	h.Write(taskId)
	return h.Sum(nil)
}

func (v *Verifier) makeResult(taskId []byte, pass bool, logitsMatch, samplingMatch uint8, verifierLogits [5]float32, verifiedInputTokens, verifiedOutputTokens uint32) *p2ptypes.VerifyResult {
	result := &p2ptypes.VerifyResult{
		TaskId:               taskId,
		VerifierAddr:         v.Pubkey,
		Pass:                 pass,
		LogitsMatch:          logitsMatch,
		SamplingMatch:        samplingMatch,
		VerifiedInputTokens:  verifiedInputTokens,
		VerifiedOutputTokens: verifiedOutputTokens,
	}

	// P1-2: hash actual verifier logits (not placeholders) so second_verifiers can verify teacher forcing
	logitsData := make([]byte, 0, 5*4+len(taskId)+len(v.Pubkey))
	for i := 0; i < 5; i++ {
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, gomath.Float32bits(verifierLogits[i]))
		logitsData = append(logitsData, buf...)
	}
	logitsData = append(logitsData, result.TaskId...)
	logitsData = append(logitsData, result.VerifierAddr...)
	hash := sha256.Sum256(logitsData)
	result.LogitsHash = hash[:]

	// S9: include token counts in signature coverage
	sigData := sha256.New()
	sigData.Write(hash[:])
	itcBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(itcBuf, verifiedInputTokens)
	sigData.Write(itcBuf)
	otcBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(otcBuf, verifiedOutputTokens)
	sigData.Write(otcBuf)
	sigHash := sigData.Sum(nil)

	if len(v.PrivKey) == 32 {
		msgHash := sha256.Sum256(sigHash)
		privKey := secp256k1.PrivKey(v.PrivKey)
		sig, err := privKey.Sign(msgHash[:])
		if err == nil {
			result.Signature = sig
		}
	}

	return result
}

// BroadcastResult publishes the VerifyResult to the P2P network.
func (v *Verifier) BroadcastResult(ctx context.Context, result *p2ptypes.VerifyResult, modelId string) error {
	data, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}

	topic := fmt.Sprintf("%s/verify", p2phost.ModelTopic(modelId))
	return v.Host.Publish(ctx, topic, data)
}
