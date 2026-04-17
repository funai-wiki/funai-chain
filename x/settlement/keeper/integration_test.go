package keeper_test

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/settlement/keeper"
	"github.com/funai-wiki/funai-chain/x/settlement/types"
)

func integrationTaskId(index int) []byte {
	h := sha256.Sum256([]byte(fmt.Sprintf("integration-task-%d", index)))
	return h[:]
}

func integrationEntries(userAddr, workerAddr string, verifierAddrs []string, count int, status types.SettlementStatus, fee sdk.Coin, expireBlock int64, idOffset int) []types.SettlementEntry {
	entries := make([]types.SettlementEntry, count)
	for i := 0; i < count; i++ {
		vr := make([]types.VerifierResult, len(verifierAddrs))
		for j, v := range verifierAddrs {
			vr[j] = types.VerifierResult{Address: v, Pass: status == types.SettlementSuccess, Signature: []byte("sig")}
		}
		entries[i] = types.SettlementEntry{
			TaskId:          integrationTaskId(idOffset + i),
			UserAddress:     userAddr,
			WorkerAddress:   workerAddr,
			VerifierResults: vr,
			Fee:             fee,
			Status:          status,
			ExpireBlock:     expireBlock,
			UserSigHash:     []byte("user-sig-hash-32bytes-padding!!"),
			WorkerSigHash:   []byte("worker-sig-hash-32bytes-padding"),
			VerifySigHashes: [][]byte{[]byte("vsig1-hash"), []byte("vsig2-hash"), []byte("vsig3-hash")},
		}
	}
	return entries
}

func integrationBatchMsg(proposer string, entries []types.SettlementEntry) *types.MsgBatchSettlement {
	merkleRoot := keeper.ComputeMerkleRoot(entries)
	// P1-6: use proper secp256k1 signature for proposer
	msgHash := sha256.Sum256(merkleRoot)
	sig, _ := testProposerKey.Sign(msgHash[:])
	return types.NewMsgBatchSettlement(proposer, merkleRoot, entries, sig)
}

// TestIntegration_FullSettlementLifecycle validates the complete settlement flow:
// deposit → SUCCESS settle → FAIL settle → fraud proof → duplicate protection → merkle mismatch
func TestIntegration_FullSettlementLifecycle(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("int-user")
	workerAddr := makeAddr("int-worker")
	v1 := makeAddr("int-v1")
	v2 := makeAddr("int-v2")
	v3 := makeAddr("int-v3")
	proposer := makeAddr("int-proposer")
	verifiers := []string{v1.String(), v2.String(), v3.String()}

	// 1. Deposit
	deposit := sdk.NewCoin("ufai", math.NewInt(100_000_000_000))
	if err := k.ProcessDeposit(ctx, userAddr, deposit); err != nil {
		t.Fatalf("Deposit: %v", err)
	}
	ia, _ := k.GetInferenceAccount(ctx, userAddr)
	if !ia.Balance.Equal(deposit) {
		t.Fatalf("Deposit balance: want %s, got %s", deposit, ia.Balance)
	}
	t.Log("[PASS] Deposit 100,000 FAI")

	// 2. SUCCESS settlement (10 tasks × 1 FAI)
	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	entries := integrationEntries(userAddr.String(), workerAddr.String(), verifiers, 10, types.SettlementSuccess, fee, 100000, 0)
	msg := integrationBatchMsg(proposer.String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("SUCCESS batch: %v", err)
	}

	ia, _ = k.GetInferenceAccount(ctx, userAddr)
	expectedBal := deposit.Amount.Sub(fee.Amount.MulRaw(10))
	if !ia.Balance.Amount.Equal(expectedBal) {
		t.Fatalf("After SUCCESS: want %s, got %s", expectedBal, ia.Balance.Amount)
	}
	t.Logf("[PASS] Batch %d: 10 SUCCESS, user balance reduced by 10 FAI", batchId)

	// 3. FAIL settlement (2 tasks)
	failEntries := integrationEntries(userAddr.String(), workerAddr.String(), verifiers, 2, types.SettlementFail, fee, 100000, 100)
	failMsg := integrationBatchMsg(proposer.String(), failEntries)
	batchId2, err := k.ProcessBatchSettlement(ctx, failMsg)
	if err != nil {
		t.Fatalf("FAIL batch: %v", err)
	}

	ia, _ = k.GetInferenceAccount(ctx, userAddr)
	failFee := fee.Amount.MulRaw(150).QuoRaw(1000) // 15%
	expectedBal = expectedBal.Sub(failFee.MulRaw(2))
	if !ia.Balance.Amount.Equal(expectedBal) {
		t.Fatalf("After FAIL: want %s, got %s", expectedBal, ia.Balance.Amount)
	}
	if len(wk.jailCalls) != 2 {
		t.Fatalf("Jail calls: want 2, got %d", len(wk.jailCalls))
	}
	t.Logf("[PASS] Batch %d: 2 FAIL, user charged 15%%, worker jailed %d times", batchId2, len(wk.jailCalls))

	// 4. Duplicate task_id protection
	dupEntries := integrationEntries(userAddr.String(), workerAddr.String(), verifiers, 1, types.SettlementSuccess, fee, 100000, 0) // same offset=0
	dupMsg := integrationBatchMsg(proposer.String(), dupEntries)
	dupId, _ := k.ProcessBatchSettlement(ctx, dupMsg)
	br, _ := k.GetBatchRecord(ctx, dupId)
	if br.ResultCount != 0 {
		t.Fatalf("Duplicate: settled %d, want 0", br.ResultCount)
	}
	t.Log("[PASS] Duplicate task_id skipped")

	// 5. FraudProof
	fraudId := integrationTaskId(0)
	fraudCH, fraudCS := signFraudContent(t, []byte("content"))
	err = k.ProcessFraudProof(ctx, types.NewMsgFraudProof(
		userAddr.String(), fraudId, workerAddr.String(),
		fraudCH, fraudCS, []byte("content"),
	))
	if err != nil {
		t.Fatalf("FraudProof: %v", err)
	}
	if !k.HasFraudMark(ctx, fraudId) {
		t.Fatal("FraudProof: no FRAUD mark")
	}
	if len(wk.slashCalls) != 1 {
		t.Fatalf("Slash calls: want 1, got %d", len(wk.slashCalls))
	}
	t.Log("[PASS] FraudProof → FRAUD mark + slash")

	// 6. Merkle mismatch → reject (signature valid but merkle root doesn't match entries)
	badEntries := integrationEntries(userAddr.String(), workerAddr.String(), verifiers, 1, types.SettlementSuccess, fee, 100000, 500)
	badRoot := []byte("bad-root")
	badRootHash := sha256.Sum256(badRoot)
	badSig, _ := testProposerKey.Sign(badRootHash[:])
	badMsg := types.NewMsgBatchSettlement(proposer.String(), badRoot, badEntries, badSig)
	_, err = k.ProcessBatchSettlement(ctx, badMsg)
	if err == nil {
		t.Fatal("Expected error for merkle mismatch")
	}
	t.Log("[PASS] Merkle mismatch → batch rejected")

	// 7. Epoch stats
	epoch := ctx.BlockHeight() / 100
	stats := k.GetEpochStats(ctx, epoch)
	if stats.TotalSettled != 12 {
		t.Fatalf("EpochStats total: want 12, got %d", stats.TotalSettled)
	}
	if stats.FailSettled != 2 {
		t.Fatalf("EpochStats fail: want 2, got %d", stats.FailSettled)
	}
	t.Logf("[PASS] EpochStats: total=%d, fail=%d", stats.TotalSettled, stats.FailSettled)

	t.Log("\n=== FULL SETTLEMENT LIFECYCLE: ALL PASSED ===")
}

// TestIntegration_SecondVerificationTimeout verifies audit timeout handling.
func TestIntegration_SecondVerificationTimeout(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	apt := types.SecondVerificationPendingTask{
		TaskId:            integrationTaskId(900),
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       1,
		UserAddress:       makeAddr("timeout-user").String(),
		WorkerAddress:     makeAddr("timeout-worker").String(),
		VerifierAddresses: []string{"v1", "v2", "v3"},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       100000,
	}
	k.SetSecondVerificationPending(ctx, apt)

	params := k.GetParams(ctx)
	params.SecondVerificationTimeout = 10
	k.SetParams(ctx, params)

	ctx = ctx.WithBlockHeight(15)
	timeouts := k.HandleSecondVerificationTimeouts(ctx)
	if timeouts != 1 {
		t.Fatalf("Timeouts: want 1, got %d", timeouts)
	}

	_, found := k.GetSecondVerificationPending(ctx, apt.TaskId)
	if found {
		t.Fatal("Pending task should be removed after timeout")
	}
	t.Log("[PASS] Audit timeout → original result stands, pending removed")
}

// TestIntegration_DynamicAuditRate validates the audit rate formula.
func TestIntegration_DynamicAuditRate(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	tests := []struct {
		name      string
		fail      uint64
		total     uint64
		wantRate  uint32
		wantClamp string
	}{
		{"normal", 0, 100, 100, ""},
		{"5% fail", 5, 100, 150, ""},
		{"20% fail", 20, 100, 300, "capped at max"},
		{"1% fail", 1, 100, 110, ""},
	}

	for i, tt := range tests {
		stats := types.EpochStats{
			Epoch:                   int64(i),
			TotalSettled:            tt.total,
			FailSettled:             tt.fail,
			SecondVerificationTotal: 10,
			TotalFees:               math.NewInt(100_000_000),
		}
		k.SetEpochStats(ctx, stats)
		rate := k.CalculateSecondVerificationRate(ctx, int64(i))
		if rate != tt.wantRate {
			t.Fatalf("%s: rate want %d, got %d", tt.name, tt.wantRate, rate)
		}
		t.Logf("[PASS] %s: rate=%d (%s)", tt.name, rate, tt.wantClamp)
	}
}

// TestIntegration_SecondVerificationResult_AfterTimeout (E8) verifies that audit results
// submitted after timeout are effectively no-ops: the pending task was already
// cleared by HandleSecondVerificationTimeouts, so processAuditJudgment finds no pending
// task and returns early. No jails or settlements occur.
func TestIntegration_SecondVerificationResult_AfterTimeout(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)

	params := k.GetParams(ctx)
	params.SecondVerificationTimeout = 10
	k.SetParams(ctx, params)

	taskId := integrationTaskId(800)
	userAddr := makeAddr("e8-user")
	workerAddr := makeAddr("e8-worker")
	v1 := makeAddr("e8-v1")
	v2 := makeAddr("e8-v2")
	v3 := makeAddr("e8-v3")

	// Deposit so settleAuditedTask won't bail on missing account
	deposit := sdk.NewCoin("ufai", math.NewInt(100_000_000))
	if err := k.ProcessDeposit(ctx, userAddr, deposit); err != nil {
		t.Fatalf("Deposit: %v", err)
	}

	apt := types.SecondVerificationPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       1,
		UserAddress:       userAddr.String(),
		WorkerAddress:     workerAddr.String(),
		VerifierAddresses: []string{v1.String(), v2.String(), v3.String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       100000,
	}
	k.SetSecondVerificationPending(ctx, apt)

	// Advance past timeout: submittedAt=1, timeout=10, height=15 → expired
	ctx = ctx.WithBlockHeight(15)
	timeouts := k.HandleSecondVerificationTimeouts(ctx)
	if timeouts != 1 {
		t.Fatalf("Expected 1 timeout, got %d", timeouts)
	}

	// Confirm pending task is gone
	_, found := k.GetSecondVerificationPending(ctx, taskId)
	if found {
		t.Fatal("Pending task should be cleared after timeout")
	}
	t.Log("[PASS] HandleSecondVerificationTimeouts cleared the pending task")

	// Now submit 3 audit results for the same task — they arrive too late
	jailsBefore := len(wk.jailCalls)
	second_verifiers := []string{
		makeAddr("e8-aud1").String(),
		makeAddr("e8-aud2").String(),
		makeAddr("e8-aud3").String(),
	}
	for _, aud := range second_verifiers {
		err := k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
			SecondVerifier: aud,
			TaskId:         taskId,
			Epoch:          0,
			Pass:           true,
			LogitsHash:     []byte("logits-hash"),
		})
		if err != nil {
			t.Fatalf("ProcessSecondVerificationResult from %s: %v", aud, err)
		}
	}

	// SecondVerificationRecord should exist with 3 results (they were recorded)
	ar, arFound := k.GetSecondVerificationRecord(ctx, taskId)
	if !arFound {
		t.Fatal("SecondVerificationRecord should exist after 3 submissions")
	}
	if len(ar.Results) != 3 {
		t.Fatalf("SecondVerificationRecord results: want 3, got %d", len(ar.Results))
	}
	t.Log("[PASS] SecondVerificationRecord created with 3 results")

	// Key check: no jail calls occurred — processAuditJudgment found no pending task
	if len(wk.jailCalls) != jailsBefore {
		t.Fatalf("Expected no new jail calls after timeout, got %d new calls", len(wk.jailCalls)-jailsBefore)
	}

	// Task was already settled by timeout (original result stands), not by audit judgment
	st, stFound := k.GetSettledTask(ctx, taskId)
	if !stFound {
		t.Fatal("Task should have been settled by timeout handler")
	}
	t.Logf("[PASS] Task settled by timeout with status=%d, no jails from late audit results", st.Status)
}

// TestIntegration_DuplicateSecondVerifierSubmission (E9) documents that the current
// code does NOT deduplicate second_verifier addresses. The same second_verifier can submit
// multiple times, and each submission is appended to the SecondVerificationRecord.
// After SecondVerifierCount (3) results are collected, judgment triggers.
func TestIntegration_DuplicateSecondVerifierSubmission(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	k.SetCurrentThirdVerificationRate(ctx, 0)

	taskId := integrationTaskId(810)
	userAddr := makeAddr("e9-user")
	workerAddr := makeAddr("e9-worker")
	v1 := makeAddr("e9-v1")
	v2 := makeAddr("e9-v2")
	v3 := makeAddr("e9-v3")

	// Deposit for settlement
	deposit := sdk.NewCoin("ufai", math.NewInt(100_000_000))
	if err := k.ProcessDeposit(ctx, userAddr, deposit); err != nil {
		t.Fatalf("Deposit: %v", err)
	}

	// Create audit pending task
	apt := types.SecondVerificationPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       ctx.BlockHeight(),
		UserAddress:       userAddr.String(),
		WorkerAddress:     workerAddr.String(),
		VerifierAddresses: []string{v1.String(), v2.String(), v3.String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       100000,
	}
	k.SetSecondVerificationPending(ctx, apt)

	aud1 := makeAddr("e9-aud1").String()
	aud2 := makeAddr("e9-aud2").String()

	// First submission from aud1
	err := k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
		SecondVerifier: aud1, TaskId: taskId, Epoch: 0, Pass: true, LogitsHash: []byte("lh"),
	})
	if err != nil {
		t.Fatalf("First aud1 submission: %v", err)
	}

	// Second submission from aud1 (duplicate — current code accepts it)
	err = k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
		SecondVerifier: aud1, TaskId: taskId, Epoch: 0, Pass: true, LogitsHash: []byte("lh"),
	})
	if err != nil {
		t.Fatalf("Second aud1 submission: %v", err)
	}

	// Check record after 2 submissions — should have 2 entries, both from aud1
	ar, found := k.GetSecondVerificationRecord(ctx, taskId)
	if !found {
		t.Fatal("SecondVerificationRecord not found after 2 submissions")
	}
	if len(ar.SecondVerifierAddresses) != 2 {
		t.Fatalf("After 2 submissions: want 2 second_verifier entries, got %d", len(ar.SecondVerifierAddresses))
	}
	if ar.SecondVerifierAddresses[0] != aud1 || ar.SecondVerifierAddresses[1] != aud1 {
		t.Fatalf("Expected both entries from aud1, got %v", ar.SecondVerifierAddresses)
	}
	t.Log("[PASS] Duplicate second_verifier submission accepted (no deduplication)")

	// Third submission from aud2 — triggers judgment (3 results collected)
	jailsBefore := len(wk.jailCalls)
	err = k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
		SecondVerifier: aud2, TaskId: taskId, Epoch: 0, Pass: true, LogitsHash: []byte("lh"),
	})
	if err != nil {
		t.Fatalf("aud2 submission: %v", err)
	}

	// After 3 results (all PASS), judgment triggers: SUCCESS + audit PASS → settle as success
	ar, _ = k.GetSecondVerificationRecord(ctx, taskId)
	if len(ar.SecondVerifierAddresses) != 3 {
		t.Fatalf("Final record: want 3 entries, got %d", len(ar.SecondVerifierAddresses))
	}
	// Entries are: aud1, aud1, aud2 — documenting that duplicates are stored
	if ar.SecondVerifierAddresses[0] != aud1 || ar.SecondVerifierAddresses[1] != aud1 || ar.SecondVerifierAddresses[2] != aud2 {
		t.Fatalf("Expected [aud1, aud1, aud2], got %v", ar.SecondVerifierAddresses)
	}
	t.Log("[PASS] SecondVerificationRecord has 3 entries: [aud1, aud1, aud2]")

	// Judgment should have settled the task as success (no jails for SUCCESS + audit PASS)
	if len(wk.jailCalls) != jailsBefore {
		t.Fatalf("Expected no new jail calls for SUCCESS+PASS, got %d", len(wk.jailCalls)-jailsBefore)
	}

	st, stFound := k.GetSettledTask(ctx, taskId)
	if !stFound {
		t.Fatal("Task should be settled after judgment")
	}
	if st.Status != types.TaskSettled {
		t.Fatalf("Expected TaskSettled, got %d", st.Status)
	}
	t.Log("[PASS] Judgment triggered after 3 results, task settled as success")
}

// TestIntegration_ThirdVerificationFourQuadrants (F1-F5) is a table-driven test for audit
// judgment scenarios. It exercises the four combinations of original verification
// status (SUCCESS/FAIL) and audit vote outcome (PASS/FAIL), plus a majority
// decision case.
//
// Note on implementation: the third_verification code path in processAuditJudgment is
// unreachable through ProcessSecondVerificationResult because GetSecondVerificationPending only checks
// SecondVerificationPendingKey, while third_verification tasks are stored at ThirdVerificationPendingKey.
// Therefore this test exercises the non-third_verification judgment branch (which has
// equivalent logic for the four quadrants) by storing tasks with IsThirdVerification=false.
// SetCurrentThirdVerificationRate=0 prevents VRF from triggering actual third_verification.
func TestIntegration_ThirdVerificationFourQuadrants(t *testing.T) {
	tests := []struct {
		name          string
		origStatus    types.SettlementStatus
		auditVotes    []bool // 3 audit votes
		expectJailMin int    // minimum jail calls from this case
		expectSettled bool   // true = TaskSettled (success), false = TaskFailed or TaskFailSettled
	}{
		// F1: SUCCESS + audit PASS → confirms, settle as success
		{"F1_OrigSuccess_AuditPass", types.SettlementSuccess, []bool{true, true, true}, 0, true},
		// F2: SUCCESS + audit FAIL → overturn, jail worker + verifiers, TaskFailed
		{"F2_OrigSuccess_AuditFail", types.SettlementSuccess, []bool{false, false, false}, 4, false},
		// F3: FAIL + audit PASS → overturn, settle as success, jail FAIL verifiers
		{"F3_OrigFail_AuditPass", types.SettlementFail, []bool{true, true, true}, 1, true},
		// F4: FAIL + audit FAIL → confirms, settle as fail
		{"F4_OrigFail_AuditFail", types.SettlementFail, []bool{false, false, false}, 0, false},
		// F5: FAIL + majority 2:1 PASS → overturn (2 >= threshold 2), settle as success
		{"F5_Majority_2Pass1Fail", types.SettlementFail, []bool{true, true, false}, 1, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, ctx, _, wk := setupKeeper(t)
			k.SetCurrentSecondVerificationRate(ctx, 0)
			k.SetCurrentThirdVerificationRate(ctx, 0)

			taskId := []byte(fmt.Sprintf("third_verification-quad-%s", tt.name))
			userAddr := makeAddr("rq-user-" + tt.name[:2])
			workerAddr := makeAddr("rq-work-" + tt.name[:2])
			v1 := makeAddr("rq-v1-" + tt.name[:2])
			v2 := makeAddr("rq-v2-" + tt.name[:2])
			v3 := makeAddr("rq-v3-" + tt.name[:2])

			// Deposit enough for fee settlement
			deposit := sdk.NewCoin("ufai", math.NewInt(100_000_000))
			if err := k.ProcessDeposit(ctx, userAddr, deposit); err != nil {
				t.Fatalf("Deposit: %v", err)
			}

			// Create audit pending task (IsThirdVerification=false so it is stored at
			// SecondVerificationPendingKey where processAuditJudgment can find it)
			apt := types.SecondVerificationPendingTask{
				TaskId:              taskId,
				OriginalStatus:      tt.origStatus,
				SubmittedAt:         ctx.BlockHeight(),
				UserAddress:         userAddr.String(),
				WorkerAddress:       workerAddr.String(),
				VerifierAddresses:   []string{v1.String(), v2.String(), v3.String()},
				Fee:                 sdk.NewCoin("ufai", math.NewInt(1_000_000)),
				ExpireBlock:         100000,
				IsThirdVerification: false,
			}
			k.SetSecondVerificationPending(ctx, apt)

			// Submit 3 audit results with the specified votes
			second_verifiers := []string{
				makeAddr("rq-a1-" + tt.name[:2]).String(),
				makeAddr("rq-a2-" + tt.name[:2]).String(),
				makeAddr("rq-a3-" + tt.name[:2]).String(),
			}
			for i, aud := range second_verifiers {
				err := k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
					SecondVerifier: aud,
					TaskId:         taskId,
					Epoch:          0,
					Pass:           tt.auditVotes[i],
					LogitsHash:     []byte("logits"),
				})
				if err != nil {
					t.Fatalf("Audit result %d from %s: %v", i, aud, err)
				}
			}

			// Check jail calls
			if len(wk.jailCalls) < tt.expectJailMin {
				t.Fatalf("Jail calls: want >= %d, got %d", tt.expectJailMin, len(wk.jailCalls))
			}
			t.Logf("Jail calls: %d (min expected: %d)", len(wk.jailCalls), tt.expectJailMin)

			// Check settled task
			st, stFound := k.GetSettledTask(ctx, taskId)
			if !stFound {
				t.Fatal("Task should be settled after audit judgment")
			}

			if tt.expectSettled {
				if st.Status != types.TaskSettled {
					t.Fatalf("Expected TaskSettled, got status=%d", st.Status)
				}
			} else {
				if st.Status != types.TaskFailed && st.Status != types.TaskFailSettled {
					t.Fatalf("Expected TaskFailed or TaskFailSettled, got status=%d", st.Status)
				}
			}
			t.Logf("[PASS] %s: status=%d, jails=%d", tt.name, st.Status, len(wk.jailCalls))
		})
	}
}
