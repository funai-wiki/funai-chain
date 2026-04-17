# Worker Operator Guide

This guide covers how to set up and operate a FunAI Worker node for AI inference, verification, and earning FAI rewards.

## Overview

A FunAI Worker node participates in the decentralized AI inference network by:

- **Inference execution** — Running AI models on GPU
- **Verification** — Teacher forcing to verify other Workers' results
- **Second verificationing** — Random second verification of verified tasks
- **Leader** — Dispatching tasks for a model topic (auto-elected)
- **Proposer** — Batching settlements on-chain (CometBFT rotation)

All roles run on the same node. The network assigns roles dynamically via VRF (Verifiable Random Function).

## Requirements

### Hardware

| Component | Minimum | Recommended |
|-----------|---------|-------------|
| GPU | 1x with 16GB+ VRAM | Multiple GPUs, 24GB+ VRAM |
| CPU | 4 cores | 8+ cores |
| RAM | 16 GB | 32+ GB |
| Storage | 100 GB SSD | 500 GB NVMe |
| Network | 100 Mbps | 1 Gbps |

### Software

- Go 1.25+
- CUDA toolkit (matching GPU driver)
- Inference backend: TGI, vLLM, SGLang, Ollama, or OpenAI-compatible server
- Linux (Ubuntu 22.04+ recommended)

## Setup

### 1. Build Binaries

```bash
git clone https://github.com/funai-network/funai-chain.git
cd funai-chain
make build-all    # Builds funaid + funai-node → ./build/
```

### 2. Initialize Chain Node

```bash
# Initialize node config
./build/funaid init my-worker --chain-id funai_123123123-3

# Import or create a key
./build/funaid keys add worker-key

# Note your address
./build/funaid keys show worker-key -a
# funai1abc...
```

### 3. Get FAI Tokens

You need FAI tokens for staking. Obtain them through:
- Testnet faucet (for testnet)
- Token purchase (for mainnet)

### 4. Register as Worker

```bash
# Get your public key
PUBKEY=$(./build/funaid keys show worker-key --pubkey | jq -r '.key')

# Register with stake
./build/funaid tx worker register \
    --pubkey "$PUBKEY" \
    --models "model-id-1,model-id-2" \
    --endpoint "/ip4/<your-ip>/tcp/4001" \
    --gpu-model "NVIDIA A100" \
    --gpu-vram 80 \
    --gpu-count 1 \
    --operator-id "my-operator" \
    --from worker-key \
    --chain-id funai_123123123-3
```

### 5. Add Stake

```bash
./build/funaid tx worker stake 100000000000000ufai \
    --from worker-key \
    --chain-id funai_123123123-3
```

### 6. Start Inference Backend

Choose one of the supported backends:

**TGI (Text Generation Inference):**
```bash
docker run --gpus all -p 8080:80 \
    ghcr.io/huggingface/text-generation-inference:latest \
    --model-id Qwen/Qwen2.5-32B-Instruct-GPTQ-Int4 \
    --max-input-length 4096 \
    --max-total-tokens 8192
```

**vLLM:**
```bash
python -m vllm.entrypoints.openai.api_server \
    --model Qwen/Qwen2.5-32B-Instruct-GPTQ-Int4 \
    --port 8080
```

**Ollama:**
```bash
ollama serve  # default port 11434
ollama pull qwen2.5:32b
```

### 7. Start P2P Node

```bash
export FUNAI_WORKER_ADDR=$(./build/funaid keys show worker-key -a)
export FUNAI_WORKER_PRIVKEY="<hex-encoded-private-key>"
export FUNAI_MODELS="model-id-1,model-id-2"
export FUNAI_BOOT_PEERS="/ip4/<boot-node>/tcp/4001/p2p/<peer-id>"
export FUNAI_TGI_ENDPOINT="http://localhost:8080"
export FUNAI_CHAIN_RPC="http://localhost:26657"
export FUNAI_CHAIN_REST="http://localhost:1317"

./build/funai-node
```

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `FUNAI_LISTEN_ADDR` | `/ip4/0.0.0.0/tcp/4001` | P2P listen address |
| `FUNAI_CHAIN_RPC` | `http://localhost:26657` | Chain RPC URL |
| `FUNAI_CHAIN_REST` | `http://localhost:1317` | Chain REST URL |
| `FUNAI_TGI_ENDPOINT` | `http://localhost:8080` | Inference backend endpoint |
| `FUNAI_TGI_TOKEN` | — | Bearer token for remote TGI auth |
| `FUNAI_WORKER_ADDR` | — | Worker address (funai1...) |
| `FUNAI_WORKER_PRIVKEY` | — | Hex-encoded secp256k1 private key |
| `FUNAI_WORKER_PUBKEY` | — | Hex-encoded public key (auto-derived if empty) |
| `FUNAI_MODELS` | — | Comma-separated model IDs |
| `FUNAI_BOOT_PEERS` | — | Comma-separated bootstrap peer multiaddrs |
| `FUNAI_EPSILON` | `0.01` | Verification epsilon tolerance |
| `FUNAI_MAX_CONCURRENT` | — | Max concurrent inference tasks |
| `FUNAI_INFERENCE_BACKEND` | `tgi` | `tgi`, `openai`, `vllm`, `sglang`, `ollama` |
| `FUNAI_INFERENCE_MODEL` | — | Model name for OpenAI-compatible backends |
| `FUNAI_CHAIN_ID` | `funai_123123123-3` | Blockchain ID |
| `FUNAI_BATCH_INTERVAL` | `5s` | Settlement batch interval |
| `FUNAI_METRICS_ADDR` | `:9091` | Prometheus metrics listen address |

## Model Management

### Declare Model Installed

After downloading and verifying a model, declare it to the network:

```bash
./build/funaid tx modelreg declare-installed <model-id> \
    --from worker-key \
    --chain-id funai_123123123-3
```

### Update Supported Models

```bash
./build/funaid tx worker update-models "model-id-1,model-id-3" \
    --from worker-key \
    --chain-id funai_123123123-3
```

### Model Activation Requirements

A model becomes active when:
- `installed_stake_ratio >= 2/3` of total network stake
- `worker_count >= 4`
- `operator_count >= 4` (distinct operators)

A model can serve inference when:
- `installed_stake_ratio >= 2/3`
- `installed_worker_count >= 10`

### Model ID

```
model_id = SHA256(weight_hash || quant_config_hash || runtime_image_hash)
```

The model proposer tests epsilon tolerance across 100 prompts x 2+ GPU types x 3 runs, using P99.9. Workers verify independently before installing.

## Staking & Economics

### Token

- Symbol: **$FAI**, denom: `ufai`
- 1 FAI = 1,000,000 ufai

### Block Rewards

- Base: 4,000 FAI/block, halving every ~4.16 years
- **With inference activity**: 99% distributed by inference contribution, 1% by verification/second verification count
- **Without inference**: 100% to consensus committee by signed blocks

### Inference Fee Distribution (on SUCCESS)

| Recipient | Share |
|-----------|-------|
| Worker | 85% |
| 3 Verifiers | 12% (4% each) |
| Second verification fund | 3% |

### Contribution Weight Formula

```
w_i = 0.8 × (fee_i / total_fee) + 0.2 × (task_count_i / total_tasks)
```

## Reputation System

Each worker has a reputation score that affects VRF ranking.

### Score Range

- Initial: 1.0 (internal value: 10000)
- Min: 0.0, Max: 1.2

### Events That Change Reputation

| Event | Delta | Notes |
|-------|-------|-------|
| Task accepted & completed | +0.01 | Also resets consecutive reject counter |
| Worker miss (timeout/fail) | -0.10 | |
| SecondVerifier miss | -0.20 | Higher penalty for second verification failures |
| 10+ consecutive rejects (not busy) | -0.05 | Penalty for excessive idle rejections |
| Hourly decay | ±0.005 | Gradually returns toward 1.0 |

### Impact on VRF Ranking

```
effective_stake = stake × reputation
score = hash(seed || pubkey) / effective_stake^α
```

Workers with higher reputation get better VRF scores, meaning more task assignments.

### Latency Factor

Workers with lower average first-token latency get a bonus:

| Latency vs Threshold | Factor |
|---------------------|--------|
| < 50% of threshold | 1.5x boost |
| 50-80% | 1.0x (normal) |
| > 100% | 0.1x penalty |

## Jail & Penalties

### Automatic Jailing

Workers are jailed for task failures (timeout, verification fail, etc.):

| Offense | Duration | Effect |
|---------|----------|--------|
| 1st jail | 120 blocks (~10 min) | Wait, then unjail |
| 2nd jail | 720 blocks (~1 hour) | Wait, then unjail |
| 3rd jail | Permanent | 5% stake slash + tombstone |

50 consecutive successful tasks resets the jail counter to 0.

### Unjail

```bash
# Check if jail period has elapsed
./build/funaid query worker show $(./build/funaid keys show worker-key -a)

# Unjail
./build/funaid tx worker unjail \
    --from worker-key \
    --chain-id funai_123123123-3
```

### Fraud Proof

If a user's SDK detects output mismatch (M7 verification), a `MsgFraudProof` is submitted:
- **Immediate** 5% stake slash + permanent tombstone
- No unjail possible

## Monitoring

### Prometheus Metrics

The P2P node exposes Prometheus metrics at `FUNAI_METRICS_ADDR` (default `:9091`).

### Query Worker Status

```bash
# Worker details
./build/funaid query worker show <address>

# All workers
./build/funaid query worker list

# Module parameters
./build/funaid query worker params
```

### Key Metrics to Watch

- `ReputationScore` — Should stay above 10000 (1.0)
- `SuccessStreak` — Aim for 50+ to reset jail count
- `AvgLatencyMs` — Lower is better for VRF ranking
- `JailCount` — 3 = permanent tombstone
- `TotalFeeEarned` — Cumulative earnings

## Exit

To leave the network:

```bash
./build/funaid tx worker exit \
    --from worker-key \
    --chain-id funai_123123123-3
```

This starts a 21-day unbonding period. After the period, your stake is returned automatically.

## Troubleshooting

### Worker not receiving tasks

1. Check `IsActive` status — must not be jailed or tombstoned
2. Verify model IDs match active models on the network
3. Ensure P2P node is connected to bootstrap peers
4. Check that inference backend is running and responsive
5. Verify reputation score is not too low

### High latency

1. Check GPU utilization — may need to reduce `FUNAI_MAX_CONCURRENT`
2. Ensure inference backend has enough VRAM for the model
3. Check network connectivity to peers

### Jailed unexpectedly

1. Query worker status to see `JailCount` and `JailUntil`
2. Common causes: inference timeout, verification failure, network disconnection
3. Wait for jail period, then unjail
4. Focus on completing 50 consecutive successful tasks to reset jail counter
