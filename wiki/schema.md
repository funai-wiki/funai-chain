# FunAI Chain Wiki — Schema

## Purpose

This wiki is an LLM-maintained knowledge base for the FunAI Chain project. It sits between raw source documents (`docs/`) and the user, providing structured, interlinked, and continuously updated summaries. The LLM writes and maintains all pages; humans curate sources and direct analysis.

## Three Layers

1. **Raw sources** (`docs/`) — Immutable design specs, second verification reports, test plans. The LLM reads but never modifies these.
2. **The wiki** (`wiki/`) — LLM-generated markdown pages. Summaries, concept pages, status trackers, cross-references. The LLM owns this layer entirely.
3. **The schema** (this file) — Conventions, structure, and workflows.

## Directory Structure

```
wiki/
  schema.md            — This file (conventions and rules)
  index.md             — Content-oriented page catalog
  log.md               — Chronological operations log

  # Core concepts
  architecture.md      — Three-layer architecture overview
  settlement.md        — Settlement state machine and fee distribution
  vrf.md               — VRF unified formula and all use cases
  verification.md      — Teacher forcing, logits, deterministic sampling
  tokenomics.md        — FAI token, supply, rewards, halving
  jail-and-slashing.md — Jail escalation, fraud proofs, penalties
  model-registry.md    — model_id, activation, running thresholds
  p2p-layer.md         — Leader election, dispatch, failover
  overspend-protection.md — Three layers of balance protection

  # Components and features
  evm-integration.md   — Cosmos EVM, JSON-RPC, precompiles
  sdk.md               — Client SDK, OpenClaw integration
  per-token-billing.md — S9 per-token billing, anti-cheat
  msg-types.md         — All on-chain message types
  parameters.md        — All on-chain parameters with defaults

  # Operations and status
  security-second verification.md    — Second verification findings and fix status
  code-review.md       — Spec vs implementation gaps
  test-status.md       — Test plan coverage and results
  testnet.md           — Testnet configuration and join guide
  operations.md        — Deployment, monitoring, troubleshooting
```

## Page Conventions

- **Frontmatter:** Each page starts with `# Title` followed by a one-line summary and `Sources: [list]`.
- **Cross-references:** Use `[link text](other-page.md)` for wiki-internal links. Use `[link text](../docs/file.md)` for raw source references.
- **Tables over prose:** Prefer tables for parameters, comparisons, and enumerations.
- **Numbers are exact:** Never round or approximate protocol parameters. Copy exact values from source.
- **Status indicators:** Use `FIXED`, `OPEN`, `DEFERRED`, `NOT A BUG` for second verification/review items.
- **No opinions:** Wiki pages state facts from sources. Flag contradictions explicitly rather than resolving them silently.

## Workflows

### Ingest

When a new source document is added to `docs/`:
1. Read the source fully.
2. Write a summary and extract key facts.
3. Update or create relevant wiki pages.
4. Update `index.md` with any new pages.
5. Append an entry to `log.md`.

### Query

When answering questions against the wiki:
1. Read `index.md` to locate relevant pages.
2. Read those pages and synthesize an answer.
3. If the answer reveals a gap or new insight, file it back as a wiki page update.

### Lint

Periodic health check:
1. Look for contradictions between pages.
2. Identify stale claims superseded by newer sources.
3. Find orphan pages with no inbound links.
4. Note important concepts lacking their own page.
5. Check for missing cross-references.

## Source Document Inventory

| Source | Type | Lines | Key Content |
|--------|------|-------|-------------|
| `docs/protocol/FunAI_V52_Final.md` | Spec | 1234 | Primary architecture specification |
| `docs/protocol/FunAI_V52_Supplement.md` | Spec | 669 | S1-S9 supplements, concurrency, recovery |
| `docs/protocol/S9_PerToken_Billing_Supplement.md` | Spec | 948 | Per-token billing full spec |
| `docs/protocol/S9_PerToken_Billing_Revised_KT_2.md` | Spec | 948 | Revised per-token billing |
| `docs/integration/FunAI_SDK_OpenClaw_Integration_Spec.md` | Spec | 932 | SDK design, OpenAI compatibility |
| `docs/integration/FunAI_CosmosEVM_Integration_KT.md` | Spec | 408 | EVM integration guide |
| `docs/internal/FunAI_Security_Second verification_Findings_KT.md` | Second verification | 317 | A1-A7 security findings (internal) |
| `docs/internal/funai-chain-review.md` | Review | 262 | Spec vs code compliance (internal) |
| `docs/internal/Dispatch_Second verification_Fix_Checklist.md` | Second verification | 471 | Dispatch code second verification (internal) |
| `docs/internal/FunAI_Dispatch_Second verification_Fixes_KT_1.md` | Second verification | 471 | Dispatch fix checklist (internal) |
| `docs/testing/FunAI_Integration_Test_Plan_V3.md` | Test | 503 | 142 integration test cases |
| `docs/testing/FunAI_Test_Execution_Plan_KT.md` | Test | 597 | 227 test scenarios across 6 layers |
| `docs/testing/T4_E2E_Test_Plan.md` | Test | 475 | T4 GPU E2E plan |
| `docs/testing/FunAI_Test_Plan_Review.md` | Test | 241 | Spec vs implementation gaps |
| `docs/testing/Pre_Mainnet_Test_Plan.md` | Test | 275 | Pre-mainnet synthesis: P0 / P1 / P2 with decision gates (2026-04-27) |
| `docs/testing/FunAI_V6_Byzantine_Test_Plan_KT.md` | Test | 192 | KT V6 penalty-path fuzzing plan: 30 scenarios + 7 invariants (2026-04-27) |
| `docs/testing/Test_Plan_Execution_Status.md` | Test | 125 | Per-plan execution dashboard: 0 fully run / 4 partial / 2 not started; 12-slice priority list (2026-04-27) |
| `docs/internal/ops-runbook.md` | Ops | 319 | Deployment, env vars, monitoring (internal) |
| `docs/guides/Join_Testnet.md` | Guide | 350 | Testnet join guide |
| `docs/guides/Phase4_Full_Network_Guide.md` | Guide | 159 | Multi-node network guide |
| `docs/guides/SDK_Developer_Guide.md` | Guide | — | SDK API reference with code examples |
| `docs/guides/Worker_Operator_Guide.md` | Guide | — | Worker setup, staking, reputation |
| `docs/guides/Validator_Guide.md` | Guide | — | Validator committee, block rewards |
| `p2p/README.md` | Readme | 55 | P2P layer overview |
| `sdk/README.md` | Readme | 45 | SDK overview |
| `CLAUDE.md` | Config | 184 | Project guide for LLM |
