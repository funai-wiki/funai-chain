# C0 Concurrent-Batching Logits Consistency — Result Report

| | |
|---|---|
| **Date** | 2026-04-20 13:29 CST (05:29 UTC reference) |
| **Operator** | dmldevai |
| **Test** | `docs/testing/FunAI_TPS_Logits_Test_Plan_KT.md` §1.3 C0 |
| **Verdict** | **FAIL** |
| **Blocks** | C1 – C4 and all TPS-layer tests (per KT precondition clause) |

---

## 1. Executive summary

TGI 3.3.6 continuous batching perturbs per-position logits enough to flip sampled tokens
and cascade into an entirely different generation for the same prompt and seed, as soon
as other requests are batched alongside. The measured max relative logprob error at the
first generated position is **2.27 × 10⁻²**, which is **> 20× the FAIL threshold** defined
in KT §1.3 (`≥ 1 × 10⁻³`).

The current FunAI verification architecture's assumption — that a Verifier running
single-request teacher forcing can reproduce the Worker's logits at the 5 VRF-selected
positions — is falsified on this hardware + software stack. KT §1.3 is explicit that
no ε tolerance rescues this case:

> "If they do not match, this is a systemic bias that no tolerance can fix — the entire
> verification architecture must be revisited."

C0 is a precondition gate. All downstream tests are paused until a mitigation is
selected and re-validated.

---

## 2. Environment

| Component | Value |
|---|---|
| Cloud / Region | Alibaba Cloud 华东 1 (杭州), 可用区 K |
| Instance | `ecs.gn7i-c8g1.2xlarge` (spot, public IP `47.96.179.86`) |
| GPU | NVIDIA A10, 24 GB VRAM, driver 570.195.03, CUDA 12.8 |
| OS | Alibaba Cloud Linux 3.2104 LTS |
| Docker | 26.1.3 + nvidia-container-toolkit 1.17.8 |
| TGI image | `ghcr.io/huggingface/text-generation-inference:3.3.6` (re-tagged from `ghcr.m.daocloud.io/...` proxy) |
| Model | `Qwen/Qwen2.5-3B-Instruct` — see §2.1 for substitution note |
| Model SHA | `aa8e72537993ba99e69dfaafa59ed015b17504d1` |
| dtype | float16, `--num-shard 1` |
| TGI runtime config (default) | `prefix-caching=true`, `attention=flashinfer`, `max_batch_prefill_tokens=4096`, `max_client_batch_size=4`, `cuda_graphs=[1,2,4,8,16,32]` |
| `HF_HUB_ENABLE_HF_TRANSFER` | `0` (download-time only; does not affect inference) |
| `HF_ENDPOINT` | `https://hf-mirror.com` |
| Client | `scripts/c0-logits-consistency.py` (with TGI-3.x extractor fix and `--prompt` flag, this PR) |
| Caller | GCP VM `google-sg-dml-02` (Python 3.10.12) calling ECS port 8080 over public internet |

### 2.1 Model substitution — why 3B, not 8B

KT §1.3 specifies **`Qwen/Qwen2.5-8B-Instruct`** as the baseline. That exact repository
was not downloadable on this infrastructure at test time:

| Source | Result |
|---|---|
| `hf-mirror.com/api/models/Qwen/Qwen2.5-8B-Instruct` | HTTP 401 (mirror has restricted this repo) |
| `huggingface.co` direct (from Aliyun mainland) | TCP timeout (mainland egress) |
| `huggingface.co` direct (from GCP Singapore) | HTTP 401 (anonymous API now rate-limited/auth-gated for this repo) |
| `modelscope.cn` at both `qwen/...` and `Qwen/...` namespaces | 404 (no matching revision via path API) |

All other Qwen2.5 sizes (0.5B / 1.5B / 3B / 7B) and Qwen2 models return HTTP 200 from
hf-mirror. `Qwen2.5-3B-Instruct` was substituted as the nearest available proxy.

C0 measures TGI's batching behavior, which is a property of the **scheduler + attention
kernel + cache logic**, not of the model weights. The batching code path is identical
for 3B and 8B checkpoints. The conclusions below therefore carry over to the KT baseline;
however, §7 P1 lists a 8B confirmation run as follow-up work.

---

## 3. Tests

### 3.1 Sanity — single-vs-single determinism

**Goal.** Rule out TGI internal non-determinism before attributing drift to batching.

**Method.** Two back-to-back single-request `/generate` calls, identical parameters:
prompt `"Write a creative short sentence about the night sky:"`, `seed=42`,
`temperature=0.7`, `max_new_tokens=10`, `top_n_tokens=5`, `do_sample=true`.

**Result.**

| Check | Value |
|---|---|
| Generated text bit-identical | ✅ yes (` The stars twinkle like diamonds scattered across the velvet`) |
| Top-5 logprobs aligned, 10 positions × 5 candidates | max `|Δ|` = **0** |
| Top-5 id mismatches | **0** |

**Conclusion.** TGI 3.3.6 is fully deterministic at the single-request level on this
hardware. Any drift observed later is attributable to the only varied factor (batching),
not to intrinsic TGI noise.

### 3.2 C0 — single-request vs 4-concurrent-batch

**Method.** A single request carrying prompt A, then four concurrent `/generate` calls
(prompt A + three distinct distractors, each with its own seed) dispatched via
`ThreadPoolExecutor` so TGI packs them into the same micro-batch. Diff the target
response for prompt A across the two runs.

**Parameters.** `seed=42`, `temperature=0.7`, `max_new_tokens=10`, `top_n_tokens=5`,
`do_sample=true`.

**Prompt A.** `"Write a creative short sentence about the night sky:"`

**Distractors** (prompts D1-D3). Number-words query, primary-colors list,
largest-planet question — deliberately distinct lengths to avoid prefix-cache collapse.

**Generated text.**

| Run | Output |
|---|---|
| Single | `" The stars twinkle like diamonds scattered across the velvet"` |
| Batch (prompt A) | `" The inky blackness of the night sky was"` |

**Same prompt, same seed, same temperature — different trajectory.**

**Per-position diffs.**

| Position | sampled id (single) | sampled id (batch) | top-5 shared / union | max rel err (logprob) |
|---:|---:|---:|---:|---:|
| 0 | 279 | 279 | 5 / 5 | **2.27 × 10⁻²** |
| 1 | 9759 | 304 | 5 / 5 | 1.21 × 10⁻² |
| 2 | 4384 | 7891 | 0 / 10 | — (post-divergence) |
| 3 | 35144 | 3691 | 0 / 10 | — |
| 4 | 1075 | 2090 | 0 / 10 | — |
| 5 | 48051 | 315 | 0 / 10 | — |
| 6 | 36967 | 279 | 0 / 10 | — |
| 7 | 3941 | 3729 | 0 / 10 | — |
| 8 | 279 | 12884 | 0 / 10 | — |
| 9 | 71326 | 572 | 0 / 10 | — |

**Aggregate.**

| Metric | Observed | KT threshold |
|---|---|---|
| Max absolute logprob error | **2.54 × 10⁻²** | — |
| Max relative logprob error | **2.27 × 10⁻²** | FAIL ≥ 1 × 10⁻³ |
| Top-5 membership drift | 80 of 100 samples | PASS requires 0 |
| Sampled-token drift | 9 of 10 positions | PASS requires 0 |

**Verdict.** `FAIL`.

---

## 4. Interpretation

### 4.1 Cascade mechanism

1. **Position 0:** same sampled id (`279`). Top-5 sets identical. Logprob values differ by
   ~2.3 % relative. Under `temperature=0.7`, this difference does not yet flip the sample
   — the PRNG draw from `seed=42` happens to land on the same candidate.
2. **Position 1:** the position-0 logit shift propagates through the model state. The next
   PRNG draw now picks a different token (`9759` → `304`).
3. **Positions 2-9:** the two runs are now generating under different prefixes. All
   subsequent logits are computed on different inputs. "Divergence" at these positions is
   trivially inevitable and does not measure additional batching impact.

**The load-bearing measurement is position 0's `max_rel_err = 2.27 × 10⁻²`.** Everything
else is downstream cascade.

### 4.2 Why the current verification scheme breaks

The FunAI verification layer computes teacher-forcing logits at 5 VRF-selected positions
and checks 4/5 match within ε. This assumes the Worker's logits at those positions can
be reproduced by a single-request Verifier.

- Under batched inference (Worker normal path), logit errors of ~1-3 % appear at the
  very first output position.
- Over longer generations these errors compound — subsequent positions are computed on
  differently-sampled prefixes, so their logits differ structurally, not just numerically.
- The 4/5 match threshold at currently-coded ε is unreliable:
  - **False positive** at position 0 (top-5 still matches) — a lazy/cheating Worker
    could match only on the first position.
  - **Dramatic false negative** at position 2+ (shared top-5 = 0) — an honest Worker
    producing the correct batched output will fail the check because the Verifier's
    single-request trajectory has diverged.

### 4.3 Root-cause hypotheses (not yet isolated)

Any of the following is individually sufficient to explain the drift. All three are on
by default in TGI 3.3.6:

1. **Prefix caching.** KV-cache reuse depends on co-batched neighbors — different batch
   neighbours reshape the cache interaction for prompt A.
2. **FlashInfer fused attention.** A single kernel call processes the whole batch;
   floating-point accumulation order inside the fused kernel depends on batch layout
   (queries' lengths and positions).
3. **Continuous-batching scheduler.** With `max_batch_prefill_tokens=4096`, which
   requests are grouped into the same prefill pass depends on arrival order and prompt
   sizes.

Mitigation Option B below is robust against all three root causes.

---

## 5. Mitigation options (per KT §1.3 C0)

| Option | What it does | Assessment |
|---|---|---|
| **A** | Verifier runs teacher forcing inside a batch, mimicking the Worker path. | Infeasible. A Verifier validates one task at a time and is structurally single-request. Fabricating peer traffic opens a fresh attack surface. **Reject.** |
| **B** | Worker runs a **separate, single-request** forward pass on (prompt + completed output) to record logits at the 5 VRF-selected positions, attaches them to the receipt. Verifier's single-request teacher forcing then compares against those bit-exact logits. | Cost is one extra forward pass per task (~100-300 ms on A10 FP16, dwarfed by the primary generation). Robust against all three root-cause hypotheses. Keeps Worker's continuous-batching throughput intact for the actual generation. **Recommend.** |
| **C** | Start TGI with `--max-batch-prefill-tokens=1024` (or lower) to reduce batch granularity. | Indirect — still batches inside the limit. Shrinks, does not eliminate, the FP-order variance. Useful as a **diagnostic** (if drift is cut by 10× with a 4× smaller cap, prefill fusion is a dominant contributor) but not a principled mitigation. |

### 5.1 Recommended architectural change (Option B)

- **Worker path.** On receipt of `AssignTask`, generate as normal (continuous-batched
  for throughput). After generation completes, issue a **single-request forward pass**
  on `(prompt + completed_output)` with no sampling, capturing the 5 VRF-selected
  positions' logits. Attach those logits to the `InferReceipt`.
- **Verifier path.** Unchanged — still single-request teacher forcing. The comparison
  is now single-vs-single, which §3.1 shows to be bit-exact.
- **Performance impact.** One additional forward pass per completed task, logits-only.
  On A10 FP16 with an 8B model and a typical 200-token generation, this is
  roughly 100-300 ms of added latency and negligible throughput impact (the extra pass
  can be queued on the worker's GPU between tasks).
- **Protocol impact.** `InferReceipt` gains a `verification_logits` field (already
  reserved in design docs for this purpose — cross-check before changing the wire
  format).

---

## 6. What was not tested

| Dimension | Covered | Not yet |
|---|---|---|
| Model | Qwen2.5-3B-Instruct FP16 | Qwen2.5-8B FP16 (KT baseline), Qwen2.5-32B-Instruct-GPTQ-Int4 |
| GPU | A10 (Ampere, 24 GB) | 4090 (Ada Lovelace), A100 (Ampere, 80 GB), H100 |
| TGI startup options | default | `--max-batch-prefill-tokens=1024`, `=0`, prefix-caching off, `--disable-custom-kernels` |
| Prompt set | 1 generative prompt × 1 seed | KT spec calls for 30 short + 30 medium + 30 long × multiple seeds |
| Concurrency | 4 (equal to `max_client_batch_size`) | 1, 2, 8, 16, 32 |
| Attention backend | `flashinfer` (default) | `paged`, `eager` |

None of these gaps change the PASS/FAIL verdict — a single failing case with non-zero
drift is already sufficient per KT §1.3's pass criteria. They matter for root-causing and
for choosing the final mitigation.

---

## 7. Recommended next steps

### P0 — before any further testing

1. File a mainnet-readiness issue titled "C0 FAIL: TGI batching perturbs logits;
   verification scheme must change" and link this report.
2. Pause C1-C4 and all TPS-layer testing per KT §1.3's blocking clause.
3. Architectural decision: adopt Option B (recommended), with a short design note
   covering the `InferReceipt.verification_logits` field, Worker extra-pass scheduling,
   and Verifier comparison logic.

### P1 — diagnostic validation

1. Re-run C0 with TGI started with `--max-batch-prefill-tokens=1024` (Option C).
   If drift drops to < 1 × 10⁻³, prefill fusion is a dominant contributor; if it
   persists, Option B is structurally necessary.
2. Re-run C0 with prefix caching disabled (subject to TGI flag support in 3.3.6).
3. Re-run C0 once on Qwen2.5-8B (obtained via ModelScope or an authenticated HF
   download) to confirm the result carries over to the KT baseline.

### P2 — follow-ups

1. Extend the test matrix to KT's full spec (30 prompts × 3 lengths × multiple seeds,
   concurrency 1-32) to build an empirical drift distribution.
2. Cross-hardware run (4090, A100) to measure drift variability across silicon — may
   turn some of KT's "4/5 threshold tuning" numbers into a derived quantity rather
   than a tunable knob.
3. If Option B is adopted, add a targeted regression test: given a worker-side receipt,
   the verifier's re-computed logits should be bit-exact to the receipt's
   `verification_logits`.

---

## 8. Raw artifacts

Captured alongside this report:

```
docs/testing/reports/2026-04-20-1329-c0-fail/
├── report.md               this file
├── single_response.json    full TGI response for single-request run (8.1 KB)
├── batch_response.json     full TGI response for prompt A in the batched run (8.1 KB)
└── verdict.json            stats + thresholds + verdict summary (0.6 KB)
```

`verdict.json` key fields:

```json
{
  "result": "FAIL",
  "stats": {
    "positions": 10,
    "samples_compared": 10,
    "max_abs_err": 2.54e-02,
    "max_rel_err": 2.27e-02,
    "sampled_id_drift": 9,
    "top_membership_drift": 80
  },
  "thresholds": { "pass": 1e-06, "investigate": 1e-03 },
  "config": {
    "endpoint": "http://47.96.179.86:8080",
    "seed": 42, "temperature": 0.7,
    "max_new_tokens": 10, "concurrent_count": 4, "top_n": 5
  }
}
```

---

## 9. References

- Test Plan: [`docs/testing/FunAI_TPS_Logits_Test_Plan_KT.md`](../../FunAI_TPS_Logits_Test_Plan_KT.md) §1.3 C0
- Runner script: [`scripts/c0-logits-consistency.py`](../../../../scripts/c0-logits-consistency.py)
- TGI 3.3.6 release: <https://github.com/huggingface/text-generation-inference/releases/tag/v3.3.6>

---

*End of report.*
