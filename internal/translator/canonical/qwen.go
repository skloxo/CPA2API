package canonical

import (
	"context"
	"encoding/json"

	qwenreq "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/qwen"
	sdkcanonical "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator/canonical"
)

func init() {
	sdkcanonical.RegisterParser("qwen", ParseQwenRequest)
	sdkcanonical.RegisterEmitter("qwen", EmitQwenRequest)

	sdkcanonical.RegisterResponseParser("qwen", ParseQwenResponse)
	sdkcanonical.RegisterStreamEventParser("qwen", ParseQwenStreamEvent)
}

// ParseQwenRequest converts a Qwen request JSON into UnifiedChatRequest (optional placeholder).
func ParseQwenRequest(rawJSON []byte) (*sdkcanonical.UnifiedChatRequest, error) {
	return nil, nil
}

// EmitQwenRequest converts a UnifiedChatRequest into a Qwen request JSON.
func EmitQwenRequest(req *sdkcanonical.UnifiedChatRequest) ([]byte, error) {
	openaiJSON, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	return qwenreq.ConvertOpenAIRequestToQwen(req.Model, openaiJSON, req.Stream), nil
}

// ParseQwenResponse converts Qwen response bytes into a UnifiedChatResponse by reusing the legacy OpenAI response converter.
func ParseQwenResponse(rawJSON []byte) (*sdkcanonical.UnifiedChatResponse, error) {
	openaiRespJSON := qwenreq.ConvertQwenResponseToOpenAINonStream(context.Background(), "", nil, nil, rawJSON, nil)
	return ParseOpenAIResponse(openaiRespJSON)
}

// ParseQwenStreamEvent parses a single Qwen SSE event line into a UnifiedEvent.
func ParseQwenStreamEvent(rawLine []byte) (*sdkcanonical.UnifiedEvent, error) {
	// Streaming responses for Qwen are pre-converted to OpenAI format in the executor,
	// so this is a placeholder.
	return nil, nil
}
