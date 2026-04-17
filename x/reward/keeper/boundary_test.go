package keeper_test

// Boundary and edge-case tests for the reward module — supplementary to existing tests.

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/reward/types"
)

// ============================================================
// B1. Distribute by consensus signing (empty epoch path)
// ============================================================

func TestDistributeRewards_ByConsensusSigning(t *testing.T) {
	k, ctx, bk := setupRewardKeeper(t)

	v1 := sdk.AccAddress([]byte("validator1__________"))
	v2 := sdk.AccAddress([]byte("validator2__________"))

	signers := []types.ConsensusSignerInfo{
		{ValidatorAddress: v1.String(), BlocksSigned: 80},
		{ValidatorAddress: v2.String(), BlocksSigned: 20},
	}

	err := k.DistributeRewards(ctx, nil, nil, signers, nil)
	if err != nil {
		t.Fatalf("distribute by consensus should succeed: %v", err)
	}

	sent1 := bk.sent[v1.String()]
	sent2 := bk.sent[v2.String()]

	if sent1.IsZero() || sent2.IsZero() {
		t.Fatal("both validators should receive rewards")
	}

	// v1 signed 4x more blocks, should get roughly 4x more
	if sent1.LTE(sent2) {
		t.Fatalf("v1 (80 blocks) should get more than v2 (20 blocks): %s vs %s", sent1, sent2)
	}
}

// ============================================================
// B2. Distribute with no contributors at all → no reward distributed
// ============================================================

func TestDistributeRewards_NobodyContributed(t *testing.T) {
	k, ctx, bk := setupRewardKeeper(t)

	err := k.DistributeRewards(ctx, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("should not error: %v", err)
	}
	if len(bk.sent) != 0 {
		t.Fatal("no contributors → no rewards should be sent")
	}
}

// ============================================================
// B3. Dust loss: 3 equal workers get fair distribution
// ============================================================

func TestDistributeRewards_ThreeEqualWorkers_NoDustLoss(t *testing.T) {
	k, ctx, bk := setupRewardKeeper(t)

	addrs := make([]sdk.AccAddress, 3)
	contributions := make([]types.WorkerContribution, 3)
	for i := 0; i < 3; i++ {
		name := []byte("worker______________")
		name[6] = byte('1' + i)
		addrs[i] = sdk.AccAddress(name)
		contributions[i] = types.WorkerContribution{
			WorkerAddress: addrs[i].String(),
			FeeAmount:     math.NewInt(1000),
			TaskCount:     10,
		}
	}

	err := k.DistributeRewards(ctx, contributions, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Total distributed should equal epoch reward (last worker gets remainder)
	totalSent := math.ZeroInt()
	for _, addr := range addrs {
		totalSent = totalSent.Add(bk.sent[addr.String()])
	}

	epochReward := k.CalculateEpochReward(ctx, 100)
	inferenceReward := types.DefaultInferenceWeight.MulInt(epochReward).TruncateInt()
	if !totalSent.Equal(inferenceReward) {
		t.Fatalf("total distributed %s should equal inference reward %s (no dust loss)", totalSent, inferenceReward)
	}
}

// ============================================================
// B4. Block reward at multiple halving boundaries
// ============================================================

func TestBlockReward_MultipleHalvings(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	base := types.DefaultBaseBlockReward
	hp := types.DefaultHalvingPeriod

	// 2nd halving
	r2 := k.CalculateBlockReward(ctx, 2*hp)
	expected2 := base.QuoRaw(4)
	if !r2.Equal(expected2) {
		t.Fatalf("2nd halving: expected %s, got %s", expected2, r2)
	}

	// 3rd halving
	r3 := k.CalculateBlockReward(ctx, 3*hp)
	expected3 := base.QuoRaw(8)
	if !r3.Equal(expected3) {
		t.Fatalf("3rd halving: expected %s, got %s", expected3, r3)
	}
}

// ============================================================
// B5. Block reward at 64+ halvings → zero
// ============================================================

func TestBlockReward_64Halvings_Zero(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	hp := types.DefaultHalvingPeriod
	r := k.CalculateBlockReward(ctx, 64*hp)
	if !r.IsZero() {
		t.Fatalf("64 halvings should produce zero reward, got %s", r)
	}
}

// ============================================================
// B6. Params validation boundaries
// ============================================================

func TestParams_Validation(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*types.Params)
		wantErr bool
	}{
		{"valid_defaults", func(p *types.Params) {}, false},
		{"negative_base_reward", func(p *types.Params) { p.BaseBlockReward = math.NewInt(-1) }, true},
		{"zero_halving_period", func(p *types.Params) { p.HalvingPeriod = 0 }, true},
		{"negative_halving_period", func(p *types.Params) { p.HalvingPeriod = -1 }, true},
		{"fee_weight_negative", func(p *types.Params) { p.FeeWeight = math.LegacyNewDec(-1) }, true},
		{"fee_weight_over_1", func(p *types.Params) { p.FeeWeight = math.LegacyNewDecWithPrec(101, 2) }, true},
		{"count_weight_negative", func(p *types.Params) { p.CountWeight = math.LegacyNewDec(-1) }, true},
		{"epoch_blocks_zero", func(p *types.Params) { p.EpochBlocks = 0 }, true},
		{"inference_weight_over_1", func(p *types.Params) { p.InferenceWeight = math.LegacyNewDecWithPrec(101, 2) }, true},
		{"verification_weight_negative", func(p *types.Params) { p.VerificationWeight = math.LegacyNewDec(-1) }, true},
		{"zero_base_reward_ok", func(p *types.Params) { p.BaseBlockReward = math.ZeroInt() }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := types.DefaultParams()
			tt.modify(&p)
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
// B7. Genesis round-trip with reward records
// ============================================================

func TestGenesis_RoundTrip(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	gs := types.GenesisState{
		Params: types.DefaultParams(),
		RewardRecords: []types.RewardRecord{
			{Epoch: 1, WorkerAddress: "cosmos1worker1", Amount: sdk.NewCoin(types.BondDenom, math.NewInt(5000))},
			{Epoch: 1, WorkerAddress: "cosmos1worker2", Amount: sdk.NewCoin(types.BondDenom, math.NewInt(3000))},
			{Epoch: 2, WorkerAddress: "cosmos1worker1", Amount: sdk.NewCoin(types.BondDenom, math.NewInt(7000))},
		},
	}

	k.InitGenesis(ctx, gs)
	exported := k.ExportGenesis(ctx)

	if len(exported.RewardRecords) != 3 {
		t.Fatalf("expected 3 records, got %d", len(exported.RewardRecords))
	}
}

// ============================================================
// B8. Genesis with invalid params → panics
// ============================================================

func TestGenesis_InvalidParams_Panics(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for invalid params in genesis")
		}
	}()

	gs := types.GenesisState{
		Params: types.Params{
			BaseBlockReward:    math.NewInt(-1), // invalid
			HalvingPeriod:      1,
			EpochBlocks:        1,
			FeeWeight:          math.LegacyZeroDec(),
			CountWeight:        math.LegacyZeroDec(),
			InferenceWeight:    math.LegacyZeroDec(),
			VerificationWeight: math.LegacyZeroDec(),
		},
	}
	k.InitGenesis(ctx, gs)
}

// ============================================================
// B9. Distribute by stake: multiple workers proportional
// ============================================================

func TestDistributeRewards_ByStake_Proportional(t *testing.T) {
	k, ctx, bk := setupRewardKeeper(t)

	w1 := sdk.AccAddress([]byte("worker1_____________"))
	w2 := sdk.AccAddress([]byte("worker2_____________"))

	onlineWorkers := []types.OnlineWorkerStake{
		{WorkerAddress: w1.String(), Stake: math.NewInt(75000)},
		{WorkerAddress: w2.String(), Stake: math.NewInt(25000)},
	}

	err := k.DistributeRewards(ctx, nil, nil, nil, onlineWorkers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sent1 := bk.sent[w1.String()]
	sent2 := bk.sent[w2.String()]

	// w1 has 3x stake of w2
	if sent1.LTE(sent2) {
		t.Fatalf("w1 (75k) should get more than w2 (25k): %s vs %s", sent1, sent2)
	}

	// Total should equal epoch reward
	total := sent1.Add(sent2)
	epochReward := k.CalculateEpochReward(ctx, 100)
	if !total.Equal(epochReward) {
		t.Fatalf("total %s should equal epoch reward %s", total, epochReward)
	}
}

// ============================================================
// B10. Distribute by stake: all zero stake → no distribution
// ============================================================

func TestDistributeRewards_ByStake_AllZero(t *testing.T) {
	k, ctx, bk := setupRewardKeeper(t)

	onlineWorkers := []types.OnlineWorkerStake{
		{WorkerAddress: "cosmos1w1", Stake: math.ZeroInt()},
		{WorkerAddress: "cosmos1w2", Stake: math.ZeroInt()},
	}

	err := k.DistributeRewards(ctx, nil, nil, nil, onlineWorkers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bk.sent) != 0 {
		t.Fatal("all zero stake → no rewards should be sent")
	}
}

// ============================================================
// B11. Distribute consensus: all zero blocks signed → no distribution
// ============================================================

func TestDistributeRewards_Consensus_AllZeroBlocks(t *testing.T) {
	k, ctx, bk := setupRewardKeeper(t)

	signers := []types.ConsensusSignerInfo{
		{ValidatorAddress: "cosmos1v1", BlocksSigned: 0},
		{ValidatorAddress: "cosmos1v2", BlocksSigned: 0},
	}

	err := k.DistributeRewards(ctx, nil, nil, signers, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bk.sent) != 0 {
		t.Fatal("all zero blocks signed → no rewards should be sent")
	}
}

// ============================================================
// B12. Distribute with single contributor → gets full inference reward
// ============================================================

func TestDistributeRewards_SingleContributor(t *testing.T) {
	k, ctx, bk := setupRewardKeeper(t)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	contributions := []types.WorkerContribution{
		{WorkerAddress: addr.String(), FeeAmount: math.NewInt(5000), TaskCount: 50},
	}

	err := k.DistributeRewards(ctx, contributions, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sent := bk.sent[addr.String()]
	epochReward := k.CalculateEpochReward(ctx, 100)
	inferenceReward := types.DefaultInferenceWeight.MulInt(epochReward).TruncateInt()
	if !sent.Equal(inferenceReward) {
		t.Fatalf("single contributor should get full inference reward: expected %s, got %s", inferenceReward, sent)
	}
}

// ============================================================
// B13. gRPC QueryParams
// ============================================================

func TestQueryServer_Params(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	resp, err := k.QueryParams(ctx, &types.QueryParamsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Params.EpochBlocks != types.DefaultEpochBlocks {
		t.Fatalf("expected epoch_blocks=%d, got %d", types.DefaultEpochBlocks, resp.Params.EpochBlocks)
	}
}

// ============================================================
// B14. gRPC QueryRewardHistory: empty
// ============================================================

func TestQueryServer_RewardHistory_Empty(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	resp, err := k.QueryRewardHistory(ctx, &types.QueryRewardHistoryRequest{WorkerAddress: "cosmos1nobody"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Records) != 0 {
		t.Fatalf("expected 0 records, got %d", len(resp.Records))
	}
}

// ============================================================
// B15. MsgUpdateParams ValidateBasic
// ============================================================

func TestMsgUpdateParams_ValidateBasic(t *testing.T) {
	// Invalid authority
	msg := &types.MsgUpdateParams{
		Authority: "invalid",
		Params:    types.DefaultParams(),
	}
	if err := msg.ValidateBasic(); err == nil {
		t.Fatal("expected error for invalid authority")
	}

	// Invalid params (HalvingPeriod=0)
	validAddr := sdk.AccAddress([]byte("authority___________")).String()
	msg2 := &types.MsgUpdateParams{
		Authority: validAddr,
		Params: types.Params{
			BaseBlockReward:    math.NewInt(4000),
			HalvingPeriod:      0, // invalid
			EpochBlocks:        1,
			FeeWeight:          math.LegacyZeroDec(),
			CountWeight:        math.LegacyZeroDec(),
			InferenceWeight:    math.LegacyZeroDec(),
			VerificationWeight: math.LegacyZeroDec(),
		},
	}
	if err := msg2.ValidateBasic(); err == nil {
		t.Fatal("expected error for invalid params")
	}
}

// ============================================================
// B16. EpochReward with very small epochBlocks
// ============================================================

func TestEpochReward_EpochBlocksTwo(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	params := types.DefaultParams()
	params.EpochBlocks = 2
	_ = k.SetParams(ctx, params)

	reward := k.CalculateEpochReward(ctx, 100)
	block99 := k.CalculateBlockReward(ctx, 99)
	block100 := k.CalculateBlockReward(ctx, 100)
	expected := block99.Add(block100)
	if !reward.Equal(expected) {
		t.Fatalf("epoch=2 blocks: expected %s, got %s", expected, reward)
	}
}

// ============================================================
// B17. Inference + verification split
// ============================================================

func TestDistributeRewards_InferenceAndVerification(t *testing.T) {
	k, ctx, bk := setupRewardKeeper(t)

	w1 := sdk.AccAddress([]byte("worker1_____________"))
	v1 := sdk.AccAddress([]byte("verifier1___________"))

	contributions := []types.WorkerContribution{
		{WorkerAddress: w1.String(), FeeAmount: math.NewInt(5000), TaskCount: 50},
	}
	verifiers := []types.VerificationContribution{
		{WorkerAddress: v1.String(), VerificationCount: 100, AuditCount: 10},
	}

	err := k.DistributeRewards(ctx, contributions, verifiers, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sentW := bk.sent[w1.String()]
	sentV := bk.sent[v1.String()]

	if sentW.IsZero() || sentV.IsZero() {
		t.Fatal("both worker and verifier should receive rewards")
	}

	// Worker gets 85% portion, verifier gets 12%, multi-verification fund gets 3%.
	sentFund := bk.sent["module:settlement"]
	if sentFund.IsNil() {
		sentFund = math.ZeroInt()
	}
	total := sentW.Add(sentV).Add(sentFund)
	epochReward := k.CalculateEpochReward(ctx, 100)
	if !total.Equal(epochReward) {
		t.Fatalf("total %s (worker + verifier + fund) should equal epoch reward %s", total, epochReward)
	}

	// Worker should get more (85% vs 12%)
	if sentW.LT(sentV) {
		t.Fatalf("worker (85%%) should get more than verifier (12%%): %s vs %s", sentW, sentV)
	}
}
