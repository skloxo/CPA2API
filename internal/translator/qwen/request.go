// Package qwen implements request translation between OpenAI and Qwen API formats.
//
// Key transformations:
//   - Model name mapping between OpenAI and Qwen identifiers
//   - System message merging into user messages for Qwen compatibility
//   - VLM image content part handling with optional base64→OSS upload
//   - Pass-through for most other fields since Qwen is OpenAI-compatible
package qwen

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// qwenModelMapping maps OpenAI model names to Qwen model names.
var qwenModelMapping = map[string]string{
	// Qwen 3.7 series
	"qwen3.7-max": "qwen3.7-max",
	// Qwen 3.6 series
	"qwen3.6-plus":         "qwen3.6-plus",
	"qwen3.6-plus-preview": "qwen3.6-plus-preview",
	"qwen3.6-max-preview":  "qwen3.6-max-preview",
	"qwen3.6-27b":          "qwen3.6-27b",
	"qwen3.6-35b-a3b":      "qwen3.6-35b-a3b",
	// Qwen 3.5 series
	"qwen3.5-plus":           "qwen3.5-plus",
	"qwen3.5-flash":          "qwen3.5-flash",
	"qwen3.5-omni-plus":      "qwen3.5-omni-plus",
	"qwen3.5-omni-flash":     "qwen3.5-omni-flash",
	"qwen3.5-max-2026-03-08": "qwen3.5-max-2026-03-08",
	"qwen3.5-397b-a17b":      "qwen3.5-397b-a17b",
	"qwen3.5-122b-a10b":      "qwen3.5-122b-a10b",
	"qwen3.5-27b":            "qwen3.5-27b",
	"qwen3.5-35b-a3b":        "qwen3.5-35b-a3b",
	// Qwen 3 series
	"qwen3-coder-plus":            "qwen3-coder-plus",
	"qwen3-vl-plus":               "qwen3-vl-plus",
	"qwen3-max-2026-01-23":        "qwen3-max-2026-01-23",
	"qwen3-omni-flash-2025-12-01": "qwen3-omni-flash-2025-12-01",
	// Qwen latest / classic
	"qwen-max-latest":      "qwen-max-latest",
	"qwen-plus-2025-07-28": "qwen-plus-2025-07-28",
	// Beta series
	"qwen-latest-series-invite-beta-v24": "qwen-latest-series-invite-beta-v24",
	"qwen-latest-series-invite-beta-v16": "qwen-latest-series-invite-beta-v16",
	// Long-context models routed via CLI endpoint
	"qwen-max-latest:long": "qwen-max-latest",
	"qwen3.7-max:long":     "qwen3.7-max",
}

// reverseQwenModelMapping maps Qwen model names back to OpenAI model names.
var reverseQwenModelMapping map[string]string

func init() {
	reverseQwenModelMapping = make(map[string]string, len(qwenModelMapping))
	for k, v := range qwenModelMapping {
		reverseQwenModelMapping[v] = k
	}
}

// ConvertOpenAIRequestToQwen translates an OpenAI-format request to Qwen native API format.
// Qwen's /api/v2/chat/completions endpoint requires a specific message structure with:
//   - version, incremental_output, chat_mode, parent_id top-level fields
//   - Messages with fid, parentId, childrenIds, user_action, files, timestamp, models,
//     chat_type, feature_config, extra, sub_chat_type
//   - System messages merged into user messages
//   - chat_id is NOT set here (injected by executor after translation)
func ConvertOpenAIRequestToQwen(model string, rawJSON []byte, stream bool) []byte {
	if len(rawJSON) == 0 || !gjson.ValidBytes(rawJSON) {
		return rawJSON
	}

	// 1. Fold incoming tool messages at the top of the function
	rawJSON = foldToolMessages(rawJSON)

	mappedModel := mapModelToQwen(model)
	ts := time.Now().UnixMilli()

	// --- Extract system messages and merge into user content ---
	var systemParts []string
	openaiMessages := gjson.GetBytes(rawJSON, "messages")
	if !openaiMessages.Exists() || !openaiMessages.IsArray() {
		return rawJSON
	}

	for _, msg := range openaiMessages.Array() {
		if msg.Get("role").String() == "system" {
			content := extractTextContent(msg.Get("content"))
			if content != "" {
				systemParts = append(systemParts, content)
			}
		}
	}

	// --- Build pre-processed messages array ---
	type msgInfo struct {
		role    string
		content string
		files   []interface{}
	}
	var processedMessages []msgInfo

	for _, msg := range openaiMessages.Array() {
		role := msg.Get("role").String()
		if role == "system" {
			continue // already extracted
		}

		var contentStr string
		var files []interface{}

		contentVal := msg.Get("content")
		if contentVal.Type == gjson.String {
			contentStr = contentVal.String()
		} else if contentVal.IsArray() {
			var textParts []string
			for _, part := range contentVal.Array() {
				partType := part.Get("type").String()
				if partType == "text" {
					textParts = append(textParts, part.Get("text").String())
				} else if partType == "image_url" {
					imageURL := part.Get("image_url.url").String()
					if imageURL != "" {
						if isDataURI(imageURL) {
							mimeType, data, err := parseDataURI(imageURL)
							if err == nil {
								fileExt := mimeToFileExt(mimeType)
								filename := fmt.Sprintf("upload.%s", fileExt)
								files = append(files, map[string]interface{}{
									"name":        filename,
									"filetype":    "image",
									"size":        len(data),
									"url":         imageURL,
									"is_snapshot": false,
									"status":      "done",
									"biz_type":    "qwen",
									"space_type":  "qwen",
								})
							}
						} else {
							filename := "upload.png"
							if strings.Contains(imageURL, ".jpg") || strings.Contains(imageURL, ".jpeg") {
								filename = "upload.jpg"
							} else if strings.Contains(imageURL, ".gif") {
								filename = "upload.gif"
							} else if strings.Contains(imageURL, ".webp") {
								filename = "upload.webp"
							}
							files = append(files, map[string]interface{}{
								"name":        filename,
								"filetype":    "image",
								"url":         imageURL,
								"is_snapshot": false,
								"status":      "done",
								"biz_type":    "qwen",
								"space_type":  "qwen",
							})
						}
					}
				}
			}
			contentStr = strings.Join(textParts, "\n")
		}

		// Prepend system instructions to the first user message
		if role == "user" && len(systemParts) > 0 {
			sysText := strings.Join(systemParts, "\n\n")
			if contentStr != "" {
				contentStr = sysText + "\n\n" + contentStr
			} else {
				contentStr = sysText
			}
			systemParts = nil // only merge once
		}

		processedMessages = append(processedMessages, msgInfo{
			role:    role,
			content: contentStr,
			files:   files,
		})
	}

	// If no user message was found but we had system content, create a user message
	if len(systemParts) > 0 && len(processedMessages) == 0 {
		processedMessages = append(processedMessages, msgInfo{
			role:    "user",
			content: strings.Join(systemParts, "\n\n"),
		})
	}

	// Consolidate multiple turns into a single message with role prefixes to satisfy Qwen Web API limits.
	if len(processedMessages) > 1 {
		var parts []string
		var consolidatedFiles []interface{}
		for _, msg := range processedMessages {
			if msg.content != "" {
				prefix := "Human: "
				switch strings.ToLower(msg.role) {
				case "user":
					prefix = "Human: "
				case "assistant":
					prefix = "Assistant: "
				case "system":
					prefix = "System: "
				}
				parts = append(parts, prefix+msg.content)
			}
			if len(msg.files) > 0 {
				consolidatedFiles = append(consolidatedFiles, msg.files...)
			}
		}
		consolidatedText := strings.Join(parts, "\n\n")
		processedMessages = []msgInfo{
			{
				role:    "user",
				content: consolidatedText,
				files:   consolidatedFiles,
			},
		}
	}

	N := len(processedMessages)
	ids := make([]string, N)
	for i := 0; i < N; i++ {
		ids[i] = uuid.New().String()
	}

	// --- Build Qwen messages array with sequential UUID tree chaining ---
	var qwenMessages []map[string]interface{}
	for i, msg := range processedMessages {
		var pID interface{} = nil
		if i > 0 {
			pID = ids[i-1]
		}
		var childrenIds []string
		if i < N-1 {
			childrenIds = []string{ids[i+1]}
		} else {
			childrenIds = []string{uuid.New().String()}
		}

		filesVal := msg.files
		if filesVal == nil {
			filesVal = []interface{}{}
		}

		qwenMsg := map[string]interface{}{
			"fid":            ids[i],
			"parentId":       pID,
			"childrenIds":    childrenIds,
			"role":           msg.role,
			"content":        msg.content,
			"user_action":    "chat",
			"files":          filesVal,
			"timestamp":      ts,
			"models":         []string{mappedModel},
			"chat_type":      "t2t",
			"feature_config": buildFeatureConfig(),
			"extra":          map[string]interface{}{"meta": map[string]interface{}{"subChatType": "t2t"}},
			"sub_chat_type":  "t2t",
			"parent_id":      pID,
		}
		qwenMessages = append(qwenMessages, qwenMsg)
	}

	// 2. Generate and inject the tool system prompt into the last user message
	var toolSystemPrompt string
	toolsVal := gjson.GetBytes(rawJSON, "tools")
	if toolsVal.Exists() && toolsVal.IsArray() {
		var toolsSlice []map[string]interface{}
		if err := json.Unmarshal([]byte(toolsVal.Raw), &toolsSlice); err == nil && len(toolsSlice) > 0 {
			toolSystemPrompt = BuildToolSystemPrompt(toolsSlice, gjson.GetBytes(rawJSON, "tool_choice").Value())
		}
	}

	if toolSystemPrompt != "" {
		// Find the last user message in the message list and prepend the tool system instructions
		for i := len(qwenMessages) - 1; i >= 0; i-- {
			if qwenMessages[i]["role"] == "user" {
				if content, ok := qwenMessages[i]["content"].(string); ok {
					if content != "" {
						qwenMessages[i]["content"] = toolSystemPrompt + "\n\n" + content
					} else {
						qwenMessages[i]["content"] = toolSystemPrompt
					}
				}
				break
			}
		}
	}

	// --- Build top-level Qwen request ---
	qwenReq := map[string]interface{}{
		"stream":             stream,
		"version":            "2.1",
		"incremental_output": true,
		"chat_mode":          "normal",
		"model":              mappedModel,
		"parent_id":          nil,
		"timestamp":          ts,
	}

	// Preserve chat_id if already present in the body (executor injects it later)
	if chatID := gjson.GetBytes(rawJSON, "chat_id").String(); chatID != "" {
		qwenReq["chat_id"] = chatID
	}

	// Preserve temperature, top_p, max_tokens if present
	if temp := gjson.GetBytes(rawJSON, "temperature"); temp.Exists() {
		qwenReq["temperature"] = temp.Float()
	}
	if topP := gjson.GetBytes(rawJSON, "top_p"); topP.Exists() {
		qwenReq["top_p"] = topP.Float()
	}
	if maxTok := gjson.GetBytes(rawJSON, "max_tokens"); maxTok.Exists() {
		qwenReq["max_tokens"] = maxTok.Int()
	}

	qwenReq["messages"] = qwenMessages

	out, err := json.Marshal(qwenReq)
	if err != nil {
		return rawJSON
	}
	return out
}

// buildFeatureConfig returns the standard Qwen feature_config for chat requests.
func buildFeatureConfig() map[string]interface{} {
	return map[string]interface{}{
		"thinking_enabled":     true,
		"output_schema":        "phase",
		"research_mode":        "normal",
		"auto_thinking":        true,
		"thinking_mode":        "Auto",
		"thinking_format":      "summary",
		"auto_search":          false,
		"code_interpreter":     false,
		"plugins_enabled":      false,
		"function_calling":     false,
		"enable_tools":         false,
		"enable_function_call": false,
		"tool_choice":          "none",
	}
}

// ConvertOpenAIRequestToQwenWithAuth translates an OpenAI-format request to Qwen native format,
// uploading base64 images to Qwen OSS when an auth token is provided.
// This uses the same native format conversion as ConvertOpenAIRequestToQwen,
// but additionally handles base64 image upload to Qwen OSS.
func ConvertOpenAIRequestToQwenWithAuth(model string, rawJSON []byte, stream bool, token string) []byte {
	// First do the standard OpenAI→Qwen native conversion
	result := ConvertOpenAIRequestToQwen(model, rawJSON, stream)
	if bytes.Equal(result, rawJSON) {
		return rawJSON // validation failed, return as-is
	}

	// If we have a token, handle base64 image uploads
	if strings.TrimSpace(token) != "" {
		result = ConvertQwenNativeImageUpload(result, token)
	}

	return result
}

// ConvertQwenNativeImageUpload handles base64 image upload in Qwen native format.
// In the native format, files are in message.files array rather than message.content.
func ConvertQwenNativeImageUpload(body []byte, token string) []byte {
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return body
	}

	for msgIdx, msg := range messages.Array() {
		files := msg.Get("files")
		if !files.Exists() || !files.IsArray() {
			continue
		}

		var newFiles []interface{}
		changed := false
		for _, f := range files.Array() {
			url := f.Get("url").String()
			if isDataURI(url) {
				uploadedURL, err := uploadBase64ToQwenOSS(url, token)
				if err == nil && uploadedURL != "" {
					changed = true
					fMap := make(map[string]interface{})
					f.ForEach(func(k, v gjson.Result) bool {
						fMap[k.String()] = v.Value()
						return true
					})
					fMap["url"] = uploadedURL
					newFiles = append(newFiles, fMap)
					continue
				}
			}
			newFiles = append(newFiles, f.Value())
		}

		if changed {
			path := fmt.Sprintf("messages.%d.files", msgIdx)
			var err error
			body, err = sjson.SetBytes(body, path, newFiles)
			if err != nil {
				return body
			}
		}
	}

	return body
}

// ConvertQwenRequestToOpenAI translates a Qwen-format request to OpenAI format.
// Since Qwen is OpenAI-compatible, the main transformations are:
//   - Model name reverse mapping
func ConvertQwenRequestToOpenAI(model string, rawJSON []byte, stream bool) []byte {
	if len(rawJSON) == 0 || !gjson.ValidBytes(rawJSON) {
		return rawJSON
	}

	result := rawJSON

	// Reverse map model name
	mappedModel := mapModelToOpenAI(model)
	if mappedModel != model {
		var err error
		result, err = sjson.SetBytes(result, "model", mappedModel)
		if err != nil {
			return rawJSON
		}
	}

	return result
}

// mapModelToQwen maps an OpenAI model name to a Qwen model name.
func mapModelToQwen(model string) string {
	model = strings.TrimSpace(model)
	if mapped, ok := qwenModelMapping[model]; ok {
		return mapped
	}
	return model
}

// mapModelToOpenAI maps a Qwen model name back to an OpenAI model name.
func mapModelToOpenAI(model string) string {
	model = strings.TrimSpace(model)
	if mapped, ok := reverseQwenModelMapping[model]; ok {
		return mapped
	}
	return model
}

// IsLongContextModel reports whether the model should be routed to the CLI long-context endpoint.
func IsLongContextModel(model string) bool {
	model = strings.TrimSpace(model)
	lower := strings.ToLower(model)
	return strings.HasSuffix(lower, ":long") || lower == "qwen-long"
}

// hasSystemMessages checks if the request contains system messages.
func hasSystemMessages(body []byte) bool {
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return false
	}
	for _, msg := range messages.Array() {
		if msg.Get("role").String() == "system" {
			return true
		}
	}
	return false
}

// mergeSystemMessages merges system messages into the first user message.
// Qwen requires system instructions to be part of the user message content.
func mergeSystemMessages(body []byte) []byte {
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return body
	}

	msgs := messages.Array()
	var systemParts []string
	var otherMsgs []gjson.Result

	for _, msg := range msgs {
		role := msg.Get("role").String()
		if role == "system" {
			content := extractTextContent(msg.Get("content"))
			if content != "" {
				systemParts = append(systemParts, content)
			}
		} else {
			otherMsgs = append(otherMsgs, msg)
		}
	}

	if len(systemParts) == 0 {
		return body
	}

	// Find the first user message and prepend system content
	systemText := strings.Join(systemParts, "\n\n")
	found := false
	for i, msg := range otherMsgs {
		if msg.Get("role").String() == "user" {
			originalContent := extractTextContent(msg.Get("content"))
			mergedContent := systemText
			if originalContent != "" {
				mergedContent = systemText + "\n\n" + originalContent
			}
			var err error
			body, err = sjson.SetBytes(body, "messages."+fmt.Sprintf("%d", findOriginalIndex(msgs, msg))+".content", mergedContent)
			if err == nil {
				found = true
			}
			break
		}
		_ = i
	}

	if !found {
		// No user message found; create one with system content
		newMsg := `{"role":"user","content":"` + escapeJSON(systemText) + `"}`
		// Remove system messages and prepend user message
		newMsgs := "[" + newMsg
		for _, msg := range otherMsgs {
			newMsgs += "," + msg.Raw
		}
		newMsgs += "]"
		var err error
		body, err = sjson.SetRawBytes(body, "messages", []byte(newMsgs))
		if err != nil {
			return body
		}
	} else {
		// Remove system messages from the array
		body = removeSystemMessages(body)
	}

	return body
}

// extractTextContent extracts text content from a message content field.
func extractTextContent(content gjson.Result) string {
	if !content.Exists() {
		return ""
	}
	if content.Type == gjson.String {
		return content.String()
	}
	if content.IsArray() {
		var parts []string
		for _, part := range content.Array() {
			if part.Get("type").String() == "text" {
				text := part.Get("text").String()
				if text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// findOriginalIndex finds the index of a message in the original messages array.
func findOriginalIndex(original []gjson.Result, target gjson.Result) int {
	for i, msg := range original {
		if msg.Raw == target.Raw {
			return i
		}
	}
	return 0
}

// foldToolMessages converts OpenAI tool_calls and role=tool messages into
// Qwen-compatible format. OpenAI uses structured tool_calls JSON, but Qwen
// expects tool interactions as text in the message content.
func foldToolMessages(body []byte) []byte {
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return body
	}

	msgs := messages.Array()
	if len(msgs) == 0 {
		return body
	}

	var newMessages []interface{}
	for i := 0; i < len(msgs); i++ {
		msg := msgs[i]
		role := msg.Get("role").String()

		if role == "assistant" && msg.Get("tool_calls").Exists() {
			// Convert tool_calls to text format
			var parts []string
			for _, tc := range msg.Get("tool_calls").Array() {
				fnName := tc.Get("function.name").String()
				fnArgs := tc.Get("function.arguments").String()
				tcID := tc.Get("id").String()
				parts = append(parts, fmt.Sprintf(`{\n  "tool_calls\n  [call:%s](%s)\n  [id:%s]\n}`, fnName, fnArgs, tcID))
			}
			// Build folded assistant message
			content := msg.Get("content").String()
			if content == "" {
				content = "null"
			}
			foldedMsg := map[string]interface{}{
				"role":    "assistant",
				"content": content + "\n" + strings.Join(parts, "\n"),
			}
			newMessages = append(newMessages, foldedMsg)

			// Collect subsequent role=tool messages
			for i+1 < len(msgs) && msgs[i+1].Get("role").String() == "tool" {
				i++
				toolMsg := msgs[i]
				toolContent := toolMsg.Get("content").String()
				toolContent = compactToolResult(toolContent)
				toolCallID := toolMsg.Get("tool_call_id").String()
				toolFolded := map[string]interface{}{
					"role":    "user",
					"content": fmt.Sprintf(`{\n  "tool_result\n  [call:%s](%s)\n}`, toolCallID, toolContent),
				}
				newMessages = append(newMessages, toolFolded)
			}
		} else {
			// Keep message as-is
			var m map[string]interface{}
			if err := json.Unmarshal([]byte(msg.Raw), &m); err == nil {
				newMessages = append(newMessages, m)
			}
		}
	}

	result, _ := sjson.SetBytes(body, "messages", newMessages)
	return result
}

// compactToolResult checks if tool result content exceeds max size and compresses it.
// Preserves 3000 head characters and 1000 tail characters.
func compactToolResult(s string) string {
	runes := []rune(s)
	if len(runes) <= 6000 {
		return s
	}
	head := string(runes[:3000])
	tail := string(runes[len(runes)-1000:])
	truncated := len(runes) - 4000
	return fmt.Sprintf("%s\n... [TRUNCATED %d CHARS] ...\n%s", head, truncated, tail)
}

// removeSystemMessages removes system messages from the request body.
func removeSystemMessages(body []byte) []byte {
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return body
	}

	var kept []string
	for _, msg := range messages.Array() {
		if msg.Get("role").String() != "system" {
			kept = append(kept, msg.Raw)
		}
	}

	rawMessages := "[" + strings.Join(kept, ",") + "]"
	result, err := sjson.SetRawBytes(body, "messages", []byte(rawMessages))
	if err != nil {
		return body
	}
	return result
}

// escapeJSON escapes a string for safe JSON embedding.
// Uses encoding/json to handle all special characters correctly.
func escapeJSON(s string) string {
	b, _ := json.Marshal(s)
	if len(b) >= 2 && b[0] == '"' && b[len(b)-1] == '"' {
		return string(b[1 : len(b)-1])
	}
	return string(b)
}

// convertImageContentParts transforms OpenAI image_url content parts into
// Qwen-compatible format. Both URL and base64 data URI images pass through
// since Qwen's OpenAI-compatible endpoint accepts both formats.
func convertImageContentParts(body []byte) []byte {
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return body
	}

	for msgIdx, msg := range messages.Array() {
		content := msg.Get("content")
		if !content.Exists() || !content.IsArray() {
			continue
		}

		var newParts []string
		changed := false
		for _, part := range content.Array() {
			partType := part.Get("type").String()
			if partType == "image_url" {
				imageURL := part.Get("image_url.url").String()
				if imageURL != "" {
					changed = true
					newParts = append(newParts, fmt.Sprintf(
						`{"type":"image_url","image_url":{"url":"%s"}}`,
						escapeJSON(imageURL),
					))
					continue
				}
			}
			newParts = append(newParts, part.Raw)
		}

		if changed {
			path := fmt.Sprintf("messages.%d.content", msgIdx)
			rawArr := "[" + strings.Join(newParts, ",") + "]"
			var err error
			body, err = sjson.SetRawBytes(body, path, []byte(rawArr))
			if err != nil {
				return body
			}
		}
	}

	return body
}

// convertImageContentPartsWithUpload handles image conversion with base64→OSS upload.
// When a token is provided, base64 data URIs are uploaded to Qwen's OSS for better reliability.
func convertImageContentPartsWithUpload(body []byte, token string) []byte {
	messages := gjson.GetBytes(body, "messages")
	if !messages.Exists() || !messages.IsArray() {
		return body
	}

	for msgIdx, msg := range messages.Array() {
		content := msg.Get("content")
		if !content.Exists() || !content.IsArray() {
			continue
		}

		var newParts []string
		changed := false
		for _, part := range content.Array() {
			partType := part.Get("type").String()
			if partType != "image_url" {
				newParts = append(newParts, part.Raw)
				continue
			}

			imageURL := part.Get("image_url.url").String()
			if imageURL == "" {
				newParts = append(newParts, part.Raw)
				continue
			}

			if isDataURI(imageURL) {
				// Try uploading base64 to Qwen OSS
				uploadedURL, err := uploadBase64ToQwenOSS(imageURL, token)
				if err == nil && uploadedURL != "" {
					changed = true
					newParts = append(newParts, fmt.Sprintf(
						`{"type":"image_url","image_url":{"url":"%s"}}`,
						escapeJSON(uploadedURL),
					))
					continue
				}
				// Upload failed; pass through as-is
			}

			newParts = append(newParts, part.Raw)
		}

		if changed {
			path := fmt.Sprintf("messages.%d.content", msgIdx)
			rawArr := "[" + strings.Join(newParts, ",") + "]"
			var err error
			body, err = sjson.SetRawBytes(body, path, []byte(rawArr))
			if err != nil {
				return body
			}
		}
	}

	return body
}

// isDataURI checks if a string is a base64 data URI.
func isDataURI(s string) bool {
	return strings.HasPrefix(s, "data:") && strings.Contains(s, ";base64,")
}

// parseDataURI extracts the MIME type and base64 data from a data URI.
func parseDataURI(dataURI string) (mimeType string, data []byte, err error) {
	idx := strings.Index(dataURI, ";base64,")
	if idx < 0 {
		return "", nil, fmt.Errorf("invalid data URI format")
	}
	mimeType = dataURI[5:idx]
	b64Data := dataURI[idx+8:]
	data, err = base64.StdEncoding.DecodeString(b64Data)
	if err != nil {
		return "", nil, fmt.Errorf("base64 decode failed: %w", err)
	}
	return mimeType, data, nil
}

// mimeToFileExt extracts a simple file extension from a MIME type.
func mimeToFileExt(mime string) string {
	switch {
	case strings.Contains(mime, "png"):
		return "png"
	case strings.Contains(mime, "jpeg"), strings.Contains(mime, "jpg"):
		return "jpg"
	case strings.Contains(mime, "gif"):
		return "gif"
	case strings.Contains(mime, "webp"):
		return "webp"
	default:
		return "png"
	}
}

// qwenOssStsResponse represents the STS token response from Qwen's file upload API.
type qwenOssStsResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Credentials struct {
			AccessKeyID     string `json:"AccessKeyId"`
			AccessKeySecret string `json:"AccessKeySecret"`
			SecurityToken   string `json:"SecurityToken"`
		} `json:"credentials"`
		FileInfo struct {
			URL string `json:"url"`
			ID  string `json:"id"`
		} `json:"file_info"`
	} `json:"data"`
}

// uploadBase64ToQwenOSS uploads a base64 data URI to Qwen's OSS and returns the URL.
func uploadBase64ToQwenOSS(dataURI string, token string) (string, error) {
	mimeType, data, err := parseDataURI(dataURI)
	if err != nil {
		return "", err
	}

	fileExt := mimeToFileExt(mimeType)
	filename := fmt.Sprintf("upload.%s", fileExt)
	filesize := len(data)

	// Step 1: Get STS token
	stsURL := "https://chat.qwen.ai/api/v1/files/upload"
	stsPayload := map[string]any{
		"filename":    filename,
		"filesize":    filesize,
		"filetype":    "image",
		"is_snapshot": false,
		"biz_type":    "qwen",
		"space_type":  "qwen",
	}
	stsBody, _ := json.Marshal(stsPayload)

	stsReq, err := http.NewRequestWithContext(context.Background(), http.MethodPost, stsURL, bytes.NewReader(stsBody))
	if err != nil {
		return "", err
	}
	stsReq.Header.Set("Content-Type", "application/json")
	stsReq.Header.Set("Authorization", "Bearer "+token)

	stsResp, err := (&http.Client{}).Do(stsReq)
	if err != nil {
		return "", fmt.Errorf("sts request failed: %w", err)
	}
	defer stsResp.Body.Close()

	stsRespBody, err := io.ReadAll(stsResp.Body)
	if err != nil {
		return "", fmt.Errorf("sts read failed: %w", err)
	}

	var stsResult qwenOssStsResponse
	if err := json.Unmarshal(stsRespBody, &stsResult); err != nil {
		return "", fmt.Errorf("sts parse failed: %w", err)
	}
	if !stsResult.Success || stsResult.Data.FileInfo.URL == "" {
		return "", fmt.Errorf("sts returned no file URL")
	}

	// Step 2: Upload to OSS with STS credentials
	cred := stsResult.Data.Credentials
	ossURL := fmt.Sprintf("https://qwen-chat-cn-hangzhou.oss-cn-hangzhou.aliyuncs.com/?x-oss-security-token=%s",
		cred.SecurityToken)

	ossReq, err := http.NewRequestWithContext(context.Background(), http.MethodPut, ossURL, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	ossReq.Header.Set("Content-Type", mimeType)
	ossReq.Header.Set("Authorization", fmt.Sprintf("OSS %s:%s", cred.AccessKeyID, cred.AccessKeySecret))

	ossResp, err := (&http.Client{}).Do(ossReq)
	if err != nil {
		return "", fmt.Errorf("oss upload failed: %w", err)
	}
	defer ossResp.Body.Close()
	io.Copy(io.Discard, ossResp.Body)

	if ossResp.StatusCode < 200 || ossResp.StatusCode >= 300 {
		return "", fmt.Errorf("oss upload returned status %d", ossResp.StatusCode)
	}

	return stsResult.Data.FileInfo.URL, nil
}

// GetQwenModelMapping returns a copy of the Qwen model mapping for external use.
func GetQwenModelMapping() map[string]string {
	out := make(map[string]string, len(qwenModelMapping))
	for k, v := range qwenModelMapping {
		out[k] = v
	}
	return out
}
