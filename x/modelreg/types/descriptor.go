package types

import (
	"github.com/funai-wiki/funai-chain/pkg/protodesc"
)

var modelregDescGz []byte

func init() {
	modelregDescGz = protodesc.BuildAndRegister(protodesc.FileDescriptor{
		FileName:    "funai/modelreg/types.proto",
		PackageName: "funai.modelreg",
		Messages: []protodesc.MsgEntry{
			{Name: "MsgModelProposal", Instance: MsgModelProposal{}},
			{Name: "MsgUpdateModelStats", Instance: MsgUpdateModelStats{}},
			{Name: "MsgDeclareInstalled", Instance: MsgDeclareInstalled{}},
			{Name: "MsgModelProposalResponse", Instance: MsgModelProposalResponse{}},
			{Name: "MsgUpdateModelStatsResponse", Instance: MsgUpdateModelStatsResponse{}},
			{Name: "MsgDeclareInstalledResponse", Instance: MsgDeclareInstalledResponse{}},
			{Name: "Model", Instance: Model{}},
			{Name: "QueryModelRequest", Instance: QueryModelRequest{}},
			{Name: "QueryModelResponse", Instance: QueryModelResponse{}},
			{Name: "QueryModelsRequest", Instance: QueryModelsRequest{}},
			{Name: "QueryModelsResponse", Instance: QueryModelsResponse{}},
			{Name: "QueryParamsRequest", Instance: QueryParamsRequest{}},
			{Name: "QueryParamsResponse", Instance: QueryParamsResponse{}},
			{Name: "QueryModelByAliasRequest", Instance: QueryModelByAliasRequest{}},
		},
		Services: []protodesc.ServiceEntry{
			{
				Name: "Msg",
				Methods: []protodesc.MethodEntry{
					{Name: "ProposeModel", InputType: ".funai.modelreg.MsgModelProposal", OutputType: ".funai.modelreg.MsgModelProposalResponse"},
					{Name: "UpdateModelStats", InputType: ".funai.modelreg.MsgUpdateModelStats", OutputType: ".funai.modelreg.MsgUpdateModelStatsResponse"},
					{Name: "DeclareInstalled", InputType: ".funai.modelreg.MsgDeclareInstalled", OutputType: ".funai.modelreg.MsgDeclareInstalledResponse"},
				},
			},
			{
				Name: "Query",
				Methods: []protodesc.MethodEntry{
					{Name: "Model", InputType: ".funai.modelreg.QueryModelRequest", OutputType: ".funai.modelreg.QueryModelResponse"},
					{Name: "Models", InputType: ".funai.modelreg.QueryModelsRequest", OutputType: ".funai.modelreg.QueryModelsResponse"},
					{Name: "Params", InputType: ".funai.modelreg.QueryParamsRequest", OutputType: ".funai.modelreg.QueryParamsResponse"},
					{Name: "ModelByAlias", InputType: ".funai.modelreg.QueryModelByAliasRequest", OutputType: ".funai.modelreg.QueryModelResponse"},
				},
			},
		},
	})
}

func (m *MsgModelProposal) Descriptor() ([]byte, []int)            { return modelregDescGz, []int{0} }
func (m *MsgUpdateModelStats) Descriptor() ([]byte, []int)         { return modelregDescGz, []int{1} }
func (m *MsgDeclareInstalled) Descriptor() ([]byte, []int)         { return modelregDescGz, []int{2} }
func (m *MsgModelProposalResponse) Descriptor() ([]byte, []int)    { return modelregDescGz, []int{3} }
func (m *MsgUpdateModelStatsResponse) Descriptor() ([]byte, []int) { return modelregDescGz, []int{4} }
func (m *MsgDeclareInstalledResponse) Descriptor() ([]byte, []int) { return modelregDescGz, []int{5} }
func (m *Model) Descriptor() ([]byte, []int)                       { return modelregDescGz, []int{6} }
func (m *QueryModelRequest) Descriptor() ([]byte, []int)           { return modelregDescGz, []int{7} }
func (m *QueryModelResponse) Descriptor() ([]byte, []int)          { return modelregDescGz, []int{8} }
func (m *QueryModelsRequest) Descriptor() ([]byte, []int)          { return modelregDescGz, []int{9} }
func (m *QueryModelsResponse) Descriptor() ([]byte, []int)         { return modelregDescGz, []int{10} }
func (m *QueryParamsRequest) Descriptor() ([]byte, []int)          { return modelregDescGz, []int{11} }
func (m *QueryParamsResponse) Descriptor() ([]byte, []int)         { return modelregDescGz, []int{12} }
func (m *QueryModelByAliasRequest) Descriptor() ([]byte, []int)    { return modelregDescGz, []int{13} }
