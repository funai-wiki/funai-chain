"""
Verifier economics simulator — Pre_Mainnet_Test_Plan §2.3.

Question this answers
---------------------
Under V6 batch-replay verification, can the existing 12% verifier
allocation pay first-tier verifiers enough to break even on GPU rental?
The cost amplification factor is the load-bearing variable: a worker
processes a batch of N tasks in one forward pass, but each verifier of
any task in that batch must replay the WHOLE batch — the verifier pays
N×T compute regardless of how many tasks they're assigned within the
batch.

How to use
----------
Run as a script for the default sweep:
    python3 docs/economics/verifier_economics.py

Tweak parameters at the bottom of the file (CONFIGS list) for custom
scenarios. No third-party deps — just stdlib so this runs anywhere.

The companion report `verifier_economics_report.md` summarises the
findings + KT-review recommendation.

Cost model
----------
Per-task verifier cost (uniform-random VRF dispatch across a pool of
M verifiers, 3 verifiers selected per task):
    expected_tasks_per_batch_per_verifier = N × 3 / M
    cost_per_batch_replay              = N × T × $/hr / 3600
    cost_per_verified_task              = cost_per_batch_replay /
                                          expected_tasks_per_batch_per_verifier
                                        = M × T × $/hr / (3 × 3600)
    income_per_verified_task            = fee × verifier_pool_pct / 3

Net per verified task = income - cost. Sustainable if > 0.

Notice the cost per verified task is INDEPENDENT of N (batch size
factors cancel out in the uniform-VRF case). The batch-replay
amplification shows up as a LATENCY hit, not a per-task economic hit
under uniform dispatch. This is a non-obvious result the simulator
surfaces — see report §3.2.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Iterable


# ── Domain types ─────────────────────────────────────────────────────────────


@dataclass
class Workload:
    """Task arrival profile for one model topic."""

    tasks_per_sec: float           # network-wide arrival rate to this model
    fee_per_task_usd: float        # average user-paid fee
    batch_size: int                # avg worker batch size (V6 continuous batching)


@dataclass
class VerifierMarket:
    """Per-verifier economic profile."""

    pool_size: int                 # M = number of verifiers competing for VRF slots
    verifiers_per_task: int        # spec is 3 per V52
    inference_time_per_task_sec: float  # T = single-task forward-pass latency
    gpu_rental_usd_per_hour: float


@dataclass
class FeeSplit:
    """Fee split sent to the verifier pool. Settlement keeper uses these
    as ratios over 1000 — 120 = 12.0%."""

    verifier_pool_pct: float = 0.12       # current default
    audit_fund_pct: float = 0.03          # second/third-tier split


@dataclass
class EconomicResult:
    """Per-verifier-per-task economics + sustainability verdict."""

    workload: Workload
    market: VerifierMarket
    split: FeeSplit
    income_per_verified_task_usd: float
    cost_per_verified_task_usd: float
    margin_usd: float
    margin_ratio: float            # margin / cost
    sustainable: bool

    @property
    def label(self) -> str:
        return (
            f"M={self.market.pool_size:>3d} N={self.workload.batch_size:>3d} "
            f"T={self.market.inference_time_per_task_sec:>4.2f}s "
            f"GPU=${self.market.gpu_rental_usd_per_hour:>5.2f}/hr "
            f"fee=${self.workload.fee_per_task_usd:>6.4f}"
        )


# ── Simulator ───────────────────────────────────────────────────────────────


def simulate(workload: Workload, market: VerifierMarket, split: FeeSplit) -> EconomicResult:
    """Compute steady-state per-verified-task economics for one verifier.

    Returns ``EconomicResult`` with the raw numbers + sustainability flag.
    See module docstring for the cost-model derivation.
    """
    # Per-verified-task income.
    income = workload.fee_per_task_usd * split.verifier_pool_pct / market.verifiers_per_task

    # Per-verified-task cost — cancels the batch-size factor under uniform VRF.
    cost = (
        market.pool_size
        * market.inference_time_per_task_sec
        * market.gpu_rental_usd_per_hour
        / (market.verifiers_per_task * 3600)
    )

    margin = income - cost
    ratio = margin / cost if cost > 0 else float("inf")
    return EconomicResult(
        workload=workload,
        market=market,
        split=split,
        income_per_verified_task_usd=income,
        cost_per_verified_task_usd=cost,
        margin_usd=margin,
        margin_ratio=ratio,
        sustainable=margin > 0,
    )


def break_even_fee(market: VerifierMarket, split: FeeSplit) -> float:
    """Minimum per-task fee a verifier needs to break even.

    Solving margin = 0 for fee:
        fee = M × T × $/hr / (3 × 3600 × verifier_pool_pct / verifiers_per_task)
            = M × T × $/hr × verifiers_per_task / (3 × 3600 × verifier_pool_pct)
    """
    numer = (
        market.pool_size
        * market.inference_time_per_task_sec
        * market.gpu_rental_usd_per_hour
        * market.verifiers_per_task
    )
    denom = market.verifiers_per_task * 3600 * split.verifier_pool_pct
    return numer / denom


def break_even_pool_size(workload: Workload, market: VerifierMarket, split: FeeSplit) -> float:
    """Maximum pool size M at which a verifier still breaks even.

    Solving margin = 0 for M:
        M_max = fee × verifier_pool_pct × 3 × 3600 / (verifiers_per_task × T × $/hr)
    """
    numer = (
        workload.fee_per_task_usd
        * split.verifier_pool_pct
        * market.verifiers_per_task
        * 3600
    )
    denom = market.verifiers_per_task * market.inference_time_per_task_sec * market.gpu_rental_usd_per_hour
    return numer / denom


# ── Output formatting ───────────────────────────────────────────────────────


def fmt_usd(x: float) -> str:
    if abs(x) < 0.0001:
        return f"${x*1e6:>+8.2f} µ"   # micro-USD
    if abs(x) < 1.0:
        return f"${x*1000:>+8.4f} m"   # milli-USD
    return f"${x:>+10.4f}  "


def print_result_table(rows: Iterable[EconomicResult]) -> None:
    print()
    print(f"{'Scenario':<70} {'Income/task':>14} {'Cost/task':>14} {'Margin/task':>14} {'Ratio':>8} {'OK':>3}")
    print("─" * 130)
    for r in rows:
        ok = "✓" if r.sustainable else "✗"
        print(
            f"{r.label:<70} "
            f"{fmt_usd(r.income_per_verified_task_usd):>14} "
            f"{fmt_usd(r.cost_per_verified_task_usd):>14} "
            f"{fmt_usd(r.margin_usd):>14} "
            f"{r.margin_ratio:>+7.2f} "
            f"{ok:>3}"
        )


# ── Default parameter ranges ────────────────────────────────────────────────
#
# Realistic ranges drawn from the C0 RunPod report (4090 / A100 prices) and
# typical inference latencies for the model sizes we've validated in V6 PoC
# (Qwen 0.5B–7B, Mixtral 8x7B AWQ, Phi-3.5-MoE 42B).


DEFAULT_FEE_USD = [
    0.0001,   # ~ today's per-token fee for tiny models
    0.001,
    0.005,
    0.01,
    0.05,
    0.1,
]

DEFAULT_BATCH_SIZE = [1, 4, 16, 32]
DEFAULT_POOL_SIZE = [3, 6, 10, 25, 50, 100]
DEFAULT_INFERENCE_T = [0.05, 0.2, 0.6, 2.0]   # sec; small → large model
DEFAULT_GPU_RENTAL = [
    0.50,    # T4 / cheap commodity
    1.89,    # RTX PRO 6000 (RunPod, what we used)
    3.50,    # A100 80GB
    11.00,   # H100 80GB on-demand
]


def default_sweep() -> list[EconomicResult]:
    """A focused sweep that highlights the load-bearing decisions."""
    split = FeeSplit()  # current 12 / 3 split
    out: list[EconomicResult] = []

    # Scenario A — vary pool size (M) at a typical workload + GPU rental.
    # Shows the M dependence the cost model predicts.
    for M in DEFAULT_POOL_SIZE:
        out.append(
            simulate(
                Workload(tasks_per_sec=10, fee_per_task_usd=0.01, batch_size=8),
                VerifierMarket(
                    pool_size=M,
                    verifiers_per_task=3,
                    inference_time_per_task_sec=0.2,
                    gpu_rental_usd_per_hour=1.89,
                ),
                split,
            )
        )

    # Scenario B — vary batch size N at a fixed pool. Cost model predicts
    # margin INDEPENDENT of N under uniform VRF; this row confirms it.
    for N in DEFAULT_BATCH_SIZE:
        out.append(
            simulate(
                Workload(tasks_per_sec=10, fee_per_task_usd=0.01, batch_size=N),
                VerifierMarket(
                    pool_size=10,
                    verifiers_per_task=3,
                    inference_time_per_task_sec=0.2,
                    gpu_rental_usd_per_hour=1.89,
                ),
                split,
            )
        )

    # Scenario C — vary fee at fixed M.
    for fee in DEFAULT_FEE_USD:
        out.append(
            simulate(
                Workload(tasks_per_sec=10, fee_per_task_usd=fee, batch_size=8),
                VerifierMarket(
                    pool_size=10,
                    verifiers_per_task=3,
                    inference_time_per_task_sec=0.2,
                    gpu_rental_usd_per_hour=1.89,
                ),
                split,
            )
        )

    # Scenario D — vary inference latency T (proxy for model size).
    for T in DEFAULT_INFERENCE_T:
        out.append(
            simulate(
                Workload(tasks_per_sec=10, fee_per_task_usd=0.01, batch_size=8),
                VerifierMarket(
                    pool_size=10,
                    verifiers_per_task=3,
                    inference_time_per_task_sec=T,
                    gpu_rental_usd_per_hour=1.89,
                ),
                split,
            )
        )

    # Scenario E — vary GPU rental cost.
    for gpu in DEFAULT_GPU_RENTAL:
        out.append(
            simulate(
                Workload(tasks_per_sec=10, fee_per_task_usd=0.01, batch_size=8),
                VerifierMarket(
                    pool_size=10,
                    verifiers_per_task=3,
                    inference_time_per_task_sec=0.2,
                    gpu_rental_usd_per_hour=gpu,
                ),
                split,
            )
        )

    return out


def print_break_even_table() -> None:
    """Tabulate break-even fee across realistic (M, T, GPU) combinations.

    This is the load-bearing output of this study: for each model-size
    bucket and pool size, what's the MINIMUM fee that keeps verifiers
    sustainable under the current 12% allocation?
    """
    split = FeeSplit()

    print()
    print("=" * 80)
    print("Break-even fee per task (USD) under current 12% allocation")
    print(f"verifiers_per_task = 3, fee_split = {int(split.verifier_pool_pct*100)}%")
    print("=" * 80)

    print()
    print(f"{'inference time':>14} | {'pool size':>10} |", end="")
    for gpu in DEFAULT_GPU_RENTAL:
        print(f" GPU=${gpu:>5.2f}/hr", end="")
    print()
    print("-" * 95)

    for T in DEFAULT_INFERENCE_T:
        for M in DEFAULT_POOL_SIZE:
            print(f"{T:>14.2f}s | {M:>10d} |", end="")
            for gpu in DEFAULT_GPU_RENTAL:
                market = VerifierMarket(
                    pool_size=M,
                    verifiers_per_task=3,
                    inference_time_per_task_sec=T,
                    gpu_rental_usd_per_hour=gpu,
                )
                fee = break_even_fee(market, split)
                print(f"  ${fee:>9.5f}  ", end="")
            print()
        print()


def print_alternative_split_comparison() -> None:
    """Compare current 12% vs alternative allocations on a stress scenario."""
    workload = Workload(tasks_per_sec=10, fee_per_task_usd=0.001, batch_size=8)
    market = VerifierMarket(
        pool_size=25,
        verifiers_per_task=3,
        inference_time_per_task_sec=0.6,
        gpu_rental_usd_per_hour=3.50,
    )

    print()
    print("=" * 80)
    print("Alternative verifier-pool allocations on a stress scenario")
    print(f"  workload: {workload}")
    print(f"  market:   {market}")
    print("=" * 80)

    for pct in [0.05, 0.08, 0.10, 0.12, 0.15, 0.18, 0.20, 0.25]:
        result = simulate(workload, market, FeeSplit(verifier_pool_pct=pct))
        ok = "✓ sustainable" if result.sustainable else "✗ underwater"
        print(
            f"  verifier_pool = {pct*100:>5.1f}%   "
            f"income {fmt_usd(result.income_per_verified_task_usd)} "
            f"cost {fmt_usd(result.cost_per_verified_task_usd)} "
            f"margin {fmt_usd(result.margin_usd)}   {ok}"
        )


# ── Entry point ─────────────────────────────────────────────────────────────


def main() -> None:
    print("Verifier economics — Pre_Mainnet_Test_Plan §2.3")
    print("Cost model: per-task verifier cost = M × T × $/hr / (3 × 3600)")
    print("Income:     per-task verifier income = fee × 12% / 3 = fee × 4%")
    print()

    rows = default_sweep()
    print_result_table(rows)

    print_break_even_table()
    print_alternative_split_comparison()


if __name__ == "__main__":
    main()
