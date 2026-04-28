# Verifier Economics under V6 Batch-Replay

> Sustainability analysis of the 12% verifier-pool fee allocation under V6's batch-replay verification cost amplification. Closes Pre_Mainnet_Test_Plan §2.3.
>
> Sources: [`docs/economics/verifier_economics.py`](../docs/economics/verifier_economics.py), [`docs/economics/verifier_economics_report.md`](../docs/economics/verifier_economics_report.md)

## TL;DR

**Keep the 12% allocation as-is.** Market self-regulation (verifiers withdrawing when underwater) handles the dominant variable, pool size M. The non-obvious result the simulator surfaces: under uniform-VRF dispatch, **per-task verifier cost is independent of batch size N**. V6's "tens-of-times single-task inference" cost amplification flagged in the V6 PoC SUMMARY's open question is real per BATCH but cancels per TASK.

## Cost model

```
income_per_verified_task = fee × verifier_pool_pct / verifiers_per_task
                         = fee × 12% / 3   = fee × 4%

cost_per_verified_task   = M × T × GPU$/hr / (verifiers_per_task × 3600)
                         = M × T × GPU$/hr / 10800
```

| Symbol | Meaning |
|---|---|
| `M` | Total verifiers competing for VRF slots in this model's pool |
| `T` | Single-task forward-pass latency (seconds) |
| `GPU$/hr` | Verifier's GPU rental rate |
| `verifiers_per_task = 3` | Spec ([Jail & Slashing](jail-and-slashing.md), V52 §13) |

## Key findings

1. **Cost INDEPENDENT of batch size N (under uniform-VRF dispatch).** The N's cancel between cost-per-batch-replay and tasks-per-batch-per-verifier. V6 imposes a LATENCY hit on the verifier per verification, not an economic hit per task verified. Confirmed against V6 KT design §3.4: "The Verifier must replay the Worker's entire batch at once — batches cannot be split for verification."
2. **Pool size M is the dominant cost driver.** Knee point at M ≈ 10 for default workload. Beyond M ≈ 13 the verifier is paying to verify; market self-regulates by withdrawal.
3. **Inference time T scales cost linearly.** Per-model variable: a 70B model on $11/hr H100 needs a fee 100× higher than a Qwen 0.5B on T4 to keep verifiers above water.
4. **Increasing the chain-wide split doesn't fix an under-priced model.** The right fix is the operator setting a higher per-task fee at the model layer.
5. **VRAM filtering: M depends on N (refinement of #1).** V6 design requires verifiers' VRAM ≥ batch-replay footprint; large N effectively shrinks eligible M, which counter-intuitively IMPROVES per-task economics for the eligible subset. Workers can voluntarily lower `batch_capacity` to widen the verifier pool — a per-model game-theoretic choice trading throughput for decentralisation.

## Break-even fee table (concrete)

Minimum per-task fee for verifier sustainability under the current 12% allocation:

| Model size proxy | Pool M | T4 ($0.50/hr) | RTX PRO 6000 ($1.89/hr) | A100 ($3.50/hr) | H100 ($11/hr) |
|---|---|---|---|---|---|
| Tiny (T = 50 ms) | 10 | $0.0006 | $0.0022 | $0.0041 | $0.0127 |
| Mid (T = 200 ms) | 10 | $0.0023 | $0.0088 | $0.0162 | $0.0509 |
| Large (T = 600 ms) | 10 | $0.0069 | $0.0263 | $0.0486 | $0.1528 |
| 70B+ (T = 2.0 s) | 10 | $0.0231 | $0.0875 | $0.1620 | $0.5093 |

Operators reading this set defensible fee floors per model bucket.

## Recommendation summary

1. **Keep `verifier_pool_pct = 12%`** in [Settlement](settlement.md) keeper params.
2. **Document the M-dependent break-even** in V52 spec: `M × T × GPU$/hr ≤ fee × 432` is the sustainability inequality.
3. **Add an out-of-envelope monitor** in [Model Registry](model-registry.md) — flag models whose `M × T × GPU$/hr / (fee × 432) > 1` as "warn". Estimated ~½ day, gated by `make test-byzantine-quick`. Recommended, not blocking.

## Open questions for KT review

1. Are the simulator's parameter ranges (fee $0.0001–$0.10, T 0.05–2.0s, GPU $0.50–$11/hr, M 3–100) right for the launch envelope?
2. What's the actual M today on testnet? §2 of the report predicts M ≈ 13 equilibrium — testnet observation should match.
3. Does the §4.3 monitor ship pre- or post-mainnet?

## Related

- [Settlement](settlement.md) — fee distribution (85/12/3) being analyzed here
- [Jail & Slashing](jail-and-slashing.md) — the 3-verifier-per-task rule that gates the income side
- [Tokenomics](tokenomics.md) — broader fee + supply context
- [Test Plan Status](test-status.md) — closes §2.3 row in the pre-mainnet plan
