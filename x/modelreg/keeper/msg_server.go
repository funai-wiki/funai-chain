package keeper

import (
	"context"
	"fmt"

	sdkerrors "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/modelreg/types"
)

type msgServer struct {
	Keeper
}

// NewMsgServerImpl returns an implementation of the modelreg MsgServer interface.
func NewMsgServerImpl(keeper Keeper) types.MsgServer {
	return &msgServer{Keeper: keeper}
}

var _ types.MsgServer = msgServer{}

func (m msgServer) ProposeModel(goCtx context.Context, msg *types.MsgModelProposal) (*types.MsgModelProposalResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	_, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return nil, sdkerrors.Wrap(err, "invalid creator address")
	}

	modelId := ComputeModelId(msg.WeightHash, msg.QuantConfigHash, msg.RuntimeImageHash)

	if _, found := m.GetModel(ctx, modelId); found {
		return nil, types.ErrModelAlreadyExists
	}

	if m.HasAlias(ctx, msg.Alias) {
		return nil, types.ErrAliasAlreadyTaken
	}

	model := types.Model{
		ModelId:             modelId,
		Name:                msg.Name,
		Alias:               msg.Alias,
		Epsilon:             msg.Epsilon,
		Status:              types.ModelStatusProposed,
		ProposerAddress:     msg.Creator,
		WeightHash:          msg.WeightHash,
		QuantConfigHash:     msg.QuantConfigHash,
		RuntimeImageHash:    msg.RuntimeImageHash,
		InstalledStakeRatio: 0,
		WorkerCount:         0,
		OperatorCount:       0,
		SuggestedPrice:      msg.SuggestedPrice,
		ActivatedAt:         0,
		CreatedAt:           ctx.BlockHeight(),
	}

	m.SetModel(ctx, model)
	m.SetAliasIndex(ctx, msg.Alias, modelId)

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventModelProposed,
		sdk.NewAttribute(types.AttributeKeyModelId, modelId),
		sdk.NewAttribute(types.AttributeKeyModelName, msg.Name),
		sdk.NewAttribute(types.AttributeKeyProposer, msg.Creator),
		sdk.NewAttribute(types.AttributeKeyEpsilon, fmt.Sprintf("%d", msg.Epsilon)),
	))

	return &types.MsgModelProposalResponse{ModelId: modelId}, nil
}

func (m msgServer) UpdateModelStats(goCtx context.Context, msg *types.MsgUpdateModelStats) (*types.MsgUpdateModelStatsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	// KT Issue 16: gate by governance authority. Pre-fix the handler did not
	// validate msg.Authority at all — any account with a valid bech32 address
	// could call MsgUpdateModelStats and overwrite InstalledStakeRatio /
	// WorkerCount / OperatorCount, then trigger CheckAndActivateModel with
	// the manipulated values. Effect: anyone could activate or deactivate
	// arbitrary models. The Authority field was already in the msg shape
	// (only ValidateBasic'd for bech32 syntax), so the gate goes here.
	if m.GetAuthority() != "" && msg.Authority != m.GetAuthority() {
		return nil, sdkerrors.Wrapf(types.ErrUnauthorized,
			"invalid authority: expected %s, got %s", m.GetAuthority(), msg.Authority)
	}

	model, found := m.GetModel(ctx, msg.ModelId)
	if !found {
		return nil, types.ErrModelNotFound
	}

	params := m.GetParams(ctx)
	previousCanServe := model.CanServe(params.MinServiceWorkerCount, params.ServiceStakeRatio)

	model.InstalledStakeRatio = msg.InstalledStakeRatio
	model.WorkerCount = msg.WorkerCount
	model.OperatorCount = msg.OperatorCount

	m.SetModel(ctx, model)

	m.CheckAndActivateModel(ctx, msg.ModelId)

	m.CheckServiceStatus(ctx, model, previousCanServe)

	return &types.MsgUpdateModelStatsResponse{}, nil
}

func (m msgServer) DeclareInstalled(goCtx context.Context, msg *types.MsgDeclareInstalled) (*types.MsgDeclareInstalledResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	_, found := m.GetModel(ctx, msg.ModelId)
	if !found {
		return nil, types.ErrModelNotFound
	}

	creatorAddr, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return nil, sdkerrors.Wrap(err, "invalid creator address")
	}

	if m.workerKeeper != nil && !m.workerKeeper.IsWorkerActive(ctx, creatorAddr) {
		return nil, sdkerrors.Wrap(types.ErrModelNotFound, "creator is not an active worker")
	}

	// Trust-boundary check (audit §7): worker can only declare installation of
	// a model it actually registered in SupportedModels. Without this, a
	// worker could DeclareInstalled on any model_id in the registry — that
	// inflates InstalledStakeRatio / WorkerCount / OperatorCount and biases
	// model activation thresholds + the VRF serving set, even though the
	// worker has no obligation to actually serve.
	if m.workerKeeper != nil && !m.workerKeeper.WorkerSupportsModel(ctx, creatorAddr, msg.ModelId) {
		return nil, sdkerrors.Wrap(types.ErrModelNotFound,
			"creator did not declare model_id in SupportedModels — register the model in MsgRegisterWorker first")
	}

	// Idempotency: if already installed, return success without re-scanning stats
	if m.HasWorkerInstalledModel(ctx, creatorAddr, msg.ModelId) {
		return &types.MsgDeclareInstalledResponse{}, nil
	}

	// Save reverse index: worker → model_id
	m.SetWorkerInstalledModel(ctx, creatorAddr, msg.ModelId)

	// Refresh model stats (InstalledStakeRatio, WorkerCount, OperatorCount)
	m.RefreshModelStats(ctx, msg.ModelId)

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventModelInstalled,
		sdk.NewAttribute(types.AttributeKeyModelId, msg.ModelId),
		sdk.NewAttribute(types.AttributeKeyWorker, msg.Creator),
	))

	return &types.MsgDeclareInstalledResponse{}, nil
}
