# Test Plan Status

Overview of test planning, execution readiness, and current coverage for FunAI Chain.

## Integration Test Plan V3

Source: [FunAI_Integration_Test_Plan_V3.md](../docs/FunAI_Integration_Test_Plan_V3.md)

**142 test cases** across 22 partitions (A through V). Coverage areas:

- User lifecycle (deposit, withdraw, balance)
- Worker jail/unjail/tombstone
- Settlement normal flow and anomaly paths
- Second verification and third-verification flows
- FraudProof submission and slashing
- Block reward distribution
- Dynamic second verification rates
- Overspend protection (3 layers)
- Model registry (proposal, activation, running thresholds)
- VRF unified formula
- P2P dispatch, leader election, failover
- Worker lifecycle (register, stake, models)
- End-to-end scenarios
- Economic conservation invariants

## Test Execution Plan

Source: [FunAI_Test_Execution_Plan_KT.md](../docs/FunAI_Test_Execution_Plan_KT.md)

**227 total scenarios** across 6 layers. Estimated execution time: ~4.5 hours.

| Layer | Description | Scenarios | Est. Time |
|-------|-------------|-----------|-----------|
| L1 | Chain module tests | 184 | ~15 min |
| L2 | P2P network tests | 10 | ~40 min |
| L3 | Privacy tests | 7 | ~20 min |
| L4 | Security tests | 10 | ~10 min |
| L5 | Performance tests | 7 | ~2 hours |
| L6 | GPU inference tests | 9 | ~1 hour |

New test code needed: ~2,450 lines.

## Test Plan Review

Source: [FunAI_Test_Plan_Review.md](../docs/FunAI_Test_Plan_Review.md). Baseline commit: `aa57082`.

**Implementation status:** 73 of 85 implemented, 8 partial, 4 not implemented.

### P0 Blockers

- **E14:** Verifier all-return-zero -- verifiers return zero logits and pass verification, masking real mismatches.
- **S4:** Worker doesn't verify `AssignTask` signature -- Worker accepts unsigned dispatch from any source.

### P1 Blockers

- **P7:** Key rotation -- no test coverage for rotating P2P or chain keys mid-session.
- **E9-E11:** Insufficient verifier count behavior -- unclear what happens when fewer than 3 verifiers are available.

## T4 E2E Test Plan

Source: [T4_E2E_Test_Plan.md](../docs/T4_E2E_Test_Plan.md)

4-phase end-to-end plan covering single-node, multi-node, adversarial, and performance scenarios.

### Blocking Items

| ID | Description |
|----|-------------|
| B1 | Missing pubsub dispatch loop in `funai-node` |
| B2 | Missing environment variable reading for node configuration |
| B3 | TGI API compatibility layer not implemented |
| B4 | OpenClaw provider integration pending |
| B5 | SDK Python bindings not available |

## TPS Stress + Logits Consistency Test Plan

Source: [FunAI_TPS_Logits_Test_Plan_KT.md](../docs/testing/FunAI_TPS_Logits_Test_Plan_KT.md). Baseline commit: `ce87883`.

Two parallel test tracks on pinned TGI `3.3.6` + Qwen2.5-8B-Instruct FP16.

### Logits consistency (C0–C4)

| ID | Scope | Scale | Pass criterion | Status |
|----|-------|-------|----------------|--------|
| **C0** | Concurrent batching vs single-request logits (⚠ blocking) | 1 GPU, 10 min | `< 1e-6` rel error | **FAIL** 2026-04-20 (A10 / TGI 3.3.6 / Qwen2.5-3B, `rel_err = 2.27×10⁻²`) |
| C1 | Same-hardware bit-exactness | 2 × 4090, 2 hr | 100% identical | paused (C0 blocker) |
| C2 | Cross-hardware tolerance (4090 vs A100) | 4090 + A100, 2 hr | Curve vs prompt length drives `logits_match_threshold` | paused |
| C3 | FP16 vs INT4 must diverge (register as distinct `model_id`) | 1 × 4090 | `> 0.01` rel error (inverse check) | paused |
| C4 | TGI v2 vs v3 mixability | 1 × 4090 | Identical → mixable; diverge → lock version | paused |

**C0 is failing as of 2026-04-20** — batched logits diverge from single-request logits at the first generated position (~2.3% relative), sampled tokens flip from position 1, generation fully diverges by position 2. Single-vs-single is bit-exact; drift is genuinely caused by TGI continuous batching. Two same-session diagnostic runs isolate the root cause: `--max-batch-prefill-tokens` drives the sampling divergence (quartering it eliminates the cascade but leaves ~3% residual logprob drift); attention backend and prefix caching are not the dominant factors. Full report + artifacts: [`docs/testing/reports/2026-04-20-1329-c0-fail/`](../docs/testing/reports/2026-04-20-1329-c0-fail/report.md). Recommended mitigation: Option B (Worker runs a separate single-request forward pass to record bit-exact logits for the 5 VRF positions), optionally paired with Option C (`--max-batch-prefill-tokens=1024`) for defence in depth. C1-C4 and TPS-layer tests are paused pending that architectural change.

### TPS stress (5 layers)

Total network TPS = `min(` layer 1 throughput × GPU count, layer 2 pipeline latency⁻¹, layer 3 Leader ceiling, layer 4 P2P gossipsub ceiling, layer 5 on-chain BatchSettlement ceiling `)`.

| Layer | Scope | Budget |
|-------|-------|--------|
| 1 | Single-GPU tok/s at 1/2/4/8-way concurrency | Local 5090 |
| 2 | End-to-end pipeline t0–t8 timestamps, 4 nodes (Leader + Worker + 2 Verifiers) | 4 × 4090, 2 hr |
| 3 | Leader dispatch knee point (1 → 20 req/s) | 10–20 × 4090, 2 hr |
| 4 | P2P gossipsub propagation at 100 nodes with tc netem 100 ms | 100 × CPU, 3 hr |
| 5 | BatchSettlement gas + time at 1K/5K/10K/40K entries | Local `go test -bench` |

### Execution

4-day timeline, total budget ~$35 on Vast.ai (see doc §3). For teams executing on Alibaba Cloud instead, see `scripts/tgi-bootstrap-aliyun.sh` which provisions a pinned-TGI endpoint on A10 / L20 / A100 class ECS instances in one command.

## Live execution dashboard

[`docs/testing/Test_Plan_Execution_Status.md`](../docs/testing/Test_Plan_Execution_Status.md) — updated 2026-04-27. Tracks per-plan execution status (0 fully run, 4 partial, 2 not started, 1 meta) plus a 12-slice priority list, gating chain, and update protocol. Treat it as the canonical "where are we" view; this wiki page is the conceptual summary.

## V6 PoC Phase 1 — MoE cross-family validation (2026-04-27)

[`docs/testing/reports/2026-04-27-2003-runpod-moe-phase1-rtxpro6000/`](../docs/testing/reports/2026-04-27-2003-runpod-moe-phase1-rtxpro6000/report.md) — first MoE validation of V6 batch-replay PoC on cloud GPU. RunPod RTX PRO 6000 Blackwell 96 GB. Three pytest runs: Qwen2.5-0.5B dense baseline 26/26 PASS; **Qwen1.5-MoE-A2.7B (top-k=4) 9/9 PASS** including expert-routing capture; **DeepSeek-V2-Lite-Chat (top-k=6) 8/9 PASS** — logits bit-exact for all targets, single FAIL is a PoC instrumentation gap (DeepSeek transformers does not expose `output.router_logits` like Mixtral / Qwen do). Net result: V6 batch-replay holds bit-exact on two MoE families with different top-k values; neither Path 1 (gating non-determinism) nor Path 2 (expert internal drift) has fired. Cost ~$2.30. Phi-3.5-MoE 42 B (top-k=2) deferred — 84 GB does not fit the 50 GB pod volume.

## Pre-mainnet test plans (2026-04-27)

Two synthesis docs land the outstanding work between today and mainnet:

| Document | Scope |
|---|---|
| [`docs/testing/Pre_Mainnet_Test_Plan.md`](../docs/testing/Pre_Mainnet_Test_Plan.md) | Cross-track synthesis. P0 (V6 production-engine validation, V6 cross-hardware A2, Verifier economics modelling, Byzantine scenarios, FAIL-path economic invariants, disaster-recovery rehearsal, Day-0 concurrency) / P1 (V6 adversarial + dispatch boundaries, SDK privacy E2E, soak, security regression, penalty mechanism, script polish) / P2 (multimodal, EVM bridge, IBC, wallet integrations, etc.). 4 explicit decision gates at week 2 / 4 / 5. |
| [`docs/testing/FunAI_V6_Byzantine_Test_Plan_KT.md`](../docs/testing/FunAI_V6_Byzantine_Test_Plan_KT.md) | KT-authored V6 penalty-path stress plan. 30 fuzz scenarios across 4 tiers (L1–L5 light, M1–M8 moderate, S1–S6 severe, C1–C10 combined), 7 invariant checks (fee / stake / reputation bounds / state-machine legality / `jail_count` consistency / task uniqueness / `in_flight`), CI hooks `make test-byzantine-quick` per PR + `make test-byzantine-full` nightly. No GPU required (mock logits + mock TGI, same harness as PR #23 e2e-mock). |

Coverage gap: KT's 30 scenarios do not include "Leader signs `AssignTask` but never publishes" — flagged in `Pre_Mainnet_Test_Plan.md` §2.4 as a to-add item.

## Related Pages

- [Security Second verification Findings](security-second verification.md)
- [Code vs Spec Compliance](code-review.md)
- [Settlement](settlement.md)
- [P2P Layer](p2p-layer.md)
