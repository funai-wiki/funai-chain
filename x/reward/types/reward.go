package types

import (
	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"
)

func init() {
	proto.RegisterType((*RewardRecord)(nil), "funai.reward.RewardRecord")
	proto.RegisterType((*WorkerContribution)(nil), "funai.reward.WorkerContribution")
	proto.RegisterType((*VerificationContribution)(nil), "funai.reward.VerificationContribution")
	proto.RegisterType((*ConsensusSignerInfo)(nil), "funai.reward.ConsensusSignerInfo")
	proto.RegisterType((*OnlineWorkerStake)(nil), "funai.reward.OnlineWorkerStake")
	proto.RegisterType((*EpochRewardSummary)(nil), "funai.reward.EpochRewardSummary")
}

// RewardRecord records a reward distributed to a worker for a specific epoch.
type RewardRecord struct {
	Epoch         int64    `protobuf:"varint,1,opt,name=epoch,proto3" json:"epoch"`
	WorkerAddress string   `protobuf:"bytes,2,opt,name=worker_address,proto3" json:"worker_address"`
	Amount        sdk.Coin `protobuf:"bytes,3,opt,name=amount,proto3" json:"amount"`
}

func (m *RewardRecord) ProtoMessage()  {}
func (m *RewardRecord) Reset()         { *m = RewardRecord{} }
func (m *RewardRecord) String() string { return "reward.RewardRecord" }

type WorkerContribution struct {
	WorkerAddress string   `protobuf:"bytes,1,opt,name=worker_address,proto3" json:"worker_address"`
	FeeAmount     math.Int `protobuf:"bytes,2,opt,name=fee_amount,proto3" json:"fee_amount"`
	TaskCount     uint64   `protobuf:"varint,3,opt,name=task_count,proto3" json:"task_count"`
}

func (m *WorkerContribution) ProtoMessage()  {}
func (m *WorkerContribution) Reset()         { *m = WorkerContribution{} }
func (m *WorkerContribution) String() string { return "reward.WorkerContribution" }

// VerificationContribution tracks verification + audit (2nd/3rd verification) work
// for the 12% verifier reward pool. Distributed by 85% fee-weight + 15% count-weight,
// same as the inference pool.
type VerificationContribution struct {
	WorkerAddress     string   `protobuf:"bytes,1,opt,name=worker_address,proto3" json:"worker_address"`
	VerificationCount uint64   `protobuf:"varint,2,opt,name=verification_count,proto3" json:"verification_count"`
	AuditCount        uint64   `protobuf:"varint,3,opt,name=audit_count,proto3" json:"audit_count"`
	FeeAmount         math.Int `protobuf:"bytes,4,opt,name=fee_amount,proto3" json:"fee_amount"` // total fees earned by this verifier+second_verifier across all roles this epoch
}

func (m *VerificationContribution) ProtoMessage()  {}
func (m *VerificationContribution) Reset()         { *m = VerificationContribution{} }
func (m *VerificationContribution) String() string { return "reward.VerificationContribution" }

// ConsensusSignerInfo tracks block-signing activity for empty-epoch rewards (V5.2).
type ConsensusSignerInfo struct {
	ValidatorAddress string `protobuf:"bytes,1,opt,name=validator_address,proto3" json:"validator_address"`
	BlocksSigned     uint64 `protobuf:"varint,2,opt,name=blocks_signed,proto3" json:"blocks_signed"`
}

func (m *ConsensusSignerInfo) ProtoMessage()  {}
func (m *ConsensusSignerInfo) Reset()         { *m = ConsensusSignerInfo{} }
func (m *ConsensusSignerInfo) String() string { return "reward.ConsensusSignerInfo" }

// OnlineWorkerStake represents a worker's stake for proportional reward distribution
// when no inference contributions exist in an epoch.
type OnlineWorkerStake struct {
	WorkerAddress string   `protobuf:"bytes,1,opt,name=worker_address,proto3" json:"worker_address"`
	Stake         math.Int `protobuf:"bytes,2,opt,name=stake,proto3" json:"stake"`
}

func (m *OnlineWorkerStake) ProtoMessage()  {}
func (m *OnlineWorkerStake) Reset()         { *m = OnlineWorkerStake{} }
func (m *OnlineWorkerStake) String() string { return "reward.OnlineWorkerStake" }

// EpochRewardSummary summarizes reward distribution for an epoch.
type EpochRewardSummary struct {
	Epoch               int64                `protobuf:"varint,1,opt,name=epoch,proto3" json:"epoch"`
	EpochStartHeight    int64                `protobuf:"varint,2,opt,name=epoch_start_height,proto3" json:"epoch_start_height"`
	EpochEndHeight      int64                `protobuf:"varint,3,opt,name=epoch_end_height,proto3" json:"epoch_end_height"`
	TotalReward         math.Int             `protobuf:"bytes,4,opt,name=total_reward,proto3" json:"total_reward"`
	TotalFees           math.Int             `protobuf:"bytes,5,opt,name=total_fees,proto3" json:"total_fees"`
	TotalCount          uint64               `protobuf:"varint,6,opt,name=total_count,proto3" json:"total_count"`
	WorkerContributions []WorkerContribution `protobuf:"bytes,7,rep,name=worker_contributions,proto3" json:"worker_contributions"`
	DistributedByStake  bool                 `protobuf:"varint,8,opt,name=distributed_by_stake,proto3" json:"distributed_by_stake"`
}

func (m *EpochRewardSummary) ProtoMessage()  {}
func (m *EpochRewardSummary) Reset()         { *m = EpochRewardSummary{} }
func (m *EpochRewardSummary) String() string { return "reward.EpochRewardSummary" }
