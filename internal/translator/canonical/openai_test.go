package canonical

import (
	"bytes"
	"encoding/json"
	"testing"

	sdkcanonical "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator/canonical"
)

func TestOpenAIRequestTranslation(t *testing.T) {
	rawReq := []byte(`{
		"model": "gpt-4o-mini",
		"messages": [
			{"role": "system", "content": [{"type": "text", "text": "You are a helpful assistant."}]},
			{"role": "user", "content": [{"type": "text", "text": "Hello!"}]}
		],
		"stream": true,
		"temperature": 0.7
	}`)

	// Parse to UnifiedChatRequest
	unified, err := ParseOpenAIRequest(rawReq)
	if err != nil {
		t.Fatalf("ParseOpenAIRequest failed: %v", err)
	}

	if unified.Model != "gpt-4o-mini" {
		t.Errorf("Expected model gpt-4o-mini, got %s", unified.Model)
	}
	if len(unified.Messages) != 2 {
		t.Errorf("Expected 2 messages, got %d", len(unified.Messages))
	}
	if unified.Messages[1].Role != "user" {
		t.Errorf("Expected user role, got %s", unified.Messages[1].Role)
	}
	if !unified.Stream {
		t.Errorf("Expected stream to be true")
	}
	if unified.Temperature == nil || *unified.Temperature != 0.7 {
		t.Errorf("Expected temperature 0.7")
	}

	// Emit back to JSON
	emitted, err := EmitOpenAIRequest(unified)
	if err != nil {
		t.Fatalf("EmitOpenAIRequest failed: %v", err)
	}

	// Re-parse emitted JSON to verify semantic equivalence
	var checkUnified sdkcanonical.UnifiedChatRequest
	if err := json.Unmarshal(emitted, &checkUnified); err != nil {
		t.Fatalf("failed to unmarshal emitted request: %v", err)
	}
	if checkUnified.Model != unified.Model {
		t.Errorf("Mismatch in model after emit")
	}
}

func TestOpenAIResponseTranslation(t *testing.T) {
	rawResp := []byte(`{
		"id": "chatcmpl-123",
		"object": "chat.completion",
		"created": 1677652288,
		"model": "gpt-4o-mini",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": "Hello! How can I help you today?"
			},
			"finish_reason": "stop"
		}],
		"usage": {
			"prompt_tokens": 9,
			"completion_tokens": 12,
			"total_tokens": 21
		}
	}`)

	// Parse to UnifiedChatResponse
	unified, err := ParseOpenAIResponse(rawResp)
	if err != nil {
		t.Fatalf("ParseOpenAIResponse failed: %v", err)
	}

	if unified.ID != "chatcmpl-123" {
		t.Errorf("Expected ID chatcmpl-123, got %s", unified.ID)
	}
	if unified.Message.Content[0].Text != "Hello! How can I help you today?" {
		t.Errorf("Expected text content, got %s", unified.Message.Content[0].Text)
	}
	if unified.FinishReason != "stop" {
		t.Errorf("Expected finish_reason stop, got %s", unified.FinishReason)
	}
	if unified.Usage == nil || unified.Usage.TotalTokens != 21 {
		t.Errorf("Expected usage total 21")
	}

	// Emit back to JSON
	emitted, err := EmitOpenAIResponse(unified)
	if err != nil {
		t.Fatalf("EmitOpenAIResponse failed: %v", err)
	}

	var checkResp openaiResponse
	if err := json.Unmarshal(emitted, &checkResp); err != nil {
		t.Fatalf("failed to unmarshal emitted response: %v", err)
	}
	if len(checkResp.Choices) == 0 || checkResp.Choices[0].Message.Role != "assistant" {
		t.Errorf("Invalid choice message role in emitted response")
	}
}

func TestOpenAIStreamEventTranslation(t *testing.T) {
	rawEvent := []byte(`data: {"id":"chatcmpl-123","object":"chat.completion.chunk","created":1677652288,"model":"gpt-4o-mini","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}`)

	// Parse to UnifiedEvent
	unified, err := ParseOpenAIStreamEvent(rawEvent)
	if err != nil {
		t.Fatalf("ParseOpenAIStreamEvent failed: %v", err)
	}

	if unified.ID != "chatcmpl-123" {
		t.Errorf("Expected ID chatcmpl-123, got %s", unified.ID)
	}
	if unified.Delta.Content != "Hello" {
		t.Errorf("Expected delta content 'Hello', got %s", unified.Delta.Content)
	}

	// Emit back to JSON bytes
	emitted, err := EmitOpenAIStreamEvent(unified)
	if err != nil {
		t.Fatalf("EmitOpenAIStreamEvent failed: %v", err)
	}

	if bytes.HasPrefix(emitted, []byte("data: ")) {
		t.Errorf("Expected emitted SSE line NOT to start with 'data: '")
	}

	// Test DONE marker
	doneEvent := []byte("data: [DONE]")
	unifiedDone, err := ParseOpenAIStreamEvent(doneEvent)
	if err != nil {
		t.Fatalf("ParseOpenAIStreamEvent [DONE] failed: %v", err)
	}
	if unifiedDone.FinishReason != "done_marker" {
		t.Errorf("Expected finish reason done_marker, got %s", unifiedDone.FinishReason)
	}

	emittedDone, err := EmitOpenAIStreamEvent(unifiedDone)
	if err != nil {
		t.Fatalf("EmitOpenAIStreamEvent [DONE] failed: %v", err)
	}
	if emittedDone != nil {
		t.Errorf("Expected emitted DONE event to be nil")
	}
}
