# T4 GPU E2E Test Execution Plan

> Goal: On a single T4 (16GB VRAM) machine, using OpenClaw as the actual inference scenario, complete the FunAI full-pipeline E2E tests
>
> Date: 2026-03-23

---

## 1. Hardware Constraints and Model Selection

### 1.1 T4 GPU Specifications

| Parameter | Value |
|------|---|
| GPU | NVIDIA Tesla T4 |
| VRAM | 16 GB GDDR6 |
| FP16 | 8.1 TFLOPS |
| INT8 | 130 TOPS |

### 1.2 Available Models (16GB VRAM Constraint)

| Model | Parameters | Quantization | VRAM Usage | Recommendation | Alias |
|------|--------|------|-----------|--------|------|
| Qwen2.5-7B-Instruct | 7B | AWQ/GPTQ Q4 | ~5 GB | **Primary** | `qwen25-7b-q4` |
| Llama-3.1-8B-Instruct | 8B | AWQ Q4 | ~6 GB | Alternative | `llama31-8b-q4` |
| Qwen2.5-3B-Instruct | 3B | FP16 | ~7 GB | Lightweight testing | `qwen25-3b-fp16` |
| Qwen2.5-0.5B-Instruct | 0.5B | FP16 | ~1.5 GB | Quick validation | `qwen25-05b-fp16` |

**Recommendation**: First use `qwen25-05b-fp16` to quickly run through the full pipeline, then switch to `qwen25-7b-q4` for formal testing.

---

## 2. Architecture Overview

```
┌──────────────────────────────────────────────────────────┐
│  T4 GPU Machine                                           │
│                                                          │
│  ┌─────────────┐  ┌──────────────┐  ┌──────────────────┐ │
│  │ vLLM Server  │  │ funaid       │  │ funai-node ×4    │ │
│  │ :8000        │  │ (single-node │  │ (Worker/Leader/  │ │
│  │ Qwen2.5-7B  │  │  chain)      │  │  Verifier)       │ │
│  │             │  │ :26657 :1317 │  │                  │ │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────────┘ │
│         │                 │                  │             │
│         └────────TGI API──┘──────P2P+Chain───┘             │
│                                                          │
│  ┌──────────────────────────────────────────────────────┐ │
│  │ Test Client                                           │ │
│  │ ┌──────────┐  ┌──────────────┐  ┌─────────────────┐ │ │
│  │ │ SDK Direct│  │ OpenClaw +   │  │ funaid CLI      │ │ │
│  │ │ (Go test) │  │ FunAI Plugin │  │ (tx validation) │ │ │
│  │ └──────────┘  └──────────────┘  └─────────────────┘ │ │
│  └──────────────────────────────────────────────────────┘ │
└──────────────────────────────────────────────────────────┘
```

---

## 3. Prerequisites (Phase 0)

### 3.1 Environment Setup

```bash
# 1. NVIDIA Driver + CUDA (T4 requires CUDA 12.x)
nvidia-smi  # Confirm driver is working

# 2. Docker + NVIDIA Container Toolkit
sudo apt-get install -y docker.io nvidia-container-toolkit
sudo systemctl restart docker
docker run --rm --gpus all nvidia/cuda:12.4.0-base-ubuntu22.04 nvidia-smi

# 3. Go 1.22+
go version

# 4. Clone and build
git clone https://github.com/funai-wiki/funai-chain.git
cd funai-chain
make build-all

# 5. Python (OpenClaw runtime environment)
python3 --version  # 3.10+
pip install openclaw  # Or install from source
```

### 3.2 vLLM Startup

```bash
# Quick validation model (0.5B, barely uses VRAM)
docker run --gpus all -d --name funai-vllm \
  -p 8000:8000 \
  vllm/vllm-openai:latest \
  --model Qwen/Qwen2.5-0.5B-Instruct \
  --dtype float16 \
  --gpu-memory-utilization 0.85 \
  --max-model-len 4096 \
  --host 0.0.0.0 \
  --port 8000

# Verify vLLM health
curl http://localhost:8000/health
curl -X POST http://localhost:8000/generate \
  -H "Content-Type: application/json" \
  -d '{"inputs":"Hello, who are you?","parameters":{"max_new_tokens":50}}'
```

> **Note**: vLLM provides an OpenAI-compatible API by default (`/v1/completions`), but funai-node's `TGIClient` uses TGI format (`/generate`, `/generate_stream`). You need to add `--api-type generate` when starting vLLM or add a TGI-compatible adapter layer in front of vLLM.
>
> Simplest approach: Use text-generation-inference (TGI) instead of vLLM:
> ```bash
> docker run --gpus all -d --name funai-tgi \
>   -p 8000:80 \
>   ghcr.io/huggingface/text-generation-inference:latest \
>   --model-id Qwen/Qwen2.5-0.5B-Instruct \
>   --dtype float16 \
>   --max-input-length 4096 \
>   --max-total-tokens 8192
> ```

### 3.3 Chain Initialization (Single Node)

```bash
# Clean old data
rm -rf ~/.funaid

# Initialize
make init

# Create test accounts
./build/funaid keys add user1 --keyring-backend test
./build/funaid keys add worker1 --keyring-backend test
./build/funaid keys add worker2 --keyring-backend test
./build/funaid keys add worker3 --keyring-backend test
./build/funaid keys add worker4 --keyring-backend test

# Allocate funds to test accounts
./build/funaid genesis add-genesis-account user1 50000000000000ufai --keyring-backend test
./build/funaid genesis add-genesis-account worker1 50000000000000ufai --keyring-backend test
./build/funaid genesis add-genesis-account worker2 50000000000000ufai --keyring-backend test
./build/funaid genesis add-genesis-account worker3 50000000000000ufai --keyring-backend test
./build/funaid genesis add-genesis-account worker4 50000000000000ufai --keyring-backend test

# Collect genesis transactions
./build/funaid genesis collect-gentxs

# Start chain
./build/funaid start &
sleep 10
curl -s http://localhost:26657/status | jq '.result.sync_info.latest_block_height'
```

### 3.4 Critical Code Patch: P2P Message Dispatch

> **Blocker**: Currently `p2p/node.go`'s `Node.Start()` only subscribes to topics but **does not route messages to `Leader.HandleRequest` / `Worker.HandleTask`**. The pubsub dispatch logic must be completed first before E2E can run.

The following needs to be added to `Node.Start()`:

```go
// Pseudocode: message dispatch loop
go func() {
    for msg := range modelTopicSub.Messages() {
        var req p2ptypes.InferRequest
        if json.Unmarshal(msg.Data, &req) == nil {
            // Leader handles user requests
            go node.Leader.HandleRequest(ctx, &req)
        }
        var assign p2ptypes.AssignTask
        if json.Unmarshal(msg.Data, &assign) == nil {
            // Worker handles task assignments
            go node.Worker.HandleTask(ctx, &assign)
        }
    }
}()
```

**This is the first task of Phase 1.**

---

## 4. Execution Phases

### Phase 1: Single-Node Smoke Test (1-2 days)

**Goal**: Verify Chain -> P2P -> TGI basic connectivity.

| Step | Operation | Verification Point | Pass Criteria |
|------|------|--------|---------|
| 1.1 | Start vLLM/TGI + chain + 1 funai-node | No process panic | 3 processes running continuously |
| 1.2 | CLI register worker1 | `funaid tx worker register` | code: 0, `query worker list` contains worker1 |
| 1.3 | CLI register model (alias=qwen25-05b-fp16) | `funaid tx modelreg propose-model` | code: 0, `query modelreg model-by-alias` returns result |
| 1.4 | CLI declare installed | `funaid tx modelreg declare-installed` | code: 0 |
| 1.5 | CLI user deposit | `funaid tx settlement deposit 10000000000ufai --from user1` | InferenceAccount balance > 0 |
| 1.6 | **SDK direct inference** | Go test: `client.Infer(InferParams{...})` | Receives token stream, output is non-empty, task_id is valid |
| 1.7 | Check TGI logs | docker logs funai-tgi | Has /generate request records |

**Key script**:

```bash
#!/bin/bash
# scripts/t4-smoke.sh

set -e

echo "=== Step 1: Verify infrastructure ==="
curl -sf http://localhost:8000/health > /dev/null && echo "TGI: OK"
curl -sf http://localhost:26657/status > /dev/null && echo "Chain: OK"

echo "=== Step 2: Register worker ==="
./build/funaid tx worker register \
  --gpu-model "Tesla-T4" --gpu-vram 16 --gpu-count 1 \
  --from worker1 --keyring-backend test --chain-id funai-1 -y

echo "=== Step 3: Register model ==="
./build/funaid tx modelreg propose-model \
  --name "Qwen2.5-0.5B-Instruct" \
  --alias "qwen25-05b-fp16" \
  --weight-hash "$(sha256sum /path/to/model | cut -d' ' -f1)" \
  --from worker1 --keyring-backend test --chain-id funai-1 -y

echo "=== Step 4: Deposit ==="
./build/funaid tx settlement deposit 10000000000ufai \
  --from user1 --keyring-backend test --chain-id funai-1 -y

echo "=== Step 5: SDK inference test ==="
go test ./tests/e2e/ -run TestSmoke_T4 -v -timeout 60s
```

---

### Phase 2: 4-Worker Full-Role E2E (2-3 days)

**Goal**: Verify the complete inference -> verification -> settlement -> reward pipeline.

**Deployment topology** (single machine simulating 4 Workers with 4 processes):

```
vLLM/TGI :8000 ← shared (all 4 funai-nodes point to the same inference engine)
funaid    :26657 ← single-node chain
funai-node-0 :4001  (Leader + Worker + Verifier)
funai-node-1 :4002  (Worker + Verifier)
funai-node-2 :4003  (Worker + Verifier)
funai-node-3 :4004  (Worker + Verifier)
```

| Number | Test Scenario | Roles Involved | Verification Points |
|------|---------|---------|---------|
| E1 | Normal inference happy path | User → Leader → Worker → Verifier | Token stream complete, verification 3/3 PASS, settlement SUCCESS, user balance deducted fee, worker receives 95% |
| E2 | Function calling | OpenClaw Skill → SDK | Model returns tool_call JSON, SDK parses correctly, OpenClaw executes tool and returns result |
| E3 | JSON mode | OpenClaw Skill → SDK | `response_format: json_object`, returns valid JSON, max 3 retries |
| E4 | Multi-turn conversation | OpenClaw → SDK | messages array → chat template → prompt, context maintained |
| E5 | Second verification trigger | Proposer → SecondVerifier | Set second_verification_rate high (100%), second verification PASS → CLEARED |
| E6 | Worker timeout retry | SDK → Leader | Worker doesn't respond for 5s, SDK auto-resends, second Worker completes |
| E7 | Insufficient balance | User | Deposit 1 ufai, inference fee=100000 ufai, returns insufficient_balance error |
| E8 | Concurrent inference | 3 OpenClaw Skills concurrent | 3 requests arrive simultaneously, 3 different Workers handle them respectively, all succeed |

**Phase 2 key verification script**:

```bash
#!/bin/bash
# scripts/t4-e2e.sh

echo "=== E1: Happy path ==="
# SDK sends inference request, waits for result
go test ./tests/e2e/ -run TestE2E_T4_HappyPath -v

echo "=== E2: Function calling ==="
# OpenClaw test Skill calls get_weather tool
python3 tests/openclaw/test_function_calling.py

echo "=== E3: JSON mode ==="
python3 tests/openclaw/test_json_mode.py

echo "=== E4: Multi-turn ==="
python3 tests/openclaw/test_multiturn.py

echo "=== E5-E8: Advanced scenarios ==="
go test ./tests/e2e/ -run "TestE2E_T4_Second verification|TestE2E_T4_Timeout|TestE2E_T4_InsufficientBalance|TestE2E_T4_Concurrent" -v
```

---

### Phase 3: OpenClaw Integration Test (2-3 days)

**Goal**: Verify the complete experience from the OpenClaw end-user perspective.

#### 3.1 OpenClaw FunAI Provider Setup

```python
# openclaw_funai_provider.py — minimal FunAI provider module

import funai_sdk  # Go SDK Python bindings or HTTP gateway

class FunAIProvider:
    def __init__(self, wallet_path, chain_rpc, boot_peers):
        self.client = funai_sdk.Client(
            wallet_path=wallet_path,
            chain_rpc=chain_rpc,
            boot_peers=boot_peers,
        )
    
    def chat_completion(self, messages, model="qwen25-7b-q4", **kwargs):
        return self.client.chat.completions.create(
            model=model,
            messages=messages,
            **kwargs,
        )
```

#### 3.2 Test Case Matrix

| Number | Skill Type | Test Content | Pass Criteria |
|------|-----------|---------|---------|
| OC1 | Plain text conversation | "Write me a poem about spring" | Returns a meaningful poem, streamed display |
| OC2 | Tool calling | Skill defines `get_weather` tool → AI calls it → returns weather | tool_call parsed successfully, tool executed, AI summarizes |
| OC3 | JSON extraction | Skill requests entity extraction: `{"name": ..., "age": ...}` | Returns valid JSON, all fields present |
| OC4 | Code generation | "Write a quicksort in Python" | Returns executable code |
| OC5 | Long context | After 10 turns of conversation, ask about content from turn 2 | Context not lost |
| OC6 | Error recovery | Kill vLLM mid-stream → restart | SDK timeout retry, resumes after recovery |
| OC7 | Balance warning | Send request after balance is used up | OpenClaw shows "insufficient balance" notification |
| OC8 | Model switch | Switch from qwen25-05b to qwen25-7b | Inference uses new model after switch |

#### 3.3 Automated Test Script

```python
# tests/openclaw/run_all.py

import subprocess, sys, json

TESTS = [
    ("OC1 Plain Text", "test_text_chat.py"),
    ("OC2 Tool Calling", "test_function_calling.py"),
    ("OC3 JSON Extraction", "test_json_mode.py"),
    ("OC4 Code Generation", "test_code_gen.py"),
    ("OC5 Long Context", "test_long_context.py"),
    ("OC6 Error Recovery", "test_error_recovery.py"),
    ("OC7 Balance Warning", "test_balance_warning.py"),
    ("OC8 Model Switch", "test_model_switch.py"),
]

results = []
for name, script in TESTS:
    r = subprocess.run(["python3", f"tests/openclaw/{script}"], capture_output=True)
    status = "PASS" if r.returncode == 0 else "FAIL"
    results.append({"test": name, "status": status})
    print(f"  [{status}] {name}")

passed = sum(1 for r in results if r["status"] == "PASS")
print(f"\n{passed}/{len(results)} passed")
```

---

### Phase 4: Upgrade to 7B Model + Stress Test (1-2 days)

**Goal**: Verify performance and stability at real model scale.

#### 4.1 Model Upgrade

```bash
# Stop old vLLM, start 7B model
docker stop funai-tgi && docker rm funai-tgi

docker run --gpus all -d --name funai-tgi \
  -p 8000:80 \
  ghcr.io/huggingface/text-generation-inference:latest \
  --model-id Qwen/Qwen2.5-7B-Instruct-AWQ \
  --quantize awq \
  --max-input-length 4096 \
  --max-total-tokens 8192 \
  --max-batch-prefill-tokens 4096

# Register new model on chain
./build/funaid tx modelreg propose-model \
  --name "Qwen2.5-7B-Instruct-AWQ" \
  --alias "qwen25-7b-q4" \
  --from worker1 --keyring-backend test --chain-id funai-1 -y
```

#### 4.2 Stress Test

| Test | Parameters | Verification Points |
|------|------|--------|
| Throughput | 10 concurrent x 50 requests | TPS, average latency, P99 latency |
| Long text | max_tokens=2048, 10 requests | All succeed, no OOM |
| Sustained run | 1 req/s x 1 hour | No memory leak, no goroutine leak |
| GPU utilization | nvidia-smi monitoring | GPU utilization > 50%, VRAM does not overflow |

```bash
# Stress test script
#!/bin/bash
echo "=== Throughput test: 10 concurrent × 50 requests ==="
go test ./tests/e2e/ -run TestStress_T4_Throughput -v \
  -concurrent=10 -requests=50 -timeout 600s

echo "=== Long text test ==="
go test ./tests/e2e/ -run TestStress_T4_LongText -v -timeout 300s

echo "=== Soak test: 1 hour ==="
go test ./tests/e2e/ -run TestStress_T4_Soak -v -timeout 3700s
```

---

## 5. Pre-Requisite Blocker Checklist

Listed by priority, must be resolved before E2E testing:

| # | Blocker | Severity | Impact | Estimated Effort |
|---|--------|--------|------|-----------|
| **B1** | `Node.Start()` missing pubsub message dispatch loop | **P0 Blocker** | Leader/Worker/Verifier cannot receive messages | ~100 lines |
| **B2** | `cmd/funai-node/main.go` missing `WorkerPubkey`/`WorkerPrivKey` environment variable reading | **P0 Blocker** | Worker cannot sign receipts | ~15 lines |
| **B3** | TGI/vLLM API compatibility validation | P1 | `TGIClient` uses `/generate` format, vLLM defaults to OpenAI format | Adapter layer or use TGI |
| **B4** | OpenClaw FunAI Provider module | P1 | OpenClaw cannot route to FunAI | ~200 lines Python |
| **B5** | SDK Python bindings or HTTP Gateway | P1 | OpenClaw (Python) calling Go SDK | ~300 lines |

---

## 6. Timeline Overview

```
Week 1 (first half): Phase 0 — Environment preparation + resolve B1/B2 blockers
Week 1 (second half): Phase 1 — Single-node Smoke Test (0.5B model)
Week 2 (first half): Phase 2 — 4-Worker full-role E2E
Week 2 (second half): Phase 3 — OpenClaw integration test
Week 3 (first half): Phase 4 — Upgrade to 7B + stress test
Week 3 (second half): Fix discovered bugs + regression testing
```

---

## 7. Acceptance Criteria

### Must Pass (GA prerequisite)

- [ ] Phase 1 all 7 steps pass
- [ ] Phase 2 E1 (happy path) + E2 (function calling) + E4 (multi-turn) pass
- [ ] Phase 3 OC1-OC5 pass
- [ ] Phase 4 throughput test with no OOM, no panic

### Should Pass (high priority)

- [ ] Phase 2 E3 (JSON mode) + E5 (second verification) + E7 (insufficient balance) pass
- [ ] Phase 3 OC6 (error recovery) + OC7 (balance warning) pass
- [ ] Phase 4 sustained run for 1 hour without anomalies

### Can Be Deferred (low priority)

- [ ] Phase 2 E6 (timeout retry) + E8 (concurrent) — depends on B1 pubsub dispatch full implementation
- [ ] Phase 3 OC8 (model switch) — requires running two TGI instances simultaneously
- [ ] Multi-GPU machine validation (requires additional hardware)

---

## 8. Monitoring and Log Collection

```bash
# Real-time GPU monitoring
watch -n 1 nvidia-smi

# Chain logs
tail -f /tmp/funai-node.log | grep -E "height|ERROR"

# P2P node logs
FUNAI_LOG_LEVEL=debug ./build/funai-node 2>&1 | tee /tmp/p2p-node.log

# TGI logs
docker logs -f funai-tgi

# Combined analysis
grep -h "task_id=" /tmp/p2p-node*.log | sort -t= -k2 | less
```

---

*Document version: V1*
*Created: 2026-03-23*
