# Validator Guide

This guide covers how to become a FunAI Chain validator and participate in block consensus.

## Overview

FunAI Chain uses CometBFT consensus with a VRF-selected 100-person validator committee. Validators:

- Sign and propose blocks
- Earn block rewards when no inference activity exists
- Participate in governance

Validators are selected from registered Workers via VRF every 120 blocks (~10 minutes).

## Validator vs Worker

| Role | Hardware | Selection | Reward |
|------|----------|-----------|--------|
| Validator | CPU only | VRF committee (100 seats), α=1.0 | Block rewards (when no inference) |
| Worker | GPU required | VRF per task, α=1.0 | Inference fees + block rewards |

All validators are Workers. You must first register as a Worker to be eligible for the validator committee.

## Requirements

- A registered and active Worker (see [Worker Operator Guide](Worker_Operator_Guide.md))
- Staked FAI tokens (higher stake = better VRF score)
- Reliable uptime for block signing

## How Validator Selection Works

### VRF Committee

Every 120 blocks (~10 minutes), the network selects a 100-person validator committee:

```
score = hash(epoch_block_hash || pubkey) / stake^1.0
```

The 100 Workers with the lowest scores become validators for that epoch.

### Effective Stake

Your effective stake for VRF includes reputation:

```
effective_stake = stake × reputation
```

Where reputation ranges from 0.0 to 1.2 (see [Worker Operator Guide](Worker_Operator_Guide.md#reputation-system)).

## Block Rewards

### Reward Formula

```
block_reward(h) = base_reward × 0.5 ^ floor(h / halving_period)
```

- Base reward: 4,000 FAI per block
- Block time: 5 seconds
- Halving period: ~4.16 years
- Epoch: 100 blocks (500 seconds)

### Distribution Scenarios

**Scenario 1: Inference activity exists (normal)**

| Pool | Share | Distributed by |
|------|-------|---------------|
| Inference pool | 99% | Fee contribution (80%) + task count (20%) |
| Verification pool | 1% | Verification + second verification count |

Validators earn from inference pool if they also perform inference/verification work.

**Scenario 2: No inference activity**

100% of epoch rewards distributed to consensus committee members, proportional to blocks signed.

**Scenario 3: Fallback (no consensus signers recorded)**

100% distributed by online worker stake (proportional).

## Setup

### 1. Register as Worker

Follow the [Worker Operator Guide](Worker_Operator_Guide.md) to register and stake.

### 2. Ensure Node is Running

Your chain node (`funaid`) must be running and synced:

```bash
# Check sync status
./build/funaid status | jq '.SyncInfo.catching_up'
# Should be "false"
```

### 3. Validator Auto-Selection

Once registered as an active Worker with sufficient stake, you are automatically eligible for the VRF validator committee. No additional registration is needed.

## Monitoring

### Check Validator Status

```bash
# Your worker info (includes validator eligibility)
./build/funaid query worker show $(./build/funaid keys show worker-key -a)

# Current validator set
./build/funaid query comet-validator-set
```

### Key Metrics

| Metric | What to Watch |
|--------|--------------|
| Stake | Higher stake = better chance of committee selection |
| Reputation | Must be > 0 to be eligible |
| Uptime | Missing blocks affects reward share |
| JailCount | Jailed workers cannot be validators |

## Economics

### Staking

```bash
# Add stake
./build/funaid tx worker stake 50000000000000ufai \
    --from worker-key \
    --chain-id funai_123123123-3

# Check stake
./build/funaid query worker show $(./build/funaid keys show worker-key -a)
```

### Reward Estimation

With no inference activity (consensus-only rewards):

```
your_reward = epoch_reward × (your_blocks_signed / total_blocks_signed)
```

With inference activity, validator rewards come from inference/verification work rather than block signing.

## Governance

Validators participate in on-chain governance including:

- Model proposals (`MsgModelProposal`)
- Parameter change proposals
- Software upgrade proposals

## Best Practices

1. **Maximize uptime** — Missed blocks reduce your reward share
2. **Maintain high reputation** — Complete tasks successfully, avoid timeouts
3. **Stake appropriately** — Higher stake improves VRF selection probability
4. **Monitor jail status** — Unjail promptly when jailed
5. **Run both binaries** — `funaid` (chain) + `funai-node` (P2P) for full participation
6. **Keep software updated** — Follow upgrade announcements

## Unbonding

To stop being a validator, exit as a Worker:

```bash
./build/funaid tx worker exit \
    --from worker-key \
    --chain-id funai_123123123-3
```

21-day unbonding period applies. During unbonding:
- You remain in the validator set until the period ends
- Stake is locked
- You can still be slashed for misbehavior
