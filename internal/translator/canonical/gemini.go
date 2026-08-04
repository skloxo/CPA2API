package canonical

import (
	"bytes"
	"encoding/json"

	geminireq "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/gemini/openai/chat-completions"
	sdkcanonical "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator/canonical"
)

func init() {
	sdkcanonical.RegisterParser("gemini", ParseGeminiRequest)
	sdkcanonical.RegisterEmitter("gemini", EmitGeminiRequest)

	sdkcanonical.RegisterResponseParser("gemini", ParseGeminiResponse)
	sdkcanonical.RegisterStreamEventParser("gemini", ParseGeminiStreamEvent)
}

// ParseGeminiRequest converts a Gemini request JSON into UnifiedChatRequest (optional placeholder).
func ParseGeminiRequest(rawJSON []byte) (*sdkcanonical.UnifiedChatRequest, error) {
	return nil, nil
}

// EmitGeminiRequest converts a UnifiedChatRequest into a Gemini request JSON.
func EmitGeminiRequest(req *sdkcanonical.UnifiedChatRequest) ([]byte, error) {
	openaiJSON, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return geminireq.ConvertOpenAIRequestToGemini(req.Model, openaiJSON, req.Stream), nil
}

type geminiPart struct {
	Text         string          `json:"text,omitempty"`
	Thought      bool            `json:"thought,omitempty"`
	FunctionCall *geminiFuncCall `json:"functionCall,omitempty"`
}

type geminiFuncCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type geminiCandidate struct {
	Content struct {
		Parts []geminiPart `json:"parts"`
		Role  string       `json:"role"`
	} `json:"content"`
	FinishReason string `json:"finishReason"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata,omitempty"`
}

// ParseGeminiResponse converts a Gemini response JSON into UnifiedChatResponse.
func ParseGeminiResponse(rawJSON []byte) (*sdkcanonical.UnifiedChatResponse, error) {
	var gResp geminiResponse
	if err := json.Unmarshal(rawJSON, &gResp); err != nil {
		return nil, err
	}
	var msg sdkcanonical.Message
	msg.Role = "assistant"
	var fr string
	var toolCalls []sdkcanonical.ToolCall

	if len(gResp.Candidates) > 0 {
		cand := gResp.Candidates[0]
		fr = cand.FinishReason

		for _, part := range cand.Content.Parts {
			if part.FunctionCall != nil {
				toolCalls = append(toolCalls, sdkcanonical.ToolCall{
					Type: "function",
					Function: sdkcanonical.Func{
						Name:      part.FunctionCall.Name,
						Arguments: string(part.FunctionCall.Args),
					},
				})
			} else if part.Text != "" {
				if part.Thought {
					msg.Thinking += part.Text
				} else {
					msg.Content = append(msg.Content, sdkcanonical.ContentPart{
						Type: "text",
						Text: part.Text,
					})
				}
			}
		}
	}

	msg.ToolCalls = toolCalls

	var usage *sdkcanonical.Usage
	if gResp.UsageMetadata != nil {
		usage = &sdkcanonical.Usage{
			PromptTokens:     gResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: gResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      gResp.UsageMetadata.TotalTokenCount,
		}
	}

	return &sdkcanonical.UnifiedChatResponse{
		Message:      msg,
		FinishReason: fr,
		Usage:        usage,
	}, nil
}

// ParseGeminiStreamEvent parses a single streaming event chunk (SSE data or NDJSON line) into a UnifiedEvent.
func ParseGeminiStreamEvent(rawLine []byte) (*sdkcanonical.UnifiedEvent, error) {
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

	var gResp geminiResponse
	if err := json.Unmarshal(trimmed, &gResp); err != nil {
		return nil, err
	}

	var delta sdkcanonical.Delta
	var fr string
	var toolCalls []sdkcanonical.ToolCall

	if len(gResp.Candidates) > 0 {
		cand := gResp.Candidates[0]
		fr = cand.FinishReason

		for _, part := range cand.Content.Parts {
			if part.FunctionCall != nil {
				toolCalls = append(toolCalls, sdkcanonical.ToolCall{
					Type: "function",
					Function: sdkcanonical.Func{
						Name:      part.FunctionCall.Name,
						Arguments: string(part.FunctionCall.Args),
					},
				})
			} else if part.Text != "" {
				if part.Thought {
					delta.Thinking += part.Text
				} else {
					delta.Content += part.Text
				}
			}
		}
	}

	delta.ToolCalls = toolCalls

	var usage *sdkcanonical.Usage
	if gResp.UsageMetadata != nil {
		usage = &sdkcanonical.Usage{
			PromptTokens:     gResp.UsageMetadata.PromptTokenCount,
			CompletionTokens: gResp.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      gResp.UsageMetadata.TotalTokenCount,
		}
	}

	return &sdkcanonical.UnifiedEvent{
		Delta:        delta,
		FinishReason: fr,
		Usage:        usage,
	}, nil
}
