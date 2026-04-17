package keeper

import (
	"context"
	"encoding/json"
	"fmt"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/reward/types"
)

type Keeper struct {
	cdc      codec.BinaryCodec
	storeKey storetypes.StoreKey

	bankKeeper    BankKeeper
	accountKeeper AccountKeeper

	authority string
}

type BankKeeper interface {
	MintCoins(ctx context.Context, moduleName string, amounts sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
	SendCoinsFromModuleToModule(ctx context.Context, senderModule, recipientModule string, amt sdk.Coins) error
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
}

type AccountKeeper interface {
	GetModuleAddress(name string) sdk.AccAddress
	GetModuleAccount(ctx context.Context, name string) sdk.ModuleAccountI
}

// MultiVerificationFundName is the module account that receives the 3% block-reward
// slice of the multi-verification fund (formerly audit fund). Matches x/settlement
// ModuleName to share a module account with fee-based audit fund accumulation.
const MultiVerificationFundName = "settlement"

func NewKeeper(
	cdc codec.BinaryCodec,
	storeKey storetypes.StoreKey,
	bankKeeper BankKeeper,
	accountKeeper AccountKeeper,
	authority string,
) Keeper {
	return Keeper{
		cdc:           cdc,
		storeKey:      storeKey,
		bankKeeper:    bankKeeper,
		accountKeeper: accountKeeper,
		authority:     authority,
	}
}

func (k Keeper) Logger(ctx sdk.Context) log.Logger {
	return ctx.Logger().With("module", fmt.Sprintf("x/%s", types.ModuleName))
}

func (k Keeper) GetAuthority() string {
	return k.authority
}

// CalculateBlockReward applies the halving formula: reward(h) = base * 0.5^floor(h/halving_period)
func (k Keeper) CalculateBlockReward(ctx sdk.Context, height int64) math.Int {
	params := k.GetParams(ctx)
	halvings := height / params.HalvingPeriod
	if halvings >= 64 {
		return math.ZeroInt()
	}
	reward := params.BaseBlockReward
	for i := int64(0); i < halvings; i++ {
		reward = reward.QuoRaw(2)
	}
	if reward.IsZero() {
		return math.ZeroInt()
	}
	return reward
}

// CalculateEpochReward computes the total reward for an epoch.
func (k Keeper) CalculateEpochReward(ctx sdk.Context, epochEndHeight int64) math.Int {
	params := k.GetParams(ctx)
	epochBlocks := params.EpochBlocks
	epochStartHeight := epochEndHeight - epochBlocks + 1
	if epochStartHeight < 1 {
		epochStartHeight = 1
	}

	totalReward := math.ZeroInt()
	for h := epochStartHeight; h <= epochEndHeight; h++ {
		totalReward = totalReward.Add(k.CalculateBlockReward(ctx, h))
	}
	return totalReward
}

// DistributeRewards distributes epoch rewards.
// V5.2 spec:
//   - With inference contributions: 99% by inference contribution, 1% by verification/audit count
//   - Without inference: 100% by consensus committee signing count
//   - Fallback: distribute by stake if no consensus signer info available
func (k Keeper) DistributeRewards(
	ctx sdk.Context,
	contributions []types.WorkerContribution,
	verificationContribs []types.VerificationContribution,
	consensusSigners []types.ConsensusSignerInfo,
	onlineWorkers []types.OnlineWorkerStake,
) error {
	height := ctx.BlockHeight()
	params := k.GetParams(ctx)
	epochBlocks := params.EpochBlocks

	epoch := (height-1)/epochBlocks + 1

	epochReward := k.CalculateEpochReward(ctx, height)
	k.Logger(ctx).Info("reward DistributeRewards",
		"height", height, "epoch", epoch, "reward", epochReward.String(),
		"contribs", len(contributions), "signers", len(consensusSigners), "workers", len(onlineWorkers))
	if epochReward.IsZero() {
		return nil
	}

	if len(contributions) > 0 {
		inferenceReward := params.InferenceWeight.MulInt(epochReward).TruncateInt()
		verifierReward := params.VerificationWeight.MulInt(epochReward).TruncateInt()
		// Remainder goes to multi-verification fund (3% by default) to cover dust.
		fundReward := epochReward.Sub(inferenceReward).Sub(verifierReward)

		if err := k.distributeByContribution(ctx, epoch, inferenceReward, contributions, params); err != nil {
			return err
		}

		if verifierReward.IsPositive() && len(verificationContribs) > 0 {
			if err := k.distributeByVerification(ctx, epoch, verifierReward, verificationContribs, params); err != nil {
				return err
			}
		}

		// Multi-verification fund: mint into x/reward then transfer to settlement
		// module account, where it co-mingles with fee-based audit fund accumulation
		// and is distributed per-epoch to 2nd/3rd verifiers via settlement.DistributeMultiVerificationFund.
		if fundReward.IsPositive() {
			coins := sdk.NewCoins(sdk.NewCoin(types.BondDenom, fundReward))
			if err := k.bankKeeper.MintCoins(ctx, types.ModuleName, coins); err != nil {
				return err
			}
			if err := k.bankKeeper.SendCoinsFromModuleToModule(ctx, types.ModuleName, MultiVerificationFundName, coins); err != nil {
				return err
			}
			ctx.EventManager().EmitEvent(sdk.NewEvent(
				types.EventTypeRewardDistributed,
				sdk.NewAttribute(types.AttributeKeyEpoch, fmt.Sprintf("%d", epoch)),
				sdk.NewAttribute(types.AttributeKeyRewardAmount, fundReward.String()),
				sdk.NewAttribute(types.AttributeKeyDistributionMode, "multi_verification_fund"),
			))
		}
		return nil
	}

	// V5.2: empty epoch → 100% by consensus committee signing count
	if len(consensusSigners) > 0 {
		return k.distributeByConsensusSigning(ctx, epoch, epochReward, consensusSigners)
	}

	// Fallback: distribute by stake
	if len(onlineWorkers) > 0 {
		return k.distributeByStake(ctx, epoch, epochReward, onlineWorkers)
	}

	k.Logger(ctx).Info("no contributions and no online workers in epoch, reward not distributed",
		"epoch", epoch, "reward", epochReward.String())
	return nil
}

// distributeByContribution distributes the 85% inference-pool rewards using:
// w_i = FeeWeight * (fee_i / sum_fee) + CountWeight * (count_i / sum_count)
// Default weights: 0.85 fee + 0.15 count.
func (k Keeper) distributeByContribution(ctx sdk.Context, epoch int64, epochReward math.Int, contributions []types.WorkerContribution, params types.Params) error {
	totalFees := math.ZeroInt()
	totalCount := uint64(0)
	for _, c := range contributions {
		totalFees = totalFees.Add(c.FeeAmount)
		totalCount += c.TaskCount
	}

	totalDistributed := math.ZeroInt()

	for i, c := range contributions {
		weight := math.LegacyZeroDec()
		if totalFees.IsPositive() {
			feeRatio := math.LegacyNewDecFromInt(c.FeeAmount).Quo(math.LegacyNewDecFromInt(totalFees))
			weight = weight.Add(params.FeeWeight.Mul(feeRatio))
		}
		if totalCount > 0 {
			countRatio := math.LegacyNewDec(int64(c.TaskCount)).Quo(math.LegacyNewDec(int64(totalCount)))
			weight = weight.Add(params.CountWeight.Mul(countRatio))
		}

		var alloc math.Int
		if i == len(contributions)-1 {
			alloc = epochReward.Sub(totalDistributed)
		} else {
			alloc = weight.MulInt(epochReward).TruncateInt()
		}

		if alloc.IsPositive() {
			if err := k.mintAndSend(ctx, c.WorkerAddress, alloc); err != nil {
				return err
			}
			k.SetRewardRecord(ctx, types.RewardRecord{
				Epoch:         epoch,
				WorkerAddress: c.WorkerAddress,
				Amount:        sdk.NewCoin(types.BondDenom, alloc),
			})
			totalDistributed = totalDistributed.Add(alloc)

			ctx.EventManager().EmitEvent(sdk.NewEvent(
				types.EventTypeRewardDistributed,
				sdk.NewAttribute(types.AttributeKeyEpoch, fmt.Sprintf("%d", epoch)),
				sdk.NewAttribute(types.AttributeKeyWorkerAddress, c.WorkerAddress),
				sdk.NewAttribute(types.AttributeKeyRewardAmount, alloc.String()),
				sdk.NewAttribute(types.AttributeKeyDistributionMode, "inference"),
			))
		}
	}
	return nil
}

// distributeByVerification distributes the 12% verifier-pool rewards to all verifiers
// (1st-tier + 2nd/3rd-verifiers) using the same weighted formula as the inference pool:
// w_i = FeeWeight * (fee_i / sum_fee) + CountWeight * (count_i / sum_count)
// Default weights: 0.85 fee + 0.15 count. fee_i = verifier's earned fees from verification
// + 2nd/3rd-verification roles this epoch; count_i = VerificationCount + AuditCount.
func (k Keeper) distributeByVerification(ctx sdk.Context, epoch int64, totalReward math.Int, contribs []types.VerificationContribution, params types.Params) error {
	totalCount := uint64(0)
	totalFee := math.ZeroInt()
	for _, c := range contribs {
		totalCount += c.VerificationCount + c.AuditCount
		if !c.FeeAmount.IsNil() {
			totalFee = totalFee.Add(c.FeeAmount)
		}
	}
	if totalCount == 0 {
		return nil
	}

	totalDistributed := math.ZeroInt()
	for i, c := range contribs {
		workerCount := c.VerificationCount + c.AuditCount
		weight := math.LegacyZeroDec()
		if totalFee.IsPositive() && !c.FeeAmount.IsNil() && c.FeeAmount.IsPositive() {
			feeRatio := math.LegacyNewDecFromInt(c.FeeAmount).Quo(math.LegacyNewDecFromInt(totalFee))
			weight = weight.Add(params.FeeWeight.Mul(feeRatio))
		}
		if totalCount > 0 {
			countRatio := math.LegacyNewDec(int64(workerCount)).Quo(math.LegacyNewDec(int64(totalCount)))
			weight = weight.Add(params.CountWeight.Mul(countRatio))
		}
		// If no fee data available (totalFee == 0), distribute purely by count.
		if totalFee.IsZero() {
			countRatio := math.LegacyNewDec(int64(workerCount)).Quo(math.LegacyNewDec(int64(totalCount)))
			weight = countRatio // 100% by count
		}

		var alloc math.Int
		if i == len(contribs)-1 {
			alloc = totalReward.Sub(totalDistributed)
		} else {
			alloc = weight.MulInt(totalReward).TruncateInt()
		}
		if alloc.IsPositive() {
			if err := k.mintAndSend(ctx, c.WorkerAddress, alloc); err != nil {
				return err
			}
			k.SetRewardRecord(ctx, types.RewardRecord{
				Epoch:         epoch,
				WorkerAddress: c.WorkerAddress,
				Amount:        sdk.NewCoin(types.BondDenom, alloc),
			})
			totalDistributed = totalDistributed.Add(alloc)

			ctx.EventManager().EmitEvent(sdk.NewEvent(
				types.EventTypeRewardDistributed,
				sdk.NewAttribute(types.AttributeKeyEpoch, fmt.Sprintf("%d", epoch)),
				sdk.NewAttribute(types.AttributeKeyWorkerAddress, c.WorkerAddress),
				sdk.NewAttribute(types.AttributeKeyRewardAmount, alloc.String()),
				sdk.NewAttribute(types.AttributeKeyDistributionMode, "verification"),
			))
		}
	}
	return nil
}

// distributeByConsensusSigning distributes rewards by consensus committee signing count.
// V5.2: empty epoch → 100% by Validator committee block signing.
func (k Keeper) distributeByConsensusSigning(ctx sdk.Context, epoch int64, epochReward math.Int, signers []types.ConsensusSignerInfo) error {
	totalSigned := uint64(0)
	for _, s := range signers {
		totalSigned += s.BlocksSigned
	}
	if totalSigned == 0 {
		return nil
	}

	totalDistributed := math.ZeroInt()
	for i, s := range signers {
		var alloc math.Int
		if i == len(signers)-1 {
			alloc = epochReward.Sub(totalDistributed)
		} else {
			ratio := math.LegacyNewDec(int64(s.BlocksSigned)).Quo(math.LegacyNewDec(int64(totalSigned)))
			alloc = ratio.MulInt(epochReward).TruncateInt()
		}
		if alloc.IsPositive() {
			if err := k.mintAndSend(ctx, s.ValidatorAddress, alloc); err != nil {
				return err
			}
			k.SetRewardRecord(ctx, types.RewardRecord{
				Epoch:         epoch,
				WorkerAddress: s.ValidatorAddress,
				Amount:        sdk.NewCoin(types.BondDenom, alloc),
			})
			totalDistributed = totalDistributed.Add(alloc)

			ctx.EventManager().EmitEvent(sdk.NewEvent(
				types.EventTypeRewardDistributed,
				sdk.NewAttribute(types.AttributeKeyEpoch, fmt.Sprintf("%d", epoch)),
				sdk.NewAttribute(types.AttributeKeyWorkerAddress, s.ValidatorAddress),
				sdk.NewAttribute(types.AttributeKeyRewardAmount, alloc.String()),
				sdk.NewAttribute(types.AttributeKeyDistributionMode, "consensus"),
			))
		}
	}
	return nil
}

// distributeByStake is the fallback when no consensus signer info is available.
func (k Keeper) distributeByStake(ctx sdk.Context, epoch int64, epochReward math.Int, onlineWorkers []types.OnlineWorkerStake) error {
	totalStake := math.ZeroInt()
	for _, w := range onlineWorkers {
		totalStake = totalStake.Add(w.Stake)
	}

	if totalStake.IsZero() {
		return nil
	}

	totalDistributed := math.ZeroInt()

	for i, w := range onlineWorkers {
		var alloc math.Int
		if i == len(onlineWorkers)-1 {
			alloc = epochReward.Sub(totalDistributed)
		} else {
			stakeRatio := math.LegacyNewDecFromInt(w.Stake).Quo(math.LegacyNewDecFromInt(totalStake))
			alloc = stakeRatio.MulInt(epochReward).TruncateInt()
		}

		if alloc.IsPositive() {
			if err := k.mintAndSend(ctx, w.WorkerAddress, alloc); err != nil {
				return err
			}
			k.SetRewardRecord(ctx, types.RewardRecord{
				Epoch:         epoch,
				WorkerAddress: w.WorkerAddress,
				Amount:        sdk.NewCoin(types.BondDenom, alloc),
			})
			totalDistributed = totalDistributed.Add(alloc)

			ctx.EventManager().EmitEvent(sdk.NewEvent(
				types.EventTypeRewardDistributed,
				sdk.NewAttribute(types.AttributeKeyEpoch, fmt.Sprintf("%d", epoch)),
				sdk.NewAttribute(types.AttributeKeyWorkerAddress, w.WorkerAddress),
				sdk.NewAttribute(types.AttributeKeyRewardAmount, alloc.String()),
				sdk.NewAttribute(types.AttributeKeyDistributionMode, "stake"),
			))
		}
	}
	return nil
}

func (k Keeper) mintAndSend(ctx sdk.Context, workerAddress string, amount math.Int) error {
	coins := sdk.NewCoins(sdk.NewCoin(types.BondDenom, amount))
	if err := k.bankKeeper.MintCoins(ctx, types.ModuleName, coins); err != nil {
		return err
	}
	workerAddr, err := sdk.AccAddressFromBech32(workerAddress)
	if err != nil {
		return types.ErrInvalidAddress.Wrapf("invalid worker address: %s", workerAddress)
	}
	if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleName, workerAddr, coins); err != nil {
		return err
	}
	return nil
}

func (k Keeper) GetParams(ctx sdk.Context) types.Params {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get([]byte(types.ParamsKey))
	if bz == nil {
		return types.DefaultParams()
	}
	var params types.Params
	if err := json.Unmarshal(bz, &params); err != nil {
		return types.DefaultParams()
	}
	return params
}

func (k Keeper) SetParams(ctx sdk.Context, params types.Params) error {
	if err := params.Validate(); err != nil {
		return err
	}
	store := ctx.KVStore(k.storeKey)
	bz, err := json.Marshal(params)
	if err != nil {
		return err
	}
	store.Set([]byte(types.ParamsKey), bz)
	return nil
}

func (k Keeper) SetRewardRecord(ctx sdk.Context, record types.RewardRecord) {
	store := ctx.KVStore(k.storeKey)
	key := types.KeyRewardRecord(record.Epoch, record.WorkerAddress)
	bz, err := json.Marshal(record)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal reward record: %v", err))
	}
	store.Set(key, bz)
}

func (k Keeper) GetRewardRecords(ctx sdk.Context, workerAddress string) []types.RewardRecord {
	store := ctx.KVStore(k.storeKey)
	iterator := storetypes.KVStorePrefixIterator(store, []byte(types.RewardRecordKeyPrefix))
	defer iterator.Close()

	var records []types.RewardRecord
	for ; iterator.Valid(); iterator.Next() {
		var record types.RewardRecord
		if err := json.Unmarshal(iterator.Value(), &record); err != nil {
			continue
		}
		if workerAddress == "" || record.WorkerAddress == workerAddress {
			records = append(records, record)
		}
	}
	return records
}
