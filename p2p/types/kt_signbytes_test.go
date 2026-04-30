package types

// Tests for KT non-state-machine findings (2026-04-30):
//
//   Issue A — AcceptTask.SignBytes binds (TaskId, WorkerPubkey, Accepted).
//             Pre-fix the Worker only signed `taskId` alone, leaving the
//             accept/reject bit and the worker identity unbound; rejects
//             were unsigned entirely.
//
//   Issue B — VerifyResult.SignBytes binds the verdict + identity fields.
//             Pre-fix the Verifier signed an inline shape covering only
//             LogitsHash + token counts — Pass / TaskId / VerifierAddr were
//             outside the canonical pre-image, so a MITM could flip the
//             verdict bit without invalidating the signature.

import (
	"bytes"
	"testing"
)

// ============================================================
// Issue A — AcceptTask.SignBytes covers all relevant fields.
// ============================================================

func TestKT_IssueA_AcceptTask_SignBytes_BindsTaskId(t *testing.T) {
	a := AcceptTask{
		TaskId:       []byte("task-A"),
		WorkerPubkey: []byte{0x02, 0x01, 0x02, 0x03},
		Accepted:     true,
	}
	b := AcceptTask{
		TaskId:       []byte("task-B"), // different task
		WorkerPubkey: []byte{0x02, 0x01, 0x02, 0x03},
		Accepted:     true,
	}
	if bytes.Equal(a.SignBytes(), b.SignBytes()) {
		t.Fatal("Issue A: SignBytes must differ when TaskId differs")
	}
}

func TestKT_IssueA_AcceptTask_SignBytes_BindsWorkerPubkey(t *testing.T) {
	a := AcceptTask{
		TaskId:       []byte("task-X"),
		WorkerPubkey: []byte{0x02, 0xaa, 0xaa},
		Accepted:     true,
	}
	b := AcceptTask{
		TaskId:       []byte("task-X"),
		WorkerPubkey: []byte{0x02, 0xbb, 0xbb}, // different pubkey
		Accepted:     true,
	}
	if bytes.Equal(a.SignBytes(), b.SignBytes()) {
		t.Fatal("Issue A: SignBytes must differ when WorkerPubkey differs (else attacker can reuse another worker's sig)")
	}
}

func TestKT_IssueA_AcceptTask_SignBytes_BindsAcceptedBit(t *testing.T) {
	a := AcceptTask{
		TaskId:       []byte("task-X"),
		WorkerPubkey: []byte{0x02, 0xaa, 0xaa},
		Accepted:     true,
	}
	b := AcceptTask{
		TaskId:       []byte("task-X"),
		WorkerPubkey: []byte{0x02, 0xaa, 0xaa},
		Accepted:     false, // flipped
	}
	if bytes.Equal(a.SignBytes(), b.SignBytes()) {
		t.Fatal("Issue A: SignBytes must differ when Accepted bit differs (else MITM can flip accept→reject)")
	}
}

// ============================================================
// Issue B — VerifyResult.SignBytes covers verdict + identity fields.
// ============================================================

func TestKT_IssueB_VerifyResult_SignBytes_BindsPass(t *testing.T) {
	r1 := VerifyResult{
		TaskId:       []byte("vr-task-1"),
		VerifierAddr: []byte{0x02, 0xaa},
		Pass:         true,
		LogitsHash:   []byte("h"),
	}
	r2 := r1
	r2.Pass = false // flipped
	if bytes.Equal(r1.SignBytes(), r2.SignBytes()) {
		t.Fatal("Issue B: SignBytes must differ when Pass differs (the central canonical-pre-image bug pre-fix)")
	}
}

func TestKT_IssueB_VerifyResult_SignBytes_BindsTaskId(t *testing.T) {
	r1 := VerifyResult{TaskId: []byte("task-A"), VerifierAddr: []byte{0x02, 0x01}, Pass: true}
	r2 := VerifyResult{TaskId: []byte("task-B"), VerifierAddr: []byte{0x02, 0x01}, Pass: true}
	if bytes.Equal(r1.SignBytes(), r2.SignBytes()) {
		t.Fatal("Issue B: SignBytes must differ when TaskId differs (else cross-task signature replay possible)")
	}
}

func TestKT_IssueB_VerifyResult_SignBytes_BindsVerifierAddr(t *testing.T) {
	r1 := VerifyResult{TaskId: []byte("t"), VerifierAddr: []byte{0x02, 0xaa}, Pass: true}
	r2 := VerifyResult{TaskId: []byte("t"), VerifierAddr: []byte{0x02, 0xbb}, Pass: true}
	if bytes.Equal(r1.SignBytes(), r2.SignBytes()) {
		t.Fatal("Issue B: SignBytes must differ when VerifierAddr differs (else identity swap)")
	}
}

func TestKT_IssueB_VerifyResult_SignBytes_BindsTokenCounts(t *testing.T) {
	r1 := VerifyResult{TaskId: []byte("t"), VerifierAddr: []byte{0x02, 0xaa}, VerifiedInputTokens: 100, VerifiedOutputTokens: 200}
	r2 := VerifyResult{TaskId: []byte("t"), VerifierAddr: []byte{0x02, 0xaa}, VerifiedInputTokens: 100, VerifiedOutputTokens: 999}
	if bytes.Equal(r1.SignBytes(), r2.SignBytes()) {
		t.Fatal("Issue B: SignBytes must differ when token counts differ (S9 billing dispute coverage)")
	}
}

func TestKT_IssueB_VerifyResult_SignBytes_StableForSameInput(t *testing.T) {
	r := VerifyResult{
		TaskId:               []byte("stable"),
		VerifierAddr:         []byte{0x02, 0x01, 0x02},
		Pass:                 true,
		LogitsMatch:          5,
		SamplingMatch:        4,
		LogitsHash:           []byte("logits"),
		VerifiedInputTokens:  10,
		VerifiedOutputTokens: 20,
	}
	if !bytes.Equal(r.SignBytes(), r.SignBytes()) {
		t.Fatal("Issue B: SignBytes must be deterministic for the same input")
	}
}
