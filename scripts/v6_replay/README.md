# V6 Batch Log-Replay PoC

Research-stage proof-of-concept for the V6 Batch Log-Replay verification scheme
described in [`docs/protocol/FunAI_V6_BatchReplay_Design.md`](../../docs/protocol/FunAI_V6_BatchReplay_Design.md).

This directory is **not** production code. It validates the load-bearing
assumptions of V6 *before* committing engineering effort to the protocol
rewrite. Ships on a `research/v6-replay-poc` branch; only promotes to
`mainnet-readiness/*` if Phase 1 and Phase 2 both PASS.

## Purpose

V6 rests on two claims that current FunAI evidence does not yet support:

- **A1 (engineering feasibility).** Some inference runtime can be driven to
  *replay* a pre-recorded per-step batch schedule, producing the same logits
  as the original Worker that generated that schedule.
- **A2 (cross-hardware determinism).** When the same schedule is replayed on
  different GPU hardware (e.g. T4 vs RTX 5090), the logits at every decode
  step are bit-exact.

If either is false, V6's verification architecture does not work and
development should fall back to Option B (single-request teacher forcing
attached to the receipt). This PoC answers both in ~3 weeks of focused work,
before any protocol-layer code is written.

## Engine choice

HuggingFace transformers, driven manually at the token level — not TGI, not
vLLM.

Rationale:

- TGI and vLLM both hide continuous-batch scheduling inside Rust / C++ and do
  not expose a public "replay this schedule" API. Forking either adds 2–3
  months before the first cross-hardware run.
- `transformers` is pure Python and exposes `forward(input_ids,
  past_key_values=...)` directly, so implementing a deterministic manual
  scheduler is a few hundred lines.
- Throughput is 10–20× lower than TGI at runtime, which disqualifies this
  engine for production Workers — but Phase 1 and Phase 2 are not about
  throughput, they are about determinism. Production engine choice is a
  Phase 3+ decision, contingent on Phase 1/2 results.

A positive PoC does **not** prove V6 is implementable on TGI or vLLM, only
that the scheme is internally consistent. That's deliberate. If the
transformers-based PoC fails, we stop. If it passes, Phase 3 evaluates
production-engine ports.

## Phase structure

| Phase | Duration | Deliverable | Gate question |
|---|---|---|---|
| 0 | 0.5–1 d | This README + stub modules + failing tests | Is the contract definable? |
| 1 | 2–4 w | `WorkerSimulator` + `ReplayEngine` on transformers | Does same-GPU replay produce bit-exact logits? |
| 2 | 1 w | Cross-hardware run on 2 GPU instances | Does cross-hardware replay produce bit-exact logits? |
| 3+ | TBD | Protocol integration only if Phases 1–2 PASS | — |

## Acceptance criteria

Each phase has a **hard PASS condition** and a **kill condition**. A kill
means V6 research stops — fall back to Option B and either close
`research/v6-replay-poc` or reframe it as a negative-result writeup.

### Phase 0 — Scaffold (this PR)

**PASS when:**
- `scripts/v6_replay/` directory exists with all files listed below
- `pytest scripts/v6_replay/` runs and fails with clear `NotImplementedError`
  messages referencing the methods that still need to be written
- Python syntax is clean (`python -m py_compile` on every `.py`)

**Kill:** N/A. Phase 0 is mechanical.

### Phase 1 — Single-machine replay bit-exact

Phase 1 is internally staged to separate three concerns that can each
fail independently. A KILL at any sub-phase sends V6 back to the drawing
board; an INVESTIGATE must be resolved before the next sub-phase starts.

| Sub-phase | Scope | V6 claim strength |
|---|---|---|
| 1a | `temperature=0` argmax; fixed batch (no joins/leaves) | Weakest — validates determinism floor only |
| 1b | `temperature>0` with ChaCha20-seeded sampling; fixed batch | Validates sampling path is replayable |
| 1c | `temperature>0`; **dynamic** batch composition (joins/leaves) | Validates V6's distinctive claim — batched continuous-batching is replayable when the schedule is recorded |

Phase 1 PASS = 1a ∧ 1b ∧ 1c all PASS. A PASS on only 1a/1b is not enough
to move to Phase 2 — Phase 2 extends 1c to cross-hardware, so 1c must
work single-machine first.

#### Phase 1a — temperature=0, fixed batch

**Method.**
- Model `Qwen/Qwen2.5-3B-Instruct` FP16 (C0 baseline). Override via
  `V6_MODEL` env var for quicker iteration.
- Run `WorkerSimulator.run_batch(prompts=[P1..P4], temperature=0.0,
  max_new_tokens=10)` once. Capture `(outputs, per-prompt logits at every
  position, batch_log)`.
- Call `ReplayEngine.replay(batch_log, target_task_id=P_i)` for each of
  the 4 targets. Capture the replayed logits.
- Diff the Worker's logits for target `P_i` against the replayed logits
  for target `P_i`.

**PASS condition.** `max_abs_err == 0.0` exactly, across all output
positions of all 4 targets, across 3 repeated runs of the whole
procedure. 12 Worker-vs-Replay comparisons, zero drift.

**INVESTIGATE.** `max_abs_err ∈ (0, 1e-6]`. Suggests an implementation
leak (dropout active, RNG not seeded, deterministic flag missing). Fix
before moving on.

**KILL for Phase 1a.** `max_abs_err > 1e-6` and not fixable by
deterministic-flag tuning, OR replay output text differs from Worker
output text for the same seed. At this severity, the determinism floor
V6 depends on is not reachable via transformers + flags on the PoC box.

#### Phase 1b — temperature>0, fixed batch, deterministic sampling

Adds seeded multinomial sampling (ChaCha20 per V5.2 §9.3 semantics) to
Phase 1a. Same hardware, same batch composition.

**PASS condition.** Same as 1a — `max_abs_err == 0.0` on logits, and
identical sampled token IDs.

**KILL for Phase 1b.** Logits match but sampled tokens differ → RNG
state leak between Worker and Replayer (ChaCha20 seed derivation not
identical). Or logits diverge → Phase 1a's determinism base regressed,
unlikely but possible if the sampling path touches the model.

#### Phase 1c — temperature>0, dynamic batch

Adds tasks joining/leaving mid-batch. Worker's scheduler decides when
each task is active; `BatchLog.steps` records the exact roster per step;
`ReplayEngine.replay` executes the recorded roster.

**PASS condition.** Same as 1b, plus: the `BatchLog` must contain at
least one step where `active_task_ids` differs from the previous step
(otherwise 1c degenerates to 1b).

**KILL for Phase 1c.** `max_abs_err > 1e-6` on logits for a target that
transits across a join/leave boundary. Means the manual-scheduler
abstraction cannot capture enough state to reproduce the continuous-batch
execution. **V6 dies at this gate.** Report findings; fall back to
Option B.

#### Phase 1 MoE coverage — `test_phase1_moe.py`

Phase 1a / 1b / 1c above were originally validated on dense models
(Qwen2.5-3B / Qwen2.5-0.5B). MoE introduces a separate non-determinism
risk — **expert-routing drift**: the gating network's softmax may pick
different top-k experts on the replay side under the same inputs, even
when the batch composition is identical.

`scripts/v6_replay/test_phase1_moe.py` runs the dynamic-batch path
(`run_batch_dynamic` / `replay_dynamic`) on a Mixture-of-Experts model
and asserts both bit-exact logits AND bit-exact top-k expert IDs at
every (step, layer). The two assertions together let one run distinguish
between two failure modes that differ in mitigation:

| Failure mode | Logits diff | Expert-ID diff | Mitigation |
|---|---|---|---|
| **Path 1 — gating non-determinism** | > 0 | mismatched | Record top-k expert IDs in BatchLog and force the Replayer to follow them (not yet implemented; this test surfaces whether it is needed) |
| **Path 2 — expert internal compute drift** | > 0 | matched | Same as the dense-model batch drift; addressed by the existing replay path |

Hardware notes:

- **Mixtral-8x7B-Instruct-v0.1** (47 B / 13 B active) bf16 = ~94 GB → needs A100/H100 80 GB; AWQ Q4 ~24 GB → tight on a single 4090
- **Qwen1.5-MoE-A2.7B** (14 B / 2.7 B active) bf16 ~28 GB → fits L20 48 GB / A100; tight on 4090
- **Smaller MoE alternatives** are scarce; the test is opt-in via `V6_MODEL` env var so the dense Phase 1 suite is unaffected on workstations without a big-MoE-capable GPU

Usage:

```
V6_MODEL=mistralai/Mixtral-8x7B-Instruct-v0.1 \
V6_DEVICE=cuda \
pytest scripts/v6_replay/test_phase1_moe.py -v
```

A PASS here is what V6 needs in order to claim "supports MoE models";
a Path 1 hit triggers the force-routing follow-up patch; a Path 2 hit
gets folded into the existing batch-replay analysis.

### Phase 2 — Cross-hardware replay bit-exact

**Method.**
- Two GPU machines provisioned via `scripts/tgi-bootstrap-aliyun.sh` (or
  equivalent) — one T4-class, one 5090/4090-class, different SM architectures
  deliberately. Record driver / CUDA / torch versions for both.
- On machine A: run `WorkerSimulator.run_batch` → record `batch_log` and
  `reference_logits`.
- Transfer `batch_log` + prompts to machine B.
- On machine B: run `ReplayEngine.replay(batch_log)` → capture
  `replayed_logits`.
- Diff `reference_logits` vs `replayed_logits`.

**PASS condition.** `max_abs_err == 0.0` exactly, across all output positions
of all 4 targets, across both (A→B) and (B→A) directions, across 3 repeats
each. 24 cross-machine comparisons, zero drift.

**INVESTIGATE.** `max_abs_err ∈ (0, 1e-6]`. Document; this may still be
acceptable if a chain-level ε_floor of 1e-6 is tolerable (matches Option B's
ε_floor). Not a PASS in the strict sense but not a KILL either.

**KILL.** `max_abs_err > 1e-6`. Means cross-hardware determinism claim A2
is false; V6 assumption breaks. Stop V6; Option B remains the answer.

### Phase 3 gate (non-goal for this PoC)

Do not start Phase 3 until both Phase 1 and Phase 2 are PASS (or mutually
acceptable INVESTIGATE). The gate is binary: pass or fall back.

## Output format

Each phase run emits a `verdict.json` colocated with its artifacts, following
the C0 report convention ([`docs/testing/reports/2026-04-20-1329-c0-fail/verdict.json`](../../docs/testing/reports/2026-04-20-1329-c0-fail/verdict.json)):

```json
{
  "result": "PASS" | "INVESTIGATE" | "KILL",
  "phase": 1 | 2,
  "stats": {
    "targets": 4,
    "positions_per_target": 10,
    "repeats": 3,
    "comparisons": 12,
    "max_abs_err": 0.0,
    "max_rel_err": 0.0,
    "mismatching_positions": 0
  },
  "thresholds": { "pass": 0.0, "investigate": 1e-06 },
  "config": { ... },
  "artifacts_dir": "scripts/v6_replay/results/phaseN-<timestamp>/"
}
```

## Directory layout

```
scripts/v6_replay/
├── README.md              # this file
├── requirements.txt       # torch, transformers, numpy, pytest
├── __init__.py            # package marker
├── _common.py             # configure_determinism, load_model_and_tokenizer
├── replay_types.py        # BatchStep, BatchLog, TaskLogits
├── worker_simulator.py    # WorkerSimulator.run_batch
├── replay_engine.py       # ReplayEngine.replay
├── test_phase1.py         # Phase 1a/1b/1c acceptance tests
├── test_phase2.py         # Phase 2 cross-hardware acceptance test
└── results/               # phase run outputs (gitignored, created at run time)
```

## What this PoC does not cover

- **Item 3 (batch-mode dispatch), Item 4 (settlement adjustments), Items
  5–9 (penalty mechanics), Item 11 (ChaCha20 100 %)** — all protocol-layer.
  Out of scope until Phase 3 gates.
- **BatchReceipt wire format.** The PoC uses Python dataclasses; a real
  chain-level `BatchReceipt` gets defined in Phase 3 once we know the log
  shape / size is actually tractable.
- **Log-forgery defence (review finding C1).** The PoC trusts `batch_log`
  inputs unconditionally — fine at research stage. Phase 3 must reject
  `batch_log` entries whose `task_id` does not resolve to a real on-chain
  `InferRequest`.
- **Adversarial-partner injection defence (review finding C2).** Same — out
  of scope for the PoC.
- **Verifier compute amplification (review finding B1).** The PoC records
  replay cost but does not explore fee-distribution changes.

See [`docs/protocol/FunAI_V6_BatchReplay_Design.md`](../../docs/protocol/FunAI_V6_BatchReplay_Design.md)
for the full design; this README only covers what Phase 0–2 verify.

## Running the PoC

Phase 0 only exists to gate the contract. There is nothing to run yet except
the failing test skeletons:

```bash
cd scripts/v6_replay
pip install -r requirements.txt
pytest -v test_phase1.py  # all fail with NotImplementedError — expected
```

Phase 1a implementation fills in `WorkerSimulator` and `ReplayEngine`
for argmax, fixed-batch generation. Acceptance:

```bash
# default: Qwen2.5-3B-Instruct on cuda (C0 baseline)
pytest -v test_phase1.py

# quick iteration on a tiny model — validates the determinism path
# without the 6 GB download:
V6_MODEL=Qwen/Qwen2.5-0.5B-Instruct pytest -v test_phase1.py

# CPU-only smoke test (very slow, but determinism is orthogonal to device):
V6_MODEL=Qwen/Qwen2.5-0.5B-Instruct V6_DEVICE=cpu pytest -v test_phase1.py
```

Phase 1b / 1c tests will land in this same file as the implementation
grows; each sub-phase gets its own marker so `pytest -m phase1a` can
target a specific stage.

Phase 2 acceptance run (requires 2 GPU machines, see acceptance criteria §
Phase 2 above):

```bash
# on machine A
python -m scripts.v6_replay.worker_simulator --emit-log phase2-run1.json
# transfer phase2-run1.json to machine B
# on machine B
pytest -v test_phase2.py --log=phase2-run1.json
```

## Related

- V6 design note: [`docs/protocol/FunAI_V6_BatchReplay_Design.md`](../../docs/protocol/FunAI_V6_BatchReplay_Design.md)
- C0 report (this PoC's motivation): [`docs/testing/reports/2026-04-20-1329-c0-fail/report.md`](../../docs/testing/reports/2026-04-20-1329-c0-fail/report.md)
- C0 test script (reference style for acceptance scripts): [`scripts/c0-logits-consistency.py`](../c0-logits-consistency.py)
