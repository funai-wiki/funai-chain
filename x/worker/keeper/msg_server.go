package keeper

import (
	"context"
	"strings"

	sdkerrors "cosmossdk.io/errors"
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/worker/types"
)

type msgServer struct {
	Keeper
}

func NewMsgServerImpl(keeper Keeper) types.MsgServer {
	return &msgServer{Keeper: keeper}
}

var _ types.MsgServer = msgServer{}

func (m msgServer) RegisterWorker(goCtx context.Context, msg *types.MsgRegisterWorker) (*types.MsgRegisterWorkerResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)
	params := m.GetParams(ctx)

	creatorAddr, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return nil, sdkerrors.Wrap(err, "invalid creator address")
	}

	if _, found := m.GetWorker(ctx, creatorAddr); found {
		return nil, types.ErrWorkerAlreadyRegistered
	}

	currentHeight := ctx.BlockHeight()
	minStake := params.MinStake
	isColdStart := currentHeight <= params.ColdStartFreeBlocks

	initialStake := sdk.NewCoin(minStake.Denom, math.ZeroInt())

	if !isColdStart {
		stakeCoins := sdk.NewCoins(minStake)
		if err := m.bankKeeper.SendCoinsFromAccountToModule(ctx, creatorAddr, types.ModuleAccountName, stakeCoins); err != nil {
			return nil, sdkerrors.Wrap(types.ErrInsufficientStake, err.Error())
		}
		initialStake = minStake
	}

	// V6 / KT v2 §2.3: honour operator-declared batch capacity; default to 1
	// when omitted so a legacy registration keeps its busy/idle-equivalent
	// behaviour without any Worker-side change.
	maxConcurrentTasks := msg.MaxConcurrentTasks
	if maxConcurrentTasks == 0 {
		maxConcurrentTasks = 1
	}

	worker := types.Worker{
		Address:             msg.Creator,
		Pubkey:              msg.Pubkey,
		Stake:               initialStake,
		SupportedModels:     msg.SupportedModels,
		Status:              types.WorkerStatusActive,
		JoinedAt:            currentHeight,
		ExitRequestedAt:     0,
		Endpoint:            msg.Endpoint,
		GpuModel:            msg.GpuModel,
		GpuVramGb:           msg.GpuVramGb,
		GpuCount:            msg.GpuCount,
		OperatorId:          msg.OperatorId,
		JailCount:           0,
		Jailed:              false,
		JailUntil:           0,
		Tombstoned:          false,
		SuccessStreak:       0,
		TotalTasks:          0,
		TotalFeeEarned:      sdk.NewCoin(minStake.Denom, math.ZeroInt()),
		LastActiveBlock:     currentHeight,
		MaxConcurrentTasks:  maxConcurrentTasks,
		MaxConcurrentVerify: 2,
	}

	m.SetWorker(ctx, worker)
	m.SetModelIndices(ctx, creatorAddr, msg.SupportedModels)

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventWorkerRegistered,
		sdk.NewAttribute(types.AttributeKeyWorker, msg.Creator),
		sdk.NewAttribute(types.AttributeKeyModels, strings.Join(msg.SupportedModels, ",")),
		sdk.NewAttribute(types.AttributeKeyStake, initialStake.String()),
	))

	return &types.MsgRegisterWorkerResponse{}, nil
}

func (m msgServer) ExitWorker(goCtx context.Context, msg *types.MsgExitWorker) (*types.MsgExitWorkerResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	creatorAddr, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return nil, sdkerrors.Wrap(err, "invalid creator address")
	}

	worker, found := m.GetWorker(ctx, creatorAddr)
	if !found {
		return nil, types.ErrWorkerNotFound
	}

	if worker.Jailed {
		return nil, types.ErrWorkerJailed
	}
	if worker.Tombstoned {
		return nil, types.ErrWorkerTombstoned
	}
	if worker.Status != types.WorkerStatusActive {
		return nil, types.ErrWorkerNotActive
	}

	worker.Status = types.WorkerStatusExiting
	worker.ExitRequestedAt = ctx.BlockHeight()
	m.SetWorker(ctx, worker)

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventWorkerExited,
		sdk.NewAttribute(types.AttributeKeyWorker, msg.Creator),
		sdk.NewAttribute(types.AttributeKeyStatus, types.WorkerStatusExiting.String()),
	))

	return &types.MsgExitWorkerResponse{}, nil
}

func (m msgServer) UpdateModels(goCtx context.Context, msg *types.MsgUpdateModels) (*types.MsgUpdateModelsResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	creatorAddr, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return nil, sdkerrors.Wrap(err, "invalid creator address")
	}

	worker, found := m.GetWorker(ctx, creatorAddr)
	if !found {
		return nil, types.ErrWorkerNotFound
	}

	if !worker.IsActive() {
		return nil, types.ErrWorkerNotActive
	}

	m.RemoveModelIndices(ctx, creatorAddr, worker.SupportedModels)
	worker.SupportedModels = msg.SupportedModels
	m.SetWorker(ctx, worker)
	m.SetModelIndices(ctx, creatorAddr, msg.SupportedModels)

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventModelsUpdated,
		sdk.NewAttribute(types.AttributeKeyWorker, msg.Creator),
		sdk.NewAttribute(types.AttributeKeyModels, strings.Join(msg.SupportedModels, ",")),
	))

	return &types.MsgUpdateModelsResponse{}, nil
}

func (m msgServer) AddStake(goCtx context.Context, msg *types.MsgStake) (*types.MsgStakeResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	creatorAddr, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return nil, sdkerrors.Wrap(err, "invalid creator address")
	}

	worker, found := m.GetWorker(ctx, creatorAddr)
	if !found {
		return nil, types.ErrWorkerNotFound
	}

	if worker.Tombstoned {
		return nil, types.ErrWorkerTombstoned
	}

	coins := sdk.NewCoins(msg.Amount)
	if err := m.bankKeeper.SendCoinsFromAccountToModule(ctx, creatorAddr, types.ModuleAccountName, coins); err != nil {
		return nil, sdkerrors.Wrap(types.ErrInsufficientStake, err.Error())
	}

	worker.Stake = worker.Stake.Add(msg.Amount)
	m.SetWorker(ctx, worker)

	if m.modelRegKeeper != nil {
		m.modelRegKeeper.OnWorkerStateChange(ctx, creatorAddr)
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventStakeAdded,
		sdk.NewAttribute(types.AttributeKeyWorker, msg.Creator),
		sdk.NewAttribute(types.AttributeKeyAmount, msg.Amount.String()),
		sdk.NewAttribute(types.AttributeKeyStake, worker.Stake.String()),
	))

	return &types.MsgStakeResponse{}, nil
}

func (m msgServer) Unjail(goCtx context.Context, msg *types.MsgUnjail) (*types.MsgUnjailResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	creatorAddr, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return nil, sdkerrors.Wrap(err, "invalid creator address")
	}

	if err := m.UnjailWorker(ctx, creatorAddr); err != nil {
		return nil, err
	}

	return &types.MsgUnjailResponse{}, nil
}
