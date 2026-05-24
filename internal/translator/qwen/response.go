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
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
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

	// Strip the SSE "data: " prefix if present.
	// The executor passes the raw SSE line including the prefix.
	if strings.HasPrefix(line, "data:") {
		line = strings.TrimSpace(line[len("data:"):])
	}

	// Pass through [DONE] markers
	if line == "[DONE]" {
		return [][]byte{[]byte("data: [DONE]\n\n")}
	}

	// Try to parse as JSON
	if !gjson.Valid(line) {
		return nil
	}

	result := gjson.Parse(line)

	// Drop response.created events — no content to forward
	if result.Get(`response\.created`).Exists() {
		return nil
	}

	// Track think/answer phase transitions for <think></think> tag injection and content status tracking.
	var stateMap map[string]string
	if param != nil {
		if *param == nil {
			*param = make(map[string]string)
		}
		if m, ok := (*param).(map[string]string); ok {
			stateMap = m
		}
	}

	// Extract content from either native root format or OpenAI choices delta path
	content := result.Get("content").String()
	if content == "" {
		content = result.Get("choices.0.delta.content").String()
	}
	if content != "" && stateMap != nil {
		stateMap["has_content"] = "true"
	}

	reasoningContent := result.Get("extra.reasoning_content").String()
	if reasoningContent == "" {
		reasoningContent = result.Get("choices.0.delta.reasoning_content").String()
	}

	status := result.Get("status").String()
	usageData := result.Get("usage")
	if status == "" {
		if usageData.Exists() && content == "" {
			// Only fallback to "done" if we have already received some content or tool calls
			// (i.e., this is a trailing usage-only chunk of a finished response), and NOT
			// the very first chunk of the stream where no content has been generated yet.
			hasContentOrTool := false
			if stateMap != nil {
				if stateMap["has_content"] == "true" || stateMap["is_tool_call_mode"] == "true" {
					hasContentOrTool = true
				}
			}
			if hasContentOrTool {
				status = "done"
			}
		}
	}

	// Handle phase field for deep thinking (reasoning_content)
	phase := result.Get("choices.0.delta.phase").String()
	if phase == "thinking" && content != "" && reasoningContent == "" {
		reasoningContent = content
		content = ""
	}

	isLastChunk := status == "done"

	// Empty frames are allowed to pass through as keep-alive heartbeats.

	// Reuse the same completion ID across all chunks in a single response
	chunkID := getCompletionID(param)
	created := time.Now().Unix()

	// Use the model from the request, or fall back to the provided model
	responseModel := model
	if requestModel := gjson.GetBytes(requestRawJSON, "model"); requestModel.Exists() {
		responseModel = requestModel.String()
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
	if thinkingStarted && !thinkingEnded && (content != "" || isLastChunk) {
		thinkingEnded = true
		if stateMap != nil {
			stateMap["thinking_ended"] = "true"
		}
		content = "\n</think>\n" + content
	}

	// Build usage string for the final chunk
	var usageStr string
	if usageData.Exists() && isLastChunk {
		inputTokens := usageData.Get("input_tokens").Int()
		outputTokens := usageData.Get("output_tokens").Int()
		totalTokens := usageData.Get("total_tokens").Int()
		// Fallback: some Qwen responses use prompt_tokens/completion_tokens already
		if inputTokens == 0 {
			inputTokens = usageData.Get("prompt_tokens").Int()
		}
		if outputTokens == 0 {
			outputTokens = usageData.Get("completion_tokens").Int()
		}
		if totalTokens == 0 {
			totalTokens = inputTokens + outputTokens
		}
		if totalTokens > 0 {
			usageStr = fmt.Sprintf(`"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}`, inputTokens, outputTokens, totalTokens)
		}
	}

	// ────────────────────────────────────────────────────────────────
	// Streaming Tool Call Interception & Translation (P0 & Task 2)
	// ────────────────────────────────────────────────────────────────
	var allowedNames []string
	var reqJSONToParse []byte
	if len(originalRequestRawJSON) > 0 {
		reqJSONToParse = originalRequestRawJSON
	} else {
		reqJSONToParse = requestRawJSON
	}
	if reqJSONToParse != nil {
		tools := gjson.GetBytes(reqJSONToParse, "tools")
		if tools.Exists() && tools.IsArray() {
			for _, tool := range tools.Array() {
				if name := tool.Get("function.name").String(); name != "" {
					allowedNames = append(allowedNames, name)
				}
			}
		}
		functions := gjson.GetBytes(reqJSONToParse, "functions")
		if functions.Exists() && functions.IsArray() {
			for _, fn := range functions.Array() {
				if name := fn.Get("name").String(); name != "" {
					allowedNames = append(allowedNames, name)
				}
			}
		}
	}

	accumulated := ""
	if stateMap != nil {
		accumulated = stateMap["accumulated_content"] + content
		stateMap["accumulated_content"] = accumulated
	} else {
		accumulated = content
	}

	// Detect if we are starting a tool call or already in one
	toolCallIdx := -1
	isToolCallMode := false
	if len(allowedNames) > 0 {
		toolCallIdx = strings.Index(accumulated, "<tool_call")
		if toolCallIdx < 0 {
			toolCallIdx = strings.Index(accumulated, "<tool_calls>")
		}
		if stateMap != nil && stateMap["is_tool_call_mode"] == "true" {
			isToolCallMode = true
		} else if toolCallIdx >= 0 {
			isToolCallMode = true
			if stateMap != nil {
				stateMap["is_tool_call_mode"] = "true"
			}
		}
	}

	if isToolCallMode {
		var toolCallsDelta []map[string]interface{}
		var outContent string

		// 1. Stream any preceding content before the tool call tag
		if toolCallIdx >= 0 {
			sentLen := 0
			if stateMap != nil {
				if lenStr, ok := stateMap["sent_content_len"]; ok {
					fmt.Sscanf(lenStr, "%d", &sentLen)
				}
			}
			precedingContent := accumulated[:toolCallIdx]
			if len(precedingContent) > sentLen {
				outContent = precedingContent[sentLen:]
				sentLen = len(precedingContent)
				if stateMap != nil {
					stateMap["sent_content_len"] = fmt.Sprintf("%d", sentLen)
				}
			}
		}

		// 2. Parse the tool calls incrementally
		tcs := ParseIncrementalToolCalls(accumulated, allowedNames)
		for _, tc := range tcs {
			toolName := tc.Name
			if toolName == "" {
				continue
			}

			toolCallID := ""
			idKey := fmt.Sprintf("tool_call_id_%d", tc.Index)
			emittedKey := fmt.Sprintf("tool_call_emitted_%d", tc.Index)
			emittedArgsLenKey := fmt.Sprintf("emitted_args_len_%d", tc.Index)

			if stateMap != nil {
				toolCallID = stateMap[idKey]
				if toolCallID == "" {
					toolCallID = fmt.Sprintf("call_%s_%d", chunkID, tc.Index)
					stateMap[idKey] = toolCallID
					stateMap[fmt.Sprintf("tool_call_name_%d", tc.Index)] = toolName
				}
			} else {
				toolCallID = fmt.Sprintf("call_%s_%d", chunkID, tc.Index)
			}

			argsStr := tc.Arguments

			emittedLen := 0
			if stateMap != nil {
				if emLenStr, ok := stateMap[emittedArgsLenKey]; ok {
					fmt.Sscanf(emLenStr, "%d", &emittedLen)
				}
			}

			// Emit starting chunk if not done yet
			if stateMap != nil && stateMap[emittedKey] != "true" {
				stateMap[emittedKey] = "true"
				toolCallsDelta = append(toolCallsDelta, map[string]interface{}{
					"index": tc.Index,
					"id":    toolCallID,
					"type":  "function",
					"function": map[string]interface{}{
						"name":      toolName,
						"arguments": "",
					},
				})
			}

			// Emit new arguments characters
			if len(argsStr) > emittedLen {
				deltaArgs := argsStr[emittedLen:]
				emittedLen = len(argsStr)
				if stateMap != nil {
					stateMap[emittedArgsLenKey] = fmt.Sprintf("%d", emittedLen)
				}

				toolCallsDelta = append(toolCallsDelta, map[string]interface{}{
					"index": tc.Index,
					"function": map[string]interface{}{
						"arguments": deltaArgs,
					},
				})
			}

			if tc.IsComplete || isLastChunk {
				suffix := GetJSONRepairSuffix(argsStr)
				if suffix != "" {
					repairedArgs := argsStr + suffix
					repairedArgs = FixToolArgs(repairedArgs)
					if len(repairedArgs) > emittedLen {
						deltaArgs := repairedArgs[emittedLen:]
						toolCallsDelta = append(toolCallsDelta, map[string]interface{}{
							"index": tc.Index,
							"function": map[string]interface{}{
								"arguments": deltaArgs,
							},
						})
						emittedLen = len(repairedArgs)
						if stateMap != nil {
							stateMap[emittedArgsLenKey] = fmt.Sprintf("%d", emittedLen)
						}
					}
				}
			}
		}

		// Build and return the chunks
		var chunks [][]byte
		if outContent != "" {
			chunks = append(chunks, buildOpenAIStreamChunkWithUsage(chunkID, created, responseModel, outContent, reasoningContent, "", ""))
		}
		if len(toolCallsDelta) > 0 {
			finishReason := ""
			if isLastChunk {
				finishReason = "tool_calls"
			}
			chunks = append(chunks, buildOpenAIToolCallStreamChunk(chunkID, created, responseModel, toolCallsDelta, finishReason, usageStr))
		} else if isLastChunk {
			chunks = append(chunks, buildOpenAIToolCallStreamChunk(chunkID, created, responseModel, nil, "tool_calls", usageStr))
		}
		return chunks
	}

	// Build the standard OpenAI-format chunk
	chunk := buildOpenAIStreamChunkWithUsage(chunkID, created, responseModel, content, reasoningContent, status, usageStr)
	return [][]byte{chunk}
}

// ConvertQwenResponseToOpenAINonStream converts a Qwen non-streaming response to OpenAI format.
func ConvertQwenResponseToOpenAINonStream(ctx context.Context, model string, originalRequestRawJSON, requestRawJSON, rawJSON []byte, param *any) []byte {
	if len(rawJSON) == 0 || !gjson.ValidBytes(rawJSON) {
		return rawJSON
	}

	result := gjson.ParseBytes(rawJSON)
	responseModel := result.Get("model").String()
	if responseModel == "" {
		if requestModel := gjson.GetBytes(requestRawJSON, "model"); requestModel.Exists() {
			responseModel = requestModel.String()
		} else {
			responseModel = model
		}
	}

	choices := result.Get("choices")
	if !choices.Exists() || !choices.IsArray() || len(choices.Array()) == 0 {
		return rawJSON
	}

	message := choices.Array()[0].Get("message")
	if !message.Exists() {
		return rawJSON
	}

	content := message.Get("content").String()
	if content == "" {
		return rawJSON
	}

	var allowedNames []string
	var reqJSONToParse []byte
	if len(originalRequestRawJSON) > 0 {
		reqJSONToParse = originalRequestRawJSON
	} else {
		reqJSONToParse = requestRawJSON
	}
	if reqJSONToParse != nil {
		tools := gjson.GetBytes(reqJSONToParse, "tools")
		if tools.Exists() && tools.IsArray() {
			for _, tool := range tools.Array() {
				if name := tool.Get("function.name").String(); name != "" {
					allowedNames = append(allowedNames, name)
				}
			}
		}
		functions := gjson.GetBytes(reqJSONToParse, "functions")
		if functions.Exists() && functions.IsArray() {
			for _, fn := range functions.Array() {
				if name := fn.Get("name").String(); name != "" {
					allowedNames = append(allowedNames, name)
				}
			}
		}
	}

	if len(allowedNames) == 0 {
		return rawJSON
	}

	calls := ParseToolCallFromText(content, allowedNames)
	if len(calls) == 0 {
		return rawJSON
	}

	var openAIToolCalls []map[string]interface{}
	for i, call := range calls {
		repairedArgs := "{}"
		if argsBytes, err := json.Marshal(call.Input); err == nil {
			repairedArgs = string(argsBytes)
		}
		repairedArgs = FixToolArgs(repairedArgs)

		openAIToolCalls = append(openAIToolCalls, map[string]interface{}{
			"id":   fmt.Sprintf("call_qwen_%d_%d", time.Now().UnixNano(), i),
			"type": "function",
			"function": map[string]interface{}{
				"name":      call.Name,
				"arguments": repairedArgs,
			},
		})
	}

	cleanContent := stripToolCallText(content)

	updated := rawJSON
	var err error
	if cleanContent == "" {
		updated, err = sjson.SetBytes(updated, "choices.0.message.content", nil)
	} else {
		updated, err = sjson.SetBytes(updated, "choices.0.message.content", cleanContent)
	}
	if err != nil {
		return rawJSON
	}

	updated, err = sjson.SetBytes(updated, "choices.0.message.tool_calls", openAIToolCalls)
	if err != nil {
		return rawJSON
	}

	updated, err = sjson.SetBytes(updated, "choices.0.finish_reason", "tool_calls")
	if err != nil {
		return rawJSON
	}

	if responseModel != "" {
		updated, _ = sjson.SetBytes(updated, "model", responseModel)
	}

	return updated
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

// buildOpenAIStreamChunkWithUsage creates an OpenAI-compatible streaming chunk.
func buildOpenAIStreamChunkWithUsage(chunkID string, created int64, model, content, reasoningContent, status, usageStr string) []byte {
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

	// Build the base chunk
	chunk := fmt.Sprintf(
		`{"id":"%s","object":"chat.completion.chunk","created":%d,"model":"%s","choices":[{"index":0,"delta":%s,"finish_reason":%s}]}`,
		chunkID, created, model, delta, finishReason,
	)

	// Append usage data if present (final chunk)
	if usageStr != "" {
		chunk = chunk[:len(chunk)-1] + "," + usageStr + "}"
	}

	return []byte("data: " + chunk + "\n\n")
}

// buildOpenAIStreamChunk is a convenience wrapper without usage data.
func buildOpenAIStreamChunk(chunkID string, created int64, model, content, reasoningContent, status string) []byte {
	return buildOpenAIStreamChunkWithUsage(chunkID, created, model, content, reasoningContent, status, "")
}

// ConvertQwenSSELineToOpenAI converts a single raw Qwen SSE line (possibly with "data: " prefix)
// to one or more OpenAI-format SSE bytes. Returns nil if the line produces no output.
// This is the exported adapter used by QwenExecutor to implement the two-step
// (Qwen → OpenAI → client) translation strategy for streaming responses.
func ConvertQwenSSELineToOpenAI(line, model string, originalRequestRawJSON, requestRawJSON []byte, param *any) [][]byte {
	return ConvertQwenResponseToOpenAI(nil, model, originalRequestRawJSON, requestRawJSON, []byte(line), param)
}

// buildOpenAIToolCallStreamChunk creates a streaming tool calls chunk in OpenAI format.
func buildOpenAIToolCallStreamChunk(chunkID string, created int64, model string, toolCalls []map[string]interface{}, finishReason string, usageStr string) []byte {
	toolCallsBytes, _ := json.Marshal(toolCalls)

	finishReasonStr := "null"
	if finishReason != "" {
		finishReasonStr = `"` + finishReason + `"`
	}

	chunk := fmt.Sprintf(
		`{"id":"%s","object":"chat.completion.chunk","created":%d,"model":"%s","choices":[{"index":0,"delta":{"tool_calls":%s},"finish_reason":%s}]}`,
		chunkID, created, model, string(toolCallsBytes), finishReasonStr,
	)

	// Append usage data if present
	if usageStr != "" {
		chunk = chunk[:len(chunk)-1] + "," + usageStr + "}"
	}

	return []byte("data: " + chunk + "\n\n")
}

// stripToolCallText removes all text blocks representing tool calls in any of the 7 supported formats.
func stripToolCallText(text string) string {
	text = stripThinking(text)
	// Strip XML
	reXML := regexp.MustCompile(`(?s)<tool_calls>.*?</tool_calls>`)
	text = reXML.ReplaceAllString(text, "")
	reXML2 := regexp.MustCompile(`(?s)<tool_call.*?>.*?</tool_call>`)
	text = reXML2.ReplaceAllString(text, "")

	// Strip bracket format
	reBracket := regexp.MustCompile(`(?s)\[function_calls\].*?\[/function_calls\]`)
	text = reBracket.ReplaceAllString(text, "")

	// Strip anthropic format
	reAntml := regexp.MustCompile(`(?s)<antml:function_calls>.*?</antml:function_calls>`)
	text = reAntml.ReplaceAllString(text, "")

	// Strip text marker
	reMarker := regexp.MustCompile(`(?i)##TOOL_CALL##.*?##END_CALL##`)
	text = reMarker.ReplaceAllString(text, "")

	// Strip code block format
	reCode := regexp.MustCompile("(?s)```tool_call.*?```")
	text = reCode.ReplaceAllString(text, "")

	return strings.TrimSpace(text)
}
