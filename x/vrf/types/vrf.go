package types

import (
	"crypto/sha256"
	"fmt"
	"math/big"
	"sort"

	"cosmossdk.io/math"
)

// VRFAlpha is the exponent applied to the **stake** term in the unified VRF formula.
// The reputation × latency_factor term is ALWAYS applied with exponent 1.0
// (for α > 0 it is folded into effective_stake; for α = 0 it is the only factor).
// Lower score = higher rank.
type VRFAlpha float64

const (
	// AlphaDispatch (α=1.0): Worker dispatch.
	// score = hash / (stake × reputation × latency_factor)
	// Rank is proportional to stake × rep × speed.
	AlphaDispatch VRFAlpha = 1.0
	// AlphaVerification (α=0.5): 1st-tier verifier selection.
	// score = hash / sqrt(stake × reputation × latency_factor)
	// Rank is proportional to sqrt(stake × rep × speed).
	AlphaVerification VRFAlpha = 0.5
	// AlphaSecondThirdVerification (α=0.0): 2nd/3rd-tier verifier selection.
	// score = hash / (reputation × latency_factor)
	// **Stake is NOT considered**. Rank is proportional to rep × speed only.
	AlphaSecondThirdVerification VRFAlpha = 0.0
)

// rankLatencyReferenceMs is the reference latency used to normalize AvgLatencyMs
// into a rank multiplier. A worker with AvgLatencyMs == refMs gets factor 1.0;
// faster → up to 1.5x; slower → down to 0.1x.
const rankLatencyReferenceMs uint32 = 3000

// rankSpeedMultiplier returns the latency multiplier used in VRF ranking,
// clamped to [0.1, 1.5]. Missing data (AvgLatencyMs == 0) → 1.0 (neutral).
// Distinct from LatencyFactor below, which is used for dispatch-time eligibility
// filtering against a task-specific MaxLatencyMs.
func rankSpeedMultiplier(avgMs uint32) float64 {
	if avgMs == 0 {
		return 1.0
	}
	ratio := float64(rankLatencyReferenceMs) / float64(avgMs)
	if ratio > 1.5 {
		return 1.5
	}
	if ratio < 0.1 {
		return 0.1
	}
	return ratio
}

// ComputeScore calculates VRF score = hash(seed || pubkey) / stake^α
// Lower score = higher rank. When α=0.0, stake is ignored (pure hash).
//
// NOTE: this legacy function does NOT include reputation or latency weighting.
// For correct per-role behavior use RankWorkers, which wires in rep × speed
// according to the role's alpha.
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

// computeScoreByFloatWeight returns hash(seed||pubkey) / weight.
// Used when the effective weight is a float (e.g. rep × latency_factor for
// 2nd/3rd-tier verifier selection, where stake is excluded entirely).
func computeScoreByFloatWeight(seed []byte, pubkey []byte, weight float64) *big.Float {
	data := append(append([]byte{}, seed...), pubkey...)
	h := sha256.Sum256(data)
	hashInt := new(big.Int).SetBytes(h[:])
	hashFloat := new(big.Float).SetPrec(256).SetInt(hashInt)
	if weight <= 0 {
		return hashFloat
	}
	weightFloat := new(big.Float).SetPrec(256).SetFloat64(weight)
	return new(big.Float).SetPrec(256).Quo(hashFloat, weightFloat)
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
//
// Per-role weighting (v5.3):
//
//	AlphaDispatch (α=1.0, Worker):
//	    score = hash / (stake × reputation × latency_factor)
//	AlphaVerification (α=0.5, 1st-tier verifier):
//	    score = hash / sqrt(stake × reputation × latency_factor)
//	AlphaSecondThirdVerification (α=0.0, 2nd/3rd-tier verifier):
//	    score = hash / (reputation × latency_factor)        [stake ignored]
//
// Reputation (0.0–1.2, uninitialized treated as 1.0) and latency multiplier
// (3000 ms reference, clamped to [0.1, 1.5]; missing latency data treated as 1.0)
// are ALWAYS applied. The α exponent controls only the stake contribution.
func RankWorkers(seed []byte, workers []RankedWorker, alpha VRFAlpha) []RankedWorker {
	for i := range workers {
		rep := workers[i].Reputation
		if rep <= 0 {
			rep = 1.0 // uninitialized → neutral
		}
		latFactor := rankSpeedMultiplier(workers[i].AvgLatencyMs)
		repSpeed := rep * latFactor

		if alpha == AlphaSecondThirdVerification {
			// 2nd/3rd verifier: stake excluded entirely.
			workers[i].Score = computeScoreByFloatWeight(seed, workers[i].Pubkey, repSpeed)
		} else {
			// Dispatch / 1st-tier verifier: fold rep × latency into effective stake,
			// then apply α exponent uniformly (matches stake × rep × speed for α=1,
			// sqrt(stake × rep × speed) for α=0.5).
			effectiveStake := workers[i].Stake
			if repSpeed != 1.0 {
				scaledBig := new(big.Float).SetPrec(256).SetInt(effectiveStake.BigInt())
				scaledBig.Mul(scaledBig, new(big.Float).SetPrec(256).SetFloat64(repSpeed))
				scaledInt, _ := scaledBig.Int(nil)
				if scaledInt.Sign() <= 0 {
					scaledInt = big.NewInt(1)
				}
				effectiveStake = math.NewIntFromBigInt(scaledInt)
			}
			workers[i].Score = ComputeScore(seed, workers[i].Pubkey, effectiveStake, alpha)
		}
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
