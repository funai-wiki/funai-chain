# FunAI Complete Test Execution Plan

> Date: 2026-03-31
>
> Baseline: commit 59a21cd (C1 BatchSettlement pipeline + C2 second verification dispatch + TGI v3 top_n_tokens fix)
>
> Total: 227 test scenarios (142 existing integration + 85 new)
>
> Principle: This is a chain — once released, it's released. All scenarios must PASS before release.
>
> Document suffix: KT

---

## Execution Overview

| Layer | What Is Tested | Scenario Count | Execution Method | Duration | Dependencies |
|---|--------|-------|---------|------|------|
| L1 | On-chain module correctness | 142 + 19 + 23 = 184 | go test automated | ~15 min | None |
| L2 | P2P network | 10 | docker-compose testnet + fault injection | ~40 min | Docker |
| L3 | Privacy | 7 | go test + code-level verification | ~20 min | None |
| L4 | Security | 10 | go test constructing malicious messages | ~10 min | None |
| L5 | Performance and stress | 7 | Cloud VPS simulating 100+ nodes | ~2 hours | Cloud resources |
| L6 | Real GPU inference | 9 | 5090 real machine | ~1 hour | GPU |
| **Total** | | **227** | | **~4.5 hours** | |

Note: L1 includes 142 existing integration tests + 42 new. L2-L6 total 43 new. Grand total 227 independent verification points.

---

## Execution Order

```
Phase 0: Build and smoke test (5 minutes)
  -> go build ./... passes
  -> go test ./... -count=1 all PASS
  -> Single node produces 50 blocks

  Important background note (commit 59a21cd fixes):
  - C1: BatchSettlement pipeline was never called before -- Proposer.ProcessPending() and
    BuildBatch() were written but not wired into the dispatch loop, tasks accumulated in memory
    and were never settled on-chain.
    Now connected via startBatchLoop (5s ticker) -> doBatchSettlement -> BroadcastSettlement.
  - C2: Second verification dispatch same issue -- Second verificationDispatch returned by ProcessPending is now actually
    sent to P2P.
  - TGI v3 top_n_tokens 256->5 -- TGI 3.3.6 rejects >5, causing teacher forcing to silently
    fail, meaning all Verifier verification was previously non-functional.
  
  This means all E2E tests before 59a21cd only got as far as verification; settlement and second verification
  never actually occurred.
  Phase 0 smoke test must confirm BatchSettlement tx successfully lands on-chain.

Phase 1: L1 on-chain modules (run first, discovers issues fastest)
Phase 2: L4 security (can run with go test)
Phase 3: L3 privacy (go test + code-level verification)
Phase 4: L2 P2P network (requires docker testnet)
Phase 5: L6 GPU inference (requires 5090)
Phase 6: L5 performance stress (run last, requires cloud resources)
```

---

## L1: On-Chain Module Correctness

### L1-A: 142 Existing Integration Test Cases (Confirm PASS)

```bash
cd ~/funai-chain
go test ./... -count=1 -v 2>&1 | tee l1a-results.log
grep -E 'FAIL|PASS|ok' l1a-results.log | tail -20
```

All must PASS before proceeding to subsequent tests. Any FAIL must be fixed first.

### L1-B: S9 Per-Token Billing Tests (19, need to be added)

File: `x/settlement/keeper/s9_pertoken_test.go`

| ID | Test Name | Verification Content | Key Assertions |
|----|--------|---------|---------|
| PT1 | TestPerToken_NormalBilling | actual < max_fee -> charge actual, refund difference | user_balance decrease == actual_fee |
| PT2 | TestPerToken_MaxFeeCap | actual > max_fee -> charge max_fee | user_balance decrease == max_fee |
| PT3 | TestPerToken_DisabledFallback | PerTokenBillingEnabled=false -> use max_fee | Identical result to per-request |
| PT4 | TestPerToken_FailBilling | FAIL scenario -> actual x 5% | worker_reward == 0, refund == max_fee - fail_fee |
| PT5 | TestPerToken_ZeroPriceFallback | fee_per_input=0 -> use per-request | Behavior unchanged |
| PT6 | TestPerToken_OverflowProtection | fee_per_output = MaxUint64/2, count=3 | Overflow -> caps at max_fee |
| PT7 | TestPerToken_FeeConservation | 1M random parameters | sum(worker+verifier+second verification+refund) == sum(user_debit) deviation == 0 |
| PT8 | TestPerToken_TimeoutFee | Worker timeout | Charge 5% to multi_verification_fund, refund 95%, worker jail |
| AC1 | TestAntiCheat_HonestWorker | Worker reports 423, Verifier x3 report 423 | settled == 423 |
| AC2 | TestAntiCheat_WorkerOverreport | Worker reports 800, Verifier median 423 | settled == 423, dishonest_count == 1 |
| AC3 | TestAntiCheat_ThreeStrikesJail | 3 over-reports | Worker jailed |
| AC4 | TestAntiCheat_CollusionSecond verification | Worker+2 Verifiers collude -> second verification overturns | Re-settlement + refund + colluding Verifiers direct jail |
| AC5 | TestAntiCheat_WithinTolerance | Worker reports 425, Verifier reports 423 | diff=2 <= tolerance -> settled == 425 |
| AC6 | TestAntiCheat_StreakReset | 50 consecutive successes | dishonest_count resets to 0 |
| AC7 | TestAntiCheat_DisabledNoCheck | PerTokenBillingEnabled=false | No token count comparison |
| AC8 | TestAntiCheat_PairTracking | Same pair 8/10 deviations | Second-verification rate boost triggered |
| AC9 | TestAntiCheat_VerifierDirectJail | Second verification discovers Verifier collusion | Verifier direct jail (doesn't wait for 3 strikes) |
| TR4 | TestTruncation_MinBudget | max_fee=1 | At least generates 1 output token |

### L1-C: Economic Layer + Edge Cases + Batch Pipeline Tests (23, need to be added)

File: `x/settlement/keeper/economic_test.go`

| ID | Test Name | Verification Content | Key Assertions |
|----|--------|---------|---------|
| E1 | TestDustAccumulation_1M | 1M settlements, each fee=99 ufai | Accumulated dust == 0 (executor uses remainder fallback) |
| E2 | TestFeeConservation_Randomized | 1M random fee/token_count/price | sum deviation == 0 |
| E3 | TestExtremePrices | fee_per_token=1 and fee_per_token=MaxUint64/2 | No panic, correct results |
| E4 | TestEpochBoundary | Settlement at exact epoch transition block | Reward attributed to correct epoch |
| E5 | TestGenesisMigration | Old params (without S9 fields) loaded | Default values populated, chain starts normally |
| E6 | TestTombstoneReregister | New address registration after tombstone | dishonest_count == 0 |
| E7 | TestPairStorageScale | 10000 Worker x 100 Verifier pairs | Storage size < 100MB, query < 10ms |
| E8 | TestBatchSettlement_Large | Single batch 10K entries (keeper mock pure computation) + extrapolate 50K/125K | 10K processing time < 1s, extrapolate 125K < 5s |
| E9 | TestVerifierInsufficient_2 | Only 2 Verifiers available (3rd is doing inference) | medianUint32 for 2 values takes the larger value, settlement normal no panic |
| E10 | TestVerifierInsufficient_1 | Only 1 Verifier available | Degrades to single-point verification, settlement normal, should be marked as low confidence |
| E11 | TestVerifierInsufficient_0 | 0 Verifiers (all Workers are busy) | Task times out, follows timeout flow (charge 5% + jail Worker), doesn't deadlock |
| E12 | TestExpireBlockTooShort | expire_block only 20 blocks (100 seconds), inference takes 60 seconds | If Worker completes normally it's not a timeout; if not, follows timeout flow; should not incorrectly jail |
| E13 | TestDoubleSettlement | Same task_id submitted in two different batches | Entry in second batch skipped by SettledTask deduplication |
| E14 | TestVerifierAllReturnZero | All 3 Verifiers' verified_output_tokens are 0 (TGI error) | medianUint32 returns 0 -> settles at 0 tokens -> actual_fee=input_cost only -> should not let Worker work for free at 0 tokens (need protection: when all Verifiers return 0, fallback to per-request or trigger second verification) |
| E15 | TestEpochBoundary_ProposerRotation | Epoch transition + Proposer rotation happen simultaneously | Both settlement authority and reward attribution are correct |
| E16 | TestBlockTimeVariance | Block time varies 3-8 seconds (not fixed 5 seconds) | Block-count-based timeout calculation still reasonable (expire_block tolerance sufficient) |
| E17 | TestBatchGasLimit | Gas consumption of BatchSettlement with 10K entries | gas < block gas limit (default 100M gas), won't be rejected |
| E18 | TestChainHaltRecovery | Simulate chain halt for 10 minutes then recovery | All in-flight tasks timeout-settled, shadow balance rebuilt correctly |
| E19 | TestBatchLoop_EmptyBatch | doBatchSettlement when no pending tasks | Returns directly, doesn't broadcast empty tx |
| E20 | TestBatchLoop_BroadcastFail | BatchSettlement broadcast failure | Entries retained in Proposer queue (not lost), retried on next tick |
| E21 | TestBatchLoop_SequenceReset | sequence mismatch -> reset -> succeeds next time | First attempt fails + reset, second tick succeeds with new sequence |
| E22 | TestBatchLoop_GasEstimate | Gas calculation for 1K/5K/10K entries | 200000 + len*2000 doesn't exceed block gas limit. Per-token settlement is more complex than per-request, observe whether actual gas exceeds estimate |
| E23 | TestBatchLoop_Second verificationDispatch | ProcessPending returns Second verificationDispatch | doBatchSettlement correctly sends to settlement topic |

> **E14 is especially important:** If all Verifiers return 0 (TGI crash or tokenizer error), the current medianUint32 returns 0, causing actual_fee to contain only input_cost, meaning the Worker completed full inference but earns almost no income. This is a design blind spot -- the test should verify current behavior, and if the behavior is unreasonable, protection logic should be added (e.g., fallback to per-request when all Verifiers return 0, or force second verification).

### L1 Execution Commands

```bash
# Full suite (including new tests)
go test ./x/settlement/keeper/... -v -count=1 -timeout=30m -run 'TestPerToken|TestAntiCheat|TestTruncation|TestDust|TestFeeConservation|TestExtreme|TestEpoch|TestGenesis|TestTombstone|TestPair|TestBatch|TestVerifierInsufficient|TestExpireBlock|TestDoubleSettlement|TestVerifierAllReturn|TestBlockTime|TestChainHalt' 2>&1 | tee l1bc-results.log

# Batch pipeline tests (P2P dispatch layer)
go test ./p2p/... -v -count=1 -run 'TestBatchLoop' 2>&1 | tee l1-batch-results.log

# 1M conservation tests (run separately, takes longer, must add -timeout)
go test ./x/settlement/keeper/... -v -count=1 -run TestFeeConservation_Randomized -timeout 30m
go test ./x/settlement/keeper/... -v -count=1 -run TestDustAccumulation_1M -timeout 30m
```

> **Note on `-timeout 30m`:** The 1M simulation runs for 5-10 minutes, and go test's default timeout of 10 minutes will kill it. You must explicitly add `-timeout 30m`.

---

## L2: P2P Network

Prerequisite: docker-compose 4-node testnet

```bash
# Start 4 nodes
bash scripts/e2e-test.sh setup

# Or manually
bash scripts/init-testnet.sh 4
docker-compose up -d
```

| ID | Test Name | Steps | Verification | Tools |
|----|--------|------|------|------|
| N1 | Message reordering | Use tc netem to add 2-second delay to Worker node | AssignTask arrives first, VerifyPayload arrives later, Worker processes normally | `tc qdisc add dev eth0 root netem delay 2000ms` |
| N2 | Network partition | iptables disconnects node3 and node4 | node1+2 continue producing blocks, node3+4 stall, catch up after recovery | `iptables -A INPUT -s node3 -j DROP` |
| N3 | Proposer crash | kill node2 (Proposer), wait 30 seconds, restart | VerifyResult 30s rebroadcast picked up by new Proposer | `docker kill node2 && sleep 30 && docker start node2` |
| N4 | Leader rotation in-flight | Send 100 consecutive requests, trigger Leader rotation in the middle | SDK retry covers all requests, none lost | Custom Go script |
| N5 | P2P message storm | Malicious node sends 10000 garbage InferRequests per second | Test in two layers: (1) libp2p layer: check if gossipsub peer scoring has rate limit configured, inspect score parameters; (2) Leader layer: check if HandleRequest itself has per-user rate limit. If neither exists, they need to be added | Go stress test script + libp2p peer score config review |
| N6 | Gossipsub latency | 100 nodes (docker), each container with tc netem delay 100ms to simulate real network latency, A sends message, measure B's receive latency | Latency < 3 seconds | Note: Cannot use docker default network (latency ~0.1ms), must add real delay. Results from docker bridge are not representative |
| N7 | Node join | Start new node, measure time until first selected by Leader | refreshWorkerList discovers new node within 30 seconds | Log timestamps |
| N8 | Cross-model isolation | node1 registers model A, node2 registers model B, send model A request | node2 does not receive model A's AssignTask | Log grep |
| N9 | Chain halt recovery | Stop all 4 validators for 10 minutes, restart | Chain resumes block production, all in-flight tasks timeout-settled, Leader shadow balance rebuilt from zero | docker stop/start + on-chain state query |
| N10 | SDK nonce concurrency | Same user sends 10 inference requests simultaneously | All requests succeed (SDK should use P2P send which doesn't go on-chain, no Cosmos nonce involved. But if deposit/withdraw and other on-chain txs are concurrent, nonce management is needed) | Go concurrency script |

### L2 Execution Script Framework

```bash
#!/bin/bash
# l2-network-tests.sh

echo "=== N1: Message Ordering ==="
docker exec node3 tc qdisc add dev eth0 root netem delay 2000ms
# Send an inference request
go run cmd/e2e-client/main.go --model test-model --prompt "hello"
# Check Worker logs: processed normally
docker logs node3 2>&1 | grep "dispatch: task.*completed"
docker exec node3 tc qdisc del dev eth0 root

echo "=== N2: Network Partition ==="
docker exec node1 iptables -A INPUT -s node3_ip -j DROP
docker exec node1 iptables -A INPUT -s node4_ip -j DROP
sleep 30
# Check node1 is still producing blocks
BLOCK1=$(docker exec node1 funaid query block latest | jq .block.header.height)
sleep 10
BLOCK2=$(docker exec node1 funaid query block latest | jq .block.header.height)
[ "$BLOCK2" -gt "$BLOCK1" ] && echo "N2 PASS" || echo "N2 FAIL"
# Recover
docker exec node1 iptables -D INPUT -s node3_ip -j DROP
docker exec node1 iptables -D INPUT -s node4_ip -j DROP
sleep 30
# Check node3 has caught up
BLOCK3=$(docker exec node3 funaid query block latest | jq .block.header.height)
[ "$BLOCK3" -ge "$BLOCK2" ] && echo "N2 recovery PASS" || echo "N2 recovery FAIL"

# ... N3-N8 follow similar pattern
```

---

## L3: Privacy

| ID | Test Name | Steps | Verification | Tools |
|----|--------|------|------|------|
| P1 | End-to-end encryption | SDK ModeTLS sends request | Verify at code level: In Leader's handleModelMessage, raw msg.Data JSON unmarshal should fail (it's ciphertext), DecryptMessage then unmarshal succeeds (it's plaintext). tcpdump is unreliable -- libp2p itself may have noise encryption, not seeing plaintext in packet capture doesn't mean application-layer encryption is working | go test + assertion hook |
| P2 | ModePlain compatibility | SDK ModePlain sends request | Leader processes normally, no crash | go test |
| P3 | Key exchange security | Man-in-the-middle forges X25519 public key | Key exchange signature verification fails, connection rejected | go test |
| P4 | Worker cannot see prompt | Worker logs + memory dump | Does not contain prompt plaintext (Leader decrypts then only passes AssignTask) | grep + code review |
| P5 | StreamToken encryption | Worker->SDK streaming tokens, verify at code level | Same approach as P1, verify at SDK receiving end that raw msg is ciphertext | go test |
| P6 | Logs don't leak data | grep all log.Printf | Does not print prompt/output plaintext | `grep -rn 'req\.Prompt\|payload\.Prompt\|task\.Prompt' --include='*.go' p2p/ \| grep -i 'log\|print\|fmt'` |
| P7 | TLS key rotation | New key after node restart | Old key messages cannot be decrypted | go test |

### L3 Execution

```bash
# P1: End-to-end encryption -- code-level verification (no tcpdump)
# In p2p/privacy_test.go:
# 1. SDK uses ModeTLS to encrypt a message
# 2. Directly json.Unmarshal the encrypted bytes -> expect failure (confirm it's ciphertext)
# 3. Call DecryptMessage -> json.Unmarshal -> expect success (confirm decryption works)
# 4. Compare decrypted content == original prompt
go test ./p2p/... -v -run 'TestE2EEncryption|TestModePlain|TestKeyExchange|TestStreamTokenEncrypt|TestKeyRotation'

# P6: Log review -- static check
grep -rn 'req\.Prompt\|payload\.Prompt\|task\.Prompt' --include='*.go' p2p/ | grep -i 'log\|print\|fmt\|Printf'
# Expected: 0 results. If any found, each must be confirmed not to print plaintext in production
```

---

## L4: Security

File: `p2p/security_test.go` + `x/settlement/keeper/security_test.go`

| ID | Test Name | Attack Method | Expected Result |
|----|--------|---------|---------|
| S1 | TestForgedSecondVerificationResponse | Construct SecondVerificationResponse, sign with wrong private key | handleSecondVerificationResponse rejects, log "reject.*invalid signature" |
| S2 | TestReplayAttack | Resubmit BatchSettlement with same task_id | Second submission rejected by SettledTask deduplication |
| S3 | TestCrossDenomAttack | Deposit uatom, settle inference with ufai | ValidateBasic rejects, returns "invalid denom" |
| S4 | TestMaliciousLeaderAssignTask | Leader tampers with AssignTask's prompt_hash | Worker signature verification fails, refuses to execute |
| S5 | TestFakeInferReceipt | Worker sends valid signature but fabricated logits | Verifier judges FAIL, Worker jailed |
| S6 | TestSybilAttack_VRF | 1 Worker stake=1000 vs 100 Workers each stake=10 (same total stake), run VRF election 10000 times | Both groups' selection probability should be similar (difference < 5%). Not testing "uniform distribution" (that's VRF correctness), but testing "splitting stake cannot gain extra advantage" |
| S7 | TestSignatureMalleability | secp256k1 signature s-value flip | Rejected (Cosmos SDK normalization check) |
| S8 | TestOverflowProtection | CalculatePerTokenFee all 3 overflow paths | All return max_fee |
| S9 | TestBalanceDrainAttack | User sends 1000 requests, each max_fee close to balance | Shadow balance blocks most, settlement layer caps as fallback |
| S10 | TestUnauthorizedProposer | Non-current Proposer submits BatchSettlement | Signature verification fails, tx rejected |

### L4 Execution

```bash
go test ./p2p/... -v -run 'TestForged|TestReplay|TestCrossDenom|TestMalicious|TestFake|TestSybil|TestSignature' 2>&1 | tee l4-results.log
go test ./x/settlement/keeper/... -v -run 'TestOverflow|TestBalanceDrain|TestUnauthorized' 2>&1 | tee -a l4-results.log
```

---

## L5: Performance and Stress

| ID | Test Name | Scale | Key Metrics | Pass Criteria |
|----|--------|------|---------|---------|
| R1 | 10-node testnet | 10 docker nodes | Block production normal, consensus latency | Latency < 2 seconds |
| R2 | 100-node gossipsub | 100 VPS simulated nodes | P2P message propagation latency | A->B < 3 seconds |
| R3 | 1000 Worker registration | Register 1000 Workers on-chain | VRF election time | Election < 100ms |
| R4 | BatchSettlement large batch | 10K / 50K / 125K entries | Processing time | < 5 seconds (block time) |
| R5 | Concurrent inference throughput | 1 / 10 / 100 / 1000 req/s | Leader processing latency | p99 < 500ms |
| R6 | On-chain state growth | Run 1M settlements | DB size | < 10 GB |
| R7 | Pair tracking query | 10000 pair records | calculateWorkerSecond verificationBoost time | < 10ms |

### R4 BatchSettlement Stress Test Script

> **Important: Test in two steps.** Directly constructing 125K entries requires 125K InferenceAccount on-chain states; the initialization itself is slow and masks real settlement performance.

```go
// bench/batch_settlement_bench_test.go

// Step 1: Pure computation performance (keeper mock, no DB I/O)
func BenchmarkBatchSettlement_MockKeeper(b *testing.B) {
    sizes := []int{1000, 10000, 50000}
    for _, size := range sizes {
        b.Run(fmt.Sprintf("mock_%d", size), func(b *testing.B) {
            entries := generateRandomEntries(size)
            // Use mock keeper, all balance queries return fixed values
            mockKeeper := NewMockSettlementKeeper()
            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                mockKeeper.ProcessEntries(entries)
            }
        })
    }
}

// Step 2: Real DB I/O (smaller scale, includes full on-chain state reads/writes)
func BenchmarkBatchSettlement_RealDB(b *testing.B) {
    sizes := []int{1000, 5000, 10000}
    for _, size := range sizes {
        b.Run(fmt.Sprintf("real_%d", size), func(b *testing.B) {
            // Pre-initialize size InferenceAccounts
            ctx, keeper := setupBenchKeeper(b, size)
            entries := generateEntriesForAccounts(size)
            msg := &types.MsgBatchSettlement{Entries: entries}
            
            b.ResetTimer()
            for i := 0; i < b.N; i++ {
                keeper.ProcessBatchSettlement(ctx, msg)
            }
        })
    }
    // 125K extrapolated from mock results: if 10K real = 800ms, 125K ~ 10s -> exceeds target, needs optimization
}
```

### R5 Concurrent Inference Stress Test Script

```go
// bench/concurrent_infer_bench_test.go
func BenchmarkConcurrentInfer(b *testing.B) {
    rates := []int{1, 10, 100, 1000}
    for _, rate := range rates {
        b.Run(fmt.Sprintf("rps_%d", rate), func(b *testing.B) {
            var wg sync.WaitGroup
            start := time.Now()
            for i := 0; i < rate; i++ {
                wg.Add(1)
                go func() {
                    defer wg.Done()
                    leader.HandleRequest(ctx, makeInferRequest(), blockHash)
                }()
            }
            wg.Wait()
            elapsed := time.Since(start)
            b.ReportMetric(float64(elapsed.Milliseconds()), "ms/batch")
        })
    }
}
```

### L5 Execution

```bash
# R1: 10 nodes
bash scripts/init-testnet.sh 10
docker-compose up -d
sleep 60
# Verify all nodes are synced
for i in $(seq 1 10); do
  HEIGHT=$(docker exec node$i funaid query block latest | jq .block.header.height)
  echo "node$i: height=$HEIGHT"
done

# R3-R7: go bench
go test ./bench/... -bench=. -benchtime=10s -v 2>&1 | tee l5-bench-results.log

# R4: Large batch run separately (memory intensive)
go test ./bench/... -bench=BenchmarkBatchSettlement -benchtime=3s -timeout=30m

# R6: State growth (requires long-running)
go test ./bench/... -bench=BenchmarkStateGrowth -benchtime=1x -timeout=60m
```

---

## L6: Real GPU Inference

Prerequisite: 5090 + TGI + Qwen2.5-0.5B (smoke) / Qwen3-8B (formal)

| ID | Test Name | Steps | Verification | Pass Criteria |
|----|--------|------|------|---------|
| G1 | Single inference full pipeline | SDK->Leader->Worker->TGI->Verifier->Settlement | Full pipeline runs through | Receipt valid, settlement successful |
| G2 | Deterministic verification | Same prompt + same seed + temp=0.7, run 2 times | Two outputs are exactly the same | byte-exact. Note: Must use temperature>0 + seed to test ChaCha20 deterministic sampling path. temperature=0 uses argmax which is a different code path and cannot verify FunAI's core verification mechanism |
| G3 | 4-way concurrent inference | Send 4 requests simultaneously | All 4 return successfully | throughput >= 100 tok/s |
| G4 | Inference+verification parallel | Send verification request during ongoing inference | No mutual blocking | Verification latency < 2 seconds |
| G5 | GPU OOM protection | 4-way inference + verification prefill | Reject verification request rather than crash | Worker process does not exit |
| G6 | TGI version compatibility | Run same prompt on TGI v2 and v3 respectively | Logits match to 5 decimal places | Relative error < 1e-5. Note: Must confirm top_n_tokens=5 (not 256), TGI 3.3.6 rejects >5 which causes teacher forcing to silently fail (fixed in 59a21cd, needs verification) |
| G7 | Worker truncation | Set max_fee to only afford 50 tokens | Worker stops at approximately 50 tokens | output_token_count ~ 50 |
| G8 | Throughput benchmark | 8B model 1/2/4 concurrent | Record tok/s, latency, VRAM | Data recording (no pass criteria) |
| G9 | TGI crash recovery | Kill TGI mid-inference | Worker times out, doesn't hang | Worker times out within 30 seconds |

### L6 Execution

```bash
# Preparation
cd ~/funai-chain

# G1: Full pipeline smoke test
bash scripts/e2e-real-inference.sh --model Qwen/Qwen2.5-0.5B-Instruct 2>&1 | tee g1-results.log

# G2: Determinism (ChaCha20 sampling path, must use temperature > 0)
PROMPT="What is 1+1?"
RESULT1=$(go run cmd/e2e-client/main.go --prompt "$PROMPT" --temperature 0.7 --seed 42)
RESULT2=$(go run cmd/e2e-client/main.go --prompt "$PROMPT" --temperature 0.7 --seed 42)
[ "$RESULT1" == "$RESULT2" ] && echo "G2 PASS" || echo "G2 FAIL: outputs differ"
# Do not use --temperature 0, that uses argmax not ChaCha20

# G3: Concurrent
for i in 1 2 3 4; do
  go run cmd/e2e-client/main.go --prompt "Count to $i" &
done
wait
echo "G3: check all 4 succeeded"

# G8: Throughput benchmark
for CONCURRENT in 1 2 4; do
  echo "=== $CONCURRENT concurrent ==="
  time (
    for i in $(seq 1 $CONCURRENT); do
      go run cmd/e2e-client/main.go --prompt "Write a 200 word essay about AI" &
    done
    wait
  )
done

# G9: TGI crash
go run cmd/e2e-client/main.go --prompt "Write a very long essay" &
sleep 3
kill $(pgrep -f 'text-generation-launcher')
# Wait for Worker timeout
wait
echo "G9: check Worker did not hang"
```

---

## Test Results Recording Template

```
# FunAI Test Results -- [Date]
# Commit: [commit hash]
# Tester: [Name]

## Phase 0: Smoke Test
  go build ./...           [PASS/FAIL]
  go test ./... -count=1   [PASS/FAIL] (x/y tests passed)
  Single node 50 blocks    [PASS/FAIL]

## L1: Chain Module Correctness
  L1-A: 142 integration    [PASS/FAIL] (x/142 passed)
  L1-B: 19 S9 per-token    [PASS/FAIL] (x/19 passed)
  L1-C: 23 economic+edge+batch [PASS/FAIL] (x/23 passed)
  
  E14 (Verifier all return 0): [Behavior recorded -- may need protection logic added]
  
  FAIL details:
    [test name]: [error message]

## L2: P2P Network
  N1 Message ordering      [PASS/FAIL]
  N2 Network partition     [PASS/FAIL]
  N3 Proposer crash        [PASS/FAIL]
  N4 Leader rotation       [PASS/FAIL]
  N5 Message storm         [PASS/FAIL] (libp2p rate limit: [yes/no], Leader rate limit: [yes/no])
  N6 Gossipsub latency     [PASS/FAIL] ([x]ms avg, with 100ms simulated delay)
  N7 Node join             [PASS/FAIL] ([x]s to first task)
  N8 Cross-model isolation [PASS/FAIL]
  N9 Chain halt recovery   [PASS/FAIL]
  N10 SDK nonce concurrent [PASS/FAIL]

## L3: Privacy
  P1 E2E encryption        [PASS/FAIL]
  P2 ModePlain compat      [PASS/FAIL]
  P3 Key exchange security [PASS/FAIL]
  P4 Worker no prompt      [PASS/FAIL]
  P5 StreamToken encrypted [PASS/FAIL]
  P6 Log no leaks          [PASS/FAIL] ([x] leak points found)
  P7 Key rotation          [PASS/FAIL]

## L4: Security
  S1-S10                   [PASS/FAIL] (x/10 passed)

## L5: Performance
  R1 10 node consensus     [PASS/FAIL] ([x]ms latency)
  R2 100 node gossip       [PASS/FAIL] ([x]ms propagation)
  R3 1000 Worker VRF       [PASS/FAIL] ([x]ms election)
  R4 Batch 10K/50K/125K    [PASS/FAIL] ([x]/[x]/[x]ms)
  R5 Concurrent 1/10/100   [PASS/FAIL] ([x]/[x]/[x]ms p99)
  R6 State growth 1M       [PASS/FAIL] ([x]GB)
  R7 Pair query 10K        [PASS/FAIL] ([x]ms)

## L6: GPU Inference
  G1 Full E2E              [PASS/FAIL]
  G2 Deterministic (ChaCha20) [PASS/FAIL]
  G3 4-way concurrent      [PASS/FAIL] ([x] tok/s)
  G4 Infer+Verify parallel [PASS/FAIL]
  G5 OOM protection        [PASS/FAIL]
  G6 TGI v2/v3 compat     [PASS/FAIL]
  G7 Budget truncation     [PASS/FAIL]
  G8 Throughput baseline   [DATA] (see below)
  G9 TGI crash recovery    [PASS/FAIL]

## G8 Throughput Data
  | Model | Concurrent | tok/s | Latency p50 | Latency p99 | VRAM |
  |-------|-----------|-------|-------------|-------------|------|
  | 8B    | 1         |       |             |             |      |
  | 8B    | 2         |       |             |             |      |
  | 8B    | 4         |       |             |             |      |

## Summary
  Total: [x]/227 PASS
  Blockers: [list]
  Notes: [any observations]
```

---

## Engineer Task Assignment

### New Test Code Engineers Need to Write

| File | Content | Estimated Lines | Priority |
|------|------|---------|--------|
| `x/settlement/keeper/s9_pertoken_test.go` | PT1-8 + AC1-10 + TR4 | ~400 lines | P0 |
| `x/settlement/keeper/economic_test.go` | E1-E18 economic conservation, boundaries, insufficient Verifiers, timeout boundaries, double settlement | ~500 lines | P0 |
| `p2p/dispatch_batch_test.go` | E19-E23 Batch pipeline (empty batch, broadcast failure, sequence reset, gas estimation, second verification dispatch) | ~200 lines | P0 |
| `p2p/security_test.go` | S1-S5 forged message defense | ~200 lines | P0 |
| `x/settlement/keeper/security_test.go` | S6-S10 on-chain security (S6 uses stake comparison method) | ~200 lines | P0 |
| `p2p/privacy_test.go` | P1-P7 encryption and keys (P1 uses code-level hook verification) | ~200 lines | P1 |
| `bench/batch_settlement_bench_test.go` | R4 large batch stress test (split into mock + real two steps) | ~150 lines | P1 |
| `bench/concurrent_infer_bench_test.go` | R5 concurrency stress test | ~100 lines | P1 |
| `bench/state_growth_bench_test.go` | R6-R7 state growth | ~100 lines | P2 |
| `scripts/l2-network-tests.sh` | N1-N10 network test scripts (N6 with tc netem) | ~400 lines | P1 |
| **Total** | | **~2450 lines** | |

### Does Not Require Engineers to Write

| Test | Who Does It | How |
|------|------|--------|
| G1-G9 GPU tests | You (Jms) on the 5090 | bash scripts + manual observation |
| R1-R2 Multi-node | Engineer | docker-compose + existing e2e-test.sh |
| P6 Log review | Code review | grep once and done |

---

## Pre-Launch Checklist

- [ ] Phase 0: go build + go test + single node block production PASS
- [ ] L1-A: All 142 integration tests PASS
- [ ] L1-B: All 19 S9 tests PASS
- [ ] L1-C: All 23 economic+edge+batch pipeline tests PASS (including 1M conservation, note -timeout 30m)
- [ ] L1-C E14: Verifier all-return-0 behavior confirmed (if unreasonable, add protection logic and retest)
- [ ] L1-C E20: BatchSettlement broadcast failure entries not lost confirmed
- [ ] L2: All 10 network tests PASS (N6 must add tc netem delay)
- [ ] L3: All 7 privacy tests PASS (P1 uses code-level verification not tcpdump)
- [ ] L4: All 10 security tests PASS (S6 uses stake comparison not chi-squared test)
- [ ] L5: All 7 performance test data recorded (R4 split into mock + real two-step testing)
- [ ] L6: G1 full pipeline PASS + G2 determinism PASS (G2 must use temp>0 + seed for ChaCha20 path)
- [ ] L6: G8 throughput data recorded
- [ ] All FAILs fixed and regression tested
- [ ] Final go test ./... -count=1 -timeout 30m all PASS
- [ ] Final commit hash recorded

All boxes must be checked before launch.

---

*Document version: V3 (baseline updated to 59a21cd: +5 Batch pipeline tests, corrected G6 TGI top_n_tokens)*
*Date: 2026-03-31*
*Baseline: commit 59a21cd*
*Document suffix: KT*

---

## Appendix: V2 Self-Review Correction Record

### 10 Missing Scenarios Added

| ID | Scenario | Added Where | Why the Omission Is Dangerous |
|----|------|-------|---------------|
| E9 | Only 2 Verifiers | L1-C | medianUint32 behaves differently for 2 values; inevitably encountered during cold-start when network is small |
| E10 | Only 1 Verifier | L1-C | Degrades to single-point trust, needs explicit low-confidence marking |
| E11 | 0 Verifiers | L1-C | When all Workers are busy, does the task deadlock or timeout? |
| E12 | expire_block too short | L1-C | Large model inference takes 2 minutes but expire is only 100 seconds, Worker gets wrongly jailed |
| E13 | Double settlement | L1-C | Same task submitted in two batches, user charged twice |
| E14 | All Verifiers return 0 | L1-C | When TGI crashes, median=0, Worker completed full inference but earns no income -- design blind spot |
| E15 | Epoch+Proposer simultaneous rotation | L1-C | Two state transitions overlapping, reward attribution and settlement authority may both go wrong |
| E16 | Block time variance | L1-C | Block-count-based timeout assumes fixed 5 seconds, actual is 3-8 seconds |
| E17 | Batch gas limit | L1-C | 125K entries' gas may exceed block gas limit, large batch can't be sent |
| N9 | Chain halt recovery | L2 | After all validators go offline and recover, in-flight tasks and shadow balance state |
| N10 | SDK nonce concurrency | L2 | Nonce conflicts with concurrent on-chain txs from same user |

### 7 Test Method Corrections

| ID | Original Method | Problem | Corrected To |
|----|--------|------|--------|
| F1 (G2) | temperature=0 compare two outputs | FunAI uses ChaCha20 deterministic sampling; temp=0 uses argmax which is a different code path and doesn't test the core verification mechanism | Changed to temperature=0.7 + seed=42, uses ChaCha20 path |
| F2 (S6) | Chi-squared test for VRF uniformity | VRF uniformity doesn't equal Sybil resistance. 100 low-stake Workers with same total stake as 1 high-stake Worker may have different selection probability distributions | Changed to stake comparison method: 1x1000 vs 100x10, both groups' selection probability should be similar |
| F3 (N6) | Docker default network for gossipsub testing | Docker bridge latency ~0.1ms, completely different from real network 50-200ms. Low latency makes gossipsub perform perfectly, masking real issues | Add tc netem delay 100ms to each container |
| F4 (P1) | tcpdump packet capture grep for prompt | libp2p itself has noise encrypted transport layer; not seeing plaintext in capture doesn't mean application-layer encryption is working | Changed to code-level hook: verify raw msg.Data is ciphertext (JSON unmarshal fails), only plaintext after DecryptMessage |
| F5 (E1/E2) | go test default timeout | 1M simulation runs 5-10 minutes, default 10 minute timeout will kill it | Add -timeout 30m |
| F6 (R4) | Directly construct 125K entries | 125K InferenceAccount initialization time masks real settlement performance | Split into two steps: mock keeper for pure computation (50K), real DB for I/O included (10K), extrapolate 125K |
| F7 (N5) | Only test Leader CPU | gossipsub built-in peer scoring may discard garbage messages at the P2P layer; what's being tested is not Leader logic | Test in two layers: (1) libp2p peer scoring config review (2) Leader.HandleRequest's own rate limit |
