package types

// VRF uses the unified formula: score = hash(seed || pubkey) / stake^α
// Lower score = higher rank. Alpha values (see types.AlphaDispatch etc.):
//   - α=1.0: dispatch — pure stake weight
//   - α=0.5: verification — √stake weight (0.5 stake + 0.5 random)
//   - α=0.0: audit — equal probability, ignores stake

type Params struct {
	LeaderEpochDuration int64  `protobuf:"varint,1,opt,name=leader_epoch_duration,proto3" json:"leader_epoch_duration"`
	HeartbeatInterval   int64  `protobuf:"varint,2,opt,name=heartbeat_interval,proto3" json:"heartbeat_interval"`
	LeaderTimeoutBlocks int64  `protobuf:"varint,3,opt,name=leader_timeout_blocks,proto3" json:"leader_timeout_blocks"`
	CommitteeSize       uint64 `protobuf:"varint,4,opt,name=committee_size,proto3" json:"committee_size"`
	CommitteeRotation   int64  `protobuf:"varint,5,opt,name=committee_rotation,proto3" json:"committee_rotation"`
	ConsensusThreshold  uint64 `protobuf:"varint,6,opt,name=consensus_threshold,proto3" json:"consensus_threshold"`
	TimeoutProofPercent uint64 `protobuf:"varint,7,opt,name=timeout_proof_percent,proto3" json:"timeout_proof_percent"`
}

func (m *Params) ProtoMessage()  {}
func (m *Params) Reset()         { *m = Params{} }
func (m *Params) String() string { return "vrf.Params" }

func DefaultParams() Params {
	return Params{
		LeaderEpochDuration: 6,   // 6 blocks = 30s (V5.2: leader_epoch = 30 seconds)
		HeartbeatInterval:   1,   // 1 block
		LeaderTimeoutBlocks: 1,   // 1 block (closest to 3s at 5s/block)
		CommitteeSize:       100, // 100 workers
		CommitteeRotation:   120, // 120 blocks ≈ 10 minutes
		ConsensusThreshold:  70,  // 70% of committee required
		TimeoutProofPercent: 70,  // 70% timeout proofs for re-election
	}
}

func (p Params) Validate() error {
	if p.LeaderEpochDuration <= 0 {
		return ErrInvalidParams.Wrap("leader epoch duration must be positive")
	}
	if p.HeartbeatInterval <= 0 {
		return ErrInvalidParams.Wrap("heartbeat interval must be positive")
	}
	if p.LeaderTimeoutBlocks <= 0 {
		return ErrInvalidParams.Wrap("leader timeout blocks must be positive")
	}
	if p.CommitteeSize == 0 {
		return ErrInvalidParams.Wrap("committee size must be positive")
	}
	if p.CommitteeRotation <= 0 {
		return ErrInvalidParams.Wrap("committee rotation must be positive")
	}
	if p.ConsensusThreshold == 0 || p.ConsensusThreshold > 100 {
		return ErrInvalidParams.Wrap("consensus threshold must be between 1 and 100")
	}
	if p.TimeoutProofPercent == 0 || p.TimeoutProofPercent > 100 {
		return ErrInvalidParams.Wrap("timeout proof percent must be between 1 and 100")
	}
	return nil
}
