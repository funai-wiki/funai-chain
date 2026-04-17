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

	"github.com/funai-wiki/funai-chain/x/worker/types"
)

// BankKeeper defines the expected bank module interface.
type BankKeeper interface {
	SendCoins(ctx context.Context, fromAddr, toAddr sdk.AccAddress, amt sdk.Coins) error
	SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
	BurnCoins(ctx context.Context, moduleName string, amt sdk.Coins) error
}

// ModelRegKeeper defines the expected modelreg module interface for worker state change callbacks.
type ModelRegKeeper interface {
	OnWorkerStateChange(ctx sdk.Context, workerAddr sdk.AccAddress)
	OnWorkerRemoved(ctx sdk.Context, workerAddr sdk.AccAddress)
}

type Keeper struct {
	cdc            codec.BinaryCodec
	storeKey       storetypes.StoreKey
	bankKeeper     BankKeeper
	modelRegKeeper ModelRegKeeper
	logger         log.Logger
}

func NewKeeper(
	cdc codec.BinaryCodec,
	storeKey storetypes.StoreKey,
	bankKeeper BankKeeper,
	logger log.Logger,
) Keeper {
	return Keeper{
		cdc:        cdc,
		storeKey:   storeKey,
		bankKeeper: bankKeeper,
		logger:     logger.With("module", "x/"+types.ModuleName),
	}
}

// SetModelRegKeeper sets the modelreg keeper reference (called after both keepers are created to avoid circular init).
func (k *Keeper) SetModelRegKeeper(mrk ModelRegKeeper) {
	k.modelRegKeeper = mrk
}

func (k Keeper) Logger() log.Logger {
	return k.logger
}

// -------- Worker CRUD --------

func (k Keeper) SetWorker(ctx sdk.Context, worker types.Worker) {
	store := ctx.KVStore(k.storeKey)
	addr, _ := sdk.AccAddressFromBech32(worker.Address)
	bz, _ := json.Marshal(worker)
	store.Set(types.WorkerKey(addr), bz)
}

func (k Keeper) GetWorker(ctx sdk.Context, addr sdk.AccAddress) (types.Worker, bool) {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.WorkerKey(addr))
	if bz == nil {
		return types.Worker{}, false
	}
	var worker types.Worker
	if err := json.Unmarshal(bz, &worker); err != nil {
		return types.Worker{}, false
	}
	return worker, true
}

// GetWorkerPubkey returns the pubkey string for a registered worker.
// Used by settlement keeper for Proposer signature verification (P0-2/P1-5).
func (k Keeper) GetWorkerPubkey(ctx sdk.Context, addr sdk.AccAddress) (string, bool) {
	w, found := k.GetWorker(ctx, addr)
	if !found {
		return "", false
	}
	return w.Pubkey, true
}

func (k Keeper) DeleteWorker(ctx sdk.Context, addr sdk.AccAddress) {
	store := ctx.KVStore(k.storeKey)
	store.Delete(types.WorkerKey(addr))
}

func (k Keeper) GetAllWorkers(ctx sdk.Context) []types.Worker {
	store := ctx.KVStore(k.storeKey)
	iter := storetypes.KVStorePrefixIterator(store, types.WorkerKeyPrefix)
	defer iter.Close()

	var workers []types.Worker
	for ; iter.Valid(); iter.Next() {
		var w types.Worker
		if err := json.Unmarshal(iter.Value(), &w); err != nil {
			continue
		}
		workers = append(workers, w)
	}
	return workers
}

// -------- Model Index --------

func (k Keeper) SetModelIndex(ctx sdk.Context, modelID string, addr sdk.AccAddress) {
	store := ctx.KVStore(k.storeKey)
	store.Set(types.ModelIndexKey(modelID, addr), []byte{1})
}

func (k Keeper) RemoveModelIndex(ctx sdk.Context, modelID string, addr sdk.AccAddress) {
	store := ctx.KVStore(k.storeKey)
	store.Delete(types.ModelIndexKey(modelID, addr))
}

func (k Keeper) SetModelIndices(ctx sdk.Context, addr sdk.AccAddress, models []string) {
	for _, m := range models {
		k.SetModelIndex(ctx, m, addr)
	}
}

func (k Keeper) RemoveModelIndices(ctx sdk.Context, addr sdk.AccAddress, models []string) {
	for _, m := range models {
		k.RemoveModelIndex(ctx, m, addr)
	}
}

func (k Keeper) GetWorkersByModel(ctx sdk.Context, modelID string) []types.Worker {
	store := ctx.KVStore(k.storeKey)
	prefix := types.ModelIndexIteratorPrefix(modelID)
	iter := storetypes.KVStorePrefixIterator(store, prefix)
	defer iter.Close()

	var workers []types.Worker
	for ; iter.Valid(); iter.Next() {
		key := iter.Key()
		addrBytes := key[len(prefix):]
		addr := sdk.AccAddress(addrBytes)
		w, found := k.GetWorker(ctx, addr)
		if found {
			workers = append(workers, w)
		}
	}
	return workers
}

// -------- Helpers --------

func (k Keeper) IsWorkerActive(ctx sdk.Context, addr sdk.AccAddress) bool {
	w, found := k.GetWorker(ctx, addr)
	if !found {
		return false
	}
	return w.IsActive()
}

// GetWorkerStake returns the stake amount for a worker. Returns zero if not found.
func (k Keeper) GetWorkerStake(ctx sdk.Context, addr sdk.AccAddress) math.Int {
	w, found := k.GetWorker(ctx, addr)
	if !found {
		return math.ZeroInt()
	}
	return w.Stake.Amount
}

// GetWorkerOperatorId returns the operator_id for a worker. Returns "" if not found.
func (k Keeper) GetWorkerOperatorId(ctx sdk.Context, addr sdk.AccAddress) string {
	w, found := k.GetWorker(ctx, addr)
	if !found {
		return ""
	}
	return w.OperatorId
}

func (k Keeper) GetActiveWorkerCount(ctx sdk.Context) uint64 {
	var count uint64
	workers := k.GetAllWorkers(ctx)
	for _, w := range workers {
		if w.IsActive() {
			count++
		}
	}
	return count
}

// -------- Operator / Stake queries --------

func (k Keeper) GetWorkersByOperatorId(ctx sdk.Context, operatorId string) []types.Worker {
	allWorkers := k.GetAllWorkers(ctx)
	var result []types.Worker
	for _, w := range allWorkers {
		if w.OperatorId == operatorId {
			result = append(result, w)
		}
	}
	return result
}

func (k Keeper) CountUniqueOperators(ctx sdk.Context, modelId string) int {
	workers := k.GetWorkersByModel(ctx, modelId)
	opSet := make(map[string]struct{})
	for _, w := range workers {
		if w.OperatorId != "" {
			opSet[w.OperatorId] = struct{}{}
		}
	}
	return len(opSet)
}

func (k Keeper) GetActiveWorkerStake(ctx sdk.Context) math.Int {
	total := math.ZeroInt()
	workers := k.GetAllWorkers(ctx)
	for _, w := range workers {
		if w.IsActive() {
			total = total.Add(w.Stake.Amount)
		}
	}
	return total
}

func (k Keeper) GetModelInstalledStake(ctx sdk.Context, modelId string) math.Int {
	total := math.ZeroInt()
	workers := k.GetWorkersByModel(ctx, modelId)
	for _, w := range workers {
		if w.IsActive() {
			total = total.Add(w.Stake.Amount)
		}
	}
	return total
}

// TombstoneWorker immediately tombstones a worker (used by FraudProof).
// V5.2 §12.4: fraud_proof_action = slash 5% + tombstone.
func (k Keeper) TombstoneWorker(ctx sdk.Context, workerAddr sdk.AccAddress) {
	worker, found := k.GetWorker(ctx, workerAddr)
	if !found || worker.Tombstoned {
		return
	}

	worker.Tombstoned = true
	worker.Status = types.WorkerStatusJailed
	worker.Jailed = true
	worker.JailUntil = 0
	k.SetWorker(ctx, worker)

	if k.modelRegKeeper != nil {
		k.modelRegKeeper.OnWorkerStateChange(ctx, workerAddr)
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventWorkerTombstoned,
		sdk.NewAttribute(types.AttributeKeyWorker, worker.Address),
	))
}

// -------- V5.2 Jail / Slash / Unjail --------

// JailWorker jails a worker using progressive durations based on jail_count.
// V5.2: 1st jail = Jail1Duration (120 blocks), 2nd = Jail2Duration (720 blocks),
// 3rd+ = slash 5% + tombstone.
// All roles (Worker, Leader, Proposer, verifier, second_verifier) share the same jail progression.
func (k Keeper) JailWorker(ctx sdk.Context, workerAddr sdk.AccAddress, _ int64) {
	worker, found := k.GetWorker(ctx, workerAddr)
	if !found {
		return
	}
	if worker.Tombstoned {
		return
	}

	currentHeight := ctx.BlockHeight()
	params := k.GetParams(ctx)

	worker.JailCount++
	worker.SuccessStreak = 0

	if worker.JailCount >= 3 {
		k.SlashWorker(ctx, workerAddr, params.SlashFraudPercent)
		worker, _ = k.GetWorker(ctx, workerAddr)
		worker.Tombstoned = true
		worker.Status = types.WorkerStatusJailed
		worker.Jailed = true
		worker.JailUntil = 0
		k.SetWorker(ctx, worker)

		// Notify modelreg: worker tombstoned → refresh affected model stats
		if k.modelRegKeeper != nil {
			k.modelRegKeeper.OnWorkerStateChange(ctx, workerAddr)
		}

		ctx.EventManager().EmitEvent(sdk.NewEvent(
			types.EventWorkerTombstoned,
			sdk.NewAttribute(types.AttributeKeyWorker, worker.Address),
		))
		return
	}

	var jailDuration int64
	if worker.JailCount == 1 {
		jailDuration = params.Jail1Duration
	} else {
		jailDuration = params.Jail2Duration
	}

	worker.Jailed = true
	worker.Status = types.WorkerStatusJailed
	worker.JailUntil = currentHeight + jailDuration
	k.SetWorker(ctx, worker)

	// Notify modelreg: worker jailed → refresh affected model stats
	if k.modelRegKeeper != nil {
		k.modelRegKeeper.OnWorkerStateChange(ctx, workerAddr)
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventWorkerJailed,
		sdk.NewAttribute(types.AttributeKeyWorker, worker.Address),
		sdk.NewAttribute(types.AttributeKeyJailCount, fmt.Sprintf("%d", worker.JailCount)),
		sdk.NewAttribute(types.AttributeKeyJailUntil, fmt.Sprintf("%d", worker.JailUntil)),
	))
}

// UnjailWorker removes the jail status from a worker if the jail period has elapsed.
func (k Keeper) UnjailWorker(ctx sdk.Context, workerAddr sdk.AccAddress) error {
	worker, found := k.GetWorker(ctx, workerAddr)
	if !found {
		return types.ErrWorkerNotFound
	}
	if !worker.Jailed {
		return types.ErrWorkerNotJailed
	}
	if worker.Tombstoned {
		return types.ErrWorkerTombstoned
	}
	if ctx.BlockHeight() < worker.JailUntil {
		return types.ErrJailPeriodNotElapsed
	}

	worker.Jailed = false
	worker.Status = types.WorkerStatusActive
	worker.JailUntil = 0
	k.SetWorker(ctx, worker)

	// Notify modelreg: worker unjailed (active again) → refresh affected model stats
	if k.modelRegKeeper != nil {
		k.modelRegKeeper.OnWorkerStateChange(ctx, workerAddr)
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventWorkerUnjailed,
		sdk.NewAttribute(types.AttributeKeyWorker, worker.Address),
	))
	return nil
}

// SlashWorker burns a percentage of the worker's stake.
func (k Keeper) SlashWorker(ctx sdk.Context, workerAddr sdk.AccAddress, percent uint32) {
	k.slashWorkerInternal(ctx, workerAddr, percent, nil)
}

// SlashWorkerTo slashes a percentage of the worker's stake and sends it to a recipient.
// V5.2 §12.4: FraudProof → slash 5% stake → compensate user.
func (k Keeper) SlashWorkerTo(ctx sdk.Context, workerAddr sdk.AccAddress, percent uint32, recipient sdk.AccAddress) {
	k.slashWorkerInternal(ctx, workerAddr, percent, recipient)
}

func (k Keeper) slashWorkerInternal(ctx sdk.Context, workerAddr sdk.AccAddress, percent uint32, recipient sdk.AccAddress) {
	worker, found := k.GetWorker(ctx, workerAddr)
	if !found {
		return
	}

	if worker.Stake.IsZero() {
		return
	}

	slashAmount := worker.Stake.Amount.MulRaw(int64(percent)).QuoRaw(100)
	if slashAmount.IsZero() {
		return
	}

	slashCoin := sdk.NewCoin(worker.Stake.Denom, slashAmount)
	slashCoins := sdk.NewCoins(slashCoin)

	if recipient != nil {
		if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleAccountName, recipient, slashCoins); err != nil {
			k.Logger().Error("failed to send slashed coins to recipient", "worker", worker.Address, "recipient", recipient.String(), "error", err)
			return
		}
	} else {
		if err := k.bankKeeper.BurnCoins(ctx, types.ModuleAccountName, slashCoins); err != nil {
			k.Logger().Error("failed to burn slashed coins", "worker", worker.Address, "error", err)
			return
		}
	}

	worker.Stake = worker.Stake.Sub(slashCoin)
	k.SetWorker(ctx, worker)

	// Notify modelreg: worker stake changed → refresh affected model stats
	if k.modelRegKeeper != nil {
		k.modelRegKeeper.OnWorkerStateChange(ctx, workerAddr)
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventWorkerSlashed,
		sdk.NewAttribute(types.AttributeKeyWorker, worker.Address),
		sdk.NewAttribute(types.AttributeKeyAmount, slashCoin.String()),
		sdk.NewAttribute(types.AttributeKeySlashPct, fmt.Sprintf("%d", percent)),
	))
}

// IncrementSuccessStreak increments the success streak and resets jail_count
// if the threshold is reached (V5.2: 50 consecutive successes).
func (k Keeper) IncrementSuccessStreak(ctx sdk.Context, workerAddr sdk.AccAddress) {
	worker, found := k.GetWorker(ctx, workerAddr)
	if !found {
		return
	}

	params := k.GetParams(ctx)
	worker.SuccessStreak++

	if worker.SuccessStreak >= params.SuccessResetThreshold {
		worker.JailCount = 0
		worker.SuccessStreak = 0
	}

	k.SetWorker(ctx, worker)
}

// GetSuccessStreak returns the current success streak for a worker.
func (k Keeper) GetSuccessStreak(ctx sdk.Context, workerAddr sdk.AccAddress) uint32 {
	worker, found := k.GetWorker(ctx, workerAddr)
	if !found {
		return 0
	}
	return worker.SuccessStreak
}

// UpdateWorkerStats updates cumulative worker statistics after settlement.
func (k Keeper) UpdateWorkerStats(ctx sdk.Context, workerAddr sdk.AccAddress, feeEarned sdk.Coin) {
	worker, found := k.GetWorker(ctx, workerAddr)
	if !found {
		return
	}

	worker.TotalTasks++
	worker.TotalFeeEarned = worker.TotalFeeEarned.Add(feeEarned)
	worker.LastActiveBlock = ctx.BlockHeight()
	k.SetWorker(ctx, worker)
}

// -------- EndBlocker: Process Exiting Workers --------

// ProcessExitingWorkers handles workers whose exit_wait period has elapsed.
func (k Keeper) ProcessExitingWorkers(ctx sdk.Context) {
	params := k.GetParams(ctx)
	currentHeight := ctx.BlockHeight()

	workers := k.GetAllWorkers(ctx)
	for _, w := range workers {
		if w.Status != types.WorkerStatusExiting {
			continue
		}
		if w.ExitRequestedAt == 0 {
			continue
		}
		if currentHeight-w.ExitRequestedAt < params.ExitWaitPeriod {
			continue
		}

		addr, err := sdk.AccAddressFromBech32(w.Address)
		if err != nil {
			continue
		}

		if !w.Stake.IsZero() {
			coins := sdk.NewCoins(w.Stake)
			if err := k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleAccountName, addr, coins); err != nil {
				k.Logger().Error("failed to return stake on exit", "worker", w.Address, "error", err)
				continue
			}
		}

		// Notify modelreg before deletion: worker removed → clean up reverse index + refresh model stats
		if k.modelRegKeeper != nil {
			k.modelRegKeeper.OnWorkerRemoved(ctx, addr)
		}

		k.RemoveModelIndices(ctx, addr, w.SupportedModels)
		k.DeleteWorker(ctx, addr)

		ctx.EventManager().EmitEvent(sdk.NewEvent(
			types.EventWorkerExited,
			sdk.NewAttribute(types.AttributeKeyWorker, w.Address),
			sdk.NewAttribute(types.AttributeKeyStatus, types.WorkerStatusExited.String()),
		))
	}
}

// -------- Params --------

func (k Keeper) SetParams(ctx sdk.Context, params types.Params) {
	store := ctx.KVStore(k.storeKey)
	bz, _ := json.Marshal(params)
	store.Set(types.ParamsKey, bz)
}

func (k Keeper) GetParams(ctx sdk.Context) types.Params {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.ParamsKey)
	if bz == nil {
		return types.DefaultParams()
	}
	var params types.Params
	if err := json.Unmarshal(bz, &params); err != nil {
		return types.DefaultParams()
	}
	return params
}

// -------- Reputation Mechanism (Audit KT §3) --------

// ensureReputation initializes ReputationScore if zero (uninitialized worker).
func ensureReputation(w *types.Worker) {
	if w.ReputationScore == 0 {
		w.ReputationScore = types.ReputationInitial
	}
}

// ReputationOnAccept increases reputation for successfully accepting a task.
func (k Keeper) ReputationOnAccept(ctx sdk.Context, addr sdk.AccAddress) {
	w, found := k.GetWorker(ctx, addr)
	if !found {
		return
	}
	ensureReputation(&w)
	w.ReputationScore += types.ReputationAcceptDelta
	if w.ReputationScore > types.ReputationMax {
		w.ReputationScore = types.ReputationMax
	}
	w.ConsecutiveRejects = 0 // accept resets reject counter
	k.SetWorker(ctx, w)
}

// ReputationOnMiss decreases reputation for timeout (no response within window).
// role: "worker" → standard miss, "second_verifier" → doubled penalty.
func (k Keeper) ReputationOnMiss(ctx sdk.Context, addr sdk.AccAddress, role string) {
	w, found := k.GetWorker(ctx, addr)
	if !found {
		return
	}
	ensureReputation(&w)
	delta := types.ReputationMissDelta
	if role == "second_verifier" {
		delta = types.ReputationAuditMiss
	}
	if w.ReputationScore > delta {
		w.ReputationScore -= delta
	} else {
		w.ReputationScore = 0
	}
	k.SetWorker(ctx, w)
}

// ReputationOnReject tracks consecutive rejects. After 10 → penalty.
// "busy" rejects are neutral (ConsecutiveRejects not incremented).
func (k Keeper) ReputationOnReject(ctx sdk.Context, addr sdk.AccAddress, isBusy bool) {
	if isBusy {
		return // honest busy reject → neutral
	}
	w, found := k.GetWorker(ctx, addr)
	if !found {
		return
	}
	ensureReputation(&w)
	w.ConsecutiveRejects++
	if w.ConsecutiveRejects >= types.ReputationRejectLimit {
		if w.ReputationScore > types.ReputationRejectChain {
			w.ReputationScore -= types.ReputationRejectChain
		} else {
			w.ReputationScore = 0
		}
		w.ConsecutiveRejects = 0
	}
	k.SetWorker(ctx, w)
}

// ReputationDecayAll decays all active workers' reputation toward 1.0 (±0.005).
// Called from BeginBlocker every ~720 blocks (1 hour at 5s/block).
func (k Keeper) ReputationDecayAll(ctx sdk.Context) {
	workers := k.GetAllWorkers(ctx)
	for _, w := range workers {
		if !w.IsActive() {
			continue
		}
		ensureReputation(&w)
		if w.ReputationScore > types.ReputationInitial {
			// Above 1.0 → decay down
			if w.ReputationScore-types.ReputationDecayStep >= types.ReputationInitial {
				w.ReputationScore -= types.ReputationDecayStep
			} else {
				w.ReputationScore = types.ReputationInitial
			}
		} else if w.ReputationScore < types.ReputationInitial {
			// Below 1.0 → decay up
			if w.ReputationScore+types.ReputationDecayStep <= types.ReputationInitial {
				w.ReputationScore += types.ReputationDecayStep
			} else {
				w.ReputationScore = types.ReputationInitial
			}
		}
		k.SetWorker(ctx, w)
	}
}

// UpdateAvgLatency updates a worker's average latency using exponential moving average.
// Called during settlement with the task's measured latency.
func (k Keeper) UpdateAvgLatency(ctx sdk.Context, addr sdk.AccAddress, latencyMs uint32) {
	w, found := k.GetWorker(ctx, addr)
	if !found || latencyMs == 0 {
		return
	}
	if w.AvgLatencyMs == 0 {
		w.AvgLatencyMs = latencyMs
	} else {
		// EMA: new = 0.8*old + 0.2*sample
		w.AvgLatencyMs = (w.AvgLatencyMs*4 + latencyMs) / 5
	}
	k.SetWorker(ctx, w)
}
