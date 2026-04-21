"""
Verifier-side replay engine.

Phase 1a scope
--------------
In Phase 1a the batch composition is fixed — every ``BatchStep`` in a
given ``BatchLog`` lists the same ``active_task_ids``. Replay therefore
reduces to re-running the Worker's fixed-batch inference and extracting
logits for the designated target. Phase 1c adds honoring per-step
join/leave events.

The replay engine shares ``_common.load_model_and_tokenizer`` and
``configure_determinism`` with the Worker; any divergence on these is a
determinism leak and will surface as a Phase 1a assertion failure.
"""

from __future__ import annotations

import torch

from ._common import configure_determinism, load_model_and_tokenizer
from .replay_types import BatchLog, TaskLogits


class ReplayEngine:
    def __init__(self, model_id: str, device: str) -> None:
        self.model_id = model_id
        self.device = device
        self.model, self.tokenizer = load_model_and_tokenizer(model_id, device)

    @torch.no_grad()
    def replay(self, batch_log: BatchLog, *, target_task_id: str) -> TaskLogits:
        if target_task_id not in batch_log.task_prompts:
            raise ValueError(
                f"target_task_id={target_task_id} not in batch_log.task_prompts "
                f"(have {sorted(batch_log.task_prompts)})"
            )
        if batch_log.temperature != 0.0:
            raise NotImplementedError(
                "Phase 1a MVP supports temperature=0 only; the ingested log "
                f"has temperature={batch_log.temperature}."
            )
        if batch_log.model_id != self.model_id:
            raise ValueError(
                f"batch_log.model_id={batch_log.model_id!r} does not match "
                f"ReplayEngine.model_id={self.model_id!r}"
            )

        configure_determinism(batch_log.seed)

        self._require_fixed_composition(batch_log)
        task_ids = tuple(batch_log.steps[0].active_task_ids)
        if target_task_id not in task_ids:
            raise ValueError(
                f"target_task_id={target_task_id} is not active in step 0 "
                f"(active: {task_ids})"
            )
        target_index = task_ids.index(target_task_id)

        prompts = [batch_log.task_prompts[tid] for tid in task_ids]
        enc = self.tokenizer(prompts, return_tensors="pt", padding=True)
        input_ids = enc["input_ids"].to(self.device)
        attention_mask = enc["attention_mask"].to(self.device)

        out = self.model(
            input_ids=input_ids,
            attention_mask=attention_mask,
            use_cache=True,
        )
        cache = out.past_key_values
        step_logits = out.logits[:, -1, :]
        collected = [step_logits[target_index].detach().float().cpu().numpy()]
        next_tokens = torch.argmax(step_logits, dim=-1)

        for _ in range(1, batch_log.max_new_tokens):
            attention_mask = torch.cat(
                [
                    attention_mask,
                    torch.ones(
                        (attention_mask.shape[0], 1),
                        dtype=attention_mask.dtype,
                        device=attention_mask.device,
                    ),
                ],
                dim=-1,
            )
            out = self.model(
                input_ids=next_tokens.unsqueeze(-1),
                attention_mask=attention_mask,
                past_key_values=cache,
                use_cache=True,
            )
            cache = out.past_key_values
            step_logits = out.logits[:, -1, :]
            collected.append(step_logits[target_index].detach().float().cpu().numpy())
            next_tokens = torch.argmax(step_logits, dim=-1)

        return TaskLogits(task_id=target_task_id, logits=collected)

    @staticmethod
    def _require_fixed_composition(batch_log: BatchLog) -> None:
        if not batch_log.steps:
            raise ValueError("batch_log.steps is empty")
        reference = set(batch_log.steps[0].active_task_ids)
        for step in batch_log.steps:
            if set(step.active_task_ids) != reference:
                raise NotImplementedError(
                    "Phase 1a MVP expects fixed batch composition; dynamic "
                    "membership (step "
                    f"{step.step_index} differs from step 0) is Phase 1c+ work."
                )
