package canonical

import (
	"encoding/json"
	"testing"

	sdkcanonical "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator/canonical"
)

func TestQwenRequestTranslation(t *testing.T) {
	req := &sdkcanonical.UnifiedChatRequest{
		Model: "qwen3.7-max",
		Messages: []sdkcanonical.Message{
			{
				Role:    "user",
				Content: sdkcanonical.MessageContent{{Type: "text", Text: "Hello Qwen!"}},
			},
		},
		Stream: false,
	}

	rawQwen, err := EmitQwenRequest(req)
	if err != nil {
		t.Fatalf("EmitQwenRequest failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(rawQwen, &parsed); err != nil {
		t.Fatalf("failed to unmarshal emitted Qwen JSON: %v", err)
	}

	if parsed["model"] != "qwen3.7-max" {
		t.Errorf("Expected model qwen3.7-max, got %v", parsed["model"])
	}

	messages, ok := parsed["messages"].([]interface{})
	if !ok || len(messages) != 1 {
		t.Fatalf("Expected messages array of size 1, got %v", parsed["messages"])
	}

	userMsg := messages[0].(map[string]interface{})
	if userMsg["role"] != "user" {
		t.Errorf("Expected role user, got %s", userMsg["role"])
	}
}

func TestQwenResponseTranslation(t *testing.T) {
	// Qwen non-streaming response JSON in standard OpenAI-compatible format
	rawResp := []byte(`{
		"choices": [{
			"message": {
				"role": "assistant",
				"content": "This is Qwen response content."
			},
			"finish_reason": "stop"
		}],
		"model": "qwen3.7-max",
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 15,
			"total_tokens": 25
		}
	}`)

	unified, err := ParseQwenResponse(rawResp)
	if err != nil {
		t.Fatalf("ParseQwenResponse failed: %v", err)
	}

	if len(unified.Message.Content) != 1 || unified.Message.Content[0].Text != "This is Qwen response content." {
		t.Errorf("Expected response content, got %v", unified.Message.Content)
	}
	if unified.FinishReason != "stop" {
		t.Errorf("Expected finish reason stop, got %s", unified.FinishReason)
	}
	if unified.Usage == nil || unified.Usage.TotalTokens != 25 {
		t.Errorf("Expected total tokens 25, got %v", unified.Usage)
	}
}
