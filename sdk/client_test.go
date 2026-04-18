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
