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
	proto.RegisterType((*MsgFraudProof)(nil), "funai.settlement.MsgFraudProof")
	proto.RegisterType((*MsgSecondVerificationResult)(nil), "funai.settlement.MsgSecondVerificationResult")
}

var (
	_ sdk.Msg = &MsgDeposit{}
	_ sdk.Msg = &MsgWithdraw{}
	_ sdk.Msg = &MsgBatchSettlement{}
	_ sdk.Msg = &MsgFraudProof{}
	_ sdk.Msg = &MsgSecondVerificationResult{}
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

// -------- MsgFraudProof --------

type MsgFraudProof struct {
	Reporter         string `protobuf:"bytes,1,opt,name=reporter,proto3" json:"reporter"`
	TaskId           []byte `protobuf:"bytes,2,opt,name=task_id,proto3" json:"task_id"`
	WorkerAddress    string `protobuf:"bytes,3,opt,name=worker_address,proto3" json:"worker_address"`
	ContentHash      []byte `protobuf:"bytes,4,opt,name=content_hash,proto3" json:"content_hash"`
	WorkerContentSig []byte `protobuf:"bytes,5,opt,name=worker_content_sig,proto3" json:"worker_content_sig"`
	ActualContent    []byte `protobuf:"bytes,6,opt,name=actual_content,proto3" json:"actual_content"`
}

func NewMsgFraudProof(reporter string, taskId []byte, workerAddress string, contentHash, workerContentSig, actualContent []byte) *MsgFraudProof {
	return &MsgFraudProof{
		Reporter:         reporter,
		TaskId:           taskId,
		WorkerAddress:    workerAddress,
		ContentHash:      contentHash,
		WorkerContentSig: workerContentSig,
		ActualContent:    actualContent,
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
	if len(msg.TaskId) == 0 {
		return sdkerrors.Wrap(ErrInvalidSettlement, "task_id cannot be empty")
	}
	if _, err := sdk.AccAddressFromBech32(msg.WorkerAddress); err != nil {
		return sdkerrors.Wrap(err, "invalid worker address")
	}
	if len(msg.ContentHash) == 0 {
		return sdkerrors.Wrap(ErrInvalidSettlement, "content_hash cannot be empty")
	}
	if len(msg.WorkerContentSig) == 0 {
		return sdkerrors.Wrap(ErrInvalidSignature, "worker_content_sig cannot be empty")
	}
	if len(msg.ActualContent) == 0 {
		return sdkerrors.Wrap(ErrInvalidSettlement, "actual_content cannot be empty")
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
