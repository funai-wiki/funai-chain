package keeper

import (
	"context"
	"encoding/hex"
	"strconv"

	sdkerrors "cosmossdk.io/errors"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/settlement/types"
)

type msgServer struct {
	Keeper
}

func NewMsgServerImpl(keeper Keeper) types.MsgServer {
	return &msgServer{Keeper: keeper}
}

var _ types.MsgServer = msgServer{}

func (m msgServer) Deposit(goCtx context.Context, msg *types.MsgDeposit) (*types.MsgDepositResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	creatorAddr, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return nil, sdkerrors.Wrap(err, "invalid creator address")
	}

	if err := m.ProcessDeposit(ctx, creatorAddr, msg.Amount); err != nil {
		return nil, err
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventDeposit,
		sdk.NewAttribute(types.AttributeKeyUser, msg.Creator),
		sdk.NewAttribute(types.AttributeKeyAmount, msg.Amount.String()),
	))

	return &types.MsgDepositResponse{}, nil
}

func (m msgServer) Withdraw(goCtx context.Context, msg *types.MsgWithdraw) (*types.MsgWithdrawResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	creatorAddr, err := sdk.AccAddressFromBech32(msg.Creator)
	if err != nil {
		return nil, sdkerrors.Wrap(err, "invalid creator address")
	}

	if err := m.ProcessWithdraw(ctx, creatorAddr, msg.Amount); err != nil {
		return nil, err
	}

	ia, _ := m.GetInferenceAccount(ctx, creatorAddr)

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventWithdraw,
		sdk.NewAttribute(types.AttributeKeyUser, msg.Creator),
		sdk.NewAttribute(types.AttributeKeyAmount, msg.Amount.String()),
		sdk.NewAttribute(types.AttributeKeyBalance, ia.Balance.String()),
	))

	return &types.MsgWithdrawResponse{}, nil
}

func (m msgServer) BatchSettle(goCtx context.Context, msg *types.MsgBatchSettlement) (*types.MsgBatchSettlementResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	batchId, err := m.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		return nil, err
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventBatchSettlement,
		sdk.NewAttribute(types.AttributeKeyProposer, msg.Proposer),
		sdk.NewAttribute(types.AttributeKeyBatchId, strconv.FormatUint(batchId, 10)),
		sdk.NewAttribute(types.AttributeKeyResultCount, strconv.Itoa(len(msg.Entries))),
	))

	return &types.MsgBatchSettlementResponse{BatchId: batchId}, nil
}

func (m msgServer) SubmitFraudProof(goCtx context.Context, msg *types.MsgFraudProof) (*types.MsgFraudProofResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	if err := m.ProcessFraudProof(ctx, msg); err != nil {
		return nil, err
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventFraudProof,
		sdk.NewAttribute(types.AttributeKeyReporter, msg.Reporter),
		sdk.NewAttribute(types.AttributeKeyTaskId, hex.EncodeToString(msg.TaskId)),
		sdk.NewAttribute(types.AttributeKeyWorker, msg.WorkerAddress),
	))

	return &types.MsgFraudProofResponse{}, nil
}

func (m msgServer) SubmitSecondVerificationResult(goCtx context.Context, msg *types.MsgSecondVerificationResult) (*types.MsgSecondVerificationResultResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	if err := m.ProcessSecondVerificationResult(ctx, msg); err != nil {
		return nil, err
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventSecondVerificationResult,
		sdk.NewAttribute(types.AttributeKeySecondVerifier, msg.SecondVerifier),
		sdk.NewAttribute(types.AttributeKeyTaskId, hex.EncodeToString(msg.TaskId)),
		sdk.NewAttribute(types.AttributeKeyEpoch, strconv.FormatInt(msg.Epoch, 10)),
		sdk.NewAttribute(types.AttributeKeyPass, strconv.FormatBool(msg.Pass)),
	))

	return &types.MsgSecondVerificationResultResponse{}, nil
}
