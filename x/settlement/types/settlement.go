package types

import (
	"encoding/hex"
	"fmt"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// TaskStatus represents the lifecycle state of a task on-chain.
// V5.2: VERIFIED → CLEARED (90%) / PENDING_AUDIT (10%) → CLEARED / PENDING_REAUDIT / FAILED
type TaskStatus uint32

const (
	TaskPending                   TaskStatus = 0
	TaskSettled                   TaskStatus = 1
	TaskFraud                     TaskStatus = 2
	TaskFailSettled               TaskStatus = 3
	TaskVerified                  TaskStatus = 4
	TaskCleared                   TaskStatus = 5
	TaskPendingSecondVerification TaskStatus = 6
	TaskPendingThirdVerification  TaskStatus = 7
	TaskFailed                    TaskStatus = 8
)

func (s TaskStatus) String() string {
	switch s {
	case TaskPending:
		return "TASK_PENDING"
	case TaskSettled:
		return "TASK_SETTLED"
	case TaskFraud:
		return "TASK_FRAUD"
	case TaskFailSettled:
		return "TASK_FAIL_SETTLED"
	case TaskVerified:
		return "TASK_VERIFIED"
	case TaskCleared:
		return "TASK_CLEARED"
	case TaskPendingSecondVerification:
		return "TASK_PENDING_AUDIT"
	case TaskPendingThirdVerification:
		return "TASK_PENDING_REAUDIT"
	case TaskFailed:
		return "TASK_FAILED"
	default:
		return "TASK_UNKNOWN"
	}
}

// SettlementStatus is the verification outcome within a SettlementEntry.
type SettlementStatus uint32

const (
	SettlementSuccess SettlementStatus = 0
	SettlementFail    SettlementStatus = 1
)

func (s SettlementStatus) String() string {
	switch s {
	case SettlementSuccess:
		return "SUCCESS"
	case SettlementFail:
		return "FAIL"
	default:
		return "UNKNOWN"
	}
}

// InferenceAccount is the user's on-chain inference balance. No slots.
// task_id = hash(user_pubkey + model_id + prompt_hash + timestamp) provides uniqueness.
type InferenceAccount struct {
	Address       string   `protobuf:"bytes,1,opt,name=address,proto3" json:"address"`
	Balance       sdk.Coin `protobuf:"bytes,2,opt,name=balance,proto3" json:"balance"`
	FrozenBalance sdk.Coin `protobuf:"bytes,3,opt,name=frozen_balance,proto3" json:"frozen_balance"` // S9: sum of max_fee frozen across active per-token tasks
}

// AvailableBalance returns Balance minus FrozenBalance (S9).
func (ia InferenceAccount) AvailableBalance() sdk.Coin {
	if ia.FrozenBalance.IsZero() || !ia.FrozenBalance.IsValid() {
		return ia.Balance
	}
	if ia.Balance.IsLT(ia.FrozenBalance) {
		return sdk.NewCoin(ia.Balance.Denom, math.ZeroInt())
	}
	return ia.Balance.Sub(ia.FrozenBalance)
}

func (m *InferenceAccount) ProtoMessage()  {}
func (m *InferenceAccount) Reset()         { *m = InferenceAccount{} }
func (m *InferenceAccount) String() string { return fmt.Sprintf("InferenceAccount{%s}", m.Address) }

func (ia InferenceAccount) Validate() error {
	if _, err := sdk.AccAddressFromBech32(ia.Address); err != nil {
		return fmt.Errorf("invalid address: %w", err)
	}
	if !ia.Balance.IsValid() {
		return fmt.Errorf("invalid balance")
	}
	return nil
}

// SettlementEntry is one task's data inside MsgBatchSettlement (V5.2: inline, no DA).
// ~200 bytes per entry.
type SettlementEntry struct {
	TaskId          []byte           `protobuf:"bytes,1,opt,name=task_id,proto3" json:"task_id"`
	UserAddress     string           `protobuf:"bytes,2,opt,name=user_address,proto3" json:"user_address"`
	WorkerAddress   string           `protobuf:"bytes,3,opt,name=worker_address,proto3" json:"worker_address"`
	VerifierResults []VerifierResult `protobuf:"bytes,4,rep,name=verifier_results,proto3" json:"verifier_results"`
	Fee             sdk.Coin         `protobuf:"bytes,5,opt,name=fee,proto3" json:"fee"`
	Status          SettlementStatus `protobuf:"varint,6,opt,name=status,proto3" json:"status"`
	ExpireBlock     int64            `protobuf:"varint,7,opt,name=expire_block,proto3" json:"expire_block"`
	ModelId         string           `protobuf:"bytes,8,opt,name=model_id,proto3" json:"model_id,omitempty"`
	LatencyMs       uint64           `protobuf:"varint,9,opt,name=latency_ms,proto3" json:"latency_ms,omitempty"`
	UserSigHash     []byte           `protobuf:"bytes,10,opt,name=user_sig_hash,proto3" json:"user_sig_hash"`
	WorkerSigHash   []byte           `protobuf:"bytes,11,opt,name=worker_sig_hash,proto3" json:"worker_sig_hash"`
	VerifySigHashes [][]byte         `protobuf:"bytes,12,rep,name=verify_sig_hashes,proto3" json:"verify_sig_hashes,omitempty"`
	ResultCount     uint32           `protobuf:"varint,13,opt,name=result_count,proto3" json:"result_count,omitempty"`

	// S9: per-token billing fields
	FeePerInputToken  uint64   `protobuf:"varint,14,opt,name=fee_per_input_token,proto3" json:"fee_per_input_token,omitempty"`
	FeePerOutputToken uint64   `protobuf:"varint,15,opt,name=fee_per_output_token,proto3" json:"fee_per_output_token,omitempty"`
	MaxFee            sdk.Coin `protobuf:"bytes,16,opt,name=max_fee,proto3" json:"max_fee,omitempty"`
	// S9: Worker's self-reported token counts
	WorkerInputTokens  uint32 `protobuf:"varint,17,opt,name=worker_input_tokens,proto3" json:"worker_input_tokens,omitempty"`
	WorkerOutputTokens uint32 `protobuf:"varint,18,opt,name=worker_output_tokens,proto3" json:"worker_output_tokens,omitempty"`

	// Dispatch rank verification: which VRF rank the assigned Worker held (0-based).
	// Proposer re-computes VRF ranking and records this for on-chain audit.
	DispatchRank uint32 `protobuf:"varint,19,opt,name=dispatch_rank,proto3" json:"dispatch_rank,omitempty"`

	// P1 AvgLatencyMs fix: Proposer-observed unix-ms timestamps used to compute
	// SettlementLatencyMs on-chain without trusting Worker-self-reported values.
	// AcceptedAtMs is the Proposer's wall-clock when it observed AssignTask for
	// this task on the dispatch topic; ReceiptAtMs is its wall-clock when the
	// InferReceipt arrived. LatencyMs (field 9) is now derived from these two
	// by the Proposer before submission, instead of from receipt.InferenceLatencyMs.
	AcceptedAtMs uint64 `protobuf:"varint,20,opt,name=accepted_at_ms,proto3" json:"accepted_at_ms,omitempty"`
	ReceiptAtMs  uint64 `protobuf:"varint,21,opt,name=receipt_at_ms,proto3" json:"receipt_at_ms,omitempty"`
}

// IsPerToken returns true if this entry uses per-token billing (S9).
func (e *SettlementEntry) IsPerToken() bool {
	return e.FeePerInputToken > 0 && e.FeePerOutputToken > 0
}

// FrozenTaskMeta stores metadata for a frozen per-token task (for timeout handling).
type FrozenTaskMeta struct {
	TaskId        []byte `json:"task_id"`
	UserAddress   string `json:"user_address"`
	WorkerAddress string `json:"worker_address"`
	MaxFee        uint64 `json:"max_fee"`
	ExpireBlock   int64  `json:"expire_block"`
}

// TokenMismatchRecord tracks per Worker-Verifier pair mismatch statistics (S9 §5.2).
type TokenMismatchRecord struct {
	WorkerAddress   string `json:"worker_address"`
	VerifierAddress string `json:"verifier_address"`
	TotalTasks      uint32 `json:"total_tasks"`
	MismatchCount   uint32 `json:"mismatch_count"`
}

func (m *SettlementEntry) ProtoMessage() {}
func (m *SettlementEntry) Reset()        { *m = SettlementEntry{} }
func (m *SettlementEntry) String() string {
	return fmt.Sprintf("SettlementEntry{%s}", hex.EncodeToString(m.TaskId))
}

// SettledTaskID tracks which task_ids have been settled (dedup).
// Chain stores task_id -> settled status. Cleaned up after expire_block + 1000.
type SettledTaskID struct {
	TaskId            []byte     `protobuf:"bytes,1,opt,name=task_id,proto3" json:"task_id"`
	Status            TaskStatus `protobuf:"varint,2,opt,name=status,proto3" json:"status"`
	ExpireBlock       int64      `protobuf:"varint,3,opt,name=expire_block,proto3" json:"expire_block"`
	SettledAt         int64      `protobuf:"varint,4,opt,name=settled_at,proto3" json:"settled_at"`
	WorkerAddress     string     `protobuf:"bytes,5,opt,name=worker_address,proto3" json:"worker_address,omitempty"`
	OriginalVerifiers []string   `protobuf:"bytes,6,rep,name=original_verifiers,proto3" json:"original_verifiers,omitempty"`
	Fee               sdk.Coin   `protobuf:"bytes,7,opt,name=fee,proto3" json:"fee,omitempty"`
	UserAddress       string     `protobuf:"bytes,8,opt,name=user_address,proto3" json:"user_address,omitempty"`
}

func (m *SettledTaskID) ProtoMessage() {}
func (m *SettledTaskID) Reset()        { *m = SettledTaskID{} }
func (m *SettledTaskID) String() string {
	return fmt.Sprintf("SettledTaskID{%s}", hex.EncodeToString(m.TaskId))
}

func (st SettledTaskID) TaskIdHex() string {
	return hex.EncodeToString(st.TaskId)
}

// BatchRecord stores the on-chain summary of a batch settlement.
// V5.2: chain only persists merkle_root after processing entries.
type BatchRecord struct {
	BatchId     uint64   `protobuf:"varint,1,opt,name=batch_id,proto3" json:"batch_id"`
	Proposer    string   `protobuf:"bytes,2,opt,name=proposer,proto3" json:"proposer"`
	MerkleRoot  []byte   `protobuf:"bytes,3,opt,name=merkle_root,proto3" json:"merkle_root"`
	ResultCount uint32   `protobuf:"varint,4,opt,name=result_count,proto3" json:"result_count"`
	TotalFees   sdk.Coin `protobuf:"bytes,5,opt,name=total_fees,proto3" json:"total_fees"`
	SettledAt   int64    `protobuf:"varint,6,opt,name=settled_at,proto3" json:"settled_at"`
}

func (m *BatchRecord) ProtoMessage()  {}
func (m *BatchRecord) Reset()         { *m = BatchRecord{} }
func (m *BatchRecord) String() string { return fmt.Sprintf("BatchRecord{%d}", m.BatchId) }

func (br BatchRecord) Validate() error {
	if _, err := sdk.AccAddressFromBech32(br.Proposer); err != nil {
		return fmt.Errorf("invalid proposer address: %w", err)
	}
	if len(br.MerkleRoot) == 0 {
		return fmt.Errorf("merkle_root cannot be empty")
	}
	if br.ResultCount == 0 {
		return fmt.Errorf("result_count must be positive")
	}
	return nil
}

// VerifierResult represents a single verifier's PASS/FAIL result with signature.
type VerifierResult struct {
	Address    string `protobuf:"bytes,1,opt,name=address,proto3" json:"address"`
	Pass       bool   `protobuf:"varint,2,opt,name=pass,proto3" json:"pass"`
	Signature  []byte `protobuf:"bytes,3,opt,name=signature,proto3" json:"signature"`
	LogitsHash []byte `protobuf:"bytes,4,opt,name=logits_hash,proto3" json:"logits_hash"`

	// S9: Verifier's independent token count from teacher forcing
	VerifiedInputTokens  uint32 `protobuf:"varint,5,opt,name=verified_input_tokens,proto3" json:"verified_input_tokens,omitempty"`
	VerifiedOutputTokens uint32 `protobuf:"varint,6,opt,name=verified_output_tokens,proto3" json:"verified_output_tokens,omitempty"`
}

func (m *VerifierResult) ProtoMessage()  {}
func (m *VerifierResult) Reset()         { *m = VerifierResult{} }
func (m *VerifierResult) String() string { return fmt.Sprintf("VerifierResult{%s}", m.Address) }

// SecondVerificationRecord stores the result of a random audit for a task.
type SecondVerificationRecord struct {
	TaskId                  []byte   `protobuf:"bytes,1,opt,name=task_id,proto3" json:"task_id"`
	Epoch                   int64    `protobuf:"varint,2,opt,name=epoch,proto3" json:"epoch"`
	SecondVerifierAddresses []string `protobuf:"bytes,3,rep,name=second_verifier_addresses,proto3" json:"second_verifier_addresses"`
	Results                 []bool   `protobuf:"varint,4,rep,packed,name=results,proto3" json:"results"`
	ProcessedAt             int64    `protobuf:"varint,5,opt,name=processed_at,proto3" json:"processed_at"`
	// S9: second_verifier token counts for per-token verification
	SecondVerifierInputTokens  []uint32 `protobuf:"varint,6,rep,packed,name=second_verifier_input_tokens,proto3" json:"second_verifier_input_tokens,omitempty"`
	SecondVerifierOutputTokens []uint32 `protobuf:"varint,7,rep,packed,name=second_verifier_output_tokens,proto3" json:"second_verifier_output_tokens,omitempty"`
}

func (m *SecondVerificationRecord) ProtoMessage() {}
func (m *SecondVerificationRecord) Reset()        { *m = SecondVerificationRecord{} }
func (m *SecondVerificationRecord) String() string {
	return fmt.Sprintf("SecondVerificationRecord{%s}", hex.EncodeToString(m.TaskId))
}

func (ar SecondVerificationRecord) Validate() error {
	if len(ar.TaskId) == 0 {
		return fmt.Errorf("task_id cannot be empty")
	}
	if ar.Epoch < 0 {
		return fmt.Errorf("epoch cannot be negative")
	}
	if len(ar.SecondVerifierAddresses) == 0 {
		return fmt.Errorf("at least one second_verifier address required")
	}
	if len(ar.SecondVerifierAddresses) != len(ar.Results) {
		return fmt.Errorf("second_verifier addresses count (%d) must match results count (%d)", len(ar.SecondVerifierAddresses), len(ar.Results))
	}
	for _, addr := range ar.SecondVerifierAddresses {
		if _, err := sdk.AccAddressFromBech32(addr); err != nil {
			return fmt.Errorf("invalid second_verifier address %s: %w", addr, err)
		}
	}
	return nil
}

// SecondVerificationPendingTask tracks a task awaiting audit or third_verification completion.
type SecondVerificationPendingTask struct {
	TaskId              []byte           `protobuf:"bytes,1,opt,name=task_id,proto3" json:"task_id"`
	OriginalStatus      SettlementStatus `protobuf:"varint,2,opt,name=original_status,proto3" json:"original_status"`
	SubmittedAt         int64            `protobuf:"varint,3,opt,name=submitted_at,proto3" json:"submitted_at"`
	UserAddress         string           `protobuf:"bytes,4,opt,name=user_address,proto3" json:"user_address"`
	WorkerAddress       string           `protobuf:"bytes,5,opt,name=worker_address,proto3" json:"worker_address"`
	VerifierAddresses   []string         `protobuf:"bytes,6,rep,name=verifier_addresses,proto3" json:"verifier_addresses"`
	VerifierVotes       []bool           `protobuf:"varint,7,rep,packed,name=verifier_votes,proto3" json:"verifier_votes,omitempty"`
	Fee                 sdk.Coin         `protobuf:"bytes,8,opt,name=fee,proto3" json:"fee"`
	ExpireBlock         int64            `protobuf:"varint,9,opt,name=expire_block,proto3" json:"expire_block"`
	IsThirdVerification bool             `protobuf:"varint,10,opt,name=is_third_verification,proto3" json:"is_third_verification"`
	// S9: per-token fields preserved for audit re-settlement
	FeePerInputToken    uint64   `protobuf:"varint,11,opt,name=fee_per_input_token,proto3" json:"fee_per_input_token,omitempty"`
	FeePerOutputToken   uint64   `protobuf:"varint,12,opt,name=fee_per_output_token,proto3" json:"fee_per_output_token,omitempty"`
	MaxFee              sdk.Coin `protobuf:"bytes,13,opt,name=max_fee,proto3" json:"max_fee,omitempty"`
	SettledOutputTokens uint32   `protobuf:"varint,14,opt,name=settled_output_tokens,proto3" json:"settled_output_tokens,omitempty"`
	SettledInputTokens  uint32   `protobuf:"varint,15,opt,name=settled_input_tokens,proto3" json:"settled_input_tokens,omitempty"`
}

func (m *SecondVerificationPendingTask) ProtoMessage() {}
func (m *SecondVerificationPendingTask) Reset()        { *m = SecondVerificationPendingTask{} }
func (m *SecondVerificationPendingTask) String() string {
	return fmt.Sprintf("SecondVerificationPendingTask{%s}", hex.EncodeToString(m.TaskId))
}

// EpochStats tracks per-epoch statistics for dynamic audit rate calculation.
type EpochStats struct {
	Epoch                         int64    `protobuf:"varint,1,opt,name=epoch,proto3" json:"epoch"`
	TotalSettled                  uint64   `protobuf:"varint,2,opt,name=total_settled,proto3" json:"total_settled"`
	FailSettled                   uint64   `protobuf:"varint,3,opt,name=fail_settled,proto3" json:"fail_settled"`
	SecondVerificationTotal       uint64   `protobuf:"varint,4,opt,name=audit_total,proto3" json:"audit_total"`
	AuditFail                     uint64   `protobuf:"varint,5,opt,name=audit_fail,proto3" json:"audit_fail"`
	AuditOverturn                 uint64   `protobuf:"varint,6,opt,name=audit_overturn,proto3" json:"audit_overturn"`
	ThirdVerificationTotal        uint64   `protobuf:"varint,7,opt,name=third_verification_total,proto3" json:"third_verification_total"`
	ThirdVerificationOverturn     uint64   `protobuf:"varint,8,opt,name=third_verification_overturn,proto3" json:"third_verification_overturn"`
	TotalFees                     math.Int `protobuf:"bytes,9,opt,name=total_fees,proto3" json:"total_fees"`
	SecondVerificationPersonCount uint64   `protobuf:"varint,10,opt,name=second_verification_person_count,proto3" json:"second_verification_person_count"`
	VerificationCount             uint64   `protobuf:"varint,11,opt,name=verification_count,proto3" json:"verification_count"`
}

func (m *EpochStats) ProtoMessage()  {}
func (m *EpochStats) Reset()         { *m = EpochStats{} }
func (m *EpochStats) String() string { return fmt.Sprintf("EpochStats{%d}", m.Epoch) }

// DefaultEpochStats returns zeroed epoch stats.
func DefaultEpochStats(epoch int64) EpochStats {
	return EpochStats{
		Epoch:     epoch,
		TotalFees: math.ZeroInt(),
	}
}

// WorkerSnapshot stores a worker's cumulative stats at the start of an epoch.
// P1-8: used to compute per-epoch deltas for reward distribution.
type WorkerSnapshot struct {
	TotalFeeEarned math.Int `protobuf:"bytes,1,opt,name=total_fee_earned,proto3" json:"total_fee_earned"`
	TotalTasks     int64    `protobuf:"varint,2,opt,name=total_tasks,proto3" json:"total_tasks"`
}

func (m *WorkerSnapshot) ProtoMessage()  {}
func (m *WorkerSnapshot) Reset()         { *m = WorkerSnapshot{} }
func (m *WorkerSnapshot) String() string { return "WorkerSnapshot" }

// WorkerEpochContribution stores a worker's contribution during the current epoch.
// P1-8: computed as current cumulative - snapshot.
type WorkerEpochContribution struct {
	WorkerAddress string   `protobuf:"bytes,1,opt,name=worker_address,proto3" json:"worker_address"`
	FeeAmount     math.Int `protobuf:"bytes,2,opt,name=fee_amount,proto3" json:"fee_amount"`
	TaskCount     uint64   `protobuf:"varint,3,opt,name=task_count,proto3" json:"task_count"`
}

func (m *WorkerEpochContribution) ProtoMessage() {}
func (m *WorkerEpochContribution) Reset()        { *m = WorkerEpochContribution{} }
func (m *WorkerEpochContribution) String() string {
	return fmt.Sprintf("WorkerEpochContribution{%s}", m.WorkerAddress)
}
