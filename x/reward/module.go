package reward

import (
	"context"
	"encoding/json"
	"fmt"

	"cosmossdk.io/core/appmodule"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	cdctypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"

	"github.com/funai-wiki/funai-chain/x/reward/keeper"
	"github.com/funai-wiki/funai-chain/x/reward/types"
)

var (
	_ module.AppModuleBasic   = AppModuleBasic{}
	_ module.HasGenesis       = AppModule{}
	_ module.HasServices      = AppModule{}
	_ appmodule.AppModule     = AppModule{}
	_ appmodule.HasEndBlocker = AppModule{}
)

type AppModuleBasic struct {
	cdc codec.Codec
}

func NewAppModuleBasic(cdc codec.Codec) AppModuleBasic {
	return AppModuleBasic{cdc: cdc}
}

func (AppModuleBasic) Name() string { return types.ModuleName }

func (AppModuleBasic) RegisterLegacyAminoCodec(_ *codec.LegacyAmino) {}

func (AppModuleBasic) RegisterInterfaces(registry cdctypes.InterfaceRegistry) {
	registry.RegisterImplementations((*sdk.Msg)(nil),
		&types.MsgUpdateParams{},
	)
}

func (AppModuleBasic) RegisterGRPCGatewayRoutes(_ client.Context, _ *runtime.ServeMux) {}

func (AppModuleBasic) DefaultGenesis(_ codec.JSONCodec) json.RawMessage {
	gs := types.DefaultGenesis()
	bz, err := json.Marshal(gs)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal default genesis: %v", err))
	}
	return bz
}

func (AppModuleBasic) ValidateGenesis(_ codec.JSONCodec, _ client.TxEncodingConfig, bz json.RawMessage) error {
	var gs types.GenesisState
	if err := json.Unmarshal(bz, &gs); err != nil {
		return fmt.Errorf("failed to unmarshal %s genesis state: %w", types.ModuleName, err)
	}
	return gs.Validate()
}

type AppModule struct {
	AppModuleBasic
	keeper                keeper.Keeper
	getContribFn          func(ctx sdk.Context) []types.WorkerContribution
	getVerifContribFn     func(ctx sdk.Context) []types.VerificationContribution
	getConsensusSignersFn func(ctx sdk.Context) []types.ConsensusSignerInfo
	getOnlineWorkersFn    func(ctx sdk.Context) []types.OnlineWorkerStake
}

func NewAppModule(
	cdc codec.Codec,
	keeper keeper.Keeper,
	getContribFn func(ctx sdk.Context) []types.WorkerContribution,
	getVerifContribFn func(ctx sdk.Context) []types.VerificationContribution,
	getConsensusSignersFn func(ctx sdk.Context) []types.ConsensusSignerInfo,
	getOnlineWorkersFn func(ctx sdk.Context) []types.OnlineWorkerStake,
) AppModule {
	return AppModule{
		AppModuleBasic:        NewAppModuleBasic(cdc),
		keeper:                keeper,
		getContribFn:          getContribFn,
		getVerifContribFn:     getVerifContribFn,
		getConsensusSignersFn: getConsensusSignersFn,
		getOnlineWorkersFn:    getOnlineWorkersFn,
	}
}

func (AppModule) IsOnePerModuleType() {}
func (AppModule) IsAppModule()        {}

func (am AppModule) RegisterServices(cfg module.Configurator) {
	types.RegisterMsgServer(cfg, keeper.NewMsgServerImpl(am.keeper))
	types.RegisterQueryServer(cfg, am.keeper)
}

func (am AppModule) InitGenesis(ctx sdk.Context, _ codec.JSONCodec, data json.RawMessage) {
	var gs types.GenesisState
	if err := json.Unmarshal(data, &gs); err != nil {
		panic(fmt.Sprintf("failed to unmarshal %s genesis: %v", types.ModuleName, err))
	}
	am.keeper.InitGenesis(ctx, gs)
}

func (am AppModule) ExportGenesis(ctx sdk.Context, _ codec.JSONCodec) json.RawMessage {
	gs := am.keeper.ExportGenesis(ctx)
	bz, err := json.Marshal(gs)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal %s genesis: %v", types.ModuleName, err))
	}
	return bz
}

// EndBlock distributes rewards at epoch boundaries (every EpochBlocks blocks).
func (am AppModule) EndBlock(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	logger := am.keeper.Logger(sdkCtx)

	params := am.keeper.GetParams(sdkCtx)
	height := sdkCtx.BlockHeight()

	if height%params.EpochBlocks != 0 {
		return nil
	}
	logger.Info("reward EndBlock: epoch boundary", "height", height, "epoch", height/params.EpochBlocks)

	var contributions []types.WorkerContribution
	if am.getContribFn != nil {
		contributions = am.getContribFn(sdkCtx)
	}

	var verifContribs []types.VerificationContribution
	if am.getVerifContribFn != nil {
		verifContribs = am.getVerifContribFn(sdkCtx)
	}

	var consensusSigners []types.ConsensusSignerInfo
	if am.getConsensusSignersFn != nil {
		consensusSigners = am.getConsensusSignersFn(sdkCtx)
	}

	var onlineWorkers []types.OnlineWorkerStake
	if am.getOnlineWorkersFn != nil {
		onlineWorkers = am.getOnlineWorkersFn(sdkCtx)
	}

	if err := am.keeper.DistributeRewards(sdkCtx, contributions, verifContribs, consensusSigners, onlineWorkers); err != nil {
		logger.Error("failed to distribute epoch rewards", "error", err, "height", height)
		return err
	}

	return nil
}

func (AppModule) ConsensusVersion() uint64 { return 2 }
