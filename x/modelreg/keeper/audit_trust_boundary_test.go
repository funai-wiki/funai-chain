package keeper_test

// Tests for the trust-boundary fixes called out in the audit checklist
// (docs/mainnet-readiness/同型信任边界问题工程验证清单.md).
//
// §7 — DeclareInstalled must check that the worker actually registered
// the model in MsgRegisterWorker.SupportedModels. Without this, an
// active worker can self-report installation of any model_id and pollute
// InstalledStakeRatio / WorkerCount / OperatorCount, biasing model
// activation and the VRF serving set.

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/modelreg/keeper"
	"github.com/funai-wiki/funai-chain/x/modelreg/types"
)

func TestAudit_Item7_DeclareInstalled_RejectsModelNotInSupportedModels(t *testing.T) {
	k, ctx, wk := setupModelregKeeper(t)
	msgServer := keeper.NewMsgServerImpl(k)

	// Model exists in registry, but the worker did NOT register it in
	// SupportedModels at MsgRegisterWorker time.
	model := types.Model{
		ModelId:        "model-not-supported-by-this-worker",
		Name:           "test",
		Epsilon:        100,
		Status:         types.ModelStatusProposed,
		SuggestedPrice: sdk.NewCoin("ufai", math.NewInt(100)),
	}
	k.SetModel(ctx, model)

	workerAddr := sdk.AccAddress([]byte("audit_i7_active_____"))
	wk.activeAddrs[workerAddr.String()] = true
	// Worker has registered some OTHER model — but not this one.
	wk.addSupportedModel(workerAddr, "some-other-model-id")

	msg := types.NewMsgDeclareInstalled(workerAddr.String(), "model-not-supported-by-this-worker")
	_, err := msgServer.DeclareInstalled(ctx, msg)
	if err == nil {
		t.Fatal("audit §7: DeclareInstalled must reject when model_id is not in worker's SupportedModels")
	}
	// State must remain clean — the worker→model index must NOT be set,
	// and the model's stats must NOT be refreshed in a way that includes
	// this worker.
	if k.HasWorkerInstalledModel(ctx, workerAddr, "model-not-supported-by-this-worker") {
		t.Fatal("audit §7: worker→model installed index must not be written when SupportedModels check fails")
	}
}

func TestAudit_Item7_DeclareInstalled_AcceptsModelInSupportedModels(t *testing.T) {
	// Sanity / regression: when SupportedModels DOES contain the model_id,
	// the existing happy path still works.
	k, ctx, wk := setupModelregKeeper(t)
	msgServer := keeper.NewMsgServerImpl(k)

	model := types.Model{
		ModelId:        "model-supported-by-worker",
		Name:           "test",
		Epsilon:        100,
		Status:         types.ModelStatusProposed,
		SuggestedPrice: sdk.NewCoin("ufai", math.NewInt(100)),
	}
	k.SetModel(ctx, model)

	workerAddr := sdk.AccAddress([]byte("audit_i7_supported__"))
	wk.activeAddrs[workerAddr.String()] = true
	wk.addSupportedModel(workerAddr, "model-supported-by-worker")

	msg := types.NewMsgDeclareInstalled(workerAddr.String(), "model-supported-by-worker")
	if _, err := msgServer.DeclareInstalled(ctx, msg); err != nil {
		t.Fatalf("audit §7: declared model in SupportedModels must succeed: %v", err)
	}
	if !k.HasWorkerInstalledModel(ctx, workerAddr, "model-supported-by-worker") {
		t.Fatal("audit §7: installed index must be written on accepted DeclareInstalled")
	}
}

func TestAudit_Item7_DeclareInstalled_RejectsWorkerWithoutSupportedModelsRegistered(t *testing.T) {
	// Worker is active (somehow) but has no SupportedModels entries at all.
	// In practice this should never happen post-registration, but the
	// SupportedModels check must still fail closed rather than fall
	// through to the previous permissive behavior.
	k, ctx, wk := setupModelregKeeper(t)
	msgServer := keeper.NewMsgServerImpl(k)

	model := types.Model{
		ModelId:        "any-model",
		Name:           "test",
		Epsilon:        100,
		Status:         types.ModelStatusProposed,
		SuggestedPrice: sdk.NewCoin("ufai", math.NewInt(100)),
	}
	k.SetModel(ctx, model)

	workerAddr := sdk.AccAddress([]byte("audit_i7_no_models__"))
	wk.activeAddrs[workerAddr.String()] = true
	// Note: no addSupportedModel — supportedModels[workerAddr] is empty.

	msg := types.NewMsgDeclareInstalled(workerAddr.String(), "any-model")
	if _, err := msgServer.DeclareInstalled(ctx, msg); err == nil {
		t.Fatal("audit §7: DeclareInstalled must fail closed when worker has no SupportedModels entries")
	}
}
