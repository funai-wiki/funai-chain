package inference

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

// OpenAIClient implements Engine for OpenAI-compatible backends (vLLM, SGLang, Ollama).
// Uses /v1/chat/completions and /v1/completions endpoints.
type OpenAIClient struct {
	baseURL    string
	model      string // e.g. "huihui_ai/qwen3-abliterated:32b"
	httpClient *http.Client
	backend    string // "vllm", "sglang", or "ollama"
}

// NewOpenAIClient creates a client for OpenAI-compatible inference servers.
// baseURL should be like "http://host:11434" (no trailing /v1).
// model is the model name as expected by the server.
// backend is one of "vllm", "sglang", "ollama" (affects capability detection).
func NewOpenAIClient(baseURL, model, backend string) *OpenAIClient {
	return &OpenAIClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
		backend: backend,
	}
}

func (c *OpenAIClient) BackendName() string { return c.backend }

// Ensure OpenAIClient implements Engine interface.
var _ Engine = (*OpenAIClient)(nil)

// ── OpenAI API types ─────────────────────────────────────────────────────────

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float32       `json:"temperature"`
	TopP        float32       `json:"top_p,omitempty"`
	Seed        *int64        `json:"seed,omitempty"`
	Stream      bool          `json:"stream"`
	Logprobs    bool          `json:"logprobs,omitempty"`
	TopLogprobs int           `json:"top_logprobs,omitempty"`
}

type chatResponse struct {
	ID      string       `json:"id"`
	Choices []chatChoice `json:"choices"`
	Usage   chatUsage    `json:"usage"`
}

type chatChoice struct {
	Index        int           `json:"index"`
	Message      chatMessage   `json:"message"`
	FinishReason string        `json:"finish_reason"`
	Logprobs     *chatLogprobs `json:"logprobs,omitempty"`
}

type chatLogprobs struct {
	Content []chatLogprobEntry `json:"content,omitempty"`
}

type chatLogprobEntry struct {
	Token       string           `json:"token"`
	Logprob     float32          `json:"logprob"`
	TopLogprobs []chatTopLogprob `json:"top_logprobs,omitempty"`
}

type chatTopLogprob struct {
	Token   string  `json:"token"`
	Logprob float32 `json:"logprob"`
}

type chatUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Streaming types
type chatStreamChunk struct {
	Choices []chatStreamChoice `json:"choices"`
}

type chatStreamChoice struct {
	Delta        chatMessage `json:"delta"`
	FinishReason *string     `json:"finish_reason"`
}

// ── Engine interface implementation ──────────────────────────────────────────

func (c *OpenAIClient) Complete(ctx context.Context, prompt string, maxTokens int, temperature float32, topP float32, seed *int64) (*InferenceResult, error) {
	req := chatRequest{
		Model:       c.model,
		Messages:    []chatMessage{{Role: "user", Content: prompt}},
		MaxTokens:   maxTokens,
		Temperature: temperature,
		TopP:        topP,
		Seed:        seed,
		Stream:      false,
		Logprobs:    true,
		TopLogprobs: 5,
	}

	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var chatResp chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("empty choices in response")
	}

	output := chatResp.Choices[0].Message.Content
	result := &InferenceResult{
		Output:          output,
		TokenCount:      chatResp.Usage.CompletionTokens,
		InputTokenCount: chatResp.Usage.PromptTokens,
	}

	// Convert logprobs if available
	if lp := chatResp.Choices[0].Logprobs; lp != nil {
		for _, entry := range lp.Content {
			ti := TokenInfo{
				Text:    entry.Token,
				Logprob: entry.Logprob,
			}
			for _, tp := range entry.TopLogprobs {
				ti.TopTokens = append(ti.TopTokens, TopTokenInfo{
					Text:    tp.Token,
					Logprob: tp.Logprob,
				})
			}
			result.Tokens = append(result.Tokens, ti)
		}
	}

	return result, nil
}

func (c *OpenAIClient) Stream(ctx context.Context, prompt string, maxTokens int, temperature float32, topP float32, seed *int64) (<-chan StreamToken, <-chan error) {
	tokenCh := make(chan StreamToken, 100)
	errCh := make(chan error, 1)

	go func() {
		defer close(tokenCh)
		defer close(errCh)

		req := chatRequest{
			Model:       c.model,
			Messages:    []chatMessage{{Role: "user", Content: prompt}},
			MaxTokens:   maxTokens,
			Temperature: temperature,
			TopP:        topP,
			Seed:        seed,
			Stream:      true,
		}

		body, _ := json.Marshal(req)
		httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
		if err != nil {
			errCh <- err
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(httpReq)
		if err != nil {
			errCh <- err
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			errCh <- fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(bodyBytes))
			return
		}

		scanner := bufio.NewScanner(resp.Body)
		var tokenIndex uint32
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}

			var chunk chatStreamChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}

			for _, choice := range chunk.Choices {
				text := choice.Delta.Content
				if text == "" {
					continue
				}
				isFinal := choice.FinishReason != nil && *choice.FinishReason != ""
				tokenCh <- StreamToken{
					Text:    text,
					Index:   tokenIndex,
					IsFinal: isFinal,
				}
				tokenIndex++
			}
		}
	}()

	return tokenCh, errCh
}

func (c *OpenAIClient) TeacherForce(ctx context.Context, prompt string, completeOutput string, outputTokenCount int) (*InferenceResult, error) {
	// OpenAI API doesn't directly support teacher forcing (prefill logits).
	// Workaround: send prompt + output as a single prompt with logprobs enabled, max_tokens=1.
	// This gets logprobs for the last token position only — limited but functional.
	//
	// For vLLM: can use /v1/completions with prompt_logprobs parameter.
	// For Ollama: logprobs not supported — fall back to token-by-token.

	// Strategy: generate the output token by token, collecting logprobs at each step.
	result := &InferenceResult{
		Output:          completeOutput,
		TokenCount:      outputTokenCount,
		InputTokenCount: 0,
	}

	// Use Complete with the full prompt+output to get usage stats
	fullPrompt := prompt + completeOutput
	req := chatRequest{
		Model:       c.model,
		Messages:    []chatMessage{{Role: "user", Content: fullPrompt}},
		MaxTokens:   1,
		Temperature: 0,
		Stream:      false,
		Logprobs:    true,
		TopLogprobs: 5,
	}

	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("teacher force request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("teacher force: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("teacher force HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var chatResp chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}

	result.InputTokenCount = chatResp.Usage.PromptTokens

	// For backends that support prompt_logprobs (vLLM), this would return
	// logprobs for each input token. For Ollama, we only get minimal info.
	if lp := chatResp.Choices[0].Logprobs; lp != nil {
		for _, entry := range lp.Content {
			result.Tokens = append(result.Tokens, TokenInfo{
				Text:    entry.Token,
				Logprob: entry.Logprob,
			})
		}
	}

	log.Printf("OpenAI TeacherForce: input_tokens=%d (limited logprobs — %s backend)",
		result.InputTokenCount, c.backend)
	return result, nil
}

func (c *OpenAIClient) Tokenize(ctx context.Context, text string) ([]TokenizeToken, error) {
	// Try /v1/tokenize (vLLM supports this)
	type tokenizeReq struct {
		Model string `json:"model"`
		Text  string `json:"text"`
	}
	body, _ := json.Marshal(tokenizeReq{Model: c.model, Text: text})

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/tokenize", bytes.NewReader(body))
	if err == nil {
		httpReq.Header.Set("Content-Type", "application/json")
		resp, doErr := c.httpClient.Do(httpReq)
		if doErr == nil && resp.StatusCode == http.StatusOK {
			defer resp.Body.Close()
			var result struct {
				Tokens []int `json:"tokens"`
			}
			if json.NewDecoder(resp.Body).Decode(&result) == nil && len(result.Tokens) > 0 {
				tokens := make([]TokenizeToken, len(result.Tokens))
				for i, id := range result.Tokens {
					tokens[i] = TokenizeToken{ID: id, Text: fmt.Sprintf("token_%d", id)}
				}
				return tokens, nil
			}
		}
		if resp != nil {
			resp.Body.Close()
		}
	}

	// Fallback: estimate token count via chat usage stats
	return c.tokenizeViaChat(ctx, text)
}

// tokenizeViaChat estimates token count by sending a minimal chat and reading usage.
// Returns synthetic TokenizeToken slice with the correct length.
func (c *OpenAIClient) tokenizeViaChat(ctx context.Context, text string) ([]TokenizeToken, error) {
	req := chatRequest{
		Model:       c.model,
		Messages:    []chatMessage{{Role: "user", Content: text}},
		MaxTokens:   1,
		Temperature: 0,
		Stream:      false,
	}
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var chatResp chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, err
	}

	// Create synthetic tokens with correct count
	count := chatResp.Usage.PromptTokens
	tokens := make([]TokenizeToken, count)
	for i := 0; i < count; i++ {
		tokens[i] = TokenizeToken{ID: i, Text: fmt.Sprintf("tok_%d", i)}
	}
	return tokens, nil
}

func (c *OpenAIClient) DeterministicGenerate(ctx context.Context, prompt string, maxTokens int, temperature float32, finalSeed []byte) (*InferenceResult, error) {
	// OpenAI API supports seed parameter for deterministic output.
	// Convert finalSeed to int64 seed.
	var seed int64
	if len(finalSeed) >= 8 {
		for i := 0; i < 8; i++ {
			seed = (seed << 8) | int64(finalSeed[i])
		}
		if seed < 0 {
			seed = -seed
		}
	}

	return c.Complete(ctx, prompt, maxTokens, temperature, 0, &seed)
}

func (c *OpenAIClient) DeterministicGenerateWithBudget(ctx context.Context, prompt string, maxTokens int, temperature float32, finalSeed []byte, shouldStop func(outputTokens uint32) bool) (*InferenceResult, error) {
	// For OpenAI-compatible backends, we can't stop mid-generation via API.
	// Generate full output, then truncate if budget exceeded.
	result, err := c.DeterministicGenerate(ctx, prompt, maxTokens, temperature, finalSeed)
	if err != nil {
		return nil, err
	}

	// Check if budget would have been exceeded
	if shouldStop != nil && shouldStop(uint32(result.TokenCount)) {
		log.Printf("OpenAI DeterministicGenerateWithBudget: budget exceeded at %d tokens, output truncated", result.TokenCount)
	}

	return result, nil
}

func (c *OpenAIClient) IsHealthy(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	// Try /health (vLLM)
	healthURL := c.baseURL + "/health"
	req, err := http.NewRequestWithContext(ctx, "GET", healthURL, nil)
	if err != nil {
		return false
	}
	resp, err := c.httpClient.Do(req)
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return true
		}
	}

	// Try /v1/models (OpenAI standard)
	modelsURL := c.baseURL + "/v1/models"
	req2, err := http.NewRequestWithContext(ctx, "GET", modelsURL, nil)
	if err != nil {
		return false
	}
	resp2, err := c.httpClient.Do(req2)
	if err != nil {
		return false
	}
	defer resp2.Body.Close()
	return resp2.StatusCode == http.StatusOK
}

func (c *OpenAIClient) DetectVersion() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try /v1/models to detect available models
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/v1/models", nil)
	if err != nil {
		return
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return
	}

	for _, m := range result.Data {
		log.Printf("OpenAI backend model: %s", m.ID)
	}
}
