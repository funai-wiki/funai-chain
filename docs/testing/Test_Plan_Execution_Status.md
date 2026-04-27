# Test Plan Execution Status

| | |
|---|---|
| **Last updated** | 2026-04-27 |
| **Purpose** | Dashboard of which test plans have been run, partially run, or not started. Companion to [`Pre_Mainnet_Test_Plan.md`](Pre_Mainnet_Test_Plan.md) (which orders the *unstarted* work) and the [reports/](reports/) directory (which archives evidence). |
| **Update cadence** | Bump after every test execution that produces a new `reports/` entry, and any time a plan changes scope. |

---

## 1. Headline

| Metric | Value |
|---|---|
| Plan documents tracked | 7 |
| Fully executed (with archived report) | **0** |
| Partially executed | 4 |
| Not started | 2 |
| Meta / non-executable | 1 |
| Execution reports in `docs/testing/reports/` | 3 |

No single plan is fully green. Two plans landed only this week and have zero execution against them.

---

## 2. Per-plan status

| Plan document | Status | Coverage today | Outstanding work |
|---|---|---|---|
| [`FunAI_TPS_Logits_Test_Plan_KT.md`](FunAI_TPS_Logits_Test_Plan_KT.md) | ⚠ partial | **C0 executed → FAIL** ([report](reports/2026-04-20-1329-c0-fail/report.md), 2026-04-20). C1-C4 + 5-layer TPS paused on the C0 blocker. | Same-hardware bit-exactness (C1), cross-hardware tolerance 4090 vs A100 (C2), FP16 vs INT4 inverse check (C3), TGI v2 vs v3 mixability (C4); 5-layer TPS (single-GPU tok/s, end-to-end pipeline, Leader knee, gossipsub, BatchSettlement gas) |
| [`T4_E2E_Test_Plan.md`](T4_E2E_Test_Plan.md) | ⚠ partial | Phase 1 happy path covered by RunPod 4090 run ([report](reports/2026-04-23-1047-e2e-real-runpod-4090/report.md), 2026-04-23) on Qwen2.5-0.5B and 7B — same model class, different hardware. | Phase 2 (OpenClaw integration, JSON mode, function calling, multi-turn, insufficient-balance, concurrent), Phase 3 (OC1–OC8 OpenClaw skill matrix), Phase 4 (7B upgrade + 1-hour soak + throughput stress) |
| [`FunAI_Integration_Test_Plan_V3.md`](FunAI_Integration_Test_Plan_V3.md) | ⚠ partial | 142 cases — most have direct unit-test coverage in `x/` and `p2p/`. No full integration run report archived. | Run the 142-case integration suite end-to-end on a 4-node testnet; archive the full report in `reports/`. |
| [`FunAI_Test_Execution_Plan_KT.md`](FunAI_Test_Execution_Plan_KT.md) | ⚠ partial | 227 scenarios across 6 layers. Wiki tracks 73/85 implemented at unit level; **P0 blockers E14 (logits guard) and S4 (Leader signature) carried open historically** — confirm current status. | Land E14 / S4 fixes if not yet done; close the remaining ~12 P2 + 4 P3 unimplemented scenarios; produce a 6-layer execution roll-up report. |
| [`FunAI_V6_Byzantine_Test_Plan_KT.md`](FunAI_V6_Byzantine_Test_Plan_KT.md) | ❌ not started | Doc landed 2026-04-27. Zero scenarios implemented. The previously-flagged `jail_count` 50-vs-1000 contradiction is now RESOLVED — KT confirmed 1000 decay-by-1 canonical, code landed (worker keeper + params + tests + docs). Fuzzer can now be implemented with confidence. | Implement 30-scenario Go fuzzer (testing.F or hand-rolled harness); wire `make test-byzantine-quick` (100 rounds per scenario, every PR) and `make test-byzantine-full` (10 000 rounds, nightly). |
| [`Pre_Mainnet_Test_Plan.md`](Pre_Mainnet_Test_Plan.md) | ❌ not started | Doc landed 2026-04-27. Synthesis only — itself does not execute. | Drive each P0 child item (§2.1 V6 production-engine validation, §2.2 cross-hardware A2, §2.3 Verifier economics modelling, §2.4 Byzantine scenarios, §2.5 FAIL-path economic invariants, §2.6 disaster-recovery rehearsal, §2.7 Day-0 concurrency) to its own report under `reports/`. P1 set after that. |
| [`FunAI_Test_Plan_Review.md`](FunAI_Test_Plan_Review.md) | — meta | Gap-analysis only; not directly executable. | n/a — re-read on each planning cycle to make sure the gap list it identifies has been folded into the active plans. |

---

## 3. Execution report inventory

Reports archived in `docs/testing/reports/`:

| Report | Date | Verdict |
|---|---|---|
| [`2026-04-20-1329-c0-fail/`](reports/2026-04-20-1329-c0-fail/report.md) | 2026-04-20 | **FAIL** — TGI 3.3.6 continuous batching perturbs logits (max relative error 2.27 × 10⁻², > 20× the FAIL threshold). Mitigation: Option B (Worker dedicated single-request teacher-force pass) adopted; Option C `--max-batch-prefill-tokens=1024` recommended for defence in depth. |
| [`2026-04-21-v6-phase1a/`](reports/2026-04-21-v6-phase1a/SUMMARY.md) | 2026-04-21 | **PASS (single-machine A1)** — V6 batch-replay protocol PoC on HuggingFace transformers; 26/26 cases bit-exact (`max_abs_err == 0.0`); cross-hardware A2 still open. |
| [`2026-04-23-1047-e2e-real-runpod-4090/`](reports/2026-04-23-1047-e2e-real-runpod-4090/report.md) | 2026-04-23 | **PASS (Run 1c + Run 2)** — full chain + 4 P2P + SDK + RunPod TGI E2E on Qwen2.5-0.5B and 7B. 20/20 assertions, 4 `BatchSettlement` tx broadcast per task. §14 lists follow-up work that the [`Pre_Mainnet_Test_Plan.md`](Pre_Mainnet_Test_Plan.md) extends. |

---

## 4. Recommended execution priority

Ordered by "if this fails, mainnet cannot launch as designed":

| # | Slice | Plan / section | Owner suggestion | Effort |
|---|---|---|---|---|
| 1 | V6 production-engine validation (continuous batching, Phase 1c.2 + 1b on TGI/vLLM) | `Pre_Mainnet_Test_Plan.md` §2.1 + `FunAI_TPS_Logits_Test_Plan_KT.md` C0/C4 | V6 track engineer | 2–3 wk |
| 2 | Cross-hardware A2 (4090 → L20 / A100) | `Pre_Mainnet_Test_Plan.md` §2.2 + TPS_Logits C2 | V6 track engineer (cont'd) | 1 wk |
| 3 | Verifier economics modelling | `Pre_Mainnet_Test_Plan.md` §2.3 | analyst | 2 wk |
| 4 | KT V6 Byzantine fuzzer (30 scenarios + 7 invariants) | `FunAI_V6_Byzantine_Test_Plan_KT.md` (all) | chain engineer | 2–3 wk |
| 5 | 5-layer TPS stress | `FunAI_TPS_Logits_Test_Plan_KT.md` TPS section | chain ops | 1 wk |
| 6 | Disaster-recovery rehearsal | `Pre_Mainnet_Test_Plan.md` §2.6 | chain ops | 1 wk |
| 7 | Day-0 concurrency + sub-topic split | `Pre_Mainnet_Test_Plan.md` §2.7 | chain engineer (cont'd) | 1 wk |
| 8 | FAIL-path economic invariants | `Pre_Mainnet_Test_Plan.md` §2.5 | chain engineer (cont'd) | 1 wk |
| 9 | T4 Phase 4 stress + 1-hour soak | `T4_E2E_Test_Plan.md` Phase 4 | SDK / ops | 3 d |
| 10 | T4 Phase 2 + Phase 3 (OpenClaw integration + skill matrix) | `T4_E2E_Test_Plan.md` Phase 2 / 3 | SDK | 1–2 wk |
| 11 | Integration Test Plan V3 — 142 case full run | `FunAI_Integration_Test_Plan_V3.md` | rotating | 1 wk |
| 12 | Test Execution KT — 6-layer roll-up + remaining ~16 unimplemented | `FunAI_Test_Execution_Plan_KT.md` | rotating | 1 wk |

**Headcount estimate**: 4 dedicated owners running items 1–8 in parallel. Wall-clock 6–10 weeks once you account for findings that surface code or spec changes (see decision gates in `Pre_Mainnet_Test_Plan.md` §6).

---

## 5. Known blockers between items

```
[1 V6 production-engine] ──┬─→ [4 V6 Byzantine] (fuzzer is meaningless if engine determinism fails)
                           │
                           ├─→ [2 Cross-hardware A2] (different hardware on same engine)
                           │
                           └─→ [9, 10 T4 Phase 2/3/4] (assumes engine path is decided)

[3 Verifier economics] ──── decides whether 12% allocation needs governance change before mainnet

[6 Disaster recovery] ───── independent; can run in parallel with anything

[5 TPS stress, 7 Day-0, 8 FAIL invariants] ── independent of V6, can be run on current code
```

**Gating chain**: item 1 outcome may pause item 4 or invalidate part of items 9–12; item 3 outcome may force a fee-split governance change before launch; items 6 / 5 / 7 / 8 are not on this gating chain.

---

## 6. How to update this file

When you produce a new `reports/<date>-<slug>/report.md`:

1. Add a row to §3 with date + verdict.
2. Find the matching plan in §2 and update its status / coverage.
3. If the report covers a slice in §4, mark it complete or update the residual.
4. Bump §1 stats and the document `Last updated` date.
5. Append a one-line entry to `wiki/log.md` so the wiki layer stays in sync.

When a new test plan doc lands in `docs/testing/`:

1. Add a row to §2 with status `❌ not started`.
2. Add it to the priority list in §4 if it is mainnet-blocking.
3. Update the headline plan count in §1.
4. Run `bash scripts/wiki-check.sh` and ingest into the wiki layer.

---

## 7. References

- [`Pre_Mainnet_Test_Plan.md`](Pre_Mainnet_Test_Plan.md) — what to test next, ordered P0 / P1 / P2
- [`FunAI_V6_Byzantine_Test_Plan_KT.md`](FunAI_V6_Byzantine_Test_Plan_KT.md) — KT V6 penalty-path fuzzer (30 scenarios + 7 invariants)
- [`reports/`](reports/) — archived execution evidence
- [`wiki/test-status.md`](../../wiki/test-status.md) — wiki summary of test status (for cross-page linking)
