# Security Second verification Findings

Tracker for all security second verification findings across the FunAI Chain project.

## Security Second verification (KT)

Baseline commit: `ce87883` (2026-04-03). Source: [FunAI_Security_Second verification_Findings_KT.md](../docs/FunAI_Security_Second verification_Findings_KT.md)

| ID | Description | Severity | Status | Resolution |
|----|-------------|----------|--------|------------|
| A1 | `ProcessWithdraw` doesn't check frozen balance | CRITICAL | FIXED | Commit `2cb3dab` -- uses `AvailableBalance()` |
| A2 | Timeout fee not transferred to multi-verification fund | NOT A BUG | Closed | Second verification fund = implicit difference between user payment and Worker/verifier payouts |
| A3 | FraudProof clawback when Worker balance insufficient | NOT A BUG | Closed | Negative EV attack (-14.67 FAI per attempt) -- not economically viable |
| A4 | 1M iteration fee conservation test | PASS | VERIFIED | Commit `494ac59` -- E1+E2 zero deviation across 1M iterations |
| A5 | Genesis parameter review | Informational | CONFIRMED | `chain_id=funai_333-1`, EVM chain ID = 333 |
| A6 | Private key storage security | YELLOW | DOCUMENTED | Guidance added in [Join_Testnet.md](../docs/Join_Testnet.md) |
| A7 | Proposer selective non-inclusion | YELLOW | Acknowledged | Mitigated by epoch rotation + 30s rebroadcast; full fix deferred |

**Pre-launch checklist:** All items complete.

## Dispatch Second verification Fix Checklist

Baseline commit: `599fcd7` (2026-03-30). Source: [Dispatch_Second verification_Fix_Checklist.md](../docs/Dispatch_Second verification_Fix_Checklist.md)

| ID | Priority | Description | Scope |
|----|----------|-------------|-------|
| D1 | P0 | SDK retry timeout hardcoded 5s -- make configurable with 30s default | ~30 lines |
| D2 | P1 | `SecondVerificationResponse` has no signature -- add secp256k1 sig | ~30 lines |
| D3 | P2 | Multi-model node broadcasts to wrong topic -- carry `modelId` in message | ~10 lines |
| D4 | P2 | `DecryptMessage` called inconsistently (2 of 4 functions) | 4 lines |

## Related Pages

- [Code vs Spec Compliance](code-review.md)
- [Test Plan Status](test-status.md)
- [Settlement](settlement.md)
- [Jail and Slashing](jail-and-slashing.md)
