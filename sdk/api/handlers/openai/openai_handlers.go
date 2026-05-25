// Package openai provides HTTP handlers for OpenAI API endpoints.
// This package implements the OpenAI-compatible API interface, including model listing
// and chat completion functionality. It supports both streaming and non-streaming responses,
// and manages a pool of clients to interact with backend services.
// The handlers translate OpenAI API requests to the appropriate backend format and
// convert responses back to OpenAI-compatible format.
package openai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	qwenauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/qwen"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"

	"github.com/gin-gonic/gin"
	. "github.com/router-for-me/CLIProxyAPI/v7/internal/constant"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	responsesconverter "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/openai/openai/responses"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// OpenAIAPIHandler contains the handlers for OpenAI API endpoints.
// It holds a pool of clients to interact with the backend service.
type OpenAIAPIHandler struct {
	*handlers.BaseAPIHandler
}

// NewOpenAIAPIHandler creates a new OpenAI API handlers instance.
// It takes an BaseAPIHandler instance as input and returns an OpenAIAPIHandler.
//
// Parameters:
//   - apiHandlers: The base API handlers instance
//
// Returns:
//   - *OpenAIAPIHandler: A new OpenAI API handlers instance
func NewOpenAIAPIHandler(apiHandlers *handlers.BaseAPIHandler) *OpenAIAPIHandler {
	return &OpenAIAPIHandler{
		BaseAPIHandler: apiHandlers,
	}
}

// HandlerType returns the identifier for this handler implementation.
func (h *OpenAIAPIHandler) HandlerType() string {
	return OpenAI
}

// Models returns the OpenAI-compatible model metadata supported by this handler.
func (h *OpenAIAPIHandler) Models() []map[string]any {
	// Get dynamic models from the global registry
	modelRegistry := registry.GetGlobalRegistry()
	return modelRegistry.GetAvailableModels("openai")
}

// OpenAIModels handles the /v1/models endpoint.
// It returns a list of available AI models with their capabilities
// and specifications in OpenAI-compatible format.
func (h *OpenAIAPIHandler) OpenAIModels(c *gin.Context) {
	if _, ok := c.Request.URL.Query()["client_version"]; ok {
		c.JSON(http.StatusOK, h.codexClientModelsResponse())
		return
	}

	// Get all available models
	allModels := h.Models()

	// Filter to only include the 4 required fields: id, object, created, owned_by
	filteredModels := make([]map[string]any, len(allModels))
	for i, model := range allModels {
		filteredModel := map[string]any{
			"id":     model["id"],
			"object": model["object"],
		}

		// Add created field if it exists
		if created, exists := model["created"]; exists {
			filteredModel["created"] = created
		}

		// Add owned_by field if it exists
		if ownedBy, exists := model["owned_by"]; exists {
			filteredModel["owned_by"] = ownedBy
		}

		filteredModels[i] = filteredModel
	}

	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   filteredModels,
	})
}

// ChatCompletions handles the /v1/chat/completions endpoint.
// It determines whether the request is for a streaming or non-streaming response
// and calls the appropriate handler based on the model provider.
//
// Parameters:
//   - c: The Gin context containing the HTTP request and response
func (h *OpenAIAPIHandler) ChatCompletions(c *gin.Context) {
	rawJSON, err := handlers.ReadRequestBody(c)
	// If data retrieval fails, return a 400 Bad Request error.
	if err != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
			},
		})
		return
	}

	// Check if the client requested a streaming response.
	streamResult := gjson.GetBytes(rawJSON, "stream")
	stream := streamResult.Type == gjson.True

	// Some clients send OpenAI Responses-format payloads to /v1/chat/completions.
	// Convert them to Chat Completions so downstream translators preserve tool metadata.
	if shouldTreatAsResponsesFormat(rawJSON) {
		modelName := gjson.GetBytes(rawJSON, "model").String()
		rawJSON = responsesconverter.ConvertOpenAIResponsesRequestToOpenAIChatCompletions(modelName, rawJSON, stream)
		stream = gjson.GetBytes(rawJSON, "stream").Bool()
	}

	// Drawing prompt interception
	if prompt, ok := matchDrawingIntent(rawJSON); ok {
		if h.handleDrawingInterception(c, prompt, stream) {
			return
		}
	}

	if stream {
		h.handleStreamingResponse(c, rawJSON)
	} else {
		h.handleNonStreamingResponse(c, rawJSON)
	}

}

// shouldTreatAsResponsesFormat detects OpenAI Responses-style payloads that are
// accidentally sent to the Chat Completions endpoint.
func shouldTreatAsResponsesFormat(rawJSON []byte) bool {
	if gjson.GetBytes(rawJSON, "messages").Exists() {
		return false
	}
	if gjson.GetBytes(rawJSON, "input").Exists() {
		return true
	}
	if gjson.GetBytes(rawJSON, "instructions").Exists() {
		return true
	}
	return false
}

// Completions handles the /v1/completions endpoint.
// It determines whether the request is for a streaming or non-streaming response
// and calls the appropriate handler based on the model provider.
// This endpoint follows the OpenAI completions API specification.
//
// Parameters:
//   - c: The Gin context containing the HTTP request and response
func (h *OpenAIAPIHandler) Completions(c *gin.Context) {
	rawJSON, err := handlers.ReadRequestBody(c)
	// If data retrieval fails, return a 400 Bad Request error.
	if err != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
			},
		})
		return
	}

	// Check if the client requested a streaming response.
	streamResult := gjson.GetBytes(rawJSON, "stream")
	if streamResult.Type == gjson.True {
		h.handleCompletionsStreamingResponse(c, rawJSON)
	} else {
		h.handleCompletionsNonStreamingResponse(c, rawJSON)
	}

}

// convertCompletionsRequestToChatCompletions converts OpenAI completions API request to chat completions format.
// This allows the completions endpoint to use the existing chat completions infrastructure.
//
// Parameters:
//   - rawJSON: The raw JSON bytes of the completions request
//
// Returns:
//   - []byte: The converted chat completions request
func convertCompletionsRequestToChatCompletions(rawJSON []byte) []byte {
	root := gjson.ParseBytes(rawJSON)

	// Extract prompt from completions request
	prompt := root.Get("prompt").String()
	if prompt == "" {
		prompt = "Complete this:"
	}

	// Create chat completions structure
	out := []byte(`{"model":"","messages":[{"role":"user","content":""}]}`)

	// Set model
	if model := root.Get("model"); model.Exists() {
		out, _ = sjson.SetBytes(out, "model", model.String())
	}

	// Set the prompt as user message content
	out, _ = sjson.SetBytes(out, "messages.0.content", prompt)

	// Copy other parameters from completions to chat completions
	if maxTokens := root.Get("max_tokens"); maxTokens.Exists() {
		out, _ = sjson.SetBytes(out, "max_tokens", maxTokens.Int())
	}

	if temperature := root.Get("temperature"); temperature.Exists() {
		out, _ = sjson.SetBytes(out, "temperature", temperature.Float())
	}

	if topP := root.Get("top_p"); topP.Exists() {
		out, _ = sjson.SetBytes(out, "top_p", topP.Float())
	}

	if frequencyPenalty := root.Get("frequency_penalty"); frequencyPenalty.Exists() {
		out, _ = sjson.SetBytes(out, "frequency_penalty", frequencyPenalty.Float())
	}

	if presencePenalty := root.Get("presence_penalty"); presencePenalty.Exists() {
		out, _ = sjson.SetBytes(out, "presence_penalty", presencePenalty.Float())
	}

	if stop := root.Get("stop"); stop.Exists() {
		out, _ = sjson.SetRawBytes(out, "stop", []byte(stop.Raw))
	}

	if stream := root.Get("stream"); stream.Exists() {
		out, _ = sjson.SetBytes(out, "stream", stream.Bool())
	}

	if logprobs := root.Get("logprobs"); logprobs.Exists() {
		out, _ = sjson.SetBytes(out, "logprobs", logprobs.Bool())
	}

	if topLogprobs := root.Get("top_logprobs"); topLogprobs.Exists() {
		out, _ = sjson.SetBytes(out, "top_logprobs", topLogprobs.Int())
	}

	if echo := root.Get("echo"); echo.Exists() {
		out, _ = sjson.SetBytes(out, "echo", echo.Bool())
	}

	return out
}

// convertChatCompletionsResponseToCompletions converts chat completions API response back to completions format.
// This ensures the completions endpoint returns data in the expected format.
//
// Parameters:
//   - rawJSON: The raw JSON bytes of the chat completions response
//
// Returns:
//   - []byte: The converted completions response
func convertChatCompletionsResponseToCompletions(rawJSON []byte) []byte {
	root := gjson.ParseBytes(rawJSON)

	// Base completions response structure
	out := []byte(`{"id":"","object":"text_completion","created":0,"model":"","choices":[]}`)

	// Copy basic fields
	if id := root.Get("id"); id.Exists() {
		out, _ = sjson.SetBytes(out, "id", id.String())
	}

	if created := root.Get("created"); created.Exists() {
		out, _ = sjson.SetBytes(out, "created", created.Int())
	}

	if model := root.Get("model"); model.Exists() {
		out, _ = sjson.SetBytes(out, "model", model.String())
	}

	if usage := root.Get("usage"); usage.Exists() {
		out, _ = sjson.SetRawBytes(out, "usage", []byte(usage.Raw))
	}

	// Convert choices from chat completions to completions format
	var choices []interface{}
	if chatChoices := root.Get("choices"); chatChoices.Exists() && chatChoices.IsArray() {
		chatChoices.ForEach(func(_, choice gjson.Result) bool {
			completionsChoice := map[string]interface{}{
				"index": choice.Get("index").Int(),
			}

			// Extract text content from message.content
			if message := choice.Get("message"); message.Exists() {
				if content := message.Get("content"); content.Exists() {
					completionsChoice["text"] = content.String()
				}
			} else if delta := choice.Get("delta"); delta.Exists() {
				// For streaming responses, use delta.content
				if content := delta.Get("content"); content.Exists() {
					completionsChoice["text"] = content.String()
				}
			}

			// Copy finish_reason
			if finishReason := choice.Get("finish_reason"); finishReason.Exists() {
				completionsChoice["finish_reason"] = finishReason.String()
			}

			// Copy logprobs if present
			if logprobs := choice.Get("logprobs"); logprobs.Exists() {
				completionsChoice["logprobs"] = logprobs.Value()
			}

			choices = append(choices, completionsChoice)
			return true
		})
	}

	if len(choices) > 0 {
		choicesJSON, _ := json.Marshal(choices)
		out, _ = sjson.SetRawBytes(out, "choices", choicesJSON)
	}

	return out
}

// convertChatCompletionsStreamChunkToCompletions converts a streaming chat completions chunk to completions format.
// This handles the real-time conversion of streaming response chunks and filters out empty text responses.
//
// Parameters:
//   - chunkData: The raw JSON bytes of a single chat completions stream chunk
//
// Returns:
//   - []byte: The converted completions stream chunk, or nil if should be filtered out
func convertChatCompletionsStreamChunkToCompletions(chunkData []byte) []byte {
	root := gjson.ParseBytes(chunkData)

	// Check if this chunk has any meaningful content
	hasContent := false
	hasUsage := root.Get("usage").Exists()
	if chatChoices := root.Get("choices"); chatChoices.Exists() && chatChoices.IsArray() {
		chatChoices.ForEach(func(_, choice gjson.Result) bool {
			// Check if delta has content or finish_reason
			if delta := choice.Get("delta"); delta.Exists() {
				if content := delta.Get("content"); content.Exists() && content.String() != "" {
					hasContent = true
					return false // Break out of forEach
				}
			}
			// Also check for finish_reason to ensure we don't skip final chunks
			if finishReason := choice.Get("finish_reason"); finishReason.Exists() && finishReason.String() != "" && finishReason.String() != "null" {
				hasContent = true
				return false // Break out of forEach
			}
			return true
		})
	}

	// If no meaningful content and no usage, return nil to indicate this chunk should be skipped
	if !hasContent && !hasUsage {
		return nil
	}

	// Base completions stream response structure
	out := []byte(`{"id":"","object":"text_completion","created":0,"model":"","choices":[]}`)

	// Copy basic fields
	if id := root.Get("id"); id.Exists() {
		out, _ = sjson.SetBytes(out, "id", id.String())
	}

	if created := root.Get("created"); created.Exists() {
		out, _ = sjson.SetBytes(out, "created", created.Int())
	}

	if model := root.Get("model"); model.Exists() {
		out, _ = sjson.SetBytes(out, "model", model.String())
	}

	// Convert choices from chat completions delta to completions format
	var choices []interface{}
	if chatChoices := root.Get("choices"); chatChoices.Exists() && chatChoices.IsArray() {
		chatChoices.ForEach(func(_, choice gjson.Result) bool {
			completionsChoice := map[string]interface{}{
				"index": choice.Get("index").Int(),
			}

			// Extract text content from delta.content
			if delta := choice.Get("delta"); delta.Exists() {
				if content := delta.Get("content"); content.Exists() && content.String() != "" {
					completionsChoice["text"] = content.String()
				} else {
					completionsChoice["text"] = ""
				}
			} else {
				completionsChoice["text"] = ""
			}

			// Copy finish_reason
			if finishReason := choice.Get("finish_reason"); finishReason.Exists() && finishReason.String() != "null" {
				completionsChoice["finish_reason"] = finishReason.String()
			}

			// Copy logprobs if present
			if logprobs := choice.Get("logprobs"); logprobs.Exists() {
				completionsChoice["logprobs"] = logprobs.Value()
			}

			choices = append(choices, completionsChoice)
			return true
		})
	}

	if len(choices) > 0 {
		choicesJSON, _ := json.Marshal(choices)
		out, _ = sjson.SetRawBytes(out, "choices", choicesJSON)
	}

	// Copy usage if present
	if usage := root.Get("usage"); usage.Exists() {
		out, _ = sjson.SetRawBytes(out, "usage", []byte(usage.Raw))
	}

	return out
}

// handleNonStreamingResponse handles non-streaming chat completion responses
// for Gemini models. It selects a client from the pool, sends the request, and
// aggregates the response before sending it back to the client in OpenAI format.
//
// Parameters:
//   - c: The Gin context containing the HTTP request and response
//   - rawJSON: The raw JSON bytes of the OpenAI-compatible request
func (h *OpenAIAPIHandler) handleNonStreamingResponse(c *gin.Context, rawJSON []byte) {
	c.Header("Content-Type", "application/json")

	modelName := gjson.GetBytes(rawJSON, "model").String()
	cliCtx, cliCancel := h.GetContextWithCancel(h, c, context.Background())
	resp, upstreamHeaders, errMsg := h.ExecuteWithAuthManager(cliCtx, h.HandlerType(), modelName, rawJSON, h.GetAlt(c))
	if errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		cliCancel(errMsg.Error)
		return
	}
	handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)
	_, _ = c.Writer.Write(resp)
	cliCancel()
}

// handleStreamingResponse handles streaming responses for Gemini models.
// It establishes a streaming connection with the backend service and forwards
// the response chunks to the client in real-time using Server-Sent Events.
//
// Parameters:
//   - c: The Gin context containing the HTTP request and response
//   - rawJSON: The raw JSON bytes of the OpenAI-compatible request
func (h *OpenAIAPIHandler) handleStreamingResponse(c *gin.Context, rawJSON []byte) {
	// Get the http.Flusher interface to manually flush the response.
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "Streaming not supported",
				Type:    "server_error",
			},
		})
		return
	}

	modelName := gjson.GetBytes(rawJSON, "model").String()
	cliCtx, cliCancel := h.GetContextWithCancel(h, c, context.Background())
	dataChan, upstreamHeaders, errChan := h.ExecuteStreamWithAuthManager(cliCtx, h.HandlerType(), modelName, rawJSON, h.GetAlt(c))

	setSSEHeaders := func() {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("Access-Control-Allow-Origin", "*")
	}

	// Peek at the first chunk to determine success or failure before setting headers
	for {
		select {
		case <-c.Request.Context().Done():
			cliCancel(c.Request.Context().Err())
			return
		case errMsg, ok := <-errChan:
			if !ok {
				// Err channel closed cleanly; wait for data channel.
				errChan = nil
				continue
			}
			// Upstream failed immediately. Return proper error status and JSON.
			h.WriteErrorResponse(c, errMsg)
			if errMsg != nil {
				cliCancel(errMsg.Error)
			} else {
				cliCancel(nil)
			}
			return
		case chunk, ok := <-dataChan:
			if !ok {
				// Stream closed without data? Send DONE or just headers.
				setSSEHeaders()
				handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)
				_, _ = fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
				flusher.Flush()
				cliCancel(nil)
				return
			}

			// Success! Commit to streaming headers.
			setSSEHeaders()
			handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)

			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunk))
			flusher.Flush()

			// Continue streaming the rest
			h.handleStreamResult(c, flusher, func(err error) { cliCancel(err) }, dataChan, errChan)
			return
		}
	}
}

// handleCompletionsNonStreamingResponse handles non-streaming completions responses.
// It converts completions request to chat completions format, sends to backend,
// then converts the response back to completions format before sending to client.
//
// Parameters:
//   - c: The Gin context containing the HTTP request and response
//   - rawJSON: The raw JSON bytes of the OpenAI-compatible completions request
func (h *OpenAIAPIHandler) handleCompletionsNonStreamingResponse(c *gin.Context, rawJSON []byte) {
	c.Header("Content-Type", "application/json")

	// Convert completions request to chat completions format
	chatCompletionsJSON := convertCompletionsRequestToChatCompletions(rawJSON)

	modelName := gjson.GetBytes(chatCompletionsJSON, "model").String()
	cliCtx, cliCancel := h.GetContextWithCancel(h, c, context.Background())
	stopKeepAlive := h.StartNonStreamingKeepAlive(c, cliCtx)
	resp, upstreamHeaders, errMsg := h.ExecuteWithAuthManager(cliCtx, h.HandlerType(), modelName, chatCompletionsJSON, "")
	stopKeepAlive()
	if errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		cliCancel(errMsg.Error)
		return
	}
	handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)
	completionsResp := convertChatCompletionsResponseToCompletions(resp)
	_, _ = c.Writer.Write(completionsResp)
	cliCancel()
}

// handleCompletionsStreamingResponse handles streaming completions responses.
// It converts completions request to chat completions format, streams from backend,
// then converts each response chunk back to completions format before sending to client.
//
// Parameters:
//   - c: The Gin context containing the HTTP request and response
//   - rawJSON: The raw JSON bytes of the OpenAI-compatible completions request
func (h *OpenAIAPIHandler) handleCompletionsStreamingResponse(c *gin.Context, rawJSON []byte) {
	// Get the http.Flusher interface to manually flush the response.
	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "Streaming not supported",
				Type:    "server_error",
			},
		})
		return
	}

	// Convert completions request to chat completions format
	chatCompletionsJSON := convertCompletionsRequestToChatCompletions(rawJSON)

	modelName := gjson.GetBytes(chatCompletionsJSON, "model").String()
	cliCtx, cliCancel := h.GetContextWithCancel(h, c, context.Background())
	dataChan, upstreamHeaders, errChan := h.ExecuteStreamWithAuthManager(cliCtx, h.HandlerType(), modelName, chatCompletionsJSON, "")

	setSSEHeaders := func() {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("Access-Control-Allow-Origin", "*")
	}

	// Peek at the first chunk
	for {
		select {
		case <-c.Request.Context().Done():
			cliCancel(c.Request.Context().Err())
			return
		case errMsg, ok := <-errChan:
			if !ok {
				// Err channel closed cleanly; wait for data channel.
				errChan = nil
				continue
			}
			h.WriteErrorResponse(c, errMsg)
			if errMsg != nil {
				cliCancel(errMsg.Error)
			} else {
				cliCancel(nil)
			}
			return
		case chunk, ok := <-dataChan:
			if !ok {
				setSSEHeaders()
				handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)
				_, _ = fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
				flusher.Flush()
				cliCancel(nil)
				return
			}

			// Success! Set headers.
			setSSEHeaders()
			handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)

			// Write the first chunk
			converted := convertChatCompletionsStreamChunkToCompletions(chunk)
			if converted != nil {
				_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(converted))
				flusher.Flush()
			}

			done := make(chan struct{})
			var doneOnce sync.Once
			stop := func() { doneOnce.Do(func() { close(done) }) }

			convertedChan := make(chan []byte)
			go func() {
				defer close(convertedChan)
				for {
					select {
					case <-done:
						return
					case chunk, ok := <-dataChan:
						if !ok {
							return
						}
						converted := convertChatCompletionsStreamChunkToCompletions(chunk)
						if converted == nil {
							continue
						}
						select {
						case <-done:
							return
						case convertedChan <- converted:
						}
					}
				}
			}()

			h.handleStreamResult(c, flusher, func(err error) {
				stop()
				cliCancel(err)
			}, convertedChan, errChan)
			return
		}
	}
}
func (h *OpenAIAPIHandler) handleStreamResult(c *gin.Context, flusher http.Flusher, cancel func(error), data <-chan []byte, errs <-chan *interfaces.ErrorMessage) {
	h.ForwardStream(c, flusher, cancel, data, errs, handlers.StreamForwardOptions{
		WriteChunk: func(chunk []byte) {
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(chunk))
		},
		WriteTerminalError: func(errMsg *interfaces.ErrorMessage) {
			if errMsg == nil {
				return
			}
			status := http.StatusInternalServerError
			if errMsg.StatusCode > 0 {
				status = errMsg.StatusCode
			}
			errText := http.StatusText(status)
			if errMsg.Error != nil && errMsg.Error.Error() != "" {
				errText = errMsg.Error.Error()
			}
			body := handlers.BuildErrorResponseBody(status, errText)
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", string(body))
		},
		WriteDone: func() {
			_, _ = fmt.Fprint(c.Writer, "data: [DONE]\n\n")
		},
	})
}

// matchDrawingIntent checks if the request triggers drawing.
func matchDrawingIntent(rawJSON []byte) (string, bool) {
	messagesResult := gjson.GetBytes(rawJSON, "messages")
	if !messagesResult.IsArray() {
		return "", false
	}
	arr := messagesResult.Array()
	if len(arr) == 0 {
		return "", false
	}
	lastMsg := arr[len(arr)-1]
	if lastMsg.Get("role").String() != "user" {
		return "", false
	}

	var textContent string
	contentVal := lastMsg.Get("content")
	if contentVal.Type == gjson.String {
		textContent = contentVal.String()
	} else if contentVal.IsArray() {
		for _, part := range contentVal.Array() {
			if part.Get("type").String() == "text" {
				textContent = part.Get("text").String()
				break
			}
		}
	}

	textContent = strings.TrimSpace(textContent)
	if textContent == "" {
		return "", false
	}

	// 1. Drawing prefixes
	if strings.HasPrefix(textContent, "画个") {
		prompt := strings.TrimSpace(strings.TrimPrefix(textContent, "画个"))
		if prompt != "" {
			return prompt, true
		}
	}
	if strings.HasPrefix(strings.ToLower(textContent), "draw a ") {
		prompt := strings.TrimSpace(textContent[7:])
		if prompt != "" {
			return prompt, true
		}
	}

	// 2. Regex match
	drawingRegex := regexp.MustCompile(`(?i)^(?:画|绘制|生成|设计|创建|创作|draw|paint|generate|create|make)\s*(?:一幅|一个|一只|一张|一些|a|an|the)?\s*(?:图片|图|画|插画|头像|照片|海报|image|picture|photo|illustration|painting|drawing|poster|avatar)\s*[:：]?(.*)$`)
	matches := drawingRegex.FindStringSubmatch(textContent)
	if len(matches) > 1 {
		prompt := strings.TrimSpace(matches[1])
		if prompt != "" {
			return prompt, true
		}
		return textContent, true
	}

	return "", false
}

// getActiveQwenAuth searches for an active, enabled Qwen auth configuration.
func (h *OpenAIAPIHandler) getActiveQwenAuth() *cliproxyauth.Auth {
	for _, a := range h.AuthManager.List() {
		if strings.EqualFold(a.Provider, "qwen") && a.Status == cliproxyauth.StatusActive && !a.Disabled && !a.Unavailable {
			return a
		}
	}
	return nil
}

// handleDrawingInterception executes Qwen drawing and returns the formatted response.
func (h *OpenAIAPIHandler) handleDrawingInterception(c *gin.Context, prompt string, stream bool) bool {
	auth := h.getActiveQwenAuth()
	if auth == nil {
		log.Warn("Drawing interception: no active Qwen credential available")
		c.JSON(http.StatusServiceUnavailable, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "Drawing service temporarily unavailable: no active Qwen credential",
				Type:    "server_error",
			},
		})
		return true
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	contentURL, err := h.generateImageQwen(ctx, auth, prompt)
	if err != nil {
		log.Errorf("Drawing interception: failed to generate image: %v", err)
		c.JSON(http.StatusInternalServerError, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Failed to generate image: %v", err),
				Type:    "server_error",
			},
		})
		return true
	}

	markdownLink := fmt.Sprintf("![Generated Image](%s)", contentURL)
	modelName := "qwen-vl-max"

	if stream {
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("Access-Control-Allow-Origin", "*")

		flusher, ok := c.Writer.(http.Flusher)
		if !ok {
			c.String(http.StatusOK, "data: %s\n\n", markdownLink)
			return true
		}

		id := fmt.Sprintf("chatcmpl-draw-%d", time.Now().UnixNano())

		// Send first chunk (role)
		sendStreamChunk(c.Writer, flusher, id, modelName, "", nil)

		// Send content characters gradually
		runes := []rune(markdownLink)
		chunkSize := 4
		for i := 0; i < len(runes); i += chunkSize {
			end := i + chunkSize
			if end > len(runes) {
				end = len(runes)
			}
			sendStreamChunk(c.Writer, flusher, id, modelName, string(runes[i:end]), nil)
			time.Sleep(30 * time.Millisecond)
		}

		// Send final stop chunk
		sendStreamChunk(c.Writer, flusher, id, modelName, "", "stop")

		// Send [DONE]
		_, _ = fmt.Fprint(c.Writer, "data: [DONE]\n\n")
		flusher.Flush()
	} else {
		id := fmt.Sprintf("chatcmpl-draw-%d", time.Now().UnixNano())
		sendNonStreamResponse(c, id, modelName, markdownLink)
	}

	return true
}

func (h *OpenAIAPIHandler) generateImageQwen(ctx context.Context, auth *cliproxyauth.Auth, prompt string) (string, error) {
	token, cookie := qwenCredsFromAuth(auth)

	chatID, err := h.qwenGenerateChatID(ctx, auth, token, cookie)
	if err != nil {
		return "", fmt.Errorf("generate chat_id: %w", err)
	}

	reqBody := buildQwenImageRequest(chatID, "qwen-vl-max", prompt, "1024x1024", false)
	url := qwenauth.QwenAPIBaseURL + "/api/v2/chat/completions?chat_id=" + chatID

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(reqBody)))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	qwenauth.ApplyAllQwenHeaders(httpReq, token, cookie, true)

	var helpsCfg *config.Config
	if h.Cfg != nil {
		helpsCfg = &config.Config{
			SDKConfig: *h.Cfg,
		}
	}
	httpClient := helps.NewProxyAwareHTTPClient(ctx, helpsCfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("upstream request: %w", err)
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("qwen images: close response body error: %v", errClose)
		}
	}()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		body, _ := io.ReadAll(httpResp.Body)
		return "", fmt.Errorf("qwen API error (status %d): %s", httpResp.StatusCode, truncateString(string(body), 500))
	}

	contentURL, err := extractImageURLFromSSE(httpResp.Body)
	if err != nil {
		log.Warnf("qwen images: SSE did not yield URL, trying chat detail fallback")
		contentURL, err = h.qwenFetchImageFromChatDetail(ctx, auth, chatID, token, cookie)
		if err != nil {
			return "", fmt.Errorf("extract image: %w", err)
		}
	}

	return contentURL, nil
}

func (h *OpenAIAPIHandler) qwenGenerateChatID(ctx context.Context, auth *cliproxyauth.Auth, token, cookie string) (string, error) {
	url := qwenauth.QwenAPIBaseURL + "/api/v2/chats"

	reqBody := []byte(`{"name":"New Chat"}`)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(reqBody)))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	qwenauth.ApplyAllQwenHeaders(httpReq, token, cookie, false)

	var helpsCfg *config.Config
	if h.Cfg != nil {
		helpsCfg = &config.Config{
			SDKConfig: *h.Cfg,
		}
	}
	httpClient := helps.NewProxyAwareHTTPClient(ctx, helpsCfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("qwen images: close chat_id response body error: %v", errClose)
		}
	}()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", err
	}

	chatID := strings.TrimSpace(gjson.GetBytes(body, "data.id").String())
	if chatID == "" {
		chatID = strings.TrimSpace(gjson.GetBytes(body, "id").String())
	}
	if chatID == "" {
		return "", fmt.Errorf("qwen API did not return chat_id, status=%d body=%s", httpResp.StatusCode, string(body))
	}
	return chatID, nil
}

func (h *OpenAIAPIHandler) qwenGetChatDetail(ctx context.Context, auth *cliproxyauth.Auth, chatID, token, cookie string) ([]byte, error) {
	url := qwenauth.QwenAPIBaseURL + "/api/v2/chats/" + chatID

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	qwenauth.ApplyAllQwenHeaders(httpReq, token, cookie, false)

	var helpsCfg *config.Config
	if h.Cfg != nil {
		helpsCfg = &config.Config{
			SDKConfig: *h.Cfg,
		}
	}
	httpClient := helps.NewProxyAwareHTTPClient(ctx, helpsCfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("qwen images: close chat detail response body error: %v", errClose)
		}
	}()

	return io.ReadAll(httpResp.Body)
}

func (h *OpenAIAPIHandler) qwenFetchImageFromChatDetail(ctx context.Context, auth *cliproxyauth.Auth, chatID, token, cookie string) (string, error) {
	for attempt := 0; attempt < 5; attempt++ {
		detail, err := h.qwenGetChatDetail(ctx, auth, chatID, token, cookie)
		if err != nil {
			log.Warnf("qwen images: getChatDetail attempt %d failed: %v", attempt+1, err)
			time.Sleep(800 * time.Millisecond)
			continue
		}

		url := extractResourceURLFromPayload(detail)
		if url != "" {
			return url, nil
		}
		time.Sleep(800 * time.Millisecond)
	}
	return "", fmt.Errorf("image URL not found in chat detail after 5 attempts")
}

func qwenCredsFromAuth(a *cliproxyauth.Auth) (token, cookie string) {
	if a == nil {
		return "", ""
	}
	if a.Metadata != nil {
		if v, ok := a.Metadata["access_token"].(string); ok && strings.TrimSpace(v) != "" {
			token = v
		}
		if v, ok := a.Metadata["cookie"].(string); ok && strings.TrimSpace(v) != "" {
			cookie = v
		}
	}
	if a.Attributes != nil {
		if token == "" {
			if v := a.Attributes["access_token"]; v != "" {
				token = v
			}
		}
		if token == "" {
			if v := a.Attributes["api_key"]; v != "" {
				token = v
			}
		}
		if cookie == "" {
			if v := a.Attributes["cookie"]; v != "" {
				cookie = v
			}
		}
	}
	return token, cookie
}

func buildQwenImageRequest(chatID, model, prompt, size string, stream bool) []byte {
	req := []byte(`{}`)
	req, _ = sjson.SetBytes(req, "stream", stream)
	req, _ = sjson.SetBytes(req, "version", "2.1")
	req, _ = sjson.SetBytes(req, "incremental_output", true)
	req, _ = sjson.SetBytes(req, "chat_id", chatID)

	if model == "" {
		model = "qwen-vl-max"
	}
	req, _ = sjson.SetBytes(req, "model", model)

	msg := []byte(`{}`)
	msg, _ = sjson.SetBytes(msg, "role", "user")
	msg, _ = sjson.SetBytes(msg, "content", prompt)
	msg, _ = sjson.SetBytes(msg, "chat_type", "t2i")
	msg, _ = sjson.SetRawBytes(msg, "files", []byte(`[]`))
	msg, _ = sjson.SetRawBytes(msg, "feature_config", []byte(`{"output_schema":"phase"}`))
	req, _ = sjson.SetRawBytes(req, "messages.-1", msg)

	if size != "" {
		ratio := size
		if size == "1024x1024" {
			ratio = "1:1"
		} else if size == "1536x1024" {
			ratio = "4:3"
		} else if size == "1024x1536" {
			ratio = "3:4"
		} else if size == "1792x1024" {
			ratio = "16:9"
		} else if size == "1024x1792" {
			ratio = "9:16"
		}
		req, _ = sjson.SetBytes(req, "size", ratio)
	}

	return req
}

func extractImageURLFromSSE(body io.Reader) (string, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(nil, 1_048_576) // 1MB

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		jsonData := strings.TrimPrefix(line, "data: ")
		if jsonData == "" || jsonData == "[DONE]" {
			continue
		}

		url := extractResourceURLFromPayload([]byte(jsonData))
		if url != "" {
			return url, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("SSE read error: %w", err)
	}
	return "", fmt.Errorf("no image URL found in SSE stream")
}

func extractResourceURLFromPayload(payload []byte) string {
	for _, path := range []string{
		"url", "image_url", "video_url", "download_url", "file_url",
		"resource_url", "output_url", "result_url", "final_url", "uri",
	} {
		if v := gjson.GetBytes(payload, path); v.Exists() && v.Type == gjson.String {
			u := strings.TrimSpace(v.String())
			if u != "" && (strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")) {
				return u
			}
		}
	}

	if content := gjson.GetBytes(payload, "content"); content.Exists() && content.Type == gjson.String {
		text := content.String()
		if idx := strings.Index(text, "![image]("); idx >= 0 {
			start := idx + len("![image](")
			if end := strings.Index(text[start:], ")"); end >= 0 {
				return text[start : start+end]
			}
		}
		for _, prefix := range []string{"http://", "https://"} {
			if idx := strings.Index(text, prefix); idx >= 0 {
				end := idx
				for end < len(text) && text[end] != ' ' && text[end] != '\n' && text[end] != '"' && text[end] != ')' {
					end++
				}
				u := text[idx:end]
				if len(u) > 10 {
					return u
				}
			}
		}
	}

	for _, path := range []string{"data", "message", "delta", "extra", "output", "result", "results", "choices"} {
		if nested := gjson.GetBytes(payload, path); nested.Exists() {
			if nested.IsArray() {
				for _, item := range nested.Array() {
					if u := extractResourceURLFromPayload([]byte(item.Raw)); u != "" {
						return u
					}
				}
			} else if nested.IsObject() {
				if u := extractResourceURLFromPayload([]byte(nested.Raw)); u != "" {
					return u
				}
			}
		}
	}

	return ""
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func sendStreamChunk(w io.Writer, flusher http.Flusher, id string, model string, content string, finishReason any) {
	var finishReasonStr string
	if finishReason == nil {
		finishReasonStr = "null"
	} else {
		finishReasonStr = fmt.Sprintf("%q", finishReason.(string))
	}
	
	var deltaJSON string
	if content == "" && finishReason == nil {
		deltaJSON = `{"role":"assistant"}`
	} else {
		deltaJSON = fmt.Sprintf(`{"content":%q}`, content)
	}

	chunk := fmt.Sprintf(`{"id":%q,"object":"chat.completion.chunk","created":%d,"model":%q,"choices":[{"index":0,"delta":%s,"finish_reason":%s}]}`,
		id, time.Now().Unix(), model, deltaJSON, finishReasonStr)

	_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
	flusher.Flush()
}

func sendNonStreamResponse(c *gin.Context, id string, model string, markdownLink string) {
	c.JSON(http.StatusOK, gin.H{
		"id":      id,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []gin.H{
			{
				"index": 0,
				"message": gin.H{
					"role":    "assistant",
					"content": markdownLink,
				},
				"finish_reason": "stop",
			},
		},
		"usage": gin.H{
			"prompt_tokens":     10,
			"completion_tokens": 10,
			"total_tokens":      20,
		},
	})
}
