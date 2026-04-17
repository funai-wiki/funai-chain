package cli

import (
	"context"
	"encoding/json"
	"fmt"

	rpchttp "github.com/cometbft/cometbft/rpc/client/http"
	"github.com/cosmos/cosmos-sdk/client/flags"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/cobra"

	"github.com/funai-wiki/funai-chain/x/worker/types"
)

func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Worker query commands",
		DisableFlagParsing:         false,
		SuggestionsMinimumDistance: 2,
		// Override root's PersistentPreRunE to avoid WebSocket client creation hang
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	}

	cmd.AddCommand(CmdQueryParams())
	cmd.AddCommand(CmdQueryWorkers())
	cmd.AddCommand(CmdQueryWorker())

	return cmd
}

func CmdQueryParams() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "params",
		Short: "Query worker module parameters",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeURI, _ := cmd.Flags().GetString("node")

			bz, err := abciQuery(nodeURI, types.ParamsKey, types.StoreKey)
			if err != nil {
				return fmt.Errorf("failed to query params: %w", err)
			}
			if len(bz) == 0 {
				return fmt.Errorf("worker params not found")
			}

			var params types.Params
			if err := json.Unmarshal(bz, &params); err != nil {
				return fmt.Errorf("failed to unmarshal params: %w", err)
			}

			out, _ := json.MarshalIndent(params, "", "  ")
			cmd.Println(string(out))
			return nil
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func CmdQueryWorkers() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all registered workers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.Println("Use 'funaid query worker show [address]' to query a specific worker.")
			cmd.Println("Workers are stored on-chain and can be queried via gRPC when enabled.")
			return nil
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func CmdQueryWorker() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show [address]",
		Short: "Query a worker by address",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			nodeURI, _ := cmd.Flags().GetString("node")

			addr, err := sdk.AccAddressFromBech32(args[0])
			if err != nil {
				return fmt.Errorf("invalid address: %w", err)
			}

			key := append(types.WorkerKeyPrefix, addr...)
			bz, err := abciQuery(nodeURI, key, types.StoreKey)
			if err != nil {
				return fmt.Errorf("failed to query worker: %w", err)
			}
			if len(bz) == 0 {
				return fmt.Errorf("worker not found: %s", args[0])
			}

			var w types.Worker
			if err := json.Unmarshal(bz, &w); err != nil {
				return fmt.Errorf("failed to unmarshal worker: %w", err)
			}

			out, _ := json.MarshalIndent(w, "", "  ")
			cmd.Println(string(out))
			return nil
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

// abciQuery creates an HTTP RPC client and performs an ABCI store query.
func abciQuery(nodeURI string, key []byte, storeName string) ([]byte, error) {
	if nodeURI == "" {
		nodeURI = "tcp://localhost:26657"
	}

	httpClient, err := rpchttp.New(nodeURI, "")
	if err != nil {
		return nil, fmt.Errorf("failed to create RPC client: %w", err)
	}

	path := fmt.Sprintf("/store/%s/key", storeName)
	resp, err := httpClient.ABCIQuery(context.Background(), path, key)
	if err != nil {
		return nil, fmt.Errorf("ABCI query failed: %w", err)
	}

	return resp.Response.Value, nil
}
