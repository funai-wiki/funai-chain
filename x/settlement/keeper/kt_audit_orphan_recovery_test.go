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
// KT-Issue2-D. SUCCESS path: balance < chargeAmount.
//
// Pre-Rule-4 behaviour: settleAuditedTask returned false on shortfall →
// pending preserved → user could re-deposit and HandleSecondVerificationTimeouts
// would retry. The test asserted that two-round flow.
//
// Rule 4 (audit max_fee pre-authorisation): no silent drops. The audit
// re-settle now lands partial-pay — Worker absorbs the shortfall, the
// task settles in round 1, pending is deleted. The "re-deposit + retry"
// flow no longer applies (preserved as TimeoutRetry was deleted below).
// ============================================================

func TestKT_Issue2_SuccessPath_BalanceShortfall_SettlesPartialPay(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	k.SetCurrentThirdVerificationRate(ctx, 0)

	taskId := []byte("kt-issue2-succ-bal-01")
	user := makeAddr("kt-i2sb-poor-user")
	// Deposit < fee → SUCCESS audit re-settle will hit Rule 4 partial-pay.
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

	// Rule 4: pending deleted (settled), SettledTask written with partial Fee.
	if _, found := k.GetSecondVerificationPending(ctx, taskId); found {
		t.Fatal("Rule 4: pending must be deleted on partial-pay settle")
	}
	st, found := k.GetSettledTask(ctx, taskId)
	if !found {
		t.Fatal("Rule 4: SettledTask must be written even on shortfall — never silently drop")
	}
	if st.Status != types.TaskSettled {
		t.Fatalf("Rule 4: expected TaskSettled on shortfall partial-pay, got %s", st.Status)
	}
	// Balance fully drained.
	ia, _ := k.GetInferenceAccount(ctx, user)
	if !ia.Balance.Amount.Equal(math.NewInt(0)) {
		t.Fatalf("Rule 4: balance fully consumed by partial-pay, got %s", ia.Balance.Amount)
	}
}

// ============================================================
// KT-Issue2-E. FAIL-confirmed path: balance < failFee.
//
// Pre-Rule-4: pending preserved on shortfall.
// Post-Rule-4: settles partial-pay (FAIL still jails Worker), pending deleted.
// ============================================================

func TestKT_Issue2_FailPath_BalanceShortfall_SettlesPartialPay(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
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

	// 3 FAIL audits → settleAuditedTask(asSuccess=false) → Rule 4 partial-pay.
	submit3FailAuditResults(t, k, ctx, taskId, "kt-i2fb")

	if _, found := k.GetSecondVerificationPending(ctx, taskId); found {
		t.Fatal("Rule 4: pending must be deleted on partial-pay settle")
	}
	st, found := k.GetSettledTask(ctx, taskId)
	if !found {
		t.Fatal("Rule 4: SettledTask must be written on FAIL-path shortfall partial-pay")
	}
	if st.Status != types.TaskFailSettled {
		t.Fatalf("Rule 4: expected TaskFailSettled, got %s", st.Status)
	}
	ia, _ := k.GetInferenceAccount(ctx, user)
	if !ia.Balance.Amount.Equal(math.NewInt(0)) {
		t.Fatalf("Rule 4: balance fully consumed by partial fail-fee, got %s", ia.Balance.Amount)
	}
	if len(wk.jailCalls) != 1 {
		t.Fatalf("FAIL still jails Worker independent of shortfall, got %d jails", len(wk.jailCalls))
	}
}

// KT-Issue2-F + KT-Issue2-G removed.
//
// Pre-Rule-4 those tests pinned the "two-round" flow: round 1 hits a balance
// shortfall and returns false → pending preserved; round 2 (via
// HandleSecondVerificationTimeouts) either retries successfully after a
// re-deposit (F) or force-terminates with a TaskFailed record at audit
// timeout (G). Rule 4 (audit max_fee pre-authorisation: never silently drop
// a settled task) settles partial-pay in round 1 itself, deleting pending
// immediately. The two-round retry-on-shortfall flow no longer exists for
// balance shortfalls.
//
// Orphan recovery still applies for the OTHER settleAuditedTask
// false-return triggers — bad UserAddress, bad WorkerAddress, missing
// InferenceAccount, distributeSuccessFee SendCoins failure — covered by
// tests A, B, C above and the integration timeout test in
// integration_test.go. If a future trigger is added that legitimately
// wants the two-round flow, copy the F/G shape from git history at
// commit 215a79e.
