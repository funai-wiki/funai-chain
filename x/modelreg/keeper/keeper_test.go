package keeper_test

import (
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

	"github.com/funai-wiki/funai-chain/x/modelreg/keeper"
	"github.com/funai-wiki/funai-chain/x/modelreg/types"
)

type mockWorkerKeeper struct {
	activeAddrs map[string]bool
	stakes      map[string]math.Int
	operatorIds map[string]string
	// supportedModels is the per-worker set of model_ids registered via
	// MsgRegisterWorker.SupportedModels. The DeclareInstalled handler
	// (audit §7) now requires the declared model to be in this set.
	supportedModels map[string]map[string]bool
	totalStake      math.Int
}

func (m *mockWorkerKeeper) IsWorkerActive(_ sdk.Context, addr sdk.AccAddress) bool {
	return m.activeAddrs[addr.String()]
}

func (m *mockWorkerKeeper) GetWorkerStake(_ sdk.Context, addr sdk.AccAddress) math.Int {
	if s, ok := m.stakes[addr.String()]; ok {
		return s
	}
	return math.ZeroInt()
}

func (m *mockWorkerKeeper) GetWorkerOperatorId(_ sdk.Context, addr sdk.AccAddress) string {
	if id, ok := m.operatorIds[addr.String()]; ok {
		return id
	}
	return ""
}

func (m *mockWorkerKeeper) GetActiveWorkerStake(_ sdk.Context) math.Int {
	if m.totalStake.IsNil() {
		return math.ZeroInt()
	}
	return m.totalStake
}

func (m *mockWorkerKeeper) WorkerSupportsModel(_ sdk.Context, addr sdk.AccAddress, modelId string) bool {
	models, ok := m.supportedModels[addr.String()]
	if !ok {
		return false
	}
	return models[modelId]
}

// addSupportedModel is a test helper that mirrors the production
// MsgRegisterWorker.SupportedModels storage so DeclareInstalled tests can
// satisfy the new audit-§7 trust-boundary check without booting a real
// worker keeper.
func (m *mockWorkerKeeper) addSupportedModel(addr sdk.AccAddress, modelId string) {
	if m.supportedModels[addr.String()] == nil {
		m.supportedModels[addr.String()] = make(map[string]bool)
	}
	m.supportedModels[addr.String()][modelId] = true
}

func setupModelregKeeper(t *testing.T) (keeper.Keeper, sdk.Context, *mockWorkerKeeper) {
	t.Helper()
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	db := dbm.NewMemDB()
	stateStore := store.NewCommitMultiStore(db, log.NewNopLogger(), storemetrics.NewNoOpMetrics())
	stateStore.MountStoreWithDB(storeKey, storetypes.StoreTypeIAVL, db)
	_ = stateStore.LoadLatestVersion()
	registry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(registry)
	wk := &mockWorkerKeeper{
		activeAddrs:     make(map[string]bool),
		stakes:          make(map[string]math.Int),
		operatorIds:     make(map[string]string),
		supportedModels: make(map[string]map[string]bool),
		totalStake:      math.ZeroInt(),
	}
	// KT Issue 16: tests pass a deterministic authority string so the
	// MsgUpdateModelStats authority-gate path can be exercised without
	// depending on the live gov module address.
	k := keeper.NewKeeper(cdc, storeKey, wk, testGovAuthority, log.NewNopLogger())
	ctx := sdk.NewContext(stateStore, cmtproto.Header{Height: 100}, false, log.NewNopLogger())
	k.SetParams(ctx, types.DefaultParams())
	return k, ctx, wk
}

// testGovAuthority is the bech32-shaped string used as the modelreg keeper
// authority in tests. Production wiring uses authtypes.NewModuleAddress("gov").
const testGovAuthority = "cosmos1modelreg-test-authority"

func TestModelCRUD(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)

	model := types.Model{
		ModelId:             "test_model_id_abc123",
		Name:                "llama-70b",
		Epsilon:             10,
		Status:              types.ModelStatusProposed,
		ProposerAddress:     "cosmos1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq",
		WeightHash:          "whash",
		QuantConfigHash:     "qhash",
		RuntimeImageHash:    "rhash",
		InstalledStakeRatio: 0,
		WorkerCount:         0,
		OperatorCount:       0,
		SuggestedPrice:      sdk.NewCoin("ufai", math.NewInt(100)),
		CreatedAt:           100,
	}

	k.SetModel(ctx, model)

	got, found := k.GetModel(ctx, model.ModelId)
	if !found {
		t.Fatal("model not found after SetModel")
	}
	if got.ModelId != model.ModelId {
		t.Fatalf("expected model_id %s, got %s", model.ModelId, got.ModelId)
	}
	if got.Name != model.Name {
		t.Fatalf("expected name %s, got %s", model.Name, got.Name)
	}
	if got.Epsilon != model.Epsilon {
		t.Fatalf("expected epsilon %d, got %d", model.Epsilon, got.Epsilon)
	}
	if got.Status != types.ModelStatusProposed {
		t.Fatalf("expected PROPOSED status, got %s", got.Status)
	}

	k.DeleteModel(ctx, model.ModelId)
	_, found = k.GetModel(ctx, model.ModelId)
	if found {
		t.Fatal("model should be deleted")
	}
}

func TestGetAllModels(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)

	m1 := types.Model{ModelId: "model_1", Name: "m1", Epsilon: 100, SuggestedPrice: sdk.NewCoin("ufai", math.NewInt(1))}
	m2 := types.Model{ModelId: "model_2", Name: "m2", Epsilon: 200, SuggestedPrice: sdk.NewCoin("ufai", math.NewInt(2))}
	k.SetModel(ctx, m1)
	k.SetModel(ctx, m2)

	all := k.GetAllModels(ctx)
	if len(all) != 2 {
		t.Fatalf("expected 2 models, got %d", len(all))
	}
}

func TestGetModel_NotFound(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)
	_, found := k.GetModel(ctx, "nonexistent")
	if found {
		t.Fatal("should not find nonexistent model")
	}
}

func TestModelProposal(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)
	msgServer := keeper.NewMsgServerImpl(k)

	creator := sdk.AccAddress([]byte("proposer____________"))
	msg := types.NewMsgModelProposal(
		creator.String(),
		"llama-70b-quantized",
		"llama-70b-q4",
		"weight_hash_abc",
		"quant_config_hash_xyz",
		"runtime_image_hash_123",
		10,
		sdk.NewCoin("ufai", math.NewInt(500)),
	)

	resp, err := msgServer.ProposeModel(ctx, msg)
	if err != nil {
		t.Fatalf("ProposeModel failed: %v", err)
	}
	if resp.ModelId == "" {
		t.Fatal("expected non-empty model_id in response")
	}

	expectedId := keeper.ComputeModelId(msg.WeightHash, msg.QuantConfigHash, msg.RuntimeImageHash)
	if resp.ModelId != expectedId {
		t.Fatalf("model_id mismatch: expected %s, got %s", expectedId, resp.ModelId)
	}

	model, found := k.GetModel(ctx, resp.ModelId)
	if !found {
		t.Fatal("proposed model not found in store")
	}
	if model.Status != types.ModelStatusProposed {
		t.Fatalf("expected PROPOSED status, got %s", model.Status)
	}
	if model.Name != msg.Name {
		t.Fatalf("expected name %s, got %s", msg.Name, model.Name)
	}
	if model.ProposerAddress != creator.String() {
		t.Fatalf("expected proposer %s, got %s", creator.String(), model.ProposerAddress)
	}

	// Duplicate proposal should fail
	_, err = msgServer.ProposeModel(ctx, msg)
	if err == nil {
		t.Fatal("duplicate proposal should fail")
	}
}

func TestCheckAndActivateModel(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)

	model := types.Model{
		ModelId:             "activation_test",
		Name:                "test-model",
		Epsilon:             100,
		Status:              types.ModelStatusProposed,
		InstalledStakeRatio: 0.5,
		WorkerCount:         3,
		OperatorCount:       3,
		SuggestedPrice:      sdk.NewCoin("ufai", math.NewInt(100)),
	}
	k.SetModel(ctx, model)

	// Below all thresholds — should not activate
	activated := k.CheckAndActivateModel(ctx, "activation_test")
	if activated {
		t.Fatal("model should not activate below thresholds")
	}
	got, _ := k.GetModel(ctx, "activation_test")
	if got.Status != types.ModelStatusProposed {
		t.Fatalf("expected PROPOSED, got %s", got.Status)
	}

	// Meet stake ratio (>= 2/3) but not worker/operator count
	model.InstalledStakeRatio = 0.7
	k.SetModel(ctx, model)
	activated = k.CheckAndActivateModel(ctx, "activation_test")
	if activated {
		t.Fatal("should not activate without enough workers/operators")
	}

	// Meet worker count but not operator count
	model.WorkerCount = 4
	k.SetModel(ctx, model)
	activated = k.CheckAndActivateModel(ctx, "activation_test")
	if activated {
		t.Fatal("should not activate without enough operators")
	}

	// Meet all thresholds: ratio >= 2/3, workers >= 4, operators >= 4
	model.OperatorCount = 4
	k.SetModel(ctx, model)
	activated = k.CheckAndActivateModel(ctx, "activation_test")
	if !activated {
		t.Fatal("model should activate when all thresholds are met")
	}

	got, _ = k.GetModel(ctx, "activation_test")
	if got.Status != types.ModelStatusActive {
		t.Fatalf("expected ACTIVE status, got %s", got.Status)
	}
	if got.ActivatedAt != ctx.BlockHeight() {
		t.Fatalf("expected ActivatedAt=%d, got %d", ctx.BlockHeight(), got.ActivatedAt)
	}

	// Already active — should return true immediately
	activated = k.CheckAndActivateModel(ctx, "activation_test")
	if !activated {
		t.Fatal("already-active model should return true")
	}
}

func TestCheckAndActivateModel_NotFound(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)
	activated := k.CheckAndActivateModel(ctx, "nonexistent")
	if activated {
		t.Fatal("should return false for nonexistent model")
	}
}

func TestCheckAndActivateModel_ExactThreshold(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)

	model := types.Model{
		ModelId:             "exact_threshold",
		Name:                "threshold-model",
		Epsilon:             100,
		Status:              types.ModelStatusProposed,
		InstalledStakeRatio: 2.0 / 3.0,
		WorkerCount:         4,
		OperatorCount:       4,
		SuggestedPrice:      sdk.NewCoin("ufai", math.NewInt(100)),
	}
	k.SetModel(ctx, model)

	activated := k.CheckAndActivateModel(ctx, "exact_threshold")
	if !activated {
		t.Fatal("model should activate at exact threshold (2/3, 4, 4)")
	}
}

func TestDeclareInstalled(t *testing.T) {
	k, ctx, wk := setupModelregKeeper(t)
	msgServer := keeper.NewMsgServerImpl(k)

	model := types.Model{
		ModelId:        "declare_test",
		Name:           "test-model",
		Epsilon:        100,
		Status:         types.ModelStatusProposed,
		SuggestedPrice: sdk.NewCoin("ufai", math.NewInt(100)),
	}
	k.SetModel(ctx, model)

	workerAddr := sdk.AccAddress([]byte("active_worker_______"))
	wk.activeAddrs[workerAddr.String()] = true
	wk.addSupportedModel(workerAddr, "declare_test")

	msg := types.NewMsgDeclareInstalled(workerAddr.String(), "declare_test")
	_, err := msgServer.DeclareInstalled(ctx, msg)
	if err != nil {
		t.Fatalf("DeclareInstalled should succeed for active worker: %v", err)
	}
}

func TestDeclareInstalled_InactiveWorker(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)
	msgServer := keeper.NewMsgServerImpl(k)

	model := types.Model{
		ModelId:        "declare_inactive_test",
		Name:           "test-model",
		Epsilon:        100,
		Status:         types.ModelStatusProposed,
		SuggestedPrice: sdk.NewCoin("ufai", math.NewInt(100)),
	}
	k.SetModel(ctx, model)

	inactiveAddr := sdk.AccAddress([]byte("inactive_worker_____"))
	msg := types.NewMsgDeclareInstalled(inactiveAddr.String(), "declare_inactive_test")
	_, err := msgServer.DeclareInstalled(ctx, msg)
	if err == nil {
		t.Fatal("DeclareInstalled should fail for inactive worker")
	}
}

func TestDeclareInstalled_ModelNotFound(t *testing.T) {
	k, ctx, wk := setupModelregKeeper(t)
	msgServer := keeper.NewMsgServerImpl(k)

	workerAddr := sdk.AccAddress([]byte("active_worker_______"))
	wk.activeAddrs[workerAddr.String()] = true

	msg := types.NewMsgDeclareInstalled(workerAddr.String(), "nonexistent_model")
	_, err := msgServer.DeclareInstalled(ctx, msg)
	if err == nil {
		t.Fatal("DeclareInstalled should fail for nonexistent model")
	}
}

func TestDefaultParams(t *testing.T) {
	params := types.DefaultParams()
	if err := params.Validate(); err != nil {
		t.Fatalf("default params should be valid: %v", err)
	}
	if params.ActivationStakeRatio < 0.66 || params.ActivationStakeRatio > 0.67 {
		t.Fatalf("expected activation_stake_ratio ~2/3, got %f", params.ActivationStakeRatio)
	}
	if params.ServiceStakeRatio < 0.66 || params.ServiceStakeRatio > 0.67 {
		t.Fatalf("expected service_stake_ratio ~2/3, got %f", params.ServiceStakeRatio)
	}
	if params.MinEligibleWorkers != 4 {
		t.Fatalf("expected min_eligible_workers 4, got %d", params.MinEligibleWorkers)
	}
	if params.MinUniqueOperators != 4 {
		t.Fatalf("expected min_unique_operators 4, got %d", params.MinUniqueOperators)
	}
}

func TestParamsValidation_Invalid(t *testing.T) {
	tests := []struct {
		name   string
		params types.Params
	}{
		{"zero activation ratio", types.Params{ActivationStakeRatio: 0, ServiceStakeRatio: 0.1, MinEligibleWorkers: 4, MinUniqueOperators: 4}},
		{"negative activation ratio", types.Params{ActivationStakeRatio: -0.1, ServiceStakeRatio: 0.1, MinEligibleWorkers: 4, MinUniqueOperators: 4}},
		{"activation ratio > 1", types.Params{ActivationStakeRatio: 1.1, ServiceStakeRatio: 0.1, MinEligibleWorkers: 4, MinUniqueOperators: 4}},
		{"zero service ratio", types.Params{ActivationStakeRatio: 0.67, ServiceStakeRatio: 0, MinEligibleWorkers: 4, MinUniqueOperators: 4}},
		{"zero workers", types.Params{ActivationStakeRatio: 0.67, ServiceStakeRatio: 0.1, MinEligibleWorkers: 0, MinUniqueOperators: 4}},
		{"zero operators", types.Params{ActivationStakeRatio: 0.67, ServiceStakeRatio: 0.1, MinEligibleWorkers: 4, MinUniqueOperators: 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.params.Validate(); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestComputeModelId_Deterministic(t *testing.T) {
	id1 := keeper.ComputeModelId("w", "q", "r")
	id2 := keeper.ComputeModelId("w", "q", "r")
	if id1 != id2 {
		t.Fatal("ComputeModelId should be deterministic")
	}
	if id1 == "" {
		t.Fatal("ComputeModelId should not be empty")
	}
}

func TestComputeModelId_DifferentInputs(t *testing.T) {
	id1 := keeper.ComputeModelId("w1", "q1", "r1")
	id2 := keeper.ComputeModelId("w2", "q2", "r2")
	if id1 == id2 {
		t.Fatal("different inputs should produce different model_ids")
	}
}

func TestModelIsActivated(t *testing.T) {
	m := types.Model{
		InstalledStakeRatio: 2.0 / 3.0,
		WorkerCount:         4,
		OperatorCount:       4,
	}
	if !m.IsActivated() {
		t.Fatal("model should be activated at exact threshold")
	}

	m.InstalledStakeRatio = 0.5
	if m.IsActivated() {
		t.Fatal("model should not be activated below stake ratio threshold")
	}
}

func TestModelCanServe(t *testing.T) {
	// Audit KT §6: CanServe requires both stake ratio >= serviceStakeRatio AND worker count >= minWorkerCount
	stakeRatio := 2.0 / 3.0
	m := types.Model{InstalledStakeRatio: 0.7, WorkerCount: 10}
	if !m.CanServe(10, stakeRatio) {
		t.Fatal("model should be able to serve at ratio=0.7 with 10 workers")
	}

	m.InstalledStakeRatio = 0.6
	if m.CanServe(10, stakeRatio) {
		t.Fatal("model should not serve below 2/3 stake threshold")
	}

	m.InstalledStakeRatio = 0.8
	m.WorkerCount = 9
	if m.CanServe(10, stakeRatio) {
		t.Fatal("model should not serve with less than 10 workers")
	}
}

func TestSetAndGetParams(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)

	custom := types.Params{
		ActivationStakeRatio: 0.8,
		ServiceStakeRatio:    0.2,
		MinEligibleWorkers:   5,
		MinUniqueOperators:   6,
	}
	k.SetParams(ctx, custom)

	got := k.GetParams(ctx)
	if got.ActivationStakeRatio != 0.8 {
		t.Fatalf("expected 0.8, got %f", got.ActivationStakeRatio)
	}
	if got.MinEligibleWorkers != 5 {
		t.Fatalf("expected 5, got %d", got.MinEligibleWorkers)
	}
}

func TestGenesisValidation(t *testing.T) {
	gs := types.DefaultGenesis()
	if err := gs.Validate(); err != nil {
		t.Fatalf("default genesis should be valid: %v", err)
	}

	// Duplicate model IDs should fail
	gs.Models = []types.Model{
		{ModelId: "dup"},
		{ModelId: "dup"},
	}
	if err := gs.Validate(); err == nil {
		t.Fatal("genesis with duplicate model IDs should fail validation")
	}

	// Empty model ID should fail
	gs.Models = []types.Model{
		{ModelId: ""},
	}
	if err := gs.Validate(); err == nil {
		t.Fatal("genesis with empty model ID should fail validation")
	}
}

// -------- Logger --------

func TestLogger(t *testing.T) {
	k, _, _ := setupModelregKeeper(t)
	l := k.Logger()
	if l == nil {
		t.Fatal("logger should not be nil")
	}
}

// -------- Genesis Init / Export --------

func TestInitGenesis_ExportGenesis(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)

	gs := types.GenesisState{
		Params: types.DefaultParams(),
		Models: []types.Model{
			{ModelId: "genesis_model_1", Name: "m1", Epsilon: 100, SuggestedPrice: sdk.NewCoin("ufai", math.NewInt(100))},
			{ModelId: "genesis_model_2", Name: "m2", Epsilon: 200, SuggestedPrice: sdk.NewCoin("ufai", math.NewInt(200))},
		},
	}

	k.InitGenesis(ctx, gs)

	m1, found := k.GetModel(ctx, "genesis_model_1")
	if !found {
		t.Fatal("model1 should exist after InitGenesis")
	}
	if m1.Name != "m1" {
		t.Fatalf("expected name m1, got %s", m1.Name)
	}

	m2, found := k.GetModel(ctx, "genesis_model_2")
	if !found {
		t.Fatal("model2 should exist after InitGenesis")
	}
	if m2.Name != "m2" {
		t.Fatalf("expected name m2, got %s", m2.Name)
	}

	exported := k.ExportGenesis(ctx)
	if len(exported.Models) != 2 {
		t.Fatalf("expected 2 models in exported genesis, got %d", len(exported.Models))
	}
}

func TestInitGenesis_EmptyModelId_Panics(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for empty model_id in genesis")
		}
	}()

	gs := types.GenesisState{
		Params: types.DefaultParams(),
		Models: []types.Model{
			{ModelId: "", Name: "bad"},
		},
	}
	k.InitGenesis(ctx, gs)
}

// -------- gRPC Query --------

func TestQueryServer_Model(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	model := types.Model{
		ModelId:        "query_test_model",
		Name:           "test",
		Epsilon:        100,
		SuggestedPrice: sdk.NewCoin("ufai", math.NewInt(100)),
	}
	k.SetModel(ctx, model)

	resp, err := qs.Model(ctx, &types.QueryModelRequest{ModelId: "query_test_model"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Model.Name != "test" {
		t.Fatalf("expected name test, got %s", resp.Model.Name)
	}

	_, err = qs.Model(ctx, &types.QueryModelRequest{ModelId: "nonexistent"})
	if err == nil {
		t.Fatal("expected error for nonexistent model")
	}

	_, err = qs.Model(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}

	_, err = qs.Model(ctx, &types.QueryModelRequest{ModelId: ""})
	if err == nil {
		t.Fatal("expected error for empty model id")
	}
}

func TestQueryServer_Models(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	k.SetModel(ctx, types.Model{ModelId: "m1", Name: "model1", SuggestedPrice: sdk.NewCoin("ufai", math.NewInt(1))})
	k.SetModel(ctx, types.Model{ModelId: "m2", Name: "model2", SuggestedPrice: sdk.NewCoin("ufai", math.NewInt(2))})

	resp, err := qs.Models(ctx, &types.QueryModelsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(resp.Models))
	}
}

func TestQueryServer_Params(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	resp, err := qs.Params(ctx, &types.QueryParamsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Params.MinEligibleWorkers != 4 {
		t.Fatalf("expected min workers 4, got %d", resp.Params.MinEligibleWorkers)
	}
}

// -------- UpdateModelStats --------

func TestUpdateModelStats(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)
	msgServer := keeper.NewMsgServerImpl(k)

	model := types.Model{
		ModelId:             "stats_test",
		Name:                "test-model",
		Epsilon:             100,
		Status:              types.ModelStatusProposed,
		InstalledStakeRatio: 0.0,
		WorkerCount:         0,
		OperatorCount:       0,
		SuggestedPrice:      sdk.NewCoin("ufai", math.NewInt(100)),
	}
	k.SetModel(ctx, model)

	auth := sdk.AccAddress([]byte("authority___________"))
	_ = auth
	msg := types.NewMsgUpdateModelStats(testGovAuthority, "stats_test", 0.8, 5, 5)

	_, err := msgServer.UpdateModelStats(ctx, msg)
	if err != nil {
		t.Fatalf("UpdateModelStats failed: %v", err)
	}

	m, _ := k.GetModel(ctx, "stats_test")
	if m.InstalledStakeRatio != 0.8 {
		t.Fatalf("expected ratio 0.8, got %f", m.InstalledStakeRatio)
	}
	if m.WorkerCount != 5 {
		t.Fatalf("expected 5 workers, got %d", m.WorkerCount)
	}
	if m.Status != types.ModelStatusActive {
		t.Fatalf("model should be activated after meeting thresholds, got %s", m.Status)
	}
}

func TestUpdateModelStats_NotFound(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)
	msgServer := keeper.NewMsgServerImpl(k)

	auth := sdk.AccAddress([]byte("authority___________"))
	_ = auth
	msg := types.NewMsgUpdateModelStats(testGovAuthority, "nonexistent", 0.5, 2, 2)

	_, err := msgServer.UpdateModelStats(ctx, msg)
	if err == nil {
		t.Fatal("expected error for nonexistent model")
	}
}

// -------- CheckServiceStatus --------

func TestCheckServiceStatus_PauseAndResume(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)

	model := types.Model{
		ModelId:             "service_test",
		Name:                "test-model",
		InstalledStakeRatio: 0.05,
		SuggestedPrice:      sdk.NewCoin("ufai", math.NewInt(100)),
	}

	k.CheckServiceStatus(ctx, model, true)

	model.InstalledStakeRatio = 0.15
	k.CheckServiceStatus(ctx, model, false)

	model.InstalledStakeRatio = 0.15
	k.CheckServiceStatus(ctx, model, true)

	model.InstalledStakeRatio = 0.05
	k.CheckServiceStatus(ctx, model, false)
}

// -------- ProposeModel edge cases --------

func TestProposeModel_InvalidAddress(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)
	msgServer := keeper.NewMsgServerImpl(k)

	msg := types.NewMsgModelProposal(
		"invalid", "model", "test-alias", "wh", "qh", "rh", 100,
		sdk.NewCoin("ufai", math.NewInt(100)),
	)

	_, err := msgServer.ProposeModel(ctx, msg)
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}

// -------- DeclareInstalled edge cases --------

func TestDeclareInstalled_InvalidAddress(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)
	msgServer := keeper.NewMsgServerImpl(k)

	model := types.Model{ModelId: "installed_test", Name: "test", SuggestedPrice: sdk.NewCoin("ufai", math.NewInt(1))}
	k.SetModel(ctx, model)

	msg := types.NewMsgDeclareInstalled("invalid", "installed_test")
	_, err := msgServer.DeclareInstalled(ctx, msg)
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}
