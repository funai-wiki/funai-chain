"""
Pre_Mainnet_Test_Plan §2.9 boundary-condition validators.

Pure-Python helpers — no torch / numpy / transformers imports — so
they're testable in CPU-only CI without a GPU. Both Worker and Replayer
import these to enforce the same contract.

Each validator carries an explicit §2.9 row reference in its raised
message so a failed run points to the test-plan section without grep.
"""

from __future__ import annotations

from typing import Any, Iterable

from .replay_types import BatchLog


def gather_eos_token_ids(tokenizer, model) -> tuple[int, ...]:
    """Return every token id the loaded tokenizer + model treat as EOS.

    A model can have multiple end-of-text tokens (e.g. Llama 3 has both
    `<|eot_id|>` and `<|end_of_text|>`; Phi-3 surfaces `<|endoftext|>`
    plus chat-specific end markers). The Worker must record the FULL set
    so the Replayer can confirm it has loaded the same tokenizer
    revision. The PoC's replay logic does NOT use these for any
    control-flow decision — the schedule recorded in `BatchLog.steps` is
    the only authority for when a task leaves the batch (§2.9 row 1).
    EOS here is purely a tokenizer-identity fingerprint.

    Returns the IDs sorted + deduped so equality comparison is order-free.
    """
    raw: list[int] = []
    if tokenizer is not None and getattr(tokenizer, "eos_token_id", None) is not None:
        raw.append(int(tokenizer.eos_token_id))

    cfg = getattr(model, "config", None) if model is not None else None
    if cfg is not None:
        cfg_eos = getattr(cfg, "eos_token_id", None)
        if isinstance(cfg_eos, int):
            raw.append(cfg_eos)
        elif isinstance(cfg_eos, Iterable):
            raw.extend(int(x) for x in cfg_eos if isinstance(x, int))

    return tuple(sorted(set(raw)))


def validate_log_against_tokenizer(
    log: BatchLog,
    tokenizer,
    model=None,
    *,
    expected_sampling_extra: dict[str, Any] | None = None,
) -> None:
    """Raise ``ValueError`` when `log`'s recorded tokenizer / sampler
    config differs from what `tokenizer` (and the Replayer's sampler)
    would actually use.

    Catches Pre_Mainnet_Test_Plan §2.9 boundary divergences before any
    model forward pass — fail in milliseconds instead of after a
    multi-second prefill that produces meaningless output.

    Args:
      log: the BatchLog the Worker emitted.
      tokenizer: the Replayer's tokenizer instance.
      model: optional Replayer model (used to surface a fuller EOS set
             when the tokenizer alone is missing some).
      expected_sampling_extra: the Replayer's sampler-extra-params dict.
             None means "Replayer has no extras" → log must also be {}.
    """
    # §2.9 row 2 — padding contract.
    if log.padding_side and log.padding_side != tokenizer.padding_side:
        raise ValueError(
            f"§2.9 row 2: padding_side mismatch — Worker recorded "
            f"{log.padding_side!r} but Replayer tokenizer is "
            f"{tokenizer.padding_side!r}. Different padding flips the "
            f"attention mask and silently desynchronises the KV cache "
            f"from step 0."
        )
    if tokenizer.pad_token_id is not None and int(tokenizer.pad_token_id) != log.pad_token_id:
        raise ValueError(
            f"§2.9 row 2: pad_token_id mismatch — Worker recorded "
            f"{log.pad_token_id} but Replayer tokenizer has "
            f"{int(tokenizer.pad_token_id)}. Different pad token writes "
            f"different literal IDs into input_ids."
        )

    # §2.9 row 1 — EOS contract.
    if log.eos_token_ids:
        replayer_eos = gather_eos_token_ids(tokenizer, model)
        if replayer_eos != log.eos_token_ids:
            raise ValueError(
                f"§2.9 row 1: EOS set mismatch — Worker recorded "
                f"{list(log.eos_token_ids)} but Replayer surfaces "
                f"{list(replayer_eos)}. The model_id resolved to a "
                f"different tokenizer revision; refuse to replay."
            )

    # §2.9 row 3 — sampler extras.
    expected = expected_sampling_extra or {}
    if dict(log.sampling_params_extra) != dict(expected):
        raise ValueError(
            f"§2.9 row 3: sampling_params_extra mismatch — Worker "
            f"recorded {dict(log.sampling_params_extra)!r} but Replayer "
            f"is configured for {dict(expected)!r}. Every sampler input "
            f"must be enumerated explicitly on both sides."
        )


def validate_per_task_token_counts(
    log: BatchLog,
    tokenizer,
) -> None:
    """§2.9 row 4 — assert the Replayer tokenizes each prompt to the
    same length the Worker recorded.

    This is split from `validate_log_against_tokenizer` because it has
    to actually call the tokenizer on each prompt (potentially
    expensive for very long prompts), whereas the per-tokenizer-config
    checks above are O(1). Replayers can call both; tests can call just
    the cheap one.

    Honours `log.max_prompt_tokens`: the Replayer must apply the same
    truncation cap as the Worker before measuring length.

    Empty `log.task_prompt_token_counts` is treated as "Worker did not
    record" (legacy path) and the check is skipped silently.
    """
    if not log.task_prompt_token_counts:
        return
    cap = log.max_prompt_tokens
    for tid, prompt in log.task_prompts.items():
        recorded = log.task_prompt_token_counts.get(tid)
        if recorded is None:
            raise ValueError(
                f"§2.9 row 4: task {tid!r} appears in task_prompts but "
                f"task_prompt_token_counts is missing it. Worker bug."
            )
        ids = list(tokenizer(prompt, add_special_tokens=True)["input_ids"])
        if cap is not None and len(ids) > cap:
            ids = ids[:cap]
        if len(ids) != recorded:
            raise ValueError(
                f"§2.9 row 4: task {tid!r} prompt tokenizes to "
                f"{len(ids)} tokens on Replayer but Worker recorded "
                f"{recorded} (cap={cap}). Tokenizer revision drift."
            )
