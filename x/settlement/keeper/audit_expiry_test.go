package keeper_test

// Audit §1 / §2 — SecondVerificationPending ExpireBlock enforcement.
//
// `ProcessSecondVerificationResult` previously checked that a pending
// audit existed but did NOT check whether the pending had passed its
// `ExpireBlock`. A late second_verifier could resurrect an expired
// pending entry and reroute its settlement on stale evidence, even
// though `HandleSecondVerificationTimeouts` is the only intended path
// for finalising past-expiry audits. The audit checklist
// (docs/mainnet-readiness/同型信任边界问题工程验证清单.md §1) flagged
// this as a P0.

import (
	"testing"

	"cosmossdk.io/math"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/funai-wiki/funai-chain/x/settlement/types"
)

func TestAudit_Item1_SecondVerificationResult_RejectsPastExpiry(t *testing.T) {
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	taskId := []byte("audit-i1-expired-task")
	expireBlock := int64(50)

	k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       int64(40),
		WorkerAddress:     makeAddr("audit-i1-worker").String(),
		UserAddress:       makeAddr("audit-i1-user").String(),
		VerifierAddresses: []string{makeAddr("audit-i1-orig-v1").String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       expireBlock,
	})

	// Advance ctx past ExpireBlock — the timeout sweep is the only legal path
	// to finalise an expired audit; a late audit-result submission must fail.
	futureCtx := ctx.WithBlockHeader(cmtproto.Header{Height: expireBlock + 1})

	err := k.ProcessSecondVerificationResult(futureCtx, &types.MsgSecondVerificationResult{
		SecondVerifier: makeAddr("audit-i1-aud-v0").String(),
		TaskId:         taskId,
		Epoch:          1,
		Pass:           true,
		LogitsHash:     []byte("h"),
	})
	if err == nil {
		t.Fatal("audit §1: ProcessSecondVerificationResult must reject results past ExpireBlock")
	}
	// No SecondVerificationRecord should be created from a rejected late submission.
	if _, found := k.GetSecondVerificationRecord(futureCtx, taskId); found {
		t.Fatal("audit §1: no audit record may be written when the submission is rejected for expiry")
	}
}

func TestAudit_Item1_SecondVerificationResult_AcceptsBeforeExpiry(t *testing.T) {
	// Sanity / regression: inside the window, the existing happy path holds.
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	taskId := []byte("audit-i1-inside-task1")
	k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       ctx.BlockHeight(),
		WorkerAddress:     makeAddr("audit-i1-w-in").String(),
		UserAddress:       makeAddr("audit-i1-u-in").String(),
		VerifierAddresses: []string{makeAddr("audit-i1-orig-v-in").String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       ctx.BlockHeight() + 10000,
	})

	err := k.ProcessSecondVerificationResult(ctx, &types.MsgSecondVerificationResult{
		SecondVerifier: makeAddr("audit-i1-aud-in").String(),
		TaskId:         taskId,
		Epoch:          1,
		Pass:           true,
		LogitsHash:     []byte("h"),
	})
	if err != nil {
		t.Fatalf("audit §1: inside-window submission must succeed, got: %v", err)
	}
}

func TestAudit_Item1_SecondVerificationResult_GracefulOnLegacyZeroExpire(t *testing.T) {
	// Legacy migration: pending entries written before this audit fix may
	// have ExpireBlock=0. The check must skip rather than reject — so
	// legacy in-flight audits don't all fail post-upgrade.
	k, ctx, _, _ := setupKeeper(t)
	k.SetCurrentSecondVerificationRate(ctx, 0)

	taskId := []byte("audit-i1-legacy-zero1")
	k.SetSecondVerificationPending(ctx, types.SecondVerificationPendingTask{
		TaskId:            taskId,
		OriginalStatus:    types.SettlementSuccess,
		SubmittedAt:       int64(40),
		WorkerAddress:     makeAddr("audit-i1-w-leg").String(),
		UserAddress:       makeAddr("audit-i1-u-leg").String(),
		VerifierAddresses: []string{makeAddr("audit-i1-orig-leg").String()},
		Fee:               sdk.NewCoin("ufai", math.NewInt(1_000_000)),
		ExpireBlock:       0, // legacy
	})

	// Even a "far-future" ctx should not reject when ExpireBlock==0.
	futureCtx := ctx.WithBlockHeader(cmtproto.Header{Height: 1_000_000})
	err := k.ProcessSecondVerificationResult(futureCtx, &types.MsgSecondVerificationResult{
		SecondVerifier: makeAddr("audit-i1-aud-leg").String(),
		TaskId:         taskId,
		Epoch:          1,
		Pass:           true,
		LogitsHash:     []byte("h"),
	})
	if err != nil {
		t.Fatalf("audit §1: legacy ExpireBlock=0 must skip expiry check, got: %v", err)
	}
}
