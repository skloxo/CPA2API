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
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	qwenauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/qwen"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	qwenTranslator "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/qwen"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
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
	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	return httpClient.Do(httpReq)
}

// Execute performs a non-streaming chat completion request to Qwen.
func (e *QwenExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	from := opts.SourceFormat
	baseModel := thinking.ParseSuffix(req.Model).ModelName
	token, _ := qwenCreds(auth)

	reporter := helps.NewUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	to := sdktranslator.FromString("openai")
	originalPayloadSource := req.Payload
	if len(opts.OriginalRequest) > 0 {
		originalPayloadSource = opts.OriginalRequest
	}
	originalPayload := bytes.Clone(originalPayloadSource)
	originalTranslated := sdktranslator.TranslateRequest(from, to, baseModel, originalPayload, false)
	body := sdktranslator.TranslateRequest(from, to, baseModel, bytes.Clone(req.Payload), false)

	body, err = thinking.ApplyThinking(body, req.Model, from.String(), "qwen", e.Identifier())
	if err != nil {
		return resp, err
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

	// Session affinity: reuse chat_id from auth metadata if available
	chatID := e.resolveChatID(auth)
	url := apiBase + "/api/v2/chat/completions?chat_id=" + chatID

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return resp, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	// Apply all anti-detection headers
	qwenauth.ApplyAllQwenHeaders(httpReq, token, qwenCookie(auth), false)

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

	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("qwen executor: close response body error: %v", errClose)
		}
	}()
	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		helps.AppendAPIResponseChunk(ctx, e.cfg, b)
		helps.LogWithRequestID(ctx).Debugf("request error, error status: %d, error message: %s", httpResp.StatusCode, helps.SummarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
		err = statusErr{code: httpResp.StatusCode, msg: string(b)}
		return resp, err
	}
	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	helps.AppendAPIResponseChunk(ctx, e.cfg, data)
	reporter.Publish(ctx, helps.ParseOpenAIUsage(data))
	var param any
	out := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, opts.OriginalRequest, body, data, &param)
	resp = cliproxyexecutor.Response{Payload: out, Headers: httpResp.Header.Clone()}
	return resp, nil
}

// ExecuteStream performs a streaming chat completion request to Qwen.
// Qwen uses an event-based SSE format that is parsed and converted to OpenAI-compatible chunks.
func (e *QwenExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (_ *cliproxyexecutor.StreamResult, err error) {
	from := opts.SourceFormat
	baseModel := thinking.ParseSuffix(req.Model).ModelName
	token, _ := qwenCreds(auth)

	reporter := helps.NewUsageReporter(ctx, e.Identifier(), baseModel, auth)
	defer reporter.TrackFailure(ctx, &err)

	to := sdktranslator.FromString("openai")
	originalPayloadSource := req.Payload
	if len(opts.OriginalRequest) > 0 {
		originalPayloadSource = opts.OriginalRequest
	}
	originalPayload := bytes.Clone(originalPayloadSource)
	originalTranslated := sdktranslator.TranslateRequest(from, to, baseModel, originalPayload, true)
	body := sdktranslator.TranslateRequest(from, to, baseModel, bytes.Clone(req.Payload), true)

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

	// Session affinity: reuse chat_id
	chatID := e.resolveChatID(auth)
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

	httpClient := helps.NewProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		helps.RecordAPIResponseError(ctx, e.cfg, err)
		return nil, err
	}
	helps.RecordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())
	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		helps.AppendAPIResponseChunk(ctx, e.cfg, b)
		helps.LogWithRequestID(ctx).Debugf("request error, error status: %d, error message: %s", httpResp.StatusCode, helps.SummarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("qwen executor: close response body error: %v", errClose)
		}
		err = statusErr{code: httpResp.StatusCode, msg: string(b)}
		return nil, err
	}

	out := make(chan cliproxyexecutor.StreamChunk)
	go func() {
		defer close(out)
		defer func() {
			if errClose := httpResp.Body.Close(); errClose != nil {
				log.Errorf("qwen executor: close response body error: %v", errClose)
			}
		}()

		currentEvent := ""
		var param any
		scanner := bufio.NewScanner(httpResp.Body)
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

			// Handle complete event - emit finish chunk
			if currentEvent == "complete" {
				finishChunk := buildQwenFinishChunk(baseModel)
				select {
				case out <- cliproxyexecutor.StreamChunk{Payload: finishChunk}:
				case <-ctx.Done():
					return
				}
				currentEvent = ""
				continue
			}

			// Handle message event (or default) - convert to OpenAI chunk
			chunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, opts.OriginalRequest, body, []byte(jsonData), &param)
			for i := range chunks {
				helps.AppendAPIResponseChunk(ctx, e.cfg, chunks[i])
				select {
				case out <- cliproxyexecutor.StreamChunk{Payload: chunks[i]}:
				case <-ctx.Done():
					return
				}
			}
			currentEvent = ""
		}

		// Emit final [DONE] marker
		reporter.EnsurePublished(ctx)
		doneChunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, opts.OriginalRequest, body, []byte("[DONE]"), &param)
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

// resolveChatID returns a stable chat_id for session affinity.
// If auth metadata contains a stored chat_id, it is reused; otherwise a new UUID is generated
// and optionally persisted back to metadata for subsequent requests.
func (e *QwenExecutor) resolveChatID(auth *cliproxyauth.Auth) string {
	if auth != nil && auth.Metadata != nil {
		if v, ok := auth.Metadata["chat_id"].(string); ok && strings.TrimSpace(v) != "" {
			return v
		}
		// Also check for API-key-scoped session cache
		if apiKey, ok := auth.Metadata["api_key"].(string); ok && strings.TrimSpace(apiKey) != "" {
			cached := helps.CachedSessionID(apiKey)
			if cached != "" {
				return cached
			}
		}
	}
	newID := uuid.New().String()
	e.storeChatID(auth, newID)
	return newID
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

	var email, password string
	if auth.Metadata != nil {
		if v, ok := auth.Metadata["email"].(string); ok {
			email = strings.TrimSpace(v)
		}
		if v, ok := auth.Metadata["password"].(string); ok {
			password = strings.TrimSpace(v)
		}
	}
	if email == "" || password == "" {
		return auth, fmt.Errorf("qwen executor: cannot refresh without email and password in metadata")
	}

	qwenAuthSvc := qwenauth.NewQwenAuth(e.cfg)
	result, err := qwenAuthSvc.SignIn(ctx, email, password)
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
	now := time.Now().Format(time.RFC3339)
	auth.Metadata["last_refresh"] = now

	if storage, ok := auth.Storage.(*qwenauth.QwenTokenStorage); ok {
		storage.AccessToken = result.Token
		if result.Expired != "" {
			storage.Expired = result.Expired
		}
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

// buildQwenFinishChunk creates an OpenAI-compatible finish chunk for stream completion.
func buildQwenFinishChunk(model string) []byte {
	chunkID := fmt.Sprintf("chatcmpl-qwen-%d", time.Now().UnixNano())
	return []byte(fmt.Sprintf(
		`{"id":"%s","object":"chat.completion.chunk","created":%d,"model":"%s","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		chunkID, time.Now().Unix(), model,
	))
}

// buildQwenDoneChunk creates the final [DONE] marker chunk.
func buildQwenDoneChunk() []byte {
	return []byte("data: [DONE]\n\n")
}
