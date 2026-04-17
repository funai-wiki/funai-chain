package chain

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestBatchLoop_SequenceReset verifies the sequence manager behavior:
// 1. First call queries chain for account info
// 2. Sequence increments locally on each call
// 3. ResetSequence forces re-query from chain
// E21: sequence mismatch → reset → next attempt uses fresh sequence.
func TestBatchLoop_SequenceReset(t *testing.T) {
	var queryCount atomic.Int32

	// Mock REST server returning account info
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queryCount.Add(1)
		resp := map[string]interface{}{
			"account": map[string]interface{}{
				"account_number": "5",
				"sequence":       fmt.Sprintf("%d", 10+queryCount.Load()-1), // sequence advances on chain
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	c := NewClient("http://localhost:26657", srv.URL)

	// First call: should query chain (queryCount → 1)
	accNum, seq, err := c.getNextSequence(context.Background(), "funai1test")
	if err != nil {
		t.Fatalf("getNextSequence: %v", err)
	}
	if accNum != 5 {
		t.Fatalf("expected accNum=5, got %d", accNum)
	}
	if seq != 10 {
		t.Fatalf("expected seq=10, got %d", seq)
	}
	if queryCount.Load() != 1 {
		t.Fatalf("expected 1 chain query, got %d", queryCount.Load())
	}

	// Second call: should use cached (no new query), seq incremented
	_, seq2, _ := c.getNextSequence(context.Background(), "funai1test")
	if seq2 != 11 {
		t.Fatalf("expected cached seq=11, got %d", seq2)
	}
	if queryCount.Load() != 1 {
		t.Fatalf("expected still 1 chain query (cached), got %d", queryCount.Load())
	}

	// Simulate sequence mismatch → reset
	c.ResetSequence()

	// Third call: should re-query chain (queryCount → 2)
	_, seq3, _ := c.getNextSequence(context.Background(), "funai1test")
	if queryCount.Load() != 2 {
		t.Fatalf("expected 2 chain queries after reset, got %d", queryCount.Load())
	}
	// Fresh sequence from chain (mock returns 10 + queryCount - 1 = 11)
	if seq3 != 11 {
		t.Fatalf("expected fresh seq=11 after reset, got %d", seq3)
	}
}
