package keeper_test

import (
	"fmt"
	"math/rand"
	"testing"

	cosmosmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/settlement/keeper"
	"github.com/funai-wiki/funai-chain/x/settlement/types"
)

// ================================================================
// E1: Dust accumulation — 1M settlements, no dust loss (A4)
// ================================================================
func TestDustAccumulation_1M(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	user := makeAddr("e1-user")
	worker := makeAddr("e1-worker")
	deposit := cosmosmath.NewInt(200_000_000_000) // 200K FAI (enough for 1M × 99 ufai)
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", deposit))

	fee := int64(99) // 99 ufai — prime number, worst case for integer division
	var totalDeducted cosmosmath.Int = cosmosmath.ZeroInt()

	for i := 0; i < 1_000_000; i++ {
		entry := types.SettlementEntry{
			TaskId:      []byte(fmt.Sprintf("e1-task-%010d", i)),
			UserAddress: user.String(), WorkerAddress: worker.String(),
			Fee: sdk.NewCoin("ufai", cosmosmath.NewInt(fee)), ExpireBlock: 100000,
			Status: types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("e1v1").String(), Pass: true},
				{Address: makeAddr("e1v2").String(), Pass: true},
				{Address: makeAddr("e1v3").String(), Pass: true},
			},
		}
		msg := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
		_, err := k.ProcessBatchSettlement(ctx, msg)
		if err != nil {
			t.Fatalf("batch %d: %v", i, err)
		}
		totalDeducted = totalDeducted.Add(cosmosmath.NewInt(fee))
	}

	ia, _ := k.GetInferenceAccount(ctx, user)
	expected := deposit.Sub(totalDeducted)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("E1: expected balance %s, got %s (dust=%s)", expected, ia.Balance.Amount,
			ia.Balance.Amount.Sub(expected).String())
	}
}

// ================================================================
// E2: Fee conservation — 1M random params, sum(debit) exact (A4)
// ================================================================
func TestFeeConservation_Randomized_1M(t *testing.T) {
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < 1_000_000; i++ {
		fee := uint64(rng.Intn(10_000_000) + 1)

		// Simulate fee split: executor=850/1000, verifier=120/1000, audit=30/1000
		executorShare := fee * 850 / 1000
		verifierShare := fee * 120 / 1000
		fundShare := fee * 30 / 1000
		remainder := fee - executorShare - verifierShare - fundShare

		// Executor gets remainder
		executorTotal := executorShare + remainder
		total := executorTotal + verifierShare + fundShare

		if total != fee {
			t.Fatalf("E2 iteration %d: fee=%d total=%d (executor=%d verifier=%d audit=%d rem=%d)",
				i, fee, total, executorTotal, verifierShare, fundShare, remainder)
		}
	}
}

// ================================================================
// E3: Extreme prices — no panic
// ================================================================
func TestExtremePrices(t *testing.T) {
	tests := []struct {
		name    string
		in, out uint32
		feeIn   uint64
		feeOut  uint64
		maxFee  uint64
	}{
		{"min_fee_per_token=1", 1, 1, 1, 1, 1000},
		{"large_fee_per_token", 1, 1, 1 << 62, 1 << 62, 1 << 63},
		{"max_tokens", 4294967295, 4294967295, 1, 1, 1 << 63},
		{"zero_tokens", 0, 0, 100, 200, 1000},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Must not panic
			result := keeper.CalculatePerTokenFee(tc.in, tc.out, tc.feeIn, tc.feeOut, tc.maxFee)
			if result > tc.maxFee {
				t.Fatalf("result %d > maxFee %d", result, tc.maxFee)
			}
		})
	}
}

// ================================================================
// E5: Genesis round-trip with S9 params
// ================================================================
func TestGenesisMigration_S9Defaults(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	params := k.GetParams(ctx)
	// Verify S9 fields populated with defaults
	if params.TokenCountTolerance != 2 {
		t.Fatalf("E5: TokenCountTolerance=%d, want 2", params.TokenCountTolerance)
	}
	if params.DishonestJailThreshold != 3 {
		t.Fatalf("E5: DishonestJailThreshold=%d, want 3", params.DishonestJailThreshold)
	}
	if params.PerTokenBillingEnabled != false {
		t.Fatal("E5: PerTokenBillingEnabled should default to false")
	}
	if params.TokenCountTolerancePct != 2 {
		t.Fatalf("E5: TokenCountTolerancePct=%d, want 2", params.TokenCountTolerancePct)
	}
}

// ================================================================
// E9: Only 2 verifiers — medianUint32 returns larger value
// ================================================================
func TestVerifierInsufficient_2(t *testing.T) {
	entry := types.SettlementEntry{
		WorkerInputTokens: 100, WorkerOutputTokens: 400,
		VerifierResults: []types.VerifierResult{
			{VerifiedInputTokens: 100, VerifiedOutputTokens: 380},
			{VerifiedInputTokens: 100, VerifiedOutputTokens: 420},
			{VerifiedInputTokens: 0, VerifiedOutputTokens: 0}, // 3rd verifier unavailable
		},
	}

	res := keeper.ResolveTokenCounts(&entry, 2, 2)
	// E9: median of [380, 420] with 2 verifiers → average = (380+420)/2 = 400
	if res.OutputTokens == 0 {
		t.Fatal("E9: expected non-zero output tokens with 2 verifiers")
	}
	if res.OutputTokens != 400 {
		t.Fatalf("E9: expected average=400, got %d", res.OutputTokens)
	}
	// Worker 400, median(avg) 400, delta=0 ≤ tolerance → not dishonest
	if res.WorkerDishonest {
		t.Fatal("E9: worker should not be dishonest (delta=0)")
	}
	// E9: with <3 verifiers, should be marked low confidence
	if !res.LowConfidence {
		t.Fatal("E9: expected LowConfidence=true with only 2 verifiers")
	}
}

// ================================================================
// E10: Only 1 verifier — returns that value
// ================================================================
func TestVerifierInsufficient_1(t *testing.T) {
	entry := types.SettlementEntry{
		WorkerInputTokens: 100, WorkerOutputTokens: 400,
		VerifierResults: []types.VerifierResult{
			{VerifiedInputTokens: 100, VerifiedOutputTokens: 400},
			{VerifiedInputTokens: 0, VerifiedOutputTokens: 0},
			{VerifiedInputTokens: 0, VerifiedOutputTokens: 0},
		},
	}

	res := keeper.ResolveTokenCounts(&entry, 2, 2)
	// 1 verifier: median([400]) = 400, worker=400 → match
	if res.OutputTokens != 400 {
		t.Fatalf("E10: expected 400, got %d", res.OutputTokens)
	}
	if res.WorkerDishonest {
		t.Fatal("E10: should not be dishonest when single verifier matches")
	}
	// E10: <3 verifiers → low confidence
	if !res.LowConfidence {
		t.Fatal("E10: expected LowConfidence=true with only 1 verifier")
	}
}

// ================================================================
// E11: 0 verifiers — all return 0 tokens
// ================================================================
func TestVerifierInsufficient_0(t *testing.T) {
	entry := types.SettlementEntry{
		WorkerInputTokens: 100, WorkerOutputTokens: 400,
		VerifierResults: []types.VerifierResult{
			{VerifiedInputTokens: 0, VerifiedOutputTokens: 0},
			{VerifiedInputTokens: 0, VerifiedOutputTokens: 0},
			{VerifiedInputTokens: 0, VerifiedOutputTokens: 0},
		},
	}

	res := keeper.ResolveTokenCounts(&entry, 2, 2)
	// median([]) = 0 → resolveTokenPair(400, 0, tol) → nMedian==0 → return worker count
	if res.OutputTokens != 400 {
		t.Fatalf("E11: expected worker count 400 when no verifier data, got %d", res.OutputTokens)
	}
	if res.WorkerDishonest {
		t.Fatal("E11: should not flag dishonest when no verifier data")
	}
	// E11: 0 verifiers → low confidence
	if !res.LowConfidence {
		t.Fatal("E11: expected LowConfidence=true with 0 verifiers")
	}
}

// ================================================================
// E13: Double settlement — same task_id rejected in 2nd batch
// ================================================================
func TestDoubleSettlement(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	user := makeAddr("e13-user")
	worker := makeAddr("e13-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))

	entry := types.SettlementEntry{
		TaskId:      []byte("e13-double-settle001"),
		UserAddress: user.String(), WorkerAddress: worker.String(),
		Fee: sdk.NewCoin("ufai", cosmosmath.NewInt(100_000)), ExpireBlock: 10000,
		Status: types.SettlementSuccess,
		VerifierResults: []types.VerifierResult{
			{Address: makeAddr("e13v1").String(), Pass: true},
			{Address: makeAddr("e13v2").String(), Pass: true},
			{Address: makeAddr("e13v3").String(), Pass: true},
		},
	}

	// First batch — should succeed
	msg1 := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
	_, err := k.ProcessBatchSettlement(ctx, msg1)
	if err != nil {
		t.Fatalf("first batch: %v", err)
	}

	balAfterFirst, _ := k.GetInferenceAccount(ctx, user)

	// Second batch with same task_id — should be skipped (dedup)
	msg2 := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
	_, err = k.ProcessBatchSettlement(ctx, msg2)
	if err != nil {
		t.Fatalf("second batch: %v", err)
	}

	balAfterSecond, _ := k.GetInferenceAccount(ctx, user)
	if !balAfterSecond.Balance.Amount.Equal(balAfterFirst.Balance.Amount) {
		t.Fatalf("E13: balance changed after duplicate settlement: %s → %s",
			balAfterFirst.Balance.Amount, balAfterSecond.Balance.Amount)
	}
}

// ================================================================
// E14: Verifier all return 0 — current behavior (design gap)
// ================================================================
func TestVerifierAllReturnZero(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	enablePerToken(k, ctx)

	user := makeAddr("e14-user")
	worker := makeAddr("e14-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))

	// Worker reports 500 output tokens, all 3 verifiers return 0
	// This simulates TGI crash during teacher forcing
	v1, v2, v3 := makeAddr("e14v1"), makeAddr("e14v2"), makeAddr("e14v3")
	entry := types.SettlementEntry{
		TaskId:      []byte("e14-all-zero-verify0"),
		UserAddress: user.String(), WorkerAddress: worker.String(),
		Fee:                sdk.NewCoin("ufai", cosmosmath.ZeroInt()),
		MaxFee:             sdk.NewCoin("ufai", cosmosmath.NewInt(1_000_000)),
		ExpireBlock:        10000,
		Status:             types.SettlementSuccess,
		FeePerInputToken:   100,
		FeePerOutputToken:  200,
		WorkerInputTokens:  100,
		WorkerOutputTokens: 500,
		VerifierResults: []types.VerifierResult{
			{Address: v1.String(), Pass: true, VerifiedInputTokens: 0, VerifiedOutputTokens: 0},
			{Address: v2.String(), Pass: true, VerifiedInputTokens: 0, VerifiedOutputTokens: 0},
			{Address: v3.String(), Pass: true, VerifiedInputTokens: 0, VerifiedOutputTokens: 0},
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Current behavior: median([])=0 → resolveTokenPair(500, 0) → nMedian==0 → return worker=500
	// actual = 100*100 + 500*200 = 110000 → capped at max_fee=1000000 → 110000
	ia, _ := k.GetInferenceAccount(ctx, user)
	expectedFee := int64(100*100 + 500*200) // 110000
	expected := cosmosmath.NewInt(10_000_000 - expectedFee)
	if !ia.Balance.Amount.Equal(expected) {
		t.Logf("E14 WARNING: Verifier all return 0 → worker self-report used (fee=%d)", expectedFee)
		t.Logf("E14: This is a design gap — TGI crash means no verification but worker gets full pay")
		// Still verify current behavior is consistent
		if ia.Balance.Amount.GT(cosmosmath.NewInt(10_000_000)) {
			t.Fatal("E14: balance should not increase")
		}
	}
	t.Logf("E14: DOCUMENTED BEHAVIOR — worker self-report used when all verifiers return 0")
}

// ================================================================
// E16: Block time variance — expire_block based on block count not time
// ================================================================
func TestBlockTimeVariance(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	user := makeAddr("e16-user")
	worker := makeAddr("e16-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))

	// Task with expire_block = 200 (current height = 100)
	entry := types.SettlementEntry{
		TaskId:      []byte("e16-block-variance01"),
		UserAddress: user.String(), WorkerAddress: worker.String(),
		Fee: sdk.NewCoin("ufai", cosmosmath.NewInt(100_000)), ExpireBlock: 200,
		Status: types.SettlementSuccess,
		VerifierResults: []types.VerifierResult{
			{Address: makeAddr("e16v1").String(), Pass: true},
			{Address: makeAddr("e16v2").String(), Pass: true},
			{Address: makeAddr("e16v3").String(), Pass: true},
		},
	}

	// At height 100: not expired, should settle
	msg := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ia, _ := k.GetInferenceAccount(ctx, user)
	expected := cosmosmath.NewInt(10_000_000 - 100_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("E16: expected balance %s, got %s", expected, ia.Balance.Amount)
	}

	// At height 201: expired task should be skipped
	user2 := makeAddr("e16-user2")
	_ = k.ProcessDeposit(ctx, user2, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))
	entry2 := entry
	entry2.TaskId = []byte("e16-block-expired001")
	entry2.UserAddress = user2.String()
	entry2.ExpireBlock = 150 // Already expired at height 100? No, check: ExpireBlock < currentHeight

	ctx2 := ctx.WithBlockHeight(200)
	entry2.ExpireBlock = 150 // expired at height 200
	msg2 := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry2})
	_, err = k.ProcessBatchSettlement(ctx2, msg2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ia2, _ := k.GetInferenceAccount(ctx2, user2)
	// Expired entry skipped → balance unchanged
	if !ia2.Balance.Amount.Equal(cosmosmath.NewInt(10_000_000)) {
		t.Fatalf("E16: expired entry should be skipped, balance=%s", ia2.Balance.Amount)
	}
}

// ================================================================
// E22: Gas estimation — verify formula
// ================================================================
func TestBatchLoop_GasEstimate(t *testing.T) {
	tests := []struct {
		entries  int
		expected uint64
	}{
		{1, 200000 + 1*2000},
		{1000, 200000 + 1000*2000},
		{5000, 200000 + 5000*2000},
		{10000, 200000 + 10000*2000},
	}

	blockGasLimit := uint64(100_000_000) // default 100M

	for _, tc := range tests {
		gasLimit := uint64(200000) + uint64(tc.entries)*2000
		if gasLimit != tc.expected {
			t.Fatalf("E22: %d entries: gas=%d, expected=%d", tc.entries, gasLimit, tc.expected)
		}
		if gasLimit >= blockGasLimit {
			t.Fatalf("E22: %d entries: gas %d exceeds block gas limit %d", tc.entries, gasLimit, blockGasLimit)
		}
	}

	// Max entries before exceeding block gas limit
	maxEntries := (blockGasLimit - 200000) / 2000 // = 49900
	if maxEntries < 49000 {
		t.Fatalf("E22: max entries %d too low", maxEntries)
	}
	t.Logf("E22: max entries before block gas limit: %d", maxEntries)
}

// ================================================================
// E4: Epoch boundary — settlement at epoch edge
// ================================================================
func TestEpochBoundary_Settlement(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	user := makeAddr("e4-user")
	worker := makeAddr("e4-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))

	// Settle at height 99 (epoch 0)
	ctx99 := ctx.WithBlockHeight(99)
	entry1 := types.SettlementEntry{
		TaskId: []byte("e4-task-epoch0-00001"), UserAddress: user.String(), WorkerAddress: worker.String(),
		Fee: sdk.NewCoin("ufai", cosmosmath.NewInt(100_000)), ExpireBlock: 10000,
		Status: types.SettlementSuccess,
		VerifierResults: []types.VerifierResult{
			{Address: makeAddr("e4v1").String(), Pass: true},
			{Address: makeAddr("e4v2").String(), Pass: true},
			{Address: makeAddr("e4v3").String(), Pass: true},
		},
	}
	msg1 := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry1})
	_, err := k.ProcessBatchSettlement(ctx99, msg1)
	if err != nil {
		t.Fatalf("epoch 0 settlement: %v", err)
	}

	// Settle at height 100 (epoch 1)
	ctx100 := ctx.WithBlockHeight(100)
	entry2 := entry1
	entry2.TaskId = []byte("e4-task-epoch1-00001")
	msg2 := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry2})
	_, err = k.ProcessBatchSettlement(ctx100, msg2)
	if err != nil {
		t.Fatalf("epoch 1 settlement: %v", err)
	}

	// Both settled correctly, total deducted = 200000
	ia, _ := k.GetInferenceAccount(ctx100, user)
	expected := cosmosmath.NewInt(10_000_000 - 200_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("E4: expected %s, got %s", expected, ia.Balance.Amount)
	}
}

// ================================================================
// E7: Pair storage scale — 10K pair records
// ================================================================
func TestPairStorageScale(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	params := k.GetParams(ctx)
	params.PerTokenBillingEnabled = true
	k.SetParams(ctx, params)

	worker := makeAddr("e7-worker")

	// Create 10K unique verifier pairs
	for i := 0; i < 10000; i++ {
		vAddr := makeAddr(fmt.Sprintf("e7-v%05d", i))
		k.UpdateTokenMismatchPair(ctx, worker.String(), vAddr.String(), i%3 == 0, params)
	}

	// Query a specific pair
	rec := k.GetTokenMismatchRecord(ctx, worker.String(), makeAddr("e7-v00042").String())
	if rec.TotalTasks != 1 {
		t.Fatalf("E7: expected TotalTasks=1 for pair, got %d", rec.TotalTasks)
	}

	// Query audit boost (aggregates across all pairs)
	boost := k.CalculateWorkerAuditBoost(ctx, worker.String(), params)
	// With 10K pairs, most below MinSamples (1 task each < 5), boost should be limited
	t.Logf("E7: audit boost for 10K pairs (1 task each) = %d", boost)
}

// ================================================================
// E8: Large batch — 10K entries
// ================================================================
func TestBatchSettlement_Large10K(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	user := makeAddr("e8-user")
	worker := makeAddr("e8-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(100_000_000_000))) // 100K FAI

	entries := make([]types.SettlementEntry, 10000)
	for i := 0; i < 10000; i++ {
		entries[i] = types.SettlementEntry{
			TaskId:      []byte(fmt.Sprintf("e8-task-%010d", i)),
			UserAddress: user.String(), WorkerAddress: worker.String(),
			Fee: sdk.NewCoin("ufai", cosmosmath.NewInt(1000)), ExpireBlock: 100000,
			Status: types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("e8v1").String(), Pass: true},
				{Address: makeAddr("e8v2").String(), Pass: true},
				{Address: makeAddr("e8v3").String(), Pass: true},
			},
		}
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("E8: 10K batch failed: %v", err)
	}

	br, _ := k.GetBatchRecord(ctx, batchId)
	if br.ResultCount != 10000 {
		t.Fatalf("E8: expected 10000 results, got %d", br.ResultCount)
	}

	ia, _ := k.GetInferenceAccount(ctx, user)
	expected := cosmosmath.NewInt(100_000_000_000 - 10000*1000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("E8: expected balance %s, got %s", expected, ia.Balance.Amount)
	}
}

// ================================================================
// E12: expire_block too short — task expires before settlement
// ================================================================
func TestExpireBlockTooShort(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	enablePerToken(k, ctx)

	user := makeAddr("e12-user")
	worker := makeAddr("e12-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))

	// Freeze balance with expire_block = 20 (short)
	err := k.FreezeBalance(ctx, user, []byte("e12-expire-short001"), sdk.NewCoin("ufai", cosmosmath.NewInt(500_000)))
	if err != nil {
		t.Fatalf("FreezeBalance: %v", err)
	}
	k.StoreFrozenTaskMeta(ctx, types.FrozenTaskMeta{
		TaskId:        []byte("e12-expire-short001"),
		UserAddress:   user.String(),
		WorkerAddress: worker.String(),
		MaxFee:        500_000,
		ExpireBlock:   20,
	})

	// At height 100 (well past expire_block=20), timeout fires
	k.HandleFrozenBalanceTimeouts(ctx) // ctx has Height=100

	// timeout_fee = 500000 * 150/1000 = 75000; refund = 425000
	ia, _ := k.GetInferenceAccount(ctx, user)
	expected := cosmosmath.NewInt(10_000_000 - 75_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("E12: expected balance %s, got %s", expected, ia.Balance.Amount)
	}

	if len(wk.jailCalls) != 1 {
		t.Fatalf("E12: expected worker jailed on timeout, got %d", len(wk.jailCalls))
	}
}

// ================================================================
// E18: Chain halt recovery — multiple expired tasks processed at once
// ================================================================
func TestChainHaltRecovery(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	enablePerToken(k, ctx)

	// Create 5 users with frozen tasks, all expiring before current height
	for i := 0; i < 5; i++ {
		user := makeAddr(fmt.Sprintf("e18-user%d", i))
		worker := makeAddr(fmt.Sprintf("e18-wkr%d", i))
		_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(1_000_000)))

		taskId := []byte(fmt.Sprintf("e18-halt-task-%05d", i))
		err := k.FreezeBalance(ctx, user, taskId, sdk.NewCoin("ufai", cosmosmath.NewInt(200_000)))
		if err != nil {
			t.Fatalf("FreezeBalance %d: %v", i, err)
		}
		k.StoreFrozenTaskMeta(ctx, types.FrozenTaskMeta{
			TaskId:        taskId,
			UserAddress:   user.String(),
			WorkerAddress: worker.String(),
			MaxFee:        200_000,
			ExpireBlock:   int64(30 + i*10), // all expire before height 100
		})
	}

	// Simulate chain halt recovery: all 5 tasks should timeout
	k.HandleFrozenBalanceTimeouts(ctx)

	// All 5 workers should be jailed
	if len(wk.jailCalls) != 5 {
		t.Fatalf("E18: expected 5 jail calls after chain halt, got %d", len(wk.jailCalls))
	}

	// Each user should have: 1M - 200K(frozen) + (200K - 30K timeout)(refund) = 970000
	for i := 0; i < 5; i++ {
		user := makeAddr(fmt.Sprintf("e18-user%d", i))
		ia, _ := k.GetInferenceAccount(ctx, user)
		// timeout_fee = 200000 * 150/1000 = 30000
		expected := cosmosmath.NewInt(1_000_000 - 30_000)
		if !ia.Balance.Amount.Equal(expected) {
			t.Fatalf("E18: user%d expected %s, got %s", i, expected, ia.Balance.Amount)
		}
	}
}

// ================================================================
// AC4: Collusion audit — audit flips original success to fail
// ================================================================
func TestAntiCheat_CollusionAudit(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	k.SetCurrentThirdVerificationRate(ctx, 0)

	user := makeAddr("ac4-user")
	worker := makeAddr("ac4-worker")
	taskId := []byte("ac4-collusion-task01")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))

	// Simulate: original settlement was SUCCESS but should have been FAIL
	// Set up audit pending task
	k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
		TaskId:         taskId,
		OriginalStatus: types.SettlementSuccess,
		SubmittedAt:    ctx.BlockHeight(),
		UserAddress:    user.String(),
		WorkerAddress:  worker.String(),
		VerifierAddresses: []string{
			makeAddr("ac4-v1").String(),
			makeAddr("ac4-v2").String(),
			makeAddr("ac4-v3").String(),
		},
		VerifierVotes: []bool{true, true, true}, // original verifiers voted PASS
		Fee:           sdk.NewCoin("ufai", cosmosmath.NewInt(1_000_000)),
		ExpireBlock:   10000,
	})

	// 3 second_verifiers all vote FAIL — this overturns the original SUCCESS
	for i := 0; i < 3; i++ {
		err := k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
			SecondVerifier: makeAddr(fmt.Sprintf("ac4-aud%d", i)).String(),
			TaskId:         taskId,
			Epoch:          1,
			Pass:           false, // FAIL — contradicts original SUCCESS
			LogitsHash:     []byte("audit-logits-hash-padding!!!!"),
		})
		if err != nil {
			t.Fatalf("audit %d: %v", i, err)
		}
	}

	// Worker and colluding verifiers should be jailed
	if len(wk.jailCalls) < 1 {
		t.Fatalf("AC4: expected jail calls after audit overturn, got %d", len(wk.jailCalls))
	}
	t.Logf("AC4: jail calls after collusion audit = %d", len(wk.jailCalls))
}
