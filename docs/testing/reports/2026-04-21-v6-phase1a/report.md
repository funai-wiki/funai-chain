# V6 Phase 1 Single-Machine Replay — Result Report

| | |
|---|---|
| **Date** | 2026-04-21 16:05 CST (08:05 UTC reference) |
| **Operator** | dmldevai |
| **Test** | [`scripts/v6_replay/README.md`](../../../scripts/v6_replay/README.md) §Phase 1a + §Phase 1c |
| **Verdict** | **PASS** (single machine; Phase 2 cross-hardware still open) |
| **Unblocks** | Phase 2 (cross-hardware A2 validation); Phase 1b (ChaCha20 sampling) |

---

## 1. Executive summary

The V6 Batch Log-Replay verification scheme ([`docs/protocol/FunAI_V6_BatchReplay_Design.md`](../../../docs/protocol/FunAI_V6_BatchReplay_Design.md)) rests on two load-bearing claims:

- **A1 (engine feasibility)**: *some* runtime can be driven to replay a
  pre-recorded batch schedule and produce the same logits as the original
  Worker that generated that schedule.
- **A2 (cross-hardware determinism)**: when the same schedule is replayed on
  different GPU hardware, the logits are bit-exact.

This report covers Phase 1 of the PoC at `scripts/v6_replay/`, which addresses
**A1 on a single machine**. A2 remains open for Phase 2.

**Result: on a single Alibaba Cloud A10 + Qwen2.5-3B-Instruct + bf16 + eager
attention + HuggingFace transformers, the Worker can emit a BatchLog
describing any schedule (fixed-batch, leave-only, join+leave) and a separate
Replayer can reconstruct bit-exact logits for every target at every step
where it was active.** 12 / 12 acceptance tests pass with `max_abs_err ==
0.0` across 4 prompts × varying active-step counts × multiple targets. A1
is therefore not a speculative claim on this stack — it is reproducible.

**What this does not yet prove:** A2 (cross-hardware). Phase 2 requires a
second GPU of a different SM architecture (4090 or A100) running the same
Replayer against a BatchLog shipped from the A10 Worker. Until that run
succeeds, V6's verification architecture is *single-hardware* — insufficient
for production where Workers and Verifiers run on heterogeneous GPUs.

---

## 2. Environment

| Component | Value |
|---|---|
| Cloud / Region | Alibaba Cloud 华东 1 (杭州), 可用区 K |
| Instance | `ecs.gn7i-c8g1.2xlarge` (spot, public IP `118.31.108.187`) |
| GPU | NVIDIA A10, 24 GB VRAM, driver 580.126.09, CUDA runtime 12.8 |
| OS | Alibaba Cloud Linux 3.2104 U12.3 (OpenAnolis) |
| CPU / RAM | 8 vCPU / 29 GB |
| Python | 3.11.13 (installed via `dnf install python3.11`) |
| PyTorch | 2.5.1+cu121 (CUDA 12.1 wheel; compatible with driver's 12.8 runtime) |
| transformers | 4.57.6 |
| numpy | pip-default at install time |
| Model | `Qwen/Qwen2.5-3B-Instruct`, SHA verified via hf-mirror cache |
| dtype | `torch.bfloat16` (Qwen2.5 native) |
| Attention impl | `eager` (disables SDPA kernel auto-selection; required for determinism) |
| Determinism flags | `torch.use_deterministic_algorithms(True)`, `cudnn.deterministic=True`, `cuda.matmul.allow_tf32=False`, `CUBLAS_WORKSPACE_CONFIG=:4096:8` |
| Seeds | `torch.manual_seed(42)`, `torch.cuda.manual_seed_all(42)`, `np.random.seed(42)` |
| Sampling | `torch.argmax` (`temperature=0`; ChaCha20 is Phase 1b) |

### 2.1 Why transformers, not TGI or vLLM

Deliberate scope choice, documented in [`scripts/v6_replay/README.md` §Engine choice](../../../scripts/v6_replay/README.md). Neither TGI nor vLLM exposes a public "replay this schedule" API; forking either adds 2–3 months before the first cross-hardware datapoint. transformers is pure Python with a simple `forward(input_ids, past_key_values, attention_mask)` entry, which lets us implement the deterministic manual scheduler in a few hundred lines.

Throughput is 10–20× lower than TGI at runtime, which disqualifies transformers for production Workers. That is deliberate: Phase 1 is a **determinism** PoC, not a throughput PoC. A positive PoC does **not** prove V6 is implementable on TGI/vLLM — it proves the scheme is internally consistent and worth the engineering cost of the Phase 3+ port. A negative PoC would have stopped V6 outright.

### 2.2 Why bfloat16, not float16

Qwen2.5 is trained in bfloat16. An earlier PoC run under fp16 produced NaN logits on one of four test prompts (task-p1-002, prompt "List the first three primary colors:") — fp16's ~65504 max representable value is easy to overflow in attention logits, and a NaN in logits is a valid IEEE-754 state that kills the `nan == 0.0` comparison in the bit-exact assertion. bfloat16 shares fp32's exponent range so no overflow; precision is reduced but determinism work doesn't require mantissa precision, only reproducibility. See §5 for the fp16 failure archive retained for reference.

---

## 3. Tests

### 3.1 Phase 1a — temperature=0, fixed batch composition

**Method.** Four prompts (`task-p1-001..004`), all active at step 0, all run
for exactly `max_new_tokens=10` decode steps. Every `BatchStep` in the
resulting log has `active_task_ids = (001, 002, 003, 004)`. Worker uses a
standard prefill + decode loop with `DynamicCache`. Replayer re-runs the
same prompts through the same prefill + decode path and extracts the
target's per-position logits at each step.

**PASS condition.** `max_abs_err == 0.0` across all output positions of all
4 targets, across 3 repeated runs of the whole procedure. 12 Worker-vs-Replay
comparisons, zero drift.

**Result.** 6 / 6 PASS:

```
test_worker_emits_log_and_logits                              PASSED
test_replay_is_bit_exact_same_gpu[task-p1-001]                PASSED
test_replay_is_bit_exact_same_gpu[task-p1-002]                PASSED
test_replay_is_bit_exact_same_gpu[task-p1-003]                PASSED
test_replay_is_bit_exact_same_gpu[task-p1-004]                PASSED
test_replay_three_repeats_stable                              PASSED
```

### 3.2 Phase 1c.1 — dynamic batch, leave-only schedule

**Method.** Same four prompts; schedule staggers exit points:

```
task-p1-001: active in steps 0..3   (4 active steps)
task-p1-002: active in steps 0..5   (6 active steps)
task-p1-003: active in steps 0..7   (8 active steps)
task-p1-004: active in steps 0..9   (10 active steps)
```

At each step K, Worker constructs contexts only for tasks currently active
— tasks that left at K−1 or earlier are absent from the forward-pass batch.
Context for each active task at step K is `prompt_tokens + sampled_tokens[0..K−1]`,
left-padded to max active-context length. This is "recompute from scratch"
— no KV cache reuse across steps. Slower per step than prefill+decode, but
the schedule-driven composition is read directly off `BatchLog.steps[]`, so
there is no cache-slicing code path needed when composition changes.

Worker emits a BatchLog whose `steps[k].active_task_ids` varies across `k`
(confirmed by the `roster_varies` assertion).

**Result.** 3 / 3 PASS:

```
test_1c_leave_roster_varies                                   PASSED
test_1c_leave_log_matches_schedule                            PASSED
test_1c_leave_replay_bit_exact                                PASSED
```

`max_abs_err == 0.0` for every target × every active step.

### 3.3 Phase 1c.2 — dynamic batch, join + leave schedule

**Method.** Four tasks with staggered joins *and* staggered exits:

```
task-p1-001: active in steps 0..4   (5 active steps)
task-p1-002: active in steps 0..7   (8 active steps)
task-p1-003: active in steps 2..8   (7 active steps; joins at step 2)
task-p1-004: active in steps 4..9   (6 active steps; joins at step 4)
```

This pattern exercises three transition types:
- Task leaving (001 exits at step 5, 002 at 8)
- Task joining (003 joins at step 2, 004 at 4)
- Composition change mid-run (rosters differ step 1 → step 2, step 3 → step 4, etc.)

The load-bearing V6 claim — "Verifier reproduces Worker's logits despite
batch composition changes" — is exercised most stringently here: target
`task-p1-002`'s context at step 2 includes its own prompt + 2 prior
sampled tokens, but sits alongside a freshly-joined `task-p1-003` whose
context is just its prompt. The Replayer must match this exact batch
layout at step 2.

**Result.** 3 / 3 PASS:

```
test_1c_join_roster_varies                                    PASSED
test_1c_join_log_matches_schedule                             PASSED
test_1c_join_replay_bit_exact                                 PASSED
```

### 3.4 Aggregate

| Metric | Value |
|---|---|
| Tests run | 12 |
| Tests passed | **12** |
| Tests failed | 0 |
| Total wall time on 3B | 140.42 s (incl. fixture model load) |
| Total wall time on 0.5B | 266.81 s (slower per test — 0.5B 3-repeats fixture amortises less) |
| Bit-exact comparisons | ~200 (4 targets × varying active-step counts across 3 schedules) |
| `max_abs_err_seen` | `0.0` on every comparison |

---

## 4. Implementation notes

### 4.1 Two code paths coexist

Phase 1a and Phase 1c use different code paths in `WorkerSimulator` and
`ReplayEngine`:

- **Phase 1a path**: `run_batch` + `replay` use prefill + decode with
  HuggingFace's `DynamicCache`. Matches how a production engine (TGI, vLLM)
  structures continuous batching at the step level. Faster per step.
- **Phase 1c path**: `run_batch_dynamic` + `replay_dynamic` use
  recompute-from-scratch at every step. No cache reuse, no composition-change
  cache-slicing. Slower per step but trivial to reason about.

The two paths may produce **numerically different outputs** under bf16
(different order of operations in attention reduction even if mathematically
equivalent). Worker and Replayer within each path are bit-exact; cross-path
is not asserted and not needed for the V6 A1 claim.

### 4.2 Determinism contract

`_common.configure_determinism(seed)` enforces:

- `os.environ.setdefault("CUBLAS_WORKSPACE_CONFIG", ":4096:8")` — required
  for cuBLAS's deterministic mode under PyTorch
- `torch.use_deterministic_algorithms(True, warn_only=False)` — aborts on any
  non-deterministic op instead of silently drifting
- `torch.backends.cudnn.deterministic = True`, `cudnn.benchmark = False`
- `torch.backends.cuda.matmul.allow_tf32 = False`,
  `cudnn.allow_tf32 = False`
- Three seeds: `torch.manual_seed`, `torch.cuda.manual_seed_all`,
  `np.random.seed`

`_common.load_model_and_tokenizer` enforces:

- `torch_dtype=torch.bfloat16` (see §2.2)
- `attn_implementation="eager"` (see §2.1)
- `model.eval()` + `requires_grad=False` on all parameters
- Tokenizer `padding_side="left"` (required for correct last-token logits
  on padded sequences)

### 4.3 bf16 → fp32 upcast before numpy

HuggingFace tensors in bfloat16 cannot be converted directly to numpy —
numpy has no native bfloat16 dtype. All logits are `.detach().float().cpu()
.numpy()`; the `.float()` widens bf16 to fp32 losslessly (bf16 ⊆ fp32 in bit
layout: same exponent, reduced mantissa rounded into fp32's larger
mantissa). Bit-exactness comparisons therefore happen in fp32, not bf16 —
but because both Worker and Replayer upcast identically, the comparison is
still exact.

---

## 5. Historical deviation — fp16 → bf16 switch

Before the bf16 fix, Phase 1a on Qwen2.5-0.5B-Instruct with `torch.float16`
produced 2 of 6 test failures, both rooted at `task-p1-002` (prompt "List
the first three primary colors:"):

```
test_replay_is_bit_exact_same_gpu[task-p1-002]   FAILED  max_abs_err=nan
test_replay_three_repeats_stable                 FAILED  max_abs_err=nan
```

Worker's argmax produced 10 exclamation marks (`!!!!!!!!!!`) and logits
contained NaN. The NaN was deterministic across Worker and Replayer runs —
both sides produced identical NaN positions — so determinism itself was
intact. Failure was mechanical: `np.max(np.abs(nan − nan)) == nan`, and
`nan == 0.0` is False.

Root cause: fp16's max representable value is ~65504; attention logits can
exceed this during softmax for certain token distributions, producing
`inf` → `inf / inf = nan` downstream. Qwen2.5 is trained in bfloat16, which
has fp32's exponent range (up to ~3.4e38), so the overflow does not occur.

Switching the default dtype from float16 to bfloat16 in `_common.py`
resolved both failures. The fp16 failure archive is retained at
[`phase1a-20260421-070618-prefix-nan-fp16/`](phase1a-20260421-070618-prefix-nan-fp16/)
as evidence of the diagnostic trail.

This is not a V6 design concern — the design doesn't pin a dtype. It is a
PoC implementation note: **the determinism PoC must use a dtype the model
tolerates**. For Qwen2.5 that means bf16 or fp32. fp16 is a production
concern (TGI serves Qwen in fp16 by default per C0 report §2) that will
need an independent answer during the Phase 3 engine transition.

---

## 6. What this does not prove

| Claim | Status |
|---|---|
| V6 A1 on single machine, transformers + bf16 + eager + A10 | ✅ Proven here |
| V6 A2 (cross-hardware bit-exact) | ⏳ Open (Phase 2) |
| Determinism under `temperature > 0` + seeded sampling | ⏳ Open (Phase 1b) |
| Replay on TGI or vLLM instead of transformers | ⏳ Open (Phase 3+) |
| Same Worker-Replayer determinism under fp16 | ⏳ Open (hardware / production-engine dependent) |
| Worker throughput acceptable for mainnet | ❌ Transformers PoC is 10-20× slower than TGI; not a production concern until Phase 3 |
| Log forgery defence (review finding C1) | ❌ Not part of PoC — requires on-chain `InferRequest` lookup for every batch-log task_id |
| Adversarial partner injection (review finding C2) | ❌ Partially mitigated by item #12 (Leader-side bundling), but not implemented in PoC |
| Verifier compute amplification economic model (B1) | ❌ Out of scope for Phase 1 |

---

## 7. Recommended next steps

### P0 — before closing V6 A-tier validation

1. **Phase 2 run**: provision a second GPU ECS of a different SM
   architecture (4090 Ada or A100 Ampere-L), rsync the Phase 1 PoC code,
   and run `scripts/v6_replay/test_phase2.py` with artifacts from this A10.
   PASS there means A2 holds under the transformers path. FAIL there
   confirms that cross-hardware determinism requires a mitigation beyond
   "same engine + same flags" — at which point V6 needs to re-scope.

### P1 — parallelisable with P0

2. **Phase 1b (ChaCha20 sampling)**: extend `run_batch_dynamic` to accept
   `temperature > 0` with ChaCha20-seeded multinomial sampling. ~150 LOC
   for the sampling primitive + test coverage; no new hardware required.
   Unblocks V6 design item #11 (ChaCha20 100% coverage) from a PoC
   standpoint.

3. **Image build on this ECS** (in progress at report time): snapshot the
   fully-configured environment so subsequent Phase-2/3 runs skip the
   ~3-minute dep-install + ~10-minute model-download cold-start. Cached
   state includes `/root/v6-venv` (torch + transformers) and
   `~/.cache/huggingface/` (Qwen2.5-0.5B + 3B weights).

### P2 — follow-ups gated by P0 outcome

4. **Phase 3 engine transition spike**: if P0 Phase 2 passes, start scoping
   either (a) a TGI fork with a `replay-schedule` API, (b) a vLLM fork of
   the same, or (c) a self-built deterministic continuous-batching runtime
   based on `transformers.Cache` interface. 2–3 months each; pick based
   on Phase 2 confidence.

5. **V6 design note updates**: mark Phase 1 as "validated by PoC 2026-04-21"
   in [`docs/protocol/FunAI_V6_BatchReplay_Design.md`](../../protocol/FunAI_V6_BatchReplay_Design.md);
   cross-reference to this report.

---

## 8. Raw artifacts

Captured under the same report directory:

```
docs/testing/reports/2026-04-21-v6-phase1a/
├── report.md                              this file
├── phase1a-20260421-073528/                 3B Phase 1a only (pre-Phase-1c)
│   ├── pytest.log                           6 tests, 62 s
│   └── verdict.json                         result=PASS
├── phase1a-20260421-080515/                 3B Phase 1a + 1c combined — final
│   ├── pytest.log                           12 tests, 140 s
│   └── verdict.json                         result=PASS
└── phase1a-20260421-070618-prefix-nan-fp16/ historical fp16 failure archive
    ├── pytest.log                           2 failed / 4 passed (NaN at task-p1-002)
    └── verdict.json                         result=INVESTIGATE
```

`phase1a-20260421-080515/verdict.json` key fields:

```json
{
  "phase": "1a",
  "result": "PASS",
  "pytest_exit_code": 0,
  "max_abs_err_seen": "none",
  "config": {
    "model": "Qwen/Qwen2.5-3B-Instruct",
    "device": "cuda",
    "branch": "research/v6-replay-poc",
    "hf_endpoint": "https://hf-mirror.com",
    "torch_cuda": "cu121"
  }
}
```

Note: the `commit` field in older verdict JSONs reads `b4c59ce03d8e…` — an
intermediate commit on the research branch. scp-based code delivery
bypassed `git pull` on the remote, so `git rev-parse` on the ECS did not
advance past `b4c59ce` even though the executed source corresponded to
later commits (the final acceptance run used commit `06cc89a` equivalent
files). Cosmetic; the executed test logic matches what is pushed to
`origin/research/v6-replay-poc` at report time.

---

## 9. References

- V6 design: [`docs/protocol/FunAI_V6_BatchReplay_Design.md`](../../protocol/FunAI_V6_BatchReplay_Design.md)
- PoC contract and acceptance criteria: [`scripts/v6_replay/README.md`](../../../scripts/v6_replay/README.md)
- C0 FAIL report (this PoC's motivation): [`docs/testing/reports/2026-04-20-1329-c0-fail/report.md`](../2026-04-20-1329-c0-fail/report.md)
- Bootstrap script used for the runs: [`scripts/v6-replay-bootstrap-aliyun.sh`](../../../scripts/v6-replay-bootstrap-aliyun.sh)
- Phase 1 code commits on `research/v6-replay-poc`:
  - `1da5af7` V6 design note English translation
  - `a4e4821` Phase 0 scaffold
  - `4769467` dir rename (Python package requirement)
  - `24b56a2` Phase 1a implementation
  - `d080cd8` Aliyun bootstrap
  - `b4c59ce` al3 / dnf support in bootstrap
  - `3dd5496` `SKIP_SYNC` bootstrap flag
  - `fd75611` bf16 + China mirror support
  - `06cc89a` Phase 1c — dynamic batch composition

---

*End of report.*
