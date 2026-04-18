package worker

import (
	"testing"

	"github.com/cometbft/cometbft/crypto/secp256k1"

	p2ptypes "github.com/funai-wiki/funai-chain/p2p/types"
)

// ── S4: AssignTask Leader signature verification ─────────────────────────────
//
// These tests cover the guard that replaces the old "silent skip when pubkey
// not set" behavior. Worker now stores the top-3 VRF-elected leader pubkeys
// per model and must accept ONLY signatures produced by one of them.

func makeSignedAssignTask(t *testing.T, modelId string, priv secp256k1.PrivKey) *p2ptypes.AssignTask {
	t.Helper()
	task := &p2ptypes.AssignTask{
		TaskId:            []byte("task-id-12345678"),
		ModelId:           []byte(modelId),
		Prompt:            "hello world",
		Fee:               1000,
		UserAddr:          []byte("user-addr-000000"),
		Temperature:       5000,
		TopP:              9000,
		UserSeed:          []byte("seed-1234"),
		DispatchBlockHash: []byte("block-hash-abcdef0123456789"),
		FeePerInputToken:  0,
		FeePerOutputToken: 0,
		MaxFee:            1000,
		MaxTokens:         256,
	}
	digest := task.SigDigest()
	sig, err := priv.Sign(digest[:])
	if err != nil {
		t.Fatalf("sign task: %v", err)
	}
	task.LeaderSig = sig
	return task
}

// TestIsLeaderAuthorized_Bootstrap: cold start with no leaders seeded → permits task
// with `bootstrap=true`. Caller logs a warning; task is allowed through so the Worker
// doesn't black-hole traffic while waiting for the first chain refresh.
func TestIsLeaderAuthorized_Bootstrap(t *testing.T) {
	priv := secp256k1.GenPrivKey()
	w := &Worker{}
	task := makeSignedAssignTask(t, "model-A", priv)

	authorized, bootstrap := w.isLeaderAuthorized(task)
	if !bootstrap {
		t.Fatal("expected bootstrap=true when no leader set has been seeded")
	}
	if authorized {
		t.Fatal("expected authorized=false during bootstrap (caller uses bootstrap flag to permit)")
	}
}

// TestIsLeaderAuthorized_UnknownModel: leaders seeded for model-A, task is for model-B
// → must hard-fail (not bootstrap, not authorized). An adversary could otherwise publish
// AssignTasks for a model the Worker doesn't support and exploit the bootstrap path.
func TestIsLeaderAuthorized_UnknownModel(t *testing.T) {
	otherPriv := secp256k1.GenPrivKey()
	attackerPriv := secp256k1.GenPrivKey()
	w := &Worker{}
	w.SetLeadersForModel("model-A", []string{"addr-A"}, [][]byte{otherPriv.PubKey().Bytes()})

	task := makeSignedAssignTask(t, "model-B", attackerPriv)
	authorized, bootstrap := w.isLeaderAuthorized(task)
	if bootstrap {
		t.Fatal("expected bootstrap=false once any model has been seeded")
	}
	if authorized {
		t.Fatal("expected authorized=false when task's model has no known leaders")
	}
}

// TestIsLeaderAuthorized_Rank1Accepted: sig from rank#1 Leader must verify.
func TestIsLeaderAuthorized_Rank1Accepted(t *testing.T) {
	p1, p2, p3 := secp256k1.GenPrivKey(), secp256k1.GenPrivKey(), secp256k1.GenPrivKey()
	w := &Worker{}
	w.SetLeadersForModel("m1",
		[]string{"a1", "a2", "a3"},
		[][]byte{p1.PubKey().Bytes(), p2.PubKey().Bytes(), p3.PubKey().Bytes()},
	)

	task := makeSignedAssignTask(t, "m1", p1)
	authorized, bootstrap := w.isLeaderAuthorized(task)
	if bootstrap {
		t.Fatal("expected bootstrap=false")
	}
	if !authorized {
		t.Fatal("expected rank#1 leader signature to be authorized")
	}
}

// TestIsLeaderAuthorized_Rank2Accepted: Leader failover to rank#2 (§6.2, 1.5s inactivity)
// must still pass so Worker doesn't reject valid failover dispatches.
func TestIsLeaderAuthorized_Rank2Accepted(t *testing.T) {
	p1, p2, p3 := secp256k1.GenPrivKey(), secp256k1.GenPrivKey(), secp256k1.GenPrivKey()
	w := &Worker{}
	w.SetLeadersForModel("m1",
		[]string{"a1", "a2", "a3"},
		[][]byte{p1.PubKey().Bytes(), p2.PubKey().Bytes(), p3.PubKey().Bytes()},
	)

	task := makeSignedAssignTask(t, "m1", p2)
	authorized, _ := w.isLeaderAuthorized(task)
	if !authorized {
		t.Fatal("expected rank#2 leader signature to be authorized (failover)")
	}
}

// TestIsLeaderAuthorized_Rank3Accepted: third-rank failover also covered.
func TestIsLeaderAuthorized_Rank3Accepted(t *testing.T) {
	p1, p2, p3 := secp256k1.GenPrivKey(), secp256k1.GenPrivKey(), secp256k1.GenPrivKey()
	w := &Worker{}
	w.SetLeadersForModel("m1",
		[]string{"a1", "a2", "a3"},
		[][]byte{p1.PubKey().Bytes(), p2.PubKey().Bytes(), p3.PubKey().Bytes()},
	)

	task := makeSignedAssignTask(t, "m1", p3)
	authorized, _ := w.isLeaderAuthorized(task)
	if !authorized {
		t.Fatal("expected rank#3 leader signature to be authorized (failover)")
	}
}

// TestIsLeaderAuthorized_AttackerKey: a key outside the top-3 set must be rejected.
// This is the core S4 guarantee — prevents random peers from spoofing dispatch.
func TestIsLeaderAuthorized_AttackerKey(t *testing.T) {
	p1, p2, p3 := secp256k1.GenPrivKey(), secp256k1.GenPrivKey(), secp256k1.GenPrivKey()
	attacker := secp256k1.GenPrivKey()
	w := &Worker{}
	w.SetLeadersForModel("m1",
		[]string{"a1", "a2", "a3"},
		[][]byte{p1.PubKey().Bytes(), p2.PubKey().Bytes(), p3.PubKey().Bytes()},
	)

	task := makeSignedAssignTask(t, "m1", attacker)
	authorized, bootstrap := w.isLeaderAuthorized(task)
	if bootstrap {
		t.Fatal("expected bootstrap=false")
	}
	if authorized {
		t.Fatal("expected forged task (signed by attacker) to be rejected")
	}
}

// TestIsLeaderAuthorized_TamperedPrompt: the digest covers every mutable field.
// If a MITM changes the prompt after Leader signs, the sig must stop validating.
func TestIsLeaderAuthorized_TamperedPrompt(t *testing.T) {
	p1 := secp256k1.GenPrivKey()
	w := &Worker{}
	w.SetLeadersForModel("m1",
		[]string{"a1"},
		[][]byte{p1.PubKey().Bytes()},
	)

	task := makeSignedAssignTask(t, "m1", p1)
	task.Prompt = "tampered prompt"

	authorized, _ := w.isLeaderAuthorized(task)
	if authorized {
		t.Fatal("expected tampered prompt to invalidate leader signature")
	}
}

// TestIsLeaderAuthorized_TamperedFee: the billing fields must be signature-covered.
// S9 billing fields changing should invalidate the sig so a Worker cannot be tricked
// into charging the wrong fee.
func TestIsLeaderAuthorized_TamperedFee(t *testing.T) {
	p1 := secp256k1.GenPrivKey()
	w := &Worker{}
	w.SetLeadersForModel("m1", []string{"a1"}, [][]byte{p1.PubKey().Bytes()})

	task := makeSignedAssignTask(t, "m1", p1)
	task.MaxFee = task.MaxFee * 10

	authorized, _ := w.isLeaderAuthorized(task)
	if authorized {
		t.Fatal("expected MaxFee tampering to invalidate leader signature")
	}
}

// TestIsLeaderAuthorized_MalformedPubkey: if the stored pubkey is not a valid 33-byte
// secp256k1 pubkey, verification must not panic and must reject.
func TestIsLeaderAuthorized_MalformedPubkey(t *testing.T) {
	p1 := secp256k1.GenPrivKey()
	w := &Worker{}
	w.SetLeadersForModel("m1", []string{"a1"}, [][]byte{{0x01, 0x02}}) // 2-byte garbage

	task := makeSignedAssignTask(t, "m1", p1)
	authorized, _ := w.isLeaderAuthorized(task)
	if authorized {
		t.Fatal("expected malformed pubkey to reject")
	}
}

// TestIsLeaderAuthorized_MissingSig: empty LeaderSig field must reject under the new
// regime — the old behavior silently skipped when LeaderSig was empty, which meant an
// adversary could simply omit the field. The bootstrap carve-out is independent.
func TestIsLeaderAuthorized_MissingSig(t *testing.T) {
	p1 := secp256k1.GenPrivKey()
	w := &Worker{}
	w.SetLeadersForModel("m1", []string{"a1"}, [][]byte{p1.PubKey().Bytes()})

	task := makeSignedAssignTask(t, "m1", p1)
	task.LeaderSig = nil

	authorized, _ := w.isLeaderAuthorized(task)
	if authorized {
		t.Fatal("expected empty LeaderSig to reject")
	}
}

// TestSetLeadersForModel_ClearsWhenEmpty: passing an empty list clears the entry.
// After clearing one model, queries to THAT model return unknown while other models
// remain seeded (bootstrap flag stays false — the Worker has seen real data before).
func TestSetLeadersForModel_ClearsWhenEmpty(t *testing.T) {
	p1 := secp256k1.GenPrivKey()
	w := &Worker{}
	w.SetLeadersForModel("m1", []string{"a1"}, [][]byte{p1.PubKey().Bytes()})
	w.SetLeadersForModel("m2", []string{"a2"}, [][]byte{p1.PubKey().Bytes()})

	// Clear m1
	w.SetLeadersForModel("m1", nil, nil)

	task := makeSignedAssignTask(t, "m1", p1)
	authorized, bootstrap := w.isLeaderAuthorized(task)
	if bootstrap {
		t.Fatal("expected bootstrap=false (m2 is still seeded)")
	}
	if authorized {
		t.Fatal("expected unknown model after clear to reject")
	}
}

// ── Audit KT §5: InferReceipt.InferenceLatencyMs is signature-covered ────────
//
// Regression tests that the new field participates in the Worker's signature
// so a MITM cannot rewrite the reported latency to game the Worker's on-chain
// AvgLatencyMs (and thus the VRF speed ranking).

func makeSignedReceipt(t *testing.T, priv secp256k1.PrivKey, inferenceLatencyMs uint32) *p2ptypes.InferReceipt {
	t.Helper()
	r := &p2ptypes.InferReceipt{
		TaskId:             []byte("task-id-latency-1"),
		WorkerPubkey:       priv.PubKey().Bytes(),
		WorkerLogits:       [5]float32{1.0, 2.0, 3.0, 4.0, 5.0},
		ResultHash:         []byte("result-hash-0000"),
		FinalSeed:          []byte("final-seed-00"),
		SampledTokens:      [5]uint32{10, 20, 30, 40, 50},
		InputTokenCount:    42,
		OutputTokenCount:   7,
		InferenceLatencyMs: inferenceLatencyMs,
	}
	sig, err := priv.Sign(r.SignBytes())
	if err != nil {
		t.Fatalf("sign receipt: %v", err)
	}
	r.WorkerSig = sig
	return r
}

// TestInferReceiptSig_ValidRoundTrip: a signed receipt round-trips through
// SignBytes → VerifySignature with the original Worker's pubkey.
func TestInferReceiptSig_ValidRoundTrip(t *testing.T) {
	priv := secp256k1.GenPrivKey()
	r := makeSignedReceipt(t, priv, 250)
	pk := secp256k1.PubKey(r.WorkerPubkey)
	if !pk.VerifySignature(r.SignBytes(), r.WorkerSig) {
		t.Fatal("valid receipt signature must verify")
	}
}

// TestInferReceiptSig_TamperedInferenceLatencyMs: a MITM cannot change the
// reported latency without invalidating the signature.
func TestInferReceiptSig_TamperedInferenceLatencyMs(t *testing.T) {
	priv := secp256k1.GenPrivKey()
	r := makeSignedReceipt(t, priv, 250)

	r.InferenceLatencyMs = 10 // attacker tries to make this Worker look 25× faster

	pk := secp256k1.PubKey(r.WorkerPubkey)
	if pk.VerifySignature(r.SignBytes(), r.WorkerSig) {
		t.Fatal("tampered InferenceLatencyMs must invalidate the Worker signature")
	}
}

// TestInferReceiptSig_DigestChangesWithLatency: sanity check that changing
// only InferenceLatencyMs (all other fields equal) produces a different digest.
// Guards against accidentally dropping the field from SignBytes in a future
// refactor.
func TestInferReceiptSig_DigestChangesWithLatency(t *testing.T) {
	base := &p2ptypes.InferReceipt{
		TaskId:             []byte("task-id-latency-2"),
		WorkerPubkey:       []byte("pk-33-bytes......................"), // 33 chars, non-functional
		WorkerLogits:       [5]float32{0.1, 0.2, 0.3, 0.4, 0.5},
		ResultHash:         []byte("result-hash"),
		FinalSeed:          []byte("final-seed"),
		SampledTokens:      [5]uint32{1, 2, 3, 4, 5},
		InputTokenCount:    10,
		OutputTokenCount:   20,
		InferenceLatencyMs: 100,
	}
	faster := *base
	faster.InferenceLatencyMs = 50

	if string(base.SignBytes()) == string(faster.SignBytes()) {
		t.Fatal("SignBytes must differ when only InferenceLatencyMs differs")
	}
}

// ── S4: AssignTask Leader signature — SigDigest regression tests ─────────────

// TestAssignTaskSigDigest_Deterministic: the same task produces the same digest.
func TestAssignTaskSigDigest_Deterministic(t *testing.T) {
	p1 := secp256k1.GenPrivKey()
	task := makeSignedAssignTask(t, "m1", p1)
	d1 := task.SigDigest()
	d2 := task.SigDigest()
	if d1 != d2 {
		t.Fatal("SigDigest must be deterministic")
	}
}

// TestAssignTaskSigDigest_CoversAllCriticalFields: flipping any signature-covered
// field must change the digest. This guards against accidentally dropping a field
// from the digest when the AssignTask struct grows.
func TestAssignTaskSigDigest_CoversAllCriticalFields(t *testing.T) {
	p1 := secp256k1.GenPrivKey()
	base := makeSignedAssignTask(t, "m1", p1)
	baseDigest := base.SigDigest()

	mutators := map[string]func(*p2ptypes.AssignTask){
		"TaskId":            func(a *p2ptypes.AssignTask) { a.TaskId = []byte("task-id-FFFFFFFF") },
		"ModelId":           func(a *p2ptypes.AssignTask) { a.ModelId = []byte("other-model") },
		"Prompt":            func(a *p2ptypes.AssignTask) { a.Prompt = "different prompt" },
		"UserAddr":          func(a *p2ptypes.AssignTask) { a.UserAddr = []byte("user-addr-999999") },
		"Temperature":       func(a *p2ptypes.AssignTask) { a.Temperature = 7000 },
		"UserSeed":          func(a *p2ptypes.AssignTask) { a.UserSeed = []byte("seed-9999") },
		"DispatchBlockHash": func(a *p2ptypes.AssignTask) { a.DispatchBlockHash = []byte("different-hash-0123") },
		"FeePerInputToken":  func(a *p2ptypes.AssignTask) { a.FeePerInputToken = 500 },
		"FeePerOutputToken": func(a *p2ptypes.AssignTask) { a.FeePerOutputToken = 800 },
		"MaxFee":            func(a *p2ptypes.AssignTask) { a.MaxFee = 9999 },
		"MaxTokens":         func(a *p2ptypes.AssignTask) { a.MaxTokens = 1024 },
	}
	for name, mutate := range mutators {
		t.Run(name, func(t *testing.T) {
			tCopy := *base
			mutate(&tCopy)
			if tCopy.SigDigest() == baseDigest {
				t.Fatalf("SigDigest unchanged after mutating %s — field is not signature-covered", name)
			}
		})
	}
}


// TestShouldStopGeneration_PerRequest verifies no truncation in per-request mode.
// TR2: per-request mode should never stop (relies on max_tokens).
func TestShouldStopGeneration_PerRequest(t *testing.T) {
	task := &p2ptypes.AssignTask{
		Fee:               1000000,
		FeePerInputToken:  0,
		FeePerOutputToken: 0,
	}
	if shouldStopGeneration(task, 100, 500) {
		t.Fatal("per-request mode should never stop generation")
	}
}

// TestShouldStopGeneration_NormalBudget verifies truncation at 95% of max_fee.
// TR1: running_cost reaches budgetLimit → stop.
func TestShouldStopGeneration_NormalBudget(t *testing.T) {
	task := &p2ptypes.AssignTask{
		FeePerInputToken:  100,   // 100 ufai/token
		FeePerOutputToken: 200,   // 200 ufai/token
		MaxFee:            50000, // 50000 ufai
	}
	inputTokens := uint32(100) // input cost = 100 * 100 = 10000
	// budget = 50000 * 95/100 = 47500
	// remaining for output = 47500 - 10000 = 37500
	// max output tokens = 37500 / 200 = 187.5

	// At 187 tokens: cost = 10000 + 187*200 = 47400 < 47500 → don't stop
	if shouldStopGeneration(task, inputTokens, 187) {
		t.Fatal("should not stop at 187 tokens (cost=47400 < budget=47500)")
	}

	// At 188 tokens: cost = 10000 + 188*200 = 47600 >= 47500 → stop
	if !shouldStopGeneration(task, inputTokens, 188) {
		t.Fatal("should stop at 188 tokens (cost=47600 >= budget=47500)")
	}
}

// TestShouldStopGeneration_MinBudget verifies at least 1 output token is generated.
// TR4: extreme small max_fee still generates 1 token.
func TestShouldStopGeneration_MinBudget(t *testing.T) {
	task := &p2ptypes.AssignTask{
		FeePerInputToken:  100,
		FeePerOutputToken: 200,
		MaxFee:            150, // barely enough: input=100, 1 output=200, total=300 > 150
	}
	inputTokens := uint32(1) // input cost = 100

	// budgetLimit = 150 * 95/100 = 142
	// minBudget = 100 + 200 = 300 > 142, so budgetLimit = 300
	// But budgetLimit > MaxFee (300 > 150), so budgetLimit = 150
	// At 0 tokens: cost = 100 + 0 = 100 < 150 → don't stop (can generate 1 token)
	if shouldStopGeneration(task, inputTokens, 0) {
		t.Fatal("should allow at least attempt to generate first token")
	}

	// At 1 token: cost = 100 + 200 = 300 >= 150 → stop
	if !shouldStopGeneration(task, inputTokens, 1) {
		t.Fatal("should stop after 1 token when budget exhausted")
	}
}

// TestShouldStopGeneration_ZeroMaxFee verifies max_fee=0 handling.
func TestShouldStopGeneration_ZeroMaxFee(t *testing.T) {
	task := &p2ptypes.AssignTask{
		FeePerInputToken:  100,
		FeePerOutputToken: 200,
		MaxFee:            0,
	}
	// budgetLimit = 0, minBudget = 100+200 = 300 > 0, capped to MaxFee=0
	// cost at 0 tokens = 100 >= 0 → stop immediately
	if !shouldStopGeneration(task, 1, 0) {
		t.Fatal("should stop immediately when max_fee=0")
	}
}

// TestShouldStopGeneration_ExactBudget verifies boundary at exact budget.
func TestShouldStopGeneration_ExactBudget(t *testing.T) {
	task := &p2ptypes.AssignTask{
		FeePerInputToken:  0,
		FeePerOutputToken: 100,
		MaxFee:            10000,
	}
	// per-request: FeePerInputToken == 0 → returns false
	if shouldStopGeneration(task, 0, 999) {
		t.Fatal("FeePerInputToken=0 should be treated as per-request mode")
	}
}
