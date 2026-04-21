"""
Worker-side batched inference + batch log emission.

Phase 1a scope
--------------
- ``temperature == 0`` only (argmax sampling). ChaCha20-seeded sampling
  lands in Phase 1b.
- Fixed batch composition: all tasks start together and run for exactly
  ``max_new_tokens`` decode steps, regardless of EOS. Every ``BatchStep``
  in the resulting log has the same ``active_task_ids``. Dynamic
  join/leave scheduling lands in Phase 1c.

The Phase 1a implementation intentionally exercises the weakest
interesting invariant: "same-GPU batched forward pass is deterministic
and re-runnable." A PASS here does not yet validate V6 — it validates the
determinism floor that V6 depends on. V6's distinctive claim comes in
Phase 1c (dynamic batch composition replayable bit-exact).

Determinism contract — all enforced by ``configure_determinism`` in
``_common``:

- ``torch.use_deterministic_algorithms(True)``
- ``torch.manual_seed(seed)`` + ``torch.cuda.manual_seed_all(seed)`` at
  the start of each ``run_batch`` call
- ``model.eval()`` with ``requires_grad=False`` on all parameters
- ``torch.float16`` end-to-end (matches C0 report baseline)
- eager attention — avoids SDPA kernel-selection non-determinism
"""

from __future__ import annotations

from typing import Any

import torch

from ._common import configure_determinism, load_model_and_tokenizer
from .replay_types import BatchLog, BatchStep, TaskLogits


class WorkerSimulator:
    def __init__(self, model_id: str, device: str) -> None:
        self.model_id = model_id
        self.device = device
        self.model, self.tokenizer = load_model_and_tokenizer(model_id, device)

    @torch.no_grad()
    def run_batch(
        self,
        task_prompts: dict[str, str],
        *,
        max_new_tokens: int,
        temperature: float,
        top_p: float,
        seed: int,
    ) -> tuple[dict[str, str], BatchLog, dict[str, TaskLogits]]:
        if temperature != 0.0:
            raise NotImplementedError(
                "Phase 1a MVP supports temperature=0 (argmax) only. "
                "Temperature>0 with ChaCha20 sampling lands in Phase 1b. "
                "See scripts/v6_replay/README.md."
            )

        configure_determinism(seed)

        # Fix a deterministic task ordering. dict preserves insertion order
        # since Python 3.7; a Worker that wants a different layout must
        # rewrite this ordering explicitly (and document it) so the
        # Replayer can reproduce.
        task_ids = tuple(task_prompts.keys())
        prompts = [task_prompts[tid] for tid in task_ids]

        enc = self.tokenizer(prompts, return_tensors="pt", padding=True)
        input_ids = enc["input_ids"].to(self.device)
        attention_mask = enc["attention_mask"].to(self.device)

        next_logits, cache, attention_mask, first_tok = self._prefill(
            input_ids, attention_mask
        )

        per_task_logits: dict[str, list[Any]] = {tid: [] for tid in task_ids}
        per_task_tokens: dict[str, list[int]] = {tid: [] for tid in task_ids}
        for i, tid in enumerate(task_ids):
            per_task_logits[tid].append(next_logits[i].detach().float().cpu().numpy())
            per_task_tokens[tid].append(int(first_tok[i]))

        steps = [BatchStep(step_index=0, active_task_ids=task_ids)]

        next_tokens = first_tok
        for step_idx in range(1, max_new_tokens):
            step_logits, cache, attention_mask, next_tokens = self._decode_step(
                next_tokens, cache, attention_mask
            )
            for i, tid in enumerate(task_ids):
                per_task_logits[tid].append(step_logits[i].detach().float().cpu().numpy())
                per_task_tokens[tid].append(int(next_tokens[i]))
            steps.append(BatchStep(step_index=step_idx, active_task_ids=task_ids))

        outputs = {
            tid: self.tokenizer.decode(per_task_tokens[tid], skip_special_tokens=True)
            for tid in task_ids
        }

        log = BatchLog(
            model_id=self.model_id,
            seed=seed,
            temperature=temperature,
            top_p=top_p,
            max_new_tokens=max_new_tokens,
            task_prompts=dict(task_prompts),
            steps=steps,
            dtype="bfloat16",
        )

        task_logits = {
            tid: TaskLogits(task_id=tid, logits=per_task_logits[tid])
            for tid in task_ids
        }

        return outputs, log, task_logits

    def _prefill(self, input_ids: torch.Tensor, attention_mask: torch.Tensor):
        """Run the prompt through the model; return (last-token logits,
        updated cache, extended attention mask, argmax next-tokens)."""
        out = self.model(
            input_ids=input_ids,
            attention_mask=attention_mask,
            use_cache=True,
        )
        cache = out.past_key_values
        # Last non-padded token for each sequence. With left padding, the
        # last column is always the real last token.
        last_logits = out.logits[:, -1, :]
        next_tokens = torch.argmax(last_logits, dim=-1)
        return last_logits, cache, attention_mask, next_tokens

    def _decode_step(
        self,
        prev_tokens: torch.Tensor,
        cache,
        attention_mask: torch.Tensor,
    ):
        """One decode step for the whole batch; returns logits + updated state."""
        step_input = prev_tokens.unsqueeze(-1)
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
            input_ids=step_input,
            attention_mask=attention_mask,
            past_key_values=cache,
            use_cache=True,
        )
        step_logits = out.logits[:, -1, :]
        next_tokens = torch.argmax(step_logits, dim=-1)
        return step_logits, out.past_key_values, attention_mask, next_tokens
