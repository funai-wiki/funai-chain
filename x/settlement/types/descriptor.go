package types

import (
	"github.com/funai-wiki/funai-chain/pkg/protodesc"
)

var settlementDescGz []byte

func init() {
	settlementDescGz = protodesc.BuildAndRegister(protodesc.FileDescriptor{
		FileName:    "funai/settlement/types.proto",
		PackageName: "funai.settlement",
		Messages: []protodesc.MsgEntry{
			{Name: "MsgDeposit", Instance: MsgDeposit{}},
			{Name: "MsgWithdraw", Instance: MsgWithdraw{}},
			{Name: "MsgBatchSettlement", Instance: MsgBatchSettlement{}},
			{Name: "MsgFraudProof", Instance: MsgFraudProof{}},
			{Name: "MsgSecondVerificationResult", Instance: MsgSecondVerificationResult{}},
			{Name: "MsgDepositResponse", Instance: MsgDepositResponse{}},
			{Name: "MsgWithdrawResponse", Instance: MsgWithdrawResponse{}},
			{Name: "MsgBatchSettlementResponse", Instance: MsgBatchSettlementResponse{}},
			{Name: "MsgFraudProofResponse", Instance: MsgFraudProofResponse{}},
			{Name: "MsgSecondVerificationResultResponse", Instance: MsgSecondVerificationResultResponse{}},
			{Name: "SettlementEntry", Instance: SettlementEntry{}},
			{Name: "VerifierResult", Instance: VerifierResult{}},
			{Name: "InferenceAccount", Instance: InferenceAccount{}},
			{Name: "BatchRecord", Instance: BatchRecord{}},
			{Name: "QueryInferenceAccountRequest", Instance: QueryInferenceAccountRequest{}},
			{Name: "QueryInferenceAccountResponse", Instance: QueryInferenceAccountResponse{}},
			{Name: "QueryBatchRequest", Instance: QueryBatchRequest{}},
			{Name: "QueryBatchResponse", Instance: QueryBatchResponse{}},
			{Name: "QueryParamsRequest", Instance: QueryParamsRequest{}},
			{Name: "QueryParamsResponse", Instance: QueryParamsResponse{}},
		},
		Services: []protodesc.ServiceEntry{
			{
				Name: "Msg",
				Methods: []protodesc.MethodEntry{
					{Name: "Deposit", InputType: ".funai.settlement.MsgDeposit", OutputType: ".funai.settlement.MsgDepositResponse"},
					{Name: "Withdraw", InputType: ".funai.settlement.MsgWithdraw", OutputType: ".funai.settlement.MsgWithdrawResponse"},
					{Name: "BatchSettle", InputType: ".funai.settlement.MsgBatchSettlement", OutputType: ".funai.settlement.MsgBatchSettlementResponse"},
					{Name: "SubmitFraudProof", InputType: ".funai.settlement.MsgFraudProof", OutputType: ".funai.settlement.MsgFraudProofResponse"},
					{Name: "SubmitSecondVerificationResult", InputType: ".funai.settlement.MsgSecondVerificationResult", OutputType: ".funai.settlement.MsgSecondVerificationResultResponse"},
				},
			},
			{
				Name: "Query",
				Methods: []protodesc.MethodEntry{
					{Name: "InferenceAccount", InputType: ".funai.settlement.QueryInferenceAccountRequest", OutputType: ".funai.settlement.QueryInferenceAccountResponse"},
					{Name: "Batch", InputType: ".funai.settlement.QueryBatchRequest", OutputType: ".funai.settlement.QueryBatchResponse"},
					{Name: "Params", InputType: ".funai.settlement.QueryParamsRequest", OutputType: ".funai.settlement.QueryParamsResponse"},
				},
			},
		},
	})
}

func (m *MsgDeposit) Descriptor() ([]byte, []int)                  { return settlementDescGz, []int{0} }
func (m *MsgWithdraw) Descriptor() ([]byte, []int)                 { return settlementDescGz, []int{1} }
func (m *MsgBatchSettlement) Descriptor() ([]byte, []int)          { return settlementDescGz, []int{2} }
func (m *MsgFraudProof) Descriptor() ([]byte, []int)               { return settlementDescGz, []int{3} }
func (m *MsgSecondVerificationResult) Descriptor() ([]byte, []int) { return settlementDescGz, []int{4} }
func (m *MsgDepositResponse) Descriptor() ([]byte, []int)          { return settlementDescGz, []int{5} }
func (m *MsgWithdrawResponse) Descriptor() ([]byte, []int)         { return settlementDescGz, []int{6} }
func (m *MsgBatchSettlementResponse) Descriptor() ([]byte, []int)  { return settlementDescGz, []int{7} }
func (m *MsgFraudProofResponse) Descriptor() ([]byte, []int)       { return settlementDescGz, []int{8} }
func (m *MsgSecondVerificationResultResponse) Descriptor() ([]byte, []int) {
	return settlementDescGz, []int{9}
}
func (m *SettlementEntry) Descriptor() ([]byte, []int)  { return settlementDescGz, []int{10} }
func (m *VerifierResult) Descriptor() ([]byte, []int)   { return settlementDescGz, []int{11} }
func (m *InferenceAccount) Descriptor() ([]byte, []int) { return settlementDescGz, []int{12} }
func (m *BatchRecord) Descriptor() ([]byte, []int)      { return settlementDescGz, []int{13} }
func (m *QueryInferenceAccountRequest) Descriptor() ([]byte, []int) {
	return settlementDescGz, []int{14}
}
func (m *QueryInferenceAccountResponse) Descriptor() ([]byte, []int) {
	return settlementDescGz, []int{15}
}
func (m *QueryBatchRequest) Descriptor() ([]byte, []int)   { return settlementDescGz, []int{16} }
func (m *QueryBatchResponse) Descriptor() ([]byte, []int)  { return settlementDescGz, []int{17} }
func (m *QueryParamsRequest) Descriptor() ([]byte, []int)  { return settlementDescGz, []int{18} }
func (m *QueryParamsResponse) Descriptor() ([]byte, []int) { return settlementDescGz, []int{19} }
