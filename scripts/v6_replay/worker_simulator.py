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

import numpy as np
import torch

from ._common import configure_determinism, load_model_and_tokenizer
from .replay_types import BatchLog, BatchStep, TaskLogits
from .sampling import chacha20_sample, derive_final_seed


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

    # ── Phase 1c: dynamic batch composition ──────────────────────────────────
    #
    # `run_batch_dynamic` is the load-bearing V6 validation path. Unlike Phase
    # 1a's `run_batch`, it honours a per-task schedule so each task can join
    # and leave the batch at different steps, producing a BatchLog with
    # non-uniform per-step `active_task_ids`. The ReplayEngine then replays
    # that exact schedule and must recover bit-identical logits for any
    # target task.
    #
    # Implementation style: recompute-from-scratch at every decode step
    # (no shared KV cache across steps). This is 5-10x slower than true
    # continuous batching on a per-step basis but avoids the complexity of
    # slicing per-task KV tensors in/out of a shared cache when composition
    # changes. Same result; simpler code; sufficient for a determinism PoC.
    # Production engines (TGI, vLLM) will use proper cache management.
    #
    # Both Worker and Replayer use the same recompute path, so bit-exactness
    # is a function of PyTorch determinism (seeds, algorithms flag, eager
    # attention) — not of whether the two paths match some external engine.

    @torch.no_grad()
    def run_batch_dynamic(
        self,
        task_prompts: dict[str, str],
        task_schedule: dict[str, tuple[int, int]],
        *,
        temperature: float,
        top_p: float,
        seed: int,
    ) -> tuple[dict[str, str], BatchLog, dict[str, TaskLogits]]:
        """
        Run inference with a dynamic per-task schedule.

        Args:
            task_prompts: ``task_id -> prompt``.
            task_schedule: ``task_id -> (start_step, end_step)``; task is active
                at step K iff ``start_step <= K < end_step``. Keys must match
                ``task_prompts``.
            temperature: ``0.0`` → argmax (Phase 1c). ``> 0`` → ChaCha20-seeded
                inverse-CDF sampling (Phase 1b). See ``sampling.py``.
            top_p: nucleus threshold. Ignored when temperature == 0.
            seed: Worker seed, fed to ``configure_determinism`` and to
                ``derive_final_seed`` for per-task ChaCha20 key derivation.

        Returns: same shape as ``run_batch``.
        """
        if temperature < 0:
            raise ValueError(f"temperature must be >= 0, got {temperature}")
        if temperature > 0 and not (0.0 < top_p <= 1.0):
            raise ValueError(f"top_p must be in (0, 1] when temperature > 0, got {top_p}")
        if set(task_schedule) != set(task_prompts):
            missing = set(task_prompts) - set(task_schedule)
            extra = set(task_schedule) - set(task_prompts)
            raise ValueError(
                f"task_schedule keys must match task_prompts; "
                f"missing={missing}, extra={extra}"
            )

        configure_determinism(seed)

        # Pre-tokenize each prompt once. Returned list of ints, no padding —
        # padding happens per-step because active contexts have varying length.
        prompt_ids: dict[str, list[int]] = {
            tid: list(self.tokenizer(prompt, add_special_tokens=True)["input_ids"])
            for tid, prompt in task_prompts.items()
        }

        max_step = max(end for _, end in task_schedule.values())
        sampled_tokens: dict[str, list[int]] = {tid: [] for tid in task_prompts}
        per_task_logits: dict[str, list[Any]] = {tid: [] for tid in task_prompts}
        steps: list[BatchStep] = []

        pad_id = self.tokenizer.pad_token_id
        # Per-task ChaCha20 key — pre-derived so the ChaCha stream for each task
        # is independent of other tasks' activity.
        final_seeds: dict[str, bytes] = {
            tid: derive_final_seed(tid, seed) for tid in task_prompts
        }

        for step in range(max_step):
            active = tuple(
                tid
                for tid in task_prompts
                if task_schedule[tid][0] <= step < task_schedule[tid][1]
            )
            if not active:
                continue

            # One forward pass over active tasks; contexts are left-padded to
            # the max active context length.
            contexts = [prompt_ids[tid] + sampled_tokens[tid] for tid in active]
            max_len = max(len(c) for c in contexts)
            padded = [[pad_id] * (max_len - len(c)) + c for c in contexts]
            attn = [[0] * (max_len - len(c)) + [1] * len(c) for c in contexts]

            input_ids = torch.tensor(padded, dtype=torch.long, device=self.device)
            attention_mask = torch.tensor(attn, dtype=torch.long, device=self.device)

            out = self.model(
                input_ids=input_ids,
                attention_mask=attention_mask,
                use_cache=False,
            )
            step_logits = out.logits[:, -1, :]

            for i, tid in enumerate(active):
                logit_vec = step_logits[i].detach().float().cpu().numpy()
                per_task_logits[tid].append(logit_vec)
                if temperature == 0.0:
                    tok = int(np.argmax(logit_vec))
                else:
                    tok = chacha20_sample(
                        logit_vec,
                        temperature=temperature,
                        top_p=top_p,
                        final_seed=final_seeds[tid],
                        step_index=step,
                    )
                sampled_tokens[tid].append(tok)

            steps.append(BatchStep(step_index=step, active_task_ids=active))

        outputs = {
            tid: self.tokenizer.decode(sampled_tokens[tid], skip_special_tokens=True)
            for tid in task_prompts
        }

        log = BatchLog(
            model_id=self.model_id,
            seed=seed,
            temperature=temperature,
            top_p=top_p,
            max_new_tokens=max_step,
            task_prompts=dict(task_prompts),
            steps=steps,
            dtype="bfloat16",
        )

        task_logits = {
            tid: TaskLogits(
                task_id=tid,
                logits=per_task_logits[tid],
                sampled_tokens=list(sampled_tokens[tid]),
            )
            for tid in task_prompts
        }

        return outputs, log, task_logits
