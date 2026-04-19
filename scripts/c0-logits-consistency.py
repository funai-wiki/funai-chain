#!/usr/bin/env python3
# c0-logits-consistency.py — FunAI Test Plan KT §1.3 C0 check.
#
# Purpose
#   Detect whether TGI's continuous batching perturbs per-token logits for a
#   prompt vs running the same prompt single-shot. If it does, the Verifier's
#   teacher-forcing path (always single-request) will never match a Worker's
#   batched path, and the verification architecture must change.
#
# What the script does
#   1. Sends prompt A once, standalone (single-request path).
#   2. Sends prompt A alongside N-1 distractor prompts concurrently
#      (continuous-batching path).
#   3. Extracts per-position `top_n_tokens` logprobs for prompt A from both
#      runs and diffs them.
#   4. Reports max_abs_err, max_rel_err, top-k membership drift, and a
#      PASS / INVESTIGATE / FAIL verdict per the Test Plan thresholds.
#
# Verdict (per KT §1.3 C0)
#   max_rel_err < 1e-6  → PASS          exit 0  (continue remaining tests)
#   max_rel_err < 1e-3  → INVESTIGATE   exit 1  (Verifier may need single-request mode)
#   max_rel_err ≥ 1e-3  → FAIL          exit 2  (verification architecture must change)
#   any error           → ERROR         exit 3
#
# Requirements
#   - TGI already running and reachable at --endpoint (use scripts/tgi-bootstrap-aliyun.sh).
#   - TGI built with top_n_tokens ≥ N enabled (default 5). If disabled, the
#     script prints the exact flag to add.
#   - Python 3.8+. Standard library only — no pip install needed.
#
# Usage
#   python3 scripts/c0-logits-consistency.py --endpoint http://<tgi-host>:8080
#
# Env / flag overrides
#   --endpoint          TGI base URL (default http://localhost:8080)
#   --seed              PRNG seed for prompt A (default 42)
#   --temperature       Sampling temperature (default 0.7 — matches Test Plan)
#   --max-new-tokens    Number of generated positions to compare (default 5)
#   --concurrent-count  Total concurrent requests in batch run (default 4)
#   --top-n             Top-N tokens captured per position (default 5)
#   --output-dir        Where to save raw JSON responses (default ./results/c0)
#   --timeout           HTTP timeout seconds per request (default 120)
#   --verbose           Print per-position diffs

from __future__ import annotations

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path
from typing import Any

# ── Test Plan §1.3 C0 thresholds ───────────────────────────────────────────────
THRESHOLD_PASS         = 1e-6
THRESHOLD_INVESTIGATE  = 1e-3

# Deterministic distractor prompts. Distinct lengths & content so TGI cannot
# collapse them into a shared prefix cache.
DISTRACTOR_PROMPTS = [
    "Write the number seventeen as digits.",
    "List three primary colors separated by commas.",
    "Name the largest planet in our solar system in one word.",
    "Reply with exactly the word 'hello' and nothing else.",
    "What is the square root of nine? Answer with only the digit.",
    "Give the chemical symbol for gold.",
    "Translate 'good morning' to French, lower case.",
]

PROMPT_A = "What is the capital of France? Answer with just the city name."

# ── Colors (TTY only) ──────────────────────────────────────────────────────────
def _c(code: str, s: str) -> str:
    if sys.stdout.isatty():
        return f"\033[{code}m{s}\033[0m"
    return s

def info(msg: str) -> None:    print(_c("0;34", "[INFO]"), msg)
def ok(msg: str) -> None:      print(_c("0;32", "[ OK ]"), msg)
def warn(msg: str) -> None:    print(_c("1;33", "[WARN]"), msg)
def fail(msg: str) -> None:    print(_c("0;31", "[FAIL]"), msg)
def phase(msg: str) -> None:   print("\n" + _c("0;36", f"══ {msg} ══") + "\n")

# ── TGI client (stdlib only) ───────────────────────────────────────────────────
class TGIError(RuntimeError):
    pass

def tgi_get(endpoint: str, path: str, timeout: float) -> dict[str, Any]:
    url = endpoint.rstrip("/") + path
    req = urllib.request.Request(url, method="GET")
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return json.loads(r.read())
    except urllib.error.URLError as e:
        raise TGIError(f"GET {url} failed: {e}") from e

def tgi_generate(
    endpoint: str,
    prompt: str,
    seed: int,
    temperature: float,
    max_new_tokens: int,
    top_n: int,
    timeout: float,
) -> dict[str, Any]:
    url = endpoint.rstrip("/") + "/generate"
    body = {
        "inputs": prompt,
        "parameters": {
            "max_new_tokens": max_new_tokens,
            "seed": seed,
            "temperature": temperature,
            "details": True,
            "decoder_input_details": False,
            "top_n_tokens": top_n,
            # do_sample is inferred from temperature > 0 in TGI, but make it
            # explicit so reproducibility does not depend on server defaults.
            "do_sample": True,
        },
    }
    data = json.dumps(body).encode()
    req = urllib.request.Request(
        url,
        method="POST",
        data=data,
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as r:
            return json.loads(r.read())
    except urllib.error.HTTPError as e:
        err_body = e.read().decode(errors="replace")
        if "top_n_tokens" in err_body.lower():
            raise TGIError(
                "TGI rejected top_n_tokens. Restart TGI with:\n"
                f"  --max-top-n-tokens {top_n}\n"
                "(Add it to the `docker run ... tgi` args in tgi-bootstrap-aliyun.sh.)\n"
                f"Raw error: {err_body[:500]}"
            ) from e
        raise TGIError(f"HTTP {e.code} on {url}: {err_body[:500]}") from e
    except urllib.error.URLError as e:
        raise TGIError(f"POST {url} failed: {e}") from e

# ── C0 core ───────────────────────────────────────────────────────────────────
def extract_positions(resp: dict[str, Any]) -> list[dict[str, Any]]:
    """Pull the per-position snapshots we need for the diff.

    Each returned dict: {
        "sampled_id":        int,
        "sampled_logprob":   float,
        "top": {token_id:int -> logprob:float},  # length = top_n_tokens
    }
    """
    details = resp.get("details")
    if not details:
        raise TGIError(
            "TGI response missing `details`. Is `parameters.details=true` supported? "
            "Check TGI version; KT fixes it to 3.3.6."
        )
    out = []
    for tok in details.get("tokens", []):
        top_map: dict[int, float] = {}
        for t in tok.get("top_tokens", []) or []:
            top_map[int(t["id"])] = float(t["logprob"])
        out.append(
            {
                "sampled_id": int(tok["id"]),
                "sampled_logprob": float(tok["logprob"]),
                "top": top_map,
            }
        )
    if not out:
        raise TGIError("details.tokens is empty — cannot compare positions.")
    return out

def run_single(endpoint: str, args: argparse.Namespace) -> dict[str, Any]:
    phase("Single-request run (Verifier path)")
    info(f"Prompt A, seed={args.seed}, temperature={args.temperature}")
    resp = tgi_generate(
        endpoint,
        PROMPT_A,
        args.seed,
        args.temperature,
        args.max_new_tokens,
        args.top_n,
        args.timeout,
    )
    ok(f"generated_text: {resp.get('generated_text', '')[:80]!r}")
    return resp

def run_batch(endpoint: str, args: argparse.Namespace) -> dict[str, Any]:
    phase(f"Concurrent-batch run ({args.concurrent_count} parallel — Worker path)")

    # prompt A + (N-1) distractors, each with its own seed to avoid collisions
    if args.concurrent_count < 2:
        raise ValueError("--concurrent-count must be ≥ 2 (1 target + ≥1 distractors)")
    n_distractors = args.concurrent_count - 1
    distractors = DISTRACTOR_PROMPTS[:n_distractors]
    if len(distractors) < n_distractors:
        raise ValueError(
            f"Only {len(DISTRACTOR_PROMPTS)} distractor prompts available; "
            f"--concurrent-count ≤ {len(DISTRACTOR_PROMPTS) + 1}"
        )

    prompts = [("A", PROMPT_A, args.seed)] + [
        (f"D{i+1}", p, args.seed + 1000 + i) for i, p in enumerate(distractors)
    ]
    info(f"Submitting {len(prompts)} requests concurrently")

    start = time.time()
    target_resp: dict[str, Any] | None = None
    # All threads submit at t≈0 so TGI packs them into the same micro-batch.
    with ThreadPoolExecutor(max_workers=len(prompts)) as pool:
        future_to_tag = {
            pool.submit(
                tgi_generate,
                endpoint,
                p,
                s,
                args.temperature,
                args.max_new_tokens,
                args.top_n,
                args.timeout,
            ): tag
            for (tag, p, s) in prompts
        }
        for fut in as_completed(future_to_tag):
            tag = future_to_tag[fut]
            resp = fut.result()
            if tag == "A":
                target_resp = resp
            ok(
                f"{tag}: {resp.get('generated_text', '')[:40]!r} "
                f"({resp.get('details', {}).get('generated_tokens')} tokens)"
            )
    elapsed = time.time() - start
    info(f"Batch completed in {elapsed:.2f}s")
    if target_resp is None:
        raise TGIError("Prompt A never returned in the batch run.")
    return target_resp

def compare(
    single: list[dict[str, Any]],
    batch: list[dict[str, Any]],
    verbose: bool,
) -> dict[str, Any]:
    phase("Diff: single vs batch (prompt A)")

    if len(single) != len(batch):
        warn(
            f"position count differs — single={len(single)}, batch={len(batch)}. "
            "Truncating to min."
        )
    n_pos = min(len(single), len(batch))

    max_abs_err = 0.0
    max_rel_err = 0.0
    top_membership_drift = 0
    sampled_id_drift = 0
    positions_compared = 0
    per_position_max_rel: list[float] = []

    for i in range(n_pos):
        s = single[i]
        b = batch[i]

        if s["sampled_id"] != b["sampled_id"]:
            sampled_id_drift += 1
            if verbose:
                warn(
                    f"pos {i}: sampled_id differs  single={s['sampled_id']}  "
                    f"batch={b['sampled_id']}"
                )

        shared = set(s["top"]) & set(b["top"])
        union = set(s["top"]) | set(b["top"])
        drift_at_i = len(union) - len(shared)
        top_membership_drift += drift_at_i

        pos_max_rel = 0.0
        for tok_id in shared:
            la = s["top"][tok_id]
            lb = b["top"][tok_id]
            abs_err = abs(la - lb)
            rel_err = abs_err / (abs(la) + 1e-10)
            max_abs_err = max(max_abs_err, abs_err)
            pos_max_rel = max(pos_max_rel, rel_err)
            max_rel_err = max(max_rel_err, rel_err)
            positions_compared += 1

        per_position_max_rel.append(pos_max_rel)
        if verbose:
            info(
                f"pos {i}: shared_top={len(shared)}/{len(union)}  "
                f"max_rel_err={pos_max_rel:.2e}"
            )

    return {
        "positions": n_pos,
        "samples_compared": positions_compared,
        "max_abs_err": max_abs_err,
        "max_rel_err": max_rel_err,
        "sampled_id_drift": sampled_id_drift,
        "top_membership_drift": top_membership_drift,
        "per_position_max_rel": per_position_max_rel,
    }

def verdict(stats: dict[str, Any]) -> tuple[str, int]:
    rel = stats["max_rel_err"]
    if rel < THRESHOLD_PASS and stats["top_membership_drift"] == 0 and stats["sampled_id_drift"] == 0:
        return ("PASS", 0)
    if rel < THRESHOLD_INVESTIGATE:
        return ("INVESTIGATE", 1)
    return ("FAIL", 2)

def save_artifact(path: Path, payload: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, indent=2))

# ── Main ──────────────────────────────────────────────────────────────────────
def main() -> int:
    ap = argparse.ArgumentParser(
        description="FunAI Test Plan KT §1.3 C0 — TGI batching logits consistency check.",
    )
    ap.add_argument("--endpoint", default=os.environ.get("TGI_ENDPOINT", "http://localhost:8080"))
    ap.add_argument("--seed", type=int, default=42)
    ap.add_argument("--temperature", type=float, default=0.7)
    ap.add_argument("--max-new-tokens", type=int, default=5)
    ap.add_argument("--concurrent-count", type=int, default=4)
    ap.add_argument("--top-n", type=int, default=5)
    ap.add_argument("--output-dir", default="./results/c0")
    ap.add_argument("--timeout", type=float, default=120.0)
    ap.add_argument("--verbose", action="store_true")
    args = ap.parse_args()

    phase("C0: Concurrent batching impact on logits")
    info(f"TGI endpoint: {args.endpoint}")

    try:
        info("Probing /health …")
        # /health returns plain text on TGI; urllib success == healthy.
        url = args.endpoint.rstrip("/") + "/health"
        urllib.request.urlopen(url, timeout=args.timeout).read()
        ok("TGI is healthy")

        try:
            i = tgi_get(args.endpoint, "/info", args.timeout)
            info(f"Model: {i.get('model_id', '?')}   TGI version: {i.get('version', '?')}")
        except TGIError as e:
            warn(f"/info lookup skipped: {e}")

        single_resp = run_single(args.endpoint, args)
        batch_resp  = run_batch(args.endpoint, args)

        single_positions = extract_positions(single_resp)
        batch_positions  = extract_positions(batch_resp)

        out = Path(args.output_dir)
        save_artifact(out / "single_response.json", single_resp)
        save_artifact(out / "batch_response.json",  batch_resp)
        info(f"Raw responses saved under {out}/")

        stats = compare(single_positions, batch_positions, args.verbose)
        result, exit_code = verdict(stats)

        phase("Result")
        print(f"  Positions compared:     {stats['positions']}")
        print(f"  top-N samples compared: {stats['samples_compared']}")
        print(f"  Max absolute error:     {stats['max_abs_err']:.2e}")
        print(f"  Max relative error:     {stats['max_rel_err']:.2e}")
        print(f"  Top-k membership drift: {stats['top_membership_drift']}")
        print(f"  Sampled-id drift:       {stats['sampled_id_drift']}")
        print(f"  Thresholds (KT §1.3):   PASS < {THRESHOLD_PASS:.0e}   "
              f"INVESTIGATE < {THRESHOLD_INVESTIGATE:.0e}")
        print()
        banner = {
            "PASS":        _c("0;32", "  PASS         → continue with C1-C4."),
            "INVESTIGATE": _c("1;33", "  INVESTIGATE  → Verifier may need single-request mode (KT §1.3 C0 Option A/B)."),
            "FAIL":        _c("0;31", "  FAIL         → STOP. Verification architecture must change (KT §1.3 C0 Options A-C)."),
        }[result]
        print(banner)
        print()

        save_artifact(
            out / "verdict.json",
            {
                "result": result,
                "stats": {k: v for k, v in stats.items() if k != "per_position_max_rel"},
                "per_position_max_rel": stats["per_position_max_rel"],
                "thresholds": {"pass": THRESHOLD_PASS, "investigate": THRESHOLD_INVESTIGATE},
                "config": {
                    "endpoint": args.endpoint,
                    "seed": args.seed,
                    "temperature": args.temperature,
                    "max_new_tokens": args.max_new_tokens,
                    "concurrent_count": args.concurrent_count,
                    "top_n": args.top_n,
                },
            },
        )
        return exit_code

    except TGIError as e:
        fail(str(e))
        return 3
    except KeyboardInterrupt:
        fail("Interrupted.")
        return 3

if __name__ == "__main__":
    sys.exit(main())
