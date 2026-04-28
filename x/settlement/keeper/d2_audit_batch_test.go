package keeper_test

import (
	"crypto/sha256"
	"testing"

	"github.com/cometbft/cometbft/crypto/secp256k1"

	"github.com/funai-wiki/funai-chain/x/settlement/keeper"
	"github.com/funai-wiki/funai-chain/x/settlement/types"
)

// ── D2 (issue #9): MsgSecondVerificationResultBatch keeper tests ─────────────
//
// Closes the chain-broadcast gap where second/third-tier verification results
// were buffered in Proposer memory and never reached the chain. Each batch
// entry carries the verifier's secp256k1 signature over the canonical
// response bytes; the keeper looks up the claimed verifier's on-chain
// pubkey, verifies the signature, and only then funnels the entry through
// the existing ProcessSecondVerificationResult path.
//
// The existing mockWorkerKeeper.GetWorkerPubkey returns testProposerKey.PubKey
// for every address, which is exactly what we want for "correct sig → accept"
// (sign with testProposerKey) and "wrong sig → reject" (sign with a different
// key; the mock still returns testProposerKey.PubKey so verification fails).

// signBatchEntry signs a batch entry with the given key, writing the same
// canonical pre-image used by the keeper (SecondVerificationEntrySigBytes
// then cometbft's internal sha256 via Sign). Mirrors the P2P dispatch
// sign path (p2p/dispatch.go:343-345) so the test exercises the same
// triple-sha256 shape that production uses.
func signBatchEntry(t *testing.T, entry *types.SecondVerificationBatchEntry, signerKey secp256k1.PrivKey, pubkeyForDigest []byte) {
	t.Helper()
	canonical := keeper.SecondVerificationEntrySigBytes(*entry, pubkeyForDigest)
	msgHash := sha256.Sum256(canonical)
	sig, err := signerKey.Sign(msgHash[:])
	if err != nil {
		t.Fatalf("sign entry: %v", err)
	}
	entry.Signature = sig
}

func newBatchEntry(verifier string, taskId []byte, pass bool) types.SecondVerificationBatchEntry {
	return types.SecondVerificationBatchEntry{
		TaskId:               taskId,
		SecondVerifier:       verifier,
		Epoch:                0,
		Pass:                 pass,
		LogitsHash:           []byte("logits-hash"),
		VerifiedInputTokens:  10,
		VerifiedOutputTokens: 20,
	}
}

// TestProcessSecondVerificationResultBatch_AcceptsValidSigs: the happy path.
// Three verifiers each sign their own entry; keeper looks up each pubkey
// via the mock worker keeper and accepts all three.
func TestProcessSecondVerificationResultBatch_AcceptsValidSigs(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	taskId := []byte("task-batch-valid")
	mockPubkey := testProposerKey.PubKey().Bytes() // what the mock returns for every addr

	// §2.9 row 5 timing-attack rule: ProcessSecondVerificationResult requires
	// a SecondVerificationPending entry. Original verifiers are kept disjoint
	// from the second_verifiers below (v1/v2/v3) so the conflict-of-interest
	// check (P2-7) doesn't trip.
	seedAuditPending(k, ctx, taskId, []string{makeAddr("orig-v1").String()})

	entries := make([]types.SecondVerificationBatchEntry, 3)
	for i := 0; i < 3; i++ {
		entries[i] = newBatchEntry(makeAddr("v"+string(rune('1'+i))).String(), taskId, true)
		signBatchEntry(t, &entries[i], testProposerKey, mockPubkey)
	}

	msg := types.NewMsgSecondVerificationResultBatch(makeAddr("proposer").String(), entries)
	accepted, rejected := k.ProcessSecondVerificationResultBatch(ctx, msg)

	if accepted != 3 || rejected != 0 {
		t.Fatalf("expected all 3 entries accepted, got accepted=%d rejected=%d", accepted, rejected)
	}

	record, found := k.GetSecondVerificationRecord(ctx, taskId)
	if !found {
		t.Fatal("expected SecondVerificationRecord written after batch processing")
	}
	if len(record.SecondVerifierAddresses) != 3 {
		t.Fatalf("expected 3 verifier addresses recorded, got %d", len(record.SecondVerifierAddresses))
	}
}

// TestProcessSecondVerificationResultBatch_RejectsBadSig: entries whose
// signature was produced by a key other than the on-chain pubkey for that
// verifier must be rejected. This is the core D2 guarantee against
// unsigned / forged audit results reaching chain.
func TestProcessSecondVerificationResultBatch_RejectsBadSig(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	taskId := []byte("task-batch-bad-sig")
	mockPubkey := testProposerKey.PubKey().Bytes()
	attacker := secp256k1.GenPrivKey()
	seedAuditPending(k, ctx, taskId, []string{makeAddr("orig-v-bad-sig").String()})

	good := newBatchEntry(makeAddr("v-good").String(), taskId, true)
	signBatchEntry(t, &good, testProposerKey, mockPubkey)

	bad := newBatchEntry(makeAddr("v-bad").String(), taskId, true)
	// Signed by attacker but the mock's GetWorkerPubkey returns testProposerKey's
	// pubkey → signature verification must fail for this entry.
	signBatchEntry(t, &bad, attacker, mockPubkey)

	msg := types.NewMsgSecondVerificationResultBatch(
		makeAddr("proposer").String(),
		[]types.SecondVerificationBatchEntry{good, bad},
	)
	accepted, rejected := k.ProcessSecondVerificationResultBatch(ctx, msg)

	if accepted != 1 || rejected != 1 {
		t.Fatalf("expected accepted=1 rejected=1, got accepted=%d rejected=%d", accepted, rejected)
	}
}

// TestProcessSecondVerificationResultBatch_RejectsTamperedPass: a MITM
// flipping Pass from false to true after the verifier signed invalidates
// the signature. Guarantees the per-entry sig covers the binary outcome
// that drives settlement (the whole point of the audit mechanism).
func TestProcessSecondVerificationResultBatch_RejectsTamperedPass(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	taskId := []byte("task-batch-tampered-pass")
	mockPubkey := testProposerKey.PubKey().Bytes()

	entry := newBatchEntry(makeAddr("v-mitm").String(), taskId, false)
	signBatchEntry(t, &entry, testProposerKey, mockPubkey)
	entry.Pass = true // attacker flips the outcome after signing

	msg := types.NewMsgSecondVerificationResultBatch(
		makeAddr("proposer").String(),
		[]types.SecondVerificationBatchEntry{entry},
	)
	accepted, rejected := k.ProcessSecondVerificationResultBatch(ctx, msg)

	if accepted != 0 || rejected != 1 {
		t.Fatalf("expected all entries rejected after Pass tampering, got accepted=%d rejected=%d", accepted, rejected)
	}
}

// TestProcessSecondVerificationResultBatch_RejectsTamperedLogitsHash: same
// guarantee but for LogitsHash — protects the evidence trail the keeper
// stores into SecondVerificationRecord.
func TestProcessSecondVerificationResultBatch_RejectsTamperedLogitsHash(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	taskId := []byte("task-batch-tampered-logits")
	mockPubkey := testProposerKey.PubKey().Bytes()

	entry := newBatchEntry(makeAddr("v-mitm").String(), taskId, true)
	signBatchEntry(t, &entry, testProposerKey, mockPubkey)
	entry.LogitsHash = []byte("forged-logits")

	msg := types.NewMsgSecondVerificationResultBatch(
		makeAddr("proposer").String(),
		[]types.SecondVerificationBatchEntry{entry},
	)
	accepted, rejected := k.ProcessSecondVerificationResultBatch(ctx, msg)
	if accepted != 0 || rejected != 1 {
		t.Fatalf("expected rejection after LogitsHash tampering, got accepted=%d rejected=%d", accepted, rejected)
	}
}

// TestProcessSecondVerificationResultBatch_PartialFailureDoesNotBlockRest:
// one malformed entry must not prevent other valid entries from landing.
// The tx-level return is (accepted, rejected), not an error, so a single
// bad verifier cannot spoil a batch.
func TestProcessSecondVerificationResultBatch_PartialFailureDoesNotBlockRest(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	mockPubkey := testProposerKey.PubKey().Bytes()
	attacker := secp256k1.GenPrivKey()

	t1 := []byte("task-partial-1")
	t2 := []byte("task-partial-2")
	seedAuditPending(k, ctx, t1, []string{makeAddr("orig-partial-1").String()})
	seedAuditPending(k, ctx, t2, []string{makeAddr("orig-partial-2").String()})

	good1 := newBatchEntry(makeAddr("v-good-1").String(), t1, true)
	signBatchEntry(t, &good1, testProposerKey, mockPubkey)
	bad := newBatchEntry(makeAddr("v-bad").String(), t1, true)
	signBatchEntry(t, &bad, attacker, mockPubkey)
	good2 := newBatchEntry(makeAddr("v-good-2").String(), t2, true)
	signBatchEntry(t, &good2, testProposerKey, mockPubkey)

	msg := types.NewMsgSecondVerificationResultBatch(
		makeAddr("proposer").String(),
		[]types.SecondVerificationBatchEntry{good1, bad, good2},
	)
	accepted, rejected := k.ProcessSecondVerificationResultBatch(ctx, msg)

	if accepted != 2 || rejected != 1 {
		t.Fatalf("expected 2 accepted, 1 rejected, got accepted=%d rejected=%d", accepted, rejected)
	}

	// Both tasks should have records written despite the bad entry.
	if _, ok := k.GetSecondVerificationRecord(ctx, t1); !ok {
		t.Fatal("expected task 1 record written from its valid entry")
	}
	if _, ok := k.GetSecondVerificationRecord(ctx, t2); !ok {
		t.Fatal("expected task 2 record written from its valid entry")
	}
}

// TestMsgSecondVerificationResultBatch_ValidateBasic: proto-level rejects
// cover the malformed-message cases the keeper should never see.
func TestMsgSecondVerificationResultBatch_ValidateBasic(t *testing.T) {
	proposer := makeAddr("proposer").String()
	v := makeAddr("v1").String()

	cases := []struct {
		name    string
		msg     *types.MsgSecondVerificationResultBatch
		wantErr bool
	}{
		{
			name: "happy",
			msg: types.NewMsgSecondVerificationResultBatch(proposer, []types.SecondVerificationBatchEntry{{
				TaskId:         []byte("t"),
				SecondVerifier: v,
				LogitsHash:     []byte("lh"),
				Signature:      []byte("sig"),
			}}),
			wantErr: false,
		},
		{
			name:    "empty entries",
			msg:     types.NewMsgSecondVerificationResultBatch(proposer, nil),
			wantErr: true,
		},
		{
			name: "bad proposer address",
			msg: &types.MsgSecondVerificationResultBatch{
				Proposer: "not-a-bech32",
				Entries: []types.SecondVerificationBatchEntry{{
					TaskId:         []byte("t"),
					SecondVerifier: v,
					LogitsHash:     []byte("lh"),
					Signature:      []byte("sig"),
				}},
			},
			wantErr: true,
		},
		{
			name: "entry missing task_id",
			msg: types.NewMsgSecondVerificationResultBatch(proposer, []types.SecondVerificationBatchEntry{{
				TaskId:         nil,
				SecondVerifier: v,
				LogitsHash:     []byte("lh"),
				Signature:      []byte("sig"),
			}}),
			wantErr: true,
		},
		{
			name: "entry missing signature",
			msg: types.NewMsgSecondVerificationResultBatch(proposer, []types.SecondVerificationBatchEntry{{
				TaskId:         []byte("t"),
				SecondVerifier: v,
				LogitsHash:     []byte("lh"),
				Signature:      nil,
			}}),
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.msg.ValidateBasic()
			if tc.wantErr && err == nil {
				t.Fatal("expected ValidateBasic error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected ValidateBasic error: %v", err)
			}
		})
	}
}
