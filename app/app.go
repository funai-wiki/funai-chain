package app

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	dbm "github.com/cosmos/cosmos-db"

	abci "github.com/cometbft/cometbft/abci/types"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/grpc/cmtservice"
	nodeservice "github.com/cosmos/cosmos-sdk/client/grpc/node"
	"github.com/cosmos/cosmos-sdk/codec"
	"github.com/cosmos/cosmos-sdk/codec/address"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/server"
	"github.com/cosmos/cosmos-sdk/server/api"
	serverconfig "github.com/cosmos/cosmos-sdk/server/config"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	"github.com/cosmos/cosmos-sdk/x/auth"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/bank"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/cosmos-sdk/x/consensus"
	consensuskeeper "github.com/cosmos/cosmos-sdk/x/consensus/keeper"
	"github.com/cosmos/cosmos-sdk/x/genutil"
	genutiltypes "github.com/cosmos/cosmos-sdk/x/genutil/types"
	"github.com/cosmos/cosmos-sdk/x/staking"
	stakingkeeper "github.com/cosmos/cosmos-sdk/x/staking/keeper"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
	"github.com/cosmos/gogoproto/grpc"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"

	// Cosmos EVM modules
	feemarketmod "github.com/cosmos/evm/x/feemarket"
	feemarketkeeper "github.com/cosmos/evm/x/feemarket/keeper"
	feemarkettypes "github.com/cosmos/evm/x/feemarket/types"
	precisebankmod "github.com/cosmos/evm/x/precisebank"
	precisebankkeeper "github.com/cosmos/evm/x/precisebank/keeper"
	precisebanktypes "github.com/cosmos/evm/x/precisebank/types"
	evmmod "github.com/cosmos/evm/x/vm"
	evmkeeper "github.com/cosmos/evm/x/vm/keeper"
	evmtypes "github.com/cosmos/evm/x/vm/types"

	modelregmod "github.com/funai-wiki/funai-chain/x/modelreg"
	modelregkeeper "github.com/funai-wiki/funai-chain/x/modelreg/keeper"
	modelregtypes "github.com/funai-wiki/funai-chain/x/modelreg/types"
	rewardmod "github.com/funai-wiki/funai-chain/x/reward"
	rewardkeeper "github.com/funai-wiki/funai-chain/x/reward/keeper"
	rewardtypes "github.com/funai-wiki/funai-chain/x/reward/types"
	settlementmod "github.com/funai-wiki/funai-chain/x/settlement"
	settlementkeeper "github.com/funai-wiki/funai-chain/x/settlement/keeper"
	settlementtypes "github.com/funai-wiki/funai-chain/x/settlement/types"
	vrfmod "github.com/funai-wiki/funai-chain/x/vrf"
	vrfkeeper "github.com/funai-wiki/funai-chain/x/vrf/keeper"
	vrftypes "github.com/funai-wiki/funai-chain/x/vrf/types"
	workermod "github.com/funai-wiki/funai-chain/x/worker"
	workerkeeper "github.com/funai-wiki/funai-chain/x/worker/keeper"
	workertypes "github.com/funai-wiki/funai-chain/x/worker/types"
)

const (
	AppName        = "FunAIChain"
	DefaultChainID = "funai_123123123-3" // mainnet; override with FUNAI_CHAIN_ID env for testnet
)

// noopErc20Keeper is a no-op implementation of evmtypes.Erc20Keeper.
// Returns "not found" for all precompile lookups. Safe to pass to EVM keeper
// when ERC20 precompiles are not needed.
type noopErc20Keeper struct{}

func (n *noopErc20Keeper) GetERC20PrecompileInstance(_ sdk.Context, _ common.Address) (vm.PrecompiledContract, bool, error) {
	return nil, false, nil
}

type stakingModuleBasic struct {
	staking.AppModuleBasic
}

func (s stakingModuleBasic) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	genState := stakingtypes.DefaultGenesisState()
	genState.Params.BondDenom = BondDenom
	genState.Params.UnbondingTime = 21 * 24 * time.Hour
	genState.Params.MaxValidators = 100
	genState.Params.MinCommissionRate = math.LegacyZeroDec()
	return cdc.MustMarshalJSON(genState)
}

var (
	DefaultNodeHome string

	ModuleBasics = module.NewBasicManager(
		auth.AppModuleBasic{},
		genutil.NewAppModuleBasic(genutiltypes.DefaultMessageValidator),
		bank.AppModuleBasic{},
		stakingModuleBasic{},
		consensus.AppModuleBasic{},
		workermod.AppModuleBasic{},
		modelregmod.AppModuleBasic{},
		settlementmod.AppModuleBasic{},
		rewardmod.NewAppModuleBasic(nil),
		vrfmod.NewAppModuleBasic(nil),
		evmmod.AppModuleBasic{},
		feemarketmod.AppModuleBasic{},
	)

	maccPerms = map[string][]string{
		authtypes.FeeCollectorName:        nil,
		stakingtypes.BondedPoolName:       {authtypes.Burner, authtypes.Staking},
		stakingtypes.NotBondedPoolName:    {authtypes.Burner, authtypes.Staking},
		workertypes.ModuleName:            {authtypes.Burner},
		settlementtypes.ModuleAccountName: nil,
		rewardtypes.ModuleName:            {authtypes.Minter, authtypes.Burner},
		evmtypes.ModuleName:               {authtypes.Minter, authtypes.Burner},
		precisebanktypes.ModuleName:       {authtypes.Minter, authtypes.Burner},
	}
)

type FunAIApp struct {
	*baseapp.BaseApp

	legacyAmino       *codec.LegacyAmino
	appCodec          codec.Codec
	txConfig          client.TxConfig
	interfaceRegistry codectypes.InterfaceRegistry

	keys    map[string]*storetypes.KVStoreKey
	tkeys   map[string]*storetypes.TransientStoreKey
	memKeys map[string]*storetypes.MemoryStoreKey

	// Cosmos built-in keepers
	AccountKeeper   authkeeper.AccountKeeper
	BankKeeper      bankkeeper.Keeper
	StakingKeeper   *stakingkeeper.Keeper
	ConsensusKeeper consensuskeeper.Keeper

	// FunAI custom keepers (V5.2: 5 modules)
	WorkerKeeper     workerkeeper.Keeper
	ModelRegKeeper   modelregkeeper.Keeper
	SettlementKeeper settlementkeeper.Keeper
	RewardKeeper     rewardkeeper.Keeper
	VRFKeeper        vrfkeeper.Keeper

	// EVM keepers
	EvmKeeper         *evmkeeper.Keeper
	FeeMarketKeeper   feemarketkeeper.Keeper
	PreciseBankKeeper precisebankkeeper.Keeper

	ModuleManager *module.Manager
}

func init() {
	// Configure EVM chain config before app startup.
	// This sets the global chainConfig used by JSON-RPC handlers.
	// Configure EVM with 18 decimals — ufai maps directly to EVM wei.
	// This means 1 ufai = 1 wei in EVM. Cosmos bank balance is directly usable as EVM balance.
	chainID := DefaultChainID
	if env := os.Getenv("FUNAI_CHAIN_ID"); env != "" {
		chainID = env
	}
	configurator := evmtypes.NewEVMConfigurator()
	if err := configurator.
		WithChainConfig(evmtypes.DefaultChainConfig(chainID)).
		WithEVMCoinInfo(evmtypes.EvmCoinInfo{
			Denom:         BondDenom, // "ufai" — same for both Cosmos and EVM
			ExtendedDenom: BondDenom, // same denom (18 decimals requires denom == extendedDenom)
			Decimals:      evmtypes.EighteenDecimals,
		}).
		Configure(); err != nil {
		panic(fmt.Sprintf("failed to configure EVM: %v", err))
	}
}

func NewFunAIApp(
	logger log.Logger,
	db dbm.DB,
	traceStore io.Writer,
	loadLatest bool,
	appOpts servertypes.AppOptions,
	baseAppOptions ...func(*baseapp.BaseApp),
) *FunAIApp {
	encCfg := MakeEncodingConfig()
	interfaceRegistry := encCfg.InterfaceRegistry
	appCodec := encCfg.Codec.(*codec.ProtoCodec)
	legacyAmino := encCfg.Amino
	txCfg := encCfg.TxConfig

	bApp := baseapp.NewBaseApp(AppName, logger, db, txCfg.TxDecoder(), baseAppOptions...)
	bApp.SetInterfaceRegistry(interfaceRegistry)

	keys := storetypes.NewKVStoreKeys(
		authtypes.StoreKey,
		banktypes.StoreKey,
		stakingtypes.StoreKey,
		"consensus",
		workertypes.StoreKey,
		modelregtypes.StoreKey,
		settlementtypes.StoreKey,
		rewardtypes.StoreKey,
		vrftypes.StoreKey,
		evmtypes.StoreKey,
		feemarkettypes.StoreKey,
		precisebanktypes.StoreKey,
	)
	tkeys := storetypes.NewTransientStoreKeys(evmtypes.TransientKey, feemarkettypes.TransientKey)
	memKeys := storetypes.NewMemoryStoreKeys()

	app := &FunAIApp{
		BaseApp:           bApp,
		legacyAmino:       legacyAmino,
		appCodec:          appCodec,
		txConfig:          txCfg,
		interfaceRegistry: interfaceRegistry,
		keys:              keys,
		tkeys:             tkeys,
		memKeys:           memKeys,
	}

	addressCodec := address.Bech32Codec{Bech32Prefix: Bech32PrefixAccAddr}
	valAddressCodec := address.Bech32Codec{Bech32Prefix: Bech32PrefixValAddr}

	// ---------- Cosmos built-in keepers ----------
	app.AccountKeeper = authkeeper.NewAccountKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[authtypes.StoreKey]),
		authtypes.ProtoBaseAccount,
		maccPerms,
		addressCodec,
		Bech32PrefixAccAddr,
		authtypes.NewModuleAddress("gov").String(),
	)

	app.BankKeeper = bankkeeper.NewBaseKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[banktypes.StoreKey]),
		app.AccountKeeper,
		blockedAddresses(),
		authtypes.NewModuleAddress("gov").String(),
		logger,
	)

	app.StakingKeeper = stakingkeeper.NewKeeper(
		appCodec,
		runtime.NewKVStoreService(keys[stakingtypes.StoreKey]),
		app.AccountKeeper,
		app.BankKeeper,
		authtypes.NewModuleAddress("gov").String(),
		valAddressCodec,
		addressCodec,
	)

	app.ConsensusKeeper = consensuskeeper.NewKeeper(
		appCodec,
		runtime.NewKVStoreService(keys["consensus"]),
		authtypes.NewModuleAddress("gov").String(),
		runtime.EventService{},
	)

	// ---------- FunAI custom keepers (V5.2) ----------
	app.WorkerKeeper = workerkeeper.NewKeeper(
		appCodec,
		keys[workertypes.StoreKey],
		app.BankKeeper,
		logger,
	)

	app.ModelRegKeeper = modelregkeeper.NewKeeper(
		appCodec,
		keys[modelregtypes.StoreKey],
		app.WorkerKeeper,
		logger,
	)

	// Wire cross-module callback: worker state changes notify modelreg to refresh model stats.
	// Must be done after both keepers are created to avoid circular init.
	app.WorkerKeeper.SetModelRegKeeper(app.ModelRegKeeper)

	// V5.2: no DA layer, settlement keeper takes no DAClient
	app.SettlementKeeper = settlementkeeper.NewKeeper(
		appCodec,
		keys[settlementtypes.StoreKey],
		app.BankKeeper,
		app.WorkerKeeper,
		authtypes.NewModuleAddress("gov").String(),
		logger,
	)
	app.SettlementKeeper.SetModelRegKeeper(app.ModelRegKeeper)

	app.RewardKeeper = rewardkeeper.NewKeeper(
		appCodec,
		keys[rewardtypes.StoreKey],
		app.BankKeeper,
		app.AccountKeeper,
		authtypes.NewModuleAddress("gov").String(),
	)

	app.VRFKeeper = vrfkeeper.NewKeeper(
		appCodec,
		keys[vrftypes.StoreKey],
		app.StakingKeeper,
		authtypes.NewModuleAddress("gov").String(),
	)

	// Worker provider callbacks for VRF module
	workerProvider := &vrfmod.WorkerProvider{
		GetActiveModelIds: func(ctx sdk.Context) []string {
			workers := app.WorkerKeeper.GetAllWorkers(ctx)
			modelSet := make(map[string]bool)
			for _, w := range workers {
				if w.IsActive() {
					for _, m := range w.SupportedModels {
						modelSet[m] = true
					}
				}
			}
			models := make([]string, 0, len(modelSet))
			for m := range modelSet {
				models = append(models, m)
			}
			return models
		},
		GetOnlineWorkersByModel: func(ctx sdk.Context) map[string][]string {
			workers := app.WorkerKeeper.GetAllWorkers(ctx)
			result := make(map[string][]string)
			for _, w := range workers {
				if w.IsActive() {
					for _, m := range w.SupportedModels {
						result[m] = append(result[m], w.Address)
					}
				}
			}
			return result
		},
		GetAllEligibleWorkers: func(ctx sdk.Context) []string {
			workers := app.WorkerKeeper.GetAllWorkers(ctx)
			var addrs []string
			for _, w := range workers {
				if w.IsActive() {
					addrs = append(addrs, w.Address)
					// Sync worker pubkey+stake to VRF store so SelectCommittee can rank them
					pubkey, _ := hex.DecodeString(w.Pubkey)
					if len(pubkey) > 0 {
						app.VRFKeeper.SetWorkerStatus(ctx, vrftypes.WorkerStatus{
							Address: w.Address,
							Pubkey:  pubkey,
							Stake:   w.Stake.Amount,
						})
					}
				}
			}
			return addrs
		},
	}

	// P1-8: Wire inference contribution callback using per-epoch deltas (not cumulative).
	// At epoch boundary, snapshot worker stats and compute epoch contributions.
	getContribFn := func(ctx sdk.Context) []rewardtypes.WorkerContribution {
		height := ctx.BlockHeight()
		epoch := height / 100
		prevEpoch := epoch - 1
		if prevEpoch < 0 {
			return nil
		}
		stats := app.SettlementKeeper.GetEpochStats(ctx, prevEpoch)
		if stats.TotalSettled == 0 {
			return nil
		}

		// Snapshot current worker stats and compute per-epoch deltas
		workers := app.WorkerKeeper.GetAllWorkers(ctx)
		var workerInfos []settlementkeeper.WorkerStatsInfo
		for _, w := range workers {
			if w.IsActive() {
				workerInfos = append(workerInfos, settlementkeeper.WorkerStatsInfo{
					Address:        w.Address,
					TotalFeeEarned: w.TotalFeeEarned.Amount,
					TotalTasks:     int64(w.TotalTasks),
				})
			}
		}
		app.SettlementKeeper.SnapshotAndComputeEpochContributions(ctx, workerInfos)

		// Return per-epoch contributions
		epochContribs := app.SettlementKeeper.GetAllEpochContributions(ctx)
		var contributions []rewardtypes.WorkerContribution
		for _, ec := range epochContribs {
			contributions = append(contributions, rewardtypes.WorkerContribution{
				WorkerAddress: ec.WorkerAddress,
				FeeAmount:     ec.FeeAmount,
				TaskCount:     ec.TaskCount,
			})
		}
		return contributions
	}

	// P1-9: Wire verification/audit contribution callback using per-worker tracking.
	getVerifContribFn := func(ctx sdk.Context) []rewardtypes.VerificationContribution {
		height := ctx.BlockHeight()
		epoch := height / 100
		prevEpoch := epoch - 1
		if prevEpoch < 0 {
			return nil
		}
		stats := app.SettlementKeeper.GetEpochStats(ctx, prevEpoch)
		if stats.VerificationCount == 0 && stats.SecondVerificationTotal == 0 {
			return nil
		}

		// Return actual per-worker verification/2nd-3rd-verification counts and fees.
		// Fees are used for the 85% amount-weighted portion of the 12% verifier reward pool.
		counts := app.SettlementKeeper.GetAllVerifierSecondVerifierEpochCounts(ctx)
		var contribs []rewardtypes.VerificationContribution
		for _, c := range counts {
			contribs = append(contribs, rewardtypes.VerificationContribution{
				WorkerAddress:     c.Address,
				VerificationCount: c.VerificationCount,
				AuditCount:        c.AuditCount,
				FeeAmount:         c.TotalFee(),
			})
		}

		// Clear counts for next epoch
		app.SettlementKeeper.ClearVerifierSecondVerifierEpochCounts(ctx)

		return contribs
	}

	// P1-10: Wire consensus signer callback.
	// Consensus signing counts are accumulated in BeginBlocker via AccumulateBlockSigners.
	// The callback returns the accumulated counts and clears them for the next epoch.
	getConsensusSignersFn := func(ctx sdk.Context) []rewardtypes.ConsensusSignerInfo {
		counts := app.SettlementKeeper.GetAndClearBlockSignerCounts(ctx)
		// Sort by address for deterministic iteration (consensus safety)
		addrs := make([]string, 0, len(counts))
		for addr := range counts {
			addrs = append(addrs, addr)
		}
		sort.Strings(addrs)
		signers := make([]rewardtypes.ConsensusSignerInfo, 0, len(addrs))
		for _, addr := range addrs {
			signers = append(signers, rewardtypes.ConsensusSignerInfo{
				ValidatorAddress: addr,
				BlocksSigned:     counts[addr],
			})
		}
		return signers
	}

	getOnlineWorkersFn := func(ctx sdk.Context) []rewardtypes.OnlineWorkerStake {
		workers := app.WorkerKeeper.GetAllWorkers(ctx)
		var result []rewardtypes.OnlineWorkerStake
		for _, w := range workers {
			if w.IsActive() {
				result = append(result, rewardtypes.OnlineWorkerStake{
					WorkerAddress: w.Address,
					Stake:         w.Stake.Amount,
				})
			}
		}
		return result
	}

	// ---------- EVM keepers ----------
	app.FeeMarketKeeper = feemarketkeeper.NewKeeper(
		appCodec,
		authtypes.NewModuleAddress("gov"),
		keys[feemarkettypes.StoreKey],
		tkeys[feemarkettypes.TransientKey],
	)

	// PreciseBank bridges Cosmos 6-decimal coins to EVM 18-decimal wei
	app.PreciseBankKeeper = precisebankkeeper.NewKeeper(
		appCodec,
		keys[precisebanktypes.StoreKey],
		app.BankKeeper,
		app.AccountKeeper,
	)

	app.EvmKeeper = evmkeeper.NewKeeper(
		appCodec,
		keys[evmtypes.StoreKey],
		tkeys[evmtypes.TransientKey],
		authtypes.NewModuleAddress("gov"),
		app.AccountKeeper,
		app.PreciseBankKeeper, // Use PreciseBank as bank keeper for EVM (handles 6→18 decimal bridging)
		app.StakingKeeper,
		app.FeeMarketKeeper,
		&noopErc20Keeper{}, // no ERC20 precompiles yet
		"",                 // tracer
	)

	// ---------- Module Manager ----------
	app.ModuleManager = module.NewManager(
		auth.NewAppModule(appCodec, app.AccountKeeper, nil, nil),
		genutil.NewAppModule(app.AccountKeeper, app.StakingKeeper, app, txCfg),
		bank.NewAppModule(appCodec, app.BankKeeper, app.AccountKeeper, nil),
		staking.NewAppModule(appCodec, app.StakingKeeper, app.AccountKeeper, app.BankKeeper, nil),
		consensus.NewAppModule(appCodec, app.ConsensusKeeper),
		workermod.NewAppModule(app.WorkerKeeper, app.VRFKeeper),
		modelregmod.NewAppModule(app.ModelRegKeeper),
		settlementmod.NewAppModule(app.SettlementKeeper),
		rewardmod.NewAppModule(appCodec, app.RewardKeeper, getContribFn, getVerifContribFn, getConsensusSignersFn, getOnlineWorkersFn),
		vrfmod.NewAppModule(appCodec, app.VRFKeeper, workerProvider),
		evmmod.NewAppModule(app.EvmKeeper, app.AccountKeeper),
		feemarketmod.NewAppModule(app.FeeMarketKeeper),
		precisebankmod.NewAppModule(app.PreciseBankKeeper, app.BankKeeper, app.AccountKeeper),
	)

	app.ModuleManager.SetOrderBeginBlockers(
		precisebanktypes.ModuleName,
		feemarkettypes.ModuleName,
		evmtypes.ModuleName,
		stakingtypes.ModuleName,
		authtypes.ModuleName,
		banktypes.ModuleName,
		genutiltypes.ModuleName,
		"consensus",
		vrftypes.ModuleName,
		workertypes.ModuleName,
		modelregtypes.ModuleName,
		settlementtypes.ModuleName,
		rewardtypes.ModuleName,
	)

	app.ModuleManager.SetOrderEndBlockers(
		evmtypes.ModuleName,
		feemarkettypes.ModuleName,
		precisebanktypes.ModuleName,
		stakingtypes.ModuleName,
		authtypes.ModuleName,
		banktypes.ModuleName,
		genutiltypes.ModuleName,
		"consensus",
		workertypes.ModuleName,
		settlementtypes.ModuleName,
		modelregtypes.ModuleName,
		rewardtypes.ModuleName,
		vrftypes.ModuleName,
	)

	app.ModuleManager.SetOrderInitGenesis(
		authtypes.ModuleName,
		banktypes.ModuleName,
		stakingtypes.ModuleName,
		genutiltypes.ModuleName,
		"consensus",
		workertypes.ModuleName,
		modelregtypes.ModuleName,
		settlementtypes.ModuleName,
		rewardtypes.ModuleName,
		vrftypes.ModuleName,
		precisebanktypes.ModuleName,
		feemarkettypes.ModuleName,
		evmtypes.ModuleName,
	)

	bApp.SetParamStore(app.ConsensusKeeper.ParamsStore)
	bApp.SetInitChainer(app.InitChainer)
	bApp.SetBeginBlocker(app.BeginBlocker)
	bApp.SetEndBlocker(app.EndBlocker)

	// Register services for ALL modules (message handlers + query handlers).
	// Custom modules register synthetic file descriptors via init() so the
	// HybridResolver can find their service/method descriptors.
	configurator := module.NewConfigurator(appCodec, bApp.MsgServiceRouter(), bApp.GRPCQueryRouter())
	if err := app.ModuleManager.RegisterServices(configurator); err != nil {
		panic(fmt.Sprintf("failed to register module services: %v", err))
	}

	for _, key := range keys {
		bApp.MountStore(key, storetypes.StoreTypeIAVL)
	}
	for _, key := range tkeys {
		bApp.MountStore(key, storetypes.StoreTypeTransient)
	}
	for _, key := range memKeys {
		bApp.MountStore(key, storetypes.StoreTypeMemory)
	}

	if loadLatest {
		if err := bApp.LoadLatestVersion(); err != nil {
			panic(err)
		}
	}

	return app
}

func (app *FunAIApp) Name() string { return AppName }

func (app *FunAIApp) BeginBlocker(ctx sdk.Context) (sdk.BeginBlock, error) {
	return app.ModuleManager.BeginBlock(ctx)
}

func (app *FunAIApp) EndBlocker(ctx sdk.Context) (sdk.EndBlock, error) {
	endBlock, err := app.ModuleManager.EndBlock(ctx)
	// FunAI uses its own worker/VRF system for validator management.
	// Clear CometBFT validator updates from staking module to avoid
	// pubkey format conflicts (staking returns secp256k1, CometBFT expects ed25519).
	endBlock.ValidatorUpdates = nil
	return endBlock, err
}

func (app *FunAIApp) InitChainer(ctx sdk.Context, req *abci.RequestInitChain) (*abci.ResponseInitChain, error) {
	var genesisState map[string]json.RawMessage
	if err := json.Unmarshal(req.AppStateBytes, &genesisState); err != nil {
		return nil, err
	}
	return app.ModuleManager.InitGenesis(ctx, app.appCodec, genesisState)
}

func (app *FunAIApp) RegisterAPIRoutes(apiSvr *api.Server, _ serverconfig.APIConfig) {
	clientCtx := apiSvr.ClientCtx
	authtypes.RegisterInterfaces(clientCtx.InterfaceRegistry)
	banktypes.RegisterInterfaces(clientCtx.InterfaceRegistry)

	// Register gRPC-gateway routes for tx, CometBFT queries, node info, and all modules.
	// Required for REST API / block explorer (Ping.pub) support.
	authtx.RegisterGRPCGatewayRoutes(clientCtx, apiSvr.GRPCGatewayRouter)
	cmtservice.RegisterGRPCGatewayRoutes(clientCtx, apiSvr.GRPCGatewayRouter)
	nodeservice.RegisterGRPCGatewayRoutes(clientCtx, apiSvr.GRPCGatewayRouter)
	ModuleBasics.RegisterGRPCGatewayRoutes(clientCtx, apiSvr.GRPCGatewayRouter)

	// Register custom REST routes for FunAI modules (settlement, worker, etc.).
	// These modules use synthetic proto descriptors and can't use standard grpc-gateway.
	RegisterCustomRESTRoutes(apiSvr, app)
}

func (app *FunAIApp) RegisterGRPCServer(server grpc.Server) {
	// Delegate to BaseApp which registers GRPCQueryRouter services on the external gRPC server.
	// This enables grpcurl, Ping.pub, and REST API gateway to query module services.
	app.BaseApp.RegisterGRPCServer(server)
}

func (app *FunAIApp) RegisterTxService(clientCtx client.Context) {
	authtx.RegisterTxService(app.GRPCQueryRouter(), clientCtx, app.Simulate, app.interfaceRegistry)
}

func (app *FunAIApp) RegisterTendermintService(clientCtx client.Context) {
	cmtApp := server.NewCometABCIWrapper(app)
	cmtservice.RegisterTendermintService(
		clientCtx,
		app.GRPCQueryRouter(),
		app.interfaceRegistry,
		cmtApp.Query,
	)
}

func (app *FunAIApp) RegisterNodeService(clientCtx client.Context, cfg serverconfig.Config) {
	nodeservice.RegisterNodeService(clientCtx, app.GRPCQueryRouter(), cfg)
}

func (app *FunAIApp) LegacyAmino() *codec.LegacyAmino                 { return app.legacyAmino }
func (app *FunAIApp) AppCodec() codec.Codec                           { return app.appCodec }
func (app *FunAIApp) InterfaceRegistry() codectypes.InterfaceRegistry { return app.interfaceRegistry }
func (app *FunAIApp) TxConfig() client.TxConfig                       { return app.txConfig }

func (app *FunAIApp) DefaultGenesis() map[string]json.RawMessage {
	return ModuleBasics.DefaultGenesis(app.appCodec)
}

func (app *FunAIApp) ExportAppStateAndValidators(forZeroHeight bool, jailAllowedAddrs []string, modulesToExport []string) (servertypes.ExportedApp, error) {
	ctx := app.NewContext(true)
	genState, err := app.ModuleManager.ExportGenesis(ctx, app.appCodec)
	if err != nil {
		return servertypes.ExportedApp{}, err
	}
	appState, err := json.MarshalIndent(genState, "", "  ")
	if err != nil {
		return servertypes.ExportedApp{}, err
	}
	return servertypes.ExportedApp{
		AppState: appState,
	}, nil
}

func (app *FunAIApp) SimulationManager() *module.SimulationManager { return nil }

func blockedAddresses() map[string]bool {
	blocked := make(map[string]bool)
	for acc := range maccPerms {
		blocked[authtypes.NewModuleAddress(acc).String()] = true
	}
	return blocked
}
