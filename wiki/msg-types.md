# On-Chain Message Types

Reference page for all transaction message types on FunAI Chain. Each Msg corresponds to an on-chain transaction that modifies state. The chain acts strictly as a settlement and registry layer -- all inference happens off-chain via the [P2P layer](p2p-layer.md).

Source: [FunAI V52 Final Design Spec](../docs/FunAI_V52_Final.md)

---

## Message Summary

| Msg | Submitter | Key Fields | Purpose | Notes |
|-----|-----------|------------|---------|-------|
| `MsgDeposit` | User | `user` (address), `amount` (uint128) | Top up inference balance | Balance is used for off-chain inference requests; see [Overspend Protection](overspend-protection.md) |
| `MsgWithdraw` | User / Worker | `address` (address), `amount` (uint128) | Withdraw funds from on-chain balance | Users withdraw unused inference balance; Workers withdraw earned fees |
| `MsgRegisterWorker` | Worker | `pubkey` (bytes32), `stake` (uint128), `endpoint` (string), `gpu_model` (string), `gpu_vram_gb` (uint16), `gpu_count` (uint8), `supported_models` (Vec\<model_id\>), `operator_id` (bytes32) | Register as a Worker node | min_stake = 10,000 FAI (10,000,000,000 ufai). Stake acts as [VRF](vrf.md) weight. Stake is only slashed on `MsgFraudProof` or 3rd jail -- both slash 5%. See [Jail & Slashing](jail-and-slashing.md) |
| `MsgModelProposal` | Anyone | `model_id` (bytes32), `epsilon` (float32), `weight_hash` (bytes32), `quant_config_hash` (bytes32), `runtime_image_hash` (bytes32) | Propose a new model_id with epsilon tolerance | `model_id = SHA256(weight_hash \|\| quant_config_hash \|\| runtime_image_hash)`. Proposer must test 100 prompts x 2+ GPU types x 3 runs to calibrate epsilon. See [Model Registry](model-registry.md) |
| `MsgDeclareInstalled` | Worker | `worker_pubkey` (bytes32), `model_id` (bytes32) | Declare a model installed on the Worker | Counts toward [activation thresholds](model-registry.md): installed_stake >= 2/3 AND workers >= 4 AND operators >= 4 |
| `MsgBatchSettlement` | Proposer | `batch_id` (uint64), `proposer` (bytes32), `merkle_root` (bytes32), `entries` ([SettlementEntry; N]), `proposer_sig` (bytes64) | Batch-settle CLEARED tasks on-chain | Only CLEARED tasks are included -- never PENDING_AUDIT or PENDING_REAUDIT. See [Settlement](settlement.md) for the full state machine |
| `MsgSecondVerificationResult` | Proposer | `task_id` (bytes32), `second verification_type` (uint8), `result` (uint8), `second verifier_sigs` ([]bytes64) | Submit second verification or third-verification results | Proposer packages results received over P2P. Outcome moves the task to CLEARED or FAILED |
| `MsgFraudProof` | User SDK | `task_id` (bytes32), `user_pubkey` (bytes32), `evidence` (bytes) | Report content mismatch -- slash Worker 5% + tombstone | User receives full refund. See [Jail & Slashing](jail-and-slashing.md) for timing scenarios |
| `MsgUnjail` | Any jailed node | `address` (address) | Remove jail status after cooldown expires | 1st jail: 120 blocks (10 min), 2nd jail: 720 blocks (1 hour), 3rd: permanent tombstone. See [Jail & Slashing](jail-and-slashing.md) |
| `MsgDelegate` | -- | -- | -- | **RESERVED** -- not implemented in V1 |
| `MsgUndelegate` | -- | -- | -- | **RESERVED** -- not implemented in V1 |

---

## MsgBatchSettlement Detail

`MsgBatchSettlement` is the highest-volume transaction on the chain. Each batch contains 1,000--10,000 settlement entries.

### SettlementEntry Structure (~200 bytes each)

| Field | Type | Description |
|-------|------|-------------|
| `task_id` | bytes32 | Unique task identifier: `hash(user_pubkey + model_id + prompt_hash + timestamp)` |
| `user_pubkey` | bytes32 | Public key of the requesting user |
| `worker_pubkey` | bytes32 | Public key of the executing Worker |
| `verifiers` | [bytes32; 3] | Public keys of the 3 [VRF-selected verifiers](verification.md) |
| `fee` | uint128 | Agreed inference fee |
| `status` | uint8 | Settlement status (CLEARED) |
| `user_sig_hash` | bytes32 | Hash of the user's off-chain request signature |
| `worker_sig_hash` | bytes32 | Hash of the Worker's result signature |
| `verifier_sig_hashes` | [bytes32; 3] | Hashes of the verifier signatures |

### Batch Size

- Typical batch: 1,000 tasks (~200 KB)
- Maximum batch: 10,000 tasks (~2 MB)
- Settlement frequency: every block (~5 seconds)

### Validation Rules

1. `merkle_root` must match the Merkle tree computed from the submitted entries.
2. `proposer_sig` must be a valid signature from the declared Proposer.
3. Per-entry skip conditions (entry is skipped, not the whole batch):
   - Task already settled (duplicate `task_id`)
   - Task marked as FRAUD
   - Task expired (`expire_block` exceeded; max = 17,280 blocks / 24 hours)
   - Insufficient user balance (see [Overspend Protection](overspend-protection.md) Layer 3)
   - Invalid user or Worker signature

Skipped entries are recorded as REFUNDED; the batch continues processing remaining entries.

---

## MsgFraudProof Detail

`MsgFraudProof` is the strongest penalty mechanism. It is submitted by the User SDK when the client detects a content mismatch between what was requested and what was delivered.

### Effects

- Worker is slashed 5% of staked amount
- Worker is permanently tombstoned (no unjail possible)
- User receives a full refund of the task fee

### Timing Scenarios

1. **Before settlement** -- the FraudProof arrives before `MsgBatchSettlement` includes the task. The task is marked FRAUD and excluded from future batches. The user is never charged.
2. **After settlement** -- the FraudProof arrives after the task was already settled and paid out. The slash is applied retroactively, and the user is refunded from the slashed funds.

---

## Fee Distribution on Settlement

When a task settles as SUCCESS (user pays 100% of the agreed fee):

| Recipient | Share | Parameter |
|-----------|-------|-----------|
| Worker (executor) | 95.0% | `executor_fee_ratio = 950` |
| 3 Verifiers (split) | 4.5% (1.5% each) | `verifier_fee_ratio = 45` |
| Second verification fund | 0.5% | `multi_verification_fund_ratio = 5` |

When a task settles as FAIL (user pays only 5% of the agreed fee):

| Recipient | Share | Parameter |
|-----------|-------|-----------|
| 3 Verifiers (split) | 4.5% (1.5% each) | `verifier_fee_ratio = 45` |
| Second verification fund | 0.5% | `multi_verification_fund_ratio = 5` |
| Worker | 0% | Worker is jailed |

See [Parameters](parameters.md) for all configurable fee ratios and [Settlement](settlement.md) for the full state machine.

---

## Reserved Messages

`MsgDelegate` and `MsgUndelegate` are reserved interfaces for future delegation and inference pool functionality. They are defined in the protobuf schema but not implemented in V1. Do not use them -- transactions will be rejected.
