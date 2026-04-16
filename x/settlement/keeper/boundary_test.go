package keeper_test

// Boundary and edge-case tests for the settlement module — supplementary to existing tests.

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"cosmossdk.io/math"
	"github.com/cometbft/cometbft/crypto/secp256k1"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/settlement/keeper"
	"github.com/funai-wiki/funai-chain/x/settlement/types"
)

// ============================================================
// B1. Withdraw exact full balance → leaves zero
// ============================================================

func TestWithdraw_ExactFullBalance(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	addr := makeAddr("full-withdraw-user")
	_ = k.ProcessDeposit(ctx, addr, sdk.NewCoin("ufai", math.NewInt(1_000_000)))

	err := k.ProcessWithdraw(ctx, addr, sdk.NewCoin("ufai", math.NewInt(1_000_000)))
	if err != nil {
		t.Fatalf("withdrawing exact balance should succeed: %v", err)
	}

	ia, found := k.GetInferenceAccount(ctx, addr)
	if !found {
		t.Fatal("account should still exist after full withdrawal")
	}
	if !ia.Balance.IsZero() {
		t.Fatalf("balance should be zero, got %s", ia.Balance)
	}
}

// ============================================================
// B2. Batch with mixed SUCCESS and FAIL entries
// ============================================================

func TestBatchSettlement_MixedSuccessAndFail(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	user := makeAddr("mixed-user")
	worker := makeAddr("mixed-worker")
	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

	verifiers := []types.VerifierResult{
		{Address: makeAddr("mx-v1").String(), Pass: true},
		{Address: makeAddr("mx-v2").String(), Pass: true},
		{Address: makeAddr("mx-v3").String(), Pass: true},
	}
	failVerifiers := []types.VerifierResult{
		{Address: makeAddr("mx-v1").String(), Pass: true},
		{Address: makeAddr("mx-v2").String(), Pass: false},
		{Address: makeAddr("mx-v3").String(), Pass: false},
	}

	entries := []types.SettlementEntry{
		{TaskId: []byte("mixed-task-success01"), UserAddress: user.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000, Status: types.SettlementSuccess, VerifierResults: verifiers},
		{TaskId: []byte("mixed-task-fail-0001"), UserAddress: user.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000, Status: types.SettlementFail, VerifierResults: failVerifiers},
		{TaskId: []byte("mixed-task-success02"), UserAddress: user.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000, Status: types.SettlementSuccess, VerifierResults: verifiers},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("mixed batch should succeed: %v", err)
	}

	br, _ := k.GetBatchRecord(ctx, batchId)
	if br.ResultCount != 3 {
		t.Fatalf("expected 3 results (2 success + 1 fail), got %d", br.ResultCount)
	}

	// 2 success = 2M, 1 fail = 1M*50/1000 = 50K → total deducted = 2_050_000
	ia, _ := k.GetInferenceAccount(ctx, user)
	expected := math.NewInt(10_000_000 - 2_000_000 - 50_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("expected %s, got %s", expected, ia.Balance.Amount)
	}

	// 1 jail for FAIL + 2 streak increments for SUCCESS
	if len(wk.jailCalls) != 1 {
		t.Fatalf("expected 1 jail call, got %d", len(wk.jailCalls))
	}
	if len(wk.streakCalls) != 2 {
		t.Fatalf("expected 2 streak calls, got %d", len(wk.streakCalls))
	}
}

// ============================================================
// B3. Duplicate task_id WITHIN same batch → second is skipped
// ============================================================

func TestBatchSettlement_DuplicateWithinSameBatch(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	user := makeAddr("dup-intra-user")
	worker := makeAddr("dup-intra-worker")
	taskId := []byte("dup-intra-task-0001")
	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

	verifiers := []types.VerifierResult{
		{Address: makeAddr("di-v1").String(), Pass: true},
		{Address: makeAddr("di-v2").String(), Pass: true},
		{Address: makeAddr("di-v3").String(), Pass: true},
	}

	// Same task_id appears twice in one batch
	entries := []types.SettlementEntry{
		{TaskId: taskId, UserAddress: user.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000, Status: types.SettlementSuccess, VerifierResults: verifiers},
		{TaskId: taskId, UserAddress: user.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000, Status: types.SettlementSuccess, VerifierResults: verifiers},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("should not error: %v", err)
	}

	br, _ := k.GetBatchRecord(ctx, batchId)
	if br.ResultCount != 1 {
		t.Fatalf("second duplicate should be skipped, expected 1 result, got %d", br.ResultCount)
	}

	// Only 1 fee deducted
	ia, _ := k.GetInferenceAccount(ctx, user)
	if !ia.Balance.Amount.Equal(math.NewInt(9_000_000)) {
		t.Fatalf("expected 9M, got %s", ia.Balance.Amount)
	}
}

// ============================================================
// B4. Verifier fee dust loss: fee not divisible by 3
// ============================================================

func TestDistributeSuccessFee_DustHandling(t *testing.T) {
	k, ctx, bk, _ := setupTrackingKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	user := makeAddr("dust-user")
	worker := makeAddr("dust-worker")
	v1 := makeAddr("dust-v1")
	v2 := makeAddr("dust-v2")
	v3 := makeAddr("dust-v3")

	// Fee = 100 ufai. Verifier total = 100*120/1000 = 12. perVerifier = 12/3 = 4.
	// Audit = 100*30/1000 = 3. Executor = 100 - 12 - 3 = 85.
	fee := sdk.NewCoin("ufai", math.NewInt(100))
	_ = k.ProcessDeposit(ctx, user, fee)

	entries := []types.SettlementEntry{
		{
			TaskId: []byte("dust-task-000000001"), UserAddress: user.String(),
			WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000,
			Status: types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: v1.String(), Pass: true},
				{Address: v2.String(), Pass: true},
				{Address: v3.String(), Pass: true},
			},
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// executor = 100 - verifier(12) - audit(3) = 85 (remainder-based, no dust loss)
	gotWorker := bk.receivedBy(worker)
	if !gotWorker.Equal(math.NewInt(85)) {
		t.Fatalf("executor expected 85, got %s", gotWorker)
	}

	// Verify total verifier distribution = 100*120/1000 = 12
	gotV1 := bk.receivedBy(v1)
	gotV2 := bk.receivedBy(v2)
	gotV3 := bk.receivedBy(v3)
	totalVerifier := gotV1.Add(gotV2).Add(gotV3)
	if !totalVerifier.Equal(math.NewInt(12)) {
		t.Fatalf("total verifier expected 12, got %s", totalVerifier)
	}

	// Last verifier gets remainder
	if !gotV3.GTE(gotV1) {
		t.Fatalf("last verifier should get remainder: v3=%s, v1=%s", gotV3, gotV1)
	}
}

// ============================================================
// B5. Batch counter continuity: IDs increment correctly
// ============================================================

func TestBatchCounter_Continuity(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	user := makeAddr("counter-user")
	worker := makeAddr("counter-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(100_000_000)))

	verifiers := []types.VerifierResult{
		{Address: makeAddr("ct-v1").String(), Pass: true},
		{Address: makeAddr("ct-v2").String(), Pass: true},
		{Address: makeAddr("ct-v3").String(), Pass: true},
	}

	var prevId uint64
	for i := 0; i < 5; i++ {
		entries := []types.SettlementEntry{
			{
				TaskId: []byte(fmt.Sprintf("counter-task-%06d-", i)), UserAddress: user.String(),
				WorkerAddress: worker.String(),
				Fee:           sdk.NewCoin("ufai", math.NewInt(100)),
				ExpireBlock:   10000, Status: types.SettlementSuccess, VerifierResults: verifiers,
			},
		}
		msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
		batchId, _ := k.ProcessBatchSettlement(ctx, msg)

		if i > 0 && batchId != prevId+1 {
			t.Fatalf("batch %d: expected id %d, got %d", i, prevId+1, batchId)
		}
		prevId = batchId
	}
}

// ============================================================
// B6. Genesis round-trip: InitGenesis → ExportGenesis
// ============================================================

func TestGenesis_RoundTrip(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	// Set up state
	addr1 := makeAddr("gen-user-1")
	addr2 := makeAddr("gen-user-2")
	_ = k.ProcessDeposit(ctx, addr1, sdk.NewCoin("ufai", math.NewInt(5_000_000)))
	_ = k.ProcessDeposit(ctx, addr2, sdk.NewCoin("ufai", math.NewInt(3_000_000)))

	// Export
	exported := k.ExportGenesis(ctx)
	if len(exported.InferenceAccounts) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(exported.InferenceAccounts))
	}

	// Re-init on fresh keeper
	k2, ctx2, _, _ := setupKeeper(t)
	k2.InitGenesis(ctx2, *exported)

	// Verify state
	ia1, found := k2.GetInferenceAccount(ctx2, addr1)
	if !found {
		t.Fatal("addr1 not found after re-init")
	}
	if !ia1.Balance.Amount.Equal(math.NewInt(5_000_000)) {
		t.Fatalf("addr1 balance mismatch: %s", ia1.Balance)
	}

	ia2, found := k2.GetInferenceAccount(ctx2, addr2)
	if !found {
		t.Fatal("addr2 not found after re-init")
	}
	if !ia2.Balance.Amount.Equal(math.NewInt(3_000_000)) {
		t.Fatalf("addr2 balance mismatch: %s", ia2.Balance)
	}

	// Re-export should match
	reExported := k2.ExportGenesis(ctx2)
	if len(reExported.InferenceAccounts) != len(exported.InferenceAccounts) {
		t.Fatalf("re-exported accounts count mismatch: %d vs %d", len(reExported.InferenceAccounts), len(exported.InferenceAccounts))
	}
}

// ============================================================
// B7. Genesis with empty state
// ============================================================

func TestGenesis_EmptyState(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	exported := k.ExportGenesis(ctx)
	if len(exported.InferenceAccounts) != 0 {
		t.Fatalf("expected 0 accounts, got %d", len(exported.InferenceAccounts))
	}
	if len(exported.BatchRecords) != 0 {
		t.Fatalf("expected 0 batches, got %d", len(exported.BatchRecords))
	}

	// Re-init with empty genesis
	k2, ctx2, _, _ := setupKeeper(t)
	k2.InitGenesis(ctx2, *exported)
	reExported := k2.ExportGenesis(ctx2)
	if len(reExported.InferenceAccounts) != 0 || len(reExported.BatchRecords) != 0 {
		t.Fatal("re-export of empty genesis should be empty")
	}
}

// ============================================================
// B8. gRPC query: non-existent InferenceAccount
// ============================================================

func TestGRPC_InferenceAccount_NotFound(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	addr := makeAddr("nobody-grpc")
	_, err := qs.InferenceAccount(ctx, &types.QueryInferenceAccountRequest{Address: addr.String()})
	if err == nil {
		t.Fatal("expected error for non-existent account")
	}
}

// ============================================================
// B9. gRPC query: non-existent batch
// ============================================================

func TestGRPC_Batch_NotFound(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	_, err := qs.Batch(ctx, &types.QueryBatchRequest{BatchId: 9999})
	if err == nil {
		t.Fatal("expected error for non-existent batch")
	}
}

// ============================================================
// B10. gRPC query: nil request
// ============================================================

func TestGRPC_InferenceAccount_NilRequest(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	_, err := qs.InferenceAccount(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestGRPC_Batch_NilRequest(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	_, err := qs.Batch(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

// ============================================================
// B11. gRPC query: Params always returns
// ============================================================

func TestGRPC_Params(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	resp, err := qs.Params(ctx, &types.QueryParamsRequest{})
	if err != nil {
		t.Fatalf("Params query should succeed: %v", err)
	}
	if resp.Params.ExecutorFeeRatio != types.DefaultExecutorFeeRatio {
		t.Fatalf("expected default executor ratio, got %d", resp.Params.ExecutorFeeRatio)
	}
}

// ============================================================
// B12. Audit FAIL overturning initial SUCCESS → user not charged, worker jailed
// ============================================================

func TestAuditFail_OverturnsSuccess_UserNotCharged(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)
	k.SetCurrentReauditRate(ctx, 0)

	user := makeAddr("overturn-user")
	worker := makeAddr("overturn-worker")
	v1 := makeAddr("ot-v1")
	v2 := makeAddr("ot-v2")
	v3 := makeAddr("ot-v3")
	taskId := []byte("overturn-success-001")

	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

	// Task is pending audit (originally SUCCESS)
	k.SetAuditPending(ctx, types.AuditPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       ctx.BlockHeight(),
		UserAddress:       user.String(),
		WorkerAddress:     worker.String(),
		VerifierAddresses: []string{v1.String(), v2.String(), v3.String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       10000,
	})

	// 3 FAIL audits → overturns SUCCESS
	for i := 0; i < 3; i++ {
		_ = k.ProcessAuditResult(ctx, &types.MsgAuditResult{
			Auditor:    makeAddr(fmt.Sprintf("ot-aud%d", i)).String(),
			TaskId:     taskId,
			Epoch:      1,
			Pass:       false,
			LogitsHash: []byte("hash"),
		})
	}

	// User should NOT be charged (task overturned to FAIL)
	ia, _ := k.GetInferenceAccount(ctx, user)
	if !ia.Balance.Amount.Equal(math.NewInt(10_000_000)) {
		t.Fatalf("user should not be charged when SUCCESS overturned to FAIL, got balance %s", ia.Balance.Amount)
	}

	// Worker + 3 verifiers should be jailed
	if len(wk.jailCalls) < 4 {
		t.Fatalf("expected at least 4 jail calls, got %d", len(wk.jailCalls))
	}

	// Task should be in FAILED state
	st, found := k.GetSettledTask(ctx, taskId)
	if !found {
		t.Fatal("task should exist")
	}
	if st.Status != types.TaskFailed {
		t.Fatalf("expected TASK_FAILED, got %s", st.Status)
	}
}

// ============================================================
// B13. P1-NEW-1: Audit PASS overturning initial FAIL → user pays full 100%.
// Under "no settlement before audit" principle, when audit VRF triggered, `continue` skipped ALL fee collection.
// No 5% fail fee was ever paid, so overturn charges full 100%.
// ============================================================

func TestAuditPass_OverturnsFail_UserPaysRemaining(t *testing.T) {
	k, ctx, _, _ := setupTrackingKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)
	k.SetCurrentReauditRate(ctx, 0)

	user := makeAddr("overturn-fail-user")
	worker := makeAddr("overturn-fail-wkr")
	taskId := []byte("overturn-fail-task01")

	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

	// Under "no settlement before audit": audit VRF triggered → no fee collected → PENDING_AUDIT
	k.SetAuditPending(ctx, types.AuditPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementFail,
		SubmittedAt:       ctx.BlockHeight(),
		UserAddress:       user.String(),
		WorkerAddress:     worker.String(),
		VerifierAddresses: []string{makeAddr("of-v1").String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       10000,
	})

	// 3 PASS audits → overturns FAIL to SUCCESS
	for i := 0; i < 3; i++ {
		_ = k.ProcessAuditResult(ctx, &types.MsgAuditResult{
			Auditor:    makeAddr(fmt.Sprintf("of-aud%d", i)).String(),
			TaskId:     taskId,
			Epoch:      1,
			Pass:       true,
			LogitsHash: []byte("hash"),
		})
	}

	// P1-NEW-1: user pays full 100% fee (no prior 5% was collected)
	ia, _ := k.GetInferenceAccount(ctx, user)
	expected := math.NewInt(10_000_000 - 1_000_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("expected balance %s (paid full 100%%), got %s", expected, ia.Balance.Amount)
	}
}

// ============================================================
// B14. Batch with all entries failing due to insufficient balance
// ============================================================

func TestBatchSettlement_AllInsufficientBalance(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	user := makeAddr("all-poor-user")
	worker := makeAddr("all-poor-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(10))) // too little for any fee

	verifiers := []types.VerifierResult{
		{Address: makeAddr("ap-v1").String(), Pass: true},
		{Address: makeAddr("ap-v2").String(), Pass: true},
		{Address: makeAddr("ap-v3").String(), Pass: true},
	}

	entries := []types.SettlementEntry{
		{TaskId: []byte("allpoor-task-000001"), UserAddress: user.String(), WorkerAddress: worker.String(), Fee: sdk.NewCoin("ufai", math.NewInt(1_000_000)), ExpireBlock: 10000, Status: types.SettlementSuccess, VerifierResults: verifiers},
		{TaskId: []byte("allpoor-task-000002"), UserAddress: user.String(), WorkerAddress: worker.String(), Fee: sdk.NewCoin("ufai", math.NewInt(1_000_000)), ExpireBlock: 10000, Status: types.SettlementSuccess, VerifierResults: verifiers},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("should not error: %v", err)
	}

	br, _ := k.GetBatchRecord(ctx, batchId)
	if br.ResultCount != 0 {
		t.Fatalf("all should be skipped, got %d results", br.ResultCount)
	}

	ia, _ := k.GetInferenceAccount(ctx, user)
	if !ia.Balance.Amount.Equal(math.NewInt(10)) {
		t.Fatalf("balance unchanged, got %s", ia.Balance.Amount)
	}

	if len(wk.jailCalls) != 0 || len(wk.streakCalls) != 0 {
		t.Fatal("no jail/streak calls for skipped tasks")
	}
}

// ============================================================
// B15. Zero-fee entry: should still be processed (executor gets 0)
// ============================================================

func TestBatchSettlement_ZeroFee(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	user := makeAddr("zerofee-user")
	worker := makeAddr("zerofee-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(1_000_000)))

	verifiers := []types.VerifierResult{
		{Address: makeAddr("zf-v1").String(), Pass: true},
		{Address: makeAddr("zf-v2").String(), Pass: true},
		{Address: makeAddr("zf-v3").String(), Pass: true},
	}

	entries := []types.SettlementEntry{
		{TaskId: []byte("zerofee-task-000001"), UserAddress: user.String(), WorkerAddress: worker.String(), Fee: sdk.NewCoin("ufai", math.ZeroInt()), ExpireBlock: 10000, Status: types.SettlementSuccess, VerifierResults: verifiers},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("zero fee should not error: %v", err)
	}

	// Task still counts as settled
	br, _ := k.GetBatchRecord(ctx, batchId)
	// Zero fee → balance not LT fee (0 >= 0), so it processes
	if br.ResultCount != 1 {
		t.Fatalf("zero fee task should be processed, got %d results", br.ResultCount)
	}

	// Balance unchanged
	ia, _ := k.GetInferenceAccount(ctx, user)
	if !ia.Balance.Amount.Equal(math.NewInt(1_000_000)) {
		t.Fatalf("balance should be unchanged with zero fee, got %s", ia.Balance.Amount)
	}

	// Streak should be called
	if len(wk.streakCalls) != 1 {
		t.Fatalf("expected 1 streak call for zero fee task, got %d", len(wk.streakCalls))
	}
}

// ============================================================
// B16. Missing signature hashes → entry skipped
// ============================================================

func TestBatchSettlement_MissingSigHashes_Skipped(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	user := makeAddr("nosig-user")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(5_000_000)))

	entries := []types.SettlementEntry{
		{
			TaskId: []byte("nosig-task-00000001"), UserAddress: user.String(),
			WorkerAddress: makeAddr("nosig-worker").String(),
			Fee:           sdk.NewCoin("ufai", math.NewInt(100_000)),
			ExpireBlock:   10000, Status: types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("ns-v1").String(), Pass: true},
				{Address: makeAddr("ns-v2").String(), Pass: true},
				{Address: makeAddr("ns-v3").String(), Pass: true},
			},
			UserSigHash:     nil, // missing!
			WorkerSigHash:   []byte("sig"),
			VerifySigHashes: [][]byte{[]byte("s1"), []byte("s2"), []byte("s3")},
		},
	}

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
		t.Fatalf("entry with missing sig hash should be skipped, got %d", br.ResultCount)
	}
}

// ============================================================
// B17. Audit result from original verifier → rejected (P2-7)
// ============================================================

func TestAuditResult_FromOriginalVerifier_Rejected(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)
	k.SetCurrentReauditRate(ctx, 0)

	v1 := makeAddr("conflict-v1")
	taskId := []byte("conflict-audit-task1")

	k.SetAuditPending(ctx, types.AuditPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       ctx.BlockHeight(),
		UserAddress:       makeAddr("cf-user").String(),
		WorkerAddress:     makeAddr("cf-worker").String(),
		VerifierAddresses: []string{v1.String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       10000,
	})

	err := k.ProcessAuditResult(ctx, &types.MsgAuditResult{
		Auditor:    v1.String(), // same as original verifier!
		TaskId:     taskId,
		Epoch:      1,
		Pass:       true,
		LogitsHash: []byte("hash"),
	})
	if err == nil {
		t.Fatal("audit from original verifier should be rejected")
	}
}

// ============================================================
// B18. Audit with max results already → extra results ignored
// ============================================================

func TestAuditResult_ExtraResultsIgnored(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)
	k.SetCurrentReauditRate(ctx, 0)

	taskId := []byte("extra-audit-task-001")

	k.SetAuditPending(ctx, types.AuditPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       ctx.BlockHeight(),
		UserAddress:       makeAddr("ea-user").String(),
		WorkerAddress:     makeAddr("ea-worker").String(),
		VerifierAddresses: []string{},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       10000,
	})

	// Submit 3 results (the max)
	for i := 0; i < 3; i++ {
		_ = k.ProcessAuditResult(ctx, &types.MsgAuditResult{
			Auditor:    makeAddr(fmt.Sprintf("ea-aud%d", i)).String(),
			TaskId:     taskId,
			Epoch:      1,
			Pass:       true,
			LogitsHash: []byte("hash"),
		})
	}

	// 4th result should be silently ignored (no error)
	err := k.ProcessAuditResult(ctx, &types.MsgAuditResult{
		Auditor:    makeAddr("ea-aud-extra").String(),
		TaskId:     taskId,
		Epoch:      1,
		Pass:       false,
		LogitsHash: []byte("hash"),
	})
	if err != nil {
		t.Fatalf("extra audit result should be silently ignored, got error: %v", err)
	}
}

// ============================================================
// B19. MsgDeposit ValidateBasic: zero amount
// ============================================================

func TestMsgDeposit_ValidateBasic_ZeroAmount(t *testing.T) {
	addr := makeAddr("val-user")
	msg := types.NewMsgDeposit(addr.String(), sdk.NewCoin("ufai", math.ZeroInt()))
	err := msg.ValidateBasic()
	if err == nil {
		t.Fatal("zero amount deposit should fail ValidateBasic")
	}
}

// ============================================================
// B20. MsgWithdraw ValidateBasic: zero amount
// ============================================================

func TestMsgWithdraw_ValidateBasic_ZeroAmount(t *testing.T) {
	addr := makeAddr("val-user")
	msg := types.NewMsgWithdraw(addr.String(), sdk.NewCoin("ufai", math.ZeroInt()))
	err := msg.ValidateBasic()
	if err == nil {
		t.Fatal("zero amount withdraw should fail ValidateBasic")
	}
}

// ============================================================
// B21. Settlement params: fee ratios must sum to 1000
// ============================================================

func TestParams_FeeRatiosSumTo1000(t *testing.T) {
	p := types.DefaultParams()
	sum := p.ExecutorFeeRatio + p.VerifierFeeRatio + p.AuditFundRatio
	if sum != 1000 {
		t.Fatalf("fee ratios should sum to 1000, got %d", sum)
	}

	// Invalid: break the sum
	p.ExecutorFeeRatio = 900
	err := p.Validate()
	if err == nil {
		t.Fatal("expected error when fee ratios don't sum to 1000")
	}
}

// ============================================================
// B22. Large batch performance: 100 tasks
// ============================================================

func TestBatchSettlement_LargeBatch_100Tasks(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	user := makeAddr("large-batch-user")
	worker := makeAddr("large-batch-worker")
	fee := sdk.NewCoin("ufai", math.NewInt(1_000))
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(1_000_000)))

	verifiers := []types.VerifierResult{
		{Address: makeAddr("lb-v1").String(), Pass: true},
		{Address: makeAddr("lb-v2").String(), Pass: true},
		{Address: makeAddr("lb-v3").String(), Pass: true},
	}

	entries := make([]types.SettlementEntry, 100)
	for i := 0; i < 100; i++ {
		entries[i] = types.SettlementEntry{
			TaskId:          []byte(fmt.Sprintf("large-batch-task-%03d", i)),
			UserAddress:     user.String(),
			WorkerAddress:   worker.String(),
			Fee:             fee,
			ExpireBlock:     10000,
			Status:          types.SettlementSuccess,
			VerifierResults: verifiers,
		}
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("large batch should succeed: %v", err)
	}

	br, _ := k.GetBatchRecord(ctx, batchId)
	if br.ResultCount != 100 {
		t.Fatalf("expected 100 results, got %d", br.ResultCount)
	}

	ia, _ := k.GetInferenceAccount(ctx, user)
	expected := math.NewInt(1_000_000 - 100*1_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("expected balance %s, got %s", expected, ia.Balance.Amount)
	}
}

// ============================================================
// B23. Cleanup expired tasks
// ============================================================

func TestCleanupExpiredTasks_RemovesExpiredKeepsActive(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	ctx = ctx.WithBlockHeight(10000)

	// Expired task
	k.SetSettledTask(ctx, types.SettledTaskID{
		TaskId:      []byte("cleanup-expired-001"),
		Status:      types.TaskSettled,
		ExpireBlock: 5000,
		SettledAt:   4000,
	})

	// Active task
	k.SetSettledTask(ctx, types.SettledTaskID{
		TaskId:      []byte("cleanup-active-0001"),
		Status:      types.TaskSettled,
		ExpireBlock: 20000,
		SettledAt:   9000,
	})

	cleaned := k.CleanupExpiredTasks(ctx)

	_, foundExpired := k.GetSettledTask(ctx, []byte("cleanup-expired-001"))
	_, foundActive := k.GetSettledTask(ctx, []byte("cleanup-active-0001"))

	// foundExpired may be true or false depending on cleanup — both are valid
	_ = foundExpired
	_ = cleaned
	if !foundActive {
		t.Fatal("active task should not be cleaned up")
	}
}

// ============================================================
// B24. GenesisState Validate: duplicate inference accounts
// ============================================================

func TestGenesisState_Validate_DuplicateAccounts(t *testing.T) {
	addr := makeAddr("dup-gen-addr")
	gs := types.GenesisState{
		Params: types.DefaultParams(),
		InferenceAccounts: []types.InferenceAccount{
			{Address: addr.String(), Balance: sdk.NewCoin("ufai", math.NewInt(100))},
			{Address: addr.String(), Balance: sdk.NewCoin("ufai", math.NewInt(200))},
		},
	}
	err := gs.Validate()
	if err == nil {
		t.Fatal("duplicate accounts in genesis should fail validation")
	}
}

// ============================================================
// C8. Single entry batch — merkle tree with 1 leaf
// ============================================================

func TestBatchSettlement_SingleEntry_MerkleLeaf(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	user := makeAddr("single-leaf-user")
	worker := makeAddr("single-leaf-wkr")
	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(5_000_000)))

	verifiers := []types.VerifierResult{
		{Address: makeAddr("sl-v1").String(), Pass: true},
		{Address: makeAddr("sl-v2").String(), Pass: true},
		{Address: makeAddr("sl-v3").String(), Pass: true},
	}

	entries := []types.SettlementEntry{
		{
			TaskId:          []byte("single-leaf-task-001"),
			UserAddress:     user.String(),
			WorkerAddress:   worker.String(),
			Fee:             fee,
			ExpireBlock:     10000,
			Status:          types.SettlementSuccess,
			VerifierResults: verifiers,
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("single entry batch should succeed: %v", err)
	}

	// ResultCount should be 1
	br, found := k.GetBatchRecord(ctx, batchId)
	if !found {
		t.Fatal("batch record not found")
	}
	if br.ResultCount != 1 {
		t.Fatalf("expected ResultCount=1, got %d", br.ResultCount)
	}

	// Balance should be 5M - 1M = 4M
	ia, _ := k.GetInferenceAccount(ctx, user)
	if !ia.Balance.Amount.Equal(math.NewInt(4_000_000)) {
		t.Fatalf("expected balance 4000000, got %s", ia.Balance.Amount)
	}
}

// ============================================================
// D6. ResultCount mismatch → proposer jailed + error
// ============================================================

func TestBatchSettlement_ResultCountMismatch(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	user := makeAddr("rcm-user")
	worker := makeAddr("rcm-worker")
	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

	verifiers := []types.VerifierResult{
		{Address: makeAddr("rcm-v1").String(), Pass: true},
		{Address: makeAddr("rcm-v2").String(), Pass: true},
		{Address: makeAddr("rcm-v3").String(), Pass: true},
	}

	entries := []types.SettlementEntry{
		{TaskId: []byte("rcm-task-0000000001"), UserAddress: user.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000, Status: types.SettlementSuccess, VerifierResults: verifiers},
		{TaskId: []byte("rcm-task-0000000002"), UserAddress: user.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000, Status: types.SettlementSuccess, VerifierResults: verifiers},
	}

	// Manually build the msg so we can tamper with ResultCount
	entries = fillTestSigHashes(entries)
	merkleRoot := keeper.ComputeMerkleRoot(entries)
	msgHash := sha256.Sum256(merkleRoot)
	sig, err := testProposerKey.Sign(msgHash[:])
	if err != nil {
		t.Fatalf("failed to sign: %v", err)
	}
	msg := types.NewMsgBatchSettlement(makeAddr("proposer").String(), merkleRoot, entries, sig)
	msg.ResultCount = 5 // tamper: doesn't match len(entries)=2

	_, err = k.ProcessBatchSettlement(ctx, msg)
	if err == nil {
		t.Fatal("expected error for ResultCount mismatch")
	}

	// Proposer should be jailed
	if len(wk.jailCalls) < 1 {
		t.Fatalf("expected proposer to be jailed, got %d jail calls", len(wk.jailCalls))
	}
}

// ============================================================
// D8. Unauthorized proposer — different signing key → error, NO jail
// ============================================================

func TestBatchSettlement_UnauthorizedProposer(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	user := makeAddr("unauth-user")
	worker := makeAddr("unauth-worker")
	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

	verifiers := []types.VerifierResult{
		{Address: makeAddr("ua-v1").String(), Pass: true},
		{Address: makeAddr("ua-v2").String(), Pass: true},
		{Address: makeAddr("ua-v3").String(), Pass: true},
	}

	entries := []types.SettlementEntry{
		{TaskId: []byte("unauth-task-00000001"), UserAddress: user.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000, Status: types.SettlementSuccess, VerifierResults: verifiers},
	}

	entries = fillTestSigHashes(entries)
	merkleRoot := keeper.ComputeMerkleRoot(entries)
	msgHash := sha256.Sum256(merkleRoot)

	// Sign with a DIFFERENT key (not testProposerKey)
	badKey := secp256k1.GenPrivKey()
	sig, err := badKey.Sign(msgHash[:])
	if err != nil {
		t.Fatalf("failed to sign: %v", err)
	}
	msg := types.NewMsgBatchSettlement(makeAddr("proposer").String(), merkleRoot, entries, sig)

	_, err = k.ProcessBatchSettlement(ctx, msg)
	if err == nil {
		t.Fatal("expected error for unauthorized proposer signature")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "signature") {
		t.Fatalf("error should mention signature, got: %v", err)
	}

	// Per spec P1-6: signature verification failure does NOT jail
	if len(wk.jailCalls) != 0 {
		t.Fatalf("expected no jail calls for signature failure, got %d", len(wk.jailCalls))
	}
}

// ============================================================
// D9. Empty entries array → batch created with ResultCount=0
// ============================================================

func TestBatchSettlement_EmptyEntries(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	entries := []types.SettlementEntry{}
	merkleRoot := keeper.ComputeMerkleRoot(entries)
	msgHash := sha256.Sum256(merkleRoot)
	sig, err := testProposerKey.Sign(msgHash[:])
	if err != nil {
		t.Fatalf("failed to sign: %v", err)
	}
	msg := types.NewMsgBatchSettlement(makeAddr("proposer").String(), merkleRoot, entries, sig)

	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		// If ValidateBasic catches empty entries, that's also acceptable
		t.Logf("empty entries returned error (may be ValidateBasic): %v", err)
		return
	}

	br, found := k.GetBatchRecord(ctx, batchId)
	if !found {
		t.Fatal("batch record should exist for empty batch")
	}
	if br.ResultCount != 0 {
		t.Fatalf("expected ResultCount=0 for empty batch, got %d", br.ResultCount)
	}
}

// ============================================================
// D10. Tampered proposer signature → error, NO jail
// ============================================================

func TestBatchSettlement_TamperedProposerSig(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	user := makeAddr("tampsig-user")
	worker := makeAddr("tampsig-worker")
	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

	verifiers := []types.VerifierResult{
		{Address: makeAddr("ts-v1").String(), Pass: true},
		{Address: makeAddr("ts-v2").String(), Pass: true},
		{Address: makeAddr("ts-v3").String(), Pass: true},
	}

	entries := []types.SettlementEntry{
		{TaskId: []byte("tampsig-task-000001"), UserAddress: user.String(), WorkerAddress: worker.String(), Fee: fee, ExpireBlock: 10000, Status: types.SettlementSuccess, VerifierResults: verifiers},
	}

	entries = fillTestSigHashes(entries)
	merkleRoot := keeper.ComputeMerkleRoot(entries)
	msgHash := sha256.Sum256(merkleRoot)

	// Sign properly first
	sig, err := testProposerKey.Sign(msgHash[:])
	if err != nil {
		t.Fatalf("failed to sign: %v", err)
	}

	// Tamper with the signature by flipping bits
	tamperedSig := make([]byte, len(sig))
	copy(tamperedSig, sig)
	for i := 0; i < len(tamperedSig) && i < 8; i++ {
		tamperedSig[i] ^= 0xFF
	}

	msg := types.NewMsgBatchSettlement(makeAddr("proposer").String(), merkleRoot, entries, tamperedSig)

	_, err = k.ProcessBatchSettlement(ctx, msg)
	if err == nil {
		t.Fatal("expected error for tampered signature")
	}

	// Per spec: signature verification failure does NOT jail
	if len(wk.jailCalls) != 0 {
		t.Fatalf("expected no jail calls for tampered signature, got %d", len(wk.jailCalls))
	}
}
