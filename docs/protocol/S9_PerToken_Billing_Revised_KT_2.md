# S9 Per-Token Billing Complete Specification

> Date: 2026-03-31
>
> Baseline: FunAI V5.2 Final + FunAI_V52_Supplement.md (S9)
>
> This document supersedes the original S9_PerToken_Billing_Supplement.md and serves as the sole specification document for per-token billing
>
> Document suffix: KT

---

## 1. Overview

Switching the billing model from per-request fixed fees to per-token usage-based billing. Users pay based on the actual number of input/output tokens consumed, with max_fee cap protection.

**Core Principles:**
- Users never overpay (max_fee cap)
- Workers are compensated based on volume (more work = more earnings)
- Token counts cannot be forged (Verifier cross-validation + second verification backstop)
- Distribution ratios remain unchanged (retaining V5.2's 95/4.5/0.5)
- Implement all at once, no phased rollout

---

## 2. S9.A — Shadow Balance + Worker Local Truncation

### 2.1 Three-Layer Balance Protection

| Layer | Who | When | What It Protects |
|---|------|---------|---------|
| SDK | EstimateFee calculation | Before sending request | Reject outright if balance is insufficient |
| Leader | Check on-chain balance + local shadow balance | Upon receiving request | Reject if available < max_fee |
| On-chain | Proposer checks balance at settlement | At settlement | Cap at available balance if insufficient, ensuring no over-deduction |

Note: The Leader is a P2P role and cannot write on-chain state. V5.2 Section 2.1 explicitly states that all inference is P2P and does not go through the chain; inference requests are not submitted on-chain, only BatchSettlement is. Therefore, no on-chain pre-deduction freeze is performed; instead, the Leader uses a local shadow balance.

### 2.2 Complete Flow

```
Request Phase
  1. Developer sets fee_per_input_token, fee_per_output_token, max_tokens in the SDK
  2. SDK calculates max_fee = input_tokens × input_price + max_tokens × output_price
  3. SDK queries on-chain InferenceAccount balance
  4. Balance < max_fee → SDK returns error directly, does not submit transaction
  5. Balance >= max_fee → Submit InferRequest

Dispatch Phase (Leader Shadow Balance)
  6. Leader receives request → queries on-chain balance (cached, refreshed every 5 seconds) → subtracts the user's local pending_fees total
  7. available < max_fee → reject, return insufficient_balance
  8. available >= max_fee → append this entry to pending_fees[user] → dispatch to Worker
  (Note: Shadow balance is a best-effort protection, not an on-chain guarantee. In extreme cases, over-approval may still occur; the settlement layer caps at available balance as a backstop.)

Generation Phase (Worker Local Truncation)
  9. Worker receives AssignTask (containing fee_per_input_token, fee_per_output_token, max_fee)
  10. Worker generates tokens one by one, computing locally after each token:
      running_cost = input_tokens × fee_per_input_token
                   + current_output_tokens × fee_per_output_token
  11. running_cost >= budgetLimit → Worker stops generation, sends IsFinal=true
  12. Worker constructs InferReceipt (containing input_token_count, output_token_count)

Verification Phase
  13. Verifier validates SHA256(prompt) == InferRequest.prompt_hash (prevents Worker from tampering with prompt)
  14. Verifier performs teacher forcing → independently obtains the true token count
  15. Verifier submits VerifyResult (containing verified_input_tokens, verified_output_tokens)

Settlement Phase
  16. Compare Worker's self-reported token count vs Verifier median (see S9.B)
  17. Calculate actual_fee based on confirmed token count
  18. actual_fee <= max_fee → deduct actual_fee, refund max_fee - actual_fee
  19. actual_fee > max_fee → deduct max_fee (capped; theoretically should not occur since Worker already truncated)
```

### 2.3 Leader Shadow Balance Implementation

The Leader locally maintains `pending_fees map[user_address][]PendingEntry`, purely in-memory, not on-chain.

#### Data Structure

```go
type PendingEntry struct {
    TaskId      []byte
    MaxFee      uint64
    ExpireBlock uint64
}

type LeaderState struct {
    activeInferenceTasks map[string]uint32         // Already exists in S1
    activeVerifyTasks    map[string]uint32         // Already exists in S2
    pendingFees          map[string][]PendingEntry // New in S9, key: user_address
}
```

#### Balance Check at Dispatch

```go
func (l *Leader) checkBalance(userAddr string, maxFee uint64, expireBlock uint64) bool {
    l.cleanExpiredPending()

    onChainBalance := l.getCachedBalance(userAddr) // Cached, refreshed every 5 seconds

    var totalPending uint64
    for _, entry := range l.pendingFees[userAddr] {
        totalPending += entry.MaxFee
    }

    available := onChainBalance - totalPending
    if totalPending > onChainBalance {
        available = 0
    }

    return available >= maxFee
}
```

#### Release Timing

| Trigger | When | Action |
|------|------|------|
| Normal completion | Leader observes InferReceipt for the task_id | Remove the corresponding PendingEntry |
| Timeout | currentBlock > entry.ExpireBlock | Clean up expired entries |
| Leader rotation | New Leader takes over | pending_fees starts from zero |

```go
func (l *Leader) cleanExpiredPending() {
    currentBlock := l.getCurrentBlock()
    for user, entries := range l.pendingFees {
        kept := entries[:0]
        for _, e := range entries {
            if currentBlock <= e.ExpireBlock {
                kept = append(kept, e)
            }
        }
        if len(kept) == 0 {
            delete(l.pendingFees, user)
        } else {
            l.pendingFees[user] = kept
        }
    }
}
```

#### Leader Rotation Handling

When a new Leader takes over, pending_fees is empty. Worst case: a brief window after rotation where a few extra requests are admitted. Optional solution: S4's LeaderSync could also synchronize pending_fees. Not mandatory — the risk during the rotation window (a few seconds) is minimal, and the settlement layer provides a backstop.

#### Cross-Model Leader Desynchronization

If a user simultaneously sends large requests to model A and model B, the two Leaders' shadow balances are invisible to each other. This is not addressed — cross-Leader synchronization is equivalent to distributed locking, and the complexity is not worth it. If it does happen, the Proposer settles in chronological order at settlement time; later entries with insufficient balance are capped at the available balance.

### 2.4 Worker Local Truncation Logic

```go
func (w *Worker) shouldStopGeneration(task *AssignTask, currentOutputTokens uint32) bool {
    if task.FeePerInputToken == 0 || task.FeePerOutputToken == 0 {
        return false // per-request mode, no truncation (relies on max_tokens limit)
    }
    inputCost := uint64(task.InputTokenCount) * task.FeePerInputToken
    outputCost := uint64(currentOutputTokens) * task.FeePerOutputToken
    runningCost := inputCost + outputCost

    budgetLimit := task.MaxFee * 95 / 100
    // Ensure at least 1 output token can be generated
    minBudget := inputCost + task.FeePerOutputToken
    if budgetLimit < minBudget {
        budgetLimit = minBudget
    }
    // But do not exceed max_fee itself
    if budgetLimit > task.MaxFee {
        budgetLimit = task.MaxFee
    }

    return runningCost >= budgetLimit
}
```

Logic: truncate at 95%, but reserve enough budget for at least 1 output token. If max_fee cannot even cover input_cost + 1 output token, truncate at max_fee itself.

**Why not have the Leader send a StopSignal:**
- The Leader rotates every 30 seconds; running_cost state is lost during rotation
- The Worker already knows all billing parameters (passed via AssignTask) and can compute on its own
- One fewer protocol message, one fewer signature verification, one fewer Leader responsibility

### 2.5 Edge Cases

| Scenario | Handling |
|------|---------|
| max_fee is exactly enough for 1 output token | Generate 1 token normally, then truncate |
| max_fee = 0 | Leader rejects, returns invalid_parameters |
| max_fee is extremely small (< input_cost + 1 token) | Worker generates as much as possible until max_fee is exhausted |
| Worker ignores truncation and continues generating | Settlement uses min(actual_cost, max_fee); excess is not charged |
| per-token fields are all 0 | Settle using max_fee as fixed fee (per-request mode, backward compatible) |

### 2.6 SDK EstimateFee

```go
// Pure multiplication, no estimation, no guessing
// Developer provides max_tokens themselves (based on product requirements)
// input_tokens is obtained precisely by the SDK locally tokenizing the prompt
func EstimateFee(inputTokens uint32, maxTokens uint32, inputPrice uint64, outputPrice uint64) uint64 {
    return uint64(inputTokens) * inputPrice + uint64(maxTokens) * outputPrice
}
```

The SDK does not presume any scenarios and does not fill in default values. If the developer does not set max_tokens, an error is returned.

### 2.7 SDK Parameter Design Principles

All parameters affecting billing and experience are explicitly set by the developer; the SDK only provides default values and validation:

| Parameter | Set By | Default | SDK Behavior |
|------|------|--------|---------|
| max_tokens | Required from developer | None (error if not set) | Validate range [1, 8192] |
| fee_per_input_token | Developer | 0 (per-request mode) | 0 = per-request, >0 = per-token |
| fee_per_output_token | Developer | 0 (per-request mode) | Must be set together with input or both unset |
| max_fee | Developer or computed by EstimateFee | None (error if not set) | Validate > 0 |
| infer_timeout | Developer | 30 seconds | Validate range [5s, 120s] |
| task_type | Developer | 0 (TEXT_GENERATION) | Currently only 0 is supported |
| content_tag | Developer | 0 (GENERAL) | 0/1/2 |

### 2.8 How OpenClaw Skill Developers Configure

OpenClaw Skill developers do not use the FunAI SDK directly. They fill in parameters in the Skill configuration file, and the OpenClaw platform translates them into FunAI SDK calls:

```yaml
# skill.yaml — OpenClaw Skill configuration
name: my-chatbot
model: qwen-32b-chat              # model alias (resolved to model_id through OpenClaw model routing)
max_tokens: 1024                   # Developer sets based on product requirements
infer_timeout: 45s                 # Developer sets based on model size
pricing:
  fee_per_input_token: 500         # ufai/token
  fee_per_output_token: 1500       # ufai/token
  # max_fee is automatically computed by OpenClaw calling EstimateFee; developer does not need to manage it
content_tag: general               # general / nsfw / untagged
```

OpenClaw platform internal processing:

```
Skill user sends message
  → OpenClaw reads skill.yaml configuration
  → Locally tokenize user input → input_tokens = 326
  → EstimateFee(326, 1024, 500, 1500) → max_fee = 1,699,000 ufai
  → Call FunAI SDK:
      client.Infer({
          ModelId:           resolveAlias("qwen-32b-chat"),
          Prompt:            userMessage,
          MaxTokens:         1024,
          MaxFee:            1699000,
          FeePerInputToken:  500,
          FeePerOutputToken: 1500,
          InferTimeout:      45s,
      })
  → Return result to Skill user
```

**Skill developers only need to fill in the yaml; they never touch SDK code. End users are completely unaware.**

"Auto-pricing" mode is supported:

```yaml
# skill.yaml — Auto-pricing
name: simple-bot
model: qwen-7b-chat
max_tokens: 512
pricing: auto    # OpenClaw queries the network's current average price and fills it automatically
```

OpenClaw periodically queries the average fee_per_token for each model from the chain and automatically sets a competitive price. Zero configuration for developers.

### 2.9 Backward Compatibility

| Client Version | Behavior |
|-----------|------|
| Old SDK (per-request) | fee_per_input_token=0, fee_per_output_token=0 → settle at fixed max_fee |
| New SDK (per-token) | fee_per_input_token>0, fee_per_output_token>0 → settle based on actual tokens |
| Both set | per-token takes precedence (fee_per_*_token > 0 has priority) |

---

## 3. S9.B — Two-Party Cross-Validation of Token Counts

### 3.1 Attack Scenario

The Worker actually generated 500 output tokens, but reports `output_token_count = 1000` in the InferReceipt, attempting to charge double the fee.

### 3.2 Two Independent Counting Sources

```
Source 1: Worker Self-Report
  InferReceipt.output_token_count = N_worker
  Signed, cannot be tampered with, but can lie

Source 2: Verifier Independent Verification
  Before Verifier performs teacher forcing:
    1. Receives the prompt and original InferRequest forwarded by Worker
    2. Validates SHA256(prompt) == InferRequest.prompt_hash
       → Mismatch → discard, do not participate in verification (prompt was tampered with)
    3. Locally tokenize prompt → verified_input_tokens
    4. Teacher forcing produces complete output → locally tokenize → verified_output_tokens
  Signed, cryptographically verifiable

  Note: prompt_hash in InferRequest is covered by the user's signature; Worker cannot tamper with it.
  This guarantees the Verifier receives the same prompt the user sent.
```

### 3.3 Tokenizer Consistency Requirement

```
Token counting must use the model's bundled tokenizer (i.e., tokenizer.json
or sentencepiece.model from the model repository); external libraries'
default tokenizers must not be used.

All nodes for the same model_id (Worker / Verifier / SecondVerifier) use the same
model files, so the tokenizer is fully consistent. Token count differences
can only come from:
  - Floating-point precision causing generation length differences of 1-2 tokens
  - Implementation bugs

The tolerance max(2, count × 2%) is to accommodate extreme edge cases, not
to accommodate tokenizer inconsistencies.
If testing confirms that same-model tokenizers are fully consistent (which
they should be), the token_count_tolerance_pct can be reduced from 2% to 1%
or even 0% (keeping only the absolute tolerance of 2) via governance vote.
```

### 3.4 Comparison Rules at Settlement

```
Tolerance formula: tolerance = max(token_count_tolerance, count × token_count_tolerance_pct / 100)
Default: max(2, count × 2%)

3 Verifiers each report verified_output_tokens, take the median = N_median

Case A: |N_worker - N_median| <= tolerance
  → Settle at N_worker (consistent, normal)

Case B: |N_worker - N_median| > tolerance
  → Settle at N_median (Worker falsely reported)
  → Worker dishonest_count += 1
  → Cumulative >= 3 times → jail

Input tokens follow the same logic:
  Worker reports input_token_count vs Verifier reports verified_input_tokens
  Same determination rules apply
```

### 3.5 Why Workers Cannot Cheat

| Cheating Method | Caught By | Reason |
|---------|---------|------|
| Falsely reporting output_token_count | Verifier | Token count from teacher forcing does not match |
| Sending fake StreamTokens | Verifier | result_hash does not match |
| Giving Verifier a tampered prompt | Verifier | SHA256(prompt) != prompt_hash, discarded |
| Colluding with Verifier | SecondVerifier | SecondVerifiers are VRF-randomly selected, perform independent teacher forcing to obtain true counts (see S9.D) |

### 3.6 Protocol Fields

#### InferRequest Field Definition

```
Field rename note:
  InferRequest.fee (protobuf field #3, uint64) is renamed to max_fee.
  Type unchanged (uint64), field number unchanged (#3), wire format fully compatible.
  Semantic change: original fee was a fixed settlement price; max_fee is the user's willingness-to-pay ceiling.
  The fee value sent by old SDKs will be read as max_fee by the new code, with per-token fields being 0
  → settles in per-request mode → behavior unchanged. No new field number added, no old field deprecated. Pure rename.

InferRequest {
    model_id:              bytes32   // Existing
    prompt_hash:           bytes32   // Existing
    max_fee:               uint64    // Renamed: fee → max_fee (protobuf #3 unchanged, binary compatible)
    expire_block:          uint64    // Existing
    user_seed:             bytes32   // Existing
    temperature:           uint16    // Existing
    timestamp:             uint64    // Existing
    user_pubkey:           bytes33   // Existing
    user_signature:        bytes65   // Existing
    prompt:                string    // Existing (not covered by signature)
    max_tokens:            uint32    // S3
    fee_per_input_token:   uint64    // New in S9: ufai/token, 0=per-request mode
    fee_per_output_token:  uint64    // New in S9: ufai/token, 0=per-request mode
    task_type:             uint32    // S7
    content_tag:           uint32    // S8
}

SignBytes covers all fields except prompt and user_signature
```

#### InferReceipt Field Definition

```
InferReceipt {
    task_id:               bytes32   // Existing
    worker_pubkey:         bytes33   // Existing
    worker_logits:         [5]float32 // Existing
    result_hash:           bytes32   // Existing
    final_seed:            bytes32   // Existing
    sampled_tokens:        [5]uint32 // Existing
    worker_sig:            bytes65   // Existing
    input_token_count:     uint32    // New in S9: Worker-counted input token count
    output_token_count:    uint32    // New in S9: Worker-counted output token count
    // input_token_count and output_token_count are covered by worker_sig signature
}
```

#### VerifyResult Field Definition

```
VerifyResult {
    task_id:                bytes32   // Existing
    verifier_addr:          bytes     // Existing
    pass:                   bool      // Existing
    logits_match:           uint8     // Existing
    sampling_match:         uint8     // Existing
    logits_hash:            bytes32   // Existing
    signature:              bytes65   // Existing
    verified_input_tokens:  uint32    // New in S9: Input token count confirmed by Verifier teacher forcing
    verified_output_tokens: uint32    // New in S9: Output token count confirmed by Verifier teacher forcing
    // Both new fields are covered by the signature
}
```

---

## 4. S9.C — Settlement Formula

### 4.1 Per-Token Settlement

```
// token_count uses the confirmed value after S9.B cross-validation
actual_fee = confirmed_input_tokens × fee_per_input_token
           + confirmed_output_tokens × fee_per_output_token

if actual_fee > max_fee:
    actual_fee = max_fee  // User never overpays

refund = max_fee - actual_fee  // Refund the difference
```

### 4.2 Fee Distribution (Retaining V5.2 Section 11 Ratios, Unchanged)

```
SUCCESS:
  worker_reward   = actual_fee × 950 / 1000  (95%)
  verifier_reward = actual_fee × 45 / 1000   (4.5%, split equally among 3 verifiers)
  multi_verification_fund      = actual_fee × 5 / 1000    (0.5%)

  refund_to_user  = max_fee - actual_fee
```

### 4.3 FAIL Scenario

```
Verification FAIL (Worker output is incorrect):
  fail_base = confirmed_input_tokens × fee_per_input_token
            + confirmed_output_tokens × fee_per_output_token
  fail_fee = fail_base × FailSettlementFeeRatio / 1000  (default 50/1000 = 5%)

  verifier_reward = fail_fee × 45 / 1000
  multi_verification_fund      = fail_fee × 5 / 1000
  worker_reward   = 0
  Worker → jail

  refund_to_user = max_fee - fail_fee
```

### 4.4 Worker Timeout

```
Worker timeout (InferReceipt not submitted before expire_block):

  Billing:
    timeout_fee = max_fee × FailSettlementFeeRatio / 1000  (= max_fee × 5%)
    (No verified token count available at timeout, cannot calculate actual_fee; uniformly use 5% of max_fee)

  Distribution:
    worker_reward    = 0             (Worker gets nothing)
    verifier_reward  = 0             (Verification did not occur)
    multi_verification_fund      += timeout_fee   (Full amount goes to multi-verification fund, subsidizing network maintenance costs)

  Refund:
    refund_to_user = max_fee - timeout_fee  (= 95%)

  Penalty:
    Worker → jail (shares progressive mechanism with verification FAIL)

  User Experience:
    The user may have already received partial output via StreamToken (P2P real-time streaming),
    This partial output is unverified; the user decides whether to use it.
    The protocol layer does not charge for unverified output.

  Anti-Spam:
    The 5% cost prevents attackers from consuming network resources at zero cost.
    Sending 100 spam requests (each with max_fee=100000 ufai), even if all time out,
    attacker's loss = 100 × 100000 × 5% = 500000 ufai.
```

### 4.5 Overflow Protection

```go
inputCost := uint64(confirmedInputTokens) * entry.FeePerInputToken
outputCost := uint64(confirmedOutputTokens) * entry.FeePerOutputToken
actualFee := inputCost + outputCost

// Overflow detection
if inputCost / entry.FeePerInputToken != uint64(confirmedInputTokens) ||
   outputCost / entry.FeePerOutputToken != uint64(confirmedOutputTokens) ||
   actualFee < inputCost {
    actualFee = entry.MaxFee // Overflow → cap
}
if actualFee > entry.MaxFee {
    actualFee = entry.MaxFee // Normal cap
}
```

### 4.6 Billing Mode Determination

```
When request arrives:
├── FeePerInputToken == 0 || FeePerOutputToken == 0
│   └── Use MaxFee as fixed fee (per-request mode, backward compatible)
│
└── FeePerInputToken > 0 && FeePerOutputToken > 0
    ├── PerTokenBillingEnabled == false (on-chain parameter)
    │   └── Still use MaxFee (governance not enabled, ignore per-token fields)
    │
    └── PerTokenBillingEnabled == true
        └── Settle based on actual token count × unit price, capped at MaxFee
```

---

## 5. S9.D — Anti-Cheating Rules

Per-token billing introduces 3 cheating incentives that do not exist in per-request mode:

| # | Cheating Method | Incentive | Severity |
|---|---------|------|--------|
| C1 | Worker falsely reports token count | Actually 500, reports 1000, charges double | P0 |
| C2 | Worker + Verifier collude to falsely report | Both report inflated numbers, split the money privately | P0 |
| C3 | Worker pads output to extend length | Adds gibberish after a normal answer, earns more token fees | P2 |

### 5.1 C1 Defense — Verifier Cross-Validation (Settlement Layer)

At settlement, Worker's self-reported token count vs 3 Verifiers' median is compared. If inconsistent, settle at Verifier count + dishonest_count++. See S9.B Section 3.4 for determination rules.

### 5.2 C2 Defense — Worker+Verifier Pair-Level Tracking + Second verification Backstop

#### 5.2.1 Problem

When a Worker and Verifier collude as a fixed pair, per-Worker level statistics get diluted — a Worker paired with 10 different Verifiers, colluding only a few times with each, results in a very low per-Worker ratio that cannot be detected.

#### 5.2.2 Pair-Level Tracking Data Structure

New per-pair statistics added on-chain:

```go
type TokenMismatchRecord struct {
    WorkerAddress   string
    VerifierAddress string
    TotalTasks      uint32
    MismatchCount   uint32
}
// Storage key: 0x10 || worker_addr || verifier_addr
```

#### 5.2.3 Recording Timing

After each settlement completes, update the pair record for each participating Verifier:

```go
func (k Keeper) updateTokenMismatchPair(ctx sdk.Context, workerAddr string, verifierAddr string, isMismatch bool) {
    record := k.GetTokenMismatchRecord(ctx, workerAddr, verifierAddr)
    record.TotalTasks++
    if isMismatch {
        record.MismatchCount++
    }

    // Sliding window: compress after exceeding lookback
    if record.TotalTasks >= params.TokenMismatchLookback {
        record.TotalTasks = record.TotalTasks / 2
        record.MismatchCount = record.MismatchCount / 2
    }

    k.SetTokenMismatchRecord(ctx, workerAddr, verifierAddr, record)
}
```

Determining "deviation": A Verifier's reported count deviates from the confirmed value (median) by more than `token_mismatch_deviation_pct`, and is marked as a mismatch.

#### 5.2.4 Second-Verification Rate Weighting

```go
func (k Keeper) calculateWorkerSecond verificationBoost(ctx sdk.Context, workerAddr string) uint32 {
    pairs := k.GetAllTokenMismatchRecords(ctx, workerAddr)

    maxPairRatio := uint32(0)
    for _, pair := range pairs {
        if pair.TotalTasks < params.TokenMismatchPairMinSamples {
            continue
        }
        ratio := pair.MismatchCount * 100 / pair.TotalTasks
        if ratio > maxPairRatio {
            maxPairRatio = ratio
        }
    }

    if maxPairRatio > 50 {
        return params.TokenMismatchSecond verificationWeight  // Default 20, maximum weighting
    } else if maxPairRatio > 30 {
        return params.TokenMismatchSecond verificationWeight / 2  // 10, medium weighting
    }
    return 0
}
```

Second-verification rate formula (extending V5.2 Section 13.9):

```
second_verification_rate = base_rate × (1 + 10 × recent_second verification_overturn_ratio
                            + 50 × recent_third_verification_overturn
                            + worker_second verification_boost / 10)
```

#### 5.2.5 Second verification Layer Token Count Comparison

SecondVerifiers independently perform teacher forcing to obtain the true token count. Second verification determination adds token count comparison:

```
if PerTokenBillingEnabled:
    settled_output = output_token_count used at settlement
    second verifier_median_output = median of verified_output_tokens reported by 3 second verifiers
    tolerance = max(token_count_tolerance, settled_output × token_count_tolerance_pct / 100)

    if |settled_output - second verifier_median_output| > tolerance:
        → Token count fraud determination
        → Worker jail
        → Verifiers whose reported counts deviate significantly from second verification results → direct jail (collusion is deliberate malice, no three-strike chance)
        → Re-settle based on second verifier_median_output, refund excess to user
```

#### SecondVerificationResponse Field Definition

```
SecondVerificationResponse {
    task_id:                bytes32   // Existing
    pass:                   bool      // Existing
    second verifier_addr:           bytes     // Existing
    logits_hash:            bytes32   // Existing
    signature:              bytes65   // Existing (already required to be added in the dispatch second verification fix KT)
    verified_input_tokens:  uint32    // New in S9
    verified_output_tokens: uint32    // New in S9
    // Both new fields are covered by the signature
}
```

#### Re-settlement Logic

```go
func resettleWithCorrectedTokenCount(apt SecondVerificationPendingTask, correctedOutputTokens uint32) {
    originalFee := apt.SettledFee
    correctedFee := uint64(apt.InputTokenCount) * apt.FeePerInputToken +
                    uint64(correctedOutputTokens) * apt.FeePerOutputToken

    if correctedFee > apt.MaxFee {
        correctedFee = apt.MaxFee
    }

    if correctedFee < originalFee {
        refund := originalFee - correctedFee
        refundUser(apt.UserAddress, refund)
        clawbackFromWorker(apt.WorkerAddress, refund)
    }
}
```

### 5.3 C3 Defense — Padding Output to Extend Length (No Protocol-Level Changes Needed)

Triple caps keep losses within acceptable limits:

```
Cap 1: max_tokens (set by developer) → Worker generates at most max_tokens tokens
Cap 2: max_fee → User pays at most max_fee
Cap 3: Worker local truncation → stops when running_cost >= budgetLimit
```

Padded content is genuinely generated by the model, logits match verification passes, and the protocol layer cannot distinguish "useful output" from "padding gibberish." This is a product-level issue, determined by market competition — Workers with poor output quality are naturally eliminated by users/developers.

### 5.4 dishonest_count Cumulative Penalty Mechanism

#### Worker New Fields

```
Worker {
    // Existing 23 fields ...
    dishonesty_count: uint32  // Field 24: Cumulative count of falsely reported token numbers
}
```

#### Penalty Logic

```
Worker falsely reports token count (S9.B Case B):
  dishonesty_count += 1
  Cumulative >= dishonesty_jail_threshold (default 3) → jail
  dishonesty_count resets to 0 after jail
  Jail progression is shared with verification FAIL: 1st time 10 minutes, 2nd time 1 hour, 3rd time slash 5% + tombstone

Verifier caught colluding by second verification:
  → Direct jail, no three-strike chance (collusion is deliberate malice, not an innocent mistake)

Upon 50 consecutive successes (SuccessResetThreshold):
  dishonesty_count also resets to 0
```

---

## 6. S9.E — SettlementEntry Transmitted Fields

Each record in the BatchSettlement message needs to transmit per-token fields:

```
SettlementEntry {
    // Existing fields ...
    task_id:               bytes32
    user_address:          string
    worker_address:        string
    max_fee:               uint64    // Renamed: fee → max_fee
    pass:                  bool
    expire_block:          uint64
    sig_hashes:            bytes
    verifier_addresses:    []string
    verifier_votes:        []bool

    // New in S9
    fee_per_input_token:   uint64
    fee_per_output_token:  uint64
    input_token_count:     uint32    // Confirmed value after S9.B cross-validation
    output_token_count:    uint32    // Confirmed value after S9.B cross-validation
}
```

When the Proposer packages a batch, it takes values from InferRequest + InferReceipt + VerifyResult, performs cross-validation, and fills in the confirmed token counts.

---

## 7. S9.F — Complete Token Count Verification Flow

```
Worker completes inference
  ├── InferReceipt { input_token_count: 156, output_token_count: 423 }  (Worker self-report)
  │
  ├── Verifier ×3 perform teacher forcing
  │   ├── Validate SHA256(prompt) == prompt_hash ✅
  │   ├── Verifier A: { verified_output_tokens: 423 }
  │   ├── Verifier B: { verified_output_tokens: 423 }
  │   └── Verifier C: { verified_output_tokens: 423 }
  │
  ├── Settlement layer comparison (executed by Proposer)
  │   │
  │   ├── Case A: Worker reports 423, Verifier median 423
  │   │   → Consistent → settle at 423 ✅
  │   │   → Update pair records: Worker-A/B/C each +1 total, +0 mismatch
  │   │
  │   ├── Case B: Worker reports 800, Verifier median 423
  │   │   → Inconsistent → settle at 423 + Worker dishonest_count += 1
  │   │
  │   └── Collusion scenario: Worker reports 800, Verifier A/B report 800, Verifier C reports 423
  │       → Median 800 → settle at 800
  │       → Update pair records: Worker-C mismatch +1 (C is honest but marked as the minority)
  │       → But Worker-A/B pairs are frequently "consistent" with Worker at high numbers
  │       → Pair-level second verification rate increases → second verification triggered
  │
  └── Second verification (if triggered)
      ├── SecondVerifier ×3 perform teacher forcing
      │   ├── SecondVerifier A: { verified_output_tokens: 423 }
      │   ├── SecondVerifier B: { verified_output_tokens: 423 }
      │   └── SecondVerifier C: { verified_output_tokens: 423 }
      │
      └── Second verification determination
          ├── settled=800, second verifier median=423 → token count fraud
          │   → Worker jail
          │   → Verifier A/B (reported counts deviate significantly from second verification) → direct jail
          │   → Re-settle: bill at 423, refund difference to user
          │
          └── settled=423, second verifier median=423 → confirmed correct ✅
```

---

## 8. Economic Impact Analysis

### 8.1 Impact on Users

| Scenario | per-request (current) | per-token (S9) |
|------|-------------------|----------------|
| Short reply (50 tokens) | Fixed max_fee | ~5% max_fee (95% savings) |
| Normal reply (500 tokens) | Fixed max_fee | ~50% max_fee (50% savings) |
| Long reply (uses all max_tokens) | Fixed max_fee | = max_fee (same) |

Users benefit significantly in short-reply scenarios, incentivizing frequent daily usage.

### 8.2 Impact on Miners

| Metric | per-request | per-token |
|------|------------|-----------|
| Revenue predictability | Fixed | Usage-based (fairer) |
| Long reply incentive | None | More tokens = more revenue |
| Cheating incentive | None | Exists → suppressed by S9.B + S9.D |
| Truncation risk | None | Worker local truncation (predictable) |

---

## 9. Section 23 Parameter Table Update

### New On-Chain Parameters

```
| Parameter                          | Type    | Default | Description                                         |
|----------------------------------|---------|-------|----------------------------------------------|
| per_token_billing_enabled         | bool    | false | Whether per-token billing is enabled (activated by governance vote)            |
| token_count_tolerance             | uint32  | 2     | Absolute tolerance allowed for token count comparison                        |
| token_count_tolerance_pct         | uint32  | 2     | Percentage tolerance allowed for token count comparison (%)                   |
| dishonesty_jail_threshold         | uint32  | 3     | Number of cumulative false token reports to trigger jail                  |
| token_mismatch_second verification_weight       | uint32  | 20    | Maximum weighting coefficient for pair-level token deviation on second verification rate            |
| token_mismatch_lookback           | uint32  | 100   | Pair statistics sliding window size (number of entries)                      |
| token_mismatch_deviation_pct      | uint32  | 20    | Deviation from confirmed value exceeding this percentage counts as mismatch        |
| token_mismatch_pair_min_samples   | uint32  | 5     | Pair records below this count are excluded from second verification rate calculation               |
```

### New/Modified Message Fields

```
InferRequest new fields:
| Field                  | Type   | protobuf # | Description                                    |
|----------------------|--------|-----------|------------------------------------------|
| max_fee              | uint64 | 3         | Renamed: fee → max_fee (binary compatible, pure rename)      |
| fee_per_input_token  | uint64 | 12        | ufai/input token, 0=per-request mode       |
| fee_per_output_token | uint64 | 13        | ufai/output token, 0=per-request mode      |

InferReceipt new fields:
| Field               | Type   | protobuf # | Description                              |
|-------------------|--------|-----------|-----------------------------------|
| input_token_count | uint32 | 8         | Worker-counted input token count (covered by signature) |
| output_token_count| uint32 | 9         | Worker-counted output token count (covered by signature)|

VerifyResult new fields:
| Field                    | Type   | protobuf # | Description                                    |
|------------------------|--------|-----------|----------------------------------------|
| verified_input_tokens  | uint32 | 8         | Input count confirmed by Verifier teacher forcing   |
| verified_output_tokens | uint32 | 9         | Output count confirmed by Verifier teacher forcing  |

SecondVerificationResponse new fields:
| Field                    | Type   | protobuf # | Description                                    |
|------------------------|--------|-----------|----------------------------------------|
| verified_input_tokens  | uint32 | 6         | Input count confirmed by second verifier teacher forcing      |
| verified_output_tokens | uint32 | 7         | Output count confirmed by second verifier teacher forcing     |

SettlementEntry new fields:
| Field                  | Type   | protobuf # | Description                              |
|----------------------|--------|-----------|-----------------------------------|
| fee_per_input_token  | uint64 | 10        | Passed from InferRequest               |
| fee_per_output_token | uint64 | 11        | Passed from InferRequest               |
| input_token_count    | uint32 | 12        | Cross-validation confirmed input token count       |
| output_token_count   | uint32 | 13        | Cross-validation confirmed output token count      |

Worker new fields:
| Field              | Type   | protobuf # | Description                          |
|------------------|--------|-----------|-------------------------------|
| dishonesty_count | uint32 | 24        | Cumulative count of falsely reported token numbers            |

TokenMismatchRecord new (on-chain storage):
| Field              | Type   | Description                          |
|------------------|--------|-------------------------------|
| worker_address   | string | Worker address                    |
| verifier_address | string | Verifier address                  |
| total_tasks      | uint32 | Total tasks for this pair (within sliding window)     |
| mismatch_count   | uint32 | Token deviation count for this pair          |
```

---

## 10. Test Cases

```
Per-Token Settlement:
  PT1: per-token normal settlement (actual < max_fee → deduct actual, refund difference)
  PT2: per-token cap (actual > max_fee → deduct max_fee)
  PT3: per-token disabled falls back to per-request (PerTokenBillingEnabled=false)
  PT4: per-token FAIL scenario (deduct actual × 5%, refund remainder)
  PT5: fee_per_input=0 fallback to max_fee
  PT6: uint64 overflow protection
  PT7: Fee total conservation (user_debit == executor + verifier + multi_verification_fund + refund)
  PT8: Timeout scenario (deduct max_fee × 5% to multi-verification fund, refund 95%, Worker jail)

Anti-Cheating:
  AC1: Worker reports honestly → settle at Worker count
  AC2: Worker falsely reports → settle at Verifier median + dishonest_count++
  AC3: Cumulative 3 false reports → jail
  AC4: Worker + 2 Verifiers collude → second verification overturns + re-settle + refund
  AC5: Difference within tolerance → treated as consistent
  AC6: SuccessStreak resets dishonesty_count
  AC7: per-token disabled skips token count comparison
  AC8: Pair-level tracking — fixed-partner collusion triggers higher second verification rate
  AC9: Colluding Verifier caught by second verification → direct jail (no three strikes)
  AC10: Pair statistics sliding window compression

Worker Truncation:
  TR1: Worker stops generation when running_cost reaches budgetLimit
  TR2: per-request mode does not truncate (relies on max_tokens limit)
  TR3: Worker ignores truncation → settlement uses min(actual_cost, max_fee)
  TR4: Extremely small max_fee → generate at least 1 output token

Shadow Balance:
  SB1: Leader shadow balance correctly blocks insufficient balance requests
  SB2: Pending released after InferReceipt arrives
  SB3: Expired entries are cleaned up
  SB4: pending_fees starts from zero after Leader rotation
```

---

## 11. Differences from the Original S9 Document

| Location | Original S9 | This Document | Reason for Change |
|------|-------|--------|---------|
| S9.A pre-deduction | On-chain freeze of max_fee | Leader local shadow balance | Leader is a P2P role and cannot write to chain |
| S9.A truncation | Leader sends StopSignal | Worker local truncation | Leader rotates every 30 seconds, losing state |
| S9.A truncation edge case | No generation when budgetLimit=0 | Generate at least 1 output token | Extremely small max_fee protection |
| S9.A message | New StopSignal message type added | Removed | Worker computes on its own, no external signal needed |
| S9.B verification | Three-party (Worker/Proposer/Verifier) | Two-party (Worker/Verifier) | Proposer cannot see StreamToken |
| S9.B verification chain | No prompt_hash validation | Verifier first validates prompt_hash | Prevents Worker from tampering with prompt |
| S9.B determination | Cases A-D (four types) | Cases A-B (two types) | Only two needed after removing Proposer |
| S9.B tolerance | delta <= 2 | max(2, count × 2%) | Tokenizer version differences may exceed 2 |
| S9.B tokenizer | Unconstrained | Must use model's bundled tokenizer | Ensures consistent counting for same model_id |
| S9.C distribution | 95/3/1/1 (including burn) | 95/4.5/0.5 (V5.2 original ratios) | per-token only changes billing, not distribution |
| S9.C FAIL | actual_fee all goes to second verification + full refund to user | actual × 5% deducted + refund remainder | Original scheme created funds from nothing |
| S9.C timeout | Partial settlement based on unverified token count | Deduct max_fee × 5% + refund 95% | No verified token count available, and prevents zero-cost attacks |
| Anti-cheating stats | None / per-Worker | per-Worker+Verifier pair | per-Worker gets diluted and cannot detect collusion |
| Anti-cheating Verifier | dishonest_count += 1 | Direct jail when second verification discovers collusion | Collusion is deliberate malice |
| Implementation | Phased (Phase 1/2) | All at once | Changing protocol after decentralized system launch is too painful |
| EstimateFee | Contains scenario presets + buffer | Pure multiplication, developer sets max_tokens themselves | SDK does not guess scenarios |
| Protobuf | fee rename not explained | Explicitly stated field #3 is a pure rename, binary compatible | Eliminates ambiguity |

---

*Document version: V3 (final merged version, supersedes KT_1 + KT_2)*
*Date: 2026-03-31*
*Baseline: FunAI V5.2 Final + FunAI_V52_Supplement.md*
*Document suffix: KT*
