package proposer

// Tests for KT non-state-machine Issue B — Proposer.AddVerifyResult must
// verify the signature + dedup by VerifierAddr. Pre-fix the inbound P2P
// path accepted any VerifyResult that passed VRF top-21 with no signature
// check, and counted duplicates by row.

import (
	"bytes"
	"crypto/sha256"
	"testing"

	"github.com/cometbft/cometbft/crypto/secp256k1"

	p2ptypes "github.com/funai-wiki/funai-chain/p2p/types"
	vrftypes "github.com/funai-wiki/funai-chain/x/vrf/types"
)

// signVerifyResult populates Signature using the Verifier's private key.
// Mirrors the production Verifier path (p2p/verifier:550-557).
func signVerifyResult(t *testing.T, r *p2ptypes.VerifyResult, priv secp256k1.PrivKey) {
	t.Helper()
	hash := sha256.Sum256(r.SignBytes())
	sig, err := priv.Sign(hash[:])
	if err != nil {
		t.Fatalf("sign verify result: %v", err)
	}
	r.Signature = sig
}

// newProposerWithReceipt sets up a proposer with one pending task and a
// minimal Receipt + active workers list so AddVerifyResult's VRF top-21
// gate has something to compare against.
func newProposerWithReceipt(t *testing.T, taskId []byte, workerPubkey []byte) *Proposer {
	t.Helper()
	p := &Proposer{
		Address:       "test-proposer",
		pendingTasks:  make(map[string]*TaskEvidence),
		pendingAudits: make(map[string]*AuditEvidence),
	}
	p.pendingTasks[bytesToHex(taskId)] = &TaskEvidence{
		Receipt: &p2ptypes.InferReceipt{
			TaskId:       taskId,
			WorkerPubkey: workerPubkey,
			ResultHash:   []byte("result-hash"),
		},
	}
	return p
}

// bytesToHex avoids importing encoding/hex just for this single use.
func bytesToHex(b []byte) string {
	const hexchars = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hexchars[v>>4]
		out[i*2+1] = hexchars[v&0xf]
	}
	return string(out)
}

func TestKT_IssueB_AddVerifyResult_RejectsMissingSig(t *testing.T) {
	taskId := []byte("issueB-task-1")
	worker := secp256k1.GenPrivKey()
	p := newProposerWithReceipt(t, taskId, worker.PubKey().Bytes())

	verif := secp256k1.GenPrivKey()
	r := &p2ptypes.VerifyResult{
		TaskId:       taskId,
		VerifierAddr: verif.PubKey().Bytes(),
		Pass:         true,
		Signature:    nil, // missing
	}
	if err := p.AddVerifyResult(r); err == nil {
		t.Fatal("Issue B: AddVerifyResult must reject missing signature")
	}
}

func TestKT_IssueB_AddVerifyResult_RejectsWrongSig(t *testing.T) {
	taskId := []byte("issueB-task-2")
	worker := secp256k1.GenPrivKey()
	p := newProposerWithReceipt(t, taskId, worker.PubKey().Bytes())

	verif := secp256k1.GenPrivKey()
	other := secp256k1.GenPrivKey() // wrong signing key

	r := &p2ptypes.VerifyResult{
		TaskId:       taskId,
		VerifierAddr: verif.PubKey().Bytes(), // claims to be `verif`
		Pass:         true,
	}
	signVerifyResult(t, r, other) // but signed by `other`

	if err := p.AddVerifyResult(r); err == nil {
		t.Fatal("Issue B: AddVerifyResult must reject sig from a different key than VerifierAddr claims")
	}
}

func TestKT_IssueB_AddVerifyResult_AcceptsCorrectSig(t *testing.T) {
	taskId := []byte("issueB-task-3")
	worker := secp256k1.GenPrivKey()
	p := newProposerWithReceipt(t, taskId, worker.PubKey().Bytes())

	verif := secp256k1.GenPrivKey()
	verifPubkey := verif.PubKey().Bytes()

	// Skip the VRF top-21 gate by leaving activeWorkers empty (the gate at
	// p2p/proposer/proposer.go only fires when len(p.activeWorkers) > 0).
	// This isolates the signature path under test.
	r := &p2ptypes.VerifyResult{
		TaskId:       taskId,
		VerifierAddr: verifPubkey,
		Pass:         true,
		LogitsHash:   []byte("h"),
	}
	signVerifyResult(t, r, verif)

	if err := p.AddVerifyResult(r); err != nil {
		t.Fatalf("Issue B: legitimate sig must be accepted: %v", err)
	}
}

func TestKT_IssueB_AddVerifyResult_DedupsSameVerifier(t *testing.T) {
	taskId := []byte("issueB-task-4")
	worker := secp256k1.GenPrivKey()
	p := newProposerWithReceipt(t, taskId, worker.PubKey().Bytes())

	verif := secp256k1.GenPrivKey()

	makeRow := func(pass bool) *p2ptypes.VerifyResult {
		r := &p2ptypes.VerifyResult{
			TaskId:       taskId,
			VerifierAddr: verif.PubKey().Bytes(),
			Pass:         pass,
			LogitsHash:   []byte{0x01},
		}
		signVerifyResult(t, r, verif)
		return r
	}

	// First row accepted.
	if err := p.AddVerifyResult(makeRow(true)); err != nil {
		t.Fatalf("first row: %v", err)
	}
	// Same verifier submitting again (even with flipped Pass to make it a
	// "new" canonical pre-image / signature) MUST be rejected.
	if err := p.AddVerifyResult(makeRow(false)); err == nil {
		t.Fatal("Issue B: same verifier identity must not be allowed to vote twice")
	}

	ev := p.pendingTasks[bytesToHex(taskId)]
	if len(ev.Verifiers) != 1 {
		t.Fatalf("Issue B: expected 1 verifier row after dedup, got %d", len(ev.Verifiers))
	}
	if !ev.Verifiers[0].Pass {
		t.Fatalf("Issue B: first vote (Pass=true) must be the one retained")
	}
}

// TestKT_IssueB_AddVerifyResult_RejectsWrongPubkeyLength: pre-fix path
// accepted any non-empty bytes as VerifierAddr; sig verification implicitly
// required the right size, but it failed with a less helpful error message.
// Post-fix the length check fires before the verify, returning a clear
// reason.
func TestKT_IssueB_AddVerifyResult_RejectsWrongPubkeyLength(t *testing.T) {
	taskId := []byte("issueB-task-5")
	worker := secp256k1.GenPrivKey()
	p := newProposerWithReceipt(t, taskId, worker.PubKey().Bytes())

	r := &p2ptypes.VerifyResult{
		TaskId:       taskId,
		VerifierAddr: []byte("not-33-bytes"),
		Pass:         true,
		Signature:    []byte("anything"),
	}
	if err := p.AddVerifyResult(r); err == nil {
		t.Fatal("Issue B: must reject when VerifierAddr is not 33-byte compressed pubkey")
	}
}

// Compile-time guard against stripping unused vrftypes import via auto-format.
var _ = vrftypes.AlphaVerification

// Compile-time guard for bytes pkg (used in production AddVerifyResult).
var _ = bytes.Equal
