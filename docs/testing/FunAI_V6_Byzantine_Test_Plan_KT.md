# FunAI V6 — Penalty-Path Byzantine Stress Test Plan

| | |
|---|---|
| **Priority** | P0 — required before mainnet |
| **Date** | 2026-04-27 |
| **Author** | KT |
| **Depends on** | V6 batch-replay scheme single-machine validation passed (Phase 1, 26/26 PASS — `docs/testing/reports/2026-04-21-v6-phase1a/SUMMARY.md`) |
| **Execution** | On-chain state-machine test. No real GPU needed. Mock logits + mock TGI; reuses the harness from PR #23's `e2e-mock`. |

---

## 1. Test goal

For every "misbehaviour" path on the Worker / Verifier / Leader side, run a large randomised combination set and confirm:

- The on-chain state machine handles each scenario correctly.
- Fee accounting is conserved at all times.
- State transitions never enter an illegal state.

---

## 2. Scenario tiers

### 2.1 Light tier — reputation drop only, no jail

| ID | Scenario | Expected result |
|---|---|---|
| **L1** | Worker misses 1 task occasionally | reputation −0.10, no jail, task is redispatched |
| **L2** | Worker misses 2 consecutive tasks (still under threshold) | reputation −0.20, no jail |
| **L3** | Worker voluntarily lowers `capacity` | on-chain `capacity` updated, `in_flight` unaffected |
| **L4** | Worker is slow but does not time out | settles normally, no penalty |
| **L5** | Verifier is slow but submits inside the verification window | receives the verifier fee normally |

### 2.2 Moderate tier — triggers jail

| ID | Scenario | Expected result |
|---|---|---|
| **M1** | Sliding window: 3 misses across 10 tasks | 1st jail (120 blocks), reputation −0.30 |
| **M2** | Worker fails to submit batch log | every task in the batch is FAIL, 1st jail |
| **M3** | Verification FAIL (Worker logits ≠ Verifier logits) | task FAIL, fee not settled, 1st jail |
| **M4** | Worker timeouts exceed sliding-window threshold | jail; all unfinished tasks redispatched |
| **M5** | After 1st jail → unjail → reoffend | 2nd jail (720 blocks) |
| **M6** | Worker tries to accept tasks while jailed | chain rejects; `in_flight` does not grow |
| **M7** | After 1st jail, completes 999 honest tasks (decay threshold not reached), then reoffends | `jail_count` still 1, reoffence goes straight to 2nd jail |
| **M8** | After 1st jail, completes 1000 honest tasks (one decay step) | `jail_count` decays to 0; reoffence goes back to 1st jail |

### 2.3 Severe tier — slash + permanent ban

| ID | Scenario | Expected result |
|---|---|---|
| **S1** | 3rd jail | permanent ban + 5 % slash + tombstone; stake deduction is exact |
| **S2** | Successful FraudProof submitted by user | Worker permanently banned + slashed; user receives compensation if applicable |
| **S3** | Verifier liability chain (1st-tier verifies PASS, 2nd-tier returns FAIL) | each of the 3 first-tier verifiers loses 2 % stake + 0.20 reputation |
| **S4** | Slashed Worker tries to unjail | chain rejects (tombstone) |
| **S5** | Slashed Worker tries to re-register | must restake from scratch; remaining stake retrievable only after the slash deduction has been processed |
| **S6** | Worker is judged FAIL while a second-verification fee lock is in flight | fee is not released; jail flow proceeds |

### 2.4 Combined tier — multiple events overlapping

| ID | Scenario | Expected result |
|---|---|---|
| **C1** | 1st jail → unjail → 500 honest → 1 cheat → 500 honest → reoffend (the two 500-streaks add to exactly the 1000-task decay threshold) | `jail_count` should still be 1, reoffence returns to 1st jail |
| **C2** | 1st jail → unjail → 999 honest → cheat | `jail_count` still 1, goes straight to 2nd jail (720 blocks) |
| **C3** | One batch contains 3 FAIL tasks + 2 PASS tasks | the 3 FAIL tasks do not settle; the 2 PASS tasks settle normally; jail still fires |
| **C4** | 10 Workers jailed simultaneously, Leader redispatches all in-flight tasks | the remaining Workers correctly accept; no task is lost |
| **C5** | Verifier reputation drops below 0.1 after liability cascade | VRF essentially stops selecting them, but no permanent ban (reputation can recover) |
| **C6** | Worker submits result one block before `expire_block` | settles normally, not judged expired |
| **C7** | Worker submits result on the `expire_block` itself | boundary: expired or accepted? **Protocol must define explicitly.** |
| **C8** | User balance drops to 0 after dispatch but before settle | task moves to `REFUNDED`; Worker does not get paid but is not penalised (not the Worker's fault) |
| **C9** | Same `task_id` resubmitted | chain dedupes; the second submission is rejected |
| **C10** | Second-tier Verifier also returns a lazy PASS, then third-tier verification catches it | every 1st-tier and 2nd-tier Verifier in the liability chain is penalised |

---

## 3. Invariant checks

After each round of every scenario, the harness re-checks all seven invariants. Any failure stops the run immediately and emits the offending state.

### 3.1 Fee conservation

`total_supply = initial_supply + block_rewards − burns`. For every individual fee, the splits — Worker 85 % / Verifier 12 % / multi-verification fund 3 % — sum back to the 100 % the user paid. On a FAIL path, Worker receives nothing; the user refund + Verifier share + fund share still sum to 100 %.

### 3.2 Stake conservation

The amount slashed is exactly `stake × 5 %`. After a slash, `stake_remaining = stake_original − slashed`.

### 3.3 Reputation bounds

Reputation always stays in `[0.0, 1.0]`. A −0.10 miss never drops below 0.0; a +0.01 success never goes above 1.0.

### 3.4 State-machine legality

Worker state transitions only along legal edges:

```
ACTIVE  →  JAILED  →  ACTIVE   (via unjail)
ACTIVE  →  JAILED  →  TOMBSTONED
```

Any other transition is an invariant violation.

### 3.5 `jail_count` consistency

`jail_count ≥ 0` always. Decay occurs only at the 1000-honest-task milestone, decay step is exactly `−1`.

### 3.6 Task uniqueness

The same `task_id` is settled at most once.

### 3.7 `in_flight` consistency

`0 ≤ in_flight ≤ capacity` always. After a task completes, fails, or is redispatched, `in_flight` decrements by exactly 1.

---

## 4. Fuzzing parameters

Each scenario is not run once — it is parameter-randomised across N rounds.

| Parameter | Random range |
|---|---|
| Worker count | 5–50 |
| Verifier count | 10–100 |
| Task count | 100–10 000 |
| `batch_capacity` | 1–32 |
| Cheat frequency | 0.1 % – 10 % |
| Miss frequency | 1 % – 20 % |
| Concurrent SDK requests | 1–100 |
| Output token length | 10–500 |
| Temperature | 0 / 0.3 / 0.7 / 0.9 / 1.2 |

Each scenario runs **at least 1000 randomised rounds**. Any single invariant violation halts and emits the failing state.

---

## 5. Execution

Use Go's `testing.F` fuzz framework, or a hand-rolled harness. One `Test*` function per scenario; parameters derive from the fuzzing seed.

CI integration:

| Trigger | Target |
|---|---|
| Every PR | `make test-byzantine-quick` — 100 rounds per scenario |
| Nightly | `make test-byzantine-full` — 10 000 rounds per scenario |

**No real GPU required.** All penalty-path tests exercise on-chain state-machine logic with mock logits and mock TGI — same harness as PR #23's `e2e-mock`.

---

## 6. Expected output

After each fuzzing run the harness prints a report. A passing run looks like this:

```
=== Byzantine Fuzzing Report ===
Scenarios:                 30
Rounds per scenario:       1000
Total assertions:          210,000

L1-L5 (light):             5,000/5,000 PASS
M1-M8 (moderate):          8,000/8,000 PASS
S1-S6 (severe):            6,000/6,000 PASS
C1-C10 (combined):         10,000/10,000 PASS

Invariants checked:        7 × 30,000 = 210,000
Violations:                0

Fee conservation:          PASS (max drift: 0 uFAI)
Stake conservation:        PASS
Reputation bounds:         PASS
State machine:             PASS
jail_count:                PASS
Task uniqueness:           PASS
in_flight:                 PASS
```

Any line that is not `PASS` blocks mainnet — fix the code, re-run, all green is the gate.

---

## 7. Cross-references

- `docs/testing/Pre_Mainnet_Test_Plan.md` §2.4 — this plan is the concrete realisation of the "Protocol Byzantine scenarios" item there
- `docs/testing/reports/2026-04-21-v6-phase1a/SUMMARY.md` — the Phase 1 prerequisite (V6 single-machine A1)
- `docs/protocol/FunAI_V52_Final.md` — penalty + jail spec these scenarios derive from
- `docs/protocol/FunAI_PreLaunch_Final_Audit_KT.md` — KT pre-launch audit decisions, several of which (jail mechanics, second-verification fee handling) are tested here

---

*Translated from `FunAI V6 Byzantine Test Plan KT.pdf` (Chinese source by KT, 2026-04-27). The Chinese source is removed per the repo's English-only policy for tracked files.*
