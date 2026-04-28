"""
Pre_Mainnet_Test_Plan §2.9 rows 1–4 — V6 boundary-condition guards.

These tests pin the "shouldn't silently drift" contract for the four V6
PoC schema additions:

  Row 1 — EOS set is recorded; replay refuses if Replayer's tokenizer
          surfaces a different set (tokenizer revision drift).
  Row 2 — padding_side + pad_token_id are recorded; mismatch on either
          raises before any model forward.
  Row 3 — sampling_params_extra is enumerated explicitly; any divergence
          between Worker and Replayer (e.g. a new sampler param landed on
          one side but not the other) raises.
  Row 4 — max_prompt_tokens cap is honoured by both sides; per-task
          tokenized prompt length is recorded so tokenizer revision drift
          surfaces here instead of as silent logit divergence.

These are pure unit tests — they construct synthetic ``BatchLog``
instances and a minimal mock tokenizer. No GPU, no real model, no HF
network round-trip. They run on any CPU in milliseconds and protect the
schema against future refactors.

The integration-level test (real model + real replay) is exercised by
``test_phase1.py`` / ``test_phase1_moe.py``; those still need a GPU.
"""

from __future__ import annotations

import pytest

from ._validation import (
    gather_eos_token_ids,
    validate_log_against_tokenizer,
    validate_per_task_token_counts,
)
from .replay_types import BatchLog, BatchStep


# ── Mocks ───────────────────────────────────────────────────────────────────


class MockTokenizer:
    """Minimum surface area the validator touches.

    Real `transformers.PreTrainedTokenizerBase` exposes a callable plus
    `padding_side`, `pad_token_id`, `eos_token_id` — the validator only
    needs the last three plus tokenize-prompt. Mock the lot rather than
    pull a 100 MB tokenizer from HuggingFace into a unit test.
    """

    def __init__(
        self,
        *,
        padding_side: str = "left",
        pad_token_id: int = 0,
        eos_token_id: int | None = 50256,
        tokenize: dict[str, list[int]] | None = None,
    ) -> None:
        self.padding_side = padding_side
        self.pad_token_id = pad_token_id
        self.eos_token_id = eos_token_id
        # Per-prompt static tokenization. Tests pre-populate this so the
        # tokenizer is fully deterministic without needing a model.
        self._tokenize = tokenize or {}

    def __call__(self, prompt: str, *, add_special_tokens: bool = True):
        if prompt not in self._tokenize:
            # Default: 1 token per character. Lets tests measure length
            # without enumerating every possible prompt up front.
            ids = [ord(c) for c in prompt]
        else:
            ids = list(self._tokenize[prompt])
        return {"input_ids": ids}


class MockModelConfig:
    """Just enough to satisfy `gather_eos_token_ids`."""

    def __init__(self, eos_token_id: int | list[int] | None = None) -> None:
        self.eos_token_id = eos_token_id


class MockModel:
    def __init__(self, eos_token_id: int | list[int] | None = None) -> None:
        self.config = MockModelConfig(eos_token_id)


# ── Helpers ────────────────────────────────────────────────────────────────


def _make_log(**overrides) -> BatchLog:
    """Build a BatchLog with sensible defaults; tests override one field."""
    base = dict(
        model_id="test/mock-model",
        seed=42,
        temperature=0.0,
        top_p=1.0,
        max_new_tokens=10,
        task_prompts={"t1": "hi"},
        steps=[BatchStep(step_index=0, active_task_ids=("t1",))],
        eos_token_ids=(50256,),
        padding_side="left",
        pad_token_id=0,
        sampling_params_extra={},
        max_prompt_tokens=None,
        task_prompt_token_counts={"t1": 2},  # default tokenize gives 1 token per char
    )
    base.update(overrides)
    return BatchLog(**base)


# ── §2.9 row 1 — EOS set ────────────────────────────────────────────────────


def test_gather_eos_token_ids_dedupes_tokenizer_and_model_config():
    """EOS surfaces both from tokenizer and model.config; result is sorted + deduped."""
    tok = MockTokenizer(eos_token_id=128009)
    model = MockModel(eos_token_id=[128001, 128009])  # Llama-3-style multi-EOS
    got = gather_eos_token_ids(tok, model)
    assert got == (128001, 128009), got


def test_gather_eos_token_ids_handles_missing_pieces():
    """Tokenizer-only or model-only or both-None must not crash."""
    assert gather_eos_token_ids(MockTokenizer(eos_token_id=None), None) == ()
    assert gather_eos_token_ids(MockTokenizer(eos_token_id=7), None) == (7,)
    assert gather_eos_token_ids(MockTokenizer(eos_token_id=None), MockModel(eos_token_id=9)) == (9,)


def test_eos_set_mismatch_raises():
    """Replayer's tokenizer surfaces a different EOS set than the log → reject."""
    log = _make_log(eos_token_ids=(50256,))
    replayer_tok = MockTokenizer(eos_token_id=128009)  # different model entirely
    with pytest.raises(ValueError, match="§2.9 row 1: EOS set mismatch"):
        validate_log_against_tokenizer(log, replayer_tok)


def test_eos_set_empty_in_log_is_skipped():
    """Legacy logs that didn't capture EOS must still validate cleanly."""
    log = _make_log(eos_token_ids=())
    replayer_tok = MockTokenizer(eos_token_id=128009)
    # Should not raise.
    validate_log_against_tokenizer(log, replayer_tok)


# ── §2.9 row 2 — padding contract ────────────────────────────────────────────


def test_padding_side_mismatch_raises():
    log = _make_log(padding_side="left")
    replayer_tok = MockTokenizer(padding_side="right")
    with pytest.raises(ValueError, match="§2.9 row 2: padding_side mismatch"):
        validate_log_against_tokenizer(log, replayer_tok)


def test_pad_token_id_mismatch_raises():
    log = _make_log(pad_token_id=0)
    replayer_tok = MockTokenizer(pad_token_id=999)
    with pytest.raises(ValueError, match="§2.9 row 2: pad_token_id mismatch"):
        validate_log_against_tokenizer(log, replayer_tok)


def test_padding_match_passes():
    """Sanity: identical config does NOT raise."""
    log = _make_log()
    validate_log_against_tokenizer(log, MockTokenizer())


# ── §2.9 row 3 — sampler extras ──────────────────────────────────────────────


def test_sampling_extras_mismatch_raises():
    """A new sampler param on Replayer side that Worker did not record → reject."""
    log = _make_log(sampling_params_extra={})  # Worker had no extras
    with pytest.raises(ValueError, match="§2.9 row 3: sampling_params_extra mismatch"):
        validate_log_against_tokenizer(
            log, MockTokenizer(),
            expected_sampling_extra={"top_k": 50},  # Replayer added one
        )


def test_sampling_extras_match_passes():
    log = _make_log(sampling_params_extra={"top_k": 50, "rep_penalty": 1.1})
    validate_log_against_tokenizer(
        log, MockTokenizer(),
        expected_sampling_extra={"top_k": 50, "rep_penalty": 1.1},
    )


def test_sampling_extras_default_to_empty():
    """Replayer with no extras configured + log with no extras = pass."""
    log = _make_log(sampling_params_extra={})
    validate_log_against_tokenizer(log, MockTokenizer())


# ── §2.9 row 4 — prompt token count + truncation ────────────────────────────


def test_token_count_mismatch_raises():
    """Worker tokenized 'hi' to 2 tokens; if Replayer tokenizer maps the
    same string to 5 tokens, the run is using a different tokenizer
    revision and the bit-exact assertion is doomed."""
    log = _make_log(
        task_prompts={"t1": "hi"},
        task_prompt_token_counts={"t1": 2},  # Worker's count
    )
    # Replayer tokenizer maps "hi" → 5 tokens (different revision).
    replayer_tok = MockTokenizer(tokenize={"hi": [1, 2, 3, 4, 5]})
    with pytest.raises(ValueError, match=r"§2.9 row 4: task 't1' prompt tokenizes to 5"):
        validate_per_task_token_counts(log, replayer_tok)


def test_token_count_match_passes():
    log = _make_log(
        task_prompts={"t1": "hello"},
        task_prompt_token_counts={"t1": 5},  # default tokenize: 1 char -> 1 tok
    )
    validate_per_task_token_counts(log, MockTokenizer())


def test_token_count_truncation_applied():
    """If Worker recorded max_prompt_tokens=3, Replayer must truncate
    its tokenization to 3 tokens before comparing length — even if
    its own tokenization of the prompt is longer than 3."""
    log = _make_log(
        task_prompts={"t1": "abcde"},  # default tokenize gives 5 tokens
        task_prompt_token_counts={"t1": 3},  # Worker capped at 3
        max_prompt_tokens=3,
    )
    validate_per_task_token_counts(log, MockTokenizer())


def test_token_count_missing_field_raises():
    """task_prompts has a task whose count is missing from the dict."""
    log = _make_log(
        task_prompts={"t1": "hi", "t2": "yo"},
        task_prompt_token_counts={"t1": 2},  # t2 missing
    )
    with pytest.raises(ValueError, match=r"§2.9 row 4: task 't2'.*missing"):
        validate_per_task_token_counts(log, MockTokenizer())


def test_legacy_log_with_empty_token_counts_is_skipped():
    """Backward compat: Worker that pre-dates §2.9 wrote {} for token
    counts; the validator no-ops rather than raising."""
    log = _make_log(task_prompt_token_counts={})
    validate_per_task_token_counts(log, MockTokenizer())
