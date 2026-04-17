package keeper_test

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

	"github.com/funai-wiki/funai-chain/x/settlement/keeper"
	"github.com/funai-wiki/funai-chain/x/settlement/types"
)

// testProposerKey is a fixed secp256k1 key pair used for test signing.
var testProposerKey = secp256k1.GenPrivKey()

// -------- Mocks --------

type mockBankKeeper struct {
	balances map[string]sdk.Coins
}

func newMockBankKeeper() *mockBankKeeper {
	return &mockBankKeeper{balances: make(map[string]sdk.Coins)}
}

func (m *mockBankKeeper) SendCoins(_ context.Context, _, _ sdk.AccAddress, _ sdk.Coins) error {
	return nil
}

func (m *mockBankKeeper) SendCoinsFromAccountToModule(_ context.Context, _ sdk.AccAddress, _ string, _ sdk.Coins) error {
	return nil
}

func (m *mockBankKeeper) SendCoinsFromModuleToAccount(_ context.Context, _ string, _ sdk.AccAddress, _ sdk.Coins) error {
	return nil
}

type mockWorkerKeeper struct {
	jailCalls   []sdk.AccAddress
	slashCalls  []sdk.AccAddress
	streakCalls []sdk.AccAddress
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

func (m *mockWorkerKeeper) UpdateWorkerStats(_ sdk.Context, _ sdk.AccAddress, _ sdk.Coin) {}

func (m *mockWorkerKeeper) TombstoneWorker(_ sdk.Context, _ sdk.AccAddress) {}

func (m *mockWorkerKeeper) ReputationOnAccept(_ sdk.Context, _ sdk.AccAddress) {}

func (m *mockWorkerKeeper) UpdateAvgLatency(_ sdk.Context, _ sdk.AccAddress, _ uint32) {}

func (m *mockWorkerKeeper) GetWorkerPubkey(_ sdk.Context, _ sdk.AccAddress) (string, bool) {
	// P1-6: return the test proposer's pubkey for signature verification.
	return string(testProposerKey.PubKey().Bytes()), true
}

// signFraudContent creates a properly signed ContentHash+WorkerContentSig pair for FraudProof tests.
func signFraudContent(t *testing.T, content []byte) (contentHash []byte, sig []byte) {
	t.Helper()
	h := sha256.Sum256(content)
	s, err := testProposerKey.Sign(h[:])
	if err != nil {
		t.Fatalf("failed to sign fraud content: %v", err)
	}
	return h[:], s
}

// -------- Helpers --------

func setupKeeper(t *testing.T) (keeper.Keeper, sdk.Context, *mockBankKeeper, *mockWorkerKeeper) {
	t.Helper()

	storeKey := storetypes.NewKVStoreKey(types.StoreKey)

	db := dbm.NewMemDB()
	stateStore := store.NewCommitMultiStore(db, log.NewNopLogger(), storemetrics.NewNoOpMetrics())
	stateStore.MountStoreWithDB(storeKey, storetypes.StoreTypeIAVL, db)
	if err := stateStore.LoadLatestVersion(); err != nil {
		t.Fatal(err)
	}

	registry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(registry)

	bk := newMockBankKeeper()
	wk := newMockWorkerKeeper()

	k := keeper.NewKeeper(cdc, storeKey, bk, wk, "authority", log.NewNopLogger())

	ctx := sdk.NewContext(stateStore, cmtproto.Header{Height: 100}, false, log.NewNopLogger())
	k.SetParams(ctx, types.DefaultParams())

	return k, ctx, bk, wk
}

func makeAddr(name string) sdk.AccAddress {
	buf := make([]byte, 20)
	copy(buf, name)
	return sdk.AccAddress(buf)
}

func fillTestSigHashes(entries []types.SettlementEntry) []types.SettlementEntry {
	dummySig := []byte("test-sig-hash-32-bytes-padding!!")
	for i := range entries {
		if len(entries[i].UserSigHash) == 0 {
			entries[i].UserSigHash = dummySig
		}
		if len(entries[i].WorkerSigHash) == 0 {
			entries[i].WorkerSigHash = dummySig
		}
		if len(entries[i].VerifySigHashes) < 3 {
			entries[i].VerifySigHashes = [][]byte{dummySig, dummySig, dummySig}
		}
	}
	return entries
}

func makeBatchMsg(t *testing.T, proposer string, entries []types.SettlementEntry) *types.MsgBatchSettlement {
	t.Helper()

	entries = fillTestSigHashes(entries)
	merkleRoot := keeper.ComputeMerkleRoot(entries)

	// P1-6: sign the merkle root with the test proposer key for proper signature verification.
	msgHash := sha256.Sum256(merkleRoot)
	sig, err := testProposerKey.Sign(msgHash[:])
	if err != nil {
		t.Fatalf("failed to sign merkle root: %v", err)
	}

	return types.NewMsgBatchSettlement(
		proposer,
		merkleRoot,
		entries,
		sig,
	)
}

// -------- Deposit Tests --------

func TestProcessDeposit_Success(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	addr := makeAddr("user1")
	amount := sdk.NewCoin("ufai", math.NewInt(1_000_000))

	if err := k.ProcessDeposit(ctx, addr, amount); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ia, found := k.GetInferenceAccount(ctx, addr)
	if !found {
		t.Fatal("inference account not found after deposit")
	}
	if !ia.Balance.Equal(amount) {
		t.Fatalf("expected balance %s, got %s", amount, ia.Balance)
	}
}

func TestProcessDeposit_NewAccount(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	addr := makeAddr("brand-new-user")

	_, found := k.GetInferenceAccount(ctx, addr)
	if found {
		t.Fatal("account should not exist before first deposit")
	}

	amount := sdk.NewCoin("ufai", math.NewInt(500_000))
	if err := k.ProcessDeposit(ctx, addr, amount); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ia, found := k.GetInferenceAccount(ctx, addr)
	if !found {
		t.Fatal("account should exist after first deposit")
	}
	if ia.Address != addr.String() {
		t.Fatalf("expected address %s, got %s", addr.String(), ia.Address)
	}
	if !ia.Balance.Equal(amount) {
		t.Fatalf("expected balance %s, got %s", amount, ia.Balance)
	}
}

func TestProcessDeposit_AddToExisting(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	addr := makeAddr("user1")
	_ = k.ProcessDeposit(ctx, addr, sdk.NewCoin("ufai", math.NewInt(1_000_000)))
	_ = k.ProcessDeposit(ctx, addr, sdk.NewCoin("ufai", math.NewInt(500_000)))

	ia, found := k.GetInferenceAccount(ctx, addr)
	if !found {
		t.Fatal("inference account not found")
	}
	expected := sdk.NewCoin("ufai", math.NewInt(1_500_000))
	if !ia.Balance.Equal(expected) {
		t.Fatalf("expected balance %s, got %s", expected, ia.Balance)
	}
}

// -------- Withdraw Tests --------

func TestProcessWithdraw_Success(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	addr := makeAddr("user1")
	_ = k.ProcessDeposit(ctx, addr, sdk.NewCoin("ufai", math.NewInt(1_000_000)))

	if err := k.ProcessWithdraw(ctx, addr, sdk.NewCoin("ufai", math.NewInt(400_000))); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ia, _ := k.GetInferenceAccount(ctx, addr)
	expected := sdk.NewCoin("ufai", math.NewInt(600_000))
	if !ia.Balance.Equal(expected) {
		t.Fatalf("expected balance %s, got %s", expected, ia.Balance)
	}
}

func TestProcessWithdraw_InsufficientBalance(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	addr := makeAddr("user1")
	_ = k.ProcessDeposit(ctx, addr, sdk.NewCoin("ufai", math.NewInt(1_000)))

	err := k.ProcessWithdraw(ctx, addr, sdk.NewCoin("ufai", math.NewInt(2_000)))
	if err == nil {
		t.Fatal("expected error for insufficient balance")
	}

	ia, _ := k.GetInferenceAccount(ctx, addr)
	if !ia.Balance.Amount.Equal(math.NewInt(1_000)) {
		t.Fatalf("balance should be unchanged, got %s", ia.Balance)
	}
}

func TestProcessWithdraw_AccountNotFound(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	addr := makeAddr("nobody")
	err := k.ProcessWithdraw(ctx, addr, sdk.NewCoin("ufai", math.NewInt(100)))
	if err == nil {
		t.Fatal("expected error for account not found")
	}
}

// A1: Withdraw must respect frozen balance
func TestWithdraw_FrozenBalance(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	addr := makeAddr("a1-user")
	_ = k.ProcessDeposit(ctx, addr, sdk.NewCoin("ufai", math.NewInt(1_000_000)))

	// Freeze 800K (simulating 4 active per-token tasks)
	for i := 0; i < 4; i++ {
		taskId := []byte(fmt.Sprintf("a1-freeze-task-%05d", i))
		err := k.FreezeBalance(ctx, addr, taskId, sdk.NewCoin("ufai", math.NewInt(200_000)))
		if err != nil {
			t.Fatalf("FreezeBalance %d: %v", i, err)
		}
	}

	// Available = 1M - 800K = 200K
	ia, _ := k.GetInferenceAccount(ctx, addr)
	avail := ia.AvailableBalance()
	if !avail.Amount.Equal(math.NewInt(200_000)) {
		t.Fatalf("expected available 200000, got %s", avail.Amount)
	}

	// Withdraw 500K should FAIL (only 200K available)
	err := k.ProcessWithdraw(ctx, addr, sdk.NewCoin("ufai", math.NewInt(500_000)))
	if err == nil {
		t.Fatal("A1: withdraw of frozen funds should be rejected")
	}

	// Withdraw 200K should SUCCEED (exactly available)
	err = k.ProcessWithdraw(ctx, addr, sdk.NewCoin("ufai", math.NewInt(200_000)))
	if err != nil {
		t.Fatalf("A1: withdraw of available balance should succeed: %v", err)
	}

	// Verify balance
	ia2, _ := k.GetInferenceAccount(ctx, addr)
	if !ia2.Balance.Amount.Equal(math.NewInt(800_000)) {
		t.Fatalf("A1: expected balance 800000 after withdraw, got %s", ia2.Balance.Amount)
	}

	// Withdraw 1 more should FAIL (all remaining is frozen)
	err = k.ProcessWithdraw(ctx, addr, sdk.NewCoin("ufai", math.NewInt(1)))
	if err == nil {
		t.Fatal("A1: withdraw from fully frozen balance should be rejected")
	}
}

// -------- BatchSettlement Tests --------

func TestProcessBatchSettlement_Success(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("user1")
	workerAddr := makeAddr("worker1")
	v1 := makeAddr("verifier1")
	v2 := makeAddr("verifier2")
	v3 := makeAddr("verifier3")

	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	_ = k.ProcessDeposit(ctx, userAddr, fee)

	entries := []types.SettlementEntry{
		{
			TaskId:        []byte("task-success-001-pad"),
			UserAddress:   userAddr.String(),
			WorkerAddress: workerAddr.String(),
			Fee:           fee,
			ExpireBlock:   200,
			Status:        types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: v1.String(), Pass: true},
				{Address: v2.String(), Pass: true},
				{Address: v3.String(), Pass: true},
			},
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)

	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batchId == 0 {
		t.Fatal("expected non-zero batch_id")
	}

	ia, _ := k.GetInferenceAccount(ctx, userAddr)
	if !ia.Balance.IsZero() {
		t.Fatalf("expected zero balance after full fee deduction, got %s", ia.Balance)
	}

	if len(wk.streakCalls) != 1 {
		t.Fatalf("expected 1 IncrementSuccessStreak call, got %d", len(wk.streakCalls))
	}
	if !wk.streakCalls[0].Equals(workerAddr) {
		t.Fatalf("streak called for wrong worker")
	}

	st, found := k.GetSettledTask(ctx, entries[0].TaskId)
	if !found {
		t.Fatal("settled task record should exist")
	}
	if st.Status != types.TaskSettled {
		t.Fatalf("expected TaskSettled status, got %s", st.Status)
	}
}

func TestProcessBatchSettlement_Fail(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("user1")
	workerAddr := makeAddr("worker1")
	v1 := makeAddr("verifier1")
	v2 := makeAddr("verifier2")
	v3 := makeAddr("verifier3")

	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(2_000_000)))

	entries := []types.SettlementEntry{
		{
			TaskId:        []byte("task-fail-001--pad-"),
			UserAddress:   userAddr.String(),
			WorkerAddress: workerAddr.String(),
			Fee:           fee,
			ExpireBlock:   200,
			Status:        types.SettlementFail,
			VerifierResults: []types.VerifierResult{
				{Address: v1.String(), Pass: true},
				{Address: v2.String(), Pass: true},
				{Address: v3.String(), Pass: false},
			},
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)

	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(wk.jailCalls) != 1 {
		t.Fatalf("expected 1 JailWorker call for FAIL, got %d", len(wk.jailCalls))
	}
	if !wk.jailCalls[0].Equals(workerAddr) {
		t.Fatalf("jail called for wrong worker")
	}

	// FAIL fee = fee * 150/1000 = 150_000 (15%)
	ia, _ := k.GetInferenceAccount(ctx, userAddr)
	failFee := math.NewInt(1_000_000).MulRaw(150).QuoRaw(1000)
	expected := math.NewInt(2_000_000).Sub(failFee)
	if !ia.Balance.Amount.Equal(expected) {
		t.Fatalf("expected balance %s, got %s", expected, ia.Balance.Amount)
	}

	st, found := k.GetSettledTask(ctx, entries[0].TaskId)
	if !found {
		t.Fatal("settled task record should exist for FAIL")
	}
	if st.Status != types.TaskFailSettled {
		t.Fatalf("expected TaskFailSettled status, got %s", st.Status)
	}
}

func TestProcessBatchSettlement_DuplicateTaskId(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("user1")
	workerAddr := makeAddr("worker1")
	taskId := []byte("dup-task-001-padded")

	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(5_000_000)))

	k.SetSettledTask(ctx, types.SettledTaskID{
		TaskId:    taskId,
		Status:    types.TaskSettled,
		SettledAt: 50,
	})

	entries := []types.SettlementEntry{
		{
			TaskId:        taskId,
			UserAddress:   userAddr.String(),
			WorkerAddress: workerAddr.String(),
			Fee:           sdk.NewCoin("ufai", math.NewInt(1_000_000)),
			ExpireBlock:   200,
			Status:        types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("v1").String(), Pass: true},
				{Address: makeAddr("v2").String(), Pass: true},
				{Address: makeAddr("v3").String(), Pass: true},
			},
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)

	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ia, _ := k.GetInferenceAccount(ctx, userAddr)
	if !ia.Balance.Amount.Equal(math.NewInt(5_000_000)) {
		t.Fatalf("balance should be unchanged for duplicate task, got %s", ia.Balance)
	}
}

func TestProcessBatchSettlement_FraudMarked(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("user1")
	workerAddr := makeAddr("worker1")
	taskId := []byte("fraud-task-001-pad-")

	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(5_000_000)))

	k.SetFraudMark(ctx, taskId)

	entries := []types.SettlementEntry{
		{
			TaskId:        taskId,
			UserAddress:   userAddr.String(),
			WorkerAddress: workerAddr.String(),
			Fee:           sdk.NewCoin("ufai", math.NewInt(1_000_000)),
			ExpireBlock:   200,
			Status:        types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("v1").String(), Pass: true},
				{Address: makeAddr("v2").String(), Pass: true},
				{Address: makeAddr("v3").String(), Pass: true},
			},
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)

	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ia, _ := k.GetInferenceAccount(ctx, userAddr)
	if !ia.Balance.Amount.Equal(math.NewInt(5_000_000)) {
		t.Fatalf("balance should be unchanged for fraud-marked task, got %s", ia.Balance)
	}
}

func TestProcessBatchSettlement_Expired(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("user1")
	workerAddr := makeAddr("worker1")

	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(5_000_000)))

	entries := []types.SettlementEntry{
		{
			TaskId:        []byte("expired-task-01-pad"),
			UserAddress:   userAddr.String(),
			WorkerAddress: workerAddr.String(),
			Fee:           sdk.NewCoin("ufai", math.NewInt(1_000_000)),
			ExpireBlock:   50, // ctx height is 100 → expired
			Status:        types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("v1").String(), Pass: true},
				{Address: makeAddr("v2").String(), Pass: true},
				{Address: makeAddr("v3").String(), Pass: true},
			},
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)

	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ia, _ := k.GetInferenceAccount(ctx, userAddr)
	if !ia.Balance.Amount.Equal(math.NewInt(5_000_000)) {
		t.Fatalf("balance should be unchanged for expired task, got %s", ia.Balance)
	}
}

// -------- FraudProof Tests --------

func TestProcessFraudProof_Success(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)

	workerAddr := makeAddr("worker1")
	taskId := []byte("fraud-proof-task-01")

	contentHash, contentSig := signFraudContent(t, []byte("content"))
	msg := &types.MsgFraudProof{
		Reporter:         makeAddr("reporter").String(),
		TaskId:           taskId,
		WorkerAddress:    workerAddr.String(),
		ContentHash:      contentHash,
		WorkerContentSig: contentSig,
		ActualContent:    []byte("content"),
	}

	if err := k.ProcessFraudProof(ctx, msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !k.HasFraudMark(ctx, taskId) {
		t.Fatal("fraud mark should be set")
	}

	if len(wk.slashCalls) != 1 {
		t.Fatalf("expected 1 SlashWorker call, got %d", len(wk.slashCalls))
	}
	if !wk.slashCalls[0].Equals(workerAddr) {
		t.Fatal("slash called for wrong worker")
	}
}

func TestProcessFraudProof_AlreadyMarked(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	taskId := []byte("already-marked-task")
	k.SetFraudMark(ctx, taskId)

	msg := &types.MsgFraudProof{
		Reporter:         makeAddr("reporter").String(),
		TaskId:           taskId,
		WorkerAddress:    makeAddr("worker1").String(),
		ContentHash:      []byte("hash"),
		WorkerContentSig: []byte("sig"),
		ActualContent:    []byte("content"),
	}

	err := k.ProcessFraudProof(ctx, msg)
	if err == nil {
		t.Fatal("expected error for already-marked fraud")
	}
}

// -------- SecondVerificationResult Tests --------

func TestProcessSecondVerificationResult_PassMajority(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)

	taskId := []byte("audit-pass-task-001")
	workerAddr := makeAddr("worker1")
	verifierAddr := makeAddr("verifier1")

	k.SetSettledTask(ctx, types.SettledTaskID{
		TaskId:            taskId,
		Status:            types.TaskSettled,
		SettledAt:         50,
		WorkerAddress:     workerAddr.String(),
		OriginalVerifiers: []string{verifierAddr.String()},
	})

	second_verifiers := []sdk.AccAddress{
		makeAddr("second_verifier1"),
		makeAddr("second_verifier2"),
		makeAddr("second_verifier3"),
	}

	// 2/3 PASS → majority passes (threshold is 2)
	passFlags := []bool{true, true, false}
	for i, second_verifier := range second_verifiers {
		err := k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
			SecondVerifier: second_verifier.String(),
			TaskId:         taskId,
			Epoch:          1,
			Pass:           passFlags[i],
			LogitsHash:     []byte("hash"),
		})
		if err != nil {
			t.Fatalf("audit result %d: unexpected error: %v", i, err)
		}
	}

	if len(wk.jailCalls) != 0 {
		t.Fatalf("expected 0 jail calls for PASS majority, got %d", len(wk.jailCalls))
	}
}

func TestProcessSecondVerificationResult_FailMajority(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)

	taskId := []byte("audit-fail-task-001")
	workerAddr := makeAddr("worker1")
	verifierAddr := makeAddr("verifier1")
	userAddr := makeAddr("user1")

	k.SetSettledTask(ctx, types.SettledTaskID{
		TaskId:            taskId,
		Status:            types.TaskSettled,
		SettledAt:         50,
		WorkerAddress:     workerAddr.String(),
		OriginalVerifiers: []string{verifierAddr.String()},
	})

	// V5.2: processAuditJudgment requires SecondVerificationPendingTask to exist
	k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
		TaskId:              taskId,
		OriginalStatus:      types.SettlementSuccess,
		SubmittedAt:         ctx.BlockHeight(),
		UserAddress:         userAddr.String(),
		WorkerAddress:       workerAddr.String(),
		VerifierAddresses:   []string{verifierAddr.String()},
		Fee:                 sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:         200,
		IsThirdVerification: false,
	})

	second_verifiers := []sdk.AccAddress{
		makeAddr("second_verifier1"),
		makeAddr("second_verifier2"),
		makeAddr("second_verifier3"),
	}

	// 1/3 PASS → fail majority (below threshold of 2)
	passFlags := []bool{true, false, false}
	for i, second_verifier := range second_verifiers {
		err := k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
			SecondVerifier: second_verifier.String(),
			TaskId:         taskId,
			Epoch:          1,
			Pass:           passFlags[i],
			LogitsHash:     []byte("hash"),
		})
		if err != nil {
			t.Fatalf("audit result %d: unexpected error: %v", i, err)
		}
	}

	// Worker + 1 original verifier = at least 2 jail calls
	if len(wk.jailCalls) < 2 {
		t.Fatalf("expected at least 2 jail calls (worker + verifier), got %d", len(wk.jailCalls))
	}
}

// -------- CleanupExpiredTasks Tests --------

func TestCleanupExpiredTasks(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	// cutoff = 5000 - 1000(TaskCleanupBuffer) = 4000
	ctx = ctx.WithBlockHeight(5000)

	k.SetSettledTask(ctx, types.SettledTaskID{
		TaskId:      []byte("old-task-to-cleanup"),
		Status:      types.TaskSettled,
		ExpireBlock: 100, // 100 < 4000 → cleaned
		SettledAt:   50,
	})

	k.SetSettledTask(ctx, types.SettledTaskID{
		TaskId:      []byte("new-task-to-retain"),
		Status:      types.TaskSettled,
		ExpireBlock: 4500, // 4500 >= 4000 → retained
		SettledAt:   4400,
	})

	cleaned := k.CleanupExpiredTasks(ctx)
	if cleaned != 1 {
		t.Fatalf("expected 1 cleaned, got %d", cleaned)
	}

	_, found := k.GetSettledTask(ctx, []byte("old-task-to-cleanup"))
	if found {
		t.Fatal("old task should be deleted")
	}

	_, found = k.GetSettledTask(ctx, []byte("new-task-to-retain"))
	if !found {
		t.Fatal("new task should still exist")
	}
}

func TestCleanupExpiredTasks_NothingToClean(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	ctx = ctx.WithBlockHeight(500)

	k.SetSettledTask(ctx, types.SettledTaskID{
		TaskId:      []byte("recent-settled-task"),
		Status:      types.TaskSettled,
		ExpireBlock: 400,
		SettledAt:   300,
	})

	// cutoff = 500 - 1000 = -500; expireBlock 400 is not < -500
	cleaned := k.CleanupExpiredTasks(ctx)
	if cleaned != 0 {
		t.Fatalf("expected 0 cleaned, got %d", cleaned)
	}
}

// -------- Params Tests --------

func TestDefaultParams_Valid(t *testing.T) {
	params := types.DefaultParams()
	if err := params.Validate(); err != nil {
		t.Fatalf("default params should be valid: %v", err)
	}

	if params.ExecutorFeeRatio != 850 {
		t.Fatalf("expected executor ratio 850, got %d", params.ExecutorFeeRatio)
	}
	if params.VerifierFeeRatio != 120 {
		t.Fatalf("expected verifier ratio 120, got %d", params.VerifierFeeRatio)
	}
	if params.MultiVerificationFundRatio != 30 {
		t.Fatalf("expected audit fund ratio 30, got %d", params.MultiVerificationFundRatio)
	}
	if params.ExecutorFeeRatio+params.VerifierFeeRatio+params.MultiVerificationFundRatio != 1000 {
		t.Fatal("fee ratios should sum to 1000")
	}
}

func TestParams_InvalidFeeRatios(t *testing.T) {
	params := types.DefaultParams()
	params.ExecutorFeeRatio = 900 // 900+120+30 = 1050 ≠ 1000
	if err := params.Validate(); err == nil {
		t.Fatal("params with invalid fee ratios should fail validation")
	}
}

func TestParams_InvalidSignatureExpireMax(t *testing.T) {
	params := types.DefaultParams()
	params.SignatureExpireMax = 0
	if err := params.Validate(); err == nil {
		t.Fatal("params with zero SignatureExpireMax should fail validation")
	}
}

func TestParams_InvalidFailSettlementFeeRatio(t *testing.T) {
	params := types.DefaultParams()
	params.FailSettlementFeeRatio = 0
	if err := params.Validate(); err == nil {
		t.Fatal("params with zero FailSettlementFeeRatio should fail validation")
	}
}

func TestParams_InvalidSecondVerificationMatchThreshold(t *testing.T) {
	params := types.DefaultParams()
	params.SecondVerificationMatchThreshold = 0
	if err := params.Validate(); err == nil {
		t.Fatal("params with zero SecondVerificationMatchThreshold should fail validation")
	}

	params = types.DefaultParams()
	params.SecondVerificationMatchThreshold = params.SecondVerifierCount + 1
	if err := params.Validate(); err == nil {
		t.Fatal("params with SecondVerificationMatchThreshold > SecondVerifierCount should fail validation")
	}
}

func TestParams_InvalidLogitsMatchRequired(t *testing.T) {
	params := types.DefaultParams()
	params.LogitsMatchRequired = 0
	if err := params.Validate(); err == nil {
		t.Fatal("params with zero LogitsMatchRequired should fail validation")
	}

	params = types.DefaultParams()
	params.LogitsMatchRequired = params.LogitsSamplePositions + 1
	if err := params.Validate(); err == nil {
		t.Fatal("params with LogitsMatchRequired > LogitsSamplePositions should fail validation")
	}
}

// -------- CRUD Tests --------

func TestSetAndGetParams(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	params := k.GetParams(ctx)
	if params.ExecutorFeeRatio != 850 {
		t.Fatalf("expected default executor ratio 850, got %d", params.ExecutorFeeRatio)
	}

	params.ExecutorFeeRatio = 940
	params.VerifierFeeRatio = 50
	params.MultiVerificationFundRatio = 10
	k.SetParams(ctx, params)

	got := k.GetParams(ctx)
	if got.ExecutorFeeRatio != 940 {
		t.Fatalf("expected 940, got %d", got.ExecutorFeeRatio)
	}
	if got.VerifierFeeRatio != 50 {
		t.Fatalf("expected 50, got %d", got.VerifierFeeRatio)
	}
}

func TestBatchCounter(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	first := k.GetNextBatchId(ctx)
	if first != 1 {
		t.Fatalf("expected initial batch id 1, got %d", first)
	}

	k.SetBatchCounter(ctx, 5)
	next := k.GetNextBatchId(ctx)
	if next != 6 {
		t.Fatalf("expected batch id 6 after setting counter to 5, got %d", next)
	}
}

func TestFraudMark_SetAndCheck(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	taskId := []byte("test-fraud-mark-001")

	if k.HasFraudMark(ctx, taskId) {
		t.Fatal("fraud mark should not exist initially")
	}

	k.SetFraudMark(ctx, taskId)

	if !k.HasFraudMark(ctx, taskId) {
		t.Fatal("fraud mark should exist after setting")
	}
}

func TestSecondVerificationRecord_SetAndGet(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	taskId := []byte("audit-record-task01")
	ar := types.SecondVerificationRecord{
		TaskId:                  taskId,
		Epoch:                   5,
		SecondVerifierAddresses: []string{makeAddr("aud1").String()},
		Results:                 []bool{true},
		ProcessedAt:             100,
	}

	k.SetSecondVerificationRecord(ctx, ar)

	got, found := k.GetSecondVerificationRecord(ctx, taskId)
	if !found {
		t.Fatal("audit record not found")
	}
	if got.Epoch != 5 {
		t.Fatalf("expected epoch 5, got %d", got.Epoch)
	}
	if len(got.SecondVerifierAddresses) != 1 {
		t.Fatalf("expected 1 second_verifier, got %d", len(got.SecondVerifierAddresses))
	}
}

func TestGetAllInferenceAccounts(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	addrs := []sdk.AccAddress{
		makeAddr("acct1"),
		makeAddr("acct2"),
		makeAddr("acct3"),
	}

	for _, addr := range addrs {
		_ = k.ProcessDeposit(ctx, addr, sdk.NewCoin("ufai", math.NewInt(100)))
	}

	all := k.GetAllInferenceAccounts(ctx)
	if len(all) != 3 {
		t.Fatalf("expected 3 accounts, got %d", len(all))
	}
}

func TestBatchRecord_SetAndGet(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	br := types.BatchRecord{
		BatchId:     42,
		Proposer:    makeAddr("proposer").String(),
		MerkleRoot:  []byte("root"),
		ResultCount: 10,
		TotalFees:   sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		SettledAt:   100,
	}

	k.SetBatchRecord(ctx, br)

	got, found := k.GetBatchRecord(ctx, 42)
	if !found {
		t.Fatal("batch record not found")
	}
	if got.ResultCount != 10 {
		t.Fatalf("expected result count 10, got %d", got.ResultCount)
	}
	if got.Proposer != br.Proposer {
		t.Fatalf("proposer mismatch")
	}
}

func TestDeleteSettledTask(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	taskId := []byte("task-to-delete-0001")
	k.SetSettledTask(ctx, types.SettledTaskID{
		TaskId:    taskId,
		Status:    types.TaskSettled,
		SettledAt: 50,
	})

	_, found := k.GetSettledTask(ctx, taskId)
	if !found {
		t.Fatal("settled task should exist before delete")
	}

	k.DeleteSettledTask(ctx, taskId)

	_, found = k.GetSettledTask(ctx, taskId)
	if found {
		t.Fatal("settled task should not exist after delete")
	}
}

// -------- Integration / Edge-case Tests --------

func TestProcessBatchSettlement_MultipleTasks(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)

	// Set audit rate to 0 so no tasks are routed to PENDING_AUDIT
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("user1")
	workerAddr := makeAddr("worker1")

	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(10_000_000)))

	fee := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	verifiers := []types.VerifierResult{
		{Address: makeAddr("v1").String(), Pass: true},
		{Address: makeAddr("v2").String(), Pass: true},
		{Address: makeAddr("v3").String(), Pass: true},
	}

	entries := []types.SettlementEntry{
		{
			TaskId:          []byte("multi-task-001-pad-"),
			UserAddress:     userAddr.String(),
			WorkerAddress:   workerAddr.String(),
			Fee:             fee,
			ExpireBlock:     200,
			Status:          types.SettlementSuccess,
			VerifierResults: verifiers,
		},
		{
			TaskId:          []byte("multi-task-002-pad-"),
			UserAddress:     userAddr.String(),
			WorkerAddress:   workerAddr.String(),
			Fee:             fee,
			ExpireBlock:     200,
			Status:          types.SettlementSuccess,
			VerifierResults: verifiers,
		},
		{
			TaskId:          []byte("multi-task-003-pad-"),
			UserAddress:     userAddr.String(),
			WorkerAddress:   workerAddr.String(),
			Fee:             fee,
			ExpireBlock:     200,
			Status:          types.SettlementFail,
			VerifierResults: verifiers,
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)

	batchId, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if batchId == 0 {
		t.Fatal("expected non-zero batch id")
	}

	// 2 success + 1 fail
	if len(wk.streakCalls) != 2 {
		t.Fatalf("expected 2 streak calls (2 success tasks), got %d", len(wk.streakCalls))
	}
	if len(wk.jailCalls) != 1 {
		t.Fatalf("expected 1 jail call (1 fail task), got %d", len(wk.jailCalls))
	}

	// Check batch record
	br, found := k.GetBatchRecord(ctx, batchId)
	if !found {
		t.Fatal("batch record not found")
	}
	if br.ResultCount != 3 {
		t.Fatalf("expected 3 results, got %d", br.ResultCount)
	}
}

func TestProcessSecondVerificationResult_IgnoresExtraSecondVerifiers(t *testing.T) {
	k, ctx, _, wk := setupKeeper(t)

	taskId := []byte("audit-extra-task001")
	k.SetSettledTask(ctx, types.SettledTaskID{
		TaskId:            taskId,
		Status:            types.TaskSettled,
		SettledAt:         50,
		WorkerAddress:     makeAddr("worker1").String(),
		OriginalVerifiers: []string{makeAddr("v1").String()},
	})

	// Submit 3 PASS audits (threshold met → no jail)
	for i := 0; i < 3; i++ {
		_ = k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
			SecondVerifier: makeAddr("aud" + string(rune('A'+i))).String(),
			TaskId:         taskId,
			Epoch:          1,
			Pass:           true,
			LogitsHash:     []byte("hash"),
		})
	}

	// 4th second_verifier should be ignored (audit already has 3 results)
	_ = k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
		SecondVerifier: makeAddr("aud-extra").String(),
		TaskId:         taskId,
		Epoch:          1,
		Pass:           false,
		LogitsHash:     []byte("hash"),
	})

	ar, found := k.GetSecondVerificationRecord(ctx, taskId)
	if !found {
		t.Fatal("audit record not found")
	}
	if len(ar.Results) != 3 {
		t.Fatalf("expected 3 audit results (extra ignored), got %d", len(ar.Results))
	}

	if len(wk.jailCalls) != 0 {
		t.Fatalf("expected 0 jail calls with all PASS, got %d", len(wk.jailCalls))
	}
}

func TestProcessWithdraw_ExactBalance(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	addr := makeAddr("user1")
	amount := sdk.NewCoin("ufai", math.NewInt(1_000_000))
	_ = k.ProcessDeposit(ctx, addr, amount)

	if err := k.ProcessWithdraw(ctx, addr, amount); err != nil {
		t.Fatalf("should be able to withdraw exact balance: %v", err)
	}

	ia, found := k.GetInferenceAccount(ctx, addr)
	if !found {
		t.Fatal("account should still exist after full withdrawal")
	}
	if !ia.Balance.IsZero() {
		t.Fatalf("expected zero balance, got %s", ia.Balance)
	}
}

// -------- Logger / GetAuthority --------

func TestLoggerAndAuthority(t *testing.T) {
	k, _, _, _ := setupKeeper(t)
	l := k.Logger()
	if l == nil {
		t.Fatal("logger should not be nil")
	}
	auth := k.GetAuthority()
	if auth != "authority" {
		t.Fatalf("expected authority 'authority', got '%s'", auth)
	}
}

// -------- Genesis --------

func TestInitGenesis_ExportGenesis(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	addr1 := makeAddr("user1")
	addr2 := makeAddr("user2")

	gs := types.GenesisState{
		Params: types.DefaultParams(),
		InferenceAccounts: []types.InferenceAccount{
			{Address: addr1.String(), Balance: sdk.NewCoin("ufai", math.NewInt(1000))},
			{Address: addr2.String(), Balance: sdk.NewCoin("ufai", math.NewInt(2000))},
		},
		BatchRecords: []types.BatchRecord{
			{BatchId: 1, Proposer: makeAddr("proposer").String(), MerkleRoot: []byte("root"), ResultCount: 5, TotalFees: sdk.NewCoin("ufai", math.NewInt(500)), SettledAt: 50},
		},
	}

	k.InitGenesis(ctx, gs)

	ia, found := k.GetInferenceAccount(ctx, addr1)
	if !found {
		t.Fatal("account1 should exist after InitGenesis")
	}
	if !ia.Balance.Amount.Equal(math.NewInt(1000)) {
		t.Fatalf("expected 1000, got %s", ia.Balance.Amount.String())
	}

	br, found := k.GetBatchRecord(ctx, 1)
	if !found {
		t.Fatal("batch record should exist after InitGenesis")
	}
	if br.ResultCount != 5 {
		t.Fatalf("expected result count 5, got %d", br.ResultCount)
	}

	exported := k.ExportGenesis(ctx)
	if len(exported.InferenceAccounts) != 2 {
		t.Fatalf("expected 2 accounts in exported genesis, got %d", len(exported.InferenceAccounts))
	}
}

// -------- gRPC Query --------

func TestQueryServer_InferenceAccount(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	addr := makeAddr("user1")
	_ = k.ProcessDeposit(ctx, addr, sdk.NewCoin("ufai", math.NewInt(500)))

	resp, err := qs.InferenceAccount(ctx, &types.QueryInferenceAccountRequest{Address: addr.String()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Account.Balance.Amount.Equal(math.NewInt(500)) {
		t.Fatalf("expected 500, got %s", resp.Account.Balance.Amount.String())
	}

	_, err = qs.InferenceAccount(ctx, &types.QueryInferenceAccountRequest{Address: "invalid"})
	if err == nil {
		t.Fatal("expected error for invalid address")
	}

	nobody := makeAddr("nobody")
	_, err = qs.InferenceAccount(ctx, &types.QueryInferenceAccountRequest{Address: nobody.String()})
	if err == nil {
		t.Fatal("expected error for nonexistent account")
	}

	_, err = qs.InferenceAccount(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestQueryServer_Batch(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	br := types.BatchRecord{
		BatchId:     42,
		Proposer:    makeAddr("proposer").String(),
		MerkleRoot:  []byte("root"),
		ResultCount: 10,
		TotalFees:   sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		SettledAt:   100,
	}
	k.SetBatchRecord(ctx, br)

	resp, err := qs.Batch(ctx, &types.QueryBatchRequest{BatchId: 42})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Batch.ResultCount != 10 {
		t.Fatalf("expected 10, got %d", resp.Batch.ResultCount)
	}

	_, err = qs.Batch(ctx, &types.QueryBatchRequest{BatchId: 999})
	if err == nil {
		t.Fatal("expected error for nonexistent batch")
	}

	_, err = qs.Batch(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestQueryServer_Params(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	qs := keeper.NewQueryServerImpl(k)

	resp, err := qs.Params(ctx, &types.QueryParamsRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Params.ExecutorFeeRatio != 850 {
		t.Fatalf("expected executor ratio 850, got %d", resp.Params.ExecutorFeeRatio)
	}
}

// -------- MsgServer --------

func TestMsgServer_Deposit(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := makeAddr("user1")
	msg := types.NewMsgDeposit(addr.String(), sdk.NewCoin("ufai", math.NewInt(1000)))

	_, err := ms.Deposit(ctx, msg)
	if err != nil {
		t.Fatalf("Deposit failed: %v", err)
	}

	ia, found := k.GetInferenceAccount(ctx, addr)
	if !found {
		t.Fatal("account should exist after deposit")
	}
	if !ia.Balance.Amount.Equal(math.NewInt(1000)) {
		t.Fatalf("expected 1000, got %s", ia.Balance.Amount.String())
	}
}

func TestMsgServer_Deposit_InvalidAddress(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	_, err := ms.Deposit(ctx, types.NewMsgDeposit("invalid", sdk.NewCoin("ufai", math.NewInt(1000))))
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}

func TestMsgServer_Withdraw(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	addr := makeAddr("user1")
	_ = k.ProcessDeposit(ctx, addr, sdk.NewCoin("ufai", math.NewInt(2000)))

	msg := types.NewMsgWithdraw(addr.String(), sdk.NewCoin("ufai", math.NewInt(1000)))
	_, err := ms.Withdraw(ctx, msg)
	if err != nil {
		t.Fatalf("Withdraw failed: %v", err)
	}

	ia, _ := k.GetInferenceAccount(ctx, addr)
	if !ia.Balance.Amount.Equal(math.NewInt(1000)) {
		t.Fatalf("expected 1000, got %s", ia.Balance.Amount.String())
	}
}

func TestMsgServer_Withdraw_InvalidAddress(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	_, err := ms.Withdraw(ctx, types.NewMsgWithdraw("invalid", sdk.NewCoin("ufai", math.NewInt(100))))
	if err == nil {
		t.Fatal("expected error for invalid address")
	}
}

func TestMsgServer_BatchSettle(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	userAddr := makeAddr("user1")
	workerAddr := makeAddr("worker1")
	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(5_000_000)))

	entries := []types.SettlementEntry{
		{
			TaskId:        []byte("msg-batch-task-01--"),
			UserAddress:   userAddr.String(),
			WorkerAddress: workerAddr.String(),
			Fee:           sdk.NewCoin("ufai", math.NewInt(1_000_000)),
			ExpireBlock:   200,
			Status:        types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("v1").String(), Pass: true},
				{Address: makeAddr("v2").String(), Pass: true},
				{Address: makeAddr("v3").String(), Pass: true},
			},
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	resp, err := ms.BatchSettle(ctx, msg)
	if err != nil {
		t.Fatalf("BatchSettle failed: %v", err)
	}
	if resp.BatchId == 0 {
		t.Fatal("expected non-zero batch id")
	}
}

func TestMsgServer_SubmitFraudProof(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	contentHash, contentSig := signFraudContent(t, []byte("content"))
	msg := &types.MsgFraudProof{
		Reporter:         makeAddr("reporter").String(),
		TaskId:           []byte("fraud-msg-task-0001"),
		WorkerAddress:    makeAddr("worker1").String(),
		ContentHash:      contentHash,
		WorkerContentSig: contentSig,
		ActualContent:    []byte("content"),
	}

	_, err := ms.SubmitFraudProof(ctx, msg)
	if err != nil {
		t.Fatalf("SubmitFraudProof failed: %v", err)
	}
}

func TestMsgServer_SubmitSecondVerificationResult(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	ms := keeper.NewMsgServerImpl(k)

	msg := &types.MsgSecondVerificationResult{
		SecondVerifier: makeAddr("second_verifier1").String(),
		TaskId:         []byte("audit-msg-task-0001"),
		Epoch:          1,
		Pass:           true,
		LogitsHash:     []byte("hash"),
	}

	_, err := ms.SubmitSecondVerificationResult(ctx, msg)
	if err != nil {
		t.Fatalf("SubmitSecondVerificationResult failed: %v", err)
	}
}

// -------- FraudProof with existing settled task --------

func TestProcessFraudProof_WithSettledTask(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	taskId := []byte("fraud-settled-task01")
	workerAddr := makeAddr("worker1")

	k.SetSettledTask(ctx, types.SettledTaskID{
		TaskId:        taskId,
		Status:        types.TaskSettled,
		SettledAt:     50,
		WorkerAddress: workerAddr.String(),
	})

	contentHash, contentSig := signFraudContent(t, []byte("content"))
	msg := &types.MsgFraudProof{
		Reporter:         makeAddr("reporter").String(),
		TaskId:           taskId,
		WorkerAddress:    workerAddr.String(),
		ContentHash:      contentHash,
		WorkerContentSig: contentSig,
		ActualContent:    []byte("content"),
	}

	if err := k.ProcessFraudProof(ctx, msg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	st, found := k.GetSettledTask(ctx, taskId)
	if !found {
		t.Fatal("settled task should still exist")
	}
	if st.Status != types.TaskFraud {
		t.Fatalf("expected TaskFraud status, got %s", st.Status)
	}
}

// -------- Batch settlement with insufficient balance --------

func TestProcessBatchSettlement_InsufficientBalance(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	userAddr := makeAddr("user1")
	workerAddr := makeAddr("worker1")

	_ = k.ProcessDeposit(ctx, userAddr, sdk.NewCoin("ufai", math.NewInt(100)))

	entries := []types.SettlementEntry{
		{
			TaskId:        []byte("insuff-balance-task"),
			UserAddress:   userAddr.String(),
			WorkerAddress: workerAddr.String(),
			Fee:           sdk.NewCoin("ufai", math.NewInt(1_000_000)),
			ExpireBlock:   200,
			Status:        types.SettlementSuccess,
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr("v1").String(), Pass: true},
				{Address: makeAddr("v2").String(), Pass: true},
				{Address: makeAddr("v3").String(), Pass: true},
			},
		},
	}

	msg := makeBatchMsg(t, makeAddr("proposer").String(), entries)
	_, err := k.ProcessBatchSettlement(ctx, msg)
	if err != nil {
		t.Fatalf("should not error (task skipped due to low balance): %v", err)
	}

	ia, _ := k.GetInferenceAccount(ctx, userAddr)
	if !ia.Balance.Amount.Equal(math.NewInt(100)) {
		t.Fatalf("balance should be unchanged, got %s", ia.Balance.Amount.String())
	}
}
