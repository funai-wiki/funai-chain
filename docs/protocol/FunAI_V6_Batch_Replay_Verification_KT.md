# FunAI V6 Execution Spec: Batch-Log Replay Verification

| Field | Value |
|---|---|
| Priority | P0 — blocks mainnet |
| Date | 2026-04-21 |
| Replaces | V5.2's entire stack of 7 % VRF sampling + verification proxy + top-K rank check |
| Evidence | C0 test FAIL (batch drift 2.27 %) + engineer-side measurement: same batch, same content, across T4 and 5090 → bit-exact (0.000000) |

---

## 1. Core Principle

Given identical content inside an identical batch, any GPU produces bit-exact
logits (empirically verified across T4 vs 5090 → 0.000000).

Worker records the batch schedule → Verifier replays the same batch from that
schedule → logits are compared exactly → no room for cheating to hide.

The scheme depends on neither a specific inference engine (SGLang deterministic
mode / LLM-42 are both unnecessary) nor on hardware partitioning, and incurs no
throughput loss.

---

## 2. P0 Changes (block mainnet)

### 2.1 Replay Scheduler

Add a replay mode to the Verifier's inference engine that, given the Worker's
batch log, inserts/removes designated requests into/out of the batch at the
designated decode steps.

**Why.** FunAI's verification scenario is: Worker runs batched inference,
Verifier verifies after the fact. The C0 test proved that Worker `batch=4`
logits differ from Verifier `batch=1` logits by 2.27 %, so the verification
breaks down. Engineer-side measurement showed that same batch, same content,
across T4 and 5090 is bit-exact at 0.000000. Therefore, as long as the
Verifier can precisely replay the Worker's batch process, verification holds.
The replay scheduler is what gives the Verifier that capability.

### 2.2 Worker Batch Logging

Add a hook to the Worker's scheduler that, at every decode step, records the
batch roster (request IDs and their in-batch order). After inference
completes, the log is broadcast via P2P alongside the `BatchReceipt`. The
`BatchReceipt` gains a field tagging which inference batch each task belongs
to.

**Why.** Under continuous batching, the batch composition at every decode
step changes (requests join, completed requests leave). The Verifier must
know the per-step roster in order to replay precisely. Without the log, the
Verifier cannot know how the Worker actually ran.

### 2.3 Dispatch switches to batch mode

- Worker declares `batch_capacity` at registration time. No upper bound.
- Chain maintains an `in_flight` counter; Leader keeps dispatching while
  `in_flight < capacity`.
- Remove the busy/idle two-state.
- Add `batch_wait_timeout` (default 500 ms, chain-adjustable). After the
  Worker receives its first task, it waits up to the timeout to accumulate
  more tasks into the batch.

**Why.** The original one-request-per-dispatch + busy state forced Worker to
`batch=1` forever, producing 10–20 % GPU utilization. Under batch mode the
Worker processes multiple tasks concurrently; throughput rises 5-10×,
operator revenue rises materially, and operators actually show up. No upper
bound on capacity because the technology keeps advancing; a hard cap would
be outdated. A Worker that over-states capacity naturally triggers timeouts
and thus jail — the market punishes misrepresentation automatically.
`batch_wait_timeout` exists so low-traffic periods don't make users wait too
long — after 500 ms the Worker starts regardless of whether the batch is full.

### 2.4 Settlement Adjustment

- First-tier (3 Verifiers) PASS → settle immediately.
- Picked by VRF for second-/third-tier verification → fee is locked until
  verification completes; the full three-round chain must finish within 24
  hours.
- `BatchReceipt` carries an inference-batch-ID field; the Proposer's
  `MsgBatchSettlement` logic itself is unchanged.

**Why.** Workers must not be made to wait for every task to walk through all
three rounds before getting paid — the vast majority of tasks clear after
first-tier verification and should be paid out immediately. Only tasks
selected for later verification have their fee locked. That keeps Worker cash
flow healthy instead of tying up funds waiting for verification. 24 hours is
a hard cap; exceeding it means the verification chain itself is broken and
needs human intervention.

### 2.5 Engine-version declaration at model registration

When proposing a model, the proposer declares which inference engine and
version (vLLM / SGLang / TGI + specific version number) the model uses. The
declaration is stored on-chain as part of the model's registration metadata,
alongside `weight_hash` and `quant_config_hash`. Workers and Verifiers
running this model must use the declared engine and version. Different
models can use different engines. Upgrading the engine version is equivalent
to registering a new `model_id`.

**Why.** If Worker runs vLLM 0.6.1 and Verifier runs vLLM 0.6.2, the internal
scheduler may have been tweaked and the same batch log replays to different
results. Pinning the engine version is what allows batch replay to remain
bit-exact.

---

## 3. P1 Changes (penalty mechanism fixes)

### 3.1 `jail_count` decays per 1000 tasks

Decay one level per 1000 tasks, rather than resetting to zero at 50 tasks.

**Why.** The "reset at 50" rule invites cadenced cheating — cheat once → do
50 honest tasks → `jail_count` resets → cheat again. The Worker stays
permanently at jail-count = 1 (10 min cooldown), so cheating cost is
constant. Changing to "one level decay per 1000 tasks" compresses cheating
frequency 20× and causes the penalty to escalate on repeat offence within
the window.

### 3.2 Sliding-window miss triggers jail

If `miss >= 3` within the most recent 10 tasks → trigger progressive jail.
Progressive-jail ladder is unchanged (10 min → 1 h → permanent + 5 % slash).
Window size (default 10) and threshold (default 3) are both chain-adjustable
parameters.

**Why.** The original design only deducted reputation (-0.10) on a miss, with
no jail. A Worker that over-states capacity could then time out many requests
in a row — reputation would slowly decline while users accumulated
wait-time and re-dispatch cost. Using a sliding window rather than strict
consecutive is better: a one-off network blip or GPU OOM should not jail an
honest operator, but 3 misses in 10 tasks is no longer incidental — either
the hardware is broken or the capacity declaration is over-stated. The
correct Worker response is: miss some tasks → lower capacity → take fewer
tasks → miss less → no jail. The system guides Workers toward honestly
declaring their capacity, rather than punishing occasional failures.

### 3.3 No batch log = the whole batch FAILs

If the Worker fails to deliver the batch log, the Verifier cannot replay, so
every task in that batch is judged FAIL and the Worker walks the
progressive-jail ladder.

**Why.** The batch log is the Verifier's only replay input. A Worker who
realized it was cheating (e.g. ran a smaller model) might withhold the log
to escape verification. Equating "no log" with "caught cheating" closes this
free escape hatch. The whole batch must FAIL (not just one task) because
without a log the Verifier cannot even tell which task is correct.

### 3.4 Verifier collective punishment

First-tier PASS but second-tier FAIL → each of the 3 first-tier Verifiers
loses 2 % stake and 0.20 reputation.

**Why.** Without a penalty, the Verifier's dominant strategy is to PASS
every received verification task and skip the GPU work — there is no
downside. Collective punishment flips the expected value: skipping saves a
handful of pennies of GPU, but risks being caught by second-tier
verification at a cost of 2 % stake (hundreds to thousands of FAI). The 2 %
and -0.20 values are suggested; make both chain-adjustable.

### 3.5 Capacity over-statement penalty

No dedicated mechanism needed. Over-statement → frequent timeouts → sliding
window `miss >= 3 in 10` → jail → progressive jail.

**Why.** §3.2 already covers this. A Worker declaring capacity = 100 but
able to run only 10 will time out most tasks, quickly tripping the sliding
window threshold, then walking the jail ladder. No extra logic required.

---

## 4. P2 Changes (verification logic tweaks)

### 4.1 Logits comparison

Keep the existing design: epsilon is set by the model proposer at
registration time, based on their own measurements, and stored in the model
registry.

**Why.** Different models have different logit value ranges — a single
global epsilon is wrong. The proposer measures across 100 prompts × 2+ GPU
families × 3 runs at model-registration time, picks P99.9, and records it.
That design was correct. Under batch log-replay the expected diff is
0.000000, but the protocol must not hard-code epsilon = 0 — this is an
open-source foundational system and the full combinatorial space of
hardware / CUDA / driver versions in the wild is unpredictable. Letting the
model proposer decide from measured data is the most resilient approach.

### 4.2 ChaCha20 sampling verification restored to 100 % coverage

Batch log-replay guarantees that logits match precisely → the sampling path
is fully replayable → every `temperature > 0` task is 100 % verifiable.

**Why.** Under V5.2, only 7 % of sampled tasks could undergo ChaCha20
sampling verification (the remaining 93 % were skipped because batch drift
made logits disagree, and sampling verification was unreliable). With batch
replay guaranteeing exact logits, ChaCha20 can replay the sampling path
correctly on every task. Sampling-manipulation attacks (advertisement
injection, conversational steering, content censorship) move from
"undetectable on 93 % of tasks" to "detected with precision on 100 %". This
is V6's single largest security gain relative to V5.2.

### 4.3 VRF Verifier selection with VRAM filter

The Verifier must replay the Worker's entire batch at once — batches cannot
be split for verification. When selecting Verifiers via VRF, only consider
nodes whose VRAM is large enough to hold the batch.

**Why.** If the Worker runs `batch=20` on an A100, a 4090 cannot hold it —
it cannot replay the full batch. Splitting the batch for individual
verification is not an option: logits depend on the composition of the
entire batch, and splitting causes drift. The Worker can also voluntarily
lower its `batch_capacity` to widen the Verifier candidate pool — a big
batch earns more but has a smaller Verifier pool, a small batch earns less
but has a larger pool. The market self-regulates.

---

## 5. Deprecated

The following V5.2 mechanisms are no longer needed:

- verification proxy (superseded by batch replay; no need to force
  `batch=1` on the Verifier path)
- 7 % VRF-sampled logits resubmission (100 % precise verification; no
  sampling fallback required)
- top-K rank check (precise comparison removes the need for lenient
  matching)
- 48 h `TaskCache` (no Worker after-the-fact data resubmission)
- Auto-adjusted `sampling_rate` (no sampling rate to tune; every task is
  verified)
- SGLang deterministic integration (no dependency on a specific engine)
- LLM-42 integration (same reason)
- Hardware-partitioned subnets (batch replay is bit-exact across T4 and
  5090; no partitioning)
- Clawback (fee is locked until verification completes; no "pay first,
  claw back later")

---

## 6. Unchanged

- Three-tier verification architecture (first / second / third) — layered
  defence, each tier addresses different attacks.
- Progressive-jail ladder (10 min → 1 h → permanent + 5 % slash) — existing
  design is sound.
- FraudProof user reports — last line of defence at the user layer.
- VRF Verifier selection — existing design is sound.
- Reputation system (miss -0.10, success +0.01, hourly decay toward 1.0) —
  existing design is sound.
- Per-model-registration epsilon set by the proposer — existing design is
  sound.
- `MsgBatchSettlement` logic — Proposer's on-chain batch packaging
  unchanged.

---

## 7. Issues GPU testing will likely surface

### 7.1 Continuous-batching replay precision

The inference engine's internal scheduler may have hidden state (KV-cache
allocation order, prefill/decode interleaving strategy). The Worker's log
captures batch rosters but may miss hidden variables that affect logits,
causing the Verifier's replay to diverge.

**Recommendation.** Validate with static batches first, then add
continuous-batching complexity step by step and observe where drift begins.

### 7.2 Chunked prefill split points

Long prompts are split by the engine into multiple prefill chunks. Different
split points produce different logits.

**Recommendation.** Empirically confirm that, given the same input and the
same engine version, the split is deterministic. If not, the log must
additionally record the split points.

### 7.3 Request join/leave boundary conditions

Under continuous batching, new requests are inserted between decode steps.
"C joined at step 5" vs "C joined after step 5 ended" are two different
events with different logits.

**Recommendation.** Log granularity must be precise enough to record at
which step and which phase an insert occurred.

### 7.4 Engine version alignment

Already addressed in §2.5. Declare engine and version at model registration;
Workers and Verifiers must match.

### 7.5 Verifier VRAM shortage

Already addressed in §4.3. Filter by VRAM in VRF Verifier selection.
Workers can lower `batch_capacity` voluntarily to widen the Verifier
candidate pool.

### 7.6 Log transmission bandwidth

A 500-token task at `batch=8` records roughly 4000 entries per step, a few
tens of KB per task — not large. But under high throughput (100 batches/s)
the cumulative P2P log traffic may consume bandwidth.

**Recommendation.** Measure whether log transmission becomes a bottleneck
at the target TPS; compress if necessary.

---

## 8. Execution order

1. Do §2.1 and §2.2 first (replay scheduler + log recording) — this is the
   core engineering bottleneck for the entire scheme.
2. After §2.1 and §2.2 are done, run an end-to-end test: one Worker runs a
   batch, one Verifier replays from the log, check whether logits are
   bit-exact. PASS → continue; FAIL → return to discussion.
3. After the test passes, implement §2.3 (dispatch switches to batch mode).
4. Then §2.4 and §2.5 (settlement adjustment + engine-version declaration).
5. Finally §3 and §4 (penalty mechanisms + verification logic tweaks, all
   parameter-level changes).

---

## 9. V6 vs V5.2 — key advantages

| Dimension | V5.2 | V6 |
|---|---|---|
| Cross-hardware | Requires subnet partition | T4/5090 verified; no partition needed |
| Throughput loss | 34 % (SGLang deterministic mode) | Zero |
| Precise-verification coverage | 7 % sampling | 100 % |
| Sampling-manipulation detection | 93 % unreliable | 100 % precise |
| Inference-engine dependency | SGLang deterministic | None — any engine (pin version only) |
| temperature > 0 verification | Valid within 7 % sample | 100 % valid (full ChaCha20 coverage) |
