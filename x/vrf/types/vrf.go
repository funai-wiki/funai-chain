package types

import (
	"crypto/sha256"
	"fmt"
	"math/big"
	"sort"

	"cosmossdk.io/math"
)

// VRFAlpha is the exponent in the unified VRF formula: score = hash(seed||pubkey) / stake^α
// Lower score = higher rank.
type VRFAlpha float64

const (
	// AlphaDispatch (α=1.0): dispatch selection — pure stake weight. Higher stake = higher rank probability.
	AlphaDispatch VRFAlpha = 1.0
	// AlphaVerification (α=0.5): verifier selection — √stake weight. Balances stake and randomness.
	AlphaVerification VRFAlpha = 0.5
	// AlphaAudit (α=0.0): audit selection — equal probability, ignores stake.
	AlphaAudit VRFAlpha = 0.0
)

// ComputeScore calculates VRF score = hash(seed || pubkey) / stake^α
// Lower score = higher rank.
// When α=0.0, stake is ignored (equal probability).
func ComputeScore(seed []byte, pubkey []byte, stakeAmount math.Int, alpha VRFAlpha) *big.Float {
	data := append(append([]byte{}, seed...), pubkey...)
	h := sha256.Sum256(data)
	hashInt := new(big.Int).SetBytes(h[:])
	hashFloat := new(big.Float).SetPrec(256).SetInt(hashInt)

	if alpha == 0.0 || stakeAmount.IsZero() {
		return hashFloat
	}

	stakeFloat := new(big.Float).SetPrec(256).SetInt(stakeAmount.BigInt())

	if alpha == 1.0 {
		return new(big.Float).SetPrec(256).Quo(hashFloat, stakeFloat)
	}

	// α=0.5: divide by √stake
	sqrtStake := new(big.Float).SetPrec(256).Sqrt(stakeFloat)
	return new(big.Float).SetPrec(256).Quo(hashFloat, sqrtStake)
}

// RankedWorker holds worker info for VRF ranking.
type RankedWorker struct {
	Address      string     `protobuf:"bytes,1,opt,name=address,proto3" json:"address"`
	Pubkey       []byte     `protobuf:"bytes,2,opt,name=pubkey,proto3" json:"pubkey"`
	Stake        math.Int   `protobuf:"bytes,3,opt,name=stake,proto3" json:"stake"`
	Score        *big.Float `json:"-"`
	Reputation   float64    `json:"-"` // Audit KT §3: 0.0-1.2, default 1.0
	AvgLatencyMs uint32     `json:"-"` // Audit KT §5: average first-token latency
}

// RankWorkers computes VRF scores and sorts workers by score ascending.
// Lower score = higher rank (rank 1 = lowest score).
// Audit KT §3+§5: effective weight = stake × reputation. Reputation=0 → treated as 1.0.
func RankWorkers(seed []byte, workers []RankedWorker, alpha VRFAlpha) []RankedWorker {
	for i := range workers {
		effectiveStake := workers[i].Stake
		rep := workers[i].Reputation
		if rep <= 0 {
			rep = 1.0 // uninitialized → neutral
		}
		if rep != 1.0 {
			// Scale stake by reputation: effectiveStake = stake × reputation
			scaledBig := new(big.Float).SetPrec(256).SetInt(effectiveStake.BigInt())
			scaledBig.Mul(scaledBig, new(big.Float).SetPrec(256).SetFloat64(rep))
			scaledInt, _ := scaledBig.Int(nil)
			if scaledInt.Sign() <= 0 {
				scaledInt = big.NewInt(1)
			}
			effectiveStake = math.NewIntFromBigInt(scaledInt)
		}
		workers[i].Score = ComputeScore(seed, workers[i].Pubkey, effectiveStake, alpha)
	}
	sort.Slice(workers, func(i, j int) bool {
		cmp := workers[i].Score.Cmp(workers[j].Score)
		if cmp != 0 {
			return cmp < 0
		}
		// Deterministic tie-breaking: lower address wins
		return workers[i].Address < workers[j].Address
	})
	return workers
}

// LatencyFactor computes the VRF weight multiplier based on worker's avg latency
// vs the request's MaxLatencyMs. Returns 1.0 if no latency constraint.
// Audit KT §5: fast→1.5, ok→1.0, slow→0.1
func LatencyFactor(workerAvgMs uint32, maxLatencyMs uint32) float64 {
	if maxLatencyMs == 0 || workerAvgMs == 0 {
		return 1.0
	}
	threshold := float64(maxLatencyMs)
	avg := float64(workerAvgMs)
	if avg < threshold*0.5 {
		return 1.5 // fast node boost
	}
	if avg < threshold*0.8 {
		return 1.0
	}
	return 0.1 // slow node penalty
}

type VRFOutput struct {
	Proof     []byte `protobuf:"bytes,1,opt,name=proof,proto3" json:"proof"`
	Value     []byte `protobuf:"bytes,2,opt,name=value,proto3" json:"value"`
	PublicKey []byte `protobuf:"bytes,3,opt,name=public_key,proto3" json:"public_key"`
}

func (m *VRFOutput) ProtoMessage()  {}
func (m *VRFOutput) Reset()         { *m = VRFOutput{} }
func (m *VRFOutput) String() string { return "VRFOutput" }

type LeaderInfo struct {
	Address       string `protobuf:"bytes,1,opt,name=address,proto3" json:"address"`
	ModelId       string `protobuf:"bytes,2,opt,name=model_id,proto3" json:"model_id"`
	StartBlock    int64  `protobuf:"varint,3,opt,name=start_block,proto3" json:"start_block"`
	EndBlock      int64  `protobuf:"varint,4,opt,name=end_block,proto3" json:"end_block"`
	LastHeartbeat int64  `protobuf:"varint,5,opt,name=last_heartbeat,proto3" json:"last_heartbeat"`
}

func (m *LeaderInfo) ProtoMessage()  {}
func (m *LeaderInfo) Reset()         { *m = LeaderInfo{} }
func (m *LeaderInfo) String() string { return fmt.Sprintf("LeaderInfo{%s}", m.Address) }

type CommitteeMember struct {
	Address string `protobuf:"bytes,1,opt,name=address,proto3" json:"address"`
	VRFHash []byte `protobuf:"bytes,2,opt,name=vrf_hash,proto3" json:"vrf_hash"`
}

func (m *CommitteeMember) ProtoMessage()  {}
func (m *CommitteeMember) Reset()         { *m = CommitteeMember{} }
func (m *CommitteeMember) String() string { return fmt.Sprintf("CommitteeMember{%s}", m.Address) }

type CommitteeInfo struct {
	Members       []CommitteeMember `protobuf:"bytes,1,rep,name=members,proto3" json:"members"`
	RotationBlock int64             `protobuf:"varint,2,opt,name=rotation_block,proto3" json:"rotation_block"`
	Epoch         uint64            `protobuf:"varint,3,opt,name=epoch,proto3" json:"epoch"`
}

func (m *CommitteeInfo) ProtoMessage()  {}
func (m *CommitteeInfo) Reset()         { *m = CommitteeInfo{} }
func (m *CommitteeInfo) String() string { return "CommitteeInfo" }

type VRFSeed struct {
	Value       []byte `protobuf:"bytes,1,opt,name=value,proto3" json:"value"`
	BlockHeight int64  `protobuf:"varint,2,opt,name=block_height,proto3" json:"block_height"`
}

func (m *VRFSeed) ProtoMessage()  {}
func (m *VRFSeed) Reset()         { *m = VRFSeed{} }
func (m *VRFSeed) String() string { return "VRFSeed" }

type WorkerStatus struct {
	Address  string   `protobuf:"bytes,1,opt,name=address,proto3" json:"address"`
	Pubkey   []byte   `protobuf:"bytes,2,opt,name=pubkey,proto3" json:"pubkey"`
	IsOnline bool     `protobuf:"varint,3,opt,name=is_online,proto3" json:"is_online"`
	IsBusy   bool     `protobuf:"varint,4,opt,name=is_busy,proto3" json:"is_busy"`
	ModelIds []string `protobuf:"bytes,5,rep,name=model_ids,proto3" json:"model_ids"`
	Stake    math.Int `protobuf:"bytes,6,opt,name=stake,proto3" json:"stake"`
}

func (m *WorkerStatus) ProtoMessage()  {}
func (m *WorkerStatus) Reset()         { *m = WorkerStatus{} }
func (m *WorkerStatus) String() string { return fmt.Sprintf("WorkerStatus{%s}", m.Address) }

type TaskDispatchResult struct {
	TaskId        string `protobuf:"bytes,1,opt,name=task_id,proto3" json:"task_id"`
	WorkerAddress string `protobuf:"bytes,2,opt,name=worker_address,proto3" json:"worker_address"`
	VRFProof      []byte `protobuf:"bytes,3,opt,name=vrf_proof,proto3" json:"vrf_proof"`
}

func (m *TaskDispatchResult) ProtoMessage()  {}
func (m *TaskDispatchResult) Reset()         { *m = TaskDispatchResult{} }
func (m *TaskDispatchResult) String() string { return fmt.Sprintf("TaskDispatchResult{%s}", m.TaskId) }

// TaskDispatchBroadcast represents a task broadcast by the leader (V5.1.1).
type TaskDispatchBroadcast struct {
	TaskId      string `protobuf:"bytes,1,opt,name=task_id,proto3" json:"task_id"`
	ModelId     string `protobuf:"bytes,2,opt,name=model_id,proto3" json:"model_id"`
	BlockHash   []byte `protobuf:"bytes,3,opt,name=block_hash,proto3" json:"block_hash"`
	BroadcastAt int64  `protobuf:"varint,4,opt,name=broadcast_at,proto3" json:"broadcast_at"`
}

func (m *TaskDispatchBroadcast) ProtoMessage() {}
func (m *TaskDispatchBroadcast) Reset()        { *m = TaskDispatchBroadcast{} }
func (m *TaskDispatchBroadcast) String() string {
	return fmt.Sprintf("TaskDispatchBroadcast{%s}", m.TaskId)
}

// WorkerRank represents a worker's VRF-computed rank for a task (V5.1.1).
type WorkerRank struct {
	Address  string `protobuf:"bytes,1,opt,name=address,proto3" json:"address"`
	Rank     uint64 `protobuf:"varint,2,opt,name=rank,proto3" json:"rank"`
	Stake    string `protobuf:"bytes,3,opt,name=stake,proto3" json:"stake"`
	VRFProof []byte `protobuf:"bytes,4,opt,name=vrf_proof,proto3" json:"vrf_proof"`
}

func (m *WorkerRank) ProtoMessage()  {}
func (m *WorkerRank) Reset()         { *m = WorkerRank{} }
func (m *WorkerRank) String() string { return fmt.Sprintf("WorkerRank{%s}", m.Address) }
