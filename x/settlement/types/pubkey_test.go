package types

// Tests for KT 30-case Issue 4 — DecodeWorkerPubkey accepts the three
// pubkey formats observed across the codebase. Pre-fix, settlement keeper's
// FraudProof H3 check did `[]byte(pubkeyStr)` directly, treating a hex
// string's printable characters as if they were raw bytes — every legitimate
// fraud report silently failed in production where the testnet CLI stores
// pubkeys as hex (scripts/e2e-real-inference.sh:509).

import (
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/cometbft/cometbft/crypto/secp256k1"
	"github.com/cosmos/cosmos-sdk/types/bech32"
)

// makeRawPubkey returns a deterministic 33-byte compressed secp256k1 public
// key suitable for round-tripping through the three encoding formats below.
func makeRawPubkey(t *testing.T) []byte {
	t.Helper()
	priv := secp256k1.GenPrivKey()
	pub := priv.PubKey().Bytes()
	if len(pub) != CompressedSecp256k1PubkeyLen {
		t.Fatalf("expected compressed pubkey of %d bytes, got %d", CompressedSecp256k1PubkeyLen, len(pub))
	}
	return pub
}

func TestDecodeWorkerPubkey_RawBytesAsString(t *testing.T) {
	raw := makeRawPubkey(t)
	got := DecodeWorkerPubkey(string(raw))
	if len(got) != CompressedSecp256k1PubkeyLen {
		t.Fatalf("expected 33-byte decode, got len=%d", len(got))
	}
	if string(got) != string(raw) {
		t.Fatalf("raw round-trip mismatch")
	}
}

func TestDecodeWorkerPubkey_Hex(t *testing.T) {
	raw := makeRawPubkey(t)
	hexStr := hex.EncodeToString(raw)
	if len(hexStr) != 66 {
		t.Fatalf("hex of 33 bytes must be 66 chars, got %d", len(hexStr))
	}
	got := DecodeWorkerPubkey(hexStr)
	if string(got) != string(raw) {
		t.Fatalf("hex round-trip mismatch")
	}
}

func TestDecodeWorkerPubkey_Base64(t *testing.T) {
	raw := makeRawPubkey(t)
	b64 := base64.StdEncoding.EncodeToString(raw)
	if len(b64) != 44 {
		t.Fatalf("base64 of 33 bytes must be 44 chars, got %d", len(b64))
	}
	got := DecodeWorkerPubkey(b64)
	if string(got) != string(raw) {
		t.Fatalf("base64 round-trip mismatch")
	}
}

func TestDecodeWorkerPubkey_EmptyReturnsNil(t *testing.T) {
	if got := DecodeWorkerPubkey(""); got != nil {
		t.Fatalf("empty string must return nil, got %x", got)
	}
}

func TestDecodeWorkerPubkey_WrongLengthRaw(t *testing.T) {
	// 32 bytes: not a valid compressed pubkey.
	short := string(make([]byte, 32))
	if got := DecodeWorkerPubkey(short); got != nil {
		t.Fatalf("32-byte raw must return nil, got len=%d", len(got))
	}
	// 64 bytes: also wrong (uncompressed pubkey would be 65 with 0x04 prefix).
	long := string(make([]byte, 64))
	if got := DecodeWorkerPubkey(long); got != nil {
		t.Fatalf("64-byte raw must return nil, got len=%d", len(got))
	}
}

func TestDecodeWorkerPubkey_WrongLengthHex(t *testing.T) {
	// 64 hex chars = 32 bytes after decode — must reject.
	short := hex.EncodeToString(make([]byte, 32))
	if got := DecodeWorkerPubkey(short); got != nil {
		t.Fatalf("32-byte hex must return nil, got len=%d", len(got))
	}
}

func TestDecodeWorkerPubkey_GarbageInputReturnsNil(t *testing.T) {
	cases := []string{
		"not-hex-not-base64-x",
		"!!!@@@",
		"this-is-much-too-long-to-be-anything-meaningful-as-a-pubkey",
	}
	for _, c := range cases {
		if got := DecodeWorkerPubkey(c); got != nil {
			t.Fatalf("garbage input %q must return nil, got len=%d", c, len(got))
		}
	}
}

func TestDecodeWorkerPubkey_Bech32(t *testing.T) {
	// Bech32 encoding: HRP + bech32(amino-prefix-bytes || raw-pubkey-bytes).
	// Use the SDK ConvertAndEncode helper — same path the keyring CLI uses.
	raw := makeRawPubkey(t)
	payload := append(append([]byte{}, aminoSecp256k1PubkeyPrefix...), raw...)
	encoded, err := bech32.ConvertAndEncode("funaipub", payload)
	if err != nil {
		t.Fatalf("bech32 encode: %v", err)
	}
	got := DecodeWorkerPubkey(encoded)
	if string(got) != string(raw) {
		t.Fatalf("bech32 round-trip mismatch")
	}
}

func TestDecodeWorkerPubkey_Bech32_WrongAminoPrefix(t *testing.T) {
	// Same length payload but wrong amino prefix (e.g. ed25519's `1624de64 20`
	// instead of secp256k1's `eb5ae98721`) → decoder MUST reject so an
	// ed25519 pubkey is not silently treated as if it were secp256k1.
	raw := makeRawPubkey(t)
	wrongPrefix := []byte{0x16, 0x24, 0xde, 0x64, 0x20}
	payload := append(append([]byte{}, wrongPrefix...), raw...)
	encoded, err := bech32.ConvertAndEncode("funaipub", payload)
	if err != nil {
		t.Fatalf("bech32 encode: %v", err)
	}
	if got := DecodeWorkerPubkey(encoded); got != nil {
		t.Fatalf("must reject ed25519-prefix bech32, got len=%d", len(got))
	}
}

// TestDecodeWorkerPubkey_HexBeforeRawForCommonAmbiguity asserts that a 33-byte
// raw input which also happens to be valid hex stays interpreted as base64
// or raw, never silently treated as 16.5 bytes of hex (impossible — but the
// ordering choice still matters to confirm). Mostly a smoke test for the
// length-gated decoder ordering.
func TestDecodeWorkerPubkey_OrderingIsLengthGated(t *testing.T) {
	raw := makeRawPubkey(t)
	rawAsString := string(raw)
	got := DecodeWorkerPubkey(rawAsString)
	if string(got) != string(raw) {
		t.Fatalf("33-byte raw must round-trip even when the bytes happen to look like other encodings")
	}
}
