# VRF Unified Formula

FunAI Chain uses a single VRF formula for all ranking and selection across the system. Every use case -- dispatch, verification, second verification, leader election, and validator committee selection -- is an instance of the same formula with different parameters.

Source: [FunAI V52 Final Design Spec](../docs/FunAI_V52_Final.md)

---

## Formula

**Effective weight** combines three node-level factors:

```
effective_stake   = stake × reputation × latency_factor
effective_repspeed = reputation × latency_factor        (stake excluded)
```

- `reputation` is on-chain node reliability in range [0.0, 1.2], default 1.0.
- `latency_factor` is the latency multiplier derived from the node's on-chain `avg_latency_ms`: reference = 3000 ms, clamped to [0.1, 1.5]. Missing data (new node) → 1.0.
- **Known bug (P1, 2026-04-20):** `avg_latency_ms` is currently sourced from the Worker's self-signed `InferReceipt.InferenceLatencyMs`, which the secp256k1 signature authenticates but does not prove truthful. A malicious Worker can hardcode a low value to gain up to 1.5× dispatch boost. Fix scheduled: compute `latency_ms` on-chain as `ReceiptAtMs - AcceptedAtMs` (both recorded by the Proposer). See [P1_AvgLatencyMs_SelfReport_Bug_KT_1.md](../docs/protocol/P1_AvgLatencyMs_SelfReport_Bug_KT_1.md).

**VRF score** depends on the role:

```
Worker dispatch         (alpha = 1.0):  score = hash(seed || pubkey) / effective_stake
1st-tier verifier       (alpha = 0.5):  score = hash(seed || pubkey) / sqrt(effective_stake)
2nd/3rd-tier verifier   (alpha = 0.0):  score = hash(seed || pubkey) / effective_repspeed
```

**Lower score = higher rank.** The `alpha` exponent controls the **stake** contribution only. `reputation × latency_factor` is always weighted at exponent 1.0 (for the 2nd/3rd tier, it is the ONLY factor).

---

## Use Cases

| Use Case | alpha | Weight proportional to | Seed |
|----------|-------|------------------------|------|
| **Dispatch** | 1.0 | stake × reputation × speed | `task_id \|\| block_hash` |
| **Verification (1st tier)** | 0.5 | sqrt(stake × reputation × speed) | `task_id \|\| result_hash` |
| **Second verification (2nd tier)** | 0.0 | reputation × speed (stake ignored) | `task_id \|\| post_verification_block_hash` |
| **Third verification (3rd tier)** | 0.0 | reputation × speed (stake ignored) | `task_id \|\| post_second_verification_block_hash` |
| **Leader election** | 1.0 | stake × reputation × speed | `model_id \|\| sub_topic_id \|\| epoch_block_hash` |
| **Validator committee** | 1.0 | stake × reputation × speed | `epoch_block_hash` (100 members, 10 min rotation) |

---

## Design Rationale

### Dispatch (alpha = 1.0) -- proportional to stake

Selection probability is directly proportional to stake. This means a participant with 2x the stake gets dispatched roughly 2x as often.

**Why this works:** splitting stake across multiple identities yields zero benefit. If a Worker splits their stake into two equal accounts, each account gets half the selection probability -- the total expected work is unchanged. There is no incentive to Sybil-attack the dispatch system.

### Verification (alpha = 0.5) -- proportional to sqrt(stake)

Selection probability is proportional to the square root of stake. A participant with 4x the stake gets only 2x the verification probability.

**Why this works:** this reduces the ability of large stakeholders to dominate the verification pool. If a single entity controlled enough verifiers to collude with a dishonest Worker, the sqrt weighting makes that significantly more expensive -- they would need to quadruple their stake to double their chance of filling the 3 verifier slots.

### Second/Third verification (alpha = 0.0) -- stake ignored, rep × speed weighted

Stake has **zero influence** on 2nd/3rd-tier verifier selection. Whale stake cannot be used to control or predict the verification outcome. Reputation and latency still drive the selection, because those are earned signals of reliability and cost basis, not something that can be Sybil-attacked by splitting coins across accounts.

**Why this works:**
- **Stake-independent selection** makes 2nd/3rd-tier verification the last economic firewall. An attacker cannot increase their odds by buying more FAI; they must build a track record of good behavior (reputation) and fast response (latency_factor) across many nodes, which is substantially harder than buying stake.
- **Reputation × latency still matter** so honest, fast, well-behaved nodes are preferred — dishonest or chronically slow nodes drop out of contention even though stake is ignored.
- The 3rd-tier seed uses `post_second_verification_block_hash` (a future block hash unknown at 2nd-verification time), so the 3rd-tier verifier cannot be predicted while the 2nd-tier verification is still being computed.

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
