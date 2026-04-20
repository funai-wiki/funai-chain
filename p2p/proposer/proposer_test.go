package proposer

import (
	"context"
	"encoding/hex"
	"testing"
	"time"

	"github.com/cometbft/cometbft/crypto/secp256k1"

	p2ptypes "github.com/funai-wiki/funai-chain/p2p/types"
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

// ── D2 (issue #9): audit-batch build / commit / retry-on-failure ─────────────

func makeAuditResponse(taskId []byte, verifierPriv secp256k1.PrivKey, pass bool) p2ptypes.SecondVerificationResponse {
	resp := p2ptypes.SecondVerificationResponse{
		TaskId:               taskId,
		Pass:                 pass,
		SecondVerifierAddr:   verifierPriv.PubKey().Bytes(),
		LogitsHash:           []byte("logits-hash"),
		VerifiedInputTokens:  10,
		VerifiedOutputTokens: 20,
	}
	sig, err := verifierPriv.Sign(resp.SignBytes())
	if err != nil {
		panic("sign audit response: " + err.Error())
	}
	resp.Signature = sig
	return resp
}

// TestAuditBatch_EmptyBeforeCollection: no ready audits → BuildAuditBatch nil.
func TestAuditBatch_EmptyBeforeCollection(t *testing.T) {
	p := New("funai1test", nil, nil, 100, 100)
	if msg := p.BuildAuditBatch(); msg != nil {
		t.Fatal("BuildAuditBatch must return nil when nothing is queued")
	}
	if n := p.ReadyAuditCount(); n != 0 {
		t.Fatalf("ReadyAuditCount: want 0, got %d", n)
	}
}

// TestAuditBatch_NoProposerAddressNoBuild: even with ready audits, a
// proposer with no address cannot sign the on-chain tx — BuildAuditBatch
// must return nil rather than emit a batch with an empty Proposer.
func TestAuditBatch_NoProposerAddressNoBuild(t *testing.T) {
	p := New("", nil, nil, 100, 100) // no address
	taskId := []byte("t1")
	v1, v2, v3 := secp256k1.GenPrivKey(), secp256k1.GenPrivKey(), secp256k1.GenPrivKey()
	for _, v := range []secp256k1.PrivKey{v1, v2, v3} {
		resp := makeAuditResponse(taskId, v, true)
		p.CollectSecondVerificationResponse(&resp)
	}
	if n := p.ReadyAuditCount(); n != 1 {
		t.Fatalf("ReadyAuditCount after 3 responses: want 1, got %d", n)
	}
	if msg := p.BuildAuditBatch(); msg != nil {
		t.Fatal("BuildAuditBatch must return nil when proposer address is unset")
	}
}

// TestAuditBatch_ThreeResponsesMoveToReady: after 3 responses for the same
// task, the audit is queued; BuildAuditBatch returns exactly those 3 entries.
func TestAuditBatch_ThreeResponsesMoveToReady(t *testing.T) {
	p := New("funai1test", nil, nil, 100, 100)
	taskId := []byte("task-ready-1")
	v1, v2, v3 := secp256k1.GenPrivKey(), secp256k1.GenPrivKey(), secp256k1.GenPrivKey()

	r1 := makeAuditResponse(taskId, v1, true)
	complete, _ := p.CollectSecondVerificationResponse(&r1)
	if complete {
		t.Fatal("1st response must not complete")
	}
	r2 := makeAuditResponse(taskId, v2, true)
	complete, _ = p.CollectSecondVerificationResponse(&r2)
	if complete {
		t.Fatal("2nd response must not complete")
	}
	r3 := makeAuditResponse(taskId, v3, false)
	complete, pass := p.CollectSecondVerificationResponse(&r3)
	if !complete {
		t.Fatal("3rd response must complete")
	}
	if !pass {
		t.Fatal("2 PASS + 1 FAIL with threshold 2 must aggregate to pass=true")
	}

	if n := p.ReadyAuditCount(); n != 1 {
		t.Fatalf("ReadyAuditCount: want 1, got %d", n)
	}

	msg := p.BuildAuditBatch()
	if msg == nil {
		t.Fatal("BuildAuditBatch must return a msg after 3 responses")
	}
	if len(msg.Entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(msg.Entries))
	}
	// Each entry must carry forward the verifier's P2P signature.
	for i, e := range msg.Entries {
		if len(e.Signature) == 0 {
			t.Fatalf("entry %d: missing Signature (P2P sig must propagate)", i)
		}
	}
}

// TestAuditBatch_BuildThenCommitClears: successful broadcast → CommitAuditBatch
// drains the ready queue; next BuildAuditBatch returns nil.
func TestAuditBatch_BuildThenCommitClears(t *testing.T) {
	p := New("funai1test", nil, nil, 100, 100)
	taskId := []byte("task-commit-1")
	v1, v2, v3 := secp256k1.GenPrivKey(), secp256k1.GenPrivKey(), secp256k1.GenPrivKey()
	for _, v := range []secp256k1.PrivKey{v1, v2, v3} {
		resp := makeAuditResponse(taskId, v, true)
		p.CollectSecondVerificationResponse(&resp)
	}

	if p.BuildAuditBatch() == nil {
		t.Fatal("expected non-nil batch")
	}
	p.CommitAuditBatch()

	if n := p.ReadyAuditCount(); n != 0 {
		t.Fatalf("ReadyAuditCount after commit: want 0, got %d", n)
	}
	if msg := p.BuildAuditBatch(); msg != nil {
		t.Fatal("BuildAuditBatch must return nil after CommitAuditBatch")
	}
}

// TestAuditBatch_BuildWithoutCommitAllowsRetry: if the chain broadcast
// fails, the Commit call is skipped — subsequent BuildAuditBatch must
// still return the same entries so the next tick can retry.
func TestAuditBatch_BuildWithoutCommitAllowsRetry(t *testing.T) {
	p := New("funai1test", nil, nil, 100, 100)
	taskId := []byte("task-retry-1")
	v1, v2, v3 := secp256k1.GenPrivKey(), secp256k1.GenPrivKey(), secp256k1.GenPrivKey()
	for _, v := range []secp256k1.PrivKey{v1, v2, v3} {
		resp := makeAuditResponse(taskId, v, true)
		p.CollectSecondVerificationResponse(&resp)
	}

	msg1 := p.BuildAuditBatch()
	if msg1 == nil || len(msg1.Entries) != 3 {
		t.Fatalf("first BuildAuditBatch must return 3 entries, got %v", msg1)
	}
	// Simulate broadcast failure: do NOT call CommitAuditBatch.
	msg2 := p.BuildAuditBatch()
	if msg2 == nil || len(msg2.Entries) != 3 {
		t.Fatalf("retry BuildAuditBatch must re-return the same 3 entries, got %v", msg2)
	}
}

// TestAuditBatch_MultipleTasksDrainTogether: audits from multiple tasks
// batch into a single msg, preserving FIFO order across tasks.
func TestAuditBatch_MultipleTasksDrainTogether(t *testing.T) {
	p := New("funai1test", nil, nil, 100, 100)
	t1 := []byte("task-multi-1")
	t2 := []byte("task-multi-2")

	for _, task := range [][]byte{t1, t2} {
		for i := 0; i < 3; i++ {
			v := secp256k1.GenPrivKey()
			resp := makeAuditResponse(task, v, true)
			p.CollectSecondVerificationResponse(&resp)
		}
	}

	if n := p.ReadyAuditCount(); n != 2 {
		t.Fatalf("ReadyAuditCount: want 2 tasks, got %d", n)
	}
	msg := p.BuildAuditBatch()
	if msg == nil {
		t.Fatal("BuildAuditBatch nil when 2 tasks are ready")
	}
	if len(msg.Entries) != 6 {
		t.Fatalf("want 6 entries (2 tasks × 3 verifiers), got %d", len(msg.Entries))
	}
	// First 3 entries from t1, next 3 from t2 (FIFO).
	for i := 0; i < 3; i++ {
		if string(msg.Entries[i].TaskId) != string(t1) {
			t.Fatalf("entry %d: want TaskId=%s, got %s", i, t1, msg.Entries[i].TaskId)
		}
	}
	for i := 3; i < 6; i++ {
		if string(msg.Entries[i].TaskId) != string(t2) {
			t.Fatalf("entry %d: want TaskId=%s, got %s", i, t2, msg.Entries[i].TaskId)
		}
	}

	p.CommitAuditBatch()
	if n := p.ReadyAuditCount(); n != 0 {
		t.Fatalf("ReadyAuditCount after commit: want 0, got %d", n)
	}
}

// ── P1: AvgLatencyMs fix — Proposer-observed timestamps ──────────────────────

// makeVerifyResultsForTest returns 3 Pass=true results so ProcessPending will
// follow the normal settlement path and emit a cleared entry.
func makeVerifyResultsForTest(taskId []byte) []*p2ptypes.VerifyResult {
	results := make([]*p2ptypes.VerifyResult, 3)
	for i := range results {
		v := secp256k1.GenPrivKey()
		results[i] = &p2ptypes.VerifyResult{
			TaskId:       taskId,
			VerifierAddr: v.PubKey().Bytes(),
			Pass:         true,
		}
	}
	return results
}

// TestOnAssignTask_RecordsFirstWallClock verifies that OnAssignTask stamps
// AcceptedAtMs on first observation and keeps the earliest value on repeat
// calls (relevant when Leader re-broadcasts on failover).
//
// docs/protocol/P1_AvgLatencyMs_SelfReport_Bug_KT_1.md.
func TestOnAssignTask_RecordsFirstWallClock(t *testing.T) {
	p := New("funai1test", nil, nil, 0, 100)
	taskId := []byte("task-p1-assign")

	before := uint64(time.Now().UnixMilli())
	p.OnAssignTask(taskId)
	after := uint64(time.Now().UnixMilli())

	key := hex.EncodeToString(taskId)
	p.mu.Lock()
	ev, ok := p.pendingTasks[key]
	firstStamp := ev.AcceptedAtMs
	p.mu.Unlock()
	if !ok {
		t.Fatal("expected pending task entry after OnAssignTask")
	}
	if firstStamp < before || firstStamp > after {
		t.Fatalf("AcceptedAtMs %d not within [%d, %d]", firstStamp, before, after)
	}

	time.Sleep(5 * time.Millisecond) // ensure wall-clock advances
	p.OnAssignTask(taskId)
	p.mu.Lock()
	second := p.pendingTasks[key].AcceptedAtMs
	p.mu.Unlock()
	if second != firstStamp {
		t.Fatalf("AcceptedAtMs must not advance on repeat OnAssignTask: first=%d, second=%d", firstStamp, second)
	}
}

// TestProcessPending_LatencyFromProposerTimestamps verifies the happy path:
// LatencyMs is computed from Proposer-observed anchors, and the raw timestamps
// are preserved on the entry for on-chain visibility.
func TestProcessPending_LatencyFromProposerTimestamps(t *testing.T) {
	p := New("funai1test", nil, nil, 0, 100) // auditRate=0 → always settle

	taskId := []byte("task-p1-happy")
	workerPriv := secp256k1.GenPrivKey()

	receipt := &p2ptypes.InferReceipt{
		TaskId:             taskId,
		WorkerPubkey:       workerPriv.PubKey().Bytes(),
		ResultHash:         []byte("result-hash"),
		InferenceLatencyMs: 9999, // attacker's inflated claim — must be ignored
	}

	acceptedAt := uint64(time.Now().UnixMilli())
	receivedAt := acceptedAt + 200

	key := hex.EncodeToString(taskId)
	p.mu.Lock()
	p.pendingTasks[key] = &TaskEvidence{
		Receipt:      receipt,
		Verifiers:    makeVerifyResultsForTest(taskId),
		AcceptedAtMs: acceptedAt,
		ReceivedAt:   receivedAt,
	}
	p.mu.Unlock()

	processed, _ := p.ProcessPending(context.Background(), []byte("blockhash"))
	if processed != 1 {
		t.Fatalf("expected 1 processed, got %d", processed)
	}

	msg := p.BuildBatch()
	if msg == nil || len(msg.Entries) != 1 {
		t.Fatalf("expected 1 entry in batch, got %v", msg)
	}
	entry := msg.Entries[0]
	if entry.LatencyMs != 200 {
		t.Fatalf("LatencyMs: expected 200 (ReceivedAt - AcceptedAtMs), got %d", entry.LatencyMs)
	}
	if entry.AcceptedAtMs != acceptedAt {
		t.Fatalf("AcceptedAtMs: expected %d, got %d", acceptedAt, entry.AcceptedAtMs)
	}
	if entry.ReceiptAtMs != receivedAt {
		t.Fatalf("ReceiptAtMs: expected %d, got %d", receivedAt, entry.ReceiptAtMs)
	}
}

// TestProcessPending_LatencyZeroOnReversedTimestamps ensures that a ReceiptAt
// earlier than AcceptedAt (anomalous; shouldn't happen in practice) does not
// produce a nonsense latency. Raw timestamps are still preserved for forensics.
func TestProcessPending_LatencyZeroOnReversedTimestamps(t *testing.T) {
	p := New("funai1test", nil, nil, 0, 100)

	taskId := []byte("task-p1-reversed")
	workerPriv := secp256k1.GenPrivKey()

	receipt := &p2ptypes.InferReceipt{
		TaskId:       taskId,
		WorkerPubkey: workerPriv.PubKey().Bytes(),
		ResultHash:   []byte("result-hash"),
	}

	accepted := uint64(1_000_000)
	received := uint64(999_000) // earlier than accepted — anomaly

	key := hex.EncodeToString(taskId)
	p.mu.Lock()
	p.pendingTasks[key] = &TaskEvidence{
		Receipt:      receipt,
		Verifiers:    makeVerifyResultsForTest(taskId),
		AcceptedAtMs: accepted,
		ReceivedAt:   received,
	}
	p.mu.Unlock()

	p.ProcessPending(context.Background(), []byte("blockhash"))
	msg := p.BuildBatch()
	if msg == nil || len(msg.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %v", msg)
	}
	entry := msg.Entries[0]
	if entry.LatencyMs != 0 {
		t.Fatalf("reversed timestamps: expected LatencyMs=0, got %d", entry.LatencyMs)
	}
	if entry.AcceptedAtMs != accepted || entry.ReceiptAtMs != received {
		t.Fatalf("raw timestamps must be preserved on anomaly: accepted=%d, receipt=%d",
			entry.AcceptedAtMs, entry.ReceiptAtMs)
	}
}

// TestProcessPending_WorkerSelfReportIgnored is the load-bearing security test:
// a task whose AssignTask was never observed by the Proposer (AcceptedAtMs == 0)
// must produce LatencyMs == 0, regardless of InferReceipt.InferenceLatencyMs.
// Before the P1 fix, a malicious Worker signing a small InferenceLatencyMs value
// gained up to 1.5× dispatch boost via VRF rankSpeedMultiplier.
func TestProcessPending_WorkerSelfReportIgnored(t *testing.T) {
	p := New("funai1test", nil, nil, 0, 100)

	taskId := []byte("task-p1-selfreport")
	workerPriv := secp256k1.GenPrivKey()

	receipt := &p2ptypes.InferReceipt{
		TaskId:             taskId,
		WorkerPubkey:       workerPriv.PubKey().Bytes(),
		ResultHash:         []byte("result-hash"),
		InferenceLatencyMs: 50, // the forged "I'm super fast!" value
	}

	key := hex.EncodeToString(taskId)
	p.mu.Lock()
	p.pendingTasks[key] = &TaskEvidence{
		Receipt:      receipt,
		Verifiers:    makeVerifyResultsForTest(taskId),
		AcceptedAtMs: 0, // never observed the dispatch
		ReceivedAt:   uint64(time.Now().UnixMilli()),
	}
	p.mu.Unlock()

	p.ProcessPending(context.Background(), []byte("blockhash"))
	msg := p.BuildBatch()
	if msg == nil || len(msg.Entries) != 1 {
		t.Fatalf("expected 1 entry, got %v", msg)
	}
	entry := msg.Entries[0]
	if entry.LatencyMs != 0 {
		t.Fatalf("Worker self-report must not drive LatencyMs: expected 0, got %d (attack value was 50)", entry.LatencyMs)
	}
}
