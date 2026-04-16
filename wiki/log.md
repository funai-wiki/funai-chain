# FunAI Chain Wiki — Operations Log

## [2026-04-16] update | Sync wiki pages with merged fixes and current code

**Operator:** Claude (LLM)

**Wiki pages updated (7):**
- `wiki/code-review.md` — Moved P0-1, P0-2, P0-3 to Previously Fixed (all merged). Moved P1-3, P1-4 to Previously Fixed. 3 open P1 remain (P1-1, P1-2, P1-5). Updated Priority Summary.
- `wiki/testnet.md` — Chain ID updated from `funai_333-1` to `funai-testnet-1`, EVM Chain ID from `333` to `123123123`.
- `wiki/settlement.md` — Fee distribution updated to match code: Executor 85% (850/1000), Verifiers 12% (120/1000), Audit fund 3% (30/1000).
- `wiki/sdk.md` — Added note about SDK spec path relocation to `docs/integration/`.
- `wiki/operations.md` — EVM chain ID updated to `123123123`, recovery chain-id updated to `funai_123123123-3`.
- `wiki/index.md` — Updated summaries for code-review, settlement, EVM, and testnet entries.
- `wiki/log.md` — This entry.

**Notes:** Fee ratios verified against `x/settlement/types/params.go` defaults (850/120/30 per-mille). P0 fixes confirmed in commits `335618d` (P0-1+P0-2 TGI v3 top_tokens parsing) and `3840189` (P1-3 AssignTask sig + Worker concurrency).

---

## [2026-04-16] ingest | Add FunAI Whitepaper

**Operator:** Claude (LLM)

**Sources ingested (1):**
- `docs/FunAI_Whitepaper.md` — Public whitepaper (566 lines, 14 sections)

**Wiki pages updated (1):**
- `wiki/index.md` — Added Whitepaper section at top

---

## [2026-04-16] ingest | Add 3 new guides (SDK, Worker, Validator)

**Operator:** Claude (LLM)

**Sources ingested (3):**
- `docs/guides/SDK_Developer_Guide.md` — Full SDK API reference with code examples, privacy modes, error handling
- `docs/guides/Worker_Operator_Guide.md` — Worker setup, registration, staking, GPU config, model management, reputation, penalties
- `docs/guides/Validator_Guide.md` — VRF committee selection, block rewards, staking, governance

**Wiki pages updated (3):**
- `wiki/sdk.md` — Added SDK Developer Guide as source, added privacy mode details and related pages
- `wiki/operations.md` — Added links to Worker Operator Guide and Validator Guide
- `wiki/index.md` — Added 3 new guide entries to Operations & Status section

**Notes:** These guides fill documentation gaps identified during public release review. docs/ reorganized into protocol/, integration/, testing/, guides/, internal/ subdirectories.

---

## [2026-04-05] ingest | Initial wiki build from 20 source documents

**Operator:** Claude (LLM)

**Sources ingested (20):**
- `docs/FunAI_V52_Final.md` (1234 lines) — Primary architecture spec
- `docs/FunAI_V52_Supplement.md` (669 lines) — S1-S9 supplements
- `docs/S9_PerToken_Billing_Supplement.md` (948 lines) — Per-token billing
- `docs/S9_PerToken_Billing_Revised_KT_2.md` (948 lines) — Revised billing
- `docs/FunAI_SDK_OpenClaw_Integration_Spec.md` (932 lines) — SDK spec
- `docs/FunAI_CosmosEVM_Integration_KT.md` (408 lines) — EVM integration
- `docs/FunAI_Security_Audit_Findings_KT.md` (317 lines) — Security audit
- `docs/funai-chain-review.md` (262 lines) — Code review
- `docs/Dispatch_Audit_Fix_Checklist.md` (471 lines) — Dispatch audit
- `docs/FunAI_Dispatch_Audit_Fixes_KT_1.md` (471 lines) — Dispatch fixes
- `docs/FunAI_Integration_Test_Plan_V3.md` (503 lines) — 142 test cases
- `docs/FunAI_Test_Execution_Plan_KT.md` (597 lines) — 227 scenarios
- `docs/T4_E2E_Test_Plan.md` (475 lines) — T4 GPU testing
- `docs/FunAI_Test_Plan_Review.md` (241 lines) — Review gaps
- `docs/ops-runbook.md` (319 lines) — Operations runbook
- `docs/Join_Testnet.md` (350 lines) — Testnet join guide
- `docs/Phase4_Full_Network_Guide.md` (159 lines) — Multi-node guide
- `p2p/README.md` (55 lines) — P2P overview
- `sdk/README.md` (45 lines) — SDK overview
- `CLAUDE.md` (184 lines) — Project guide

**Wiki pages created (19):**
- 9 core concept pages: architecture, settlement, vrf, verification, tokenomics, jail-and-slashing, model-registry, p2p-layer, overspend-protection
- 5 component pages: evm-integration, sdk, per-token-billing, msg-types, parameters
- 5 operations pages: security-audit, code-review, test-status, testnet, operations
- Plus: schema.md, index.md, log.md

**Key findings during ingest:**
- 3 open P0 blockers in code review (sampling pipeline, ChaCha20, sig verification)
- Security audit A1-A7 mostly resolved; A7 (proposer non-inclusion) acknowledged as known limitation
- Test coverage at 73/85 (86%) with 4 unimplemented scenarios
- Per-token billing (S9) spec is comprehensive but governance flag is currently `false`
