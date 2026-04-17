package types

import "encoding/binary"

const (
	ModuleName        = "settlement"
	StoreKey          = ModuleName
	RouterKey         = ModuleName
	QuerierRoute      = ModuleName
	ModuleAccountName = ModuleName
	DefaultDenom      = "ufai"
)

var (
	InferenceAccountKeyPrefix                 = []byte{0x01}
	SettledTaskKeyPrefix                      = []byte{0x02}
	FraudMarkKeyPrefix                        = []byte{0x03}
	SecondVerificationRecordKeyPrefix         = []byte{0x04}
	ParamsKey                                 = []byte{0x05}
	BatchRecordKeyPrefix                      = []byte{0x06}
	BatchCounterKey                           = []byte{0x07}
	SecondVerificationPendingKeyPrefix        = []byte{0x08}
	ThirdVerificationPendingKeyPrefix         = []byte{0x09}
	EpochStatsKeyPrefix                       = []byte{0x0A}
	SecondVerificationRateKey                 = []byte{0x0B}
	ThirdVerificationRateKey                  = []byte{0x0C}
	SecondVerificationPendingTimeoutKeyPrefix = []byte{0x0D} // height-indexed for efficient timeout lookup
	ThirdVerificationPendingTimeoutKeyPrefix  = []byte{0x0E}
	WorkerSnapshotKeyPrefix                   = []byte{0x0F} // P1-8: per-worker epoch snapshot
	WorkerEpochContribKeyPrefix               = []byte{0x10} // P1-8: per-worker epoch contribution
	VerifierEpochCountKeyPrefix               = []byte{0x11} // P1-9: per-worker verification count in epoch
	SecondVerifierEpochCountKeyPrefix         = []byte{0x12} // P1-9: per-worker audit count in epoch
	BlockSignerCountKeyPrefix                 = []byte{0x13} // P1-10: per-validator block signing count in epoch
	DishonestCountKeyPrefix                   = []byte{0x14} // S9: per-worker dishonest token count
	FrozenBalanceKeyPrefix                    = []byte{0x15} // S9: per-task frozen max_fee
	FrozenTaskIndexKeyPrefix                  = []byte{0x16} // S9: expireBlock→taskId index for timeout scan
	TokenMismatchKeyPrefix                    = []byte{0x17} // S9: Worker-Verifier pair mismatch tracking
	VerifierEpochFeeKeyPrefix                 = []byte{0x18} // per-worker verification-fee earned in epoch (for 85/15 reward weighting)
	SecondVerifierEpochFeeKeyPrefix           = []byte{0x19} // per-worker 2nd/3rd-verification-fee earned in epoch
)

func InferenceAccountKey(userAddr []byte) []byte {
	return append(InferenceAccountKeyPrefix, userAddr...)
}

func SettledTaskKey(taskID []byte) []byte {
	return append(SettledTaskKeyPrefix, taskID...)
}

func FraudMarkKey(taskID []byte) []byte {
	return append(FraudMarkKeyPrefix, taskID...)
}

func SecondVerificationRecordKey(taskID []byte) []byte {
	return append(SecondVerificationRecordKeyPrefix, taskID...)
}

func BatchRecordKey(batchId uint64) []byte {
	bz := make([]byte, 8)
	binary.BigEndian.PutUint64(bz, batchId)
	return append(BatchRecordKeyPrefix, bz...)
}

func SecondVerificationPendingKey(taskID []byte) []byte {
	return append(SecondVerificationPendingKeyPrefix, taskID...)
}

func ThirdVerificationPendingKey(taskID []byte) []byte {
	return append(ThirdVerificationPendingKeyPrefix, taskID...)
}

func EpochStatsKey(epoch int64) []byte {
	bz := make([]byte, 8)
	binary.BigEndian.PutUint64(bz, uint64(epoch))
	return append(EpochStatsKeyPrefix, bz...)
}

// WorkerSnapshotKey returns the key for a worker's epoch snapshot.
func WorkerSnapshotKey(workerAddr []byte) []byte {
	return append(WorkerSnapshotKeyPrefix, workerAddr...)
}

// WorkerEpochContribKey returns the key for a worker's current epoch contribution.
func WorkerEpochContribKey(workerAddr []byte) []byte {
	return append(WorkerEpochContribKeyPrefix, workerAddr...)
}

// VerifierEpochCountKey returns the key for a verifier's epoch verification count.
func VerifierEpochCountKey(workerAddr []byte) []byte {
	return append(VerifierEpochCountKeyPrefix, workerAddr...)
}

// SecondVerifierEpochCountKey returns the key for an second_verifier's epoch audit count.
func SecondVerifierEpochCountKey(workerAddr []byte) []byte {
	return append(SecondVerifierEpochCountKeyPrefix, workerAddr...)
}

// VerifierEpochFeeKey returns the key for a verifier's epoch fee-earned total.
func VerifierEpochFeeKey(workerAddr []byte) []byte {
	return append(VerifierEpochFeeKeyPrefix, workerAddr...)
}

// SecondVerifierEpochFeeKey returns the key for an second_verifier's (2nd/3rd verifier) epoch fee-earned total.
func SecondVerifierEpochFeeKey(workerAddr []byte) []byte {
	return append(SecondVerifierEpochFeeKeyPrefix, workerAddr...)
}

// BlockSignerCountKey returns key for a validator's block signing count.
func BlockSignerCountKey(validatorAddr string) []byte {
	return append(BlockSignerCountKeyPrefix, []byte(validatorAddr)...)
}

// DishonestCountKey returns the key for a worker's dishonest token reporting count (S9).
func DishonestCountKey(workerAddr []byte) []byte {
	return append(DishonestCountKeyPrefix, workerAddr...)
}

// FrozenBalanceKey returns the key for a task's frozen max_fee (S9).
func FrozenBalanceKey(taskID []byte) []byte {
	return append(FrozenBalanceKeyPrefix, taskID...)
}

// FrozenTaskIndexKey stores expireBlock + taskId for efficient timeout scanning.
// Format: prefix + expireBlock(8 bytes BE) + taskId
func FrozenTaskIndexKey(expireBlock int64, taskID []byte) []byte {
	bz := make([]byte, 8)
	binary.BigEndian.PutUint64(bz, uint64(expireBlock))
	key := append(FrozenTaskIndexKeyPrefix, bz...)
	return append(key, taskID...)
}

// TokenMismatchKey returns key for a Worker-Verifier pair mismatch record.
func TokenMismatchKey(workerAddr, verifierAddr string) []byte {
	key := append(TokenMismatchKeyPrefix, []byte(workerAddr)...)
	key = append(key, byte('|'))
	return append(key, []byte(verifierAddr)...)
}

// TokenMismatchPrefixForWorker returns prefix for scanning all pairs of a worker.
func TokenMismatchPrefixForWorker(workerAddr string) []byte {
	key := append(TokenMismatchKeyPrefix, []byte(workerAddr)...)
	return append(key, byte('|'))
}

// SecondVerificationPendingTimeoutKey returns key: prefix + height(8 bytes) + taskID.
// Enables efficient range scan for timed-out tasks by height.
func SecondVerificationPendingTimeoutKey(height int64, taskID []byte) []byte {
	bz := make([]byte, 8)
	binary.BigEndian.PutUint64(bz, uint64(height))
	key := append(SecondVerificationPendingTimeoutKeyPrefix, bz...)
	return append(key, taskID...)
}

func ThirdVerificationPendingTimeoutKey(height int64, taskID []byte) []byte {
	bz := make([]byte, 8)
	binary.BigEndian.PutUint64(bz, uint64(height))
	key := append(ThirdVerificationPendingTimeoutKeyPrefix, bz...)
	return append(key, taskID...)
}

// SecondVerificationPendingTimeoutPrefixUpTo returns prefix for scanning all timeout keys up to a given height (inclusive).
func SecondVerificationPendingTimeoutPrefixUpTo(maxHeight int64) []byte {
	bz := make([]byte, 8)
	binary.BigEndian.PutUint64(bz, uint64(maxHeight+1))
	return append(SecondVerificationPendingTimeoutKeyPrefix, bz...)
}

func ThirdVerificationPendingTimeoutPrefixUpTo(maxHeight int64) []byte {
	bz := make([]byte, 8)
	binary.BigEndian.PutUint64(bz, uint64(maxHeight+1))
	return append(ThirdVerificationPendingTimeoutKeyPrefix, bz...)
}
