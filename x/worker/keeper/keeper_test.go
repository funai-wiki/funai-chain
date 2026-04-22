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

	"github.com/funai-wiki/funai-chain/x/worker/keeper"
	"github.com/funai-wiki/funai-chain/x/worker/types"
)

type mockBankKeeper struct{}

func (m *mockBankKeeper) SendCoins(_ context.Context, _, _ sdk.AccAddress, _ sdk.Coins) error {
	return nil
}
func (m *mockBankKeeper) SendCoinsFromAccountToModule(_ context.Context, _ sdk.AccAddress, _ string, _ sdk.Coins) error {
	return nil
}
func (m *mockBankKeeper) SendCoinsFromModuleToAccount(_ context.Context, _ string, _ sdk.AccAddress, _ sdk.Coins) error {
	return nil
}
func (m *mockBankKeeper) BurnCoins(_ context.Context, _ string, _ sdk.Coins) error { return nil }

func setupKeeper(t *testing.T) (keeper.Keeper, sdk.Context) {
	t.Helper()
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	db := dbm.NewMemDB()
	stateStore := store.NewCommitMultiStore(db, log.NewNopLogger(), storemetrics.NewNoOpMetrics())
	stateStore.MountStoreWithDB(storeKey, storetypes.StoreTypeIAVL, db)
	_ = stateStore.LoadLatestVersion()
	registry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(registry)
	bk := &mockBankKeeper{}
	k := keeper.NewKeeper(cdc, storeKey, bk, log.NewNopLogger())
	ctx := sdk.NewContext(stateStore, cmtproto.Header{Height: 100}, false, log.NewNopLogger())
	k.SetParams(ctx, types.DefaultParams())
	return k, ctx
}

func makeWorker(addr string) types.Worker {
	return types.Worker{
		Address:         addr,
		Pubkey:          "pubkey",
		Stake:           sdk.NewCoin("ufai", math.NewInt(10000)),
		SupportedModels: []string{"model1"},
		Status:          types.WorkerStatusActive,
		JoinedAt:        1,
		Endpoint:        "localhost:8080",
		GpuModel:        "H100",
		GpuVramGb:       80,
		GpuCount:        1,
		OperatorId:      "op1",
		TotalFeeEarned:  sdk.NewCoin("ufai", math.ZeroInt()),
	}
}

func TestWorkerCRUD(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	k.SetWorker(ctx, w)

	got, found := k.GetWorker(ctx, addr)
	if !found {
		t.Fatal("worker not found")
	}
	if got.Address != w.Address {
		t.Fatalf("expected %s, got %s", w.Address, got.Address)
	}

	k.DeleteWorker(ctx, addr)
	_, found = k.GetWorker(ctx, addr)
	if found {
		t.Fatal("worker should be deleted")
	}
}

func TestGetAllWorkers(t *testing.T) {
	k, ctx := setupKeeper(t)

	addr1 := sdk.AccAddress([]byte("worker1_____________"))
	addr2 := sdk.AccAddress([]byte("worker2_____________"))
	k.SetWorker(ctx, makeWorker(addr1.String()))
	k.SetWorker(ctx, makeWorker(addr2.String()))

	all := k.GetAllWorkers(ctx)
	if len(all) != 2 {
		t.Fatalf("expected 2 workers, got %d", len(all))
	}
}

func TestIsWorkerActive(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	k.SetWorker(ctx, w)

	if !k.IsWorkerActive(ctx, addr) {
		t.Fatal("worker should be active")
	}

	w.Status = types.WorkerStatusJailed
	w.Jailed = true
	k.SetWorker(ctx, w)
	if k.IsWorkerActive(ctx, addr) {
		t.Fatal("jailed worker should not be active")
	}
}

func TestGetActiveWorkerCount(t *testing.T) {
	k, ctx := setupKeeper(t)

	addr1 := sdk.AccAddress([]byte("worker1_____________"))
	addr2 := sdk.AccAddress([]byte("worker2_____________"))
	w1 := makeWorker(addr1.String())
	w2 := makeWorker(addr2.String())
	w2.Status = types.WorkerStatusJailed
	w2.Jailed = true
	k.SetWorker(ctx, w1)
	k.SetWorker(ctx, w2)

	count := k.GetActiveWorkerCount(ctx)
	if count != 1 {
		t.Fatalf("expected 1 active worker, got %d", count)
	}
}

func TestModelIndex(t *testing.T) {
	k, ctx := setupKeeper(t)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.SupportedModels = []string{"modelA", "modelB"}
	k.SetWorker(ctx, w)
	k.SetModelIndices(ctx, addr, w.SupportedModels)

	workersA := k.GetWorkersByModel(ctx, "modelA")
	if len(workersA) != 1 {
		t.Fatalf("expected 1 worker for modelA, got %d", len(workersA))
	}

	workersB := k.GetWorkersByModel(ctx, "modelB")
	if len(workersB) != 1 {
		t.Fatalf("expected 1 worker for modelB, got %d", len(workersB))
	}

	workersC := k.GetWorkersByModel(ctx, "modelC")
	if len(workersC) != 0 {
		t.Fatalf("expected 0 workers for modelC, got %d", len(workersC))
	}

	k.RemoveModelIndices(ctx, addr, []string{"modelA"})
	workersA = k.GetWorkersByModel(ctx, "modelA")
	if len(workersA) != 0 {
		t.Fatalf("expected 0 workers after removing modelA index, got %d", len(workersA))
	}
}

func TestJailWorker_Progressive(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	k.SetWorker(ctx, w)

	params := k.GetParams(ctx)

	k.JailWorker(ctx, addr, params.Jail1Duration)
	w1, _ := k.GetWorker(ctx, addr)
	if !w1.Jailed {
		t.Fatal("should be jailed after first offense")
	}
	if w1.JailCount != 1 {
		t.Fatalf("expected jail_count 1, got %d", w1.JailCount)
	}
	if w1.JailUntil != 100+params.Jail1Duration {
		t.Fatalf("expected jail_until %d, got %d", 100+params.Jail1Duration, w1.JailUntil)
	}

	w1.Jailed = false
	w1.Status = types.WorkerStatusActive
	k.SetWorker(ctx, w1)

	k.JailWorker(ctx, addr, params.Jail2Duration)
	w2, _ := k.GetWorker(ctx, addr)
	if w2.JailCount != 2 {
		t.Fatalf("expected jail_count 2, got %d", w2.JailCount)
	}

	w2.Jailed = false
	w2.Status = types.WorkerStatusActive
	k.SetWorker(ctx, w2)

	k.JailWorker(ctx, addr, params.Jail1Duration)
	w3, _ := k.GetWorker(ctx, addr)
	if !w3.Tombstoned {
		t.Fatal("should be tombstoned after 3rd offense")
	}
}

func TestUnjailWorker(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Jailed = true
	w.JailUntil = 50
	w.JailCount = 1
	w.Status = types.WorkerStatusJailed
	k.SetWorker(ctx, w)

	err := k.UnjailWorker(ctx, addr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	w2, _ := k.GetWorker(ctx, addr)
	if w2.Jailed {
		t.Fatal("should not be jailed after unjail")
	}
	if w2.Status != types.WorkerStatusActive {
		t.Fatalf("expected ACTIVE status, got %s", w2.Status)
	}
}

func TestUnjailWorker_PeriodNotElapsed(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Jailed = true
	w.JailUntil = 200
	w.JailCount = 1
	w.Status = types.WorkerStatusJailed
	k.SetWorker(ctx, w)

	err := k.UnjailWorker(ctx, addr)
	if err == nil {
		t.Fatal("expected error when jail period not elapsed")
	}
}

func TestUnjailWorker_Tombstoned(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Jailed = true
	w.Tombstoned = true
	w.Status = types.WorkerStatusJailed
	k.SetWorker(ctx, w)

	err := k.UnjailWorker(ctx, addr)
	if err == nil {
		t.Fatal("expected error for tombstoned worker")
	}
}

func TestUnjailWorker_NotJailed(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	k.SetWorker(ctx, w)

	err := k.UnjailWorker(ctx, addr)
	if err == nil {
		t.Fatal("expected error for non-jailed worker")
	}
}

func TestIncrementSuccessStreak(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.JailCount = 2
	w.SuccessStreak = 49
	k.SetWorker(ctx, w)

	k.IncrementSuccessStreak(ctx, addr)

	w2, _ := k.GetWorker(ctx, addr)
	if w2.JailCount != 0 {
		t.Fatalf("expected jail_count reset to 0 after 50 successes, got %d", w2.JailCount)
	}
	if w2.SuccessStreak != 0 {
		t.Fatalf("expected success_streak reset to 0, got %d", w2.SuccessStreak)
	}
}

func TestIncrementSuccessStreak_NoReset(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.JailCount = 1
	w.SuccessStreak = 10
	k.SetWorker(ctx, w)

	k.IncrementSuccessStreak(ctx, addr)

	w2, _ := k.GetWorker(ctx, addr)
	if w2.SuccessStreak != 11 {
		t.Fatalf("expected success_streak 11, got %d", w2.SuccessStreak)
	}
	if w2.JailCount != 1 {
		t.Fatalf("expected jail_count unchanged at 1, got %d", w2.JailCount)
	}
}

func TestProcessExitingWorkers(t *testing.T) {
	k, ctx := setupKeeper(t)
	params := k.GetParams(ctx)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Status = types.WorkerStatusExiting
	w.ExitRequestedAt = 1
	k.SetWorker(ctx, w)

	exitCtx := ctx.WithBlockHeight(1 + params.ExitWaitPeriod + 1)
	k.ProcessExitingWorkers(exitCtx)

	_, found := k.GetWorker(exitCtx, addr)
	if found {
		t.Fatal("exited worker should be deleted")
	}
}

func TestProcessExitingWorkers_NotYetReady(t *testing.T) {
	k, ctx := setupKeeper(t)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Status = types.WorkerStatusExiting
	w.ExitRequestedAt = 90
	k.SetWorker(ctx, w)

	k.ProcessExitingWorkers(ctx)

	_, found := k.GetWorker(ctx, addr)
	if !found {
		t.Fatal("worker should still exist - exit period not elapsed")
	}
}

func TestProcessExitingWorkers_ActiveNotAffected(t *testing.T) {
	k, ctx := setupKeeper(t)
	params := k.GetParams(ctx)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	k.SetWorker(ctx, w)

	exitCtx := ctx.WithBlockHeight(1 + params.ExitWaitPeriod + 1)
	k.ProcessExitingWorkers(exitCtx)

	_, found := k.GetWorker(exitCtx, addr)
	if !found {
		t.Fatal("active worker should not be affected by ProcessExitingWorkers")
	}
}

func TestGetWorkersByOperatorId(t *testing.T) {
	k, ctx := setupKeeper(t)

	addr1 := sdk.AccAddress([]byte("worker1_____________"))
	addr2 := sdk.AccAddress([]byte("worker2_____________"))
	w1 := makeWorker(addr1.String())
	w1.OperatorId = "op_alpha"
	w2 := makeWorker(addr2.String())
	w2.OperatorId = "op_beta"
	k.SetWorker(ctx, w1)
	k.SetWorker(ctx, w2)

	result := k.GetWorkersByOperatorId(ctx, "op_alpha")
	if len(result) != 1 {
		t.Fatalf("expected 1 worker for op_alpha, got %d", len(result))
	}
	if result[0].Address != w1.Address {
		t.Fatalf("expected %s, got %s", w1.Address, result[0].Address)
	}
}

func TestGetActiveWorkerStake(t *testing.T) {
	k, ctx := setupKeeper(t)

	addr1 := sdk.AccAddress([]byte("worker1_____________"))
	addr2 := sdk.AccAddress([]byte("worker2_____________"))
	w1 := makeWorker(addr1.String())
	w1.Stake = sdk.NewCoin("ufai", math.NewInt(5000))
	w2 := makeWorker(addr2.String())
	w2.Stake = sdk.NewCoin("ufai", math.NewInt(3000))
	k.SetWorker(ctx, w1)
	k.SetWorker(ctx, w2)

	total := k.GetActiveWorkerStake(ctx)
	if !total.Equal(math.NewInt(8000)) {
		t.Fatalf("expected total stake 8000, got %s", total.String())
	}
}

func TestDefaultParams_Valid(t *testing.T) {
	params := types.DefaultParams()
	if err := params.Validate(); err != nil {
		t.Fatalf("default params should be valid: %v", err)
	}
}

// -------- Logger --------

func TestLogger(t *testing.T) {
	k, _ := setupKeeper(t)
	l := k.Logger()
	if l == nil {
		t.Fatal("logger should not be nil")
	}
}

// -------- CountUniqueOperators --------

func TestCountUniqueOperators(t *testing.T) {
	k, ctx := setupKeeper(t)

	addr1 := sdk.AccAddress([]byte("worker1_____________"))
	addr2 := sdk.AccAddress([]byte("worker2_____________"))
	addr3 := sdk.AccAddress([]byte("worker3_____________"))

	w1 := makeWorker(addr1.String())
	w1.OperatorId = "op_alpha"
	w1.SupportedModels = []string{"modelX"}
	w2 := makeWorker(addr2.String())
	w2.OperatorId = "op_beta"
	w2.SupportedModels = []string{"modelX"}
	w3 := makeWorker(addr3.String())
	w3.OperatorId = "op_alpha"
	w3.SupportedModels = []string{"modelX"}

	k.SetWorker(ctx, w1)
	k.SetModelIndices(ctx, addr1, w1.SupportedModels)
	k.SetWorker(ctx, w2)
	k.SetModelIndices(ctx, addr2, w2.SupportedModels)
	k.SetWorker(ctx, w3)
	k.SetModelIndices(ctx, addr3, w3.SupportedModels)

	count := k.CountUniqueOperators(ctx, "modelX")
	if count != 2 {
		t.Fatalf("expected 2 unique operators, got %d", count)
	}

	count = k.CountUniqueOperators(ctx, "nonexistent")
	if count != 0 {
		t.Fatalf("expected 0 unique operators for unknown model, got %d", count)
	}
}

// -------- GetModelInstalledStake --------

func TestGetModelInstalledStake(t *testing.T) {
	k, ctx := setupKeeper(t)

	addr1 := sdk.AccAddress([]byte("worker1_____________"))
	addr2 := sdk.AccAddress([]byte("worker2_____________"))

	w1 := makeWorker(addr1.String())
	w1.Stake = sdk.NewCoin("ufai", math.NewInt(5000))
	w1.SupportedModels = []string{"modelA"}
	w2 := makeWorker(addr2.String())
	w2.Stake = sdk.NewCoin("ufai", math.NewInt(3000))
	w2.SupportedModels = []string{"modelA"}

	k.SetWorker(ctx, w1)
	k.SetModelIndices(ctx, addr1, w1.SupportedModels)
	k.SetWorker(ctx, w2)
	k.SetModelIndices(ctx, addr2, w2.SupportedModels)

	total := k.GetModelInstalledStake(ctx, "modelA")
	if !total.Equal(math.NewInt(8000)) {
		t.Fatalf("expected 8000, got %s", total.String())
	}

	total = k.GetModelInstalledStake(ctx, "nonexistent")
	if !total.IsZero() {
		t.Fatalf("expected 0 for nonexistent model, got %s", total.String())
	}
}

// -------- SlashWorker edge cases --------

func TestSlashWorker_NotFound(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("nonexistent_________"))
	k.SlashWorker(ctx, addr, 5) // should not panic
}

func TestSlashWorker_ZeroStake(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Stake = sdk.NewCoin("ufai", math.ZeroInt())
	k.SetWorker(ctx, w)
	k.SlashWorker(ctx, addr, 5) // should not panic
}

func TestSlashWorker_Success(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Stake = sdk.NewCoin("ufai", math.NewInt(100000))
	k.SetWorker(ctx, w)
	k.SlashWorker(ctx, addr, 5)
	w2, _ := k.GetWorker(ctx, addr)
	expected := math.NewInt(100000).Sub(math.NewInt(100000).MulRaw(5).QuoRaw(100))
	if !w2.Stake.Amount.Equal(expected) {
		t.Fatalf("expected stake %s after 5%% slash, got %s", expected.String(), w2.Stake.Amount.String())
	}
}

// -------- IncrementSuccessStreak edge cases --------

func TestIncrementSuccessStreak_NotFound(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("nonexistent_________"))
	k.IncrementSuccessStreak(ctx, addr) // should not panic
}

// -------- JailWorker edge cases --------

func TestJailWorker_NotFound(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("nonexistent_________"))
	k.JailWorker(ctx, addr, 100) // should not panic
}

// -------- UnjailWorker edge cases --------

func TestUnjailWorker_NotFound(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("nonexistent_________"))
	err := k.UnjailWorker(ctx, addr)
	if err == nil {
		t.Fatal("expected error for nonexistent worker")
	}
}

// -------- IsWorkerActive edge cases --------

func TestIsWorkerActive_NotFound(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("nonexistent_________"))
	if k.IsWorkerActive(ctx, addr) {
		t.Fatal("nonexistent worker should not be active")
	}
}

// -------- Genesis --------

func TestInitGenesis_ExportGenesis(t *testing.T) {
	k, ctx := setupKeeper(t)

	addr1 := sdk.AccAddress([]byte("worker1_____________"))
	addr2 := sdk.AccAddress([]byte("worker2_____________"))

	gs := types.GenesisState{
		Params: types.DefaultParams(),
		Workers: []types.Worker{
			makeWorker(addr1.String()),
			makeWorker(addr2.String()),
		},
	}

	k.InitGenesis(ctx, gs)

	_, found := k.GetWorker(ctx, addr1)
	if !found {
		t.Fatal("worker1 should exist after InitGenesis")
	}
	_, found = k.GetWorker(ctx, addr2)
	if !found {
		t.Fatal("worker2 should exist after InitGenesis")
	}

	exported := k.ExportGenesis(ctx)
	if len(exported.Workers) != 2 {
		t.Fatalf("expected 2 workers in exported genesis, got %d", len(exported.Workers))
	}
}

// -------- gRPC Query --------

func TestQueryServer_Worker(t *testing.T) {
	k, ctx := setupKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	k.SetWorker(ctx, w)

	resp, err := qs.Worker(ctx, &types.QueryWorkerRequest{Address: addr.String()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Worker.Address != addr.String() {
		t.Fatalf("expected %s, got %s", addr.String(), resp.Worker.Address)
	}

	_, err = qs.Worker(ctx, &types.QueryWorkerRequest{Address: "invalid"})
	if err == nil {
		t.Fatal("expected error for invalid address")
	}

	missingAddr := sdk.AccAddress([]byte("nobody______________"))
	_, err = qs.Worker(ctx, &types.QueryWorkerRequest{Address: missingAddr.String()})
	if err == nil {
		t.Fatal("expected error for nonexistent worker")
	}

	_, err = qs.Worker(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestQueryServer_Workers(t *testing.T) {
	k, ctx := setupKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	addr1 := sdk.AccAddress([]byte("worker1_____________"))
	addr2 := sdk.AccAddress([]byte("worker2_____________"))
	k.SetWorker(ctx, makeWorker(addr1.String()))
	k.SetWorker(ctx, makeWorker(addr2.String()))

	resp, err := qs.Workers(ctx, &types.QueryWorkersRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Workers) != 2 {
		t.Fatalf("expected 2 workers, got %d", len(resp.Workers))
	}
}

func TestQueryServer_WorkersByModel(t *testing.T) {
	k, ctx := setupKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.SupportedModels = []string{"modelA"}
	k.SetWorker(ctx, w)
	k.SetModelIndices(ctx, addr, w.SupportedModels)

	resp, err := qs.WorkersByModel(ctx, &types.QueryWorkersByModelRequest{ModelId: "modelA"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Workers) != 1 {
		t.Fatalf("expected 1 worker for modelA, got %d", len(resp.Workers))
	}

	_, err = qs.WorkersByModel(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}

	_, err = qs.WorkersByModel(ctx, &types.QueryWorkersByModelRequest{ModelId: ""})
	if err == nil {
		t.Fatal("expected error for empty model id")
	}
}

func TestQueryServer_Params(t *testing.T) {
	k, ctx := setupKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	resp, err := qs.Params(ctx, &types.QueryParamsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Params.MinStake.IsZero() {
		t.Fatal("params should have non-zero min_stake")
	}
}

// -------- MsgServer --------

func TestMsgServer_RegisterWorker_ColdStart(t *testing.T) {
	k, ctx := setupKeeper(t)
	params := k.GetParams(ctx)
	params.ColdStartFreeBlocks = 200
	k.SetParams(ctx, params)

	ms := keeper.NewMsgServerImpl(k)
	addr := sdk.AccAddress([]byte("worker1_____________"))

	msg := types.NewMsgRegisterWorker(
		addr.String(), "pubkey1", []string{"model1"}, "localhost:8080",
		"H100", 80, 1, "op1", 0,
	)

	_, err := ms.RegisterWorker(ctx, msg)
	if err != nil {
		t.Fatalf("RegisterWorker (cold start) failed: %v", err)
	}

	w, found := k.GetWorker(ctx, addr)
	if !found {
		t.Fatal("worker should be registered")
	}
	if w.Status != types.WorkerStatusActive {
		t.Fatalf("expected ACTIVE status, got %s", w.Status)
	}
	if !w.Stake.IsZero() {
		t.Fatalf("cold-start worker should have zero stake, got %s", w.Stake)
	}
}

func TestMsgServer_RegisterWorker_NormalStake(t *testing.T) {
	k, ctx := setupKeeper(t)
	params := k.GetParams(ctx)
	params.ColdStartFreeBlocks = 0
	k.SetParams(ctx, params)

	ms := keeper.NewMsgServerImpl(k)
	addr := sdk.AccAddress([]byte("worker1_____________"))

	msg := types.NewMsgRegisterWorker(
		addr.String(), "pubkey1", []string{"model1"}, "localhost:8080",
		"H100", 80, 1, "op1", 0,
	)

	_, err := ms.RegisterWorker(ctx, msg)
	if err != nil {
		t.Fatalf("RegisterWorker (normal) failed: %v", err)
	}
}

func TestMsgServer_RegisterWorker_AlreadyRegistered(t *testing.T) {
	k, ctx := setupKeeper(t)
	params := k.GetParams(ctx)
	params.ColdStartFreeBlocks = 200
	k.SetParams(ctx, params)

	ms := keeper.NewMsgServerImpl(k)
	addr := sdk.AccAddress([]byte("worker1_____________"))

	msg := types.NewMsgRegisterWorker(
		addr.String(), "pubkey1", []string{"model1"}, "localhost:8080",
		"H100", 80, 1, "op1", 0,
	)

	_, _ = ms.RegisterWorker(ctx, msg)
	_, err := ms.RegisterWorker(ctx, msg)
	if err == nil {
		t.Fatal("expected error for duplicate registration")
	}
}

func TestMsgServer_RegisterWorker_InvalidAddress(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	msg := types.NewMsgRegisterWorker(
		"invalid", "pubkey1", []string{"model1"}, "localhost:8080",
		"H100", 80, 1, "op1", 0,
	)

	_, err := ms.RegisterWorker(ctx, msg)
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}

func TestMsgServer_ExitWorker(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	k.SetWorker(ctx, w)

	_, err := ms.ExitWorker(ctx, types.NewMsgExitWorker(addr.String()))
	if err != nil {
		t.Fatalf("ExitWorker failed: %v", err)
	}

	w2, _ := k.GetWorker(ctx, addr)
	if w2.Status != types.WorkerStatusExiting {
		t.Fatalf("expected EXITING, got %s", w2.Status)
	}
}

func TestMsgServer_ExitWorker_NotFound(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("nobody______________"))
	_, err := ms.ExitWorker(ctx, types.NewMsgExitWorker(addr.String()))
	if err == nil {
		t.Fatal("expected error for nonexistent worker")
	}
}

func TestMsgServer_ExitWorker_Jailed(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Jailed = true
	w.Status = types.WorkerStatusJailed
	k.SetWorker(ctx, w)

	_, err := ms.ExitWorker(ctx, types.NewMsgExitWorker(addr.String()))
	if err == nil {
		t.Fatal("expected error for jailed worker")
	}
}

func TestMsgServer_ExitWorker_Tombstoned(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Tombstoned = true
	k.SetWorker(ctx, w)

	_, err := ms.ExitWorker(ctx, types.NewMsgExitWorker(addr.String()))
	if err == nil {
		t.Fatal("expected error for tombstoned worker")
	}
}

func TestMsgServer_ExitWorker_NotActive(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Status = types.WorkerStatusExiting
	k.SetWorker(ctx, w)

	_, err := ms.ExitWorker(ctx, types.NewMsgExitWorker(addr.String()))
	if err == nil {
		t.Fatal("expected error for already-exiting worker")
	}
}

func TestMsgServer_ExitWorker_InvalidAddress(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	_, err := ms.ExitWorker(ctx, types.NewMsgExitWorker("invalid"))
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}

func TestMsgServer_UpdateModels(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.SupportedModels = []string{"modelA"}
	k.SetWorker(ctx, w)
	k.SetModelIndices(ctx, addr, w.SupportedModels)

	msg := types.NewMsgUpdateModels(addr.String(), []string{"modelB", "modelC"})
	_, err := ms.UpdateModels(ctx, msg)
	if err != nil {
		t.Fatalf("UpdateModels failed: %v", err)
	}

	w2, _ := k.GetWorker(ctx, addr)
	if len(w2.SupportedModels) != 2 {
		t.Fatalf("expected 2 models, got %d", len(w2.SupportedModels))
	}

	workersA := k.GetWorkersByModel(ctx, "modelA")
	if len(workersA) != 0 {
		t.Fatal("old model index should be removed")
	}
	workersB := k.GetWorkersByModel(ctx, "modelB")
	if len(workersB) != 1 {
		t.Fatal("new model index should exist")
	}
}

func TestMsgServer_UpdateModels_NotFound(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("nobody______________"))
	_, err := ms.UpdateModels(ctx, types.NewMsgUpdateModels(addr.String(), []string{"m1"}))
	if err == nil {
		t.Fatal("expected error for nonexistent worker")
	}
}

func TestMsgServer_UpdateModels_NotActive(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Status = types.WorkerStatusJailed
	w.Jailed = true
	k.SetWorker(ctx, w)

	_, err := ms.UpdateModels(ctx, types.NewMsgUpdateModels(addr.String(), []string{"m1"}))
	if err == nil {
		t.Fatal("expected error for non-active worker")
	}
}

func TestMsgServer_UpdateModels_InvalidAddress(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	_, err := ms.UpdateModels(ctx, types.NewMsgUpdateModels("invalid", []string{"m1"}))
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}

func TestMsgServer_AddStake(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Stake = sdk.NewCoin("ufai", math.NewInt(10000))
	k.SetWorker(ctx, w)

	msg := types.NewMsgStake(addr.String(), sdk.NewCoin("ufai", math.NewInt(5000)))
	_, err := ms.AddStake(ctx, msg)
	if err != nil {
		t.Fatalf("AddStake failed: %v", err)
	}

	w2, _ := k.GetWorker(ctx, addr)
	if !w2.Stake.Amount.Equal(math.NewInt(15000)) {
		t.Fatalf("expected 15000 stake, got %s", w2.Stake.Amount.String())
	}
}

func TestMsgServer_AddStake_NotFound(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("nobody______________"))
	_, err := ms.AddStake(ctx, types.NewMsgStake(addr.String(), sdk.NewCoin("ufai", math.NewInt(100))))
	if err == nil {
		t.Fatal("expected error for nonexistent worker")
	}
}

func TestMsgServer_AddStake_Tombstoned(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Tombstoned = true
	k.SetWorker(ctx, w)

	_, err := ms.AddStake(ctx, types.NewMsgStake(addr.String(), sdk.NewCoin("ufai", math.NewInt(100))))
	if err == nil {
		t.Fatal("expected error for tombstoned worker")
	}
}

func TestMsgServer_AddStake_InvalidAddress(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	_, err := ms.AddStake(ctx, types.NewMsgStake("invalid", sdk.NewCoin("ufai", math.NewInt(100))))
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}

func TestMsgServer_Unjail(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Jailed = true
	w.JailUntil = 50
	w.JailCount = 1
	w.Status = types.WorkerStatusJailed
	k.SetWorker(ctx, w)

	_, err := ms.Unjail(ctx, types.NewMsgUnjail(addr.String()))
	if err != nil {
		t.Fatalf("Unjail failed: %v", err)
	}
}

func TestMsgServer_Unjail_InvalidAddress(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	_, err := ms.Unjail(ctx, types.NewMsgUnjail("invalid"))
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}
