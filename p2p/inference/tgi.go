package inference

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/chacha20"
)

// TGIClient wraps HuggingFace Text Generation Inference HTTP API.
type TGIClient struct {
	baseURL    string
	httpClient *http.Client
	tgiMajor   int // 0 = unknown, 2 = v2, 3 = v3+; set by DetectVersion()
}

// authTransport injects a Bearer token into every outgoing HTTP request.
type authTransport struct {
	token string
	base  http.RoundTripper
}

func (t *authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(req)
}

func NewTGIClient(baseURL string) *TGIClient {
	return &TGIClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

// SetAuthToken configures a Bearer token for all TGI HTTP requests.
// Used when TGI is behind a reverse proxy with token authentication.
func (c *TGIClient) SetAuthToken(token string) {
	if token == "" {
		return
	}
	c.httpClient.Transport = &authTransport{
		token: token,
		base:  http.DefaultTransport,
	}
}

// IsHealthy checks TGI /health endpoint to detect OOM or crash states (G5).
// Returns false if TGI is unhealthy or unreachable, preventing new task acceptance.
func (c *TGIClient) IsHealthy(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// DetectVersion queries GET /info and sets the TGI major version.
func (c *TGIClient) DetectVersion() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/info", nil)
	if err != nil {
		return
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	var info struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil || info.Version == "" {
		return
	}
	// Parse major version from "3.3.6-dev0", "2.0.4", etc.
	parts := strings.SplitN(info.Version, ".", 2)
	if len(parts) > 0 {
		var major int
		fmt.Sscanf(parts[0], "%d", &major)
		if major > 0 {
			c.tgiMajor = major
		}
	}
	log.Printf("TGI version detected: %s (major=%d)", info.Version, c.tgiMajor)
}

// TGIMajor returns the detected TGI major version (0 if not detected).
func (c *TGIClient) TGIMajor() int { return c.tgiMajor }

// BackendName returns "tgi".
func (c *TGIClient) BackendName() string { return "tgi" }

// Ensure TGIClient implements Engine interface.
var _ Engine = (*TGIClient)(nil)

// TGI request/response types

type GenerateRequest struct {
	Inputs     string         `json:"inputs"`
	Parameters GenerateParams `json:"parameters"`
}

type GenerateParams struct {
	MaxNewTokens        int     `json:"max_new_tokens,omitempty"`
	Temperature         float32 `json:"temperature,omitempty"`
	TopP                float32 `json:"top_p,omitempty"` // nucleus sampling threshold (0 or 1.0 = disabled)
	DoSample            bool    `json:"do_sample,omitempty"`
	ReturnFullText      bool    `json:"return_full_text,omitempty"`
	Details             bool    `json:"details"`
	Seed                *int64  `json:"seed,omitempty"`
	DecoderInputDetails bool    `json:"decoder_input_details,omitempty"`
	TopNTokens          int     `json:"top_n_tokens,omitempty"` // S1: return top-N logprobs per position
}

type GenerateResponse struct {
	GeneratedText string           `json:"generated_text"`
	Details       *GenerateDetails `json:"details,omitempty"`
}

type GenerateDetails struct {
	FinishReason string           `json:"finish_reason"`
	Tokens       []TokenInfo      `json:"tokens"`
	Prefill      []TokenInfo      `json:"prefill"`
	TopTokens    [][]TopTokenInfo `json:"top_tokens,omitempty"` // TGI v3: per-position top-N at details level
}

type TokenInfo struct {
	ID        int            `json:"id"`
	Text      string         `json:"text"`
	Logprob   float32        `json:"logprob"`
	Special   bool           `json:"special"`
	TopTokens []TopTokenInfo `json:"top_tokens,omitempty"` // S1: top-N alternatives at this position
}

// TopTokenInfo represents one of the top-N token alternatives at a position.
type TopTokenInfo struct {
	ID      int     `json:"id"`
	Text    string  `json:"text"`
	Logprob float32 `json:"logprob"`
}

// UnmarshalJSON handles both TGI v2 "logprob" and v3 "log_prob" field names.
func (t *TokenInfo) UnmarshalJSON(data []byte) error {
	type Alias TokenInfo
	aux := &struct {
		LogProb *float32 `json:"log_prob,omitempty"`
		*Alias
	}{Alias: (*Alias)(t)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	if t.Logprob == 0 && aux.LogProb != nil {
		t.Logprob = *aux.LogProb
	}
	return nil
}

// UnmarshalJSON handles both TGI v2 "logprob" and v3 "log_prob" field names.
func (t *TopTokenInfo) UnmarshalJSON(data []byte) error {
	type Alias TopTokenInfo
	aux := &struct {
		LogProb *float32 `json:"log_prob,omitempty"`
		*Alias
	}{Alias: (*Alias)(t)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}
	if t.Logprob == 0 && aux.LogProb != nil {
		t.Logprob = *aux.LogProb
	}
	return nil
}

type StreamResponse struct {
	Token         *TokenInfo       `json:"token,omitempty"`
	GeneratedText string           `json:"generated_text,omitempty"`
	Details       *GenerateDetails `json:"details,omitempty"`
}

// InferenceResult holds a complete inference output.
type InferenceResult struct {
	Output          string
	Tokens          []TokenInfo
	TokenCount      int // output token count
	InputTokenCount int // S9: input token count (from prompt tokenization)
}

// Complete runs inference and returns the full result with logits.
func (c *TGIClient) Complete(ctx context.Context, prompt string, maxTokens int, temperature float32, topP float32, seed *int64) (*InferenceResult, error) {
	doSample := temperature > 0
	req := GenerateRequest{
		Inputs: prompt,
		Parameters: GenerateParams{
			MaxNewTokens:   maxTokens,
			Temperature:    temperature,
			TopP:           topP,
			DoSample:       doSample,
			ReturnFullText: false,
			Details:        true,
			Seed:           seed,
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/generate", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("TGI request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("TGI error %d: %s", resp.StatusCode, string(respBody))
	}

	var genResp GenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	result := &InferenceResult{
		Output: genResp.GeneratedText,
	}

	if genResp.Details != nil {
		result.Tokens = genResp.Details.Tokens
		result.TokenCount = len(genResp.Details.Tokens)
		if genResp.Details.Prefill != nil {
			result.InputTokenCount = len(genResp.Details.Prefill)
		}
	}

	return result, nil
}

// Stream runs inference and streams tokens via a channel.
func (c *TGIClient) Stream(ctx context.Context, prompt string, maxTokens int, temperature float32, topP float32, seed *int64) (<-chan StreamToken, <-chan error) {
	tokenCh := make(chan StreamToken, 100)
	errCh := make(chan error, 1)

	go func() {
		defer close(tokenCh)
		defer close(errCh)

		doSample := temperature > 0
		req := GenerateRequest{
			Inputs: prompt,
			Parameters: GenerateParams{
				MaxNewTokens:   maxTokens,
				Temperature:    temperature,
				TopP:           topP,
				DoSample:       doSample,
				ReturnFullText: false,
				Details:        true,
				Seed:           seed,
			},
		}

		body, err := json.Marshal(req)
		if err != nil {
			errCh <- fmt.Errorf("marshal: %w", err)
			return
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/generate_stream", bytes.NewReader(body))
		if err != nil {
			errCh <- fmt.Errorf("create request: %w", err)
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			errCh <- fmt.Errorf("TGI stream: %w", err)
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			respBody, _ := io.ReadAll(resp.Body)
			errCh <- fmt.Errorf("TGI error %d: %s", resp.StatusCode, string(respBody))
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		index := uint32(0)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimPrefix(line, "data:")
			data = strings.TrimSpace(data)

			var sr StreamResponse
			if err := json.Unmarshal([]byte(data), &sr); err != nil {
				continue
			}

			if sr.Token != nil {
				tokenCh <- StreamToken{
					Text:    sr.Token.Text,
					TokenID: sr.Token.ID,
					Index:   index,
					IsFinal: sr.GeneratedText != "",
				}
				index++
			}
		}
	}()

	return tokenCh, errCh
}

// StreamToken represents a single streamed token.
type StreamToken struct {
	Text    string
	TokenID int
	Index   uint32
	IsFinal bool
}

// TeacherForce runs teacher forcing: given prompt + complete output,
// returns logprobs at all output positions via TGI decoder_input_details.
// Used by verifiers to reproduce the model's predictions at each output token.
func (c *TGIClient) TeacherForce(ctx context.Context, prompt string, completeOutput string, outputTokenCount int) (*InferenceResult, error) {
	fullInput := prompt + completeOutput
	req := GenerateRequest{
		Inputs: fullInput,
		Parameters: GenerateParams{
			MaxNewTokens:        1,
			Temperature:         0,
			ReturnFullText:      false,
			Details:             true,
			DecoderInputDetails: true,
			TopNTokens:          256, // §8.3: need enough tokens for full-vocab CDF coverage
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/generate", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("TGI request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("TGI error %d: %s", resp.StatusCode, string(respBody))
	}

	var genResp GenerateResponse
	if err := json.NewDecoder(resp.Body).Decode(&genResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	result := &InferenceResult{
		Output: completeOutput,
	}

	if genResp.Details != nil && len(genResp.Details.Prefill) > 0 {
		prefill := genResp.Details.Prefill
		startIdx := len(prefill) - outputTokenCount
		if startIdx < 0 {
			startIdx = 0
		}
		result.Tokens = prefill[startIdx:]
		result.TokenCount = len(result.Tokens)
	}

	// TGI v3 may not support decoder_input_details — fallback to token-by-token.
	if result.TokenCount == 0 {
		log.Printf("TeacherForce: prefill empty (TGI v3?), falling back to token-by-token")
		return c.teacherForceTokenByToken(ctx, prompt, completeOutput, outputTokenCount)
	}

	return result, nil
}

// teacherForceTokenByToken replays inference one token at a time to collect logprobs.
// Used as a fallback when TGI v3 does not support decoder_input_details.
func (c *TGIClient) teacherForceTokenByToken(ctx context.Context, prompt string, completeOutput string, outputTokenCount int) (*InferenceResult, error) {
	// Tokenize the output to get real token boundaries.
	outputTokens, err := c.Tokenize(ctx, completeOutput)
	if err != nil {
		return nil, fmt.Errorf("teacherForceTokenByToken tokenize: %w", err)
	}

	// Reconstruct cumulative text prefixes from token texts.
	// Each step: send prompt + output_prefix, generate 1 token, collect logprobs.
	var allTokens []TokenInfo
	outputPrefix := ""
	for i, tok := range outputTokens {
		input := prompt + outputPrefix
		req := GenerateRequest{
			Inputs: input,
			Parameters: GenerateParams{
				MaxNewTokens: 1,
				Temperature:  0,
				DoSample:     false,
				Details:      true,
				TopNTokens:   256,
			},
		}
		body, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("teacherForceTokenByToken marshal step %d: %w", i, err)
		}
		httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/generate", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("teacherForceTokenByToken request step %d: %w", i, err)
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("teacherForceTokenByToken TGI step %d: %w", i, err)
		}
		var genResp GenerateResponse
		err = json.NewDecoder(resp.Body).Decode(&genResp)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("teacherForceTokenByToken decode step %d: %w", i, err)
		}

		if genResp.Details != nil && len(genResp.Details.Tokens) > 0 {
			t := genResp.Details.Tokens[0]
			// TGI v3: merge top_tokens from details level into token
			if len(t.TopTokens) == 0 && len(genResp.Details.TopTokens) > 0 {
				t.TopTokens = genResp.Details.TopTokens[0]
			}
			allTokens = append(allTokens, t)
		} else {
			// No detail at this position — insert a placeholder.
			allTokens = append(allTokens, TokenInfo{ID: tok.ID, Text: tok.Text})
		}
		outputPrefix += tok.Text
	}

	return &InferenceResult{
		Output:     completeOutput,
		Tokens:     allTokens,
		TokenCount: len(allTokens),
	}, nil
}

// DeterministicGenerate runs inference with ChaCha20 deterministic sampling (spec §8.3).
// Instead of using TGI's native sampler, this method:
// 1. Calls TGI with temperature=0 + top_n_tokens=256 to get logits at each step
// 2. Applies temperature scaling + softmax + ChaCha20 CDF sampling locally
// This ensures Worker and Verifier produce identical tokens for the same seed.
// Returns the generated tokens (with text and top_tokens at each position) and full output.
func (c *TGIClient) DeterministicGenerate(ctx context.Context, prompt string, maxTokens int, temperature float32, finalSeed []byte) (*InferenceResult, error) {
	if temperature <= 0 {
		return c.Complete(ctx, prompt, maxTokens, 0, 0, nil)
	}

	var output strings.Builder
	var allTokens []TokenInfo
	currentPrompt := prompt

	for step := 0; step < maxTokens; step++ {
		req := GenerateRequest{
			Inputs: currentPrompt,
			Parameters: GenerateParams{
				MaxNewTokens:   1,
				Temperature:    0,
				DoSample:       false,
				ReturnFullText: false,
				Details:        true,
				TopNTokens:     256,
			},
		}

		body, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("marshal step %d: %w", step, err)
		}

		httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/generate", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request step %d: %w", step, err)
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("TGI step %d: %w", step, err)
		}

		var genResp GenerateResponse
		err = json.NewDecoder(resp.Body).Decode(&genResp)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode step %d: %w", step, err)
		}

		if genResp.Details == nil || len(genResp.Details.Tokens) == 0 {
			break
		}

		greedyToken := genResp.Details.Tokens[0]

		// TGI v3: top_tokens is at details level, not per-token
		topK := greedyToken.TopTokens
		if len(topK) == 0 && len(genResp.Details.TopTokens) > 0 {
			topK = genResp.Details.TopTokens[0]
		}
		if len(topK) == 0 {
			topK = []TopTokenInfo{{ID: greedyToken.ID, Logprob: greedyToken.Logprob}}
		}

		selectedID := chacha20SelectToken(topK, temperature, finalSeed, uint64(step))
		selectedText := greedyToken.Text
		for _, t := range topK {
			if t.ID == selectedID {
				selectedText = t.Text
				break
			}
		}

		tokenInfo := TokenInfo{
			ID:        selectedID,
			Text:      selectedText,
			Logprob:   greedyToken.Logprob,
			TopTokens: topK,
		}
		allTokens = append(allTokens, tokenInfo)
		output.WriteString(selectedText)
		currentPrompt += selectedText

		if genResp.Details.FinishReason == "eos_token" || genResp.Details.FinishReason == "stop" {
			break
		}
	}

	// S9: get input token count via Tokenize (best effort)
	inputTokenCount := 0
	if inputTokens, err := c.Tokenize(ctx, prompt); err == nil {
		inputTokenCount = len(inputTokens)
	}

	return &InferenceResult{
		Output:          output.String(),
		Tokens:          allTokens,
		TokenCount:      len(allTokens),
		InputTokenCount: inputTokenCount,
	}, nil
}

// DeterministicGenerateWithBudget is like DeterministicGenerate but accepts a stop callback.
// S9 §2.4: Worker stops when shouldStop returns true (per-token budget exhausted).
func (c *TGIClient) DeterministicGenerateWithBudget(ctx context.Context, prompt string, maxTokens int, temperature float32, finalSeed []byte, shouldStop func(outputTokens uint32) bool) (*InferenceResult, error) {
	if temperature <= 0 {
		return c.Complete(ctx, prompt, maxTokens, 0, 0, nil)
	}

	var output strings.Builder
	var allTokens []TokenInfo
	currentPrompt := prompt

	for step := 0; step < maxTokens; step++ {
		// S9: check budget before each TGI call
		if shouldStop != nil && shouldStop(uint32(step)) {
			break
		}

		req := GenerateRequest{
			Inputs: currentPrompt,
			Parameters: GenerateParams{
				MaxNewTokens:   1,
				Temperature:    0,
				DoSample:       false,
				ReturnFullText: false,
				Details:        true,
				TopNTokens:     256,
			},
		}

		body, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("marshal step %d: %w", step, err)
		}
		httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/generate", bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("create request step %d: %w", step, err)
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("TGI step %d: %w", step, err)
		}
		var genResp GenerateResponse
		err = json.NewDecoder(resp.Body).Decode(&genResp)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("decode step %d: %w", step, err)
		}

		if genResp.Details == nil || len(genResp.Details.Tokens) == 0 {
			break
		}

		greedyToken := genResp.Details.Tokens[0]
		topK := greedyToken.TopTokens
		if len(topK) == 0 && len(genResp.Details.TopTokens) > 0 {
			topK = genResp.Details.TopTokens[0]
		}
		if len(topK) == 0 {
			topK = []TopTokenInfo{{ID: greedyToken.ID, Logprob: greedyToken.Logprob}}
		}

		selectedID := chacha20SelectToken(topK, temperature, finalSeed, uint64(step))
		selectedText := greedyToken.Text
		for _, t := range topK {
			if t.ID == selectedID {
				selectedText = t.Text
				break
			}
		}

		allTokens = append(allTokens, TokenInfo{
			ID: selectedID, Text: selectedText, Logprob: greedyToken.Logprob, TopTokens: topK,
		})
		output.WriteString(selectedText)
		currentPrompt += selectedText

		if genResp.Details.FinishReason == "eos_token" || genResp.Details.FinishReason == "stop" {
			break
		}
	}

	inputTokenCount := 0
	if inputTokens, err := c.Tokenize(ctx, prompt); err == nil {
		inputTokenCount = len(inputTokens)
	}

	return &InferenceResult{
		Output:          output.String(),
		Tokens:          allTokens,
		TokenCount:      len(allTokens),
		InputTokenCount: inputTokenCount,
	}, nil
}

// chacha20SelectToken applies ChaCha20 deterministic sampling over top-K logprobs.
// Mirrors the logic in verifier's chacha20Sample for consistency.
func chacha20SelectToken(topK []TopTokenInfo, temperature float32, seed []byte, position uint64) int {
	if len(topK) == 0 {
		return 0
	}
	if temperature <= 0 {
		return topK[0].ID
	}

	type tp struct {
		id     int
		scaled float32
	}
	sorted := make([]tp, len(topK))
	for i, t := range topK {
		sorted[i] = tp{id: t.ID, scaled: t.Logprob / temperature}
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].id < sorted[j].id })

	maxVal := sorted[0].scaled
	for _, s := range sorted[1:] {
		if s.scaled > maxVal {
			maxVal = s.scaled
		}
	}
	sumExp := float32(0)
	probs := make([]float32, len(sorted))
	for i, s := range sorted {
		probs[i] = expf32(s.scaled - maxVal)
		sumExp += probs[i]
	}
	for i := range probs {
		probs[i] /= sumExp
	}

	cdf := make([]float32, len(probs))
	cdf[0] = probs[0]
	for i := 1; i < len(probs); i++ {
		cdf[i] = cdf[i-1] + probs[i]
	}

	key := make([]byte, 32)
	copy(key, seed)
	// Nonce encoding MUST match verifier: LE uint64 at [0:8], spec §23
	nonce := make([]byte, 12)
	binary.LittleEndian.PutUint64(nonce, position)

	block := make([]byte, 8)
	cipher, err := chacha20.NewUnauthenticatedCipher(key, nonce)
	if err != nil {
		return topK[0].ID
	}
	cipher.XORKeyStream(block, block)
	randVal := binary.LittleEndian.Uint64(block)
	randFloat := float64(randVal) / 18446744073709551616.0

	for i, c := range cdf {
		if float64(c) > randFloat {
			return sorted[i].id
		}
	}
	return sorted[len(sorted)-1].id
}

// expf32 implements float32 exp using Cephes-style range reduction + polynomial.
// MUST match verifier/verifier.go expf32 exactly for cross-implementation consistency.
// Go has no float32 exp; math.Exp(float64) truncated to float32 differs from C expf.
func expf32(x float32) float32 {
	if x > 88.72 {
		return float32(math.Inf(1))
	}
	if x < -87.33 {
		return 0
	}

	const ln2 = float32(0.6931471805599453)
	const ln2inv = float32(1.4426950408889634)
	k := float32(math.Round(float64(x * ln2inv)))
	r := x - k*ln2

	r2 := r * r
	p := float32(1.0) + r + r2*float32(0.5) +
		r2*r*float32(0.16666667) +
		r2*r2*float32(0.041666668) +
		r2*r2*r*float32(0.008333334) +
		r2*r2*r2*float32(0.001388889)

	bits := math.Float32bits(p)
	bits += uint32(k) << 23
	return math.Float32frombits(bits)
}

// TokenizeRequest is the request body for TGI's /tokenize endpoint.
type TokenizeRequest struct {
	Inputs string `json:"inputs"`
}

// TokenizeToken represents a single token from the tokenizer response.
type TokenizeToken struct {
	ID   int    `json:"id"`
	Text string `json:"text"`
}

// Tokenize calls TGI's /tokenize endpoint to get actual BPE/SentencePiece token IDs.
// P1-3: replaces whitespace-based tokenizeOutput with real tokenizer output.
func (c *TGIClient) Tokenize(ctx context.Context, text string) ([]TokenizeToken, error) {
	reqBody := TokenizeRequest{Inputs: text}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal tokenize request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/tokenize", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create tokenize request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("tokenize request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tokenize error %d: %s", resp.StatusCode, string(respBody))
	}

	// Handle both TGI v2 (bare array) and v3 (wrapped object) response formats.
	rawBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read tokenize response: %w", err)
	}
	var tokens []TokenizeToken
	if err := json.Unmarshal(rawBody, &tokens); err == nil {
		return tokens, nil
	}
	// Fallback: TGI v3 may wrap as {"tokens": [...]}
	var wrapped struct {
		Tokens []TokenizeToken `json:"tokens"`
	}
	if err := json.Unmarshal(rawBody, &wrapped); err != nil {
		return nil, fmt.Errorf("decode tokenize response (tried bare array and wrapped object): %w", err)
	}
	return wrapped.Tokens, nil
}

// SelectLogitsPositions uses VRF to deterministically select 5 non-repeating
// random positions from [0, outputLen). Positions depend on hash(taskId||resultHash),
// so the worker cannot predict them before completing full output.
func SelectLogitsPositions(taskId, resultHash []byte, outputLen int) [5]int {
	var positions [5]int
	if outputLen <= 0 {
		return positions
	}
	if outputLen <= 5 {
		for i := 0; i < 5; i++ {
			positions[i] = i % outputLen
		}
		return positions
	}

	seed := sha256.Sum256(append(append([]byte{}, taskId...), resultHash...))
	used := make(map[int]bool)
	for i := 0; i < 5; i++ {
		buf := make([]byte, len(seed)+1)
		copy(buf, seed[:])
		buf[len(seed)] = byte(i)
		h := sha256.Sum256(buf)
		pos := int(binary.BigEndian.Uint32(h[:4])) % outputLen
		for used[pos] {
			h = sha256.Sum256(h[:])
			pos = int(binary.BigEndian.Uint32(h[:4])) % outputLen
		}
		used[pos] = true
		positions[i] = pos
	}
	return positions
}

// ExtractLogitsAtPositions extracts logprobs and token IDs at the specified positions.
func ExtractLogitsAtPositions(tokens []TokenInfo, positions [5]int) ([5]float32, [5]uint32) {
	var logprobs [5]float32
	var tokenIDs [5]uint32
	for i, pos := range positions {
		if pos >= 0 && pos < len(tokens) {
			logprobs[i] = tokens[pos].Logprob
			tokenIDs[i] = uint32(tokens[pos].ID)
		}
	}
	return logprobs, tokenIDs
}

// ExtractTopKAtPositions extracts the top-K logprobs at each of the 5 VRF positions.
// S1: needed for ChaCha20 softmax+CDF sampling verification.
func ExtractTopKAtPositions(tokens []TokenInfo, positions [5]int) [5][]TopTokenInfo {
	var topK [5][]TopTokenInfo
	for i, pos := range positions {
		if pos >= 0 && pos < len(tokens) {
			if len(tokens[pos].TopTokens) > 0 {
				topK[i] = tokens[pos].TopTokens
			} else {
				topK[i] = []TopTokenInfo{{
					ID:      tokens[pos].ID,
					Logprob: tokens[pos].Logprob,
				}}
			}
		}
	}
	return topK
}

// CompareLogits compares worker and verifier logits at 5 positions.
// Returns match count (4/5 = PASS per V5.2 §9.2).
func CompareLogits(workerLogits, verifierLogits [5]float32, epsilon float32) uint8 {
	matches := uint8(0)
	for i := 0; i < 5; i++ {
		diff := float32(math.Abs(float64(workerLogits[i] - verifierLogits[i])))
		if diff < epsilon {
			matches++
		}
	}
	return matches
}
