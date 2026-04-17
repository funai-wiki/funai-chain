package e2e_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	"cosmossdk.io/store"
	storemetrics "cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	"github.com/cometbft/cometbft/crypto/secp256k1"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"

	rewardkeeper "github.com/funai-wiki/funai-chain/x/reward/keeper"
	rewardtypes "github.com/funai-wiki/funai-chain/x/reward/types"
	settlementkeeper "github.com/funai-wiki/funai-chain/x/settlement/keeper"
	settlementtypes "github.com/funai-wiki/funai-chain/x/settlement/types"
)

// ---- Fixed test key for proposer signing ----

var testProposerKey = secp256k1.GenPrivKey()

// ---- Tracking bank keeper: records all coin movements ----

type trackingBankKeeper struct {
	received map[string]math.Int // addr → total coins received from module
	minted   map[string]math.Int // module → total coins minted
}

func newTrackingBankKeeper() *trackingBankKeeper {
	return &trackingBankKeeper{
		received: make(map[string]math.Int),
		minted:   make(map[string]math.Int),
	}
}

func (m *trackingBankKeeper) SendCoins(_ context.Context, _, _ sdk.AccAddress, _ sdk.Coins) error {
	return nil
}

func (m *trackingBankKeeper) SendCoinsFromAccountToModule(_ context.Context, _ sdk.AccAddress, _ string, _ sdk.Coins) error {
	return nil
}

func (m *trackingBankKeeper) SendCoinsFromModuleToAccount(_ context.Context, _ string, recipient sdk.AccAddress, amt sdk.Coins) error {
	addr := recipient.String()
	if _, ok := m.received[addr]; !ok {
		m.received[addr] = math.ZeroInt()
	}
	m.received[addr] = m.received[addr].Add(amt[0].Amount)
	return nil
}

func (m *trackingBankKeeper) MintCoins(_ context.Context, moduleName string, amounts sdk.Coins) error {
	if _, ok := m.minted[moduleName]; !ok {
		m.minted[moduleName] = math.ZeroInt()
	}
	for _, c := range amounts {
		m.minted[moduleName] = m.minted[moduleName].Add(c.Amount)
	}
	return nil
}

func (m *trackingBankKeeper) SendCoinsFromModuleToModule(_ context.Context, _, recipientModule string, amt sdk.Coins) error {
	key := "module:" + recipientModule
	if _, ok := m.received[key]; !ok {
		m.received[key] = math.ZeroInt()
	}
	for _, c := range amt {
		m.received[key] = m.received[key].Add(c.Amount)
	}
	return nil
}

func (m *trackingBankKeeper) GetBalance(_ context.Context, _ sdk.AccAddress, _ string) sdk.Coin {
	return sdk.NewCoin("ufai", math.ZeroInt())
}

func (m *trackingBankKeeper) receivedBy(addr sdk.AccAddress) math.Int {
	v, ok := m.received[addr.String()]
	if !ok {
		return math.ZeroInt()
	}
	return v
}

func (m *trackingBankKeeper) totalMinted() math.Int {
	total := math.ZeroInt()
	for _, v := range m.minted {
		total = total.Add(v)
	}
	return total
}

func (m *trackingBankKeeper) totalReceived() math.Int {
	total := math.ZeroInt()
	for _, v := range m.received {
		total = total.Add(v)
	}
	return total
}

// ---- Mock worker keeper ----

type mockWorkerKeeper struct {
	jailCalls   []sdk.AccAddress
	slashCalls  []sdk.AccAddress
	streakCalls []sdk.AccAddress
	statsCalls  []sdk.AccAddress
}

func newMockWorkerKeeper() *mockWorkerKeeper {
	return &mockWorkerKeeper{}
}

func (m *mockWorkerKeeper) JailWorker(_ sdk.Context, addr sdk.AccAddress, _ int64) {
	m.jailCalls = append(m.jailCalls, addr)
}

func (m *mockWorkerKeeper) SlashWorker(_ sdk.Context, addr sdk.AccAddress, _ uint32) {
	m.slashCalls = append(m.slashCalls, addr)
}

func (m *mockWorkerKeeper) SlashWorkerTo(_ sdk.Context, addr sdk.AccAddress, _ uint32, _ sdk.AccAddress) {
	m.slashCalls = append(m.slashCalls, addr)
}

func (m *mockWorkerKeeper) IncrementSuccessStreak(_ sdk.Context, addr sdk.AccAddress) {
	m.streakCalls = append(m.streakCalls, addr)
}

func (m *mockWorkerKeeper) GetSuccessStreak(_ sdk.Context, _ sdk.AccAddress) uint32 { return 0 }

func (m *mockWorkerKeeper) UpdateWorkerStats(_ sdk.Context, addr sdk.AccAddress, _ sdk.Coin) {
	m.statsCalls = append(m.statsCalls, addr)
}

func (m *mockWorkerKeeper) TombstoneWorker(_ sdk.Context, _ sdk.AccAddress) {}

func (m *mockWorkerKeeper) ReputationOnAccept(_ sdk.Context, _ sdk.AccAddress) {}

func (m *mockWorkerKeeper) UpdateAvgLatency(_ sdk.Context, _ sdk.AccAddress, _ uint32) {}

func (m *mockWorkerKeeper) GetWorkerPubkey(_ sdk.Context, _ sdk.AccAddress) (string, bool) {
	return string(testProposerKey.PubKey().Bytes()), true
}

// ---- Mock account keeper (for reward module) ----

type mockAccountKeeper struct{}

func (m *mockAccountKeeper) GetModuleAddress(name string) sdk.AccAddress {
	return sdk.AccAddress([]byte(name))
}

func (m *mockAccountKeeper) GetModuleAccount(_ context.Context, _ string) sdk.ModuleAccountI {
	return nil
}

// ---- Helpers ----

func makeAddr(name string) sdk.AccAddress {
	addr := make([]byte, 20)
	copy(addr, name)
	return addr
}

func taskId(index int) []byte {
	h := sha256.Sum256([]byte(fmt.Sprintf("e2e-task-%d", index)))
	return h[:]
}

func buildEntries(userAddr, workerAddr string, verifierAddrs []string, count int, fee sdk.Coin, expireBlock int64) []settlementtypes.SettlementEntry {
	entries := make([]settlementtypes.SettlementEntry, count)
	for i := 0; i < count; i++ {
		vr := make([]settlementtypes.VerifierResult, len(verifierAddrs))
		for j, v := range verifierAddrs {
			vr[j] = settlementtypes.VerifierResult{Address: v, Pass: true, Signature: []byte("sig")}
		}
		entries[i] = settlementtypes.SettlementEntry{
			TaskId:          taskId(i),
			UserAddress:     userAddr,
			WorkerAddress:   workerAddr,
			VerifierResults: vr,
			Fee:             fee,
			Status:          settlementtypes.SettlementSuccess,
			ExpireBlock:     expireBlock,
			ModelId:         "model-abc",
			LatencyMs:       50,
			UserSigHash:     []byte("user-sig-hash-32bytes-padding!!"),
			WorkerSigHash:   []byte("worker-sig-hash-32bytes-padding"),
			VerifySigHashes: [][]byte{[]byte("vsig1-hash"), []byte("vsig2-hash"), []byte("vsig3-hash")},
		}
	}
	return entries
}

func buildBatchMsg(proposer string, entries []settlementtypes.SettlementEntry) *settlementtypes.MsgBatchSettlement {
	merkleRoot := settlementkeeper.ComputeMerkleRoot(entries)
	msgHash := sha256.Sum256(merkleRoot)
	sig, _ := testProposerKey.Sign(msgHash[:])
	return settlementtypes.NewMsgBatchSettlement(proposer, merkleRoot, entries, sig)
}

// ---- Test setup: dual-keeper with shared store ----

type testEnv struct {
	settlementKeeper settlementkeeper.Keeper
	rewardKeeper     rewardkeeper.Keeper
	ctx              sdk.Context
	bk               *trackingBankKeeper
	wk               *mockWorkerKeeper
}

func setupTestEnv(t *testing.T) *testEnv {
	t.Helper()

	settlementStoreKey := storetypes.NewKVStoreKey(settlementtypes.StoreKey)
	rewardStoreKey := storetypes.NewKVStoreKey(rewardtypes.StoreKey)

	db := dbm.NewMemDB()
	stateStore := store.NewCommitMultiStore(db, log.NewNopLogger(), storemetrics.NewNoOpMetrics())
	stateStore.MountStoreWithDB(settlementStoreKey, storetypes.StoreTypeIAVL, db)
	stateStore.MountStoreWithDB(rewardStoreKey, storetypes.StoreTypeIAVL, db)
	if err := stateStore.LoadLatestVersion(); err != nil {
		t.Fatal(err)
	}

	registry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(registry)

	bk := newTrackingBankKeeper()
	wk := newMockWorkerKeeper()
	ak := &mockAccountKeeper{}

	sk := settlementkeeper.NewKeeper(cdc, settlementStoreKey, bk, wk, "authority", log.NewNopLogger())
	rk := rewardkeeper.NewKeeper(cdc, rewardStoreKey, bk, ak, "authority")

	ctx := sdk.NewContext(stateStore, cmtproto.Header{Height: 100}, false, log.NewNopLogger())
	sk.SetParams(ctx, settlementtypes.DefaultParams())
	_ = rk.SetParams(ctx, rewardtypes.DefaultParams())

	return &testEnv{
		settlementKeeper: sk,
		rewardKeeper:     rk,
		ctx:              ctx,
		bk:               bk,
		wk:               wk,
	}
}

// TestHappyPath_InferenceToReward validates the full on-chain flow:
// deposit → batch settlement (SUCCESS) → fee deduction → fee distribution → epoch reward.
func TestHappyPath_InferenceToReward(t *testing.T) {
	env := setupTestEnv(t)
	sk := env.settlementKeeper
	rk := env.rewardKeeper
	ctx := env.ctx
	bk := env.bk
	wk := env.wk

	userAddr := makeAddr("e2e-user")
	workerAddr := makeAddr("e2e-worker")
	v1 := makeAddr("e2e-v1")
	v2 := makeAddr("e2e-v2")
	v3 := makeAddr("e2e-v3")
	proposerAddr := makeAddr("e2e-proposer")
	verifiers := []string{v1.String(), v2.String(), v3.String()}

	// Disable audit so all tasks settle immediately
	sk.SetCurrentSecondVerificationRate(ctx, 0)

	// ========== Phase 2: Deposit ==========
	deposit := sdk.NewCoin("ufai", math.NewInt(100_000_000_000))
	if err := sk.ProcessDeposit(ctx, userAddr, deposit); err != nil {
		t.Fatalf("Phase 2 - Deposit failed: %v", err)
	}
	ia, found := sk.GetInferenceAccount(ctx, userAddr)
	if !found || !ia.Balance.Equal(deposit) {
		t.Fatalf("Phase 2 - Balance: want %s, got %s (found=%v)", deposit, ia.Balance, found)
	}
	t.Logf("[Phase 2] Deposit %s OK", deposit)

	// ========== Phase 3: Batch Settlement (10 SUCCESS tasks × 1 FAI each) ==========
	taskCount := 10
	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	entries := buildEntries(userAddr.String(), workerAddr.String(), verifiers, taskCount, fee, 100000)
	msg := buildBatchMsg(proposerAddr.String(), entries)
	batchId, err := sk.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("Phase 3 - BatchSettlement failed: %v", err)
	}
	t.Logf("[Phase 3] Batch %d settled: %d tasks", batchId, taskCount)

	// ========== Phase 4: Verify Fee Deduction ==========
	ia, _ = sk.GetInferenceAccount(ctx, userAddr)
	expectedBalance := deposit.Amount.Sub(fee.Amount.MulRaw(int64(taskCount)))
	if !ia.Balance.Amount.Equal(expectedBalance) {
		t.Fatalf("Phase 4 - User balance: want %s, got %s", expectedBalance, ia.Balance.Amount)
	}
	t.Logf("[Phase 4] User balance after settlement: %s (deducted %s)", ia.Balance.Amount, fee.Amount.MulRaw(int64(taskCount)))

	epoch := ctx.BlockHeight() / 100
	stats := sk.GetEpochStats(ctx, epoch)
	if stats.TotalSettled != uint64(taskCount) {
		t.Fatalf("Phase 4 - TotalSettled: want %d, got %d", taskCount, stats.TotalSettled)
	}
	if stats.FailSettled != 0 {
		t.Fatalf("Phase 4 - FailSettled: want 0, got %d", stats.FailSettled)
	}
	if len(wk.streakCalls) != taskCount {
		t.Fatalf("Phase 4 - IncrementSuccessStreak calls: want %d, got %d", taskCount, len(wk.streakCalls))
	}
	if len(wk.statsCalls) != taskCount {
		t.Fatalf("Phase 4 - UpdateWorkerStats calls: want %d, got %d", taskCount, len(wk.statsCalls))
	}
	t.Logf("[Phase 4] EpochStats: total=%d, fail=%d, streak=%d, stats=%d",
		stats.TotalSettled, stats.FailSettled, len(wk.streakCalls), len(wk.statsCalls))

	// ========== Phase 5: Verify Fee Distribution ==========
	params := sk.GetParams(ctx)

	// Per 1 FAI (1_000_000 ufai) task (S9 ratios):
	//   verifier total = 1_000_000 * 30 / 1000 = 30_000
	//   audit fund     = 1_000_000 * 10 / 1000 = 10_000
	//   burn           = 1_000_000 * 10 / 1000 = 10_000
	//   executor       = 1_000_000 - 30_000 - 10_000 - 10_000 = 950_000
	perTaskVerifierTotal := fee.Amount.MulRaw(int64(params.VerifierFeeRatio)).QuoRaw(1000)
	perTaskMultiVerificationFund := fee.Amount.MulRaw(int64(params.MultiVerificationFundRatio)).QuoRaw(1000)
	perTaskExecutor := fee.Amount.Sub(perTaskVerifierTotal).Sub(perTaskMultiVerificationFund)

	totalExecutorExpected := perTaskExecutor.MulRaw(int64(taskCount))
	gotExecutor := bk.receivedBy(workerAddr)
	if !gotExecutor.Equal(totalExecutorExpected) {
		t.Fatalf("Phase 5 - Executor total: want %s, got %s", totalExecutorExpected, gotExecutor)
	}

	// Each verifier gets perVerifier = 30_000 / 3 = 10_000 per task
	perVerifier := perTaskVerifierTotal.QuoRaw(3)
	for i, v := range []sdk.AccAddress{v1, v2, v3} {
		got := bk.receivedBy(v)
		// Last verifier per task gets remainder: 45_000 - 15_000*2 = 15_000
		expected := perVerifier.MulRaw(int64(taskCount))
		if i == 2 {
			lastVerifierPerTask := perTaskVerifierTotal.Sub(perVerifier.MulRaw(2))
			expected = lastVerifierPerTask.MulRaw(int64(taskCount))
		}
		if !got.Equal(expected) {
			t.Fatalf("Phase 5 - Verifier %d (%s): want %s, got %s", i, v, expected, got)
		}
	}

	// Verify total fee accounting: sum of all distributions should equal total fees minus audit fund and burn
	totalDistributed := gotExecutor
	for _, v := range []sdk.AccAddress{v1, v2, v3} {
		totalDistributed = totalDistributed.Add(bk.receivedBy(v))
	}
	totalUserCharged := fee.Amount.MulRaw(int64(taskCount))
	totalMultiVerificationFund := perTaskMultiVerificationFund.MulRaw(int64(taskCount))
	expectedDistributed := totalUserCharged.Sub(totalMultiVerificationFund)
	if !totalDistributed.Equal(expectedDistributed) {
		t.Fatalf("Phase 5 - No-leakage: distributed=%s, expected=%s (user charged=%s, audit fund=%s)",
			totalDistributed, expectedDistributed, totalUserCharged, totalMultiVerificationFund)
	}

	t.Logf("[Phase 5] Fee distribution OK: executor=%s/task, verifier=%s/task each, audit=%s/task, total distributed=%s",
		perTaskExecutor, perVerifier, perTaskMultiVerificationFund, totalDistributed)

	// ========== Phase 6: Epoch Reward Distribution ==========
	// Reset bk tracking for reward phase
	bk.received = make(map[string]math.Int)
	bk.minted = make(map[string]math.Int)

	// Build worker contributions from settlement data
	contributions := []rewardtypes.WorkerContribution{
		{
			WorkerAddress: workerAddr.String(),
			FeeAmount:     fee.Amount.MulRaw(int64(taskCount)),
			TaskCount:     uint64(taskCount),
		},
	}

	// Build verification contributions from verifier epoch counts
	// Each verifier verified 10 tasks (incremented in ProcessBatchSettlement)
	verificationContribs := []rewardtypes.VerificationContribution{
		{WorkerAddress: v1.String(), VerificationCount: uint64(taskCount), AuditCount: 0},
		{WorkerAddress: v2.String(), VerificationCount: uint64(taskCount), AuditCount: 0},
		{WorkerAddress: v3.String(), VerificationCount: uint64(taskCount), AuditCount: 0},
	}

	err = rk.DistributeRewards(ctx, contributions, verificationContribs, nil, nil)
	if err != nil {
		t.Fatalf("Phase 6 - DistributeRewards failed: %v", err)
	}

	// Verify epoch reward calculation
	epochReward := rk.CalculateEpochReward(ctx, ctx.BlockHeight())
	if epochReward.IsZero() {
		t.Fatal("Phase 6 - Epoch reward is zero")
	}

	rewardParams := rk.GetParams(ctx)

	// 85% inference reward → worker (sole contributor)
	inferenceReward := rewardParams.InferenceWeight.MulInt(epochReward).TruncateInt()
	// 12% verifier pool → split among 3 verifiers by fee + count weight
	verifierPool := rewardParams.VerificationWeight.MulInt(epochReward).TruncateInt()
	// 3% multi-verification fund → sent to settlement module
	fundReward := epochReward.Sub(inferenceReward).Sub(verifierPool)

	gotWorkerReward := bk.receivedBy(workerAddr)
	if !gotWorkerReward.Equal(inferenceReward) {
		t.Fatalf("Phase 6 - Worker inference reward: want %s, got %s", inferenceReward, gotWorkerReward)
	}

	// Each verifier contributed identical counts; with no per-verifier fee data wired in this test,
	// the count-weight (15%) dominates and distribution falls back to even-thirds (see
	// distributeByVerification's totalFee==0 branch). Last verifier may get a small remainder.
	sumVerifierRewards := math.ZeroInt()
	for _, v := range []sdk.AccAddress{v1, v2, v3} {
		got := bk.receivedBy(v)
		if got.IsZero() {
			t.Fatalf("Phase 6 - Verifier %s reward should be positive", v)
		}
		sumVerifierRewards = sumVerifierRewards.Add(got)
	}
	if !sumVerifierRewards.Equal(verifierPool) {
		t.Fatalf("Phase 6 - Sum of verifier rewards: want %s (12%%), got %s", verifierPool, sumVerifierRewards)
	}

	// 3% multi-verification fund goes to settlement module account via SendCoinsFromModuleToModule.
	fundReceived := bk.received["module:settlement"]
	if fundReceived.IsNil() {
		fundReceived = math.ZeroInt()
	}
	if !fundReceived.Equal(fundReward) {
		t.Fatalf("Phase 6 - Multi-verification fund: want %s (3%%), got %s", fundReward, fundReceived)
	}

	// Verify RewardRecords are persisted
	workerRecords := rk.GetRewardRecords(ctx, workerAddr.String())
	if len(workerRecords) != 1 {
		t.Fatalf("Phase 6 - Worker reward records: want 1, got %d", len(workerRecords))
	}
	if !workerRecords[0].Amount.Amount.Equal(inferenceReward) {
		t.Fatalf("Phase 6 - Worker record amount: want %s, got %s", inferenceReward, workerRecords[0].Amount.Amount)
	}

	// Verify total minted == total distributed (no dust loss beyond 1 ufai tolerance).
	// Note: SendCoinsFromModuleToModule does not mint; only MintCoins mints. So totalMinted
	// includes the 3% fund (minted at reward module) and totalReceived includes it (tracked
	// at "module:settlement" by our mock). Both should balance.
	totalMinted := bk.totalMinted()
	totalRewarded := bk.totalReceived()
	if !totalMinted.Equal(totalRewarded) {
		t.Fatalf("Phase 6 - Mint vs distribute mismatch: minted=%s, distributed=%s", totalMinted, totalRewarded)
	}

	t.Logf("[Phase 6] Rewards OK: epoch_reward=%s, inference(85%%)=%s→worker, verifier(12%%)=%s→3 verifiers, fund(3%%)=%s, minted=%s",
		epochReward, inferenceReward, sumVerifierRewards, fundReward, totalMinted)

	// ========== Summary ==========
	t.Log("\n=== HAPPY PATH END-TO-END: ALL PHASES PASSED ===")
	t.Logf("  Deposit:     %s", deposit)
	t.Logf("  Tasks:       %d SUCCESS × %s fee", taskCount, fee)
	t.Logf("  User balance: %s → %s", deposit.Amount, expectedBalance)
	t.Logf("  Fee split:   executor=%s, verifiers=%s, multi-verif-fund=%s (per task)",
		perTaskExecutor, perTaskVerifierTotal, perTaskMultiVerificationFund)
	t.Logf("  Rewards:     epoch=%s, inference=%s, verifier_pool=%s, fund=%s",
		epochReward, inferenceReward, sumVerifierRewards, fundReward)
}

// TestE2E_EpochBoundarySettlement verifies that settlements at epoch boundaries
// produce separate EpochStats for each epoch. Epoch = height / 100.
func TestE2E_EpochBoundarySettlement(t *testing.T) {
	env := setupTestEnv(t)
	sk := env.settlementKeeper
	ctx := env.ctx
	bk := env.bk
	_ = bk

	userAddr := makeAddr("epoch-user")
	workerAddr := makeAddr("epoch-worker")
	v1 := makeAddr("epoch-v1")
	v2 := makeAddr("epoch-v2")
	v3 := makeAddr("epoch-v3")
	proposerAddr := makeAddr("epoch-proposer")
	verifiers := []string{v1.String(), v2.String(), v3.String()}

	sk.SetCurrentSecondVerificationRate(ctx, 0)

	deposit := sdk.NewCoin("ufai", math.NewInt(100_000_000_000))
	if err := sk.ProcessDeposit(ctx, userAddr, deposit); err != nil {
		t.Fatalf("Deposit failed: %v", err)
	}

	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))

	// Settle 5 tasks at block 99 (last block of epoch 0: epoch = 99/100 = 0)
	ctx99 := ctx.WithBlockHeight(99)
	entries99 := buildEntries(userAddr.String(), workerAddr.String(), verifiers, 5, fee, 200000)
	msg99 := buildBatchMsg(proposerAddr.String(), entries99)
	_, err := sk.ProcessBatchSettlement(ctx99, msg99)
	if err != nil {
		t.Fatalf("Settlement at block 99 failed: %v", err)
	}

	// Settle 3 tasks at block 100 (first block of epoch 1: epoch = 100/100 = 1)
	ctx100 := ctx.WithBlockHeight(100)
	// Use different task IDs by building entries with offset
	entries100 := make([]settlementtypes.SettlementEntry, 3)
	for i := 0; i < 3; i++ {
		vr := make([]settlementtypes.VerifierResult, len(verifiers))
		for j, v := range verifiers {
			vr[j] = settlementtypes.VerifierResult{Address: v, Pass: true, Signature: []byte("sig")}
		}
		entries100[i] = settlementtypes.SettlementEntry{
			TaskId:          taskId(100 + i), // offset to avoid duplicate task IDs
			UserAddress:     userAddr.String(),
			WorkerAddress:   workerAddr.String(),
			VerifierResults: vr,
			Fee:             fee,
			Status:          settlementtypes.SettlementSuccess,
			ExpireBlock:     200000,
			ModelId:         "model-abc",
			LatencyMs:       50,
			UserSigHash:     []byte("user-sig-hash-32bytes-padding!!"),
			WorkerSigHash:   []byte("worker-sig-hash-32bytes-padding"),
			VerifySigHashes: [][]byte{[]byte("vsig1-hash"), []byte("vsig2-hash"), []byte("vsig3-hash")},
		}
	}
	msg100 := buildBatchMsg(proposerAddr.String(), entries100)
	_, err = sk.ProcessBatchSettlement(ctx100, msg100)
	if err != nil {
		t.Fatalf("Settlement at block 100 failed: %v", err)
	}

	// Verify epoch stats are separate
	epoch0Stats := sk.GetEpochStats(ctx, 0) // epoch = 99/100 = 0
	epoch1Stats := sk.GetEpochStats(ctx, 1) // epoch = 100/100 = 1

	if epoch0Stats.TotalSettled != 5 {
		t.Fatalf("Epoch 0 should have 5 settled tasks, got %d", epoch0Stats.TotalSettled)
	}
	if epoch1Stats.TotalSettled != 3 {
		t.Fatalf("Epoch 1 should have 3 settled tasks, got %d", epoch1Stats.TotalSettled)
	}
	if epoch0Stats.Epoch != 0 {
		t.Fatalf("Epoch 0 stats should have epoch=0, got %d", epoch0Stats.Epoch)
	}
	if epoch1Stats.Epoch != 1 {
		t.Fatalf("Epoch 1 stats should have epoch=1, got %d", epoch1Stats.Epoch)
	}

	t.Logf("[EpochBoundary] Epoch 0: %d tasks, Epoch 1: %d tasks", epoch0Stats.TotalSettled, epoch1Stats.TotalSettled)
}

// TestE2E_RewardHalvingBoundary verifies reward calculation at the halving boundary.
func TestE2E_RewardHalvingBoundary(t *testing.T) {
	env := setupTestEnv(t)
	rk := env.rewardKeeper

	base := rewardtypes.DefaultBaseBlockReward
	hp := rewardtypes.DefaultHalvingPeriod

	// Block just before halving: full reward
	rewardBefore := rk.CalculateBlockReward(env.ctx, hp-1)
	if !rewardBefore.Equal(base) {
		t.Fatalf("Block %d (before halving): expected %s, got %s", hp-1, base, rewardBefore)
	}

	// Block at halving: halved reward
	rewardAt := rk.CalculateBlockReward(env.ctx, hp)
	expectedHalved := base.QuoRaw(2)
	if !rewardAt.Equal(expectedHalved) {
		t.Fatalf("Block %d (at halving): expected %s, got %s", hp, expectedHalved, rewardAt)
	}

	// Verify halving ratio is exactly 2:1
	if !rewardBefore.Equal(rewardAt.MulRaw(2)) {
		t.Fatalf("Halving ratio should be exactly 2:1: before=%s, after=%s", rewardBefore, rewardAt)
	}

	// Block at 2nd halving
	rewardAt2 := rk.CalculateBlockReward(env.ctx, 2*hp)
	expectedQuarter := base.QuoRaw(4)
	if !rewardAt2.Equal(expectedQuarter) {
		t.Fatalf("Block %d (2nd halving): expected %s, got %s", 2*hp, expectedQuarter, rewardAt2)
	}

	t.Logf("[HalvingBoundary] block %d=%s, block %d=%s, block %d=%s",
		hp-1, rewardBefore, hp, rewardAt, 2*hp, rewardAt2)
}
