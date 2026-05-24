package qwen

import (
	"context"
	"strings"
	"testing"
)

func TestConvertQwenResponseToOpenAI_ContentChunk(t *testing.T) {
	// Simulates what the executor passes: raw SSE line with "data: " prefix
	input := []byte(`data: {"choices": [{"delta": {"role": "assistant", "content": "Hello"}}], "response_id":"r1", "usage":{"input_tokens":50,"output_tokens":1,"total_tokens":51}}`)

	var param any
	chunks := ConvertQwenResponseToOpenAI(context.Background(), "qwen3.5-flash", nil, nil, input, &param)
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk, got none")
	}

	out := string(chunks[0])
	if !strings.Contains(out, `"content":"Hello"`) {
		t.Errorf("expected content 'Hello' in output, got: %s", out)
	}
	if !strings.HasPrefix(out, "data: ") {
		t.Errorf("expected 'data: ' prefix, got: %s", out)
	}
	if !strings.Contains(out, `"object":"chat.completion.chunk"`) {
		t.Errorf("expected OpenAI chunk object, got: %s", out)
	}
	if !strings.Contains(out, `"model":"qwen3.5-flash"`) {
		t.Errorf("expected model qwen3.5-flash, got: %s", out)
	}
	t.Logf("output: %s", out)
}

func TestConvertQwenResponseToOpenAI_FinalChunkWithUsage(t *testing.T) {
	input := []byte(`data: {"choices": [{"delta": {"role": "assistant", "content": ""}}], "response_id":"r1", "usage":{"input_tokens":50,"output_tokens":9,"total_tokens":59}}`)

	var param any
	// First chunk to establish the completion ID
	ConvertQwenResponseToOpenAI(context.Background(), "qwen3.5-flash", nil, nil,
		[]byte(`data: {"choices": [{"delta": {"role": "assistant", "content": "Hi"}}]}`), &param)

	chunks := ConvertQwenResponseToOpenAI(context.Background(), "qwen3.5-flash", nil, nil, input, &param)
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk, got none")
	}

	out := string(chunks[0])
	if !strings.Contains(out, `"finish_reason":"stop"`) {
		t.Errorf("expected finish_reason stop, got: %s", out)
	}
	if !strings.Contains(out, `"prompt_tokens":50`) {
		t.Errorf("expected usage in output, got: %s", out)
	}
	if !strings.Contains(out, `"completion_tokens":9`) {
		t.Errorf("expected completion_tokens 9, got: %s", out)
	}
	t.Logf("output: %s", out)
}

func TestConvertQwenResponseToOpenAI_DoneMarker(t *testing.T) {
	input := []byte(`data: [DONE]`)

	chunks := ConvertQwenResponseToOpenAI(context.Background(), "qwen3.5-flash", nil, nil, input, nil)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	out := strings.TrimSpace(string(chunks[0]))
	if out != "data: [DONE]" {
		t.Errorf("expected 'data: [DONE]', got: %s", out)
	}
}

func TestConvertQwenResponseToOpenAI_ResponseCreated(t *testing.T) {
	input := []byte(`data: {"response.created":{"chat_id":"xxx","parent_id":"yyy","response_id":"zzz","response_index":"0"}}`)

	chunks := ConvertQwenResponseToOpenAI(context.Background(), "qwen3.5-flash", nil, nil, input, nil)
	if len(chunks) != 0 {
		t.Errorf("expected response.created to be dropped, got %d chunks", len(chunks))
	}
}

func TestConvertQwenResponseToOpenAI_EmptyInput(t *testing.T) {
	chunks := ConvertQwenResponseToOpenAI(context.Background(), "qwen3.5-flash", nil, nil, nil, nil)
	if chunks != nil {
		t.Errorf("expected nil for empty input, got %v", chunks)
	}
}

func TestConvertQwenResponseToOpenAI_InvalidJSON(t *testing.T) {
	input := []byte(`data: not json at all`)

	chunks := ConvertQwenResponseToOpenAI(context.Background(), "qwen3.5-flash", nil, nil, input, nil)
	if len(chunks) != 0 {
		t.Errorf("expected invalid JSON to be dropped, got %d chunks", len(chunks))
	}
}

func TestConvertQwenResponseToOpenAI_CompletionIDStability(t *testing.T) {
	var param any

	input1 := []byte(`data: {"choices": [{"delta": {"role": "assistant", "content": "Hello"}}]}`)
	input2 := []byte(`data: {"choices": [{"delta": {"role": "assistant", "content": " World"}}]}`)

	chunks1 := ConvertQwenResponseToOpenAI(context.Background(), "qwen3.5-flash", nil, nil, input1, &param)
	chunks2 := ConvertQwenResponseToOpenAI(context.Background(), "qwen3.5-flash", nil, nil, input2, &param)

	if len(chunks1) == 0 || len(chunks2) == 0 {
		t.Fatal("expected chunks from both calls")
	}

	// Extract IDs — both should use the same completion ID
	id1 := extractID(string(chunks1[0]))
	id2 := extractID(string(chunks2[0]))
	if id1 != id2 {
		t.Errorf("expected same completion ID across chunks, got %s and %s", id1, id2)
	}
}

func TestConvertQwenResponseToOpenAI_NoDataPrefix(t *testing.T) {
	// Also handles raw JSON without "data: " prefix (defensive)
	input := []byte(`{"choices": [{"delta": {"role": "assistant", "content": "test"}}]}`)

	var param any
	chunks := ConvertQwenResponseToOpenAI(context.Background(), "qwen3.5-flash", nil, nil, input, &param)
	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk, got none")
	}

	out := string(chunks[0])
	if !strings.Contains(out, `"content":"test"`) {
		t.Errorf("expected content 'test', got: %s", out)
	}
}

// extractID pulls the "id" field from a JSON string (rough extraction for test purposes).
func extractID(s string) string {
	// Find "id":"..."
	idx := strings.Index(s, `"id":"`)
	if idx < 0 {
		return ""
	}
	start := idx + len(`"id":"`)
	end := strings.Index(s[start:], `"`)
	if end < 0 {
		return ""
	}
	return s[start : start+end]
}
