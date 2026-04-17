package types

const (
	EventDeposit                  = "settlement_deposit"
	EventWithdraw                 = "settlement_withdraw"
	EventBatchSettlement          = "batch_settlement"
	EventFraudProof               = "fraud_proof"
	EventSecondVerificationResult = "second_verification_result"
	EventFailSettlement           = "fail_settlement"
	EventTaskCleanup              = "task_cleanup"

	AttributeKeyUser           = "user"
	AttributeKeyAmount         = "amount"
	AttributeKeyBalance        = "balance"
	AttributeKeyProposer       = "proposer"
	AttributeKeyBatchId        = "batch_id"
	AttributeKeyResultCount    = "result_count"
	AttributeKeyTotalFees      = "total_fees"
	AttributeKeyTaskId         = "task_id"
	AttributeKeyWorker         = "worker"
	AttributeKeyReporter       = "reporter"
	AttributeKeySecondVerifier = "second_verifier"
	AttributeKeyEpoch          = "epoch"
	AttributeKeyPass           = "pass"
	AttributeKeyCleanedTasks   = "cleaned_tasks"
)
