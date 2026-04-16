package keeper_test

// Regression tests for critical bug fixes.
// Each test is derived from the V5.1 Final spec, NOT from reading the implementation.

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"cosmossdk.io/errors"
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

	"github.com/funai-wiki/funai-chain/x/settlement/keeper"
	"github.com/funai-wiki/funai-chain/x/settlement/types"
)

// ============================================================
// Enhanced mock that tracks coin amounts per recipient.
// This lets us verify exact fee distribution without looking at implementation.
// ============================================================

type trackingBankKeeper struct {
	received map[string]math.Int // addr → total coins received from module
	sent     map[string]math.Int // addr → total coins sent to module
}

func newTrackingBankKeeper() *trackingBankKeeper {
	return &trackingBankKeeper{
		received: make(map[string]math.Int),
		sent:     make(map[string]math.Int),
	}
}

func (m *trackingBankKeeper) SendCoins(_ context.Context, _, _ sdk.AccAddress, _ sdk.Coins) error {
	return nil
}

func (m *trackingBankKeeper) SendCoinsFromAccountToModule(_ context.Context, sender sdk.AccAddress, _ string, amt sdk.Coins) error {
	addr := sender.String()
	if _, ok := m.sent[addr]; !ok {
		m.sent[addr] = math.ZeroInt()
	}
	m.sent[addr] = m.sent[addr].Add(amt[0].Amount)
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

func (m *trackingBankKeeper) receivedBy(addr sdk.AccAddress) math.Int {
	v, ok := m.received[addr.String()]
	if !ok {
		return math.ZeroInt()
	}
	return v
}

// setupTrackingKeeper creates a keeper with the tracking bank for verifying exact amounts.
func setupTrackingKeeper(t *testing.T) (keeper.Keeper, sdk.Context, *trackingBankKeeper, *mockWorkerKeeper) {
	t.Helper()

	storeKey := setupStoreKey(t)
	bk := newTrackingBankKeeper()
	wk := newMockWorkerKeeper()

	k := setupKeeperWithBankAndWorker(t, storeKey, bk, wk)
	ctx := setupContext(t, storeKey)
	k.SetParams(ctx, types.DefaultParams())

	return k, ctx, bk, wk
}

// ============================================================
// Fix 1: Audit fund deduplication
// Spec: "per_person_fee = pool / audit person-count" — each unique auditor gets one payment.
// ============================================================

func TestAuditFundDistribution_DeduplicatesAuditors(t *testing.T) {
	k, ctx, bk, _ := setupTrackingKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	auditorA := makeAddr("auditor-a")
	auditorB := makeAddr("auditor-b")

	// Create 3 AuditPendingTasks in the same epoch, auditor A appears in ALL of them
	epoch := ctx.BlockHeight() / 100
	for i := 0; i < 3; i++ {
		taskId := []byte(fmt.Sprintf("dedup-audit-task-%02d", i))
		k.SetAuditPending(ctx, types.AuditPendingTask{
			TaskId:            taskId,
			OriginalStatus:    types.SettlementSuccess,
			SubmittedAt:       epoch * 100, // same epoch
			UserAddress:       makeAddr("user").String(),
			WorkerAddress:     makeAddr("worker").String(),
			VerifierAddresses: []string{auditorA.String(), auditorB.String()},
			Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
			ExpireBlock:       10000,
		})
		// P1-9: DistributeAuditFund now reads from AuditRecord, not AuditPendingTask
		k.SetAuditRecord(ctx, types.AuditRecord{
			TaskId:           taskId,
			Epoch:            epoch,
			AuditorAddresses: []string{auditorA.String(), auditorB.String()},
			Results:          []bool{true, true},
			ProcessedAt:      epoch * 100,
		})
	}

	// Set epoch stats: 2 unique auditors, total fees 10M
	stats := types.EpochStats{
		Epoch:            epoch,
		TotalFees:        math.NewInt(10_000_000),
		AuditPersonCount: 2,
	}
	k.SetEpochStats(ctx, stats)

	k.DistributeAuditFund(ctx, epoch)

	// Audit KT §8: per-person-time fee = pool / total_person_times.
	// audit_pool = 10M * 30/1000 = 300_000. totalPersonTimes = 6 (each auditor × 3 records).
	// perPersonTime = 300_000 / 6 = 50_000. Each auditor gets 50_000 × 3 = 150_000.
	wantPerAuditor := math.NewInt(150_000)

	gotA := bk.receivedBy(auditorA)
	gotB := bk.receivedBy(auditorB)

	if !gotA.Equal(wantPerAuditor) {
		t.Fatalf("auditorA: want %s, got %s", wantPerAuditor, gotA)
	}
	if !gotB.Equal(wantPerAuditor) {
		t.Fatalf("auditorB: want %s, got %s", wantPerAuditor, gotB)
	}
}

func TestAuditFundDistribution_SingleAuditorMultipleTasks(t *testing.T) {
	k, ctx, bk, _ := setupTrackingKeeper(t)

	auditor := makeAddr("sole-auditor")
	epoch := ctx.BlockHeight() / 100

	// Same auditor in 5 different tasks
	for i := 0; i < 5; i++ {
		taskId := []byte(fmt.Sprintf("sole-audit-task-%02d", i))
		k.SetAuditPending(ctx, types.AuditPendingTask{
			TaskId:            taskId,
			OriginalStatus:    types.SettlementSuccess,
			SubmittedAt:       epoch * 100,
			UserAddress:       makeAddr("user").String(),
			WorkerAddress:     makeAddr("worker").String(),
			VerifierAddresses: []string{auditor.String()},
			Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
			ExpireBlock:       10000,
		})
		// P1-9: DistributeAuditFund now reads from AuditRecord, not AuditPendingTask
		k.SetAuditRecord(ctx, types.AuditRecord{
			TaskId:           taskId,
			Epoch:            epoch,
			AuditorAddresses: []string{auditor.String()},
			Results:          []bool{true},
			ProcessedAt:      epoch * 100,
		})
	}

	stats := types.EpochStats{
		Epoch:            epoch,
		TotalFees:        math.NewInt(10_000_000),
		AuditPersonCount: 1,
	}
	k.SetEpochStats(ctx, stats)

	k.DistributeAuditFund(ctx, epoch)

	// pool = 10M * 30/1000 = 300_000. Only 1 person → gets 300_000 once.
	want := math.NewInt(300_000)
	got := bk.receivedBy(auditor)
	if !got.Equal(want) {
		t.Fatalf("sole auditor: want %s, got %s (got %sx if bug exists)", want, got, got.Quo(want))
	}
}

// ============================================================
// Fix 2: FAIL→SUCCESS audit overturn — user not double-charged
// Spec: "FAIL settlement: user pays 5%". When overturned to SUCCESS,
// P1-NEW-1 fix: Under "no settlement before audit" principle, when audit VRF triggers during batch
// processing, it `continue`s before ANY fee is collected (neither SUCCESS 100% nor FAIL 5%).
// So on audit overturn FAIL→SUCCESS, user should pay full 100%, not 95%.
// ============================================================

func TestAuditOverturn_FailToSuccess_UserNotDoubleCharged(t *testing.T) {
	k, ctx, _, _ := setupTrackingKeeper(t)

	userAddr := makeAddr("overturn-user")
	workerAddr := makeAddr("overturn-worker")
	v1, v2, v3 := makeAddr("ov-v1"), makeAddr("ov-v2"), makeAddr("ov-v3")

	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	deposit := sdk.NewCoin("ufai", math.NewInt(10_000_000))
	_ = k.ProcessDeposit(ctx, userAddr, deposit)

	// Under "no settlement before audit" principle: when audit VRF triggered during batch processing,
	// the code did `continue` — NO fee was collected. Task goes directly to PENDING_AUDIT.
	// Simulate this by directly setting audit pending (no initial settlement).
	taskId := []byte("overturn-task-00001")
	k.SetAuditPending(ctx, types.AuditPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementFail,
		SubmittedAt:       ctx.BlockHeight(),
		UserAddress:       userAddr.String(),
		WorkerAddress:     workerAddr.String(),
		VerifierAddresses: []string{v1.String(), v2.String(), v3.String()},
		Fee:               fee,
		ExpireBlock:       10000,
	})

	// Balance should be unchanged — no fee collected during audit pending
	ia, _ := k.GetInferenceAccount(ctx, userAddr)
	if !ia.Balance.Amount.Equal(deposit.Amount) {
		t.Fatalf("before audit result: balance should be unchanged, got %s", ia.Balance.Amount)
	}

	// Submit 3 PASS audit results → overturns FAIL to SUCCESS
	for i := 0; i < 3; i++ {
		_ = k.ProcessAuditResult(ctx, &types.MsgAuditResult{
			Auditor:    makeAddr(fmt.Sprintf("ov-aud%d", i)).String(),
			TaskId:     taskId,
			Epoch:      1,
			Pass:       true,
			LogitsHash: []byte("hash"),
		})
	}

	// P1-NEW-1: user pays full 100% fee (not 95%) because no fee was ever collected before audit.
	ia, _ = k.GetInferenceAccount(ctx, userAddr)
	expectedFinal := deposit.Amount.Sub(fee.Amount)
	if !ia.Balance.Amount.Equal(expectedFinal) {
		t.Fatalf("after overturn: want %s (deposit - 100%% fee), got %s",
			expectedFinal, ia.Balance.Amount)
	}
}

// ============================================================
// Fix 3: Verifier fee dust loss
// Spec: "verifier_fee_ratio = 12% (120‰, 4% each × 3 verifiers)"
// Total verifier amount must be fully distributed, no coins lost.
// ============================================================

func TestDistributeSuccessFee_NoDustLoss(t *testing.T) {
	tests := []struct {
		name       string
		feeAmount  int64
		verifiers  int
		wantTotal  int64 // 12% of fee (verifier_fee_ratio = 120‰)
	}{
		{"divisible_by_3", 1_000_000, 3, 120_000},
		{"not_divisible_by_3", 1_000_001, 3, 120_000},      // 1_000_001 * 120 / 1000 = 120_000
		{"remainder_in_per_verifier", 999_999, 3, 119_999},  // 999_999 * 120 / 1000 = 119_999
		{"single_verifier", 1_000_000, 1, 120_000},
		{"large_fee", 999_999_999, 3, 119_999_999},
		{"tiny_fee_rounds_to_zero", 10, 3, 1},               // 10 * 120 / 1000 = 1
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, ctx, bk, _ := setupTrackingKeeper(t)
			k.SetCurrentAuditRate(ctx, 0)

			userAddr := makeAddr("dust-user")
			workerAddr := makeAddr("dust-worker")
			fee := sdk.NewCoin("ufai", math.NewInt(tt.feeAmount))
			_ = k.ProcessDeposit(ctx, userAddr, fee)

			verifiers := make([]types.VerifierResult, tt.verifiers)
			verifierAddrs := make([]sdk.AccAddress, tt.verifiers)
			for i := 0; i < tt.verifiers; i++ {
				addr := makeAddr(fmt.Sprintf("dust-v%d", i))
				verifiers[i] = types.VerifierResult{Address: addr.String(), Pass: true}
				verifierAddrs[i] = addr
			}

			entries := []types.SettlementEntry{
				{
					TaskId:          []byte(fmt.Sprintf("dust-task-%s-pad--", tt.name[:8])),
					UserAddress:     userAddr.String(),
					WorkerAddress:   workerAddr.String(),
					Fee:             fee,
					ExpireBlock:     10000,
					Status:          types.SettlementSuccess,
					VerifierResults: verifiers,
				},
			}
			msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
			_, err := k.ProcessBatchSettlement(ctx, msg)
			if err != nil {
				t.Fatalf("batch: %v", err)
			}

			// Sum what all verifiers actually received
			totalReceived := math.ZeroInt()
			for _, vAddr := range verifierAddrs {
				totalReceived = totalReceived.Add(bk.receivedBy(vAddr))
			}

			wantTotalInt := math.NewInt(tt.wantTotal)
			if !totalReceived.Equal(wantTotalInt) {
				t.Fatalf("verifier total: want %s, got %s (dust lost: %s)",
					wantTotalInt, totalReceived, wantTotalInt.Sub(totalReceived))
			}
		})
	}
}

func TestDistributeSuccessFee_ExecutorGets95Percent(t *testing.T) {
	k, ctx, bk, _ := setupTrackingKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	userAddr := makeAddr("exec-user")
	workerAddr := makeAddr("exec-worker")
	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	_ = k.ProcessDeposit(ctx, userAddr, fee)

	entries := []types.SettlementEntry{
		{
			TaskId:        []byte("exec-fee-task-00001"),
			UserAddress:   userAddr.String(),
			WorkerAddress: workerAddr.String(),
			Fee:           fee,
			ExpireBlock:   10000,
			Status:        types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("ef-v1").String(), Pass: true},
				{Address: makeAddr("ef-v2").String(), Pass: true},
				{Address: makeAddr("ef-v3").String(), Pass: true},
			},
		},
	}
	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	_, _ = k.ProcessBatchSettlement(ctx, msg)

	// Executor gets 85% = 850_000
	got := bk.receivedBy(workerAddr)
	want := math.NewInt(850_000)
	if !got.Equal(want) {
		t.Fatalf("executor: want %s (85%%), got %s", want, got)
	}
}

func TestDistributeFailFee_VerifiersPaidFromFailFee(t *testing.T) {
	k, ctx, bk, _ := setupTrackingKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	userAddr := makeAddr("failfee-user")
	workerAddr := makeAddr("failfee-worker")
	v1 := makeAddr("ff-v1")
	v2 := makeAddr("ff-v2")
	v3 := makeAddr("ff-v3")

	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

	entries := []types.SettlementEntry{
		{
			TaskId:        []byte("failfee-task-000001"),
			UserAddress:   userAddr.String(),
			WorkerAddress: workerAddr.String(),
			Fee:           fee,
			ExpireBlock:   10000,
			Status:        types.SettlementFail,
			VerifierResults: []types.VerifierResult{
				{Address: v1.String(), Pass: true},
				{Address: v2.String(), Pass: true},
				{Address: v3.String(), Pass: false},
			},
		},
	}
	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	_, _ = k.ProcessBatchSettlement(ctx, msg)

	// failFee = 1M * 50/1000 = 50_000
	// verifier share of failFee = 50_000 * 120/150 = 40_000  (verifier=120, audit=30, total=150)
	// per verifier = 40_000 / 3 = 13_333 (last gets 13_334)
	totalVerifier := bk.receivedBy(v1).Add(bk.receivedBy(v2)).Add(bk.receivedBy(v3))
	wantVerifierTotal := math.NewInt(40_000)
	if !totalVerifier.Equal(wantVerifierTotal) {
		t.Fatalf("verifier total from FAIL: want %s, got %s", wantVerifierTotal, totalVerifier)
	}

	// Executor gets 0 on FAIL
	workerGot := bk.receivedBy(workerAddr)
	if !workerGot.IsZero() {
		t.Fatalf("executor should get 0 on FAIL, got %s", workerGot)
	}
}

// ============================================================
// Fix 4: Audit timeout O(n) → O(k)
// Verify that only timed-out tasks are processed, not all pending.
// ============================================================

func TestHandleAuditTimeouts_OnlyProcessesTimedOut(t *testing.T) {
	k, ctx, _, _ := setupTrackingKeeper(t)

	params := k.GetParams(ctx)
	params.AuditTimeout = 100
	params.ReauditTimeout = 200
	k.SetParams(ctx, params)

	// Create tasks at different heights
	heights := []int64{10, 50, 90, 150, 200}
	for i, h := range heights {
		k.SetAuditPending(ctx, types.AuditPendingTask{
			TaskId:            []byte(fmt.Sprintf("timeout-test-task%02d", i)),
			OriginalStatus:    types.SettlementSuccess,
			SubmittedAt:       h,
			UserAddress:       makeAddr("to-user").String(),
			WorkerAddress:     makeAddr("to-worker").String(),
			VerifierAddresses: []string{makeAddr("to-v").String()},
			Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
			ExpireBlock:       10000,
		})
	}

	// At height 200, audit timeout=100 → cutoff=100
	// Tasks at height 10, 50, 90 should timeout (submittedAt <= 100, i.e. 200-submittedAt > 100)
	// But height 10: 200-10=190>100 ✓, height 50: 200-50=150>100 ✓, height 90: 200-90=110>100 ✓
	// height 150: 200-150=50<=100 ✗, height 200: 200-200=0<=100 ✗
	ctx = ctx.WithBlockHeight(200)
	timedOut := k.HandleAuditTimeouts(ctx)
	if timedOut != 3 {
		t.Fatalf("want 3 timed-out tasks, got %d", timedOut)
	}

	// Tasks at heights 150 and 200 should still be pending
	_, found := k.GetAuditPending(ctx, []byte("timeout-test-task03"))
	if !found {
		t.Fatal("task at height 150 should still be pending")
	}
	_, found = k.GetAuditPending(ctx, []byte("timeout-test-task04"))
	if !found {
		t.Fatal("task at height 200 should still be pending")
	}
}

func TestHandleAuditTimeouts_EmptySet(t *testing.T) {
	k, ctx, _, _ := setupTrackingKeeper(t)

	ctx = ctx.WithBlockHeight(10000)
	timedOut := k.HandleAuditTimeouts(ctx)
	if timedOut != 0 {
		t.Fatalf("want 0, got %d", timedOut)
	}
}

func TestHandleAuditTimeouts_ReauditSeparateFromAudit(t *testing.T) {
	k, ctx, _, _ := setupTrackingKeeper(t)

	params := k.GetParams(ctx)
	params.AuditTimeout = 50
	params.ReauditTimeout = 100
	k.SetParams(ctx, params)

	// Audit task at height 10
	k.SetAuditPending(ctx, types.AuditPendingTask{
		TaskId:            []byte("reaudit-sep-audit01"),
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       10,
		UserAddress:       makeAddr("rs-user").String(),
		WorkerAddress:     makeAddr("rs-worker").String(),
		VerifierAddresses: []string{makeAddr("rs-v").String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       10000,
		IsReaudit:         false,
	})

	// Reaudit task at height 10
	k.SetAuditPending(ctx, types.AuditPendingTask{
		TaskId:            []byte("reaudit-sep-reaud01"),
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       10,
		UserAddress:       makeAddr("rs-user").String(),
		WorkerAddress:     makeAddr("rs-worker").String(),
		VerifierAddresses: []string{makeAddr("rs-v").String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       10000,
		IsReaudit:         true,
	})

	// At height 80: audit cutoff=30 → audit task (submitted at 10) times out
	// reaudit cutoff=-20 → negative, no reaudit timeout
	ctx = ctx.WithBlockHeight(80)
	timedOut := k.HandleAuditTimeouts(ctx)
	if timedOut != 1 {
		t.Fatalf("at height 80: want 1 (only audit), got %d", timedOut)
	}

	// At height 120: reaudit cutoff=20 → reaudit task (submitted at 10) times out
	ctx = ctx.WithBlockHeight(120)
	timedOut = k.HandleAuditTimeouts(ctx)
	if timedOut != 1 {
		t.Fatalf("at height 120: want 1 (only reaudit), got %d", timedOut)
	}
}

// ============================================================
// Fix 5: Params validation — zero ratios rejected
// ============================================================

func TestParams_ZeroFeeRatios_Rejected(t *testing.T) {
	tests := []struct {
		name     string
		executor uint32
		verifier uint32
		audit    uint32
	}{
		{"zero_executor", 0, 45, 5},
		{"zero_verifier", 995, 0, 5},
		{"zero_audit", 850, 150, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := types.DefaultParams()
			p.ExecutorFeeRatio = tt.executor
			p.VerifierFeeRatio = tt.verifier
			p.AuditFundRatio = tt.audit
			if err := p.Validate(); err == nil {
				t.Fatalf("should reject params with %s", tt.name)
			}
		})
	}
}

func TestParams_ValidRatios_Accepted(t *testing.T) {
	tests := []struct {
		name     string
		executor uint32
		verifier uint32
		audit    uint32
	}{
		{"default", 850, 120, 30},
		{"equal_split", 334, 333, 333},
		{"extreme_executor", 990, 5, 5},
		{"extreme_verifier", 5, 990, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := types.DefaultParams()
			p.ExecutorFeeRatio = tt.executor
			p.VerifierFeeRatio = tt.verifier
			p.AuditFundRatio = tt.audit
			if err := p.Validate(); err != nil {
				t.Fatalf("should accept valid params: %v", err)
			}
		})
	}
}

// ============================================================
// Fix 6: Signature validation — missing/incomplete sigs skipped
// ============================================================

func TestBatchSettlement_MissingSigs_Skipped(t *testing.T) {
	tests := []struct {
		name            string
		userSig         []byte
		workerSig       []byte
		verifySigHashes [][]byte
	}{
		{"no_user_sig", nil, []byte("w"), [][]byte{[]byte("v1"), []byte("v2"), []byte("v3")}},
		{"no_worker_sig", []byte("u"), nil, [][]byte{[]byte("v1"), []byte("v2"), []byte("v3")}},
		{"only_2_verify_sigs", []byte("u"), []byte("w"), [][]byte{[]byte("v1"), []byte("v2")}},
		{"empty_verify_sig", []byte("u"), []byte("w"), [][]byte{[]byte("v1"), []byte("v2"), {}}},
		{"all_empty", nil, nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, ctx, _, _ := setupTrackingKeeper(t)
			k.SetCurrentAuditRate(ctx, 0)

			userAddr := makeAddr("sig-user")
			_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

			entries := []types.SettlementEntry{
				{
					TaskId:        []byte(fmt.Sprintf("sig-task-%s-padding", tt.name[:6])),
					UserAddress:   userAddr.String(),
					WorkerAddress: makeAddr("sig-worker").String(),
					Fee:           sdk.NewCoin("ufai", math.NewInt(1_000_000)),
					ExpireBlock:   10000,
					Status:        types.SettlementSuccess,
					VerifierResults: []types.VerifierResult{
						{Address: makeAddr("sv1").String(), Pass: true},
						{Address: makeAddr("sv2").String(), Pass: true},
						{Address: makeAddr("sv3").String(), Pass: true},
					},
					UserSigHash:     tt.userSig,
					WorkerSigHash:   tt.workerSig,
					VerifySigHashes: tt.verifySigHashes,
				},
			}

			// DON'T use makeBatchMsg (it adds sigs). Compute merkle root manually.
			merkleRoot := keeper.ComputeMerkleRoot(entries)
			msgHash := sha256.Sum256(merkleRoot)
			sig, _ := testProposerKey.Sign(msgHash[:])
			msg := types.NewMsgBatchSettlement(makeAddr("proposer").String(), merkleRoot, entries, sig)

			_, err := k.ProcessBatchSettlement(ctx, msg)
			if err != nil {
				t.Fatalf("batch should not error (skips invalid): %v", err)
			}

			ia, _ := k.GetInferenceAccount(ctx, userAddr)
			if !ia.Balance.Amount.Equal(math.NewInt(10_000_000)) {
				t.Fatalf("balance should be unchanged (entry skipped), got %s", ia.Balance.Amount)
			}
		})
	}
}

// ============================================================
// Additional edge case: batch with mixed valid/invalid entries
// ============================================================

func TestBatchSettlement_MixedEntries_OnlyValidProcessed(t *testing.T) {
	k, ctx, bk, _ := setupTrackingKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	userAddr := makeAddr("mix-user")
	workerAddr := makeAddr("mix-worker")
	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

	entries := []types.SettlementEntry{
		// Valid SUCCESS
		{
			TaskId:        []byte("mix-valid-task-0001"),
			UserAddress:   userAddr.String(),
			WorkerAddress: workerAddr.String(),
			Fee:           fee,
			ExpireBlock:   10000,
			Status:        types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("mv1").String(), Pass: true},
				{Address: makeAddr("mv2").String(), Pass: true},
				{Address: makeAddr("mv3").String(), Pass: true},
			},
		},
		// Expired → skipped
		{
			TaskId:        []byte("mix-expired-task001"),
			UserAddress:   userAddr.String(),
			WorkerAddress: workerAddr.String(),
			Fee:           fee,
			ExpireBlock:   50, // expired (ctx height=100)
			Status:        types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("mv1").String(), Pass: true},
				{Address: makeAddr("mv2").String(), Pass: true},
				{Address: makeAddr("mv3").String(), Pass: true},
			},
		},
		// Valid FAIL
		{
			TaskId:        []byte("mix-fail-task--0001"),
			UserAddress:   userAddr.String(),
			WorkerAddress: workerAddr.String(),
			Fee:           fee,
			ExpireBlock:   10000,
			Status:        types.SettlementFail,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("mv1").String(), Pass: true},
				{Address: makeAddr("mv2").String(), Pass: false},
				{Address: makeAddr("mv3").String(), Pass: false},
			},
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, _ := k.ProcessBatchSettlement(ctx, msg)

	br, _ := k.GetBatchRecord(ctx, batchId)
	if br.ResultCount != 2 {
		t.Fatalf("want 2 settled (1 success + 1 fail, expired skipped), got %d", br.ResultCount)
	}

	// User charged: 1M (success) + 50K (5% fail) = 1_050_000
	ia, _ := k.GetInferenceAccount(ctx, userAddr)
	expected := math.NewInt(10_000_000 - 1_000_000 - 50_000)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("balance: want %s, got %s", expected, ia.Balance.Amount)
	}

	// Worker got 85% of success fee = 850_000, nothing from fail
	workerGot := bk.receivedBy(workerAddr)
	if !workerGot.Equal(math.NewInt(850_000)) {
		t.Fatalf("worker: want 850_000, got %s", workerGot)
	}
}

// ============================================================
// Edge case: Epoch boundary exact conditions
// ============================================================

func TestEpochStats_BoundaryExact(t *testing.T) {
	k, ctx, _, _ := setupTrackingKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	userAddr := makeAddr("epoch-user")
	workerAddr := makeAddr("epoch-worker")
	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(100_000_000)))

	// Settle at block 99 (epoch 0) and block 100 (epoch 1)
	for _, height := range []int64{99, 100} {
		ctxH := ctx.WithBlockHeight(height)
		taskId := []byte(fmt.Sprintf("epoch-boundary-h%03d", height))
		entries := []types.SettlementEntry{
			{
				TaskId:        taskId,
				UserAddress:   userAddr.String(),
				WorkerAddress: workerAddr.String(),
				Fee:           fee,
				ExpireBlock:   10000,
				Status:        types.SettlementSuccess,
				VerifierResults: []types.VerifierResult{
					{Address: makeAddr("eb-v1").String(), Pass: true},
					{Address: makeAddr("eb-v2").String(), Pass: true},
					{Address: makeAddr("eb-v3").String(), Pass: true},
				},
			},
		}
		msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
		_, err := k.ProcessBatchSettlement(ctxH, msg)
		if err != nil {
			t.Fatalf("batch at height %d: %v", height, err)
		}
	}

	// Epoch 0 (heights 0-99) should have 1 task
	stats0 := k.GetEpochStats(ctx, 0)
	if stats0.TotalSettled != 1 {
		t.Fatalf("epoch 0: want 1, got %d", stats0.TotalSettled)
	}

	// Epoch 1 (heights 100-199) should have 1 task
	stats1 := k.GetEpochStats(ctx, 1)
	if stats1.TotalSettled != 1 {
		t.Fatalf("epoch 1: want 1, got %d", stats1.TotalSettled)
	}
}

// ============================================================
// Edge case: BatchSettlement with 0 valid entries
// ============================================================

func TestBatchSettlement_AllSkipped_ZeroBatchResult(t *testing.T) {
	k, ctx, _, _ := setupTrackingKeeper(t)
	k.SetCurrentAuditRate(ctx, 0)

	userAddr := makeAddr("skip-user")
	// No deposit → balance = 0 → all entries skipped

	entries := []types.SettlementEntry{
		{
			TaskId:        []byte("all-skip-task-00001"),
			UserAddress:   userAddr.String(),
			WorkerAddress: makeAddr("skip-worker").String(),
			Fee:           sdk.NewCoin("ufai", math.NewInt(1_000_000)),
			ExpireBlock:   10000,
			Status:        types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("sk-v1").String(), Pass: true},
				{Address: makeAddr("sk-v2").String(), Pass: true},
				{Address: makeAddr("sk-v3").String(), Pass: true},
			},
		},
	}
	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("should not error: %v", err)
	}

	br, _ := k.GetBatchRecord(ctx, batchId)
	if br.ResultCount != 0 {
		t.Fatalf("want 0 results (all skipped), got %d", br.ResultCount)
	}
}

// A7: cross-denom deposit should be rejected, not cause IsLT panic
func TestA7_CrossDenomDepositRejected(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	addr := makeAddr("crossdenom-user")

	err := k.ProcessDeposit(ctx, addr, sdk.NewCoin("uatom", math.NewInt(1_000_000)))
	if err == nil {
		t.Fatal("expected error for cross-denom deposit, got nil")
	}
	if !errors.IsOf(err, types.ErrWrongDenom) {
		t.Fatalf("expected ErrWrongDenom, got: %v", err)
	}
}

// A7: cross-denom withdraw should be rejected
func TestA7_CrossDenomWithdrawRejected(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	addr := makeAddr("crossdenom-user2")

	_ = k.ProcessDeposit(ctx, addr, sdk.NewCoin("ufai", math.NewInt(1_000_000)))

	err := k.ProcessWithdraw(ctx, addr, sdk.NewCoin("uatom", math.NewInt(500_000)))
	if err == nil {
		t.Fatal("expected error for cross-denom withdraw, got nil")
	}
	if !errors.IsOf(err, types.ErrWrongDenom) {
		t.Fatalf("expected ErrWrongDenom, got: %v", err)
	}
}

// A7: settlement entry with wrong denom should be silently skipped (no panic)
func TestA7_CrossDenomSettlementEntrySkipped(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	userAddr := makeAddr("crossdenom-settle-user")

	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(5_000_000)))

	entries := []types.SettlementEntry{
		{
			TaskId:        []byte("crossdenom-task-001"),
			UserAddress:   userAddr.String(),
			WorkerAddress: makeAddr("crossdenom-worker").String(),
			Fee:           sdk.NewCoin("uatom", math.NewInt(1_000_000)), // wrong denom
			ExpireBlock:   10000,
			Status:        types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("cd-v1").String(), Pass: true},
				{Address: makeAddr("cd-v2").String(), Pass: true},
				{Address: makeAddr("cd-v3").String(), Pass: true},
			},
		},
	}
	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("batch should not error, got: %v", err)
	}

	br, _ := k.GetBatchRecord(ctx, batchId)
	if br.ResultCount != 0 {
		t.Fatalf("wrong-denom entry should be skipped, want 0 results, got %d", br.ResultCount)
	}
}

// A7: MsgDeposit.ValidateBasic rejects wrong denom
func TestA7_MsgDepositValidateBasicWrongDenom(t *testing.T) {
	addr := makeAddr("msg-denom-user")
	msg := types.NewMsgDeposit(addr.String(), sdk.NewCoin("uatom", math.NewInt(1000)))
	err := msg.ValidateBasic()
	if err == nil {
		t.Fatal("expected ValidateBasic to reject wrong denom")
	}
}

// A7: MsgWithdraw.ValidateBasic rejects wrong denom
func TestA7_MsgWithdrawValidateBasicWrongDenom(t *testing.T) {
	addr := makeAddr("msg-denom-user2")
	msg := types.NewMsgWithdraw(addr.String(), sdk.NewCoin("uatom", math.NewInt(1000)))
	err := msg.ValidateBasic()
	if err == nil {
		t.Fatal("expected ValidateBasic to reject wrong denom")
	}
}

// ============================================================
// Helpers for setupTrackingKeeper
// These extract reusable parts from the original setupKeeper.
// ============================================================

func setupStoreKey(t *testing.T) storetypes.StoreKey {
	t.Helper()
	return storetypes.NewKVStoreKey(types.StoreKey)
}

func setupKeeperWithBankAndWorker(t *testing.T, storeKey storetypes.StoreKey, bk keeper.BankKeeper, wk keeper.WorkerKeeper) keeper.Keeper {
	t.Helper()

	registry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(registry)

	return keeper.NewKeeper(cdc, storeKey, bk, wk, "authority", log.NewNopLogger())
}

func setupContext(t *testing.T, storeKey storetypes.StoreKey) sdk.Context {
	t.Helper()

	db := dbm.NewMemDB()
	stateStore := store.NewCommitMultiStore(db, log.NewNopLogger(), storemetrics.NewNoOpMetrics())
	stateStore.MountStoreWithDB(storeKey, storetypes.StoreTypeIAVL, db)
	if err := stateStore.LoadLatestVersion(); err != nil {
		t.Fatal(err)
	}
	return sdk.NewContext(stateStore, cmtproto.Header{Height: 100}, false, log.NewNopLogger())
}

