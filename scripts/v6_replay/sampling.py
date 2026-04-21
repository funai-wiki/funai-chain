"""
ChaCha20-seeded deterministic sampling, per FunAI V5.2 §9.3.

Used by WorkerSimulator and ReplayEngine when ``temperature > 0``. Both
sides must produce identical token ids given identical logits + identical
seed + identical step index. Bit-exactness in logits (Phase 1a / 1c) plus
this deterministic sampling (Phase 1b) is what lets a Verifier reconstruct
a Worker's full generation trajectory, including the ChaCha20 PRNG state.

Spec-relevant invariants implemented here:

- All math in ``float32``. bf16 logits are upcast before sampling.
- Softmax accumulation strictly in ``token_id`` ascending order
  (``np.add.accumulate`` is documented left-to-right, matching the spec's
  "softmax accumulation order 0..vocab_size-1" requirement).
- Top-p filter applied post-softmax via stable descending sort; ties
  broken by token id (stable sort preserves input order).
- ChaCha20 keystream keyed on ``final_seed`` (32-byte SHA256 output);
  nonce derived from ``step_index`` (little-endian, zero counter prefix).
- Inverse-CDF sampling using a uniform float in [0, 1) drawn from
  4 bytes of keystream.

V5.2 derives ``final_seed`` as ``SHA256(user_seed || dispatch_block_hash
|| task_id)``. The PoC uses the simplified
``SHA256(worker_seed || task_id)`` since ``user_seed`` and
``dispatch_block_hash`` aren't threaded through the test harness.
Production V6 code will use the full V5.2 derivation.
"""

from __future__ import annotations

import hashlib

import numpy as np
from cryptography.hazmat.primitives.ciphers import Cipher, algorithms


def derive_final_seed(task_id: str, worker_seed: int) -> bytes:
    """Return a 32-byte seed keyed to ``(task_id, worker_seed)``.

    Collisions across tasks with different ids are SHA256-safe.
    """
    data = worker_seed.to_bytes(8, "little", signed=False) + task_id.encode("utf-8")
    return hashlib.sha256(data).digest()


def chacha20_uniform(final_seed: bytes, step_index: int) -> float:
    """
    Draw a single uniform float in [0, 1) from the ChaCha20 keystream.

    Key is ``final_seed`` (32 bytes). Nonce is a 16-byte value: the first
    4 bytes are the ChaCha20 counter (zero), the last 12 bytes encode
    ``step_index`` as little-endian unsigned integer.

    Drawing exactly 4 bytes of keystream and dividing by 2**32 gives a
    uniform float in [0, 1) with 32 bits of entropy — well beyond the
    vocabulary size (~150k ≈ 2**17.2), so inverse-CDF resolution is not
    a concern.
    """
    if step_index < 0:
        raise ValueError("step_index must be non-negative")
    nonce = b"\x00" * 4 + step_index.to_bytes(12, "little", signed=False)
    cipher = Cipher(algorithms.ChaCha20(final_seed, nonce), mode=None)
    keystream = cipher.encryptor().update(b"\x00" * 4)
    r_uint32 = int.from_bytes(keystream, "little")
    return r_uint32 / float(1 << 32)


def chacha20_sample(
    logits: np.ndarray,
    *,
    temperature: float,
    top_p: float,
    final_seed: bytes,
    step_index: int,
) -> int:
    """
    Deterministic top-p sampling keyed by ChaCha20.

    Args:
        logits: ``[vocab_size]`` array, any float dtype. Upcast to float32
            internally.
        temperature: must be > 0.
        top_p: nucleus threshold. Set to 1.0 to disable filtering.
        final_seed: 32-byte ChaCha20 key (see ``derive_final_seed``).
        step_index: decode step number. Feeds the ChaCha20 nonce so each
            step draws an independent uniform float.

    Returns:
        Sampled token id (int, in [0, vocab_size)).
    """
    if temperature <= 0:
        raise ValueError("temperature must be > 0 for chacha20_sample")
    if not (0.0 < top_p <= 1.0):
        raise ValueError("top_p must be in (0, 1]")

    # Upcast to float32 per V5.2 §9.3.
    logits_f32 = np.asarray(logits, dtype=np.float32, order="C").copy()

    # Temperature scaling.
    scaled = logits_f32 / np.float32(temperature)

    # Numerically stable softmax: subtract max before exp.
    max_logit = np.max(scaled)
    exp = np.exp(scaled - max_logit, dtype=np.float32)

    # Strict token_id ascending cumsum → normalization constant.
    cumsum_exp = np.add.accumulate(exp, dtype=np.float32)
    total = cumsum_exp[-1]
    probs = exp / total  # fp32 probs in token-id order

    # Top-p (nucleus) filter. Sort descending, cut at cumulative >= top_p,
    # renormalize. Stable sort so token-id order breaks ties deterministically.
    if top_p < 1.0:
        desc_idx = np.argsort(-probs, kind="stable")
        desc_probs = probs[desc_idx]
        desc_cumsum = np.add.accumulate(desc_probs, dtype=np.float32)
        # Keep tokens whose prefix cumsum <= top_p, plus the first one that
        # crosses the threshold. Ensure at least one token is kept.
        keep_desc = desc_cumsum <= np.float32(top_p)
        keep_desc[0] = True
        # Include the first token that pushes the cumulative sum strictly
        # above top_p; this is "include up to top_p" semantics.
        # The highest-prob token is always kept (index 0 in desc order).
        # If cumsum at index k crosses, we've already kept 0..k-1 and need k.
        # Find the first index where desc_cumsum > top_p and include it.
        over = np.where(desc_cumsum > np.float32(top_p))[0]
        if over.size > 0:
            keep_desc[over[0]] = True
        keep = np.zeros_like(probs, dtype=bool)
        keep[desc_idx[keep_desc]] = True
        # Zero out filtered tokens, renormalize.
        filtered = np.where(keep, probs, np.float32(0.0))
        renorm = np.add.accumulate(filtered, dtype=np.float32)[-1]
        probs = filtered / renorm

    # Build CDF in token_id ascending order.
    cdf = np.add.accumulate(probs, dtype=np.float32)

    # Inverse-CDF sampling using uniform r ∈ [0, 1).
    r = chacha20_uniform(final_seed, step_index)
    r_scaled = np.float32(r) * cdf[-1]  # multiply by cdf[-1] to handle fp renorm drift
    idx = int(np.searchsorted(cdf, r_scaled, side="left"))
    if idx >= len(cdf):
        idx = len(cdf) - 1
    return idx
