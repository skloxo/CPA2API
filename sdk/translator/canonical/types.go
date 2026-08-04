package canonical

import (
	"encoding/json"
	"fmt"
)

// Message represents a single message in the conversation.
type Message struct {
	Role       string         `json:"role"`                   // "system", "user", "assistant", "tool"
	Content    MessageContent `json:"content,omitempty"`      // Supports text and multi-modal (images/files)
	Name       string         `json:"name,omitempty"`         // Optional name for tool calls/response routing
	ToolCallID string         `json:"tool_call_id,omitempty"` // For tool message matching
	ToolCalls  []ToolCall     `json:"tool_calls,omitempty"`   // Assistant's tool use requests
	Thinking   string         `json:"thinking,omitempty"`     // Thinking budget or intermediate reasoning content
}

// MessageContent is a custom type that can marshal/unmarshal from either a string or a list of ContentParts.
type MessageContent []ContentPart

// UnmarshalJSON parses content as either a plain string or a list of content parts.
func (mc *MessageContent) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*mc = []ContentPart{{Type: "text", Text: s}}
		return nil
	}
	var parts []ContentPart
	if err := json.Unmarshal(data, &parts); err == nil {
		*mc = parts
		return nil
	}
	return fmt.Errorf("invalid message content format")
}

// MarshalJSON serializes content. If it's a single text part, it serializes as a plain string.
func (mc MessageContent) MarshalJSON() ([]byte, error) {
	if len(mc) == 1 && mc[0].Type == "text" {
		return json.Marshal(mc[0].Text)
	}
	type alias []ContentPart
	return json.Marshal(alias(mc))
}

// ContentPart represents a chunk of message content (text or media).
type ContentPart struct {
	Type      string `json:"type"`                 // "text", "image_url", "image_base64", "document"
	Text      string `json:"text,omitempty"`       // For type == "text"
	ImageURL  string `json:"image_url,omitempty"`  // For type == "image_url"
	ImageB64  string `json:"image_b64,omitempty"`  // For type == "image_base64" (raw base64 string)
	MediaType string `json:"media_type,omitempty"` // Optional mime-type (e.g. image/jpeg, application/pdf)
}

// ToolCall represents a requested tool invocation.
type ToolCall struct {
	Index    *int   `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"` // always "function"
	Function Func   `json:"function"`
}

// Func represents the function call details.
type Func struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON string representation of arguments
}

// ToolDefinition represents a schema exposed to the model.
type ToolDefinition struct {
	Type     string `json:"type"` // "function"
	Function struct {
		Name        string                 `json:"name"`
		Description string                 `json:"description,omitempty"`
		Parameters  map[string]interface{} `json:"parameters,omitempty"`
	} `json:"function"`
}

// UnifiedChatRequest is the canonical intermediate representation of a chat request.
type UnifiedChatRequest struct {
	Model           string           `json:"model"`
	Messages        []Message        `json:"messages"`
	Tools           []ToolDefinition `json:"tools,omitempty"`
	Stream          bool             `json:"stream,omitempty"`
	Temperature     *float64         `json:"temperature,omitempty"`
	MaxTokens       *int             `json:"max_tokens,omitempty"`
	ThinkingEnabled bool             `json:"thinking_enabled,omitempty"`
	ThinkingBudget  int              `json:"thinking_budget,omitempty"` // in tokens
	Stop            []string         `json:"stop,omitempty"`
	Metadata        map[string]any   `json:"metadata,omitempty"` // custom options / session affinity
}

// UnifiedChatResponse is the canonical intermediate representation of a non-streaming chat response.
type UnifiedChatResponse struct {
	ID           string  `json:"id,omitempty"`
	Object       string  `json:"object,omitempty"` // "chat.completion"
	Created      int64   `json:"created,omitempty"`
	Model        string  `json:"model,omitempty"`
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason,omitempty"` // "stop", "tool_calls"
	Usage        *Usage  `json:"usage,omitempty"`
}

// UnifiedEvent is the canonical intermediate representation of a streaming output chunk.
type UnifiedEvent struct {
	ID           string `json:"id,omitempty"`
	Object       string `json:"object,omitempty"` // "chat.completion.chunk"
	Created      int64  `json:"created,omitempty"`
	Model        string `json:"model,omitempty"`
	Delta        Delta  `json:"delta"`
	FinishReason string `json:"finish_reason,omitempty"` // "stop", "tool_calls"
	Usage        *Usage `json:"usage,omitempty"`
}

// Delta holds the incremental content returned in a stream event.
type Delta struct {
	Role      string     `json:"role,omitempty"`
	Content   string     `json:"content,omitempty"`
	Thinking  string     `json:"thinking,omitempty"` // incremental reasoning
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

// Usage holds token consumption statistics.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}
