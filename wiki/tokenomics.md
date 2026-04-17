# Token Economics

FunAI Chain's native token **$FAI** powers all economic activity: inference payments, worker staking, block rewards, and governance.

## Token Basics

| Parameter | Value |
|-----------|-------|
| Token symbol | $FAI |
| On-chain denom | `ufai` |
| Decimal conversion | 1 FAI = 1,000,000 ufai |
| Total supply | 210 billion FAI |
| Block time | 5 seconds |
| Epoch length | 100 blocks (500 seconds) |

## Block Rewards

| Parameter | Value |
|-----------|-------|
| Reward per block | 4,000 FAI |
| Halving interval | 26,250,000 blocks (~4.16 years) |

Rewards are calculated once per **epoch end** (every 100 blocks).

## Staking Requirements

| Parameter | Value |
|-----------|-------|
| Minimum worker stake | 10,000 FAI (governance adjustable) |
| Worker exit waiting period | 21 days |
| Cold start period | First 3 days: free registration, no stake required |

## Reward Distribution

### With Inference Activity

When the network is processing inference tasks, the block-reward pool is split with the same 85/12/3 ratio as the inference fee:

| Split | Recipients | Calculation |
|-------|-----------|-------------|
| **85%** | Workers (inference pool) | `w_i = 0.85 * (fee_i / sum_fee) + 0.15 * (count_i / sum_count)` |
| **12%** | Verifiers + 2nd verifiers + 3rd verifiers (combined verifier pool) | Same 85/15 formula, where `fee_i` is the fee each verifier earned this epoch from all verification roles |
| **3%** | Multi-verification fund | Minted into settlement module account; distributed per-epoch to 2nd/3rd verifiers via `DistributeMultiVerificationFund` alongside the fee-based multi-verification fund accumulation |

Only tasks in `CLEARED` status are counted toward reward distribution. See the [settlement state machine](settlement.md) for task lifecycle details.

### Without Inference Activity

When no inference has occurred during the epoch:

| Split | Recipients | Calculation |
|-------|-----------|-------------|
| **100%** | Consensus committee (100 validators) | Proportional to signed blocks |

### Why align block-reward split with inference-fee split?

Matching both splits at 85/12/3 keeps the economic incentive of every role identical whether their revenue comes from fees or from block rewards. Verifiers and second/third verifiers don't need a separate subsidy to make verification work profitable — the 12% verifier pool + 3% multi-verification fund cover that explicitly.

## Fee Distribution per Task

### SUCCESS (task passes verification)

| Recipient | Share | Notes |
|-----------|-------|-------|
| Worker | **85%** | Executing worker |
| 3 Verifiers | **12%** (~4% each) | [Verification protocol](verification.md) |
| Multi-verification fund | **3%** | Funds random 2nd/3rd verifications (formerly "audit fund") |

The user pays 100% of the agreed fee.

### FAIL (task fails verification, Worker caught cheating)

| Recipient | Share | Notes |
|-----------|-------|-------|
| Worker | 0% | Worker is [jailed](jail-and-slashing.md) |
| 3 Verifiers | **12%** (~4% each) | Compensated for verification work |
| Multi-verification fund | **3%** | Funds second/third verifications |

The user pays **15%** of the original fee. This matches the non-worker share of a SUCCESS fee, so verification + multi-verification costs are fully covered regardless of outcome.

## User Deposits

Users pre-deposit FAI into their inference balance via `MsgDeposit`. Withdrawals use `MsgWithdraw`. The [settlement module](../x/settlement/) handles balance tracking and batch payouts.

Three layers of [overspend protection](../p2p/leader/) prevent users from spending beyond their balance:

1. Leader local tracking of pending totals
2. Worker self-check with 3x safety factor
3. On-chain fallback during `MsgBatchSettlement`

## Sources

- [FunAI V52 Design Specification](../docs/FunAI_V52_Final.md)
- [Reward Module](../x/reward/)
- [Settlement Module](../x/settlement/)
- [Worker Module](../x/worker/)
