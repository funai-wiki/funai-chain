package keeper_test

// Boundary and edge-case tests for the worker module — supplementary to existing tests.

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/worker/keeper"
	"github.com/funai-wiki/funai-chain/x/worker/types"
)

// ============================================================
// B1. Consecutive jails without unjailing in between
// ============================================================

func TestBoundary_JailWorker_ConsecutiveWithoutUnjail(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	k.SetWorker(ctx, w)

	// 1st jail
	k.JailWorker(ctx, addr, 120)
	got, _ := k.GetWorker(ctx, addr)
	if got.JailCount != 1 {
		t.Fatalf("expected jail_count=1, got %d", got.JailCount)
	}

	// 2nd jail without unjailing (still jailed)
	k.JailWorker(ctx, addr, 720)
	got, _ = k.GetWorker(ctx, addr)
	if got.JailCount != 2 {
		t.Fatalf("expected jail_count=2, got %d", got.JailCount)
	}

	// 3rd jail → tombstone
	k.JailWorker(ctx, addr, 120)
	got, _ = k.GetWorker(ctx, addr)
	if !got.Tombstoned {
		t.Fatal("3rd consecutive jail should tombstone")
	}
}

// ============================================================
// B2. Unjail non-jailed worker → error
// ============================================================

func TestBoundary_UnjailWorker_NotJailed(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	k.SetWorker(ctx, w)

	err := k.UnjailWorker(ctx, addr)
	if err == nil {
		t.Fatal("unjailing a non-jailed worker should return error")
	}
}

// ============================================================
// B3. Unjail tombstoned worker → error
// ============================================================

func TestBoundary_UnjailWorker_Tombstoned(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Jailed = true
	w.Tombstoned = true
	w.JailUntil = 50
	w.Status = types.WorkerStatusJailed
	k.SetWorker(ctx, w)

	err := k.UnjailWorker(ctx, addr)
	if err == nil {
		t.Fatal("unjailing a tombstoned worker should return error")
	}
}

// ============================================================
// B4. Unjail non-existent worker → error
// ============================================================

func TestBoundary_UnjailWorker_NotFound(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("nonexistent_________"))

	err := k.UnjailWorker(ctx, addr)
	if err == nil {
		t.Fatal("unjailing non-existent worker should return error")
	}
}

// ============================================================
// B5. SlashWorker on non-existent worker → no panic
// ============================================================

func TestBoundary_SlashWorker_NonExistent(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("nonexistent_________"))
	k.SlashWorker(ctx, addr, 5)
}

// ============================================================
// B6. SlashWorker with 0% → no change
// ============================================================

func TestBoundary_SlashWorker_ZeroPercent(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Stake = sdk.NewCoin("ufai", math.NewInt(100000))
	k.SetWorker(ctx, w)

	k.SlashWorker(ctx, addr, 0)

	got, _ := k.GetWorker(ctx, addr)
	if !got.Stake.Amount.Equal(math.NewInt(100000)) {
		t.Fatalf("0%% slash should not change stake, got %s", got.Stake.Amount)
	}
}

// ============================================================
// B7. SlashWorker zero stake → no panic
// ============================================================

func TestBoundary_SlashWorker_ZeroStake(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Stake = sdk.NewCoin("ufai", math.ZeroInt())
	k.SetWorker(ctx, w)

	k.SlashWorker(ctx, addr, 5)

	got, _ := k.GetWorker(ctx, addr)
	if !got.Stake.IsZero() {
		t.Fatalf("slash on zero stake should remain zero, got %s", got.Stake)
	}
}

// ============================================================
// B8. Genesis round-trip with workers
// ============================================================

func TestBoundary_Genesis_RoundTrip(t *testing.T) {
	k, ctx := setupKeeper(t)

	addr1 := sdk.AccAddress([]byte("worker1_____________"))
	addr2 := sdk.AccAddress([]byte("worker2_____________"))
	w1 := makeWorker(addr1.String())
	w1.JailCount = 2
	w1.SuccessStreak = 10
	w2 := makeWorker(addr2.String())
	w2.Status = types.WorkerStatusExiting
	w2.ExitRequestedAt = 50

	k.SetWorker(ctx, w1)
	k.SetModelIndices(ctx, addr1, w1.SupportedModels)
	k.SetWorker(ctx, w2)
	k.SetModelIndices(ctx, addr2, w2.SupportedModels)

	exported := k.ExportGenesis(ctx)
	if len(exported.Workers) != 2 {
		t.Fatalf("expected 2 workers in export, got %d", len(exported.Workers))
	}

	k2, ctx2 := setupKeeper(t)
	k2.InitGenesis(ctx2, *exported)

	all := k2.GetAllWorkers(ctx2)
	if len(all) != 2 {
		t.Fatalf("expected 2 workers after import, got %d", len(all))
	}
}

// ============================================================
// B9. Genesis validation: duplicate addresses
// ============================================================

func TestBoundary_Genesis_DuplicateWorkers(t *testing.T) {
	gs := types.GenesisState{
		Params: types.DefaultParams(),
		Workers: []types.Worker{
			{Address: "cosmos1aaaa"},
			{Address: "cosmos1aaaa"},
		},
	}
	if err := gs.Validate(); err == nil {
		t.Fatal("genesis with duplicate worker addresses should fail validation")
	}
}

// ============================================================
// B10. Genesis default is valid
// ============================================================

func TestBoundary_Genesis_DefaultValid(t *testing.T) {
	gs := types.DefaultGenesis()
	if err := gs.Validate(); err != nil {
		t.Fatalf("default genesis should be valid: %v", err)
	}
}

// ============================================================
// B11. gRPC Worker query: not found
// ============================================================

func TestBoundary_QueryServer_Worker_NotFound(t *testing.T) {
	k, ctx := setupKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	addr := sdk.AccAddress([]byte("nonexistent_________"))
	_, err := qs.Worker(ctx, &types.QueryWorkerRequest{Address: addr.String()})
	if err == nil {
		t.Fatal("expected error for nonexistent worker query")
	}
}

// ============================================================
// B12. gRPC Worker query: nil request
// ============================================================

func TestBoundary_QueryServer_Worker_NilRequest(t *testing.T) {
	k, ctx := setupKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	_, err := qs.Worker(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

// ============================================================
// B13. gRPC WorkersByModel query: nil/empty model_id
// ============================================================

func TestBoundary_QueryServer_WorkersByModel_EmptyModelId(t *testing.T) {
	k, ctx := setupKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	_, err := qs.WorkersByModel(ctx, &types.QueryWorkersByModelRequest{ModelId: ""})
	if err == nil {
		t.Fatal("expected error for empty model_id")
	}

	_, err = qs.WorkersByModel(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

// ============================================================
// B14. gRPC Params query
// ============================================================

func TestBoundary_QueryServer_Params(t *testing.T) {
	k, ctx := setupKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	resp, err := qs.Params(ctx, &types.QueryParamsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Params.ExitWaitPeriod <= 0 {
		t.Fatal("exit wait period should be positive")
	}
}

// ============================================================
// B15. MsgServer ExitWorker: jailed worker cannot exit
// ============================================================

func TestBoundary_MsgServer_ExitWorker_Jailed(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Jailed = true
	w.Status = types.WorkerStatusJailed
	k.SetWorker(ctx, w)

	_, err := ms.ExitWorker(ctx, types.NewMsgExitWorker(addr.String()))
	if err == nil {
		t.Fatal("jailed worker should not be able to exit")
	}
}

// ============================================================
// B16. MsgServer ExitWorker: tombstoned worker cannot exit
// ============================================================

func TestBoundary_MsgServer_ExitWorker_Tombstoned(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Tombstoned = true
	w.Jailed = true
	w.Status = types.WorkerStatusJailed
	k.SetWorker(ctx, w)

	_, err := ms.ExitWorker(ctx, types.NewMsgExitWorker(addr.String()))
	if err == nil {
		t.Fatal("tombstoned worker should not be able to exit")
	}
}

// ============================================================
// B17. MsgServer ExitWorker: already exiting worker cannot exit again
// ============================================================

func TestBoundary_MsgServer_ExitWorker_AlreadyExiting(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Status = types.WorkerStatusExiting
	w.ExitRequestedAt = 50
	k.SetWorker(ctx, w)

	_, err := ms.ExitWorker(ctx, types.NewMsgExitWorker(addr.String()))
	if err == nil {
		t.Fatal("already exiting worker should not be able to exit again")
	}
}

// ============================================================
// B18. MsgServer ExitWorker: non-existent worker
// ============================================================

func TestBoundary_MsgServer_ExitWorker_NotFound(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("nonexistent_________"))
	_, err := ms.ExitWorker(ctx, types.NewMsgExitWorker(addr.String()))
	if err == nil {
		t.Fatal("exit of non-existent worker should fail")
	}
}

// ============================================================
// B19. MsgServer RegisterWorker: duplicate registration
// ============================================================

func TestBoundary_MsgServer_RegisterWorker_Duplicate(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	k.SetWorker(ctx, w)

	msg := types.NewMsgRegisterWorker(
		addr.String(), "pk2", []string{"m1"}, "localhost:9090",
		"A100", 40, 1, "op2", 0,
	)
	_, err := ms.RegisterWorker(ctx, msg)
	if err == nil {
		t.Fatal("duplicate registration should fail")
	}
}

// ============================================================
// B20. MsgServer UpdateModels: non-active worker
// ============================================================

func TestBoundary_MsgServer_UpdateModels_JailedWorker(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Jailed = true
	w.Status = types.WorkerStatusJailed
	k.SetWorker(ctx, w)

	msg := types.NewMsgUpdateModels(addr.String(), []string{"new_model"})
	_, err := ms.UpdateModels(ctx, msg)
	if err == nil {
		t.Fatal("jailed worker should not be able to update models")
	}
}

// ============================================================
// B21. MsgServer AddStake: tombstoned worker
// ============================================================

func TestBoundary_MsgServer_AddStake_Tombstoned(t *testing.T) {
	k, ctx := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Tombstoned = true
	k.SetWorker(ctx, w)

	msg := types.NewMsgStake(addr.String(), sdk.NewCoin("ufai", math.NewInt(5000)))
	_, err := ms.AddStake(ctx, msg)
	if err == nil {
		t.Fatal("tombstoned worker should not be able to add stake")
	}
}

// ============================================================
// B22. Worker IsActive / IsJailed / CanUnjail boundary checks
// ============================================================

func TestBoundary_WorkerStatus_Methods(t *testing.T) {
	tests := []struct {
		name      string
		w         types.Worker
		active    bool
		jailed    bool
		canUnjail bool
		height    int64
	}{
		{
			"active_normal",
			types.Worker{Status: types.WorkerStatusActive},
			true, false, false, 100,
		},
		{
			"jailed_not_tombstoned_before_unjail",
			types.Worker{Status: types.WorkerStatusJailed, Jailed: true, JailUntil: 200},
			false, true, false, 100,
		},
		{
			"jailed_at_exact_unjail_height",
			types.Worker{Status: types.WorkerStatusJailed, Jailed: true, JailUntil: 100},
			false, true, true, 100,
		},
		{
			"tombstoned",
			types.Worker{Status: types.WorkerStatusJailed, Jailed: true, Tombstoned: true, JailUntil: 0},
			false, true, false, 100,
		},
		{
			"exiting",
			types.Worker{Status: types.WorkerStatusExiting},
			false, false, false, 100,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.w.IsActive(); got != tt.active {
				t.Fatalf("IsActive: expected %v, got %v", tt.active, got)
			}
			if got := tt.w.IsJailed(); got != tt.jailed {
				t.Fatalf("IsJailed: expected %v, got %v", tt.jailed, got)
			}
			if got := tt.w.CanUnjail(tt.height); got != tt.canUnjail {
				t.Fatalf("CanUnjail: expected %v, got %v", tt.canUnjail, got)
			}
		})
	}
}

// ============================================================
// B23. ProcessExitingWorkers: not yet elapsed
// ============================================================

func TestBoundary_ProcessExitingWorkers_NotElapsed(t *testing.T) {
	k, ctx := setupKeeper(t)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Status = types.WorkerStatusExiting
	w.ExitRequestedAt = 90
	k.SetWorker(ctx, w)

	k.ProcessExitingWorkers(ctx)

	_, found := k.GetWorker(ctx, addr)
	if !found {
		t.Fatal("worker should still exist, exit period not elapsed")
	}
}

// ============================================================
// B24. IncrementSuccessStreak on non-existent worker → no panic
// ============================================================

func TestBoundary_IncrementSuccessStreak_NonExistent(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("nonexistent_________"))
	k.IncrementSuccessStreak(ctx, addr)
}

// ============================================================
// B25. UpdateWorkerStats on non-existent worker → no panic
// ============================================================

func TestBoundary_UpdateWorkerStats_NonExistent(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("nonexistent_________"))
	k.UpdateWorkerStats(ctx, addr, sdk.NewCoin("ufai", math.NewInt(1000)))
}

// ============================================================
// B26. GetWorkerPubkey
// ============================================================

func TestBoundary_GetWorkerPubkey(t *testing.T) {
	k, ctx := setupKeeper(t)

	addr := sdk.AccAddress([]byte("nonexistent_________"))
	_, found := k.GetWorkerPubkey(ctx, addr)
	if found {
		t.Fatal("should not find pubkey for non-existent worker")
	}

	wAddr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(wAddr.String())
	w.Pubkey = "my_pubkey_123"
	k.SetWorker(ctx, w)

	pk, found := k.GetWorkerPubkey(ctx, wAddr)
	if !found {
		t.Fatal("should find pubkey for existing worker")
	}
	if pk != "my_pubkey_123" {
		t.Fatalf("expected my_pubkey_123, got %s", pk)
	}
}

// ============================================================
// B27. ValidateBasic for message types
// ============================================================

func TestBoundary_ValidateBasic_Messages(t *testing.T) {
	validAddr := sdk.AccAddress([]byte("valid_address_______")).String()

	msg1 := types.NewMsgRegisterWorker(validAddr, "", []string{"m1"}, "ep", "gpu", 80, 1, "op", 0)
	if err := msg1.ValidateBasic(); err == nil {
		t.Fatal("empty pubkey should fail")
	}

	msg2 := types.NewMsgRegisterWorker(validAddr, "pk", []string{}, "ep", "gpu", 80, 1, "op", 0)
	if err := msg2.ValidateBasic(); err == nil {
		t.Fatal("empty models should fail")
	}

	msg3 := types.NewMsgRegisterWorker(validAddr, "pk", []string{"m1", ""}, "ep", "gpu", 80, 1, "op", 0)
	if err := msg3.ValidateBasic(); err == nil {
		t.Fatal("model with empty string should fail")
	}

	msg4 := types.NewMsgStake(validAddr, sdk.NewCoin("ufai", math.ZeroInt()))
	if err := msg4.ValidateBasic(); err == nil {
		t.Fatal("zero stake amount should fail")
	}

	msg5 := types.NewMsgUpdateModels(validAddr, []string{})
	if err := msg5.ValidateBasic(); err == nil {
		t.Fatal("empty models in UpdateModels should fail")
	}

	// Invalid address in all message types
	if err := types.NewMsgRegisterWorker("invalid", "pk", []string{"m1"}, "ep", "gpu", 80, 1, "op", 0).ValidateBasic(); err == nil {
		t.Fatal("invalid address should fail for MsgRegisterWorker")
	}
	if err := types.NewMsgExitWorker("invalid").ValidateBasic(); err == nil {
		t.Fatal("invalid address should fail for MsgExitWorker")
	}
	if err := types.NewMsgUpdateModels("invalid", []string{"m1"}).ValidateBasic(); err == nil {
		t.Fatal("invalid address should fail for MsgUpdateModels")
	}
	if err := types.NewMsgStake("invalid", sdk.NewCoin("ufai", math.NewInt(1000))).ValidateBasic(); err == nil {
		t.Fatal("invalid address should fail for MsgStake")
	}
	if err := types.NewMsgUnjail("invalid").ValidateBasic(); err == nil {
		t.Fatal("invalid address should fail for MsgUnjail")
	}
}

// ============================================================
// B28. WorkerStatus String method
// ============================================================

func TestBoundary_WorkerStatus_String(t *testing.T) {
	tests := []struct {
		s    types.WorkerStatus
		want string
	}{
		{types.WorkerStatusActive, "ACTIVE"},
		{types.WorkerStatusJailed, "JAILED"},
		{types.WorkerStatusExiting, "EXITING"},
		{types.WorkerStatusExited, "EXITED"},
		{types.WorkerStatus(99), "UNKNOWN"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Fatalf("WorkerStatus(%d).String() = %s, want %s", tt.s, got, tt.want)
		}
	}
}

// ============================================================
// B29. GetWorkersByOperatorId
// ============================================================

func TestBoundary_GetWorkersByOperatorId(t *testing.T) {
	k, ctx := setupKeeper(t)

	for i, name := range []string{"worker1_____________", "worker2_____________", "worker3_____________"} {
		addr := sdk.AccAddress([]byte(name))
		w := makeWorker(addr.String())
		if i < 2 {
			w.OperatorId = "op_alpha"
		} else {
			w.OperatorId = "op_beta"
		}
		k.SetWorker(ctx, w)
	}

	alpha := k.GetWorkersByOperatorId(ctx, "op_alpha")
	if len(alpha) != 2 {
		t.Fatalf("expected 2 workers for op_alpha, got %d", len(alpha))
	}

	beta := k.GetWorkersByOperatorId(ctx, "op_beta")
	if len(beta) != 1 {
		t.Fatalf("expected 1 worker for op_beta, got %d", len(beta))
	}

	none := k.GetWorkersByOperatorId(ctx, "op_nonexistent")
	if len(none) != 0 {
		t.Fatalf("expected 0 workers for nonexistent operator, got %d", len(none))
	}
}
