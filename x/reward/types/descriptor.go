package types

import (
	"github.com/funai-wiki/funai-chain/pkg/protodesc"
)

var rewardDescGz []byte

func init() {
	rewardDescGz = protodesc.BuildAndRegister(protodesc.FileDescriptor{
		FileName:    "funai/reward/types.proto",
		PackageName: "funai.reward",
		Messages: []protodesc.MsgEntry{
			{Name: "MsgUpdateParams", Instance: MsgUpdateParams{}},
			{Name: "MsgUpdateParamsResponse", Instance: MsgUpdateParamsResponse{}},
			{Name: "QueryParamsRequest", Instance: QueryParamsRequest{}},
			{Name: "QueryParamsResponse", Instance: QueryParamsResponse{}},
			{Name: "QueryRewardHistoryRequest", Instance: QueryRewardHistoryRequest{}},
			{Name: "QueryRewardHistoryResponse", Instance: QueryRewardHistoryResponse{}},
		},
		Services: []protodesc.ServiceEntry{
			{
				Name: "Msg",
				Methods: []protodesc.MethodEntry{
					{Name: "UpdateParams", InputType: ".funai.reward.MsgUpdateParams", OutputType: ".funai.reward.MsgUpdateParamsResponse"},
				},
			},
			{
				Name: "Query",
				Methods: []protodesc.MethodEntry{
					{Name: "Params", InputType: ".funai.reward.QueryParamsRequest", OutputType: ".funai.reward.QueryParamsResponse"},
					{Name: "RewardHistory", InputType: ".funai.reward.QueryRewardHistoryRequest", OutputType: ".funai.reward.QueryRewardHistoryResponse"},
				},
			},
		},
	})
}

func (m *MsgUpdateParams) Descriptor() ([]byte, []int)            { return rewardDescGz, []int{0} }
func (m *MsgUpdateParamsResponse) Descriptor() ([]byte, []int)    { return rewardDescGz, []int{1} }
func (m *QueryParamsRequest) Descriptor() ([]byte, []int)         { return rewardDescGz, []int{2} }
func (m *QueryParamsResponse) Descriptor() ([]byte, []int)        { return rewardDescGz, []int{3} }
func (m *QueryRewardHistoryRequest) Descriptor() ([]byte, []int)  { return rewardDescGz, []int{4} }
func (m *QueryRewardHistoryResponse) Descriptor() ([]byte, []int) { return rewardDescGz, []int{5} }
