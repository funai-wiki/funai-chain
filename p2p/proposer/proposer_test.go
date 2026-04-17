package proposer

import (
	"context"
	"testing"

	settlementtypes "github.com/funai-wiki/funai-chain/x/settlement/types"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

// TestBatchLoop_EmptyBatch verifies BuildBatch returns nil when no pending tasks exist.
// E19: doBatchSettlement should return early without broadcasting.
func TestBatchLoop_EmptyBatch(t *testing.T) {
	p := New("funai1test", nil, nil, 100, 100)

	// No tasks added — BuildBatch must return nil
	msg := p.BuildBatch()
	if msg != nil {
		t.Fatal("BuildBatch should return nil when no cleared tasks")
	}

	// ProcessPending with no tasks should return 0 processed, 0 audits
	processed, audits := p.ProcessPending(context.Background(), []byte("blockhash"))
	if processed != 0 {
		t.Fatalf("expected 0 processed, got %d", processed)
	}
	if len(audits) != 0 {
		t.Fatalf("expected 0 audits, got %d", len(audits))
	}

	// BuildBatch still nil after empty ProcessPending
	msg = p.BuildBatch()
	if msg != nil {
		t.Fatal("BuildBatch should still return nil after empty ProcessPending")
	}
}

// TestBatchLoop_BroadcastFail verifies that entries are retained in the Proposer
// when the caller does NOT call CommitBatch (simulating broadcast failure).
// E20: Previously BuildBatch cleared entries immediately, causing data loss.
func TestBatchLoop_BroadcastFail(t *testing.T) {
	p := New("funai1proposer", nil, nil, 0, 100) // auditRate=0 → no audit VRF

	// Manually inject a cleared entry (simulating ProcessPending result)
	entry := settlementtypes.SettlementEntry{
		TaskId:        []byte("task-001"),
		UserAddress:   "funai1user",
		WorkerAddress: "funai1worker",
		Fee:           sdk.NewCoin("ufai", math.NewInt(1000000)),
		Status:        settlementtypes.SettlementSuccess,
	}
	p.mu.Lock()
	p.clearedTasks = append(p.clearedTasks, entry)
	p.mu.Unlock()

	// First BuildBatch — should return the msg
	msg := p.BuildBatch()
	if msg == nil {
		t.Fatal("BuildBatch should return msg with 1 entry")
	}
	if len(msg.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(msg.Entries))
	}

	// Simulate broadcast failure: do NOT call CommitBatch()

	// Second BuildBatch — entries should still be there (retained for retry)
	msg2 := p.BuildBatch()
	if msg2 == nil {
		t.Fatal("entries lost after BuildBatch without CommitBatch — broadcast failure would lose data")
	}
	if len(msg2.Entries) != 1 {
		t.Fatalf("expected 1 entry on retry, got %d", len(msg2.Entries))
	}

	// Now simulate success: call CommitBatch
	p.CommitBatch()

	// Third BuildBatch — should be nil (committed)
	msg3 := p.BuildBatch()
	if msg3 != nil {
		t.Fatal("BuildBatch should return nil after CommitBatch")
	}
}
