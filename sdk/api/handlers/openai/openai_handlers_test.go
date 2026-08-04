package openai

import (
	"testing"
)

func TestMatchDrawingIntent(t *testing.T) {
	tests := []struct {
		name       string
		rawJSON    string
		wantPrompt string
		wantOk     bool
	}{
		{
			name:       "not matching user message",
			rawJSON:    `{"messages": [{"role": "system", "content": "You are a drawer."}]}`,
			wantPrompt: "",
			wantOk:     false,
		},
		{
			name:       "no messages",
			rawJSON:    `{"messages": []}`,
			wantPrompt: "",
			wantOk:     false,
		},
		{
			name:       "simple query not drawing",
			rawJSON:    `{"messages": [{"role": "user", "content": "Hello, how are you?"}]}`,
			wantPrompt: "",
			wantOk:     false,
		},
		{
			name:       "prefix 画个",
			rawJSON:    `{"messages": [{"role": "user", "content": "画个可爱的猫咪"}]}`,
			wantPrompt: "可爱的猫咪",
			wantOk:     true,
		},
		{
			name:       "prefix draw a",
			rawJSON:    `{"messages": [{"role": "user", "content": "draw a beautiful sunset"}]}`,
			wantPrompt: "beautiful sunset",
			wantOk:     true,
		},
		{
			name:       "regex matching generate image",
			rawJSON:    `{"messages": [{"role": "user", "content": "生成一张图片:美丽的花园"}]}`,
			wantPrompt: "美丽的花园",
			wantOk:     true,
		},
		{
			name:       "regex matching draw picture without separator",
			rawJSON:    `{"messages": [{"role": "user", "content": "绘制一幅画：太空飞船"}]}`,
			wantPrompt: "太空飞船",
			wantOk:     true,
		},
		{
			name:       "regex matching with empty prompt",
			rawJSON:    `{"messages": [{"role": "user", "content": "生成一个头像"}]}`,
			wantPrompt: "生成一个头像",
			wantOk:     true,
		},
		{
			name:       "multimodal text content matching",
			rawJSON:    `{"messages": [{"role": "user", "content": [{"type": "text", "text": "画个猫咪"}]}]}`,
			wantPrompt: "猫咪",
			wantOk:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPrompt, gotOk := matchDrawingIntent([]byte(tt.rawJSON))
			if gotOk != tt.wantOk {
				t.Errorf("matchDrawingIntent() ok = %v, wantOk = %v", gotOk, tt.wantOk)
			}
			if gotPrompt != tt.wantPrompt {
				t.Errorf("matchDrawingIntent() prompt = %q, wantPrompt = %q", gotPrompt, tt.wantPrompt)
			}
		})
	}
}
