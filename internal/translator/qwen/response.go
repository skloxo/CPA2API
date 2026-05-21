// Package qwen implements response translation between Qwen and OpenAI API formats.
//
// Qwen streaming uses an event-based SSE format:
//
//	event: message
//	data: {"content":"Hello", "extra":{"reasoning_content":"..."}, "status":"partial"}
//
//	event: complete
//	data: {"content":"", "status":"done"}
//
// This is converted to OpenAI SSE format:
//
//	data: {"id":"...","choices":[{"delta":{"content":"Hello"},"finish_reason":null}]}
//	data: {"choices":[{"finish_reason":"stop"}]}
//	data: [DONE]
package qwen

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// completionIDKey is used to store a shared completion ID in the param context.
const completionIDKey = "qwen_completion_id"

// getCompletionID returns a stable completion ID for all chunks in a single response.
func getCompletionID(param *any) string {
	if param != nil {
		if m, ok := (*param).(map[string]string); ok {
			if id, ok := m[completionIDKey]; ok {
				return id
			}
		}
	}
	id := fmt.Sprintf("chatcmpl-qwen-%d", time.Now().UnixNano())
	if param != nil {
		if *param == nil {
			*param = map[string]string{completionIDKey: id}
		} else if m, ok := (*param).(map[string]string); ok {
			m[completionIDKey] = id
		}
	}
	return id
}

// ConvertQwenResponseToOpenAI converts Qwen response data to OpenAI format (streaming).
// The translator receives parsed Qwen JSON data and converts it to OpenAI chunk format.
func ConvertQwenResponseToOpenAI(ctx context.Context, model string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) [][]byte {
	if len(rawJSON) == 0 {
		return nil
	}

	line := strings.TrimSpace(string(rawJSON))

	// Pass through [DONE] markers
	if line == "[DONE]" {
		return [][]byte{[]byte("data: [DONE]\n\n")}
	}

	// Try to parse as Qwen message JSON
	if !gjson.Valid(line) {
		return nil
	}

	result := gjson.Parse(line)
	content := result.Get("content").String()
	reasoningContent := result.Get("extra.reasoning_content").String()
	status := result.Get("status").String()

	// Skip if no content and not a done status
	if content == "" && reasoningContent == "" && status != "done" {
		return nil
	}

	// Reuse the same completion ID across all chunks in a single response
	chunkID := getCompletionID(param)
	created := time.Now().Unix()

	// Use the model from the request, or fall back to the provided model
	responseModel := model
	if requestModel := gjson.GetBytes(requestRawJSON, "model"); requestModel.Exists() {
		responseModel = requestModel.String()
	}

	// Track think/answer phase transitions for <think></think> tag injection.
	// Qwen sends reasoning_content during the thinking phase and content during the answer phase.
	// We inject <think> at the start of thinking and </think> when transitioning to answer.
	var stateMap map[string]string
	if param != nil {
		stateMap, _ = (*param).(map[string]string)
	}
	thinkingStarted := stateMap != nil && stateMap["thinking_started"] == "true"
	thinkingEnded := stateMap != nil && stateMap["thinking_ended"] == "true"

	// Phase 1: First reasoning content → inject <think> opening tag
	if reasoningContent != "" && !thinkingStarted {
		thinkingStarted = true
		if stateMap != nil {
			stateMap["thinking_started"] = "true"
		}
		reasoningContent = "<think>\n\n" + reasoningContent
	}

	// Phase 2: First answer content (or done) after thinking → inject </think> closing tag
	if thinkingStarted && !thinkingEnded && (content != "" || status == "done") {
		thinkingEnded = true
		if stateMap != nil {
			stateMap["thinking_ended"] = "true"
		}
		content = "\n</think>\n" + content
	}

	// Build the OpenAI-format chunk
	chunk := buildOpenAIStreamChunk(chunkID, created, responseModel, content, reasoningContent, status)
	return [][]byte{chunk}
}

// ConvertQwenResponseToOpenAINonStream converts a Qwen non-streaming response to OpenAI format.
// Since Qwen's non-streaming response is already largely OpenAI-compatible, this is mostly a passthrough.
func ConvertQwenResponseToOpenAINonStream(ctx context.Context, model string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) []byte {
	if len(rawJSON) == 0 || !gjson.ValidBytes(rawJSON) {
		return rawJSON
	}

	// The response is likely already in OpenAI-compatible format.
	// Ensure the model field matches what the client expects.
	result := gjson.ParseBytes(rawJSON)
	responseModel := result.Get("model").String()
	if responseModel == "" {
		// Set the model from the request
		if requestModel := gjson.GetBytes(requestRawJSON, "model"); requestModel.Exists() {
			responseModel = requestModel.String()
		} else {
			responseModel = model
		}
	}

	return rawJSON
}

// ConvertOpenAIResponseToQwen converts OpenAI response data to Qwen format (streaming).
// This is a passthrough since the executor emits OpenAI-format data.
func ConvertOpenAIResponseToQwen(ctx context.Context, model string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) [][]byte {
	if len(rawJSON) == 0 {
		return nil
	}
	return [][]byte{rawJSON}
}

// ConvertOpenAIResponseToQwenNonStream converts an OpenAI non-streaming response to Qwen format.
// This is a passthrough since Qwen uses OpenAI-compatible format.
func ConvertOpenAIResponseToQwenNonStream(ctx context.Context, model string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) []byte {
	return rawJSON
}

// buildOpenAIStreamChunk creates an OpenAI-compatible streaming chunk.
// Maps Qwen fields to OpenAI format:
//   - Qwen content → OpenAI delta.content
//   - Qwen extra.reasoning_content → OpenAI delta.reasoning_content
// Both fields can be present simultaneously.
func buildOpenAIStreamChunk(chunkID string, created int64, model, content, reasoningContent, status string) []byte {
	var delta string
	switch {
	case content != "" && reasoningContent != "":
		delta = fmt.Sprintf(`{"content":"%s","reasoning_content":"%s"}`, escapeJSON(content), escapeJSON(reasoningContent))
	case content != "":
		delta = fmt.Sprintf(`{"content":"%s"}`, escapeJSON(content))
	case reasoningContent != "":
		delta = fmt.Sprintf(`{"reasoning_content":"%s"}`, escapeJSON(reasoningContent))
	default:
		delta = "{}"
	}

	finishReason := "null"
	if status == "done" || status == "finished" {
		finishReason = `"stop"`
	}

	return []byte(fmt.Sprintf(
		`data: {"id":"%s","object":"chat.completion.chunk","created":%d,"model":"%s","choices":[{"index":0,"delta":%s,"finish_reason":%s}]}`+"\n\n",
		chunkID, created, model, delta, finishReason,
	))
}
