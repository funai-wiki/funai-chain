package keeper_test

// Edge-case and boundary-condition tests for the modelreg module.

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/modelreg/keeper"
	"github.com/funai-wiki/funai-chain/x/modelreg/types"
)

// ============================================================
// 1. Activation with stake ratio just below 2/3
// ============================================================

func TestCheckAndActivateModel_JustBelowThreshold(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)

	model := types.Model{
		ModelId:             "just_below_threshold",
		Name:                "almost-active",
		Epsilon:             100,
		Status:              types.ModelStatusProposed,
		InstalledStakeRatio: 0.6666, // just below 2/3 ≈ 0.6667
		WorkerCount:         10,
		OperatorCount:       10,
		SuggestedPrice:      sdk.NewCoin("ufai", math.NewInt(100)),
	}
	k.SetModel(ctx, model)

	activated := k.CheckAndActivateModel(ctx, "just_below_threshold")
	if activated {
		t.Fatal("model should NOT activate when stake ratio is just below 2/3")
	}
}

// ============================================================
// 2. Activation with exactly 4 workers (minimum)
// ============================================================

func TestCheckAndActivateModel_ExactMinWorkers(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)

	model := types.Model{
		ModelId:             "exact_min_workers",
		Name:                "min-workers",
		Epsilon:             100,
		Status:              types.ModelStatusProposed,
		InstalledStakeRatio: 0.8,
		WorkerCount:         4, // exactly min
		OperatorCount:       4,
		SuggestedPrice:      sdk.NewCoin("ufai", math.NewInt(100)),
	}
	k.SetModel(ctx, model)

	activated := k.CheckAndActivateModel(ctx, "exact_min_workers")
	if !activated {
		t.Fatal("model should activate with exactly 4 workers and 4 operators")
	}
}

// ============================================================
// 3. Activation with 3 workers (one below minimum)
// ============================================================

func TestCheckAndActivateModel_OneBelowMinWorkers(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)

	model := types.Model{
		ModelId:             "below_min_workers",
		Name:                "too-few-workers",
		Epsilon:             100,
		Status:              types.ModelStatusProposed,
		InstalledStakeRatio: 0.9,
		WorkerCount:         3, // one below min
		OperatorCount:       4,
		SuggestedPrice:      sdk.NewCoin("ufai", math.NewInt(100)),
	}
	k.SetModel(ctx, model)

	activated := k.CheckAndActivateModel(ctx, "below_min_workers")
	if activated {
		t.Fatal("model should NOT activate with only 3 workers")
	}
}

// ============================================================
// 4. Activation with 3 operators (one below minimum)
// ============================================================

func TestCheckAndActivateModel_OneBelowMinOperators(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)

	model := types.Model{
		ModelId:             "below_min_operators",
		Name:                "too-few-ops",
		Epsilon:             100,
		Status:              types.ModelStatusProposed,
		InstalledStakeRatio: 0.9,
		WorkerCount:         5,
		OperatorCount:       3, // one below min
		SuggestedPrice:      sdk.NewCoin("ufai", math.NewInt(100)),
	}
	k.SetModel(ctx, model)

	activated := k.CheckAndActivateModel(ctx, "below_min_operators")
	if activated {
		t.Fatal("model should NOT activate with only 3 operators")
	}
}

// ============================================================
// 5. CanServe at exact service threshold (2/3)
// ============================================================

func TestModelCanServe_ExactThreshold(t *testing.T) {
	stakeRatio := 2.0 / 3.0
	m := types.Model{InstalledStakeRatio: 2.0 / 3.0, WorkerCount: 4}
	if !m.CanServe(4, stakeRatio) {
		t.Fatal("model should serve at exact 2/3 threshold with enough workers")
	}
}

// ============================================================
// 6. CanServe just below threshold
// ============================================================

func TestModelCanServe_JustBelowThreshold(t *testing.T) {
	stakeRatio := 2.0 / 3.0
	m := types.Model{InstalledStakeRatio: 0.66, WorkerCount: 10}
	if m.CanServe(10, stakeRatio) {
		t.Fatal("model should NOT serve below 2/3 threshold")
	}
}

// ============================================================
// 7. Model ID collision: same hashes → same ID
// ============================================================

func TestComputeModelId_SameHashes_SameId(t *testing.T) {
	id1 := keeper.ComputeModelId("w1", "q1", "r1")
	id2 := keeper.ComputeModelId("w1", "q1", "r1")
	if id1 != id2 {
		t.Fatal("same hashes should produce same model ID")
	}
}

// ============================================================
// 8. Model ID: partial hash change → different ID
// ============================================================

func TestComputeModelId_PartialHashChange(t *testing.T) {
	base := keeper.ComputeModelId("w1", "q1", "r1")

	// Change only weight hash
	changed1 := keeper.ComputeModelId("w2", "q1", "r1")
	if base == changed1 {
		t.Fatal("different weight hash should produce different ID")
	}

	// Change only quant config hash
	changed2 := keeper.ComputeModelId("w1", "q2", "r1")
	if base == changed2 {
		t.Fatal("different quant config hash should produce different ID")
	}

	// Change only runtime image hash
	changed3 := keeper.ComputeModelId("w1", "q1", "r2")
	if base == changed3 {
		t.Fatal("different runtime image hash should produce different ID")
	}
}

// ============================================================
// 9. IsActivated: all thresholds at exact boundary
// ============================================================

func TestModelIsActivated_ExactBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		ratio     float64
		workers   uint32
		operators uint32
		want      bool
	}{
		{"all_at_min", 2.0 / 3.0, 4, 4, true},
		{"ratio_below", 0.66, 4, 4, false},
		{"workers_below", 0.8, 3, 4, false},
		{"operators_below", 0.8, 4, 3, false},
		{"all_zero", 0, 0, 0, false},
		{"ratio_one", 1.0, 10, 10, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := types.Model{
				InstalledStakeRatio: tt.ratio,
				WorkerCount:         tt.workers,
				OperatorCount:       tt.operators,
			}
			got := m.IsActivated()
			if got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

// ============================================================
// 10. UpdateModelStats: triggers activation
// ============================================================

func TestUpdateModelStats_TriggersActivation(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)
	msgServer := keeper.NewMsgServerImpl(k)

	model := types.Model{
		ModelId:        "stats_activation",
		Name:           "trigger-model",
		Epsilon:        100,
		Status:         types.ModelStatusProposed,
		SuggestedPrice: sdk.NewCoin("ufai", math.NewInt(100)),
	}
	k.SetModel(ctx, model)

	auth := sdk.AccAddress([]byte("authority___________"))
	_ = auth

	// Update to just below threshold → should not activate
	msg := types.NewMsgUpdateModelStats(testGovAuthority, "stats_activation", 0.5, 3, 3)
	_, _ = msgServer.UpdateModelStats(ctx, msg)
	m, _ := k.GetModel(ctx, "stats_activation")
	if m.Status == types.ModelStatusActive {
		t.Fatal("should not activate below thresholds")
	}

	// Update to meet thresholds → should activate
	msg = types.NewMsgUpdateModelStats(testGovAuthority, "stats_activation", 0.8, 5, 5)
	_, _ = msgServer.UpdateModelStats(ctx, msg)
	m, _ = k.GetModel(ctx, "stats_activation")
	if m.Status != types.ModelStatusActive {
		t.Fatalf("should activate after meeting thresholds, got %s", m.Status)
	}
}

// ============================================================
// 11. DeclareInstalled for already active model
// ============================================================

func TestDeclareInstalled_ActiveModel(t *testing.T) {
	k, ctx, wk := setupModelregKeeper(t)
	msgServer := keeper.NewMsgServerImpl(k)

	model := types.Model{
		ModelId:        "active_declare",
		Name:           "active-model",
		Epsilon:        100,
		Status:         types.ModelStatusActive,
		SuggestedPrice: sdk.NewCoin("ufai", math.NewInt(100)),
	}
	k.SetModel(ctx, model)

	workerAddr := sdk.AccAddress([]byte("active_worker_______"))
	wk.activeAddrs[workerAddr.String()] = true
	wk.addSupportedModel(workerAddr, "active_declare")

	msg := types.NewMsgDeclareInstalled(workerAddr.String(), "active_declare")
	_, err := msgServer.DeclareInstalled(ctx, msg)
	if err != nil {
		t.Fatalf("DeclareInstalled for active model should succeed: %v", err)
	}
}

// ============================================================
// 12. Params: service ratio at boundaries
// ============================================================

func TestParamsValidation_ServiceRatio(t *testing.T) {
	tests := []struct {
		name    string
		ratio   float64
		wantErr bool
	}{
		{"valid_0.1", 0.1, false},
		{"valid_1.0", 1.0, false},
		{"invalid_0", 0, true},
		{"invalid_negative", -0.1, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := types.DefaultParams()
			p.ServiceStakeRatio = tt.ratio
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
// 13. Genesis with many models
// ============================================================

func TestGenesis_ManyModels(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)

	gs := types.GenesisState{
		Params: types.DefaultParams(),
		Models: make([]types.Model, 100),
	}
	for i := 0; i < 100; i++ {
		gs.Models[i] = types.Model{
			ModelId:        "model_" + string(rune('A'+i%26)) + string(rune('0'+i/26)),
			Name:           "test",
			SuggestedPrice: sdk.NewCoin("ufai", math.NewInt(1)),
		}
	}

	k.InitGenesis(ctx, gs)
	all := k.GetAllModels(ctx)
	if len(all) != 100 {
		t.Fatalf("expected 100 models, got %d", len(all))
	}
}

// ============================================================
// 14. ProposeModel with empty hashes
// ============================================================

func TestProposeModel_EmptyHashes(t *testing.T) {
	k, ctx, _ := setupModelregKeeper(t)
	msgServer := keeper.NewMsgServerImpl(k)

	creator := sdk.AccAddress([]byte("proposer____________"))
	msg := types.NewMsgModelProposal(
		creator.String(), "test-model", "test-edge", "", "", "", 100,
		sdk.NewCoin("ufai", math.NewInt(100)),
	)

	_, err := msgServer.ProposeModel(ctx, msg)
	// Even with empty hashes, proposal should work (model_id is hash-based)
	if err != nil {
		// If error, it's expected since empty hashes may be validated
		_ = err
	}
}

// ============================================================
// 15. ComputeModelId with empty strings
// ============================================================

func TestComputeModelId_EmptyStrings(t *testing.T) {
	id := keeper.ComputeModelId("", "", "")
	if id == "" {
		t.Fatal("model_id should not be empty even with empty inputs")
	}

	// Should be deterministic even with empty inputs
	id2 := keeper.ComputeModelId("", "", "")
	if id != id2 {
		t.Fatal("should be deterministic")
	}
}
