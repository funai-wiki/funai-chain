# Settlement State Machine

FunAI Chain uses a multi-stage settlement pipeline that moves tasks from verification through optional second verification to final payout. Only CLEARED tasks are ever included in a `MsgBatchSettlement` -- tasks in PENDING_AUDIT or PENDING_REAUDIT are never settled until they reach a terminal state.

Source: [FunAI V52 Final Design Spec](../docs/FunAI_V52_Final.md)

---

## State Machine

```
VERIFIED
  │
  ▼
VRF check
  ├── 90% ──► CLEARED ──► BatchSettlement ──► instant payout (~15s)
  │
  └── 10% ──► PENDING_AUDIT
                  │
                  ▼
              Second verification result
                  ├── 99% ──► CLEARED or FAILED (second verification result applies)
                  │
                  └──  1% ──► PENDING_REAUDIT
                                  │
                                  ▼
                              Re-second verification result ──► CLEARED or FAILED
```

- **90% of tasks** are CLEARED immediately after verification and settle in the next batch (~15 seconds).
- **10% of tasks** enter PENDING_AUDIT for a full second verification cycle.
- **1% of second verificationed tasks** (0.1% of all tasks) undergo a second third-verification.

---

## Fee Distribution

### SUCCESS (user pays 100% of the agreed fee)

| Recipient | Share |
|-----------|-------|
| Executor (Worker) | 85.0% (850/1000) |
| Verifier 1 | ~4.0% |
| Verifier 2 | ~4.0% |
| Verifier 3 | ~4.0% |
| Second verification fund | 3.0% (30/1000) |
| **Total** | **100.0%** |

Verifiers share 12.0% (120/1000) equally (~4% each for 3 verifiers).

### FAIL (user pays only 5% of the agreed fee)

| Recipient | Share of 5% fee | Effect |
|-----------|-----------------|--------|
| Worker | 0% | Jailed |
| Verifiers | 12.0% | -- |
| Second verification fund | 3.0% | -- |

### TIMEOUT (user pays only 5% of the agreed fee)

| Recipient | Share of 5% fee | Effect |
|-----------|-----------------|--------|
| Worker | 0% | Jailed |
| Verifiers | 0% | -- |
| Second verification fund | 5.0% | Receives entire 5% |

---

## Second verification Overturns

### Second verification overturns SUCCESS to FAIL

- No settlement occurs for the task.
- Worker is jailed.
- Verifiers who originally returned PASS are jailed.

### Second verification overturns FAIL to SUCCESS

- Task is settled as a normal SUCCESS (Executor 85%, Verifiers 12%, Second verification fund 3%).
- Verifiers who originally returned FAIL are jailed.

---

## FraudProof

A user SDK can submit `MsgFraudProof` if it detects a content mismatch between what was received and what was signed.

- **Before settlement:** the task entry is skipped in the batch. No payout occurs.
- **After settlement:** the Worker's 85% share is recovered and the user is refunded.
- **Worker penalty:** immediate slash of 5% of stake + permanent tombstone. This is the only slash scenario besides a 3rd jail offense.

---

## Jail Mechanism

Jailing follows a Cosmos-style progressive penalty system shared across all roles (Worker, Verifier, Proposer):

| Offense | Duration | Effect |
|---------|----------|--------|
| 1st jail | 10 minutes (120 blocks) | Wait, then `MsgUnjail` to resume |
| 2nd jail | 1 hour (720 blocks) | Wait, then `MsgUnjail` to resume |
| 3rd jail | Permanent | Slash 5% of stake + tombstone |

- **Rehabilitation:** 50 consecutive successful tasks resets `jail_count` to 0.
- **FraudProof:** bypasses the progressive system -- immediate slash 5% + tombstone regardless of jail count.

---

## Second-Verification Rates

Second verification and third-verification rates are **dynamic** -- they are never hardcoded to a fixed value.

### Second-verification rate

- **Base rate:** 10%
- **Range:** 5% -- 30%
- **Formula:** `rate = base * (1 + 10 * recent_fail_rate + 50 * recent_second verification_fail_rate)`
- A Worker with a high recent failure rate or second verification failure rate will be second verificationed much more frequently.

### Re-second verification rate

- **Base rate:** 1%
- **Range:** 0.5% -- 5%

---

## Second-Verification Timeouts

| Stage | Timeout | On timeout |
|-------|---------|------------|
| Initial second verification | 12 hours | Original verification result takes effect |
| Re-second verification | 24 hours | Original second verification result takes effect |

If an second verification or third-verification times out, the system falls back to the previous stage's result rather than leaving the task in limbo.

---

## Batch Parameters

| Parameter | Value |
|-----------|-------|
| Batch size | 1,000 -- 10,000 tasks per batch |
| Task ID cleanup | 1,000 blocks after settlement |
| `expire_block` max | 17,280 blocks (24 hours) -- hard chain limit |

---

## Overspend Protection

Three layers prevent users from spending more than their deposited balance:

1. **Leader local tracking:** `available = on_chain_balance - local_pending_total`
2. **Worker self-check:** if balance < fee * 3x safety factor, the Worker rejects the task.
3. **On-chain fallback:** if `BatchSettlement` finds insufficient balance, the entry is marked REFUNDED and skipped.

---

## Related Pages

- [Three-Layer Architecture](architecture.md) -- where settlement fits in the L1 chain layer
- [VRF Unified Formula](vrf.md) -- how VRF determines second verification selection and dispatch ranking
- [Schema Reference](schema.md) -- protobuf message definitions for `MsgBatchSettlement`, `MsgFraudProof`, `MsgSecondVerificationResult`
