"""
Phase 1 MoE bit-exactness tests.

Run on a Mixture-of-Experts model (Mixtral 8x7B, Qwen-MoE, DeepSeek-MoE,
…) — the path that does not yet have a Phase 1 PASS in the V6 PoC. The
question this test answers:

    Under V6's batch-replay protocol, does an MoE model produce
    bit-exact logits + bit-exact expert routing on Worker and Verifier?

A non-zero ``max_abs_err`` here means MoE introduces non-determinism that
batch logging alone does not cover. Two diagnoses:

    Path 1 — gating non-determinism: the router's softmax output picks
             different top-k experts on the replay side. Mitigation:
             record top-k expert IDs in the batch log and force the
             Replayer to follow them (not implemented yet; this test
             surfaces whether the mitigation is needed).

    Path 2 — expert internal compute drift: same as the dense-model
             case, addressed by the existing batch-replay path.

The test produces both signals so the team can tell the two apart from
a single run instead of needing a second ablation.

Hardware notes
--------------
- Mixtral-8x7B-Instruct-v0.1 in bfloat16 needs ≈ 94 GB VRAM. Single
  4090 (24 GB) is OOM. Either (a) use AWQ / GPTQ quantization, (b) use
  multi-GPU with ``device_map="auto"``, (c) downscale the model.
- Smaller MoE alternatives that fit a single 4090 24 GB in bfloat16 are
  rare; ``Qwen/Qwen1.5-MoE-A2.7B`` (~14 B total / 2.7 B active) is
  ≈ 28 GB bf16 and still tight. AWQ variants drop ≈ 25 GB → 4090 OK.
- Recommended runtimes:
    - Single A100/H100 80GB:  Mixtral 8x7B bf16
    - 1 × L20 / RTX A6000 48GB:  Qwen1.5-MoE-A2.7B bf16
    - 1 × 4090 24GB:  Mixtral-AWQ or Qwen1.5-MoE-AWQ (with `autoawq`)

Usage
-----
::

    V6_MODEL=mistralai/Mixtral-8x7B-Instruct-v0.1 \\
    V6_DEVICE=cuda \\
    pytest scripts/v6_replay/test_phase1_moe.py -v

If the chosen ``V6_MODEL`` is not actually MoE (no router_logits surfaced
by the model), this test is skipped — keeping the dense-model Phase 1
suite unaffected.
"""

from __future__ import annotations

import os

import numpy as np
import pytest

from ._common import is_moe_model, load_model_and_tokenizer
from .replay_engine import ReplayEngine
from .worker_simulator import WorkerSimulator

MODEL = os.environ.get("V6_MODEL", "")
DEVICE = os.environ.get("V6_DEVICE", "cuda")
PROMPTS = {
    "task-moe-001": "Write a short sentence about the night sky:",
    "task-moe-002": "List the first three primary colors:",
    "task-moe-003": "How many sides does a hexagon have?",
    "task-moe-004": "What is the capital of France?",
}
SAMPLING = dict(temperature=0.0, top_p=1.0, seed=42)


def _resolve_skip_reason() -> str | None:
    if not MODEL:
        return (
            "V6_MODEL not set; specify a Mixture-of-Experts model id "
            "(e.g. V6_MODEL=mistralai/Mixtral-8x7B-Instruct-v0.1)"
        )
    return None


@pytest.fixture(scope="module")
def moe_run():
    """Run the Worker once on the configured MoE model.

    Skipped if V6_MODEL is unset (test is opt-in — a fresh contributor
    running the full suite without V6_MODEL should see Phase 1a / 1c
    pass, not be required to download a 50 GB Mixtral checkpoint just
    to skip this case).

    Also skipped at runtime if the model loads but isn't actually MoE
    (caller mis-pointed V6_MODEL at a dense Qwen / Llama). Phase 1 dense
    coverage exists in test_phase1.py / test_phase2.py and is the right
    place to test those.
    """
    reason = _resolve_skip_reason()
    if reason:
        pytest.skip(reason)

    w = WorkerSimulator(MODEL, DEVICE)
    if not is_moe_model(w.model):
        pytest.skip(
            f"V6_MODEL={MODEL!r} loaded but is not Mixture-of-Experts; "
            f"this test is MoE-specific. For dense models use test_phase1.py."
        )
    # Schedule: tasks all start at step 0 and run for 10 decode steps —
    # mirrors test_phase1.py SAMPLING. Dynamic-composition variants live
    # in test_phase1c on dense models.
    schedule = {tid: (0, 10) for tid in PROMPTS}
    return w.run_batch_dynamic(PROMPTS, schedule, **SAMPLING)


def test_worker_emits_expert_routing(moe_run):
    """Worker must populate TaskLogits.expert_routing on a real MoE model.

    Empty expert_routing here means either (a) the model is not actually
    MoE (we already gate that in the fixture) or (b) the transformers
    version does not expose router_logits via ``output_router_logits=True``.
    Either way, the rest of this file's assertions are meaningless without
    routing data, so we fail loudly here.
    """
    _, _, task_logits = moe_run
    for tid, tl in task_logits.items():
        assert tl.expert_routing, (
            f"{tid}: expert_routing is empty — the model loaded as MoE but "
            f"the worker did not capture top-k expert IDs. Likely either "
            f"transformers version mismatch or a model family whose "
            f"router_logits are surfaced under a different attribute."
        )
        # One entry per active step.
        assert len(tl.expert_routing) == len(tl.logits), (
            f"{tid}: expert_routing length ({len(tl.expert_routing)}) must "
            f"equal logits length ({len(tl.logits)})"
        )
        # Each step has at least one MoE layer.
        per_layer = tl.expert_routing[0]
        assert per_layer, (
            f"{tid}: step 0 captured zero MoE layers — model claims to be "
            f"MoE but no router output came back."
        )


@pytest.mark.parametrize("target", list(PROMPTS))
def test_replay_logits_bit_exact_moe(moe_run, target):
    """Logits-level Phase 1 assertion for MoE models — same as dense.

    PASS criterion: ``max_abs_err == 0.0``.
    """
    _, log, worker_logits = moe_run

    r = ReplayEngine(MODEL, DEVICE)
    replayed = r.replay_dynamic(log, target_task_id=target)

    w = worker_logits[target].logits
    rp = replayed.logits
    assert len(w) == len(rp), (
        f"{target}: step count differs — worker={len(w)}, replay={len(rp)}"
    )
    for i, (w_step, r_step) in enumerate(zip(w, rp)):
        diff = float(np.max(np.abs(np.asarray(w_step) - np.asarray(r_step))))
        assert diff == 0.0, (
            f"Phase 1 MoE KILL: target={target} step={i} max_abs_err={diff:g} "
            f"— logits drifted across replay. Inspect the expert-routing "
            f"diff next (test_replay_expert_routing_bit_exact_moe) to tell "
            f"Path 1 from Path 2."
        )


@pytest.mark.parametrize("target", list(PROMPTS))
def test_replay_expert_routing_bit_exact_moe(moe_run, target):
    """Expert-routing Phase 1 assertion for MoE models.

    PASS criterion: every (step, layer) selects the same top-k expert
    IDs on Worker and Replayer. A failure here, with logits passing,
    is impossible (different routing → different experts → different
    logits). A failure here with logits also failing means we have hit
    Path 1: gating non-determinism. Mitigation is to record the IDs in
    the BatchLog and force the Replayer to follow them — that mitigation
    is intentionally NOT implemented yet; this test exists to find out
    whether it is needed.
    """
    _, _, worker_logits = moe_run

    r = ReplayEngine(MODEL, DEVICE)
    replayed = r.replay_dynamic(_get_log(moe_run), target_task_id=target)

    w_routing = worker_logits[target].expert_routing
    r_routing = replayed.expert_routing
    assert len(w_routing) == len(r_routing), (
        f"{target}: routing step count differs — "
        f"worker={len(w_routing)}, replay={len(r_routing)}"
    )

    mismatches: list[tuple[int, int, list[int], list[int]]] = []
    for step_i, (w_step, r_step) in enumerate(zip(w_routing, r_routing)):
        assert len(w_step) == len(r_step), (
            f"{target} step={step_i}: layer count differs — "
            f"worker={len(w_step)}, replay={len(r_step)}"
        )
        for layer_i, (w_top, r_top) in enumerate(zip(w_step, r_step)):
            if sorted(w_top) != sorted(r_top):
                mismatches.append((step_i, layer_i, w_top, r_top))

    assert not mismatches, (
        f"Phase 1 MoE Path 1 hit: target={target} — gating selected different "
        f"experts on the replay side at {len(mismatches)} (step, layer) "
        f"positions. First 5: {mismatches[:5]}. "
        f"Path 1 mitigation (force Replayer to use logged expert IDs) is "
        f"needed before V6 can ship MoE support."
    )


def _get_log(moe_run):
    """Helper: extract the BatchLog from the fixture tuple. Kept as a
    function rather than a fixture so the test signatures stay flat."""
    _, log, _ = moe_run
    return log
