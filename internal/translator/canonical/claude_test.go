package canonical

import (
	"encoding/json"
	"testing"

	sdkcanonical "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator/canonical"
)

func TestClaudeRequestTranslation(t *testing.T) {
	req := &sdkcanonical.UnifiedChatRequest{
		Model: "claude-3-5-sonnet",
		Messages: []sdkcanonical.Message{
			{
				Role:    "user",
				Content: sdkcanonical.MessageContent{{Type: "text", Text: "Hello Claude!"}},
			},
		},
		Stream: false,
	}

	rawClaude, err := EmitClaudeRequest(req)
	if err != nil {
		t.Fatalf("EmitClaudeRequest failed: %v", err)
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(rawClaude, &parsed); err != nil {
		t.Fatalf("failed to unmarshal emitted Claude JSON: %v", err)
	}

	if parsed["model"] != "claude-3-5-sonnet" {
		t.Errorf("Expected model claude-3-5-sonnet, got %v", parsed["model"])
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

func TestClaudeResponseTranslation(t *testing.T) {
	rawResp := []byte("data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_123\",\"model\":\"claude-opus-4-6\"}}\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello World\"}}\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":13,\"output_tokens\":4,\"cache_read_input_tokens\":0,\"cache_creation_input_tokens\":0}}\n" +
		"data: {\"type\":\"message_stop\"}\n")

	unified, err := ParseClaudeResponse(rawResp)
	if err != nil {
		t.Fatalf("ParseClaudeResponse failed: %v", err)
	}

	if unified.ID != "msg_123" {
		t.Errorf("Expected ID msg_123, got %s", unified.ID)
	}
	if len(unified.Message.Content) != 1 || unified.Message.Content[0].Text != "Hello World" {
		t.Errorf("Expected message content 'Hello World', got %v", unified.Message.Content)
	}
	if unified.FinishReason != "stop" {
		t.Errorf("Expected finish reason stop, got %s", unified.FinishReason)
	}
	if unified.Usage == nil || unified.Usage.TotalTokens != 17 {
		t.Errorf("Expected usage total 17, got %v", unified.Usage)
	}
}

func TestClaudeStreamEventTranslation(t *testing.T) {
	rawChunk := []byte(`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" part 1"}}`)

	unified, err := ParseClaudeStreamEvent(rawChunk)
	if err != nil {
		t.Fatalf("ParseClaudeStreamEvent failed: %v", err)
	}

	if unified.Delta.Content != " part 1" {
		t.Errorf("Expected delta content ' part 1', got %s", unified.Delta.Content)
	}

	// Test DONE
	doneChunk := []byte("data: [DONE]")
	unifiedDone, err := ParseClaudeStreamEvent(doneChunk)
	if err != nil {
		t.Fatalf("ParseClaudeStreamEvent [DONE] failed: %v", err)
	}
	if unifiedDone.FinishReason != "done_marker" {
		t.Errorf("Expected done_marker finishReason")
	}
}
