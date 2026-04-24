package sdk

import (
	"strings"
	"testing"

	"github.com/cometbft/cometbft/crypto/secp256k1"
	"github.com/cosmos/cosmos-sdk/types/bech32"

	p2ptypes "github.com/funai-wiki/funai-chain/p2p/types"
)

// ── Worker-sign → SDK-verify round-trip ──────────────────────────────────────
//
// Regression tests for the SDK receipt-signature verification path.
//
// Previously `verifyWorkerReceiptSig` had an extra sha256.Sum256(SignBytes())
// step on top of cometbft's internal sha256, producing a 3-layer digest that
// never matched Worker's 2-layer Sign(SignBytes()) output. Result: the SDK
// silently rejected every real Worker receipt ("signature invalid, ignoring")
// and the M7 FRAUD-DETECTED branch was unreachable. These tests make sure
// future refactors cannot reintroduce the same mismatch.

// signReceiptLikeWorker mirrors p2p/worker.signReceipt: pass receipt.SignBytes()
// directly to cometbft secp256k1 Sign, which internally sha256's the input.
// Keep this helper identical to the real Worker signer — if it drifts, the
// test stops being a regression for the real production path.
func signReceiptLikeWorker(t *testing.T, r *p2ptypes.InferReceipt, priv secp256k1.PrivKey) {
	t.Helper()
	// WorkerPubkey must be populated BEFORE signing because
	// InferReceipt.SignBytes writes it into the digest. This matches the
	// order in p2p/worker.HandleTask which sets WorkerPubkey at receipt
	// creation and only then calls signReceipt.
	r.WorkerPubkey = priv.PubKey().Bytes()
	sig, err := priv.Sign(r.SignBytes())
	if err != nil {
		t.Fatalf("worker sign: %v", err)
	}
	r.WorkerSig = sig
}

func fixtureReceipt(inferenceLatencyMs uint32) *p2ptypes.InferReceipt {
	return &p2ptypes.InferReceipt{
		TaskId:             []byte("task-id-sdk-rt-1"),
		WorkerLogits:       [5]float32{1.0, 2.0, 3.0, 4.0, 5.0},
		ResultHash:         []byte("result-hash-0000"),
		FinalSeed:          []byte("final-seed-00"),
		SampledTokens:      [5]uint32{10, 20, 30, 40, 50},
		InputTokenCount:    42,
		OutputTokenCount:   7,
		InferenceLatencyMs: inferenceLatencyMs,
	}
}

func TestVerifyWorkerReceiptSig_HappyPath(t *testing.T) {
	priv := secp256k1.GenPrivKey()
	r := fixtureReceipt(250)
	signReceiptLikeWorker(t, r, priv)

	if !verifyWorkerReceiptSig(r) {
		t.Fatal("SDK must accept a receipt signed the same way p2p/worker.signReceipt does")
	}
}

func TestVerifyWorkerReceiptSig_RejectsTamperedResultHash(t *testing.T) {
	priv := secp256k1.GenPrivKey()
	r := fixtureReceipt(250)
	signReceiptLikeWorker(t, r, priv)

	r.ResultHash = []byte("result-hash-EVIL")

	if verifyWorkerReceiptSig(r) {
		t.Fatal("tampered result_hash must invalidate Worker signature (M7 fraud detection relies on this)")
	}
}

func TestVerifyWorkerReceiptSig_RejectsTamperedInferenceLatencyMs(t *testing.T) {
	priv := secp256k1.GenPrivKey()
	r := fixtureReceipt(250)
	signReceiptLikeWorker(t, r, priv)

	r.InferenceLatencyMs = 10 // attacker tries to make this Worker look 25x faster

	if verifyWorkerReceiptSig(r) {
		t.Fatal("tampered InferenceLatencyMs must invalidate Worker signature (Audit KT §5)")
	}
}

func TestVerifyWorkerReceiptSig_RejectsWrongPubkey(t *testing.T) {
	signer := secp256k1.GenPrivKey()
	attackerPubkey := secp256k1.GenPrivKey().PubKey().Bytes()

	r := fixtureReceipt(250)
	signReceiptLikeWorker(t, r, signer)

	r.WorkerPubkey = attackerPubkey

	if verifyWorkerReceiptSig(r) {
		t.Fatal("receipt presented with a different pubkey than the signer must be rejected")
	}
}

func TestVerifyWorkerReceiptSig_RejectsMalformedInputs(t *testing.T) {
	priv := secp256k1.GenPrivKey()
	good := fixtureReceipt(250)
	signReceiptLikeWorker(t, good, priv)

	t.Run("nil receipt", func(t *testing.T) {
		if verifyWorkerReceiptSig(nil) {
			t.Fatal("nil receipt must be rejected")
		}
	})

	t.Run("empty sig", func(t *testing.T) {
		r := *good
		r.WorkerSig = nil
		if verifyWorkerReceiptSig(&r) {
			t.Fatal("empty sig must be rejected")
		}
	})

	t.Run("non-33-byte pubkey", func(t *testing.T) {
		r := *good
		r.WorkerPubkey = []byte{0x01, 0x02, 0x03}
		if verifyWorkerReceiptSig(&r) {
			t.Fatal("short pubkey must be rejected (expected 33-byte compressed secp256k1)")
		}
	})
}

// ── FraudProof submission helpers ────────────────────────────────────────────
//
// Tests for the SDK's MsgFraudProof construction path. Prior behavior
// marshalled a custom struct to raw JSON and shipped it directly to
// CometBFT's /broadcast_tx_sync — which rejects as "tx parse error"
// because it expects a protobuf-encoded Cosmos SDK tx envelope. These
// tests lock in the corrected behavior: the SDK must build a real
// MsgFraudProof with correct field names, bech32 addresses derived from
// pubkeys, and the captured WorkerContentSig.

// TestBech32FromPubkey_RoundTrip: the SDK-internal helper must produce a
// "funai1..." address that decodes back to the original 20-byte address
// bytes. Guards against the regression where the old submitFraudProof
// used `string(c.config.UserPubkey)` (raw pubkey bytes as string) which
// would fail on-chain ValidateBasic immediately.
func TestBech32FromPubkey_RoundTrip(t *testing.T) {
	priv := secp256k1.GenPrivKey()
	pub := priv.PubKey().Bytes()

	addr, err := bech32FromPubkey(pub)
	if err != nil {
		t.Fatalf("bech32FromPubkey: %v", err)
	}
	if !strings.HasPrefix(addr, "funai1") {
		t.Fatalf("expected funai1-prefixed bech32, got %s", addr)
	}

	// Decode and verify the payload matches the pubkey's Address().Bytes().
	prefix, decoded, err := bech32.DecodeAndConvert(addr)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if prefix != "funai" {
		t.Fatalf("expected prefix 'funai', got %s", prefix)
	}
	expectedAddrBytes := priv.PubKey().Address().Bytes()
	if len(decoded) != len(expectedAddrBytes) {
		t.Fatalf("decoded length %d, want %d", len(decoded), len(expectedAddrBytes))
	}
	for i := range decoded {
		if decoded[i] != expectedAddrBytes[i] {
			t.Fatalf("decoded byte %d: got %x want %x", i, decoded[i], expectedAddrBytes[i])
		}
	}
}

// TestBech32FromPubkey_RejectsMalformedLength: SDK must not silently
// produce garbage when given a truncated or padded pubkey.
func TestBech32FromPubkey_RejectsMalformedLength(t *testing.T) {
	cases := []struct {
		name   string
		pubkey []byte
	}{
		{"empty", nil},
		{"too short", []byte{0x02, 0x03}},
		{"too long", make([]byte, 65)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := bech32FromPubkey(tc.pubkey); err == nil {
				t.Fatal("expected error for malformed pubkey length")
			}
		})
	}
}

// TestBech32FromPubkey_DifferentPubkeysProduceDifferentAddresses: sanity
// check — two distinct pubkeys must not collide to the same bech32.
func TestBech32FromPubkey_DifferentPubkeysProduceDifferentAddresses(t *testing.T) {
	a, _ := bech32FromPubkey(secp256k1.GenPrivKey().PubKey().Bytes())
	b, _ := bech32FromPubkey(secp256k1.GenPrivKey().PubKey().Bytes())
	if a == "" || b == "" {
		t.Fatalf("unexpected empty bech32 output: a=%q b=%q", a, b)
	}
	if a == b {
		t.Fatal("two distinct pubkeys produced the same bech32 address")
	}
}

// ── reassembleStreamTokens ───────────────────────────────────────────────────
//
// Regression tests for the SDK stream-token reassembly path. libp2p pubsub
// does not guarantee message delivery order, so the SDK must index-sort
// StreamToken messages before assembling the output. Without this, longer
// outputs (e.g. 34 tokens on Qwen2.5-7B, real test 2026-04-23) observed
// scrambled output like "You correct're! to2+" instead of "You're correct!".
// See docs/testing/reports/2026-04-23-1047-e2e-real-runpod-4090/report.md §13.4.

func TestReassembleStreamTokens_InOrder(t *testing.T) {
	received := []p2ptypes.StreamToken{
		{Index: 0, Token: "Hello", IsFinal: false},
		{Index: 1, Token: " ", IsFinal: false},
		{Index: 2, Token: "world", IsFinal: true, ContentSig: []byte{0xaa}},
	}
	tokens, sig, ready := reassembleStreamTokens(received)
	if !ready {
		t.Fatal("expected ready=true")
	}
	got := strings.Join(tokens, "")
	if got != "Hello world" {
		t.Errorf("output mismatch: got %q want %q", got, "Hello world")
	}
	if len(sig) != 1 || sig[0] != 0xaa {
		t.Errorf("content sig mismatch: got %v", sig)
	}
}

func TestReassembleStreamTokens_OutOfOrder(t *testing.T) {
	// Simulates the real Run 2 scenario: pubsub delivered tokens out of
	// index order. Before the fix, the SDK concatenated them by arrival
	// order and produced scrambled output; with the fix, Index wins.
	received := []p2ptypes.StreamToken{
		{Index: 2, Token: "world", IsFinal: true, ContentSig: []byte{0xbb}},
		{Index: 0, Token: "Hello", IsFinal: false},
		{Index: 1, Token: " ", IsFinal: false},
	}
	tokens, sig, ready := reassembleStreamTokens(received)
	if !ready {
		t.Fatal("expected ready=true")
	}
	got := strings.Join(tokens, "")
	if got != "Hello world" {
		t.Errorf("out-of-order reassembly wrong: got %q want %q", got, "Hello world")
	}
	if len(sig) != 1 || sig[0] != 0xbb {
		t.Errorf("content sig mismatch: got %v", sig)
	}
}

func TestReassembleStreamTokens_NotReadyWithoutFinal(t *testing.T) {
	received := []p2ptypes.StreamToken{
		{Index: 0, Token: "Hello", IsFinal: false},
		{Index: 1, Token: " ", IsFinal: false},
	}
	_, _, ready := reassembleStreamTokens(received)
	if ready {
		t.Error("expected ready=false when no IsFinal present")
	}
}

func TestReassembleStreamTokens_NotReadyWithGap(t *testing.T) {
	received := []p2ptypes.StreamToken{
		{Index: 0, Token: "A", IsFinal: false},
		{Index: 2, Token: "C", IsFinal: true},
	}
	_, _, ready := reassembleStreamTokens(received)
	if ready {
		t.Error("expected ready=false when intermediate index missing")
	}
}

func TestReassembleStreamTokens_DeduplicatesSameIndex(t *testing.T) {
	received := []p2ptypes.StreamToken{
		{Index: 0, Token: "Hello", IsFinal: false},
		{Index: 0, Token: "Hello", IsFinal: false},
		{Index: 1, Token: " world", IsFinal: true},
	}
	tokens, _, ready := reassembleStreamTokens(received)
	if !ready {
		t.Fatal("expected ready=true")
	}
	if strings.Join(tokens, "") != "Hello world" {
		t.Errorf("got %q, want %q", strings.Join(tokens, ""), "Hello world")
	}
	if len(tokens) != 2 {
		t.Errorf("dedupe failed: expected 2 tokens, got %d", len(tokens))
	}
}

func TestReassembleStreamTokens_SingleTokenIsFinal(t *testing.T) {
	received := []p2ptypes.StreamToken{
		{Index: 0, Token: "done", IsFinal: true, ContentSig: []byte{0xcc}},
	}
	tokens, sig, ready := reassembleStreamTokens(received)
	if !ready {
		t.Fatal("expected ready=true for single-token output")
	}
	if len(tokens) != 1 || tokens[0] != "done" {
		t.Errorf("single token mismatch: got %v", tokens)
	}
	if len(sig) != 1 || sig[0] != 0xcc {
		t.Errorf("content sig mismatch: got %v", sig)
	}
}

func TestReassembleStreamTokens_FinalBeforeEarlierTokens(t *testing.T) {
	// Pubsub can deliver the Final token first, with Index=N-1. We must
	// still wait for tokens 0..N-2 before declaring ready.
	received := []p2ptypes.StreamToken{
		{Index: 3, Token: "END", IsFinal: true},
	}
	_, _, ready := reassembleStreamTokens(received)
	if ready {
		t.Error("expected ready=false when only final (Index=3) received, 0..2 still missing")
	}
	received = append(received,
		p2ptypes.StreamToken{Index: 0, Token: "A", IsFinal: false},
		p2ptypes.StreamToken{Index: 1, Token: "B", IsFinal: false},
		p2ptypes.StreamToken{Index: 2, Token: "C", IsFinal: false},
	)
	tokens, _, ready := reassembleStreamTokens(received)
	if !ready {
		t.Fatal("expected ready=true once all 4 tokens present")
	}
	if strings.Join(tokens, "") != "ABCEND" {
		t.Errorf("got %q, want %q", strings.Join(tokens, ""), "ABCEND")
	}
}

func TestReassembleStreamTokens_Empty(t *testing.T) {
	_, _, ready := reassembleStreamTokens(nil)
	if ready {
		t.Error("expected ready=false on empty input")
	}
}
