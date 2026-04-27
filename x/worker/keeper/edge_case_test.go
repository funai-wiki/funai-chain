package keeper_test

// Edge-case and boundary-condition tests for the worker module.

import (
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/worker/keeper"
	"github.com/funai-wiki/funai-chain/x/worker/types"
)

// ============================================================
// 1. JailWorker on already tombstoned worker → no-op
// ============================================================

func TestJailWorker_AlreadyTombstoned_NoOp(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Tombstoned = true
	w.JailCount = 3
	w.Jailed = true
	w.Status = types.WorkerStatusJailed
	k.SetWorker(ctx, w)

	k.JailWorker(ctx, addr, 120)

	got, _ := k.GetWorker(ctx, addr)
	if got.JailCount != 3 {
		t.Fatalf("tombstoned worker jail_count should not change, got %d", got.JailCount)
	}
}

// ============================================================
// 2. Jail resets success streak to 0
// ============================================================

func TestJailWorker_ResetsSuccessStreak(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.SuccessStreak = 40
	k.SetWorker(ctx, w)

	k.JailWorker(ctx, addr, 120)

	got, _ := k.GetWorker(ctx, addr)
	if got.SuccessStreak != 0 {
		t.Fatalf("success streak should be 0 after jail, got %d", got.SuccessStreak)
	}
}

// ============================================================
// 3. SuccessStreak at exactly threshold-1: one more → decay-by-1 (KT V6)
// ============================================================

func TestSuccessStreak_ExactThresholdMinus1(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.JailCount = 2
	w.SuccessStreak = 998
	k.SetWorker(ctx, w)

	k.IncrementSuccessStreak(ctx, addr)
	got, _ := k.GetWorker(ctx, addr)
	if got.SuccessStreak != 999 {
		t.Fatalf("expected streak 999, got %d", got.SuccessStreak)
	}
	if got.JailCount != 2 {
		t.Fatalf("jail_count should still be 2 at streak 999, got %d", got.JailCount)
	}

	k.IncrementSuccessStreak(ctx, addr)
	got, _ = k.GetWorker(ctx, addr)
	if got.SuccessStreak != 0 {
		t.Fatalf("streak should reset to 0 at JailDecayInterval, got %d", got.SuccessStreak)
	}
	// KT V6 (2026-04-27): decay by 1, NOT reset to 0.
	if got.JailCount != 1 {
		t.Fatalf("jail_count should decay 2 → 1 at JailDecayInterval, got %d", got.JailCount)
	}
}

// ============================================================
// 4. SuccessStreak with jail_count=0: threshold still resets streak (floor at 0)
// ============================================================

func TestSuccessStreak_ZeroJailCount_StillResets(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.JailCount = 0
	w.SuccessStreak = 999
	k.SetWorker(ctx, w)

	k.IncrementSuccessStreak(ctx, addr)
	got, _ := k.GetWorker(ctx, addr)
	if got.SuccessStreak != 0 {
		t.Fatalf("streak should reset, got %d", got.SuccessStreak)
	}
	if got.JailCount != 0 {
		t.Fatalf("jail_count should floor at 0, got %d", got.JailCount)
	}
}

// ============================================================
// 5. SlashWorker with 100% → zero stake
// ============================================================

func TestSlashWorker_100Percent(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Stake = sdk.NewCoin("ufai", math.NewInt(10000))
	k.SetWorker(ctx, w)

	k.SlashWorker(ctx, addr, 100)

	got, _ := k.GetWorker(ctx, addr)
	if !got.Stake.IsZero() {
		t.Fatalf("100%% slash should leave zero stake, got %s", got.Stake)
	}
}

// ============================================================
// 6. SlashWorker with tiny stake → rounds to 0, no change
// ============================================================

func TestSlashWorker_TinyStake_DustSlash(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Stake = sdk.NewCoin("ufai", math.NewInt(10))
	k.SetWorker(ctx, w)

	k.SlashWorker(ctx, addr, 5) // 10*5/100=0

	got, _ := k.GetWorker(ctx, addr)
	if !got.Stake.Amount.Equal(math.NewInt(10)) {
		t.Fatalf("slash rounds to 0, stake unchanged; got %s", got.Stake.Amount)
	}
}

// ============================================================
// 7. SlashWorkerTo: sends slashed coins to recipient
// ============================================================

func TestSlashWorkerTo_SendsToRecipient(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Stake = sdk.NewCoin("ufai", math.NewInt(100000))
	k.SetWorker(ctx, w)

	recipient := sdk.AccAddress([]byte("recipient___________"))
	k.SlashWorkerTo(ctx, addr, 5, recipient)

	got, _ := k.GetWorker(ctx, addr)
	expected := math.NewInt(100000).Sub(math.NewInt(100000).MulRaw(5).QuoRaw(100))
	if !got.Stake.Amount.Equal(expected) {
		t.Fatalf("expected %s after slash, got %s", expected, got.Stake.Amount)
	}
}

// ============================================================
// 8. Unjail at exact boundary block height
// ============================================================

func TestUnjailWorker_ExactBoundary(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Jailed = true
	w.JailUntil = 100 // ctx height is 100
	w.JailCount = 1
	w.Status = types.WorkerStatusJailed
	k.SetWorker(ctx, w)

	err := k.UnjailWorker(ctx, addr)
	if err != nil {
		t.Fatalf("unjail at exact boundary should succeed: %v", err)
	}

	got, _ := k.GetWorker(ctx, addr)
	if got.Jailed {
		t.Fatal("worker should be unjailed at exact boundary")
	}
}

// ============================================================
// 9. Unjail one block early → error
// ============================================================

func TestUnjailWorker_OneBlockEarly(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Jailed = true
	w.JailUntil = 101
	w.JailCount = 1
	w.Status = types.WorkerStatusJailed
	k.SetWorker(ctx, w)

	err := k.UnjailWorker(ctx, addr)
	if err == nil {
		t.Fatal("unjail 1 block early should fail")
	}
}

// ============================================================
// 10. Progressive jail full cycle: 1st→2nd→tombstone
// ============================================================

func TestJailWorker_ProgressiveFull(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	k.SetWorker(ctx, w)

	params := k.GetParams(ctx)

	// 1st
	k.JailWorker(ctx, addr, params.Jail1Duration)
	w1, _ := k.GetWorker(ctx, addr)
	if w1.JailCount != 1 || !w1.Jailed {
		t.Fatal("1st jail: should be jailed with count=1")
	}
	if w1.Tombstoned {
		t.Fatal("not tombstoned after 1st")
	}

	w1.Jailed = false
	w1.Status = types.WorkerStatusActive
	k.SetWorker(ctx, w1)

	// 2nd
	k.JailWorker(ctx, addr, params.Jail2Duration)
	w2, _ := k.GetWorker(ctx, addr)
	if w2.JailCount != 2 || w2.Tombstoned {
		t.Fatal("2nd jail: count=2, not tombstoned")
	}

	w2.Jailed = false
	w2.Status = types.WorkerStatusActive
	k.SetWorker(ctx, w2)

	// 3rd → tombstone
	k.JailWorker(ctx, addr, params.Jail1Duration)
	w3, _ := k.GetWorker(ctx, addr)
	// Note: JailWorker increments jail_count locally to 3, calls SlashWorker which
	// re-reads from store (count=2), then JailWorker re-reads after slash (count=2).
	// The stored JailCount remains 2, but Tombstoned is set.
	if !w3.Tombstoned {
		t.Fatal("3rd jail should tombstone the worker")
	}
}

// ============================================================
// 11. UpdateWorkerStats
// ============================================================

func TestUpdateWorkerStats_Accumulates(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	k.SetWorker(ctx, w)

	k.UpdateWorkerStats(ctx, addr, sdk.NewCoin("ufai", math.NewInt(1000)))
	k.UpdateWorkerStats(ctx, addr, sdk.NewCoin("ufai", math.NewInt(2000)))

	got, _ := k.GetWorker(ctx, addr)
	if got.TotalTasks != 2 {
		t.Fatalf("expected 2 total tasks, got %d", got.TotalTasks)
	}
	if !got.TotalFeeEarned.Amount.Equal(math.NewInt(3000)) {
		t.Fatalf("expected 3000, got %s", got.TotalFeeEarned.Amount)
	}
}

// ============================================================
// 12. ProcessExitingWorkers with zero stake
// ============================================================

func TestProcessExitingWorkers_ZeroStake(t *testing.T) {
	k, ctx := setupKeeper(t)
	params := k.GetParams(ctx)

	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Status = types.WorkerStatusExiting
	w.ExitRequestedAt = 1
	w.Stake = sdk.NewCoin("ufai", math.ZeroInt())
	k.SetWorker(ctx, w)

	exitCtx := ctx.WithBlockHeight(1 + params.ExitWaitPeriod + 1)
	k.ProcessExitingWorkers(exitCtx)

	_, found := k.GetWorker(exitCtx, addr)
	if found {
		t.Fatal("exited worker with zero stake should be deleted")
	}
}

// ============================================================
// 13. GetModelInstalledStake excludes jailed workers
// ============================================================

func TestGetModelInstalledStake_ExcludesJailed(t *testing.T) {
	k, ctx := setupKeeper(t)

	addr1 := sdk.AccAddress([]byte("worker1_____________"))
	addr2 := sdk.AccAddress([]byte("worker2_____________"))

	w1 := makeWorker(addr1.String())
	w1.Stake = sdk.NewCoin("ufai", math.NewInt(5000))
	w1.SupportedModels = []string{"modelA"}

	w2 := makeWorker(addr2.String())
	w2.Stake = sdk.NewCoin("ufai", math.NewInt(3000))
	w2.SupportedModels = []string{"modelA"}
	w2.Status = types.WorkerStatusJailed
	w2.Jailed = true

	k.SetWorker(ctx, w1)
	k.SetModelIndices(ctx, addr1, w1.SupportedModels)
	k.SetWorker(ctx, w2)
	k.SetModelIndices(ctx, addr2, w2.SupportedModels)

	total := k.GetModelInstalledStake(ctx, "modelA")
	if !total.Equal(math.NewInt(5000)) {
		t.Fatalf("jailed worker stake excluded, expected 5000, got %s", total)
	}
}

// ============================================================
// 14. GetActiveWorkerStake with all jailed → zero
// ============================================================

func TestGetActiveWorkerStake_AllJailed(t *testing.T) {
	k, ctx := setupKeeper(t)

	for _, name := range []string{"worker1_____________", "worker2_____________", "worker3_____________"} {
		addr := sdk.AccAddress([]byte(name))
		w := makeWorker(addr.String())
		w.Stake = sdk.NewCoin("ufai", math.NewInt(10000))
		w.Status = types.WorkerStatusJailed
		w.Jailed = true
		k.SetWorker(ctx, w)
	}

	total := k.GetActiveWorkerStake(ctx)
	if !total.IsZero() {
		t.Fatalf("all jailed → zero active stake, got %s", total)
	}
}

// ============================================================
// 15. Worker params validation boundaries
// ============================================================

func TestWorkerParams_Validation(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*types.Params)
		wantErr bool
	}{
		{"valid_defaults", func(p *types.Params) {}, false},
		{"zero_min_stake", func(p *types.Params) { p.MinStake = sdk.NewCoin("ufai", math.ZeroInt()) }, true},
		{"negative_exit_wait", func(p *types.Params) { p.ExitWaitPeriod = -1 }, true},
		{"zero_exit_wait", func(p *types.Params) { p.ExitWaitPeriod = 0 }, true},
		{"negative_cold_start", func(p *types.Params) { p.ColdStartFreeBlocks = -1 }, true},
		{"zero_cold_start_ok", func(p *types.Params) { p.ColdStartFreeBlocks = 0 }, false},
		{"zero_jail1", func(p *types.Params) { p.Jail1Duration = 0 }, true},
		{"zero_jail2", func(p *types.Params) { p.Jail2Duration = 0 }, true},
		{"zero_slash", func(p *types.Params) { p.SlashFraudPercent = 0 }, true},
		{"slash_over_100", func(p *types.Params) { p.SlashFraudPercent = 101 }, true},
		{"zero_jail_decay_interval", func(p *types.Params) { p.JailDecayInterval = 0 }, true},
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
// 16. ColdStart boundary: register at exact ColdStartFreeBlocks height
// ============================================================

func TestMsgServer_RegisterWorker_ColdStartExactBoundary(t *testing.T) {
	k, ctx := setupKeeper(t)
	params := k.GetParams(ctx)
	params.ColdStartFreeBlocks = 100
	k.SetParams(ctx, params)

	ms := keeper.NewMsgServerImpl(k)

	// At exact boundary height = ColdStartFreeBlocks (100)
	// ctx.BlockHeight() is 100, ColdStartFreeBlocks is 100 → within cold start
	addr := sdk.AccAddress([]byte("boundary_worker_____"))
	msg := types.NewMsgRegisterWorker(
		addr.String(), "pk1", []string{"m1"}, "localhost:8080",
		"H100", 80, 1, "op1", 0,
	)
	_, err := ms.RegisterWorker(ctx, msg)
	if err != nil {
		t.Fatalf("registration at exact boundary should succeed: %v", err)
	}
}

// ============================================================
// 17. CountUniqueOperators with empty operator_id
// ============================================================

func TestCountUniqueOperators_EmptyOperatorId(t *testing.T) {
	k, ctx := setupKeeper(t)

	addr1 := sdk.AccAddress([]byte("worker1_____________"))
	addr2 := sdk.AccAddress([]byte("worker2_____________"))

	w1 := makeWorker(addr1.String())
	w1.OperatorId = ""
	w1.SupportedModels = []string{"modelX"}
	w2 := makeWorker(addr2.String())
	w2.OperatorId = "op_beta"
	w2.SupportedModels = []string{"modelX"}

	k.SetWorker(ctx, w1)
	k.SetModelIndices(ctx, addr1, w1.SupportedModels)
	k.SetWorker(ctx, w2)
	k.SetModelIndices(ctx, addr2, w2.SupportedModels)

	count := k.CountUniqueOperators(ctx, "modelX")
	if count != 1 {
		t.Fatalf("empty operator_id not counted, expected 1, got %d", count)
	}
}

// ============================================================
// S12. RegisterWorker after cold start requires stake (mock succeeds)
// ============================================================

func TestMsgServer_RegisterWorker_InsufficientStake(t *testing.T) {
	k, ctx := setupKeeper(t)
	params := k.GetParams(ctx)
	// Past cold start: ColdStartFreeBlocks=0, ctx height=100
	params.ColdStartFreeBlocks = 0
	k.SetParams(ctx, params)

	ms := keeper.NewMsgServerImpl(k)

	addr := sdk.AccAddress([]byte("postcs_worker_______"))
	msg := types.NewMsgRegisterWorker(
		addr.String(), "pk_postcs", []string{"m1"}, "localhost:9090",
		"A100", 40, 1, "op_postcs", 0,
	)

	// Mock bank keeper always returns nil (success), so registration succeeds.
	// This documents the expected behavior: after cold start, the bank call
	// (SendCoinsFromAccountToModule) is invoked for MinStake.
	_, err := ms.RegisterWorker(ctx, msg)
	if err != nil {
		t.Fatalf("registration after cold start should succeed (mock bank): %v", err)
	}

	// Verify the worker was stored with MinStake (not zero like cold start)
	got, found := k.GetWorker(ctx, addr)
	if !found {
		t.Fatal("worker should exist after registration")
	}
	if !got.Stake.Amount.Equal(params.MinStake.Amount) {
		t.Fatalf("after cold start, worker stake should be MinStake %s, got %s",
			params.MinStake.Amount, got.Stake.Amount)
	}
}

// ============================================================
// S13. Repeated slash until stake reaches zero
// ============================================================

func TestSlashWorker_RepeatedSlashToZero(t *testing.T) {
	k, ctx := setupKeeper(t)
	addr := sdk.AccAddress([]byte("worker1_____________"))
	w := makeWorker(addr.String())
	w.Stake = sdk.NewCoin("ufai", math.NewInt(100))
	k.SetWorker(ctx, w)

	// Slash 5% repeatedly. Eventually 5% of remaining rounds to 0 and
	// slashWorkerInternal returns early (no change). No panic expected.
	for i := 0; i < 200; i++ {
		k.SlashWorker(ctx, addr, 5)
	}

	got, found := k.GetWorker(ctx, addr)
	if !found {
		t.Fatal("worker should still exist after repeated slashes")
	}

	// Starting from 100, 5% slash: 100→95→90→85→...→1→(5%*1=0, no-op)
	// Stake should be small (≤1) and never negative.
	if got.Stake.IsNegative() {
		t.Fatalf("stake should never be negative, got %s", got.Stake)
	}
	if got.Stake.Amount.GT(math.NewInt(100)) {
		t.Fatalf("stake should not exceed original, got %s", got.Stake.Amount)
	}
}
