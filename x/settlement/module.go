package settlement

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"

	"cosmossdk.io/core/appmodule"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/spf13/cobra"

	"github.com/funai-wiki/funai-chain/x/settlement/client/cli"
	"github.com/funai-wiki/funai-chain/x/settlement/keeper"
	"github.com/funai-wiki/funai-chain/x/settlement/types"
)

var (
	_ module.AppModuleBasic     = AppModuleBasic{}
	_ module.AppModule          = AppModule{}
	_ module.HasABCIEndBlock    = AppModule{}
	_ appmodule.HasBeginBlocker = AppModule{} // P1-10: accumulate block signers
)

// -------- AppModuleBasic --------

type AppModuleBasic struct{}

func (AppModuleBasic) Name() string {
	return types.ModuleName
}

func (AppModuleBasic) RegisterLegacyAminoCodec(_ *codec.LegacyAmino) {}

func (AppModuleBasic) RegisterInterfaces(registry cdctypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil),
		&types.MsgDeposit{},
		&types.MsgWithdraw{},
		&types.MsgBatchSettlement{},
		&types.MsgFraudProof{},
		&types.MsgSecondVerificationResult{},
	)
}

func (AppModuleBasic) RegisterGRPCGatewayRoutes(_ client.Context, _ *runtime.ServeMux) {}

func (AppModuleBasic) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	gs := types.DefaultGenesis()
	bz, _ := json.Marshal(gs)
	return bz
}

func (AppModuleBasic) ValidateGenesis(_ codec.JSONCodec, _ client.TxEncodingConfig, bz json.RawMessage) error {
	var gs types.GenesisState
	if err := json.Unmarshal(bz, &gs); err != nil {
		return fmt.Errorf("failed to unmarshal %s genesis state: %w", types.ModuleName, err)
	}
	return gs.Validate()
}

func (AppModuleBasic) GetTxCmd() *cobra.Command {
	return cli.GetTxCmd()
}

func (AppModuleBasic) GetQueryCmd() *cobra.Command {
	return cli.GetQueryCmd()
}

// -------- AppModule --------

type AppModule struct {
	AppModuleBasic
	keeper keeper.Keeper
}

func NewAppModule(k keeper.Keeper) AppModule {
	return AppModule{
		AppModuleBasic: AppModuleBasic{},
		keeper:         k,
	}
}

func (am AppModule) RegisterServices(cfg module.Configurator) {
	types.RegisterMsgServer(cfg, keeper.NewMsgServerImpl(am.keeper))
	types.RegisterQueryServer(cfg, keeper.NewQueryServerImpl(am.keeper))
}

func (am AppModule) InitGenesis(ctx sdk.Context, cdc codec.JSONCodec, data json.RawMessage) {
	var gs types.GenesisState
	if err := json.Unmarshal(data, &gs); err != nil {
		panic(fmt.Sprintf("failed to unmarshal %s genesis state: %s", types.ModuleName, err))
	}
	am.keeper.InitGenesis(ctx, gs)
}

func (am AppModule) ExportGenesis(ctx sdk.Context, _ codec.JSONCodec) json.RawMessage {
	gs := am.keeper.ExportGenesis(ctx)
	bz, _ := json.Marshal(gs)
	return bz
}

func (AppModule) ConsensusVersion() uint64 { return 2 }

func (am AppModule) IsOnePerModuleType() {}
func (am AppModule) IsAppModule()        {}

// BeginBlock accumulates block signer counts for consensus reward distribution.
// P1-10: tracks which validators signed each block during the epoch.
func (am AppModule) BeginBlock(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	am.keeper.AccumulateBlockSigners(sdkCtx)
	return nil
}

// EndBlock runs the settlement EndBlocker:
// 1. Cleanup expired task_id records
// 2. Handle audit/third_verification timeouts (V5.2)
// 3. Recalculate audit_rate / third_verification_rate at epoch boundaries (V5.2)
func (am AppModule) EndBlock(ctx context.Context) ([]abci.ValidatorUpdate, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	// 1. Cleanup expired tasks
	cleaned := am.keeper.CleanupExpiredTasks(sdkCtx)
	if cleaned > 0 {
		sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
			types.EventTaskCleanup,
			sdk.NewAttribute(types.AttributeKeyCleanedTasks, strconv.Itoa(cleaned)),
		))
	}

	// 2. Handle audit/third_verification timeouts
	timedOut := am.keeper.HandleSecondVerificationTimeouts(sdkCtx)
	if timedOut > 0 {
		sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
			"second_verification_timeout",
			sdk.NewAttribute("timed_out_count", strconv.Itoa(timedOut)),
		))
	}

	// 2b. S9 §4.4: handle per-token frozen balance timeouts
	frozenTimedOut := am.keeper.HandleFrozenBalanceTimeouts(sdkCtx)
	if frozenTimedOut > 0 {
		sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
			"frozen_balance_timeout",
			sdk.NewAttribute("timed_out_count", strconv.Itoa(frozenTimedOut)),
		))
	}

	// 3. Recalculate audit rates and distribute audit fund at epoch boundaries (every 100 blocks)
	height := sdkCtx.BlockHeight()
	epochBlocks := int64(100)
	if height%epochBlocks == 0 {
		prevEpoch := height/epochBlocks - 1
		if prevEpoch >= 0 {
			newSecondVerificationRate := am.keeper.CalculateSecondVerificationRate(sdkCtx, prevEpoch)
			am.keeper.SetCurrentSecondVerificationRate(sdkCtx, newSecondVerificationRate)

			newThirdVerificationRate := am.keeper.CalculateThirdVerificationRate(sdkCtx, prevEpoch)
			am.keeper.SetCurrentThirdVerificationRate(sdkCtx, newThirdVerificationRate)

			// M9: distribute audit fund to second_verifiers for the previous epoch
			am.keeper.DistributeMultiVerificationFund(sdkCtx, prevEpoch)

			sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
				"audit_rate_updated",
				sdk.NewAttribute("audit_rate", fmt.Sprintf("%d", newSecondVerificationRate)),
				sdk.NewAttribute("third_verification_rate", fmt.Sprintf("%d", newThirdVerificationRate)),
				sdk.NewAttribute("epoch", fmt.Sprintf("%d", height/epochBlocks)),
			))
		}
	}

	return nil, nil
}
