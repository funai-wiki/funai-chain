package sdk

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// ChatCompletionRequest mirrors the OpenAI chat completion request format.
type ChatCompletionRequest struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Temperature    float64         `json:"temperature,omitempty"`
	TopP           float64         `json:"top_p,omitempty"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Tools          []Tool          `json:"tools,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
	Stream         bool            `json:"stream,omitempty"`
	ContentTag     string          `json:"content_tag,omitempty"`
	MaxFee         uint64          `json:"max_fee,omitempty"`
	MaxLatencyMs   uint32          `json:"max_latency_ms,omitempty"` // max first-token latency
}

// ChatCompletionResponse mirrors the OpenAI chat completion response format.
type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"`
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   *Usage                 `json:"usage,omitempty"`
}

// ChatCompletionChoice is a single choice in the response.
type ChatCompletionChoice struct {
	Index        int     `json:"index"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Usage tracks token usage.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatCompletionChunk is a streaming response chunk (SSE format).
type ChatCompletionChunk struct {
	ID      string                      `json:"id"`
	Object  string                      `json:"object"`
	Created int64                       `json:"created"`
	Model   string                      `json:"model"`
	Choices []ChatCompletionChunkChoice `json:"choices"`
}

// ChatCompletionChunkChoice is a single choice in a streaming chunk.
type ChatCompletionChunkChoice struct {
	Index        int          `json:"index"`
	Delta        MessageDelta `json:"delta"`
	FinishReason *string      `json:"finish_reason"`
}

// MessageDelta represents incremental content in a stream chunk.
type MessageDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

// ChatCompletions provides the OpenAI-compatible chat.completions namespace.
type ChatCompletions struct {
	client *Client
}

// Chat provides the OpenAI-compatible chat namespace.
type Chat struct {
	Completions *ChatCompletions
}

// NewChat creates the chat.completions accessor for a Client.
func (c *Client) NewChat() *Chat {
	return &Chat{
		Completions: &ChatCompletions{client: c},
	}
}

const maxJSONRetries = 3

// Create performs a chat completion request, handling messages→prompt conversion,
// function calling, JSON mode, and auto-pricing.
func (cc *ChatCompletions) Create(ctx context.Context, req ChatCompletionRequest) (*ChatCompletionResponse, error) {
	if len(req.Messages) == 0 {
		return nil, NewError(ErrInvalidParameters, "messages cannot be empty")
	}
	if req.Model == "" {
		return nil, NewError(ErrInvalidParameters, "model is required")
	}

	messages := req.Messages

	// Inject function calling tools into system prompt
	if len(req.Tools) > 0 {
		messages = InjectToolsIntoSystemPrompt(messages, req.Tools)
	}

	isJSONMode := req.ResponseFormat != nil && req.ResponseFormat.Type == "json_object"
	if isJSONMode {
		messages = AppendJSONConstraint(messages)
	}

	// Convert temperature: OpenAI uses 0.0-2.0, FunAI uses 0-20000
	temp := uint16(req.Temperature * 10000)
	if temp > 20000 {
		temp = 20000
	}
	// Convert top_p: OpenAI uses 0.0-1.0, FunAI uses 0-10000
	topP := uint16(req.TopP * 10000)
	if topP > 10000 {
		topP = 10000
	}

	fee := req.MaxFee
	if fee == 0 {
		fee = cc.estimateFee(ctx, req.Model)
	}

	maxTokens := uint32(req.MaxTokens)
	if maxTokens == 0 {
		maxTokens = 2048
	}

	for attempt := 0; attempt < maxJSONRetries; attempt++ {
		prompt := MessagesToPrompt(messages, req.Model)

		result, err := cc.client.Infer(ctx, InferParams{
			ModelId:      req.Model,
			Prompt:       prompt,
			Fee:          fee,
			Temperature:  temp,
			TopP:         topP,
			MaxTokens:    maxTokens,
			MaxLatencyMs: req.MaxLatencyMs,
			StreamMode:   req.Stream,
		})
		if err != nil {
			return nil, cc.wrapError(err)
		}

		output := result.Output

		// Handle function calling: parse tool calls from output
		if len(req.Tools) > 0 {
			if tc := ParseToolCall(output); tc != nil {
				return cc.buildToolCallResponse(req.Model, result.TaskId, tc), nil
			}
		}

		// Handle JSON mode: validate and retry
		if isJSONMode {
			jsonObj, ok := ExtractJSON(output)
			if ok {
				return cc.buildResponse(req.Model, result.TaskId, string(jsonObj), "stop"), nil
			}
			if attempt < maxJSONRetries-1 {
				log.Printf("SDK: JSON mode parse failed (attempt %d/%d), retrying", attempt+1, maxJSONRetries)
				messages = AppendJSONRetryHint(messages, output)
				continue
			}
			return nil, NewError(ErrJSONParseFailed, "model did not return valid JSON after retries")
		}

		return cc.buildResponse(req.Model, result.TaskId, output, "stop"), nil
	}

	return nil, NewError(ErrJSONParseFailed, "exhausted retries")
}

func (cc *ChatCompletions) buildResponse(model string, taskId []byte, content, finishReason string) *ChatCompletionResponse {
	return &ChatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-%x", taskId[:8]),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []ChatCompletionChoice{
			{
				Index: 0,
				Message: Message{
					Role:    RoleAssistant,
					Content: &content,
				},
				FinishReason: finishReason,
			},
		},
	}
}

func (cc *ChatCompletions) buildToolCallResponse(model string, taskId []byte, tc *ToolCall) *ChatCompletionResponse {
	return &ChatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-%x", taskId[:8]),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []ChatCompletionChoice{
			{
				Index: 0,
				Message: Message{
					Role:      RoleAssistant,
					ToolCalls: []ToolCall{*tc},
				},
				FinishReason: "tool_calls",
			},
		},
	}
}

// estimateFee returns a reasonable fee estimate when none is provided.
// Queries the chain for model average fee and adds 10% buffer.
func (cc *ChatCompletions) estimateFee(ctx context.Context, model string) uint64 {
	if cc.client.chainClient == nil {
		return 100000 // default 100000 ufai = 0.1 FAI
	}

	avgFee, err := cc.client.chainClient.GetModelAvgFee(ctx, model)
	if err != nil || avgFee == 0 {
		return 100000
	}

	return avgFee + avgFee/10
}

// wrapError converts internal errors to structured FunAIError.
func (cc *ChatCompletions) wrapError(err error) *FunAIError {
	msg := err.Error()

	switch {
	case contains(msg, "insufficient", "balance"):
		return NewError(ErrInsufficientBalance, "FunAI balance insufficient, please deposit")
	case contains(msg, "model", "not found", "no model"):
		return NewError(ErrModelNotFound, "model not available on the network")
	case contains(msg, "timeout", "deadline"):
		return NewError(ErrRequestTimeout, "request timed out, retrying...")
	case contains(msg, "no available", "no eligible", "no worker"):
		return NewError(ErrNoAvailableWorker, "no available workers, please try again later")
	case contains(msg, "temperature"):
		return NewError(ErrInvalidParameters, msg)
	default:
		return NewError(ErrNetworkError, msg)
	}
}

func contains(s string, substrs ...string) bool {
	lower := toLower(s)
	for _, sub := range substrs {
		if !containsStr(lower, toLower(sub)) {
			return false
		}
	}
	return true
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := range s {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		} else {
			b[i] = c
		}
	}
	return string(b)
}

func containsStr(s, sub string) bool {
	return len(sub) == 0 || len(s) >= len(sub) && searchStr(s, sub)
}

func searchStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---- Models API ----

// ModelInfo represents a model entry returned by the models.list() API.
type ModelInfo struct {
	ID            string `json:"id"`
	Alias         string `json:"alias"`
	Name          string `json:"name"`
	ActiveWorkers uint32 `json:"active_workers"`
	AvgFee        uint64 `json:"avg_fee"`
	Status        string `json:"status"`
}

// Models provides the models namespace.
type Models struct {
	client *Client
}

// NewModels creates the models accessor for a Client.
func (c *Client) NewModels() *Models {
	return &Models{client: c}
}

// List returns all available models from the chain.
func (m *Models) List(ctx context.Context) ([]ModelInfo, error) {
	if m.client.chainClient == nil {
		return nil, NewError(ErrNetworkError, "chain client not configured")
	}

	modelsJSON, err := m.client.chainClient.QueryModels(ctx)
	if err != nil {
		return nil, NewErrorf(ErrNetworkError, "query models: %v", err)
	}

	var models []ModelInfo
	if err := json.Unmarshal(modelsJSON, &models); err != nil {
		return nil, NewErrorf(ErrNetworkError, "parse models: %v", err)
	}

	return models, nil
}
