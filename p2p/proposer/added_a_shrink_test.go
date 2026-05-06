package proposer

// Tests for KT 30-case Added A — Proposer-side adaptive shrink.
//
// Pre-fix: const MaxBatchEntries was set from a block-gas estimate (40000)
// that ignored the binding constraint, CometBFT's mempool max-tx-bytes
// default of 1 MiB. The chain rejected over-sized batches; Proposer kept
// retrying the same payload forever.
//
// Post-fix: MaxBatchEntries lowered to 1300 (~1 MiB at 783 B/entry, see
// bench/bench_test.go::TestReportWireSize) AND dispatch detects
// chain.ErrTxTooLarge and calls Proposer.ShrinkBatchLimit, which halves
// the runtime ceiling further if an operator is running a tighter mempool.
// BuildBatch and CommitBatch both honor the runtime limit. The shrink is
// floored at MinBatchLimit so we don't shrink to absurd sizes.

import (
	"testing"

	settlementtypes "github.com/funai-wiki/funai-chain/x/settlement/types"
)

// newProposerWithClearedTasks constructs a Proposer pre-loaded with N
// SettlementEntry rows in clearedTasks — the queue that BuildBatch drains.
func newProposerWithClearedTasks(t *testing.T, n int) *Proposer {
	t.Helper()
	p := &Proposer{
		Address:       "test-proposer",
		pendingTasks:  make(map[string]*TaskEvidence),
		pendingAudits: make(map[string]*AuditEvidence),
	}
	p.clearedTasks = make([]settlementtypes.SettlementEntry, n)
	for i := 0; i < n; i++ {
		p.clearedTasks[i] = settlementtypes.SettlementEntry{
			TaskId: []byte("task-" + string(rune('a'+(i%26)))),
		}
	}
	return p
}

func TestProposer_GetBatchLimit_DefaultsToConstWhenUnset(t *testing.T) {
	p := newProposerWithClearedTasks(t, 0)
	if got := p.GetBatchLimit(); got != MaxBatchEntries {
		t.Fatalf("default limit must be MaxBatchEntries (%d), got %d", MaxBatchEntries, got)
	}
}

func TestProposer_SetBatchLimit_FlooredAtMin(t *testing.T) {
	p := newProposerWithClearedTasks(t, 0)

	p.SetBatchLimit(1) // below MinBatchLimit
	if got := p.GetBatchLimit(); got != MinBatchLimit {
		t.Fatalf("SetBatchLimit(1) must floor at MinBatchLimit (%d), got %d", MinBatchLimit, got)
	}

	p.SetBatchLimit(0) // 0 → reset to default
	if got := p.GetBatchLimit(); got != MaxBatchEntries {
		t.Fatalf("SetBatchLimit(0) must reset to default (%d), got %d", MaxBatchEntries, got)
	}

	p.SetBatchLimit(-5) // negative → reset to default
	if got := p.GetBatchLimit(); got != MaxBatchEntries {
		t.Fatalf("SetBatchLimit(<0) must reset to default, got %d", got)
	}

	p.SetBatchLimit(500) // reasonable
	if got := p.GetBatchLimit(); got != 500 {
		t.Fatalf("SetBatchLimit(500) → 500, got %d", got)
	}
}

// TestProposer_ShrinkBatchLimit_HalvesUntilFloor: each ShrinkBatchLimit halves
// the current effective limit until reaching MinBatchLimit. Pinpoints the
// "log2(N) iterations" convergence the dispatch retry loop relies on.
func TestProposer_ShrinkBatchLimit_HalvesUntilFloor(t *testing.T) {
	p := newProposerWithClearedTasks(t, 0)

	// Sequence: 1300 → 650 → 325 → 162 → 81 → 64 (floor) → 64 (no further shrink)
	expected := []int{650, 325, 162, 81, MinBatchLimit, MinBatchLimit}
	for i, want := range expected {
		got := p.ShrinkBatchLimit()
		if got != want {
			t.Fatalf("step %d: ShrinkBatchLimit got %d, want %d", i, got, want)
		}
	}
}

// TestProposer_BuildBatch_HonorsRuntimeLimit: with 1000 cleared entries and
// a runtime limit of 250, BuildBatch must produce exactly 250 entries and
// CommitBatch must clear exactly 250 — leaving 750 for the next tick.
func TestProposer_BuildBatch_HonorsRuntimeLimit(t *testing.T) {
	p := newProposerWithClearedTasks(t, 1000)
	p.PrivKey = nil // use the unsigned-marker placeholder; signMerkleRoot returns "unsigned"
	p.SetBatchLimit(250)

	msg := p.BuildBatch()
	if msg == nil {
		t.Fatal("BuildBatch returned nil for non-empty queue")
	}
	if len(msg.Entries) != 250 {
		t.Fatalf("BuildBatch must respect runtime limit (250), got %d entries", len(msg.Entries))
	}

	p.CommitBatch()

	if got := p.ClearedCount(); got != 750 {
		t.Fatalf("after CommitBatch with limit=250, expected 750 left, got %d", got)
	}
}

// TestProposer_BuildBatch_ConvergesUnderShrink: simulate the dispatch retry
// loop — N too-large rejections shrink the limit; eventually the batch fits.
// Pin that the queue drains correctly across the shrink/commit cycles.
func TestProposer_BuildBatch_ConvergesUnderShrink(t *testing.T) {
	p := newProposerWithClearedTasks(t, 162)
	p.PrivKey = nil

	// Simulate 3 ShrinkBatchLimit calls (1300 → 650 → 325 → 162).
	for i := 0; i < 3; i++ {
		p.ShrinkBatchLimit()
	}
	if got := p.GetBatchLimit(); got != 162 {
		t.Fatalf("expected limit 162 after 3 shrinks, got %d", got)
	}

	// First "successful" tick: drain all 162 (limit happens to equal queue).
	msg := p.BuildBatch()
	if len(msg.Entries) != 162 {
		t.Fatalf("BuildBatch should produce 162, got %d", len(msg.Entries))
	}
	p.CommitBatch()
	if got := p.ClearedCount(); got != 0 {
		t.Fatalf("queue should be empty after full drain, got %d", got)
	}
}
