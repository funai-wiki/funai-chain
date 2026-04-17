# FunAI TPS Stress + Logits Consistency Test Plan

> Date: 2026-04-03
> Baseline: commit `ce87883`
> Total budget: ~$40
> Doc suffix: KT

---

## ⚠️ Blocking Risk (must run first)

**Concurrent batching affects logits (test C0).** TGI's continuous batching may execute computations in a different order when multiple requests run in parallel. Logits produced by a Worker under concurrent batching may not match logits produced by a Verifier doing single-request teacher forcing. If they do not match, this is a systemic bias that no tolerance can fix — **the entire verification architecture must be revisited.**

```
C0 is a precondition for every other test. If C0 fails, stop — do not run the rest.
Run it on your 5090 — 10 minutes, free.
```

---

## 1. Logits Consistency Tests

### 1.1 Preconditions (must be locked down first, otherwise testing is pointless)

```
1. Lock the inference engine: TGI
   → The protocol requires every Worker/Verifier to run TGI.
   → vLLM, llama.cpp, and TensorRT-LLM are NOT supported.
   → Different engines use different Flash Attention implementations and
     different floating-point accumulation orders, so logits will always diverge.

2. Lock the Docker image:
   ghcr.io/huggingface/text-generation-inference:3.3.6
   → CUDA version, PyTorch version, and Flash Attention version all pinned.
   → Miners just pull this one image; no need to configure an environment.

3. Lock the model file:
   HuggingFace `model.safetensors` has a SHA256 hash.
   → The hash is recorded when `model_id` is registered on-chain.
   → New Workers joining the network verify against the recorded hash,
     guaranteeing that every node is running the same model weights.
```

### 1.2 Test Models

| Model | Params | Purpose | Fits on 4090 | Fits on A100 |
|-------|--------|---------|--------------|--------------|
| Qwen2.5-8B-Instruct | 8B | FP16 baseline | ✅ (~16 GB VRAM) | ✅ |
| Qwen2.5-32B-Instruct-GPTQ-Int4 | 32B INT4 | Quantized baseline | ✅ (~18 GB VRAM) | ✅ |

Only these two. We are testing "same-model logits consistency across hardware", not "all models".

### 1.3 Test Matrix

#### C0: Concurrent batching impact (⚠️ blocking — run first)

```
Hardware: your 5090 (free)
Model: Qwen2.5-8B-Instruct FP16
TGI image: ghcr.io/huggingface/text-generation-inference:3.3.6

Steps:
  1. Start TGI with its default continuous-batching configuration.
  2. Send 1 request (prompt A, seed=42, temp=0.7) and record the logits
     at the 5 sampled positions → logits_single.
  3. Send 4 requests concurrently (prompts A/B/C/D, each with its own seed).
     Record the logits for prompt A → logits_batch.
  4. Diff logits_single against logits_batch.

Pass criteria:
  Identical (relative error < 1e-6)   → PASS; continue with the rest.
  Non-identical but < 1e-3            → INVESTIGATE; the Verifier may need to
                                        force single-request mode.
  > 1e-3                              → FAIL; verification architecture must change.

If it FAILS, candidate mitigations:
  Option A: Run Verifier teacher-forcing inside a batch (match Worker's path)
            → But a Verifier only validates one task; it IS single-request by nature.
  Option B: When the Worker computes the 5 sampled positions, use a single
            forward pass (no batching) for just those positions.
            → Small performance impact, only 5 positions.
  Option C: Add --max-batch-prefill-tokens limits to TGI startup.
            → May solve it indirectly.
```

#### C1: Same-hardware determinism (must be bit-exact)

```
Hardware: 2 × RTX 4090 (rented on Vast.ai)
Model: Qwen2.5-8B-Instruct FP16
TGI image: identical on both boxes.

Steps:
  Run the same 100-prompt set on each 4090 (fixed seed=42, temp=0.7).
  Diff the 5-position logits for every prompt.

Pass criteria: 100% bit-exact (relative error = 0).
  Any divergence → TGI or CUDA non-determinism; enable
                    CUBLAS_WORKSPACE_CONFIG=:4096:8 and retry.
```

#### C2: Cross-hardware tolerance (measure the drift)

```
Hardware: 1 × 4090 + 1 × A100
Model: Qwen2.5-8B-Instruct FP16
TGI image: identical on both boxes.

Prompt sets (three lengths):
  Short   prompts: 20 tokens    ("What is 2+2?") × 30 variants
  Medium  prompts: 200 tokens                     × 30 variants
  Long    prompts: 2000 tokens                    × 30 variants

For each prompt, record the 5-position logits. For each pair (4090, A100):
  - Max absolute error
  - Mean relative error
  - 99th-percentile relative error

Output: a "relative error vs prompt length" curve.
  → Set the logits_match_threshold parameter from this curve.
  → Current code requires 4/5 positions to match; if cross-hardware
    can only manage 3/5, the threshold must be lowered.
```

#### C3: Quantization isolation confirmation

```
Hardware: 1 × 4090
Model A: Qwen2.5-8B-Instruct FP16
Model B: Qwen2.5-8B-Instruct-GPTQ-Int4

Steps:
  Same prompt + seed, once on FP16 and once on INT4.
  Diff the logits.

Expectation: logits diverge clearly → confirming FP16 and INT4 must be
             registered as DIFFERENT model_ids.
Pass criteria: significant divergence (relative error > 0.01) → PASS
               (this is an inverse check — we want them to differ).
```

#### C4: TGI v2 vs v3

```
Hardware: 1 × 4090
Model: Qwen2.5-8B-Instruct FP16
TGI image A: text-generation-inference:2.4.1
TGI image B: text-generation-inference:3.3.6

Steps:
  Same prompt + seed, once on v2 and once on v3.
  Diff the logits.

Pass criteria:
  Identical → v2 and v3 can be mixed in a network.
  Diverge   → TGI version must be locked; mixing NOT allowed.
```

### 1.4 Test Scripts

```bash
#!/bin/bash
# logits-consistency-test.sh
# Run on each machine under test.

MODEL=$1       # e.g. "Qwen/Qwen2.5-8B-Instruct"
TGI_IMAGE=$2   # e.g. "ghcr.io/huggingface/text-generation-inference:3.3.6"
OUTPUT_DIR=$3  # e.g. "./results/4090-fp16"
GPU_NAME=$(nvidia-smi --query-gpu=name --format=csv,noheader | head -1)

echo "=== Logits Consistency Test ==="
echo "GPU:    $GPU_NAME"
echo "Model:  $MODEL"
echo "TGI:    $TGI_IMAGE"
echo "Output: $OUTPUT_DIR"

# 1. Start TGI
docker run -d --gpus all -p 8080:80 --name tgi-test \
  $TGI_IMAGE \
  --model-id $MODEL \
  --num-shard 1

# Wait until TGI is ready
echo "Waiting for TGI to be ready..."
for i in $(seq 1 60); do
  curl -s http://localhost:8080/health | grep -q "true" && break
  sleep 5
done

# 2. Collect logits
mkdir -p $OUTPUT_DIR

# C0: single request vs concurrent requests
echo "=== C0: Batching Impact ==="
python3 collect_logits.py \
  --endpoint http://localhost:8080 \
  --prompt "What is the capital of France?" \
  --seed 42 --temperature 0.7 \
  --mode single \
  --output $OUTPUT_DIR/c0_single.json

python3 collect_logits.py \
  --endpoint http://localhost:8080 \
  --prompt "What is the capital of France?" \
  --seed 42 --temperature 0.7 \
  --mode concurrent --concurrent-count 4 \
  --output $OUTPUT_DIR/c0_batch.json

python3 compare_logits.py \
  --a $OUTPUT_DIR/c0_single.json \
  --b $OUTPUT_DIR/c0_batch.json \
  --label "C0: Single vs Batch"

# C1 / C2: bulk collection (100 prompts)
echo "=== Collecting logits for 100 prompts ==="
python3 collect_logits.py \
  --endpoint http://localhost:8080 \
  --prompts prompts_short.json \
  --seed 42 --temperature 0.7 \
  --output $OUTPUT_DIR/logits_short.json

python3 collect_logits.py \
  --endpoint http://localhost:8080 \
  --prompts prompts_medium.json \
  --seed 42 --temperature 0.7 \
  --output $OUTPUT_DIR/logits_medium.json

python3 collect_logits.py \
  --endpoint http://localhost:8080 \
  --prompts prompts_long.json \
  --seed 42 --temperature 0.7 \
  --output $OUTPUT_DIR/logits_long.json

# Cleanup
docker stop tgi-test && docker rm tgi-test
echo "=== Done. Results in $OUTPUT_DIR ==="
```

```python
# compare_logits.py — diff two logits dumps (runs locally, no GPU needed)
import json, sys, numpy as np

def compare(file_a, file_b, label):
    a = json.load(open(file_a))
    b = json.load(open(file_b))

    max_abs_err = 0
    total_rel_err = 0
    count = 0
    mismatches = 0

    for prompt_id in a:
        if prompt_id not in b:
            continue
        for pos in a[prompt_id]["logits"]:
            la = np.array(a[prompt_id]["logits"][pos])
            lb = np.array(b[prompt_id]["logits"][pos])

            abs_err = np.max(np.abs(la - lb))
            rel_err = np.max(np.abs(la - lb) / (np.abs(la) + 1e-10))

            max_abs_err = max(max_abs_err, abs_err)
            total_rel_err += rel_err
            count += 1

            if rel_err > 1e-5:
                mismatches += 1

    avg_rel_err = total_rel_err / count if count > 0 else 0

    print(f"\n{'='*60}")
    print(f"  {label}")
    print(f"  Prompts compared:    {count // 5}")
    print(f"  Positions compared:  {count}")
    print(f"  Max absolute error:  {max_abs_err:.2e}")
    print(f"  Avg relative error:  {avg_rel_err:.2e}")
    print(f"  Mismatches (>1e-5):  {mismatches}/{count}")
    print(f"  Result: {'PASS' if mismatches == 0 else 'INVESTIGATE' if max_abs_err < 1e-3 else 'FAIL'}")
    print(f"{'='*60}")

if __name__ == "__main__":
    compare(sys.argv[1], sys.argv[2], sys.argv[3] if len(sys.argv) > 3 else "Compare")
```

---

## 2. TPS Stress Test

### 2.1 Layered Approach

Do NOT extrapolate linearly. Find the actual bottleneck at each layer. Total TPS = the minimum of all layers.

```
Total TPS = min(
    Layer 1: single-GPU throughput × number of GPUs,
    Layer 2: inverse of end-to-end pipeline latency,
    Layer 3: Leader dispatch ceiling,
    Layer 4: P2P gossipsub message-propagation ceiling,
    Layer 5: on-chain BatchSettlement ceiling
)
```

### 2.2 Layer 1: Single-GPU Throughput Baseline

```
Where: your 5090 (free)
Model: Qwen2.5-8B-Instruct FP16
TGI image: 3.3.6

Measure at increasing concurrency:
  1-way concurrent: tok/s, latency p50/p99, VRAM
  2-way concurrent: tok/s, latency, VRAM
  4-way concurrent: tok/s, latency, VRAM
  8-way concurrent: tok/s, latency, VRAM (may OOM)

Output table:
  | Concurrency | tok/s | p50 latency | p99 latency | VRAM |
  |-------------|-------|-------------|-------------|------|
  | 1           |       |             |             |      |
  | 2           |       |             |             |      |
  | 4           |       |             |             |      |

This number × number of GPUs = theoretical TPS ceiling.
```

### 2.3 Layer 2: End-to-End Pipeline Latency

```
Where: 4 × RTX 4090 (rented on Vast.ai)
Roles: 1 Leader + 1 Worker + 2 Verifiers
All running REAL inference (TGI). No mocks.

Measure per-stage timestamps for a single request:
  t0: SDK emits InferRequest
  t1: Leader receives → dispatches task
  t2: Worker receives AssignTask → inference starts
  t3: Worker completes inference → emits InferReceipt
  t4: 3 Verifiers receive → teacher forcing starts
  t5: 3 Verifiers complete → emit VerifyResult
  t6: Proposer has all results → builds batch
  t7: BatchSettlement lands on chain
  t8: Settlement complete

Bottleneck decomposition:
  Inference latency   = t3 - t2   (GPU-bound)
  Verification latency = t5 - t4  (teacher forcing, typically ~1/10 of inference)
  P2P propagation     = t4 - t3   (Worker → Verifier)
  Settlement latency  = t8 - t6   (on-chain processing)

  The largest of these is the bottleneck.
```

### 2.4 Layer 3: Leader Dispatch Ceiling

```
Where: 10-20 × RTX 4090 (rented on Vast.ai)
All running real inference.

Measure:
  Ramp request rate from 1 req/s up to 20 req/s.
  At each rate, observe the Leader's:
    - Shadow-balance query latency
    - Worker-selection latency
    - Memory footprint growth
    - Message-processing goroutine count

  Find the knee where p99 latency spikes.

Outcomes:
  If Leader falls behind at 10 req/s:
    → Leader needs horizontal scale-out OR per-model sharding.
  If Leader only falls behind at 100 req/s:
    → Plenty of headroom for bootstrap; optimize later.
```

### 2.5 Layer 4: P2P Network Bottleneck

```
Where: 100 × CPU instances (rented on Vast.ai, cheapest CPU tier).
⚠️ This layer can use MOCK Workers (no real inference) because we only
   measure the network layer.
⚠️ MUST add `tc netem delay 100ms` on every box to simulate realistic latency.

Measure:
  100 mock Workers join the P2P mesh.
  Publish from one node; measure time-to-receipt at the 100th node.
  Ramp message rate: 10 / 100 / 1000 / 10000 msg/s.

  Observe:
    - gossipsub propagation latency vs message rate
    - Message loss rate
    - Whether peer scoring triggers (N5 message-storm defense)
    - CPU and bandwidth utilization

Notes on the mock:
  The mock only tests P2P propagation, not realistic payload sizes.
  Real StreamToken messages (encrypted prompt + logits) can be ~10× larger
  than a mock message. Size the mock to the real sizes:
    InferRequest:      ~1 KB
    StreamToken:       ~200 B × 200 tokens = 40 KB / request
    VerifyResult:      ~500 B
    Total per request: ~42 KB of P2P traffic.
```

### 2.6 Layer 5: On-Chain Settlement Bottleneck

```
Where: local `go test -bench` (free).

Measure BatchSettlement handling for 1K / 5K / 10K / 40K entries:
  - Processing time (MUST be < 5 s block time)
  - Gas consumption (MUST be < block gas limit)
  - DB I/O latency

  Two phases (already specified in the prior test plan):
    Step 1: Mock keeper — pure-compute performance (up to 40K)
    Step 2: Real DB  — includes I/O (up to 10K; extrapolate to 40K)
```

---

## 3. Execution Plan

### 3.1 Timeline

```
Day 0 — your 5090 (free):
  Morning: C0 concurrent batching impact (10 min, blocking)
  Morning: Layer 1 single-GPU throughput baseline (1 hr)
  Morning: Layer 5 go bench (30 min)
  → If C0 FAILs, stop and discuss mitigations.
  → If C0 PASSes, continue.

Day 1 — rented cloud (~$27):
  Rent 2 × 4090 for 2 hrs   → C1 same-hardware determinism + C3 quantization + C4 TGI version
  Rent 1 × A100 for 2 hrs   → C2 cross-hardware tolerance
  Rent 4 × 4090 for 2 hrs   → Layer 2 end-to-end pipeline (real inference + verification + settlement)

Day 2 — rented cloud (~$25):
  Rent 20 × 4090 for 2 hrs  → Layer 3 Leader dispatch ceiling
  → If Day 1 showed large cross-hardware logits drift, re-run with adjusted
    tolerance here at the same time.

Day 3 — rented cloud (~$9):
  Rent 100 × CPU instances for 3 hrs → Layer 4 P2P network bottleneck
  → Mock Workers + `tc netem` 100 ms delay.
```

### 3.2 Cloud Rental Cost Breakdown

**All on Vast.ai (cheapest; billed per minute).**

| Day | What | Qty | Price/hr | Duration | Subtotal |
|-----|------|-----|---------|----------|----------|
| 0 | Your 5090 | 1 | free | 2 hr | **$0** |
| 1 | 4090 (C1 + C3 + C4) | 2 | $0.35 | 2 hr | **$1.4** |
| 1 | A100 80 GB (C2) | 1 | $1.50 | 2 hr | **$3.0** |
| 1 | 4090 (Layer 2 E2E) | 4 | $0.35 | 2 hr | **$2.8** |
| 2 | 4090 (Layer 3 Leader) | 20 | $0.35 | 2 hr | **$14.0** |
| 2 | Debug reserve | - | - | - | **$5.0** |
| 3 | CPU instances (Layer 4 P2P) | 100 | $0.03 | 3 hr | **$9.0** |
| | | | | **Total** | **~$35** |

### 3.3 Vast.ai Rental Operations

```bash
# 1. Register on Vast.ai → deposit $50 (balance buffer, not urgent).

# 2. Search for 4090 instances:
#    GPU Type: RTX 4090
#    VRAM: >= 24 GB
#    Docker Image: ghcr.io/huggingface/text-generation-inference:3.3.6
#    Sort: Price Low→High

# 3. Bulk rental (Layer 3 needs 20 nodes).
#    Vast.ai CLI supports bulk operations:
pip install vastai
vastai set api-key YOUR_KEY

# Search for available 4090s
vastai search offers 'gpu_name=RTX_4090 num_gpus=1 dph<0.40 inet_down>200'

# Create 20 instances
for i in $(seq 1 20); do
  vastai create instance OFFER_ID \
    --image ghcr.io/huggingface/text-generation-inference:3.3.6 \
    --disk 50 --onstart-cmd "sleep infinity"
done

# 4. Deploy FunAI node on each box (over SSH):
git clone https://github.com/funai-wiki/funai-chain.git
cd funai-chain
make build-all
# Configure and start funai-node (connect to the existing testnet).

# 5. Tear down after testing.
vastai destroy instance --all
```

---

## 4. Results Template

```
# FunAI Logits + TPS Test Results
# Date:   [date]
# Commit: ce87883

## Logits Consistency

### C0: Concurrent Batching Impact (⚠️ blocking)
  GPU: [model]
  Single vs Batch max_rel_error: [value]
  Result: [PASS / INVESTIGATE / FAIL]
  → If FAIL, mitigation adopted: [A / B / C]

### C1: Same-Hardware Determinism
  GPU A: [model]   GPU B: [model]
  Bit-exact: [Yes / No]
  Max abs error: [value]
  → If No, cause: [CUDA non-determinism / TGI bug / ...]

### C2: Cross-Hardware Tolerance
  | Prompt length | Max abs error | Avg rel error | p99 rel error |
  |---------------|---------------|---------------|---------------|
  | 20 tokens     |               |               |               |
  | 200 tokens    |               |               |               |
  | 2000 tokens   |               |               |               |

  Recommended logits_match_threshold: [value]
  Is current 4/5 matching sufficient: [Yes / No]

### C3: Quantization Isolation
  FP16 vs INT4 divergence is significant: [Yes / No]
  → Must register as distinct model_ids: [confirm]

### C4: TGI v2 vs v3
  Safe to mix: [Yes / No]
  → If No, locked version: [3.3.6]

## TPS Stress

### Layer 1: Single-GPU Throughput
  | Concurrency | tok/s | p50 latency | p99 latency | VRAM |
  |-------------|-------|-------------|-------------|------|
  | 1           |       |             |             |      |
  | 2           |       |             |             |      |
  | 4           |       |             |             |      |
  | 8           |       |             |             |      |

### Layer 2: End-to-End Pipeline
  | Stage                 | Time | % of total |
  |-----------------------|------|------------|
  | SDK → Leader          |      |            |
  | Leader → Worker       |      |            |
  | Worker inference      |      |            |
  | Worker → Verifier     |      |            |
  | Verifier verification |      |            |
  | Batch packing         |      |            |
  | On-chain settlement   |      |            |
  | **End-to-end**        |      | 100%       |

  Bottleneck: [inference / verification / P2P / settlement]

### Layer 3: Leader Dispatch
  | req/s | Leader p99 latency | Memory | Falling behind? |
  |-------|--------------------|--------|-----------------|
  | 1     |                    |        |                 |
  | 5     |                    |        |                 |
  | 10    |                    |        |                 |
  | 20    |                    |        |                 |

  Leader bottleneck: [x] req/s

### Layer 4: P2P Network
  | msg/s | Propagation p50 | Propagation p99 | Loss rate | Notes |
  |-------|-----------------|-----------------|-----------|-------|
  | 10    |                 |                 |           |       |
  | 100   |                 |                 |           |       |
  | 1000  |                 |                 |           |       |
  | 10000 |                 |                 |           |       |

  P2P bottleneck: [x] msg/s
  Equivalent inference requests: [x/209] req/s

### Layer 5: On-Chain Settlement
  | Batch size | Processing time | Gas |
  |------------|-----------------|-----|
  | 1K         |                 |     |
  | 5K         |                 |     |
  | 10K        |                 |     |
  | 40K        |                 |     |

## Overall TPS Conclusion

  Single-GPU ceiling:       [x] tok/s
  End-to-end pipeline:      [x] req/s  (bottleneck: [xxx])
  Leader ceiling:           [x] req/s
  P2P ceiling:              [x] req/s  (100 nodes)
  Settlement ceiling:       [x] entries/s

  Current system (10 × 4090) effective TPS: min(above) = [x] req/s = [x] tok/s
  To reach 1 M tok/s:
    GPUs needed:        [x]
    Leaders needed:     [x] (will require sharding)
    Known bottlenecks:  [list]

## Actual Cloud Spend
  Vast.ai total: $[x]
  Overspend reason (if any): [explanation]
```

---

## 5. Why C2 Does Not Need an H100

Architecturally, 4090 (Ada Lovelace) and A100 (Ampere) are far enough apart to expose any cross-hardware logits drift. H100 (Hopper) and A100 are both data-center parts and share closer architectural lineage.

If 4090 vs A100 logits fit inside our tolerance, 4090/A100 vs H100 very likely fits too. At $2.50/hr, the H100 is not worth a dedicated run at this stage.

If 4090 vs A100 drift is large, only then rent an H100 to cross-check — budgeted in Day 2's $5 debug reserve.

---

*Doc version: V1*
*Date: 2026-04-03*
*Baseline: commit `ce87883`*
*Doc suffix: KT*
