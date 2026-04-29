package keeper_test

// Audit retry-handle / orphan recovery tests for KT 30-case Issue 2.
// (See engineer's classification 2026-04-29 + FunAI-18h-issue-summary review.)
//
// Pre-fix behaviour: processAuditJudgment unconditionally deleted the
// SecondVerificationPending after calling settleAuditedTask. settleAuditedTask
// has 5 early-return paths that never write a SettledTask record:
//
//   keeper.go:1671  bad UserAddress    (sdk.AccAddressFromBech32 fails)
//   keeper.go:1675  bad WorkerAddress  (sdk.AccAddressFromBech32 fails)
//   keeper.go:1680  InferenceAccount   missing
//   keeper.go:1713  SUCCESS path       balance < chargeAmount
//   keeper.go:1735  FAIL path          balance < failFee
//
// Hitting any of those after pending was deleted left the task as a permanent
// orphan: no SettledTask, no pending, no retry handle on chain.
//
// Post-fix: settleAuditedTask returns bool. processAuditJudgment only deletes
// pending on true. Pending stays alive for HandleSecondVerificationTimeouts to
// retry. At timeout the safety net force-writes a TaskFailed terminal record
// and deletes pending — guarantees no permanent orphan, even when settle never
// becomes possible (e.g. user balance permanently drained).
//
// Tests below pin both legs of the contract:
//   - TestKT_Issue2_*_PendingPreserved      — settle false → pending stays
//   - TestKT_Issue2_TimeoutRetrySucceeds    — re-deposit between attempts
//   - TestKT_Issue2_TimeoutForceTerminal    — both attempts fail, terminal written

import (
	"fmt"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/settlement/types"
)

// submit3PassAuditResults triggers processAuditJudgment with auditPass=true
// (3/3 PASS, threshold = 2). Used by the SUCCESS-path tests below.
func submit3PassAuditResults(t *testing.T, k auditTestKeeper, ctx sdk.Context, taskId []byte, prefix string) {
	t.Helper()
	for i := 0; i < 3; i++ {
		if err := k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
			SecondVerifier: makeAddr(fmt.Sprintf("%s-aud%d", prefix, i)).String(),
			TaskId:         taskId,
			Epoch:          1,
			Pass:           true,
			LogitsHash:     []byte("hash"),
		}); err != nil {
			t.Fatalf("audit result %d: %v", i, err)
		}
	}
}

// submit3FailAuditResults triggers processAuditJudgment with auditPass=false
// (0/3 PASS, threshold = 2). Used by the FAIL-confirmed path tests below.
func submit3FailAuditResults(t *testing.T, k auditTestKeeper, ctx sdk.Context, taskId []byte, prefix string) {
	t.Helper()
	for i := 0; i < 3; i++ {
		if err := k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
			SecondVerifier: makeAddr(fmt.Sprintf("%s-aud%d", prefix, i)).String(),
			TaskId:         taskId,
			Epoch:          1,
			Pass:           false,
			LogitsHash:     []byte("hash"),
		}); err != nil {
			t.Fatalf("audit result %d: %v", i, err)
		}
	}
}

// auditTestKeeper is the subset of keeper.Keeper methods used in this file.
// We accept the concrete type via local alias purely to avoid import churn.
type auditTestKeeper interface {
	ProcessSecondVerificationResult(ctx sdk.Context, msg *types.MsgSecondVerificationResult) error
}

// ============================================================
// KT-Issue2-A. Bad UserAddress → settleAuditedTask returns false at line 1671
// → pending must be preserved.
// ============================================================

func TestKT_Issue2_BadUserAddr_PendingPreserved(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	k.SetCurrentThirdVerificationRate(ctx, 0)

	taskId := []byte("kt-issue2-baduser-001")

	// Bypass MsgValidateBasic by writing pending directly with a malformed
	// bech32 string. AccAddressFromBech32 at keeper.go:1669 will reject it.
	k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       ctx.BlockHeight(),
		UserAddress:       "not-a-valid-bech32-address",
		WorkerAddress:     makeAddr("kt-i2bu-worker").String(),
		VerifierAddresses: []string{makeAddr("kt-i2bu-orig-v1").String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       10000,
	})

	submit3PassAuditResults(t, k, ctx, taskId, "kt-i2bu")

	// Pending must be preserved (settleAuditedTask returned false at line 1671).
	if _, found := k.GetSecondVerificationPending(ctx, taskId); !found {
		t.Fatal("KT-Issue2-A: pending must be preserved when UserAddress decode fails")
	}
	if _, found := k.GetSettledTask(ctx, taskId); found {
		t.Fatal("KT-Issue2-A: SettledTask must NOT be written when settle returns false")
	}
}

// ============================================================
// KT-Issue2-B. Bad WorkerAddress → return false at line 1675 → pending stays.
// ============================================================

func TestKT_Issue2_BadWorkerAddr_PendingPreserved(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	k.SetCurrentThirdVerificationRate(ctx, 0)

	taskId := []byte("kt-issue2-badworker-1")
	user := makeAddr("kt-i2bw-user")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

	k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       ctx.BlockHeight(),
		UserAddress:       user.String(),
		WorkerAddress:     "not-a-valid-bech32",
		VerifierAddresses: []string{makeAddr("kt-i2bw-orig-v1").String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       10000,
	})

	submit3PassAuditResults(t, k, ctx, taskId, "kt-i2bw")

	if _, found := k.GetSecondVerificationPending(ctx, taskId); !found {
		t.Fatal("KT-Issue2-B: pending must be preserved when WorkerAddress decode fails")
	}
	if _, found := k.GetSettledTask(ctx, taskId); found {
		t.Fatal("KT-Issue2-B: SettledTask must NOT be written when settle returns false")
	}
}

// ============================================================
// KT-Issue2-C. InferenceAccount missing → return false at line 1680.
// ============================================================

func TestKT_Issue2_NoInferenceAccount_PendingPreserved(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	k.SetCurrentThirdVerificationRate(ctx, 0)

	taskId := []byte("kt-issue2-noaccount-1")
	user := makeAddr("kt-i2na-noacct-user")
	// Note: NO ProcessDeposit. GetInferenceAccount will return found=false.

	k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       ctx.BlockHeight(),
		UserAddress:       user.String(),
		WorkerAddress:     makeAddr("kt-i2na-worker").String(),
		VerifierAddresses: []string{makeAddr("kt-i2na-orig-v1").String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       10000,
	})

	submit3PassAuditResults(t, k, ctx, taskId, "kt-i2na")

	if _, found := k.GetSecondVerificationPending(ctx, taskId); !found {
		t.Fatal("KT-Issue2-C: pending must be preserved when InferenceAccount missing")
	}
	if _, found := k.GetSettledTask(ctx, taskId); found {
		t.Fatal("KT-Issue2-C: SettledTask must NOT be written when settle returns false")
	}
}

// ============================================================
// KT-Issue2-D. SUCCESS path: balance < chargeAmount → return false at line 1713.
//
// This is the most likely real-world trigger: user dispatched a task with
// pending fee, then withdrew most of their balance before audit completion
// (per-request billing has no on-chain freeze — KT 30-case Issue 1).
// ============================================================

func TestKT_Issue2_SuccessPath_BalanceShortfall_PendingPreserved(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	k.SetCurrentThirdVerificationRate(ctx, 0)

	taskId := []byte("kt-issue2-succ-bal-01")
	user := makeAddr("kt-i2sb-poor-user")
	// Deposit < fee → SUCCESS path will fail balance check.
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(100)))

	k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       ctx.BlockHeight(),
		UserAddress:       user.String(),
		WorkerAddress:     makeAddr("kt-i2sb-worker").String(),
		VerifierAddresses: []string{makeAddr("kt-i2sb-orig-v1").String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)), // way more than 100
		ExpireBlock:       10000,
	})

	submit3PassAuditResults(t, k, ctx, taskId, "kt-i2sb")

	if _, found := k.GetSecondVerificationPending(ctx, taskId); !found {
		t.Fatal("KT-Issue2-D: pending must be preserved when SUCCESS-path balance check fails")
	}
	if _, found := k.GetSettledTask(ctx, taskId); found {
		t.Fatal("KT-Issue2-D: SettledTask must NOT be written when settle returns false")
	}
	// Balance untouched (no fee was charged).
	ia, _ := k.GetInferenceAccount(ctx, user)
	if !ia.Balance.Amount.Equal(math.NewInt(100)) {
		t.Fatalf("KT-Issue2-D: balance should be unchanged, got %s", ia.Balance.Amount)
	}
}

// ============================================================
// KT-Issue2-E. FAIL-confirmed path: balance < failFee → return false at line 1735.
//
// OriginalStatus=FAIL + auditPass=false → keeper.go:1583/1589 calls
// settleAuditedTask(asSuccess=false). FailFee = fee × FailSettlementFeeRatio /
// 1000 (default 150/1000 = 15%). Setup: deposit < failFee.
// ============================================================

func TestKT_Issue2_FailPath_BalanceShortfall_PendingPreserved(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	k.SetCurrentThirdVerificationRate(ctx, 0)

	taskId := []byte("kt-issue2-fail-bal-01")
	user := makeAddr("kt-i2fb-poor-user")
	// failFee = 1_000_000 × 150 / 1000 = 150_000. Deposit far less.
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(50)))

	verifiers := []string{
		makeAddr("kt-i2fb-orig-v1").String(),
		makeAddr("kt-i2fb-orig-v2").String(),
		makeAddr("kt-i2fb-orig-v3").String(),
	}
	k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementFail,
		SubmittedAt:       ctx.BlockHeight(),
		UserAddress:       user.String(),
		WorkerAddress:     makeAddr("kt-i2fb-worker").String(),
		VerifierAddresses: verifiers,
		VerifierVotes:     []bool{false, false, false},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       10000,
	})

	// 3 FAIL audits → !auditPass → keeper.go:1583/1589 → settleAuditedTask(asSuccess=false)
	// → fails on balance check at keeper.go:1735.
	submit3FailAuditResults(t, k, ctx, taskId, "kt-i2fb")

	if _, found := k.GetSecondVerificationPending(ctx, taskId); !found {
		t.Fatal("KT-Issue2-E: pending must be preserved when FAIL-path balance check fails")
	}
	if _, found := k.GetSettledTask(ctx, taskId); found {
		t.Fatal("KT-Issue2-E: SettledTask must NOT be written when settle returns false")
	}
	ia, _ := k.GetInferenceAccount(ctx, user)
	if !ia.Balance.Amount.Equal(math.NewInt(50)) {
		t.Fatalf("KT-Issue2-E: balance should be unchanged, got %s", ia.Balance.Amount)
	}
}

// ============================================================
// KT-Issue2-F. Timeout retry succeeds after re-deposit.
//
// Round 1: balance shortfall → pending preserved (no SettledTask).
// User re-deposits.
// Block height advanced past SecondVerificationTimeout.
// Round 2: HandleSecondVerificationTimeouts retries; balance now sufficient
// → settle succeeds → pending deleted, SettledTask written.
// ============================================================

func TestKT_Issue2_TimeoutRetrySucceedsAfterRedeposit(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	k.SetCurrentThirdVerificationRate(ctx, 0)

	taskId := []byte("kt-issue2-retry-001")
	user := makeAddr("kt-i2re-user")
	worker := makeAddr("kt-i2re-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(100)))

	k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       ctx.BlockHeight(),
		UserAddress:       user.String(),
		WorkerAddress:     worker.String(),
		VerifierAddresses: []string{makeAddr("kt-i2re-orig-v1").String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       10000,
	})

	submit3PassAuditResults(t, k, ctx, taskId, "kt-i2re")

	// Round 1 — settle failed, pending preserved.
	if _, found := k.GetSecondVerificationPending(ctx, taskId); !found {
		t.Fatal("KT-Issue2-F: pending must be preserved after first attempt fails")
	}
	if _, found := k.GetSettledTask(ctx, taskId); found {
		t.Fatal("KT-Issue2-F: no SettledTask after first attempt")
	}

	// User re-deposits enough to cover the fee.
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(2_000_000)))

	// Advance past audit timeout.
	params := types.DefaultParams()
	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + params.SecondVerificationTimeout + 1)

	// Round 2 — timeout retry should succeed.
	processed := k.HandleSecondVerificationTimeouts(ctx)
	if processed < 1 {
		t.Fatalf("KT-Issue2-F: HandleSecondVerificationTimeouts should process the pending, got %d", processed)
	}

	// Pending now deleted, SettledTask written as TaskSettled.
	if _, found := k.GetSecondVerificationPending(ctx, taskId); found {
		t.Fatal("KT-Issue2-F: pending must be deleted after successful timeout retry")
	}
	st, found := k.GetSettledTask(ctx, taskId)
	if !found {
		t.Fatal("KT-Issue2-F: SettledTask must exist after successful retry")
	}
	if st.Status != types.TaskSettled {
		t.Fatalf("KT-Issue2-F: expected TaskSettled, got %s", st.Status)
	}
}

// ============================================================
// KT-Issue2-G. Timeout force-terminal when both attempts fail.
//
// Round 1: balance shortfall → pending preserved.
// User does NOT re-deposit (stays poor).
// Block height advanced past SecondVerificationTimeout.
// Round 2: HandleSecondVerificationTimeouts retries; settle still fails;
// safety net writes a TaskFailed record (no fees) and deletes pending.
// ============================================================

func TestKT_Issue2_TimeoutForceTerminal(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	k.SetCurrentThirdVerificationRate(ctx, 0)

	taskId := []byte("kt-issue2-terminal-01")
	user := makeAddr("kt-i2tt-user")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(100)))

	k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       ctx.BlockHeight(),
		UserAddress:       user.String(),
		WorkerAddress:     makeAddr("kt-i2tt-worker").String(),
		VerifierAddresses: []string{makeAddr("kt-i2tt-orig-v1").String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       10000,
	})

	submit3PassAuditResults(t, k, ctx, taskId, "kt-i2tt")

	// Round 1 — settle failed, pending preserved.
	if _, found := k.GetSecondVerificationPending(ctx, taskId); !found {
		t.Fatal("KT-Issue2-G: pending must be preserved after first attempt fails")
	}

	// No re-deposit. Advance past timeout.
	params := types.DefaultParams()
	ctx = ctx.WithBlockHeight(ctx.BlockHeight() + params.SecondVerificationTimeout + 1)

	// Round 2 — timeout retry; settle still fails (poor balance) → force-terminal.
	processed := k.HandleSecondVerificationTimeouts(ctx)
	if processed < 1 {
		t.Fatalf("KT-Issue2-G: HandleSecondVerificationTimeouts should process the pending, got %d", processed)
	}

	// Pending must be deleted (timeout is the last chance).
	if _, found := k.GetSecondVerificationPending(ctx, taskId); found {
		t.Fatal("KT-Issue2-G: pending must be deleted at timeout (force-terminal)")
	}
	// SettledTask must exist with status TaskFailed (no fees collected).
	st, found := k.GetSettledTask(ctx, taskId)
	if !found {
		t.Fatal("KT-Issue2-G: SettledTask must be force-written at timeout")
	}
	if st.Status != types.TaskFailed {
		t.Fatalf("KT-Issue2-G: expected TaskFailed at force-terminal, got %s", st.Status)
	}

	// Balance still untouched — force-terminal does not collect a fee.
	ia, _ := k.GetInferenceAccount(ctx, user)
	if !ia.Balance.Amount.Equal(math.NewInt(100)) {
		t.Fatalf("KT-Issue2-G: balance should be unchanged at force-terminal, got %s", ia.Balance.Amount)
	}
}
