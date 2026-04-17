package keeper_test

import (
	"context"
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	"cosmossdk.io/store"
	storemetrics "cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"

	"github.com/funai-wiki/funai-chain/x/reward/keeper"
	"github.com/funai-wiki/funai-chain/x/reward/types"
)

type mockBankKeeper struct {
	minted map[string]math.Int
	sent   map[string]math.Int
}

func newMockBankKeeper() *mockBankKeeper {
	return &mockBankKeeper{
		minted: make(map[string]math.Int),
		sent:   make(map[string]math.Int),
	}
}

func (m *mockBankKeeper) MintCoins(_ context.Context, moduleName string, amounts sdk.Coins) error {
	for _, c := range amounts {
		prev, ok := m.minted[moduleName]
		if !ok {
			prev = math.ZeroInt()
		}
		m.minted[moduleName] = prev.Add(c.Amount)
	}
	return nil
}

func (m *mockBankKeeper) SendCoinsFromModuleToAccount(_ context.Context, _ string, recipientAddr sdk.AccAddress, amt sdk.Coins) error {
	for _, c := range amt {
		prev, ok := m.sent[recipientAddr.String()]
		if !ok {
			prev = math.ZeroInt()
		}
		m.sent[recipientAddr.String()] = prev.Add(c.Amount)
	}
	return nil
}

func (m *mockBankKeeper) SendCoinsFromModuleToModule(_ context.Context, _ string, recipientModule string, amt sdk.Coins) error {
	key := "module:" + recipientModule
	for _, c := range amt {
		prev, ok := m.sent[key]
		if !ok {
			prev = math.ZeroInt()
		}
		m.sent[key] = prev.Add(c.Amount)
	}
	return nil
}

func (m *mockBankKeeper) GetBalance(_ context.Context, _ sdk.AccAddress, _ string) sdk.Coin {
	return sdk.NewCoin(types.BondDenom, math.ZeroInt())
}

type mockAccountKeeper struct{}

func (m *mockAccountKeeper) GetModuleAddress(name string) sdk.AccAddress {
	return sdk.AccAddress([]byte(name))
}

func (m *mockAccountKeeper) GetModuleAccount(_ context.Context, _ string) sdk.ModuleAccountI {
	return nil
}

func setupRewardKeeper(t *testing.T) (keeper.Keeper, sdk.Context, *mockBankKeeper) {
	t.Helper()
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	db := dbm.NewMemDB()
	stateStore := store.NewCommitMultiStore(db, log.NewNopLogger(), storemetrics.NewNoOpMetrics())
	stateStore.MountStoreWithDB(storeKey, storetypes.StoreTypeIAVL, db)
	_ = stateStore.LoadLatestVersion()
	registry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(registry)
	bk := newMockBankKeeper()
	ak := &mockAccountKeeper{}
	k := keeper.NewKeeper(cdc, storeKey, bk, ak, "authority")
	ctx := sdk.NewContext(stateStore, cmtproto.Header{Height: 100}, false, log.NewNopLogger())
	_ = k.SetParams(ctx, types.DefaultParams())
	return k, ctx, bk
}

func TestDistributeRewards_WithContributions(t *testing.T) {
	k, ctx, bk := setupRewardKeeper(t)

	addr1 := sdk.AccAddress([]byte("worker1_____________"))
	addr2 := sdk.AccAddress([]byte("worker2_____________"))

	contributions := []types.WorkerContribution{
		{WorkerAddress: addr1.String(), FeeAmount: math.NewInt(800), TaskCount: 20},
		{WorkerAddress: addr2.String(), FeeAmount: math.NewInt(200), TaskCount: 80},
	}

	err := k.DistributeRewards(ctx, contributions, nil, nil, nil)
	if err != nil {
		t.Fatalf("DistributeRewards failed: %v", err)
	}

	// Verify both workers received rewards
	sent1, ok1 := bk.sent[addr1.String()]
	sent2, ok2 := bk.sent[addr2.String()]
	if !ok1 || !ok2 {
		t.Fatal("both workers should receive rewards")
	}
	if !sent1.IsPositive() {
		t.Fatal("worker1 should have positive reward")
	}
	if !sent2.IsPositive() {
		t.Fatal("worker2 should have positive reward")
	}

	// Worker1 has 80% of fees but only 20% of task count
	// w1 = 0.8*(800/1000) + 0.2*(20/100) = 0.64 + 0.04 = 0.68
	// w2 = 0.8*(200/1000) + 0.2*(80/100) = 0.16 + 0.16 = 0.32
	// Worker1 should get more than worker2
	if sent1.LTE(sent2) {
		t.Fatalf("worker1 (higher fees) should get more reward; got w1=%s, w2=%s", sent1.String(), sent2.String())
	}

	// V5.2: 99% goes to inference contributors, so total sent = 99% of epoch reward
	totalSent := sent1.Add(sent2)
	epochReward := k.CalculateEpochReward(ctx, 100)
	inferenceReward := types.DefaultInferenceWeight.MulInt(epochReward).TruncateInt()
	if !totalSent.Equal(inferenceReward) {
		t.Fatalf("total distributed %s should equal inference reward (99%%) %s", totalSent.String(), inferenceReward.String())
	}

	// Verify reward records were created
	records := k.GetRewardRecords(ctx, addr1.String())
	if len(records) != 1 {
		t.Fatalf("expected 1 reward record for worker1, got %d", len(records))
	}
}

func TestDistributeRewards_NoContributions_ByStake(t *testing.T) {
	k, ctx, bk := setupRewardKeeper(t)

	addr1 := sdk.AccAddress([]byte("worker1_____________"))
	addr2 := sdk.AccAddress([]byte("worker2_____________"))

	onlineWorkers := []types.OnlineWorkerStake{
		{WorkerAddress: addr1.String(), Stake: math.NewInt(30000)},
		{WorkerAddress: addr2.String(), Stake: math.NewInt(10000)},
	}

	err := k.DistributeRewards(ctx, nil, nil, nil, onlineWorkers)
	if err != nil {
		t.Fatalf("DistributeRewards by stake failed: %v", err)
	}

	sent1, ok1 := bk.sent[addr1.String()]
	sent2, ok2 := bk.sent[addr2.String()]
	if !ok1 || !ok2 {
		t.Fatal("both workers should receive rewards")
	}

	// Worker1 has 3x the stake of worker2, should get ~3x reward
	if sent1.LTE(sent2) {
		t.Fatalf("worker1 (3x stake) should get more reward; got w1=%s, w2=%s", sent1.String(), sent2.String())
	}

	totalSent := sent1.Add(sent2)
	epochReward := k.CalculateEpochReward(ctx, 100)
	if !totalSent.Equal(epochReward) {
		t.Fatalf("total distributed %s should equal epoch reward %s", totalSent.String(), epochReward.String())
	}
}

func TestDistributeRewards_NoWorkersNoContributions(t *testing.T) {
	k, ctx, bk := setupRewardKeeper(t)

	err := k.DistributeRewards(ctx, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("should not error with no workers: %v", err)
	}

	if len(bk.sent) != 0 {
		t.Fatal("no rewards should be sent when there are no workers")
	}
}

func TestDistributeRewards_SingleWorker(t *testing.T) {
	k, ctx, bk := setupRewardKeeper(t)

	addr1 := sdk.AccAddress([]byte("worker1_____________"))
	contributions := []types.WorkerContribution{
		{WorkerAddress: addr1.String(), FeeAmount: math.NewInt(1000), TaskCount: 50},
	}

	err := k.DistributeRewards(ctx, contributions, nil, nil, nil)
	if err != nil {
		t.Fatalf("DistributeRewards failed: %v", err)
	}

	sent1 := bk.sent[addr1.String()]
	epochReward := k.CalculateEpochReward(ctx, 100)
	inferenceReward := types.DefaultInferenceWeight.MulInt(epochReward).TruncateInt()
	if !sent1.Equal(inferenceReward) {
		t.Fatalf("single worker should get 99%% inference reward: expected %s, got %s", inferenceReward.String(), sent1.String())
	}
}

func TestBlockReward_Halving(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	baseReward := types.DefaultBaseBlockReward
	halvingPeriod := types.DefaultHalvingPeriod

	// Block 1: full reward
	r0 := k.CalculateBlockReward(ctx, 1)
	if !r0.Equal(baseReward) {
		t.Fatalf("block 1 reward should be %s, got %s", baseReward.String(), r0.String())
	}

	// Block at halving boundary: still full reward (halvings = halvingPeriod/halvingPeriod = 1 → halved)
	r1 := k.CalculateBlockReward(ctx, halvingPeriod)
	expected1 := baseReward.QuoRaw(2)
	if !r1.Equal(expected1) {
		t.Fatalf("block %d reward should be %s (halved), got %s", halvingPeriod, expected1.String(), r1.String())
	}

	// Just before first halving: still full reward
	rBeforeHalving := k.CalculateBlockReward(ctx, halvingPeriod-1)
	if !rBeforeHalving.Equal(baseReward) {
		t.Fatalf("block %d (pre-halving) reward should be %s, got %s", halvingPeriod-1, baseReward.String(), rBeforeHalving.String())
	}

	// After 2 halvings
	r2 := k.CalculateBlockReward(ctx, 2*halvingPeriod)
	expected2 := baseReward.QuoRaw(4)
	if !r2.Equal(expected2) {
		t.Fatalf("after 2 halvings: expected %s, got %s", expected2.String(), r2.String())
	}

	// After 64+ halvings should be zero
	r64 := k.CalculateBlockReward(ctx, 64*halvingPeriod)
	if !r64.IsZero() {
		t.Fatalf("after 64 halvings reward should be zero, got %s", r64.String())
	}
}

func TestCalculateEpochReward(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	// Default epoch is 100 blocks, base reward is 4000 * 10^6 ufai per block
	epochReward := k.CalculateEpochReward(ctx, 100)
	expectedPerBlock := types.DefaultBaseBlockReward
	expected := expectedPerBlock.MulRaw(100)
	if !epochReward.Equal(expected) {
		t.Fatalf("epoch reward for blocks 1-100 should be %s, got %s", expected.String(), epochReward.String())
	}
}

func TestCalculateEpochReward_AcrossHalving(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	params := types.DefaultParams()
	params.EpochBlocks = 10
	_ = k.SetParams(ctx, params)

	halvingPeriod := types.DefaultHalvingPeriod
	epochEnd := halvingPeriod + 5

	reward := k.CalculateEpochReward(ctx, epochEnd)
	if reward.IsZero() {
		t.Fatal("epoch reward across halving should not be zero")
	}
	if !reward.IsPositive() {
		t.Fatal("epoch reward should be positive")
	}

	// Epoch spans blocks (halvingPeriod-4) to (halvingPeriod+5):
	//   4 blocks before halving at full reward + 6 blocks after at half reward
	//   = 4*base + 6*(base/2) = 4*base + 3*base = 7*base
	base := types.DefaultBaseBlockReward
	expected := base.MulRaw(4).Add(base.QuoRaw(2).MulRaw(6))
	if !reward.Equal(expected) {
		t.Fatalf("epoch across halving: expected %s (4*full + 6*half), got %s",
			expected.String(), reward.String())
	}
}

func TestDefaultParams_Valid(t *testing.T) {
	params := types.DefaultParams()
	if err := params.Validate(); err != nil {
		t.Fatalf("default params should be valid: %v", err)
	}
	if !params.BaseBlockReward.Equal(math.NewInt(4_000_000_000)) {
		t.Fatalf("expected base block reward 4000*10^6, got %s", params.BaseBlockReward.String())
	}
	if params.HalvingPeriod != 26_250_000 {
		t.Fatalf("expected halving period 26250000, got %d", params.HalvingPeriod)
	}
	if !params.FeeWeight.Equal(math.LegacyNewDecWithPrec(85, 2)) {
		t.Fatalf("expected fee_weight 0.85, got %s", params.FeeWeight.String())
	}
	if !params.CountWeight.Equal(math.LegacyNewDecWithPrec(15, 2)) {
		t.Fatalf("expected count_weight 0.15, got %s", params.CountWeight.String())
	}
	if params.EpochBlocks != 100 {
		t.Fatalf("expected epoch_blocks 100, got %d", params.EpochBlocks)
	}
}

func TestParamsValidation_Invalid(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*types.Params)
	}{
		{"negative base reward", func(p *types.Params) { p.BaseBlockReward = math.NewInt(-1) }},
		{"zero halving period", func(p *types.Params) { p.HalvingPeriod = 0 }},
		{"negative halving period", func(p *types.Params) { p.HalvingPeriod = -1 }},
		{"negative fee weight", func(p *types.Params) { p.FeeWeight = math.LegacyNewDec(-1) }},
		{"fee weight > 1", func(p *types.Params) { p.FeeWeight = math.LegacyNewDecWithPrec(101, 2) }},
		{"negative count weight", func(p *types.Params) { p.CountWeight = math.LegacyNewDec(-1) }},
		{"zero epoch blocks", func(p *types.Params) { p.EpochBlocks = 0 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := types.DefaultParams()
			tc.modify(&p)
			if err := p.Validate(); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestSetAndGetParams(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	custom := types.Params{
		BaseBlockReward:             math.NewInt(2000),
		HalvingPeriod:               1000,
		FeeWeight:                   math.LegacyNewDecWithPrec(60, 2),
		CountWeight:                 math.LegacyNewDecWithPrec(40, 2),
		EpochBlocks:                 50,
		TotalSupply:                 math.NewInt(100_000_000),
		InferenceWeight:             math.LegacyNewDecWithPrec(85, 2),
		VerificationWeight:          math.LegacyNewDecWithPrec(12, 2),
		MultiVerificationFundWeight: math.LegacyNewDecWithPrec(3, 2),
	}
	err := k.SetParams(ctx, custom)
	if err != nil {
		t.Fatalf("SetParams failed: %v", err)
	}

	got := k.GetParams(ctx)
	if !got.BaseBlockReward.Equal(math.NewInt(2000)) {
		t.Fatalf("expected base_block_reward 2000, got %s", got.BaseBlockReward.String())
	}
	if got.EpochBlocks != 50 {
		t.Fatalf("expected epoch_blocks 50, got %d", got.EpochBlocks)
	}
}

func TestRewardRecord_SetAndGet(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	record := types.RewardRecord{
		Epoch:         1,
		WorkerAddress: addr.String(),
		Amount:        sdk.NewCoin(types.BondDenom, math.NewInt(5000)),
	}
	k.SetRewardRecord(ctx, record)

	records := k.GetRewardRecords(ctx, addr.String())
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Epoch != 1 {
		t.Fatalf("expected epoch 1, got %d", records[0].Epoch)
	}
	if !records[0].Amount.Amount.Equal(math.NewInt(5000)) {
		t.Fatalf("expected amount 5000, got %s", records[0].Amount.Amount.String())
	}
}

func TestRewardRecord_GetAll(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	addr1 := sdk.AccAddress([]byte("worker1_____________"))
	addr2 := sdk.AccAddress([]byte("worker2_____________"))

	k.SetRewardRecord(ctx, types.RewardRecord{Epoch: 1, WorkerAddress: addr1.String(), Amount: sdk.NewCoin(types.BondDenom, math.NewInt(100))})
	k.SetRewardRecord(ctx, types.RewardRecord{Epoch: 1, WorkerAddress: addr2.String(), Amount: sdk.NewCoin(types.BondDenom, math.NewInt(200))})
	k.SetRewardRecord(ctx, types.RewardRecord{Epoch: 2, WorkerAddress: addr1.String(), Amount: sdk.NewCoin(types.BondDenom, math.NewInt(300))})

	// Get all records (empty filter)
	all := k.GetRewardRecords(ctx, "")
	if len(all) != 3 {
		t.Fatalf("expected 3 total records, got %d", len(all))
	}

	// Filter by worker
	w1Records := k.GetRewardRecords(ctx, addr1.String())
	if len(w1Records) != 2 {
		t.Fatalf("expected 2 records for worker1, got %d", len(w1Records))
	}
}

func TestGenesisValidation(t *testing.T) {
	gs := types.DefaultGenesis()
	if err := gs.Validate(); err != nil {
		t.Fatalf("default genesis should be valid: %v", err)
	}
}

func TestDistributeRewards_ContributionWeightAccuracy(t *testing.T) {
	k, ctx, bk := setupRewardKeeper(t)

	addr1 := sdk.AccAddress([]byte("worker1_____________"))
	addr2 := sdk.AccAddress([]byte("worker2_____________"))
	addr3 := sdk.AccAddress([]byte("worker3_____________"))

	// Equal contributions across 3 workers
	contributions := []types.WorkerContribution{
		{WorkerAddress: addr1.String(), FeeAmount: math.NewInt(1000), TaskCount: 100},
		{WorkerAddress: addr2.String(), FeeAmount: math.NewInt(1000), TaskCount: 100},
		{WorkerAddress: addr3.String(), FeeAmount: math.NewInt(1000), TaskCount: 100},
	}

	err := k.DistributeRewards(ctx, contributions, nil, nil, nil)
	if err != nil {
		t.Fatalf("DistributeRewards failed: %v", err)
	}

	sent1 := bk.sent[addr1.String()]
	sent2 := bk.sent[addr2.String()]
	sent3 := bk.sent[addr3.String()]

	// V5.2: 99% goes to inference contributors
	totalSent := sent1.Add(sent2).Add(sent3)
	epochReward := k.CalculateEpochReward(ctx, 100)
	inferenceReward := types.DefaultInferenceWeight.MulInt(epochReward).TruncateInt()
	if !totalSent.Equal(inferenceReward) {
		t.Fatalf("total distributed %s should equal inference reward (99%%) %s", totalSent.String(), inferenceReward.String())
	}
}

func TestDistributeRewards_ZeroEpochReward(t *testing.T) {
	k, ctx, bk := setupRewardKeeper(t)

	// Set height far beyond all halvings
	farCtx := ctx.WithBlockHeight(64 * types.DefaultHalvingPeriod)

	addr1 := sdk.AccAddress([]byte("worker1_____________"))
	contributions := []types.WorkerContribution{
		{WorkerAddress: addr1.String(), FeeAmount: math.NewInt(1000), TaskCount: 50},
	}

	err := k.DistributeRewards(farCtx, contributions, nil, nil, nil)
	if err != nil {
		t.Fatalf("should not error with zero reward: %v", err)
	}

	if len(bk.sent) != 0 {
		t.Fatal("no rewards should be sent when epoch reward is zero")
	}
}

// -------- GetAuthority --------

func TestGetAuthority(t *testing.T) {
	k, _, _ := setupRewardKeeper(t)
	auth := k.GetAuthority()
	if auth != "authority" {
		t.Fatalf("expected authority 'authority', got '%s'", auth)
	}
}

// -------- Genesis Init / Export --------

func TestInitGenesis_ExportGenesis(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	gs := types.GenesisState{
		Params: types.DefaultParams(),
		RewardRecords: []types.RewardRecord{
			{Epoch: 1, WorkerAddress: addr.String(), Amount: sdk.NewCoin(types.BondDenom, math.NewInt(5000))},
			{Epoch: 2, WorkerAddress: addr.String(), Amount: sdk.NewCoin(types.BondDenom, math.NewInt(3000))},
		},
	}

	k.InitGenesis(ctx, gs)

	records := k.GetRewardRecords(ctx, addr.String())
	if len(records) != 2 {
		t.Fatalf("expected 2 records after InitGenesis, got %d", len(records))
	}

	exported := k.ExportGenesis(ctx)
	if len(exported.RewardRecords) != 2 {
		t.Fatalf("expected 2 records in exported genesis, got %d", len(exported.RewardRecords))
	}
}

// -------- gRPC Query --------

func TestQueryParams(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	resp, err := k.QueryParams(ctx, &types.QueryParamsRequest{})
	if err != nil {
		t.Fatalf("QueryParams failed: %v", err)
	}
	if !resp.Params.BaseBlockReward.Equal(types.DefaultBaseBlockReward) {
		t.Fatalf("expected base block reward %s, got %s", types.DefaultBaseBlockReward.String(), resp.Params.BaseBlockReward.String())
	}
}

func TestQueryRewardHistory(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	k.SetRewardRecord(ctx, types.RewardRecord{
		Epoch: 1, WorkerAddress: addr.String(), Amount: sdk.NewCoin(types.BondDenom, math.NewInt(100)),
	})

	resp, err := k.QueryRewardHistory(ctx, &types.QueryRewardHistoryRequest{WorkerAddress: addr.String()})
	if err != nil {
		t.Fatalf("QueryRewardHistory failed: %v", err)
	}
	if len(resp.Records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(resp.Records))
	}
}

// -------- MsgServer UpdateParams --------

func TestMsgServer_UpdateParams(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	newParams := types.DefaultParams()
	newParams.EpochBlocks = 200

	msg := &types.MsgUpdateParams{
		Authority: "authority",
		Params:    newParams,
	}

	_, err := ms.UpdateParams(ctx, msg)
	if err != nil {
		t.Fatalf("UpdateParams failed: %v", err)
	}

	got := k.GetParams(ctx)
	if got.EpochBlocks != 200 {
		t.Fatalf("expected epoch_blocks 200, got %d", got.EpochBlocks)
	}
}

func TestMsgServer_UpdateParams_Unauthorized(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	wrongAuth := sdk.AccAddress([]byte("wrong_authority_____"))
	msg := &types.MsgUpdateParams{
		Authority: wrongAuth.String(),
		Params:    types.DefaultParams(),
	}

	_, err := ms.UpdateParams(ctx, msg)
	if err == nil {
		t.Fatal("expected error for unauthorized authority")
	}
}

func TestMsgServer_UpdateParams_InvalidParams(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	badParams := types.DefaultParams()
	badParams.HalvingPeriod = 0

	msg := &types.MsgUpdateParams{
		Authority: "authority",
		Params:    badParams,
	}

	_, err := ms.UpdateParams(ctx, msg)
	if err == nil {
		t.Fatal("expected error for invalid params")
	}
}

// -------- distributeByStake zero total stake --------

func TestDistributeRewards_ByStake_ZeroTotalStake(t *testing.T) {
	k, ctx, bk := setupRewardKeeper(t)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	onlineWorkers := []types.OnlineWorkerStake{
		{WorkerAddress: addr.String(), Stake: math.ZeroInt()},
	}

	err := k.DistributeRewards(ctx, nil, nil, nil, onlineWorkers)
	if err != nil {
		t.Fatalf("should not error with zero total stake: %v", err)
	}
	if len(bk.sent) != 0 {
		t.Fatal("no rewards should be sent with zero total stake")
	}
}

// -------- SetParams with invalid params --------

func TestSetParams_Invalid(t *testing.T) {
	k, ctx, _ := setupRewardKeeper(t)
	badParams := types.DefaultParams()
	badParams.HalvingPeriod = 0
	err := k.SetParams(ctx, badParams)
	if err == nil {
		t.Fatal("expected error for invalid params")
	}
}
