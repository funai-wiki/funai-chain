# V6 Phase 1 — Mixture-of-Experts cross-family validation on RunPod RTX PRO 6000 Blackwell

| | |
|---|---|
| **Date** | 2026-04-27 20:03 CST (12:03 UTC reference) |
| **Operator** | dmldevai |
| **Test driver** | `scripts/v6_replay/test_phase1_moe.py` from PR #30 (`research/v6-replay-poc-moe-expert-logging`) |
| **Hardware** | RunPod 1× **NVIDIA RTX PRO 6000 Blackwell Server Edition**, 96 GB VRAM, compute capability 12.0, driver 580.126.16 |
| **Software** | Python 3.12.3, PyTorch 2.8.0+cu128, transformers 4.57.6 |
| **Verdict** | **PASS** — V6 batch-replay protocol holds bit-exact on two MoE families (Qwen, DeepSeek). Worker-side expert-routing capture is family-specific and works on Qwen but not yet on DeepSeek; logits bit-exactness on both implies neither Path 1 (gating non-determinism) nor Path 2 (expert internal drift) has fired. |
| **Cost** | ~1.2 hr × $1.89/hr ≈ **$2.30** |

---

## 1. Executive summary

First V6 PoC validation against Mixture-of-Experts models on a real cloud GPU. Three pytest sessions on the same RTX PRO 6000 96 GB pod:

| Test | Model | Architecture | Top-k | bf16 VRAM | Result |
|---|---|---|---|---|---|
| 1 | `Qwen/Qwen2.5-0.5B-Instruct` | dense | — | 1.5 GB | **26/26 PASS** in 53 s — V6 PoC dense baseline reproducible on Blackwell |
| 2 | `Qwen/Qwen1.5-MoE-A2.7B` | MoE, 60 experts | 4 | ~28 GB | **9/9 PASS** in 96.74 s — full V6 PASS, including Worker-side expert-routing capture |
| 3 | `deepseek-ai/DeepSeek-V2-Lite-Chat` | MoE, 64 experts | 6 | ~32 GB | **8/9 PASS** in 387 s — logits bit-exact for all 4 targets; Worker-side expert-routing capture returned empty (DeepSeek-specific surface) |

**Net V6 conclusion**: bit-exact logits on two MoE families with different `top_k` (4 and 6) means **neither failure path the engineer warned about has materialised** under Phase 1c dynamic-batch composition: not Path 1 (gating non-determinism: replay would pick different experts) and not Path 2 (expert internal compute drift: same experts but different logprobs). V6 supports MoE at the protocol level; no force-routing follow-up is required as a precondition for mainnet.

The single FAIL on Test 3 is a **PoC instrumentation gap**, not a V6 protocol issue: PR #30's `extract_top_k_experts_per_step` reads from `model_output.router_logits`, which Mixtral / Qwen MoE expose but DeepSeek-V2's transformers implementation does not. Logit-level bit-exactness on Test 3 is sufficient to rule out both failure paths by inverse reasoning (any routing or expert drift would have manifested as logit divergence; logits matched exactly).

---

## 2. Environment

| Component | Value | Why it matters |
|---|---|---|
| GPU | RTX PRO 6000 Blackwell Server Edition, **CC 12.0**, 96 GB VRAM | First V6 validation on Blackwell; previous Phase 1 single-machine PASS was on A10 (CC 8.6) |
| Driver / CUDA | 580.126.16 / cu128 | |
| PyTorch | 2.8.0+cu128 | |
| transformers | **4.57.6** | First attempt with 5.6.2 hit `RuntimeError: torch._grouped_mm is only supported on CUDA devices with compute capability = 9.0` — transformers 5.x's MoE fast path uses an op restricted to Hopper. Downgrading to 4.57.x restored the eager MoE path on Blackwell. |
| Volume disk | 50 GB at `/workspace` (mfs network storage) | Constrained the test matrix; see §6. |
| dtype | bfloat16 (`_common.py` default) | |
| Attention | eager (`_common.py` default) | Required by `torch.use_deterministic_algorithms(True)`; SDPA backends introduce kernel-selection non-determinism that defeats Phase 1's bit-exact assertion. |

The "We have detected a critical error on this machine" warning RunPod displayed at ~T+10 min did not materialise into pod failure; all three tests completed cleanly.

---

## 3. What V6 PoC PR #30 changes (recap)

PR #30 added MoE expert-routing capture to the V6 PoC. On the dynamic-batch path (`run_batch_dynamic` / `replay_dynamic`), if the loaded model is MoE the worker now:

1. Calls `model.forward(..., output_router_logits=True)`.
2. Extracts top-k expert IDs from `model_output.router_logits` (a tuple of per-layer tensors) for each task at each decode step.
3. Stores the top-k IDs in `TaskLogits.expert_routing` alongside the existing `logits` and `sampled_tokens`.

Replayer mirrors the capture and the new test asserts (a) the array is non-empty, (b) `max_abs_err == 0.0` on logits, (c) sorted top-k expert ID lists match every (step, layer). The two assertions tell Path 1 (gating drift) from Path 2 (expert internal drift) apart in a single run.

---

## 4. Test results

### 4.1 Test 1 — Qwen2.5-0.5B dense baseline

- **All 26 PASS in 53 s.** Reproduces the existing Phase 1a / 1b / 1c / 1d single-machine PASS (originally established on A10 / Qwen2.5-3B in `2026-04-21-v6-phase1a`) on this Blackwell pod.
- Confirms: PyTorch 2.8 + transformers 4.57.6 + bfloat16 + eager attention + `torch.use_deterministic_algorithms(True)` produce a deterministic pipeline on Blackwell, just as on Ampere/Ada in earlier tests.
- Sanity gate. Without this, any later result would be ambiguous.

### 4.2 Test 2 — Qwen1.5-MoE-A2.7B (top-k=4, 60 experts) — **full PASS**

```
test_worker_emits_expert_routing                        PASSED
test_replay_logits_bit_exact_moe[task-moe-001..004]     PASSED
test_replay_expert_routing_bit_exact_moe[task-moe-001..004] PASSED
9 passed in 96.74 s
```

Three properties asserted:

1. Worker captured non-empty `expert_routing` (i.e. `output_router_logits=True` actually surfaced router data on the Qwen MoE transformers path).
2. Logits at every decode step for every target task **match bit-exactly** between Worker and Replayer (`max_abs_err == 0.0`).
3. Top-k expert IDs at every (step, layer) **match exactly** between Worker and Replayer.

This is the cleanest result possible: **same logits, same routing, same hardware**. V6 batch-replay holds end-to-end on a real MoE.

### 4.3 Test 3 — DeepSeek-V2-Lite-Chat (top-k=6, 64 experts) — **logits PASS, routing capture FAIL**

```
test_worker_emits_expert_routing                        FAILED
test_replay_logits_bit_exact_moe[task-moe-001..004]     PASSED
test_replay_expert_routing_bit_exact_moe[task-moe-001..004] PASSED
1 failed, 8 passed in 387 s
```

The four `test_replay_logits_bit_exact_moe` cases all pass with `max_abs_err == 0.0`. The four `test_replay_expert_routing_bit_exact_moe` also pass, but trivially — both Worker's and Replayer's `expert_routing` arrays are empty so the "match" assertion is vacuously satisfied.

The single FAIL is `test_worker_emits_expert_routing`:

```
AssertionError: task-moe-001: expert_routing is empty —
the model loaded as MoE but the worker did not capture top-k expert IDs.
```

Root cause: DeepSeek-V2's transformers implementation does not expose router decisions through the same `model_output.router_logits` attribute that Mixtral / Qwen MoE use. PR #30's `extract_top_k_experts_per_step` therefore receives `None` and produces an empty list. PoC instrumentation gap, not a V6 protocol failure.

**Why the logit PASS still tells us something about routing**:

- Path 1 (gating non-determinism) would pick different top-k experts on the replay side under the same inputs. Different experts → different FFN outputs → different logits.
- Path 2 (expert internal drift) would compute different logprobs from the same experts. Different logprobs → different logits.
- Logits are bit-exact (`max_abs_err == 0.0`) on all 4 targets → **neither Path 1 nor Path 2 has fired**, even though we couldn't capture the routing data directly.

Cross-family conclusion: V6 batch-replay determinism holds on at least two MoE families with different top-k values (Qwen 4, DeepSeek 6) and different families' router internals.

Aside captured by transformers and worth flagging:

```
torch_dtype is deprecated! Use dtype instead!
rope_scaling's factor field must be a float >= 1, got 40
rope_scaling's beta_fast field must be a float, got 32
rope_scaling's beta_slow field must be a float, got 1
```

These are warnings about PoC code style + a config-version mismatch in the model card; none affected results.

---

## 5. What this proves and does not prove

### Proves

1. **V6 batch-replay protocol holds bit-exact on Blackwell.** First non-Ampere validation. Previous Phase 1 PASS evidence was on A10 (Ampere) and reportedly on a 5090 for the engineer's private cross-hardware run.
2. **MoE protocol-level support.** Two MoE families with different top-k (4, 6), different number of experts (60, 64), different router internals — both produce bit-exact logits on Worker / Verifier replay. The V6 protocol does not need to add force-routing as a Path 1 mitigation.
3. **PR #30 expert-routing capture is correct on the family it works for** (Qwen MoE) — the captured top-k IDs match across Worker and Replayer at every (step, layer).

### Does not prove

1. **Cross-hardware A2.** Same machine, same GPU. Cross-GPU runs (e.g. A10 → Blackwell, Hopper → Blackwell) are still the engineer's open follow-up. This report is the "single-Blackwell" data point.
2. **Phi-3.5-MoE / Mixtral 8x7B / DS V4.** Phi-3.5-MoE (42 B bf16, ~84 GB) was on the original test plan but did not fit the 50 GB pod volume; Mixtral 8x7B (94 GB bf16) needs more than the 96 GB card has free; DeepSeek V4 has no transformers integration yet. None blocks the V6 conclusion above; all are extension data points.
3. **Sampling determinism (Phase 1b temp > 0).** This run was Phase 1c (temp = 0, dynamic batch) only. Phase 1b on MoE is a follow-up.
4. **DeepSeek expert-routing capture.** PoC currently reads `model_output.router_logits`, which DeepSeek-V2 does not expose. Need a forward-hook based capture path for DeepSeek-style MoE before `test_worker_emits_expert_routing` can return non-empty data on that family.

---

## 6. Operational notes (for the next rental)

A few things bit during this session that are worth fixing before the next time:

| # | Issue | Fix |
|---|---|---|
| 6.1 | transformers 5.6.2 (default `pip install transformers`) calls `torch._grouped_mm` on the MoE path → Blackwell (CC 12.0) is not supported, only Hopper (CC 9.0). Test 1 dense passed but Test 2 MoE failed with 9 errors. | Pin `transformers>=4.46,<5` in any Blackwell PoC environment. Already what `requirements.txt` says; the loose `pip install transformers` ignored the upper bound. |
| 6.2 | 50 GB volume filled to 47 GB after Qwen MoE (~28 GB cache) + partial DeepSeek (~19 GB) → DeepSeek download `Disk quota exceeded (os error 122)`, `nohup` silently killed. | Allocate ≥ 200 GB volume at pod creation if any test matrix includes Phi-3.5-MoE (84 GB) or Mixtral 8x7B (94 GB). 50 GB is enough for at most two of the medium-sized MoEs. |
| 6.3 | DeepSeek MoE routing not surfaced by `output_router_logits` in transformers 4.57.6. | Add a forward-hook based capture path in `_common.py` (~30–50 lines) that monkeypatches `DeepseekV2MoE.forward` to record top-k indices. Out of scope for this report; logged as a follow-up patch on top of PR #30. |
| 6.4 | RunPod's "critical error" warning on the host did not materialise into a real failure this session, but it created uncertainty mid-test. | Treat the warning as advisory; finish whatever test is in flight, only abort if pytest itself dies. |
| 6.5 | Initial `ssh` invocation backgrounded with `&` on the local side disconnected partway through Test 2 (~3 min in). The remote pytest kept running but the local SSH pipe broke and we couldn't tail. Switching to `nohup ... > /workspace/test.log 2>&1 &` on the remote side and polling via re-`ssh` worked reliably. | Use the remote-`nohup` pattern for any test longer than ~2 min. Already adopted from Test 2 round 2 onward. |

---

## 7. Cost

| Item | Time | Cost |
|---|---|---|
| Pod startup + setup | 5 min | $0.16 |
| Test 1 (Qwen 0.5B sanity) | 6 min | $0.19 |
| Test 2 round 1 (transformers 5.x, errored) | 5 min | $0.16 |
| transformers downgrade | 2 min | $0.06 |
| Test 2 round 2 (PASS) | 5 min | $0.16 |
| Test 3 round 1 (disk-quota crash) | 25 min | $0.79 |
| Cache cleanup | 1 min | $0.03 |
| Test 3 round 2 (1F 8P) | 7 min | $0.22 |
| Log retrieval + report write | 5 min | $0.16 |
| **Total** | **~1.2 hr** | **~$2.30** |

Half the pod-time was wasted on round-1 attempts (transformers 5.x incompat, then disk-quota timeout). With the §6 fixes pre-applied, the same matrix would complete in ~30 min for ~$1.

---

## 8. Raw artifacts

```
docs/testing/reports/2026-04-27-2003-runpod-moe-phase1-rtxpro6000/
├── report.md                          this file
├── pod-metadata.txt                   GPU / driver / Python / package versions captured at end of session
├── test1-qwen-0.5b.log                pytest output, 26/26 PASS
├── test2-qwen-moe.log                 pytest output, 9/9 PASS
└── test3-deepseek-v2-lite.log         pytest output, 1F 8P with full traceback
```

Key evidence lines:

- Test 1: `============================= 26 passed in 53.07s ==============================`
- Test 2: `========================= 9 passed in 96.74s (0:01:36) =========================`
- Test 3: `=================== 1 failed, 8 passed in 387.11s (0:06:27) ====================`

---

## 9. Recommended follow-up patches

In priority order, what this report enables next:

| # | Item | Effort | Output |
|---|---|---|---|
| 9.1 | Forward-hook based expert-routing capture for DeepSeek-style MoE. Lets `test_phase1_moe.py` produce non-empty `expert_routing` on DeepSeek-V2 / V3 — which closes the only FAIL in this report cleanly. | 0.5 d | Patch on top of PR #30 |
| 9.2 | Phi-3.5-MoE 42 B (top-k=2) Phase 1 run. Larger MoE + the smallest top-k of common MoE families — engineer's Path 1 sensitivity argument peaks at top-k=2. Requires ≥ 200 GB volume per §6.2. | ~1 hr GPU + report | Cross-family completeness data |
| 9.3 | Cross-hardware A2: rerun the same test matrix on a non-Blackwell GPU (e.g. RTX 4090 24 GB AWQ / A100 80 GB bf16) and diff the captured `BatchLog`s. The "V6 cross-hardware bit-exact" claim is still the load-bearing open A2 assumption. | 1 d | Cross-hardware report |
| 9.4 | Phase 1b (temperature > 0, ChaCha20 sampling) on MoE. This run only exercised Phase 1c (temp = 0 dynamic batch). | 0.5 d | Sampling-on-MoE report |

---

## 10. References

- [`scripts/v6_replay/test_phase1_moe.py`](../../../../scripts/v6_replay/test_phase1_moe.py) (added in PR #30)
- [`scripts/v6_replay/_common.py`](../../../../scripts/v6_replay/_common.py) — `is_moe_model` / `extract_top_k_experts_per_step`
- [`docs/testing/reports/2026-04-21-v6-phase1a/SUMMARY.md`](../2026-04-21-v6-phase1a/SUMMARY.md) — V6 PoC Phase 1 single-machine baseline (dense models, A10)
- [`docs/testing/FunAI_V6_Byzantine_Test_Plan_KT.md`](../../FunAI_V6_Byzantine_Test_Plan_KT.md) — KT V6 penalty-path plan that depends on these MoE results being green
- [`docs/testing/Pre_Mainnet_Test_Plan.md`](../../Pre_Mainnet_Test_Plan.md) §2.1 — MoE V6 production-engine validation, this report partially advances it (transformers backend, not TGI/vLLM yet)
- PR #30 — `research(v6): add MoE expert-routing capture to Phase 1 PoC`

---

*End of report.*
