# On-Chain Parameters

Complete reference of all governance-adjustable parameters on FunAI Chain, organized by module. Each parameter can be updated through governance proposals. Default values are set at genesis.

Sources: [FunAI V52 Final Design Spec](../docs/FunAI_V52_Final.md), [Per-Token Billing Supplement](../docs/S9_PerToken_Billing_Supplement.md)

---

## Settlement Module

Parameters governing the [settlement state machine](settlement.md), fee distribution, [verification](verification.md), and second verification behavior.

### Fee Distribution

| Parameter | Default | Unit | Description |
|-----------|---------|------|-------------|
| `executor_fee_ratio` | 850 | permille (85.0%) | Worker's share of the task fee on SUCCESS |
| `verifier_fee_ratio` | 120 | permille (12.0%) | Combined share for 3 verifiers on SUCCESS (~4% each) |
| `multi_verification_fund_ratio` | 30 | permille (3.0%) | Multi-verification fund share on SUCCESS — funds 2nd/3rd verifications |
| `fail_settlement_fee_ratio` | 150 | permille (15.0%) | Total fee charged to user on FAIL: verifiers get 12% + multi-verification fund gets 3%, matching the non-worker share of a SUCCESS settlement |

### Task Lifecycle

| Parameter | Default | Unit | Description |
|-----------|---------|------|-------------|
| `signature_expire_max` | 17,280 | blocks (24 hours) | Hard chain limit for `expire_block` on off-chain signatures |
| `task_cleanup_buffer` | 1,000 | blocks | Buffer after expiry before task state is pruned |

### Verification

| Parameter | Default | Unit | Description |
|-----------|---------|------|-------------|
| `second_verifier_count` | 3 | count | Number of [VRF-selected verifiers](vrf.md) per task (alpha = 0.5) |
| `second verification_match_threshold` | 2 | count | Minimum number of verifiers that must agree for a PASS |
| `logits_sample_positions` | 5 | count | Number of VRF-selected token positions checked during [teacher forcing](verification.md) |
| `logits_match_required` | 4 | count | Minimum positions that must match (within epsilon) for a logits PASS (4/5) |

### Second-Verification Rates

Second verification and third-verification rates are dynamic -- they adjust within their min/max bounds based on network conditions. See [Settlement](settlement.md) for the full state machine.

| Parameter | Default | Unit | Description |
|-----------|---------|------|-------------|
| `second_verification_base_rate` | 100 | permille (10%) | Default probability a VERIFIED task enters PENDING_AUDIT |
| `second_verification_rate_min` | 50 | permille (5%) | Minimum second verification rate floor |
| `second_verification_rate_max` | 300 | permille (30%) | Maximum second verification rate ceiling |
| `second_verification_timeout` | 8,640 | blocks (12 hours) | Timeout before an unresolved second verification defaults to CLEARED |
| `third_verification_base_rate` | 10 | permille (1%) | Default probability an second verificationed task enters PENDING_REAUDIT |
| `third_verification_rate_min` | 5 | permille (0.5%) | Minimum third-verification rate floor |
| `third_verification_rate_max` | 50 | permille (5%) | Maximum third-verification rate ceiling |
| `third_verification_timeout` | 17,280 | blocks (24 hours) | Timeout before an unresolved third-verification defaults to CLEARED |

### Per-Token Billing

Per-token billing is disabled by default in V1. These parameters take effect when `per_token_billing_enabled` is set to `true`.

| Parameter | Default | Unit | Description |
|-----------|---------|------|-------------|
| `per_token_billing_enabled` | false | bool | Enable per-token billing mode |
| `token_count_tolerance` | 2 | tokens | Absolute tolerance for token count mismatch |
| `token_count_tolerance_pct` | 2 | percent | Percentage tolerance for token count mismatch |

### Dishonesty Detection

| Parameter | Default | Unit | Description |
|-----------|---------|------|-------------|
| `dishonest_jail_threshold` | 3 | count | Number of dishonest detections before jail |
| `token_mismatch_second verification_weight` | 20 | weight | Weight applied to token mismatch signals when adjusting second verification rates |
| `token_mismatch_lookback` | 100 | tasks | Rolling window of recent tasks examined for mismatch patterns |
| `token_mismatch_deviation_pct` | 20 | percent | Deviation threshold that triggers elevated second verification rate |
| `token_mismatch_pair_min_samples` | 5 | count | Minimum sample count for a Worker-User pair before mismatch analysis applies |

---

## Worker Module

Parameters governing [Worker registration](msg-types.md) (`MsgRegisterWorker`), staking, exit, and the [jail mechanism](jail-and-slashing.md).

### Staking

| Parameter | Default | Unit | Description |
|-----------|---------|------|-------------|
| `min_stake` | 10,000,000,000 | ufai (10,000 FAI) | Minimum stake to register as a Worker |
| `exit_wait_period` | 362,880 | blocks (21 days) | Cooldown period after Worker initiates exit before stake is unlocked |

### Cold Start

| Parameter | Default | Unit | Description |
|-----------|---------|------|-------------|
| `cold_start_free_blocks` | 51,840 | blocks (3 days) | Grace period for newly registered Workers where no penalties apply |

### Jail Durations

Jail is progressive. Every 1000 consecutive successful tasks decays `jail_count` by 1 (floored at 0), per KT V6 Byzantine Test Plan 2026-04-27. See [Jail & Slashing](jail-and-slashing.md) for full details.

| Parameter | Default | Unit | Description |
|-----------|---------|------|-------------|
| `jail_1_duration` | 120 | blocks (10 min) | 1st jail duration -- submit `MsgUnjail` after cooldown |
| `jail_2_duration` | 720 | blocks (1 hour) | 2nd jail duration -- submit `MsgUnjail` after cooldown |
| `slash_fraud_percent` | 5 | percent | Stake percentage slashed on `MsgFraudProof` or 3rd jail (permanent tombstone) |
| `jail_decay_interval` | 1000 | tasks | Consecutive successful tasks per `jail_count` decay-by-1 (floored at 0). Replaces V5.2's `success_reset_threshold=50` per KT V6 Byzantine Test Plan 2026-04-27. |

---

## Reward Module

Parameters governing block reward distribution. See [Tokenomics](tokenomics.md) for the full economic model.

### Block Rewards

| Parameter | Default | Unit | Description |
|-----------|---------|------|-------------|
| `base_block_reward` | 4,000,000,000 | ufai (4,000 FAI) | Base reward per block before halving |
| `halving_period` | 26,250,000 | blocks (~4.16 years at 5s/block) | Interval between reward halvings |
| `total_supply` | 210,000,000,000,000,000 | ufai (210B FAI) | Maximum total supply cap |
| `epoch_blocks` | 100 | blocks (500 seconds) | Epoch length for reward aggregation and distribution |

### Reward Distribution Weights

When inference activity exists in the epoch, the block-reward pool is split 85/12/3 — matching the inference-fee split — so incentives across fees and rewards are aligned. Inside each of the inference and verifier pools, rewards are distributed by 85% fee amount + 15% task count.

| Parameter | Default | Unit | Description |
|-----------|---------|------|-------------|
| `inference_weight` | 0.85 | ratio | Share of block rewards to inference workers (85%) |
| `verification_weight` | 0.12 | ratio | Share to verifiers + 2nd + 3rd verifiers combined pool (12%) |
| `multi_verification_fund_weight` | 0.03 | ratio | Share minted into settlement module as multi-verification fund (3%) |
| `fee_weight` | 0.85 | ratio | Within each pool, weight given to fee amount earned |
| `count_weight` | 0.15 | ratio | Within each pool, weight given to task / verification count |

---

## Model Registry Module

Parameters governing model activation and service thresholds. See [Model Registry](model-registry.md) for the full lifecycle.

### Activation Thresholds

A model transitions from proposed to **active** when all activation conditions are met.

| Parameter | Default | Unit | Description |
|-----------|---------|------|-------------|
| `activation_stake_ratio` | 0.6667 | ratio (2/3) | Fraction of total network stake that must be held by Workers with the model installed |
| `min_eligible_workers` | 4 | count | Minimum number of Workers with the model installed |
| `min_unique_operators` | 4 | count | Minimum number of distinct operators running the model |

### Service Thresholds

A model transitions from active to **running** (accepting inference requests) when service conditions are met.

| Parameter | Default | Unit | Description |
|-----------|---------|------|-------------|
| `service_stake_ratio` | 0.1 | ratio (1/10) | Fraction of total network stake that must be held by Workers with the model installed |
| `min_service_worker_count` | 10 | count | Minimum number of Workers with the model installed for running status |

---

## VRF Module

Parameters governing the [unified VRF formula](vrf.md), leader election, and validator committee selection.

### Leader Election

| Parameter | Default | Unit | Description |
|-----------|---------|------|-------------|
| `leader_epoch_duration` | 6 | blocks (30s at 5s/block) | Duration of a leader epoch per model_id sub-topic |
| `heartbeat_interval` | 1 | blocks (5s) | Expected interval between leader heartbeats |
| `leader_timeout_blocks` | 1 | blocks (5s) | Blocks of leader inactivity before Workers switch to rank #2 (triggers at ~1.5s in practice via [P2P failover](p2p-layer.md)) |

### Validator Committee

| Parameter | Default | Unit | Description |
|-----------|---------|------|-------------|
| `committee_size` | 100 | count | Number of validators in the consensus committee |
| `committee_rotation` | 120 | blocks (10 min) | Committee rotation interval |
| `consensus_threshold` | 70 | percent | Minimum committee agreement required for consensus |
| `timeout_proof_percent` | 70 | percent | Minimum committee members that must attest to a timeout for it to be valid |
