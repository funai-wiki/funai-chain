package types

import (
	"fmt"

	sdkerrors "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
)

func init() {
	proto.RegisterType((*MsgRegisterWorker)(nil), "funai.worker.MsgRegisterWorker")
	proto.RegisterType((*MsgExitWorker)(nil), "funai.worker.MsgExitWorker")
	proto.RegisterType((*MsgUpdateModels)(nil), "funai.worker.MsgUpdateModels")
	proto.RegisterType((*MsgStake)(nil), "funai.worker.MsgStake")
	proto.RegisterType((*MsgUnjail)(nil), "funai.worker.MsgUnjail")
}

var (
	_ sdk.Msg = &MsgRegisterWorker{}
	_ sdk.Msg = &MsgExitWorker{}
	_ sdk.Msg = &MsgUpdateModels{}
	_ sdk.Msg = &MsgStake{}
	_ sdk.Msg = &MsgUnjail{}
)

// -------- MsgRegisterWorker --------

type MsgRegisterWorker struct {
	Creator         string   `protobuf:"bytes,1,opt,name=creator,proto3" json:"creator"`
	Pubkey          string   `protobuf:"bytes,2,opt,name=pubkey,proto3" json:"pubkey"`
	SupportedModels []string `protobuf:"bytes,3,rep,name=supported_models,proto3" json:"supported_models"`
	Endpoint        string   `protobuf:"bytes,4,opt,name=endpoint,proto3" json:"endpoint"`
	GpuModel        string   `protobuf:"bytes,5,opt,name=gpu_model,proto3" json:"gpu_model"`
	GpuVramGb       uint32   `protobuf:"varint,6,opt,name=gpu_vram_gb,proto3" json:"gpu_vram_gb"`
	GpuCount        uint32   `protobuf:"varint,7,opt,name=gpu_count,proto3" json:"gpu_count"`
	OperatorId      string   `protobuf:"bytes,8,opt,name=operator_id,proto3" json:"operator_id"`
}

func NewMsgRegisterWorker(creator, pubkey string, models []string, endpoint, gpuModel string, gpuVramGb uint32, gpuCount uint32, operatorId string) *MsgRegisterWorker {
	return &MsgRegisterWorker{
		Creator:         creator,
		Pubkey:          pubkey,
		SupportedModels: models,
		Endpoint:        endpoint,
		GpuModel:        gpuModel,
		GpuVramGb:       gpuVramGb,
		GpuCount:        gpuCount,
		OperatorId:      operatorId,
	}
}

func (msg *MsgRegisterWorker) ProtoMessage() {}
func (msg *MsgRegisterWorker) Reset()        { *msg = MsgRegisterWorker{} }
func (msg *MsgRegisterWorker) String() string {
	return fmt.Sprintf("MsgRegisterWorker{%s}", msg.Creator)
}

func (msg *MsgRegisterWorker) ValidateBasic() error {
	_, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return sdkerrors.Wrap(err, "invalid creator address")
	}
	if msg.Pubkey == "" {
		return sdkerrors.Wrap(ErrInvalidModels, "pubkey cannot be empty")
	}
	if len(msg.SupportedModels) == 0 {
		return sdkerrors.Wrap(ErrInvalidModels, "at least one supported model is required")
	}
	for _, m := range msg.SupportedModels {
		if m == "" {
			return sdkerrors.Wrap(ErrInvalidModels, "model id cannot be empty")
		}
	}
	return nil
}

func (msg *MsgRegisterWorker) GetSigners() []sdk.AccAddress {
	creator, _ := sdk.AccAddressFromBech32(msg.Creator)
	return []sdk.AccAddress{creator}
}

// -------- MsgExitWorker --------

type MsgExitWorker struct {
	Creator string `protobuf:"bytes,1,opt,name=creator,proto3" json:"creator"`
}

func NewMsgExitWorker(creator string) *MsgExitWorker {
	return &MsgExitWorker{Creator: creator}
}

func (msg *MsgExitWorker) ProtoMessage()  {}
func (msg *MsgExitWorker) Reset()         { *msg = MsgExitWorker{} }
func (msg *MsgExitWorker) String() string { return fmt.Sprintf("MsgExitWorker{%s}", msg.Creator) }

func (msg *MsgExitWorker) ValidateBasic() error {
	_, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return sdkerrors.Wrap(err, "invalid creator address")
	}
	return nil
}

func (msg *MsgExitWorker) GetSigners() []sdk.AccAddress {
	creator, _ := sdk.AccAddressFromBech32(msg.Creator)
	return []sdk.AccAddress{creator}
}

// -------- MsgUpdateModels --------

type MsgUpdateModels struct {
	Creator         string   `protobuf:"bytes,1,opt,name=creator,proto3" json:"creator"`
	SupportedModels []string `protobuf:"bytes,2,rep,name=supported_models,proto3" json:"supported_models"`
}

func NewMsgUpdateModels(creator string, models []string) *MsgUpdateModels {
	return &MsgUpdateModels{
		Creator:         creator,
		SupportedModels: models,
	}
}

func (msg *MsgUpdateModels) ProtoMessage()  {}
func (msg *MsgUpdateModels) Reset()         { *msg = MsgUpdateModels{} }
func (msg *MsgUpdateModels) String() string { return fmt.Sprintf("MsgUpdateModels{%s}", msg.Creator) }

func (msg *MsgUpdateModels) ValidateBasic() error {
	_, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return sdkerrors.Wrap(err, "invalid creator address")
	}
	if len(msg.SupportedModels) == 0 {
		return sdkerrors.Wrap(ErrInvalidModels, "at least one supported model is required")
	}
	for _, m := range msg.SupportedModels {
		if m == "" {
			return sdkerrors.Wrap(ErrInvalidModels, "model id cannot be empty")
		}
	}
	return nil
}

func (msg *MsgUpdateModels) GetSigners() []sdk.AccAddress {
	creator, _ := sdk.AccAddressFromBech32(msg.Creator)
	return []sdk.AccAddress{creator}
}

// -------- MsgStake --------

type MsgStake struct {
	Creator string   `protobuf:"bytes,1,opt,name=creator,proto3" json:"creator"`
	Amount  sdk.Coin `protobuf:"bytes,2,opt,name=amount,proto3" json:"amount"`
}

func NewMsgStake(creator string, amount sdk.Coin) *MsgStake {
	return &MsgStake{
		Creator: creator,
		Amount:  amount,
	}
}

func (msg *MsgStake) ProtoMessage()  {}
func (msg *MsgStake) Reset()         { *msg = MsgStake{} }
func (msg *MsgStake) String() string { return fmt.Sprintf("MsgStake{%s,%s}", msg.Creator, msg.Amount) }

func (msg *MsgStake) ValidateBasic() error {
	_, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return sdkerrors.Wrap(err, "invalid creator address")
	}
	if !msg.Amount.IsValid() || msg.Amount.IsZero() {
		return sdkerrors.Wrap(ErrInsufficientStake, "amount must be positive and valid")
	}
	return nil
}

func (msg *MsgStake) GetSigners() []sdk.AccAddress {
	creator, _ := sdk.AccAddressFromBech32(msg.Creator)
	return []sdk.AccAddress{creator}
}

// -------- MsgUnjail --------

type MsgUnjail struct {
	Creator string `protobuf:"bytes,1,opt,name=creator,proto3" json:"creator"`
}

func NewMsgUnjail(creator string) *MsgUnjail {
	return &MsgUnjail{Creator: creator}
}

func (msg *MsgUnjail) ProtoMessage()  {}
func (msg *MsgUnjail) Reset()         { *msg = MsgUnjail{} }
func (msg *MsgUnjail) String() string { return fmt.Sprintf("MsgUnjail{%s}", msg.Creator) }

func (msg *MsgUnjail) ValidateBasic() error {
	_, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return sdkerrors.Wrap(err, "invalid creator address")
	}
	return nil
}

func (msg *MsgUnjail) GetSigners() []sdk.AccAddress {
	creator, _ := sdk.AccAddressFromBech32(msg.Creator)
	return []sdk.AccAddress{creator}
}
