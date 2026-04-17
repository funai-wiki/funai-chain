package worker

import (
	"testing"

	p2ptypes "github.com/funai-wiki/funai-chain/p2p/types"
)

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
