// Package qwen implements request and response translation between OpenAI and Qwen API formats.
//
// Qwen (Alibaba Cloud) API is largely OpenAI-compatible but has some differences:
//   - Model name mapping (e.g., "qwen3-max" ↔ "qwen-max")
//   - System message handling (merged into user messages)
//   - SSE event-based streaming format with "event:" and "data:" prefixes
//   - reasoning_content field for thinking/reasoning output
package qwen

import (
	translatorPkg "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/translator"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
)

func init() {
	translatorPkg.Register(
		"openai",
		"qwen",
		ConvertOpenAIRequestToQwen,
		sdktranslator.ResponseTransform{
			Stream:    ConvertQwenResponseToOpenAI,
			NonStream: ConvertQwenResponseToOpenAINonStream,
		},
	)
	translatorPkg.Register(
		"qwen",
		"openai",
		ConvertQwenRequestToOpenAI,
		sdktranslator.ResponseTransform{
			Stream:    ConvertOpenAIResponseToQwen,
			NonStream: ConvertOpenAIResponseToQwenNonStream,
		},
	)
}
