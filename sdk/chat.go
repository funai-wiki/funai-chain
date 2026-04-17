package sdk

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ---- Messages Format (OpenAI-compatible) ----

// Role constants for chat messages.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message represents a single chat message in OpenAI format.
type Message struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ---- Chat Templates ----

// ChatTemplate defines the format strings for each role in a model-specific template.
type ChatTemplate struct {
	System           string
	User             string
	Assistant        string
	Tool             string
	GenerationPrefix string
}

var chatMLTemplate = ChatTemplate{
	System:           "<|im_start|>system\n%s<|im_end|>\n",
	User:             "<|im_start|>user\n%s<|im_end|>\n",
	Assistant:        "<|im_start|>assistant\n%s<|im_end|>\n",
	Tool:             "<|im_start|>tool\n%s<|im_end|>\n",
	GenerationPrefix: "<|im_start|>assistant\n",
}

var llama3Template = ChatTemplate{
	System:           "<|start_header_id|>system<|end_header_id|>\n\n%s<|eot_id|>",
	User:             "<|start_header_id|>user<|end_header_id|>\n\n%s<|eot_id|>",
	Assistant:        "<|start_header_id|>assistant<|end_header_id|>\n\n%s<|eot_id|>",
	Tool:             "<|start_header_id|>tool<|end_header_id|>\n\n%s<|eot_id|>",
	GenerationPrefix: "<|start_header_id|>assistant<|end_header_id|>\n\n",
}

// templateRegistry maps model alias prefixes to templates.
// Models not found here default to ChatML.
var templateRegistry = map[string]*ChatTemplate{
	"chatml": &chatMLTemplate,
	"llama3": &llama3Template,
}

// modelTemplateMap maps model alias patterns to template names.
var modelTemplateMap = map[string]string{
	"llama":    "llama3",
	"codellam": "llama3",
}

// GetTemplate returns the ChatTemplate for a model alias. Defaults to ChatML.
func GetTemplate(modelAlias string) *ChatTemplate {
	lower := strings.ToLower(modelAlias)
	for prefix, tmplName := range modelTemplateMap {
		if strings.Contains(lower, prefix) {
			if t, ok := templateRegistry[tmplName]; ok {
				return t
			}
		}
	}
	return &chatMLTemplate
}

// MessagesToPrompt converts an OpenAI-style messages array into a single prompt string
// using the appropriate chat template for the model.
func MessagesToPrompt(messages []Message, modelAlias string) string {
	tmpl := GetTemplate(modelAlias)
	var sb strings.Builder

	for _, msg := range messages {
		content := ""
		if msg.Content != nil {
			content = *msg.Content
		}
		switch msg.Role {
		case RoleSystem:
			sb.WriteString(fmt.Sprintf(tmpl.System, content))
		case RoleUser:
			sb.WriteString(fmt.Sprintf(tmpl.User, content))
		case RoleAssistant:
			sb.WriteString(fmt.Sprintf(tmpl.Assistant, content))
		case RoleTool:
			sb.WriteString(fmt.Sprintf(tmpl.Tool, content))
		}
	}

	sb.WriteString(tmpl.GenerationPrefix)
	return sb.String()
}

// ---- Function Calling ----

// ToolFunction describes a callable function.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// Tool represents an OpenAI-compatible tool definition.
type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolCall represents a model's request to call a tool (OpenAI format).
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction is the function name + serialized arguments inside a ToolCall.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// InjectToolsIntoSystemPrompt builds a tool-use instruction block and appends it
// to the system message content. If no system message exists, one is prepended.
func InjectToolsIntoSystemPrompt(messages []Message, tools []Tool) []Message {
	if len(tools) == 0 {
		return messages
	}

	var toolDesc strings.Builder
	toolDesc.WriteString("\n\nYou have the following tools available. " +
		"When you need to call a tool, reply with ONLY the following JSON format, no other text:\n\n" +
		`{"tool_call": {"name": "TOOL_NAME", "arguments": {ARGS}}}` + "\n\n" +
		"Available tools:\n")

	for i, tool := range tools {
		toolDesc.WriteString(fmt.Sprintf("%d. %s", i+1, tool.Function.Name))
		if tool.Function.Description != "" {
			toolDesc.WriteString(fmt.Sprintf(" - %s", tool.Function.Description))
		}
		toolDesc.WriteString("\n")
		if len(tool.Function.Parameters) > 0 {
			var params map[string]interface{}
			if json.Unmarshal(tool.Function.Parameters, &params) == nil {
				if props, ok := params["properties"].(map[string]interface{}); ok {
					required := map[string]bool{}
					if reqArr, ok := params["required"].([]interface{}); ok {
						for _, r := range reqArr {
							if s, ok := r.(string); ok {
								required[s] = true
							}
						}
					}
					for name, propRaw := range props {
						prop, _ := propRaw.(map[string]interface{})
						typ, _ := prop["type"].(string)
						desc, _ := prop["description"].(string)
						reqStr := ""
						if required[name] {
							reqStr = ", required"
						}
						toolDesc.WriteString(fmt.Sprintf("   - %s (%s%s): %s\n", name, typ, reqStr, desc))
					}
				}
			}
		}
	}
	toolDesc.WriteString("\nIf you don't need to call a tool, respond normally.\n")

	suffix := toolDesc.String()
	result := make([]Message, len(messages))
	copy(result, messages)

	for i, msg := range result {
		if msg.Role == RoleSystem && msg.Content != nil {
			combined := *msg.Content + suffix
			result[i].Content = &combined
			return result
		}
	}

	sysContent := "You are a helpful assistant." + suffix
	sysMsg := Message{Role: RoleSystem, Content: &sysContent}
	return append([]Message{sysMsg}, result...)
}

// toolCallRegex matches JSON containing a tool_call key.
var toolCallRegex = regexp.MustCompile(`\{[^{}]*"tool_call"\s*:\s*\{[^}]*"name"\s*:`)

// ParseToolCall attempts to extract a tool_call from model output.
// Returns nil if no tool call is detected.
func ParseToolCall(output string) *ToolCall {
	text := strings.TrimSpace(output)

	// Method 1: direct JSON parse
	var wrapper struct {
		ToolCallData struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"tool_call"`
	}
	if json.Unmarshal([]byte(text), &wrapper) == nil && wrapper.ToolCallData.Name != "" {
		return &ToolCall{
			ID:   generateCallID(),
			Type: "function",
			Function: ToolCallFunction{
				Name:      wrapper.ToolCallData.Name,
				Arguments: string(wrapper.ToolCallData.Arguments),
			},
		}
	}

	// Method 2: extract from code block
	codeBlockRe := regexp.MustCompile("```(?:json)?\\s*(" + `\{[\s\S]*?\}` + ")\\s*```")
	if match := codeBlockRe.FindStringSubmatch(text); len(match) > 1 {
		if json.Unmarshal([]byte(match[1]), &wrapper) == nil && wrapper.ToolCallData.Name != "" {
			return &ToolCall{
				ID:   generateCallID(),
				Type: "function",
				Function: ToolCallFunction{
					Name:      wrapper.ToolCallData.Name,
					Arguments: string(wrapper.ToolCallData.Arguments),
				},
			}
		}
	}

	// Method 3: find first { to last } if it looks like a tool call
	if toolCallRegex.MatchString(text) {
		start := strings.Index(text, "{")
		end := strings.LastIndex(text, "}")
		if start >= 0 && end > start {
			candidate := text[start : end+1]
			if json.Unmarshal([]byte(candidate), &wrapper) == nil && wrapper.ToolCallData.Name != "" {
				return &ToolCall{
					ID:   generateCallID(),
					Type: "function",
					Function: ToolCallFunction{
						Name:      wrapper.ToolCallData.Name,
						Arguments: string(wrapper.ToolCallData.Arguments),
					},
				}
			}
		}
	}

	return nil
}

var callIDCounter uint64

func generateCallID() string {
	callIDCounter++
	return fmt.Sprintf("call_%d", callIDCounter)
}

// ---- JSON Mode ----

const jsonModeConstraint = "\n\n[IMPORTANT] You must only return a single valid JSON object. " +
	"Do not include any other text, explanation, code block markers, or prefix. " +
	"Start directly with { and end with }."

const jsonRetryHint = "\n\n[RETRY] Your previous response was not valid JSON. " +
	"Please respond with ONLY a valid JSON object, starting with { and ending with }."

// ResponseFormat specifies the desired output format.
type ResponseFormat struct {
	Type string `json:"type"`
}

// AppendJSONConstraint adds JSON-mode constraints to the system message.
func AppendJSONConstraint(messages []Message) []Message {
	result := make([]Message, len(messages))
	copy(result, messages)

	for i, msg := range result {
		if msg.Role == RoleSystem && msg.Content != nil {
			combined := *msg.Content + jsonModeConstraint
			result[i].Content = &combined
			return result
		}
	}

	sysContent := "You are a helpful assistant." + jsonModeConstraint
	sysMsg := Message{Role: RoleSystem, Content: &sysContent}
	return append([]Message{sysMsg}, result...)
}

// AppendJSONRetryHint adds a retry hint after a failed JSON parse.
func AppendJSONRetryHint(messages []Message, failedOutput string) []Message {
	result := make([]Message, len(messages))
	copy(result, messages)

	assistantContent := failedOutput
	result = append(result, Message{Role: RoleAssistant, Content: &assistantContent})

	retryContent := jsonRetryHint
	result = append(result, Message{Role: RoleUser, Content: &retryContent})
	return result
}

// ExtractJSON attempts to extract a valid JSON object from model output.
// Tolerates common formatting issues (code blocks, prefix text, etc.)
func ExtractJSON(text string) (json.RawMessage, bool) {
	trimmed := strings.TrimSpace(text)

	// Method 1: direct parse
	if json.Valid([]byte(trimmed)) && strings.HasPrefix(trimmed, "{") {
		return json.RawMessage(trimmed), true
	}

	// Method 2: strip code block markers
	codeBlockRe := regexp.MustCompile("```(?:json)?\\s*(" + `\{[\s\S]*?\}` + ")\\s*```")
	if match := codeBlockRe.FindStringSubmatch(trimmed); len(match) > 1 {
		candidate := strings.TrimSpace(match[1])
		if json.Valid([]byte(candidate)) {
			return json.RawMessage(candidate), true
		}
	}

	// Method 3: find first { to last }
	start := strings.Index(trimmed, "{")
	end := strings.LastIndex(trimmed, "}")
	if start >= 0 && end > start {
		candidate := trimmed[start : end+1]
		if json.Valid([]byte(candidate)) {
			return json.RawMessage(candidate), true
		}
	}

	return nil, false
}
