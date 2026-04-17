package keeper_test

// Edge-case and boundary-condition tests for the settlement module.
// These tests cover abnormal scenarios, race conditions, and boundary values
// not covered by the existing unit and regression tests.

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/settlement/keeper"
	"github.com/funai-wiki/funai-chain/x/settlement/types"
)

// ============================================================
// 1. Audit collateral: audit FAIL jails worker AND all original verifiers
// Spec V5.1: "audit FAIL → all original PASS verifiers jail_count += 1"
// ============================================================

func TestAuditFail_JailsWorkerAndAllVerifiers(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	k.SetCurrentThirdVerificationRate(ctx, 0)

	workerAddr := makeAddr("audit-jail-worker")
	v1 := makeAddr("audit-jail-v1")
	v2 := makeAddr("audit-jail-v2")
	v3 := makeAddr("audit-jail-v3")
	userAddr := makeAddr("audit-jail-user")
	taskId := []byte("auditjail-task-0001")

	k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       ctx.BlockHeight(),
		UserAddress:       userAddr.String(),
		WorkerAddress:     workerAddr.String(),
		VerifierAddresses: []string{v1.String(), v2.String(), v3.String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       10000,
	})

	// Submit 3 audit results: 1 PASS + 2 FAIL → FAIL majority
	second_verifiers := []sdk.AccAddress{makeAddr("aj-aud1"), makeAddr("aj-aud2"), makeAddr("aj-aud3")}
	passFlags := []bool{true, false, false}
	for i, aud := range second_verifiers {
		_ = k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
			SecondVerifier: aud.String(),
			TaskId:         taskId,
			Epoch:          1,
			Pass:           passFlags[i],
			LogitsHash:     []byte("hash"),
		})
	}

	// Worker + 3 verifiers = 4 jail calls
	if len(wk.jailCalls) < 4 {
		t.Fatalf("expected at least 4 jail calls (worker + 3 verifiers), got %d", len(wk.jailCalls))
	}

	// Verify worker was jailed
	workerJailed := false
	for _, addr := range wk.jailCalls {
		if addr.Equals(workerAddr) {
			workerJailed = true
			break
		}
	}
	if !workerJailed {
		t.Fatal("worker should be jailed on audit FAIL")
	}

	// Verify all 3 verifiers were jailed
	for _, v := range []sdk.AccAddress{v1, v2, v3} {
		found := false
		for _, addr := range wk.jailCalls {
			if addr.Equals(v) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("verifier %s should be jailed on audit FAIL", v.String())
		}
	}
}

// ============================================================
// 2. Audit PASS confirms SUCCESS: no jail, task settled normally
// ============================================================

func TestAuditPass_ConfirmsSuccess_NoJail(t *testing.T) {
	k, ctx, _, wk := setupTrackingKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	k.SetCurrentThirdVerificationRate(ctx, 0)

	userAddr := makeAddr("ap-user")
	workerAddr := makeAddr("ap-worker")
	taskId := []byte("auditpass-task-0001")

	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

	k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       ctx.BlockHeight(),
		UserAddress:       userAddr.String(),
		WorkerAddress:     workerAddr.String(),
		VerifierAddresses: []string{makeAddr("ap-v1").String(), makeAddr("ap-v2").String(), makeAddr("ap-v3").String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       10000,
	})

	// 3 PASS → audit confirms SUCCESS
	for i := 0; i < 3; i++ {
		_ = k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
			SecondVerifier: makeAddr(fmt.Sprintf("ap-aud%d", i)).String(),
			TaskId:         taskId,
			Epoch:          1,
			Pass:           true,
			LogitsHash:     []byte("hash"),
		})
	}

	if len(wk.jailCalls) != 0 {
		t.Fatalf("no jail calls expected for audit PASS on SUCCESS, got %d", len(wk.jailCalls))
	}

	// User should be charged full fee
	ia, _ := k.GetInferenceAccount(ctx, userAddr)
	expected := math.NewInt(10_000_000 - 1_000_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("expected balance %s, got %s", expected, ia.Balance.Amount)
	}
}

// ============================================================
// 3. FAIL→FAIL audit (confirms FAIL): user pays 15%, worker jailed
// ============================================================

func TestAuditConfirmsFail_UserPays15Percent(t *testing.T) {
	k, ctx, _, wk := setupTrackingKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	k.SetCurrentThirdVerificationRate(ctx, 0)

	userAddr := makeAddr("ff-user")
	workerAddr := makeAddr("ff-worker")
	taskId := []byte("failfail-task-00001")

	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

	k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementFail,
		SubmittedAt:       ctx.BlockHeight(),
		UserAddress:       userAddr.String(),
		WorkerAddress:     workerAddr.String(),
		VerifierAddresses: []string{makeAddr("ff-v1").String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       10000,
	})

	// 3 FAIL audit → confirms FAIL
	for i := 0; i < 3; i++ {
		_ = k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
			SecondVerifier: makeAddr(fmt.Sprintf("ff-aud%d", i)).String(),
			TaskId:         taskId,
			Epoch:          1,
			Pass:           false,
			LogitsHash:     []byte("hash"),
		})
	}

	// Worker should be jailed
	if len(wk.jailCalls) < 1 {
		t.Fatal("worker should be jailed when audit confirms FAIL")
	}

	// User pays 15% fail fee = 150_000
	ia, _ := k.GetInferenceAccount(ctx, userAddr)
	failFee := math.NewInt(1_000_000).MulRaw(150).QuoRaw(1000)
	expected := math.NewInt(10_000_000).Sub(failFee)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("expected balance %s (15%% deducted), got %s", expected, ia.Balance.Amount)
	}
}

// ============================================================
// 4. Batch with multiple users: one has insufficient balance
// ============================================================

func TestBatchSettlement_MultiUser_OneInsufficientBalance(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	richUser := makeAddr("rich-user")
	poorUser := makeAddr("poor-user")
	worker := makeAddr("multi-worker")

	_ = k.ProcessDeposit(ctx, richUser, sdk.NewCoin("ufai", math.NewInt(5_000_000)))
	_ = k.ProcessDeposit(ctx, poorUser, sdk.NewCoin("ufai", math.NewInt(100))) // too little

	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	verifiers := []types.VerifierResult{
		{Address: makeAddr("mu-v1").String(), Pass: true},
		{Address: makeAddr("mu-v2").String(), Pass: true},
		{Address: makeAddr("mu-v3").String(), Pass: true},
	}

	entries := []types.SettlementEntry{
		{
			TaskId: []byte("multi-user-task-001"), UserAddress: richUser.String(),
			WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000,
			Status: types.SettlementSuccess, VerifierResults: verifiers,
		},
		{
			TaskId: []byte("multi-user-task-002"), UserAddress: poorUser.String(),
			WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000,
			Status: types.SettlementSuccess, VerifierResults: verifiers,
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Only 1 task should settle (rich user), poor user's task skipped
	br, _ := k.GetBatchRecord(ctx, batchId)
	if br.ResultCount != 1 {
		t.Fatalf("expected 1 result (poor user skipped), got %d", br.ResultCount)
	}

	// Rich user charged, poor user untouched
	ia, _ := k.GetInferenceAccount(ctx, richUser)
	if !ia.Balance.Amount.Equal(math.NewInt(4_000_000)) {
		t.Fatalf("rich user balance: expected 4M, got %s", ia.Balance.Amount)
	}
	ia2, _ := k.GetInferenceAccount(ctx, poorUser)
	if !ia2.Balance.Amount.Equal(math.NewInt(100)) {
		t.Fatalf("poor user balance should be unchanged, got %s", ia2.Balance.Amount)
	}

	if len(wk.streakCalls) != 1 {
		t.Fatalf("expected 1 streak call, got %d", len(wk.streakCalls))
	}
}

// ============================================================
// 5. FAIL settlement with insufficient balance for 15% fail fee
// ============================================================

func TestBatchSettlement_FailInsufficientForFailFee(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("fail-insuff-user")
	worker := makeAddr("fail-insuff-wrk")

	// Deposit 30 ufai. Fail fee = 1M * 150/1000 = 150_000. 30 < 150_000 → skip
	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(30)))

	entries := []types.SettlementEntry{
		{
			TaskId: []byte("fail-insuff-task-01"), UserAddress: userAddr.String(),
			WorkerAddress: worker.String(),
			Fee:           sdk.NewCoin("ufai", math.NewInt(1_000_000)),
			ExpireBlock:   10000, Status: types.SettlementFail,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("fi-v1").String(), Pass: true},
				{Address: makeAddr("fi-v2").String(), Pass: false},
				{Address: makeAddr("fi-v3").String(), Pass: false},
			},
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("should not error: %v", err)
	}

	br, _ := k.GetBatchRecord(ctx, batchId)
	if br.ResultCount != 0 {
		t.Fatalf("expected 0 results (insufficient for fail fee), got %d", br.ResultCount)
	}

	ia, _ := k.GetInferenceAccount(ctx, userAddr)
	if !ia.Balance.Amount.Equal(math.NewInt(30)) {
		t.Fatalf("balance should be unchanged, got %s", ia.Balance.Amount)
	}

	if len(wk.jailCalls) != 0 {
		t.Fatalf("no jail for skipped fail task, got %d", len(wk.jailCalls))
	}
}

// ============================================================
// 6. ExpireBlock=0 means no expiry → task should be processed
// ============================================================

func TestBatchSettlement_ExpireBlockZero_NoExpiry(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("noexp-user")
	worker := makeAddr("noexp-worker")

	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(5_000_000)))

	entries := []types.SettlementEntry{
		{
			TaskId: []byte("no-expire-task-0001"), UserAddress: userAddr.String(),
			WorkerAddress: worker.String(),
			Fee:           sdk.NewCoin("ufai", math.NewInt(1_000_000)),
			ExpireBlock:   0, // 0 means no expiry
			Status:        types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("ne-v1").String(), Pass: true},
				{Address: makeAddr("ne-v2").String(), Pass: true},
				{Address: makeAddr("ne-v3").String(), Pass: true},
			},
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	br, _ := k.GetBatchRecord(ctx, batchId)
	if br.ResultCount != 1 {
		t.Fatalf("expire_block=0 should not expire; expected 1 result, got %d", br.ResultCount)
	}
}

// ============================================================
// 7. Merkle root mismatch → reject batch AND jail proposer
// ============================================================

func TestBatchSettlement_MerkleMismatch_ProposerJailed(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)

	proposer := makeAddr("bad-proposer")
	userAddr := makeAddr("merkle-user")
	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(5_000_000)))

	entries := fillTestSigHashes([]types.SettlementEntry{
		{
			TaskId: []byte("merkle-mismatch-001"), UserAddress: userAddr.String(),
			WorkerAddress: makeAddr("mw").String(),
			Fee:           sdk.NewCoin("ufai", math.NewInt(100_000)),
			ExpireBlock:   10000, Status: types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("mm-v1").String(), Pass: true},
				{Address: makeAddr("mm-v2").String(), Pass: true},
				{Address: makeAddr("mm-v3").String(), Pass: true},
			},
		},
	})

	// Sign the wrong merkle root so signature passes but merkle check fails
	badRoot := []byte("wrong-root")
	badRootHash := sha256.Sum256(badRoot)
	badSig, _ := testProposerKey.Sign(badRootHash[:])
	badMsg := types.NewMsgBatchSettlement(proposer.String(), badRoot, entries, badSig)
	_, err := k.ProcessBatchSettlement(ctx, badMsg)
	if err == nil {
		t.Fatal("expected error for merkle mismatch")
	}

	// Proposer should be jailed
	if len(wk.jailCalls) != 1 {
		t.Fatalf("proposer should be jailed on merkle mismatch, got %d jail calls", len(wk.jailCalls))
	}
	if !wk.jailCalls[0].Equals(proposer) {
		t.Fatal("jailed address should be the proposer")
	}

	// User balance unchanged
	ia, _ := k.GetInferenceAccount(ctx, userAddr)
	if !ia.Balance.Amount.Equal(math.NewInt(5_000_000)) {
		t.Fatalf("balance should be unchanged after merkle mismatch, got %s", ia.Balance.Amount)
	}
}

// ============================================================
// 8. FraudProof before settlement: fraud mark prevents settlement
// ============================================================

func TestFraudProofBeforeSettlement_PreventsCharge(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("fp-before-user")
	workerAddr := makeAddr("fp-before-worker")
	taskId := []byte("fraud-before-settle1")

	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(5_000_000)))

	// Submit fraud proof BEFORE settlement
	chBefore, csBefore := signFraudContent(t, []byte("c"))
	_ = k.ProcessFraudProof(ctx, &types.MsgFraudProof{
		Reporter:         userAddr.String(),
		TaskId:           taskId,
		WorkerAddress:    workerAddr.String(),
		ContentHash:      chBefore,
		WorkerContentSig: csBefore,
		ActualContent:    []byte("c"),
	})

	// Now try to settle the same task
	entries := []types.SettlementEntry{
		{
			TaskId: taskId, UserAddress: userAddr.String(),
			WorkerAddress: workerAddr.String(),
			Fee:           sdk.NewCoin("ufai", math.NewInt(1_000_000)),
			ExpireBlock:   10000, Status: types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("fb-v1").String(), Pass: true},
				{Address: makeAddr("fb-v2").String(), Pass: true},
				{Address: makeAddr("fb-v3").String(), Pass: true},
			},
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("should not error: %v", err)
	}

	br, _ := k.GetBatchRecord(ctx, batchId)
	if br.ResultCount != 0 {
		t.Fatalf("fraud-marked task should be skipped, got %d results", br.ResultCount)
	}

	ia, _ := k.GetInferenceAccount(ctx, userAddr)
	if !ia.Balance.Amount.Equal(math.NewInt(5_000_000)) {
		t.Fatalf("user balance unchanged when fraud mark prevents settlement, got %s", ia.Balance.Amount)
	}
}

// ============================================================
// 9. FraudProof after settlement: task marked as FRAUD, worker slashed
// ============================================================

func TestFraudProofAfterSettlement_MarkedFraud(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)

	taskId := []byte("fraud-after-settle01")
	workerAddr := makeAddr("fa-worker")
	userAddr := makeAddr("fa-user")

	// Pre-settle the task
	k.SetSettledTask(ctx, types.SettledTaskID{
		TaskId:        taskId,
		Status:        types.TaskSettled,
		SettledAt:     50,
		WorkerAddress: workerAddr.String(),
		UserAddress:   userAddr.String(),
		Fee:           sdk.NewCoin("ufai", math.NewInt(1_000_000)),
	})

	chAfter, csAfter := signFraudContent(t, []byte("c"))
	err := k.ProcessFraudProof(ctx, &types.MsgFraudProof{
		Reporter:         userAddr.String(),
		TaskId:           taskId,
		WorkerAddress:    workerAddr.String(),
		ContentHash:      chAfter,
		WorkerContentSig: csAfter,
		ActualContent:    []byte("c"),
	})
	if err != nil {
		t.Fatalf("fraud proof should succeed: %v", err)
	}

	st, found := k.GetSettledTask(ctx, taskId)
	if !found {
		t.Fatal("settled task should still exist")
	}
	if st.Status != types.TaskFraud {
		t.Fatalf("expected FRAUD status, got %s", st.Status)
	}

	if len(wk.slashCalls) != 1 {
		t.Fatalf("expected 1 slash call, got %d", len(wk.slashCalls))
	}
}

// ============================================================
// 10. Duplicate FraudProof is rejected
// ============================================================

func TestFraudProof_DuplicateRejected(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	taskId := []byte("fraud-dup-task-0001")
	chDup, csDup := signFraudContent(t, []byte("c"))
	msg := &types.MsgFraudProof{
		Reporter:         makeAddr("reporter").String(),
		TaskId:           taskId,
		WorkerAddress:    makeAddr("worker").String(),
		ContentHash:      chDup,
		WorkerContentSig: csDup,
		ActualContent:    []byte("c"),
	}

	_ = k.ProcessFraudProof(ctx, msg)
	err := k.ProcessFraudProof(ctx, msg)
	if err == nil {
		t.Fatal("duplicate fraud proof should be rejected")
	}
}

// ============================================================
// 11. Audit rate clamping at min and max
// ============================================================

func TestCalculateSecondVerificationRate_Clamping(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	tests := []struct {
		name     string
		fail     uint64
		total    uint64
		wantRate uint32
	}{
		{"zero_fail_clamped_to_min", 0, 1000, 100},
		{"high_fail_clamped_to_max", 500, 1000, 300},
		{"100%_fail_clamped", 1000, 1000, 300},
		{"no_tasks_base_rate", 0, 0, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := types.EpochStats{
				Epoch:        99,
				TotalSettled: tt.total,
				FailSettled:  tt.fail,
			}
			k.SetEpochStats(ctx, stats)
			rate := k.CalculateSecondVerificationRate(ctx, 99)
			if rate != tt.wantRate {
				t.Fatalf("rate: want %d, got %d", tt.wantRate, rate)
			}
		})
	}
}

// ============================================================
// 12. ThirdVerification rate clamping
// ============================================================

func TestCalculateThirdVerificationRate_Clamping(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	tests := []struct {
		name     string
		overturn uint64
		total    uint64
		wantRate uint32
	}{
		{"zero_overturn", 0, 100, 10},
		{"high_overturn_clamped", 50, 100, 50},
		{"no_audits", 0, 0, 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := types.EpochStats{
				Epoch:                   88,
				SecondVerificationTotal: tt.total,
				AuditOverturn:           tt.overturn,
			}
			k.SetEpochStats(ctx, stats)
			rate := k.CalculateThirdVerificationRate(ctx, 88)
			if rate != tt.wantRate {
				t.Fatalf("third_verification rate: want %d, got %d", tt.wantRate, rate)
			}
		})
	}
}

// ============================================================
// 13. Audit fund distribution: zero fees → no distribution
// ============================================================

func TestMultiVerificationFund_ZeroFees_NoDistribution(t *testing.T) {
	k, ctx, bk, _ := setupTrackingKeeper(t)

	epoch := ctx.BlockHeight() / 100
	stats := types.EpochStats{
		Epoch:                         epoch,
		TotalFees:                     math.ZeroInt(),
		SecondVerificationPersonCount: 5,
	}
	k.SetEpochStats(ctx, stats)

	k.DistributeMultiVerificationFund(ctx, epoch)

	if len(bk.received) != 0 {
		t.Fatal("no distribution expected when total fees are zero")
	}
}

// ============================================================
// 14. Audit fund distribution: zero audit person count → no distribution
// ============================================================

func TestMultiVerificationFund_ZeroSecondVerificationPersonCount_NoDistribution(t *testing.T) {
	k, ctx, bk, _ := setupTrackingKeeper(t)

	epoch := ctx.BlockHeight() / 100
	stats := types.EpochStats{
		Epoch:                         epoch,
		TotalFees:                     math.NewInt(10_000_000),
		SecondVerificationPersonCount: 0,
	}
	k.SetEpochStats(ctx, stats)

	k.DistributeMultiVerificationFund(ctx, epoch)

	if len(bk.received) != 0 {
		t.Fatal("no distribution expected with zero audit person count")
	}
}

// ============================================================
// 15. Cleanup: task with ExpireBlock=0 is never cleaned
// ============================================================

func TestCleanupExpiredTasks_ZeroExpireBlock_NeverCleaned(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	ctx = ctx.WithBlockHeight(100000)

	k.SetSettledTask(ctx, types.SettledTaskID{
		TaskId:      []byte("no-expire-cleanup01"),
		Status:      types.TaskSettled,
		ExpireBlock: 0, // no expiry
		SettledAt:   50,
	})

	cleaned := k.CleanupExpiredTasks(ctx)
	if cleaned != 0 {
		t.Fatalf("tasks with expire_block=0 should never be cleaned, got %d", cleaned)
	}

	_, found := k.GetSettledTask(ctx, []byte("no-expire-cleanup01"))
	if !found {
		t.Fatal("task with no expiry should still exist")
	}
}

// ============================================================
// 16. Batch with same task_id across different batches → second is duplicate
// ============================================================

func TestBatchSettlement_DuplicateAcrossBatches(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("dup-cross-user")
	worker := makeAddr("dup-cross-worker")
	taskId := []byte("dup-cross-task-0001")

	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

	entries := []types.SettlementEntry{
		{
			TaskId: taskId, UserAddress: userAddr.String(),
			WorkerAddress: worker.String(),
			Fee:           sdk.NewCoin("ufai", math.NewInt(1_000_000)),
			ExpireBlock:   10000, Status: types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("dc-v1").String(), Pass: true},
				{Address: makeAddr("dc-v2").String(), Pass: true},
				{Address: makeAddr("dc-v3").String(), Pass: true},
			},
		},
	}

	// Batch 1
	msg1 := makeBatchMsg(t, makeAddr("proposer1").String(), entries)
	bid1, _ := k.ProcessBatchSettlement(ctx, msg1)
	br1, _ := k.GetBatchRecord(ctx, bid1)
	if br1.ResultCount != 1 {
		t.Fatalf("batch 1: expected 1 result, got %d", br1.ResultCount)
	}

	// Batch 2 with same task_id
	msg2 := makeBatchMsg(t, makeAddr("proposer2").String(), entries)
	bid2, _ := k.ProcessBatchSettlement(ctx, msg2)
	br2, _ := k.GetBatchRecord(ctx, bid2)
	if br2.ResultCount != 0 {
		t.Fatalf("batch 2: duplicate task should be skipped, got %d results", br2.ResultCount)
	}
}

// ============================================================
// 17. Batch settlement with invalid user address → entry skipped
// ============================================================

func TestBatchSettlement_InvalidUserAddress_Skipped(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	entries := fillTestSigHashes([]types.SettlementEntry{
		{
			TaskId: []byte("invalid-addr-task-01"), UserAddress: "not-a-valid-address",
			WorkerAddress: makeAddr("ia-worker").String(),
			Fee:           sdk.NewCoin("ufai", math.NewInt(1_000_000)),
			ExpireBlock:   10000, Status: types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("ia-v1").String(), Pass: true},
				{Address: makeAddr("ia-v2").String(), Pass: true},
				{Address: makeAddr("ia-v3").String(), Pass: true},
			},
		},
	})

	merkleRoot := keeper.ComputeMerkleRoot(entries)
	msgHash := sha256.Sum256(merkleRoot)
	sig, _ := testProposerKey.Sign(msgHash[:])
	msg := types.NewMsgBatchSettlement(makeAddr("proposer").String(), merkleRoot, entries, sig)

	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("should not error: %v", err)
	}

	br, _ := k.GetBatchRecord(ctx, batchId)
	if br.ResultCount != 0 {
		t.Fatalf("invalid address entry should be skipped, got %d", br.ResultCount)
	}
}

// ============================================================
// 18. Audit pending: delete cleans up both pending key and timeout key
// ============================================================

func TestSecondVerificationPending_DeleteCleansUpBothKeys(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	taskId := []byte("cleanup-pending-001")
	k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
		TaskId:              taskId,
		OriginalStatus:      types.SettlementSuccess,
		SubmittedAt:         50,
		UserAddress:         makeAddr("cp-user").String(),
		WorkerAddress:       makeAddr("cp-worker").String(),
		VerifierAddresses:   []string{makeAddr("cp-v1").String()},
		Fee:                 sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:         10000,
		IsThirdVerification: false,
	})

	// Verify it exists
	_, found := k.GetSecondVerificationPending(ctx, taskId)
	if !found {
		t.Fatal("pending task should exist before delete")
	}

	k.DeleteSecondVerificationPending(ctx, taskId, false)

	_, found = k.GetSecondVerificationPending(ctx, taskId)
	if found {
		t.Fatal("pending task should be deleted")
	}
}

// ============================================================
// 19. ThirdVerification pending: separate from audit pending
// ============================================================

func TestThirdVerificationPending_SeparateFromAudit(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	auditTask := types.SecondVerificationPendingTask{
		TaskId:              []byte("sep-audit-task-0001"),
		OriginalStatus:      types.SettlementSuccess,
		SubmittedAt:         50,
		UserAddress:         makeAddr("sep-user").String(),
		WorkerAddress:       makeAddr("sep-worker").String(),
		VerifierAddresses:   []string{makeAddr("sep-v1").String()},
		Fee:                 sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:         10000,
		IsThirdVerification: false,
	}

	third_verificationTask := types.SecondVerificationPendingTask{
		TaskId:              []byte("sep-third_verification-task001"),
		OriginalStatus:      types.SettlementSuccess,
		SubmittedAt:         60,
		UserAddress:         makeAddr("sep-user").String(),
		WorkerAddress:       makeAddr("sep-worker").String(),
		VerifierAddresses:   []string{makeAddr("sep-v1").String()},
		Fee:                 sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:         10000,
		IsThirdVerification: true,
	}

	k.SetSecondVerificationPending(ctx, auditTask)
	k.SetSecondVerificationPending(ctx, third_verificationTask)

	allAudit := k.GetAllSecondVerificationPending(ctx)
	allThirdVerification := k.GetAllThirdVerificationPending(ctx)

	if len(allAudit) != 1 {
		t.Fatalf("expected 1 audit pending, got %d", len(allAudit))
	}
	if len(allThirdVerification) != 1 {
		t.Fatalf("expected 1 third_verification pending, got %d", len(allThirdVerification))
	}
}

// ============================================================
// 20. Epoch stats: concurrent batches in same epoch accumulate
// ============================================================

func TestEpochStats_MultipleBatchesAccumulate(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("epoch-acc-user")
	worker := makeAddr("epoch-acc-worker")
	fee := sdk.NewCoin("ufai", math.NewInt(100_000))

	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(100_000_000)))

	verifiers := []types.VerifierResult{
		{Address: makeAddr("ea-v1").String(), Pass: true},
		{Address: makeAddr("ea-v2").String(), Pass: true},
		{Address: makeAddr("ea-v3").String(), Pass: true},
	}

	// Batch 1: 2 success tasks
	entries1 := []types.SettlementEntry{
		{TaskId: []byte("epoch-acc-task-0001"), UserAddress: userAddr.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000, Status: types.SettlementSuccess, VerifierResults: verifiers},
		{TaskId: []byte("epoch-acc-task-0002"), UserAddress: userAddr.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000, Status: types.SettlementSuccess, VerifierResults: verifiers},
	}
	msg1 := makeBatchMsg(t, makeAddr("proposer").String(), entries1)
	_, _ = k.ProcessBatchSettlement(ctx, msg1)

	// Batch 2: 1 fail task
	entries2 := []types.SettlementEntry{
		{TaskId: []byte("epoch-acc-task-0003"), UserAddress: userAddr.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000, Status: types.SettlementFail, VerifierResults: verifiers},
	}
	msg2 := makeBatchMsg(t, makeAddr("proposer").String(), entries2)
	_, _ = k.ProcessBatchSettlement(ctx, msg2)

	epoch := ctx.BlockHeight() / 100
	stats := k.GetEpochStats(ctx, epoch)
	if stats.TotalSettled != 3 {
		t.Fatalf("expected total 3, got %d", stats.TotalSettled)
	}
	if stats.FailSettled != 1 {
		t.Fatalf("expected 1 fail, got %d", stats.FailSettled)
	}
}

// ============================================================
// 21. Settlement params: FailSettlementFeeRatio at max (1000) and boundary
// ============================================================

func TestParams_FailSettlementFeeRatio_Boundaries(t *testing.T) {
	tests := []struct {
		name    string
		ratio   uint32
		wantErr bool
	}{
		{"valid_50", 50, false},
		{"valid_1", 1, false},
		{"valid_1000", 1000, false},
		{"invalid_0", 0, true},
		{"invalid_1001", 1001, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := types.DefaultParams()
			p.FailSettlementFeeRatio = tt.ratio
			err := p.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// ============================================================
// 22. Settlement params: SecondVerificationBaseRate boundaries
// ============================================================

func TestParams_SecondVerificationBaseRate_Boundaries(t *testing.T) {
	tests := []struct {
		name    string
		rate    uint32
		wantErr bool
	}{
		{"valid_100", 100, false},
		{"valid_1", 1, false},
		{"valid_1000", 1000, false},
		{"invalid_0", 0, true},
		{"invalid_1001", 1001, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := types.DefaultParams()
			p.SecondVerificationBaseRate = tt.rate
			err := p.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("expected error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// ============================================================
// 23. Settlement params: SecondVerificationRateMin > SecondVerificationRateMax rejected
// ============================================================

func TestParams_AuditRate_MinExceedsMax(t *testing.T) {
	p := types.DefaultParams()
	p.SecondVerificationRateMin = 500
	p.SecondVerificationRateMax = 100
	if err := p.Validate(); err == nil {
		t.Fatal("second_verification_rate_min > second_verification_rate_max should fail validation")
	}
}

// ============================================================
// 24. Settlement params: SecondVerificationTimeout=0 rejected, negative rejected
// ============================================================

func TestParams_SecondVerificationTimeout_Boundaries(t *testing.T) {
	p := types.DefaultParams()
	p.SecondVerificationTimeout = 0
	if err := p.Validate(); err == nil {
		t.Fatal("second_verification_timeout=0 should fail validation")
	}

	p.SecondVerificationTimeout = -1
	if err := p.Validate(); err == nil {
		t.Fatal("negative second_verification_timeout should fail validation")
	}
}

// ============================================================
// 25. Settlement params: ThirdVerificationTimeout boundaries
// ============================================================

func TestParams_ThirdVerificationTimeout_Boundaries(t *testing.T) {
	p := types.DefaultParams()
	p.ThirdVerificationTimeout = 0
	if err := p.Validate(); err == nil {
		t.Fatal("third_verification_timeout=0 should fail validation")
	}

	p.ThirdVerificationTimeout = -1
	if err := p.Validate(); err == nil {
		t.Fatal("negative third_verification_timeout should fail validation")
	}
}

// ============================================================
// 26. HandleSecondVerificationTimeouts with no pending tasks
// ============================================================

func TestHandleSecondVerificationTimeouts_NoPendingTasks(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	ctx = ctx.WithBlockHeight(100000)
	count := k.HandleSecondVerificationTimeouts(ctx)
	if count != 0 {
		t.Fatalf("expected 0 timeouts with no pending tasks, got %d", count)
	}
}

// ============================================================
// 27. Audit pending at exact timeout boundary
// ============================================================

func TestHandleSecondVerificationTimeouts_ExactBoundary(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	params := k.GetParams(ctx)
	params.SecondVerificationTimeout = 100
	params.ThirdVerificationTimeout = 200
	k.SetParams(ctx, params)

	// Task submitted at height 100, timeout=100 → times out at height > 200
	k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
		TaskId:            []byte("exact-boundary-task"),
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       100,
		UserAddress:       makeAddr("eb-user").String(),
		WorkerAddress:     makeAddr("eb-worker").String(),
		VerifierAddresses: []string{makeAddr("eb-v").String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(100_000)),
		ExpireBlock:       100000,
	})

	// At height 200: cutoff = 200-100=100. Task at height 100 <= 100 → times out
	ctx = ctx.WithBlockHeight(200)
	count := k.HandleSecondVerificationTimeouts(ctx)
	if count != 1 {
		t.Fatalf("exact boundary: expected 1 timeout, got %d", count)
	}
}

// ============================================================
// 28. Large batch: 100 entries, mix of success/fail/expired/fraud
// ============================================================

func TestBatchSettlement_LargeMixedBatch(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("large-batch-user")
	worker := makeAddr("large-batch-worker")
	fee := sdk.NewCoin("ufai", math.NewInt(10_000))

	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(1_000_000_000)))

	verifiers := []types.VerifierResult{
		{Address: makeAddr("lb-v1").String(), Pass: true},
		{Address: makeAddr("lb-v2").String(), Pass: true},
		{Address: makeAddr("lb-v3").String(), Pass: true},
	}

	var entries []types.SettlementEntry
	for i := 0; i < 50; i++ {
		entries = append(entries, types.SettlementEntry{
			TaskId: []byte(fmt.Sprintf("large-batch-s-task%02d", i)), UserAddress: userAddr.String(),
			WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000,
			Status: types.SettlementSuccess, VerifierResults: verifiers,
		})
	}
	for i := 0; i < 20; i++ {
		entries = append(entries, types.SettlementEntry{
			TaskId: []byte(fmt.Sprintf("large-batch-f-task%02d", i)), UserAddress: userAddr.String(),
			WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000,
			Status: types.SettlementFail, VerifierResults: verifiers,
		})
	}
	for i := 0; i < 10; i++ {
		entries = append(entries, types.SettlementEntry{
			TaskId: []byte(fmt.Sprintf("large-batch-e-task%02d", i)), UserAddress: userAddr.String(),
			WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 50, // expired
			Status: types.SettlementSuccess, VerifierResults: verifiers,
		})
	}

	// Mark 5 tasks as fraud
	for i := 0; i < 5; i++ {
		k.SetFraudMark(ctx, []byte(fmt.Sprintf("large-batch-s-task%02d", i)))
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	br, _ := k.GetBatchRecord(ctx, batchId)
	// 50 success - 5 fraud = 45 success + 20 fail = 65 total
	// 10 expired are skipped
	if br.ResultCount != 65 {
		t.Fatalf("expected 65 results (45 success + 20 fail), got %d", br.ResultCount)
	}
}

// ============================================================
// 29. Tiny fee: ensure no panics with 1 ufai fee
// ============================================================

func TestBatchSettlement_TinyFee_NoPanic(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("tiny-fee-user")
	worker := makeAddr("tiny-fee-worker")

	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(100)))

	entries := []types.SettlementEntry{
		{
			TaskId: []byte("tiny-fee-task-00001"), UserAddress: userAddr.String(),
			WorkerAddress: worker.String(),
			Fee:           sdk.NewCoin("ufai", math.NewInt(1)), // 1 ufai
			ExpireBlock:   10000, Status: types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("tf-v1").String(), Pass: true},
				{Address: makeAddr("tf-v2").String(), Pass: true},
				{Address: makeAddr("tf-v3").String(), Pass: true},
			},
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("tiny fee should not cause error: %v", err)
	}
}

// ============================================================
// 30. FraudProof with invalid worker address
// ============================================================

func TestFraudProof_InvalidWorkerAddress(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	msg := &types.MsgFraudProof{
		Reporter:         makeAddr("reporter").String(),
		TaskId:           []byte("fraud-invalid-worker"),
		WorkerAddress:    "not-a-valid-address",
		ContentHash:      []byte("h"),
		WorkerContentSig: []byte("s"),
		ActualContent:    []byte("c"),
	}

	err := k.ProcessFraudProof(ctx, msg)
	if err == nil {
		t.Fatal("expected error for invalid worker address")
	}
}

// ============================================================
// 31. Process audit result: extra results after threshold → ignored
// ============================================================

func TestProcessSecondVerificationResult_AfterThreshold_Ignored(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	taskId := []byte("aud-threshold-task01")

	// Submit 3 results (threshold)
	for i := 0; i < 3; i++ {
		_ = k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
			SecondVerifier: makeAddr(fmt.Sprintf("at-aud%d", i)).String(),
			TaskId:         taskId,
			Epoch:          1,
			Pass:           true,
			LogitsHash:     []byte("hash"),
		})
	}

	// 4th result should be silently ignored
	err := k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
		SecondVerifier: makeAddr("at-aud-extra").String(),
		TaskId:         taskId,
		Epoch:          1,
		Pass:           false,
		LogitsHash:     []byte("hash"),
	})
	if err != nil {
		t.Fatalf("extra audit result should not error: %v", err)
	}

	ar, _ := k.GetSecondVerificationRecord(ctx, taskId)
	if len(ar.Results) != 3 {
		t.Fatalf("expected 3 results (extra ignored), got %d", len(ar.Results))
	}
}

// ============================================================
// 32. Withdraw exact balance → zero balance, account still exists
// ============================================================

func TestWithdraw_ExactBalance_AccountExists(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	addr := makeAddr("exact-withdraw")
	_ = k.ProcessDeposit(ctx, addr, sdk.NewCoin("ufai", math.NewInt(1000)))

	err := k.ProcessWithdraw(ctx, addr, sdk.NewCoin("ufai", math.NewInt(1000)))
	if err != nil {
		t.Fatalf("exact withdraw should succeed: %v", err)
	}

	ia, found := k.GetInferenceAccount(ctx, addr)
	if !found {
		t.Fatal("account should still exist after full withdrawal")
	}
	if !ia.Balance.IsZero() {
		t.Fatalf("expected zero balance, got %s", ia.Balance)
	}
}

// ============================================================
// 33. Multiple deposits then multiple withdrawals
// ============================================================

func TestDepositWithdraw_MultipleOperations(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	addr := makeAddr("multi-op-user")

	_ = k.ProcessDeposit(ctx, addr, sdk.NewCoin("ufai", math.NewInt(1000)))
	_ = k.ProcessDeposit(ctx, addr, sdk.NewCoin("ufai", math.NewInt(2000)))
	_ = k.ProcessDeposit(ctx, addr, sdk.NewCoin("ufai", math.NewInt(3000)))

	ia, _ := k.GetInferenceAccount(ctx, addr)
	if !ia.Balance.Amount.Equal(math.NewInt(6000)) {
		t.Fatalf("expected 6000, got %s", ia.Balance.Amount)
	}

	_ = k.ProcessWithdraw(ctx, addr, sdk.NewCoin("ufai", math.NewInt(1500)))
	_ = k.ProcessWithdraw(ctx, addr, sdk.NewCoin("ufai", math.NewInt(2500)))

	ia, _ = k.GetInferenceAccount(ctx, addr)
	if !ia.Balance.Amount.Equal(math.NewInt(2000)) {
		t.Fatalf("expected 2000 after withdrawals, got %s", ia.Balance.Amount)
	}

	// Try to withdraw more than remaining
	err := k.ProcessWithdraw(ctx, addr, sdk.NewCoin("ufai", math.NewInt(3000)))
	if err == nil {
		t.Fatal("should error when withdrawing more than balance")
	}
}

// ============================================================
// 34. Batch record counter increments correctly across multiple batches
// ============================================================

func TestBatchCounter_IncrementsCorrectly(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("counter-user")
	worker := makeAddr("counter-worker")
	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(100_000_000)))

	verifiers := []types.VerifierResult{
		{Address: makeAddr("cnt-v1").String(), Pass: true},
		{Address: makeAddr("cnt-v2").String(), Pass: true},
		{Address: makeAddr("cnt-v3").String(), Pass: true},
	}

	for i := 0; i < 5; i++ {
		entries := []types.SettlementEntry{
			{
				TaskId:      []byte(fmt.Sprintf("counter-task-%05d--", i)),
				UserAddress: userAddr.String(), WorkerAddress: worker.String(),
				Fee: sdk.NewCoin("ufai", math.NewInt(1000)), ExpireBlock: 10000,
				Status: types.SettlementSuccess, VerifierResults: verifiers,
			},
		}
		msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
		batchId, _ := k.ProcessBatchSettlement(ctx, msg)
		if batchId != uint64(i+1) {
			t.Fatalf("batch %d: expected id %d, got %d", i, i+1, batchId)
		}
	}

	nextId := k.GetNextBatchId(ctx)
	if nextId != 6 {
		t.Fatalf("expected next batch id 6, got %d", nextId)
	}
}

// ============================================================
// A7. Wrong denom deposit: account created with uatom balance,
//     settlement with ufai fee skips entry due to balance mismatch
// ============================================================

func TestProcessDeposit_WrongDenom(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	userAddr := makeAddr("wrongdenom-user")

	// Deposit with wrong denom "uatom" — now rejected at keeper level (A7 fix)
	err := k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("uatom", math.NewInt(5_000_000)))
	if err == nil {
		t.Fatal("expected error for cross-denom deposit, got nil")
	}

	// Verify account was NOT created
	_, found := k.GetInferenceAccount(ctx, userAddr)
	if found {
		t.Fatal("account should not exist after rejected cross-denom deposit")
	}
}

// ============================================================
// H5. FraudProof on a task in PENDING_AUDIT state:
//     fraud mark set, worker slashed
// ============================================================

func TestFraudProof_OnPendingAuditTask(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 1000) // 100% → all tasks go to PENDING_AUDIT

	userAddr := makeAddr("fpaudit-user")
	workerAddr := makeAddr("fpaudit-worker")

	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

	// Settle with 100% audit rate so task goes to PENDING_AUDIT
	taskId := []byte("fpaudit-task-000001")
	entries := []types.SettlementEntry{
		{
			TaskId: taskId, UserAddress: userAddr.String(),
			WorkerAddress: workerAddr.String(),
			Fee:           sdk.NewCoin("ufai", math.NewInt(1_000_000)),
			ExpireBlock:   10000, Status: types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("fpa-v1").String(), Pass: true},
				{Address: makeAddr("fpa-v2").String(), Pass: true},
				{Address: makeAddr("fpa-v3").String(), Pass: true},
			},
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("batch should not error: %v", err)
	}

	// Verify task is in PENDING_AUDIT
	_, pendingFound := k.GetSecondVerificationPending(ctx, taskId)
	if !pendingFound {
		t.Fatal("task should be in PENDING_AUDIT with 100% audit rate")
	}

	// Submit FraudProof for the pending audit task
	chFraud, csFraud := signFraudContent(t, []byte("fraud-content"))
	err = k.ProcessFraudProof(ctx, &types.MsgFraudProof{
		Reporter:         userAddr.String(),
		TaskId:           taskId,
		WorkerAddress:    workerAddr.String(),
		ContentHash:      chFraud,
		WorkerContentSig: csFraud,
		ActualContent:    []byte("fraud-content"),
	})
	if err != nil {
		t.Fatalf("fraud proof on pending audit task should succeed: %v", err)
	}

	// Verify fraud mark is set
	if !k.HasFraudMark(ctx, taskId) {
		t.Fatal("fraud mark should be set after fraud proof")
	}

	// Verify worker was slashed
	if len(wk.slashCalls) < 1 {
		t.Fatal("worker should be slashed on fraud proof")
	}
	slashed := false
	for _, addr := range wk.slashCalls {
		if addr.Equals(workerAddr) {
			slashed = true
			break
		}
	}
	if !slashed {
		t.Fatal("the correct worker should be slashed")
	}
}

// ============================================================
// L2. Exact balance equals fee: deposit exactly fee amount,
//     settle one SUCCESS task, balance should be zero after
// ============================================================

func TestBatchSettlement_ExactBalanceEqualsFee(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("exactbal-user")
	workerAddr := makeAddr("exactbal-worker")
	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))

	// Deposit exactly the fee amount
	_ = k.ProcessDeposit(ctx, userAddr, fee)

	entries := []types.SettlementEntry{
		{
			TaskId: []byte("exactbal-task-00001"), UserAddress: userAddr.String(),
			WorkerAddress: workerAddr.String(),
			Fee:           fee, ExpireBlock: 10000,
			Status: types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("eb-v1").String(), Pass: true},
				{Address: makeAddr("eb-v2").String(), Pass: true},
				{Address: makeAddr("eb-v3").String(), Pass: true},
			},
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	br, _ := k.GetBatchRecord(ctx, batchId)
	if br.ResultCount != 1 {
		t.Fatalf("expected 1 result, got %d", br.ResultCount)
	}

	ia, found := k.GetInferenceAccount(ctx, userAddr)
	if !found {
		t.Fatal("account should still exist")
	}
	if !ia.Balance.IsZero() {
		t.Fatalf("expected zero balance after exact fee deduction, got %s", ia.Balance)
	}
}

// ============================================================
// L3. FAIL task with balance less than 15% fail fee:
//     task should be skipped, balance unchanged
// ============================================================

func TestBatchSettlement_FailTaskOverspend(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("failoverspd-user")
	workerAddr := makeAddr("failoverspd-wkr")

	// Deposit 140_000 ufai. Fail fee = 1_000_000 * 150/1000 = 150_000. 140_000 < 150_000 → skip
	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(140_000)))

	entries := []types.SettlementEntry{
		{
			TaskId: []byte("failoverspd-task-01"), UserAddress: userAddr.String(),
			WorkerAddress: workerAddr.String(),
			Fee:           sdk.NewCoin("ufai", math.NewInt(1_000_000)),
			ExpireBlock:   10000, Status: types.SettlementFail,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("fo-v1").String(), Pass: true},
				{Address: makeAddr("fo-v2").String(), Pass: false},
				{Address: makeAddr("fo-v3").String(), Pass: false},
			},
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("should not error: %v", err)
	}

	br, _ := k.GetBatchRecord(ctx, batchId)
	if br.ResultCount != 0 {
		t.Fatalf("expected 0 results (insufficient for fail fee), got %d", br.ResultCount)
	}

	ia, _ := k.GetInferenceAccount(ctx, userAddr)
	if !ia.Balance.Amount.Equal(math.NewInt(140_000)) {
		t.Fatalf("balance should be unchanged at 140_000, got %s", ia.Balance.Amount)
	}

	if len(wk.jailCalls) != 0 {
		t.Fatalf("no jail for skipped fail task, got %d", len(wk.jailCalls))
	}
}

// ============================================================
// V1. Fee conservation: using tracking keeper, verify total
//     fee conservation across SUCCESS and FAIL tasks.
//     Total user balance change == total distributed via bank sends.
// ============================================================

func TestFeeConservation_SuccessAndFail(t *testing.T) {
	k, ctx, bk, _ := setupTrackingKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("conserv-user")
	workerAddr := makeAddr("conserv-worker")
	v1 := makeAddr("conserv-v1")
	v2 := makeAddr("conserv-v2")
	v3 := makeAddr("conserv-v3")
	verifiers := []string{v1.String(), v2.String(), v3.String()}

	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	// 10 SUCCESS tasks + 2 FAIL tasks
	// SUCCESS total fee: 10 * 1_000_000 = 10_000_000
	// FAIL total fee: 2 * 1_000_000 * 50/1000 = 100_000
	// Total deducted: 10_100_000
	totalDeposit := math.NewInt(20_000_000) // plenty of balance
	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", totalDeposit))

	// Build 10 SUCCESS + 2 FAIL entries
	successEntries := integrationEntries(userAddr.String(), workerAddr.String(), verifiers, 10, types.SettlementSuccess, fee, 10000, 0)
	failEntries := integrationEntries(userAddr.String(), workerAddr.String(), verifiers, 2, types.SettlementFail, fee, 10000, 100)

	allEntries := append(successEntries, failEntries...)
	msg := integrationBatchMsg(makeAddr("proposer").String(), allEntries)

	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Calculate expected user deduction
	// SUCCESS: 10 * 1_000_000 = 10_000_000
	successDeduction := math.NewInt(10_000_000)
	// FAIL: 2 * 1_000_000 * 150/1000 = 300_000 (15% fail fee)
	failDeduction := math.NewInt(1_000_000).MulRaw(150).QuoRaw(1000).MulRaw(2)
	totalDeduction := successDeduction.Add(failDeduction)

	ia, _ := k.GetInferenceAccount(ctx, userAddr)
	actualDeduction := totalDeposit.Sub(ia.Balance.Amount)
	if !actualDeduction.Equal(totalDeduction) {
		t.Fatalf("user deduction mismatch: expected %s, got %s", totalDeduction, actualDeduction)
	}

	// Verify total distributed via bank + multi-verification fund == total deduction from user balance.
	// Multi-verification fund (3%) stays in module account and is NOT sent via SendCoinsFromModuleToAccount,
	// so the tracking bank does not record it. We must account for it separately.
	totalDistributed := math.ZeroInt()
	for _, amt := range bk.received {
		totalDistributed = totalDistributed.Add(amt)
	}

	// Module-retained funds (multi-verification fund only, no burn): not sent via bank, stays in module account.
	// Ratios: executor=850, verifier=120, audit=30 (per-mille).
	// For SUCCESS: fund = fee * 30/1000 = 30_000 per task × 10 = 300_000
	// For FAIL: failFee = fee * 150/1000 = 150_000. Verifiers get 150_000*120/150 = 120_000. Module retains 150_000-120_000=30_000 per task × 2 = 60_000
	totalMultiVerificationFund := math.NewInt(30_000).MulRaw(10).Add(math.NewInt(30_000).MulRaw(2)) // 360_000

	totalAccountedFor := totalDistributed.Add(totalMultiVerificationFund)
	if !totalAccountedFor.Equal(totalDeduction) {
		t.Fatalf("fee conservation violated: user deducted %s but distributed %s + audit fund %s = %s",
			totalDeduction, totalDistributed, totalMultiVerificationFund, totalAccountedFor)
	}
}

// ============================================================
// V4. Two separate batches in same block with overlapping task_id:
//     first batch settles all, second batch skips duplicates
// ============================================================

func TestBatchSettlement_SameBlockDuplicateBatch(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("dupbatch-user")
	workerAddr := makeAddr("dupbatch-worker")

	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(50_000_000)))

	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	verifiers := []types.VerifierResult{
		{Address: makeAddr("db-v1").String(), Pass: true},
		{Address: makeAddr("db-v2").String(), Pass: true},
		{Address: makeAddr("db-v3").String(), Pass: true},
	}

	taskA := []byte("dupbatch-task-A-pad")
	taskB := []byte("dupbatch-task-B-pad")
	taskC := []byte("dupbatch-task-C-pad")

	// First batch: task-A, task-B
	entries1 := []types.SettlementEntry{
		{
			TaskId: taskA, UserAddress: userAddr.String(),
			WorkerAddress: workerAddr.String(), Fee: fee, ExpireBlock: 10000,
			Status: types.SettlementSuccess, VerifierResults: verifiers,
		},
		{
			TaskId: taskB, UserAddress: userAddr.String(),
			WorkerAddress: workerAddr.String(), Fee: fee, ExpireBlock: 10000,
			Status: types.SettlementSuccess, VerifierResults: verifiers,
		},
	}

	msg1 := makeBatchMsg(t, makeAddr("proposer").String(), entries1)
	batchId1, err := k.ProcessBatchSettlement(ctx, msg1)
	if err != nil {
		t.Fatalf("batch 1 unexpected error: %v", err)
	}

	br1, _ := k.GetBatchRecord(ctx, batchId1)
	if br1.ResultCount != 2 {
		t.Fatalf("batch 1: expected 2 results, got %d", br1.ResultCount)
	}

	// Verify task-A and task-B are settled
	if _, found := k.GetSettledTask(ctx, taskA); !found {
		t.Fatal("task-A should be settled after batch 1")
	}
	if _, found := k.GetSettledTask(ctx, taskB); !found {
		t.Fatal("task-B should be settled after batch 1")
	}

	// Second batch: task-B (duplicate), task-C (new)
	entries2 := []types.SettlementEntry{
		{
			TaskId: taskB, UserAddress: userAddr.String(),
			WorkerAddress: workerAddr.String(), Fee: fee, ExpireBlock: 10000,
			Status: types.SettlementSuccess, VerifierResults: verifiers,
		},
		{
			TaskId: taskC, UserAddress: userAddr.String(),
			WorkerAddress: workerAddr.String(), Fee: fee, ExpireBlock: 10000,
			Status: types.SettlementSuccess, VerifierResults: verifiers,
		},
	}

	msg2 := makeBatchMsg(t, makeAddr("proposer").String(), entries2)
	batchId2, err := k.ProcessBatchSettlement(ctx, msg2)
	if err != nil {
		t.Fatalf("batch 2 unexpected error: %v", err)
	}

	br2, _ := k.GetBatchRecord(ctx, batchId2)
	if br2.ResultCount != 1 {
		t.Fatalf("batch 2: expected 1 result (task-B duplicate skipped), got %d", br2.ResultCount)
	}

	// Verify task-C is now settled
	if _, found := k.GetSettledTask(ctx, taskC); !found {
		t.Fatal("task-C should be settled after batch 2")
	}

	// Total user deduction: 3 tasks * 1_000_000 = 3_000_000
	ia, _ := k.GetInferenceAccount(ctx, userAddr)
	expected := math.NewInt(50_000_000 - 3_000_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("expected balance %s after 3 settled tasks, got %s", expected, ia.Balance.Amount)
	}
}

// ============================================================
// J5. CalculateSecondVerificationRate with zero total tasks:
//     no divide-by-zero panic, returns base rate (100)
// ============================================================

func TestCalculateSecondVerificationRate_ZeroTotalTasks(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	// Case 1: TotalSettled=0, FailSettled=0 → should return base rate, no panic
	stats := types.EpochStats{
		Epoch:        50,
		TotalSettled: 0,
		FailSettled:  0,
	}
	k.SetEpochStats(ctx, stats)

	rate := k.CalculateSecondVerificationRate(ctx, 50)
	if rate != 100 {
		t.Fatalf("zero total tasks: expected base rate 100, got %d", rate)
	}

	// Case 2: FailSettled=5, TotalSettled=0 → edge case, still no panic
	stats2 := types.EpochStats{
		Epoch:        51,
		TotalSettled: 0,
		FailSettled:  5,
	}
	k.SetEpochStats(ctx, stats2)

	rate2 := k.CalculateSecondVerificationRate(ctx, 51)
	// Should not panic and should return a valid rate (base or clamped)
	if rate2 < 100 || rate2 > 300 {
		t.Fatalf("zero total with nonzero fail: expected rate in [100,300], got %d", rate2)
	}
}
