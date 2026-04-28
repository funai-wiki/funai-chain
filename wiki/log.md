# FunAI Chain Wiki — Operations Log

## [2026-04-28] verifier economics simulator + 12% recommendation | §2.3 closed

**Operator:** Claude (LLM)
**Sources ingested:**
- `docs/economics/verifier_economics.py` (new) — 394-line stdlib-only Python simulator; sweeps fee × pool-size M × inference latency T × GPU rental
- `docs/economics/verifier_economics_report.md` (new) — analysis report with KT-review recommendation

**Wiki changes:**
- Created `wiki/verifier-economics.md` — TL;DR + cost model + key findings + break-even table + recommendations
- Updated `wiki/index.md` — added Verifier Economics under Components & Features

**Closes:** Pre_Mainnet_Test_Plan §2.3 (Verifier economics modelling).

**Key takeaway:** Per-task verifier cost is INDEPENDENT of batch size N under uniform-VRF dispatch — the V6 PoC SUMMARY's "tens-of-times single-task inference" cost amplification cancels per task verified. Recommendation: keep `verifier_pool_pct = 12%`; pool size M is dominant cost driver but market-self-regulated. Document the `M × T × GPU$/hr ≤ fee × 432` sustainability inequality in V52 spec; recommend (not blocking) a chain-side out-of-envelope monitor in modelreg.

---

## [2026-04-28] correction + plan extension | MoE coverage gaps from 2026-04-27 review

**Operator:** Claude (LLM)

Track engineer reviewed the 2026-04-27 RunPod MoE report (PR #31, merged) and surfaced both an overclaim and a much broader test-coverage gap list.

**Correction in `2026-04-27-2003-runpod-moe-phase1-rtxpro6000/report.md` (in place, §0 + §4.2 + §5 + §9):**
- The original report's "Phase 1c dynamic-batch composition" claim was overstated. `test_phase1_moe.py` called `run_batch_dynamic` / `replay_dynamic` (the dynamic-batch *API*) but with a schedule of `{tid: (0, 10) for tid}` — every task active for every step, functionally equivalent to **static composition**. The V6-distinctive dynamic property is therefore unverified on MoE by that report.
- Verdict softened from "PASS" to "PASS (narrowly)" — configuration-bounded.
- §9 follow-ups re-prioritised: P0 = true dynamic batch + ChaCha20 / cross-hardware A2 / AWQ quantized; P1 = top-k=2 (Phi-3.5-MoE) / temperature>0 / DeepSeek routing forward-hook.

**Pre-mainnet plan extension (`docs/testing/Pre_Mainnet_Test_Plan.md`):**
- New **§2.8 MoE coverage matrix** — three P0 dimensions still open before any white-paper-grade "MoE 100% precise verification" claim can be made: true dynamic batch + ChaCha20, cross-hardware A2, AWQ/GPTQ quantization. Effort 3–4 days code + 1–2 GPU rentals (~$5–10).
- New **§2.9 Inference determinism boundary conditions** — five P0 items the engineer surfaced beyond MoE: EOS handling under continuous batching, padding strategy, sampling-parameter completeness in BatchLog (repetition / frequency penalties etc.), malicious-oversize-prompt truncation rule, Verifier-precedes-Worker timing-attack chain enforcement. Effort 1 week.
- Two items consciously **moved out** of the test plan into protocol design tracks: Sybil resistance (VRF α design, not testing), same-prompt-twice (UX education, not determinism). Multi-turn cumulative drift downgraded to P2 because if V6 single-turn replay is bit-exact then multi-turn is bit-exact by composition.

**Wiki pages updated:**
- `wiki/log.md` — this entry.
- `docs/testing/Test_Plan_Execution_Status.md` — added 2026-04-28 amendment annotation to the report row.

---

## [2026-04-27] test report | V6 Phase 1 MoE on RunPod RTX PRO 6000 Blackwell

**Operator:** Claude (LLM)

**New source doc ingested:**
- `docs/testing/reports/2026-04-27-2003-runpod-moe-phase1-rtxpro6000/report.md` (~250 lines) — first cloud-GPU MoE validation of the V6 batch-replay PoC (PR #30). Three pytest sessions on a single RunPod RTX PRO 6000 Blackwell 96 GB pod. Qwen2.5-0.5B dense baseline 26/26 PASS in 53 s reproduces the existing Phase 1 result on Blackwell (CC 12.0, first non-Ampere validation). Qwen1.5-MoE-A2.7B (top-k=4) 9/9 PASS in 96 s including expert-routing capture. DeepSeek-V2-Lite-Chat (top-k=6) 8/9 PASS in 387 s — logits bit-exact for all 4 targets, single FAIL is the PoC's `output.router_logits` capture path which DeepSeek does not surface (instrumentation gap, not a V6 protocol issue). Net result: V6 holds bit-exact on two MoE families with different top-k; neither Path 1 (gating non-determinism) nor Path 2 (expert internal drift) has fired.

**Operational findings worth carrying forward:**
1. transformers 5.x's MoE fast path uses `torch._grouped_mm` which is hard-restricted to compute capability 9.0 (Hopper). On Blackwell (CC 12.0), and presumably on Ampere/Ada, MoE forward errors out. Pin `transformers>=4.46,<5` for Blackwell. Already what `requirements.txt` specifies; `pip install transformers` ignored the upper bound.
2. 50 GB pod volume insufficient for any test matrix that includes Phi-3.5-MoE (84 GB) or Mixtral 8x7B (94 GB). For the next rental allocate ≥ 200 GB.
3. DeepSeek-V2's transformers does not expose `model_output.router_logits`; PoC needs a forward-hook based capture path for that family. Logged as follow-up patch on PR #30.

**Wiki pages updated:**
- `wiki/test-status.md` — Added "V6 PoC Phase 1 — MoE cross-family validation (2026-04-27)" section pointing at the new report.
- `wiki/log.md` — This entry.

---

## [2026-04-27] code change | jail_count: 50-reset → 1000-decay-by-1 (KT V6 canonical)

**Operator:** Claude (LLM)

KT confirmed (in response to the contradiction this wiki flagged 2026-04-27 morning) that V6 Byzantine Test Plan's "every 1000 successful tasks decays jail_count by 1" supersedes V5.2's "50 successful tasks resets jail_count to 0". Reason: V5.2 enabled rhythm-cheating — cheat once, behave for 50, jail_count clean again, repeat at constant amortised cost. Linear decay over 1000 makes each additional offence cost a multiple of 1000 honest tasks to recover from.

**Code change**:
- `x/worker/types/params.go` — renamed `DefaultSuccessResetThreshold` → `DefaultJailDecayInterval`, value 50 → 1000; struct field + JSON / proto tag renamed correspondingly.
- `x/worker/keeper/keeper.go` — `IncrementSuccessStreak()` now decays `jail_count` by 1 (floored at 0) on hitting `JailDecayInterval`, and resets `success_streak` to 0 to start counting toward the next decay. Was: reset `jail_count` to 0 + reset `success_streak`.
- `x/settlement/keeper/keeper.go:2112` — comment updated.
- Tests: `keeper_test.go` `TestIncrementSuccessStreak_*` rewritten to cover decay (not reset), floor-at-zero, multi-decay over 2000 tasks. `edge_case_test.go` boundary test updated.

**Wiki pages updated**:
- `wiki/jail-and-slashing.md` — replaced "Jail Counter Reset (50 tasks)" section with "Jail Counter Decay (1000 tasks per −1)"; removed the contradiction marker; updated `success_streak` description.
- `wiki/settlement.md` — bullet point updated.
- `wiki/parameters.md` — `success_reset_threshold` (50) → `jail_decay_interval` (1000); preamble updated.
- `wiki/log.md` — this entry.

**Docs updated**:
- `CLAUDE.md` — line 102 rule updated.
- `docs/testing/Pre_Mainnet_Test_Plan.md` §3.7 — `OPEN` flag flipped to `RESOLVED`.
- `docs/testing/Test_Plan_Execution_Status.md` — V6 Byzantine fuzzer's blocker noted as resolved.

**Not touched** (intentionally):
- `docs/protocol/S9_PerToken_Billing_*.md` — KT-authored protocol docs, mention `success_reset_threshold` in S9 anti-cheat context. The S9 dishonest-count reset is a separate mechanism that still ties to consecutive successes; behaviour change there is inadvertently positive (more frequent idempotent resets) and does not require a doc update.
- `docs/testing/FunAI_Test_Plan_Review.md:45` — references AC6's "50 consecutive successes" which is the S9 mechanism, unchanged by this PR.

---

## [2026-04-27] ingest | Test plan execution status dashboard

**Operator:** Claude (LLM)

**New source doc ingested:**
- `docs/testing/Test_Plan_Execution_Status.md` (~125 lines) — Per-plan execution state board for the 7 tracked test plan documents. Headline: 0 fully executed, 4 partial, 2 not started, 1 meta. Includes a 12-slice priority list, an explicit gating chain (V6 production-engine validation gates V6 Byzantine fuzzer + cross-hardware + T4 Phase 2/3/4), and an update protocol so future report authors keep the dashboard in sync.

**Wiki pages updated:**
- `wiki/test-status.md` — Added "Live execution dashboard" section pointing at the new doc as the canonical "where are we" view; this wiki page becomes the conceptual summary.
- `wiki/log.md` — This entry.
- `wiki/schema.md` — Added the new doc to source inventory.

---

## [2026-04-27] ingest | Pre-mainnet test plan + KT V6 Byzantine plan

**Operator:** Claude (LLM)

**New source docs ingested:**
- `docs/testing/Pre_Mainnet_Test_Plan.md` (275 lines) — Cross-track synthesis of every testing track still required between today and mainnet, sorted P0 / P1 / P2 with owner + effort estimates and 4 explicit decision gates (week 2 V6 production-engine, week 2 Verifier economics, week 4 Byzantine findings, week 5 SDK privacy / libp2p stress).
- `docs/testing/FunAI_V6_Byzantine_Test_Plan_KT.md` (192 lines) — English translation of KT's V6 penalty-path stress plan (2026-04-27 PDF). 30 fuzz scenarios in 4 tiers (L1–L5 light reputation, M1–M8 moderate jail, S1–S6 severe slash, C1–C10 combined), 7 invariant checks, CI hooks `make test-byzantine-quick` per PR + `make test-byzantine-full` nightly. No GPU needed (mock harness, same as PR #23 e2e-mock).

**Wiki pages updated:**
- `wiki/test-status.md` — New "Pre-mainnet test plans (2026-04-27)" section. Notes one coverage gap: KT's 30 scenarios do not include "Leader signs `AssignTask` but never publishes" (flagged in `Pre_Mainnet_Test_Plan.md` §2.4 as a to-add item).
- `wiki/jail-and-slashing.md` — Annotated the "Jail Counter Reset" subsection with an explicit contradiction marker. V52 spec says "50 consecutive successful tasks → reset to 0"; KT V6 plan tests M7/M8/C1/C2 against "every 1000 successful tasks decays `jail_count` by 1". These rules are not equivalent — Worker with `jail_count=2` reaches "clean" after 50 tasks on V52 but needs 2000 on KT V6. Flagged for human resolution before fuzzer implementation.
- `wiki/index.md` — Test Plan Status row updated to mention 2026-04-27 ingest + jail_count contradiction; source count 5 → 7.
- `wiki/log.md` — This entry.

**Original Chinese source removed per English-only convention:**
- Root-level `FunAI V6 Byzantine Test Plan KT.pdf` — replaced by the English version at `docs/testing/`.

**Key contradictions surfaced:**
1. `jail_count` rehabilitation rule disagrees between V52 (`success_streak == 50 → reset to 0`) and KT V6 plan (`every 1000 → decay by 1`). Spec source of truth must be chosen before any V6 Byzantine fuzzer can produce meaningful PASS/FAIL signals.

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
