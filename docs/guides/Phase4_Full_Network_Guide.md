# Phase 4: Full Network Integration Guide

## Prerequisites

Before Phase 4, the following must be completed:
- Phase 1: Chain-level tests passing (DONE)
- Phase 2: P2P layer design (DONE)
- Phase 3: P2P implementation with go-libp2p (TODO)

## Network Topology (4+ nodes)

```
Node 0 (Genesis Validator + Leader for model-A)
  ├── funaid (chain node, port 26657)
  ├── funai-p2p (P2P node, port 4001)
  └── vLLM (inference engine, port 8000, model: Qwen-0.5B)

Node 1 (Worker + Verifier)
  ├── funaid (chain node, port 26658)
  ├── funai-p2p (P2P node, port 4002)
  └── vLLM (inference engine, port 8001, model: Qwen-0.5B)

Node 2 (Worker + Verifier)
  ├── funaid (chain node, port 26659)
  ├── funai-p2p (P2P node, port 4003)
  └── vLLM (inference engine, port 8002, model: Qwen-0.5B)

Node 3 (Worker + Verifier + SecondVerifier)
  ├── funaid (chain node, port 26660)
  ├── funai-p2p (P2P node, port 4004)
  └── vLLM (inference engine, port 8003, model: Qwen-0.5B)

User SDK (command-line tool)
  └── Connects to any P2P node
```

## Test Scenarios

### Scenario 1: Normal Inference (Happy Path)

```
1. User deposits 100 FAI
2. User sends InferRequest (model=Qwen-0.5B, prompt="Hello", fee=1 FAI)
3. Leader (Node 0) dispatches to Worker (Node 1, VRF rank #1)
4. Node 1 runs inference, streams tokens to User
5. Node 1 sends InferReceipt + prompt + output to Verifiers (Nodes 2,3)
6. Nodes 2,3 run teacher forcing, both PASS
7. Node 0 (Proposer) packages into MsgBatchSettlement
8. Chain settles: User -1 FAI, Worker +0.95, Verifiers +0.015 each

Verify:
  - User balance decreased by 1 FAI
  - Worker balance increased by 0.95 FAI
  - Each verifier balance increased by 0.015 FAI
  - Worker.TotalTasks incremented
  - EpochStats.TotalSettled incremented
```

### Scenario 2: Worker Timeout + SDK Retry

```
1. User sends InferRequest
2. Leader dispatches to Worker A
3. Worker A accepts but doesn't respond (simulated hang)
4. SDK waits 5 seconds, re-sends same task_id
5. Leader dispatches to Worker B
6. Worker B completes inference
7. Worker B gets settled, Worker A tries to settle → rejected (duplicate)

Verify:
  - Only one settlement per task_id
  - Worker B gets paid, Worker A doesn't
```

### Scenario 3: FAIL Verification + Jail

```
1. Worker deliberately returns wrong logits (simulated)
2. 3 verifiers all FAIL
3. Settlement: FAIL → user charged 5%, worker jailed
4. Wait 120 blocks (10 min jail)
5. Worker sends MsgUnjail → active again

Verify:
  - Worker.JailCount = 1
  - Worker.JailUntil = settleBlock + 120
  - After unjail: Worker.Jailed = false
```

### Scenario 4: Second verification Flip SUCCESS→FAIL

```
1. Normal inference, 3 verifiers all PASS (but verifiers were lazy)
2. VRF selects task for second verification (10% probability)
3. Task goes to PENDING_AUDIT (not settled yet)
4. 3 second verifiers independently verify → 2/3 FAIL → second verification FAIL
5. Flip: SUCCESS → FAIL
6. Worker jailed + original PASS verifiers jailed
7. Task NOT settled (money not distributed)

Verify:
  - Worker.JailCount += 1
  - Each original PASS verifier.JailCount += 1
  - No settlement occurred
```

### Scenario 5: FraudProof (Worker sends fake content)

```
1. Worker runs correct inference, verifiers PASS
2. Worker sends different content to user (fake tokens)
3. User SDK computes hash, doesn't match result_hash
4. User SDK submits MsgFraudProof
5. Worker slashed 5% + tombstoned

Verify:
  - Worker.Tombstoned = true
  - Worker.Stake reduced by 5%
  - Task marked FRAUD
```

### Scenario 6: Leader Failover

```
1. Leader (Node 0) goes offline (kill process)
2. All Workers detect 1.5s inactivity on topic
3. Rank #2 (Node 1) becomes Leader
4. New InferRequest arrives → Node 1 dispatches correctly
5. Original Leader comes back → epoch ends → normal rotation

Verify:
  - Inference continues without interruption
  - No on-chain action needed for failover
```

## Metrics to Monitor

| Metric | Target | How to Check |
|--------|--------|-------------|
| Block time | ~5 seconds | `funaid status` |
| Settlement TPS | > 0 | Count MsgBatchSettlement per block |
| Inference latency | < 10s (500 tokens) | SDK timing |
| Verification time | < 0.6s | P2P logs |
| Second-verification rate | ~10% | `funaid q settlement params` |
| Jail events | 0 (normal) | Block explorer events |
| Committee rotation | every 120 blocks | VRF events |

## Estimated Timeline

| Week | Milestone |
|------|-----------|
| 1-2 | go-libp2p integration, basic topic pub/sub |
| 3-4 | Leader dispatch + Worker inference (mock engine) |
| 5-6 | Verifier teacher forcing + evidence collection |
| 7-8 | Proposer batch construction + chain submission |
| 9-10 | SDK client + streaming + retry |
| 11-12 | 4-node testnet with real vLLM inference |
| 13-14 | Second verification/third_verification flow + FraudProof |
| 15-16 | Stress testing + bug fixing |
