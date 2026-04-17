package types

const (
	EventWorkerRegistered = "worker_registered"
	EventWorkerExited     = "worker_exited"
	EventWorkerJailed     = "worker_jailed"
	EventWorkerUnjailed   = "worker_unjailed"
	EventWorkerTombstoned = "worker_tombstoned"
	EventWorkerSlashed    = "worker_slashed"
	EventModelsUpdated    = "models_updated"
	EventStakeAdded       = "stake_added"

	AttributeKeyWorker    = "worker"
	AttributeKeyModels    = "models"
	AttributeKeyStake     = "stake"
	AttributeKeyStatus    = "status"
	AttributeKeyAmount    = "amount"
	AttributeKeyJailCount = "jail_count"
	AttributeKeyJailUntil = "jail_until"
	AttributeKeySlashPct  = "slash_percent"
)
