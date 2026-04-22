package leader

import (
	"testing"

	"cosmossdk.io/math"
)

// newTestLeader constructs a Leader with just the bookkeeping state populated —
// no Host / ChainClient / PrivKey. Sufficient for testing capacity-aware
// admission (hasInferenceCapacity) and release counters
// (ReleaseBusy / HandleReceiptBusyRelease), which do not touch P2P or chain.
func newTestLeader() *Leader {
	return &Leader{
		activeInferenceTasks: make(map[string]uint32),
		activeVerifyTasks:    make(map[string]uint32),
		pendingFees:          make(map[string][]PendingEntry),
	}
}

// TestCapacityFilter_RespectsDeclaredCapacity — the core V6 §2.3 rule:
// hasInferenceCapacity returns true iff in-flight count < declared capacity.
// Exercises the filter at mixed capacities [1, 2, 4] with various fill levels.
func TestCapacityFilter_RespectsDeclaredCapacity(t *testing.T) {
	l := newTestLeader()

	workerA := "workerA"
	workerB := "workerB"
	workerC := "workerC"

	// Capacity 1 Worker. Should admit 0→1 but not 1→2.
	if !l.hasInferenceCapacity(workerA, 1) {
		t.Fatalf("workerA at 0/1 in_flight should be admissible")
	}
	l.activeInferenceTasks[workerA] = 1
	if l.hasInferenceCapacity(workerA, 1) {
		t.Fatalf("workerA at 1/1 should NOT be admissible")
	}

	// Capacity 2 Worker. 0→2 admissible, 2→3 not.
	for i := uint32(0); i < 2; i++ {
		if !l.hasInferenceCapacity(workerB, 2) {
			t.Fatalf("workerB at %d/2 in_flight should be admissible", i)
		}
		l.activeInferenceTasks[workerB] = i + 1
	}
	if l.hasInferenceCapacity(workerB, 2) {
		t.Fatalf("workerB at 2/2 should NOT be admissible")
	}

	// Capacity 4 Worker.
	for i := uint32(0); i < 4; i++ {
		if !l.hasInferenceCapacity(workerC, 4) {
			t.Fatalf("workerC at %d/4 in_flight should be admissible", i)
		}
		l.activeInferenceTasks[workerC] = i + 1
	}
	if l.hasInferenceCapacity(workerC, 4) {
		t.Fatalf("workerC at 4/4 should NOT be admissible")
	}
}

// TestCapacityFilter_ZeroCapacityTreatedAsOne — legacy Workers that registered
// before the V6 capacity field existed (or CLI omitted --max-concurrent-tasks)
// must keep the single-task busy/idle behaviour. Passing MaxConcurrentTasks=0
// to hasInferenceCapacity is the encoded "unset" state, and the filter
// promotes it to 1 to stay backward-compatible.
func TestCapacityFilter_ZeroCapacityTreatedAsOne(t *testing.T) {
	l := newTestLeader()
	worker := "legacy"

	if !l.hasInferenceCapacity(worker, 0) {
		t.Fatalf("legacy worker at 0/1 should be admissible")
	}
	l.activeInferenceTasks[worker] = 1
	if l.hasInferenceCapacity(worker, 0) {
		t.Fatalf("legacy worker at 1/1 should NOT be admissible (capacity=0 → 1)")
	}
}

// TestReleaseBusy_DecrementsActiveCount — after dispatch, the Leader must
// decrement a worker's in-flight count on task completion. ReleaseBusy is the
// explicit-caller variant; HandleReceiptBusyRelease is the auto-on-receipt
// variant. Both should behave the same on the counter axis.
func TestReleaseBusy_DecrementsActiveCount(t *testing.T) {
	l := newTestLeader()
	worker := "worker1"
	user := "user1"
	taskId := []byte("task-release-1")

	l.activeInferenceTasks[worker] = 2

	l.ReleaseBusy(worker, user, taskId)
	if got := l.ActiveInferenceTasks(worker); got != 1 {
		t.Fatalf("after one ReleaseBusy expected 1, got %d", got)
	}

	l.ReleaseBusy(worker, user, taskId)
	if got := l.ActiveInferenceTasks(worker); got != 0 {
		t.Fatalf("after two ReleaseBusy expected 0, got %d", got)
	}
}

// TestReleaseBusy_UnderflowSafe — calling ReleaseBusy when the counter is
// already 0 is a no-op, not a panic or wraparound. Protects against spurious
// receipt relay or a misbehaving worker re-broadcasting an old receipt.
func TestReleaseBusy_UnderflowSafe(t *testing.T) {
	l := newTestLeader()
	worker := "worker1"
	user := "user1"
	taskId := []byte("task-underflow")

	// Counter never initialised — map default is 0.
	l.ReleaseBusy(worker, user, taskId)
	if got := l.ActiveInferenceTasks(worker); got != 0 {
		t.Fatalf("release on empty counter should stay at 0, got %d (uint32 wrap risk)", got)
	}

	// Explicit zero, same expectation.
	l.activeInferenceTasks[worker] = 0
	l.ReleaseBusy(worker, user, taskId)
	if got := l.ActiveInferenceTasks(worker); got != 0 {
		t.Fatalf("release on explicit 0 should stay at 0, got %d", got)
	}
}

// TestHandleReceiptBusyRelease_DecrementsActiveCount — the production path
// for releasing a slot: an InferReceipt observed on P2P, keyed by the
// worker's pubkey (not address). Internally hex-encodes pubkey → address.
func TestHandleReceiptBusyRelease_DecrementsActiveCount(t *testing.T) {
	l := newTestLeader()

	// Use a raw byte slice as pubkey; hex(workerPubkey) is what the counter
	// is keyed on.
	workerPubkey := []byte{0x02, 0xde, 0xad, 0xbe, 0xef}
	workerAddr := "02deadbeef"
	user := "user1"
	taskId := []byte("task-receipt-1")

	l.activeInferenceTasks[workerAddr] = 3

	l.HandleReceiptBusyRelease(workerPubkey, user, taskId)
	if got := l.ActiveInferenceTasks(workerAddr); got != 2 {
		t.Fatalf("after HandleReceiptBusyRelease expected 2, got %d", got)
	}

	// Idempotent on an unknown taskId (no counter effect); the call still
	// decrements because the counter was > 0.
	l.HandleReceiptBusyRelease(workerPubkey, user, []byte("task-does-not-exist"))
	if got := l.ActiveInferenceTasks(workerAddr); got != 1 {
		t.Fatalf("after second HandleReceiptBusyRelease expected 1, got %d", got)
	}
}

// TestMixedCapacities_FilterPicksAvailable — end-to-end filter behaviour on
// a batch of Workers with capacities [1, 2, 4]: simulate 3 tasks in flight on
// the cap=4 worker and 1 on the cap=1 worker; assert the cap=2 worker and the
// cap=4 worker are still admissible while the cap=1 is not.
//
// This exercises the exact admission decision `dispatchSingle` makes when
// ranking workers for a new request.
func TestMixedCapacities_FilterPicksAvailable(t *testing.T) {
	l := newTestLeader()

	workers := []WorkerInfo{
		{Address: "cap1", MaxConcurrentTasks: 1, Stake: math.NewInt(10)},
		{Address: "cap2", MaxConcurrentTasks: 2, Stake: math.NewInt(10)},
		{Address: "cap4", MaxConcurrentTasks: 4, Stake: math.NewInt(10)},
	}
	l.Workers = workers

	// Simulate: cap=1 already has 1 task in flight (full); cap=4 has 3 in
	// flight (1 slot left); cap=2 has 0 in flight (full capacity free).
	l.activeInferenceTasks["cap1"] = 1
	l.activeInferenceTasks["cap4"] = 3

	var admissible []string
	for _, w := range l.Workers {
		if l.hasInferenceCapacity(w.Address, w.MaxConcurrentTasks) {
			admissible = append(admissible, w.Address)
		}
	}

	// cap1 full; cap2 and cap4 still have slots.
	want := map[string]bool{"cap2": true, "cap4": true}
	got := make(map[string]bool, len(admissible))
	for _, a := range admissible {
		got[a] = true
	}
	if len(got) != len(want) {
		t.Fatalf("admissible count mismatch: want %v, got %v", want, got)
	}
	for addr := range want {
		if !got[addr] {
			t.Fatalf("expected %q admissible, not in filter result %v", addr, admissible)
		}
	}
	if got["cap1"] {
		t.Fatalf("cap1 is full (1/1) — filter should have excluded it")
	}
}
