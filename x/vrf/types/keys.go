package types

import (
	"encoding/binary"
)

const (
	ModuleName = "vrf"
	StoreKey   = ModuleName
	RouterKey  = ModuleName

	ParamsKey          = "Params"
	VRFSeedKey         = "VRFSeed"
	LeaderInfoPrefix   = "LeaderInfo/value/"
	CommitteeInfoKey   = "CommitteeInfo"
	VRFProofPrefix     = "VRFProof/value/"
	WorkerStatusPrefix = "WorkerStatus/value/"
)

func uint64ToBytes(v uint64) []byte {
	bz := make([]byte, 8)
	binary.BigEndian.PutUint64(bz, v)
	return bz
}

func KeyLeaderInfo(modelId string) []byte {
	return []byte(LeaderInfoPrefix + modelId)
}

func KeyVRFProof(blockHeight int64) []byte {
	return append([]byte(VRFProofPrefix), uint64ToBytes(uint64(blockHeight))...)
}

func KeyWorkerStatus(workerAddress string) []byte {
	return []byte(WorkerStatusPrefix + workerAddress)
}
