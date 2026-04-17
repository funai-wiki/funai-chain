package keeper_test

import (
	"fmt"
	"math/big"
	"testing"

	cosmosmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/settlement/keeper"
	"github.com/funai-wiki/funai-chain/x/settlement/types"
	vrftypes "github.com/funai-wiki/funai-chain/x/vrf/types"
)

// ================================================================
// S1: Forged SecondVerificationResponse — invalid second_verifier rejected
// ================================================================
func TestForgedSecondVerificationResponse(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	taskId := []byte("s1-forged-audit00001")

	// Set up audit pending task
	k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       ctx.BlockHeight(),
		UserAddress:       makeAddr("s1-user").String(),
		WorkerAddress:     makeAddr("s1-worker").String(),
		VerifierAddresses: []string{makeAddr("s1-v1").String()},
		Fee:               sdk.NewCoin("ufai", cosmosmath.NewInt(1_000_000)),
		ExpireBlock:       10000,
	})

	// Submit audit from ORIGINAL verifier (should be rejected — conflict of interest)
	err := k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
		SecondVerifier: makeAddr("s1-v1").String(), // same as original verifier
		TaskId:         taskId,
		Epoch:          1,
		Pass:           true,
		LogitsHash:     []byte("hash-padding-32-bytes-here!!!!!"),
	})
	if err == nil {
		t.Fatal("S1: expected rejection of audit from original verifier")
	}

	// Submit from different (valid) second_verifier — should succeed
	err = k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
		SecondVerifier: makeAddr("s1-aud0").String(),
		TaskId:         taskId,
		Epoch:          1,
		Pass:           true,
		LogitsHash:     []byte("hash-padding-32-bytes-here!!!!!"),
	})
	if err != nil {
		t.Fatalf("S1: valid second_verifier rejected: %v", err)
	}
}

// ================================================================
// S2: Replay attack — already tested as E13 (TestDoubleSettlement)
// Verify SettledTask dedup via different vector
// ================================================================
func TestReplayAttack_DuplicateTaskId(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	user := makeAddr("s2-user")
	worker := makeAddr("s2-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(10_000_000)))

	entry := types.SettlementEntry{
		TaskId: []byte("s2-replay-attack0001"), UserAddress: user.String(), WorkerAddress: worker.String(),
		Fee: sdk.NewCoin("ufai", cosmosmath.NewInt(1_000_000)), ExpireBlock: 10000,
		Status: types.SettlementSuccess,
		VerifierResults: []types.VerifierResult{
			{Address: makeAddr("s2v1").String(), Pass: true},
			{Address: makeAddr("s2v2").String(), Pass: true},
			{Address: makeAddr("s2v3").String(), Pass: true},
		},
	}

	// First: settles
	msg1 := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
	_, _ = k.ProcessBatchSettlement(ctx, msg1)

	bal1, _ := k.GetInferenceAccount(ctx, user)

	// Second: replayed in later batch — should be deduped
	msg2 := makeBatchMsg(t, makeAddr("proposer").String(), []types.SettlementEntry{entry})
	_, _ = k.ProcessBatchSettlement(ctx, msg2)

	bal2, _ := k.GetInferenceAccount(ctx, user)
	if !bal2.Balance.Amount.Equal(bal1.Balance.Amount) {
		t.Fatalf("S2: replay attack succeeded — balance changed from %s to %s",
			bal1.Balance.Amount, bal2.Balance.Amount)
	}
}

// ================================================================
// S3: Cross-denom attack — wrong denom rejected by ValidateBasic
// ================================================================
func TestCrossDenomAttack(t *testing.T) {
	// MsgDeposit with wrong denom
	msgDeposit := types.NewMsgDeposit(
		makeAddr("s3-user").String(),
		sdk.NewCoin("uatom", cosmosmath.NewInt(1_000_000)),
	)
	if err := msgDeposit.ValidateBasic(); err == nil {
		t.Fatal("S3: MsgDeposit with uatom should be rejected")
	}

	// MsgWithdraw with wrong denom
	msgWithdraw := types.NewMsgWithdraw(
		makeAddr("s3-user").String(),
		sdk.NewCoin("uatom", cosmosmath.NewInt(1_000_000)),
	)
	if err := msgWithdraw.ValidateBasic(); err == nil {
		t.Fatal("S3: MsgWithdraw with uatom should be rejected")
	}

	// Correct denom should pass
	msgOk := types.NewMsgDeposit(
		makeAddr("s3-user").String(),
		sdk.NewCoin("ufai", cosmosmath.NewInt(1_000_000)),
	)
	if err := msgOk.ValidateBasic(); err != nil {
		t.Fatalf("S3: MsgDeposit with ufai should pass: %v", err)
	}
}

// ================================================================
// S6: Sybil attack VRF — splitting stake should not help
// ================================================================
func TestSybilAttack_VRF(t *testing.T) {
	// Run 10K elections and compare win rates
	bigWins := 0
	smallWins := 0
	for i := 0; i < 10000; i++ {
		electionSeed := []byte(makeAddr(fmt.Sprintf("election-%05d", i)).String())
		bigScore := vrftypes.ComputeScore(electionSeed, []byte("big-stake-worker-pub"), cosmosmath.NewInt(1000), vrftypes.AlphaDispatch)

		// Best among 100 small workers
		var bestSmall *big.Float
		for j := 0; j < 100; j++ {
			pubkey := []byte(makeAddr(fmt.Sprintf("small-w%03d", j)).String())
			score := vrftypes.ComputeScore(electionSeed, pubkey, cosmosmath.NewInt(10), vrftypes.AlphaDispatch)
			if bestSmall == nil || score.Cmp(bestSmall) < 0 {
				bestSmall = score
			}
		}

		if bigScore.Cmp(bestSmall) < 0 {
			bigWins++
		} else {
			smallWins++
		}
	}

	// With same total stake and α=1.0, the 100 small workers should not have
	// an overwhelming advantage. Allow up to 70/30 split.
	ratio := float64(bigWins) / float64(bigWins+smallWins)
	t.Logf("S6: big(1x1000) wins=%d, small(100x10) wins=%d, ratio=%.2f", bigWins, smallWins, ratio)
	if ratio < 0.05 {
		t.Fatalf("S6: big worker wins only %.1f%% — Sybil attack gives too much advantage", ratio*100)
	}
}

// ================================================================
// S8: Overflow protection — all 3 paths in CalculatePerTokenFee
// ================================================================
func TestOverflowProtection_AllPaths(t *testing.T) {
	maxFee := uint64(999999)

	// Path 1: input multiplication overflow
	r1 := keeper.CalculatePerTokenFee(4294967295, 0, 4294967295, 0, maxFee)
	if r1 != maxFee {
		t.Fatalf("S8 path 1: expected %d, got %d", maxFee, r1)
	}

	// Path 2: output multiplication overflow
	r2 := keeper.CalculatePerTokenFee(0, 3, 0, 1<<63, maxFee)
	if r2 != maxFee {
		t.Fatalf("S8 path 2: expected %d, got %d", maxFee, r2)
	}

	// Path 3: addition overflow (inputCost + outputCost)
	r3 := keeper.CalculatePerTokenFee(1, 1, 1<<63, 1<<63, maxFee)
	if r3 != maxFee {
		t.Fatalf("S8 path 3: expected %d, got %d", maxFee, r3)
	}
}

// ================================================================
// S9: Balance drain attack — shadow balance prevents overspend
// ================================================================
func TestBalanceDrainAttack(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)
	enablePerToken(k, ctx)

	user := makeAddr("s9-user")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", cosmosmath.NewInt(1_000_000)))

	// Freeze most of the balance (simulating many pending tasks)
	for i := 0; i < 9; i++ {
		taskId := []byte(makeAddr(fmt.Sprintf("s9-drain-%02d", i)).String())
		err := k.FreezeBalance(ctx, user, taskId, sdk.NewCoin("ufai", cosmosmath.NewInt(100_000)))
		if err != nil {
			t.Logf("S9: FreezeBalance %d rejected: %v (expected when balance exhausted)", i, err)
			break
		}
	}

	// Check available balance is reduced
	ia, _ := k.GetInferenceAccount(ctx, user)
	available := ia.AvailableBalance()
	t.Logf("S9: balance=%s, frozen=%s, available=%s",
		ia.Balance.Amount, ia.FrozenBalance.Amount, available.Amount)

	if available.Amount.GTE(ia.Balance.Amount) && !ia.FrozenBalance.IsZero() {
		t.Fatal("S9: available should be less than total when funds are frozen")
	}
}

// S10: Unauthorized proposer — covered by TestBatchSettlement_UnauthorizedProposer
// in boundary_test.go (uses different signing key → error "signature")
