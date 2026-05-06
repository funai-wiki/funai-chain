package types

// Tests for chain-side hard cap on MsgBatchSettlement entries.
//
// Pre-fix: ValidateBasic had no upper bound on len(Entries). A malicious or
// buggy proposer could submit an unbounded batch and OOM every validator.
// Post-fix: MaxBatchSettlementEntries enforces a 1300-entry ceiling matched
// to the binding constraint (CometBFT mempool 1 MiB, ~783 B per entry).

import (
	"strings"
	"testing"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

func makeSettlementEntry(taskByte byte) SettlementEntry {
	taskId := make([]byte, 32)
	for i := range taskId {
		taskId[i] = taskByte
	}
	user := sdk.AccAddress(make([]byte, 20)).String()
	return SettlementEntry{
		TaskId:        taskId,
		UserAddress:   user,
		WorkerAddress: user,
		Fee:           sdk.NewCoin("ufai", math.NewInt(1)),
		Status:        SettlementSuccess,
		ExpireBlock:   200,
	}
}

func makeBatchMsg(t *testing.T, n int) *MsgBatchSettlement {
	t.Helper()
	entries := make([]SettlementEntry, n)
	for i := range entries {
		entries[i] = makeSettlementEntry(byte(i))
	}
	return &MsgBatchSettlement{
		Proposer:    sdk.AccAddress(make([]byte, 20)).String(),
		MerkleRoot:  []byte("merkle-root-placeholder"),
		Entries:     entries,
		ProposerSig: []byte("sig-placeholder"),
		ResultCount: uint32(n),
	}
}

func TestMsgBatchSettlement_ValidateBasic_AcceptsAtMaxEntries(t *testing.T) {
	msg := makeBatchMsg(t, MaxBatchSettlementEntries)
	if err := msg.ValidateBasic(); err != nil {
		t.Fatalf("max-size batch (%d) must validate, got: %v", MaxBatchSettlementEntries, err)
	}
}

func TestMsgBatchSettlement_ValidateBasic_RejectsOverMax(t *testing.T) {
	msg := makeBatchMsg(t, MaxBatchSettlementEntries+1)
	err := msg.ValidateBasic()
	if err == nil {
		t.Fatalf("over-max batch (%d) must be rejected", MaxBatchSettlementEntries+1)
	}
	if !strings.Contains(err.Error(), "exceeds max") {
		t.Fatalf("error must reference the cap, got: %v", err)
	}
}
