package types

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

type WorkerStatus uint32

const (
	WorkerStatusActive  WorkerStatus = 0
	WorkerStatusJailed  WorkerStatus = 1
	WorkerStatusExiting WorkerStatus = 2
	WorkerStatusExited  WorkerStatus = 3
)

func (s WorkerStatus) String() string {
	switch s {
	case WorkerStatusActive:
		return "ACTIVE"
	case WorkerStatusJailed:
		return "JAILED"
	case WorkerStatusExiting:
		return "EXITING"
	case WorkerStatusExited:
		return "EXITED"
	default:
		return "UNKNOWN"
	}
}

type Worker struct {
	Address         string       `protobuf:"bytes,1,opt,name=address,proto3" json:"address"`
	Pubkey          string       `protobuf:"bytes,2,opt,name=pubkey,proto3" json:"pubkey"`
	Stake           sdk.Coin     `protobuf:"bytes,3,opt,name=stake,proto3" json:"stake"`
	SupportedModels []string     `protobuf:"bytes,4,rep,name=supported_models,proto3" json:"supported_models"`
	Status          WorkerStatus `protobuf:"varint,5,opt,name=status,proto3" json:"status"`
	JoinedAt        int64        `protobuf:"varint,6,opt,name=joined_at,proto3" json:"joined_at"`
	ExitRequestedAt int64        `protobuf:"varint,7,opt,name=exit_requested_at,proto3" json:"exit_requested_at"`
	Endpoint        string       `protobuf:"bytes,8,opt,name=endpoint,proto3" json:"endpoint"`
	GpuModel        string       `protobuf:"bytes,9,opt,name=gpu_model,proto3" json:"gpu_model"`
	GpuVramGb       uint32       `protobuf:"varint,10,opt,name=gpu_vram_gb,proto3" json:"gpu_vram_gb"`
	GpuCount        uint32       `protobuf:"varint,11,opt,name=gpu_count,proto3" json:"gpu_count"`
	OperatorId      string       `protobuf:"bytes,12,opt,name=operator_id,proto3" json:"operator_id"`
	// V5.1 jail/penalty fields
	JailCount     uint32 `protobuf:"varint,13,opt,name=jail_count,proto3" json:"jail_count"`
	Jailed        bool   `protobuf:"varint,14,opt,name=jailed,proto3" json:"jailed"`
	JailUntil     int64  `protobuf:"varint,15,opt,name=jail_until,proto3" json:"jail_until"`
	Tombstoned    bool   `protobuf:"varint,16,opt,name=tombstoned,proto3" json:"tombstoned"`
	SuccessStreak uint32 `protobuf:"varint,17,opt,name=success_streak,proto3" json:"success_streak"`
	// Stats
	TotalTasks      uint64   `protobuf:"varint,18,opt,name=total_tasks,proto3" json:"total_tasks"`
	TotalFeeEarned  sdk.Coin `protobuf:"bytes,19,opt,name=total_fee_earned,proto3" json:"total_fee_earned"`
	LastActiveBlock int64    `protobuf:"varint,20,opt,name=last_active_block,proto3" json:"last_active_block"`
	// S1: concurrent inference capacity
	MaxConcurrentTasks uint32 `protobuf:"varint,21,opt,name=max_concurrent_tasks,proto3" json:"max_concurrent_tasks"`
	// S2: concurrent verification capacity
	MaxConcurrentVerify uint32 `protobuf:"varint,22,opt,name=max_concurrent_verify,proto3" json:"max_concurrent_verify"`

	// Audit KT §3: Reputation mechanism — continuous soft weight for VRF ranking.
	// Range 0-12000 (0.0-1.2), initial 10000 (1.0). VRF weight = stake × (reputation/10000).
	ReputationScore uint32 `protobuf:"varint,23,opt,name=reputation_score,proto3" json:"reputation_score"`
	// Audit KT §3: consecutive reject counter — 10 rejects without accept → -0.05 penalty
	ConsecutiveRejects uint32 `protobuf:"varint,24,opt,name=consecutive_rejects,proto3" json:"consecutive_rejects"`

	// Audit KT §5: average first-token latency (ms), updated each settlement.
	// VRF LatencyFactor = f(AvgLatencyMs, request.MaxLatencyMs)
	AvgLatencyMs uint32 `protobuf:"varint,25,opt,name=avg_latency_ms,proto3" json:"avg_latency_ms"`
}

func (m *Worker) ProtoMessage()  {}
func (m *Worker) Reset()         { *m = Worker{} }
func (m *Worker) String() string { return fmt.Sprintf("Worker{%s}", m.Address) }

const (
	ReputationInitial     uint32 = 10000 // 1.0
	ReputationMax         uint32 = 12000 // 1.2
	ReputationAcceptDelta uint32 = 100   // +0.01 per accept
	ReputationMissDelta   uint32 = 1000  // -0.1 per miss (10x accept)
	ReputationAuditMiss   uint32 = 2000  // -0.2 per second_verifier miss (doubled)
	ReputationRejectChain uint32 = 500   // -0.05 after 10 consecutive rejects
	ReputationDecayStep   uint32 = 50    // ±0.005 hourly decay toward 1.0
	ReputationRejectLimit uint32 = 10    // consecutive rejects before penalty
)

// EffectiveReputation returns the reputation as a float64 (0.0-1.2).
func (w Worker) EffectiveReputation() float64 {
	r := w.ReputationScore
	if r == 0 {
		r = ReputationInitial // treat uninitialized as 1.0
	}
	return float64(r) / 10000.0
}

func (w Worker) IsActive() bool {
	return w.Status == WorkerStatusActive && !w.Jailed && !w.Tombstoned
}

func (w Worker) IsJailed() bool {
	return w.Jailed
}

func (w Worker) CanUnjail(currentHeight int64) bool {
	return w.Jailed && !w.Tombstoned && currentHeight >= w.JailUntil
}
