package canonical

import (
	"bytes"
	"encoding/json"

	sdkcanonical "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator/canonical"
)

func init() {
	sdkcanonical.RegisterParser("openai", ParseOpenAIRequest)
	sdkcanonical.RegisterEmitter("openai", EmitOpenAIRequest)

	sdkcanonical.RegisterResponseParser("openai", ParseOpenAIResponse)
	sdkcanonical.RegisterResponseEmitter("openai", EmitOpenAIResponse)

	sdkcanonical.RegisterStreamEventParser("openai", ParseOpenAIStreamEvent)
	sdkcanonical.RegisterStreamEventEmitters("openai", EmitOpenAIStreamEvent)
}

// ParseOpenAIRequest converts an OpenAI request JSON into UnifiedChatRequest.
func ParseOpenAIRequest(rawJSON []byte) (*sdkcanonical.UnifiedChatRequest, error) {
	var req sdkcanonical.UnifiedChatRequest
	if err := json.Unmarshal(rawJSON, &req); err != nil {
		return nil, err
	}
	return &req, nil
}

// EmitOpenAIRequest converts a UnifiedChatRequest into an OpenAI request JSON.
func EmitOpenAIRequest(req *sdkcanonical.UnifiedChatRequest) ([]byte, error) {
	return json.Marshal(req)
}

type openaiChoice struct {
	Index        int                  `json:"index"`
	Message      sdkcanonical.Message `json:"message"`
	FinishReason string               `json:"finish_reason,omitempty"`
}

type openaiResponse struct {
	ID      string              `json:"id,omitempty"`
	Object  string              `json:"object,omitempty"`
	Created int64               `json:"created,omitempty"`
	Model   string              `json:"model,omitempty"`
	Choices []openaiChoice      `json:"choices"`
	Usage   *sdkcanonical.Usage `json:"usage,omitempty"`
}

// ParseOpenAIResponse converts an OpenAI response JSON into UnifiedChatResponse.
func ParseOpenAIResponse(rawJSON []byte) (*sdkcanonical.UnifiedChatResponse, error) {
	var resp openaiResponse
	if err := json.Unmarshal(rawJSON, &resp); err != nil {
		return nil, err
	}
	var msg sdkcanonical.Message
	var fr string
	if len(resp.Choices) > 0 {
		msg = resp.Choices[0].Message
		fr = resp.Choices[0].FinishReason
	}
	return &sdkcanonical.UnifiedChatResponse{
		ID:           resp.ID,
		Object:       resp.Object,
		Created:      resp.Created,
		Model:        resp.Model,
		Message:      msg,
		FinishReason: fr,
		Usage:        resp.Usage,
	}, nil
}

// EmitOpenAIResponse converts a UnifiedChatResponse into an OpenAI response JSON.
func EmitOpenAIResponse(resp *sdkcanonical.UnifiedChatResponse) ([]byte, error) {
	oResp := openaiResponse{
		ID:      resp.ID,
		Object:  resp.Object,
		Created: resp.Created,
		Model:   resp.Model,
		Usage:   resp.Usage,
		Choices: []openaiChoice{
			{
				Index:        0,
				Message:      resp.Message,
				FinishReason: resp.FinishReason,
			},
		},
	}
	if oResp.Object == "" {
		oResp.Object = "chat.completion"
	}
	return json.Marshal(oResp)
}

type openaiStreamChoice struct {
	Index        int                `json:"index"`
	Delta        sdkcanonical.Delta `json:"delta"`
	FinishReason string             `json:"finish_reason,omitempty"`
}

type openaiStreamChunk struct {
	ID      string               `json:"id,omitempty"`
	Object  string               `json:"object,omitempty"`
	Created int64                `json:"created,omitempty"`
	Model   string               `json:"model,omitempty"`
	Choices []openaiStreamChoice `json:"choices"`
	Usage   *sdkcanonical.Usage  `json:"usage,omitempty"`
}

// ParseOpenAIStreamEvent parses a single streaming event chunk (which might start with "data:") into a UnifiedEvent.
func ParseOpenAIStreamEvent(rawLine []byte) (*sdkcanonical.UnifiedEvent, error) {
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
	var chunk openaiStreamChunk
	if err := json.Unmarshal(trimmed, &chunk); err != nil {
		return nil, err
	}
	var delta sdkcanonical.Delta
	var fr string
	if len(chunk.Choices) > 0 {
		delta = chunk.Choices[0].Delta
		fr = chunk.Choices[0].FinishReason
	}
	return &sdkcanonical.UnifiedEvent{
		ID:           chunk.ID,
		Object:       chunk.Object,
		Created:      chunk.Created,
		Model:        chunk.Model,
		Delta:        delta,
		FinishReason: fr,
		Usage:        chunk.Usage,
	}, nil
}

// EmitOpenAIStreamEvent formats a UnifiedEvent into standard OpenAI streaming event bytes (without "data:" prefix).
func EmitOpenAIStreamEvent(event *sdkcanonical.UnifiedEvent) ([]byte, error) {
	if event.FinishReason == "done_marker" {
		return nil, nil
	}
	chunk := openaiStreamChunk{
		ID:      event.ID,
		Object:  event.Object,
		Created: event.Created,
		Model:   event.Model,
		Usage:   event.Usage,
		Choices: []openaiStreamChoice{
			{
				Index:        0,
				Delta:        event.Delta,
				FinishReason: event.FinishReason,
			},
		},
	}
	if chunk.Object == "" {
		chunk.Object = "chat.completion.chunk"
	}
	return json.Marshal(chunk)
}
