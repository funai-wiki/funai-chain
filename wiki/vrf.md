# VRF Unified Formula

FunAI Chain uses a single VRF formula for all ranking and selection across the system. Every use case -- dispatch, verification, second verification, leader election, and validator committee selection -- is an instance of the same formula with different parameters.

Source: [FunAI V52 Final Design Spec](../docs/FunAI_V52_Final.md)

---

## Formula

```
score = hash(seed || pubkey) / stake^alpha
```

**Lower score = higher rank.**

- `hash()` produces a uniformly random value from the seed and participant's public key.
- `stake^alpha` is the stake weighting factor. The exponent `alpha` controls how much stake influences selection.
- `seed` varies by use case to ensure independent randomness for each context.

---

## Use Cases

| Use Case | alpha | Seed | Selection Behavior |
|----------|-------|------|--------------------|
| **Dispatch** | 1.0 | `task_id \|\| block_hash` | Proportional to stake |
| **Verification** | 0.5 | `task_id \|\| result_hash` | Proportional to sqrt(stake) |
| **Second verification** | 0.0 | `task_id \|\| post_verification_block_hash` | Pure random |
| **Re-second verification** | 0.0 | `task_id \|\| post_second verification_block_hash` | Pure random |
| **Leader election** | 1.0 | `model_id \|\| sub_topic_id \|\| epoch_block_hash` | Proportional to stake |
| **Validator committee** | 1.0 | `epoch_block_hash` | Proportional to stake (100 members, 10 min rotation) |

---

## Design Rationale

### Dispatch (alpha = 1.0) -- proportional to stake

Selection probability is directly proportional to stake. This means a participant with 2x the stake gets dispatched roughly 2x as often.

**Why this works:** splitting stake across multiple identities yields zero benefit. If a Worker splits their stake into two equal accounts, each account gets half the selection probability -- the total expected work is unchanged. There is no incentive to Sybil-attack the dispatch system.

### Verification (alpha = 0.5) -- proportional to sqrt(stake)

Selection probability is proportional to the square root of stake. A participant with 4x the stake gets only 2x the verification probability.

**Why this works:** this reduces the ability of large stakeholders to dominate the verification pool. If a single entity controlled enough verifiers to collude with a dishonest Worker, the sqrt weighting makes that significantly more expensive -- they would need to quadruple their stake to double their chance of filling the 3 verifier slots.

### Second verification (alpha = 0.0) -- pure random

Selection probability is completely independent of stake. Every eligible participant has an equal chance.

**Why this works:** second verifications are the last line of defense. Pure randomness makes it approximately 3,400x harder for a large staking pool to control second verification outcomes compared to stake-proportional selection. Even an entity controlling a majority of total stake cannot reliably predict or influence which second verifier is selected.

### Re-second verification (alpha = 0.0) -- pure random

Same rationale as second verification. The seed uses `post_second verification_block_hash` (a future block hash unknown at second verification time) to ensure the third-verifier cannot be predicted during the initial second verification.

### Leader election (alpha = 1.0) -- proportional to stake

Leaders are elected per `model_id` topic with a 30-second epoch. Stake-proportional selection ensures that leaders have significant economic commitment to honest behavior. Auto-split occurs when TPS exceeds 500.

### Validator committee (alpha = 1.0) -- proportional to stake

100 committee members are selected per epoch with a 10-minute rotation. Stake-proportional selection aligns committee membership with economic security guarantees.

---

## VRF Position Selection for Logits Verification

Verification checks logits at 5 specific token positions. These positions are not chosen arbitrarily -- they are determined by VRF:

```
positions = VRF_select(Hash(task_id + result_hash), 5, output_length)
```

- The hash of `task_id + result_hash` serves as the seed.
- 5 positions are selected from the full output length.
- **Match threshold:** 4 out of 5 positions must match within the model's epsilon tolerance for a PASS verdict.
- Positions are unknown to the Worker until after it has committed its output, preventing selective cheating at only the checked positions.

### Deterministic Sampling Verification

For tasks with `temperature > 0`, an additional sampling verification layer applies:

- **Final seed:** `SHA256(user_seed || dispatch_block_hash || task_id)`
- **PRNG:** ChaCha20 (RFC 8439)
- **Math precision:** all operations use float32 (never float64) for cross-implementation consistency
- **Softmax accumulation order:** strictly token_id 0 to vocab_size-1
- **Combined check:** logits mismatch + sampling mismatch total <= 2 positions is PASS, >= 3 positions is FAIL

---

## Implementation

The VRF library lives at `x/vrf/` in the repository. All ranking and selection in the codebase must use this unified formula -- ad-hoc hashing for selection purposes is not permitted.

---

## Related Pages

- [Three-Layer Architecture](architecture.md) -- how VRF fits into the L1/L2 layers
- [Settlement State Machine](settlement.md) -- how VRF determines second verification selection rates and dispatch ranking
- [Schema Reference](schema.md) -- protobuf definitions for VRF-related messages
