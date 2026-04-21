# V6 Batch Log-Replay Verification

## P0 — Blocks mainnet

### 1. Replay Scheduler

Add a replay mode to the Verifier's inference engine. Following the batch log the
Worker sent, insert/remove the designated request into/out of the batch at the
designated step.

**Why.** FunAI's verification scenario is: Worker runs batched inference,
Verifier verifies after the fact. The C0 test proved that logits from a Worker
at `batch=4` differ from a Verifier at `batch=1` by 2.27 %, so the verification
breaks down. An engineer has separately verified that same-batch, same-content
execution across T4 and 5090 is bit-exact (0.000000). Therefore, if the
Verifier can precisely replay the Worker's batching process, verification holds.
The replay scheduler is what lets the Verifier do that.

### 2. Worker Batch Logging

Add a hook to the Worker's scheduler that records, at every decode step, the
list and order of requests in the batch. After inference completes, the log is
P2P-broadcast to the Verifier alongside the `BatchReceipt`. Add a field on
`BatchReceipt` marking which inference batch each task belongs to.

**Why.** Under continuous batching, batch composition changes every decode step
(new requests join, finished requests leave). Without a per-step batch roster,
the Verifier cannot know how the Worker actually ran the inference, and cannot
replay precisely.

### 3. Batch-mode Dispatch

- Worker declares `batch_capacity` at registration time. No upper bound.
- Chain maintains an `in_flight` counter. Leader keeps dispatching while
  `in_flight < capacity`.
- Remove the busy/idle two-state.
- Add `batch_wait_timeout` (default 500 ms, chain-adjustable). After the
  Worker receives its first task, it waits up to the timeout to accumulate more
  tasks into the batch.

**Why.** The original one-task-at-a-time + busy state forced the Worker to
`batch=1` forever, producing GPU utilisation of only 10-20 %. Under batch mode
the Worker processes multiple tasks concurrently; throughput rises 5-10×, so
operator revenue rises materially — operators will actually show up.

No upper bound on capacity because the technology keeps advancing; a hard cap
would be outdated quickly. A Worker over-stating capacity naturally triggers
timeouts and therefore jail — the market punishes misrepresentation
automatically.

`batch_wait_timeout` exists so low-traffic periods don't make users wait too
long — after 500 ms the Worker starts regardless of whether the batch is full.

### 4. Settlement Adjustment

- First-tier (3 verifiers) PASS → settle immediately.
- Picked by VRF for second-/third-tier verification → fee is locked until
  verification completes; the full three-round chain must finish within 24
  hours.
- `BatchReceipt` carries an inference-batch-ID field. The Proposer's
  `MsgBatchSettlement` logic itself is unchanged.

**Why.** Workers can't be made to wait for all tasks to walk through all three
rounds before getting paid — the vast majority of tasks clear after the first
verification and should be paid out immediately. Only tasks selected for
subsequent verification have their fee locked. That way Worker cash flow is
healthy and money isn't held up waiting for verification. 24 hours is a hard
cap; exceeding it means the verification chain itself is broken and needs
manual intervention.

---

## P1 — Penalty mechanism fixes

### 5. `jail_count` decays per 1000 tasks

Decay one level per 1000 tasks, rather than resetting to zero at 50 tasks.

**Why.** The original "reset at 50" rule invites cadenced cheating — cheat
once → do 50 honest tasks → `jail_count` resets → cheat again. The Worker
stays permanently at jail-count = 1 (10 min cooldown), so cheating cost is
constant. Changing to "one level decay per 1000 tasks" compresses cheating
frequency 20× and causes the penalty to escalate on repeat offence within the
window.

### 6. 3 consecutive misses trigger jail

3 consecutive timeouts / no-result deliveries → triggers progressive jail. The
progressive-jail ladder is unchanged (10 min → 1 h → permanent + 5 % slash).

**Why.** The original design deducted only reputation (-0.10) on a miss, with
no jail. A Worker that over-stated capacity could then time out many requests
in a row — reputation would slowly decline while users accumulated wait-time
and re-dispatch cost. Triggering jail after 3 consecutive misses is far
stricter than reputation alone, and escalates to permanent ban in a handful of
rounds.

### 7. No batch log = the whole batch FAILs

If the Worker fails to deliver the batch log, the Verifier cannot replay, so
every task in that batch is judged FAIL and the Worker walks the
progressive-jail ladder.

**Why.** The batch log is the Verifier's only replay input. A Worker who just
realised it was cheating (e.g. ran a smaller model) might withhold the log to
escape verification. Equating "no log" with "caught cheating" closes this free
escape hatch. The whole batch must FAIL (not just one task) because without a
log the Verifier cannot even tell which task is correct.

### 8. Verifier collective punishment

First-tier PASS but second-tier FAIL → each of the 3 first-tier verifiers
loses 2 % stake and 0.20 reputation.

**Why.** Without a penalty, the first-tier Verifier's dominant strategy is to
PASS every received verification task and skip the GPU work — there is no
downside. Collective punishment flips the expected value: skipping saves a
handful of pennies of GPU, but risks being caught by second-tier verification
at a cost of 2 % stake (hundreds to thousands of FAI). 2 % and -0.20 are
suggested values; make both chain-adjustable.

### 9. Capacity over-statement penalty

No dedicated mechanism needed. Over-statement → sustained timeouts → 3
consecutive misses → progressive jail (per #6).

**Why.** Item #6 already covers this scenario. A Worker declaring capacity
= 100 but able to run only 10 will have 90 of every 100 tasks time out, hit 3
consecutive misses almost instantly, and walk up the jail ladder to permanent
ban in a few rounds. No additional logic required.

---

## P2 — Verification logic tweaks

### 10. Logits-comparison epsilon

Keep the existing design: the model proposer measures and sets epsilon at model
registration time, stored in the model registry.

**Why.** Different models have different logit ranges — a single global
epsilon is wrong. The proposer measures across 100 prompts × 2+ GPU families ×
3 runs at model-registration time, picks P99.9, and records it. This design
was already correct.

Under batch log-replay, the expected diff is 0.000000, but the protocol
should not hard-code epsilon = 0 — this is an open-source foundational system,
and the full combinatorial space of hardware / CUDA / driver versions in the
wild is unpredictable. Letting the model proposer decide from measured data is
the most resilient approach.

### 11. ChaCha20 sampling verification restored to 100 % coverage

Batch log-replay guarantees that logits match precisely → the sampling path is
fully replayable → every `temperature > 0` task is 100 % verifiable.

**Why.** Under V5.2, only 7 % of sampled tasks could undergo ChaCha20 sampling
verification (the remaining 93 % were skipped because batch drift made logits
disagree, so sampling verification was unreliable). With batch log-replay
guaranteeing exact logits, ChaCha20 can replay the sampling path correctly on
every task. Sampling-manipulation attacks (injected advertisements,
conversational steering, content censorship) move from "undetectable on 93 %
of tasks" to "detected with precision on 100 %". This is V6's largest
security gain relative to V5.2.

---

## Deprecated

- :x: Verification proxy — superseded by batch replay; no need to force the
  Verifier path to `batch=1`.
- :x: 7 % VRF-sampled logits resubmission — with 100 % precise verification,
  no sampling fallback is needed.
- :x: Top-K rank check — precise comparison removes the need for lenient
  matching.
- :x: 48 h `TaskCache` — no Worker after-the-fact data re-submission required.
- :x: Auto-adjusted `sampling_rate` — no sampling rate to tune; every task is
  verified.
- :x: SGLang determinism mode — no dependency on a specific engine.
- :x: LLM-42 — same reason.
- :x: Hardware-partitioned subnets — batch replay is bit-exact across T4 and
  5090 already, so no partitioning needed.
- :x: Clawback — fee is locked until verification completes, then released;
  there is no "pay first, claw back later".

---

## Unchanged

- Three-tier verification architecture (first / second / third) — layered
  defence, each tier addresses different attacks.
- Progressive-jail ladder (10 min → 1 h → permanent + 5 % slash) — existing
  design is sound.
- FraudProof user reports — last-line-of-defence at the user layer.
- VRF Verifier selection — existing design is sound.
- Reputation system (miss -0.10, success +0.01, hourly decay toward 1.0) —
  existing design is sound.
- Per-model-registration epsilon set by the proposer — existing design is
  sound.
- `MsgBatchSettlement` logic — Proposer's on-chain-batch packaging remains
  unchanged.

---

**Suggestion on #6:** use a sliding window instead of strict "consecutive":
jail on `≥ 3 misses in the last 10 tasks`. This avoids tripping on occasional
1–2 misses while still catching a Worker that misses frequently. Make both
10 and 3 chain-adjustable parameters.
