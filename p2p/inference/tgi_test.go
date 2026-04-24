package inference

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ── teacherForceTokenByToken — parallel fan-out regression tests ────────────
//
// The fallback used to issue one TGI /generate call per output token in
// strict sequence. On a 34-token Qwen2.5-7B output served via the RunPod
// HTTPS proxy that meant ~17 s of wall-clock per teacher-force pass, plus
// another 17 s for each verifier — see docs/testing/reports/2026-04-23-1047-
// e2e-real-runpod-4090/report.md §13.2 / §14.2. The new implementation
// fans the per-position requests out across goroutines (capped at
// teacherForceMaxConcurrent) but must keep the externally observable
// behaviour identical: same Tokens slice, ordered by output position; same
// error short-circuit; same fallback when /generate returns no Details.

// fakeTGI is an httptest.Server that answers /tokenize and /generate the way
// real TGI v3 does on the teacher-force path: tokenize splits the output
// string into one TokenizeToken per word (good enough for unit testing),
// and /generate returns Details with one Token whose text is the full input
// — the test does not need the response to be tokenwise correct, only to
// be uniquely identifiable per request so we can assert that each position
// got the right input prefix.
type fakeTGI struct {
	server      *httptest.Server
	concurrent  atomic.Int32 // currently in-flight /generate requests
	maxObserved atomic.Int32 // running max of `concurrent`
	generateErr func(reqBody []byte) (statusCode int, respBody string)
	delay       time.Duration // per-/generate delay so we can observe parallelism
}

func newFakeTGI(t *testing.T, outputWords []string) *fakeTGI {
	t.Helper()
	tgi := &fakeTGI{}
	mux := http.NewServeMux()

	mux.HandleFunc("/tokenize", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		toks := make([]TokenizeToken, len(outputWords))
		for i, word := range outputWords {
			toks[i] = TokenizeToken{ID: i + 1, Text: word}
		}
		_ = json.NewEncoder(w).Encode(toks)
	})

	mux.HandleFunc("/generate", func(w http.ResponseWriter, r *http.Request) {
		curr := tgi.concurrent.Add(1)
		defer tgi.concurrent.Add(-1)
		// track running max
		for {
			old := tgi.maxObserved.Load()
			if curr <= old || tgi.maxObserved.CompareAndSwap(old, curr) {
				break
			}
		}
		if tgi.delay > 0 {
			time.Sleep(tgi.delay)
		}

		body, _ := io.ReadAll(r.Body)
		if tgi.generateErr != nil {
			if status, resp := tgi.generateErr(body); status != 0 {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(resp))
				return
			}
		}

		// Decode the request to read the input text — let the response
		// embed it as the produced token text so each position is unique.
		var req GenerateRequest
		_ = json.Unmarshal(body, &req)
		input := req.Inputs

		w.Header().Set("Content-Type", "application/json")
		resp := GenerateResponse{
			GeneratedText: "x",
			Details: &GenerateDetails{
				FinishReason: "length",
				Tokens: []TokenInfo{{
					ID:      99,
					Text:    "echo:" + input,
					Logprob: -1.5,
					TopTokens: []TopTokenInfo{
						{ID: 99, Text: "echo:" + input, Logprob: -1.5},
					},
				}},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	})

	tgi.server = httptest.NewServer(mux)
	t.Cleanup(tgi.server.Close)
	return tgi
}

func (f *fakeTGI) client() *TGIClient {
	return &TGIClient{
		baseURL:    f.server.URL,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// TestTeacherForceTokenByToken_PreservesOrderUnderConcurrency: even with
// per-position TGI calls completing out of submit order, the returned
// Tokens slice must be ordered by output position. This is the core
// regression for the parallel fan-out: a sequential implementation
// trivially preserves order; a parallel one must explicitly index by
// position.
func TestTeacherForceTokenByToken_PreservesOrderUnderConcurrency(t *testing.T) {
	words := []string{"alpha", "beta", "gamma", "delta", "epsilon"}
	tgi := newFakeTGI(t, words)
	tgi.delay = 50 * time.Millisecond // ensure goroutines actually overlap
	c := tgi.client()

	prompt := "say:"
	complete := strings.Join(words, "")
	res, err := c.teacherForceTokenByToken(context.Background(), prompt, complete, len(words))
	if err != nil {
		t.Fatalf("teacherForceTokenByToken: %v", err)
	}
	if res.TokenCount != len(words) {
		t.Fatalf("TokenCount=%d, want %d", res.TokenCount, len(words))
	}

	// The fakeTGI echoes the request input back as the token text. For
	// position i, the expected input is `prompt + words[0..i-1]`. So
	// the returned token text at position i must be `echo:<prompt + prefix>`.
	prefix := ""
	for i, want := range words {
		got := res.Tokens[i].Text
		expected := "echo:" + prompt + prefix
		if got != expected {
			t.Errorf("position %d: token text = %q, want %q", i, got, expected)
		}
		prefix += want
	}
}

// TestTeacherForceTokenByToken_RespectsConcurrencyCap: when the output is
// longer than teacherForceMaxConcurrent, we should never have more than
// teacherForceMaxConcurrent in-flight TGI requests. Without the bounded
// pool we'd open one socket per token.
func TestTeacherForceTokenByToken_RespectsConcurrencyCap(t *testing.T) {
	n := teacherForceMaxConcurrent + 16 // exceed the cap
	words := make([]string, n)
	for i := range words {
		words[i] = fmt.Sprintf("w%d", i)
	}
	tgi := newFakeTGI(t, words)
	tgi.delay = 30 * time.Millisecond
	c := tgi.client()

	res, err := c.teacherForceTokenByToken(context.Background(), "p:", strings.Join(words, ""), n)
	if err != nil {
		t.Fatalf("teacherForceTokenByToken: %v", err)
	}
	if res.TokenCount != n {
		t.Fatalf("TokenCount=%d, want %d", res.TokenCount, n)
	}

	maxObs := tgi.maxObserved.Load()
	if maxObs <= 0 {
		t.Fatalf("expected positive concurrency, observed %d", maxObs)
	}
	if int(maxObs) > teacherForceMaxConcurrent {
		t.Fatalf("max observed concurrent=%d exceeded cap %d", maxObs, teacherForceMaxConcurrent)
	}
	// Sanity check the parallelism actually happened — we should have
	// reached at least 2 concurrent calls (otherwise the test reduces to
	// the sequential case and isn't meaningful).
	if maxObs < 2 {
		t.Errorf("expected meaningful parallelism, max observed %d", maxObs)
	}
}

// TestTeacherForceTokenByToken_ParallelIsFasterThanSerial: the whole point
// of this refactor. With a 30 ms server-side delay per request and 32
// tokens, a sequential implementation takes >= 32 × 30 ms = 960 ms; a
// fully-parallel one is bounded by 30 ms × ceil(32/teacherForceMaxConcurrent)
// + per-request overhead. Allow a generous 500 ms to absorb scheduler
// jitter on shared CI runners.
func TestTeacherForceTokenByToken_ParallelIsFasterThanSerial(t *testing.T) {
	n := 32
	words := make([]string, n)
	for i := range words {
		words[i] = fmt.Sprintf("w%d", i)
	}
	tgi := newFakeTGI(t, words)
	tgi.delay = 30 * time.Millisecond
	c := tgi.client()

	start := time.Now()
	_, err := c.teacherForceTokenByToken(context.Background(), "p:", strings.Join(words, ""), n)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("teacherForceTokenByToken: %v", err)
	}

	// Sequential lower bound at this size: n * delay = 960 ms. Parallel
	// upper bound (with 32-cap and 32 tokens): one wave = 30 ms + slack.
	// Set the bar at 500 ms — comfortably below sequential, comfortably
	// above the theoretical parallel minimum.
	if elapsed > 500*time.Millisecond {
		t.Errorf("parallel teacher-force took %v (>= 500 ms suggests sequential execution)", elapsed)
	}
}

// TestTeacherForceTokenByToken_ShortCircuitsOnError: if any per-position
// TGI request fails, the function returns an error. The cancellation of
// in-flight peers is best-effort — tested via observing that the function
// returns reasonably quickly rather than waiting for all N delays.
func TestTeacherForceTokenByToken_ShortCircuitsOnError(t *testing.T) {
	n := 32
	words := make([]string, n)
	for i := range words {
		words[i] = fmt.Sprintf("w%d", i)
	}
	tgi := newFakeTGI(t, words)
	tgi.delay = 50 * time.Millisecond

	// Make exactly one request fail with 500 — the one whose input
	// contains "w15" (i.e. the request for position 16).
	tgi.generateErr = func(body []byte) (int, string) {
		if strings.Contains(string(body), "w15") {
			return http.StatusInternalServerError, `{"error":"injected"}`
		}
		return 0, ""
	}
	c := tgi.client()

	start := time.Now()
	_, err := c.teacherForceTokenByToken(context.Background(), "p:", strings.Join(words, ""), n)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected error from short-circuited TGI step, got nil")
	}
	// Sequential would have completed all 32 requests = 32 * 50 ms = 1600 ms.
	// Our parallel impl should return as soon as the failing one returns,
	// well under the sequential bound even allowing for in-flight peers
	// to drain.
	if elapsed > 800*time.Millisecond {
		t.Errorf("expected fast short-circuit, took %v", elapsed)
	}
}

// TestTeacherForceTokenByToken_ContextCancel: caller-side cancellation
// must abort the operation cleanly without hanging for in-flight peers.
func TestTeacherForceTokenByToken_ContextCancel(t *testing.T) {
	n := 32
	words := make([]string, n)
	for i := range words {
		words[i] = fmt.Sprintf("w%d", i)
	}
	tgi := newFakeTGI(t, words)
	tgi.delay = 200 * time.Millisecond
	c := tgi.client()

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
		wg.Done()
	}()

	start := time.Now()
	_, err := c.teacherForceTokenByToken(ctx, "p:", strings.Join(words, ""), n)
	elapsed := time.Since(start)
	wg.Wait()

	if err == nil {
		t.Fatal("expected error after context cancel, got nil")
	}
	// Should return well before all delays would have fired sequentially
	// (n × 200 ms = 6.4 s).
	if elapsed > 1*time.Second {
		t.Errorf("context cancel was not honoured: took %v", elapsed)
	}
}

// TestTeacherForceTokenByToken_EmptyOutput: a zero-length output is the
// degenerate case (worker generated nothing). Must not call /generate at
// all and must return a Tokens=nil result with TokenCount=0.
func TestTeacherForceTokenByToken_EmptyOutput(t *testing.T) {
	tgi := newFakeTGI(t, []string{})
	c := tgi.client()

	res, err := c.teacherForceTokenByToken(context.Background(), "p:", "", 0)
	if err != nil {
		t.Fatalf("teacherForceTokenByToken on empty output: %v", err)
	}
	if res.TokenCount != 0 {
		t.Fatalf("TokenCount=%d, want 0", res.TokenCount)
	}
	if len(res.Tokens) != 0 {
		t.Fatalf("Tokens len=%d, want 0", len(res.Tokens))
	}
	// And no /generate calls were made.
	if tgi.maxObserved.Load() != 0 {
		t.Errorf("expected zero /generate calls on empty output, observed %d", tgi.maxObserved.Load())
	}
}

// TestTeacherForceTokenByToken_FallbackOnNoDetails: when TGI returns a
// response with no Details (e.g. older API), we fall back to the
// tokenize-time ID/Text for that position rather than dropping it.
func TestTeacherForceTokenByToken_FallbackOnNoDetails(t *testing.T) {
	words := []string{"a", "b", "c"}
	tgi := newFakeTGI(t, words)

	// Override one position's response to return no Details. The fakeTGI
	// embeds the input text in the produced token, so we identify the
	// "b" position by its prefix `p:a`.
	tgi.generateErr = func(body []byte) (int, string) {
		if strings.Contains(string(body), `"inputs":"p:a"`) {
			return http.StatusOK, `{"generated_text":"x"}`
		}
		return 0, ""
	}
	c := tgi.client()

	res, err := c.teacherForceTokenByToken(context.Background(), "p:", "abc", 3)
	if err != nil {
		t.Fatalf("teacherForceTokenByToken: %v", err)
	}
	if res.TokenCount != 3 {
		t.Fatalf("TokenCount=%d, want 3", res.TokenCount)
	}
	// Position 1 (the "b" position, prompt+prefix "p:a") should have
	// the fallback values from tokenize: ID=2, Text="b".
	if res.Tokens[1].ID != 2 || res.Tokens[1].Text != "b" {
		t.Errorf("position 1 fallback = (id=%d text=%q), want (id=2 text=%q)", res.Tokens[1].ID, res.Tokens[1].Text, "b")
	}
}
