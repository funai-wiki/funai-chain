package types

const (
	EventDeposit                       = "settlement_deposit"
	EventWithdraw                      = "settlement_withdraw"
	EventBatchSettlement               = "batch_settlement"
	EventBatchReserve                  = "batch_reserve"
	EventFraudProof                    = "fraud_proof"
	EventSecondVerificationResult      = "second_verification_result"
	EventSecondVerificationResultBatch = "second_verification_result_batch"
	EventFailSettlement                = "fail_settlement"
	EventTaskCleanup                   = "task_cleanup"
	// EventShortfall is emitted when settlement could not pay the full
	// actualFee because the user's account had insufficient balance at
	// settle time (audit Rule 4 — never silently drop a settled task).
	// The Worker absorbs the difference; the task is still marked settled.
	EventShortfall = "settlement_shortfall"

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
	AttributeKeyAcceptedCount  = "accepted_count"
	AttributeKeyRejectedCount  = "rejected_count"
	// EventShortfall attributes (audit Rule 4).
	AttributeKeyExpectedFee      = "expected_fee"
	AttributeKeyPaidFee          = "paid_fee"
	AttributeKeyShortfallAmount  = "shortfall_amount"
)
