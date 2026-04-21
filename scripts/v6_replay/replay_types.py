"""
Shared dataclasses for the V6 Batch Log-Replay PoC.

Named `replay_types` instead of `types` to avoid shadowing the stdlib module.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


@dataclass(frozen=True)
class BatchStep:
    """
    One decode step of a continuous-batching run.

    Records which task IDs were actively generating tokens at ``step_index``,
    **in their memory-layout order**. Order matters: fp16 reduction
    determinism depends on which batch position a given task occupies, and
    replay must reproduce that layout exactly.
    """

    step_index: int
    active_task_ids: tuple[str, ...]


@dataclass
class BatchLog:
    """
    Full per-step schedule the Worker executed; enough to replay.

    ``task_prompts`` carries the prompt for every ``task_id`` that appears in
    any step, so the replay engine can reconstruct KV cache at prefill time
    without relying on external storage.
    """

    model_id: str
    seed: int
    temperature: float
    top_p: float
    max_new_tokens: int
    task_prompts: dict[str, str]
    steps: list[BatchStep] = field(default_factory=list)
    dtype: str = "float16"

    def active_step_indices(self, task_id: str) -> list[int]:
        """Return the decode step indices where ``task_id`` was active."""
        return [s.step_index for s in self.steps if task_id in s.active_task_ids]


@dataclass
class TaskLogits:
    """
    Logits captured at every decode step for one task, plus the token
    sampled at each of those steps.

    ``logits[i]`` is the vocabulary logprob vector produced at the i-th step
    where this task was active (not the i-th step of the whole batch).
    Concrete type is ``numpy.ndarray`` with shape ``[vocab_size]`` and dtype
    matching ``BatchLog.dtype``; typed as ``Any`` here to avoid a hard numpy
    dependency at type-check time.

    ``sampled_tokens[i]`` is the token id the Worker / Replayer sampled
    from ``logits[i]``. Phase 1a/1c (temperature=0) → argmax; Phase 1b
    (temperature>0) → ChaCha20-seeded inverse-CDF. Both sides must agree
    on every entry for the Phase 1b bit-exactness assertion to pass.
    ``default_factory=list`` keeps the field backward-compatible with
    Phase 1a/1c code that does not populate it.
    """

    task_id: str
    logits: list[Any]  # list[np.ndarray], shape [vocab_size] per entry
    sampled_tokens: list[int] = field(default_factory=list)
