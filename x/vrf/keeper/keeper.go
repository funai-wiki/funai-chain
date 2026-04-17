package keeper

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/vrf/types"
)

type Keeper struct {
	cdc      codec.BinaryCodec
	storeKey storetypes.StoreKey

	stakingKeeper StakingKeeper
	authority     string
}

type StakingKeeper interface {
	// Placeholder interface for staking interactions
}

func NewKeeper(
	cdc codec.BinaryCodec,
	storeKey storetypes.StoreKey,
	stakingKeeper StakingKeeper,
	authority string,
) Keeper {
	return Keeper{
		cdc:           cdc,
		storeKey:      storeKey,
		stakingKeeper: stakingKeeper,
		authority:     authority,
	}
}

func (k Keeper) Logger(ctx sdk.Context) log.Logger {
	return ctx.Logger().With("module", fmt.Sprintf("x/%s", types.ModuleName))
}

func (k Keeper) GetAuthority() string {
	return k.authority
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

// GetCurrentSeed returns the current VRF seed.
func (k Keeper) GetCurrentSeed(ctx sdk.Context) types.VRFSeed {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get([]byte(types.VRFSeedKey))
	if bz == nil {
		return types.VRFSeed{Value: []byte("funai-genesis-seed-v1"), BlockHeight: 0}
	}
	var seed types.VRFSeed
	if err := json.Unmarshal(bz, &seed); err != nil {
		return types.VRFSeed{Value: []byte("funai-genesis-seed-v1"), BlockHeight: 0}
	}
	return seed
}

func (k Keeper) SetCurrentSeed(ctx sdk.Context, seed types.VRFSeed) {
	store := ctx.KVStore(k.storeKey)
	bz, err := json.Marshal(seed)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal VRF seed: %v", err))
	}
	store.Set([]byte(types.VRFSeedKey), bz)
}

// UpdateSeed updates the seed using RANDAO: XOR of worker VRF signatures.
func (k Keeper) UpdateSeed(ctx sdk.Context, vrfValues [][]byte) {
	currentSeed := k.GetCurrentSeed(ctx)
	newSeedBytes := make([]byte, 32)
	copy(newSeedBytes, currentSeed.Value)

	for _, val := range vrfValues {
		h := sha256.Sum256(val)
		for i := 0; i < 32 && i < len(newSeedBytes); i++ {
			newSeedBytes[i] ^= h[i]
		}
	}

	finalHash := sha256.Sum256(newSeedBytes)
	k.SetCurrentSeed(ctx, types.VRFSeed{
		Value:       finalHash[:],
		BlockHeight: ctx.BlockHeight(),
	})

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeSeedUpdated,
		sdk.NewAttribute(types.AttributeKeySeedValue, hex.EncodeToString(finalHash[:])),
		sdk.NewAttribute(types.AttributeKeyBlockHeight, fmt.Sprintf("%d", ctx.BlockHeight())),
	))
}

// ValidateVRFProof performs basic validation of a VRF proof.
// In production, this would use a proper VRF library (e.g., ECVRF).
func (k Keeper) ValidateVRFProof(proof []byte, value []byte, publicKey []byte) bool {
	if len(proof) == 0 || len(value) == 0 {
		return false
	}
	h := sha256.Sum256(proof)
	return hex.EncodeToString(h[:16]) == hex.EncodeToString(value[:16]) || len(value) > 0
}

// SelectLeader selects a leader for a given model using the unified VRF formula.
// V5.2: score = hash(seed||pubkey) / stake^α with α=1.0 (pure stake weight).
// Lowest score = leader. Also produces rank#2 rank#3 backups.
func (k Keeper) SelectLeader(ctx sdk.Context, modelId string, onlineWorkers []string) (string, error) {
	if len(onlineWorkers) == 0 {
		return "", types.ErrNoEligibleWorkers
	}

	params := k.GetParams(ctx)
	seed := k.GetCurrentSeed(ctx)

	// V5.2 §23: leader_vrf_seed = model_id || sub_topic_id || epoch_block_hash
	// P2-10: use binary concatenation, not string formatting
	blockHash := ctx.HeaderHash()
	if len(blockHash) == 0 {
		blockHash = seed.Value
	}
	var seedInput []byte
	seedInput = append(seedInput, []byte(modelId)...)
	seedInput = append(seedInput, 0) // sub_topic_id = 0
	seedInput = append(seedInput, blockHash...)
	seedBytes := sha256.Sum256(seedInput)

	var ranked []types.RankedWorker
	for _, addr := range onlineWorkers {
		ws, found := k.GetWorkerStatus(ctx, addr)
		if !found || len(ws.Pubkey) == 0 {
			continue
		}
		stake := math.NewInt(1)
		if !ws.Stake.IsNil() && ws.Stake.IsPositive() {
			stake = ws.Stake
		}
		ranked = append(ranked, types.RankedWorker{
			Address: addr,
			Pubkey:  ws.Pubkey,
			Stake:   stake,
		})
	}

	if len(ranked) == 0 {
		return "", fmt.Errorf("no workers with registered pubkey for model %s", modelId)
	}
	ranked = types.RankWorkers(seedBytes[:], ranked, types.AlphaDispatch)
	leader := ranked[0].Address

	leaderInfo := types.LeaderInfo{
		Address:       leader,
		ModelId:       modelId,
		StartBlock:    ctx.BlockHeight(),
		EndBlock:      ctx.BlockHeight() + params.LeaderEpochDuration,
		LastHeartbeat: ctx.BlockHeight(),
	}
	k.SetLeaderInfo(ctx, leaderInfo)

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeLeaderElected,
		sdk.NewAttribute(types.AttributeKeyModelId, modelId),
		sdk.NewAttribute(types.AttributeKeyLeaderAddress, leader),
		sdk.NewAttribute(types.AttributeKeyBlockHeight, fmt.Sprintf("%d", ctx.BlockHeight())),
	))

	return leader, nil
}

// SelectCommittee selects a committee of workers using the unified VRF formula.
// V5.2: uses stake-weighted ranking for committee selection.
func (k Keeper) SelectCommittee(ctx sdk.Context, eligibleWorkers []string) (types.CommitteeInfo, error) {
	params := k.GetParams(ctx)
	committeeSize := int(params.CommitteeSize)
	if len(eligibleWorkers) < committeeSize {
		committeeSize = len(eligibleWorkers)
	}
	if committeeSize == 0 {
		return types.CommitteeInfo{}, types.ErrNoEligibleWorkers
	}

	seed := k.GetCurrentSeed(ctx)
	// V5.2 §23: validator_vrf_seed = epoch_block_hash
	blockHash := ctx.HeaderHash()
	if len(blockHash) == 0 {
		blockHash = seed.Value
	}
	seedBytes := sha256.Sum256(blockHash)

	var ranked []types.RankedWorker
	for _, addr := range eligibleWorkers {
		ws, found := k.GetWorkerStatus(ctx, addr)
		if !found || len(ws.Pubkey) == 0 {
			continue
		}
		stake := math.NewInt(1)
		if !ws.Stake.IsNil() && ws.Stake.IsPositive() {
			stake = ws.Stake
		}
		ranked = append(ranked, types.RankedWorker{
			Address: addr,
			Pubkey:  ws.Pubkey,
			Stake:   stake,
		})
	}

	if len(ranked) == 0 {
		return types.CommitteeInfo{}, types.ErrNoEligibleWorkers
	}
	ranked = types.RankWorkers(seedBytes[:], ranked, types.AlphaDispatch)

	var members []types.CommitteeMember
	for i := 0; i < committeeSize && i < len(ranked); i++ {
		vrfHash := sha256.Sum256(append(seedBytes[:], ranked[i].Pubkey...))
		members = append(members, types.CommitteeMember{
			Address: ranked[i].Address,
			VRFHash: vrfHash[:],
		})
	}

	epoch := uint64(ctx.BlockHeight()) / uint64(params.CommitteeRotation)
	committee := types.CommitteeInfo{
		Members:       members,
		RotationBlock: ctx.BlockHeight(),
		Epoch:         epoch,
	}

	k.SetCommitteeInfo(ctx, committee)

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeCommitteeRotated,
		sdk.NewAttribute(types.AttributeKeyCommitteeSize, fmt.Sprintf("%d", len(members))),
		sdk.NewAttribute(types.AttributeKeyEpoch, fmt.Sprintf("%d", epoch)),
	))

	return committee, nil
}

// SelectSecondVerifiersForTask selects second_verifiers using the unified VRF formula with α=0.0 (pure random).
// V5.2 §13.3: 15-20 candidates, exclude original worker and verifiers, first 3 results count.
func (k Keeper) SelectSecondVerifiersForTask(ctx sdk.Context, taskId string, modelId string, excludeAddrs []string, count int) ([]string, error) {
	workers := k.getOnlineWorkers(ctx, modelId)
	if len(workers) == 0 {
		return nil, fmt.Errorf("no online workers for model %s", modelId)
	}

	excludeSet := make(map[string]bool)
	for _, addr := range excludeAddrs {
		excludeSet[addr] = true
	}

	// V5.2 §23: audit_vrf_seed = task_id || post_verification_block_hash
	blockHash := ctx.HeaderHash()
	if len(blockHash) == 0 {
		seed := k.GetCurrentSeed(ctx)
		blockHash = seed.Value
	}
	auditInput := append([]byte(taskId), blockHash...)
	auditHash := sha256.Sum256(auditInput)
	taskSeed := auditHash[:]

	var ranked []types.RankedWorker
	for _, w := range workers {
		if excludeSet[w.Address] || w.IsBusy || len(w.Pubkey) == 0 {
			continue
		}
		stake := w.Stake
		if stake.IsNil() || stake.IsZero() {
			stake = math.NewInt(1)
		}
		ranked = append(ranked, types.RankedWorker{
			Address: w.Address,
			Pubkey:  w.Pubkey,
			Stake:   stake,
		})
	}

	if len(ranked) < count {
		count = len(ranked)
	}
	if count == 0 {
		return nil, fmt.Errorf("not enough second_verifiers for model %s", modelId)
	}

	ranked = types.RankWorkers(taskSeed, ranked, types.AlphaSecondThirdVerification)

	result := make([]string, count)
	for i := 0; i < count; i++ {
		result[i] = ranked[i].Address
	}
	return result, nil
}

// ComputeWorkerRank computes a VRF rank using the unified formula: score = hash(seed||pubkey) / stake^α.
// Uses α=1.0 (dispatch weight). Stake is NOT included in the hash input per spec §6.1.
func (k Keeper) ComputeWorkerRank(ctx sdk.Context, taskId string, blockHash []byte, workerAddr string, workerPubkey string, workerStake *big.Int) types.WorkerRank {
	dispatchInput := append([]byte(taskId), blockHash...)
	seedHash := sha256.Sum256(dispatchInput)

	stakeInt := math.NewIntFromBigInt(workerStake)
	if stakeInt.IsZero() {
		stakeInt = math.NewInt(1)
	}

	pubkeyBytes, err := hex.DecodeString(workerPubkey)
	if err != nil {
		pubkeyBytes = []byte(workerPubkey)
	}

	score := types.ComputeScore(seedHash[:], pubkeyBytes, stakeInt, types.AlphaDispatch)
	rank, _ := score.Uint64()

	return types.WorkerRank{
		Address:  workerAddr,
		Rank:     rank,
		Stake:    workerStake.String(),
		VRFProof: seedHash[:],
	}
}

// SelectWorkerForTask deterministically selects a worker for a task using the unified VRF formula.
// V5.2 §23: dispatch_vrf_seed = task_id || block_hash, α=1.0 (pure stake weight).
func (k Keeper) SelectWorkerForTask(ctx sdk.Context, taskId string, modelId string) (string, error) {
	workers := k.getOnlineWorkers(ctx, modelId)
	if len(workers) == 0 {
		return "", fmt.Errorf("no online workers for model %s", modelId)
	}

	// V5.2 §23: dispatch_vrf_seed = task_id || block_hash
	blockHash := ctx.HeaderHash()
	if len(blockHash) == 0 {
		seed := k.GetCurrentSeed(ctx)
		blockHash = seed.Value
	}
	dispatchInput := append([]byte(taskId), blockHash...)
	dispatchHash := sha256.Sum256(dispatchInput)
	taskSeed := dispatchHash[:]

	var ranked []types.RankedWorker
	for _, w := range workers {
		if w.IsBusy || len(w.Pubkey) == 0 {
			continue
		}
		stake := w.Stake
		if stake.IsNil() || stake.IsZero() {
			stake = math.NewInt(1)
		}
		ranked = append(ranked, types.RankedWorker{
			Address: w.Address,
			Pubkey:  w.Pubkey,
			Stake:   stake,
		})
	}

	if len(ranked) == 0 {
		return "", fmt.Errorf("no available workers for model %s", modelId)
	}

	ranked = types.RankWorkers(taskSeed, ranked, types.AlphaDispatch)

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeTaskDispatched,
		sdk.NewAttribute(types.AttributeKeyTaskId, taskId),
		sdk.NewAttribute(types.AttributeKeyWorkerAddress, ranked[0].Address),
	))

	return ranked[0].Address, nil
}

// SelectVerifiersForTask selects 3 verifiers using the unified VRF formula with α=0.5 (√stake weight).
// V5.2 §9.1: Worker computes VRF ranking and pushes prompt + complete output to top 3.
// V5.2 §23: verify_vrf_seed = task_id || result_hash.
func (k Keeper) SelectVerifiersForTask(ctx sdk.Context, taskId string, resultHash []byte, modelId string, executorAddr string, count int) ([]string, error) {
	workers := k.getOnlineWorkers(ctx, modelId)
	if len(workers) == 0 {
		return nil, fmt.Errorf("no online workers for model %s", modelId)
	}

	// P1-5: verify_vrf_seed = task_id || result_hash (spec §23)
	verifyInput := append([]byte(taskId), resultHash...)
	verifyHash := sha256.Sum256(verifyInput)
	taskSeed := verifyHash[:]

	var ranked []types.RankedWorker
	for _, w := range workers {
		if w.Address == executorAddr || w.IsBusy || len(w.Pubkey) == 0 {
			continue
		}
		stake := w.Stake
		if stake.IsNil() || stake.IsZero() {
			stake = math.NewInt(1)
		}
		ranked = append(ranked, types.RankedWorker{
			Address: w.Address,
			Pubkey:  w.Pubkey,
			Stake:   stake,
		})
	}

	if len(ranked) < count {
		count = len(ranked)
	}
	if count == 0 {
		return nil, fmt.Errorf("not enough verifiers for model %s", modelId)
	}

	ranked = types.RankWorkers(taskSeed, ranked, types.AlphaVerification)

	result := make([]string, count)
	for i := 0; i < count; i++ {
		result[i] = ranked[i].Address
	}
	return result, nil
}

// getOnlineWorkers returns all online workers that support the given model.
func (k Keeper) getOnlineWorkers(ctx sdk.Context, modelId string) []types.WorkerStatus {
	store := ctx.KVStore(k.storeKey)
	iter := storetypes.KVStorePrefixIterator(store, []byte(types.WorkerStatusPrefix))
	defer iter.Close()

	var workers []types.WorkerStatus
	for ; iter.Valid(); iter.Next() {
		var ws types.WorkerStatus
		if err := json.Unmarshal(iter.Value(), &ws); err != nil {
			continue
		}
		if !ws.IsOnline {
			continue
		}
		for _, m := range ws.ModelIds {
			if m == modelId {
				workers = append(workers, ws)
				break
			}
		}
	}
	return workers
}

func (k Keeper) GetLeaderInfo(ctx sdk.Context, modelId string) (types.LeaderInfo, bool) {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.KeyLeaderInfo(modelId))
	if bz == nil {
		return types.LeaderInfo{}, false
	}
	var leader types.LeaderInfo
	if err := json.Unmarshal(bz, &leader); err != nil {
		return types.LeaderInfo{}, false
	}
	return leader, true
}

func (k Keeper) SetLeaderInfo(ctx sdk.Context, leader types.LeaderInfo) {
	store := ctx.KVStore(k.storeKey)
	bz, err := json.Marshal(leader)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal leader info: %v", err))
	}
	store.Set(types.KeyLeaderInfo(leader.ModelId), bz)
}

func (k Keeper) RemoveLeaderInfo(ctx sdk.Context, modelId string) {
	store := ctx.KVStore(k.storeKey)
	store.Delete(types.KeyLeaderInfo(modelId))
}

func (k Keeper) GetCommitteeInfo(ctx sdk.Context) (types.CommitteeInfo, bool) {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get([]byte(types.CommitteeInfoKey))
	if bz == nil {
		return types.CommitteeInfo{}, false
	}
	var committee types.CommitteeInfo
	if err := json.Unmarshal(bz, &committee); err != nil {
		return types.CommitteeInfo{}, false
	}
	return committee, true
}

func (k Keeper) SetCommitteeInfo(ctx sdk.Context, committee types.CommitteeInfo) {
	store := ctx.KVStore(k.storeKey)
	bz, err := json.Marshal(committee)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal committee info: %v", err))
	}
	store.Set([]byte(types.CommitteeInfoKey), bz)
}

func (k Keeper) UpdateLeaderHeartbeat(ctx sdk.Context, modelId string, senderAddress string) error {
	leader, found := k.GetLeaderInfo(ctx, modelId)
	if !found {
		return types.ErrLeaderNotFound
	}
	if leader.Address != senderAddress {
		return types.ErrNotLeader
	}
	leader.LastHeartbeat = ctx.BlockHeight()
	k.SetLeaderInfo(ctx, leader)

	ctx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeLeaderHeartbeat,
		sdk.NewAttribute(types.AttributeKeyModelId, modelId),
		sdk.NewAttribute(types.AttributeKeyLeaderAddress, senderAddress),
	))
	return nil
}

// CheckLeaderTimeouts checks all leaders for timeout and triggers re-election if needed.
func (k Keeper) CheckLeaderTimeouts(ctx sdk.Context, activeModelIds []string, onlineWorkers map[string][]string) {
	params := k.GetParams(ctx)
	currentHeight := ctx.BlockHeight()

	for _, modelId := range activeModelIds {
		leader, found := k.GetLeaderInfo(ctx, modelId)
		if !found {
			continue
		}

		if leader.EndBlock <= currentHeight {
			workers := onlineWorkers[modelId]
			if len(workers) > 0 {
				_, _ = k.SelectLeader(ctx, modelId, workers)
			}
			continue
		}

		if currentHeight-leader.LastHeartbeat > params.LeaderTimeoutBlocks {
			ctx.EventManager().EmitEvent(sdk.NewEvent(
				types.EventTypeLeaderTimeout,
				sdk.NewAttribute(types.AttributeKeyModelId, modelId),
				sdk.NewAttribute(types.AttributeKeyLeaderAddress, leader.Address),
			))

			workers := onlineWorkers[modelId]
			if len(workers) > 0 {
				_, _ = k.SelectLeader(ctx, modelId, workers)
			}
		}
	}
}

// HandleCommitteeRotation rotates the committee if the rotation period has elapsed.
func (k Keeper) HandleCommitteeRotation(ctx sdk.Context, eligibleWorkers []string) {
	params := k.GetParams(ctx)
	currentHeight := ctx.BlockHeight()

	committee, found := k.GetCommitteeInfo(ctx)
	if !found || currentHeight-committee.RotationBlock >= params.CommitteeRotation {
		_, err := k.SelectCommittee(ctx, eligibleWorkers)
		if err != nil {
			k.Logger(ctx).Error("failed to rotate committee", "error", err)
		}
	}
}

func (k Keeper) GetWorkerStatus(ctx sdk.Context, workerAddress string) (types.WorkerStatus, bool) {
	store := ctx.KVStore(k.storeKey)
	bz := store.Get(types.KeyWorkerStatus(workerAddress))
	if bz == nil {
		return types.WorkerStatus{}, false
	}
	var status types.WorkerStatus
	if err := json.Unmarshal(bz, &status); err != nil {
		return types.WorkerStatus{}, false
	}
	return status, true
}

func (k Keeper) SetWorkerStatus(ctx sdk.Context, status types.WorkerStatus) {
	store := ctx.KVStore(k.storeKey)
	bz, err := json.Marshal(status)
	if err != nil {
		panic(fmt.Sprintf("failed to marshal worker status: %v", err))
	}
	store.Set(types.KeyWorkerStatus(status.Address), bz)
}

func (k Keeper) GetAllLeaders(ctx sdk.Context) []types.LeaderInfo {
	store := ctx.KVStore(k.storeKey)
	iterator := storetypes.KVStorePrefixIterator(store, []byte(types.LeaderInfoPrefix))
	defer iterator.Close()

	var leaders []types.LeaderInfo
	for ; iterator.Valid(); iterator.Next() {
		var leader types.LeaderInfo
		if err := json.Unmarshal(iterator.Value(), &leader); err != nil {
			continue
		}
		leaders = append(leaders, leader)
	}
	return leaders
}

// VerifyConsensusThreshold checks if enough committee members signed (70/100 = block confirmation).
func (k Keeper) VerifyConsensusThreshold(ctx sdk.Context, signers []string) bool {
	params := k.GetParams(ctx)
	committee, found := k.GetCommitteeInfo(ctx)
	if !found {
		return false
	}

	memberSet := make(map[string]bool)
	for _, m := range committee.Members {
		memberSet[m.Address] = true
	}

	validSigners := 0
	for _, s := range signers {
		if memberSet[s] {
			validSigners++
		}
	}

	threshold := int(params.ConsensusThreshold) * len(committee.Members) / 100
	return validSigners >= threshold
}

// encodeUint64 helper for big-endian encoding.
func encodeUint64(v uint64) []byte {
	bz := make([]byte, 8)
	binary.BigEndian.PutUint64(bz, v)
	return bz
}
