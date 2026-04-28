package keeper

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"
	"github.com/cometbft/cometbft/crypto/secp256k1"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	sdkerrors "cosmossdk.io/errors"

	"github.com/funai-wiki/funai-chain/x/settlement/types"
)

type BankKeeper interface {
	SendCoins(ctx context.Context, fromAddr, toAddr sdk.AccAddress, amt sdk.Coins) error
	SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipientAddr sdk.AccAddress, amt sdk.Coins) error
}

type WorkerKeeper interface {
	JailWorker(ctx sdk.Context, workerAddr sdk.AccAddress, jailDuration int64)
	SlashWorker(ctx sdk.Context, workerAddr sdk.AccAddress, percent uint32)
	SlashWorkerTo(ctx sdk.Context, workerAddr sdk.AccAddress, percent uint32, recipient sdk.AccAddress)
	IncrementSuccessStreak(ctx sdk.Context, workerAddr sdk.AccAddress)
	GetSuccessStreak(ctx sdk.Context, workerAddr sdk.AccAddress) uint32
	UpdateWorkerStats(ctx sdk.Context, workerAddr sdk.AccAddress, feeEarned sdk.Coin)
	GetWorkerPubkey(ctx sdk.Context, workerAddr sdk.AccAddress) (string, bool)
	TombstoneWorker(ctx sdk.Context, workerAddr sdk.AccAddress)
	// Audit KT §3+§5: reputation and latency updates
	ReputationOnAccept(ctx sdk.Context, addr sdk.AccAddress)
	UpdateAvgLatency(ctx sdk.Context, addr sdk.AccAddress, latencyMs uint32)
}

type ModelRegKeeper interface {
	RecordModelTask(ctx sdk.Context, modelID string, fee uint64, latencyMs uint64)
}

type Keeper struct {
	cdc            codec.BinaryCodec
	storeKey       storetypes.StoreKey
	bankKeeper     BankKeeper
	workerKeeper   WorkerKeeper
	modelRegKeeper ModelRegKeeper
	authority      string
	logger         log.Logger
}

func NewKeeper(
	cdc codec.BinaryCodec,
	storeKey storetypes.StoreKey,
	bankKeeper BankKeeper,
	workerKeeper WorkerKeeper,
	authority string,
	logger log.Logger,
) Keeper {
	return Keeper{
		cdc:          cdc,
		storeKey:     storeKey,
		bankKeeper:   bankKeeper,
		workerKeeper: workerKeeper,
		authority:    authority,
		logger:       logger.With("module", "x/"+types.ModuleName),
	}
}

func (k Keeper) Logger() log.Logger   { return k.logger }
func (k Keeper) GetAuthority() string { return k.authority }

func (k *Keeper) SetModelRegKeeper(mrk ModelRegKeeper) {
	k.modelRegKeeper = mrk
}

// -------- InferenceAccount CRUD --------

func (k Keeper) SetInferenceAccount(ctx sdk.Context, ia types.InferenceAccount) {
	store := ctx.KVStore(k.storeKey)
	addr, _ := sdk.AccAddressFromBech32(ia.Address)
	bz, _ := json.Marshal(ia)
	store.Set(types.InferenceAccountKey(addr), bz)
}

func (k Keeper) GetInferenceAccount(ctx sdk.Context, addr sdk.AccAddress) (types.InferenceAccount, bool) {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.InferenceAccountKey(addr))
	if bz == nil {
		return types.InferenceAccount{}, false
	}
	var ia types.InferenceAccount
	if err := json.Unmarshal(bz, &ia); err != nil {
		return types.InferenceAccount{}, false
	}
	return ia, true
}

func (k Keeper) GetAllInferenceAccounts(ctx sdk.Context) []types.InferenceAccount {
	store := ctx.KVStore(k.storeKey)
	iter := storetypes.KVStorePrefixIterator(store, types.InferenceAccountKeyPrefix)
	defer iter.Close()

	var accounts []types.InferenceAccount
	for ; iter.Valid(); iter.Next() {
		var ia types.InferenceAccount
		if err := json.Unmarshal(iter.Value(), &ia); err != nil {
			continue
		}
		accounts = append(accounts, ia)
	}
	return accounts
}

// -------- SettledTaskID CRUD --------

func (k Keeper) SetSettledTask(ctx sdk.Context, st types.SettledTaskID) {
	store := ctx.KVStore(k.storeKey)
	bz, _ := json.Marshal(st)
	store.Set(types.SettledTaskKey(st.TaskId), bz)
}

func (k Keeper) GetSettledTask(ctx sdk.Context, taskID []byte) (types.SettledTaskID, bool) {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.SettledTaskKey(taskID))
	if bz == nil {
		return types.SettledTaskID{}, false
	}
	var st types.SettledTaskID
	if err := json.Unmarshal(bz, &st); err != nil {
		return types.SettledTaskID{}, false
	}
	return st, true
}

func (k Keeper) DeleteSettledTask(ctx sdk.Context, taskID []byte) {
	store := ctx.KVStore(k.storeKey)
	store.Delete(types.SettledTaskKey(taskID))
}

// -------- BatchRecord CRUD --------

func (k Keeper) SetBatchRecord(ctx sdk.Context, br types.BatchRecord) {
	store := ctx.KVStore(k.storeKey)
	bz, _ := json.Marshal(br)
	store.Set(types.BatchRecordKey(br.BatchId), bz)
}

func (k Keeper) GetBatchRecord(ctx sdk.Context, batchId uint64) (types.BatchRecord, bool) {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.BatchRecordKey(batchId))
	if bz == nil {
		return types.BatchRecord{}, false
	}
	var br types.BatchRecord
	if err := json.Unmarshal(bz, &br); err != nil {
		return types.BatchRecord{}, false
	}
	return br, true
}

func (k Keeper) GetNextBatchId(ctx sdk.Context) uint64 {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.BatchCounterKey)
	if bz == nil {
		return 1
	}
	return binary.BigEndian.Uint64(bz) + 1
}

func (k Keeper) SetBatchCounter(ctx sdk.Context, batchId uint64) {
	store := ctx.KVStore(k.storeKey)
	bz := make([]byte, 8)
	binary.BigEndian.PutUint64(bz, batchId)
	store.Set(types.BatchCounterKey, bz)
}

// -------- FraudMark CRUD --------

func (k Keeper) SetFraudMark(ctx sdk.Context, taskID []byte) {
	store := ctx.KVStore(k.storeKey)
	store.Set(types.FraudMarkKey(taskID), []byte{1})
}

func (k Keeper) HasFraudMark(ctx sdk.Context, taskID []byte) bool {
	store := ctx.KVStore(k.storeKey)
	return store.Has(types.FraudMarkKey(taskID))
}

// -------- SecondVerificationRecord CRUD --------

func (k Keeper) SetSecondVerificationRecord(ctx sdk.Context, ar types.SecondVerificationRecord) {
	store := ctx.KVStore(k.storeKey)
	bz, _ := json.Marshal(ar)
	store.Set(types.SecondVerificationRecordKey(ar.TaskId), bz)
}

func (k Keeper) GetSecondVerificationRecord(ctx sdk.Context, taskID []byte) (types.SecondVerificationRecord, bool) {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.SecondVerificationRecordKey(taskID))
	if bz == nil {
		return types.SecondVerificationRecord{}, false
	}
	var ar types.SecondVerificationRecord
	if err := json.Unmarshal(bz, &ar); err != nil {
		return types.SecondVerificationRecord{}, false
	}
	return ar, true
}

// GetAllSecondVerificationRecords returns all audit records (for P1-9 audit fund distribution).
func (k Keeper) GetAllSecondVerificationRecords(ctx sdk.Context) []types.SecondVerificationRecord {
	store := ctx.KVStore(k.storeKey)
	iter := storetypes.KVStorePrefixIterator(store, types.SecondVerificationRecordKeyPrefix)
	defer iter.Close()

	var records []types.SecondVerificationRecord
	for ; iter.Valid(); iter.Next() {
		var ar types.SecondVerificationRecord
		if err := json.Unmarshal(iter.Value(), &ar); err != nil {
			continue
		}
		records = append(records, ar)
	}
	return records
}

// -------- SecondVerificationPendingTask CRUD --------

func (k Keeper) SetSecondVerificationPending(ctx sdk.Context, apt types.SecondVerificationPendingTask) {
	store := ctx.KVStore(k.storeKey)
	bz, _ := json.Marshal(apt)
	prefix := types.SecondVerificationPendingKeyPrefix
	if apt.IsThirdVerification {
		prefix = types.ThirdVerificationPendingKeyPrefix
	}
	store.Set(append(prefix, apt.TaskId...), bz)

	// Maintain height-indexed timeout key for efficient timeout scanning
	if apt.IsThirdVerification {
		store.Set(types.ThirdVerificationPendingTimeoutKey(apt.SubmittedAt, apt.TaskId), []byte{1})
	} else {
		store.Set(types.SecondVerificationPendingTimeoutKey(apt.SubmittedAt, apt.TaskId), []byte{1})
	}
}

func (k Keeper) GetSecondVerificationPending(ctx sdk.Context, taskID []byte) (types.SecondVerificationPendingTask, bool) {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.SecondVerificationPendingKey(taskID))
	if bz == nil {
		return types.SecondVerificationPendingTask{}, false
	}
	var apt types.SecondVerificationPendingTask
	if err := json.Unmarshal(bz, &apt); err != nil {
		return types.SecondVerificationPendingTask{}, false
	}
	return apt, true
}

func (k Keeper) DeleteSecondVerificationPending(ctx sdk.Context, taskID []byte, isThirdVerification bool) {
	store := ctx.KVStore(k.storeKey)
	if isThirdVerification {
		// Look up submittedAt from the pending task before deleting
		if apt, found := k.getSecondVerificationPendingByKey(ctx, types.ThirdVerificationPendingKey(taskID)); found {
			store.Delete(types.ThirdVerificationPendingTimeoutKey(apt.SubmittedAt, taskID))
		}
		store.Delete(types.ThirdVerificationPendingKey(taskID))
	} else {
		if apt, found := k.getSecondVerificationPendingByKey(ctx, types.SecondVerificationPendingKey(taskID)); found {
			store.Delete(types.SecondVerificationPendingTimeoutKey(apt.SubmittedAt, taskID))
		}
		store.Delete(types.SecondVerificationPendingKey(taskID))
	}
}

func (k Keeper) getSecondVerificationPendingByKey(ctx sdk.Context, key []byte) (types.SecondVerificationPendingTask, bool) {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(key)
	if bz == nil {
		return types.SecondVerificationPendingTask{}, false
	}
	var apt types.SecondVerificationPendingTask
	if err := json.Unmarshal(bz, &apt); err != nil {
		return types.SecondVerificationPendingTask{}, false
	}
	return apt, true
}

func (k Keeper) GetAllSecondVerificationPending(ctx sdk.Context) []types.SecondVerificationPendingTask {
	store := ctx.KVStore(k.storeKey)
	iter := storetypes.KVStorePrefixIterator(store, types.SecondVerificationPendingKeyPrefix)
	defer iter.Close()

	var tasks []types.SecondVerificationPendingTask
	for ; iter.Valid(); iter.Next() {
		var apt types.SecondVerificationPendingTask
		if err := json.Unmarshal(iter.Value(), &apt); err != nil {
			continue
		}
		tasks = append(tasks, apt)
	}
	return tasks
}

func (k Keeper) GetAllThirdVerificationPending(ctx sdk.Context) []types.SecondVerificationPendingTask {
	store := ctx.KVStore(k.storeKey)
	iter := storetypes.KVStorePrefixIterator(store, types.ThirdVerificationPendingKeyPrefix)
	defer iter.Close()

	var tasks []types.SecondVerificationPendingTask
	for ; iter.Valid(); iter.Next() {
		var apt types.SecondVerificationPendingTask
		if err := json.Unmarshal(iter.Value(), &apt); err != nil {
			continue
		}
		tasks = append(tasks, apt)
	}
	return tasks
}

// -------- EpochStats CRUD --------

func (k Keeper) SetEpochStats(ctx sdk.Context, stats types.EpochStats) {
	store := ctx.KVStore(k.storeKey)
	bz, _ := json.Marshal(stats)
	store.Set(types.EpochStatsKey(stats.Epoch), bz)
}

func (k Keeper) GetEpochStats(ctx sdk.Context, epoch int64) types.EpochStats {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.EpochStatsKey(epoch))
	if bz == nil {
		return types.DefaultEpochStats(epoch)
	}
	var stats types.EpochStats
	if err := json.Unmarshal(bz, &stats); err != nil {
		return types.DefaultEpochStats(epoch)
	}
	return stats
}

// -------- Per-Worker Epoch Contribution (P1-8) --------

func (k Keeper) SetWorkerSnapshot(ctx sdk.Context, workerAddr sdk.AccAddress, snapshot types.WorkerSnapshot) {
	store := ctx.KVStore(k.storeKey)
	bz, _ := json.Marshal(snapshot)
	store.Set(types.WorkerSnapshotKey(workerAddr), bz)
}

func (k Keeper) GetWorkerSnapshot(ctx sdk.Context, workerAddr sdk.AccAddress) types.WorkerSnapshot {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.WorkerSnapshotKey(workerAddr))
	if bz == nil {
		return types.WorkerSnapshot{TotalFeeEarned: math.ZeroInt(), TotalTasks: 0}
	}
	var snapshot types.WorkerSnapshot
	if err := json.Unmarshal(bz, &snapshot); err != nil {
		return types.WorkerSnapshot{TotalFeeEarned: math.ZeroInt(), TotalTasks: 0}
	}
	return snapshot
}

func (k Keeper) SetEpochContribution(ctx sdk.Context, workerAddr sdk.AccAddress, contrib types.WorkerEpochContribution) {
	store := ctx.KVStore(k.storeKey)
	bz, _ := json.Marshal(contrib)
	store.Set(types.WorkerEpochContribKey(workerAddr), bz)
}

func (k Keeper) GetAllEpochContributions(ctx sdk.Context) []types.WorkerEpochContribution {
	store := ctx.KVStore(k.storeKey)
	iter := storetypes.KVStorePrefixIterator(store, types.WorkerEpochContribKeyPrefix)
	defer iter.Close()

	var contribs []types.WorkerEpochContribution
	for ; iter.Valid(); iter.Next() {
		var c types.WorkerEpochContribution
		if err := json.Unmarshal(iter.Value(), &c); err == nil {
			contribs = append(contribs, c)
		}
	}
	return contribs
}

func (k Keeper) ClearEpochContributions(ctx sdk.Context) {
	store := ctx.KVStore(k.storeKey)
	iter := storetypes.KVStorePrefixIterator(store, types.WorkerEpochContribKeyPrefix)
	defer iter.Close()

	var keys [][]byte
	for ; iter.Valid(); iter.Next() {
		keys = append(keys, iter.Key())
	}
	for _, key := range keys {
		store.Delete(key)
	}
}

// -------- Per-Worker Verification/Audit Epoch Counts (P1-9) --------

func (k Keeper) IncrementVerifierEpochCount(ctx sdk.Context, verifierAddr string) {
	addr, err := sdk.AccAddressFromBech32(verifierAddr)
	if err != nil {
		return
	}
	store := ctx.KVStore(k.storeKey)
	key := types.VerifierEpochCountKey(addr)
	count := uint64(0)
	bz := store.Get(key)
	if len(bz) == 8 {
		count = binary.BigEndian.Uint64(bz)
	}
	count++
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, count)
	store.Set(key, buf)
}

func (k Keeper) IncrementSecondVerifierEpochCount(ctx sdk.Context, second_verifierAddr string) {
	addr, err := sdk.AccAddressFromBech32(second_verifierAddr)
	if err != nil {
		return
	}
	store := ctx.KVStore(k.storeKey)
	key := types.SecondVerifierEpochCountKey(addr)
	count := uint64(0)
	bz := store.Get(key)
	if len(bz) == 8 {
		count = binary.BigEndian.Uint64(bz)
	}
	count++
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, count)
	store.Set(key, buf)
}

// IncrementVerifierEpochFee adds to a verifier's epoch fee total. Used for the
// amount-weighted portion of verifier block reward distribution (85/15 weighting).
func (k Keeper) IncrementVerifierEpochFee(ctx sdk.Context, verifierAddr string, amount math.Int) {
	if amount.IsNil() || !amount.IsPositive() {
		return
	}
	addr, err := sdk.AccAddressFromBech32(verifierAddr)
	if err != nil {
		return
	}
	store := ctx.KVStore(k.storeKey)
	key := types.VerifierEpochFeeKey(addr)
	total := math.ZeroInt()
	if bz := store.Get(key); len(bz) > 0 {
		_ = total.Unmarshal(bz)
	}
	total = total.Add(amount)
	if out, err := total.Marshal(); err == nil {
		store.Set(key, out)
	}
}

// IncrementSecondVerifierEpochFee adds to a 2nd/3rd-verifier's epoch fee total.
func (k Keeper) IncrementSecondVerifierEpochFee(ctx sdk.Context, second_verifierAddr string, amount math.Int) {
	if amount.IsNil() || !amount.IsPositive() {
		return
	}
	addr, err := sdk.AccAddressFromBech32(second_verifierAddr)
	if err != nil {
		return
	}
	store := ctx.KVStore(k.storeKey)
	key := types.SecondVerifierEpochFeeKey(addr)
	total := math.ZeroInt()
	if bz := store.Get(key); len(bz) > 0 {
		_ = total.Unmarshal(bz)
	}
	total = total.Add(amount)
	if out, err := total.Marshal(); err == nil {
		store.Set(key, out)
	}
}

// VerifierSecondVerifierEpochCounts holds per-worker verification and 2nd/3rd-verification
// counts AND fees for an epoch. Fees are used for the 85% amount-weight in block
// reward distribution; counts for the 15% count-weight.
type VerifierSecondVerifierEpochCounts struct {
	Address           string
	VerificationCount uint64
	AuditCount        uint64
	VerificationFee   math.Int
	AuditFee          math.Int
}

// TotalFee returns total fees earned across verification + 2nd/3rd-verification roles.
func (c VerifierSecondVerifierEpochCounts) TotalFee() math.Int {
	vFee := c.VerificationFee
	if vFee.IsNil() {
		vFee = math.ZeroInt()
	}
	aFee := c.AuditFee
	if aFee.IsNil() {
		aFee = math.ZeroInt()
	}
	return vFee.Add(aFee)
}

func (k Keeper) GetAllVerifierSecondVerifierEpochCounts(ctx sdk.Context) []VerifierSecondVerifierEpochCounts {
	store := ctx.KVStore(k.storeKey)
	merged := make(map[string]*VerifierSecondVerifierEpochCounts)
	ensure := func(addr string) *VerifierSecondVerifierEpochCounts {
		if _, ok := merged[addr]; !ok {
			merged[addr] = &VerifierSecondVerifierEpochCounts{
				Address:         addr,
				VerificationFee: math.ZeroInt(),
				AuditFee:        math.ZeroInt(),
			}
		}
		return merged[addr]
	}

	// Verification counts
	vIter := storetypes.KVStorePrefixIterator(store, types.VerifierEpochCountKeyPrefix)
	for ; vIter.Valid(); vIter.Next() {
		addr := sdk.AccAddress(vIter.Key()[len(types.VerifierEpochCountKeyPrefix):]).String()
		count := binary.BigEndian.Uint64(vIter.Value())
		ensure(addr).VerificationCount = count
	}
	vIter.Close()

	// 2nd/3rd-verification counts
	aIter := storetypes.KVStorePrefixIterator(store, types.SecondVerifierEpochCountKeyPrefix)
	for ; aIter.Valid(); aIter.Next() {
		addr := sdk.AccAddress(aIter.Key()[len(types.SecondVerifierEpochCountKeyPrefix):]).String()
		count := binary.BigEndian.Uint64(aIter.Value())
		ensure(addr).AuditCount = count
	}
	aIter.Close()

	// Verification fees
	vfIter := storetypes.KVStorePrefixIterator(store, types.VerifierEpochFeeKeyPrefix)
	for ; vfIter.Valid(); vfIter.Next() {
		addr := sdk.AccAddress(vfIter.Key()[len(types.VerifierEpochFeeKeyPrefix):]).String()
		amt := math.ZeroInt()
		_ = amt.Unmarshal(vfIter.Value())
		ensure(addr).VerificationFee = amt
	}
	vfIter.Close()

	// 2nd/3rd-verification fees
	afIter := storetypes.KVStorePrefixIterator(store, types.SecondVerifierEpochFeeKeyPrefix)
	for ; afIter.Valid(); afIter.Next() {
		addr := sdk.AccAddress(afIter.Key()[len(types.SecondVerifierEpochFeeKeyPrefix):]).String()
		amt := math.ZeroInt()
		_ = amt.Unmarshal(afIter.Value())
		ensure(addr).AuditFee = amt
	}
	afIter.Close()

	var result []VerifierSecondVerifierEpochCounts
	for _, v := range merged {
		result = append(result, *v)
	}
	return result
}

func (k Keeper) ClearVerifierSecondVerifierEpochCounts(ctx sdk.Context) {
	store := ctx.KVStore(k.storeKey)
	prefixes := [][]byte{
		types.VerifierEpochCountKeyPrefix, types.SecondVerifierEpochCountKeyPrefix,
		types.VerifierEpochFeeKeyPrefix, types.SecondVerifierEpochFeeKeyPrefix,
	}
	for _, prefix := range prefixes {
		iter := storetypes.KVStorePrefixIterator(store, prefix)
		var keys [][]byte
		for ; iter.Valid(); iter.Next() {
			keys = append(keys, iter.Key())
		}
		iter.Close()
		for _, key := range keys {
			store.Delete(key)
		}
	}
}

// WorkerStatsInfo holds the minimal worker info needed for epoch contribution snapshots.
type WorkerStatsInfo struct {
	Address        string
	TotalFeeEarned math.Int
	TotalTasks     int64
}

// SnapshotAndComputeEpochContributions snapshots current worker stats and computes
// per-epoch contributions by diffing with previous snapshots. Called at epoch boundary.
// P1-8: ensures reward distribution uses per-epoch deltas, not cumulative values.
func (k Keeper) SnapshotAndComputeEpochContributions(ctx sdk.Context, workers []WorkerStatsInfo) {
	// Clear previous epoch contributions
	k.ClearEpochContributions(ctx)

	for _, w := range workers {
		workerAddr, err := sdk.AccAddressFromBech32(w.Address)
		if err != nil {
			continue
		}
		snapshot := k.GetWorkerSnapshot(ctx, workerAddr)

		epochFee := w.TotalFeeEarned.Sub(snapshot.TotalFeeEarned)
		epochTasks := w.TotalTasks - snapshot.TotalTasks

		if epochFee.IsPositive() || epochTasks > 0 {
			k.SetEpochContribution(ctx, workerAddr, types.WorkerEpochContribution{
				WorkerAddress: w.Address,
				FeeAmount:     epochFee,
				TaskCount:     uint64(epochTasks),
			})
		}

		// Update snapshot to current values
		k.SetWorkerSnapshot(ctx, workerAddr, types.WorkerSnapshot{
			TotalFeeEarned: w.TotalFeeEarned,
			TotalTasks:     w.TotalTasks,
		})
	}
}

// -------- Block Signer Tracking (P1-10) --------

// AccumulateBlockSigners records which validators signed the current block.
// Called from BeginBlocker to accumulate per-epoch signing counts.
func (k Keeper) AccumulateBlockSigners(ctx sdk.Context) {
	cometInfo := ctx.CometInfo()
	if cometInfo == nil {
		return
	}

	accumulated := 0
	lastCommit := cometInfo.GetLastCommit()
	votes := lastCommit.Votes()
	for i := 0; i < votes.Len(); i++ {
		vote := votes.Get(i)
		// BlockIDFlagCommit = 2 means the validator signed the block
		flag := vote.GetBlockIDFlag()
		if flag == 2 {
			// Use AccAddress (not ConsAddress) so reward distribution can
			// send coins directly without staking keeper lookup.
			addr := sdk.AccAddress(vote.Validator().Address()).String()
			k.incrementBlockSignerCount(ctx, addr)
			accumulated++
		}
	}

	// Fallback for SDK v0.50 ABCI 2.0: if no votes found via CometInfo,
	// credit the block proposer (from header) as the signer.
	if accumulated == 0 {
		proposer := ctx.BlockHeader().ProposerAddress
		if len(proposer) > 0 {
			addr := sdk.AccAddress(proposer).String()
			k.incrementBlockSignerCount(ctx, addr)
		}
	}
}

func (k Keeper) incrementBlockSignerCount(ctx sdk.Context, validatorAddr string) {
	store := ctx.KVStore(k.storeKey)
	key := types.BlockSignerCountKey(validatorAddr)
	count := uint64(0)
	bz := store.Get(key)
	if len(bz) == 8 {
		count = binary.BigEndian.Uint64(bz)
	}
	count++
	buf := make([]byte, 8)
	binary.BigEndian.PutUint64(buf, count)
	store.Set(key, buf)
}

// GetAndClearBlockSignerCounts returns all accumulated signing counts and clears them.
func (k Keeper) GetAndClearBlockSignerCounts(ctx sdk.Context) map[string]uint64 {
	store := ctx.KVStore(k.storeKey)
	iter := storetypes.KVStorePrefixIterator(store, types.BlockSignerCountKeyPrefix)
	defer iter.Close()

	counts := make(map[string]uint64)
	var keys [][]byte
	for ; iter.Valid(); iter.Next() {
		addr := string(iter.Key()[len(types.BlockSignerCountKeyPrefix):])
		count := binary.BigEndian.Uint64(iter.Value())
		counts[addr] = count
		keys = append(keys, iter.Key())
	}
	for _, key := range keys {
		store.Delete(key)
	}
	return counts
}

// -------- Audit Rate Storage --------

func (k Keeper) SetCurrentSecondVerificationRate(ctx sdk.Context, rate uint32) {
	store := ctx.KVStore(k.storeKey)
	bz := make([]byte, 4)
	binary.BigEndian.PutUint32(bz, rate)
	store.Set(types.SecondVerificationRateKey, bz)
}

func (k Keeper) GetCurrentSecondVerificationRate(ctx sdk.Context) uint32 {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.SecondVerificationRateKey)
	if bz == nil {
		return types.DefaultSecondVerificationBaseRate
	}
	return binary.BigEndian.Uint32(bz)
}

func (k Keeper) SetCurrentThirdVerificationRate(ctx sdk.Context, rate uint32) {
	store := ctx.KVStore(k.storeKey)
	bz := make([]byte, 4)
	binary.BigEndian.PutUint32(bz, rate)
	store.Set(types.ThirdVerificationRateKey, bz)
}

func (k Keeper) GetCurrentThirdVerificationRate(ctx sdk.Context) uint32 {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.ThirdVerificationRateKey)
	if bz == nil {
		return types.DefaultThirdVerificationBaseRate
	}
	return binary.BigEndian.Uint32(bz)
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

func (k Keeper) ProcessDeposit(ctx sdk.Context, creator sdk.AccAddress, amount sdk.Coin) error {
	if amount.Denom != types.DefaultDenom {
		return sdkerrors.Wrapf(types.ErrWrongDenom, "got %s", amount.Denom)
	}
	coins := sdk.NewCoins(amount)
	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, creator, types.ModuleAccountName, coins); err != nil {
		return types.ErrInsufficientBalance
	}

	ia, found := k.GetInferenceAccount(ctx, creator)
	if !found {
		ia = types.InferenceAccount{
			Address: creator.String(),
			Balance: amount,
		}
	} else {
		ia.Balance = ia.Balance.Add(amount)
	}

	k.SetInferenceAccount(ctx, ia)
	return nil
}

func (k Keeper) ProcessWithdraw(ctx sdk.Context, creator sdk.AccAddress, amount sdk.Coin) error {
	if amount.Denom != types.DefaultDenom {
		return sdkerrors.Wrapf(types.ErrWrongDenom, "got %s", amount.Denom)
	}
	ia, found := k.GetInferenceAccount(ctx, creator)
	if !found {
		return types.ErrAccountNotFound
	}

	// A1 fix: check AvailableBalance (Balance - FrozenBalance) to prevent
	// withdrawing funds frozen by active per-token inference tasks.
	if ia.AvailableBalance().IsLT(amount) {
		return types.ErrInsufficientBalance
	}

	ia.Balance = ia.Balance.Sub(amount)
	k.SetInferenceAccount(ctx, ia)

	coins := sdk.NewCoins(amount)
	return k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleAccountName, creator, coins)
}

// ProcessBatchSettlement processes a batch of CLEARED settlement entries.
// V5.2: entries are inline (no DA layer). Only CLEARED tasks should be included.
// Proposer merkle/count mismatch → reject + Proposer jail.
// P0-2/P1-5: verifies Proposer's secp256k1 signature on merkle root.
// P1-10: validates result_count matches actual entry count.
func (k Keeper) ProcessBatchSettlement(ctx sdk.Context, msg *types.MsgBatchSettlement) (uint64, error) {
	params := k.GetParams(ctx)
	currentHeight := ctx.BlockHeight()

	// P1-10: validate result_count matches actual entries
	if msg.ResultCount != uint32(len(msg.Entries)) {
		if k.workerKeeper != nil {
			proposerAddr, _ := sdk.AccAddressFromBech32(msg.Proposer)
			k.workerKeeper.JailWorker(ctx, proposerAddr, 0)
		}
		return 0, sdkerrors.Wrap(types.ErrInvalidSettlement, "result_count mismatch with actual entries, proposer jailed")
	}

	// P0-2/P1-5: verify Proposer's secp256k1 signature on merkle root
	// P1-6 fix: spec says batch_sig_invalid → reject batch only, no jail for signature failure.
	if err := k.verifyProposerSignature(ctx, msg); err != nil {
		return 0, sdkerrors.Wrap(types.ErrInvalidSettlement, "proposer signature verification failed")
	}

	if !VerifyMerkleRoot(msg.MerkleRoot, msg.Entries) {
		if k.workerKeeper != nil {
			proposerAddr, _ := sdk.AccAddressFromBech32(msg.Proposer)
			k.workerKeeper.JailWorker(ctx, proposerAddr, 0)
		}
		return 0, sdkerrors.Wrap(types.ErrInvalidSettlement, "merkle root verification failed, proposer jailed")
	}

	epoch := currentHeight / 100

	var successCount, failCount uint32
	totalFees := math.ZeroInt()

	for _, entry := range msg.Entries {
		// S9: per-token entries have Fee cleared; check MaxFee denom instead
		if entry.IsPerToken() {
			if entry.MaxFee.Denom != types.DefaultDenom {
				continue
			}
		} else if entry.Fee.Denom != types.DefaultDenom {
			continue
		}
		if _, found := k.GetSettledTask(ctx, entry.TaskId); found {
			continue
		}
		if k.HasFraudMark(ctx, entry.TaskId) {
			continue
		}
		if entry.ExpireBlock > 0 && entry.ExpireBlock < currentHeight {
			continue
		}

		// S2: verify signature hashes are present — entry without valid signatures is skipped
		if len(entry.UserSigHash) == 0 || len(entry.WorkerSigHash) == 0 {
			continue
		}
		if len(entry.VerifySigHashes) < 3 {
			continue
		}
		allSigsValid := true
		for _, sigHash := range entry.VerifySigHashes {
			if len(sigHash) == 0 {
				allSigsValid = false
				break
			}
		}
		if !allSigsValid {
			continue
		}

		userAddr, addrErr := sdk.AccAddressFromBech32(entry.UserAddress)
		if addrErr != nil {
			continue
		}
		workerAddr, addrErr := sdk.AccAddressFromBech32(entry.WorkerAddress)
		if addrErr != nil {
			continue
		}

		ia, found := k.GetInferenceAccount(ctx, userAddr)
		if !found {
			continue
		}

		verifierAddrs := make([]string, len(entry.VerifierResults))
		verifierVotes := make([]bool, len(entry.VerifierResults))
		for i, v := range entry.VerifierResults {
			verifierAddrs[i] = v.Address
			verifierVotes[i] = v.Pass
		}

		// S1 fix: check audit VRF BEFORE settlement — audited tasks must NOT be settled yet
		auditRate := k.GetCurrentSecondVerificationRate(ctx)
		// S9 §5.2.4: boost audit rate for workers with suspicious pair-level mismatch history
		if params.PerTokenBillingEnabled {
			boost := k.CalculateWorkerAuditBoost(ctx, entry.WorkerAddress, params)
			if boost > 0 {
				auditRate += (boost * params.SecondVerificationBaseRate) / 10
			}
			if auditRate > params.SecondVerificationRateMax {
				auditRate = params.SecondVerificationRateMax
			}
		}
		// E14: per-token entry with no verifier token data → force audit.
		// This catches TGI crashes where all verifiers return 0 output tokens.
		forceSecondVerification := false
		if entry.IsPerToken() && params.PerTokenBillingEnabled {
			var hasVerifierTokenData bool
			for _, v := range entry.VerifierResults {
				if v.VerifiedOutputTokens > 0 {
					hasVerifierTokenData = true
					break
				}
			}
			if !hasVerifierTokenData && entry.WorkerOutputTokens > 0 {
				forceSecondVerification = true
				k.logger.Info("E14: forcing audit — no verifier token data available",
					"task_id", fmt.Sprintf("%x", entry.TaskId),
					"worker_output_tokens", entry.WorkerOutputTokens)
			}
		}
		if forceSecondVerification || k.shouldTriggerSecondVerification(ctx, entry.TaskId, auditRate) {
			secondVerificationPending := types.SecondVerificationPendingTask{
				TaskId:            entry.TaskId,
				OriginalStatus:    entry.Status,
				SubmittedAt:       currentHeight,
				WorkerAddress:     entry.WorkerAddress,
				UserAddress:       entry.UserAddress,
				Fee:               entry.Fee,
				VerifierAddresses: verifierAddrs,
				VerifierVotes:     verifierVotes,
				ExpireBlock:       entry.ExpireBlock,
			}
			// S9: preserve per-token data for audit re-settlement
			if entry.IsPerToken() {
				secondVerificationPending.FeePerInputToken = entry.FeePerInputToken
				secondVerificationPending.FeePerOutputToken = entry.FeePerOutputToken
				secondVerificationPending.MaxFee = entry.MaxFee
				secondVerificationPending.SettledInputTokens = entry.WorkerInputTokens
				secondVerificationPending.SettledOutputTokens = entry.WorkerOutputTokens
			}
			k.SetSecondVerificationPending(ctx, secondVerificationPending)
			k.SetSettledTask(ctx, types.SettledTaskID{
				TaskId:            entry.TaskId,
				Status:            types.TaskPendingSecondVerification,
				ExpireBlock:       entry.ExpireBlock,
				SettledAt:         currentHeight,
				WorkerAddress:     entry.WorkerAddress,
				OriginalVerifiers: verifierAddrs,
				Fee:               entry.Fee,
				UserAddress:       entry.UserAddress,
			})
			continue
		}

		// Only count verifier contributions for CLEARED tasks (not PENDING_AUDIT)
		for _, v := range entry.VerifierResults {
			k.IncrementVerifierEpochCount(ctx, v.Address)
		}

		// S9: determine actual fee based on billing mode
		var actualFee sdk.Coin
		if entry.IsPerToken() && !params.PerTokenBillingEnabled {
			// §4.6: governance off → treat as per-request using MaxFee
			actualFee = entry.MaxFee
		} else if entry.IsPerToken() {
			resolution := ResolveTokenCounts(&entry, params.TokenCountTolerance, params.TokenCountTolerancePct)

			if resolution.WorkerDishonest && k.workerKeeper != nil {
				k.IncrementDishonestCount(ctx, workerAddr, params)
			}

			// S9 §5.2: update pair-level mismatch tracking for each verifier
			for _, v := range entry.VerifierResults {
				isMismatch := false
				if v.VerifiedOutputTokens > 0 {
					tol := effectiveTolerance(resolution.OutputTokens, params.TokenCountTolerance, params.TokenCountTolerancePct)
					var delta uint32
					if v.VerifiedOutputTokens > resolution.OutputTokens {
						delta = v.VerifiedOutputTokens - resolution.OutputTokens
					} else {
						delta = resolution.OutputTokens - v.VerifiedOutputTokens
					}
					devThreshold := resolution.OutputTokens * params.TokenMismatchDeviationPct / 100
					if delta > tol && delta > devThreshold {
						isMismatch = true
					}
				}
				k.UpdateTokenMismatchPair(ctx, entry.WorkerAddress, v.Address, isMismatch, params)
			}

			computedFee := CalculatePerTokenFee(
				resolution.InputTokens, resolution.OutputTokens,
				entry.FeePerInputToken, entry.FeePerOutputToken,
				entry.MaxFee.Amount.Uint64(),
			)
			actualFee = sdk.NewCoin(types.DefaultDenom, math.NewIntFromUint64(computedFee))

			_, _ = k.UnfreezeBalance(ctx, userAddr, entry.TaskId)
		} else {
			// Per-request billing: use fixed fee
			actualFee = entry.Fee
		}

		if entry.Status == types.SettlementSuccess {
			if ia.Balance.IsLT(actualFee) {
				continue // REFUNDED
			}
			ia.Balance = ia.Balance.Sub(actualFee)
			k.SetInferenceAccount(ctx, ia)

			k.distributeSuccessFee(ctx, actualFee, workerAddr, entry.VerifierResults, params)

			if k.workerKeeper != nil {
				k.workerKeeper.IncrementSuccessStreak(ctx, workerAddr)
				if k.workerKeeper.GetSuccessStreak(ctx, workerAddr) >= 50 {
					k.ResetDishonestCount(ctx, workerAddr)
				}
				k.workerKeeper.UpdateWorkerStats(ctx, workerAddr, actualFee)
				// Audit KT §3: successful settlement → reputation boost
				k.workerKeeper.ReputationOnAccept(ctx, workerAddr)
				// Audit KT §5: update average latency from settlement data
				if entry.LatencyMs > 0 {
					k.workerKeeper.UpdateAvgLatency(ctx, workerAddr, uint32(entry.LatencyMs))
				}
			}

			if k.modelRegKeeper != nil && entry.ModelId != "" {
				k.modelRegKeeper.RecordModelTask(ctx, entry.ModelId, actualFee.Amount.Uint64(), entry.LatencyMs)
			}

			k.SetSettledTask(ctx, types.SettledTaskID{
				TaskId:            entry.TaskId,
				Status:            types.TaskSettled,
				ExpireBlock:       entry.ExpireBlock,
				SettledAt:         currentHeight,
				WorkerAddress:     entry.WorkerAddress,
				OriginalVerifiers: verifierAddrs,
				Fee:               actualFee,
				UserAddress:       entry.UserAddress,
			})
			successCount++
			totalFees = totalFees.Add(actualFee.Amount)
		} else {
			if entry.IsPerToken() {
				// S9 §4.3: FAIL + per-token — charge fail_fee, refund rest
				_, _ = k.UnfreezeBalance(ctx, userAddr, entry.TaskId)

				failFee := actualFee.Amount.MulRaw(int64(params.FailSettlementFeeRatio)).QuoRaw(1000)
				failCoin := sdk.NewCoin(types.DefaultDenom, failFee)
				if ia.Balance.IsGTE(failCoin) && failCoin.IsPositive() {
					ia.Balance = ia.Balance.Sub(failCoin)
					k.SetInferenceAccount(ctx, ia)
					k.distributeFailFee(ctx, failCoin, entry.VerifierResults, params)
				}

				k.SetSettledTask(ctx, types.SettledTaskID{
					TaskId:            entry.TaskId,
					Status:            types.TaskFailSettled,
					ExpireBlock:       entry.ExpireBlock,
					SettledAt:         currentHeight,
					WorkerAddress:     entry.WorkerAddress,
					OriginalVerifiers: verifierAddrs,
					UserAddress:       entry.UserAddress,
				})
				if k.workerKeeper != nil {
					k.workerKeeper.JailWorker(ctx, workerAddr, 0)
				}
			} else {
				// Per-request FAIL: charge FailSettlementFeeRatio (default 15%) failFee
				failFee := sdk.NewCoin(entry.Fee.Denom, entry.Fee.Amount.MulRaw(int64(params.FailSettlementFeeRatio)).QuoRaw(1000))
				if ia.Balance.IsLT(failFee) {
					continue
				}
				ia.Balance = ia.Balance.Sub(failFee)
				k.SetInferenceAccount(ctx, ia)

				k.distributeFailFee(ctx, failFee, entry.VerifierResults, params)

				if k.workerKeeper != nil {
					k.workerKeeper.JailWorker(ctx, workerAddr, 0)
				}

				k.SetSettledTask(ctx, types.SettledTaskID{
					TaskId:            entry.TaskId,
					Status:            types.TaskFailSettled,
					ExpireBlock:       entry.ExpireBlock,
					SettledAt:         currentHeight,
					WorkerAddress:     entry.WorkerAddress,
					OriginalVerifiers: verifierAddrs,
				})
			}
			failCount++
			totalFees = totalFees.Add(actualFee.Amount)
		}
	}

	// Update epoch stats
	stats := k.GetEpochStats(ctx, epoch)
	stats.TotalSettled += uint64(successCount + failCount)
	stats.FailSettled += uint64(failCount)
	stats.TotalFees = stats.TotalFees.Add(totalFees)
	stats.VerificationCount += uint64(successCount+failCount) * 3
	k.SetEpochStats(ctx, stats)

	batchId := k.GetNextBatchId(ctx)
	br := types.BatchRecord{
		BatchId:     batchId,
		Proposer:    msg.Proposer,
		MerkleRoot:  msg.MerkleRoot,
		ResultCount: successCount + failCount,
		TotalFees:   sdk.NewCoin("ufai", totalFees),
		SettledAt:   currentHeight,
	}
	k.SetBatchRecord(ctx, br)
	k.SetBatchCounter(ctx, batchId)

	return batchId, nil
}

// distributeSuccessFee distributes SUCCESS task fee per V5.2 §11:
// 85% executor, 12% verifiers (3 × 4%), 3% multi-verification fund (audit fund).
// Executor gets the remainder after verifiers + fund to prevent dust loss.
func (k Keeper) distributeSuccessFee(ctx sdk.Context, fee sdk.Coin, workerAddr sdk.AccAddress, verifiers []types.VerifierResult, params types.Params) {
	totalVerifierAmount := fee.Amount.MulRaw(int64(params.VerifierFeeRatio)).QuoRaw(1000)
	auditFundAmount := fee.Amount.MulRaw(int64(params.MultiVerificationFundRatio)).QuoRaw(1000)

	verifierDistributed := math.ZeroInt()
	if len(verifiers) > 0 {
		perVerifier := totalVerifierAmount.QuoRaw(int64(len(verifiers)))
		for i, v := range verifiers {
			vAddr, vErr := sdk.AccAddressFromBech32(v.Address)
			if vErr != nil {
				continue
			}
			amount := perVerifier
			if i == len(verifiers)-1 {
				amount = totalVerifierAmount.Sub(verifierDistributed)
			}
			vCoin := sdk.NewCoin(fee.Denom, amount)
			if vCoin.IsPositive() {
				coins := sdk.NewCoins(vCoin)
				_ = k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleAccountName, vAddr, coins)
				k.IncrementVerifierEpochFee(ctx, v.Address, amount)
			}
			verifierDistributed = verifierDistributed.Add(amount)
		}
	}

	executorAmount := fee.Amount.Sub(verifierDistributed).Sub(auditFundAmount)
	executorCoin := sdk.NewCoin(fee.Denom, executorAmount)
	if executorCoin.IsPositive() {
		coins := sdk.NewCoins(executorCoin)
		_ = k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleAccountName, workerAddr, coins)
	}
}

// distributeFailFee distributes FAIL task fee (15% of original): verifiers 12% + multi-verification fund 3%.
// Split ratio between verifiers and fund matches the non-executor portion of the success-fee split
// (VerifierFeeRatio / (VerifierFeeRatio + MultiVerificationFundRatio)).
func (k Keeper) distributeFailFee(ctx sdk.Context, failFee sdk.Coin, verifiers []types.VerifierResult, params types.Params) {
	if len(verifiers) > 0 {
		totalVerifierRatio := params.VerifierFeeRatio
		totalDistributable := params.VerifierFeeRatio + params.MultiVerificationFundRatio
		totalVerifierAmount := failFee.Amount.MulRaw(int64(totalVerifierRatio)).QuoRaw(int64(totalDistributable))
		perVerifier := totalVerifierAmount.QuoRaw(int64(len(verifiers)))
		distributed := math.ZeroInt()
		for i, v := range verifiers {
			vAddr, vErr := sdk.AccAddressFromBech32(v.Address)
			if vErr != nil {
				continue
			}
			// Last verifier gets remainder to avoid dust loss
			amount := perVerifier
			if i == len(verifiers)-1 {
				amount = totalVerifierAmount.Sub(distributed)
			}
			vCoin := sdk.NewCoin(failFee.Denom, amount)
			if vCoin.IsPositive() {
				coins := sdk.NewCoins(vCoin)
				_ = k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleAccountName, vAddr, coins)
				k.IncrementVerifierEpochFee(ctx, v.Address, amount)
			}
			distributed = distributed.Add(amount)
		}
	}
	// Remaining (multi-verification-fund portion = 3/15 of failFee) stays in module account,
	// distributed per-epoch via DistributeMultiVerificationFund in EndBlocker.
}

// DistributeMultiVerificationFund distributes accumulated audit fund to second_verifiers at epoch boundary.
// M9 §9.3: audit fund per-epoch clearing. Per-person fee = pool / audit person-count.
func (k Keeper) DistributeMultiVerificationFund(ctx sdk.Context, epoch int64) {
	stats := k.GetEpochStats(ctx, epoch)
	if stats.SecondVerificationPersonCount == 0 {
		return
	}

	params := k.GetParams(ctx)
	totalFees := stats.TotalFees
	if totalFees.IsZero() {
		return
	}

	// audit_pool = total_fees * multi_verification_fund_ratio / 1000
	fundPool := totalFees.MulRaw(int64(params.MultiVerificationFundRatio)).QuoRaw(1000)
	if fundPool.IsZero() {
		return
	}

	// §9.3: per-person-time fee = pool / total audit person-times.
	// Weight distribution by actual audit count per second_verifier.
	allSecondVerificationRecords := k.GetAllSecondVerificationRecords(ctx)
	second_verifierCounts := make(map[string]int64)
	totalPersonTimes := int64(0)
	for _, ar := range allSecondVerificationRecords {
		if ar.ProcessedAt/100 != epoch {
			continue
		}
		for _, aAddr := range ar.SecondVerifierAddresses {
			second_verifierCounts[aAddr]++
			totalPersonTimes++
		}
	}
	if totalPersonTimes == 0 {
		return
	}

	perPersonTime := fundPool.QuoRaw(totalPersonTimes)
	if perPersonTime.IsZero() {
		return
	}

	// Sort second_verifiers by address so the "last gets remainder" rule
	// is deterministic across runs. Without this, Go map iteration order
	// would non-deterministically pick which second_verifier absorbs the
	// rounding dust — fine for total accounting but a hash-divergent
	// outcome on chain.
	addrs := make([]string, 0, len(second_verifierCounts))
	for vAddr := range second_verifierCounts {
		addrs = append(addrs, vAddr)
	}
	sort.Strings(addrs)

	// distributedTotal accumulates exactly what we hand out so the last
	// second_verifier can absorb `fundPool - distributedTotal` and the
	// pool sums to zero with no dust trapped in the module account.
	// Mirrors distributeFailFee's pattern (line ~1175).
	distributedTotal := math.ZeroInt()
	distributed := int64(0)
	for i, vAddr := range addrs {
		count := second_verifierCounts[vAddr]
		addr, err := sdk.AccAddressFromBech32(vAddr)
		if err != nil {
			continue
		}
		var amount math.Int
		if i == len(addrs)-1 {
			// Absorb all remaining pool dust into the last second_verifier.
			amount = fundPool.Sub(distributedTotal)
		} else {
			amount = perPersonTime.MulRaw(count)
		}
		coin := sdk.NewCoin("ufai", amount)
		if coin.IsPositive() {
			_ = k.bankKeeper.SendCoinsFromModuleToAccount(ctx, types.ModuleAccountName, addr, sdk.NewCoins(coin))
			k.IncrementSecondVerifierEpochFee(ctx, vAddr, amount)
			distributed++
			distributedTotal = distributedTotal.Add(amount)
		}
	}

	if distributed > 0 {
		ctx.EventManager().EmitEvent(sdk.NewEvent(
			"multi_verification_fund_distributed",
			sdk.NewAttribute("epoch", fmt.Sprintf("%d", epoch)),
			sdk.NewAttribute("pool", fundPool.String()),
			sdk.NewAttribute("per_person_time", perPersonTime.String()),
			sdk.NewAttribute("recipients", fmt.Sprintf("%d", distributed)),
		))
	}
}

// ProcessFraudProof handles FraudProof submission.
// V5.2 §12.4: Two time orderings, both result in Worker slash + user refund:
//   - FraudProof before settlement → mark FRAUD → BatchSettlement skips → user not charged.
//   - FraudProof after settlement → recover Worker's executor-fee share (ExecutorFeeRatio,
//     currently 850/1000 = 85% post PR #2) + refund to user → slash 5% → tombstone.
func (k Keeper) ProcessFraudProof(ctx sdk.Context, msg *types.MsgFraudProof) error {
	if k.HasFraudMark(ctx, msg.TaskId) {
		return types.ErrFraudMarked
	}

	workerAddr, addrErr := sdk.AccAddressFromBech32(msg.WorkerAddress)
	if addrErr != nil {
		return sdkerrors.Wrap(addrErr, "invalid worker address")
	}

	// H3: verify Worker's content signature using on-chain pubkey to prevent fake FraudProof.
	// §12.4: Worker signs hash(full_content), user cannot forge without Worker privkey.
	if k.workerKeeper != nil && len(msg.WorkerContentSig) > 0 && len(msg.ContentHash) > 0 {
		pubkeyStr, found := k.workerKeeper.GetWorkerPubkey(ctx, workerAddr)
		if !found {
			return fmt.Errorf("FraudProof: worker pubkey not found on-chain for %s", msg.WorkerAddress)
		}
		workerPubkey := secp256k1.PubKey([]byte(pubkeyStr))
		if !workerPubkey.VerifySignature(msg.ContentHash, msg.WorkerContentSig) {
			return fmt.Errorf("FraudProof: worker content signature verification failed")
		}
	}

	k.SetFraudMark(ctx, msg.TaskId)

	reporterAddr, _ := sdk.AccAddressFromBech32(msg.Reporter)

	st, found := k.GetSettledTask(ctx, msg.TaskId)
	if found && st.Status == types.TaskSettled {
		st.Status = types.TaskFraud
		k.SetSettledTask(ctx, st)

		// M2: recover the executor-fee share (ExecutorFeeRatio, 850/1000 = 85% post PR #2)
		// already distributed to Worker. Claw back from Worker and refund to user.
		if st.Fee.IsPositive() {
			params := k.GetParams(ctx)
			executorAmount := st.Fee.Amount.MulRaw(int64(params.ExecutorFeeRatio)).QuoRaw(1000)
			clawbackCoin := sdk.NewCoin(st.Fee.Denom, executorAmount)
			if clawbackCoin.IsPositive() {
				// Refund to original user if available, otherwise to reporter
				refundAddr := reporterAddr
				if st.UserAddress != "" {
					if userAddr, err := sdk.AccAddressFromBech32(st.UserAddress); err == nil {
						refundAddr = userAddr
					}
				}
				if refundAddr != nil {
					coins := sdk.NewCoins(clawbackCoin)
					if err := k.bankKeeper.SendCoins(ctx, workerAddr, refundAddr, coins); err != nil {
						k.logger.Error("FraudProof: failed to claw back executor fee",
							"worker", workerAddr.String(), "amount", clawbackCoin.String(), "error", err)
					}
				}
			}
		}
	}

	// V5.2 §12.4: slash 5% of Worker's stake → send to reporter (user) as compensation + tombstone.
	if k.workerKeeper != nil {
		if reporterAddr != nil {
			k.workerKeeper.SlashWorkerTo(ctx, workerAddr, 5, reporterAddr)
		} else {
			k.workerKeeper.SlashWorker(ctx, workerAddr, 5)
		}
		k.workerKeeper.TombstoneWorker(ctx, workerAddr)
	}

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventFraudProof,
		sdk.NewAttribute(types.AttributeKeyTaskId, fmt.Sprintf("%x", msg.TaskId)),
		sdk.NewAttribute(types.AttributeKeyWorker, msg.WorkerAddress),
		sdk.NewAttribute(types.AttributeKeyReporter, msg.Reporter),
	))

	return nil
}

// ProcessSecondVerificationResult processes an audit result submission.
// V5.2: Handles all 4 scenarios (SUCCESS+PASS, SUCCESS+FAIL, FAIL+PASS, FAIL+FAIL).
// P2-7: Original verifiers are excluded from auditing (conflict of interest).
//
// Pre_Mainnet_Test_Plan §2.9 row 5 (timing-attack enforcement): the call
// must reject any result whose corresponding SecondVerificationPending entry
// does not yet exist on chain. SecondVerificationPending is created by the
// Worker's batch-settlement path; without that entry, accepting a result
// here would credit the second_verifier (via IncrementSecondVerifierEpochCount
// below) for an audit they cannot have actually performed — a verifier could
// game epoch rewards by pre-submitting fake passes for tasks they have not
// seen, gambling that an honest Worker entry will eventually arrive. The
// entry's `SubmittedAt` field is the receipt height; its mere existence is
// the "result height ≥ receipt height" guarantee the §2.9 rule asks for.
func (k Keeper) ProcessSecondVerificationResult(ctx sdk.Context, msg *types.MsgSecondVerificationResult) error {
	params := k.GetParams(ctx)

	// EARLY EXIT: an existing SecondVerificationRecord at threshold means
	// processAuditJudgment has already fired and DeleteSecondVerificationPending
	// was called as part of it — so the pending entry is gone by design. The
	// 4th+ result on such a task is a late legitimate submission (the dispatch
	// path may have sent the audit before the 3rd result settled the audit).
	// Silently ignore it; this is the dedup behavior tests at
	// TestProcessSecondVerificationResult_AfterThreshold_Ignored assume and
	// predates the §2.9 timing-attack rule below.
	if ar, found := k.GetSecondVerificationRecord(ctx, msg.TaskId); found {
		if uint32(len(ar.SecondVerifierAddresses)) >= params.SecondVerifierCount {
			return nil
		}
	}

	apt, pendingFound := k.GetSecondVerificationPending(ctx, msg.TaskId)
	if !pendingFound {
		return fmt.Errorf("no SecondVerificationPending for task %x — second_verifier may not submit a result before the corresponding receipt has settled (§2.9 timing-attack rule)", msg.TaskId)
	}

	// P2-7: reject audit results from original verifiers (conflict of interest)
	for _, vAddr := range apt.VerifierAddresses {
		if vAddr == msg.SecondVerifier {
			return fmt.Errorf("second_verifier %s is an original verifier for task %x, cannot audit", msg.SecondVerifier, msg.TaskId)
		}
	}

	ar, auditFound := k.GetSecondVerificationRecord(ctx, msg.TaskId)
	if !auditFound {
		ar = types.SecondVerificationRecord{
			TaskId:                     msg.TaskId,
			Epoch:                      msg.Epoch,
			SecondVerifierAddresses:    []string{msg.SecondVerifier},
			Results:                    []bool{msg.Pass},
			SecondVerifierInputTokens:  []uint32{msg.VerifiedInputTokens},
			SecondVerifierOutputTokens: []uint32{msg.VerifiedOutputTokens},
			ProcessedAt:                ctx.BlockHeight(),
		}
	} else {
		if uint32(len(ar.SecondVerifierAddresses)) >= params.SecondVerifierCount {
			return nil
		}
		ar.SecondVerifierAddresses = append(ar.SecondVerifierAddresses, msg.SecondVerifier)
		ar.Results = append(ar.Results, msg.Pass)
		ar.SecondVerifierInputTokens = append(ar.SecondVerifierInputTokens, msg.VerifiedInputTokens)
		ar.SecondVerifierOutputTokens = append(ar.SecondVerifierOutputTokens, msg.VerifiedOutputTokens)
		ar.ProcessedAt = ctx.BlockHeight()
	}

	k.SetSecondVerificationRecord(ctx, ar)

	// P1-9: track per-worker audit count for epoch reward distribution
	k.IncrementSecondVerifierEpochCount(ctx, msg.SecondVerifier)

	// Update epoch stats
	epoch := ctx.BlockHeight() / 100
	stats := k.GetEpochStats(ctx, epoch)
	stats.SecondVerificationPersonCount++
	k.SetEpochStats(ctx, stats)

	if uint32(len(ar.Results)) >= params.SecondVerifierCount {
		k.processAuditJudgment(ctx, ar, params)
	}

	return nil
}

// processAuditJudgment handles the 4 audit judgment scenarios per V5.2 §13.6.
func (k Keeper) processAuditJudgment(ctx sdk.Context, ar types.SecondVerificationRecord, params types.Params) {
	var passCount uint32
	for _, r := range ar.Results {
		if r {
			passCount++
		}
	}

	auditPass := passCount >= params.SecondVerificationMatchThreshold

	apt, pendingFound := k.GetSecondVerificationPending(ctx, ar.TaskId)
	if !pendingFound {
		return
	}

	epoch := ctx.BlockHeight() / 100
	stats := k.GetEpochStats(ctx, epoch)
	stats.SecondVerificationTotal++

	isThirdVerification := apt.IsThirdVerification

	if !isThirdVerification {
		// S2 fix: check third_verification VRF BEFORE settling — if third_verification triggers, do NOT settle yet
		third_verificationRate := k.GetCurrentThirdVerificationRate(ctx)
		if k.shouldTriggerThirdVerification(ctx, ar.TaskId, third_verificationRate) {
			third_verificationPending := types.SecondVerificationPendingTask{
				TaskId:              apt.TaskId,
				OriginalStatus:      apt.OriginalStatus,
				SubmittedAt:         ctx.BlockHeight(),
				UserAddress:         apt.UserAddress,
				WorkerAddress:       apt.WorkerAddress,
				VerifierAddresses:   apt.VerifierAddresses,
				Fee:                 apt.Fee,
				ExpireBlock:         apt.ExpireBlock,
				IsThirdVerification: true,
				FeePerInputToken:    apt.FeePerInputToken,
				FeePerOutputToken:   apt.FeePerOutputToken,
				MaxFee:              apt.MaxFee,
				SettledOutputTokens: apt.SettledOutputTokens,
				SettledInputTokens:  apt.SettledInputTokens,
			}
			k.SetSecondVerificationPending(ctx, third_verificationPending)
			k.DeleteSecondVerificationPending(ctx, ar.TaskId, false)
			k.SetEpochStats(ctx, stats)
			return
		}

		// S9 §5.2.5: per-token audit token count verification
		if auditPass && params.PerTokenBillingEnabled && apt.FeePerOutputToken > 0 {
			second_verifierMedianOut := medianUint32(ar.SecondVerifierOutputTokens)
			if second_verifierMedianOut > 0 && apt.SettledOutputTokens > 0 {
				tol := effectiveTolerance(apt.SettledOutputTokens, params.TokenCountTolerance, params.TokenCountTolerancePct)
				var delta uint32
				if apt.SettledOutputTokens > second_verifierMedianOut {
					delta = apt.SettledOutputTokens - second_verifierMedianOut
				} else {
					delta = second_verifierMedianOut - apt.SettledOutputTokens
				}
				if delta > tol {
					// Token count fraud detected — jail Worker + colluding Verifiers
					stats.AuditOverturn++
					k.logger.Info("S9: audit token count fraud detected",
						"task", hex.EncodeToString(apt.TaskId),
						"settled_tokens", apt.SettledOutputTokens,
						"second_verifier_median", second_verifierMedianOut)

					userAddr, uErr := sdk.AccAddressFromBech32(apt.UserAddress)
					if uErr == nil {
						_, _ = k.UnfreezeBalance(ctx, userAddr, apt.TaskId)
					}

					workerAddr, _ := sdk.AccAddressFromBech32(apt.WorkerAddress)
					if k.workerKeeper != nil && workerAddr != nil {
						k.workerKeeper.JailWorker(ctx, workerAddr, 0)
					}
					// Jail verifiers whose reported tokens deviate from second_verifier median
					for i, vAddr := range apt.VerifierAddresses {
						if i < len(apt.VerifierVotes) && apt.VerifierVotes[i] {
							vAccAddr, vErr := sdk.AccAddressFromBech32(vAddr)
							if vErr == nil && k.workerKeeper != nil {
								k.workerKeeper.JailWorker(ctx, vAccAddr, 0)
							}
						}
					}

					k.SetSettledTask(ctx, types.SettledTaskID{
						TaskId:            apt.TaskId,
						Status:            types.TaskFailed,
						ExpireBlock:       apt.ExpireBlock,
						SettledAt:         ctx.BlockHeight(),
						WorkerAddress:     apt.WorkerAddress,
						OriginalVerifiers: apt.VerifierAddresses,
						UserAddress:       apt.UserAddress,
					})
					k.DeleteSecondVerificationPending(ctx, ar.TaskId, isThirdVerification)
					k.SetEpochStats(ctx, stats)

					// Resettle as FAIL using the second_verifier's true token count
					k.settleAuditedTask(ctx, apt, false, false, params, second_verifierMedianOut)
					return
				}
			}
		}

		// No third_verification → proceed with settlement based on audit result
		if apt.OriginalStatus == types.SettlementSuccess && auditPass {
			k.settleAuditedTask(ctx, apt, true, false, params, 0)
		} else if apt.OriginalStatus == types.SettlementSuccess && !auditPass {
			// H1: audit overturns SUCCESS→FAIL — §10.6: no settlement (funds not yet paid) + jail Worker + jail original PASS verifiers
			stats.AuditOverturn++
			stats.AuditFail++
			k.jailWorkerAndVerifiers(ctx, apt)
			k.SetSettledTask(ctx, types.SettledTaskID{
				TaskId:            apt.TaskId,
				Status:            types.TaskFailed,
				ExpireBlock:       apt.ExpireBlock,
				SettledAt:         ctx.BlockHeight(),
				WorkerAddress:     apt.WorkerAddress,
				OriginalVerifiers: apt.VerifierAddresses,
			})
		} else if apt.OriginalStatus == types.SettlementFail && auditPass {
			// P1-NEW-1 fix: Audit overturns FAIL→SUCCESS. Under "no settlement before audit" principle,
			// the audit VRF triggered `continue` before any fee was collected (neither SUCCESS 100%
			// nor FAIL 5%). So alreadyPaidFail must be false — charge full 100%.
			stats.AuditOverturn++
			k.settleAuditedTask(ctx, apt, true, false, params, 0)
			k.jailFailVerifiers(ctx, apt)
		} else {
			stats.AuditFail++
			k.settleAuditedTask(ctx, apt, false, false, params, 0)
		}
	} else {
		// ThirdVerification judgment
		stats.ThirdVerificationTotal++
		origAudit, origFound := k.GetSecondVerificationRecord(ctx, ar.TaskId)
		if !origFound {
			k.DeleteSecondVerificationPending(ctx, ar.TaskId, true)
			k.SetEpochStats(ctx, stats)
			return
		}

		var origAuditPass bool
		origPassCount := uint32(0)
		for _, r := range origAudit.Results {
			if r {
				origPassCount++
			}
		}
		origAuditPass = origPassCount >= params.SecondVerificationMatchThreshold

		if origAuditPass && !auditPass {
			// H2: ThirdVerification overturns audit PASS→FAIL — §10.7: no settlement + jail original PASS second_verifiers + jail Worker + jail verifiers
			stats.ThirdVerificationOverturn++
			k.jailSecondVerifiers(ctx, origAudit)
			k.jailWorkerAndVerifiers(ctx, apt)
			k.SetSettledTask(ctx, types.SettledTaskID{
				TaskId:            apt.TaskId,
				Status:            types.TaskFailed,
				ExpireBlock:       apt.ExpireBlock,
				SettledAt:         ctx.BlockHeight(),
				WorkerAddress:     apt.WorkerAddress,
				OriginalVerifiers: apt.VerifierAddresses,
			})
		} else if !origAuditPass && auditPass {
			// ThirdVerification overturns audit FAIL→PASS: malicious second_verifiers → jail original FAIL second_verifiers, settle by original verification result
			stats.ThirdVerificationOverturn++
			k.jailFailSecondVerifiers(ctx, origAudit, params)
			// P1-NEW-1 fix: Under "no settlement before audit" principle, no fee was ever collected
			// when the audit VRF triggered. alreadyPaidFail must always be false.
			k.settleAuditedTask(ctx, apt, apt.OriginalStatus == types.SettlementSuccess, false, params, 0)
		} else {
			// ThirdVerification confirms audit result
			if origAuditPass {
				k.settleAuditedTask(ctx, apt, apt.OriginalStatus == types.SettlementSuccess, false, params, 0)
			} else {
				if apt.OriginalStatus == types.SettlementSuccess {
					// ThirdVerification confirms audit overturn SUCCESS→FAIL: no settlement + jail
					k.jailWorkerAndVerifiers(ctx, apt)
					k.SetSettledTask(ctx, types.SettledTaskID{
						TaskId:            apt.TaskId,
						Status:            types.TaskFailed,
						ExpireBlock:       apt.ExpireBlock,
						SettledAt:         ctx.BlockHeight(),
						WorkerAddress:     apt.WorkerAddress,
						OriginalVerifiers: apt.VerifierAddresses,
					})
				} else {
					k.settleAuditedTask(ctx, apt, false, false, params, 0)
				}
			}
		}

		k.DeleteSecondVerificationPending(ctx, ar.TaskId, true)
	}

	if !isThirdVerification {
		k.DeleteSecondVerificationPending(ctx, ar.TaskId, false)
	}

	k.SetEpochStats(ctx, stats)

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventSecondVerificationResult,
		sdk.NewAttribute(types.AttributeKeyTaskId, fmt.Sprintf("%x", ar.TaskId)),
		sdk.NewAttribute("audit_pass", fmt.Sprintf("%v", auditPass)),
		sdk.NewAttribute("is_third_verification", fmt.Sprintf("%v", isThirdVerification)),
	))
}

// settleAuditedTask settles a task after audit/third_verification completion.
// alreadyPaidFail: if true, user already paid FailSettlementFeeRatio (150/1000 = 15%
// post PR #2) during initial FAIL settlement, so only charge the remaining 85% (not full
// fee) to avoid double-charging. Numbers are for documentation; code reads the dynamic
// ratio from params.
func (k Keeper) settleAuditedTask(ctx sdk.Context, apt types.SecondVerificationPendingTask, asSuccess bool, alreadyPaidFail bool, params types.Params, overrideOutputTokens uint32) {
	userAddr, err := sdk.AccAddressFromBech32(apt.UserAddress)
	if err != nil {
		return
	}
	workerAddr, err := sdk.AccAddressFromBech32(apt.WorkerAddress)
	if err != nil {
		return
	}

	ia, found := k.GetInferenceAccount(ctx, userAddr)
	if !found {
		return
	}

	var verifiers []types.VerifierResult
	for _, addr := range apt.VerifierAddresses {
		verifiers = append(verifiers, types.VerifierResult{Address: addr})
	}

	// S9: determine base fee for settlement
	baseFee := apt.Fee
	if apt.FeePerInputToken > 0 || apt.FeePerOutputToken > 0 {
		_, _ = k.UnfreezeBalance(ctx, userAddr, apt.TaskId)

		outTokens := apt.SettledOutputTokens
		if overrideOutputTokens > 0 {
			outTokens = overrideOutputTokens
		}

		computedFee := CalculatePerTokenFee(
			apt.SettledInputTokens, outTokens,
			apt.FeePerInputToken, apt.FeePerOutputToken,
			apt.MaxFee.Amount.Uint64(),
		)
		baseFee = sdk.NewCoin(types.DefaultDenom, math.NewIntFromUint64(computedFee))
	}

	if asSuccess {
		chargeAmount := baseFee
		if alreadyPaidFail {
			alreadyPaid := baseFee.Amount.MulRaw(int64(params.FailSettlementFeeRatio)).QuoRaw(1000)
			chargeAmount = sdk.NewCoin(baseFee.Denom, baseFee.Amount.Sub(alreadyPaid))
		}
		if ia.Balance.IsLT(chargeAmount) && chargeAmount.IsPositive() {
			return
		}
		ia.Balance = ia.Balance.Sub(chargeAmount)
		k.SetInferenceAccount(ctx, ia)
		k.distributeSuccessFee(ctx, baseFee, workerAddr, verifiers, params)

		if k.workerKeeper != nil {
			k.workerKeeper.IncrementSuccessStreak(ctx, workerAddr)
			k.workerKeeper.UpdateWorkerStats(ctx, workerAddr, baseFee)
		}

		k.SetSettledTask(ctx, types.SettledTaskID{
			TaskId:            apt.TaskId,
			Status:            types.TaskSettled,
			ExpireBlock:       apt.ExpireBlock,
			SettledAt:         ctx.BlockHeight(),
			WorkerAddress:     apt.WorkerAddress,
			OriginalVerifiers: apt.VerifierAddresses,
		})
	} else {
		failFee := sdk.NewCoin(baseFee.Denom, baseFee.Amount.MulRaw(int64(params.FailSettlementFeeRatio)).QuoRaw(1000))
		if ia.Balance.IsLT(failFee) && failFee.IsPositive() {
			return
		}
		ia.Balance = ia.Balance.Sub(failFee)
		k.SetInferenceAccount(ctx, ia)
		k.distributeFailFee(ctx, failFee, verifiers, params)

		if k.workerKeeper != nil {
			k.workerKeeper.JailWorker(ctx, workerAddr, 0)
		}

		k.SetSettledTask(ctx, types.SettledTaskID{
			TaskId:            apt.TaskId,
			Status:            types.TaskFailSettled,
			ExpireBlock:       apt.ExpireBlock,
			SettledAt:         ctx.BlockHeight(),
			WorkerAddress:     apt.WorkerAddress,
			OriginalVerifiers: apt.VerifierAddresses,
		})
	}
}

// P2-7: jails Worker + only PASS-voting verifiers (FAIL voters were correct).
func (k Keeper) jailWorkerAndVerifiers(ctx sdk.Context, apt types.SecondVerificationPendingTask) {
	if k.workerKeeper == nil {
		return
	}
	workerAddr, err := sdk.AccAddressFromBech32(apt.WorkerAddress)
	if err == nil {
		k.workerKeeper.JailWorker(ctx, workerAddr, 0)
	}
	for i, vAddr := range apt.VerifierAddresses {
		if i < len(apt.VerifierVotes) && !apt.VerifierVotes[i] {
			continue // voted FAIL — correct, don't jail
		}
		addr, err := sdk.AccAddressFromBech32(vAddr)
		if err == nil {
			k.workerKeeper.JailWorker(ctx, addr, 0)
		}
	}
}

// jailFailVerifiers jails only the verifiers who voted FAIL in the original verification.
// P1-8 §10.6: "each verifier who voted FAIL gets jail_count += 1" — only FAIL voters are jailed.
func (k Keeper) jailFailVerifiers(ctx sdk.Context, apt types.SecondVerificationPendingTask) {
	if k.workerKeeper == nil {
		return
	}
	for i, vAddr := range apt.VerifierAddresses {
		// P1-8: only jail verifiers who voted FAIL (not PASS)
		if i < len(apt.VerifierVotes) && apt.VerifierVotes[i] {
			continue // this verifier voted PASS, skip
		}
		addr, err := sdk.AccAddressFromBech32(vAddr)
		if err == nil {
			k.workerKeeper.JailWorker(ctx, addr, 0)
		}
	}
}

// jailSecondVerifiers jails only second_verifiers who voted PASS (lazy/colluding second_verifiers).
// V5.2 §10.7: "third_verification overturns audit PASS→FAIL → jail original PASS second_verifiers"
func (k Keeper) jailSecondVerifiers(ctx sdk.Context, ar types.SecondVerificationRecord) {
	if k.workerKeeper == nil {
		return
	}
	for i, aAddr := range ar.SecondVerifierAddresses {
		// Only jail second_verifiers who voted PASS (i.e., were lazy/wrong)
		if i < len(ar.Results) && !ar.Results[i] {
			continue // voted FAIL = honest, skip
		}
		addr, err := sdk.AccAddressFromBech32(aAddr)
		if err == nil {
			k.workerKeeper.JailWorker(ctx, addr, 0)
		}
	}
}

func (k Keeper) jailFailSecondVerifiers(ctx sdk.Context, ar types.SecondVerificationRecord, params types.Params) {
	if k.workerKeeper == nil {
		return
	}
	for i, aAddr := range ar.SecondVerifierAddresses {
		if !ar.Results[i] {
			addr, err := sdk.AccAddressFromBech32(aAddr)
			if err == nil {
				k.workerKeeper.JailWorker(ctx, addr, 0)
			}
		}
	}
}

// CalculateSecondVerificationRate computes the dynamic audit rate for the next epoch.
// V5.2: audit_rate = base_rate × (1 + 10 × recent_fail_ratio + 50 × recent_audit_fail)
// Clamped to [second_verification_rate_min, second_verification_rate_max].
func (k Keeper) CalculateSecondVerificationRate(ctx sdk.Context, prevEpoch int64) uint32 {
	params := k.GetParams(ctx)
	stats := k.GetEpochStats(ctx, prevEpoch)

	recentFailRatio := float64(0)
	if stats.TotalSettled > 0 {
		recentFailRatio = float64(stats.FailSettled) / float64(stats.TotalSettled)
	}

	recentAuditFail := float64(0)
	if stats.SecondVerificationTotal > 0 {
		recentAuditFail = float64(stats.AuditFail) / float64(stats.SecondVerificationTotal)
	}

	rate := float64(params.SecondVerificationBaseRate) * (1.0 + 10.0*recentFailRatio + 50.0*recentAuditFail)
	rateU32 := uint32(rate)

	if rateU32 < params.SecondVerificationRateMin {
		rateU32 = params.SecondVerificationRateMin
	}
	if rateU32 > params.SecondVerificationRateMax {
		rateU32 = params.SecondVerificationRateMax
	}
	return rateU32
}

// CalculateThirdVerificationRate computes the dynamic third_verification rate.
// V5.2: third_verification_rate = base × (1 + 10 × recent_audit_overturn_ratio + 50 × recent_third_verification_overturn)
func (k Keeper) CalculateThirdVerificationRate(ctx sdk.Context, prevEpoch int64) uint32 {
	params := k.GetParams(ctx)
	stats := k.GetEpochStats(ctx, prevEpoch)

	recentOverturn := float64(0)
	if stats.SecondVerificationTotal > 0 {
		recentOverturn = float64(stats.AuditOverturn) / float64(stats.SecondVerificationTotal)
	}

	recentThirdVerificationOverturn := float64(0)
	if stats.ThirdVerificationTotal > 0 {
		recentThirdVerificationOverturn = float64(stats.ThirdVerificationOverturn) / float64(stats.ThirdVerificationTotal)
	}

	rate := float64(params.ThirdVerificationBaseRate) * (1.0 + 10.0*recentOverturn + 50.0*recentThirdVerificationOverturn)
	rateU32 := uint32(rate)

	if rateU32 < params.ThirdVerificationRateMin {
		rateU32 = params.ThirdVerificationRateMin
	}
	if rateU32 > params.ThirdVerificationRateMax {
		rateU32 = params.ThirdVerificationRateMax
	}
	return rateU32
}

// HandleSecondVerificationTimeouts processes audit/third_verification timeouts using height-indexed keys.
// V5.2 §13.11: audit timeout 12h → original verification result stands → CLEARED.
// Uses O(k) iteration where k = number of timed-out tasks, instead of O(n) over all pending.
func (k Keeper) HandleSecondVerificationTimeouts(ctx sdk.Context) int {
	params := k.GetParams(ctx)
	currentHeight := ctx.BlockHeight()
	timeoutCount := 0

	// Check audit pending timeouts: scan timeout index up to (currentHeight - SecondVerificationTimeout)
	auditCutoff := currentHeight - params.SecondVerificationTimeout
	if auditCutoff > 0 {
		timeoutCount += k.processTimeoutsByIndex(ctx, types.SecondVerificationPendingTimeoutKeyPrefix, auditCutoff, false, params)
	}

	// Check third_verification pending timeouts
	third_verificationCutoff := currentHeight - params.ThirdVerificationTimeout
	if third_verificationCutoff > 0 {
		timeoutCount += k.processTimeoutsByIndex(ctx, types.ThirdVerificationPendingTimeoutKeyPrefix, third_verificationCutoff, true, params)
	}

	return timeoutCount
}

// processTimeoutsByIndex scans the height-indexed timeout keys up to cutoffHeight
// and settles timed-out tasks. Returns number of tasks processed.
func (k Keeper) processTimeoutsByIndex(ctx sdk.Context, prefix []byte, cutoffHeight int64, isThirdVerification bool, params types.Params) int {
	store := ctx.KVStore(k.storeKey)

	// Iterate from prefix start to prefix + cutoffHeight (inclusive)
	endKey := make([]byte, len(prefix)+8)
	copy(endKey, prefix)
	binary.BigEndian.PutUint64(endKey[len(prefix):], uint64(cutoffHeight+1))

	iter := store.Iterator(prefix, endKey)
	defer iter.Close()

	var toProcess []types.SecondVerificationPendingTask
	var timeoutKeys [][]byte
	for ; iter.Valid(); iter.Next() {
		timeoutKeys = append(timeoutKeys, append([]byte{}, iter.Key()...))
		// Extract taskID from key: prefix(1) + height(8) + taskID
		key := iter.Key()
		if len(key) <= len(prefix)+8 {
			continue
		}
		taskID := key[len(prefix)+8:]
		var apt types.SecondVerificationPendingTask
		var found bool
		if isThirdVerification {
			apt, found = k.getSecondVerificationPendingByKey(ctx, types.ThirdVerificationPendingKey(taskID))
		} else {
			apt, found = k.getSecondVerificationPendingByKey(ctx, types.SecondVerificationPendingKey(taskID))
		}
		if found {
			toProcess = append(toProcess, apt)
		}
	}

	for i, apt := range toProcess {
		// P1-7: For third_verification timeout, use audit result (not OriginalStatus).
		// Spec §13.11: "third_verification timeout: original audit result takes effect."
		settleAsSuccess := apt.OriginalStatus == types.SettlementSuccess
		if isThirdVerification {
			ar, arFound := k.GetSecondVerificationRecord(ctx, apt.TaskId)
			if arFound {
				var passCount uint32
				for _, r := range ar.Results {
					if r {
						passCount++
					}
				}
				auditPass := passCount >= params.SecondVerificationMatchThreshold
				// ThirdVerification timeout → original audit result stands
				if apt.OriginalStatus == types.SettlementSuccess {
					settleAsSuccess = auditPass
				} else {
					settleAsSuccess = auditPass // FAIL+auditPASS → SUCCESS (audit overturned original FAIL)
				}
			}
		}
		k.settleAuditedTask(ctx, apt, settleAsSuccess, false, params, 0)
		if isThirdVerification {
			store.Delete(types.ThirdVerificationPendingKey(apt.TaskId))
		} else {
			store.Delete(types.SecondVerificationPendingKey(apt.TaskId))
		}
		store.Delete(timeoutKeys[i])
	}

	return len(toProcess)
}

// CleanupExpiredTasks removes settled task_id records after SettledAt + buffer (§10.9).
// M5: use SettledAt (not ExpireBlock) as basis — ensures all tasks are eventually cleaned.
func (k Keeper) CleanupExpiredTasks(ctx sdk.Context) int {
	params := k.GetParams(ctx)
	currentHeight := ctx.BlockHeight()
	cutoff := currentHeight - params.TaskCleanupBuffer

	store := ctx.KVStore(k.storeKey)
	iter := storetypes.KVStorePrefixIterator(store, types.SettledTaskKeyPrefix)
	defer iter.Close()

	var toDelete [][]byte
	for ; iter.Valid(); iter.Next() {
		var st types.SettledTaskID
		if err := json.Unmarshal(iter.Value(), &st); err != nil {
			continue
		}
		// M5: use SettledAt as cleanup basis, but respect ExpireBlock=0 (no-expiry tasks).
		if st.ExpireBlock == 0 {
			continue
		}
		if st.SettledAt > 0 && st.SettledAt < cutoff {
			toDelete = append(toDelete, append([]byte{}, iter.Key()...))
		}
	}

	for _, key := range toDelete {
		store.Delete(key)
	}

	return len(toDelete)
}

// shouldTriggerSecondVerification checks if a task should be audited using on-chain VRF.
// S3 §13.4: VRF(task_id + post_verification_block_hash) < audit_rate → PENDING_AUDIT.
func (k Keeper) shouldTriggerSecondVerification(ctx sdk.Context, taskId []byte, auditRate uint32) bool {
	blockHash := ctx.HeaderHash()
	if len(blockHash) == 0 {
		blockHash = ctx.BlockHeader().LastBlockId.Hash
	}

	data := append(append([]byte{}, taskId...), blockHash...)
	h := sha256.Sum256(data)
	vrfValue := new(big.Int).SetBytes(h[:])

	maxUint := new(big.Int).Lsh(big.NewInt(1), 256)
	threshold := new(big.Int).Mul(maxUint, big.NewInt(int64(auditRate)))
	threshold.Div(threshold, big.NewInt(1000))

	return vrfValue.Cmp(threshold) < 0
}

// shouldTriggerThirdVerification checks if a task should go to third_verification using VRF.
// S5: VRF(task_id || post_audit_block_hash) < third_verification_rate → PENDING_REAUDIT.
func (k Keeper) shouldTriggerThirdVerification(ctx sdk.Context, taskId []byte, third_verificationRate uint32) bool {
	blockHash := ctx.HeaderHash()
	if len(blockHash) == 0 {
		blockHash = ctx.BlockHeader().LastBlockId.Hash
	}

	data := append(append([]byte{}, taskId...), blockHash...)
	h := sha256.Sum256(data)
	vrfValue := new(big.Int).SetBytes(h[:])

	maxUint := new(big.Int).Lsh(big.NewInt(1), 256)
	threshold := new(big.Int).Mul(maxUint, big.NewInt(int64(third_verificationRate)))
	threshold.Div(threshold, big.NewInt(1000))

	return vrfValue.Cmp(threshold) < 0
}

// verifyProposerSignature verifies the Proposer's secp256k1 signature on the merkle root.
// P0-2/P1-5: §10.5 — "Proposer signature valid" is the first batch validation step.
func (k Keeper) verifyProposerSignature(ctx sdk.Context, msg *types.MsgBatchSettlement) error {
	if len(msg.ProposerSig) == 0 {
		return fmt.Errorf("missing proposer signature")
	}

	if k.workerKeeper == nil {
		return nil // skip verification if no worker keeper
	}

	proposerAddr, err := sdk.AccAddressFromBech32(msg.Proposer)
	if err != nil {
		return fmt.Errorf("invalid proposer address: %w", err)
	}

	pubkeyStr, found := k.workerKeeper.GetWorkerPubkey(ctx, proposerAddr)
	if !found || len(pubkeyStr) == 0 {
		// P1-6 fix: pubkey not found must be an error. Allowing bypass lets an unregistered
		// address submit BatchSettlement without signature verification.
		return fmt.Errorf("proposer pubkey not found: %s is not a registered worker", msg.Proposer)
	}

	pubkeyBytes, err := hex.DecodeString(pubkeyStr)
	if err != nil {
		pubkeyBytes = []byte(pubkeyStr)
	}
	msgHash := sha256.Sum256(msg.MerkleRoot)

	var pubKey secp256k1.PubKey = pubkeyBytes
	if !pubKey.VerifySignature(msgHash[:], msg.ProposerSig) {
		return fmt.Errorf("proposer signature verification failed")
	}

	return nil
}

// -------- S9: Frozen Balance CRUD --------

// FreezeBalance increases the user's frozen_balance by amount (S9 pre-deduction).
func (k Keeper) FreezeBalance(ctx sdk.Context, userAddr sdk.AccAddress, taskId []byte, amount sdk.Coin) error {
	ia, found := k.GetInferenceAccount(ctx, userAddr)
	if !found {
		return fmt.Errorf("inference account not found")
	}
	available := ia.AvailableBalance()
	if available.IsLT(amount) {
		return fmt.Errorf("insufficient available balance: %s < %s", available, amount)
	}
	if !ia.FrozenBalance.IsValid() || ia.FrozenBalance.IsZero() {
		ia.FrozenBalance = amount
	} else {
		ia.FrozenBalance = ia.FrozenBalance.Add(amount)
	}
	k.SetInferenceAccount(ctx, ia)

	// Store per-task frozen amount for refund
	store := ctx.KVStore(k.storeKey)
	bz := make([]byte, 8)
	binary.BigEndian.PutUint64(bz, amount.Amount.Uint64())
	store.Set(types.FrozenBalanceKey(taskId), bz)
	return nil
}

// UnfreezeBalance decreases the user's frozen_balance (S9 settlement refund).
func (k Keeper) UnfreezeBalance(ctx sdk.Context, userAddr sdk.AccAddress, taskId []byte) (sdk.Coin, error) {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.FrozenBalanceKey(taskId))
	if bz == nil {
		return sdk.Coin{}, nil // no frozen balance for this task (per-request mode)
	}
	frozenAmount := binary.BigEndian.Uint64(bz)
	frozen := sdk.NewCoin(types.DefaultDenom, math.NewIntFromUint64(frozenAmount))

	ia, found := k.GetInferenceAccount(ctx, userAddr)
	if !found {
		return frozen, fmt.Errorf("inference account not found for unfreeze")
	}

	if ia.FrozenBalance.IsGTE(frozen) {
		ia.FrozenBalance = ia.FrozenBalance.Sub(frozen)
	} else {
		ia.FrozenBalance = sdk.NewCoin(types.DefaultDenom, math.ZeroInt())
	}
	k.SetInferenceAccount(ctx, ia)
	store.Delete(types.FrozenBalanceKey(taskId))
	return frozen, nil
}

// -------- S9: Dishonest Count CRUD --------

// GetDishonestCount returns the worker's cumulative dishonest token reporting count.
func (k Keeper) GetDishonestCount(ctx sdk.Context, workerAddr sdk.AccAddress) uint32 {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.DishonestCountKey(workerAddr))
	if bz == nil {
		return 0
	}
	return binary.BigEndian.Uint32(bz)
}

// IncrementDishonestCount increments and checks against jail threshold (S9 §3.4 Case B).
// Resets to 0 after jail (progressive: 1st=10min, 2nd=1hr, 3rd=slash+tombstone).
func (k Keeper) IncrementDishonestCount(ctx sdk.Context, workerAddr sdk.AccAddress, params types.Params) {
	count := k.GetDishonestCount(ctx, workerAddr) + 1
	store := ctx.KVStore(k.storeKey)
	bz := make([]byte, 4)
	binary.BigEndian.PutUint32(bz, count)
	store.Set(types.DishonestCountKey(workerAddr), bz)

	if count >= params.DishonestJailThreshold && k.workerKeeper != nil {
		k.workerKeeper.JailWorker(ctx, workerAddr, 0)
		k.ResetDishonestCount(ctx, workerAddr)
		k.logger.Info("S9: Worker jailed for dishonest token reporting",
			"worker", workerAddr.String(), "dishonest_count", count)
	}
}

// ResetDishonestCount resets a worker's dishonesty counter to 0.
// Called after jail or after the worker's success streak hits the S9 anti-cheat
// threshold (independently of the jail_count decay interval — see
// `IncrementSuccessStreak` in the worker keeper for the jail_count decay rule).
func (k Keeper) ResetDishonestCount(ctx sdk.Context, workerAddr sdk.AccAddress) {
	store := ctx.KVStore(k.storeKey)
	store.Delete(types.DishonestCountKey(workerAddr))
}

// -------- S9: Frozen Task Timeout (§4.4) --------

// StoreFrozenTaskMeta persists task metadata needed for timeout processing.
func (k Keeper) StoreFrozenTaskMeta(ctx sdk.Context, meta types.FrozenTaskMeta) {
	store := ctx.KVStore(k.storeKey)
	bz, _ := json.Marshal(meta)
	store.Set(types.FrozenTaskIndexKey(meta.ExpireBlock, meta.TaskId), bz)
}

// HandleFrozenBalanceTimeouts processes per-token tasks that have expired without settlement.
// S9 §4.4: charge timeout_fee = max_fee × FailSettlementFeeRatio (150/1000 = 15% post
// PR #2) to the multi-verification fund, refund the remaining 85%, jail Worker.
// Percentages are for documentation; code uses the dynamic params ratio.
func (k Keeper) HandleFrozenBalanceTimeouts(ctx sdk.Context) int {
	params := k.GetParams(ctx)
	if !params.PerTokenBillingEnabled {
		return 0
	}
	currentHeight := ctx.BlockHeight()
	store := ctx.KVStore(k.storeKey)

	prefix := types.FrozenTaskIndexKeyPrefix
	iter := storetypes.KVStorePrefixIterator(store, prefix)
	defer iter.Close()

	var processed int
	var toDelete [][]byte

	for ; iter.Valid(); iter.Next() {
		key := iter.Key()
		// Extract expireBlock from key: prefix(1) + expireBlock(8) + taskId
		if len(key) < 9 {
			continue
		}
		expireBlock := int64(binary.BigEndian.Uint64(key[1:9]))
		if expireBlock > currentHeight {
			break // keys are ordered by expireBlock, so we can stop early
		}

		var meta types.FrozenTaskMeta
		if err := json.Unmarshal(iter.Value(), &meta); err != nil {
			toDelete = append(toDelete, append([]byte{}, key...))
			continue
		}

		// Skip if task was already settled
		if _, found := k.GetSettledTask(ctx, meta.TaskId); found {
			toDelete = append(toDelete, append([]byte{}, key...))
			// Also clean the frozen balance record
			store.Delete(types.FrozenBalanceKey(meta.TaskId))
			continue
		}

		userAddr, err := sdk.AccAddressFromBech32(meta.UserAddress)
		if err != nil {
			toDelete = append(toDelete, append([]byte{}, key...))
			continue
		}

		// Unfreeze the max_fee
		_, _ = k.UnfreezeBalance(ctx, userAddr, meta.TaskId)

		// Charge timeout_fee = max_fee × FailSettlementFeeRatio / 1000
		timeoutFee := meta.MaxFee * uint64(params.FailSettlementFeeRatio) / 1000
		if timeoutFee > 0 {
			ia, found := k.GetInferenceAccount(ctx, userAddr)
			if found {
				timeoutCoin := sdk.NewCoin(types.DefaultDenom, math.NewIntFromUint64(timeoutFee))
				if ia.Balance.IsGTE(timeoutCoin) {
					ia.Balance = ia.Balance.Sub(timeoutCoin)
					k.SetInferenceAccount(ctx, ia)
				}
			}
		}

		// Record as failed
		k.SetSettledTask(ctx, types.SettledTaskID{
			TaskId:        meta.TaskId,
			Status:        types.TaskFailSettled,
			ExpireBlock:   meta.ExpireBlock,
			SettledAt:     currentHeight,
			WorkerAddress: meta.WorkerAddress,
			UserAddress:   meta.UserAddress,
		})

		// Jail worker
		if k.workerKeeper != nil {
			workerAddr, wErr := sdk.AccAddressFromBech32(meta.WorkerAddress)
			if wErr == nil {
				k.workerKeeper.JailWorker(ctx, workerAddr, 0)
			}
		}

		toDelete = append(toDelete, append([]byte{}, key...))
		processed++
	}

	for _, key := range toDelete {
		store.Delete(key)
	}
	return processed
}

// -------- S9: Token Mismatch Pair Tracking (§5.2) --------

// GetTokenMismatchRecord returns the pair tracking record.
func (k Keeper) GetTokenMismatchRecord(ctx sdk.Context, workerAddr, verifierAddr string) types.TokenMismatchRecord {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.TokenMismatchKey(workerAddr, verifierAddr))
	if bz == nil {
		return types.TokenMismatchRecord{WorkerAddress: workerAddr, VerifierAddress: verifierAddr}
	}
	var rec types.TokenMismatchRecord
	if err := json.Unmarshal(bz, &rec); err != nil {
		return types.TokenMismatchRecord{WorkerAddress: workerAddr, VerifierAddress: verifierAddr}
	}
	return rec
}

// SetTokenMismatchRecord stores a pair tracking record.
func (k Keeper) SetTokenMismatchRecord(ctx sdk.Context, rec types.TokenMismatchRecord) {
	store := ctx.KVStore(k.storeKey)
	bz, _ := json.Marshal(rec)
	store.Set(types.TokenMismatchKey(rec.WorkerAddress, rec.VerifierAddress), bz)
}

// UpdateTokenMismatchPair updates pair-level tracking after settlement (S9 §5.2.3).
func (k Keeper) UpdateTokenMismatchPair(ctx sdk.Context, workerAddr string, verifierAddr string, isMismatch bool, params types.Params) {
	rec := k.GetTokenMismatchRecord(ctx, workerAddr, verifierAddr)
	rec.TotalTasks++
	if isMismatch {
		rec.MismatchCount++
	}
	// Sliding window: compress when exceeding lookback
	if params.TokenMismatchLookback > 0 && rec.TotalTasks >= params.TokenMismatchLookback {
		rec.TotalTasks = rec.TotalTasks / 2
		rec.MismatchCount = rec.MismatchCount / 2
	}
	k.SetTokenMismatchRecord(ctx, rec)
}

// CalculateWorkerAuditBoost returns the per-worker audit rate boost from pair tracking (S9 §5.2.4).
func (k Keeper) CalculateWorkerAuditBoost(ctx sdk.Context, workerAddr string, params types.Params) uint32 {
	store := ctx.KVStore(k.storeKey)
	prefix := types.TokenMismatchPrefixForWorker(workerAddr)
	iter := storetypes.KVStorePrefixIterator(store, prefix)
	defer iter.Close()

	var maxPairRatio uint32
	for ; iter.Valid(); iter.Next() {
		var rec types.TokenMismatchRecord
		if err := json.Unmarshal(iter.Value(), &rec); err != nil {
			continue
		}
		if rec.TotalTasks < params.TokenMismatchPairMinSamples {
			continue
		}
		ratio := rec.MismatchCount * 100 / rec.TotalTasks
		if ratio > maxPairRatio {
			maxPairRatio = ratio
		}
	}

	if maxPairRatio > 50 {
		return params.TokenMismatchSecondVerificationWeight
	} else if maxPairRatio > 30 {
		return params.TokenMismatchSecondVerificationWeight / 2
	}
	return 0
}

// -------- S9: Two-Party Token Count Resolution (§3.4) --------

// TokenCountResolution holds the resolved token counts after three-party validation.
type TokenCountResolution struct {
	InputTokens     uint32
	OutputTokens    uint32
	WorkerDishonest bool // true if Worker misreported
	LowConfidence   bool // E14: true if no verifier provided token counts (all returned 0)
}

// ResolveTokenCounts implements S9 §3.4 two-party verification:
// Worker self-report vs Verifier median. Proposer count is not used.
func ResolveTokenCounts(entry *types.SettlementEntry, tolerance uint32, tolerancePct uint32) TokenCountResolution {
	nWorkerOut := entry.WorkerOutputTokens

	var verifierOuts []uint32
	for _, v := range entry.VerifierResults {
		if v.VerifiedOutputTokens > 0 {
			verifierOuts = append(verifierOuts, v.VerifiedOutputTokens)
		}
	}
	nMedianOut := medianUint32(verifierOuts)

	var verifierIns []uint32
	for _, v := range entry.VerifierResults {
		if v.VerifiedInputTokens > 0 {
			verifierIns = append(verifierIns, v.VerifiedInputTokens)
		}
	}
	nMedianIn := medianUint32(verifierIns)

	res := TokenCountResolution{}

	// E9-E11/E14: mark as low confidence when insufficient verifier data.
	// <3 verifiers = degraded verification; 0 = no verification at all.
	// This triggers forced audit to prevent unverified worker self-report payouts.
	if len(verifierOuts) < 3 {
		res.LowConfidence = true
	}

	outTol := effectiveTolerance(nMedianOut, tolerance, tolerancePct)
	res.OutputTokens, res.WorkerDishonest = resolveTokenPair(nWorkerOut, nMedianOut, outTol)

	inTol := effectiveTolerance(nMedianIn, tolerance, tolerancePct)
	nWorkerIn := entry.WorkerInputTokens
	res.InputTokens, _ = resolveTokenPair(nWorkerIn, nMedianIn, inTol)

	return res
}

// effectiveTolerance returns max(absoluteTolerance, count * pct / 100).
func effectiveTolerance(count, absTol, pct uint32) uint32 {
	pctTol := count * pct / 100
	if pctTol > absTol {
		return pctTol
	}
	return absTol
}

// resolveTokenPair implements S9 §3.4 Case A/B two-party comparison.
func resolveTokenPair(nWorker, nMedian, tolerance uint32) (count uint32, workerDishonest bool) {
	if nMedian == 0 {
		return nWorker, false
	}
	var delta uint32
	if nWorker > nMedian {
		delta = nWorker - nMedian
	} else {
		delta = nMedian - nWorker
	}
	if delta <= tolerance {
		return nWorker, false
	}
	return nMedian, true
}

// medianUint32 returns the median of a uint32 slice. Returns 0 if empty.
// E9: for 2 values, returns average (not biased toward larger).
func medianUint32(vals []uint32) uint32 {
	if len(vals) == 0 {
		return 0
	}
	// Simple sort for small slices (typically 3 verifiers)
	for i := 0; i < len(vals); i++ {
		for j := i + 1; j < len(vals); j++ {
			if vals[j] < vals[i] {
				vals[i], vals[j] = vals[j], vals[i]
			}
		}
	}
	// E9: 2 verifiers → average instead of biased upper value
	if len(vals) == 2 {
		return (vals[0] + vals[1]) / 2
	}
	return vals[len(vals)/2]
}

// CalculatePerTokenFee computes actual_fee = input_tokens * fee_per_input + output_tokens * fee_per_output.
// Capped at max_fee (S9 §4.1). Includes overflow protection (S9 §4.5).
func CalculatePerTokenFee(inputTokens, outputTokens uint32, feePerInput, feePerOutput, maxFee uint64) uint64 {
	inputCost := uint64(inputTokens) * feePerInput
	if feePerInput > 0 && inputCost/feePerInput != uint64(inputTokens) {
		return maxFee
	}
	outputCost := uint64(outputTokens) * feePerOutput
	if feePerOutput > 0 && outputCost/feePerOutput != uint64(outputTokens) {
		return maxFee
	}
	actualFee := inputCost + outputCost
	if actualFee < inputCost {
		return maxFee
	}
	if actualFee > maxFee {
		actualFee = maxFee
	}
	return actualFee
}

// SecondVerificationEntrySigBytes returns the canonical pre-image that a
// second-tier verifier signs when responding over P2P. It mirrors
// p2p/types.SecondVerificationResponse.SignBytes so a Worker-side signature
// carries through the Proposer into this chain-side batch without being
// re-generated. Any change here must be mirrored in that P2P message type
// or the embedded signatures will stop verifying.
//
// Note: the verifier's pubkey bytes (not the bech32 address) are mixed in,
// matching the P2P side where SecondVerificationResponse.SecondVerifierAddr
// is populated with Verifier.Pubkey at broadcast time.
func SecondVerificationEntrySigBytes(entry types.SecondVerificationBatchEntry, verifierPubkey []byte) []byte {
	h := sha256.New()
	h.Write(entry.TaskId)
	if entry.Pass {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	h.Write(verifierPubkey)
	h.Write(entry.LogitsHash)
	itcBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(itcBuf, entry.VerifiedInputTokens)
	h.Write(itcBuf)
	otcBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(otcBuf, entry.VerifiedOutputTokens)
	h.Write(otcBuf)
	return h.Sum(nil)
}

// ProcessSecondVerificationResultBatch verifies each entry's per-verifier
// signature against the on-chain pubkey and funnels the verified entries
// through the existing single-entry ProcessSecondVerificationResult path.
//
// D2 (issue #9): proposer-batched form that lets verifiers stay pure P2P
// responders — they do not need gas or tx-broadcasting infrastructure.
// Per-entry sig verification matches the triple-sha256 pattern used by P2P
// handleSecondVerificationResponse (explicit sha256.Sum256 on the canonical
// bytes, then cometbft's internal sha256 inside VerifySignature).
//
// Entries with an invalid sig, an unknown/tombstoned second_verifier, or a
// mismatched pubkey are dropped with a log event — they do not fail the
// whole batch, so one malformed entry cannot block the rest.
//
// Returns (accepted, rejected).
func (k Keeper) ProcessSecondVerificationResultBatch(ctx sdk.Context, msg *types.MsgSecondVerificationResultBatch) (uint32, uint32) {
	if msg == nil {
		return 0, 0
	}
	var accepted, rejected uint32
	for i, entry := range msg.Entries {
		if k.processSecondVerificationBatchEntry(ctx, i, entry) {
			accepted++
		} else {
			rejected++
		}
	}
	return accepted, rejected
}

// processSecondVerificationBatchEntry validates one batch entry and forwards
// it to the existing ProcessSecondVerificationResult. Returns true on accept.
func (k Keeper) processSecondVerificationBatchEntry(ctx sdk.Context, i int, entry types.SecondVerificationBatchEntry) bool {
	verifierAddr, err := sdk.AccAddressFromBech32(entry.SecondVerifier)
	if err != nil {
		ctx.Logger().Info("reject batch entry: bad address", "index", i, "second_verifier", entry.SecondVerifier, "err", err)
		return false
	}
	if k.workerKeeper == nil {
		ctx.Logger().Error("reject batch entry: worker keeper unavailable", "index", i)
		return false
	}
	pubkeyStr, found := k.workerKeeper.GetWorkerPubkey(ctx, verifierAddr)
	if !found || len(pubkeyStr) != 33 {
		ctx.Logger().Info("reject batch entry: unknown second_verifier pubkey", "index", i, "second_verifier", entry.SecondVerifier)
		return false
	}
	pubkeyBytes := []byte(pubkeyStr)

	canonical := SecondVerificationEntrySigBytes(entry, pubkeyBytes)
	msgHash := sha256.Sum256(canonical)
	pk := secp256k1.PubKey(pubkeyBytes)
	if !pk.VerifySignature(msgHash[:], entry.Signature) {
		ctx.Logger().Info("reject batch entry: signature verification failed", "index", i, "second_verifier", entry.SecondVerifier)
		return false
	}

	single := &types.MsgSecondVerificationResult{
		SecondVerifier:       entry.SecondVerifier,
		TaskId:               entry.TaskId,
		Epoch:                entry.Epoch,
		Pass:                 entry.Pass,
		LogitsHash:           entry.LogitsHash,
		VerifiedInputTokens:  entry.VerifiedInputTokens,
		VerifiedOutputTokens: entry.VerifiedOutputTokens,
	}
	if err := k.ProcessSecondVerificationResult(ctx, single); err != nil {
		ctx.Logger().Info("reject batch entry: processing failed", "index", i, "second_verifier", entry.SecondVerifier, "err", err)
		return false
	}
	return true
}
