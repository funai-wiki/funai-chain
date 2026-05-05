package keeper_test

// Audit Rule 4: max_fee pre-authorisation — never silently drop a settled
// task on user-balance shortfall. The Worker has done the work; the task
// must land on chain. Pay min(actualFee, balance); the difference is
// absorbed by the Worker (and proportionally by Verifiers via the
// existing distribution split).
//
// These tests pin the new semantics introduced by the Rule-4 PR:
//   - SUCCESS shortfall: settles partial, EventShortfall emitted, balance
//     fully drained, Worker streak still incremented.
//   - SUCCESS zero-balance: settles with Fee=0 (Worker absorbs 100 %),
//     SettledTask still written.
//   - FAIL shortfall: same partial-pay semantic; Worker still jailed.
//   - Audit re-settle (settleAuditedTask) shortfall paths: settle, do
//     not return false, pending deleted.

import (
	"encoding/hex"
	"strings"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/settlement/types"
)

func TestRule4_Success_Shortfall_EmitsEventAndSettlesPartial(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	user := makeAddr("rule4-su-user")
	worker := makeAddr("rule4-su-worker")
	// Deposit only 60. Fee is 100. payable = 60, shortfall = 40.
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(60)))

	verifiers := []types.VerifierResult{
		{Address: makeAddr("rule4-su-v1").String(), Pass: true},
		{Address: makeAddr("rule4-su-v2").String(), Pass: true},
		{Address: makeAddr("rule4-su-v3").String(), Pass: true},
	}
	taskId := []byte("rule4-success-shortfall-1")

	entries := []types.SettlementEntry{
		{
			TaskId:          taskId,
			UserAddress:     user.String(),
			WorkerAddress:   worker.String(),
			Fee:             sdk.NewCoin("ufai", math.NewInt(100)),
			ExpireBlock:     10000,
			Status:          types.SettlementSuccess,
			VerifierResults: verifiers,
		},
	}
	msg := makeBatchMsg(t, makeAddr("rule4-su-prop").String(), entries)
	if _, err := k.ProcessBatchSettlement(ctx, msg); err != nil {
		t.Fatalf("Rule 4: shortfall must not error the batch: %v", err)
	}

	// Task settled with partial Fee.
	st, found := k.GetSettledTask(ctx, taskId)
	if !found {
		t.Fatal("Rule 4: shortfall entry must produce SettledTask")
	}
	if st.Status != types.TaskSettled {
		t.Fatalf("Rule 4: expected TaskSettled, got %s", st.Status)
	}
	if !st.Fee.Amount.Equal(math.NewInt(60)) {
		t.Fatalf("Rule 4: settled Fee should equal payable balance (60), got %s", st.Fee.Amount)
	}

	// Balance fully drained.
	ia, _ := k.GetInferenceAccount(ctx, user)
	if !ia.Balance.Amount.Equal(math.NewInt(0)) {
		t.Fatalf("Rule 4: balance should be drained to 0, got %s", ia.Balance.Amount)
	}

	// Worker streak incremented (work was done).
	if len(wk.streakCalls) != 1 {
		t.Fatalf("Rule 4: Worker streak still incremented on shortfall, got %d", len(wk.streakCalls))
	}

	// EventShortfall emitted with the right attributes.
	var foundEvent bool
	for _, ev := range ctx.EventManager().Events() {
		if ev.Type != types.EventShortfall {
			continue
		}
		foundEvent = true
		// Decode and verify the event carries the expected amounts.
		expectedAttrs := map[string]string{
			types.AttributeKeyTaskId:          hex.EncodeToString(taskId),
			types.AttributeKeyExpectedFee:     "100ufai",
			types.AttributeKeyPaidFee:         "60ufai",
			types.AttributeKeyShortfallAmount: "40ufai",
		}
		for _, a := range ev.Attributes {
			if want, ok := expectedAttrs[a.Key]; ok && a.Value != want {
				t.Errorf("Rule 4: EventShortfall attr %s expected %q, got %q", a.Key, want, a.Value)
			}
		}
	}
	if !foundEvent {
		t.Fatal("Rule 4: EventShortfall must be emitted on partial-pay settle")
	}
}

func TestRule4_Success_ZeroBalance_StillSettles(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	user := makeAddr("rule4-zb-user")
	worker := makeAddr("rule4-zb-worker")
	// Deposit a token then drain it; ensures the account exists with zero
	// balance for the partial-pay path.
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(1)))
	if _, found := k.GetInferenceAccount(ctx, user); !found {
		k.SetInferenceAccount(ctx, types.InferenceAccount{
			Address: user.String(),
			Balance: sdk.NewCoin("ufai", math.ZeroInt()),
		})
	} else {
		// Force balance back to 0 so the partial-pay branch fires with payable=0.
		ia, _ := k.GetInferenceAccount(ctx, user)
		ia.Balance = sdk.NewCoin("ufai", math.ZeroInt())
		k.SetInferenceAccount(ctx, ia)
	}

	verifiers := []types.VerifierResult{
		{Address: makeAddr("rule4-zb-v1").String(), Pass: true},
		{Address: makeAddr("rule4-zb-v2").String(), Pass: true},
		{Address: makeAddr("rule4-zb-v3").String(), Pass: true},
	}
	taskId := []byte("rule4-zero-balance-task1")

	entries := []types.SettlementEntry{
		{
			TaskId:          taskId,
			UserAddress:     user.String(),
			WorkerAddress:   worker.String(),
			Fee:             sdk.NewCoin("ufai", math.NewInt(100)),
			ExpireBlock:     10000,
			Status:          types.SettlementSuccess,
			VerifierResults: verifiers,
		},
	}
	msg := makeBatchMsg(t, makeAddr("rule4-zb-prop").String(), entries)
	if _, err := k.ProcessBatchSettlement(ctx, msg); err != nil {
		t.Fatalf("Rule 4: zero balance must not error: %v", err)
	}

	st, found := k.GetSettledTask(ctx, taskId)
	if !found {
		t.Fatal("Rule 4: zero-balance entry must produce SettledTask (Worker absorbs 100% loss)")
	}
	if !st.Fee.Amount.Equal(math.NewInt(0)) {
		t.Fatalf("Rule 4: zero-balance partial-pay Fee=0, got %s", st.Fee.Amount)
	}
	if len(wk.streakCalls) != 1 {
		t.Fatalf("Rule 4: streak still incremented even at Fee=0, got %d", len(wk.streakCalls))
	}
}

func TestRule4_Fail_Shortfall_StillJailsAndSettles(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	user := makeAddr("rule4-fl-user")
	worker := makeAddr("rule4-fl-worker")
	// failFee = 1000 × 150 / 1000 = 150. Deposit 30 — shortfall.
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(30)))

	taskId := []byte("rule4-fail-shortfall-001")
	entries := []types.SettlementEntry{
		{
			TaskId:        taskId,
			UserAddress:   user.String(),
			WorkerAddress: worker.String(),
			Fee:           sdk.NewCoin("ufai", math.NewInt(1000)),
			ExpireBlock:   10000,
			Status:        types.SettlementFail,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("rule4-fl-v1").String(), Pass: true},
				{Address: makeAddr("rule4-fl-v2").String(), Pass: false},
				{Address: makeAddr("rule4-fl-v3").String(), Pass: false},
			},
		},
	}
	msg := makeBatchMsg(t, makeAddr("rule4-fl-prop").String(), entries)
	if _, err := k.ProcessBatchSettlement(ctx, msg); err != nil {
		t.Fatalf("Rule 4: FAIL shortfall must not error: %v", err)
	}

	st, found := k.GetSettledTask(ctx, taskId)
	if !found {
		t.Fatal("Rule 4: FAIL shortfall must produce SettledTask")
	}
	if st.Status != types.TaskFailSettled {
		t.Fatalf("Rule 4: expected TaskFailSettled, got %s", st.Status)
	}
	if !st.Fee.Amount.Equal(math.NewInt(30)) {
		t.Fatalf("Rule 4: FAIL settled Fee = available balance (30), got %s", st.Fee.Amount)
	}

	if len(wk.jailCalls) != 1 {
		t.Fatalf("FAIL still jails Worker independent of shortfall, got %d jails", len(wk.jailCalls))
	}

	// EventShortfall emitted.
	var foundEvent bool
	for _, ev := range ctx.EventManager().Events() {
		if ev.Type == types.EventShortfall {
			foundEvent = true
			break
		}
	}
	if !foundEvent {
		t.Fatal("Rule 4: EventShortfall must be emitted on FAIL-path partial-pay")
	}
}

// Regression: when balance is sufficient (the normal happy path), nothing
// changes — no shortfall, no event, full payment.
func TestRule4_Success_FullBalance_NoShortfallEvent(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	user := makeAddr("rule4-fb-user")
	worker := makeAddr("rule4-fb-worker")
	_ = k.ProcessDeposit(ctx, user, sdk.NewCoin("ufai", math.NewInt(10_000)))

	taskId := []byte("rule4-full-balance-task1")
	entries := []types.SettlementEntry{
		{
			TaskId:        taskId,
			UserAddress:   user.String(),
			WorkerAddress: worker.String(),
			Fee:           sdk.NewCoin("ufai", math.NewInt(1000)),
			ExpireBlock:   10000,
			Status:        types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("rule4-fb-v1").String(), Pass: true},
				{Address: makeAddr("rule4-fb-v2").String(), Pass: true},
				{Address: makeAddr("rule4-fb-v3").String(), Pass: true},
			},
		},
	}
	msg := makeBatchMsg(t, makeAddr("rule4-fb-prop").String(), entries)
	if _, err := k.ProcessBatchSettlement(ctx, msg); err != nil {
		t.Fatalf("happy path must not error: %v", err)
	}

	st, _ := k.GetSettledTask(ctx, taskId)
	if !st.Fee.Amount.Equal(math.NewInt(1000)) {
		t.Fatalf("happy path Fee should equal full actualFee, got %s", st.Fee.Amount)
	}
	for _, ev := range ctx.EventManager().Events() {
		if ev.Type == types.EventShortfall {
			// Walk attrs in case it's an unrelated EventShortfall, but our
			// task should NOT trigger one.
			for _, a := range ev.Attributes {
				if a.Key == types.AttributeKeyTaskId && strings.Contains(a.Value, "rule4-full-balance") {
					t.Fatal("happy path must NOT emit EventShortfall")
				}
			}
		}
	}
}
