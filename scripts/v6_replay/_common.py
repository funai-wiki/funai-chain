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
from transformers import AutoModelForCausalLM, AutoTokenizer


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
