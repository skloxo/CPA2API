package qwen

import (
	"testing"
)

func TestCleanRefusalText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "english refusal - I'm sorry",
			input:    "I'm sorry, I cannot help with that request.",
			expected: "[earlier assistant turn omitted by proxy]",
		},
		{
			name:     "english refusal - cannot help",
			input:    "I cannot help with that.",
			expected: "[earlier assistant turn omitted by proxy]",
		},
		{
			name:     "english refusal - tool does not exist",
			input:    "Tool Read does not exist in my capabilities.",
			expected: "[earlier assistant turn omitted by proxy]",
		},
		{
			name:     "chinese refusal - cannot call tool",
			input:    "我无法调用该工具",
			expected: "[earlier assistant turn omitted by proxy]",
		},
		{
			name:     "chinese refusal - tool not exist",
			input:    "工具不存在",
			expected: "[earlier assistant turn omitted by proxy]",
		},
		{
			name:     "normal text unchanged",
			input:    "The file contains 10 lines of code.",
			expected: "The file contains 10 lines of code.",
		},
		{
			name:     "empty input",
			input:    "",
			expected: "",
		},
		{
			name:     "mixed text with refusal at start",
			input:    "I'm sorry. Here is the result: ...",
			expected: "[earlier assistant turn omitted by proxy]",
		},
		{
			name:     "text with tool_use block preserved",
			input:    "Here is the tool call",
			expected: "Here is the tool call",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CleanRefusalText(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestIsRefusalText(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{"english sorry", "I'm sorry, I cannot help.", true},
		{"english tool missing", "Tool X does not exist", true},
		{"chinese refusal", "我无法调用该工具", true},
		{"chinese tool not exist", "工具不存在", true},
		{"normal text", "The answer is 42.", false},
		{"empty", "", false},
		{"tool call output", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRefusalText(tt.input)
			if result != tt.expect {
				t.Errorf("expected %v, got %v", tt.expect, result)
			}
		})
	}
}

func TestShouldCleanMessage(t *testing.T) {
	tests := []struct {
		name   string
		msg    map[string]interface{}
		expect bool
	}{
		{
			name:   "text message with refusal",
			msg:    map[string]interface{}{"role": "assistant", "content": "I'm sorry, I cannot help."},
			expect: true,
		},
		{
			name:   "text message without refusal",
			msg:    map[string]interface{}{"role": "assistant", "content": "Here is the answer."},
			expect: false,
		},
		{
			name:   "tool_use message",
			msg:    map[string]interface{}{"role": "assistant", "content": []interface{}{map[string]interface{}{"type": "tool_use"}}},
			expect: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ShouldCleanMessage(tt.msg)
			if result != tt.expect {
				t.Errorf("expected %v, got %v", tt.expect, result)
			}
		})
	}
}
