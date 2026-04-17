# FunAI Test Plan vs Current Implementation — Complete Cross-Reference Review

> Review date: 2026-04-01
>
> Review baseline: commit aa57082 (main)
>
> Test plan document: `docs/FunAI_Test_Execution_Plan_KT.md` (baseline 59a21cd)
>
> Review method: Line-by-line cross-reference against code implementation, verifying whether each test scenario has a corresponding function/logic/type to support it

---

## 1. Overview

| Category | Scenario Count | Has Implementation Support | Partial/To Confirm | Not Implemented/Blocked |
|------|--------|-----------|------------|------------|
| L1-B S9 Per-Token | 19 | 19 | 0 | 0 |
| L1-C Economics+Edge Cases+Batch | 23 | 15 | 6 | 2 |
| L2 P2P Network | 10 | 10 | 0 | 0 |
| L3 Privacy | 7 | 6 | 0 | 1 |
| L4 Security | 10 | 8 | 1 | 1 |
| L5 Performance Stress | 7 | 7 | 0 | 0 |
| L6 GPU Inference | 9 | 8 | 1 | 0 |
| **Total** | **85 new** | **73** | **8** | **4** |

---

## 2. L1-B: S9 Per-Token Billing (19) — All Have Implementation Support

| ID | Scenario | Status | Key Code Location |
|----|------|------|------------|
| PT1 | Normal per-token billing (actual < max_fee) | ✅ | `x/settlement/keeper/keeper.go:CalculatePerTokenFee` + `distributeSuccessFee` |
| PT2 | max_fee cap (actual > max_fee) | ✅ | `keeper.go:2233` actualFee > maxFee → maxFee |
| PT3 | PerTokenBillingEnabled=false fallback | ✅ | `x/settlement/types/params.go:PerTokenBillingEnabled` toggle, `keeper.go:1991` check |
| PT4 | FAIL scenario charges 5% | ✅ | `keeper.go:distributeFailFee:1029` |
| PT5 | Zero price fallback (fee_per_input=0) | ✅ | fee_per_input=0 follows per-request path |
| PT6 | Overflow protection (MaxUint64/2) | ✅ | `keeper.go:2233` three overflow checks (multiplication×2 + addition×1) |
| PT7 | 1 million iterations conservation (no bias in fee distribution) | ✅ | remainder fallback in `distributeSuccessFee:1007-1009` |
| PT8 | Worker timeout billing | ✅ | `keeper.go:HandleFrozenBalanceTimeouts:1989-2066` |
| AC1 | Honest Worker (reported count matches) | ✅ | `ResolveTokenCounts:2158` returns worker-reported count |
| AC2 | Worker over-reports (exceeds tolerance) | ✅ | `effectiveTolerance:2190` + `resolveTokenPair:2199`, takes median |
| AC3 | 3 over-reports trigger jail | ✅ | `IncrementDishonestCount:1956` + `DishonestJailThreshold=3` |
| AC4 | Collusion second verification reversal | ✅ | `keeper.go:1322-1427` collusion detection + `jailWorkerAndVerifiers:1564` |
| AC5 | Within tolerance (diff <= tolerance) | ✅ | `effectiveTolerance` abs=2 / pct=2%, takes the larger value |
| AC6 | 50 consecutive successes reset | ✅ | `ResetDishonestCount:1973` |
| AC7 | Disabled skips token count check | ✅ | `PerTokenBillingEnabled` toggle skips check |
| AC8 | Pair tracking second verification boost | ✅ | `CalculateWorkerSecond verificationBoost:2118` + `TokenMismatchRecord` |
| AC9 | Verifier direct jail | ✅ | `jailWorkerAndVerifiers:1564`, direct jail upon second verification reversal |
| TR4 | MinBudget (at least 1 token) | ✅ | Worker layer `shouldStopGeneration:487` + settlement layer `CalculatePerTokenFee` |

---

## 3. L1-C: Economics+Edge Cases+Batch (23)

| ID | Scenario | Status | Key Code Location | Notes |
|----|------|------|------------|------|
| E1 | Dust accumulation over 1M iterations | ✅ | `distributeSuccessFee:1007` executor gets remainder | Already has `TestDistributeSuccessFee_DustHandling` |
| E2 | 1M random conservation | ✅ | Distribution logic remainder fallback | |
| E3 | Extreme prices | ✅ | Three overflow protection checks | |
| E4 | Epoch boundary | ✅ | `epoch = currentHeight / 100`, `RewardRecord.Epoch` | |
| E5 | Genesis migration | ✅ | `DefaultParams` populates S9 fields | Already has `TestGenesisRoundtrip` |
| E6 | Tombstone re-registration | ⚠️ Partial | `worker.go:Tombstoned` field exists | New address registration sets dishonest_count=0 **needs flow verification** |
| E7 | Pair storage scale | ✅ | `TokenMismatchRecord` + `CalculateWorkerSecond verificationBoost` query | |
| E8 | Large batch 10K | ✅ | keeper loop processing | |
| E9 | 2 Verifiers | ⚠️ Behavior uncertain | `medianUint32([a,b])` returns the larger value | **No documentation stating this is expected behavior** |
| E10 | 1 Verifier | ⚠️ No confidence marking | `medianUint32([a])` returns a | **No low-confidence marking mechanism** |
| E11 | 0 Verifiers | ⚠️ No protection | `medianUint32([])` returns 0 | **No timeout trigger or fallback** |
| E12 | expire_block too short | ✅ | `HandleFrozenBalanceTimeouts` + max 17280 blocks | |
| E13 | Double settlement | ✅ | `SettledTask` deduplication `keeper.go:737` | |
| E14 | All Verifiers return 0 | ❌ **Design blind spot** | `medianUint32([0,0,0])=0` | **Worker completes inference but earns near-zero income, no fallback/second verification trigger** |
| E15 | Epoch+Proposer rotation | ⚠️ Partial | Proposer rotation handled by CometBFT | **No application-layer test interface** |
| E16 | Block time variance | ✅ | Based on block count, not dependent on absolute time | |
| E17 | Batch gas limit | ⚠️ | `gasLimit = 200000 + len*2000` | **No validation against block gas limit** |
| E18 | Chain halt recovery | ✅ | `EndBlocker → HandleFrozenBalanceTimeouts` | |
| E19 | Batch empty round | ✅ Implemented | `p2p/proposer/proposer_test.go:TestBatchLoop_EmptyBatch` | commit 199c694 |
| E20 | Broadcast failure doesn't lose entries | ✅ Implemented+fixed | `p2p/proposer/proposer_test.go:TestBatchLoop_BroadcastFail` | commit 30edd37 |
| E21 | Sequence reset | ✅ Implemented | `p2p/chain/client_test.go:TestBatchLoop_SequenceReset` | commit 199c694 |
| E22 | Gas estimation validation | ⚠️ | Formula exists | **Whether per-token needs more gas is unverified** |
| E23 | Second verification dispatch | ✅ | `ProcessPending` returns `Second verificationDispatch` | |

---

## 4. L2: P2P Network (10) — All Have Implementation Support

| ID | Scenario | Status | Key Code Location |
|----|------|------|------------|
| N1 | Message reordering | ✅ | CometBFT + libp2p gossipsub native support |
| N2 | Network partition | ✅ | CometBFT consensus + block catch-up |
| N3 | Proposer crash | ✅ | `leader.go:LeaderMonitor:614-686` (1.5s failover) |
| N4 | Leader rotation in-flight | ✅ | 30s epoch `LeaderEpochDuration` + SDK retry |
| N5 | Message storm | ✅ | Leader rate limit `addressRateLimit=10` req/s/addr (`leader.go:459-472`) |
| N6 | Gossipsub latency | ✅ | Requires tc netem to simulate real latency |
| N7 | Node join | ✅ | `refreshWorkerList` 30s cycle (`dispatch.go:374-400`) |
| N8 | Cross-model isolation | ✅ | Per-model libp2p topic |
| N9 | Chain halt recovery | ✅ | EndBlocker timeout + shadow balance rebuild |
| N10 | SDK nonce concurrency | ✅ | P2P send doesn't go on-chain, no Cosmos nonce issue |

---

## 5. L3: Privacy (7)

| ID | Scenario | Status | Key Code Location | Notes |
|----|------|------|------------|------|
| P1 | E2E encryption | ✅ | `sdk/privacy/transport.go:ModeTLS` + X25519 ECDH + AES-256-GCM | |
| P2 | ModePlain compatibility | ✅ | `sdk/privacy/transport.go:ModePlain` (no-op) | |
| P3 | Key exchange security | ✅ | `sdk/privacy/tls_transport.go:26-65` X25519 + `p2p/node.go:335-359` signature verification | |
| P4 | Worker cannot see prompt | ✅ | Leader decrypts then only passes AssignTask | Confirmed by code review |
| P5 | StreamToken encryption | ✅ | Published via P2P host encrypted topic | |
| P6 | Logs don't leak data | ✅ | Static grep check | |
| P7 | Key rotation | ❌ **Not implemented** | Key generated once at startup, persisted to config | **No restart rotation logic, test will inevitably FAIL** |

---

## 6. L4: Security (10)

| ID | Scenario | Status | Key Code Location | Notes |
|----|------|------|------------|------|
| S1 | Forged SecondVerificationResponse | ✅ | `p2p/dispatch.go:handleSecondVerificationResponse:346-368` signature verification | |
| S2 | Replay attack | ✅ | `SettledTask` deduplication `keeper.go:737` | |
| S3 | Cross-denom attack | ✅ | `msgs.go:ValidateBasic:50` denom validation | |
| S4 | Malicious Leader tampers AssignTask | ❌ **Not implemented** | Worker only checks sender address (`worker.go:122-126`) | **Does not verify AssignTask cryptographic signature, test will inevitably FAIL** |
| S5 | Forged InferReceipt | ✅ | `verifier.go:HandleVerifyRequest:73-150` logits comparison | |
| S6 | Sybil VRF | ✅ | `x/vrf/types/vrf.go:ComputeScore:25` `score = hash / stake^α` | |
| S7 | Signature malleability | ⚠️ Depends on underlying lib | Cosmos SDK btcec library defaults to low-S normalization | **No explicit normalization at code level, most likely PASS but should confirm** |
| S8 | Overflow protection | ✅ Tested | `x/settlement/keeper/s9_truncation_test.go` | commit 90a9146 |
| S9 | Balance drain attack | ✅ | `leader.go:checkBalanceWithPending:474-491` shadow balance | |
| S10 | Unauthorized Proposer | ✅ | `keeper.go:verifyProposerSignature:1853-1888` | |

---

## 7. L5: Performance Stress (7) — All Have Implementation Support

| ID | Scenario | Status | Notes |
|----|------|------|------|
| R1 | 10-node testnet | ✅ | `scripts/init-testnet.sh` + docker-compose |
| R2 | 100-node gossipsub | ✅ | Requires cloud VPS |
| R3 | 1000 Worker VRF election | ✅ | `x/vrf/` pure computation |
| R4 | Large batch 10K/50K/125K | ✅ | keeper loop processing, split into mock + real two steps |
| R5 | Concurrent inference throughput | ✅ | Leader HandleRequest + rate limit |
| R6 | On-chain state growth | ✅ | Cosmos DB + cleanup mechanism |
| R7 | Pair tracking query | ✅ | `CalculateWorkerSecond verificationBoost` |

---

## 8. L6: GPU Inference (9)

| ID | Scenario | Status | Key Code Location | Notes |
|----|------|------|------------|------|
| G1 | Full E2E flow | ✅ | `scripts/e2e-real-inference.sh` | |
| G2 | Deterministic sampling ChaCha20 | ✅ | `tgi.go:chacha20SelectToken:650-713` + `verifier.go:chacha20Sample:283-378` | Must have temp>0 |
| G3 | 4-way concurrent inference | ✅ | `worker.go:maxConcurrentTasks:71` | Default 1, needs to be increased |
| G4 | Inference+verification parallel | ✅ | Different goroutines | |
| G5 | OOM protection | ⚠️ Partial | `worker.go:106-109` concurrency limit rejection | **No GPU VRAM-level detection** |
| G6 | TGI v2/v3 compatibility | ✅ | `tgi.go:DetectVersion()` + fallback | top_n_tokens already fixed to 5 |
| G7 | Worker truncation | ✅ Implemented | `worker.go:shouldStopGeneration:487-506` + `DeterministicGenerateWithBudget` | |
| G8 | Throughput benchmark | ✅ | bash script sufficient | Data recording |
| G9 | TGI crash recovery | ✅ | HTTP 5min timeout (`tgi.go:44`) | |

---

## 9. Blocker Summary (Must Be Resolved Before Writing Tests)

### P0 Blockers — Require Code Fix or Design Decision

| ID | Issue | Impact | Recommendation |
|----|------|------|------|
| **E14** | No protection when all Verifiers return 0 — `medianUint32([0,0,0])=0`, Worker completes inference but earns near-zero income | Design blind spot, current behavior is unreasonable | **Make design decision first**: (A) fallback to per-request (B) force second verification (C) keep as-is but mark as low confidence |
| **S4** | Worker does not verify AssignTask cryptographic signature — malicious Leader can tamper with prompt_hash | Plan assumes signature verification exists, but **code only checks sender address** | **Need to implement AssignTask signature verification**, otherwise test cannot pass |

### P1 Blockers — Need Implementation

| ID | Issue | Impact | Recommendation |
|----|------|------|------|
| **P7** | Key rotation not implemented — node restart still uses persisted old key | Test cannot pass | **Implement new key generation on restart**, old key messages should fail to decrypt |
| **E9-E11** | Behavior undefined when insufficient Verifiers | medianUint32 runs but semantics are unclear | **Confirm expected behavior first**: Is taking the larger value with 2 reasonable? Should 0 trigger a timeout? |

### Watch Items — Tests Can Be Written But Need Attention

| ID | Issue | Description |
|----|------|------|
| **S7** | Signature malleability | Cosmos SDK btcec library defaults to low-S normalization, **most likely PASS**, but should explicitly confirm in tests |
| **E17/E22** | Gas limit | Gas formula exists but ceiling not validated. Per-token settlement may consume more gas than per-request, **needs real-world testing to confirm** |
| **G5** | OOM protection | Only has concurrent task count limit, **no GPU VRAM detection**, under extreme conditions 4 concurrent long prompts may still OOM |
| **E6** | Tombstone re-registration | Tombstone field exists, new address registration path exists, but **dishonest_count isolation needs verification** |
| **E15** | Epoch+Proposer simultaneous rotation | Proposer rotation handled by CometBFT, **no application-layer test interface**, needs integration testing |

---

## 10. Issues with the Plan Document Itself

### Baseline Is Behind

Plan baseline `59a21cd`, current main is at `aa57082` (+5 commits). The following content is outdated:

- E19-E21 labeled "needs addition" — **already implemented** (commit 199c694, 30edd37)
- TR1-TR4 labeled "needs addition" — **Worker layer already implemented** (commit 381b219, 90a9146)
- E20 bug (broadcast failure loses entries) — **already fixed** (commit 30edd37)

### File Location Mismatch

| Location in Plan | Actual Location | Impact |
|------------|---------|------|
| E19-E23 → `p2p/dispatch_batch_test.go` | E19-E20 in `p2p/proposer/proposer_test.go`, E21 in `p2p/chain/client_test.go` | `go test ./p2p/... -run TestBatchLoop` can still find them, no functional impact |
| TR4 → `x/settlement/keeper/s9_pertoken_test.go` | Worker layer TR4 in `p2p/worker/worker_test.go` | **Substantive difference**: Plan's TR4 tests settlement layer, Worker layer TR4 tests truncation function, both layers are needed |

### Number Verification

- Plan says L1-A "142 existing integration tests" — need `go test ./... -count=1` to confirm actual count
- Plan says total 227 — with completed E19-E21 + TR1-TR4, the actual "needs addition" count should decrease by approximately 7

---

## 11. Recommended Execution Priority

```
Step 1: Resolve blockers
  1. E14 design decision (behavior when all Verifiers return 0)
  2. S4 implement AssignTask signature verification
  3. E9-E11 confirm expected behavior when insufficient Verifiers

Step 2: P0 tests (can run with go test)
  4. PT1-PT8 per-token settlement tests (x/settlement/keeper/s9_pertoken_test.go)
  5. AC1-AC10 anti-cheat tests (same file)
  6. E1-E18 economic conservation+edge case tests (x/settlement/keeper/economic_test.go)
  7. S1-S10 security tests (p2p/security_test.go + x/settlement/keeper/security_test.go)

Step 3: P1 tests (require additional environment)
  8. P7 implement key rotation then write tests
  9. P1-P6 privacy tests
  10. N1-N10 network tests (require docker testnet)
  11. R4-R5 performance benchmarks

Step 4: GPU real-machine testing
  12. G1-G9 execute on 5090 / T4
```

---

*Reviewer: Claude Code*
*Review date: 2026-04-01*
*Current code baseline: commit aa57082 (main)*
