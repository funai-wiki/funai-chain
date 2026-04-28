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

import numpy as np

from ._common import (
    ATTENTION_IMPL,
    ENGINE_ID,
    configure_determinism,
    engine_version,
    extract_top_k_experts_per_step,
    is_moe_model,
    load_model_and_tokenizer,
    moe_top_k,
)
from ._validation import (
    validate_log_against_tokenizer,
    validate_per_task_token_counts,
)
from .replay_types import BatchLog, TaskLogits
from .sampling import chacha20_sample, derive_final_seed


def _check_engine_match(batch_log: BatchLog) -> None:
    """
    KT v2 §2.5 — reject any log that was produced by a different engine /
    version / attention impl than the current runtime.

    In V6 production this is enforced implicitly via `model_id` registration:
    Worker and Verifier can only run a model if they use the version
    declared by the proposer. At PoC level we check explicitly because the
    BatchLog carries the Worker's runtime metadata and the Replayer has no
    on-chain registry to cross-validate against.
    """
    current_version = engine_version()
    mismatches = []
    if batch_log.engine_id != ENGINE_ID:
        mismatches.append(f"engine_id: {batch_log.engine_id!r} vs {ENGINE_ID!r}")
    if batch_log.engine_version and batch_log.engine_version != current_version:
        mismatches.append(
            f"engine_version: {batch_log.engine_version!r} vs {current_version!r}"
        )
    if batch_log.attention_impl != ATTENTION_IMPL:
        mismatches.append(
            f"attention_impl: {batch_log.attention_impl!r} vs {ATTENTION_IMPL!r}"
        )
    if mismatches:
        raise ValueError(
            "BatchLog engine metadata does not match Replayer runtime — "
            "per KT v2 §2.5 Worker and Verifier must use the same engine + "
            "version + attention impl. Mismatches: " + "; ".join(mismatches)
        )


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

        _check_engine_match(batch_log)
        # §2.9 boundary checks fire before configure_determinism so a
        # tokenizer mismatch surfaces in milliseconds, before we burn any
        # determinism / model state on a doomed replay.
        validate_log_against_tokenizer(batch_log, self.tokenizer, self.model)
        validate_per_task_token_counts(batch_log, self.tokenizer)
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

    # ── Phase 1c: dynamic batch composition ──────────────────────────────────
    #
    # `replay_dynamic` pairs with `WorkerSimulator.run_batch_dynamic`. Both
    # use the recompute-from-scratch path: at each step listed in
    # `batch_log.steps`, rebuild the active tasks' contexts (prompt +
    # sampled tokens so far) and do one forward pass. The target's logits
    # are collected at each step where it was active.
    #
    # Symmetric with the Worker: same tokenization, same padding, same
    # attention mask layout, same determinism flags. Bit-exactness is
    # a consequence of both sides walking the same deterministic code path
    # with the same inputs (driven by BatchLog.steps).

    @torch.no_grad()
    def replay_dynamic(self, batch_log: BatchLog, *, target_task_id: str) -> TaskLogits:
        """
        Replay a dynamic-composition ``BatchLog`` and return logits for the
        designated target. Honours per-step ``active_task_ids`` exactly.

        Returned ``TaskLogits.logits`` contains one entry per step where
        ``target_task_id`` appeared in the active roster; the entries are
        in step order (monotonically increasing ``step_index``).

        Raises:
            ValueError: target not in the log, model/seed mismatch, or
                malformed sampling params for ``temperature > 0``.
        """
        if target_task_id not in batch_log.task_prompts:
            raise ValueError(
                f"target_task_id={target_task_id} not in batch_log.task_prompts "
                f"(have {sorted(batch_log.task_prompts)})"
            )
        if batch_log.temperature < 0:
            raise ValueError(
                f"batch_log.temperature must be >= 0, got {batch_log.temperature}"
            )
        if batch_log.temperature > 0 and not (0.0 < batch_log.top_p <= 1.0):
            raise ValueError(
                "batch_log.top_p must be in (0, 1] when temperature > 0, "
                f"got {batch_log.top_p}"
            )
        if batch_log.model_id != self.model_id:
            raise ValueError(
                f"batch_log.model_id={batch_log.model_id!r} does not match "
                f"ReplayEngine.model_id={self.model_id!r}"
            )
        if not batch_log.steps:
            raise ValueError("batch_log.steps is empty")

        _check_engine_match(batch_log)
        validate_log_against_tokenizer(batch_log, self.tokenizer, self.model)
        validate_per_task_token_counts(batch_log, self.tokenizer)
        configure_determinism(batch_log.seed)

        # §2.9 row 4: apply the same truncation cap the Worker recorded,
        # so prompts that would otherwise exceed `max_position_embeddings`
        # are trimmed identically on both sides. With cap=None this is a
        # no-op and matches the legacy behaviour.
        cap = batch_log.max_prompt_tokens
        prompt_ids: dict[str, list[int]] = {}
        for tid, prompt in batch_log.task_prompts.items():
            ids = list(self.tokenizer(prompt, add_special_tokens=True)["input_ids"])
            if cap is not None and len(ids) > cap:
                ids = ids[:cap]
            prompt_ids[tid] = ids
        sampled_tokens: dict[str, list[int]] = {tid: [] for tid in batch_log.task_prompts}
        target_logits: list = []
        target_sampled: list[int] = []
        target_expert_routing: list[list[list[int]]] = []
        pad_id = self.tokenizer.pad_token_id
        # Re-derive per-task ChaCha20 keys from the log — symmetric with Worker.
        final_seeds: dict[str, bytes] = {
            tid: derive_final_seed(tid, batch_log.seed) for tid in batch_log.task_prompts
        }

        moe_capture = is_moe_model(self.model)
        moe_k = moe_top_k(self.model) if moe_capture else 0

        for step in batch_log.steps:
            active = step.active_task_ids
            if not active:
                continue

            contexts = [prompt_ids[tid] + sampled_tokens[tid] for tid in active]
            max_len = max(len(c) for c in contexts)
            padded = [[pad_id] * (max_len - len(c)) + c for c in contexts]
            attn = [[0] * (max_len - len(c)) + [1] * len(c) for c in contexts]

            input_ids = torch.tensor(padded, dtype=torch.long, device=self.device)
            attention_mask = torch.tensor(attn, dtype=torch.long, device=self.device)

            forward_kwargs = dict(
                input_ids=input_ids,
                attention_mask=attention_mask,
                use_cache=False,
            )
            if moe_capture:
                forward_kwargs["output_router_logits"] = True

            out = self.model(**forward_kwargs)
            step_logits = out.logits[:, -1, :]
            step_routing = (
                extract_top_k_experts_per_step(out.router_logits, top_k=moe_k)
                if moe_capture and getattr(out, "router_logits", None) is not None
                else []
            )

            for i, tid in enumerate(active):
                logit_vec = step_logits[i].detach().float().cpu().numpy()
                if batch_log.temperature == 0.0:
                    tok = int(np.argmax(logit_vec))
                else:
                    tok = chacha20_sample(
                        logit_vec,
                        temperature=batch_log.temperature,
                        top_p=batch_log.top_p,
                        final_seed=final_seeds[tid],
                        step_index=step.step_index,
                    )
                sampled_tokens[tid].append(tok)
                if tid == target_task_id:
                    target_logits.append(logit_vec)
                    target_sampled.append(tok)
                    if moe_capture and step_routing:
                        target_expert_routing.append(step_routing[i])

        return TaskLogits(
            task_id=target_task_id,
            logits=target_logits,
            sampled_tokens=target_sampled,
            expert_routing=target_expert_routing,
        )
