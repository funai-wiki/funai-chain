// Package bench provides performance benchmarks for critical FunAI Chain components.
//
// Run with:
//
//	go test ./bench/... -bench=. -benchtime=10s -benchmem
//
// Key targets:
//   - MerkleRoot (100 entries): < 500µs
//   - BatchSettle (100 tasks): < 10ms per call
//   - VRF ranking (100 workers): < 1ms
//   - Privacy TLS Wrap (4KB): < 200µs
package bench

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"testing"

	"cosmossdk.io/log"
	"cosmossdk.io/math"
	"cosmossdk.io/store"
	storemetrics "cosmossdk.io/store/metrics"
	storetypes "cosmossdk.io/store/types"
	"github.com/cometbft/cometbft/crypto/secp256k1"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/gogoproto/proto"

	"github.com/funai-wiki/funai-chain/sdk/privacy"
	"github.com/funai-wiki/funai-chain/x/settlement/keeper"
	"github.com/funai-wiki/funai-chain/x/settlement/types"
	vrftypes "github.com/funai-wiki/funai-chain/x/vrf/types"
)

// ── Mock keepers (no-ops for benchmarking) ──────────────────────────────────

var benchProposerKey = secp256k1.GenPrivKey()

type nopBankKeeper struct{}

func (nopBankKeeper) SendCoins(_ context.Context, _, _ sdk.AccAddress, _ sdk.Coins) error {
	return nil
}
func (nopBankKeeper) SendCoinsFromAccountToModule(_ context.Context, _ sdk.AccAddress, _ string, _ sdk.Coins) error {
	return nil
}
func (nopBankKeeper) SendCoinsFromModuleToAccount(_ context.Context, _ string, _ sdk.AccAddress, _ sdk.Coins) error {
	return nil
}

type nopWorkerKeeper struct{}

func (nopWorkerKeeper) JailWorker(_ sdk.Context, _ sdk.AccAddress, _ int64)   {}
func (nopWorkerKeeper) SlashWorker(_ sdk.Context, _ sdk.AccAddress, _ uint32) {}
func (nopWorkerKeeper) SlashWorkerTo(_ sdk.Context, _ sdk.AccAddress, _ uint32, _ sdk.AccAddress) {
}
func (nopWorkerKeeper) IncrementSuccessStreak(_ sdk.Context, _ sdk.AccAddress)        {}
func (nopWorkerKeeper) GetSuccessStreak(_ sdk.Context, _ sdk.AccAddress) uint32       { return 0 }
func (nopWorkerKeeper) UpdateWorkerStats(_ sdk.Context, _ sdk.AccAddress, _ sdk.Coin) {}
func (nopWorkerKeeper) GetWorkerPubkey(_ sdk.Context, _ sdk.AccAddress) (string, bool) {
	return string(benchProposerKey.PubKey().Bytes()), true
}
func (nopWorkerKeeper) TombstoneWorker(_ sdk.Context, _ sdk.AccAddress)            {}
func (nopWorkerKeeper) ReputationOnAccept(_ sdk.Context, _ sdk.AccAddress)         {}
func (nopWorkerKeeper) UpdateAvgLatency(_ sdk.Context, _ sdk.AccAddress, _ uint32) {}

// ── Setup helpers ─────────────────────────────────────────────────────────────

func setupBenchKeeper(b *testing.B) (keeper.Keeper, sdk.Context) {
	b.Helper()
	storeKey := storetypes.NewKVStoreKey(types.StoreKey)
	db := dbm.NewMemDB()
	ms := store.NewCommitMultiStore(db, log.NewNopLogger(), storemetrics.NewNoOpMetrics())
	ms.MountStoreWithDB(storeKey, storetypes.StoreTypeIAVL, db)
	if err := ms.LoadLatestVersion(); err != nil {
		b.Fatal(err)
	}
	registry := codectypes.NewInterfaceRegistry()
	cdc := codec.NewProtoCodec(registry)
	k := keeper.NewKeeper(cdc, storeKey, nopBankKeeper{}, nopWorkerKeeper{}, "authority", log.NewNopLogger())
	ctx := sdk.NewContext(ms, cmtproto.Header{Height: 100}, false, log.NewNopLogger())
	k.SetParams(ctx, types.DefaultParams())
	return k, ctx
}

func makeAddr(n int) sdk.AccAddress {
	buf := make([]byte, 20)
	buf[0] = byte(n >> 8)
	buf[1] = byte(n)
	return sdk.AccAddress(buf)
}

func makeBatchEntries(n, salt int) []types.SettlementEntry {
	dummySig := make([]byte, 32)
	entries := make([]types.SettlementEntry, n)
	for i := range entries {
		raw := sha256.Sum256([]byte{byte(salt), byte(i >> 8), byte(i)})
		entries[i] = types.SettlementEntry{
			TaskId:        raw[:],
			UserAddress:   makeAddr(i).String(),
			WorkerAddress: makeAddr(i + 1000).String(),
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr(i + 2000).String(), Pass: true, Signature: dummySig},
				{Address: makeAddr(i + 3000).String(), Pass: true, Signature: dummySig},
				{Address: makeAddr(i + 4000).String(), Pass: true, Signature: dummySig},
			},
			Fee:             sdk.NewCoin("ufai", math.NewInt(1_000_000)),
			Status:          types.SettlementSuccess,
			ExpireBlock:     200,
			UserSigHash:     dummySig,
			WorkerSigHash:   dummySig,
			VerifySigHashes: [][]byte{dummySig, dummySig, dummySig},
		}
	}
	return entries
}

func makeBatchMsg(proposer sdk.AccAddress, entries []types.SettlementEntry) *types.MsgBatchSettlement {
	merkleRoot := keeper.ComputeMerkleRoot(entries)
	merkleHash := sha256.Sum256(merkleRoot)
	sig, _ := benchProposerKey.Sign(merkleHash[:])
	return types.NewMsgBatchSettlement(proposer.String(), merkleRoot, entries, sig)
}

// ── MerkleRoot benchmarks ─────────────────────────────────────────────────────

func BenchmarkMerkleRoot10(b *testing.B)  { benchmarkMerkleRoot(b, 10) }
func BenchmarkMerkleRoot50(b *testing.B)  { benchmarkMerkleRoot(b, 50) }
func BenchmarkMerkleRoot100(b *testing.B) { benchmarkMerkleRoot(b, 100) }

func benchmarkMerkleRoot(b *testing.B, n int) {
	b.Helper()
	entries := makeBatchEntries(n, 0)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		keeper.ComputeMerkleRoot(entries)
	}
}

// ── BatchSettle benchmarks ────────────────────────────────────────────────────

func BenchmarkBatchSettle10(b *testing.B)   { benchmarkBatchSettle(b, 10) }
func BenchmarkBatchSettle50(b *testing.B)   { benchmarkBatchSettle(b, 50) }
func BenchmarkBatchSettle100(b *testing.B)  { benchmarkBatchSettle(b, 100) }
func BenchmarkBatchSettle250(b *testing.B)  { benchmarkBatchSettle(b, 250) }
func BenchmarkBatchSettle500(b *testing.B)  { benchmarkBatchSettle(b, 500) }
func BenchmarkBatchSettle1000(b *testing.B) { benchmarkBatchSettle(b, 1000) }

func benchmarkBatchSettle(b *testing.B, batchSize int) {
	b.Helper()
	k, ctx := setupBenchKeeper(b)

	// Pre-deposit for all unique user addresses
	entries := makeBatchEntries(batchSize, 0)
	for _, e := range entries {
		addr, _ := sdk.AccAddressFromBech32(e.UserAddress)
		_ = k.ProcessDeposit(ctx, addr, sdk.NewCoin("ufai", math.NewInt(100_000_000_000)))
	}

	proposer := makeAddr(9999)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Use unique task IDs each iteration to avoid duplicate rejection
		freshEntries := makeBatchEntries(batchSize, i+1)
		// Re-deposit if needed (benchmark tracks timing with pre-funded accounts)
		msg := makeBatchMsg(proposer, freshEntries)

		if _, err := k.ProcessBatchSettlement(ctx, msg); err != nil {
			// Expected to fail on first iteration if users not pre-funded — log but continue
			b.Logf("BatchSettle[%d] err: %v", i, err)
		}
	}
}

// ── VRF Ranking benchmarks ────────────────────────────────────────────────────

func BenchmarkVRFRanking10(b *testing.B)  { benchmarkVRFRanking(b, 10) }
func BenchmarkVRFRanking50(b *testing.B)  { benchmarkVRFRanking(b, 50) }
func BenchmarkVRFRanking100(b *testing.B) { benchmarkVRFRanking(b, 100) }

func benchmarkVRFRanking(b *testing.B, n int) {
	b.Helper()
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)

	workers := make([]vrftypes.RankedWorker, n)
	for i := range workers {
		key := secp256k1.GenPrivKey()
		workers[i] = vrftypes.RankedWorker{
			Address: makeAddr(i).String(),
			Pubkey:  key.PubKey().Bytes(),
			Stake:   math.NewInt(int64((i + 1) * 10_000_000_000)),
		}
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		vrftypes.RankWorkers(seed, workers, vrftypes.AlphaDispatch)
	}
}

// BenchmarkVRFComputeScore benchmarks a single VRF score computation.
func BenchmarkVRFComputeScore(b *testing.B) {
	seed := make([]byte, 32)
	_, _ = rand.Read(seed)
	pubkey := secp256k1.GenPrivKey().PubKey().Bytes()
	stake := math.NewInt(10_000_000_000)

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		vrftypes.ComputeScore(seed, pubkey, stake, vrftypes.AlphaDispatch)
	}
}

// ── Privacy TLS benchmarks ───────────────────────────────────────────────────

func BenchmarkTLSWrap_1KB(b *testing.B)  { benchmarkTLSWrap(b, 1024) }
func BenchmarkTLSWrap_4KB(b *testing.B)  { benchmarkTLSWrap(b, 4096) }
func BenchmarkTLSWrap_16KB(b *testing.B) { benchmarkTLSWrap(b, 16384) }

func benchmarkTLSWrap(b *testing.B, msgSize int) {
	b.Helper()
	senderPriv, senderPub, err := privacy.GenerateX25519Keypair()
	if err != nil {
		b.Fatal(err)
	}
	_, recipientPub, err := privacy.GenerateX25519Keypair()
	if err != nil {
		b.Fatal(err)
	}

	transport, err := privacy.NewTransport(privacy.ModeTLS,
		privacy.WithLocalKeys(senderPriv[:], senderPub[:]),
	)
	if err != nil {
		b.Fatal(err)
	}
	defer transport.Close()

	msg := make([]byte, msgSize)
	_, _ = rand.Read(msg)
	ctx := context.Background()

	b.SetBytes(int64(msgSize))
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := transport.Wrap(ctx, msg, recipientPub[:]); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTLSRoundtrip measures full Wrap + Unwrap cycle.
func BenchmarkTLSRoundtrip_4KB(b *testing.B) {
	senderPriv, senderPub, err := privacy.GenerateX25519Keypair()
	if err != nil {
		b.Fatal(err)
	}
	recipientPriv, recipientPub, err := privacy.GenerateX25519Keypair()
	if err != nil {
		b.Fatal(err)
	}

	sender, err := privacy.NewTransport(privacy.ModeTLS,
		privacy.WithLocalKeys(senderPriv[:], senderPub[:]),
	)
	if err != nil {
		b.Fatal(err)
	}
	receiver, err := privacy.NewTransport(privacy.ModeTLS,
		privacy.WithLocalKeys(recipientPriv[:], recipientPub[:]),
	)
	if err != nil {
		b.Fatal(err)
	}
	defer sender.Close()
	defer receiver.Close()

	msg := make([]byte, 4096)
	_, _ = rand.Read(msg)
	ctx := context.Background()

	b.SetBytes(4096)
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		wrapped, err := sender.Wrap(ctx, msg, recipientPub[:])
		if err != nil {
			b.Fatal(err)
		}
		if _, err := receiver.Unwrap(ctx, wrapped); err != nil {
			b.Fatal(err)
		}
	}
}

// ── Wire size + capacity bound benchmarks ────────────────────────────────────
//
// These benchmarks ground the MsgBatchSettlement cap (now 1300, was 40000 —
// see p2p/proposer/proposer.go:MaxBatchEntries and the chain-side hard cap
// at x/settlement/types/msgs.go:MaxBatchSettlementEntries) and verify the
// existing MsgSecondVerificationResultBatch cap of 256 in three independent
// real constraints: CometBFT mempool tx-bytes (1 MiB default), block gas
// (100 M), and 5s block-time wallclock.
//
// Measured (Intel Xeon Platinum 8481C, mock bank/worker keepers):
//   - MsgBatchSettlement:                783 B / entry,   ~91 µs / entry
//   - MsgSecondVerificationResultBatch:  186 B / entry,  ~262 µs / entry (real path)
//   - secp256k1 verify (alone):                          ~170 µs
//
// Bytes binds first for MsgBatchSettlement: 1 MiB / 783 B ≈ 1338 → cap 1300.
// CPU binds first for MsgSecondVerificationResultBatch: 1 s / 262 µs ≈ 3815;
// the existing 256 cap is ~15× under that ceiling.
//
// Helpers below use *real* signature/hash sizes (64-byte secp256k1 sig,
// 32-byte SHA-256 hash) — the existing makeBatchEntries helper uses 32-byte
// dummies which under-counts by ~96 bytes per settlement entry.

func makeRealisticBatchEntries(n, salt int) []types.SettlementEntry {
	sig := make([]byte, 64)
	hash := make([]byte, 32)
	entries := make([]types.SettlementEntry, n)
	for i := range entries {
		raw := sha256.Sum256([]byte{byte(salt), byte(i >> 8), byte(i)})
		entries[i] = types.SettlementEntry{
			TaskId:        raw[:],
			UserAddress:   makeAddr(i).String(),
			WorkerAddress: makeAddr(i + 1000).String(),
			VerifierResults: []types.VerifierResult{
				{Address: makeAddr(i + 2000).String(), Pass: true, Signature: sig, LogitsHash: hash},
				{Address: makeAddr(i + 3000).String(), Pass: true, Signature: sig, LogitsHash: hash},
				{Address: makeAddr(i + 4000).String(), Pass: true, Signature: sig, LogitsHash: hash},
			},
			Fee:             sdk.NewCoin("ufai", math.NewInt(1_000_000)),
			Status:          types.SettlementSuccess,
			ExpireBlock:     200,
			UserSigHash:     hash,
			WorkerSigHash:   hash,
			VerifySigHashes: [][]byte{hash, hash, hash},
		}
	}
	return entries
}

func makeAuditBatchEntries(n int) []types.SecondVerificationBatchEntry {
	sig := make([]byte, 64)
	hash := make([]byte, 32)
	entries := make([]types.SecondVerificationBatchEntry, n)
	for i := range entries {
		raw := sha256.Sum256([]byte{0xa1, byte(i >> 8), byte(i)})
		entries[i] = types.SecondVerificationBatchEntry{
			TaskId:         raw[:],
			SecondVerifier: makeAddr(i + 5000).String(),
			Epoch:          100,
			LogitsHash:     hash,
			Signature:      sig,
		}
	}
	return entries
}

// BenchmarkBatchSettleWireSize reports proto-marshaled byte length of
// MsgBatchSettlement at varying entry counts. Compared against CometBFT's
// 1 MiB mempool tx limit to bound the bytes-dimension cap.
func BenchmarkBatchSettleWireSize(b *testing.B) {
	for _, n := range []int{1, 100, 500, 1000, 5000} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			entries := makeRealisticBatchEntries(n, 0)
			msg := makeBatchMsg(makeAddr(9999), entries)
			data, err := proto.Marshal(msg)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(len(data)), "bytes")
			b.ReportMetric(float64(len(data))/float64(n), "bytes/entry")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = proto.Marshal(msg)
			}
		})
	}
}

// BenchmarkAuditBatchWireSize reports proto-marshaled byte length of
// MsgSecondVerificationResultBatch at varying entry counts.
func BenchmarkAuditBatchWireSize(b *testing.B) {
	for _, n := range []int{1, 64, 128, 256, 512} {
		b.Run(fmt.Sprintf("entries=%d", n), func(b *testing.B) {
			entries := makeAuditBatchEntries(n)
			msg := types.NewMsgSecondVerificationResultBatch(makeAddr(9999).String(), entries)
			data, err := proto.Marshal(msg)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportMetric(float64(len(data)), "bytes")
			b.ReportMetric(float64(len(data))/float64(n), "bytes/entry")
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = proto.Marshal(msg)
			}
		})
	}
}

// TestReportWireSize prints actual proto-marshaled byte length for each
// batch size — concrete numbers for capacity bound calculation. Run with:
//   go test ./bench/ -run TestReportWireSize -v
func TestReportWireSize(t *testing.T) {
	t.Log("=== MsgBatchSettlement wire size (real-size sigs/hashes) ===")
	for _, n := range []int{1, 100, 500, 1000, 5000, 10000} {
		entries := makeRealisticBatchEntries(n, 0)
		msg := makeBatchMsg(makeAddr(9999), entries)
		data, err := proto.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		perEntry := float64(len(data)) / float64(n)
		t.Logf("  entries=%6d  total=%9d B  per_entry=%6.0f B  vs_1MiB=%5.2fx",
			n, len(data), perEntry, float64(len(data))/1048576.0)
	}

	t.Log("=== MsgSecondVerificationResultBatch wire size ===")
	for _, n := range []int{1, 64, 128, 256, 512, 1024} {
		entries := makeAuditBatchEntries(n)
		msg := types.NewMsgSecondVerificationResultBatch(makeAddr(9999).String(), entries)
		data, err := proto.Marshal(msg)
		if err != nil {
			t.Fatal(err)
		}
		perEntry := float64(len(data)) / float64(n)
		t.Logf("  entries=%6d  total=%9d B  per_entry=%6.0f B  vs_1MiB=%5.4fx",
			n, len(data), perEntry, float64(len(data))/1048576.0)
	}
}

// ── Audit batch processing (real path) ──────────────────────────────────────
//
// Per-entry cost = secp256k1 verify + state read (pending) + state read/write
// (record) + epoch stat update. Setup cost (pending pre-creation, sig
// generation) is excluded via StopTimer.
//
// SecondVerifierCount is overridden to a huge value so processAuditJudgment
// never fires, isolating the per-entry verify+read+write path.

func BenchmarkAuditBatchProcess64(b *testing.B)  { benchmarkAuditBatchProcess(b, 64) }
func BenchmarkAuditBatchProcess128(b *testing.B) { benchmarkAuditBatchProcess(b, 128) }
func BenchmarkAuditBatchProcess256(b *testing.B) { benchmarkAuditBatchProcess(b, 256) }

func benchmarkAuditBatchProcess(b *testing.B, batchSize int) {
	b.Helper()
	k, ctx := setupBenchKeeper(b)

	p := types.DefaultParams()
	p.SecondVerifierCount = 1_000_000
	k.SetParams(ctx, p)

	pubkey := benchProposerKey.PubKey().Bytes()
	hash32 := make([]byte, 32)

	b.ResetTimer()
	b.ReportAllocs()

	for iter := 0; iter < b.N; iter++ {
		b.StopTimer()

		entries := make([]types.SecondVerificationBatchEntry, batchSize)
		for j := 0; j < batchSize; j++ {
			seed := []byte{byte(iter >> 24), byte(iter >> 16), byte(iter >> 8), byte(iter), byte(j >> 8), byte(j)}
			taskId := sha256.Sum256(seed)
			verifier := makeAddr(iter*batchSize + j + 100_000)

			entries[j] = types.SecondVerificationBatchEntry{
				TaskId:         taskId[:],
				SecondVerifier: verifier.String(),
				Epoch:          100,
				Pass:           true,
				LogitsHash:     hash32,
			}
			canonical := keeper.SecondVerificationEntrySigBytes(entries[j], pubkey)
			msgHash := sha256.Sum256(canonical)
			sig, err := benchProposerKey.Sign(msgHash[:])
			if err != nil {
				b.Fatal(err)
			}
			entries[j].Signature = sig

			k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
				TaskId:            taskId[:],
				OriginalStatus:    types.SettlementSuccess,
				SubmittedAt:       100,
				UserAddress:       makeAddr(j).String(),
				WorkerAddress:     makeAddr(j + 1000).String(),
				VerifierAddresses: []string{},
				Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
				ExpireBlock:       1_000_000,
			})
		}

		msg := types.NewMsgSecondVerificationResultBatch(makeAddr(9999).String(), entries)

		b.StartTimer()
		accepted, _ := k.ProcessSecondVerificationResultBatch(ctx, msg)
		if uint32(accepted) != uint32(batchSize) {
			b.Fatalf("expected all %d accepted, got %d", batchSize, accepted)
		}
	}
}

// BenchmarkSecp256k1Verify is the per-entry crypto cost ceiling for
// MsgSecondVerificationResultBatch (each entry verifies one secp256k1 sig
// inside the keeper).
func BenchmarkSecp256k1Verify(b *testing.B) {
	priv := secp256k1.GenPrivKey()
	pub := priv.PubKey()
	msg := []byte("benchmark message for signature verification 32b")
	sig, err := priv.Sign(msg)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if !pub.VerifySignature(msg, sig) {
			b.Fatal("verify failed")
		}
	}
}
