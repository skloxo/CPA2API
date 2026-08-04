package canonical

import (
	"encoding/json"
	"testing"

	sdkcanonical "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator/canonical"
)

func TestGeminiRequestTranslation(t *testing.T) {
	temp := 0.5
	req := &sdkcanonical.UnifiedChatRequest{
		Model: "gemini-1.5-flash",
		Messages: []sdkcanonical.Message{
			{
				Role:    "system",
				Content: sdkcanonical.MessageContent{{Type: "text", Text: "System prompt instructions"}},
			},
			{
				Role:    "user",
				Content: sdkcanonical.MessageContent{{Type: "text", Text: "Hello Gemini!"}},
			},
		},
		Temperature: &temp,
		Stream:      false,
	}

	rawGemini, err := EmitGeminiRequest(req)
	if err != nil {
		t.Fatalf("EmitGeminiRequest failed: %v", err)
	}

	// Unmarshal and verify Gemini API format
	var parsed map[string]interface{}
	if err := json.Unmarshal(rawGemini, &parsed); err != nil {
		t.Fatalf("failed to unmarshal emitted Gemini JSON: %v", err)
	}

	contents, ok := parsed["contents"].([]interface{})
	if !ok || len(contents) != 1 {
		t.Fatalf("Expected contents array of size 1, got %v", parsed["contents"])
	}

	userMsg := contents[0].(map[string]interface{})
	if userMsg["role"] != "user" {
		t.Errorf("Expected role user, got %s", userMsg["role"])
	}

	systemInstruction := parsed["systemInstruction"].(map[string]interface{})
	parts := systemInstruction["parts"].([]interface{})
	if len(parts) == 0 {
		t.Errorf("Expected systemInstruction parts to be populated")
	}
}

func TestGeminiResponseTranslation(t *testing.T) {
	rawResp := []byte(`{
		"candidates": [{
			"content": {
				"parts": [
					{"text": "This is a response from Gemini."}
				],
				"role": "model"
			},
			"finishReason": "STOP"
		}],
		"usageMetadata": {
			"promptTokenCount": 10,
			"candidatesTokenCount": 15,
			"totalTokenCount": 25
		}
	}`)

	unified, err := ParseGeminiResponse(rawResp)
	if err != nil {
		t.Fatalf("ParseGeminiResponse failed: %v", err)
	}

	if len(unified.Message.Content) != 1 || unified.Message.Content[0].Text != "This is a response from Gemini." {
		t.Errorf("Mismatch in parsed content: %v", unified.Message.Content)
	}
	if unified.FinishReason != "STOP" {
		t.Errorf("Expected finishReason STOP, got %s", unified.FinishReason)
	}
	if unified.Usage == nil || unified.Usage.TotalTokens != 25 {
		t.Errorf("Expected total tokens 25, got %v", unified.Usage)
	}
}

func TestGeminiStreamEventTranslation(t *testing.T) {
	rawChunk := []byte(`data: {"candidates":[{"content":{"parts":[{"text":" part 1"}],"role":"model"}}],"usageMetadata":{"promptTokenCount":10,"candidatesTokenCount":5,"totalTokenCount":15}}`)

	unified, err := ParseGeminiStreamEvent(rawChunk)
	if err != nil {
		t.Fatalf("ParseGeminiStreamEvent failed: %v", err)
	}

	if unified.Delta.Content != " part 1" {
		t.Errorf("Expected delta content ' part 1', got %s", unified.Delta.Content)
	}
	if unified.Usage == nil || unified.Usage.TotalTokens != 15 {
		t.Errorf("Expected usage total 15")
	}

	// Test DONE event
	doneChunk := []byte("data: [DONE]")
	unifiedDone, err := ParseGeminiStreamEvent(doneChunk)
	if err != nil {
		t.Fatalf("ParseGeminiStreamEvent [DONE] failed: %v", err)
	}
	if unifiedDone.FinishReason != "done_marker" {
		t.Errorf("Expected done_marker finishReason")
	}
}
