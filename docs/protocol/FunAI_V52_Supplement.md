# FunAI V5.2 Protocol Supplementary Specification

> Date: 2026-03-25
>
> Source: Economic model validation + AI companion product requirement back-derivation + code second verification cross-findings
>
> Corresponding baseline: FunAI V5.2 Final (commit 1da0b49)
>
> This document supplements V5.2 Final and does not replace the original specification. Changes are listed by priority, and each item notes its relationship to the original specification.

---

## 1. Change Overview

| # | Change | Priority | Type | Original Spec Status | Change Size |
|---|------|--------|------|-----------|--------|
| S1 | Worker concurrent inference | P1 | Performance | Original spec busy boolean → replaced with counter | ~70 lines |
| S2 | Separate counting for verification and inference | P1 | Performance | Original spec verifier selection excludes busy → changed to independent counters | ~20 lines |
| S3 | Add max_tokens to InferRequest | P2 | Feature | Original spec hardcoded 2048 → user-configurable | ~25 lines |
| S4 | Leader rotation state recovery | P2 | Stability | Not covered in original spec | ~40 lines |
| S5 | GPU memory protection | P2 | Stability | Not covered in original spec | ~10 lines |
| S6 | Leader decryption layer | P1 | Security | Original spec §19 privacy layer designed, implementation missing last link | ~30 lines |
| S7 | Multimodal task_type framework reservation | P3 | Extension | Not covered in original spec | ~30 lines |
| S8 | NSFW content tagging | P3 | Extension | Not covered in original spec | ~20 lines |
| S9 | per-token billing | P3 | Economic | Original spec per-request → future change to per-token | ~200 lines |

---

## 2. S1: Worker Concurrent Inference (P1)

### 2.1 Problem

V5.2 §6.2 Worker state is a boolean busy/not-busy. A single GPU can only process one inference task at a time. vLLM continuous batching can support 4-6 concurrent requests, but the protocol layer limits utilization.

Impact:

| Metric | Single task (current) | Concurrent (after change) |
|------|---------------|-------------|
| 4090 output throughput | ~35 tok/s | ~120 tok/s |
| Monthly capacity (70% utilization) | 63.5M tokens | 218M tokens |
| Break-even price (Malaysia $55/month) | $0.87/M | $0.25/M |
| vs Venice $0.90/M | No advantage | 3.6x cheaper |

### 2.2 Specification Changes

#### 2.2.1 New Fields in Worker Registration

New fields added to the Worker struct in the `x/worker` module:

```
Worker {
    // ... existing 20 fields ...
    max_concurrent_tasks   uint32   // field 21, inference concurrency limit, default 1, range [1, 32]
    max_concurrent_verify  uint32   // field 22, verification concurrency limit, default 2, range [1, 8]
}
```

Set via `MsgRegisterWorker` / `MsgUpdateWorker`, range validated on-chain. Default value of 1 ensures backward compatibility — old Workers that do not set this field behave unchanged.

Recommended values:

| GPU | Model | max_concurrent_tasks | max_concurrent_verify |
|-----|------|---------------------|-----------------------|
| 4090 24GB | 32B Q4 | 3-4 | 2 |
| A100 40GB | 32B Q4 | 6-8 | 3 |
| A100 80GB | 70B Q4 | 5-6 | 3 |

#### 2.2.2 Leader Dispatch Logic Changes

Replace the busy check in §6.2:

```
Original spec (§6.2):
  if busy_workers[worker.Address] { skip }

Changed to:
  if active_inference_tasks[worker.Address] >= worker.MaxConcurrentTasks { skip }
```

Leader maintains `active_inference_tasks map[string]uint32` replacing `busy_workers map[string]bool`. Worker notifies Leader to decrement the counter after completing inference.

#### 2.2.3 Worker Local Execution Changes

Worker switches to asynchronous execution after receiving AssignTask:

```go
func (w *Worker) HandleTask(ctx context.Context, task *AssignTask, ...) {
    if w.activeInferenceTasks.Load() >= w.maxConcurrentTasks {
        w.rejectTask(task)
        return
    }
    w.activeInferenceTasks.Add(1)
    go func() {
        defer w.activeInferenceTasks.Add(-1)
        // existing inference logic unchanged
        receipt := w.runInference(ctx, task)
        w.broadcastReceipt(receipt)
    }()
}
```

#### 2.2.4 Determinism Unaffected

Each task's sampling seed is independent:

```
final_seed = SHA256(user_seed || vrf_nonce || task_id)
```

This is unrelated to whether other tasks are running on the same GPU. vLLM's continuous batching independently maintains KV cache and sampling state for each request.

---

## 3. S2: Separate Counting for Verification and Inference (P1)

### 3.1 Problem

V5.2 §9.1 verifier selection excludes all busy Workers. Verification prefill takes only ~0.6 seconds, but because of the busy flag, Workers performing inference cannot serve as verifiers. Under high load, there are not enough verifiers → verification becomes congested.

### 3.2 Specification Changes

#### 3.2.1 Verifier Selection No Longer Excludes Workers Performing Inference

Replace the exclusion rule in §9.1:

```
Original spec (§9.1):
  Exclude the executing Worker itself. Exclude busy_workers.

Changed to:
  Exclude the executing Worker itself.
  Exclude Workers where active_verify_tasks >= max_concurrent_verify.
  Do not exclude Workers performing inference.
```

Leader maintains an independent `active_verify_tasks map[string]uint32`.

#### 3.2.2 Technical Basis for Parallel Verification and Inference

| Dimension | Inference | Verification prefill |
|------|------|-------------|
| GPU time | 4-8 seconds | ~0.6 seconds |
| KV cache | Required (continuously occupied) | Not required (single forward pass only) |
| Memory usage | 1.5-6 GB/request | 200-500 MB temporary |
| After completion | KV cache retained | All memory immediately released |

Verification prefill is inserted into idle gaps during inference decode (GPU utilization < 50% during decode phase), with < 100ms impact on inference latency.

#### 3.2.3 Effect

Under high load (80% utilization):

| Metric | Before | After |
|------|------|------|
| Available verifiers | 20 (idle ones) | 100 (all) |
| Verification concurrency | 6-7 tasks | 60+ tasks |
| Verification congestion | Severe | Non-existent |

#### 3.2.4 Supplementary Specification Text

Add the following note after §9 Verification Protocol:

> Verifier prefill is a lightweight GPU operation (~0.6 seconds, no sustained memory usage). Workers can simultaneously execute inference tasks and verification tasks. The two task types use independent concurrency counters and do not affect each other. Verifier selection does not exclude Workers performing inference; it only excludes Workers at full verification capacity (active_verify_tasks >= max_concurrent_verify).

---

## 4. S3: Add max_tokens to InferRequest (P2)

### 4.1 Problem

Currently the Worker inference output limit is hardcoded at 2048 tokens. Different scenarios require different lengths (casual chat 200, story writing 500, long document 2048). Users cannot control output length.

### 4.2 Relationship to Original Specification

V5.2 §23 already states:

> v1_supported_params = temperature only (subsequent versions will gradually add top-p, top-k, etc.)

temperature is fully implemented (InferRequest field, SignBytes coverage, Leader validation rejects > 20000, Worker/Verifier passthrough).

This supplement only adds max_tokens; top_p/top_k will not be added (pending V5.3 deterministic sampling extension).

### 4.3 Specification Changes

#### 4.3.1 New Field in InferRequest

```
InferRequest {
    // ... existing 9 fields ...
    max_tokens:  uint32   // field 10, maximum output token count, default 2048, range [1, 8192]
}
```

Included in SignBytes signature coverage.

#### 4.3.2 AssignTask Passthrough

```
AssignTask {
    // ... existing fields ...
    MaxTokens:  uint32   // passed through from InferRequest
}
```

#### 4.3.3 Leader Validation

```
if req.MaxTokens == 0 {
    req.MaxTokens = 2048  // default value, backward compatible
}
if req.MaxTokens > 8192 {
    reject("max_tokens exceeds 8192")
}
```

#### 4.3.4 Worker Execution

Worker uses `task.MaxTokens` instead of hardcoded 2048 when calling TGI:

```go
// Currently
w.Engine.Stream(ctx, task.Prompt, 2048, temperature, seed)

// Changed to
w.Engine.Stream(ctx, task.Prompt, task.MaxTokens, temperature, seed)
```

#### 4.3.5 Verifier Unaffected

Verifiers only perform teacher forcing (forward pass with complete output), so the max_tokens parameter is not needed.

### 4.4 §23 Parameter Table Update

```
| Parameter | Value |
|------|---|
| max_tokens_default | 2048 |
| max_tokens_max | 8192 |
| v1_supported_params | temperature + max_tokens (subsequent additions of top-p, top-k to follow) |
```

---

## 5. S4: Leader Rotation State Recovery (P2)

### 5.1 Problem

§6.2 has per-model Leader rotation every 30 seconds. The new Leader has no knowledge of active_tasks counters or requests in the mempool. At low TPS, SDK 5-second retransmission can cover this, but at high TPS (500+) there are 300 in-flight tasks.

### 5.2 Specification Changes

#### 5.2.1 First Layer: SDK Retransmission (Existing Mechanism, No Changes Needed)

SDK receives no token for 5 seconds → retransmits the same task_id → new Leader receives it → processes normally. task_id deduplication guarantees no duplicate execution. Maximum 5 seconds of delay.

#### 5.2.2 Second Layer: LeaderSync Request (New)

After taking office, the new Leader broadcasts a `LeaderSync` request to the model topic. Workers reply with their current state:

```
LeaderSyncResponse {
    worker_addr:             string
    active_inference_tasks:  uint32
    active_verify_tasks:     uint32
    current_task_ids:        []bytes32
}
```

New Leader collects Worker states within 1 second → rebuilds active_tasks counters.

Security: If a Worker misreports its state (e.g., active=0 to get more dispatches) → cannot complete them → timeout → jail. There is no economic incentive to misreport.

#### 5.2.3 Third Layer: VerifyResult Does Not Depend on a Specific Proposer Instance

In the current architecture, VerifyResult is collected by the Proposer, whose lifecycle is independent of the Leader. Leader rotation does not affect the Proposer's verification result collection.

However, if the Proposer process restarts, it needs to be able to re-subscribe and collect from the P2P network. The 30s rebroadcast mechanism for VerifyResult (already implemented) ensures new Proposer instances can catch up.

### 5.3 §6.2 Supplementary Text

> During Leader rotation, the new Leader broadcasts a LeaderSync request to collect all Workers' current states, rebuilding the active_tasks counters within 1 second. New requests arriving during rotation enter the mempool normally. In-flight tasks are covered by the SDK's 5-second timeout retransmission mechanism.

---

## 6. S5: GPU Memory Protection (P2)

### 6.1 Problem

4090 24GB running 32B Q4 (18GB) + 4 concurrent inference KV caches (~6GB) = 24GB at full capacity. Verification prefill needs ~200-500MB of temporary memory → potential OOM.

### 6.2 Specification Changes

#### 6.2.1 Miner Configuration Recommendations (Not a Protocol Change)

Add vLLM parameter recommendations to the miner documentation:

```bash
python -m vllm.entrypoints.openai.api_server \
    --model <model_path> \
    --gpu-memory-utilization 0.85 \     # default 0.90, changed to 0.85 to reserve memory for verification
    --max-num-seqs 4                     # max concurrency, corresponds to max_concurrent_tasks
    --enable-prefix-caching              # enable APC, improves performance for repeated prompts
```

#### 6.2.2 Verification Memory Insufficient Degradation (Protocol Change)

When a Worker detects insufficient GPU memory, it rejects the verification request instead of crashing with OOM:

```go
func (w *Worker) HandleVerification(req *VerifyRequest) {
    if !w.hasEnoughGPUMemory(PREFILL_MIN_MEMORY) {
        w.rejectVerification(req)  // Leader falls through to next verifier
        return
    }
    // Execute verification prefill normally
}
```

After rejection, the Leader falls through to the next VRF-ranked candidate. No OOM, no lost verification.

Note: CPU prefill fallback is not adopted. Floating-point precision differences exist between CPU and GPU (x87 80-bit extended precision vs GPU FP32), which may cause softmax/expf32 result inconsistencies → false verification FAIL. The protocol requires verifiers to use GPU computation at the same precision as the Worker.

### 6.3 §9 Supplementary Text

> Verifiers should check whether GPU memory is sufficient before accepting a verification request. When memory is insufficient, the verifier should reject the verification request, and the Leader falls through to the next VRF-ranked candidate. Verification prefill must be executed on GPU; CPU fallback is not permitted, to ensure floating-point precision (float32 softmax + Cephes expf32) consistency with the inference side.

---

## 7. S6: Leader Decryption Layer (P1)

### 7.1 Problem

V5.2 §19 defines a complete TLS privacy layer: SDK uses X25519 ECDH + AES-256-GCM to encrypt requests, Node broadcasts signed X25519 public keys via P2P key exchange.

Current code implementation status:
- SDK side: encryption, key exchange, signature verification — all complete ✅
- Node side: key generation, private key storage, signed key exchange — all complete ✅
- Leader side: **decryption after receiving encrypted messages — missing** ❌

Leader's HandleRequest directly performs json.Unmarshal on msg.Data. SDK encrypts and sends ciphertext → Leader json.Unmarshal fails → request is dropped. The last link in the TLS encryption chain is broken.

### 7.2 Specification Changes

#### 7.2.1 Node Initializes tlsTransport

Node uses the saved X25519 private key to initialize `tlsTransport` at startup:

```go
// Already exists in NewNode:
node.EncryptionPubkey = pub[:]
node.EncryptionPrivkey = priv[:]

// New addition:
node.TLSTransport, _ = privacy.NewTransport(privacy.ModeTLS,
    privacy.WithLocalKeys(node.EncryptionPrivkey, node.EncryptionPubkey))
```

#### 7.2.2 Add Decryption Layer to Message Handling

After receiving a model topic message, the Node attempts decryption before passing it to Leader.HandleRequest:

```go
func (n *Node) handleModelMessage(ctx context.Context, msg *pubsub.Message) {
    data := msg.Data

    // Attempt TLS decryption (compatible with plaintext clients)
    if n.TLSTransport != nil {
        if decrypted, err := n.TLSTransport.Unwrap(ctx, data); err == nil {
            data = decrypted
        }
        // Decryption failed → treat as plaintext (ModePlain client)
    }

    var req InferRequest
    if err := json.Unmarshal(data, &req); err != nil {
        return // Neither valid ciphertext nor valid plaintext → discard
    }

    leader.HandleRequest(ctx, &req, blockHash)
}
```

Backward compatible: ModePlain client sends plaintext JSON → Unwrap fails → treated as plaintext → works normally.

#### 7.2.3 Worker/Verifier Side Likewise

Worker does not need to decrypt when receiving AssignTask (containing Prompt) — Leader has already decrypted and passes plaintext in AssignTask. However, if Workers need to receive encrypted messages in the future, the same pattern should be reused.

### 7.3 §19 Supplementary Text

> P2P nodes (Leader/Worker) should first attempt TLS decryption using the local X25519 private key before processing received messages. If decryption succeeds, use the decrypted plaintext; if decryption fails, treat as plaintext (compatible with unencrypted clients). This ensures TLS encryption is transparent to users while not affecting ModePlain clients.

---

## 8. S7: Multimodal task_type Framework Reservation (P3)

### 8.1 Background

V5.2's verification mechanism is based on LLM logits comparison. In the future, support for image generation (SDXL), video generation, TTS, and STT will be needed — these models' outputs are not logits.

### 8.2 Specification Changes

New task_type field added to InferRequest:

```
TaskType enum (uint8):
    TEXT_GENERATION   = 0   // LLM (existing, default value)
    IMAGE_GENERATION  = 1   // SDXL / Flux (future)
    VIDEO_GENERATION  = 2   // LTX Video / Wan (future)
    TEXT_TO_SPEECH    = 3   // TTS (future)
    SPEECH_TO_TEXT    = 4   // STT (future)

InferRequest {
    // ... existing fields + S3's max_tokens ...
    task_type:  uint8   // default 0, included in signature
}
```

Current version only implements `TEXT_GENERATION` (behavior unchanged). Requests with other task_type values → reject. Framework reservation; verification logic dispatches by task_type.

Preset verification methods for each task_type:

| task_type | Verification Method | Notes |
|-----------|---------|---------|
| TEXT_GENERATION | logits 5-position comparison < epsilon (existing) | — |
| IMAGE_GENERATION | Deterministic seed + perceptual hash comparison | Different GPU FP16 precision differences → need perceptual hash, not exact hash |
| TEXT_TO_SPEECH | Output audio perceptual hash | Determinism depends on same model + speaker_id |
| SPEECH_TO_TEXT | Output text exact match | Highest determinism |
| VIDEO_GENERATION | To be designed | Video models are not fully deterministic, requires dedicated approach |

model_id computation method differs by task_type, with unified bytes32 format. ModelReg module annotates task_type at registration.

---

## 9. S8: NSFW Content Tagging (P3)

### 9.1 Principle

The protocol does not censor content and does not reject any requests. Content tagging allows miners to autonomously choose whether to accept specific types of requests.

### 9.2 Specification Changes

```
ContentTag enum (uint8):
    TAG_GENERAL   = 0   // default, general content
    TAG_NSFW      = 1   // adult content
    TAG_UNTAGGED  = 2   // user does not tag

InferRequest {
    // ... existing fields ...
    content_tag:  uint8   // default 0, included in signature
}

Worker {
    // ... existing fields ...
    accepted_tags:  []uint8  // field 23, declares accepted tags, default [0,1,2] (accept all)
}
```

Leader matches during dispatch:

```
if request.content_tag not in worker.accepted_tags { skip }
```

Most miners declare accept-all → all requests are served normally. Compliance-minded miners accept only `TAG_GENERAL` → self-compliance. No centralized censorship.

---

## 10. S9: per-token Billing (P3 — Future Version)

### 10.1 Background

V5.2 fee is a per-request fixed value. The LLM industry standard is per-token billing. per-request leads to: users overpay for short responses, miners have insufficient profit for long responses, and miners lack information when accepting tasks.

### 10.2 Why P3

per-token billing involves extensive changes:

| Change Point | Complexity |
|--------|--------|
| InferRequest field restructuring (fee → fee_per_input/output + max_fee) | Medium |
| Add input/output_token_count to InferReceipt | Low |
| Rewrite BatchSettlement settlement logic (bill by actual tokens) | High |
| Verifier confirms token count (three-party cross-verification sub-protocol) | High |
| Leader balance check changed to max_fee pre-deduction + actual refund of difference | Medium |
| SDK auto-pricing (query network average price) | Medium |
| FAIL scenario fee calculation (by completed token count or simplified approach) | Medium |

Estimated total change size 150-200 lines, requires third-verificationing settlement security.

### 10.3 Phased Approach

**Phase 1 (current, done together with S3):**
- Add max_tokens to InferRequest → miners can estimate workload from this
- fee remains per-request → users set fee based on max_tokens × estimated unit price
- SDK provides helper function: `EstimateFee(maxTokens, modelId) → suggestedFee`

**Phase 2 (V5.3 or subsequent version):**
- Complete per-token billing
- InferRequest changed to fee_per_input_token + fee_per_output_token + max_fee
- Settlement calculated by actual token count × unit price
- Three-party cross-verification of token count (Worker reports + verifier confirms + SDK verifies)

### 10.4 Final InferRequest Structure for per-token Billing (Phase 2)

```
InferRequest {
    // existing fields
    model_id:              bytes32
    prompt_hash:           bytes32
    expire_block:          uint64
    temperature:           uint16      // already implemented
    timestamp:             uint64      // already implemented
    user_seed:             bytes32     // already implemented
    user_pubkey:           bytes33     // already implemented

    // S3 new addition
    max_tokens:            uint32

    // S7 new addition
    task_type:             uint8

    // S8 new addition
    content_tag:           uint8

    // Phase 2 per-token billing (replaces existing fee field)
    fee_per_input_token:   uint64      // ufai/token
    fee_per_output_token:  uint64      // ufai/token
    max_fee:               uint64      // maximum the user is willing to pay

    // Signature
    user_signature:        bytes65     // covers all fields above

    // Non-signature fields
    prompt:                string      // plaintext prompt (not included in signature, protected by prompt_hash)
    cache_hint:            bytes32     // optional, prompt prefix hash, accelerates APC
}

InferReceipt {
    // existing fields
    task_id:               bytes32
    worker_pubkey:         bytes33
    result_hash:           bytes32
    final_seed:            bytes32
    worker_logits:         [5]float32
    worker_signature:      bytes65
    sampled_tokens:        [5]uint32

    // Phase 2 new additions
    input_token_count:     uint32
    output_token_count:    uint32
}
```

---

## 11. KV Cache Optimization (Non-Protocol Change)

### 11.1 Background

AI companion requests have prompts of 2000-4000 tokens, of which 90% overlaps with the previous request (system prompt + memory + history).

### 11.2 Why Not preferred_worker

Considered adding a preferred_worker field to InferRequest, letting the Leader preferentially dispatch to the previous Worker (higher KV cache hit rate).

Reason for rejection — block reward farming attack:

```
V5.2 reward formula: w_i = 0.8 × (fee_i / sum_fee) + 0.2 × (count_i / sum_count)

Attacker runs own Worker + self-sends large volume of low-fee requests + preferred_worker points to self
→ bypasses VRF ranking → farms count → farms 20% weight of block rewards
```

VRF ranking's unmanipulability is a security cornerstone; we do not open a loophole for KV cache optimization.

### 11.3 Four-Layer Out-of-Protocol Optimization

**First layer: vLLM Automatic Prefix Caching (miner configuration)**

Miners enable `--enable-prefix-caching` when starting vLLM. All users of the same character share the system prompt KV cache. Zero protocol changes.

Note: VRF random dispatch means the same user's requests may go to different Workers, reducing APC hit rate. However, in the AI companion scenario, most Workers run the same model + popular characters' system prompts are highly repetitive → cross-user APC remains effective.

**Second layer: SDK prompt structure optimization**

SDK arranges prompt by stability: character system prompt (most stable) → core memory summary → conversation history → user's new message. The most stable content goes first → highest APC hit rate.

**Third layer: SDK prompt compression**

When conversation history exceeds 20 turns, automatic compression: most recent 5 turns preserved in full (~500 tokens), older turns replaced with summary (~200 tokens). Input drops from 2900 to ~1500 tokens → 50% input cost savings.

**Fourth layer: cache_hint field (optional)**

InferRequest non-signature field `cache_hint: bytes32` (prompt prefix hash). Worker's vLLM uses this to accelerate APC lookup. Leader does not process it, only passes it through. Does not affect ranking or signature verification.

---

## 12. Implementation Priority and Dependencies

```
Dependencies:

S1 (concurrent inference) → S2 (verification separation) depends on S1's counter structure
S3 (max_tokens) → independent
S4 (Leader rotation) → depends on S1's counter structure
S5 (memory protection) → depends on S2's verification handling logic
S6 (Leader decryption) → independent
S7 (multimodal) → independent
S8 (NSFW tagging) → independent
S9 (per-token) → recommended after S3

Implementation order:

  First batch (P1, must be done before FunAI chain mainnet):
    S1 + S2: concurrent inference + verification separation → ~90 lines
    S6: Leader decryption layer → ~30 lines

  Second batch (P2, 1-2 months after mainnet):
    S3: max_tokens → ~25 lines
    S4: Leader rotation recovery → ~40 lines
    S5: memory protection → ~10 lines

  Third batch (P3, scheduled by demand):
    S7: multimodal framework → ~30 lines
    S8: NSFW tagging → ~20 lines
    S9: per-token billing → ~200 lines (separate version)
```

---

## 13. §23 Parameter Table Supplement

| Parameter | Value | Source |
|------|---|------|
| max_concurrent_tasks_default | 1 | S1 |
| max_concurrent_tasks_range | [1, 32] | S1 |
| max_concurrent_verify_default | 2 | S2 |
| max_concurrent_verify_range | [1, 8] | S2 |
| max_tokens_default | 2048 | S3 |
| max_tokens_max | 8192 | S3 |
| leader_sync_timeout | 1 second | S4 |
| prefill_min_memory_mb | 512 | S5 |
| v1_supported_params | temperature + max_tokens | S3 (updated) |

---

## 14. Comprehensive Economic Impact

| Metric | Current (V5.2 original) | After S1+S2 fix | After all fixes |
|------|-------------------|-------------|-----------|
| 4090 throughput | 35 tok/s | 120 tok/s | 120 tok/s |
| Monthly capacity | 63.5M tokens | 218M tokens | 218M tokens |
| Break-even price (Malaysia) | $0.87/M | $0.25/M | $0.25/M |
| vs Venice $0.90/M | No advantage | 3.6x cheaper | 3.6x cheaper |
| Miner monthly profit (Venice price parity) | ~$11 | ~$201 | ~$201 |
| High-load verification latency | Minutes-level congestion | Seconds-level | Seconds-level |
| Leader switch interruption | 5 seconds (SDK retransmission) | 5 seconds | 1 second (LeaderSync) |
| TLS encryption | Unavailable | Unavailable | Fully available (S6) |

S1+S2 are critical to economic viability — without concurrent inference, miner profit is nearly zero, and the network cannot attract external miners to join.

---

*Document version: V1*
*Date: 2026-03-25*
*Baseline: FunAI V5.2 Final + commit 1da0b49*
