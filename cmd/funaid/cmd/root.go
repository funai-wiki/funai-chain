package cmd

import (
	"io"
	"os"

	"cosmossdk.io/log"
	cmtcfg "github.com/cometbft/cometbft/config"
	dbm "github.com/cosmos/cosmos-db"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/config"
	"github.com/cosmos/cosmos-sdk/client/keys"
	"github.com/cosmos/cosmos-sdk/server"
	serverconfig "github.com/cosmos/cosmos-sdk/server/config"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"

	"github.com/cosmos/cosmos-sdk/codec/address"
	authcli "github.com/cosmos/cosmos-sdk/x/auth/client/cli"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	bankcli "github.com/cosmos/cosmos-sdk/x/bank/client/cli"
	genutilcli "github.com/cosmos/cosmos-sdk/x/genutil/client/cli"
	evmserver "github.com/cosmos/evm/server"
	evmserverconfig "github.com/cosmos/evm/server/config"
	"github.com/spf13/cobra"

	funaiapp "github.com/funai-wiki/funai-chain/app"
	settlementcli "github.com/funai-wiki/funai-chain/x/settlement/client/cli"
	workercli "github.com/funai-wiki/funai-chain/x/worker/client/cli"
)

func NewRootCmd() *cobra.Command {
	encCfg := funaiapp.MakeEncodingConfig()

	initClientCtx := client.Context{}.
		WithCodec(encCfg.Codec).
		WithInterfaceRegistry(encCfg.InterfaceRegistry).
		WithTxConfig(encCfg.TxConfig).
		WithLegacyAmino(encCfg.Amino).
		WithInput(os.Stdin).
		WithAccountRetriever(authtypes.AccountRetriever{}).
		WithHomeDir(funaiapp.DefaultNodeHome).
		WithViper("")

	rootCmd := &cobra.Command{
		Use:   "funaid",
		Short: "FunAI Chain - Decentralized AI Inference Network",
		Long: `FunAI V5.2 - A decentralized AI inference network built on Cosmos SDK.
Chain is the bank, not the exchange. Inference is off-chain via libp2p.`,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			initClientCtx, err := client.ReadPersistentCommandFlags(initClientCtx, cmd.Flags())
			if err != nil {
				return err
			}
			initClientCtx, err = config.ReadFromClientConfig(initClientCtx)
			if err != nil {
				return err
			}
			if err := client.SetCmdClientContextHandler(initClientCtx, cmd); err != nil {
				return err
			}

			customAppTemplate, customAppConfig := initAppConfig()
			customCMTConfig := initCometBFTConfig()
			return server.InterceptConfigsPreRunHandler(cmd, customAppTemplate, customAppConfig, customCMTConfig)
		},
	}

	initRootCmd(rootCmd, encCfg)
	return rootCmd
}

func initRootCmd(rootCmd *cobra.Command, encCfg funaiapp.EncodingConfig) {
	// Query commands
	queryCmd := &cobra.Command{
		Use:                        "query",
		Aliases:                    []string{"q"},
		Short:                      "Querying subcommands",
		DisableFlagParsing:         false,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	queryCmd.AddCommand(
		authcli.QueryTxsByEventsCmd(),
		authcli.QueryTxCmd(),
		workercli.GetQueryCmd(),
		settlementcli.GetQueryCmd(),
	)

	// Transaction commands
	txCmd := &cobra.Command{
		Use:                        "tx",
		Short:                      "Transactions subcommands",
		DisableFlagParsing:         false,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	txCmd.AddCommand(
		bankcli.NewSendTxCmd(address.Bech32Codec{Bech32Prefix: funaiapp.Bech32PrefixAccAddr}),
		workercli.GetTxCmd(),
		settlementcli.GetTxCmd(),
	)

	rootCmd.AddCommand(
		genutilcli.InitCmd(funaiapp.ModuleBasics, funaiapp.DefaultNodeHome),
		genutilcli.Commands(encCfg.TxConfig, funaiapp.ModuleBasics, funaiapp.DefaultNodeHome),
		keys.Commands(),
		queryCmd,
		txCmd,
		StatusCmd(),
		VersionCmd(),
	)

	// Standard server commands (comet, export, rollback, etc.)
	server.AddCommands(rootCmd, funaiapp.DefaultNodeHome, newApp, exportApp, addModuleInitFlags)

	// Replace the standard start command with cosmos/evm's version that includes JSON-RPC
	evmStartOpts := evmserver.NewDefaultStartOptions(newApp, funaiapp.DefaultNodeHome)
	evmStartCmd := evmserver.StartCmd(evmStartOpts)
	addModuleInitFlags(evmStartCmd)
	replaceStartCommand(rootCmd, evmStartCmd)
}

func newApp(
	logger log.Logger,
	db dbm.DB,
	traceStore io.Writer,
	appOpts servertypes.AppOptions,
) servertypes.Application {
	baseAppOptions := server.DefaultBaseappOptions(appOpts)
	return funaiapp.NewFunAIApp(
		logger,
		db,
		traceStore,
		true,
		appOpts,
		baseAppOptions...,
	)
}

func exportApp(
	logger log.Logger,
	db dbm.DB,
	traceStore io.Writer,
	_ int64,
	_ bool,
	_ []string,
	appOpts servertypes.AppOptions,
	_ []string,
) (servertypes.ExportedApp, error) {
	app := funaiapp.NewFunAIApp(logger, db, traceStore, false, appOpts)
	return app.ExportAppStateAndValidators(false, nil, nil)
}

// replaceStartCommand replaces the standard SDK "start" command with cosmos/evm's version
// that includes JSON-RPC server for MetaMask/ethers.js compatibility.
func replaceStartCommand(rootCmd *cobra.Command, evmStartCmd *cobra.Command) {
	for i, cmd := range rootCmd.Commands() {
		if cmd.Name() == "start" {
			rootCmd.RemoveCommand(cmd)
			// Insert EVM start at same position
			_ = i
			rootCmd.AddCommand(evmStartCmd)
			return
		}
	}
	// If no "start" found, just add it
	rootCmd.AddCommand(evmStartCmd)
}

// unused but needed to suppress the evmserverconfig import
var _ = evmserverconfig.DefaultEVMConfig

func addModuleInitFlags(_ *cobra.Command) {}

func initAppConfig() (string, interface{}) {
	type CustomAppConfig struct {
		serverconfig.Config
	}
	srvCfg := serverconfig.DefaultConfig()
	// KT non-state-machine Issue G: non-zero default min gas price so a low-
	// cost mempool spammer cannot crowd out reserve / settlement / audit /
	// fraud-proof tx by submitting cheap small txs at high rate. 0.001 ufai
	// per gas unit × ~200_000 gas per typical tx ≈ 200 ufai per tx — high
	// enough to deter spam, low enough that legitimate user / worker /
	// proposer flow remains essentially free at testnet scale. Operators
	// can override via app.toml minimum-gas-prices in production.
	srvCfg.MinGasPrices = "0.001ufai"
	customAppConfig := CustomAppConfig{Config: *srvCfg}
	return serverconfig.DefaultConfigTemplate, customAppConfig
}

func initCometBFTConfig() *cmtcfg.Config {
	cfg := cmtcfg.DefaultConfig()
	cfg.Consensus.TimeoutCommit = 5_000_000_000 // 5 seconds
	return cfg
}

func StatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Query the status of the node",
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx := client.GetClientContextFromCmd(cmd)
			node, err := clientCtx.GetNode()
			if err != nil {
				return err
			}
			status, err := node.Status(cmd.Context())
			if err != nil {
				return err
			}
			cmd.Println(status)
			return nil
		},
	}
}

func VersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version of funaid",
		Run: func(cmd *cobra.Command, _ []string) {
			cmd.Println("FunAI Chain v5.2.0")
		},
	}
}

func init() {
	userHomeDir, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	funaiapp.DefaultNodeHome = userHomeDir + "/.funaid"
}
