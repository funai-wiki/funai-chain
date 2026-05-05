package keeper_test

// Audit §4 — `SubmitVRFProof` submitter gate.
//
// Before this fix the handler accepted a `Value` from any address and
// passed it straight into `UpdateSeed`, with `ValidateVRFProof` a stub
// that returned true on any non-empty input. The chain-wide VRF seed
// could be poisoned by anyone, breaking every downstream "VRF top-K"
// decision (dispatch, verifier selection, leader election, committee
// rotation). The Phase-1 lockdown restricts submission to the chain
// authority; permissionless submission is deferred until the per-proof
// verification protocol lands.

import (
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/store"
	storemetrics "cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/vrf/keeper"
	"github.com/funai-wiki/funai-chain/x/vrf/types"
)

// testAuthority is the bech32-shaped authority string the in-test vrf
// keeper is wired with. Production wiring uses authtypes.NewModuleAddress("gov").
const testAuthority = "cosmos1vrf-test-authority"

func setupVRFKeeper(t *testing.T) (keeper.Keeper, sdk.Context) {
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
	k := keeper.NewKeeper(cdc, storeKey, nil, testAuthority)
	ctx := sdk.NewContext(stateStore, cmtproto.Header{Height: 100}, false, log.NewNopLogger())
	return k, ctx
}

func TestAudit_Item4_SubmitVRFProof_RejectsNonAuthority(t *testing.T) {
	k, ctx := setupVRFKeeper(t)
	srv := keeper.NewMsgServerImpl(k)

	seedBefore := k.GetCurrentSeed(ctx)

	_, err := srv.SubmitVRFProof(ctx, &types.MsgSubmitVRFProof{
		Creator: "cosmos1random-outsider",
		Value:   []byte("attacker-controlled-seed-input-32x"),
		Proof:   []byte("any"),
	})
	if err == nil {
		t.Fatal("audit §4: SubmitVRFProof from non-authority must reject")
	}

	seedAfter := k.GetCurrentSeed(ctx)
	if string(seedBefore.Value) != string(seedAfter.Value) {
		t.Fatal("audit §4: rejected SubmitVRFProof must NOT update the global VRF seed")
	}
}

func TestAudit_Item5_ReportLeaderTimeout_RejectsNonAuthority(t *testing.T) {
	k, ctx := setupVRFKeeper(t)
	srv := keeper.NewMsgServerImpl(k)

	// Outsider tries to trigger leader rotation with garbage timeout
	// proofs. Pre-fix the handler only counted len(TimeoutProofs); now it
	// must reject before that check on submitter identity grounds.
	_, err := srv.ReportLeaderTimeout(ctx, &types.MsgReportLeaderTimeout{
		Creator:       "cosmos1random-outsider",
		ModelId:       "any-model",
		TimeoutProofs: [][]byte{[]byte("garbage1"), []byte("garbage2"), []byte("garbage3")},
	})
	if err == nil {
		t.Fatal("audit §5: ReportLeaderTimeout from non-authority must reject")
	}
}

func TestAudit_Item4_SubmitVRFProof_AcceptsAuthority(t *testing.T) {
	k, ctx := setupVRFKeeper(t)
	srv := keeper.NewMsgServerImpl(k)

	seedBefore := k.GetCurrentSeed(ctx)

	_, err := srv.SubmitVRFProof(ctx, &types.MsgSubmitVRFProof{
		Creator: testAuthority,
		Value:   []byte("authority-supplied-seed-input-32x"),
		Proof:   []byte("any"),
	})
	if err != nil {
		t.Fatalf("audit §4: SubmitVRFProof from authority must succeed, got: %v", err)
	}

	seedAfter := k.GetCurrentSeed(ctx)
	if string(seedBefore.Value) == string(seedAfter.Value) {
		t.Fatal("audit §4: accepted SubmitVRFProof from authority must update the seed")
	}
}
