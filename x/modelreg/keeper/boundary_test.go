package keeper_test

// Boundary and edge-case tests for the modelreg module — supplementary to existing tests.

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/modelreg/keeper"
	"github.com/funai-wiki/funai-chain/x/modelreg/types"
)

// ============================================================
// B1. PROPOSED→ACTIVE→SERVICE_STOPPED lifecycle via stats updates
// ============================================================

func TestModelLifecycle_ProposedToActiveToServiceStopped(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)
	msgServer := keeper.NewMsgServerImpl(k)
	auth := sdk.AccAddress([]byte("authority___________"))
	_ = auth

	model := types.Model{
		ModelId:        "lifecycle_model",
		Name:           "lifecycle-test",
		Epsilon:        100,
		Status:         types.ModelStatusProposed,
		SuggestedPrice: sdk.NewCoin("ufai", math.NewInt(100)),
	}
	k.SetModel(ctx, model)

	// Step 1: Activate via stats meeting thresholds
	msg := types.NewMsgUpdateModelStats(testGovAuthority, "lifecycle_model", 0.8, 5, 5)
	_, err := msgServer.UpdateModelStats(ctx, msg)
	if err != nil {
		t.Fatalf("UpdateModelStats failed: %v", err)
	}
	m, _ := k.GetModel(ctx, "lifecycle_model")
	if m.Status != types.ModelStatusActive {
		t.Fatalf("expected ACTIVE after meeting thresholds, got %s", m.Status)
	}

	// Step 2: Drop ratio below service threshold → check service status
	m.InstalledStakeRatio = 0.05
	k.SetModel(ctx, m)
	k.CheckServiceStatus(ctx, m, false)
	// Service status is event-based, model status doesn't change in store from CheckServiceStatus
	// but the event should fire — we just verify no panic

	// Step 3: Stats drop below activation → model stays active (already activated)
	msg = types.NewMsgUpdateModelStats(testGovAuthority, "lifecycle_model", 0.3, 2, 2)
	_, _ = msgServer.UpdateModelStats(ctx, msg)
	m, _ = k.GetModel(ctx, "lifecycle_model")
	// Already-active model stays active per CheckAndActivateModel logic
	if m.Status != types.ModelStatusActive {
		t.Fatalf("already-active model should stay active, got %s", m.Status)
	}
}

// ============================================================
// B2. DeclareInstalled for non-existent model
// ============================================================

func TestDeclareInstalled_NonExistentModel(t *testing.T) {
	k, ctx, wk := setupModelregKeeper(t)
	msgServer := keeper.NewMsgServerImpl(k)

	workerAddr := sdk.AccAddress([]byte("active_worker_______"))
	wk.activeAddrs[workerAddr.String()] = true

	msg := types.NewMsgDeclareInstalled(workerAddr.String(), "nonexistent_model_xyz")
	_, err := msgServer.DeclareInstalled(ctx, msg)
	if err == nil {
		t.Fatal("DeclareInstalled for nonexistent model should fail")
	}
}

// ============================================================
// B3. Multiple DeclareInstalled by same worker (idempotent)
// ============================================================

func TestDeclareInstalled_SameWorkerTwice(t *testing.T) {
	k, ctx, wk := setupModelregKeeper(t)
	msgServer := keeper.NewMsgServerImpl(k)

	model := types.Model{
		ModelId:        "idempotent_declare",
		Name:           "test",
		Epsilon:        100,
		Status:         types.ModelStatusProposed,
		SuggestedPrice: sdk.NewCoin("ufai", math.NewInt(100)),
	}
	k.SetModel(ctx, model)

	workerAddr := sdk.AccAddress([]byte("active_worker_______"))
	wk.activeAddrs[workerAddr.String()] = true
	wk.addSupportedModel(workerAddr, "idempotent_declare")

	msg := types.NewMsgDeclareInstalled(workerAddr.String(), "idempotent_declare")
	_, err1 := msgServer.DeclareInstalled(ctx, msg)
	if err1 != nil {
		t.Fatalf("first DeclareInstalled should succeed: %v", err1)
	}

	// Second call should not error (idempotent)
	_, err2 := msgServer.DeclareInstalled(ctx, msg)
	if err2 != nil {
		t.Fatalf("second DeclareInstalled should be idempotent: %v", err2)
	}
}

// ============================================================
// B4. CanServe with zero worker count
// ============================================================

func TestModelCanServe_ZeroWorkers(t *testing.T) {
	m := types.Model{InstalledStakeRatio: 0.7, WorkerCount: 0}
	if m.CanServe(1, 2.0/3.0) {
		t.Fatal("model with 0 workers should not serve")
	}
}

// ============================================================
// B5. CanServe with minWorkerCount=0
// ============================================================

func TestModelCanServe_ZeroMinWorkers(t *testing.T) {
	stakeRatio := 2.0 / 3.0
	// WorkerCount=0 >= minWorkerCount=0, ratio=0.7 >= 2/3 → should serve
	m := types.Model{InstalledStakeRatio: 0.7, WorkerCount: 0}
	if !m.CanServe(0, stakeRatio) {
		t.Fatal("model with ratio>=2/3 and workerCount>=minWorkerCount(0) should serve")
	}

	// But with ratio below threshold → should not serve
	m2 := types.Model{InstalledStakeRatio: 0.5, WorkerCount: 0}
	if m2.CanServe(0, stakeRatio) {
		t.Fatal("model with ratio<2/3 should not serve even with minWorkerCount=0")
	}
}

// ============================================================
// B6. IsActivated with float64 precision edge case
// ============================================================

func TestModelIsActivated_FloatPrecision(t *testing.T) {
	// 2.0/3.0 in float64 = 0.6666666666666666
	m := types.Model{
		InstalledStakeRatio: 0.6666666666666666,
		WorkerCount:         4,
		OperatorCount:       4,
	}
	if !m.IsActivated() {
		t.Fatal("model at 2/3 float64 precision should be activated")
	}

	// Slightly below
	m.InstalledStakeRatio = 0.6666666666666665
	if m.IsActivated() {
		t.Fatal("model slightly below 2/3 should NOT be activated")
	}
}

// ============================================================
// B7. Genesis round-trip preserves model fields
// ============================================================

func TestGenesis_RoundTrip_PreservesFields(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)

	gs := types.GenesisState{
		Params: types.DefaultParams(),
		Models: []types.Model{
			{
				ModelId:             "rt_model",
				Name:                "round-trip",
				Epsilon:             50,
				Status:              types.ModelStatusActive,
				ProposerAddress:     "cosmos1proposer",
				WeightHash:          "whash",
				QuantConfigHash:     "qhash",
				RuntimeImageHash:    "rhash",
				InstalledStakeRatio: 0.9,
				WorkerCount:         10,
				OperatorCount:       8,
				SuggestedPrice:      sdk.NewCoin("ufai", math.NewInt(500)),
				ActivatedAt:         42,
				CreatedAt:           10,
			},
		},
	}

	k.InitGenesis(ctx, gs)
	exported := k.ExportGenesis(ctx)

	if len(exported.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(exported.Models))
	}
	m := exported.Models[0]
	if m.ModelId != "rt_model" || m.Name != "round-trip" {
		t.Fatal("model ID or name mismatch")
	}
	if m.Epsilon != 50 {
		t.Fatalf("epsilon mismatch: %d", m.Epsilon)
	}
	if m.WorkerCount != 10 || m.OperatorCount != 8 {
		t.Fatal("worker/operator count mismatch")
	}
}

// ============================================================
// B8. Params validation: service ratio > activation ratio
// ============================================================

func TestParamsValidation_ServiceRatioAboveActivation(t *testing.T) {
	p := types.DefaultParams()
	p.ServiceStakeRatio = 0.9
	p.ActivationStakeRatio = 0.5
	// This is technically valid per the current Validate() logic
	err := p.Validate()
	if err != nil {
		t.Fatalf("service > activation should be valid per current logic: %v", err)
	}
}

// ============================================================
// B9. Params validation: activation ratio exactly 1.0
// ============================================================

func TestParamsValidation_ActivationRatioOne(t *testing.T) {
	p := types.DefaultParams()
	p.ActivationStakeRatio = 1.0
	err := p.Validate()
	if err != nil {
		t.Fatalf("activation ratio of 1.0 should be valid: %v", err)
	}
}

// ============================================================
// B10. gRPC Models query: empty store
// ============================================================

func TestQueryServer_Models_EmptyStore(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	resp, err := qs.Models(ctx, &types.QueryModelsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Models) != 0 {
		t.Fatalf("expected 0 models from empty store, got %d", len(resp.Models))
	}
}

// ============================================================
// B11. ComputeModelId: order sensitivity
// ============================================================

func TestComputeModelId_OrderSensitivity(t *testing.T) {
	id1 := keeper.ComputeModelId("a", "b", "c")
	id2 := keeper.ComputeModelId("b", "a", "c")
	id3 := keeper.ComputeModelId("c", "b", "a")

	if id1 == id2 || id2 == id3 || id1 == id3 {
		t.Fatal("different ordering of hashes should produce different model IDs")
	}
}

// ============================================================
// B12. Model status string representations
// ============================================================

func TestModelStatus_String(t *testing.T) {
	tests := []struct {
		s    types.ModelStatus
		want string
	}{
		{types.ModelStatusProposed, "MODEL_PROPOSED"},
		{types.ModelStatusActive, "MODEL_ACTIVE"},
		{types.ModelStatus(99), "UNKNOWN"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Fatalf("ModelStatus(%d).String() = %s, want %s", tt.s, got, tt.want)
		}
	}
}
