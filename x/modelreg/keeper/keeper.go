package keeper

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/modelreg/types"
)

// WorkerKeeper defines the expected worker module interface for verifying worker status
// and computing model statistics.
type WorkerKeeper interface {
	IsWorkerActive(ctx sdk.Context, addr sdk.AccAddress) bool
	GetWorkerStake(ctx sdk.Context, addr sdk.AccAddress) math.Int
	GetWorkerOperatorId(ctx sdk.Context, addr sdk.AccAddress) string
	GetActiveWorkerStake(ctx sdk.Context) math.Int
	// WorkerSupportsModel reports whether the worker registered the given
	// model_id in MsgRegisterWorker.SupportedModels (or a later
	// UpdateSupportedModels). DeclareInstalled uses this to refuse
	// "I installed a model I never claimed to support" — without the
	// check, an active worker can self-report installation of any model
	// and pollute InstalledStakeRatio / WorkerCount, biasing model
	// activation and the VRF serving set.
	WorkerSupportsModel(ctx sdk.Context, addr sdk.AccAddress, modelId string) bool
}

// Keeper maintains the state for the modelreg module.
type Keeper struct {
	cdc          codec.BinaryCodec
	storeKey     storetypes.StoreKey
	workerKeeper WorkerKeeper
	logger       log.Logger

	// authority is the bech32 address allowed to mutate model state via
	// MsgUpdateModelStats. Mirrors the pattern in x/settlement and x/reward.
	// Set to authtypes.NewModuleAddress("gov") at app.go wiring time so any
	// stats / activation update goes through governance.
	authority string
}

// NewKeeper creates a new modelreg module Keeper.
func NewKeeper(
	cdc codec.BinaryCodec,
	storeKey storetypes.StoreKey,
	workerKeeper WorkerKeeper,
	authority string,
	logger log.Logger,
) Keeper {
	return Keeper{
		cdc:          cdc,
		storeKey:     storeKey,
		workerKeeper: workerKeeper,
		authority:    authority,
		logger:       logger.With("module", "x/"+types.ModuleName),
	}
}

// GetAuthority returns the bech32 address of the module's governance authority.
// KT Issue 16: UpdateModelStats handler compares msg.Authority against this
// to gate model-state mutation behind governance.
func (k Keeper) GetAuthority() string {
	return k.authority
}

func (k Keeper) Logger() log.Logger {
	return k.logger
}

// -------- Model CRUD --------

func (k Keeper) SetModel(ctx sdk.Context, model types.Model) {
	store := ctx.KVStore(k.storeKey)
	bz, _ := json.Marshal(model)
	store.Set(types.ModelKey(model.ModelId), bz)
}

func (k Keeper) GetModel(ctx sdk.Context, modelID string) (types.Model, bool) {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.ModelKey(modelID))
	if bz == nil {
		return types.Model{}, false
	}
	var model types.Model
	if err := json.Unmarshal(bz, &model); err != nil {
		return types.Model{}, false
	}
	return model, true
}

func (k Keeper) DeleteModel(ctx sdk.Context, modelID string) {
	store := ctx.KVStore(k.storeKey)
	store.Delete(types.ModelKey(modelID))
}

func (k Keeper) GetAllModels(ctx sdk.Context) []types.Model {
	store := ctx.KVStore(k.storeKey)
	iter := storetypes.KVStorePrefixIterator(store, types.ModelKeyPrefix)
	defer iter.Close()

	var models []types.Model
	for ; iter.Valid(); iter.Next() {
		var m types.Model
		if err := json.Unmarshal(iter.Value(), &m); err != nil {
			continue
		}
		models = append(models, m)
	}
	return models
}

// -------- Alias Index --------

// SetAliasIndex stores the alias → model_id mapping (globally unique).
func (k Keeper) SetAliasIndex(ctx sdk.Context, alias string, modelID string) {
	store := ctx.KVStore(k.storeKey)
	store.Set(types.AliasKey(alias), []byte(modelID))
}

// GetModelIdByAlias looks up the model_id for a given alias.
func (k Keeper) GetModelIdByAlias(ctx sdk.Context, alias string) (string, bool) {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.AliasKey(alias))
	if bz == nil {
		return "", false
	}
	return string(bz), true
}

// HasAlias checks if an alias is already taken.
func (k Keeper) HasAlias(ctx sdk.Context, alias string) bool {
	store := ctx.KVStore(k.storeKey)
	return store.Has(types.AliasKey(alias))
}

// GetModelByAlias resolves alias → model_id → Model.
func (k Keeper) GetModelByAlias(ctx sdk.Context, alias string) (types.Model, bool) {
	modelID, found := k.GetModelIdByAlias(ctx, alias)
	if !found {
		return types.Model{}, false
	}
	return k.GetModel(ctx, modelID)
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

// -------- Business Logic --------

// ComputeModelId derives model_id from the three component hashes.
//
// KT 30-case (modelreg #9 / unified-test-report Issue 16): pre-fix the
// implementation just concatenated the three strings into the hasher with no
// separator. Variable-length inputs allowed boundary collisions:
//
//	ComputeModelId("ABC", "DE", "FG")  ==  ComputeModelId("AB", "CDE", "FG")
//
// An attacker could submit a model proposal with carefully shifted boundaries
// to land on the same model_id as a different (W, Q, R) tuple — a 2nd-preimage
// attack via boundary engineering, much cheaper than 2^256 brute force.
//
// Fix: prefix each field with its byte length (8-byte big-endian uint64) so
// the boundaries between fields are unambiguous. Length-prefixing is preferred
// over a single-byte sentinel because the input strings could in principle
// contain that byte; an explicit length is collision-proof.
//
// This changes the hash output for ALL inputs vs the pre-fix function. The
// chain has not yet shipped a mainnet, so no migration is needed; any model
// already registered on a local testnet will need to be re-registered.
func ComputeModelId(weightHash, quantConfigHash, runtimeImageHash string) string {
	h := sha256.New()
	writeLenPrefixed(h, weightHash)
	writeLenPrefixed(h, quantConfigHash)
	writeLenPrefixed(h, runtimeImageHash)
	return hex.EncodeToString(h.Sum(nil))
}

// writeLenPrefixed writes 8-byte big-endian length followed by the field
// bytes, so adjacent variable-length fields cannot have ambiguous boundaries.
func writeLenPrefixed(h interface{ Write([]byte) (int, error) }, s string) {
	var lenBuf [8]byte
	for i := 0; i < 8; i++ {
		lenBuf[7-i] = byte(uint64(len(s)) >> (8 * i))
	}
	_, _ = h.Write(lenBuf[:])
	_, _ = h.Write([]byte(s))
}

// CheckAndActivateModel evaluates activation thresholds and transitions a model
// from MODEL_PROPOSED to MODEL_ACTIVE if all conditions are met.
func (k Keeper) CheckAndActivateModel(ctx sdk.Context, modelID string) bool {
	model, found := k.GetModel(ctx, modelID)
	if !found {
		return false
	}
	if model.Status == types.ModelStatusActive {
		return true
	}

	params := k.GetParams(ctx)
	if model.InstalledStakeRatio >= params.ActivationStakeRatio &&
		model.WorkerCount >= params.MinEligibleWorkers &&
		model.OperatorCount >= params.MinUniqueOperators {

		model.Status = types.ModelStatusActive
		model.ActivatedAt = ctx.BlockHeight()
		k.SetModel(ctx, model)

		ctx.EventManager().EmitEvent(sdk.NewEvent(
			types.EventModelActivated,
			sdk.NewAttribute(types.AttributeKeyModelId, model.ModelId),
			sdk.NewAttribute(types.AttributeKeyModelName, model.Name),
			sdk.NewAttribute(types.AttributeKeyStatus, types.ModelStatusActive.String()),
		))
		return true
	}
	return false
}

// CheckServiceStatus emits events when a model's service eligibility changes.
func (k Keeper) CheckServiceStatus(ctx sdk.Context, model types.Model, previousCanServe bool) {
	params := k.GetParams(ctx)
	currentCanServe := model.CanServe(params.MinServiceWorkerCount, params.ServiceStakeRatio)
	if previousCanServe && !currentCanServe {
		ctx.EventManager().EmitEvent(sdk.NewEvent(
			types.EventModelServicePaused,
			sdk.NewAttribute(types.AttributeKeyModelId, model.ModelId),
			sdk.NewAttribute(types.AttributeKeyStakeRatio, fmt.Sprintf("%.6f", model.InstalledStakeRatio)),
		))
	} else if !previousCanServe && currentCanServe {
		ctx.EventManager().EmitEvent(sdk.NewEvent(
			types.EventModelServiceResumed,
			sdk.NewAttribute(types.AttributeKeyModelId, model.ModelId),
			sdk.NewAttribute(types.AttributeKeyStakeRatio, fmt.Sprintf("%.6f", model.InstalledStakeRatio)),
		))
	}
}

// -------- Worker→Model Reverse Index --------

// SetWorkerInstalledModel records that a worker installed a model (reverse index).
func (k Keeper) SetWorkerInstalledModel(ctx sdk.Context, workerAddr sdk.AccAddress, modelID string) {
	store := ctx.KVStore(k.storeKey)
	store.Set(types.WorkerInstalledModelKey(workerAddr, modelID), []byte{1})
}

// HasWorkerInstalledModel checks if a worker has already declared a model installed.
func (k Keeper) HasWorkerInstalledModel(ctx sdk.Context, workerAddr sdk.AccAddress, modelID string) bool {
	store := ctx.KVStore(k.storeKey)
	return store.Has(types.WorkerInstalledModelKey(workerAddr, modelID))
}

// RemoveWorkerInstalledModel removes a single reverse index entry.
func (k Keeper) RemoveWorkerInstalledModel(ctx sdk.Context, workerAddr sdk.AccAddress, modelID string) {
	store := ctx.KVStore(k.storeKey)
	store.Delete(types.WorkerInstalledModelKey(workerAddr, modelID))
}

// GetWorkerInstalledModels returns all model_ids installed by a worker.
func (k Keeper) GetWorkerInstalledModels(ctx sdk.Context, workerAddr sdk.AccAddress) []string {
	store := ctx.KVStore(k.storeKey)
	prefix := types.WorkerInstalledModelIteratorPrefix(workerAddr)
	iter := storetypes.KVStorePrefixIterator(store, prefix)
	defer iter.Close()

	var modelIDs []string
	for ; iter.Valid(); iter.Next() {
		key := iter.Key()
		modelID := string(key[len(prefix):])
		modelIDs = append(modelIDs, modelID)
	}
	return modelIDs
}

// RemoveAllWorkerInstalledModels removes all reverse index entries for a worker.
func (k Keeper) RemoveAllWorkerInstalledModels(ctx sdk.Context, workerAddr sdk.AccAddress) []string {
	modelIDs := k.GetWorkerInstalledModels(ctx, workerAddr)
	for _, modelID := range modelIDs {
		k.RemoveWorkerInstalledModel(ctx, workerAddr, modelID)
	}
	return modelIDs
}

// GetInstalledWorkersByModel returns all worker addresses that installed a given model.
// Uses the reverse index to collect workers, then filters by model_id.
func (k Keeper) GetInstalledWorkersByModel(ctx sdk.Context, modelID string) []sdk.AccAddress {
	store := ctx.KVStore(k.storeKey)
	// We need to iterate all reverse index entries — there's no model→worker index in modelreg.
	// Iterate the entire WorkerInstalledModelPrefix and filter by modelID suffix.
	iter := storetypes.KVStorePrefixIterator(store, types.WorkerInstalledModelPrefix)
	defer iter.Close()

	var addrs []sdk.AccAddress
	for ; iter.Valid(); iter.Next() {
		key := iter.Key()
		// Key layout: prefix | addr_bytes | 0x00 | model_id_bytes
		// Find the separator (0x00 after the prefix)
		rest := key[len(types.WorkerInstalledModelPrefix):]
		sepIdx := -1
		for i, b := range rest {
			if b == 0x00 {
				sepIdx = i
				break
			}
		}
		if sepIdx < 0 {
			continue
		}
		mid := string(rest[sepIdx+1:])
		if mid == modelID {
			addr := sdk.AccAddress(rest[:sepIdx])
			addrs = append(addrs, addr)
		}
	}
	return addrs
}

// RefreshModelStats recalculates a model's InstalledStakeRatio, WorkerCount, and OperatorCount
// based on the current state of workers that installed this model.
func (k Keeper) RefreshModelStats(ctx sdk.Context, modelID string) {
	model, found := k.GetModel(ctx, modelID)
	if !found {
		return
	}

	params := k.GetParams(ctx)
	previousCanServe := model.CanServe(params.MinServiceWorkerCount, params.ServiceStakeRatio)

	installedAddrs := k.GetInstalledWorkersByModel(ctx, modelID)

	totalStake := k.workerKeeper.GetActiveWorkerStake(ctx)
	installedStake := math.ZeroInt()
	var workerCount uint32
	opSet := make(map[string]struct{})

	for _, addr := range installedAddrs {
		if !k.workerKeeper.IsWorkerActive(ctx, addr) {
			continue
		}
		stake := k.workerKeeper.GetWorkerStake(ctx, addr)
		installedStake = installedStake.Add(stake)
		workerCount++
		opID := k.workerKeeper.GetWorkerOperatorId(ctx, addr)
		if opID != "" {
			opSet[opID] = struct{}{}
		}
	}

	if totalStake.IsPositive() {
		// Use float64 division for ratio (consistent with existing code)
		model.InstalledStakeRatio = float64(installedStake.Int64()) / float64(totalStake.Int64())
	} else {
		model.InstalledStakeRatio = 0
	}
	model.WorkerCount = workerCount
	model.OperatorCount = uint32(len(opSet))

	k.SetModel(ctx, model)
	k.CheckAndActivateModel(ctx, modelID)
	k.CheckServiceStatus(ctx, model, previousCanServe)
}

// ---- P3-2: Model Real-Time Statistics ----

// ModelInferenceStats holds inference statistics to update for a model.
type ModelInferenceStats struct {
	TaskCount      uint64
	SuccessCount   uint64
	TotalLatencyMs uint64 // sum of latencies for averaging
}

// UpdateModelStats updates a model's real-time inference statistics (P3-2).
// Called at epoch boundaries by the settlement module.
func (k Keeper) UpdateModelStats(ctx sdk.Context, modelID string, stats ModelInferenceStats, epoch int64) {
	model, found := k.GetModel(ctx, modelID)
	if !found {
		return
	}

	// Update stats if this is a new epoch
	if epoch > model.LastStatsEpoch {
		model.TpsLastEpoch = uint32(stats.TaskCount)
		model.TotalTasks24h += stats.TaskCount
		if stats.TaskCount > 0 {
			model.AvgLatencyMs = stats.TotalLatencyMs / stats.TaskCount
		}
		model.LastStatsEpoch = epoch
		k.SetModel(ctx, model)
	}
}

// RecordModelTask records a single task completion for real-time stats.
func (k Keeper) RecordModelTask(ctx sdk.Context, modelID string, fee uint64, latencyMs uint64) {
	model, found := k.GetModel(ctx, modelID)
	if !found {
		return
	}
	model.TotalTasks24h++
	if model.TotalTasks24h > 0 {
		prev := model.TotalTasks24h - 1
		model.AvgFee = ((model.AvgFee * prev) + fee) / model.TotalTasks24h
		if latencyMs > 0 {
			model.AvgLatencyMs = ((model.AvgLatencyMs * prev) + latencyMs) / model.TotalTasks24h
		}
	}
	k.SetModel(ctx, model)
}

// -------- Worker State Change Callbacks --------

// OnWorkerStateChange is called when a worker's state changes (jail, unjail, slash, stake change).
// Refreshes stats for all models the worker has installed.
func (k Keeper) OnWorkerStateChange(ctx sdk.Context, workerAddr sdk.AccAddress) {
	modelIDs := k.GetWorkerInstalledModels(ctx, workerAddr)
	for _, modelID := range modelIDs {
		k.RefreshModelStats(ctx, modelID)
	}
}

// OnWorkerRemoved is called when a worker is permanently deleted (exit complete).
// Cleans up reverse index entries and refreshes affected model stats.
func (k Keeper) OnWorkerRemoved(ctx sdk.Context, workerAddr sdk.AccAddress) {
	modelIDs := k.RemoveAllWorkerInstalledModels(ctx, workerAddr)
	for _, modelID := range modelIDs {
		k.RefreshModelStats(ctx, modelID)
	}
}
