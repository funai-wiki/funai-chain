package types

import (
	"encoding/hex"
	"fmt"

	sdkerrors "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
)

func init() {
	proto.RegisterType((*MsgDeposit)(nil), "funai.settlement.MsgDeposit")
	proto.RegisterType((*MsgWithdraw)(nil), "funai.settlement.MsgWithdraw")
	proto.RegisterType((*MsgBatchSettlement)(nil), "funai.settlement.MsgBatchSettlement")
	proto.RegisterType((*MsgBatchReserve)(nil), "funai.settlement.MsgBatchReserve")
	proto.RegisterType((*ReserveEntry)(nil), "funai.settlement.ReserveEntry")
	proto.RegisterType((*MsgFraudProof)(nil), "funai.settlement.MsgFraudProof")
	proto.RegisterType((*MsgSecondVerificationResult)(nil), "funai.settlement.MsgSecondVerificationResult")
	proto.RegisterType((*MsgSecondVerificationResultBatch)(nil), "funai.settlement.MsgSecondVerificationResultBatch")
	proto.RegisterType((*SecondVerificationBatchEntry)(nil), "funai.settlement.SecondVerificationBatchEntry")
}

var (
	_ sdk.Msg = &MsgDeposit{}
	_ sdk.Msg = &MsgWithdraw{}
	_ sdk.Msg = &MsgBatchSettlement{}
	_ sdk.Msg = &MsgBatchReserve{}
	_ sdk.Msg = &MsgFraudProof{}
	_ sdk.Msg = &MsgSecondVerificationResult{}
	_ sdk.Msg = &MsgSecondVerificationResultBatch{}
)

// -------- MsgDeposit --------

type MsgDeposit struct {
	Creator string   `protobuf:"bytes,1,opt,name=creator,proto3" json:"creator"`
	Amount  sdk.Coin `protobuf:"bytes,2,opt,name=amount,proto3" json:"amount"`
}

func NewMsgDeposit(creator string, amount sdk.Coin) *MsgDeposit {
	return &MsgDeposit{Creator: creator, Amount: amount}
}

func (msg *MsgDeposit) ProtoMessage() {}
func (msg *MsgDeposit) Reset()        { *msg = MsgDeposit{} }
func (msg *MsgDeposit) String() string {
	return fmt.Sprintf("MsgDeposit{%s,%s}", msg.Creator, msg.Amount)
}

func (msg *MsgDeposit) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(msg.Creator); err != nil {
		return sdkerrors.Wrap(err, "invalid creator address")
	}
	if !msg.Amount.IsValid() || msg.Amount.IsZero() {
		return sdkerrors.Wrap(ErrInsufficientBalance, "amount must be positive and valid")
	}
	if msg.Amount.Denom != DefaultDenom {
		return sdkerrors.Wrapf(ErrWrongDenom, "got %s", msg.Amount.Denom)
	}
	return nil
}

func (msg *MsgDeposit) GetSigners() []sdk.AccAddress {
	creator, _ := sdk.AccAddressFromBech32(msg.Creator)
	return []sdk.AccAddress{creator}
}

// -------- MsgWithdraw --------

type MsgWithdraw struct {
	Creator string   `protobuf:"bytes,1,opt,name=creator,proto3" json:"creator"`
	Amount  sdk.Coin `protobuf:"bytes,2,opt,name=amount,proto3" json:"amount"`
}

func NewMsgWithdraw(creator string, amount sdk.Coin) *MsgWithdraw {
	return &MsgWithdraw{Creator: creator, Amount: amount}
}

func (msg *MsgWithdraw) ProtoMessage() {}
func (msg *MsgWithdraw) Reset()        { *msg = MsgWithdraw{} }
func (msg *MsgWithdraw) String() string {
	return fmt.Sprintf("MsgWithdraw{%s,%s}", msg.Creator, msg.Amount)
}

func (msg *MsgWithdraw) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(msg.Creator); err != nil {
		return sdkerrors.Wrap(err, "invalid creator address")
	}
	if !msg.Amount.IsValid() || msg.Amount.IsZero() {
		return sdkerrors.Wrap(ErrInsufficientBalance, "amount must be positive and valid")
	}
	if msg.Amount.Denom != DefaultDenom {
		return sdkerrors.Wrapf(ErrWrongDenom, "got %s", msg.Amount.Denom)
	}
	return nil
}

func (msg *MsgWithdraw) GetSigners() []sdk.AccAddress {
	creator, _ := sdk.AccAddressFromBech32(msg.Creator)
	return []sdk.AccAddress{creator}
}

// -------- MsgBatchSettlement --------

// MsgBatchSettlement settles a batch of CLEARED inference tasks.
// V5.2: entries are inline (no DA layer). Only CLEARED tasks are included.
type MsgBatchSettlement struct {
	Proposer    string            `protobuf:"bytes,1,opt,name=proposer,proto3" json:"proposer"`
	MerkleRoot  []byte            `protobuf:"bytes,2,opt,name=merkle_root,proto3" json:"merkle_root"`
	Entries     []SettlementEntry `protobuf:"bytes,3,rep,name=entries,proto3" json:"entries"`
	ProposerSig []byte            `protobuf:"bytes,4,opt,name=proposer_sig,proto3" json:"proposer_sig"`
	ResultCount uint32            `protobuf:"varint,5,opt,name=result_count,proto3" json:"result_count"`
}

func NewMsgBatchSettlement(proposer string, merkleRoot []byte, entries []SettlementEntry, proposerSig []byte) *MsgBatchSettlement {
	return &MsgBatchSettlement{
		Proposer:    proposer,
		MerkleRoot:  merkleRoot,
		Entries:     entries,
		ProposerSig: proposerSig,
		ResultCount: uint32(len(entries)),
	}
}

func (msg *MsgBatchSettlement) ProtoMessage() {}
func (msg *MsgBatchSettlement) Reset()        { *msg = MsgBatchSettlement{} }
func (msg *MsgBatchSettlement) String() string {
	return fmt.Sprintf("MsgBatchSettlement{%s,count=%d}", msg.Proposer, len(msg.Entries))
}

// MaxBatchSettlementEntries is the chain-side hard cap on entries per
// MsgBatchSettlement. Pre-fix this had no upper bound — a malicious or buggy
// proposer could submit an unbounded batch and OOM every validator.
//
// Value chosen against the binding constraint: CometBFT mempool tx-bytes
// (1 MiB default). Per `bench/bench_test.go::TestReportWireSize`, a real-size
// SettlementEntry serializes to ~783 bytes; 1 MiB / 783 ≈ 1338 entries.
// 1300 leaves ~5% (40 KB) headroom for proto encoding variance.
//
// Operators that raise CometBFT `mempool.max_tx_bytes` to take larger batches
// must coordinate raising this constant — bytes-dimension protection lives in
// both places intentionally.
const MaxBatchSettlementEntries = 1300

func (msg *MsgBatchSettlement) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(msg.Proposer); err != nil {
		return sdkerrors.Wrap(err, "invalid proposer address")
	}
	if len(msg.MerkleRoot) == 0 {
		return sdkerrors.Wrap(ErrInvalidSettlement, "merkle_root cannot be empty")
	}
	if len(msg.Entries) == 0 {
		return sdkerrors.Wrap(ErrInvalidSettlement, "entries cannot be empty")
	}
	if len(msg.Entries) > MaxBatchSettlementEntries {
		return sdkerrors.Wrapf(ErrInvalidSettlement, "batch size %d exceeds max %d", len(msg.Entries), MaxBatchSettlementEntries)
	}
	if len(msg.ProposerSig) == 0 {
		return sdkerrors.Wrap(ErrInvalidSignature, "proposer_sig cannot be empty")
	}
	if msg.ResultCount != uint32(len(msg.Entries)) {
		return sdkerrors.Wrapf(ErrInvalidSettlement, "result_count mismatch: declared %d, actual %d", msg.ResultCount, len(msg.Entries))
	}
	return nil
}

func (msg *MsgBatchSettlement) GetSigners() []sdk.AccAddress {
	proposer, _ := sdk.AccAddressFromBech32(msg.Proposer)
	return []sdk.AccAddress{proposer}
}

// -------- MsgBatchReserve --------

// MsgBatchReserve creates per-task chain-side balance reservations at task
// accept time for per-request billing. Closes KT 30-case Issue 1 — without
// it, a user can dispatch a task and then withdraw their balance before
// settlement, leaving the Worker unpaid.
//
// Per-token billing already had this via FreezeBalance/UnfreezeBalance; this
// message provides the equivalent entry point for per-request billing.
//
// Submitted by the Leader (or Proposer playing Leader role) periodically as
// new tasks are accepted off-chain. The keeper iterates entries, calls
// FreezeBalance per entry, and silently skips invalid / over-budget /
// duplicate / past-expiry rows so a single bad row cannot reject the batch.
//
// BatchSettlement automatically releases the freeze at settle time via
// UnfreezeBalance (idempotent — no-op when nothing is frozen).
type MsgBatchReserve struct {
	Proposer    string         `protobuf:"bytes,1,opt,name=proposer,proto3" json:"proposer"`
	MerkleRoot  []byte         `protobuf:"bytes,2,opt,name=merkle_root,proto3" json:"merkle_root"`
	Entries     []ReserveEntry `protobuf:"bytes,3,rep,name=entries,proto3" json:"entries"`
	ProposerSig []byte         `protobuf:"bytes,4,opt,name=proposer_sig,proto3" json:"proposer_sig"`
	ResultCount uint32         `protobuf:"varint,5,opt,name=result_count,proto3" json:"result_count"`
}

// ReserveEntry is one chain-side reservation: freeze MaxFee from UserAddress
// for TaskId, until ExpireBlock or settlement.
type ReserveEntry struct {
	UserAddress string   `protobuf:"bytes,1,opt,name=user_address,proto3" json:"user_address"`
	TaskId      []byte   `protobuf:"bytes,2,opt,name=task_id,proto3" json:"task_id"`
	MaxFee      sdk.Coin `protobuf:"bytes,3,opt,name=max_fee,proto3" json:"max_fee"`
	ExpireBlock int64    `protobuf:"varint,4,opt,name=expire_block,proto3" json:"expire_block"`
}

func (m *ReserveEntry) ProtoMessage() {}
func (m *ReserveEntry) Reset()        { *m = ReserveEntry{} }
func (m *ReserveEntry) String() string {
	return fmt.Sprintf("ReserveEntry{user=%s,task=%s,max_fee=%s,expire=%d}",
		m.UserAddress, hex.EncodeToString(m.TaskId), m.MaxFee, m.ExpireBlock)
}

func NewMsgBatchReserve(proposer string, merkleRoot []byte, entries []ReserveEntry, proposerSig []byte) *MsgBatchReserve {
	return &MsgBatchReserve{
		Proposer:    proposer,
		MerkleRoot:  merkleRoot,
		Entries:     entries,
		ProposerSig: proposerSig,
		ResultCount: uint32(len(entries)),
	}
}

func (msg *MsgBatchReserve) ProtoMessage() {}
func (msg *MsgBatchReserve) Reset()        { *msg = MsgBatchReserve{} }
func (msg *MsgBatchReserve) String() string {
	return fmt.Sprintf("MsgBatchReserve{%s,count=%d}", msg.Proposer, len(msg.Entries))
}

func (msg *MsgBatchReserve) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(msg.Proposer); err != nil {
		return sdkerrors.Wrap(err, "invalid proposer address")
	}
	if len(msg.MerkleRoot) == 0 {
		return sdkerrors.Wrap(ErrInvalidSettlement, "merkle_root cannot be empty")
	}
	if len(msg.Entries) == 0 {
		return sdkerrors.Wrap(ErrInvalidSettlement, "entries cannot be empty")
	}
	if len(msg.ProposerSig) == 0 {
		return sdkerrors.Wrap(ErrInvalidSignature, "proposer_sig cannot be empty")
	}
	if msg.ResultCount != uint32(len(msg.Entries)) {
		return sdkerrors.Wrapf(ErrInvalidSettlement, "result_count mismatch: declared %d, actual %d", msg.ResultCount, len(msg.Entries))
	}
	return nil
}

func (msg *MsgBatchReserve) GetSigners() []sdk.AccAddress {
	proposer, _ := sdk.AccAddressFromBech32(msg.Proposer)
	return []sdk.AccAddress{proposer}
}

// -------- MsgFraudProof (Phase 2: receipt-vs-content contradiction proof) --------
//
// Phase 2 redesigns FraudProof around a Worker-signed contradiction:
// the Worker promised (via the receipt sent to Verifiers / Proposer) that the
// inference output hashes to ReceiptResultHash, but actually delivered content
// hashing to ReceivedOutputHash, and these two are different. Both halves are
// proven on-chain by verifying Worker's secp256k1 signature over the canonical
// payload sha256(task_id || hash) — see ReceiptSigPayload / ContentSigPayload
// in p2p/types/messages.go for the exact byte layout.
//
// Replaces the Phase 1 schema (ContentHash + ActualContent + WorkerContentSig)
// which only attested self-consistency rather than fraud — a captured legitimate
// signature could pair with its matching content and pass the bind check
// without proving any wrongdoing. This pre-mainnet schema swap is the actual
// security closure that Phase 1's fraud window + reporter binding were a
// down-payment on.
type MsgFraudProof struct {
	Reporter      string `protobuf:"bytes,1,opt,name=reporter,proto3" json:"reporter"`
	TaskId        []byte `protobuf:"bytes,2,opt,name=task_id,proto3" json:"task_id"`
	WorkerAddress string `protobuf:"bytes,3,opt,name=worker_address,proto3" json:"worker_address"`

	// ReceiptResultHash is the result_hash the Worker committed to inside the
	// InferReceipt that went to Verifiers / Proposer. The user (or watchtower)
	// captures this from the receipt published on /funai/receipt/<task_id>.
	ReceiptResultHash []byte `protobuf:"bytes,4,opt,name=receipt_result_hash,proto3" json:"receipt_result_hash"`
	// WorkerReceiptSig is the Worker's secp256k1 signature over
	// p2ptypes.ReceiptSigPayload(task_id, receipt_result_hash) — i.e.
	// sha256(task_id || receipt_result_hash). Bound to task_id so a sig from
	// one task cannot be replayed in a FraudProof for a different task.
	WorkerReceiptSig []byte `protobuf:"bytes,5,opt,name=worker_receipt_sig,proto3" json:"worker_receipt_sig"`

	// ReceivedOutputHash is sha256(content) for the bytes the user actually
	// received via the StreamToken stream.
	ReceivedOutputHash []byte `protobuf:"bytes,6,opt,name=received_output_hash,proto3" json:"received_output_hash"`
	// WorkerContentSig is the Worker's secp256k1 signature over
	// p2ptypes.ContentSigPayload(task_id, received_output_hash) — i.e.
	// sha256(task_id || received_output_hash). Same task_id binding rationale
	// as WorkerReceiptSig.
	WorkerContentSig []byte `protobuf:"bytes,7,opt,name=worker_content_sig,proto3" json:"worker_content_sig"`
}

func NewMsgFraudProof(reporter string, taskId []byte, workerAddress string,
	receiptResultHash, workerReceiptSig, receivedOutputHash, workerContentSig []byte) *MsgFraudProof {
	return &MsgFraudProof{
		Reporter:           reporter,
		TaskId:             taskId,
		WorkerAddress:      workerAddress,
		ReceiptResultHash:  receiptResultHash,
		WorkerReceiptSig:   workerReceiptSig,
		ReceivedOutputHash: receivedOutputHash,
		WorkerContentSig:   workerContentSig,
	}
}

func (msg *MsgFraudProof) ProtoMessage() {}
func (msg *MsgFraudProof) Reset()        { *msg = MsgFraudProof{} }
func (msg *MsgFraudProof) String() string {
	return fmt.Sprintf("MsgFraudProof{%s,%s}", msg.Reporter, hex.EncodeToString(msg.TaskId))
}

func (msg *MsgFraudProof) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(msg.Reporter); err != nil {
		return sdkerrors.Wrap(err, "invalid reporter address")
	}
	// PR #54 review: enforce strict 32-byte (SHA-256) length on all hash
	// fields at the mempool gate so malformed-length tx never reach the
	// keeper. Production task_id = SHA256(user_pubkey || model_id ||
	// prompt_hash || timestamp); receipt_result_hash and received_output_hash
	// are SHA-256 of inference outputs. Keeper-level tests bypass
	// ValidateBasic and may use shorter task_id literals for readability —
	// that's intentional, the ProcessFraudProof handler does not duplicate
	// the length check.
	if len(msg.TaskId) != 32 {
		return sdkerrors.Wrapf(ErrInvalidSettlement, "task_id must be 32 bytes, got %d", len(msg.TaskId))
	}
	if _, err := sdk.AccAddressFromBech32(msg.WorkerAddress); err != nil {
		return sdkerrors.Wrap(err, "invalid worker address")
	}
	if len(msg.ReceiptResultHash) != 32 {
		return sdkerrors.Wrapf(ErrInvalidSettlement, "receipt_result_hash must be 32 bytes, got %d", len(msg.ReceiptResultHash))
	}
	if len(msg.WorkerReceiptSig) == 0 {
		return sdkerrors.Wrap(ErrInvalidSignature, "worker_receipt_sig cannot be empty")
	}
	if len(msg.ReceivedOutputHash) != 32 {
		return sdkerrors.Wrapf(ErrInvalidSettlement, "received_output_hash must be 32 bytes, got %d", len(msg.ReceivedOutputHash))
	}
	if len(msg.WorkerContentSig) == 0 {
		return sdkerrors.Wrap(ErrInvalidSignature, "worker_content_sig cannot be empty")
	}
	return nil
}

func (msg *MsgFraudProof) GetSigners() []sdk.AccAddress {
	reporter, _ := sdk.AccAddressFromBech32(msg.Reporter)
	return []sdk.AccAddress{reporter}
}

// -------- MsgSecondVerificationResult --------

type MsgSecondVerificationResult struct {
	SecondVerifier       string `protobuf:"bytes,1,opt,name=second_verifier,proto3" json:"second_verifier"`
	TaskId               []byte `protobuf:"bytes,2,opt,name=task_id,proto3" json:"task_id"`
	Epoch                int64  `protobuf:"varint,3,opt,name=epoch,proto3" json:"epoch"`
	Pass                 bool   `protobuf:"varint,4,opt,name=pass,proto3" json:"pass"`
	LogitsHash           []byte `protobuf:"bytes,5,opt,name=logits_hash,proto3" json:"logits_hash"`
	VerifiedInputTokens  uint32 `protobuf:"varint,6,opt,name=verified_input_tokens,proto3" json:"verified_input_tokens,omitempty"`
	VerifiedOutputTokens uint32 `protobuf:"varint,7,opt,name=verified_output_tokens,proto3" json:"verified_output_tokens,omitempty"`
}

func NewMsgSecondVerificationResult(second_verifier string, taskId []byte, epoch int64, pass bool, logitsHash []byte) *MsgSecondVerificationResult {
	return &MsgSecondVerificationResult{
		SecondVerifier: second_verifier,
		TaskId:         taskId,
		Epoch:          epoch,
		Pass:           pass,
		LogitsHash:     logitsHash,
	}
}

func (msg *MsgSecondVerificationResult) ProtoMessage() {}
func (msg *MsgSecondVerificationResult) Reset()        { *msg = MsgSecondVerificationResult{} }
func (msg *MsgSecondVerificationResult) String() string {
	return fmt.Sprintf("MsgSecondVerificationResult{%s,%s}", msg.SecondVerifier, hex.EncodeToString(msg.TaskId))
}

func (msg *MsgSecondVerificationResult) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(msg.SecondVerifier); err != nil {
		return sdkerrors.Wrap(err, "invalid second_verifier address")
	}
	if len(msg.TaskId) == 0 {
		return sdkerrors.Wrap(ErrInvalidSettlement, "task_id cannot be empty")
	}
	if msg.Epoch < 0 {
		return sdkerrors.Wrap(ErrInvalidSettlement, "epoch cannot be negative")
	}
	if len(msg.LogitsHash) == 0 {
		return sdkerrors.Wrap(ErrInvalidSettlement, "logits_hash cannot be empty")
	}
	return nil
}

func (msg *MsgSecondVerificationResult) GetSigners() []sdk.AccAddress {
	second_verifier, _ := sdk.AccAddressFromBech32(msg.SecondVerifier)
	return []sdk.AccAddress{second_verifier}
}

// -------- MsgSecondVerificationResultBatch (D2) --------
//
// Carries one or more second/third-tier verification results that the
// Proposer has already collected and verified over P2P. Each entry embeds
// the verifier's own secp256k1 signature over the canonical result fields,
// so the keeper can attribute every entry to the correct verifier even
// though the tx itself is signed by a single proposer account.
//
// Mirrors the existing BatchSettlement pattern: proposer pays gas; per-entry
// authorship is proven by embedded sigs verified in-keeper (not by the
// Cosmos SDK antehandler). Avoids requiring every second-tier verifier to
// hold gas and run tx-broadcasting infrastructure.

// SecondVerificationBatchEntry is one audit result inside a batch.
// Field layout matches p2p/types SecondVerificationResponse (task_id, pass,
// verifier pubkey, logits_hash, token counts) so the P2P signature carries
// forward without re-signing. SecondVerifier is the bech32 account address
// resolved from that pubkey; the keeper looks the pubkey up via x/worker
// and rejects entries where the stored pubkey and the entry disagree.
type SecondVerificationBatchEntry struct {
	TaskId               []byte `protobuf:"bytes,1,opt,name=task_id,proto3" json:"task_id"`
	SecondVerifier       string `protobuf:"bytes,2,opt,name=second_verifier,proto3" json:"second_verifier"`
	Epoch                int64  `protobuf:"varint,3,opt,name=epoch,proto3" json:"epoch"`
	Pass                 bool   `protobuf:"varint,4,opt,name=pass,proto3" json:"pass"`
	LogitsHash           []byte `protobuf:"bytes,5,opt,name=logits_hash,proto3" json:"logits_hash"`
	VerifiedInputTokens  uint32 `protobuf:"varint,6,opt,name=verified_input_tokens,proto3" json:"verified_input_tokens,omitempty"`
	VerifiedOutputTokens uint32 `protobuf:"varint,7,opt,name=verified_output_tokens,proto3" json:"verified_output_tokens,omitempty"`
	Signature            []byte `protobuf:"bytes,8,opt,name=signature,proto3" json:"signature"`
}

func (m *SecondVerificationBatchEntry) ProtoMessage()  {}
func (m *SecondVerificationBatchEntry) Reset()         { *m = SecondVerificationBatchEntry{} }
func (m *SecondVerificationBatchEntry) String() string { return "SecondVerificationBatchEntry" }

type MsgSecondVerificationResultBatch struct {
	Proposer string                         `protobuf:"bytes,1,opt,name=proposer,proto3" json:"proposer"`
	Entries  []SecondVerificationBatchEntry `protobuf:"bytes,2,rep,name=entries,proto3" json:"entries"`
}

func NewMsgSecondVerificationResultBatch(proposer string, entries []SecondVerificationBatchEntry) *MsgSecondVerificationResultBatch {
	return &MsgSecondVerificationResultBatch{Proposer: proposer, Entries: entries}
}

func (msg *MsgSecondVerificationResultBatch) ProtoMessage()  {}
func (msg *MsgSecondVerificationResultBatch) Reset()         { *msg = MsgSecondVerificationResultBatch{} }
func (msg *MsgSecondVerificationResultBatch) String() string {
	return fmt.Sprintf("MsgSecondVerificationResultBatch{%s,%d}", msg.Proposer, len(msg.Entries))
}

// MaxSecondVerificationBatchEntries bounds batch size to keep gas bounded
// (per-entry keeper cost is a pubkey lookup + secp256k1 verify + state read/write).
const MaxSecondVerificationBatchEntries = 256

func (msg *MsgSecondVerificationResultBatch) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(msg.Proposer); err != nil {
		return sdkerrors.Wrap(err, "invalid proposer address")
	}
	if len(msg.Entries) == 0 {
		return sdkerrors.Wrap(ErrInvalidSettlement, "batch must contain at least one entry")
	}
	if len(msg.Entries) > MaxSecondVerificationBatchEntries {
		return sdkerrors.Wrapf(ErrInvalidSettlement, "batch size %d exceeds max %d", len(msg.Entries), MaxSecondVerificationBatchEntries)
	}
	for i, e := range msg.Entries {
		if _, err := sdk.AccAddressFromBech32(e.SecondVerifier); err != nil {
			return sdkerrors.Wrapf(err, "entry %d: invalid second_verifier address", i)
		}
		if len(e.TaskId) == 0 {
			return sdkerrors.Wrapf(ErrInvalidSettlement, "entry %d: task_id cannot be empty", i)
		}
		if e.Epoch < 0 {
			return sdkerrors.Wrapf(ErrInvalidSettlement, "entry %d: epoch cannot be negative", i)
		}
		if len(e.LogitsHash) == 0 {
			return sdkerrors.Wrapf(ErrInvalidSettlement, "entry %d: logits_hash cannot be empty", i)
		}
		if len(e.Signature) == 0 {
			return sdkerrors.Wrapf(ErrInvalidSettlement, "entry %d: signature cannot be empty", i)
		}
	}
	return nil
}

// GetSigners returns the Proposer, NOT each verifier. Per-verifier
// authenticity is proven by the embedded signature on each entry and
// verified in-keeper — the Cosmos SDK antehandler only authenticates the
// proposer (for gas and tx submission).
func (msg *MsgSecondVerificationResultBatch) GetSigners() []sdk.AccAddress {
	proposer, _ := sdk.AccAddressFromBech32(msg.Proposer)
	return []sdk.AccAddress{proposer}
}
