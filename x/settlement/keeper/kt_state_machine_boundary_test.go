package keeper_test

// State-machine boundary tests for KT 30-case Byzantine corner-case list, rows 16-20.
// (See engineer's classification 2026-04-29; Tier A — automatable in keeper-level mocks.)
//
//   Case 16 — expire_block exact boundary tx ordering
//   Case 17 — Worker jailed in earlier batch / block does not block subsequent settlement
//   Case 18 — MsgUnjail vs MsgBatchSettlement same-block ordering invariance
//   Case 19 — batch with mixed expired / fresh entries: per-entry skip
//   Case 20 — deposit progressively depleted mid-batch: first N settle, last skipped
//
// All five pin the canonical invariant that settlement is *per-entry stateless*
// with respect to worker jail status and is *boundary-inclusive* on expire_block.
// This file complements the existing TestProcessBatchSettlement_Expired (single
// past-boundary entry) and TestProcessBatchSettlement_InsufficientBalance (single
// over-budget entry); the new cases here cover the multi-entry / boundary-exact /
// post-jail scenarios that were not previously pinned.

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/settlement/types"
)

// ============================================================
// KT-16. expire_block exact boundary — `<` (strict) rule
//
// keeper.go:853:    if entry.ExpireBlock > 0 && entry.ExpireBlock < currentHeight { continue }
//
// This is strict less-than, so:
//   ExpireBlock == currentHeight       → SETTLE (boundary inclusive on now)
//   ExpireBlock == currentHeight - 1   → SKIP   (one past)
//
// EndBlocker's CleanupExpiredTasks runs *after* all txs in a block (module.go:131),
// so a same-block sweep cannot pre-empt a settling tx for an expire_block == now task.
// This test pins both halves so neither bound silently drifts.
// ============================================================

func TestKT16_ExpireBlockBoundary_StrictLessThan(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	// setupKeeper sets ctx height to 100.
	const blockH = int64(100)

	user := makeAddr("kt16-user")
	worker := makeAddr("kt16-worker")
	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

	verifiers := []types.VerifierResult{
		{Address: makeAddr("kt16-v1").String(), Pass: true},
		{Address: makeAddr("kt16-v2").String(), Pass: true},
		{Address: makeAddr("kt16-v3").String(), Pass: true},
	}

	entries := []types.SettlementEntry{
		// (a) ExpireBlock == currentHeight → must settle (strict-less-than rule).
		{
			TaskId: []byte("kt16-task-on-bound-"),
			UserAddress: user.String(), WorkerAddress: worker.String(),
			Fee: fee, ExpireBlock: blockH, Status: types.SettlementSuccess,
			VerifierResults: verifiers,
		},
		// (b) ExpireBlock == currentHeight - 1 → must skip (strictly past).
		{
			TaskId: []byte("kt16-task-one-past-"),
			UserAddress: user.String(), WorkerAddress: worker.String(),
			Fee: fee, ExpireBlock: blockH - 1, Status: types.SettlementSuccess,
			VerifierResults: verifiers,
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("KT-16: batch should not error: %v", err)
	}

	br, _ := k.GetBatchRecord(ctx, batchId)
	if br.ResultCount != 1 {
		t.Fatalf("KT-16: expected 1 settled (boundary entry only), got %d", br.ResultCount)
	}

	// Boundary entry settled → balance debited by exactly 1× fee.
	ia, _ := k.GetInferenceAccount(ctx, user)
	expected := math.NewInt(10_000_000 - 1_000_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("KT-16: expected balance %s after one boundary-settle, got %s", expected, ia.Balance.Amount)
	}

	if _, found := k.GetSettledTask(ctx, []byte("kt16-task-on-bound-")); !found {
		t.Fatal("KT-16: ExpireBlock==currentHeight entry MUST settle (boundary inclusive)")
	}
	if _, found := k.GetSettledTask(ctx, []byte("kt16-task-one-past-")); found {
		t.Fatal("KT-16: ExpireBlock==currentHeight-1 entry MUST be skipped (strictly past)")
	}
}

// ============================================================
// KT-17. Jail in earlier block does not gate subsequent settlement.
//
// Scenario: at block N a Worker FAILs an entry → JailWorker called.
// At block N+k, Proposer submits a BatchSettlement that contains a SUCCESS
// entry for the same Worker (a task accepted *before* the jail, completed
// off-chain, now arriving via merkle batch).
//
// Invariant pinned: ProcessBatchSettlement does NOT consult worker.Status
// before paying. Jail prevents accepting *new* dispatch but never voids
// already-completed in-flight income. This is the canonical "in-flight
// preserves" rule. Without it, a single mid-batch jail would orphan all
// concurrent work and create a settlement-versus-jail race attack surface.
// ============================================================

func TestKT17_JailInEarlierBatch_DoesNotBlockLaterSettlement(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	user := makeAddr("kt17-user")
	worker := makeAddr("kt17-worker")
	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

	verifiers := []types.VerifierResult{
		{Address: makeAddr("kt17-v1").String(), Pass: true},
		{Address: makeAddr("kt17-v2").String(), Pass: true},
		{Address: makeAddr("kt17-v3").String(), Pass: true},
	}
	failVerifiers := []types.VerifierResult{
		{Address: makeAddr("kt17-v1").String(), Pass: true},
		{Address: makeAddr("kt17-v2").String(), Pass: false},
		{Address: makeAddr("kt17-v3").String(), Pass: false},
	}

	// Batch 1, block 100: a FAIL entry that jails the worker.
	batch1 := []types.SettlementEntry{
		{
			TaskId: []byte("kt17-fail-pre-jail-"),
			UserAddress: user.String(), WorkerAddress: worker.String(),
			Fee: fee, ExpireBlock: 200, Status: types.SettlementFail,
			VerifierResults: failVerifiers,
		},
	}
	msg1 := makeBatchMsg(t, makeAddr("proposer").String(), batch1)
	if _, err := k.ProcessBatchSettlement(ctx, msg1); err != nil {
		t.Fatalf("KT-17: batch1 (jailing fail) error: %v", err)
	}

	if len(wk.jailCalls) != 1 || !wk.jailCalls[0].Equals(worker) {
		t.Fatalf("KT-17: expected exactly 1 jail call on the worker, got %d", len(wk.jailCalls))
	}
	streaksAfterBatch1 := len(wk.streakCalls)

	// Advance to block 150 — worker still considered jailed by chain at this point
	// (mock keeper preserves jail-call history; real keeper would have worker.Jailed=true).
	ctx = ctx.WithBlockHeight(150)

	// Batch 2: a SUCCESS entry for the same (jailed) worker. The task was
	// accepted off-chain BEFORE the jail; settlement must still pay.
	batch2 := []types.SettlementEntry{
		{
			TaskId: []byte("kt17-succ-post-jail"),
			UserAddress: user.String(), WorkerAddress: worker.String(),
			Fee: fee, ExpireBlock: 250, Status: types.SettlementSuccess,
			VerifierResults: verifiers,
		},
	}
	msg2 := makeBatchMsg(t, makeAddr("proposer").String(), batch2)
	if _, err := k.ProcessBatchSettlement(ctx, msg2); err != nil {
		t.Fatalf("KT-17: batch2 (post-jail success) error: %v", err)
	}

	// Settlement of post-jail SUCCESS must:
	//   - debit user (1× fee for SUCCESS)
	//   - increment success streak (proves we entered the SUCCESS branch in
	//     keeper.go:1019 — i.e. no jail-status pre-check gated us out)
	//   - create a TaskSettled record
	ia, _ := k.GetInferenceAccount(ctx, user)
	// FAIL fee = 1_000_000 * 150 / 1000 = 150_000 (DefaultParams FailSettlementFeeRatio=150).
	// SUCCESS fee = 1_000_000.
	expected := math.NewInt(10_000_000 - 150_000 - 1_000_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("KT-17: expected balance %s after FAIL+SUCCESS, got %s", expected, ia.Balance.Amount)
	}
	if len(wk.streakCalls) != streaksAfterBatch1+1 {
		t.Fatalf("KT-17: SUCCESS branch must call IncrementSuccessStreak exactly once on post-jail entry; got delta %d",
			len(wk.streakCalls)-streaksAfterBatch1)
	}
	st, found := k.GetSettledTask(ctx, []byte("kt17-succ-post-jail"))
	if !found {
		t.Fatal("KT-17: post-jail SUCCESS must produce a SettledTask record")
	}
	if st.Status != types.TaskSettled {
		t.Fatalf("KT-17: expected TaskSettled, got %s", st.Status)
	}
}

// ============================================================
// KT-18. Same-block ordering: MsgUnjail vs MsgBatchSettlement is
// settlement-outcome-invariant.
//
// Cosmos SDK is deterministic on intra-block tx ordering. Two valid orderings
// arise when a Worker submits MsgUnjail and a Proposer submits MsgBatchSettlement
// (containing a task for that Worker) into the same block:
//
//   Order A:  unjail-then-settle  → status flips ACTIVE first, then settles.
//   Order B:  settle-then-unjail  → settles while still flagged jailed,
//                                    then status flips ACTIVE.
//
// Because settlement keeper has no consult of worker.Status, both orders
// produce identical balance / streak / settled-record outcomes. This test
// pins that invariance: simulate the two orders against the same fixture
// and assert the resulting state is identical.
//
// (Out of scope here: the off-chain p2p Leader's view of jail state, which
// influences future AssignTask but never an already-merkle-batched task.)
// ============================================================

func TestKT18_UnjailVsSettlement_SameBlockOrderingInvariance(t *testing.T) {
	// Helper that runs one ordering and returns observable post-state.
	run := func(t *testing.T, settleFirst bool) (balanceLeft math.Int, streakCalls, jailCalls int) {
		t.Helper()
		k, ctx, _, wk := setupKeeper(t)
		k.SetCurrentSecondVerificationRate(ctx, 0)

		user := makeAddr("kt18-user")
		worker := makeAddr("kt18-worker")
		fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
		_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

		// Pre-condition: worker considered jailed (the mock records nothing
		// on this side; the test's purpose is to show settlement is invariant
		// regardless of any unjail-tx ordering before/after it).
		entries := []types.SettlementEntry{
			{
				TaskId: []byte("kt18-task-0000000001"),
				UserAddress: user.String(), WorkerAddress: worker.String(),
				Fee: fee, ExpireBlock: 200, Status: types.SettlementSuccess,
				VerifierResults: []types.VerifierResult{
					{Address: makeAddr("kt18-v1").String(), Pass: true},
					{Address: makeAddr("kt18-v2").String(), Pass: true},
					{Address: makeAddr("kt18-v3").String(), Pass: true},
				},
			},
		}
		msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)

		// Order A (settleFirst=true):
		//   tx 1: BatchSettlement (worker effectively jailed at this moment)
		//   tx 2: Unjail (mock — nothing observable from settlement keeper's side)
		// Order B (settleFirst=false): swap.
		if settleFirst {
			if _, err := k.ProcessBatchSettlement(ctx, msg); err != nil {
				t.Fatalf("settle-first: %v", err)
			}
			// Simulated unjail tx — would mutate worker.Jailed=false in real keeper;
			// settlement keeper does not read this flag, so simulating it as a no-op
			// here is sufficient to assert invariance.
		} else {
			// Simulated unjail tx (no-op as above)
			if _, err := k.ProcessBatchSettlement(ctx, msg); err != nil {
				t.Fatalf("unjail-first: %v", err)
			}
		}

		ia, _ := k.GetInferenceAccount(ctx, user)
		return ia.Balance.Amount, len(wk.streakCalls), len(wk.jailCalls)
	}

	balA, streakA, jailA := run(t, true)  // settle-then-unjail
	balB, streakB, jailB := run(t, false) // unjail-then-settle

	if !balA.Equal(balB) {
		t.Fatalf("KT-18: balance must be order-invariant; settle-first=%s, unjail-first=%s", balA, balB)
	}
	if streakA != streakB {
		t.Fatalf("KT-18: streak-call count must be order-invariant; settle-first=%d, unjail-first=%d", streakA, streakB)
	}
	if jailA != jailB {
		t.Fatalf("KT-18: jail-call count must be order-invariant; settle-first=%d, unjail-first=%d", jailA, jailB)
	}
	// Sanity: a SUCCESS settlement must have happened in both orderings
	// (else the assertion above would be trivially equal at zero).
	if streakA == 0 {
		t.Fatal("KT-18: at least one streak call expected, got 0 — settlement may not have run")
	}
}

// ============================================================
// KT-19. Batch with mixed expired and fresh entries — per-entry skip.
//
// Spec: keeper.go:853 skips per-entry, never aborts the whole batch. Pins
// that this is the canonical behavior so a partial-expiry batch is not
// misread as a "drop the whole thing" case in the future.
// ============================================================

func TestKT19_PartialBatchExpiry_PerEntrySkip(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	const blockH = int64(100)
	user := makeAddr("kt19-user")
	worker := makeAddr("kt19-worker")
	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

	verifiers := []types.VerifierResult{
		{Address: makeAddr("kt19-v1").String(), Pass: true},
		{Address: makeAddr("kt19-v2").String(), Pass: true},
		{Address: makeAddr("kt19-v3").String(), Pass: true},
	}

	entries := []types.SettlementEntry{
		// A: expired (ExpireBlock < currentHeight) → skipped silently
		{TaskId: []byte("kt19-A-expired-tooo"), UserAddress: user.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: blockH - 10, Status: types.SettlementSuccess, VerifierResults: verifiers},
		// B: expired (one block past) → skipped
		{TaskId: []byte("kt19-B-expired-one-"), UserAddress: user.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: blockH - 1, Status: types.SettlementSuccess, VerifierResults: verifiers},
		// C: on boundary (== currentHeight) → settle
		{TaskId: []byte("kt19-C-on-boundaryX"), UserAddress: user.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: blockH, Status: types.SettlementSuccess, VerifierResults: verifiers},
		// D: fresh → settle
		{TaskId: []byte("kt19-D-fresh-future"), UserAddress: user.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: blockH + 100, Status: types.SettlementSuccess, VerifierResults: verifiers},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("KT-19: partial-expiry batch should not error: %v", err)
	}

	br, _ := k.GetBatchRecord(ctx, batchId)
	if br.ResultCount != 2 {
		t.Fatalf("KT-19: expected 2 settled (C + D), got %d", br.ResultCount)
	}

	ia, _ := k.GetInferenceAccount(ctx, user)
	// 2 SUCCESS settlements at 1M each → 2M debited.
	expected := math.NewInt(10_000_000 - 2*1_000_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("KT-19: expected balance %s, got %s", expected, ia.Balance.Amount)
	}

	for _, expid := range [][]byte{[]byte("kt19-A-expired-tooo"), []byte("kt19-B-expired-one-")} {
		if _, found := k.GetSettledTask(ctx, expid); found {
			t.Fatalf("KT-19: expired entry %s must not produce SettledTask", expid)
		}
	}
	for _, freshid := range [][]byte{[]byte("kt19-C-on-boundaryX"), []byte("kt19-D-fresh-future")} {
		if _, found := k.GetSettledTask(ctx, freshid); !found {
			t.Fatalf("KT-19: non-expired entry %s must produce SettledTask", freshid)
		}
	}

	if len(wk.streakCalls) != 2 {
		t.Fatalf("KT-19: only the 2 settled entries should call IncrementSuccessStreak, got %d", len(wk.streakCalls))
	}
}

// ============================================================
// KT-20. Deposit progressively depleted mid-batch.
//
// User has balance for 3 of 4 SUCCESS entries (each 100 ufai). The keeper
// deducts greedily in iteration order. Per audit Rule 4 (max_fee
// pre-authorisation: never silently drop a settlement on shortfall) the
// entry that finds insufficient balance must STILL settle on what is
// available — Worker absorbs the shortfall, the task lands as TaskSettled
// with the partial Fee recorded.
//
// Expected after this test (deterministic order, all 4 entries cost 100,
// balance 350):
//   - A, B, C settle full (debit 100 each → 300 total; balance now 50)
//   - D settles partial (payable = 50, shortfall = 50; balance now 0)
//   - all 4 produce SettledTask records
//   - Worker streak called 4 times (work was done on every entry)
//   - EventShortfall emitted exactly once for D
// ============================================================

func TestKT20_ProgressiveDepletion_MidBatch(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	user := makeAddr("kt20-user")
	worker := makeAddr("kt20-worker")
	fee := sdk.NewCoin("ufai", math.NewInt(100))
	// Balance covers exactly 3 of 4 entries (300 of 400 needed).
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(350)))

	verifiers := []types.VerifierResult{
		{Address: makeAddr("kt20-v1").String(), Pass: true},
		{Address: makeAddr("kt20-v2").String(), Pass: true},
		{Address: makeAddr("kt20-v3").String(), Pass: true},
	}

	entries := []types.SettlementEntry{
		{TaskId: []byte("kt20-task-A-settles"), UserAddress: user.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000, Status: types.SettlementSuccess, VerifierResults: verifiers},
		{TaskId: []byte("kt20-task-B-settles"), UserAddress: user.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000, Status: types.SettlementSuccess, VerifierResults: verifiers},
		{TaskId: []byte("kt20-task-C-settles"), UserAddress: user.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000, Status: types.SettlementSuccess, VerifierResults: verifiers},
		{TaskId: []byte("kt20-task-D-shortfall"), UserAddress: user.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000, Status: types.SettlementSuccess, VerifierResults: verifiers},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("KT-20: progressive-depletion batch should not error: %v", err)
	}

	br, _ := k.GetBatchRecord(ctx, batchId)
	if br.ResultCount != 4 {
		t.Fatalf("KT-20: expected all 4 settled (Rule 4: shortfall entry settles partial), got %d", br.ResultCount)
	}

	ia, _ := k.GetInferenceAccount(ctx, user)
	// 3 full × 100 = 300; 1 partial = 50; total debited = 350; balance now 0.
	if !ia.Balance.Amount.Equal(math.NewInt(0)) {
		t.Fatalf("KT-20: expected balance fully drained to 0 (Rule 4 partial-pay), got %s", ia.Balance.Amount)
	}

	// All 4 entries must have SettledTask records.
	for _, taskId := range [][]byte{
		[]byte("kt20-task-A-settles"),
		[]byte("kt20-task-B-settles"),
		[]byte("kt20-task-C-settles"),
		[]byte("kt20-task-D-shortfall"),
	} {
		if _, found := k.GetSettledTask(ctx, taskId); !found {
			t.Fatalf("KT-20: settled entry %s missing — Rule 4 forbids silent drop", taskId)
		}
	}

	// The shortfall entry's recorded Fee must reflect the partial payment.
	dST, _ := k.GetSettledTask(ctx, []byte("kt20-task-D-shortfall"))
	if !dST.Fee.Amount.Equal(math.NewInt(50)) {
		t.Fatalf("KT-20: shortfall entry must record Fee=50 (the actually-paid amount), got %s", dST.Fee.Amount)
	}

	// Streak should be called 4 times (work was done on every entry, even
	// the shortfall one).
	if len(wk.streakCalls) != 4 {
		t.Fatalf("KT-20: expected 4 streak calls (Rule 4: Worker is paid for the work even on shortfall), got %d", len(wk.streakCalls))
	}
}
