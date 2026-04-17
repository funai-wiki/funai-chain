package types

const (
	EventTypeRewardDistributed = "reward_distributed"
	EventTypeEpochReward       = "epoch_reward"
	EventTypeStakeDistribution = "stake_distribution"

	AttributeKeyEpoch            = "epoch"
	AttributeKeyBlockHeight      = "block_height"
	AttributeKeyWorkerAddress    = "worker_address"
	AttributeKeyRewardAmount     = "reward_amount"
	AttributeKeyTotalDistributed = "total_distributed"
	AttributeKeyDistributionMode = "distribution_mode"
)
