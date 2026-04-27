# Pre-Mainnet Test Plan

| | |
|---|---|
| **Last updated** | 2026-04-27 10:06 CST (02:06 UTC) |
| **Status** | DRAFT — synthesis of in-flight workstreams; team confirmation pending |
| **Scope** | Everything still required between today and a green light to launch |
| **Out of scope** | Multimodal v1 (deferred per `project_multimodal_gap.md`); EVM bridge dApp testing; IBC; wallet integrations |

---

## 1. What is already done (foundation)

These are landed in `main` and treated as the testing baseline. New work must not regress them.

| Area | Evidence |
|---|---|
| Full E2E real-inference round-trip (chain + 4 P2P + SDK + RunPod TGI) on Qwen2.5-0.5B and 7B | `docs/testing/reports/2026-04-23-1047-e2e-real-runpod-4090/report.md` |
| C0 batching-perturbation FAIL acknowledged + Option B (Worker dedicated teacher-force pass) adopted in code | `docs/testing/reports/2026-04-20-1329-c0-fail/report.md` |
| V6 batch-replay PoC on HuggingFace transformers — 26/26 cases bit-exact, single-machine A1 confirmed | `docs/testing/reports/2026-04-21-v6-phase1a/SUMMARY.md` |
| SDK token-ordering scramble fixed (PR #22); CI runs `test-p2p-e2e-mock` on every PR (PR #23); M7 fraud-detection path repaired and E2E-covered (PR #24); teacher-force fallback parallelised — 17 s → ~1 s on 7B (PR #25) | `git log main` |
| Settlement keeper economic invariants (1M-iteration dust + fee conservation under SUCCESS path) | `x/settlement/keeper/economic_test.go` |
| Worker / Verifier / Settlement signature verification, AssignTask leader-pubkey enforcement, FraudProof H3 sig check | unit tests across `x/`, `p2p/worker/`, `sdk/` |

---

## 2. P0 — must complete before mainnet

A test-suite gap here is, in principle, mainnet-blocking. Listed roughly in dependency order so the early items unblock the later ones.

### 2.1 V6 production-engine validation

The single largest open V6 risk. The PoC ran on HuggingFace transformers (Python, slow but deterministic). Production Workers will run on TGI or vLLM — different attention kernels, different fp16 accumulation order, different schedulers. None of the V6 properties are inherited; they must be re-proven.

| Item | Today | Needs |
|---|---|---|
| Phase 1c.2 (continuous batching, dynamic join + leave) bit-exact replay on TGI | ✗ | Same test set as `scripts/v6_replay/`, but talking to TGI's `/generate` instead of the transformers driver. Probably needs prefix-cache disabled and `--max-batch-prefill-tokens=1024` per C0 §4.4. |
| Phase 1c.2 on vLLM | ✗ | Same, against vLLM's `/v1/completions` `echo=true` path. The PoC's exit criterion (`max_abs_err == 0.0`) carries over. |
| Phase 1b (`temperature > 0`, ChaCha20 deterministic sampling) on TGI **and** vLLM | ✗ | Critical for the "100 % vs 93 %" security claim of V6 over V5.2. Float32 softmax accumulation order has to match across implementations. |
| Engine pinning enforced **on chain** (not only in P2P metadata) | ✗ | Confirm `model_id = SHA256(weights || quant || engine_image)` actually folds the engine binary; if it folds only weights, an attacker can swap engine without changing model_id. |
| Prefix-cache OFF runtime check | ✗ | TGI ships with `prefix-caching=true` by default. Worker must reject startup if it can't disable it. |
| Warmup-state determinism | ✗ | A cold-start GPU and a 100-task-warm GPU may select different cuDNN kernels (autotuning). Test: replay same logs at two warmup states; assert `max_abs_err == 0.0`. |

**Effort**: 2–3 weeks of one V6-track owner. Hardware: one A10/L20/A100 instance for the cross-engine test; one 4090 + one A100 for cross-hardware (see 2.2).

**Blocker rule**: if Phase 1c.2 fails on TGI **and** vLLM, V6 cannot be deployed as designed — Option B (single-request teacher-force per task, the current `tgi.go:395-416` path) becomes the permanent answer and the V6 protocol-layer PRs do not ship.

### 2.2 V6 cross-hardware A2

| Item | Today | Needs |
|---|---|---|
| Worker on RTX 4090, Verifier on L20 / A100, replay Phase 1d test set | private one-off, never archived | Run the full `scripts/v6_replay/` Phase 1d on at least three GPU pairs. Archive logs to `docs/testing/reports/`. |

**Effort**: 1 week, mostly cloud GPU rental. Cost ~¥150–300 across pairs.

### 2.3 Verifier economics modelling

V6 PoC SUMMARY's "open questions" section flagged this and it is **not** a code test — it is an economic simulation that decides whether the 12 % fee allocation can pay Verifiers given the batch-replay cost amplification (~tens of times single-task inference). If the economics fail, the fee split must be renegotiated *before* mainnet, not after.

| Item | Today | Needs |
|---|---|---|
| Verifier net revenue under realistic task densities | ✗ | Synthetic workload simulator; sweep task arrival rate, batch size distribution, GPU rental cost. Output: minimum allocation that keeps ≥ 3 verifiers economically sustainable per model |
| Effect of small-batch vs large-batch on Verifier cost | ✗ | Hint: large batch → log size grows → verifier replay cost grows. Linear, super-linear, sub-linear? |
| Recommendation: keep 12 % or change to N % | ✗ | Output for KT review |

**Effort**: 1–2 weeks of one analyst (not necessarily the V6 engineer). Owns: TBD.

### 2.4 Protocol Byzantine scenarios

Spec covers all of these; **none** are in `tests/e2e/` or `scripts/e2e-mock-*`. Each has bitten production Cosmos chains in the past.

The fully-worked scenario list (30 cases across 4 tiers — Light / Moderate / Severe / Combined — plus 7 invariants and a fuzzing harness spec) lives in **`docs/testing/FunAI_V6_Byzantine_Test_Plan_KT.md`** (KT, 2026-04-27). The bullets below are a flat summary of the most-load-bearing scenarios; the KT plan is the canonical version once landed.

| Scenario | Spec ref | KT plan ID |
|---|---|---|
| Worker accepts task then disappears → 1.5 s inactivity → all peers switch to rank #2 | spec §6.2, V52_Final | M4 |
| Leader signs `AssignTask` but never publishes | dispatch §6.1 | (not in KT plan — gap to add) |
| 3 verifiers collude to vote the same fake `pass=true` | settlement state machine, second verification path | C10 |
| Same task_id replay (user resends after settlement) | "settles once only", V52 §3 | C9 |
| User balance race: balance goes to 0 between dispatch and settle | overspend §S9 / V52 §6 | C8 |
| Task hits `expire_block` mid-flight | hard cap 17280 blocks, V52 §3 | C6, C7 |
| Worker reoffend at the 999/1000-task `jail_count` decay boundary | spec §13 | M7, M8, C1, C2 |
| Verifier liability cascade after second-tier disagreement | V6 design | S3, C5, C10 |

**Effort**: 2–3 weeks for the harness + the KT scenario set. Each KT scenario is ~1 day of test code + invariant assertion. CI variants: `make test-byzantine-quick` per PR (100 rounds), `make test-byzantine-full` nightly (10 000 rounds).

### 2.5 Economic invariants — FAIL path

Existing 1M-iteration dust / fee tests cover SUCCESS only. FAIL path (15 % fee, no Worker payout, possible jail) has never been stressed.

| Item | Today | Needs |
|---|---|---|
| 1M random FAIL tasks → total FAI supply unchanged | ✗ | Mirror `economic_test.go` SUCCESS suite |
| Halving boundary (block 8 760 000) — fees + rewards before / after match expected schedule | ✗ | Synthetic test fast-forwarding chain across boundary |
| 5 % slash from "3rd jail" path vs "FraudProof" path produce identical state transitions | ✗ | Today they share keeper code; test assertion makes the equivalence explicit |
| Multi-verification fund per-epoch payout — `DistributeMultiVerificationFund` matches the deposits in | ✗ | Per-epoch invariant test |

**Effort**: 1–2 weeks.

### 2.6 Disaster recovery / upgrade rehearsal

| Item | Today | Needs |
|---|---|---|
| Genesis import: same input → same `app_hash` deterministically | ✗ | Run on 3 machines, compare hashes |
| 4-validator quorum: lose 1 → still produces blocks; lose 2 → halts; recover lost validators → catches up | ✗ | testnet rehearsal |
| Software upgrade flow: governance proposal → vote → halt at upgrade height → swap binary → resume from same state | ✗ | end-to-end runbook |
| Backup / restore: snapshot state at height H → wipe → restore from snapshot → block production resumes | ✗ | runbook |

**Effort**: 1 week of focused rehearsal.

### 2.7 Day-0 concurrency

| Item | Today | Needs |
|---|---|---|
| 100 / 500 / 1000 concurrent SDK clients on same model | ✗ | Confirm C0-style logits drift does not appear; if it does, V6 is the only fix |
| TPS > 500 → sub-topic split mechanic (V52 §6) | ✗ | Never exercised |
| 100 simultaneous `MsgBatchSettlement` from racing proposers | ✗ | This was already observed (4 proposers, see §13.1 of RunPod report); confirm chain-side dedup actually rejects losers cleanly rather than processing duplicates |

**Effort**: 1 week.

---

## 3. P1 — strongly recommended

Not strict mainnet blockers, but if any one breaks at launch we will eat real reputation damage.

### 3.1 V6 batch log tampering (adversarial)

A Worker submits a hand-crafted batch log that is internally self-consistent but does not match the real run. Verifier replays per the log → logits diverge → on-chain `MsgBatchSettlement` rejects + jail.

**Effort**: 3–5 days. Trivial test scenario; depends on the V6 protocol-layer keeper code being merged.

### 3.2 V6 second-tier verifier connection-liability path

Per V6 design: 1st-tier verifies `pass`, 2nd-tier verifies `fail` → original 3 first-tier verifiers each lose 2 % stake + 0.20 reputation. Currently no integration test exercises the full path; the keeper math is unit-tested in isolation.

**Effort**: 3–5 days.

### 3.3 V6 dispatch-mode boundary tests (PR #20 follow-up)

| Item | Today |
|---|---|
| `batch_capacity + in_flight` race: 100 SDK clients see Worker at capacity-1 and all dispatch to it; only `capacity` accepted, rest go to rank #2 | ✗ |
| `batch_wait_timeout=500ms` boundaries: arrived 1 task at 499ms vs 501ms; arrived `capacity+1` tasks within 500ms (correct rejection) | ✗ |

PR #20 (`feat(v6): per-worker batch capacity — wire + test`) added unit-level happy path; concurrent contention is not covered.

**Effort**: 2–3 days.

### 3.4 SDK privacy modes — real multi-node E2E

`sdk/privacy/` has unit tests for TLS / Tor / Full modes. None has been exercised end-to-end across multiple peers.

| Item | Today |
|---|---|
| TLS X25519 key exchange in 4-node mesh | ✗ |
| Tor SOCKS5 (5-node mesh, one routed through Tor) | ✗ |
| TLSWrap_4KB SLO drift — 04-19-1637 report 4-out-of-6 over 200 µs SLO; either widen SLO to 250 µs or move to a quieter benchmark host | open |

**Effort**: 1 week including Tor setup.

### 3.5 Soak test

72-hour run, 1 req/s, monitor:
- goroutine count (no growth)
- RSS (no leak)
- block production (no degradation)
- TGI memory (separately, since GPU memory bug would burn the run)

**Effort**: 3 days of wall-clock; observability is the main work.

### 3.6 Security regression + pen test

| Item | Today |
|---|---|
| `FunAI_Security_Audit_Findings_KT.md` A1–A7 + Dispatch D1–D4 status confirmed | A1 FIXED / A4 VERIFIED / A7 acknowledged; rest TBD |
| REST :1317, gRPC :9090, JSON-RPC :8545, libp2p ports — basic pen test against malformed input / DoS spam | ✗ |
| Validator key rotation rehearsal | ✗ |

**Effort**: 1 week + audit firm engagement (out-of-house).

### 3.7 Penalty-mechanism coverage

These are V5.2 features that survive into V6, not V6-specific.

| Scenario | Today |
|---|---|
| Sliding window: 10 tasks with 3 misses → 1st jail (10 min) | ✗ |
| 2nd jail (1 hour) trigger | ✗ |
| 3rd jail = permanent + slash 5 % + tombstone | ✗ |
| `jail_count` decay: 1000-task decay-by-1 confirmed canonical (KT, 2026-04-27). V5.2's 50-task reset retired. Code change landed under PR `mainnet-readiness/jail-count-1000-decay`. | RESOLVED |

**Effort**: 3–5 days once spec resolved.

### 3.8 Script preflight + cosmetic clean-ups (`§14.4–§14.7` of RunPod report)

- Add `top_n_tokens=256` probe to `e2e-real-inference.sh` Phase 0 so a 422 fails fast
- Phase 5 grep patterns updated to match current log wording
- Phase 6 queries the real depositing address (not user0)
- `--no-cleanup` writes a `run-config.env` for reproducibility

**Effort**: 1 day total. Cosmetic but each report has these `[WARN]` lines and they're noise that masks real signals.

---

## 4. P2 — post-mainnet acceptable

| Item | Why deferred |
|---|---|
| Multimodal v1 (image / audio / video dispatch) | Architecture-level; v1 scope decision pending per `project_multimodal_gap.md` |
| EVM bridge precompile dApp testing | v1 does not host EVM dApps |
| IBC | not in v1 |
| Wallet integration (Keplr / Cosmostation) | post-launch PRs |
| Multi-language SDK bindings | Go SDK suffices for v1 launch partners |
| Stake delegation | spec marks reserved interface |
| `sonic` Go 1.25 compatibility warning | stderr noise only, no functional impact; vendor patch when convenient |
| `AvgLatencyMs` TTFT / per-output-token split | depends on engine streaming timestamps not yet exposed |
| VRF `MaxLatencyMs` ×1.5 / ×1.0 / ×0.1 soft weight (Audit KT §4.4) | no on-chain function depends on this yet |

---

## 5. Owner / effort summary

Headcount estimate for the P0 + P1 work in parallel:

| Track | Owner | Weeks |
|---|---|---|
| V6 production-engine + cross-hardware (2.1, 2.2) | V6 track engineer | 3–4 |
| Verifier economics (2.3) | analyst (KT? pricing engineer?) | 2 |
| Byzantine scenarios + economic FAIL invariants (2.4, 2.5) | one chain engineer | 4 |
| Disaster recovery + concurrency (2.6, 2.7) | one chain ops engineer | 2 |
| V6 adversarial + dispatch-mode tests (3.1, 3.2, 3.3) | V6 track engineer cont'd | 1–2 |
| SDK privacy + soak + security (3.4, 3.5, 3.6) | one SDK / ops engineer | 2–3 |
| Penalty + script polish (3.7, 3.8) | rotating | 1 |

**Total parallel wall-clock**: 4–6 weeks if 4 owners are dedicated. **Realistic**: 8–10 weeks once you account for findings that turn into spec / code changes.

---

## 6. Decision points that gate the plan

| When | Decision |
|---|---|
| End of Week 2 (V6 production-engine results in) | If TGI **and** vLLM both fail Phase 1c.2 bit-exact, V6 protocol-layer PRs are paused; mainnet ships on Option B teacher-force only. This is a real possibility — C0 §4.4 already showed `max-batch-prefill-tokens` perturbs logits in TGI 3.3.6, and that's the same subsystem V6 depends on. |
| End of Week 2 (Verifier economics results in) | If 12 % allocation is insufficient, fee split needs a governance change before mainnet — not after. |
| End of Week 4 (Byzantine + economic invariants done) | If any scenario reveals a spec ambiguity, hot loop with KT to lock the behaviour. |
| End of Week 5 (Soak + security results in) | If TLS path or libp2p stress shows regression, decide between fixing in v1 vs. shipping with privacy mode = "plain" only. |

---

## 7. References

- **Recent test reports** (tracked):
  - `docs/testing/reports/2026-04-23-1047-e2e-real-runpod-4090/report.md` — full RunPod E2E + §14 remaining-work list this plan extends
  - `docs/testing/reports/2026-04-21-v6-phase1a/SUMMARY.md` — V6 PoC results, A1 (single-machine) confirmed
  - `docs/testing/reports/2026-04-20-1329-c0-fail/report.md` — TGI batching perturbation FAIL, Option B mitigation
- **Test plans** (tracked, ancestors of this doc):
  - `docs/testing/T4_E2E_Test_Plan.md`
  - `docs/testing/FunAI_Test_Execution_Plan_KT.md`
  - `docs/testing/FunAI_TPS_Logits_Test_Plan_KT.md`
  - `docs/testing/FunAI_Integration_Test_Plan_V3.md`
  - `docs/testing/FunAI_Test_Plan_Review.md`
- **Protocol docs** (tracked):
  - `docs/protocol/FunAI_V52_Final.md`
  - `docs/protocol/FunAI_V52_Supplement.md`
  - `docs/protocol/FunAI_PreLaunch_Final_Audit_KT.md` — 12 protocol-level decisions whose status this plan does not duplicate
- **Internal** (gitignored, referenced for context only):
  - `docs/internal/FunAI_Security_Audit_Findings_KT.md` — A1–A7 + D1–D4 audit status
  - `docs/internal/Dispatch_Audit_Fix_Checklist.md`
  - `docs/internal/ops-runbook.md`
  - `docs/mainnet-readiness/2026-04-19-1637.md` — most recent loop-driven readiness snapshot

---

*This document is intentionally short on prescription. It enumerates what is not yet covered and orders the work by dependency; it deliberately does not prescribe how each test must be implemented. That is for each track owner to design.*
