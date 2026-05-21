// Package qwen implements request translation between OpenAI and Qwen API formats.
//
// Key transformations:
//   - Model name mapping between OpenAI and Qwen identifiers
//   - System message merging into user messages for Qwen compatibility
//   - Pass-through for most other fields since Qwen is OpenAI-compatible
package qwen

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// qwenModelMapping maps OpenAI model names to Qwen model names.
var qwenModelMapping = map[string]string{
	"qwen3-max":          "qwen3-max",
	"qwen3-plus":         "qwen3-plus",
	"qwen3-turbo":        "qwen3-turbo",
	"qwen-max":           "qwen-max",
	"qwen-plus":          "qwen-plus",
	"qwen-turbo":         "qwen-turbo",
	"qwq-plus":           "qwq-plus",
	"qwq-max":            "qwq-max",
	"qwen-long":          "qwen-long",
	"qwen-vl-max":        "qwen-vl-max",
	"qwen-vl-plus":       "qwen-vl-plus",
	"qwen-audio-turbo":   "qwen-audio-turbo",
	"qwen-coder-plus":    "qwen-coder-plus",
	"qwen-coder-turbo":   "qwen-coder-turbo",
	"qwen2.5-max":        "qwen2.5-max",
	"qwen2.5-plus":       "qwen2.5-plus",
	"qwen2.5-turbo":      "qwen2.5-turbo",
	"qwen2.5-coder-plus": "qwen2.5-coder-plus",
}

// reverseQwenModelMapping maps Qwen model names back to OpenAI model names.
var reverseQwenModelMapping map[string]string

func init() {
	reverseQwenModelMapping = make(map[string]string, len(qwenModelMapping))
	for k, v := range qwenModelMapping {
		reverseQwenModelMapping[v] = k
	}
}

// ConvertOpenAIRequestToQwen translates an OpenAI-format request to Qwen format.
// Since Qwen is OpenAI-compatible, the main transformations are:
//   - Model name mapping
//   - System message merging into user messages
func ConvertOpenAIRequestToQwen(model string, rawJSON []byte, stream bool) []byte {
	if len(rawJSON) == 0 || !gjson.ValidBytes(rawJSON) {
		return rawJSON
	}

	result := rawJSON

	// Map model name
	mappedModel := mapModelToQwen(model)
	if mappedModel != model {
		var err error
		result, err = sjson.SetBytes(result, "model", mappedModel)
		if err != nil {
			return rawJSON
		}
	}

	// Merge system messages into user messages
	if hasSystemMessages(result) {
		result = mergeSystemMessages(result)
	}

	return result
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

// escapeJSON escapes special characters for JSON string values.
func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}
