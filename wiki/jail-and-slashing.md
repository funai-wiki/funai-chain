# Jail & Slashing Mechanism

FunAI Chain uses a Cosmos-style progressive jail mechanism shared across all roles. Jail is a **lockdown** -- stake is frozen but not deducted at levels 1 and 2. Slashing only occurs at level 3 or via [FraudProof](#fraudproof).

## Jail Effects

While jailed, a participant **cannot**:

- Accept or execute inference tasks
- Participate in [verification](verification.md)
- Participate in consensus
- Unstake or withdraw funds

## Progressive Jail Schedule

| Offense | Duration | Blocks | Recovery |
|---------|----------|--------|----------|
| 1st jail | 10 minutes | 120 blocks | `MsgUnjail` after cooldown |
| 2nd jail | 1 hour | 720 blocks | `MsgUnjail` after cooldown |
| 3rd jail | **Permanent** | N/A | Slash 5% of stake + tombstone |

### Jail Counter Reset

After **50 consecutive successful tasks**, the `jail_count` resets to 0. This gives rehabilitated participants a clean slate.

> ⚠ **Contradiction with V6 KT plan (open).** The V6 Byzantine Test Plan
> ([`docs/testing/FunAI_V6_Byzantine_Test_Plan_KT.md`](../docs/testing/FunAI_V6_Byzantine_Test_Plan_KT.md), 2026-04-27)
> tests M7/M8/C1/C2 against a different rule: **every 1000 successful tasks
> decays `jail_count` by 1** (incremental, not full reset). V52 says reset
> to 0 after 50; the V6 plan says decay by 1 after 1000. These are not
> equivalent — a Worker with `jail_count=2` reaches "clean" after 50 tasks
> on V52 but needs 2000 tasks on KT V6. **Spec source of truth must be
> chosen before the Byzantine fuzzer can be implemented.**

**What counts as "1 task" per role:**

| Role | 1 successful task = |
|------|---------------------|
| Worker | 1 completed inference |
| Verifier | 1 completed verification |
| SecondVerifier | 1 completed second verification |
| Leader | 1 dispatch epoch |
| Proposer | 1 block produced |

All roles share the **same jail counter** and progressive mechanism. A Worker who is also a Verifier has one shared jail state.

## Jail Trigger Sources

There are five scenarios that trigger a jail event:

| # | Trigger | Who gets jailed |
|---|---------|-----------------|
| 1 | Second verification overturns `SUCCESS` to `FAIL` | Worker who produced incorrect output |
| 2 | Second verification overturns `FAIL` to `SUCCESS` | Original verifiers who incorrectly reported FAIL |
| 3 | Original verification reports `FAIL` (confirmed by second verification) | Worker who produced incorrect output |
| 4 | Re-second verification overturns second verification `PASS` to `FAIL` | Original second verifier who incorrectly passed |
| 5 | Re-second verification overturns second verification `FAIL` to `PASS` | Original second verifier who incorrectly failed |

All jail triggers flow through the [settlement state machine](../x/settlement/). Tasks selected for second verification (10%) or third-verification (1%) are determined by VRF. See [tokenomics](tokenomics.md) for fee consequences of SUCCESS vs FAIL outcomes.

## FraudProof

FraudProof is a **separate mechanism** from the progressive jail system. It is triggered when the user SDK detects content mismatch between what was received and what was settled on-chain.

| Aspect | FraudProof |
|--------|-----------|
| Trigger | `MsgFraudProof` submitted by user SDK |
| Effect | **Immediate** slash 5% of stake + tombstone |
| Jail progression | Bypassed entirely |
| Recovery | None (permanent tombstone) |

FraudProof and the 3rd progressive jail are the **only two scenarios** in FunAI Chain where stake is actually slashed.

## On-Chain State

The [worker module](../x/worker/) stores the following jail-related fields per participant:

| Field | Type | Description |
|-------|------|-------------|
| `jail_count` | `uint8` | Current offense count (0-3) |
| `jailed` | `bool` | Whether currently jailed |
| `jail_until` | `uint64` | Block height when unjail becomes available |
| `tombstoned` | `bool` | Permanent ban flag |
| `success_streak` | `uint32` | Consecutive successes toward the 50-task reset |

## Sources

- [FunAI V52 Design Specification](../docs/FunAI_V52_Final.md)
- [Worker Module](../x/worker/)
- [Settlement Module](../x/settlement/)
- [Verification Protocol](verification.md)
- [Token Economics](tokenomics.md)
