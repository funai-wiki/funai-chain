"""
Shared utilities for WorkerSimulator and ReplayEngine.

Centralizes:
- Determinism setup (the exact incantation to pass Phase 1a)
- Model / tokenizer loading with the same flags on both sides

Any divergence between Worker and Replayer on these flags is a determinism
leak and Phase 1a's ``max_abs_err == 0.0`` assertion will fail — so they
must live in one place.
"""

from __future__ import annotations

import os

import numpy as np
import torch
import transformers
from transformers import AutoModelForCausalLM, AutoTokenizer

# KT v2 §2.5 — identity of the inference engine used here. These are
# embedded in every BatchLog the Worker emits and checked by the Replayer;
# a mismatch is a hard error, not a warning, because at the PoC level we
# have no on-chain model_id registry to cross-validate against.
ENGINE_ID = "transformers"
ATTENTION_IMPL = "eager"


def engine_version() -> str:
    """Return the transformers version string (e.g. '4.57.6')."""
    return transformers.__version__


def configure_determinism(seed: int) -> None:
    """Apply the PyTorch / CUDA / cuDNN flags needed for bit-exact runs.

    References:
    - https://pytorch.org/docs/stable/notes/randomness.html
    - ``torch.use_deterministic_algorithms`` — raises at call site if a
      non-deterministic op is hit, which is the failure mode we want: find
      out at Phase 1a time, not Phase 2 time.
    """
    os.environ.setdefault("CUBLAS_WORKSPACE_CONFIG", ":4096:8")
    torch.use_deterministic_algorithms(True, warn_only=False)
    torch.backends.cudnn.deterministic = True
    torch.backends.cudnn.benchmark = False
    torch.backends.cuda.matmul.allow_tf32 = False
    torch.backends.cudnn.allow_tf32 = False
    torch.manual_seed(seed)
    if torch.cuda.is_available():
        torch.cuda.manual_seed_all(seed)
    np.random.seed(seed)


def load_model_and_tokenizer(model_id: str, device: str):
    """
    Load model in bfloat16 eager-attention eval mode; tokenizer with left padding.

    dtype choice — bfloat16:
        Qwen2.5 is trained in bfloat16. Running it as float16 triggers NaNs
        in attention softmax on some prompt / batch patterns because fp16's
        max representable value (~65504) is easy to overflow in attention
        logits. bfloat16 shares fp32's exponent range, so no overflow, at
        the cost of reduced mantissa precision — which is the right
        tradeoff for determinism work where we want the model to produce
        valid (non-NaN) outputs regardless of prompt.

    attn_implementation=eager:
        Disables SDPA fused backends, which on some hardware pick
        non-deterministic kernels depending on batch size. Slower than
        SDPA but the determinism floor for Phase 1a.
    """
    tokenizer = AutoTokenizer.from_pretrained(model_id)
    if tokenizer.pad_token is None:
        tokenizer.pad_token = tokenizer.eos_token
    tokenizer.padding_side = "left"

    model = AutoModelForCausalLM.from_pretrained(
        model_id,
        torch_dtype=torch.bfloat16,
        attn_implementation="eager",
    ).to(device)
    model.eval()
    for p in model.parameters():
        p.requires_grad_(False)
    return model, tokenizer


def is_moe_model(model) -> bool:
    """Return True if the loaded model is a Mixture-of-Experts.

    Uses a duck-typed check that does not import any model-family-specific
    classes: the model config carries either ``num_experts`` (Mixtral,
    DeepSeek-MoE, Qwen-MoE) or one of the known top-k attribute names. Any
    of these implies the forward pass takes the MoE branch and we can ask
    for ``output_router_logits=True``. Returns False for dense Qwen2.5,
    Llama 3, Mistral 7B etc. so the existing Phase 1a tests are unaffected.
    """
    cfg = getattr(model, "config", None)
    if cfg is None:
        return False
    for attr in ("num_local_experts", "num_experts", "n_routed_experts"):
        n = getattr(cfg, attr, None)
        if isinstance(n, int) and n > 1:
            return True
    return False


def moe_top_k(model) -> int:
    """Top-k experts the model selects per token. Falls back to 2.

    Mixtral defaults k=2, Qwen2.5-MoE k=4, DeepSeek-MoE k=6. Reading from
    config when present keeps the captured ``expert_routing`` array
    correctly shaped per family.
    """
    cfg = getattr(model, "config", None)
    if cfg is None:
        return 2
    for attr in ("num_experts_per_tok", "num_routed_experts_per_tok", "moe_top_k"):
        k = getattr(cfg, attr, None)
        if isinstance(k, int) and k > 0:
            return k
    return 2


def extract_top_k_experts_per_step(router_logits, top_k: int) -> list[list[list[int]]]:
    """Convert a tuple of router logits (one per MoE layer) into per-token
    top-k expert IDs.

    Args:
      router_logits: tuple[Tensor], length = number of MoE-enabled decoder
                     layers. Each tensor has shape (batch, seq_len, num_experts)
                     OR (batch * seq_len, num_experts) depending on the
                     transformers version. Both shapes are handled.
      top_k: number of experts to keep per token.

    Returns:
      A list of shape ``[batch_position][layer] -> list[int]`` (length
      ``top_k`` each). The caller decides which batch position corresponds
      to which task. For decode steps the seq_len dim is 1, so the inner
      list represents this single step's routing per layer.
    """
    if not router_logits:
        return []

    import torch

    # Normalise every layer tensor to shape (B, S, E).
    normalised = []
    batch_size = None
    seq_len = None
    for t in router_logits:
        if t.dim() == 2:
            # (B*S, E) — we cannot recover B without external info; assume
            # B is fixed across the call (set on the first 3-D tensor we
            # see, else 1). For decode-step calls B*S == B (S=1).
            normalised.append(t)
        elif t.dim() == 3:
            normalised.append(t)
            batch_size = t.shape[0]
            seq_len = t.shape[1]
        else:
            raise ValueError(f"unexpected router_logits dim {t.dim()}; want 2 or 3")

    if batch_size is None:
        # All 2-D. Caller is expected to pass batch_size separately for
        # this case; fall back to treating the whole tensor as one row.
        batch_size = normalised[0].shape[0]
        seq_len = 1

    # Result[b][layer] = list[int] of length top_k (last position only —
    # decode steps care about the freshly-produced token).
    result: list[list[list[int]]] = []
    for b in range(batch_size):
        per_layer = []
        for layer_t in normalised:
            if layer_t.dim() == 3:
                # (B, S, E) — take the last position (the just-decoded token).
                vec = layer_t[b, -1, :]
            else:
                # (B*S, E) — assume row b in flattened layout corresponds to
                # batch position b at seq_len=1 (decode step).
                vec = layer_t[b, :]
            top = torch.topk(vec, k=top_k, dim=-1).indices.tolist()
            per_layer.append([int(x) for x in top])
        result.append(per_layer)
    return result
