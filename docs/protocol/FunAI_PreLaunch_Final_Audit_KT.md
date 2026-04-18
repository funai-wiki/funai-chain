# FunAI Pre-Launch Final Audit — KT

**Version:** v1.0
**Date:** 2026-04-14
**Status:** Pending engineering review and execution
**Author:** jms (KT)

> **Translator's note:** This document was originally written in Chinese before the `audit → second_verification` rename landed (see PR #2). Where this text says "Auditor", the current code / whitepaper name is "SecondVerifier"; where it says "reaudit", the current term is "third verification"; where it says "audit fund", the current term is "multi-verification fund". The decisions themselves are terminology-agnostic; the rename is purely cosmetic.

---

## Summary

This document enumerates the protocol-level changes that must land before FunAI mainnet launch. **12 decisions** in three categories:

- **Code changes** (9 items) — require modification and testing
- **Parameter tuning** (1 item) — governance parameter values
- **Engineer confirmation** (2 items) — likely already implemented in current code; verify, and implement if missing

Total engineering estimate: **3–4 weeks**.

---

## 1. Change List (read this first)

| # | Change | Why | Effort |
|---|--------|-----|--------|
| 1 | Verifier / SecondVerifier rank window 10 → 21 | Under the 20–30% offline rate typical of consumer miners, rank-10 windows are too fragile during the bootstrap phase; rank 21 is still robust at 50% offline rate | 5 min |
| 2 | Add `top-p` sampling parameter | De-facto standard in the OpenAI-compatible API; without it, OpenClaw Skill developers cannot drop-in migrate | 2–3 days |
| 3 | Reputation mechanism (covers Worker / Verifier / SecondVerifier) | Breaks the "high-stake, low-uptime miner drags the whole network" loop; automatically ejects idle nodes when the FAI price drops | 6–7 days |
| 4 | Add `MaxLatencyMs` / `StreamMode` / `TopP` fields to `AssignTask` | Workers need to know the user's latency requirement (companion-style scenarios want ≤ 2 s), otherwise they cannot evaluate whether to accept | 0.5 day |
| 5 | Feed historical latency into the VRF weight | Low-latency scenarios (companion-style) need to prioritize fast nodes; do NOT use a self-declared Tier because the protocol cannot verify it | 2.5 days |
| 6 | Long-tail model activation / deactivation mechanism | Prevents a Sybil attacker from gathering 10 nodes to re-activate a dormant long-tail model and farm block rewards | 10 min |
| 7 | Worker user-data retention: 3 days → 48 hours | Legal-risk mitigation (data minimization); 48 hours still covers 99% of second-verification windows | 5 min |
| 8 | Revenue and block-reward distribution | The old 4.5% verifier incentive was too low — participation suffered; the audit fund is split out to 3% | Governance parameter, 0 days |
| 9 | `ComputeModelId` must include a weights hash (confirm) | Prevents fake model registration: an attacker cannot claim the id "Qwen3-72B" while actually pointing at Qwen-8B weights and charging large-model prices | 1 day (if missing) |
| 10 | Leader must check user balance BEFORE dispatch (confirm) | DoS defense: a zero-balance user cannot force Leader / P2P resource consumption with a spam stream of requests | 0.5 day (if missing) |
| 11 | Tendermint Chain ID set to `funai_123123123-3` (EVM Chain ID = 123123123) | Locked in `p2p/node.go` default and testnet config; no further change needed | Done |

---

## 2. Detailed Rationale

### 1. Verifier / SecondVerifier rank window 10 → 21

**Current code locations:**
- `p2p/verifier/verifier.go` line 185: `top := 10`
- `p2p/proposer/proposer.go` line 174: `top := 10`
- SecondVerifier rank window: engineer to locate and confirm

**Problem.** Consumer miners (a 4090 / 5090 at home) have a realistic day-to-day offline rate of 20–30% — power cuts, flaky networks, reboots, games grabbing the GPU. Binomial math on "rank 0–9 cannot round up 3 online candidates":

| Offline rate | rank-10 failure | rank-21 failure |
|--------------|-----------------|-----------------|
| 20% | 0.008% (1 in 13 000) | 10⁻¹¹ (effectively zero) |
| 30% | 0.16% (1 in 629) | 10⁻⁷ (1 in 10 million) |
| 50% (major outage) | 5.47% (1 in 18, unacceptable) | 0.02% (1 in 5 000) |

Rank 21 at a 50% offline-rate extreme is still **5 000× more reliable** than rank 10 under normal conditions.

**Why 21 and not 20 or 30:**
- 21 is symmetric: 3 primary Verifiers + 6 backup layers of 3 (ranks 3–5, 6–8, 9–11, 12–14, 15–17, 18–20) — a clean 7-group structure.
- 30's marginal benefit is too small (two-in-ten-thousand → 0.4-in-a-million is imperceptible to users).

**Verification mechanism — first principles (for engineer cross-check):**
- After inference, the Worker broadcasts `InferReceipt` on the pubsub topic for that `model_id`.
- Every subscriber receives it; each Verifier independently computes its own rank with `VRF(BlockHash || TaskID)`.
- Only if its rank is within the legal window (`< 21`) does it perform teacher forcing and submit `VerifyResult`.
- The on-chain Proposer re-computes the VRF independently and rejects results whose rank is out of window.
- The Worker cannot manipulate who verifies (unlike a Leader, which is a single-point decision) — the Worker merely broadcasts; VRF outcomes are on-chain deterministic.

**Coupled with the 24-hour `expire_block`:**
- First 3 `VerifyResult`s to arrive settle the task — any time within 24 h is fine.
- If the 24-hour window closes with < 3 results: fall through to E9 (2 → LowConfidence), E10 (1 → LowConfidence), E11 (0 → wasted work).
- With rank 21 + 24 h window, the probability of actually reaching E11 approaches zero.

**Change:**
```
verifier.go line 185: top := 10  →  top := 21
proposer.go line 174: top := 10  →  top := 21
Locate and match the SecondVerifier rank window (if implemented independently)
```

---

### 2. Add `top-p` sampling

**Current state:** only `temperature` is supported (uint16 fixed-point, 10 000 = 1.0, range 0–20 000).

**Why `top-p` is required:**
- OpenAI / Anthropic / Google / every mainstream API ships `top_p` in the default parameter set.
- Developers reach for `temperature=0.7, top_p=0.95` by muscle memory.
- Without `top_p` the SDK's OpenAI-compatibility layer is incomplete, and OpenClaw Skill developers cannot drop-in migrate.

**Implementation complexity:** low. Sort the logits and truncate by cumulative probability; the Verifier replays the same sort + truncate. Cross-implementation risk is low because sort order is deterministic.

**Explicitly NOT adding:**
| Parameter | Reason |
|-----------|--------|
| `top-k` | Functional overlap with `top-p` |
| `repetition_penalty` | Complex implementation (requires maintaining token history); hard to make cross-implementation consistent |
| `frequency_penalty` / `presence_penalty` | Same as above; low usage rate |
| `logit_bias` | Users modifying logits directly would break the verification system outright |
| User-defined `stop_sequences` | Tokenization differs across engines (BPE vs SentencePiece) |

**Proto change:**
```protobuf
message InferRequest {
  uint16 temperature = X;  // existing
  uint16 top_p = Y;        // new, 0-10000, 10000 = 1.0, 0 = disabled
}
```

**Engineering effort:**
- Worker side: transparent pass-through to TGI (TGI supports `top_p` natively).
- Verifier side: add `top_p` truncation to the sampling pipeline.
- Tests: cross-hardware consistency of `temperature` + `top_p` combinations.
- Total: 2–3 days.

---

### 3. Reputation mechanism

**Problem.** The Worker's VRF dispatch weight comes from `stake`. If a Worker has high stake but a high offline rate (price drop → miner stops caring; machine sold without unregistering; node parked to farm stake), every dispatch attempt tries it first → 1-second timeout → fallback. When the FAI price drops these miners multiply, and the system-wide latency creeps up.

**Mechanism — three core rules:**

Every Worker keeps an on-chain `ReputationScore`:
- Initial: 1.0 (10 000), range 0–1.2 (0–12 000).
- VRF weight = `stake × ReputationScore`.

**Update rules:**

| Event | Reputation change |
|-------|-------------------|
| Worker Accept | +0.01 |
| Worker Miss (1 s timeout, no response) | -0.1 |
| Worker Reject("busy") (honestly declining — GPU full) | 0 (neutral) |
| 10 consecutive Rejects | -0.05 (anti-cherry-pick; the counter resets on an Accept) |
| Hourly decay (BeginBlocker) | Drift toward 1.0 by ±0.005 |

**Why the penalty is 10× the reward.** Asymmetric design: rewards are incremental, penalties are fast. After 10 misses a Reputation drops from 1.0 to 0, and an idle miner is out of the dispatch pool within hours.

**Why Reject("busy") is neutral.** A Worker performing Verifier / SecondVerifier teacher forcing has the GPU pegged. If a new `AssignTask` arrives, honestly declining should NOT incur a penalty — otherwise honest miners get penalized for being productive.

**One `ReputationScore` covers Worker / Verifier / SecondVerifier:**

| Role | Miss window |
|------|-------------|
| Worker (accept) | 1 s |
| Verifier (submit `VerifyResult`) | 30 s (teacher forcing takes time) |
| SecondVerifier (submit second-verification response) | 5 min (second verification tolerates longer latency) |

**SecondVerifier Miss penalty is -0.2 (double).** Second-verification trigger rates are low (10–30%), so each instance matters; a larger penalty enforces seriousness.

**Price-drop scenario.** Idle miners' Reputation decays to zero within hours → dispatch traffic flows entirely to honest miners → network latency does not accumulate.

**Engineering effort:**
- On-chain Worker state: add `ReputationScore` — 0.5 day
- VRF weight → `stake × Reputation` — 0.5 day
- Worker-side `Reject("busy")` response — 1 day
- Leader broadcasts Accept / Miss signals — 1 day
- On-chain BeginBlocker decay — 0.5 day
- Verifier / SecondVerifier miss detection — 2 days
- Consecutive-reject counter — 0.5 day
- Tests — 1–2 days
- Total: 6–7 days

---

### 4. `AssignTask` field extension

**Existing fields** (leader.go lines 342–354):
`TaskId`, `ModelId`, `Prompt`, `Fee`, `UserAddr`, `Temperature`, `UserSeed`, `DispatchBlockHash`, `FeePerInputToken`, `FeePerOutputToken`, `MaxFee`, `MaxTokens`.

**New fields:**

```go
type AssignTask struct {
    // ... existing fields ...
    TopP              uint16   // top-p sampling (pairs with decision 2)
    MaxLatencyMs      uint32   // first-token max latency (milliseconds)
    MaxTotalLatencyMs uint32   // full-output max latency (milliseconds, optional)
    StreamMode        bool     // request streaming response
}
```

**Worker decision flow on receipt:**
```
if estimated_first_token_latency > MaxLatencyMs:
    Reject("cannot meet latency requirement")
elif concurrency is full:
    Reject("busy")
elif model_id weights not loaded:
    Reject("model not loaded")
else:
    Accept and start inference
```

**Key point.** The Worker proactively decides whether it can meet the user's latency requirement. If it cannot, it rejects; this does NOT penalize Reputation (honest rejection).

**Why streaming must be explicit.** Companion-style use cases need the first token within 2 s; the SDK's timeout path depends on streaming.

**Engineering effort:** 0.5 day (proto definition + Worker decision logic).

---

### 5. Feed historical latency into the VRF weight

**Problem.** Low-latency scenarios (companion-style, first token within 2 s) must prioritize fast nodes. If dispatch only uses `stake × Reputation` it may pick a slow node → timeout → retry → total latency 3+ s.

**Why NOT use self-declared Tiers.** A Worker declaring "Tier S" is unverifiable — the protocol cannot check whether you are actually Tier S or just lying.

**Why NOT parallel dispatch.** 3× compute cost waste — uneconomical.

**Adopted approach: on-chain latency tracking.**

```
Each Worker maintains:
  AvgLatencyMs uint32  // sliding average over the last N inferences

Data source: no Worker self-reporting needed.
  At settlement: latency = InferReceipt.Timestamp - AssignTask.Timestamp
  On-chain timestamps cannot be forged; the data is intrinsically honest.
```

**VRF weight integration:**

```
VRF weight = stake × Reputation × LatencyFactor

LatencyFactor (when the user set MaxLatencyMs):
  Worker.AvgLatencyMs < MaxLatencyMs × 0.5  → weight × 1.5 (fast-node boost)
  Worker.AvgLatencyMs < MaxLatencyMs × 0.8  → weight × 1.0
  Worker.AvgLatencyMs > MaxLatencyMs        → weight × 0.1 (effectively deselected)

If the user did NOT set MaxLatencyMs: LatencyFactor = 1.0 (no effect on normal traffic)
```

**Effect.** Companion-style requests flow to fast nodes automatically; normal requests keep the original VRF dispatch. No Tier declarations, no parallel dispatch.

**Product-layer example:**
```javascript
funai.infer({
  model: "qwen3-72b-abliterated-q4",
  prompt: "...",
  maxLatencyMs: 1500,     // first token within 1.5 s
  stream: true,
  retryOnTimeout: 2000    // SDK retries after 2 s
})
```

**Engineering effort:**
- Worker state `AvgLatencyMs` — 0.5 day
- On-chain sliding average (updated at settlement) — 0.5 day
- VRF weight gains `LatencyFactor` — 0.5 day
- Tests — 1 day
- Total: 2.5 days

---

### 6. Long-tail model activation / deactivation mechanism

**Attack scenario.** An already-activated long-tail model falls into disuse (most Workers drop out). An attacker spins up 10 Sybil Workers registered for that `model_id`, sends requests to themselves, and farms block rewards.

**Current code gap.** `CheckServiceStatus` in `modelreg/keeper.go` line 193 only checks `WorkerCount >= MinServiceWorkerCount`. Worker count can be faked; stake cannot.

**Correct mechanism:**

**Activation / resumption condition A (AND):**
- Stake vote passes (`ActivationStakeRatio ≥ 2/3`)
- Worker count ≥ 10 (`MinEligibleWorkers`)
- Operator count ≥ N (`MinUniqueOperators`)

**Deactivation condition B (OR):**
- Worker count < 10, **or**
- The total stake held by Workers registered for that `model_id` is < 2/3 of the network.

**Asymmetric design.**
- Activation threshold is strict (AND) — blocks an attacker from activating a model with tiny resources.
- Deactivation threshold is permissive (OR) — any one metric falling triggers immediate deactivation.
- Sybil miner gathering 10 Workers → cannot pass the 2/3 stake threshold → cannot activate.
- Post-activation stake drops below 2/3 → immediate deactivation → Sybil control of Worker count is useless.

**Code change** (`modelreg/keeper.go` line 195):
```go
// Current
currentCanServe := model.CanServe(params.MinServiceWorkerCount)

// Change to
currentCanServe := model.WorkerCount >= params.MinServiceWorkerCount &&
                   model.InstalledStakeRatio >= params.MinServiceStakeRatio
```

**New parameter:**
- `MinServiceStakeRatio = 0.667` (2/3)
- Can be governance-reduced during the bootstrap phase and raised to 2/3 at maturity.

**Engineering effort:** 10 min.

---

### 7. Worker user-data retention: 3 days → 48 hours

**Current state.** Workers hold user prompts in plaintext on local disk for 3 days to support teacher forcing when on-chain audit is triggered.

**Legal risk.** Under US 18 USC §2258A, "actual knowledge" of CSAM is what triggers the reporting obligation. A Worker passively accepting tasks without viewing them ≈ no knowledge ≈ no obligation. **But 3 days of on-disk storage qualifies as "stored content"**, which is riskier under a subpoena than transient in-memory data.

**Why 48 hours is reasonable:**
- Routine second verification triggers within 24 h (sampling rate 10–30%, in real time) → 100% coverage.
- Third verification (disputes) operates on a 24–48 h window → covers 99%.
- The rare beyond-48 h third verification → degrades gracefully ("audit failed, original settlement stands").
- 48 h is the sweet spot between "audit effectiveness" and "data minimization".

**Why NOT store only a hash.** Teacher forcing needs the prompt plaintext — second-verifiers must be able to replay a forward pass on `prompt + output`. User SDKs are unreliable (the user may have closed Telegram before the SDK could store the prompt). Worker-side storage is the only viable option.

**Change.** Reduce the Worker's local-retention TTL constant from 259 200 s → 172 800 s.

**Engineering effort:** 5 min.

---

### 8. Revenue and block-reward distribution

**Old distribution (inference fee):**
- Worker: 95%
- Verifier (split 3 ways): 4.5% (1.5% each)
- Audit fund: 0.5%

**Problem with the old distribution.**
- Verifier compute load (forward pass) is close to the Worker's — a 1.5% reward is severely under-incentivized.
- Bootstrap-phase Verifier enrollment suffers accordingly.
- A 0.5% audit fund is thin; disputes bursty enough to drain it fast.

**New distribution (inference fee + block reward, same ratio):**

| Role | Share | Notes |
|------|-------|-------|
| Worker | 85% | Still takes the lion's share of inference compute |
| Verifier | 12% | 3 verifiers share → ~4% each |
| Multi-verification fund | 3% | Split out as its own bucket |

**Verifier / second-verifier block-reward breakdown:**
- **85% by fee weight:** pro-rata by the fee volume of tasks the Verifier / SecondVerifier participated in.
- **15% by count weight:** pro-rata by the number of tasks verified.

**Rationale for 85/15:**
- 85% by fee → encourages Verifiers to grab large-fee tasks first (higher value, higher reward).
- 15% by count → prevents small-fee tasks from being abandoned (they still add up to meaningful income).

Implementation: `x/reward/types/params.go` — `DefaultFeeWeight = 0.85`,
`DefaultCountWeight = 0.15` (pool-sum validation enforces they equal 1.0).

**Worker block reward is distributed pro-rata to stake among active Workers.**

**Impact on the Worker.**
- Inference-fee share drops from 95% to 85%.
- But the Worker can simultaneously register as a Verifier (same GPU).
- Combined income across multiple roles actually rises.
- Operator documentation must make "multi-role concurrency = revenue maximization" explicit so miners understand the design.

**Implementation.** On-chain parameter — no logic code changes, just value adjustments.

---

### 9. `ComputeModelId` must include a weights hash (engineer to confirm)

**Attack scenario.** An attacker registers a `model_id` claiming to be "Qwen3-72B" but actually pointing at a small model's (Qwen-8B) safetensors file. Workers download the small model under that `model_id`, charge large-model prices, and incur only small-model cost.

**Defense principle.** If `ComputeModelId_Deterministic` includes the safetensors file hash in the `model_id` calculation, then small-model hash ≠ large-model hash, and a Qwen-8B file cannot impersonate a Qwen3-72B `model_id`.

**Engineer action:**
1. Locate the `ComputeModelId_Deterministic` function.
2. Check whether its input includes the safetensors hash (or some Merkle root of the weight files).
3. If NOT included → must be added, otherwise the attack is viable.
4. If included → just confirm.

**Engineering effort if missing:** 1 day (change the hash input + update hashes for already-registered models).

---

### 10. Leader balance check must be the FIRST step of dispatch (engineer to confirm)

**Attack scenario (DoS).** Attacker wallets with zero balance blast `InferRequest` messages (across many `user_pubkey`s). For each request the Leader must:
1. Parse the request
2. VRF-sort the Worker pool
3. Broadcast `AssignTask`
4. Wait for responses
5. Ultimately fail at settlement because of insufficient balance

This flow consumes Leader CPU and P2P bandwidth. Enough zero-balance requests can DoS the Leader.

**Defense.** The FIRST step of Leader `InferRequest` handling (BEFORE any VRF sort or P2P broadcast):

```go
func (l *Leader) handleInferRequest(req *p2ptypes.InferRequest) error {
    // Step 1: balance check
    balance := l.chainClient.GetInferenceAccount(req.UserPubkey).AvailableBalance
    if balance < req.MaxFee {
        return fmt.Errorf("insufficient balance")  // drop immediately, do nothing else
    }

    // Then proceed with VRF sort, dispatch, etc.
    ...
}
```

**Engineer action:**
1. Locate the Leader's `InferRequest` entry handler.
2. Check whether balance validation is the FIRST step (before any VRF sort / P2P broadcast).
3. If NOT first → hoist it.
4. If already first → just confirm.

**Engineering effort if it needs to be moved:** 0.5 day.

---

### 11. Chain ID = `funai_123123123-3` (done)

Locked. EVM Chain ID = 123123123. Cosmos Chain ID follows the Cosmos EVM
`<prefix>_<eip155>-<version>` convention: `funai_123123123-3`. Default in
`p2p/node.go` and all testnet bootstrap configs. No further changes.

Recorded here only for document completeness.

---

## 3. P0 / P1 / P2 Priority Breakdown

### P0 (required before launch)

| # | Change | Effort |
|---|--------|--------|
| 1 | Rank 10 → 21 | 5 min |
| 2 | Add `top-p` sampling | 2–3 days |
| 3 | Reputation mechanism | 6–7 days |
| 4 | `AssignTask` field extension | 0.5 day |
| 5 | Historical latency in VRF weight | 2.5 days |
| 6 | Long-tail model activation / deactivation | 10 min |
| 7 | Data retention 48 hours | 5 min |
| 8 | Distribution tuning (on-chain parameters) | 0 days (config) |
| 9 | `ComputeModelId` with weights hash (confirm) | 0–1 day |
| 10 | Leader balance check first (confirm) | 0–0.5 day |

**Total P0 effort: ~12–14 days (2.5–3 weeks).**

### P1 (within 3 months of launch)

- Worker permanent-jail via Reputation (if Reputation < threshold for N days → permanent jail requiring manual unjail).
- SecondVerifier count ≥ 2–3 with cross-validation (confirm).
- Verifier / SecondVerifier late-submission degradation once `expire_block` passes (parameters for the E9 / E10 / E11 flow).

### P2 (later optimization)

- Leader dispatch anti-cheat (challenge mechanism for skipping rank #1).
- Proposer order-stealing defense.
- User-equals-Worker self-loop detection.
- Incremental rank-window expansion (if real data shows it's needed).

### Explicitly NOT doing

- Parallel dispatch (too expensive).
- Tier declarations (unverifiable).
- First-token receipt mechanism (SDK is untrusted).
- Challenge mechanism (maintenance-heavy, no operator).
- Reputation leaderboard / Dashboard (leave to the ecosystem).

---

## 4. Constraints and Assumptions

1. **All on-chain parameters are governance-adjustable.** Every number — rank 21, `MinStakeRatio` 2/3, Reputation delta sizes, and so on — is an initial recommendation; each is tunable once we have operational data.

2. **Reputation does NOT replace Jail.** The two are complementary: Reputation is a continuous soft adjustment (0–1.2 weight) and Jail is a hard penalty (complete ban). In the bootstrap phase only Reputation is active; Jail for Reputation-based triggers is deferred to V5.3.

3. **Every change must pass tests before merge.** Each item needs corresponding unit tests + E2E tests green through CI before merging to `main`.

4. **Chain ID is final.** `funai_123123123-3` is locked (EVM Chain ID 123123123). Every new testnet boots from this Chain ID.

5. **This document is the source of truth for decisions.** Whitepaper and technical documentation defer to this document on facts.

---

## Appendix — Rejected Options

The following options were evaluated during the decision process and rejected. Recorded here for future reference.

### A. Tier declaration

Workers declare Tier S / A / B at registration (different latency SLAs).

**Rejected because:** the protocol cannot verify declaration truthfulness. Replaced by decision 5 ("historical latency feeds the VRF weight").

### B. Parallel (speculative) dispatch

Low-latency requests dispatched to ranks #1 / #2 / #3 simultaneously; first responder wins.

**Rejected because:** 3× compute waste. Reputation + LatencyFactor are sufficient.

### C. First-token receipt

SDK signs an acknowledgement back to the Worker; the Worker must attach it to `InferReceipt` to settle.

**Rejected because:** the SDK runs on the user's device and cannot be trusted — a user can simply refuse to sign and welsh on payment.

### D. Decrypt prompts via user SDK at second-verification time

Workers store encrypted prompts; at second-verification the protocol requests a decryption key from the user.

**Rejected because:** the user's SDK may be offline; there is no guarantee of a response. Workers holding 48 h of plaintext is the only workable option.

### E. Challenge mechanism (appeal)

Workers can challenge Leader-issued miss receipts as false accusations.

**Rejected because:** high maintenance cost with no actual operator. Reputation's natural decay is sufficient.

### F. Reputation leaderboard + Dashboard

Protocol provides an official miner-reputation leaderboard and dashboard.

**Rejected because:** Bitcoin has no official dashboard either. Ecosystem players can build it.

### G. Rank windows of 10 / 20 / 30 / 50

Multiple candidate values were discussed. **Final choice: 21.** Symmetry (3 primary + 6 backup groups of 3), low-enough failure rate, and diminishing marginal returns (30 does not meaningfully improve on 21).

### H. On-chain `MinExpireBlocks` enforcement

Leader forces `ExpireBlock ≥ a minimum` (e.g. 50 s) during `InferRequest` handling, to prevent a user with a modified SDK setting a 1-second timeout and wasting Worker time.

**Rejected because:** the Worker itself decides on receipt of `AssignTask` whether `ExpireBlock` is achievable. If not, it rejects without Reputation penalty (honest rejection). A user setting a 1-second timeout via a modified SDK is only hurting themselves (the task inevitably fails and they pay the 15% `max_fee` penalty, per `x/settlement/types/params.go` `DefaultFailSettlementFeeRatio = 150` per-mille), not mounting a real external attack. The protocol does not need to enforce this; Worker-side decision-making is enough.

---

**Document version:** v1.0
**Next revision:** to be updated to v1.1 when engineering completes all P0 items (include test results).
