package types

import (
	"encoding/binary"
)

const (
	ModuleName = "reward"
	StoreKey   = ModuleName
	RouterKey  = ModuleName

	// Store key prefixes
	ParamsKey                = "Params"
	RewardRecordKeyPrefix    = "RewardRecord/value/"
	EpochRewardSummaryPrefix = "EpochRewardSummary/value/"
)

// KeyRewardRecord returns the store key for a reward record using binary encoding for the epoch number.
func KeyRewardRecord(epoch int64, workerAddress string) []byte {
	prefix := []byte(RewardRecordKeyPrefix)
	epochBz := make([]byte, 8)
	binary.BigEndian.PutUint64(epochBz, uint64(epoch))
	key := make([]byte, 0, len(prefix)+8+1+len(workerAddress))
	key = append(key, prefix...)
	key = append(key, epochBz...)
	key = append(key, '/')
	key = append(key, []byte(workerAddress)...)
	return key
}

// KeyEpochRewardSummary returns the store key for an epoch reward summary using binary encoding.
func KeyEpochRewardSummary(epoch int64) []byte {
	prefix := []byte(EpochRewardSummaryPrefix)
	epochBz := make([]byte, 8)
	binary.BigEndian.PutUint64(epochBz, uint64(epoch))
	key := make([]byte, 0, len(prefix)+8)
	key = append(key, prefix...)
	key = append(key, epochBz...)
	return key
}
