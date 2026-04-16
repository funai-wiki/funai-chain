# Client SDK

The FunAI Client SDK is a drop-in replacement for OpenAI and Claude APIs. It handles privacy, streaming, and model management so that applications can switch to decentralized inference with minimal code changes. The SDK lives in [`sdk/`](../sdk/) and operates across three layers: privacy, streaming, and model management.

Sources: [SDK & OpenClaw Integration Spec](../docs/integration/FunAI_SDK_OpenClaw_Integration_Spec.md), [SDK README](../sdk/README.md), [SDK Developer Guide](../docs/guides/SDK_Developer_Guide.md)

> Note: The SDK spec moved from `docs/FunAI_SDK_OpenClaw_Integration_Spec.md` to `docs/integration/` during the docs restructure.

---

## Initialization Modes

| Mode | Method | Use case |
|------|--------|----------|
| Mode A | Wallet file | Direct on-chain interaction; user holds private key |
| Mode B | Gateway API key | Custodial gateway manages keys; simpler integration |

---

## Model Alias System

The SDK resolves human-readable model aliases to on-chain `model_id` hashes from the [model registry](model-registry.md).

### Alias Rules

- Non-empty, 3--64 characters
- Lowercase alphanumeric plus hyphen (`a-z`, `0-9`, `-`)
- Globally unique and immutable once registered

### Examples

| Alias | Description |
|-------|-------------|
| `qwen3-32b-q4` | Qwen3 32B, 4-bit quantization |
| `llama3.3-70b-q4` | Llama 3.3 70B, 4-bit quantization |

The SDK caches alias-to-model_id mappings locally to avoid repeated on-chain lookups.

---

## Messages Format

The SDK converts OpenAI-style `messages` arrays into a single prompt string using chat templates:

| Template | Format |
|----------|--------|
| `chatml` | Default. `<\|im_start\|>role\n...<\|im_end\|>` |
| `llama3` | Llama 3 native chat format |

---

## Function Calling

The SDK enables tool use by injecting tool definitions into the system prompt and parsing `tool_call` JSON from model output.

| Metric | Target |
|--------|--------|
| First-try parse success | > 95% |
| Parse success with retry | > 99% |
| Max retries on parse failure | 2 |

---

## JSON Mode

### Phase 1 (Current)

SDK-side prompt injection with fallback extraction:

1. Inject JSON schema instructions into the prompt
2. On non-JSON output, run 3 fallback extractors (regex, bracket matching, LLM re-ask)
3. Up to 3 total attempts before returning an error

### Phase 2 (Future)

Miner-side guided decoding via vLLM outlines -- not yet implemented.

---

## Streaming

OpenAI-compatible Server-Sent Events (SSE). The SDK monitors token flow and triggers re-dispatch if no token arrives within **5 seconds** (resends the same `task_id`).

---

## Auto-Pricing

The SDK queries the on-chain average fee for the target model and applies a **10% premium** (multiplier 1.1) as the default `max_fee`. This can be overridden by the caller. See [per-token billing](per-token-billing.md) for detailed fee mechanics.

---

## Error Codes

| Code | Name | HTTP Status |
|------|------|-------------|
| `insufficient_balance` | User balance too low for requested inference | 402 |
| `model_not_found` | Alias or model_id does not exist in registry | 404 |
| `request_timeout` | Inference did not complete within deadline | 408 |
| `fee_too_low` | Offered fee below minimum Worker threshold | 422 |
| `no_available_worker` | No Workers online for the requested model | 503 |

---

## Retry Logic

| Condition | Action |
|-----------|--------|
| 5s with no token during streaming | Resend same `task_id` |
| Function calling parse failure | Retry up to 2 times |
| JSON mode extraction failure | Retry up to 3 attempts |
| `fee_too_low` error | Retry with 1.5x fee multiplier |

---

## SDK Internal Flow

The SDK processes each request through 10 sequential steps:

1. Resolve model alias to `model_id` (cached)
2. Convert `messages` array to prompt string via chat template
3. Inject function calling / JSON mode instructions (if applicable)
4. Estimate fee via auto-pricing or use caller-provided `max_fee`
5. Sign request with user private key (Mode A) or gateway credential (Mode B)
6. Submit to [P2P layer](p2p-layer.md) leader for dispatch
7. Stream SSE tokens back to caller
8. Parse function calls / JSON from output (if applicable)
9. Retry on failure (per retry logic above)
10. Wrap final output in OpenAI-compatible response format

---

## Privacy

The SDK includes built-in privacy protections in [`sdk/privacy/`](../sdk/privacy/):

| Feature | Description |
|---------|-------------|
| PII scrubbing | Regex-based removal of sensitive patterns including email, phone numbers, Chinese national ID, and Chinese phone numbers |
| Tor routing | Optional onion routing for request anonymization |
| TLS encryption | End-to-end encryption for all P2P communication (X25519 ECDH + AES-256-GCM) |

Privacy modes: `plain`, `tls`, `tor`, `full` (Tor + TLS). See [SDK Developer Guide](../docs/guides/SDK_Developer_Guide.md#privacy-modes) for details.

---

## Related Pages

- [SDK Developer Guide](../docs/guides/SDK_Developer_Guide.md) — Full API reference with code examples
- [Per-Token Billing](per-token-billing.md)
- [Architecture](architecture.md)
- [P2P Layer](p2p-layer.md)
