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
	return name
}

// FromQwenName converts a Qwen obfuscated name back to the original client name.
func FromQwenName(name string) string {
	if mapped, ok := reverseObfuscationMap[name]; ok {
		return mapped
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
		toolName)

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

	// Try stack-based repair first
	repaired := BulletproofRepairJSON(args)
	repaired = repairLooseJSON(repaired)
	if err := json.Unmarshal([]byte(repaired), &m); err == nil {
		b, _ := json.Marshal(m)
		return string(b)
	}

	// Try raw loose repair
	repairedLoose := repairLooseJSON(args)
	if err := json.Unmarshal([]byte(repairedLoose), &m); err == nil {
		b, _ := json.Marshal(m)
		return string(b)
	}

	// Return empty object as fallback
	return "{}"
}

// BulletproofRepairJSON repairs truncated/malformed JSON strings by balancing structures.
func BulletproofRepairJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "{}"
	}

	s = stripCodeFences(s)

	if !strings.HasPrefix(s, "{") {
		idx := strings.Index(s, "{")
		if idx >= 0 {
			s = s[idx:]
		} else {
			if strings.Contains(s, ":") {
				s = "{" + s + "}"
			} else {
				return "{}"
			}
		}
	}

	var stack []rune
	var inString bool
	var stringChar rune
	var escaped bool

	var clean []rune
	runes := []rune(s)

	for i := 0; i < len(runes); i++ {
		r := runes[i]

		if inString {
			if escaped {
				clean = append(clean, r)
				escaped = false
				continue
			}
			if r == '\\' {
				clean = append(clean, r)
				escaped = true
				continue
			}
			if r == stringChar {
				clean = append(clean, '"')
				inString = false
				continue
			}
			clean = append(clean, r)
			continue
		}

		if r == '"' || r == '\'' {
			inString = true
			stringChar = r
			clean = append(clean, '"')
			continue
		}

		if r == '{' {
			stack = append(stack, '{')
			clean = append(clean, '{')
			continue
		}
		if r == '[' {
			stack = append(stack, '[')
			clean = append(clean, '[')
			continue
		}
		if r == '}' {
			if len(stack) > 0 && stack[len(stack)-1] == '{' {
				stack = stack[:len(stack)-1]
				clean = append(clean, '}')
			}
			continue
		}
		if r == ']' {
			if len(stack) > 0 && stack[len(stack)-1] == '[' {
				stack = stack[:len(stack)-1]
				clean = append(clean, ']')
			}
			continue
		}

		if r == ',' {
			nextNonWs := rune(0)
			for j := i + 1; j < len(runes); j++ {
				if runes[j] != ' ' && runes[j] != '\t' && runes[j] != '\r' && runes[j] != '\n' {
					nextNonWs = runes[j]
					break
				}
			}
			if nextNonWs == '}' || nextNonWs == ']' {
				continue
			}
		}

		clean = append(clean, r)
	}

	if inString {
		clean = append(clean, '"')
	}

	cleanStr := strings.TrimRight(string(clean), " \t\r\n,")
	cleanRunes := []rune(cleanStr)

	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if top == '{' {
			cleanRunes = append(cleanRunes, '}')
		} else if top == '[' {
			cleanRunes = append(cleanRunes, ']')
		}
	}

	return string(cleanRunes)
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
	repaired := FixToolArgs(args)
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
	// Try exact match first
	for _, allowed := range allowedNames {
		if strings.EqualFold(name, allowed) {
			return allowed
		}
	}
	// Try matching after unobfuscating the Qwen name
	unobfuscated := FromQwenName(name)
	for _, allowed := range allowedNames {
		if strings.EqualFold(unobfuscated, allowed) {
			return allowed
		}
	}
	return name
}

// ────────────────────────────────────────────────────────────────────
// Format 1: XML parser
// ────────────────────────────────────────────────────────────────────

var reXMLToolCallsWrapper = regexp.MustCompile(`(?s)<tool_calls>(.*?)</tool_calls>`)
var reXMLToolCallSingle = regexp.MustCompile(`(?s)<tool_call\s+name=["']([^"']+)["']\s*>(.*?)</tool_call>`)
var reXMLToolCallBare = regexp.MustCompile(`(?s)<tool_call\s+name=["']([^"']+)["']\s*>(.*?)</tool_call>`)

func parseXMLFormat(text string, allowedNames []string) []ToolCall {
	// Try wrapped <tool_calls> first
	if m := reXMLToolCallsWrapper.FindStringSubmatch(text); m != nil {
		return parseXMLInner(m[1], allowedNames)
	}
	// Try bare tool_calls directly from the whole text
	return parseXMLInner(text, allowedNames)
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
		repaired := FixToolArgs(stripped)
		if repaired == stripped || repaired == "{}" {
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

// BuildToolSystemPrompt builds a standard system instructions prompt for tools injection.
func BuildToolSystemPrompt(tools []map[string]interface{}, toolChoice interface{}) string {
	var b strings.Builder
	b.WriteString("## Tools\n\n")
	b.WriteString("You can call a set of tools to perform actions or fetch information. ")
	b.WriteString("To call a tool, you MUST use the following standard `<tool_call>` XML format:\n")
	b.WriteString("```xml\n<tool_call name=\"ToolName\">\n")
	b.WriteString("{\n  \"arg1\": \"value1\"\n}\n")
	b.WriteString("</tool_call>\n```\n\n")
	b.WriteString("Here is the list of available tools in standard TypeScript/JSON definitions:\n\n")

	for _, tool := range tools {
		name, _ := tool["name"].(string)
		if name == "" {
			// Check under "function" nested struct
			if fn, ok := tool["function"].(map[string]interface{}); ok {
				name, _ = fn["name"].(string)
			}
		}
		if name == "" {
			continue
		}

		qwenName := ToQwenName(name)
		desc := ""
		if fn, ok := tool["function"].(map[string]interface{}); ok {
			desc, _ = fn["description"].(string)
		} else {
			desc, _ = tool["description"].(string)
		}

		b.WriteString(fmt.Sprintf("### Tool: %s\n", qwenName))
		if desc != "" {
			b.WriteString(fmt.Sprintf("Description: %s\n", desc))
		}

		var params map[string]interface{}
		if fn, ok := tool["function"].(map[string]interface{}); ok {
			if p, ok := fn["parameters"].(map[string]interface{}); ok {
				params = p
			}
		} else if p, ok := tool["parameters"].(map[string]interface{}); ok {
			params = p
		}

		if len(params) > 0 {
			b.WriteString("Parameters (JSON Schema):\n")
			pBytes, err := json.MarshalIndent(params, "", "  ")
			if err == nil {
				b.WriteString("```json\n")
				b.WriteString(string(pBytes))
				b.WriteString("\n```\n")
			}
		}
		b.WriteString("\n")
	}

	b.WriteString("## Tool Execution Rules\n")
	b.WriteString("1. You must only invoke tools that are listed above.\n")
	b.WriteString("2. You must strictly pass arguments conforming to the JSON Schema definitions.\n")
	b.WriteString("3. DO NOT hallucinate tools, parameters, or values. ")
	b.WriteString("4. Claims about the tool being unavailable or missing are strictly forbidden.\n")

	if toolChoice != nil {
		if choiceStr, ok := toolChoice.(string); ok && choiceStr == "required" {
			b.WriteString("- You MUST call a tool in your response.\n")
		} else if choiceMap, ok := toolChoice.(map[string]interface{}); ok {
			if fn, ok := choiceMap["function"].(map[string]interface{}); ok {
				if name, ok := fn["name"].(string); ok && name != "" {
					b.WriteString(fmt.Sprintf("- You MUST call the tool `%s` first.\n", ToQwenName(name)))
				}
			}
		}
	}

	return b.String()
}

// GetJSONRepairSuffix scans the string to track open quotes, braces '{', and brackets '[' (accounting for backslash escape characters).
// If the last non-whitespace character in the trimmed string is a comma, it appends a dummy repair pair "__fixed":true (if the top stack is '{') or null (if the top stack is '[') to avoid trailing comma parsing errors.
// Then, it closes all remaining open braces/brackets from the stack, and returns the computed suffix string.
func GetJSONRepairSuffix(s string) string {
	var stack []rune
	var inString bool
	var stringChar rune
	var escaped bool

	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == stringChar {
				inString = false
				continue
			}
			continue
		}

		if r == '"' || r == '\'' {
			inString = true
			stringChar = r
			continue
		}

		if r == '{' {
			stack = append(stack, '{')
		} else if r == '[' {
			stack = append(stack, '[')
		} else if r == '}' {
			if len(stack) > 0 && stack[len(stack)-1] == '{' {
				stack = stack[:len(stack)-1]
			}
		} else if r == ']' {
			if len(stack) > 0 && stack[len(stack)-1] == '[' {
				stack = stack[:len(stack)-1]
			}
		}
	}

	var suffix strings.Builder
	if inString {
		suffix.WriteRune(stringChar)
	}

	combined := s + suffix.String()
	trimmed := strings.TrimRight(combined, " \t\r\n")
	if len(trimmed) > 0 && trimmed[len(trimmed)-1] == ',' {
		if len(stack) > 0 {
			top := stack[len(stack)-1]
			if top == '{' {
				suffix.WriteString(`"__fixed":true`)
			} else if top == '[' {
				suffix.WriteString(`null`)
			}
		}
	}

	for i := len(stack) - 1; i >= 0; i-- {
		top := stack[i]
		if top == '{' {
			suffix.WriteRune('}')
		} else if top == '[' {
			suffix.WriteRune(']')
		}
	}

	return suffix.String()
}

// IncrementalToolCall represents a parsed incremental tool call.
type IncrementalToolCall struct {
	Index      int
	Name       string
	Arguments  string
	IsComplete bool
}

// ParseIncrementalToolCalls scans the accumulated string to find all <tool_call ...> blocks,
// including incomplete ones, and tracks their bounds and extracts their parameters.
func ParseIncrementalToolCalls(accumulated string, allowedNames []string) []IncrementalToolCall {
	var toolCalls []IncrementalToolCall
	pos := 0
	index := 0

	for {
		if pos >= len(accumulated) {
			break
		}
		startIdx := strings.Index(accumulated[pos:], "<tool_call")
		if startIdx == -1 {
			break
		}
		absoluteStartIdx := pos + startIdx

		// Skip the wrapper <tool_calls> tag if matched
		if len(accumulated) > absoluteStartIdx+len("<tool_call") && accumulated[absoluteStartIdx+len("<tool_call")] == 's' {
			pos = absoluteStartIdx + len("<tool_calls")
			continue
		}

		// Find the closing '>' of the opening tag
		openTagEndIdx := strings.Index(accumulated[absoluteStartIdx:], ">")
		if openTagEndIdx == -1 {
			// Opening tag is incomplete, we can't extract arguments yet
			break
		}
		absoluteOpenTagEndIdx := absoluteStartIdx + openTagEndIdx

		// Extract the tag content to get the name attribute
		tagContent := accumulated[absoluteStartIdx : absoluteOpenTagEndIdx+1]
		name := ""
		nameStart := strings.Index(tagContent, "name=")
		if nameStart != -1 {
			quoteChar := byte(0)
			quoteStart := -1
			for i := nameStart + 5; i < len(tagContent); i++ {
				if tagContent[i] == '"' || tagContent[i] == '\'' {
					quoteChar = tagContent[i]
					quoteStart = i
					break
				}
			}
			if quoteStart != -1 {
				quoteEnd := strings.IndexByte(tagContent[quoteStart+1:], quoteChar)
				if quoteEnd != -1 {
					name = tagContent[quoteStart+1 : quoteStart+1+quoteEnd]
				}
			}
		}

		name = FromQwenName(name)
		name = NormalizeToolName(name, allowedNames)

		argsStartIdx := absoluteOpenTagEndIdx + 1
		endIdx := len(accumulated)
		isComplete := false

		closeTagIdx := strings.Index(accumulated[argsStartIdx:], "</")
		if closeTagIdx != -1 {
			absoluteCloseTagIdx := argsStartIdx + closeTagIdx
			endIdx = absoluteCloseTagIdx
			restOfTag := accumulated[absoluteCloseTagIdx:]
			if strings.HasPrefix(restOfTag, "</tool_call>") || strings.HasPrefix(restOfTag, "</tool_calls>") {
				isComplete = true
			}
		} else {
			// Check if there is another `<tool_call` ahead (making sure to skip `<tool_calls>` wrapper)
			nextIdx := argsStartIdx
			for {
				nextTCIdx := strings.Index(accumulated[nextIdx:], "<tool_call")
				if nextTCIdx == -1 {
					break
				}
				absNextTCIdx := nextIdx + nextTCIdx
				// Check if this is `<tool_calls>`
				if len(accumulated) > absNextTCIdx+len("<tool_call") && accumulated[absNextTCIdx+len("<tool_call")] == 's' {
					nextIdx = absNextTCIdx + len("<tool_calls")
					continue
				}
				// It's a real next `<tool_call`, so truncate arguments here and mark as complete
				endIdx = absNextTCIdx
				isComplete = true
				break
			}
		}

		arguments := accumulated[argsStartIdx:endIdx]

		toolCalls = append(toolCalls, IncrementalToolCall{
			Index:      index,
			Name:       name,
			Arguments:  arguments,
			IsComplete: isComplete,
		})

		index++
		if isComplete && closeTagIdx != -1 {
			restOfTag := accumulated[argsStartIdx+closeTagIdx:]
			if strings.HasPrefix(restOfTag, "</tool_calls>") {
				pos = argsStartIdx + closeTagIdx + len("</tool_calls>")
			} else {
				pos = argsStartIdx + closeTagIdx + len("</tool_call>")
			}
		} else {
			pos = endIdx
			if pos <= absoluteOpenTagEndIdx {
				pos = absoluteOpenTagEndIdx + 1
			}
		}
	}

	return toolCalls
}
