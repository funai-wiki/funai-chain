package types

import (
	"context"

	"github.com/cosmos/gogoproto/proto"
)

func init() {
	proto.RegisterType((*MsgDepositResponse)(nil), "funai.settlement.MsgDepositResponse")
	proto.RegisterType((*MsgWithdrawResponse)(nil), "funai.settlement.MsgWithdrawResponse")
	proto.RegisterType((*MsgBatchSettlementResponse)(nil), "funai.settlement.MsgBatchSettlementResponse")
	proto.RegisterType((*MsgFraudProofResponse)(nil), "funai.settlement.MsgFraudProofResponse")
	proto.RegisterType((*MsgSecondVerificationResultResponse)(nil), "funai.settlement.MsgSecondVerificationResultResponse")
}

type MsgServer interface {
	Deposit(context.Context, *MsgDeposit) (*MsgDepositResponse, error)
	Withdraw(context.Context, *MsgWithdraw) (*MsgWithdrawResponse, error)
	BatchSettle(context.Context, *MsgBatchSettlement) (*MsgBatchSettlementResponse, error)
	SubmitFraudProof(context.Context, *MsgFraudProof) (*MsgFraudProofResponse, error)
	SubmitSecondVerificationResult(context.Context, *MsgSecondVerificationResult) (*MsgSecondVerificationResultResponse, error)
}

type MsgDepositResponse struct{}

func (m *MsgDepositResponse) ProtoMessage()  {}
func (m *MsgDepositResponse) Reset()         { *m = MsgDepositResponse{} }
func (m *MsgDepositResponse) String() string { return "MsgDepositResponse" }

type MsgWithdrawResponse struct{}

func (m *MsgWithdrawResponse) ProtoMessage()  {}
func (m *MsgWithdrawResponse) Reset()         { *m = MsgWithdrawResponse{} }
func (m *MsgWithdrawResponse) String() string { return "MsgWithdrawResponse" }

type MsgBatchSettlementResponse struct {
	BatchId uint64 `protobuf:"varint,1,opt,name=batch_id,proto3" json:"batch_id"`
}

func (m *MsgBatchSettlementResponse) ProtoMessage()  {}
func (m *MsgBatchSettlementResponse) Reset()         { *m = MsgBatchSettlementResponse{} }
func (m *MsgBatchSettlementResponse) String() string { return "MsgBatchSettlementResponse" }

type MsgFraudProofResponse struct{}

func (m *MsgFraudProofResponse) ProtoMessage()  {}
func (m *MsgFraudProofResponse) Reset()         { *m = MsgFraudProofResponse{} }
func (m *MsgFraudProofResponse) String() string { return "MsgFraudProofResponse" }

type MsgSecondVerificationResultResponse struct{}

func (m *MsgSecondVerificationResultResponse) ProtoMessage() {}
func (m *MsgSecondVerificationResultResponse) Reset()        { *m = MsgSecondVerificationResultResponse{} }
func (m *MsgSecondVerificationResultResponse) String() string {
	return "MsgSecondVerificationResultResponse"
}
