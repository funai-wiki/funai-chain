# Code vs Spec Compliance

Tracking spec-vs-implementation gaps identified in the funai-chain code review. Baseline commit: `38bc1ff`. Source: [funai-chain-review.md](../docs/funai-chain-review.md)

## Previously Fixed (19 of 19)

| ID | Description | Status |
|----|-------------|--------|
| P0-1 (review) | FraudProof tombstone | FIXED |
| P0-1 | Sampling uses logprob instead of raw logits (double softmax) | FIXED |
| P0-2 | Worker uses TGI native sampling instead of ChaCha20 | FIXED |
| P0-3 (review) | ChaCha20 2^64 | FIXED |
| P0-3 | SDK key exchange signature verification is a no-op | FIXED |
| P0-4 | Second verification VRF seed | FIXED |
| P0-5 | X25519 key | FIXED |
| P0-6 | Key exchange sig | PARTIALLY FIXED |
| P0-7 | `jailSecondVerifiers` | FIXED |
| P0-8 | `expire_block` | FIXED |
| P0-9 | FraudProof receipt | FIXED |
| P0-10 | PII Chinese patterns | FIXED |
| P1-1 (review) | Re-second verification timeout | FIXED |
| P1-1 | VRF pubkey decode used hex only; base64 keys from chain failed silently | FIXED |
| P1-2 (review) | Second verification fund FAIL | FIXED |
| P1-2 | `LogitsHash` uses placeholder zeros — second verifiers cannot verify logits integrity | FIXED |
| P1-3 (review) | Softmax order | FIXED |
| P1-3 | `AssignTask` missing `Temperature`, `UserSeed`, `DispatchBlockHash` | FIXED |
| P1-4 | Leader `PrivKey` never set -- `LeaderSig` always empty | FIXED |
| P1-5 (review) | Leader sig scope | FIXED |
| P1-5 | `SelectVerifiersForTask` seed missing `result_hash` | FIXED |

## P2 -- Moderate (12 issues)

Twelve moderate issues covering edge cases in timeout handling, metric reporting, retry logic, and parameter validation. See [funai-chain-review.md](../docs/internal/funai-chain-review.md) for the full list.

## P3 -- Low (4 issues)

Four low-severity issues related to logging verbosity, documentation gaps, and cosmetic inconsistencies. See [funai-chain-review.md](../docs/internal/funai-chain-review.md) for details.

## Priority Summary

All P0 and P1 blockers are resolved (P0-6 partially). Remaining items are P2 (moderate) and P3 (low) only — none block mainnet launch.

## Related Pages

- [Security Second verification Findings](security-second verification.md)
- [Test Plan Status](test-status.md)
- [Verification](verification.md)
- [VRF](vrf.md)
