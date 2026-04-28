# Verifier Economics under V6 Batch-Replay — §2.3 Recommendation

| | |
|---|---|
| **Date** | 2026-04-28 |
| **Closes** | Pre_Mainnet_Test_Plan §2.3 |
| **Companion** | [`verifier_economics.py`](./verifier_economics.py) — pure-stdlib simulator, run with `python3` |
| **Verdict** | **Keep the 12% allocation as-is.** The market self-regulates the load-bearing variable (pool size M); the 12% number is well-calibrated for the realistic operating envelope. Surface a small clarification to the spec on the M-dependent break-even fee and a lightweight per-model "verifier solvency monitor" to catch out-of-envelope models early. |

---

## 1. Question

V6 batch-replay imposes a "tens of times single-task inference" cost
amplification on the verifier (the verifier replays the WHOLE batch the
worker ran, not just the task the verifier was assigned to). The
existing fee split sends 12% of every task's fee into a verifier pool
shared across the 3 first-tier verifiers per task.

> Can the 12% allocation pay first-tier verifiers enough to break even
> on GPU rental, given V6's batch-replay cost amplification?

KT V6 PoC SUMMARY's "open questions" flagged this as P0; this report
closes the question.

---

## 2. Model

The simulator implements a steady-state per-verified-task ledger. For
the full derivation see [`verifier_economics.py`](./verifier_economics.py)
docstring; the operative formulas are:

```
income_per_verified_task = fee × verifier_pool_pct / verifiers_per_task
                         = fee × 12% / 3
                         = fee × 4%

cost_per_verified_task   = M × T × GPU$/hr / (verifiers_per_task × 3600)
                         = M × T × GPU$/hr / 10800
```

where:

| Symbol | Meaning |
|---|---|
| `M` | Total verifiers competing for VRF slots in this model's pool |
| `T` | Single-task forward-pass latency (seconds) |
| `GPU$/hr` | Verifier's GPU rental rate |
| `verifiers_per_task = 3` | Spec (V52 §13) |
| `verifier_pool_pct = 12%` | Settlement keeper default |

The model assumes uniform-random VRF dispatch across the verifier pool,
which is what the keeper actually does (see VRF unified formula in
`x/vrf/`).

---

## 3. Findings

### 3.1 Cost is independent of batch size N (the non-obvious result)

V6 design ([`docs/protocol/FunAI_V6_Batch_Replay_Verification_KT.md`](../protocol/FunAI_V6_Batch_Replay_Verification_KT.md) §3.4) **mandates full-batch replay**:

> "The Verifier must replay the Worker's entire batch at once — batches cannot
> be split for verification. When selecting Verifiers via VRF, only consider
> nodes whose VRAM is large enough to hold the batch."

So the cost-amplification framing in the V6 PoC SUMMARY's open question is
real per BATCH (the verifier replays N tasks of compute regardless of how many
of those N they are assigned to verify). But the per-task cost CANCELS the N
out under uniform VRF dispatch:

```
expected_tasks_per_batch_per_verifier = N × 3 / M
cost_per_batch_replay                  = N × T × GPU$/hr / 3600
cost_per_verified_task                 = cost_per_batch_replay
                                       / expected_tasks_per_batch_per_verifier
                                       = M × T × GPU$/hr / (3 × 3600)
```

The N's cancel. Larger batches make the verifier do more work per
batch, but also give them more verified tasks per batch, so the
amortized per-task cost is unchanged.

**Implication**: V6's batch-replay design does NOT economically
penalise the verifier vs V5.2's single-task verification. The "10x
cost" framing in the SUMMARY's open question was misleading — the cost
is the same per task verified; only LATENCY (time per task verified)
goes up with batch size. Worker throughput still benefits, verifier
revenue does not change.

This was confirmed by Scenario B in the simulator (varying N from 1
to 32 at fixed M = 10): all four rows produce identical margin.

#### 3.1.1 Refinement: VRAM filtering pushes M down at large N

The V6 design's "only consider nodes whose VRAM is large enough to hold
the batch" rule means **M is not a single constant per model — it
depends on N**. Concretely:

- A worker batched at N=8 on a 7B model needs ~14 GB activation memory →
  any 24 GB+ verifier qualifies → large eligible M.
- A worker batched at N=32 on the same model needs ~56 GB →
  only A100 80 GB / H100-class verifiers qualify → eligible M shrinks.

Since the per-task cost formula is `M × T × $/hr / 10800`, a smaller
eligible M means **lower per-task cost for the verifiers who DO
qualify**. Counter-intuitively, larger worker batches IMPROVE
per-verified-task economics for the eligible subset, even though they
push more would-be verifiers out of the pool.

System-level interpretation: larger N → fewer participants but each
earns more. Smaller N → more participants but each earns less. The
chain currently has no direct lever to balance these (the worker picks
N based on its own throughput needs); the V6 design notes that
**workers can voluntarily lower batch_capacity to widen the verifier
pool** — i.e. trade their own throughput for verification
decentralisation. This is a per-model game-theoretic decision the
operator should be aware of when setting `batch_capacity`.

The simulator's M sweep (Scenario A) implicitly covers this — pick the
M corresponding to the VRAM-eligible subset for your N.

### 3.2 Pool size M is the dominant cost driver

```
M    | margin/task at default workload (fee=$0.01, T=0.2s, GPU=$1.89/hr)
─────┼──────────────────────────────────────────
  3  | + $0.295 m  ← 281% margin
  6  | + $0.190 m  ←  90% margin
 10  | + $0.050 m  ←  14% margin (knee)
 25  | − $0.475 m  ← UNDERWATER
 50  | − $1.350 m  ← deep underwater
100  | − $3.100 m  ← deep underwater
```

The "knee point" at M ≈ 10 is where margin drops to the single-digit-%
range; beyond M ≈ 13 the verifier is paying to verify.

**Implication for the design**: the chain has no mechanism to cap M.
But the market self-regulates — verifiers who are losing money stop
verifying (they are not forced to participate; VRF only selects from
those who have registered). So in equilibrium, M settles near the
break-even point.

### 3.3 Inference time T scales cost linearly

```
T (sec) | margin/task at default (M=10, fee=$0.01, GPU=$1.89/hr)
────────┼──────────────────────────────────────────
  0.05  | + $0.313 m  ← small models (Qwen 0.5B)
  0.20  | + $0.050 m  ← Qwen 7B / Mixtral routed
  0.60  | − $0.650 m  ← Phi-3.5-MoE 42B
  2.00  | − $3.100 m  ← 70B+ class
```

Larger models → longer T → cost scales linearly → the same fee + same
pool size that worked for Qwen 7B is upside-down for Phi-3.5-MoE.

**Implication**: the operator/proposer of a 70B+ model needs to set a
materially higher fee (or accept a smaller verifier pool) for the math
to work. This is a per-model variable, not a chain-wide one.

### 3.4 Break-even fee table (concrete)

For the 4 GPU-rental tiers x 4 inference-latency buckets x 6 pool
sizes the simulator covers, the minimum fee a verifier needs to break
even at the current 12% allocation:

| Model size proxy | Pool M | T4 ($0.50/hr) | RTX PRO 6000 ($1.89/hr) | A100 ($3.50/hr) | H100 ($11/hr) |
|---|---|---|---|---|---|
| Tiny (T = 50ms) | 10 | $0.0006 | $0.0022 | $0.0041 | $0.0127 |
| Tiny (T = 50ms) | 50 | $0.0029 | $0.0109 | $0.0203 | $0.0637 |
| Mid (T = 200ms) | 10 | $0.0023 | $0.0088 | $0.0162 | $0.0509 |
| Mid (T = 200ms) | 50 | $0.0116 | $0.0438 | $0.0810 | $0.2546 |
| Large (T = 600ms) | 10 | $0.0069 | $0.0263 | $0.0486 | $0.1528 |
| Large (T = 600ms) | 50 | $0.0347 | $0.1313 | $0.2431 | $0.7639 |
| 70B+ (T = 2.0s) | 10 | $0.0231 | $0.0875 | $0.1620 | $0.5093 |
| 70B+ (T = 2.0s) | 50 | $0.1157 | $0.4375 | $0.8102 | $2.5463 |

These are concrete, not arbitrary. A model proposer can read this
table to set a defensible per-task fee floor.

### 3.5 Alternative allocations don't help when M and T are off

The simulator's stress scenario (M=25, T=0.6s, GPU=$3.50/hr,
fee=$0.001) shows that even raising verifier_pool_pct from 12% to 25%
keeps the verifier underwater:

```
verifier_pool =   5.0%   margin = -$4.8444 m   ✗
verifier_pool =  12.0%   margin = -$4.8211 m   ✗  (current default)
verifier_pool =  20.0%   margin = -$4.7944 m   ✗
verifier_pool =  25.0%   margin = -$4.7833 m   ✗
```

Cost dwarfs income by orders of magnitude — increasing the pool's
share of a too-small fee is rounding noise. The right fix is the
operator setting a higher fee for that model, not adjusting the
chain-wide split.

---

## 4. Recommendation

### 4.1 Keep 12% as-is.

The number is well-calibrated for the realistic operating envelope:
- Small/medium models on commodity GPUs (T ≤ 0.2s, GPU ≤ $2/hr): 12%
  produces double-digit-% margin at fees ≥ $0.005/task — well within
  what production users pay for token completions today.
- For larger models or pricier GPUs, the operator must set a
  proportionally higher fee. The break-even table in §3.4 is a
  defensible reference.
- Increasing the chain-wide split to compensate for an under-priced
  model just spreads the loss; it doesn't fix it. Raising the fee at
  the model layer does.

### 4.2 Documentation: clarify the M-dependent break-even

`docs/protocol/FunAI_V52_Final.md` and the V6 BatchReplay design doc
should pin:

> The verifier-pool 12% allocation is sustainable while
> `M × T × GPU$/hr ≤ fee × 432`, where M = verifier pool size for the
> model, T = single-task forward-pass latency in seconds, GPU$/hr =
> verifier rental rate. Operators of high-T or high-GPU models should
> price the per-task fee accordingly; see
> `docs/economics/verifier_economics.py` for the simulator and
> `verifier_economics_report.md` §3.4 for a break-even reference.

### 4.3 Out-of-envelope monitor (recommended, not blocking)

A lightweight chain-side monitor that flags when a model's
`verifier_pool_size × inference_latency × estimated_GPU_$` /
`fee × 432` > 1 — i.e. when the model's verifier economics are
underwater. Surface in the model registry as a "warn" status.

This prevents the "I deployed a 70B model with a $0.001 fee and now no
verifier wants to handle my tasks" failure mode from being a silent
liveness incident. Estimated effort: ~½ day in `x/modelreg/keeper/`,
gated by `make test-byzantine-quick` to lock the formula.

This is recommended — NOT blocking mainnet — because the market
self-regulates already (verifiers withdraw when underwater); the
monitor just makes the equilibrium visible.

### 4.4 Cost amplification is NOT a verifier-economic risk

The "tens of times single-task inference" cost in the V6 PoC SUMMARY
open question was misleading. Per §3.1, the per-task verifier cost is
INDEPENDENT of batch size N under uniform VRF dispatch. V6
batch-replay imposes a LATENCY hit per verification, not an economic
hit per task verified. The chain economics under V6 are no worse than
V5.2's single-task verification, and the worker throughput benefit is
real.

This does not need to be re-litigated unless the dispatch path moves
away from uniform VRF.

---

## 5. Open questions for KT review

1. **Are the realistic ranges right?** The simulator covers fee
   ($0.0001 → $0.10), T (0.05 → 2.0s), GPU ($0.50 → $11/hr), M (3 →
   100). KT to confirm these bound the launch envelope, especially the
   high end (very large models on cheap GPUs is the failure-mode
   corner).
2. **What's the actual M today on testnet?** §3.2 predicts M ≈ 13 is
   the equilibrium for default params; if early testnet shows M = 50+
   we have an oversupply that the chain should not subsidise (and the
   12% number may need a downward retune, not upward).
3. **Should the monitor in §4.3 ship pre-launch or post-launch?** It's
   half a day; the cost vs catching a "no verifiers for this model"
   incident in production seems clearly worth it but landing in the
   first chain release is a sequencing decision.

---

## 6. References

- [`verifier_economics.py`](./verifier_economics.py) — simulator
- [`docs/testing/Pre_Mainnet_Test_Plan.md`](../testing/Pre_Mainnet_Test_Plan.md) §2.3 — the row this report closes
- [`docs/testing/reports/2026-04-21-v6-phase1a/SUMMARY.md`](../testing/reports/2026-04-21-v6-phase1a/SUMMARY.md) — open question being answered
- [`docs/protocol/FunAI_V52_Final.md`](../protocol/FunAI_V52_Final.md) §13 — verifier dispatch + fee split
- [CLAUDE.md](../../CLAUDE.md) — fee split (85/12/3) and VRF formula
