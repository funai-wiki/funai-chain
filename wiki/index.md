# FunAI Chain Wiki — Index

> LLM-maintained knowledge base. 25 source documents ingested, 19 wiki pages generated.
> Last updated: 2026-04-20

## Core Concepts

| Page | Summary | Sources |
|------|---------|---------|
| [Three-Layer Architecture](architecture.md) | L1 (Cosmos chain), L2 (libp2p P2P), L3 (SDK). Five first principles. Lightning scheme. | V52_Final |
| [Settlement State Machine](settlement.md) | VERIFIED -> CLEARED/PENDING_AUDIT/PENDING_REAUDIT. Fee distribution (85/12/3). Batch processing. | V52_Final |
| [VRF Unified Formula](vrf.md) | score = hash(seed \|\| pubkey) / stake^α. Six use cases with α values 0.0-1.0. | V52_Final |
| [Verification Protocol](verification.md) | Teacher forcing (~0.6s), logits check (4/5), deterministic sampling (ChaCha20, float32). | V52_Final |
| [Token Economics](tokenomics.md) | $FAI, 210B supply, 4000 FAI/block, halving ~4.16yr, 99%/1% reward split. | V52_Final |
| [Jail & Slashing](jail-and-slashing.md) | 3-level progressive jail (10min/1hr/permanent). 50-task reset. FraudProof = instant tombstone. | V52_Final |
| [Model Registry](model-registry.md) | model_id = SHA256(weights\|\|quant\|\|runtime). Activation: 2/3 stake + 4 workers + 4 operators. | V52_Final |
| [P2P Layer](p2p-layer.md) | Leader election (30s epoch), dispatch (100ms cycle), failover (1.5s), sub-topic splitting. | V52_Final, p2p/README |
| [Overspend Protection](overspend-protection.md) | Three layers: Leader tracking, Worker 3x check, on-chain REFUNDED fallback. | V52_Final, S9_Billing |

## Components & Features

| Page | Summary | Sources |
|------|---------|---------|
| [EVM Integration](evm-integration.md) | Cosmos EVM, Chain ID 123123123, JSON-RPC :8545, precompile bridge at 0x...0900. | CosmosEVM_KT |
| [Client SDK](sdk.md) | OpenAI-compatible API, function calling, JSON mode, streaming, auto-pricing, privacy. | SDK_OpenClaw_Spec, SDK_Developer_Guide |
| [Per-Token Billing (S9)](per-token-billing.md) | Shadow balance, Worker truncation, two-party cross-verification, anti-cheat (C1/C2/C3). | S9_Billing |
| [On-Chain Message Types](msg-types.md) | All 11 Msg types: Deposit, Withdraw, RegisterWorker, BatchSettlement, FraudProof, etc. | V52_Final |
| [On-Chain Parameters](parameters.md) | Complete parameter reference by module (Settlement, Worker, Reward, ModelReg, VRF). | V52_Final, S9_Billing |

## Operations & Status

| Page | Summary | Sources |
|------|---------|---------|
| [Security Second verification](security-second verification.md) | A1-A7 findings (A1 FIXED, A4 VERIFIED, A7 acknowledged). Dispatch second verification D1-D4. | Security_Second verification_KT, Dispatch_Second verification |
| [Code vs Spec Compliance](code-review.md) | All P0+P1 fixed (P0-6 partial). Remaining: 12 P2, 4 P3. | funai-chain-review |
| [Test Plan Status](test-status.md) | 227 scenarios across 6 layers. 73/85 implemented. P0 blockers: E14, S4. TPS + logits plan (C0–C4, 5-layer TPS). **C0 FAIL 2026-04-20** — batched logits drift from single-request; all downstream tests paused. **Pre-mainnet test plan + KT V6 Byzantine plan added 2026-04-27** with 30 fuzz scenarios + 7 invariants. Flags one open spec contradiction (`jail_count` reset 50 vs decay 1000). | Test plans (7 docs) + C0 report |
| [Pre-Launch Final Audit](../docs/protocol/FunAI_PreLaunch_Final_Audit_KT.md) | 12 protocol-level decisions (jms/KT, 2026-04-14) required before mainnet: rank 10→21, top-p, Reputation, AssignTask fields, latency-weighted VRF, long-tail model gates, 48h retention, 85/12/3 distribution, weights-hash in model_id, balance-check-first in Leader. Effort: 2.5–3 weeks. | PreLaunch_Audit_KT |
| [Leader Reputation Design](../docs/protocol/FunAI_Leader_Reputation_Design.md) | P2 post-launch design (2026-04-18): Leader-specific reputation score folded into VRF election formula alongside stake. Independent from inference ReputationScore. Three automatic keeper-side detection scenarios (idle epoch / repeated failover / illegal rank skip), no new Msg types. Effort: 200–300 lines across 3 modules, 7-phase plan. | Leader_Reputation_Design |
| [P1: AvgLatencyMs Self-Report Fix](../docs/protocol/P1_AvgLatencyMs_SelfReport_Bug_KT_1.md) | P1 bug (2026-04-20, KT): Worker self-measures `inferMs`, signs it into receipt, chain consumes for VRF speed ranking. Signature defeats MITM but not self-forgery; exploit yields up to 1.5× dispatch boost. Fix: replace with Proposer-recorded `AcceptedAtMs` and `ReceiptAtMs`. Translation added §7 flagging 5 open gaps — notably `AcceptTask` has no timestamp so Worker can compress by delaying AcceptTask; the implementation PR anchors on Proposer's own wall-clock at AssignTask observation, avoiding AssignTask field / SigDigest changes. | P1_AvgLatencyMs_SelfReport_Bug_KT_1 |
| [Testnet Configuration](testnet.md) | Chain ID funai-testnet-1, seed 34.87.21.99, TGI 34.143.145.204:8080. 11-step join guide. | Join_Testnet, ops-runbook |
| [Operations Runbook](operations.md) | Env vars, monitoring metrics, troubleshooting, deployment, emergency procedures. | ops-runbook, Phase4_Guide |
| [Worker Operator Guide](../docs/guides/Worker_Operator_Guide.md) | Setup, registration, staking, GPU config, model management, reputation, penalties. | Worker_Operator_Guide |
| [Validator Guide](../docs/guides/Validator_Guide.md) | VRF committee selection, block rewards, staking, governance. | Validator_Guide |
| [SDK Developer Guide](../docs/guides/SDK_Developer_Guide.md) | Full SDK API reference with code examples, privacy, error handling. | SDK_Developer_Guide |

## Meta

| Page | Summary |
|------|---------|
| [Schema](schema.md) | Wiki conventions, structure, workflows (ingest/query/lint), source inventory. |
| [Operations Log](log.md) | Chronological record of wiki operations. |
