package vrf

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

	"github.com/funai-wiki/funai-chain/x/vrf/keeper"
	"github.com/funai-wiki/funai-chain/x/vrf/types"
)

var (
	_ module.AppModuleBasic     = AppModuleBasic{}
	_ module.HasGenesis         = AppModule{}
	_ module.HasServices        = AppModule{}
	_ appmodule.AppModule       = AppModule{}
	_ appmodule.HasBeginBlocker = AppModule{}
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
		&types.MsgSubmitVRFProof{},
		&types.MsgLeaderHeartbeat{},
		&types.MsgReportLeaderTimeout{},
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

// WorkerProvider is a callback interface that supplies online worker data.
// This decouples the VRF module from the worker module.
type WorkerProvider struct {
	GetActiveModelIds       func(ctx sdk.Context) []string
	GetOnlineWorkersByModel func(ctx sdk.Context) map[string][]string
	GetAllEligibleWorkers   func(ctx sdk.Context) []string
}

type AppModule struct {
	AppModuleBasic
	keeper         keeper.Keeper
	workerProvider *WorkerProvider
}

func NewAppModule(
	cdc codec.Codec,
	keeper keeper.Keeper,
	workerProvider *WorkerProvider,
) AppModule {
	return AppModule{
		AppModuleBasic: NewAppModuleBasic(cdc),
		keeper:         keeper,
		workerProvider: workerProvider,
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

// BeginBlock handles leader/committee rotation at the beginning of each block.
func (am AppModule) BeginBlock(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)

	if am.workerProvider == nil {
		return nil
	}

	if am.workerProvider.GetActiveModelIds != nil && am.workerProvider.GetOnlineWorkersByModel != nil {
		activeModels := am.workerProvider.GetActiveModelIds(sdkCtx)
		onlineWorkers := am.workerProvider.GetOnlineWorkersByModel(sdkCtx)
		am.keeper.CheckLeaderTimeouts(sdkCtx, activeModels, onlineWorkers)
	}

	if am.workerProvider.GetAllEligibleWorkers != nil {
		eligibleWorkers := am.workerProvider.GetAllEligibleWorkers(sdkCtx)
		am.keeper.HandleCommitteeRotation(sdkCtx, eligibleWorkers)
	}

	return nil
}

func (AppModule) ConsensusVersion() uint64 { return 1 }
