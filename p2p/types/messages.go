package types

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
)

// P2P message types for the FunAI inference network (Layer 2).
// These messages never touch the chain — they flow through libp2p.

// InferRequest is a user's signed inference request (V5.2 §3.4).
// InferRequest is a user's signed inference request (V5.2 §3.4).
// S9: Fee field renamed to MaxFee (protobuf #3 unchanged, binary compatible).
// Per-request mode: MaxFee is the fixed fee. Per-token mode: MaxFee is the budget cap.
type InferRequest struct {
	ModelId       []byte `json:"model_id"`
	PromptHash    []byte `json:"prompt_hash"`
	MaxFee        uint64 `json:"max_fee"` // S9: renamed from fee (protobuf #3, binary compatible)
	ExpireBlock   uint64 `json:"expire_block"`
	UserSeed      []byte `json:"user_seed"`
	Temperature   uint16 `json:"temperature"` // 0=argmax, 10000=1.0, max 20000
	TopP          uint16 `json:"top_p"`       // 0 or 10000=disabled, 1-9999=nucleus sampling threshold
	Timestamp     uint64 `json:"timestamp"`
	UserPubkey    []byte `json:"user_pubkey"`
	UserSignature []byte `json:"user_signature"`
	Prompt        string `json:"prompt"`
	MaxTokens     uint32 `json:"max_tokens,omitempty"`
	TaskType      uint32 `json:"task_type,omitempty"`
	ContentTag    uint32 `json:"content_tag,omitempty"`

	FeePerInputToken  uint64 `json:"fee_per_input_token,omitempty"`
	FeePerOutputToken uint64 `json:"fee_per_output_token,omitempty"`

	// Audit KT §4: latency requirements for time-sensitive scenarios (e.g. companion 2s first token)
	MaxLatencyMs uint32 `json:"max_latency_ms,omitempty"` // max first-token latency in ms (0=no constraint)
	StreamMode   bool   `json:"stream_mode,omitempty"`    // whether client needs streaming response
}

// IsPerToken returns true if this request uses per-token billing (S9).
// Both fee_per_input_token and fee_per_output_token must be set.
func (r *InferRequest) IsPerToken() bool {
	return r.FeePerInputToken > 0 && r.FeePerOutputToken > 0
}

// EffectiveFee returns MaxFee (used for both per-request and per-token modes).
func (r *InferRequest) EffectiveFee() uint64 {
	return r.MaxFee
}

// ValidateFeeMode checks billing mode consistency (S9 §2.6 backward compat).
func (r *InferRequest) ValidateFeeMode() error {
	if r.MaxFee == 0 {
		return fmt.Errorf("invalid_parameters: max_fee must be > 0")
	}
	hasPerToken := r.FeePerInputToken > 0 || r.FeePerOutputToken > 0
	if hasPerToken && (r.FeePerInputToken == 0 || r.FeePerOutputToken == 0) {
		return fmt.Errorf("invalid_parameters: fee_per_input_token and fee_per_output_token must both be set")
	}
	return nil
}

// TaskId returns hash(user_pubkey + model_id + prompt_hash + timestamp).
// V5.2 §3.4: same task_id can only be settled once on-chain.
func (r *InferRequest) TaskId() []byte {
	h := sha256.New()
	h.Write(r.UserPubkey)
	h.Write(r.ModelId)
	h.Write(r.PromptHash)
	// Encode timestamp as 8 bytes big-endian
	ts := make([]byte, 8)
	ts[0] = byte(r.Timestamp >> 56)
	ts[1] = byte(r.Timestamp >> 48)
	ts[2] = byte(r.Timestamp >> 40)
	ts[3] = byte(r.Timestamp >> 32)
	ts[4] = byte(r.Timestamp >> 24)
	ts[5] = byte(r.Timestamp >> 16)
	ts[6] = byte(r.Timestamp >> 8)
	ts[7] = byte(r.Timestamp)
	h.Write(ts)
	return h.Sum(nil)
}

// SignBytes returns the bytes used for signing the InferRequest.
// SignBytes returns the canonical bytes for signing.
// Covers all fields except Prompt and UserSignature.
func (r *InferRequest) SignBytes() []byte {
	h := sha256.New()
	h.Write(r.ModelId)
	h.Write(r.PromptHash)
	mfBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(mfBuf, r.MaxFee)
	h.Write(mfBuf)
	expBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(expBuf, r.ExpireBlock)
	h.Write(expBuf)
	tempBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(tempBuf, r.Temperature)
	h.Write(tempBuf)
	tpBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(tpBuf, r.TopP)
	h.Write(tpBuf)
	tsBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(tsBuf, r.Timestamp)
	h.Write(tsBuf)
	h.Write(r.UserSeed)
	mtBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(mtBuf, r.MaxTokens)
	h.Write(mtBuf)
	ttBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(ttBuf, r.TaskType)
	h.Write(ttBuf)
	ctBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(ctBuf, r.ContentTag)
	h.Write(ctBuf)
	fipBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(fipBuf, r.FeePerInputToken)
	h.Write(fipBuf)
	fopBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(fopBuf, r.FeePerOutputToken)
	h.Write(fopBuf)
	h.Write(r.UserPubkey)
	return h.Sum(nil)
}

// InferReceipt is the Worker's proof of completed inference (V5.2 §7.1).
type InferReceipt struct {
	TaskId        []byte     `json:"task_id"`
	WorkerPubkey  []byte     `json:"worker_pubkey"`
	WorkerLogits  [5]float32 `json:"worker_logits"`  // 5 VRF-selected positions (V5.2 §9.2)
	ResultHash    []byte     `json:"result_hash"`    // SHA256(complete output)
	FinalSeed     []byte     `json:"final_seed"`     // SHA256(user_seed || dispatch_block_hash || task_id)
	SampledTokens [5]uint32  `json:"sampled_tokens"` // 5 sampled token IDs at VRF positions
	WorkerSig     []byte     `json:"worker_sig"`

	// S9: Worker's token count (included in worker_sig coverage)
	InputTokenCount  uint32 `json:"input_token_count,omitempty"`
	OutputTokenCount uint32 `json:"output_token_count,omitempty"`

	// Audit KT §5: Worker-measured inference latency in ms, from the start of the
	// engine call to receipt creation. Replaces the proposer's previous wall-clock
	// measurement (ReceivedAt - user_request_timestamp) which included P2P dispatch
	// and user clock skew — noise for VRF speed ranking. Included in worker_sig
	// coverage so the Worker cannot be MITM-edited to fake a better score.
	InferenceLatencyMs uint32 `json:"inference_latency_ms,omitempty"`
}

// SignBytes returns canonical bytes for signing the InferReceipt.
// S6: covers TaskId, WorkerPubkey, ResultHash, FinalSeed, WorkerLogits, SampledTokens.
// S9: also covers InputTokenCount, OutputTokenCount.
// Audit KT §5: also covers InferenceLatencyMs.
func (r *InferReceipt) SignBytes() []byte {
	h := sha256.New()
	h.Write(r.TaskId)
	h.Write(r.WorkerPubkey)
	h.Write(r.ResultHash)
	h.Write(r.FinalSeed)
	for i := 0; i < 5; i++ {
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, math.Float32bits(r.WorkerLogits[i]))
		h.Write(buf)
	}
	for i := 0; i < 5; i++ {
		buf := make([]byte, 4)
		binary.BigEndian.PutUint32(buf, r.SampledTokens[i])
		h.Write(buf)
	}
	// S9: token counts
	itcBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(itcBuf, r.InputTokenCount)
	h.Write(itcBuf)
	otcBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(otcBuf, r.OutputTokenCount)
	h.Write(otcBuf)
	// Audit KT §5: inference latency
	latBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(latBuf, r.InferenceLatencyMs)
	h.Write(latBuf)
	return h.Sum(nil)
}

// VerifyResult is a verifier's assessment of an InferReceipt (V5.2 §9.2).
type VerifyResult struct {
	TaskId        []byte `json:"task_id"`
	VerifierAddr  []byte `json:"verifier_addr"`
	Pass          bool   `json:"pass"`
	LogitsMatch   uint8  `json:"logits_match"`   // number of logits positions matched (0-5)
	SamplingMatch uint8  `json:"sampling_match"` // number of sampling positions matched (0-5, only if temperature>0)
	LogitsHash    []byte `json:"logits_hash"`    // hash of verifier's logits for auditability
	Signature     []byte `json:"signature"`

	// S9: Verifier's independent token count from teacher forcing
	VerifiedInputTokens  uint32 `json:"verified_input_tokens,omitempty"`
	VerifiedOutputTokens uint32 `json:"verified_output_tokens,omitempty"`
}

// AssignTask is Leader's dispatch message to a Worker (V5.2 §6.2).
type AssignTask struct {
	TaskId            []byte `json:"task_id"`
	ModelId           []byte `json:"model_id"`
	Prompt            string `json:"prompt"`
	Fee               uint64 `json:"fee"`
	UserAddr          []byte `json:"user_addr"`
	Temperature       uint16 `json:"temperature"`
	TopP              uint16 `json:"top_p"`
	UserSeed          []byte `json:"user_seed,omitempty"`
	DispatchBlockHash []byte `json:"dispatch_block_hash"`
	LeaderSig         []byte `json:"leader_sig"`

	// S9: per-token billing fields (forwarded from InferRequest)
	FeePerInputToken  uint64 `json:"fee_per_input_token,omitempty"`
	FeePerOutputToken uint64 `json:"fee_per_output_token,omitempty"`
	MaxFee            uint64 `json:"max_fee,omitempty"`
	MaxTokens         uint32 `json:"max_tokens,omitempty"` // S9: output token limit derived from budget

	// Audit KT §4: latency requirements (forwarded from InferRequest)
	MaxLatencyMs uint32 `json:"max_latency_ms,omitempty"` // max first-token latency in ms
	StreamMode   bool   `json:"stream_mode,omitempty"`    // client needs streaming
}

// SigDigest returns the canonical 32-byte hash that the Leader signs over the
// AssignTask fields and the Worker verifies. Any change to the field set or their
// binary layout MUST be mirrored on both the signer (p2p/leader) and verifier
// (p2p/worker) sides — keeping one definition avoids drift.
//
// P1-5 / S9: coverage includes billing fields (per-token + max_tokens) so a MITM
// cannot tamper with prompt, fee, seed, or budget bounds.
func (a *AssignTask) SigDigest() [32]byte {
	h := sha256.New()
	h.Write(a.TaskId)
	h.Write(a.ModelId)
	h.Write([]byte(a.Prompt))
	feeBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(feeBuf, a.MaxFee)
	h.Write(feeBuf)
	h.Write(a.UserAddr)
	tempBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(tempBuf, a.Temperature)
	h.Write(tempBuf)
	h.Write(a.UserSeed)
	h.Write(a.DispatchBlockHash)
	fipBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(fipBuf, a.FeePerInputToken)
	h.Write(fipBuf)
	fopBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(fopBuf, a.FeePerOutputToken)
	h.Write(fopBuf)
	mfBuf := make([]byte, 8)
	binary.BigEndian.PutUint64(mfBuf, a.MaxFee)
	h.Write(mfBuf)
	mtBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(mtBuf, a.MaxTokens)
	h.Write(mtBuf)
	return sha256.Sum256(h.Sum(nil))
}

// IsPerToken returns true if this task uses per-token billing (S9).
func (a *AssignTask) IsPerToken() bool {
	return a.FeePerInputToken > 0 && a.FeePerOutputToken > 0
}

// EffectiveFee returns MaxFee for per-token, Fee for per-request.
func (a *AssignTask) EffectiveFee() uint64 {
	if a.IsPerToken() {
		return a.MaxFee
	}
	return a.Fee
}

// AcceptTask is Worker's response to Leader's dispatch.
type AcceptTask struct {
	TaskId    []byte `json:"task_id"`
	WorkerSig []byte `json:"worker_sig"`
	Accepted  bool   `json:"accepted"`
}

// StreamToken is a single token streamed from Worker to User SDK.
// P3-7: the final token includes ContentSig = sig(SHA256(complete_content))
// so the SDK can verify the Worker actually produced the content.
type StreamToken struct {
	TaskId     []byte `json:"task_id"`
	Token      string `json:"token"`
	Index      uint32 `json:"index"`
	IsFinal    bool   `json:"is_final"`
	ContentSig []byte `json:"content_sig,omitempty"` // P3-7: Worker's signature over SHA256(complete_output), only on final token
}

// AuditRequest is sent by the Proposer to selected second_verifiers via P2P.
// Contains all data needed for the second_verifier to re-execute inference and verify.
type AuditRequest struct {
	TaskId            []byte     `json:"task_id"`
	ModelId           []byte     `json:"model_id"`
	Prompt            string     `json:"prompt"`
	Output            string     `json:"output"`
	WorkerLogits      [5]float32 `json:"worker_logits"`
	SampledTokens     [5]uint32  `json:"sampled_tokens"`
	FinalSeed         []byte     `json:"final_seed"`
	Temperature       uint16     `json:"temperature"`
	TopP              uint16     `json:"top_p"`
	WorkerPubkey      []byte     `json:"worker_pubkey"`
	InputTokenCount   uint32     `json:"input_token_count,omitempty"`
	OutputTokenCount  uint32     `json:"output_token_count,omitempty"`
	VerifierAddresses []string   `json:"verifier_addresses"`
	ProposerSig       []byte     `json:"proposer_sig"`
}

// SecondVerificationResponse is returned by an second_verifier after re-executing and verifying.
type SecondVerificationResponse struct {
	TaskId               []byte `json:"task_id"`
	Pass                 bool   `json:"pass"`
	SecondVerifierAddr   []byte `json:"second_verifier_addr"`
	LogitsHash           []byte `json:"logits_hash"`
	Signature            []byte `json:"signature"`
	VerifiedInputTokens  uint32 `json:"verified_input_tokens,omitempty"`
	VerifiedOutputTokens uint32 `json:"verified_output_tokens,omitempty"`
}

// SignBytes returns canonical bytes for signing the SecondVerificationResponse.
func (r *SecondVerificationResponse) SignBytes() []byte {
	h := sha256.New()
	h.Write(r.TaskId)
	if r.Pass {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	h.Write(r.SecondVerifierAddr)
	h.Write(r.LogitsHash)
	itcBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(itcBuf, r.VerifiedInputTokens)
	h.Write(itcBuf)
	otcBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(otcBuf, r.VerifiedOutputTokens)
	h.Write(otcBuf)
	return h.Sum(nil)
}
