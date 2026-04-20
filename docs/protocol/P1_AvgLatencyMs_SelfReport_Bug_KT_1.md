# P1 Bug Fix: AvgLatencyMs Self-Report Vulnerability

| | |
|---|---|
| **Priority** | P1 — does not affect fund safety, but undermines fair competition |
| **Author** | KT |
| **Date** | 2026-04-20 |
| **Affected files** | `p2p/worker/worker.go`, `x/worker/keeper/keeper.go`, `x/settlement/keeper/keeper.go`, `x/vrf/types/vrf.go` |
| **Estimated change** | ~80 LOC + ~60 LOC of tests |

---

## 1. Problem statement

The `AvgLatencyMs` value currently stored on-chain is measured, signed, and self-reported
by the Worker. The chain cannot verify that number is truthful.

Code path:

`p2p/worker/worker.go:383` — the Worker measures its own inference duration:
```
inferMs := uint32(time.Since(inferStart).Milliseconds())
```

That value is embedded into `InferReceipt` and broadcast under the Worker's signature.

`x/worker/keeper/keeper.go:636-640` — the chain reads `inferMs` from the receipt and
updates the Worker's `AvgLatencyMs` via EMA:
```
w.AvgLatencyMs = (w.AvgLatencyMs*4 + latencyMs) / 5   // EMA 0.8/0.2
```

`x/vrf/types/vrf.go:129` — VRF ranking uses `AvgLatencyMs` directly:
```
latFactor := rankSpeedMultiplier(workers[i].AvgLatencyMs)
```

**Attack.** A malicious Worker hard-codes `inferMs = 50` (while its real latency may be
3000 ms). The on-chain `AvgLatencyMs` converges to an artificially low value →
`rankSpeedMultiplier` approaches 1.5 (maximum boost) → the Worker gains an unfair
advantage in dispatch ranking and steals orders from honest Workers.

**The signature does not defend against this.** A Receipt signature proves "this number
was reported by this Worker" but does not prove "this number is true". It is equivalent
to letting someone write their own performance review.

---

## 2. Fix: replace self-reported time with on-chain-observable time

**Core idea.** Anchor both ends of the measurement to events that can be observed on the
chain.

**Start.** The time at which the Worker accepts the task. This is recorded in
`SettlementEntry.AcceptedAt`, which the Proposer populates in `BuildBatch` from the
timestamp observed when the P2P `AcceptTask` message arrived. The Proposer is the
dispatching node, not the Worker itself — the Worker cannot tamper with this time.

**End.** The time at which the Proposer receives the `InferReceipt`. The Proposer
records the receipt arrival time in its `CollectVerificationResponse` stage and writes
it to `SettlementEntry.ReceiptReceivedAt`. This, too, is recorded by the Proposer and
is out of reach of Worker tampering.

**Latency computed on-chain:**
```
SettlementLatencyMs = ReceiptReceivedAt - AcceptedAt
```

This value includes inference time + P2P transit + signing overhead. It is slightly
noisier than pure inference time, but critically — the Worker cannot fabricate it.
Moreover, all Workers incur the same overhead, so relative ranking fairness is
unaffected.

---

## 3. Code changes

### 3.1 Add timestamp fields to `SettlementEntry`

`x/settlement/types/msgs.go` — extend the `SettlementEntry` struct:

```go
// New timestamp fields (proto tag = current max + 1)
AcceptedAtMs    uint64  `protobuf:"varint,XX,opt,name=accepted_at_ms,proto3"`
ReceiptAtMs     uint64  `protobuf:"varint,XX,opt,name=receipt_at_ms,proto3"`
```

### 3.2 Proposer populates the timestamps

`p2p/proposer/proposer.go` — when constructing the settlement entry in `BuildBatch()`:

```go
entry := SettlementEntry{
    // ... existing fields ...
    AcceptedAtMs:  task.AcceptedTimestampMs,   // timestamp when AcceptTask was observed
    ReceiptAtMs:   task.ReceiptReceivedAtMs,   // timestamp when receipt arrived at Proposer
}
```

`p2p/proposer/proposer.go` — record the receipt arrival time:

```go
func (p *Proposer) onReceiptReceived(receipt *InferReceipt) {
    task := p.pendingTasks[receipt.TaskID]
    task.ReceiptReceivedAtMs = uint64(time.Now().UnixMilli())
    // ... existing logic ...
}
```

### 3.3 Chain keeper uses on-chain time to compute latency

`x/settlement/keeper/keeper.go` — inside `ProcessBatchSettlement()`:

```go
// Replaces the old path that read inferMs from the receipt
if entry.ReceiptAtMs > entry.AcceptedAtMs {
    settlementLatencyMs := uint32(entry.ReceiptAtMs - entry.AcceptedAtMs)
    k.workerKeeper.UpdateAvgLatency(ctx, entry.WorkerAddress, settlementLatencyMs)
}
```

### 3.4 Worker keeper update logic unchanged

`x/worker/keeper/keeper.go` — `UpdateAvgLatency()`'s EMA logic stays the same. Only the
source of `latencyMs` changes: from the Worker-self-reported value in the receipt, to
the Proposer-recorded value on the settlement entry:

```go
// Before: latencyMs came from receipt.InferenceLatencyMs (self-reported by Worker)
// After:  latencyMs comes from entry.ReceiptAtMs - entry.AcceptedAtMs (recorded by Proposer)
func (k Keeper) UpdateAvgLatency(ctx sdk.Context, addr sdk.AccAddress, latencyMs uint32) {
    // EMA logic unchanged
    if w.AvgLatencyMs == 0 {
        w.AvgLatencyMs = latencyMs
    } else {
        w.AvgLatencyMs = (w.AvgLatencyMs*4 + latencyMs) / 5
    }
}
```

### 3.5 Keep the Worker-side `inferMs` field, but drop it from on-chain ranking

`p2p/worker/worker.go` — `inferMs` remains in `InferReceipt` but is demoted to
informational/reference data only. The chain no longer reads it to update
`AvgLatencyMs`. Retention avoids breaking receipt backward compatibility — older
receipts still deserialize cleanly.

---

## 4. Defending against a cheating Proposer

A natural question: shifting the clock from Worker to Proposer, won't the Proposer cheat?

The Proposer's incentives differ from the Worker's. A Worker under-reports latency to
win more orders. A Proposer has no motive to under-report latency on behalf of any
particular Worker — unless the Proposer and the Worker are colluding.

Defenses:

First, **cross-Proposer validation.** In the existing design, the Leader role rotates
(VRF-elected). Tasks from the same Worker are packaged by different Proposers over time.
If one Proposer consistently reports low latency for a particular Worker while other
Proposers report normal latency for the same Worker, the anomaly is on-chain-detectable.

Second, **physical floor check.** The chain tracks a minimum plausible latency per
`model_id` (via the `modelreg` module's statistics). Any latency below the physical
floor is rejected or flagged as suspicious. For example, an 8B model cannot produce
a 200-token completion in 10 ms on any hardware.

Third, **latency only affects dispatch priority, not fund safety.** In the worst case,
a Worker wins some extra orders by under-reporting — but they still must pass
verification to collect. If they are actually slow, users will feel the delay and the
reputation system will naturally penalize them.

---

## 5. Impact on the VRF ranking formula

The current `rankSpeedMultiplier` (`x/vrf/types/vrf.go`) uses a reference value of
3000 ms:
- `AvgLatencyMs < 3000 ms` → boost (up to 1.5×)
- `AvgLatencyMs > 3000 ms` → penalty (down to 0.1×)
- `AvgLatencyMs == 0` → neutral (1.0×)

Once the source switches to on-chain time, `AvgLatencyMs` will trend higher than before
(because it now includes P2P transit and signing overhead). The reference value may need
to be adjusted from 3000 ms to 4000–5000 ms, with the exact number tuned empirically
after real-hardware measurements.

VRF ranking is a relative ranking — every Worker carries the same added overhead, so
the relative order is preserved. Only the absolute value shifts; adjusting the reference
is sufficient.

---

## 6. Test plan

| Test | Description | Expected |
|------|-------------|----------|
| TestLatency_ProposerTimestamps | Proposer populates `AcceptedAtMs` and `ReceiptAtMs`; keeper computes the difference and updates `AvgLatencyMs` | PASS |
| TestLatency_ReceiptBeforeAccept | `ReceiptAtMs < AcceptedAtMs` (anomalous) → no update | PASS |
| TestLatency_WorkerSelfReportIgnored | The `inferMs` field inside the receipt no longer influences on-chain `AvgLatencyMs` | PASS |
| TestLatency_EMA_Convergence | After 10 tasks, `AvgLatencyMs` converges to the true mean | PASS |
| TestLatency_PhysicalFloor | Values below the per-`model_id` plausible floor are rejected | PASS |
| TestVRF_RankingWithNewLatency | Under the new on-chain latency values, VRF ranking correctly reflects relative speed | PASS |

---

## 7. Known limitations and open questions

The fix in §3 closes the direct Worker self-report channel, but has several open
items that must be resolved before implementation. They are called out here so the
implementer does not re-introduce attack surface while following §3 literally.

### 7.1 Delayed-`AcceptTask` attack — the primary residual attack

`AcceptTask` (`p2p/types/messages.go:271-276`) carries no timestamp field; the
`AcceptedAtMs` value in §3.2 must therefore be the Proposer's wall-clock observation
at the moment `AcceptTask` is received. This re-opens a Worker-controlled channel
for compressing the measured window:

> A malicious Worker receives `AssignTask`, begins inference immediately without
> acknowledging, waits until inference is ~100 ms from completion, then sends
> `AcceptTask`. The Proposer's `AcceptedAtMs` lands ~100 ms before the receipt, so
> `SettlementLatencyMs ≈ 100 ms` even though real inference was, say, 3000 ms.

**Implementation decision (this PR).** `AcceptedAtMs` is stamped when the
Proposer observes the Leader's `AssignTask` on the dispatch topic, *not* on
`AcceptTask`. The Worker has no ability to delay `AssignTask` — it is broadcast
by the Leader before the Worker even sees the task. This closes the compression
attack at its source.

Because every node running `p2p/dispatch.go` already routes `AssignTask` to
`handleAssignTask`, the only wiring change needed is a one-line
`n.Proposer.OnAssignTask(task.TaskId)` hook at the top of that handler. No
changes to the `AssignTask` wire format or `SigDigest` are required — a practical
improvement over the originally-discussed "Leader signs a dispatch timestamp"
option.

Trade-off: `AcceptedAtMs` is the Proposer's own wall-clock at dispatch
observation, not the Leader's. Leader→Proposer P2P transit therefore shortens
the measured interval slightly relative to Leader-internal timing, but this
offset is symmetric across all tasks seen by the same Proposer and cancels out
in relative VRF ranking. Inter-Proposer clock skew is the residual noise source
covered in §7.5.

**Earlier-discussed alternatives, not adopted:**

1. ~~Anchor `AcceptedAtMs` to a Leader-signed `AssignTask.DispatchTimestampMs`
   field.~~ Requires adding a field to `AssignTask` and updating `SigDigest`, for
   negligible benefit over Proposer-observation.
2. ~~Strictly enforce the 1-second Worker acceptance timeout on the
   Leader/Proposer side.~~ Weaker backstop; still allows ~1 second of compression
   per task, and is orthogonal to this fix — may still be worth implementing as a
   separate correctness improvement.

### 7.2 Leader-versus-Proposer observation point — resolved

In the current architecture, task dispatch happens at the **Leader** while batch
construction happens at the **Proposer**. These may be different nodes. §3.2's
original code snippet implied the Proposer directly observes `AcceptTask`, which
was not previously wired.

**Implementation decision (this PR).** The dispatch loop at
`p2p/dispatch.go:handleAssignTask` runs on every node, so the Proposer (wherever
it is) observes `AssignTask` alongside its other roles. A one-line
`n.Proposer.OnAssignTask(task.TaskId)` hook at the top of that handler stamps
`AcceptedAtMs`. No new P2P topic subscription, no new message type, no Leader
code change.

### 7.3 Cross-Proposer cross-validation is aspirational

§4's first defense ("other Proposers report the same Worker's normal latency, so
the chain can detect the anomaly") is a design intent — not currently implemented
in the keeper. To realize it, the chain would need per-Worker latency-distribution
state (e.g., recent-N-samples variance) and an anomaly rule above which a
particular Proposer's sample is rejected or quarantined. Scope as a follow-up
issue before relying on it.

### 7.4 Per-model physical floor is not yet a `x/modelreg` feature

§4's second defense (reject latencies below a per-`model_id` physical floor) assumes
`x/modelreg` tracks model-specific latency statistics. It does not today. Realizing
this requires:

- New state on the `Model` record: `MinObservedLatencyMs`, initialized either from
  model-proposal metadata or from the first N observed samples.
- A keeper hook called from the settlement path that rejects or flags samples
  below a configurable fraction of `MinObservedLatencyMs`.

Without these, §4's floor check is a documented intent only.

### 7.5 Cross-Proposer wall-clock skew

NTP-synced nodes typically drift <50 ms, which is <2 % of the 3 s latency scale
used by `rankSpeedMultiplier` and therefore acceptable noise. It should still be
acknowledged as a noise source so future investigations of "why did Worker X's
`AvgLatencyMs` jump" can rule it in or out quickly. If §7.2 option 2 is adopted
(Leader-signed timestamps), this source of noise is eliminated.

---

## 8. Migration plan

Pre-mainnet. No data migration needed — `AvgLatencyMs` defaults to 0 in genesis (new
nodes) and naturally accumulates via the new path once deployed.

The `InferenceLatencyMs` field on `InferReceipt` is retained but marked deprecated.
Future versions may remove it.

---

*End of document.*
