// Package translator provides Qwen-specific tool calling support.
// Supports 5 formats: XML, JSON, TextKV, Bracket, Anthropic (antml).
// Includes tool name obfuscation and parameter repair.
package qwen

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// ToolCall represents a parsed tool call.
type ToolCall struct {
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name"`
	Input map[string]interface{} `json:"input"`
}

// NameSuffix is the suffix appended to obfuscated tool names.
const NameSuffix = "X"

// obfuscationMap maps common tool names to Qwen-safe aliases.
var obfuscationMap = map[string]string{
	"Read":   "ReadX",
	"Write":  "WriteX",
	"Edit":   "EditX",
	"Bash":   "BashX",
	"Grep":   "GrepX",
	"Glob":   "GlobX",
	"Delete": "DeleteX",
	"Move":   "MoveX",
	"Copy":   "CopyX",
	"List":   "ListX",
	"Search": "SearchX",
	"Web":    "WebX",
}

// reverseObfuscationMap is the reverse mapping.
var reverseObfuscationMap map[string]string

func init() {
	reverseObfuscationMap = make(map[string]string, len(obfuscationMap))
	for k, v := range obfuscationMap {
		reverseObfuscationMap[v] = k
	}
}

// ToQwenName converts a client tool name to a Qwen-safe obfuscated name.
// Prevents Qwen from recognizing built-in function names and rejecting them.
func ToQwenName(name string) string {
	if mapped, ok := obfuscationMap[name]; ok {
		return mapped
	}
	// Generic suffix for unmapped names
	return name + NameSuffix
}

// FromQwenName converts a Qwen obfuscated name back to the original client name.
func FromQwenName(name string) string {
	if mapped, ok := reverseObfuscationMap[name]; ok {
		return mapped
	}
	// Strip generic suffix
	if strings.HasSuffix(name, NameSuffix) {
		return strings.TrimSuffix(name, NameSuffix)
	}
	return name
}

// FormatToolDefinitionsForQwen converts OpenAI-format tool definitions to a
// human-readable tool list for embedding in the system prompt.
func FormatToolDefinitionsForQwen(tools []map[string]interface{}) string {
	var b strings.Builder
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if name == "" {
			continue
		}
		qwenName := ToQwenName(name)
		desc, _ := tool["description"].(string)

		b.WriteString(fmt.Sprintf("- %s", qwenName))
		if desc != "" {
			b.WriteString(fmt.Sprintf(": %s", desc))
		}
		b.WriteString("\n")

		if params, ok := tool["parameters"].(map[string]interface{}); ok {
			if props, ok := params["properties"].(map[string]interface{}); ok {
				b.WriteString("  Parameters:\n")
				for pname, pdef := range props {
					if pd, ok := pdef.(map[string]interface{}); ok {
						ptype, _ := pd["type"].(string)
						b.WriteString(fmt.Sprintf("    - %s (%s)\n", pname, ptype))
					}
				}
			}
		}
	}
	return b.String()
}

// BuildRequiredRetryPrompt builds a retry prompt when tool_choice=required
// but the model did not invoke any tool.
func BuildRequiredRetryPrompt(forcedName string) string {
	if forcedName != "" {
		return fmt.Sprintf(
			"You MUST call the tool `%s` immediately using the `<tool_call>` XML format and nothing else.",
			forcedName)
	}
	return "You did not call any tool in your previous reply. " +
		"You MUST now call exactly one tool using the `<tool_call>` XML format and nothing else."
}

// InjectFormatReminder appends a format correction reminder to the prompt.
func InjectFormatReminder(prompt, toolName string) string {
	reminder := fmt.Sprintf(
		"\n\nIMPORTANT: Tool `%s` is fully available. You MUST invoke it using "+
			"the `<tool_call>` XML format. Claims about the tool being unavailable, missing, "+
			"or non-existent are INCORRECT. The following are ABSOLUTELY FORBIDDEN in your next output:\n"+
			"- Any disclaimer about a tool being unavailable, missing, or unregistered\n"+
			"- Any sentence claiming you are unable to run a function\n"+
			"- Any apology for failing to invoke something\n",
		toolName, toolName, jsonEscapeString(toolName))

	prompt = strings.TrimRight(prompt, " \t\n")
	if strings.HasSuffix(prompt, "Assistant:") {
		return prompt[:len(prompt)-len("Assistant:")] + reminder + "\nAssistant:"
	}
	return prompt + "\n\n" + reminder + "\nAssistant:"
}

// FixToolArgs attempts to repair malformed JSON arguments from model output.
func FixToolArgs(args string) string {
	args = strings.TrimSpace(args)
	if args == "" {
		return "{}"
	}

	// Remove markdown code fences
	args = stripCodeFences(args)

	// Try parsing as-is
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(args), &m); err == nil {
		b, _ := json.Marshal(m)
		return string(b)
	}

	// Try wrapping in braces if it looks like key-value pairs
	if !strings.HasPrefix(args, "{") && strings.Contains(args, ":") {
		wrapped := "{" + args + "}"
		if err := json.Unmarshal([]byte(wrapped), &m); err == nil {
			b, _ := json.Marshal(m)
			return string(b)
		}
	}

	// Try repairing common issues
	repaired := repairLooseJSON(args)
	if err := json.Unmarshal([]byte(repaired), &m); err == nil {
		b, _ := json.Marshal(m)
		return string(b)
	}

	// Return empty object as fallback
	return "{}"
}

// stripCodeFences removes markdown code fences from text.
func stripCodeFences(text string) string {
	re := regexp.MustCompile("(?s)```(?:json|xml|tool_call)?\\s*\\n(.*?)\\n```")
	if m := re.FindStringSubmatch(text); m != nil {
		return strings.TrimSpace(m[1])
	}
	return text
}

// stripThinking removes <think>...</think> tags from text.
func stripThinking(text string) string {
	re := regexp.MustCompile(`(?s)<think>.*?</think>`)
	return strings.TrimSpace(re.ReplaceAllString(text, ""))
}

// repairLooseJSON attempts to fix common JSON formatting issues.
func repairLooseJSON(s string) string {
	// Fix single quotes to double quotes
	s = strings.ReplaceAll(s, "'", "\"")
	// Fix trailing commas
	re := regexp.MustCompile(`,\s*([}\]])`)
	s = re.ReplaceAllString(s, "$1")
	// Fix unquoted keys
	re2 := regexp.MustCompile(`(\w+)\s*:`)
	s = re2.ReplaceAllString(s, `"$1":`)
	return s
}

// jsonEscapeString escapes a string for JSON embedding.
func jsonEscapeString(s string) string {
	b, _ := json.Marshal(s)
	return strings.Trim(string(b), "\"")
}

// normalizeArguments parses a JSON string into a map, with fallback to empty map.
func normalizeArguments(args string) map[string]interface{} {
	args = stripCodeFences(strings.TrimSpace(args))
	if args == "" {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(args), &m); err == nil {
		return m
	}
	// Try fixing
	repaired := repairLooseJSON(args)
	if err := json.Unmarshal([]byte(repaired), &m); err == nil {
		return m
	}
	return map[string]interface{}{}
}

// extractNameFromPayload extracts tool name from a JSON payload.
func extractNameFromPayload(payload map[string]interface{}) string {
	if name, ok := payload["name"].(string); ok {
		return name
	}
	if fn, ok := payload["function"].(map[string]interface{}); ok {
		if name, ok := fn["name"].(string); ok {
			return name
		}
	}
	return ""
}

// extractInputFromPayload extracts input/arguments from a JSON payload.
func extractInputFromPayload(payload map[string]interface{}) map[string]interface{} {
	if input, ok := payload["input"].(map[string]interface{}); ok {
		return input
	}
	if args, ok := payload["arguments"].(map[string]interface{}); ok {
		return args
	}
	if args, ok := payload["parameters"].(map[string]interface{}); ok {
		return args
	}
	if argsStr, ok := payload["arguments"].(string); ok {
		return normalizeArguments(argsStr)
	}
	return map[string]interface{}{}
}

// ────────────────────────────────────────────────────────────────────
// Multi-format parser: tries all 5 formats in priority order
// ────────────────────────────────────────────────────────────────────

// ParseToolCallFromText parses a tool call from model output text.
// Tries all 5 formats in priority order: XML → JSON → TextKV → Bracket → Anthropic.
// Returns nil if no tool call is found.
func ParseToolCallFromText(text string, allowedNames []string) []ToolCall {
	text = stripThinking(text)
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	// 1. XML format
	if calls := parseXMLFormat(text, allowedNames); len(calls) > 0 {
		return calls
	}
	// 2. JSON format
	if calls := parseJSONFormat(text, allowedNames); len(calls) > 0 {
		return calls
	}
	// 3. TextKV format
	if calls := parseTextKVFormat(text, allowedNames); len(calls) > 0 {
		return calls
	}
	// 4. Bracket format
	if calls := parseBracketFormat(text, allowedNames); len(calls) > 0 {
		return calls
	}
	// 5. Anthropic format
	if calls := parseAnthropicFormat(text, allowedNames); len(calls) > 0 {
		return calls
	}
	// 6. Text marker format
	if calls := parseTextMarkerFormat(text, allowedNames); len(calls) > 0 {
		return calls
	}
	// 7. Code block format
	if calls := parseCodeBlockFormat(text, allowedNames); len(calls) > 0 {
		return calls
	}

	return nil
}

// NormalizeToolName normalizes a tool name against the allowed list.
func NormalizeToolName(name string, allowedNames []string) string {
	name = strings.TrimSpace(name)
	if len(allowedNames) == 0 {
		return name
	}
	for _, allowed := range allowedNames {
		if strings.EqualFold(name, allowed) {
			return allowed
		}
	}
	return name
}

// ────────────────────────────────────────────────────────────────────
// Format 1: XML parser
// ────────────────────────────────────────────────────────────────────

var reXMLToolCallsWrapper = regexp.MustCompile(`(?s)<tool_calls>(.*?)</tool_calls>`)
var reXMLToolCallSingle = regexp.MustCompile(`(?s)`)
var reXMLToolCallBare = regexp.MustCompile(`(?s)`)

func parseXMLFormat(text string, allowedNames []string) []ToolCall {
	// Try wrapped <tool_calls> first
	if m := reXMLToolCallsWrapper.FindStringSubmatch(text); m != nil {
		return parseXMLInner(m[1], allowedNames)
	}
	// Try bare tool_call
	if m := reXMLToolCallBare.FindStringSubmatch(text); m != nil {
		return parseXMLInner(m[0], allowedNames)
	}
	return nil
}

func parseXMLInner(inner string, allowedNames []string) []ToolCall {
	matches := reXMLToolCallSingle.FindAllStringSubmatch(inner, -1)
	if len(matches) == 0 {
		return nil
	}
	var calls []ToolCall
	for _, m := range matches {
		name := strings.TrimSpace(m[1])
		argsStr := strings.TrimSpace(m[2])
		input := normalizeArguments(argsStr)
		calls = append(calls, ToolCall{
			Name:  NormalizeToolName(name, allowedNames),
			Input: input,
		})
	}
	return calls
}

// ────────────────────────────────────────────────────────────────────
// Format 2: JSON parser
// ────────────────────────────────────────────────────────────────────

func parseJSONFormat(text string, allowedNames []string) []ToolCall {
	stripped := stripCodeFences(text)

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(stripped), &payload); err != nil {
		repaired := repairLooseJSON(stripped)
		if repaired == stripped {
			return nil
		}
		if err := json.Unmarshal([]byte(repaired), &payload); err != nil {
			return nil
		}
	}

	// Handle {"tool_calls": [...]} format
	if tcRaw, ok := payload["tool_calls"]; ok {
		if tcArr, ok := tcRaw.([]interface{}); ok {
			var calls []ToolCall
			for _, item := range tcArr {
				itemMap, ok := item.(map[string]interface{})
				if !ok {
					continue
				}
				fnPayload := itemMap
				if fn, ok := itemMap["function"].(map[string]interface{}); ok {
					if _, hasName := fn["name"]; hasName {
						fnPayload = fn
					}
				}
				if call := extractCallFromPayload(fnPayload, allowedNames); call != nil {
					calls = append(calls, *call)
				}
			}
			return calls
		}
	}

	// Single object format
	if call := extractCallFromPayload(payload, allowedNames); call != nil {
		return []ToolCall{*call}
	}
	return nil
}

func extractCallFromPayload(payload map[string]interface{}, allowedNames []string) *ToolCall {
	name := extractNameFromPayload(payload)
	if name == "" {
		return nil
	}
	input := extractInputFromPayload(payload)
	return &ToolCall{
		Name:  NormalizeToolName(name, allowedNames),
		Input: input,
	}
}

// ────────────────────────────────────────────────────────────────────
// Format 3: TextKV parser
// ────────────────────────────────────────────────────────────────────

func parseTextKVFormat(text string, allowedNames []string) []ToolCall {
	var name string
	var arguments string

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, ":") {
			continue
		}
		idx := strings.Index(line, ":")
		key := strings.TrimSpace(strings.ToLower(line[:idx]))
		value := strings.TrimSpace(line[idx+1:])

		switch key {
		case "function.name":
			name = value
		case "function.arguments":
			arguments = value
		}
	}

	if name == "" {
		return nil
	}

	input := normalizeArguments(arguments)
	return []ToolCall{{
		Name:  NormalizeToolName(name, allowedNames),
		Input: input,
	}}
}

// ────────────────────────────────────────────────────────────────────
// Format 4: Bracket parser
// ────────────────────────────────────────────────────────────────────

var reBracketBlock = regexp.MustCompile(`(?s)\[function_calls\](.*?)\[/function_calls\]`)
var reBracketCall = regexp.MustCompile(`(?s)\[call:([^\]]+)\](.*?)\[/call\]`)

func parseBracketFormat(text string, allowedNames []string) []ToolCall {
	block := reBracketBlock.FindStringSubmatch(text)
	if block == nil {
		return nil
	}

	matches := reBracketCall.FindAllStringSubmatch(block[1], -1)
	if len(matches) == 0 {
		return nil
	}

	var calls []ToolCall
	for _, m := range matches {
		name := strings.TrimSpace(m[1])
		argsStr := strings.TrimSpace(m[2])
		input := normalizeArguments(argsStr)
		calls = append(calls, ToolCall{
			Name:  NormalizeToolName(name, allowedNames),
			Input: input,
		})
	}
	return calls
}

// ────────────────────────────────────────────────────────────────────
// Format 5: Anthropic parser (antml namespace)
// ────────────────────────────────────────────────────────────────────

var reAntmlBlock = regexp.MustCompile(`(?s)<antml:function_calls>(.*?)</antml:function_calls>`)
var reAntmlInvoke = regexp.MustCompile(`(?s)<antml:invoke\s+name="([^"]+)">(.*?)</antml:invoke>`)
var reAntmlParams = regexp.MustCompile(`(?s)<antml:parameters>(.*?)</antml:parameters>`)

func parseAnthropicFormat(text string, allowedNames []string) []ToolCall {
	block := reAntmlBlock.FindStringSubmatch(text)
	if block == nil {
		return nil
	}

	matches := reAntmlInvoke.FindAllStringSubmatch(block[1], -1)
	if len(matches) == 0 {
		return nil
	}

	var calls []ToolCall
	for _, m := range matches {
		name := strings.TrimSpace(m[1])
		invokeBody := m[2]

		input := map[string]interface{}{}
		if params := reAntmlParams.FindStringSubmatch(invokeBody); params != nil {
			input = normalizeArguments(strings.TrimSpace(params[1]))
		}

		calls = append(calls, ToolCall{
			Name:  NormalizeToolName(name, allowedNames),
			Input: input,
		})
	}
	return calls
}

// ────────────────────────────────────────────────────────────────────
// Format 6: Text marker parser (##TOOL_CALL##)
// ────────────────────────────────────────────────────────────────────

var reTextMarker = regexp.MustCompile(`(?i)##TOOL_CALL##\s*(.*?)\s*##END_CALL##`)

func parseTextMarkerFormat(text string, allowedNames []string) []ToolCall {
	m := reTextMarker.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(m[1]), &payload); err != nil {
		return nil
	}
	name := extractNameFromPayload(payload)
	if name == "" {
		return nil
	}
	input := extractInputFromPayload(payload)
	return []ToolCall{{
		Name:  NormalizeToolName(name, allowedNames),
		Input: input,
	}}
}

// ────────────────────────────────────────────────────────────────────
// Format 7: Code block parser (```tool_call)
// ────────────────────────────────────────────────────────────────────

var reCodeBlock = regexp.MustCompile("(?s)```tool_call\\s*\\n(.*?)\\n```")

func parseCodeBlockFormat(text string, allowedNames []string) []ToolCall {
	m := reCodeBlock.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(m[1]), &payload); err != nil {
		return nil
	}
	name := extractNameFromPayload(payload)
	if name == "" {
		return nil
	}
	input := extractInputFromPayload(payload)
	return []ToolCall{{
		Name:  NormalizeToolName(name, allowedNames),
		Input: input,
	}}
}
