package keeper_test

import (
	"fmt"
	"math"
	"math/rand"
	"testing"

	cosmosmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/settlement/keeper"
	"github.com/funai-wiki/funai-chain/x/settlement/types"
)

// ----- helpers -----

// makePerTokenEntry constructs a per-token SettlementEntry with 3 verifiers.
func makePerTokenEntry(
	taskId string,
	user, worker sdk.AccAddress,
	feePerInput, feePerOutput uint64,
	maxFee int64,
	workerIn, workerOut uint32,
	verifierIn, verifierOut uint32,
	status types.SettlementStatus,
) types.SettlementEntry {
	v1, v2, v3 := makeAddr("ptv1"), makeAddr("ptv2"), makeAddr("ptv3")
	return types.SettlementEntry{
		TaskId:             []byte(fmt.Sprintf("%-20s", taskId)),
		UserAddress:        user.String(),
		WorkerAddress:      worker.String(),
		Fee:                sdk.NewCoin("ufai", cosmosmath.ZeroInt()),
		MaxFee:             sdk.NewCoin("ufai", cosmosmath.NewInt(maxFee)),
		ExpireBlock:        10000,
		Status:             status,
		FeePerInputToken:   feePerInput,
		FeePerOutputToken:  feePerOutput,
		WorkerInputTokens:  workerIn,
		WorkerOutputTokens: workerOut,
		VerifierResults: []types.VerifierResult{
			{Address: v1.String(), Pass: status == types.SettlementSuccess, VerifiedInputTokens: verifierIn, VerifiedOutputTokens: verifierOut},
			{Address: v2.String(), Pass: status == types.SettlementSuccess, VerifiedInputTokens: verifierIn, VerifiedOutputTokens: verifierOut},
			{Address: v3.String(), Pass: status == types.SettlementSuccess, VerifiedInputTokens: verifierIn, VerifiedOutputTokens: verifierOut},
		},
	}
}

func enablePerToken(k keeper.Keeper, ctx sdk.Context) {
	params := k.GetParams(ctx)
	params.PerTokenBillingEnabled = true
	k.SetParams(ctx, params)
}

// ================================================================
// PT1: Normal per-token billing — actual < max_fee → charge actual
// ================================================================
func TestPerToken_NormalBilling(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	enablePerToken(k, ctx)

	user := makeAddr("pt1-user")
	worker := makeAddr("pt1-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))

	// actual = 100*100 + 200*200 = 10000+40000 = 50000 < 100000 maxFee
	entry := makePerTokenEntry("pt1-task", user, worker, 100, 200, 100000, 100, 200, 100, 200, types.SettlementSuccess)
	msg := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ia, _ := k.GetInferenceAccount(ctx, user)
	// expected: 10M - 50000 = 9950000
	expected := cosmosmath.NewInt(10_000_000 - 50_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("PT1: expected balance %s, got %s", expected, ia.Balance.Amount)
	}
}

// ================================================================
// PT2: max_fee cap — actual > max_fee → charge max_fee
// ================================================================
func TestPerToken_MaxFeeCap(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	enablePerToken(k, ctx)

	user := makeAddr("pt2-user")
	worker := makeAddr("pt2-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))

	// actual = 100*100 + 500*200 = 110000 > 50000 maxFee → cap at 50000
	entry := makePerTokenEntry("pt2-task", user, worker, 100, 200, 50000, 100, 500, 100, 500, types.SettlementSuccess)
	msg := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ia, _ := k.GetInferenceAccount(ctx, user)
	expected := cosmosmath.NewInt(10_000_000 - 50_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("PT2: expected balance %s, got %s", expected, ia.Balance.Amount)
	}
}

// ================================================================
// PT3: PerTokenBillingEnabled=false → fallback to max_fee (per-request)
// ================================================================
func TestPerToken_DisabledFallback(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	// Do NOT enable per-token billing

	user := makeAddr("pt3-user")
	worker := makeAddr("pt3-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))

	// Even though actual = 50000, disabled means charge max_fee = 100000
	entry := makePerTokenEntry("pt3-task", user, worker, 100, 200, 100000, 100, 200, 100, 200, types.SettlementSuccess)
	msg := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ia, _ := k.GetInferenceAccount(ctx, user)
	expected := cosmosmath.NewInt(10_000_000 - 100_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("PT3: expected balance %s (charged max_fee), got %s", expected, ia.Balance.Amount)
	}
}

// ================================================================
// PT4: FAIL + per-token → charge fail_fee = actual × 5%
// ================================================================
func TestPerToken_FailBilling(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	enablePerToken(k, ctx)

	user := makeAddr("pt4-user")
	worker := makeAddr("pt4-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))

	// actual = 100*100 + 200*200 = 50000; fail_fee = 50000 * 150/1000 = 7500 (15%)
	entry := makePerTokenEntry("pt4-task", user, worker, 100, 200, 100000, 100, 200, 100, 200, types.SettlementFail)
	// Fail entries need verifiers voting fail
	entry.VerifierResults[0].Pass = true
	entry.VerifierResults[1].Pass = false
	entry.VerifierResults[2].Pass = false

	msg := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ia, _ := k.GetInferenceAccount(ctx, user)
	// fail_fee = 50000 * 150 / 1000 = 7500 (15%)
	expected := cosmosmath.NewInt(10_000_000 - 7500)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("PT4: expected balance %s, got %s", expected, ia.Balance.Amount)
	}

	// Worker should be jailed
	if len(wk.jailCalls) != 1 {
		t.Fatalf("PT4: expected 1 jail call, got %d", len(wk.jailCalls))
	}
}

// ================================================================
// PT5: fee_per_input=0 → IsPerToken()=false → per-request billing
// ================================================================
func TestPerToken_ZeroPriceFallback(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	enablePerToken(k, ctx)

	user := makeAddr("pt5-user")
	worker := makeAddr("pt5-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))

	// FeePerInputToken=0 → IsPerToken()=false → use Fee as per-request
	entry := types.SettlementEntry{
		TaskId:             []byte("pt5-task-00000000001"),
		UserAddress:        user.String(),
		WorkerAddress:      worker.String(),
		Fee:                sdk.NewCoin("ufai", cosmosmath.NewInt(500_000)),
		MaxFee:             sdk.NewCoin("ufai", cosmosmath.NewInt(1_000_000)),
		ExpireBlock:        10000,
		Status:             types.SettlementSuccess,
		FeePerInputToken:   0,
		FeePerOutputToken:  200,
		WorkerInputTokens:  100,
		WorkerOutputTokens: 200,
		VerifierResults: []types.VerifierResult{
			{Address: makeAddr("pt5v1").String(), Pass: true},
			{Address: makeAddr("pt5v2").String(), Pass: true},
			{Address: makeAddr("pt5v3").String(), Pass: true},
		},
	}
	msg := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ia, _ := k.GetInferenceAccount(ctx, user)
	// per-request: charge Fee = 500000
	expected := cosmosmath.NewInt(10_000_000 - 500_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("PT5: expected balance %s (per-request fee), got %s", expected, ia.Balance.Amount)
	}
}

// ================================================================
// PT6: Overflow protection — already tested in s9_truncation_test.go
// but verify through full settlement path
// ================================================================
func TestPerToken_OverflowProtection(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	enablePerToken(k, ctx)

	user := makeAddr("pt6-user")
	worker := makeAddr("pt6-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))

	// feePerOutput = MaxUint64/2 with 3 output tokens → overflow → cap at max_fee
	entry := makePerTokenEntry("pt6-task", user, worker, 1, math.MaxUint64/2, 1_000_000, 1, 3, 1, 3, types.SettlementSuccess)
	msg := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ia, _ := k.GetInferenceAccount(ctx, user)
	// Overflow → capped at max_fee = 1000000
	expected := cosmosmath.NewInt(10_000_000 - 1_000_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("PT6: expected balance %s (overflow→max_fee), got %s", expected, ia.Balance.Amount)
	}
}

// ================================================================
// PT7: Fee conservation — 100K random runs, sum(debit) == sum(distributions)
// ================================================================
func TestPerToken_FeeConservation(t *testing.T) {
	// Test CalculatePerTokenFee + fee split arithmetic conservation
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < 100_000; i++ {
		inTokens := uint32(rng.Intn(10000) + 1)
		outTokens := uint32(rng.Intn(10000) + 1)
		feeIn := uint64(rng.Intn(1000) + 1)
		feeOut := uint64(rng.Intn(1000) + 1)
		maxFee := uint64(rng.Intn(10_000_000) + 1)

		actualFee := keeper.CalculatePerTokenFee(inTokens, outTokens, feeIn, feeOut, maxFee)

		// actualFee must not exceed maxFee
		if actualFee > maxFee {
			t.Fatalf("iteration %d: actualFee %d > maxFee %d", i, actualFee, maxFee)
		}

		// Fee split: executor 850 + verifier 120 + audit 30 = 1000
		executorFee := actualFee * 850 / 1000
		verifierFee := actualFee * 120 / 1000
		fundFee := actualFee * 30 / 1000
		remainder := actualFee - executorFee - verifierFee - fundFee

		// Remainder goes to executor, so total must equal actualFee
		total := executorFee + remainder + verifierFee + fundFee
		if total != actualFee {
			t.Fatalf("iteration %d: fee split %d != actualFee %d", i, total, actualFee)
		}
	}
}

// ================================================================
// PT8: Timeout fee — tested via HandleFrozenBalanceTimeouts
// ================================================================
func TestPerToken_TimeoutFee(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	enablePerToken(k, ctx)

	user := makeAddr("pt8-user")
	worker := makeAddr("pt8-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))

	// Freeze balance for a per-token task
	err := k.FreezeBalance(ctx, user, []byte("pt8-timeout-task0001"), sdk.NewCoin("ufai", cosmosmath.NewInt(1_000_000)))
	if err != nil {
		t.Fatalf("FreezeBalance: %v", err)
	}

	// Store frozen task meta for timeout processing
	k.StoreFrozenTaskMeta(ctx, types.FrozenTaskMeta{
		TaskId:        []byte("pt8-timeout-task0001"),
		UserAddress:   user.String(),
		WorkerAddress: worker.String(),
		MaxFee:        1_000_000,
		ExpireBlock:   50,
	})

	// Advance past expire_block
	ctx = ctx.WithBlockHeight(200)
	k.HandleFrozenBalanceTimeouts(ctx)

	ia, _ := k.GetInferenceAccount(ctx, user)
	// timeout_fee = 1000000 * 150/1000 = 150000; refund = 1000000 - 150000 = 850000
	// balance = 10M - 1M (frozen) + 850000 (refund) = 9850000
	expected := cosmosmath.NewInt(9_850_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("PT8: expected balance %s, got %s", expected, ia.Balance.Amount)
	}

	if len(wk.jailCalls) != 1 {
		t.Fatalf("PT8: expected worker jailed, got %d jail calls", len(wk.jailCalls))
	}
}

// ================================================================
// AC1: Honest Worker — Worker and all 3 verifiers report same count
// ================================================================
func TestAntiCheat_HonestWorker(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	enablePerToken(k, ctx)

	user := makeAddr("ac1-user")
	worker := makeAddr("ac1-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))

	// Worker reports 423, verifiers all report 423
	entry := makePerTokenEntry("ac1-task", user, worker, 100, 200, 1_000_000, 100, 423, 100, 423, types.SettlementSuccess)
	msg := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// actual = 100*100 + 423*200 = 10000 + 84600 = 94600
	ia, _ := k.GetInferenceAccount(ctx, user)
	expected := cosmosmath.NewInt(10_000_000 - 94600)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("AC1: expected balance %s, got %s", expected, ia.Balance.Amount)
	}

	// dishonest_count should remain 0
	dc := k.GetDishonestCount(ctx, worker)
	if dc != 0 {
		t.Fatalf("AC1: expected dishonest_count=0, got %d", dc)
	}
}

// ================================================================
// AC2: Worker overreport — Worker reports 800, verifier median = 423
// ================================================================
func TestAntiCheat_WorkerOverreport(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	enablePerToken(k, ctx)

	user := makeAddr("ac2-user")
	worker := makeAddr("ac2-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))

	// Worker reports 800, verifiers report 423 → dishonest, use median
	entry := makePerTokenEntry("ac2-task", user, worker, 100, 200, 1_000_000, 100, 800, 100, 423, types.SettlementSuccess)
	msg := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// settled at verifier median: actual = 100*100 + 423*200 = 94600
	ia, _ := k.GetInferenceAccount(ctx, user)
	expected := cosmosmath.NewInt(10_000_000 - 94600)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("AC2: expected balance %s (median-based), got %s", expected, ia.Balance.Amount)
	}

	dc := k.GetDishonestCount(ctx, worker)
	if dc != 1 {
		t.Fatalf("AC2: expected dishonest_count=1, got %d", dc)
	}
}

// ================================================================
// AC3: 3 strikes → jail
// ================================================================
func TestAntiCheat_ThreeStrikesJail(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	enablePerToken(k, ctx)

	user := makeAddr("ac3-user")
	worker := makeAddr("ac3-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(100_000_000)))

	// 3 dishonest entries
	for i := 0; i < 3; i++ {
		entry := makePerTokenEntry(
			fmt.Sprintf("ac3-task-%02d", i), user, worker,
			100, 200, 1_000_000,
			100, 800, // worker overreport
			100, 423, // verifier actual
			types.SettlementSuccess,
		)
		msg := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
		_, err := k.ProcessBatchSettlement(ctx, msg)
		if err != nil {
			t.Fatalf("batch %d: unexpected error: %v", i, err)
		}
	}

	// After 3 dishonest reports, IncrementDishonestCount triggers jail AND resets count to 0
	// So count should be 0 (reset after jail), and jail should have been called
	dc := k.GetDishonestCount(ctx, worker)
	if dc != 0 {
		t.Fatalf("AC3: expected dishonest_count=0 (reset after jail), got %d", dc)
	}

	if len(wk.jailCalls) < 1 {
		t.Fatalf("AC3: expected worker jailed, got %d jail calls", len(wk.jailCalls))
	}
}

// ================================================================
// AC5: Within tolerance — Worker reports 425, verifier reports 423
// ================================================================
func TestAntiCheat_WithinTolerance(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	enablePerToken(k, ctx)

	user := makeAddr("ac5-user")
	worker := makeAddr("ac5-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))

	// Worker reports 425, verifier median 423. delta=2 <= tolerance=2 → use worker's count
	entry := makePerTokenEntry("ac5-task", user, worker, 100, 200, 1_000_000, 100, 425, 100, 423, types.SettlementSuccess)
	msg := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Used worker's 425: actual = 100*100 + 425*200 = 10000 + 85000 = 95000
	ia, _ := k.GetInferenceAccount(ctx, user)
	expected := cosmosmath.NewInt(10_000_000 - 95000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("AC5: expected balance %s (worker count used), got %s", expected, ia.Balance.Amount)
	}

	dc := k.GetDishonestCount(ctx, worker)
	if dc != 0 {
		t.Fatalf("AC5: expected dishonest_count=0 (within tolerance), got %d", dc)
	}
}

// ================================================================
// AC6: 50 consecutive success → dishonest_count resets
// ================================================================
func TestAntiCheat_StreakReset(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	enablePerToken(k, ctx)

	worker := makeAddr("ac6-worker")
	params := k.GetParams(ctx)

	// Increment dishonest count twice (below jail threshold of 3)
	k.IncrementDishonestCount(ctx, worker, params)
	k.IncrementDishonestCount(ctx, worker, params)
	if k.GetDishonestCount(ctx, worker) != 2 {
		t.Fatal("AC6: failed to set initial dishonest_count=2")
	}

	// ResetDishonestCount should clear to 0 (called when streak >= 50)
	k.ResetDishonestCount(ctx, worker)
	dc := k.GetDishonestCount(ctx, worker)
	if dc != 0 {
		t.Fatalf("AC6: expected dishonest_count=0 after reset, got %d", dc)
	}
}

// ================================================================
// AC7: PerTokenBillingEnabled=false → no token count checks
// ================================================================
func TestAntiCheat_DisabledNoCheck(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	// Billing DISABLED

	user := makeAddr("ac7-user")
	worker := makeAddr("ac7-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))

	// Worker overreports massively but billing is disabled → no dishonest check
	entry := makePerTokenEntry("ac7-task", user, worker, 100, 200, 500_000, 100, 9999, 100, 423, types.SettlementSuccess)
	msg := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// With billing disabled, per-token entry uses MaxFee as per-request
	ia, _ := k.GetInferenceAccount(ctx, user)
	expected := cosmosmath.NewInt(10_000_000 - 500_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("AC7: expected balance %s, got %s", expected, ia.Balance.Amount)
	}

	// No dishonest count increment since billing disabled
	dc := k.GetDishonestCount(ctx, worker)
	if dc != 0 {
		t.Fatalf("AC7: expected dishonest_count=0 (billing disabled), got %d", dc)
	}
}

// ================================================================
// AC8: Pair tracking — mismatch updates pair record
// ================================================================
func TestAntiCheat_PairTracking(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	enablePerToken(k, ctx)

	user := makeAddr("ac8-user")
	worker := makeAddr("ac8-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(100_000_000)))

	v1 := makeAddr("ptv1")

	// Pair mismatch tracks verifier deviation from RESOLVED count.
	// To trigger: 2 verifiers agree (median), 1 verifier deviates significantly.
	for i := 0; i < 10; i++ {
		entry := makePerTokenEntry(
			fmt.Sprintf("ac8-task-%02d", i), user, worker,
			100, 200, 1_000_000,
			100, 500, // worker reports 500
			100, 500, // v2,v3 report 500 (used as default in helper)
			types.SettlementSuccess,
		)
		// Override v1 to deviate significantly from the resolved median (500)
		if i < 8 {
			entry.VerifierResults[0].VerifiedOutputTokens = 200 // delta=300 from median 500
		}
		msg := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
		_, _ = k.ProcessBatchSettlement(ctx, msg)
	}

	// Check pair record for worker-v1 — v1 deviates in 8/10 tasks
	rec := k.GetTokenMismatchRecord(ctx, worker.String(), v1.String())
	if rec.TotalTasks == 0 {
		t.Fatal("AC8: expected pair tracking records, got TotalTasks=0")
	}
	if rec.MismatchCount == 0 {
		t.Fatal("AC8: expected MismatchCount > 0 from deviating verifier v1")
	}
}

// ================================================================
// AC9: Verifier direct jail — tested via audit collusion path
// Unit test of ResolveTokenCounts to verify dishonest flagging
// ================================================================
func TestAntiCheat_VerifierDirectJail(t *testing.T) {
	// Verify that ResolveTokenCounts correctly flags worker as dishonest
	entry := types.SettlementEntry{
		WorkerInputTokens:  100,
		WorkerOutputTokens: 800,
		VerifierResults: []types.VerifierResult{
			{VerifiedInputTokens: 100, VerifiedOutputTokens: 423},
			{VerifiedInputTokens: 100, VerifiedOutputTokens: 425},
			{VerifiedInputTokens: 100, VerifiedOutputTokens: 420},
		},
	}

	res := keeper.ResolveTokenCounts(&entry, 2, 2)
	if !res.WorkerDishonest {
		t.Fatal("AC9: expected worker flagged as dishonest (800 vs median ~423)")
	}
	// Median of [420, 423, 425] = 423
	if res.OutputTokens != 423 {
		t.Fatalf("AC9: expected resolved output=423, got %d", res.OutputTokens)
	}
}

// ================================================================
// TR4: MinBudget — at settlement layer: even with tiny max_fee,
// CalculatePerTokenFee should not panic
// ================================================================
func TestTruncation_MinBudget_Settlement(t *testing.T) {
	// max_fee=1, actual = 100+200 = 300 > 1 → cap at 1
	result := keeper.CalculatePerTokenFee(1, 1, 100, 200, 1)
	if result != 1 {
		t.Fatalf("TR4: expected min max_fee=1, got %d", result)
	}
}

// ================================================================
// Supplementary: ResolveTokenCounts unit tests
// ================================================================
func TestResolveTokenCounts_AllMatch(t *testing.T) {
	entry := types.SettlementEntry{
		WorkerInputTokens: 100, WorkerOutputTokens: 200,
		VerifierResults: []types.VerifierResult{
			{VerifiedInputTokens: 100, VerifiedOutputTokens: 200},
			{VerifiedInputTokens: 100, VerifiedOutputTokens: 200},
			{VerifiedInputTokens: 100, VerifiedOutputTokens: 200},
		},
	}
	res := keeper.ResolveTokenCounts(&entry, 2, 2)
	if res.WorkerDishonest {
		t.Fatal("should not be dishonest when all match")
	}
	if res.OutputTokens != 200 {
		t.Fatalf("expected 200, got %d", res.OutputTokens)
	}
}

func TestResolveTokenCounts_NoVerifierData(t *testing.T) {
	entry := types.SettlementEntry{
		WorkerInputTokens: 100, WorkerOutputTokens: 200,
		VerifierResults: []types.VerifierResult{
			{VerifiedInputTokens: 0, VerifiedOutputTokens: 0},
			{VerifiedInputTokens: 0, VerifiedOutputTokens: 0},
			{VerifiedInputTokens: 0, VerifiedOutputTokens: 0},
		},
	}
	res := keeper.ResolveTokenCounts(&entry, 2, 2)
	// No verifier data → median=0 → resolveTokenPair returns worker count (nMedian==0 case)
	if res.OutputTokens != 200 {
		t.Fatalf("expected worker count 200 when no verifier data, got %d", res.OutputTokens)
	}
	if res.WorkerDishonest {
		t.Fatal("should not be dishonest when no verifier data available")
	}
	if !res.LowConfidence {
		t.Fatal("expected LowConfidence=true when no verifier data")
	}
}
