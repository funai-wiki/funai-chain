package keeper_test

// Pre_Mainnet_Test_Plan §2.9 row 5 — verifier-precedes-worker timing attack.
//
// A SecondVerifier off-chain could submit MsgSecondVerificationResult before
// the corresponding Worker receipt has settled (the entry that creates
// SecondVerificationPending). Pre-fix the keeper silently accepted such a
// submission and credited the second_verifier via IncrementSecondVerifierEpochCount,
// even though processAuditJudgment never fired (no pending → no judgment). The
// second_verifier could thus farm epoch reward credit on tasks they had not
// actually verified.
//
// Post-fix (keeper.go ProcessSecondVerificationResult): a missing
// SecondVerificationPending is a hard rejection. SecondVerificationRecord is
// not created and IncrementSecondVerifierEpochCount is not called.
//
// The two complementary tests below pin both sides of the rule:
//
//   - TestTimingAttack_VerifierBeforeReceipt_Rejected: no pending → reject.
//   - TestTimingAttack_VerifierAfterReceipt_Accepted:    pending exists → accept (control).

import (
	"testing"

	"github.com/funai-wiki/funai-chain/x/settlement/types"
)

func TestTimingAttack_VerifierBeforeReceipt_Rejected(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	taskId := []byte("timing-attack-task-001")
	attacker := makeAddr("greedy-verifier").String()

	// Note: NO SetSecondVerificationPending call. This is the attack scenario.
	err := k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
		SecondVerifier: attacker,
		TaskId:         taskId,
		Epoch:          1,
		Pass:           true,
		LogitsHash:     []byte("anything"),
	})
	if err == nil {
		t.Fatal("§2.9 row 5: ProcessSecondVerificationResult must reject when no pending entry exists")
	}

	// Negative consequences must NOT have happened:
	//
	// 1. No SecondVerificationRecord written → judgment cannot fire later
	//    even if pending arrives.
	if _, found := k.GetSecondVerificationRecord(ctx, taskId); found {
		t.Fatal("§2.9 row 5: rejected result must not write a SecondVerificationRecord")
	}

	// 2. No epoch reward credit accumulated for the attacker. We can't
	//    directly inspect SecondVerifierEpochCount via a public getter
	//    convenient for tests, but the rejection-before-Increment path
	//    in keeper.go is the contract being tested; the absence of a
	//    record above is its observable side-effect.
}

func TestTimingAttack_VerifierAfterReceipt_Accepted(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)

	taskId := []byte("timing-attack-control-task")
	verifier := makeAddr("honest-verifier").String()

	// Receipt-side first: simulate what MsgBatchSettlement does on its
	// audit-trigger path.
	seedAuditPending(k, ctx, taskId, []string{makeAddr("orig-control-v1").String()})

	if err := k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
		SecondVerifier: verifier,
		TaskId:         taskId,
		Epoch:          1,
		Pass:           true,
		LogitsHash:     []byte("anything"),
	}); err != nil {
		t.Fatalf("control: result after pending should succeed; got %v", err)
	}

	ar, found := k.GetSecondVerificationRecord(ctx, taskId)
	if !found {
		t.Fatal("control: SecondVerificationRecord should be written when pending exists")
	}
	if len(ar.Results) != 1 || ar.SecondVerifierAddresses[0] != verifier {
		t.Fatalf("control: record contents wrong; results=%v addrs=%v", ar.Results, ar.SecondVerifierAddresses)
	}
}
