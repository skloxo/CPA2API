// Package executor provides the Qwen API executor for the CLI Proxy API.
//
// The QwenExecutor handles chat completion requests to Qwen (Alibaba Cloud) API,
// supporting both streaming (SSE) and non-streaming modes. It manages authentication
// via JWT Bearer tokens and cookies, and translates between OpenAI and Qwen-specific
// SSE response formats.
//
// Qwen API endpoints:
//   - Base URL: https://chat.qwen.ai
//   - Chat Completions: POST /api/v2/chat/completions?chat_id={uuid}
//   - Long Context (CLI): https://portal.qwen.ai
//
// Features:
//   - VLM image support with base64→OSS upload
//   - Session affinity via chat_id reuse per API key
//   - Streaming usage statistics reporting
//   - Anti-detection browser headers and ssxmod cookies
//   - Long-context routing to CLI endpoint
package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	qwenauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/qwen"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	qwenTranslator "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/qwen"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// QwenExecutor is a stateless executor for Qwen API using chat completions.
type QwenExecutor struct {
	ClaudeExecutor
	cfg       *config.Config
	modelDisc *QwenModelDiscovery
}

// NewQwenExecutor creates a new Qwen executor.
func NewQwenExecutor(cfg *config.Config) *QwenExecutor {
	qwenauth.InitSsxmodManager()
	e := &QwenExecutor{
		cfg:       cfg,
		modelDisc: NewQwenModelDiscovery(cfg),
	}
	return e
}

// Identifier returns the executor identifier.
func (e *QwenExecutor) Identifier() string { return "qwen" }

// PrepareRequest injects Qwen credentials (Bearer token + Cookie) into the outgoing HTTP request.
// It also applies anti-detection headers and ssxmod cookies.
func (e *QwenExecutor) PrepareRequest(req *http.Request, auth *cliproxyauth.Auth) error {
	if req == nil {
		return nil
	}
	token, cookie := qwenCreds(auth)

	// Apply anti-detection headers and ssxmod cookies
	qwenauth.ApplyAllQwenHeaders(req, token, cookie, false)

	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(req, attrs)
	return nil
}

// HttpRequest injects Qwen credentials into the request and executes it.
func (e *QwenExecutor) HttpRequest(ctx context.Context, auth *cliproxyauth.Auth, req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("qwen executor: request is nil")
	}
	if ctx == nil {
		ctx = req.Context()
	}
	httpReq := req.WithContext(ctx)
	if err := e.PrepareRequest(httpReq, auth); err != nil {
		return nil, err
	}
	// Use Chrome TLS fingerprint (utls) to bypass proxy TLS inspection for chat.qwen.ai.
	httpClient := helps.NewUtlsHTTPClient(e.cfg, auth, 0)
	return httpClient.Do(httpReq)
}

// Execute performs a non-streaming chat completion request to Qwen.
// P0 fix: Qwen API does not support stream=false, so we force streaming,
// collect all SSE chunks, and assemble a single non-streaming response.
func (e *QwenExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	from := opts.SourceFormat
	baseModel := thinking.ParseSuffix(req.Model).ModelName
	token, _ := qwenCreds(auth)

	reporter := helps.NewUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	// Translate request to Qwen native format (not OpenAI).
	to := sdktranslator.FromString("qwen")
	originalPayloadSource := req.Payload
	if len(opts.OriginalRequest) > 0 {
		originalPayloadSource = opts.OriginalRequest
	}
	originalPayload := bytes.Clone(originalPayloadSource)
	originalTranslated := sdktranslator.TranslateRequest(from, to, baseModel, originalPayload, false)
	body := sdktranslator.TranslateRequest(from, to, baseModel, bytes.Clone(req.Payload), false)

	if token != "" {
		body = qwenTranslator.ConvertQwenNativeImageUpload(body, token)
		originalTranslated = qwenTranslator.ConvertQwenNativeImageUpload(originalTranslated, token)
	}

	body, err = thinking.ApplyThinking(body, req.Model, from.String(), "qwen", e.Identifier())
	if err != nil {
		return resp, err
	}

	// Force streaming since Qwen does not support stream=false.
	body, err = sjson.SetBytes(body, "stream", true)
	if err != nil {
		return resp, fmt.Errorf("qwen executor: failed to set stream=true: %w", err)
	}
	body, err = sjson.SetBytes(body, "stream_options.include_usage", true)
	if err != nil {
		return resp, fmt.Errorf("qwen executor: failed to set stream_options: %w", err)
	}

	requestedModel := helps.PayloadRequestedModel(opts, req.Model)
	requestPath := helps.PayloadRequestPath(opts)
	body = helps.ApplyPayloadConfigWithRequest(e.cfg, baseModel, to.String(), from.String(), "", body, originalTranslated, requestedModel, requestPath, opts.Headers)

	// Determine endpoint: long-context models use CLI endpoint
	apiBase := qwenauth.QwenAPIBaseURL
	isLongCtx := qwenTranslator.IsLongContextModel(req.Model)
	if isLongCtx {
		apiBase = qwenauth.QwenCLIEndpoint
	}

	// Session affinity: reuse chat_id from incoming request first, falling back to resolveChatID
	chatID := e.getIncomingChatID(req, opts)
	if chatID == "" {
		var errResolve error
		chatID, errResolve = e.resolveChatID(ctx, auth)
		if errResolve != nil {
			return resp, errResolve
		}
	}
	// Qwen requires chat_id in BOTH the URL query param AND the JSON request body.
	// Omitting it from the body causes upstream to reject the request and close the stream.
	body, err = sjson.SetBytes(body, "chat_id", chatID)
	if err != nil {
		return resp, fmt.Errorf("qwen executor: failed to set chat_id in payload: %w", err)
	}
	url := apiBase + "/api/v2/chat/completions?chat_id=" + chatID

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return resp, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Accept-Encoding", "identity")

	// Apply all anti-detection headers
	qwenauth.ApplyAllQwenHeaders(httpReq, token, qwenCookie(auth), true)

	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(httpReq, attrs)

	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	helps.RecordAPIRequest(ctx, e.cfg, helps.UpstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      body,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	// Use Chrome TLS fingerprint (utls) to bypass proxy TLS inspection for chat.qwen.ai.
	httpClient := helps.NewUtlsHTTPClient(e.cfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	decodedBody, err := decodeResponseBody(httpResp.Body, httpResp.Header.Get("Content-Encoding"))
	if err != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("response body close error: %v", errClose)
		}
		return resp, err
	}
	defer func() {
		if errClose := decodedBody.Close(); errClose != nil {
			log.Errorf("qwen executor: close response body error: %v", errClose)
		}
	}()
	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(decodedBody)
		helps.AppendAPIResponseChunk(ctx, e.cfg, b)
		helps.LogWithRequestID(ctx).Debugf("request error, error status: %d, error message: %s", httpResp.StatusCode, helps.SummarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
		err = statusErr{code: httpResp.StatusCode, msg: string(b)}
		return resp, err
	}

	// Collect streaming SSE response and assemble non-streaming result.
	collectedContent, collectedUsage, collectedErr := e.collectStreamResponse(ctx, decodedBody, baseModel, req.Model, from, body, opts, reporter)
	if collectedErr != nil {
		return resp, collectedErr
	}

	// Build a non-streaming OpenAI-compatible response from collected chunks.
	var param any
	out := sdktranslator.TranslateNonStream(ctx, sdktranslator.FromString("qwen"), from, req.Model, opts.OriginalRequest, body, collectedContent, &param)
	resp = cliproxyexecutor.Response{Payload: out, Headers: httpResp.Header.Clone()}
	_ = collectedUsage // usage already reported via reporter
	return resp, nil
}

// collectStreamResponse reads the SSE stream and assembles a single non-streaming
// OpenAI-compatible response from all chunks. Returns the assembled JSON and usage data.
func (e *QwenExecutor) collectStreamResponse(
	ctx context.Context,
	body io.Reader,
	baseModel, reqModel string,
	from sdktranslator.Format,
	requestBody []byte,
	opts cliproxyexecutor.Options,
	reporter *helps.UsageReporter,
) (assembled []byte, usageData []byte, err error) {
	var fullContent strings.Builder
	var lastUsage []byte
	var param any

	type accumulatedToolCall struct {
		ID        string
		Type      string
		Name      string
		Arguments strings.Builder
	}
	toolCallsMap := make(map[int]*accumulatedToolCall)

	scanner := bufio.NewScanner(body)
	scanner.Buffer(nil, 1_048_576) // 1MB

	for scanner.Scan() {
		line := string(scanner.Bytes())
		log.Debugf("qwen executor collectStreamResponse: scanned line: %s", line)
		helps.AppendAPIResponseChunk(ctx, e.cfg, scanner.Bytes())

		openAIChunks := qwenTranslator.ConvertQwenSSELineToOpenAI(line, reqModel, opts.OriginalRequest, requestBody, &param)
		log.Debugf("qwen executor collectStreamResponse: converted OpenAI chunks count: %d", len(openAIChunks))
		for _, chunkBytes := range openAIChunks {
			chunkStr := string(chunkBytes)
			log.Debugf("qwen executor collectStreamResponse: chunkStr: %s", chunkStr)
			jsonData := strings.TrimPrefix(chunkStr, "data: ")
			jsonData = strings.TrimSpace(jsonData)
			if jsonData == "" || jsonData == "[DONE]" || !gjson.Valid(jsonData) {
				continue
			}

			// Track usage
			if detail, ok := helps.ParseOpenAIStreamUsage([]byte("data: " + jsonData)); ok {
				reporter.Publish(ctx, detail)
			}

			result := gjson.Parse(jsonData)

			// Extract content from choices
			choices := result.Get("choices")
			if choices.Exists() && choices.IsArray() && len(choices.Array()) > 0 {
				delta := choices.Array()[0].Get("delta")
				if delta.Exists() {
					content := delta.Get("content").String()
					log.Debugf("qwen executor collectStreamResponse: extracted delta content: %q", content)
					if content != "" {
						fullContent.WriteString(content)
					}

					// Parse tool_calls if present in the chunk
					tcs := delta.Get("tool_calls")
					if tcs.Exists() && tcs.IsArray() {
						for _, tc := range tcs.Array() {
							idx := int(tc.Get("index").Int())
							acc, exists := toolCallsMap[idx]
							if !exists {
								acc = &accumulatedToolCall{}
								toolCallsMap[idx] = acc
							}
							if id := tc.Get("id").String(); id != "" {
								acc.ID = id
							}
							if t := tc.Get("type").String(); t != "" {
								acc.Type = t
							}
							if name := tc.Get("function.name").String(); name != "" {
								acc.Name = name
							}
							if args := tc.Get("function.arguments").String(); args != "" {
								acc.Arguments.WriteString(args)
							}
						}
					}
				}
			}

			// Capture usage data from the final chunk
			usage := result.Get("usage")
			if usage.Exists() {
				lastUsage = []byte(usage.Raw)
			}
		}
	}

	if errScan := scanner.Err(); errScan != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, errScan)
		return nil, nil, errScan
	}

	respID := fmt.Sprintf("chatcmpl-qwen-%d", time.Now().UnixNano())
	created := time.Now().Unix()
	content := fullContent.String()

	// Build OpenAI compatible non-streaming response object map
	msgObj := map[string]interface{}{
		"role":    "assistant",
		"content": content,
	}

	finishReason := "stop"

	if len(toolCallsMap) > 0 {
		maxIdx := -1
		for idx := range toolCallsMap {
			if idx > maxIdx {
				maxIdx = idx
			}
		}

		var toolCalls []map[string]interface{}
		for idx := 0; idx <= maxIdx; idx++ {
			if acc, ok := toolCallsMap[idx]; ok {
				cleanArgs := qwenTranslator.FixToolArgs(acc.Arguments.String())
				tc := map[string]interface{}{
					"id":   acc.ID,
					"type": "function",
					"function": map[string]interface{}{
						"name":      acc.Name,
						"arguments": cleanArgs,
					},
				}
				if acc.Type != "" {
					tc["type"] = acc.Type
				}
				toolCalls = append(toolCalls, tc)
			}
		}
		msgObj["tool_calls"] = toolCalls
		msgObj["content"] = nil
		finishReason = "tool_calls"
	}

	choiceObj := map[string]interface{}{
		"index":         0,
		"message":       msgObj,
		"finish_reason": finishReason,
	}

	respMap := map[string]interface{}{
		"id":      respID,
		"object":  "chat.completion",
		"created": created,
		"model":   baseModel,
		"choices": []interface{}{choiceObj},
	}

	// Append usage if available
	if len(lastUsage) > 0 {
		parsed := gjson.ParseBytes(lastUsage)
		inputTokens := parsed.Get("input_tokens").Int()
		outputTokens := parsed.Get("output_tokens").Int()
		totalTokens := parsed.Get("total_tokens").Int()
		if inputTokens == 0 {
			inputTokens = parsed.Get("prompt_tokens").Int()
		}
		if outputTokens == 0 {
			outputTokens = parsed.Get("completion_tokens").Int()
		}
		if totalTokens == 0 {
			totalTokens = inputTokens + outputTokens
		}
		if totalTokens > 0 {
			respMap["usage"] = map[string]interface{}{
				"prompt_tokens":     inputTokens,
				"completion_tokens": outputTokens,
				"total_tokens":      totalTokens,
			}
		}
	}

	respJSON, err := json.Marshal(respMap)
	if err != nil {
		return nil, nil, err
	}

	return respJSON, lastUsage, nil
}

// escapeJSONForNonStream escapes a string for JSON embedding in non-streaming responses.
func escapeJSONForNonStream(s string) string {
	b, _ := json.Marshal(s)
	return strings.Trim(string(b), "\"")
}

// ExecuteStream performs a streaming chat completion request to Qwen.
// Qwen uses an event-based SSE format that is parsed and converted to OpenAI-compatible chunks.
func (e *QwenExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	from := opts.SourceFormat
	baseModel := thinking.ParseSuffix(req.Model).ModelName
	token, _ := qwenCreds(auth)

	reporter := helps.NewUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	// Translate request to Qwen native format (not OpenAI).
	to := sdktranslator.FromString("qwen")
	openaiFormat := sdktranslator.FromString("openai") // used for 2-step Qwen→OpenAI→client translation
	originalPayloadSource := req.Payload
	if len(opts.OriginalRequest) > 0 {
		originalPayloadSource = opts.OriginalRequest
	}
	originalPayload := bytes.Clone(originalPayloadSource)
	originalTranslated := sdktranslator.TranslateRequest(from, to, baseModel, originalPayload, true)
	body := sdktranslator.TranslateRequest(from, to, baseModel, bytes.Clone(req.Payload), true)

	if token != "" {
		body = qwenTranslator.ConvertQwenNativeImageUpload(body, token)
		originalTranslated = qwenTranslator.ConvertQwenNativeImageUpload(originalTranslated, token)
	}

	body, err = thinking.ApplyThinking(body, req.Model, from.String(), "qwen", e.Identifier())
	if err != nil {
		return nil, err
	}

	body, err = sjson.SetBytes(body, "stream_options.include_usage", true)
	if err != nil {
		return nil, fmt.Errorf("qwen executor: failed to set stream_options in payload: %w", err)
	}
	requestedModel := helps.PayloadRequestedModel(opts, req.Model)
	requestPath := helps.PayloadRequestPath(opts)
	body = helps.ApplyPayloadConfigWithRequest(e.cfg, baseModel, to.String(), from.String(), "", body, originalTranslated, requestedModel, requestPath, opts.Headers)

	// Determine endpoint
	apiBase := qwenauth.QwenAPIBaseURL
	isLongCtx := qwenTranslator.IsLongContextModel(req.Model)
	if isLongCtx {
		apiBase = qwenauth.QwenCLIEndpoint
	}

	// Session affinity: reuse chat_id from incoming request first, falling back to resolveChatID
	chatID := e.getIncomingChatID(req, opts)
	if chatID == "" {
		var errResolve error
		chatID, errResolve = e.resolveChatID(ctx, auth)
		if errResolve != nil {
			return nil, errResolve
		}
	}
	// Qwen requires chat_id in BOTH the URL query param AND the JSON request body.
	// Omitting it from the body causes upstream to reject the request and close the stream.
	body, err = sjson.SetBytes(body, "chat_id", chatID)
	if err != nil {
		return nil, fmt.Errorf("qwen executor: failed to set chat_id in payload: %w", err)
	}
	url := apiBase + "/api/v2/chat/completions?chat_id=" + chatID

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")

	// Apply all anti-detection headers
	qwenauth.ApplyAllQwenHeaders(httpReq, token, qwenCookie(auth), true)

	var attrs map[string]string
	if auth != nil {
		attrs = auth.Attributes
	}
	util.ApplyCustomHeadersFromAttrs(httpReq, attrs)
	// Re-enforce Accept-Encoding: identity for streaming to prevent compressed SSE.
	// Compressed streams break the line scanner.
	httpReq.Header.Set("Accept-Encoding", "identity")

	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	helps.RecordAPIRequest(ctx, e.cfg, helps.UpstreamRequestLog{
		URL:       url,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      body,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	// Use Chrome TLS fingerprint (utls) to bypass proxy TLS inspection for chat.qwen.ai.
	httpClient := helps.NewUtlsHTTPClient(e.cfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		return nil, err
	}
	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())

	decodedBody, err := decodeResponseBody(httpResp.Body, httpResp.Header.Get("Content-Encoding"))
	if err != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("qwen executor: close response body error: %v", errClose)
		}
		return nil, err
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(decodedBody)
		helps.AppendAPIResponseChunk(ctx, e.cfg, b)
		helps.LogWithRequestID(ctx).Debugf("request error, error status: %d, error message: %s", httpResp.StatusCode, helps.SummarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
		if errClose := decodedBody.Close(); errClose != nil {
			log.Errorf("qwen executor: close response body error: %v", errClose)
		}
		err = statusErr{code: httpResp.StatusCode, msg: string(b)}
		return nil, err
	}

	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		defer func() {
			if errClose := decodedBody.Close(); errClose != nil {
				log.Errorf("qwen executor: close response body error: %v", errClose)
			}
		}()

		currentEvent := ""
		var param any
		scanner := bufio.NewScanner(decodedBody)
		scanner.Buffer(nil, 1_048_576) // 1MB

		for scanner.Scan() {
			line := string(scanner.Bytes())
			helps.AppendAPIResponseChunk(ctx, e.cfg, scanner.Bytes())

			// Parse Qwen event-based SSE format
			if strings.HasPrefix(line, "event: ") {
				currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
				continue
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			jsonData := strings.TrimPrefix(line, "data: ")
			if jsonData == "" {
				continue
			}

			if !gjson.Valid(jsonData) {
				log.Debugf("qwen executor: invalid JSON in SSE data: %s", jsonData)
				continue
			}

			// Parse streaming usage data
			if detail, ok := helps.ParseOpenAIStreamUsage([]byte("data: " + jsonData)); ok {
				reporter.Publish(ctx, detail)
			}

			// Handle complete event - emit finish chunk and [DONE]
			if currentEvent == "complete" {
				// Build and emit the finish-reason chunk in OpenAI format, then translate to client format.
				openAILine := buildQwenFinishSSEChunk(baseModel)
				chunks := sdktranslator.TranslateStream(ctx, from, openaiFormat, req.Model, opts.OriginalRequest, body, openAILine, &param)
				for i := range chunks {
					helps.AppendAPIResponseChunk(ctx, e.cfg, chunks[i])
					select {
					case out <- cliproxyexecutor.StreamChunk{Payload: chunks[i]}:
					case <-ctx.Done():
						return
					}
				}
				currentEvent = ""
				continue
			}

			// Handle message event (or default):
			// Step 1: Convert Qwen SSE line → OpenAI SSE chunk(s) via ConvertQwenResponseToOpenAI.
			// Step 2: Translate each OpenAI chunk → client target format via TranslateStream.
			// This 2-step approach handles all client formats (openai, claude, etc.).
			openAIChunks := qwenTranslator.ConvertQwenSSELineToOpenAI(line, req.Model, opts.OriginalRequest, body, &param)
			for _, openAIChunk := range openAIChunks {
				translated := sdktranslator.TranslateStream(ctx, from, openaiFormat, req.Model, opts.OriginalRequest, body, openAIChunk, &param)
				for i := range translated {
					helps.AppendAPIResponseChunk(ctx, e.cfg, translated[i])
					select {
					case out <- cliproxyexecutor.StreamChunk{Payload: translated[i]}:
					case <-ctx.Done():
						return
					}
				}
			}
			currentEvent = ""
		}

		// Emit final [DONE] marker in client target format
		reporter.EnsurePublished(ctx)
		doneChunks := sdktranslator.TranslateStream(ctx, from, openaiFormat, req.Model, opts.OriginalRequest, body, []byte("data: [DONE]\n\n"), &param)
		for i := range doneChunks {
			select {
			case out <- cliproxyexecutor.StreamChunk{Payload: doneChunks[i]}:
			case <-ctx.Done():
				return
			}
		}

		if errScan := scanner.Err(); errScan != nil {
			helps.RecordAPIResponseError(ctx, e.cfg, errScan)
			reporter.PublishFailure(ctx, errScan)
			select {
			case out <- cliproxyexecutor.StreamChunk{Err: errScan}:
			case <-ctx.Done():
			}
		}
	}()
	return &cliproxyexecutor.StreamResult{Headers: httpResp.Header.Clone(), Chunks: out}, nil
}

// getIncomingChatID extracts the chat_id from request body, options or headers if present.
func (e *QwenExecutor) getIncomingChatID(req cliproxyexecutor.Request, opts cliproxyexecutor.Options) string {
	if chatID := gjson.GetBytes(req.Payload, "chat_id").String(); chatID != "" {
		return chatID
	}
	if len(opts.OriginalRequest) > 0 {
		if chatID := gjson.GetBytes(opts.OriginalRequest, "chat_id").String(); chatID != "" {
			return chatID
		}
	}
	if chatID := opts.Headers.Get("X-Chat-ID"); chatID != "" {
		return chatID
	}
	if chatID := opts.Headers.Get("chat_id"); chatID != "" {
		return chatID
	}
	return ""
}

// resolveChatID returns a stable chat_id for session affinity.
// If auth metadata contains a stored chat_id, it is reused; otherwise a new chat_id
// is obtained from the Qwen API (/api/v2/chats/new) and persisted for subsequent requests.
// If the API call fails, a locally-generated UUID is used as fallback (Qwen accepts any UUID).
func (e *QwenExecutor) resolveChatID(ctx context.Context, auth *cliproxyauth.Auth) (string, error) {
	// Always generate a fresh chat_id via Qwen API or fallback to local UUID
	chatID, err := e.createChatID(ctx, auth)
	if err != nil {
		log.Debugf("qwen executor: createChatID failed (%v), using local UUID fallback", err)
		chatID = generateLocalChatID()
	}
	return chatID, nil
}

// createChatID calls the Qwen API to create a new chat session and returns its chat_id.
// POST https://chat.qwen.ai/api/v2/chats/new
func (e *QwenExecutor) createChatID(ctx context.Context, auth *cliproxyauth.Auth) (string, error) {
	token, cookie := qwenCreds(auth)
	if token == "" {
		return "", fmt.Errorf("qwen executor: no access token available for chat creation")
	}

	url := qwenauth.QwenAPIBaseURL + "/api/v2/chats/new"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader([]byte("{}")))
	if err != nil {
		return "", fmt.Errorf("qwen executor: failed to create chat request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	qwenauth.ApplyAllQwenHeaders(httpReq, token, cookie, false)

	// Use Chrome TLS fingerprint (utls) to bypass proxy TLS inspection for chat.qwen.ai.
	httpClient := helps.NewUtlsHTTPClient(e.cfg, auth, 0)
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("qwen executor: chat creation request failed: %w", err)
	}
	decodedBody, err := decodeResponseBody(resp.Body, resp.Header.Get("Content-Encoding"))
	if err != nil {
		resp.Body.Close()
		return "", fmt.Errorf("qwen executor: failed to decode chat creation response: %w", err)
	}
	defer decodedBody.Close()

	body, err := io.ReadAll(decodedBody)
	if err != nil {
		return "", fmt.Errorf("qwen executor: failed to read chat creation response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("qwen executor: chat creation returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response: {"success":true,"data":{"id":"{uuid}"}}
	idResult := gjson.GetBytes(body, "data.id")
	if !idResult.Exists() || idResult.String() == "" {
		return "", fmt.Errorf("qwen executor: chat creation response missing data.id: %s", string(body))
	}

	log.Debugf("qwen executor: created new chat_id=%s", idResult.String())
	return idResult.String(), nil
}

// storeChatID persists the chat_id back to auth metadata for session reuse.
func (e *QwenExecutor) storeChatID(auth *cliproxyauth.Auth, chatID string) {
	if auth == nil {
		return
	}
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["chat_id"] = chatID
}

// CountTokens estimates token count for Qwen requests.
func (e *QwenExecutor) CountTokens(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return e.ClaudeExecutor.CountTokens(ctx, auth, req, opts)
}

// Refresh refreshes the Qwen token by re-authenticating with stored credentials.
func (e *QwenExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	log.Debugf("qwen executor: refresh called")
	if refreshed, handled, err := helps.RefreshAuthViaHome(ctx, e.cfg, auth); handled {
		return refreshed, err
	}
	if auth == nil {
		return nil, fmt.Errorf("qwen executor: auth is nil")
	}

	var email, password, proxyURL string
	if auth.Metadata != nil {
		if v, ok := auth.Metadata["email"].(string); ok {
			email = strings.TrimSpace(v)
		}
		if v, ok := auth.Metadata["password"].(string); ok {
			password = strings.TrimSpace(v)
		}
		if v, ok := auth.Metadata["proxy_url"].(string); ok {
			proxyURL = strings.TrimSpace(v)
		}
	}
	if proxyURL == "" && auth.ProxyURL != "" {
		proxyURL = strings.TrimSpace(auth.ProxyURL)
	}
	if email == "" || password == "" {
		return auth, fmt.Errorf("qwen executor: cannot refresh without email and password in metadata")
	}

	qwenAuthSvc := qwenauth.NewQwenAuth(e.cfg)
	result, err := qwenAuthSvc.SignIn(ctx, email, password, proxyURL)
	if err != nil {
		return nil, fmt.Errorf("qwen executor: refresh failed: %w", err)
	}

	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["access_token"] = result.Token
	if result.Expired != "" {
		auth.Metadata["expired"] = result.Expired
	}
	auth.Metadata["type"] = "qwen"
	auth.Metadata["password"] = password
	auth.Metadata["proxy_url"] = proxyURL
	now := time.Now().Format(time.RFC3339)
	auth.Metadata["last_refresh"] = now

	if storage, ok := auth.Storage.(*qwenauth.QwenTokenStorage); ok {
		storage.AccessToken = result.Token
		if result.Expired != "" {
			storage.Expired = result.Expired
		}
		storage.Password = password
		storage.ProxyURL = proxyURL
	}

	// Update model discovery credentials
	e.modelDisc.SetCredentials(result.Token, qwenCookie(auth))

	return auth, nil
}

// qwenCreds extracts the access token and cookie from auth.
func qwenCreds(a *cliproxyauth.Auth) (token, cookie string) {
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

// qwenCookie extracts just the cookie from auth.
func qwenCookie(a *cliproxyauth.Auth) string {
	_, cookie := qwenCreds(a)
	return cookie
}

// generateLocalChatID creates a random UUID to use as a Qwen chat_id.
// Qwen accepts any UUID-formatted string as chat_id. This is used as a fallback
// when the API-based chat creation fails (e.g., network/proxy issues).
func generateLocalChatID() string {
	return uuid.NewString()
}

// buildQwenFinishChunk creates an OpenAI-compatible finish chunk for stream completion.
func buildQwenFinishChunk(model string) []byte {
	chunkID := fmt.Sprintf("chatcmpl-qwen-%d", time.Now().UnixNano())
	return []byte(fmt.Sprintf(
		`{"id":"%s","object":"chat.completion.chunk","created":%d,"model":"%s","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		chunkID, time.Now().Unix(), model,
	))
}

// buildQwenFinishSSEChunk creates an OpenAI SSE finish chunk with "data: " prefix
// suitable for passing through the TranslateStream pipeline.
func buildQwenFinishSSEChunk(model string) []byte {
	chunkID := fmt.Sprintf("chatcmpl-qwen-%d", time.Now().UnixNano())
	chunk := fmt.Sprintf(
		`{"id":"%s","object":"chat.completion.chunk","created":%d,"model":"%s","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		chunkID, time.Now().Unix(), model,
	)
	return []byte("data: " + chunk + "\n\n")
}

// buildQwenDoneChunk creates the final [DONE] marker chunk.
func buildQwenDoneChunk() []byte {
	return []byte("data: [DONE]\n\n")
}
