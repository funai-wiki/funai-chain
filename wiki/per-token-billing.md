# Per-Token Billing (S9)

Per-token billing ensures that users never overpay (capped at `max_fee`), Workers are paid proportionally to actual token output, and token counts are unforgeable through cross-verification. The fee split remains unchanged from per-request mode: **95% Worker / 4.5% verifiers / 0.5% multi-verification fund** (see [settlement](settlement.md)).

Source: [S9 Per-Token Billing Supplement](../docs/S9_PerToken_Billing_Supplement.md)

---

## Shadow Balance (Leader-Side)

Each [P2P layer](p2p-layer.md) Leader maintains an in-memory `pending_fees` map, refreshed every **5 seconds** from on-chain state.

```
available = on_chain_balance - total_pending
```

Pending entries are released when an `InferReceipt` arrives or the entry expires. Leaders for different models do **not** sync their shadow balances with each other -- each tracks only its own model's pending fees. See [overspend protection](overspend-protection.md) for the full 3-layer protection stack.

---

## Worker Local Truncation

Workers enforce a local budget limit to avoid generating tokens beyond what the user can pay for:

```
budgetLimit = max_fee * 95%
```

The Worker stops generating once cumulative token cost reaches `budgetLimit`. The minimum billable output is `input_cost + 1 output token` -- a request is never rejected solely because `max_tokens` is low, as long as at least one output token fits within the budget.

---

## SDK Fee Estimation

The [SDK](sdk.md) computes `max_fee` as:

```
max_fee = input_tokens * input_price + max_tokens * output_price
```

`max_tokens` is **required** when per-token billing is enabled. The SDK returns an error if the caller omits it.

---

## Backward Compatibility

| `input_price` | `output_price` | Governance flag | Mode |
|---------------|----------------|-----------------|------|
| 0 | 0 | any | Per-request (legacy) |
| > 0 | > 0 | enabled | Per-token |

Both prices must be zero for per-request mode. Both must be positive with the governance flag enabled for per-token mode.

---

## Two-Party Cross-Verification

Token counts are verified by comparing Worker self-reports against independent Verifier counts.

### Process

1. **Worker** reports `input_token_count` and `output_token_count` alongside the inference result.
2. **3 Verifiers** (selected via [VRF](vrf.md), alpha = 0.5) independently count tokens using teacher forcing + local tokenization.
3. The **median** of the 3 Verifier counts is taken as the reference.
4. **Tolerance**: `max(2, count * 2%)` -- differences within this range are considered a match.

### Outcome

| Condition | Settlement basis | Side effect |
|-----------|-----------------|-------------|
| Worker count within tolerance of median | Worker's reported count | None |
| Worker count outside tolerance | Verifier median count | `dishonesty_count++` for Worker |

---

## Anti-Cheat Mechanisms

| Code | Attack | Defense |
|------|--------|---------|
| C1 | Inflated token count | Verifier cross-check catches over-reporting via the two-party verification above |
| C2 | Worker-Verifier collusion | Pair-level tracking with sliding window (lookback = 100 tasks); deviation > 20% = mismatch; second verification rate boost up to +20 percentage points |
| C3 | Output padding (junk tokens) | Market competition drives honest pricing; `max_tokens` and `max_fee` caps limit damage |

---

## Dishonesty Penalties

When a Worker's `dishonesty_count` reaches the threshold of **3**, the Worker is jailed. Dishonesty jailing shares the same progression as [verification FAIL jailing](jail-and-slashing.md):

| Count | Duration | Effect |
|-------|----------|--------|
| 1st jail | 10 minutes (120 blocks) | Wait then `MsgUnjail` to resume |
| 2nd jail | 1 hour (720 blocks) | Wait then `MsgUnjail` to resume |
| 3rd jail | Permanent | Slash 5% stake + tombstone |

---

## Settlement Formulas

### Actual Fee

```
actual_fee = confirmed_input * input_price + confirmed_output * output_price
```

Capped at `max_fee` -- the user never pays more than their upfront commitment.

### Refund

```
refund = max_fee - actual_fee
```

The refund is credited back to the user's on-chain balance during [batch settlement](settlement.md).

---

## On-Chain Governance Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `per_token_billing_enabled` | bool | `false` | Master switch for per-token billing |
| `token_count_tolerance` | uint | `2` | Absolute minimum tolerance for token count mismatch |
| `token_count_tolerance_pct` | uint | `2%` | Percentage-based tolerance for token count mismatch |
| `dishonesty_jail_threshold` | uint | `3` | Number of dishonesty strikes before jailing |
| `token_mismatch_second verification_weight` | uint | `20` | Second-verification rate boost (percentage points) for collusion-suspected pairs |
