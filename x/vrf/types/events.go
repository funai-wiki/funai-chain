package types

const (
	EventTypeLeaderElected     = "leader_elected"
	EventTypeLeaderTimeout     = "leader_timeout"
	EventTypeLeaderHeartbeat   = "leader_heartbeat"
	EventTypeCommitteeRotated  = "committee_rotated"
	EventTypeSeedUpdated       = "seed_updated"
	EventTypeVRFProofSubmitted = "vrf_proof_submitted"
	EventTypeTaskDispatched    = "task_dispatched"
	EventTypeReElection        = "re_election"

	AttributeKeyModelId        = "model_id"
	AttributeKeyLeaderAddress  = "leader_address"
	AttributeKeyBlockHeight    = "block_height"
	AttributeKeyCommitteeSize  = "committee_size"
	AttributeKeyEpoch          = "epoch"
	AttributeKeySeedValue      = "seed_value"
	AttributeKeyTaskId         = "task_id"
	AttributeKeyWorkerAddress  = "worker_address"
	AttributeKeyProofSubmitter = "proof_submitter"
)
