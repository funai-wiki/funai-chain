# Three-Layer Architecture

FunAI Chain separates concerns into three distinct layers. The chain acts as a bank -- it holds balances, enforces penalties, and distributes block rewards. All inference happens off-chain over libp2p P2P. The client SDK handles user-facing concerns like streaming, pricing, and privacy.

> **Core principle:** the chain NEVER processes inference. This is a "Lightning scheme" -- like Bitcoin Lightning Network but simpler: one-way payments, no routing, no penalty transactions. Users pre-deposit (`MsgDeposit`), sign off-chain requests, Workers execute and collect signatures, Proposers batch-settle on-chain.

Source: [FunAI V52 Final Design Spec](../docs/FunAI_V52_Final.md)

---

## Five First Principles

1. **Work is correct** -- deterministic verification pipeline ensures every inference result can be checked.
2. **Cannot stop** -- no single point of failure; leader failover, worker fallback, and permissionless entry keep the network running.
3. **Anyone can enter** -- permissionless registration for Workers, Verifiers, and Proposers.
4. **Cannot survive = will die** -- market pricing; if a model cannot attract enough stake and usage, it deactivates naturally.
5. **Chain is a bank, not an exchange** -- the chain settles payments and enforces rules but never touches inference data.

---

## Layer Summary

| Layer | Role | Technology | Frequency |
|-------|------|------------|-----------|
| **L1 -- Cosmos Chain** | Deposits, withdrawals, settlement, staking, block rewards | CometBFT + Cosmos SDK | Low (on-chain transactions) |
| **L2 -- libp2p P2P** | Dispatch, accept, inference, verification, signature exchange | libp2p pubsub per `model_id` topic | High (per-task messaging) |
| **L3 -- SDK** | Model selection, pricing hints, streaming display, auto-retry | Pure client-side | Per-user request |

**Boundary rule:** mutable state flows through the SDK and P2P layer; only immutable, finalized results land on-chain.

---

## L1 -- Cosmos Chain

The chain is responsible for everything that requires global consensus and permanent record-keeping:

- **Balances** -- user inference balances and worker stake balances.
- **Deposits and withdrawals** -- `MsgDeposit` and `MsgWithdraw` move funds in and out.
- **Batch settlement** -- Proposers submit `MsgBatchSettlement` containing only [CLEARED](settlement.md) tasks. Fee splits are applied atomically on-chain.
- **Penalties** -- [jail/unjail/tombstone](settlement.md) for misbehaving Workers and Verifiers. `MsgFraudProof` triggers immediate slash 5% + tombstone.
- **Stake and registration** -- `MsgRegisterWorker` with pubkey, stake, endpoint, GPU info, and supported models.
- **Model registry** -- `MsgModelProposal` defines `model_id` and epsilon tolerance. Activation requires installed_stake >= 2/3 AND workers >= 4 AND operators >= 4.
- **Block rewards** -- 4,000 FAI per block (5-second block time), halving every ~4.16 years. Split: 99% by inference contribution, 1% by verification/second verification count (when inference exists); 100% to consensus committee otherwise.

### On-Chain Modules

| Module | Purpose |
|--------|---------|
| `x/settlement/` | User balances, `BatchSettlement`, per-task second verification, `FraudProof` |
| `x/worker/` | Worker registration, stake, jail/unjail/tombstone, stats |
| `x/modelreg/` | Model proposals, activation thresholds, suggested pricing |
| `x/reward/` | Block reward distribution (99% inference / 1% verify-second verification) |
| `x/vrf/` | [Unified VRF formula](vrf.md) for all ranking |

---

## L2 -- libp2p P2P

The P2P layer handles all high-frequency, latency-sensitive operations:

- **Inference transport** -- prompts and responses flow over libp2p, never touching the chain.
- **Dispatch and accept** -- Leaders rank Workers using the [VRF unified formula](vrf.md) (alpha=1.0) and dispatch tasks. Workers have a 1-second accept timeout with up to 3 fallback ranks.
- **Worker execution** -- the selected Worker runs inference and produces a result with logits.
- **Streaming** -- token-by-token streaming from Worker to user via libp2p.
- **Verification** -- 3 Verifiers per task (VRF top 3, alpha=0.5) perform teacher forcing (~0.6s). Logits check at 5 [VRF-selected positions](vrf.md); 4/5 match required within epsilon.
- **Signature collection** -- Workers and Verifiers exchange signatures; the Proposer collects them for batch settlement.
- **Leader election** -- one Leader per `model_id` topic, 30-second epoch, auto-split at TPS > 500. Failover: 1.5s inactivity triggers switch to rank #2.
- **Data retention** -- all participating nodes retain task data for 7 days.

### P2P Components

| Component | Purpose |
|-----------|---------|
| `p2p/leader/` | Leader election, dispatch, failover |
| `p2p/worker/` | Worker accept/execute/receipt logic |
| `p2p/verifier/` | Teacher forcing + sampling verification |
| `p2p/proposer/` | `BatchSettlement` construction |
| `p2p/inference/` | Inference request handling |
| `p2p/store/` | Local data store (7-day retention) |

---

## L3 -- SDK

The SDK is a pure client-side library that sits between the user application and the P2P network:

- **Timeout and price suggestions** -- estimates cost and latency based on current network state.
- **Streaming display** -- handles token-by-token rendering for chat interfaces.
- **Model name translation** -- maps human-readable model names to on-chain `model_id` hashes.
- **Privacy** -- PII scrubbing before prompts leave the client, optional Tor routing, TLS encryption.
- **Auto-retry** -- transparent retry on Worker timeout or dispatch failure.

### SDK Components

| Component | Purpose |
|-----------|---------|
| `sdk/privacy/` | PII scrubbing, Tor integration, TLS |

---

## Related Pages

- [Settlement State Machine](settlement.md) -- how tasks move from VERIFIED to payout
- [VRF Unified Formula](vrf.md) -- the single formula behind dispatch, verification, second verification, and leader election
- [Schema Reference](schema.md) -- protobuf message definitions
