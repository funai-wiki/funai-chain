package types

import (
	"context"
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
)

func init() {
	proto.RegisterType((*MsgUpdateParams)(nil), "funai.reward.MsgUpdateParams")
	proto.RegisterType((*MsgUpdateParamsResponse)(nil), "funai.reward.MsgUpdateParamsResponse")
	proto.RegisterType((*QueryParamsRequest)(nil), "funai.reward.QueryParamsRequest")
	proto.RegisterType((*QueryParamsResponse)(nil), "funai.reward.QueryParamsResponse")
	proto.RegisterType((*QueryRewardHistoryRequest)(nil), "funai.reward.QueryRewardHistoryRequest")
	proto.RegisterType((*QueryRewardHistoryResponse)(nil), "funai.reward.QueryRewardHistoryResponse")
}

type MsgUpdateParams struct {
	Authority string `protobuf:"bytes,1,opt,name=authority,proto3" json:"authority"`
	Params    Params `protobuf:"bytes,2,opt,name=params,proto3" json:"params"`
}

func (m *MsgUpdateParams) ProtoMessage() {}
func (m *MsgUpdateParams) Reset()        { *m = MsgUpdateParams{} }
func (m *MsgUpdateParams) String() string {
	return fmt.Sprintf("reward.MsgUpdateParams{authority:%q}", m.Authority)
}

func (m *MsgUpdateParams) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(m.Authority); err != nil {
		return ErrInvalidAddress.Wrapf("invalid authority address: %s", err)
	}
	return m.Params.Validate()
}

func (m *MsgUpdateParams) GetSigners() []sdk.AccAddress {
	authority, _ := sdk.AccAddressFromBech32(m.Authority)
	return []sdk.AccAddress{authority}
}

type MsgUpdateParamsResponse struct{}

func (m *MsgUpdateParamsResponse) ProtoMessage()  {}
func (m *MsgUpdateParamsResponse) Reset()         { *m = MsgUpdateParamsResponse{} }
func (m *MsgUpdateParamsResponse) String() string { return "reward.MsgUpdateParamsResponse" }

// MsgServer defines the reward module's gRPC message service.
type MsgServer interface {
	UpdateParams(ctx context.Context, msg *MsgUpdateParams) (*MsgUpdateParamsResponse, error)
}

// QueryServer defines the reward module's gRPC query service.
type QueryServer interface {
	QueryParams(ctx context.Context, req *QueryParamsRequest) (*QueryParamsResponse, error)
	QueryRewardHistory(ctx context.Context, req *QueryRewardHistoryRequest) (*QueryRewardHistoryResponse, error)
}

type QueryParamsRequest struct{}

func (m *QueryParamsRequest) ProtoMessage()  {}
func (m *QueryParamsRequest) Reset()         { *m = QueryParamsRequest{} }
func (m *QueryParamsRequest) String() string { return "reward.QueryParamsRequest" }

type QueryParamsResponse struct {
	Params Params `protobuf:"bytes,1,opt,name=params,proto3" json:"params"`
}

func (m *QueryParamsResponse) ProtoMessage()  {}
func (m *QueryParamsResponse) Reset()         { *m = QueryParamsResponse{} }
func (m *QueryParamsResponse) String() string { return "reward.QueryParamsResponse" }

type QueryRewardHistoryRequest struct {
	WorkerAddress string `protobuf:"bytes,1,opt,name=worker_address,proto3" json:"worker_address"`
}

func (m *QueryRewardHistoryRequest) ProtoMessage() {}
func (m *QueryRewardHistoryRequest) Reset()        { *m = QueryRewardHistoryRequest{} }
func (m *QueryRewardHistoryRequest) String() string {
	return fmt.Sprintf("reward.QueryRewardHistoryRequest{worker_address:%q}", m.WorkerAddress)
}

type QueryRewardHistoryResponse struct {
	Records []RewardRecord `protobuf:"bytes,1,rep,name=records,proto3" json:"records"`
}

func (m *QueryRewardHistoryResponse) ProtoMessage() {}
func (m *QueryRewardHistoryResponse) Reset()        { *m = QueryRewardHistoryResponse{} }
func (m *QueryRewardHistoryResponse) String() string {
	return fmt.Sprintf("reward.QueryRewardHistoryResponse{records:%d}", len(m.Records))
}
