package types

import (
	"context"

	"github.com/cosmos/gogoproto/proto"
)

func init() {
	proto.RegisterType((*MsgModelProposalResponse)(nil), "funai.modelreg.MsgModelProposalResponse")
	proto.RegisterType((*MsgUpdateModelStatsResponse)(nil), "funai.modelreg.MsgUpdateModelStatsResponse")
	proto.RegisterType((*MsgDeclareInstalledResponse)(nil), "funai.modelreg.MsgDeclareInstalledResponse")
}

// MsgServer defines the modelreg module's gRPC message service.
type MsgServer interface {
	ProposeModel(context.Context, *MsgModelProposal) (*MsgModelProposalResponse, error)
	UpdateModelStats(context.Context, *MsgUpdateModelStats) (*MsgUpdateModelStatsResponse, error)
	DeclareInstalled(context.Context, *MsgDeclareInstalled) (*MsgDeclareInstalledResponse, error)
}

type MsgModelProposalResponse struct {
	ModelId string `protobuf:"bytes,1,opt,name=model_id,proto3" json:"model_id"`
}

func (m *MsgModelProposalResponse) ProtoMessage()  {}
func (m *MsgModelProposalResponse) Reset()         { *m = MsgModelProposalResponse{} }
func (m *MsgModelProposalResponse) String() string { return "MsgModelProposalResponse" }

type MsgUpdateModelStatsResponse struct{}

func (m *MsgUpdateModelStatsResponse) ProtoMessage()  {}
func (m *MsgUpdateModelStatsResponse) Reset()         { *m = MsgUpdateModelStatsResponse{} }
func (m *MsgUpdateModelStatsResponse) String() string { return "MsgUpdateModelStatsResponse" }

type MsgDeclareInstalledResponse struct{}

func (m *MsgDeclareInstalledResponse) ProtoMessage()  {}
func (m *MsgDeclareInstalledResponse) Reset()         { *m = MsgDeclareInstalledResponse{} }
func (m *MsgDeclareInstalledResponse) String() string { return "MsgDeclareInstalledResponse" }
