package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/vrf/types"
)

type msgServer struct {
	Keeper
}

func NewMsgServerImpl(keeper Keeper) types.MsgServer {
	return &msgServer{Keeper: keeper}
}

var _ types.MsgServer = msgServer{}

func (m msgServer) SubmitVRFProof(goCtx context.Context, msg *types.MsgSubmitVRFProof) (*types.MsgSubmitVRFProofResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	// Audit §4 — trust-boundary gate. Pre-fix any address could submit any
	// `Value` and pollute the chain's VRF seed unchecked: ValidateVRFProof is
	// a stub (`|| len(value) > 0` makes the boolean expression always true on
	// non-empty input), and the handler doesn't even invoke it. Until a real
	// VRF protocol lands (separate spec — likely RFC 9381 ECVRF + a defined
	// committee-of-signers participation rule), restrict submission to the
	// chain authority (governance / module address). Anything else cannot
	// touch the seed.
	//
	// This is a Phase-1 lockdown: necessary because a polluted VRF seed
	// invalidates every "VRF top-K" decision downstream — dispatch, verifier
	// selection, leader election, committee rotation. Phase-2 will restore
	// permissionless submission gated by per-message proof verification.
	if msg.Creator != m.GetAuthority() {
		return nil, types.ErrUnauthorized.Wrapf(
			"SubmitVRFProof: only chain authority %q may update VRF seed (got %q); permissionless submission requires the not-yet-implemented per-proof VRF verification",
			m.GetAuthority(), msg.Creator,
		)
	}

	m.UpdateSeed(ctx, [][]byte{msg.Value})

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeVRFProofSubmitted,
		sdk.NewAttribute(types.AttributeKeyProofSubmitter, msg.Creator),
	))

	return &types.MsgSubmitVRFProofResponse{}, nil
}

func (m msgServer) LeaderHeartbeat(goCtx context.Context, msg *types.MsgLeaderHeartbeat) (*types.MsgLeaderHeartbeatResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	if err := m.UpdateLeaderHeartbeat(ctx, msg.ModelId, msg.Creator); err != nil {
		return nil, err
	}

	return &types.MsgLeaderHeartbeatResponse{}, nil
}

func (m msgServer) ReportLeaderTimeout(goCtx context.Context, msg *types.MsgReportLeaderTimeout) (*types.MsgReportLeaderTimeoutResponse, error) {
	ctx := sdk.UnwrapSDKContext(goCtx)

	// Audit §5 — trust-boundary gate. Pre-fix the handler only counted
	// `len(TimeoutProofs)` against a threshold; it never inspected the proof
	// bytes (signature, signer membership, dedup, binding to
	// model_id/leader/timeout_height). Any address could submit N garbage
	// proofs and trigger the leader-rotation event — polluting liveness
	// state.
	//
	// `CheckLeaderTimeouts` already runs every block in BeginBlock
	// (x/vrf/module.go:130) and handles legitimate timeout-driven rotation
	// without needing a Msg. This handler is therefore redundant for normal
	// operation and is locked to the chain authority until per-proof
	// signature + committee-membership verification lands as a separate
	// spec.
	if msg.Creator != m.GetAuthority() {
		return nil, types.ErrUnauthorized.Wrapf(
			"ReportLeaderTimeout: only chain authority %q may submit (got %q); BeginBlock CheckLeaderTimeouts handles automatic rotation, this Msg is for governance override only",
			m.GetAuthority(), msg.Creator,
		)
	}

	params := m.GetParams(ctx)
	requiredProofs := int(params.TimeoutProofPercent) * int(params.CommitteeSize) / 100
	if len(msg.TimeoutProofs) < requiredProofs {
		return nil, types.ErrInsufficientProofs.Wrapf(
			"need %d proofs, got %d", requiredProofs, len(msg.TimeoutProofs))
	}

	leader, found := m.GetLeaderInfo(ctx, msg.ModelId)
	if !found {
		return nil, types.ErrLeaderNotFound
	}

	if ctx.BlockHeight()-leader.LastHeartbeat <= params.LeaderTimeoutBlocks {
		return nil, types.ErrLeaderTimeout.Wrap("leader has not timed out")
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeReElection,
		sdk.NewAttribute(types.AttributeKeyModelId, msg.ModelId),
		sdk.NewAttribute(types.AttributeKeyLeaderAddress, leader.Address),
	))

	return &types.MsgReportLeaderTimeoutResponse{
		NewLeader: "",
	}, nil
}
