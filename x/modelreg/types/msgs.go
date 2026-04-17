package types

import (
	"fmt"

	sdkerrors "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
)

func init() {
	proto.RegisterType((*MsgModelProposal)(nil), "funai.modelreg.MsgModelProposal")
	proto.RegisterType((*MsgUpdateModelStats)(nil), "funai.modelreg.MsgUpdateModelStats")
	proto.RegisterType((*MsgDeclareInstalled)(nil), "funai.modelreg.MsgDeclareInstalled")
}

var (
	_ sdk.Msg = &MsgModelProposal{}
	_ sdk.Msg = &MsgUpdateModelStats{}
	_ sdk.Msg = &MsgDeclareInstalled{}
)

// -------- MsgModelProposal --------

type MsgModelProposal struct {
	Creator          string   `protobuf:"bytes,1,opt,name=creator,proto3" json:"creator"`
	Name             string   `protobuf:"bytes,2,opt,name=name,proto3" json:"name"`
	Alias            string   `protobuf:"bytes,8,opt,name=alias,proto3" json:"alias"`
	WeightHash       string   `protobuf:"bytes,3,opt,name=weight_hash,proto3" json:"weight_hash"`
	QuantConfigHash  string   `protobuf:"bytes,4,opt,name=quant_config_hash,proto3" json:"quant_config_hash"`
	RuntimeImageHash string   `protobuf:"bytes,5,opt,name=runtime_image_hash,proto3" json:"runtime_image_hash"`
	Epsilon          uint32   `protobuf:"varint,6,opt,name=epsilon,proto3" json:"epsilon"`
	SuggestedPrice   sdk.Coin `protobuf:"bytes,7,opt,name=suggested_price,proto3" json:"suggested_price"`
}

func NewMsgModelProposal(creator, name, alias, weightHash, quantConfigHash, runtimeImageHash string, epsilon uint32, suggestedPrice sdk.Coin) *MsgModelProposal {
	return &MsgModelProposal{
		Creator:          creator,
		Name:             name,
		Alias:            alias,
		WeightHash:       weightHash,
		QuantConfigHash:  quantConfigHash,
		RuntimeImageHash: runtimeImageHash,
		Epsilon:          epsilon,
		SuggestedPrice:   suggestedPrice,
	}
}

func (msg *MsgModelProposal) ProtoMessage() {}
func (msg *MsgModelProposal) Reset()        { *msg = MsgModelProposal{} }
func (msg *MsgModelProposal) String() string {
	return fmt.Sprintf("MsgModelProposal{%s, %s}", msg.Creator, msg.Name)
}

func (msg *MsgModelProposal) ValidateBasic() error {
	_, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return sdkerrors.Wrap(err, "invalid creator address")
	}
	if msg.Name == "" {
		return sdkerrors.Wrap(ErrInvalidModelId, "name cannot be empty")
	}
	if msg.Alias == "" {
		return sdkerrors.Wrap(ErrInvalidAlias, "alias cannot be empty")
	}
	if err := ValidateAlias(msg.Alias); err != nil {
		return sdkerrors.Wrap(ErrInvalidAlias, err.Error())
	}
	if msg.WeightHash == "" {
		return sdkerrors.Wrap(ErrInvalidModelId, "weight_hash cannot be empty")
	}
	if msg.QuantConfigHash == "" {
		return sdkerrors.Wrap(ErrInvalidModelId, "quant_config_hash cannot be empty")
	}
	if msg.RuntimeImageHash == "" {
		return sdkerrors.Wrap(ErrInvalidModelId, "runtime_image_hash cannot be empty")
	}
	// V5.2 §5.1: ε is a fixed-point integer ×10000. Range [1, 1000] → [0.0001, 0.1].
	if msg.Epsilon < 1 {
		return sdkerrors.Wrap(ErrInvalidEpsilon, "epsilon must be >= 1 (representing min 0.0001)")
	}
	if msg.Epsilon > 1000 {
		return sdkerrors.Wrapf(ErrInvalidEpsilon, "epsilon must be <= 1000 (representing max 0.1), got %d", msg.Epsilon)
	}
	return nil
}

func (msg *MsgModelProposal) GetSigners() []sdk.AccAddress {
	creator, _ := sdk.AccAddressFromBech32(msg.Creator)
	return []sdk.AccAddress{creator}
}

// -------- MsgUpdateModelStats --------

type MsgUpdateModelStats struct {
	Authority           string  `protobuf:"bytes,1,opt,name=authority,proto3" json:"authority"`
	ModelId             string  `protobuf:"bytes,2,opt,name=model_id,proto3" json:"model_id"`
	InstalledStakeRatio float64 `protobuf:"fixed64,3,opt,name=installed_stake_ratio,proto3" json:"installed_stake_ratio"`
	WorkerCount         uint32  `protobuf:"varint,4,opt,name=worker_count,proto3" json:"worker_count"`
	OperatorCount       uint32  `protobuf:"varint,5,opt,name=operator_count,proto3" json:"operator_count"`
}

func NewMsgUpdateModelStats(authority, modelId string, installedStakeRatio float64, workerCount, operatorCount uint32) *MsgUpdateModelStats {
	return &MsgUpdateModelStats{
		Authority:           authority,
		ModelId:             modelId,
		InstalledStakeRatio: installedStakeRatio,
		WorkerCount:         workerCount,
		OperatorCount:       operatorCount,
	}
}

func (msg *MsgUpdateModelStats) ProtoMessage() {}
func (msg *MsgUpdateModelStats) Reset()        { *msg = MsgUpdateModelStats{} }
func (msg *MsgUpdateModelStats) String() string {
	return fmt.Sprintf("MsgUpdateModelStats{%s, %s}", msg.Authority, msg.ModelId)
}

func (msg *MsgUpdateModelStats) ValidateBasic() error {
	_, err := sdk.AccAddressFromBech32(msg.Authority)
	if err != nil {
		return sdkerrors.Wrap(err, "invalid authority address")
	}
	if msg.ModelId == "" {
		return sdkerrors.Wrap(ErrInvalidModelId, "model_id cannot be empty")
	}
	if msg.InstalledStakeRatio < 0 || msg.InstalledStakeRatio > 1 {
		return sdkerrors.Wrapf(ErrInvalidModelId, "installed_stake_ratio must be in [0, 1], got %f", msg.InstalledStakeRatio)
	}
	return nil
}

func (msg *MsgUpdateModelStats) GetSigners() []sdk.AccAddress {
	authority, _ := sdk.AccAddressFromBech32(msg.Authority)
	return []sdk.AccAddress{authority}
}

// -------- MsgDeclareInstalled --------

type MsgDeclareInstalled struct {
	Creator string `protobuf:"bytes,1,opt,name=creator,proto3" json:"creator"`
	ModelId string `protobuf:"bytes,2,opt,name=model_id,proto3" json:"model_id"`
}

func NewMsgDeclareInstalled(creator, modelId string) *MsgDeclareInstalled {
	return &MsgDeclareInstalled{Creator: creator, ModelId: modelId}
}

func (msg *MsgDeclareInstalled) ProtoMessage() {}
func (msg *MsgDeclareInstalled) Reset()        { *msg = MsgDeclareInstalled{} }
func (msg *MsgDeclareInstalled) String() string {
	return fmt.Sprintf("MsgDeclareInstalled{%s,%s}", msg.Creator, msg.ModelId)
}

func (msg *MsgDeclareInstalled) ValidateBasic() error {
	if _, err := sdk.AccAddressFromBech32(msg.Creator); err != nil {
		return sdkerrors.Wrap(err, "invalid creator address")
	}
	if msg.ModelId == "" {
		return sdkerrors.Wrap(ErrModelNotFound, "model_id cannot be empty")
	}
	return nil
}

func (msg *MsgDeclareInstalled) GetSigners() []sdk.AccAddress {
	creator, _ := sdk.AccAddressFromBech32(msg.Creator)
	return []sdk.AccAddress{creator}
}
