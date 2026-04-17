# Model Registry

The model registry governs how inference models are proposed, identified, activated, and monitored on FunAI Chain. All registry state lives on-chain in the [`x/modelreg/`](../x/modelreg/) module.

Source: [FunAI V52 Final Design Spec](../docs/FunAI_V52_Final.md)

## Model Identity

Every model is uniquely identified by a content-addressed hash:

```
model_id = SHA256(weight_hash || quant_config_hash || runtime_image_hash)
```

This triple-hash ensures that any change to model weights, quantization configuration, or runtime image produces a different `model_id`. Two nodes claiming the same `model_id` are guaranteed to run bit-identical inference.

## Epsilon (Tolerance)

When a proposer submits a `MsgModelProposal`, they must include an epsilon (tolerance) value derived from empirical testing:

1. The proposer tests **100 prompts** across **2+ GPU types** with **3 runs** each.
2. The **P99.9** divergence across all runs becomes the epsilon threshold.
3. Typical hardware-induced differences are **< 0.01**, while model substitution divergence is **> 1.0** -- a **100x safety margin** separating legitimate hardware variance from fraud.

Epsilon is used during [verification](verification.md) to determine whether logit differences at sampled positions constitute a match or a mismatch.

## Activation Threshold

A model transitions to ACTIVE status when **all three** conditions are met simultaneously:

| Condition | Threshold |
|-----------|-----------|
| Installed stake | >= 2/3 of total stake |
| Worker count | >= 4 |
| Distinct operator count | >= 4 |

**ACTIVE status never reverts.** Once a model is activated, it remains ACTIVE in the registry permanently. Service availability is determined dynamically by the running threshold (below), not by activation status.

## Running Threshold

A model is eligible for dispatch when **both** conditions are met:

| Condition | Threshold |
|-----------|-----------|
| Installed stake | >= 1/10 of total stake |
| Installed worker count | >= 10 |

**Rationale for 10 workers minimum:** Each inference task requires 1 Worker (inference) + 3 Verifiers = 4 rigid slots. The remaining 6 workers serve as the second verification candidate pool. Since second verification requires 3 independent results, a pool of 6 is sufficient to guarantee availability.

If the running condition is not met, the [P2P layer](p2p-layer.md) stops dispatching tasks for that `model_id`. When conditions recover (e.g., workers come back online or new workers install the model), dispatch resumes automatically. No manual intervention or on-chain transaction is required.

## On-Chain Messages

| Message | Who can send | Purpose |
|---------|-------------|---------|
| `MsgModelProposal` | Anyone | Propose a new `model_id` with its epsilon tolerance, pricing hints, and metadata |
| `MsgDeclareInstalled` | Registered Worker | Declare that the worker has installed and is ready to serve a specific `model_id` |

Workers must first register via `MsgRegisterWorker` (see the [architecture overview](architecture.md)) before they can declare models installed. The [settlement](settlement.md) module enforces that only tasks for ACTIVE models with running status can be included in `MsgBatchSettlement`.

## Lifecycle Summary

```
MsgModelProposal (anyone)
  |
  v
PROPOSED -- waiting for workers to install
  |
  v  (installed_stake >= 2/3 AND workers >= 4 AND operators >= 4)
ACTIVE -- permanent, never reverts
  |
  +-- running check (installed_stake >= 1/10 AND installed_worker_count >= 10)
  |     |
  |     +-- YES -> dispatch enabled (P2P layer serves requests)
  |     +-- NO  -> dispatch paused (automatically resumes when conditions recover)
```
