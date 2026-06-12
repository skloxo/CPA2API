package canonical

import (
	"bytes"
	"context"
	"encoding/json"

	claudereq "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/claude/openai/chat-completions"
	sdkcanonical "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator/canonical"
)

func init() {
	sdkcanonical.RegisterParser("claude", ParseClaudeRequest)
	sdkcanonical.RegisterEmitter("claude", EmitClaudeRequest)

	sdkcanonical.RegisterResponseParser("claude", ParseClaudeResponse)
	sdkcanonical.RegisterStreamEventParser("claude", ParseClaudeStreamEvent)
}

// ParseClaudeRequest converts a Claude request JSON into UnifiedChatRequest (optional placeholder).
func ParseClaudeRequest(rawJSON []byte) (*sdkcanonical.UnifiedChatRequest, error) {
	return nil, nil
}

// EmitClaudeRequest converts a UnifiedChatRequest into a Claude request JSON.
func EmitClaudeRequest(req *sdkcanonical.UnifiedChatRequest) ([]byte, error) {
	openaiJSON, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return claudereq.ConvertOpenAIRequestToClaude(req.Model, openaiJSON, req.Stream), nil
}

// ParseClaudeResponse converts Claude response bytes (which might consist of multiple SSE lines)
// into a UnifiedChatResponse by reusing the legacy OpenAI response converter.
func ParseClaudeResponse(rawJSON []byte) (*sdkcanonical.UnifiedChatResponse, error) {
	openaiRespJSON := claudereq.ConvertClaudeResponseToOpenAINonStream(context.Background(), "", nil, nil, rawJSON, nil)
	return ParseOpenAIResponse(openaiRespJSON)
}

type claudeSSEEvent struct {
	Type    string `json:"type"`
	Message *struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Index        int `json:"index"`
	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// ParseClaudeStreamEvent parses a single Claude SSE event line into a UnifiedEvent.
func ParseClaudeStreamEvent(rawLine []byte) (*sdkcanonical.UnifiedEvent, error) {
	trimmed := bytes.TrimSpace(rawLine)
	if bytes.HasPrefix(trimmed, []byte("data:")) {
		trimmed = bytes.TrimSpace(trimmed[5:])
	}
	if len(trimmed) == 0 {
		return nil, nil
	}
	if bytes.Equal(trimmed, []byte("[DONE]")) {
		return &sdkcanonical.UnifiedEvent{
			FinishReason: "done_marker",
		}, nil
	}

	var ev claudeSSEEvent
	if err := json.Unmarshal(trimmed, &ev); err != nil {
		return nil, err
	}

	var unified sdkcanonical.UnifiedEvent
	var delta sdkcanonical.Delta

	switch ev.Type {
	case "message_start":
		if ev.Message != nil {
			unified.ID = ev.Message.ID
			unified.Model = ev.Message.Model
			if ev.Message.Usage != nil {
				unified.Usage = &sdkcanonical.Usage{
					PromptTokens:     ev.Message.Usage.InputTokens,
					CompletionTokens: ev.Message.Usage.OutputTokens,
					TotalTokens:      ev.Message.Usage.InputTokens + ev.Message.Usage.OutputTokens,
				}
			}
		}
		delta.Role = "assistant"
		unified.Delta = delta

	case "content_block_start":
		if ev.ContentBlock != nil && ev.ContentBlock.Type == "tool_use" {
			delta.ToolCalls = []sdkcanonical.ToolCall{
				{
					Index: &ev.Index,
					ID:    ev.ContentBlock.ID,
					Type:  "function",
					Function: sdkcanonical.Func{
						Name: ev.ContentBlock.Name,
					},
				},
			}
			unified.Delta = delta
		}

	case "content_block_delta":
		if ev.Delta != nil {
			switch ev.Delta.Type {
			case "text_delta":
				delta.Content = ev.Delta.Text
			case "thinking_delta":
				delta.Thinking = ev.Delta.Thinking
			case "input_json_delta":
				delta.ToolCalls = []sdkcanonical.ToolCall{
					{
						Index: &ev.Index,
						Function: sdkcanonical.Func{
							Arguments: ev.Delta.PartialJSON,
						},
					},
				}
			}
			unified.Delta = delta
		}

	case "message_delta":
		if ev.Delta != nil && ev.Delta.StopReason != "" {
			unified.FinishReason = mapAnthropicStopReasonToOpenAI(ev.Delta.StopReason)
		}
		if ev.Usage != nil {
			unified.Usage = &sdkcanonical.Usage{
				PromptTokens:     ev.Usage.InputTokens,
				CompletionTokens: ev.Usage.OutputTokens,
				TotalTokens:      ev.Usage.InputTokens + ev.Usage.OutputTokens,
			}
		}

	case "message_stop":
		return nil, nil
	}

	return &unified, nil
}

func mapAnthropicStopReasonToOpenAI(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "tool_use":
		return "tool_calls"
	case "max_tokens":
		return "length"
	case "stop_sequence":
		return "stop"
	default:
		return "stop"
	}
}
