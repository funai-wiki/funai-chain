package chain

// Tests for KT 30-case Issue 3 — broadcast confirmation semantics.
//
// Pre-fix: BroadcastTx (and its callers BroadcastSettlement / BroadcastAuditBatch
// / BroadcastFraudProof) returned "success" the moment /broadcast_tx_sync ACKed
// CheckTx. A subsequent DeliverTx failure (or the tx being orphaned and never
// included in a finalized block) was invisible to the caller, who had already
// logged "broadcast hash=…" and moved on. Critical settlement / audit / fraud
// paths could thus be reported as committed while the chain had no record.
//
// Post-fix: BroadcastTxConfirmed wraps BroadcastTx + WaitForTxInclusion; the
// three critical-path callers use it. WaitForTxInclusion polls /tx?hash= and
// returns nil only when the tx was included AND DeliverTx returned code=0.
//
// The tests below exercise both helpers against an httptest server that mimics
// the CometBFT JSON-RPC shape for /broadcast_tx_sync and /tx.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// rpcMux returns an httptest server that simulates a CometBFT RPC endpoint
// for the two paths this fix touches:
//
//   /broadcast_tx_sync?tx=…  — returns broadcastResponse
//   /tx?hash=0x…             — returns txResponseFn(callIdx)
//
// txResponseFn is invoked on each /tx call so tests can simulate "not found
// → eventually included" by returning different shapes on successive calls.
func rpcMux(t *testing.T, broadcastResponse string, txResponseFn func(callIdx int32) string) (*httptest.Server, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var bcCalls, txCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/broadcast_tx_sync"):
			bcCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(broadcastResponse))
		case strings.HasPrefix(r.URL.Path, "/tx"):
			n := txCalls.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(txResponseFn(n)))
		default:
			http.NotFound(w, r)
		}
	}))
	return srv, &bcCalls, &txCalls
}

// ============================================================
// 1. WaitForTxInclusion_Success — tx is found on first poll, code=0 → nil err.
// ============================================================

func TestWaitForTxInclusion_Success(t *testing.T) {
	srv, _, txCalls := rpcMux(t,
		``, // not used
		func(_ int32) string {
			return `{"result":{"hash":"ABCD","height":"42","tx_result":{"code":0,"log":"ok"}}}`
		})
	defer srv.Close()

	c := NewClient(srv.URL, srv.URL)
	if err := c.WaitForTxInclusion(context.Background(), "0xABCD", 5*time.Second); err != nil {
		t.Fatalf("expected nil err on success, got %v", err)
	}
	if txCalls.Load() != 1 {
		t.Fatalf("expected exactly 1 /tx poll, got %d", txCalls.Load())
	}
}

// ============================================================
// 2. WaitForTxInclusion_PollsUntilFound — first 3 polls return error
// ("not found"), 4th returns the included tx → success.
// ============================================================

func TestWaitForTxInclusion_PollsUntilFound(t *testing.T) {
	srv, _, txCalls := rpcMux(t,
		``,
		func(n int32) string {
			if n < 4 {
				return `{"error":{"code":-32603,"message":"Internal error","data":"tx (ABCD) not found"}}`
			}
			return `{"result":{"hash":"ABCD","height":"50","tx_result":{"code":0,"log":""}}}`
		})
	defer srv.Close()

	c := NewClient(srv.URL, srv.URL)
	start := time.Now()
	if err := c.WaitForTxInclusion(context.Background(), "0xABCD", 5*time.Second); err != nil {
		t.Fatalf("expected nil err once tx found, got %v", err)
	}
	if txCalls.Load() < 4 {
		t.Fatalf("expected at least 4 /tx polls, got %d", txCalls.Load())
	}
	// 3 sleeps × 500ms baseline, plus call latency.
	if elapsed := time.Since(start); elapsed < 1*time.Second {
		t.Fatalf("expected >= 1s elapsed (3 poll intervals), got %s", elapsed)
	}
}

// ============================================================
// 3. WaitForTxInclusion_DeliverTxFailed — tx included with non-zero code.
// ============================================================

func TestWaitForTxInclusion_DeliverTxFailed(t *testing.T) {
	srv, _, _ := rpcMux(t, ``,
		func(_ int32) string {
			return `{"result":{"hash":"DEAD","height":"77","tx_result":{"code":18,"log":"insufficient gas"}}}`
		})
	defer srv.Close()

	c := NewClient(srv.URL, srv.URL)
	err := c.WaitForTxInclusion(context.Background(), "0xDEAD", 2*time.Second)
	if err == nil {
		t.Fatal("expected error when DeliverTx code != 0")
	}
	if !strings.Contains(err.Error(), "code=18") {
		t.Fatalf("error should mention DeliverTx code, got: %v", err)
	}
	if !strings.Contains(err.Error(), "insufficient gas") {
		t.Fatalf("error should propagate DeliverTx log, got: %v", err)
	}
}

// ============================================================
// 4. WaitForTxInclusion_Timeout — tx never included within window.
// ============================================================

func TestWaitForTxInclusion_Timeout(t *testing.T) {
	srv, _, txCalls := rpcMux(t, ``,
		func(_ int32) string {
			return `{"error":{"code":-32603,"message":"Internal error","data":"tx (NOT-FOUND) not found"}}`
		})
	defer srv.Close()

	c := NewClient(srv.URL, srv.URL)
	start := time.Now()
	err := c.WaitForTxInclusion(context.Background(), "0xNOTFOUND", 1500*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "not found in any block") {
		t.Fatalf("error should mention timeout/not-found, got: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 1500*time.Millisecond {
		t.Fatalf("expected >= 1.5s elapsed (timeout window), got %s", elapsed)
	}
	// Some poll attempts were made.
	if txCalls.Load() < 2 {
		t.Fatalf("expected multiple poll attempts before timeout, got %d", txCalls.Load())
	}
}

// ============================================================
// 5. WaitForTxInclusion_ContextCanceled — caller cancels mid-poll.
// ============================================================

func TestWaitForTxInclusion_ContextCanceled(t *testing.T) {
	srv, _, _ := rpcMux(t, ``,
		func(_ int32) string {
			return `{"error":{"code":-32603,"message":"Internal error","data":"tx not found"}}`
		})
	defer srv.Close()

	c := NewClient(srv.URL, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()

	err := c.WaitForTxInclusion(ctx, "0xCANCEL", 30*time.Second)
	if err == nil {
		t.Fatal("expected context-canceled error")
	}
	if !strings.Contains(err.Error(), "canceled") && !strings.Contains(err.Error(), "context") {
		t.Fatalf("error should reflect cancellation, got: %v", err)
	}
}

// ============================================================
// 6. BroadcastTxConfirmed_Success — full happy path: broadcast accepted,
// tx included, DeliverTx code=0.
// ============================================================

func TestBroadcastTxConfirmed_Success(t *testing.T) {
	srv, bcCalls, txCalls := rpcMux(t,
		`{"result":{"hash":"AAAA","code":0,"log":""}}`,
		func(_ int32) string {
			return `{"result":{"hash":"AAAA","height":"100","tx_result":{"code":0,"log":""}}}`
		})
	defer srv.Close()

	c := NewClient(srv.URL, srv.URL)
	hash, err := c.BroadcastTxConfirmed(context.Background(), []byte("dummy-tx"), 3*time.Second)
	if err != nil {
		t.Fatalf("expected confirmed success, got %v", err)
	}
	if hash != "AAAA" {
		t.Fatalf("expected hash AAAA, got %s", hash)
	}
	if bcCalls.Load() != 1 || txCalls.Load() != 1 {
		t.Fatalf("expected 1 broadcast + 1 tx poll, got bc=%d tx=%d", bcCalls.Load(), txCalls.Load())
	}
}

// ============================================================
// 7. BroadcastTxConfirmed_BroadcastReject — CheckTx returns non-zero code.
// No /tx polling should occur.
// ============================================================

func TestBroadcastTxConfirmed_BroadcastReject(t *testing.T) {
	srv, bcCalls, txCalls := rpcMux(t,
		`{"result":{"hash":"","code":7,"log":"signature verification failed"}}`,
		func(_ int32) string {
			return `{"result":{"hash":"AAAA","height":"100","tx_result":{"code":0}}}`
		})
	defer srv.Close()

	c := NewClient(srv.URL, srv.URL)
	hash, err := c.BroadcastTxConfirmed(context.Background(), []byte("bad-tx"), 3*time.Second)
	if err == nil {
		t.Fatal("expected broadcast-reject error")
	}
	if hash != "" {
		t.Fatalf("hash must be empty when CheckTx fails, got %s", hash)
	}
	if !strings.Contains(err.Error(), "code=7") {
		t.Fatalf("error should mention CheckTx code, got: %v", err)
	}
	if txCalls.Load() != 0 {
		t.Fatalf("no /tx polls should occur on broadcast reject, got %d", txCalls.Load())
	}
	if bcCalls.Load() != 1 {
		t.Fatalf("expected exactly 1 broadcast call, got %d", bcCalls.Load())
	}
}

// ============================================================
// 8. BroadcastTxConfirmed_DeliverTxFailedAfterBroadcast — broadcast accepted,
// tx included, but DeliverTx returns non-zero code. Caller must see hash
// (so it can log diagnostics) AND a wrapping error explaining the failure.
// ============================================================

func TestBroadcastTxConfirmed_DeliverTxFailedAfterBroadcast(t *testing.T) {
	srv, _, _ := rpcMux(t,
		`{"result":{"hash":"BBBB","code":0,"log":""}}`,
		func(_ int32) string {
			return `{"result":{"hash":"BBBB","height":"50","tx_result":{"code":11,"log":"out of gas"}}}`
		})
	defer srv.Close()

	c := NewClient(srv.URL, srv.URL)
	hash, err := c.BroadcastTxConfirmed(context.Background(), []byte("oog-tx"), 3*time.Second)
	if err == nil {
		t.Fatal("expected DeliverTx-failed error")
	}
	if hash != "BBBB" {
		t.Fatalf("expected non-empty hash even on confirm-fail (for diagnostics), got %s", hash)
	}
	if !strings.Contains(err.Error(), "broadcast accepted") {
		t.Fatalf("error should distinguish 'broadcast accepted' from confirmation fail, got: %v", err)
	}
	if !strings.Contains(err.Error(), "out of gas") {
		t.Fatalf("error should propagate DeliverTx log, got: %v", err)
	}
}

// ============================================================
// 9. BroadcastTx_UsesSyncEndpoint — pin that BroadcastTx (the low-level
// helper) uses /broadcast_tx_sync (CheckTx-only). This is the contract the
// confirmation layer rests on; if a refactor silently switches to
// /broadcast_tx_commit or async, the BroadcastTxConfirmed semantics break.
// ============================================================

func TestBroadcastTx_UsesSyncEndpoint(t *testing.T) {
	var observed atomic.Value // string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed.Store(r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"result":{"hash":"CCCC","code":0,"log":""}}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, srv.URL)
	if _, err := c.BroadcastTx(context.Background(), []byte("anything")); err != nil {
		t.Fatalf("BroadcastTx error: %v", err)
	}
	got, _ := observed.Load().(string)
	if got != "/broadcast_tx_sync" {
		t.Fatalf("BroadcastTx must use /broadcast_tx_sync (CheckTx-only); observed: %s", got)
	}
}

// ============================================================
// 10. WaitForTxInclusion respects DefaultConfirmTimeout when caller passes 0.
// ============================================================

func TestWaitForTxInclusion_DefaultTimeout(t *testing.T) {
	// Default timeout is 60s — far too long for a unit test. We verify it's
	// applied (rather than picking 0 or a tiny fallback) by ensuring the
	// helper doesn't return immediately. We cap the test's exposure to the
	// default by canceling the context shortly after.
	srv, _, _ := rpcMux(t, ``, func(_ int32) string {
		return `{"error":{"code":-32603,"message":"Internal error","data":"tx not found"}}`
	})
	defer srv.Close()

	c := NewClient(srv.URL, srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := c.WaitForTxInclusion(ctx, "0xANY", 0) // 0 → default
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error from context cancel within default-timeout window")
	}
	// Confirms the helper kept polling for ~800ms — ie did NOT exit early
	// because of a misinterpreted 0-timeout.
	if elapsed < 700*time.Millisecond {
		t.Fatalf("default timeout should NOT short-circuit; elapsed %s", elapsed)
	}
	// Sanity-check error wording mentions cancel/context (not "timeout 0s").
	if strings.Contains(err.Error(), "0s") {
		t.Fatalf("default-timeout case must not reference 0s timeout, got: %v", err)
	}
}

// helper — silence unused-import lint when only some tests run.
var _ = json.Marshal
var _ = fmt.Sprintf
