# FunAI Chain Wiki — Operations Log

## [2026-04-21 16:05 CST] result | V6 Phase 1 PASS — single-machine replay bit-exact on Qwen2.5-3B

**Operator:** Claude (LLM), dmldevai

**New source doc ingested:**
- `docs/testing/reports/2026-04-21-v6-phase1a/report.md` — Phase 1 result report. Verdict: **PASS** on single machine. All 12 acceptance tests green on Aliyun A10 + Qwen2.5-3B-Instruct + bf16 + eager attention + HuggingFace transformers: Phase 1a (fixed batch, 6/6), Phase 1c.1 (leave-only dynamic batch, 3/3), Phase 1c.2 (join+leave dynamic batch, 3/3). `max_abs_err == 0.0` across ~200 bit-exact Worker-vs-Replay comparisons. V6 A1 claim (an engine can be driven to replay a pre-recorded batch schedule and produce matching logits) is reproducible on this stack.

**Scope boundaries captured in the report:**
- A1 proven; A2 (cross-hardware bit-exactness) still open — Phase 2 needs a second GPU of a different SM architecture (4090 Ada or A100 Ampere-L).
- temperature=0 only; temperature>0 with ChaCha20 seeded sampling is Phase 1b.
- transformers path, not TGI or vLLM — production engine transition is Phase 3+ (2-3 months).
- One historical detour captured: fp16 produced NaN logits on task-p1-002 (Qwen2.5 native bf16 → fp16 overflow in attention softmax); switching to bfloat16 resolved it without compromising the PoC. fp16 failure archive retained at `phase1a-20260421-070618-prefix-nan-fp16/`.

**Wiki pages updated:**
- `wiki/test-status.md` — Logits table: new "V6 Batch-Replay PoC — Phase 1 PASS 2026-04-21" subsection under Logits consistency; the C0 status text trimmed to reference the new PoC as the adopted mitigation path. C1-C4 / TPS tests marked superseded by V6's engine-transition track.
- `wiki/log.md` — This entry.

**Implementation commits on `research/v6-replay-poc`:**
- `06cc89a` Phase 1c dynamic batch composition (recompute-from-scratch Worker + ReplayEngine; 3 test schedules).
- Full chain of 9 commits from scaffold to Phase 1c implementation and bootstrap hardening — see report §9 References.

**Status on current Aliyun A10 ECS (118.31.108.187):**
- Custom-image snapshot in progress at report time (operator-initiated; not released).
- venv + model weights + test code all ready for Phase 1b extension or Phase 2 rsync to a second ECS.

---

## [2026-04-21] update | V6 design note — item #12 Leader-side request bundling

**Operator:** Claude (LLM)

**Change:** Added P0 item #12 "Leader-side Request Bundling" to
`docs/protocol/FunAI_V6_BatchReplay_Design.md`. Item #3 (Batch-mode
Dispatch) updated to defer bundle timing to item #12; the Worker-side
`batch_wait_timeout` bullet removed and replaced with a cross-reference.

**Motivation (operator note, not in source):** Without Leader-side
bundling, single-request dispatch forces Workers to see tasks arriving
one at a time. Worker-side batch_wait_timeout only fills when requests
happen to collide in the Worker's local P2P queue, which almost never
happens at low-to-medium traffic. Result: `batch=1` wins, item #3's
5-10× throughput promise doesn't materialise.

**New behavior:** Leader accumulates eligible requests for up to
`leader_batch_window_ms` (default 500 ms) or until a target Worker's
remaining capacity fills, then dispatches the whole bundle as a single
P2P message containing N tasks. Tight-SLA requests
(`MaxLatencyMs < 2 × leader_batch_window_ms`) bypass the window.

**Secondary benefit:** C2 (adversarial-partner attack) surface shrinks
significantly — Worker has no scheduling freedom over batch
composition. A malicious Leader could still choose adversarial
partners, but Leader is rotating VRF-elected with broadcast-observable
decisions, so sustained manipulation is harder.

**Wiki pages updated:**
- `wiki/index.md` — Updated V6 row: 11 → 12 items; mentions Leader bundling and C2 reduction.
- `wiki/log.md` — This entry.

---

## [2026-04-21] ingest | V6 Batch-Replay design note (English) + PoC scaffold

**Operator:** Claude (LLM)

**New source doc ingested:**
- `docs/protocol/FunAI_V6_BatchReplay_Design.md` (~190 lines) — English translation of the V6 Batch Log-Replay verification proposal. Supersedes the V5.2 verification path in response to the C0 FAIL (2026-04-20). Core idea: Verifier replays the Worker's exact per-step batch composition instead of trying to re-derive logits single-request, reducing the verification claim to "same engine + same schedule + deterministic kernels → bit-exact". 11 items split P0 (replay scheduler, Worker batch log, batch-mode dispatch, settlement lock) / P1 (jail decay 1000, 3-consecutive-miss jail, no-log-= FAIL, Verifier collective punishment, capacity over-statement) / P2 (per-model epsilon unchanged, ChaCha20 restored to 100 % coverage). Deprecates 9 items from earlier drafts including verification proxy, 7 % VRF sampling fallback, top-K rank check, TaskCache, SGLang determinism, hardware-partitioned subnets, clawback.

**Reviewer findings flagged in this ingest (not in the source note):**
- A1 (engineering): TGI and vLLM both lack a "replay this schedule" API; implementation path needs to pick among fork-TGI / fork-vLLM / transformers-based custom runtime.
- A2 (cross-hardware bit-exactness): source note claims "T4/5090 0.000000" from an informal engineer test. Evidence not in the repo. Needs a C0-style report before becoming P0.
- B1 (verifier compute): Verifier replays the entire batch to verify one task → ~48× original cost at batch=16 × 3 verifiers. 85/12/3 fee split can't clear; needs economic-model refresh.
- C1 (log forgery): no mechanism today rejects fabricated partner tasks in a batch log. Critical gap. Proposed fix: every `task_id` in the log must resolve to a real on-chain `InferRequest`.
- C2 (adversarial partners): even with C1 fixed, Worker picking which real tasks to batch can steer target logits. Partial mitigation: Leader-driven batch composition.
- D1: Item 3 "no upper bound on capacity" conflicts with S1's `max_concurrent_tasks` range [1, 32].
- D2: V6's deprecation of the "verification proxy" concept also retires the in-progress Option B design. User decision: V6-only, not V6 + Option B bridging.

**PoC scaffold added (Phase 0, `research/v6-replay-poc` branch):**
- `scripts/v6_replay/README.md` — 3-phase plan, hard PASS / INVESTIGATE / KILL conditions per phase, engine-choice rationale (transformers-based, not TGI/vLLM).
- `scripts/v6_replay/{replay_types.py, worker_simulator.py, replay_engine.py}` — signature-only stubs with determinism contracts documented in docstrings.
- `scripts/v6_replay/{test_phase1.py, test_phase2.py}` — pytest skeletons that fail with `NotImplementedError` until Phase 1 lands; serve as executable acceptance criteria.
- `scripts/v6_replay/requirements.txt`, `__init__.py`.

**Wiki pages updated:**
- `wiki/index.md` — Added row under Operations & Status; header bumped to 26 sources / 2026-04-21.
- `wiki/log.md` — This entry.

**Superseded (removed from working tree, never merged):**
- `docs/protocol/FunAI_V52_Option_B_Verification_Logits.md` — Option B single-request teacher-forcing path. V6 batch-replay chosen as the single verification scheme going forward.

**Phase 0 gate:** PoC scaffolding exists, tests fail cleanly with `NotImplementedError`, determinism contracts documented. Phase 1 (2–4 weeks) begins once compute is provisioned and the engine-route selection is confirmed.

---

## [2026-04-20] ingest + impl | P1 AvgLatencyMs self-report bug (KT)

**Operator:** Claude (LLM)

**New source doc ingested:**
- `docs/protocol/P1_AvgLatencyMs_SelfReport_Bug_KT_1.md` (~240 lines) — English translation of KT's P1 vulnerability note. Worker self-measures `inferMs` at `p2p/worker/worker.go:383`, signs it into `InferReceipt.InferenceLatencyMs`, chain consumes it at `x/settlement/keeper/keeper.go:1027` → `UpdateAvgLatency()` EMA → VRF `rankSpeedMultiplier`. The secp256k1 signature defeats MITM but not self-forgery. Exploit: malicious Worker hardcodes `inferMs = 50` (truth ~3000), wins ~50 % more dispatch. Fix: replace with Proposer-recorded `AcceptedAtMs` and `ReceiptAtMs` on `SettlementEntry`, compute `SettlementLatencyMs = ReceiptAtMs - AcceptedAtMs` on-chain. **Translation added review §7 "Known limitations"** flagging 5 gaps: (7.1) `AcceptTask` has no timestamp field → Worker can still compress the window by delaying `AcceptTask`; the implementation chose to anchor on the Proposer's own wall-clock at AssignTask observation instead of adding fields to AssignTask, keeping `SigDigest` untouched. (7.2) Leader vs Proposer observation point — resolved in the implementation by letting every node's dispatch loop notify the Proposer on AssignTask. (7.3) Cross-Proposer cross-validation is aspirational, not implemented. (7.4) Per-`model_id` physical floor assumes `x/modelreg` tracks latency stats — not today. (7.5) Wall-clock skew noise <50 ms.

**Implementation included in this PR:**
- `x/settlement/types/settlement.go` — new `AcceptedAtMs` and `ReceiptAtMs` proto fields on `SettlementEntry` (tags 20/21).
- `p2p/proposer/proposer.go` — `TaskEvidence.AcceptedAtMs`; new `OnAssignTask(taskId)` hook; `BuildBatch` now computes `LatencyMs = ReceivedAt - AcceptedAtMs` and populates both new entry fields; deletes the old self-reported path.
- `p2p/dispatch.go` — `handleAssignTask` notifies `n.Proposer.OnAssignTask(task.TaskId)` on every observed dispatch.
- Tests in `p2p/proposer/` covering happy path, reversed-timestamps anomaly, and "Worker self-report no longer drives the latency update".

**Wiki pages updated:**
- `wiki/index.md` — Added row under Operations & Status; header bumped to 25 sources / 2026-04-20.
- `wiki/vrf.md` — Annotated `latency_factor` section with a pointer to the fix.
- `wiki/log.md` — This entry.

**Original Chinese source removed per English-only convention:**
- Root-level `P1_AvgLatencyMs_SelfReport_Bug_KT_1.md` (Chinese) — replaced by the English version at `docs/protocol/`.

---

## [2026-04-20] ingest | C0 first-run result report (FAIL)

**Operator:** Claude (LLM)

**New source docs ingested:**
- `docs/testing/reports/2026-04-20-1329-c0-fail/report.md` (~320 lines) — First C0 execution: Aliyun Hangzhou gn7i-c8g1.2xlarge (A10 24GB), TGI 3.3.6, Qwen2.5-3B-Instruct FP16 (substituted for 8B baseline — current HF mirror blocks Qwen2.5-8B, see §2.1 of the report). Verdict: **FAIL**. `max_rel_err = 2.27×10⁻²` at generated position 0, >20× the `1e-3` FAIL threshold. Sampled tokens flip from position 1, generation fully diverges by position 2. Single-vs-single is bit-exact (§3.1 sanity), so drift is genuinely from TGI continuous batching — no ε rescues it. Report recommends Mitigation Option B: Worker runs a separate single-request forward pass to capture the 5 VRF-position logits for the receipt, keeping Verifier's single-request teacher forcing as-is.
- `docs/testing/reports/2026-04-20-1329-c0-fail/single_response.json`, `batch_response.json`, `verdict.json` — raw TGI responses + stats payload.

**Also in this batch:**
- `scripts/c0-logits-consistency.py` — `extract_positions` fix for TGI 3.x parallel `details.top_tokens` shape (was only reading 2.x nested shape, silently returning empty top-N → false PASS). Plus `--prompt` CLI flag for driving generations past EOS-early stopping.
- `docs/testing/FunAI_TPS_Logits_Test_Plan_KT.md` — §1.3 C0 now links the result report and states the blocker.
- `.gitignore` — adds the `results/` ephemeral output directory.

**Wiki pages updated:**
- `wiki/test-status.md` — Logits table gains a Status column; C0 marked FAIL with the numeric result, C1-C4 and TPS-layer tests marked paused. Added explanatory paragraph with mitigation recommendation and report link.
- `wiki/index.md` — Test Plan Status entry reflects C0 FAIL.
- `wiki/log.md` — This entry.

**Follow-up items (not in this ingest, captured in the report):**
- P1: re-run with `--max-batch-prefill-tokens=1024` to test Mitigation Option C as a diagnostic; re-run against Qwen2.5-8B via ModelScope to confirm on the KT baseline.
- P0: architectural decision on Mitigation Option B + design note for `InferReceipt.verification_logits`.

---

## [2026-04-18] ingest | FunAI_Leader_Reputation_Design.md (English)

**Operator:** Claude (LLM)

**New source doc ingested:**
- `docs/protocol/FunAI_Leader_Reputation_Design.md` (234 lines) — P2 post-launch design for a Leader-specific reputation score, independent from inference ReputationScore. Three automatic keeper-side detection scenarios (idle epoch, repeated failover, illegal VRF rank skip), all handled without new Msg types or Worker self-reporting. Folded into Leader VRF election formula as a multiplier alongside stake. 7-phase implementation plan totalling 200–300 lines across x/worker, x/vrf, x/settlement, p2p/proposer.

**Wiki pages updated:**
- `wiki/index.md` — Added entry in Operations & Status section below the Pre-Launch Audit row.
- `wiki/log.md` — This entry.

**Known issues flagged for the eventual implementation PRs (preserved unchanged in the ingest):**
- Proto tags 25/26 in the struct example collide with `AvgLatencyMs` at tag 25 (PR #10). Implementation should use tags 26/27.
- Scenario 3 (illegal rank skip) reads historical worker list via `GetOnlineWorkersAtBlock`, which the SDK kv-store does not support natively. Options: (A) use current worker list as approximation, (B) epoch snapshot store, (C) skip VRF recompute and only check reject records.
- Missing `EffectiveLeaderReputation()` helper for the uninitialized-worker default-1.0 contract analogous to `EffectiveReputation()`.

---

## [2026-04-17] ingest | FunAI_PreLaunch_Final_Audit_KT.md (English)

**Operator:** Claude (LLM)

**New source doc ingested:**
- `docs/protocol/FunAI_PreLaunch_Final_Audit_KT.md` (532 lines) — Pre-launch final audit by jms (KT), dated 2026-04-14, cataloguing 12 protocol-level decisions that must land before mainnet. Covers rank window 10→21, top-p sampling, Reputation mechanism, AssignTask field extension, latency-weighted VRF, long-tail model activation gates, 48h data retention, 85/12/3 distribution, ComputeModelId + weights hash, Leader balance-check-first, Chain ID finalization. Total effort estimate: 2.5–3 weeks. English translation of the earlier Chinese document; same technical content.

**Wiki pages updated:**
- `wiki/index.md` — Added entry in Operations & Status section.
- `wiki/log.md` — This entry.

**Status of the 12 decisions (as of this ingest):**
- **Done:** #1 rank 10→21 (verifier.go:185, proposer.go:174), #2 top-p (InferRequest.TopP), #3 Reputation (ReputationScore + ReputationOnAccept/Miss/DecayAll wired), #4 AssignTask fields (MaxLatencyMs/StreamMode/TopP), #5 latency-weighted VRF (PR #3 integrated LatencyFactor into RankWorkers for all alphas), #6 long-tail gates (CanServe uses ServiceStakeRatio), #7 48h retention (DefaultRetentionDuration), #8 85/12/3 distribution (PR #2), #9 weights-hash in ComputeModelId, #10 Leader balance-check-first (checkBalanceWithPending in HandleRequest).
- **Documentation drift — conflicts with code:** #11 (doc says `funai_333-1`, code has `funai_123123123-3`); #8 weight split inside verifier pool (doc says 80/20, code ships 85/15 via DefaultFeeWeight=0.85); decision H in the appendix still references "5% max_fee" penalty (code is now 15% after PR #2).

---

## [2026-04-17] ingest | FunAI_TPS_Logits_Test_Plan_KT.md + Alibaba Cloud bootstrap script

**Operator:** Claude (LLM)

**New source docs ingested:**
- `docs/testing/FunAI_TPS_Logits_Test_Plan_KT.md` (628 lines) — TPS stress + logits consistency test plan, pinned to TGI 3.3.6 + Qwen2.5-8B-Instruct FP16. English translation of the earlier Chinese plan; same technical content (C0–C4 logits tests, 5-layer TPS tests, Day 0–3 timeline, ~$35 Vast.ai budget).

**New tooling:**
- `scripts/tgi-bootstrap-aliyun.sh` (308 lines) — one-shot setup from a bare Alibaba Cloud ECS GPU instance to a running TGI endpoint with `ghcr.io/huggingface/text-generation-inference:3.3.6` (the exact image the test plan requires). Pulls Qwen2.5-8B-Instruct by default, supports `MODEL=` override for Int4 / other models, uses `hf-mirror.com` for China-friendly downloads.

**Wiki pages updated:**
- `wiki/test-status.md` — Added "TPS Stress + Logits Consistency Test Plan" section with C0–C4 matrix and 5-layer TPS table.
- `wiki/log.md` — This entry.

---

## [2026-04-16] update | Close all P1 issues

**Operator:** Claude (LLM)

**Fixes:**
- P1-1: `decodePubkey()` in `p2p/dispatch.go` now tries base64 before hex, fixing VRF mismatch for Cosmos-style pubkeys
- P1-2: Already fixed — verifier.go hashes actual logits (confirmed by code inspection)
- P1-5: Already fixed — verifier seed = `task_id || result_hash` (confirmed by code inspection)

**Wiki pages updated:**
- `wiki/code-review.md` — All P1 moved to Previously Fixed (19/19). Only P2/P3 remain.
- `wiki/index.md` — Updated code-review summary line.

---

## [2026-04-16] update | Sync wiki pages with merged fixes and current code

**Operator:** Claude (LLM)

**Wiki pages updated (7):**
- `wiki/code-review.md` — Moved P0-1, P0-2, P0-3 to Previously Fixed (all merged). Moved P1-3, P1-4 to Previously Fixed. 3 open P1 remain (P1-1, P1-2, P1-5). Updated Priority Summary.
- `wiki/testnet.md` — Chain ID updated from `funai_333-1` to `funai-testnet-1`, EVM Chain ID from `333` to `123123123`.
- `wiki/settlement.md` — Fee distribution updated to match code: Executor 85% (850/1000), Verifiers 12% (120/1000), Second verification fund 3% (30/1000).
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
- `docs/FunAI_Security_Second verification_Findings_KT.md` (317 lines) — Security second verification
- `docs/funai-chain-review.md` (262 lines) — Code review
- `docs/Dispatch_Second verification_Fix_Checklist.md` (471 lines) — Dispatch second verification
- `docs/FunAI_Dispatch_Second verification_Fixes_KT_1.md` (471 lines) — Dispatch fixes
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
- 5 operations pages: security-second verification, code-review, test-status, testnet, operations
- Plus: schema.md, index.md, log.md

**Key findings during ingest:**
- 3 open P0 blockers in code review (sampling pipeline, ChaCha20, sig verification)
- Security second verification A1-A7 mostly resolved; A7 (proposer non-inclusion) acknowledged as known limitation
- Test coverage at 73/85 (86%) with 4 unimplemented scenarios
- Per-token billing (S9) spec is comprehensive but governance flag is currently `false`
