package cli

import (
	"strconv"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/spf13/cobra"

	"github.com/funai-wiki/funai-chain/x/worker/types"
)

func GetTxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:                        types.ModuleName,
		Short:                      "Worker transaction commands",
		DisableFlagParsing:         false,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}

	cmd.AddCommand(CmdRegister())
	cmd.AddCommand(CmdExit())
	cmd.AddCommand(CmdUnjail())
	cmd.AddCommand(CmdStake())
	cmd.AddCommand(CmdUpdateModels())

	return cmd
}

func CmdRegister() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "register",
		Short: "Register as a worker node",
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			pubkey, _ := cmd.Flags().GetString("pubkey")
			models, _ := cmd.Flags().GetStringSlice("models")
			endpoint, _ := cmd.Flags().GetString("endpoint")
			gpuModel, _ := cmd.Flags().GetString("gpu-model")
			gpuVram, _ := cmd.Flags().GetUint32("gpu-vram")
			gpuCount, _ := cmd.Flags().GetUint32("gpu-count")
			operatorId, _ := cmd.Flags().GetString("operator-id")
			maxConcurrent, _ := cmd.Flags().GetUint32("max-concurrent-tasks")

			msg := types.NewMsgRegisterWorker(
				clientCtx.GetFromAddress().String(),
				pubkey,
				models,
				endpoint,
				gpuModel,
				gpuVram,
				gpuCount,
				operatorId,
				maxConcurrent,
			)
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}

	cmd.Flags().String("pubkey", "", "Worker public key")
	cmd.Flags().StringSlice("models", nil, "Supported model IDs (comma-separated)")
	cmd.Flags().String("endpoint", "", "P2P endpoint address")
	cmd.Flags().String("gpu-model", "", "GPU model name (e.g. H100)")
	cmd.Flags().Uint32("gpu-vram", 0, "GPU VRAM in GB")
	cmd.Flags().Uint32("gpu-count", 1, "Number of GPUs")
	cmd.Flags().String("operator-id", "", "Operator identifier")
	cmd.Flags().Uint32(
		"max-concurrent-tasks", 0,
		"V6 batch capacity: concurrent inference tasks this worker can accept (0 → chain defaults to 1)",
	)
	_ = cmd.MarkFlagRequired("pubkey")
	_ = cmd.MarkFlagRequired("models")

	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func CmdExit() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exit",
		Short: "Request to exit as a worker (21-day unbonding)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}
			msg := types.NewMsgExitWorker(clientCtx.GetFromAddress().String())
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func CmdUnjail() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "unjail",
		Short: "Unjail a jailed worker",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			msg := types.NewMsgUnjail(clientCtx.GetFromAddress().String())
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}

	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func CmdStake() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "stake [amount]",
		Short: "Add stake to worker (e.g. 10000000000ufai)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			amount, err := sdk.ParseCoinNormalized(args[0])
			if err != nil {
				return err
			}

			msg := types.NewMsgStake(clientCtx.GetFromAddress().String(), amount)
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}

	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func CmdUpdateModels() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update-models [model1,model2,...]",
		Short: "Update supported model list",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientTxContext(cmd)
			if err != nil {
				return err
			}

			models := splitModels(args[0])
			msg := types.NewMsgUpdateModels(clientCtx.GetFromAddress().String(), models)
			return tx.GenerateOrBroadcastTxCLI(clientCtx, cmd.Flags(), msg)
		},
	}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

func splitModels(s string) []string {
	var result []string
	current := ""
	for _, c := range s {
		if c == ',' {
			if current != "" {
				result = append(result, current)
			}
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		result = append(result, current)
	}
	return result
}

// suppress unused import
var _ = strconv.Itoa
