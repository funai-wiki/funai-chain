package types

import (
	"encoding/base64"
	"encoding/hex"

	"github.com/cosmos/cosmos-sdk/types/bech32"
)

// CompressedSecp256k1PubkeyLen is the byte length of a compressed secp256k1
// public key. Cosmos SDK uses this format throughout its key infrastructure.
const CompressedSecp256k1PubkeyLen = 33

// DecodeWorkerPubkey accepts the worker pubkey in any of the three formats
// observed across the codebase and returns the raw 33-byte compressed
// secp256k1 representation. Returns nil on unrecognized / wrong-length input.
//
// The three formats:
//
//   - hex      — what `funaid tx worker register --pubkey "<hex>"` stores
//                (see scripts/e2e-real-inference.sh:509 — the actual testnet
//                CLI path; results in a 66-character lowercase hex string)
//   - base64   — Cosmos SDK keyring default; what `keys show <name> --output json`
//                produces; results in a 44-character padded base64 string
//   - raw      — what test fixtures and some legacy paths produce: the 33
//                bytes-as-string ("\x02\xab..." rather than a printable form)
//
// Centralizing the decode here closes KT 30-case Issue 4: pre-fix the
// FraudProof H3 check at x/settlement/keeper/keeper.go did `[]byte(pubkeyStr)`
// directly, treating a hex-stored pubkey's printable characters as if they
// were raw bytes — signature verification therefore never matched in
// production, silently breaking every legitimate fraud report. The D2 batch
// verifier-sig path had the same inversion (`len != 33` rejected hex's 66
// chars). verifyProposerSigOnRoot had a hex+raw fallback so it worked, but
// did not handle the base64 form.
//
// Order matters: we try base64 FIRST because:
//   - Cosmos SDK's default keyring output is base64, so it's the most common
//     format on a real-world chain
//   - base64 imposes specific padding ('=' suffix) and a restricted alphabet
//     that is unlikely to coincidentally decode to 33 bytes from a non-base64
//     input — it's the most distinguishable
//
// Then hex (66-char lowercase, also distinguishable from raw by length); raw
// is the fallback for any 33-byte string-as-bytes input.
func DecodeWorkerPubkey(s string) []byte {
	if s == "" {
		return nil
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil && len(b) == CompressedSecp256k1PubkeyLen {
		return b
	}
	if b, err := hex.DecodeString(s); err == nil && len(b) == CompressedSecp256k1PubkeyLen {
		return b
	}
	if b := decodeBech32SecpPubkey(s); b != nil {
		return b
	}
	if b := []byte(s); len(b) == CompressedSecp256k1PubkeyLen {
		return b
	}
	return nil
}

// aminoSecp256k1PubkeyPrefix is the 5-byte Amino type-prefix that Cosmos SDK
// puts before a compressed secp256k1 pubkey when bech32-encoding it for
// keyring CLI output. The full bech32 payload is therefore 38 bytes; only
// the trailing 33 bytes are the actual pubkey.
//
//   eb5ae987 21 → secp256k1.PubKey amino prefix.
var aminoSecp256k1PubkeyPrefix = []byte{0xeb, 0x5a, 0xe9, 0x87, 0x21}

// decodeBech32SecpPubkey accepts the bech32 form produced by `funaid keys
// show <name> -p` (e.g. `funaipub1addwnpepqg6...`), strips the Amino prefix,
// and returns the raw 33-byte compressed secp256k1 pubkey. Returns nil if
// the input is not bech32, has the wrong total length, or the Amino prefix
// does not match secp256k1.
//
// HRP is not enforced — any HRP that resolves to (5-byte amino prefix +
// 33-byte key) is accepted, since chains using a custom Bech32PrefixAccPub
// produce valid SDK pubkey strings under their own prefix. Validation is
// purely on the decoded length + amino prefix.
func decodeBech32SecpPubkey(s string) []byte {
	_, payload, err := bech32.DecodeAndConvert(s)
	if err != nil {
		return nil
	}
	if len(payload) != len(aminoSecp256k1PubkeyPrefix)+CompressedSecp256k1PubkeyLen {
		return nil
	}
	for i, b := range aminoSecp256k1PubkeyPrefix {
		if payload[i] != b {
			return nil
		}
	}
	return payload[len(aminoSecp256k1PubkeyPrefix):]
}
