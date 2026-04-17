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

## Related Pages

- [Security Second verification Findings](security-second verification.md)
- [Code vs Spec Compliance](code-review.md)
- [Settlement](settlement.md)
- [P2P Layer](p2p-layer.md)
