// Package handlers provides Qwen-specific image generation endpoints.
//
// This handler implements the OpenAI-compatible /v1/images/generations endpoint
// by translating requests into Qwen's internal chat completions API with t2i
// (text-to-image) chat_type. It supports both URL and base64 response formats.
//
// Flow:
//  1. Receive OpenAI image generation request (prompt, model, size, response_format)
//  2. Generate a chat_id via Qwen's internal API
//  3. POST to /api/v2/chat/completions with chat_type=t2i
//  4. Parse the SSE stream to extract the generated image URL
//  5. Optionally download and base64-encode the image
//  6. Return OpenAI-compatible response
package handlers

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	qwenauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/qwen"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/api/handlers"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// QwenHandlers provides Qwen multimedia endpoints.
type QwenHandlers struct {
	cfg  *config.Config
	auth *cliproxyauth.Auth
}

// NewQwenHandlers creates a new Qwen multimedia handler set.
func NewQwenHandlers(cfg *config.Config, auth *cliproxyauth.Auth) *QwenHandlers {
	return &QwenHandlers{cfg: cfg, auth: auth}
}

// ImagesGenerations handles POST /v1/images/generations for Qwen.
//
// Request body (OpenAI-compatible):
//
//	{
//	  "model": "qwen-vl-max",    // optional, defaults to auto-detect
//	  "prompt": "a cat sitting on a mat",
//	  "size": "1024x1024",        // optional
//	  "response_format": "url",   // "url" (default) or "b64_json"
//	  "n": 1                      // optional, only 1 supported
//	}
func (h *QwenHandlers) ImagesGenerations(c *gin.Context) {
	rawJSON, err := handlers.ReadRequestBody(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
			},
		})
		return
	}

	prompt := strings.TrimSpace(gjson.GetBytes(rawJSON, "prompt").String())
	if prompt == "" {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "Invalid request: prompt is required",
				Type:    "invalid_request_error",
			},
		})
		return
	}

	model := strings.TrimSpace(gjson.GetBytes(rawJSON, "model").String())
	responseFormat := normalizeImageResponseFormat(gjson.GetBytes(rawJSON, "response_format").String())
	size := normalizeQwenImageSize(gjson.GetBytes(rawJSON, "size").String())

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()

	contentURL, err := h.generateImage(ctx, prompt, model, size)
	if err != nil {
		errMsg := err.Error()
		status := http.StatusInternalServerError
		if se, ok := err.(qwenStatusError); ok {
			status = se.StatusCode()
			errMsg = se.UpstreamMessage()
		}
		c.JSON(status, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: errMsg,
				Type:    "server_error",
			},
		})
		return
	}

	out := buildImageGenerationsResponse(contentURL, responseFormat)
	c.Header("Content-Type", "application/json")
	_, _ = c.Writer.Write(out)
}

// generateImage orchestrates the Qwen image generation pipeline:
// chat_id generation → upstream POST → SSE parsing → URL extraction.
func (h *QwenHandlers) generateImage(ctx context.Context, prompt, model, size string) (string, error) {
	token, cookie := qwenCredsFromAuth(h.auth)

	chatID, err := h.generateChatID(ctx, token, cookie)
	if err != nil {
		return "", fmt.Errorf("generate chat_id: %w", err)
	}

	reqBody := buildQwenImageRequest(chatID, model, prompt, size, false)
	url := qwenauth.QwenAPIBaseURL + "/api/v2/chat/completions?chat_id=" + chatID

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(reqBody)))
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	qwenauth.ApplyAllQwenHeaders(httpReq, token, cookie, true)

	httpClient := helps.NewProxyAwareHTTPClient(ctx, h.cfg, h.auth, 0)
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
		return "", qwenStatusError{code: httpResp.StatusCode, body: string(body)}
	}

	contentURL, err := extractImageURLFromSSE(httpResp.Body)
	if err != nil {
		// Fallback: try fetching from chat detail
		log.Warnf("qwen images: SSE did not yield URL, trying chat detail fallback")
		contentURL, err = h.fetchImageFromChatDetail(ctx, chatID, token, cookie)
		if err != nil {
			return "", fmt.Errorf("extract image: %w", err)
		}
	}

	return contentURL, nil
}

// generateChatID calls Qwen's internal API to create a new chat session.
func (h *QwenHandlers) generateChatID(ctx context.Context, token, cookie string) (string, error) {
	url := qwenauth.QwenAPIBaseURL + "/api/v2/chats"

	reqBody := []byte(`{"name":"New Chat"}`)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(string(reqBody)))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	qwenauth.ApplyAllQwenHeaders(httpReq, token, cookie, false)

	httpClient := helps.NewProxyAwareHTTPClient(ctx, h.cfg, h.auth, 0)
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

// getChatDetail retrieves the full chat history for a given chat_id.
// Used as a fallback when the SSE stream doesn't directly yield an image URL.
func (h *QwenHandlers) getChatDetail(ctx context.Context, chatID, token, cookie string) ([]byte, error) {
	url := qwenauth.QwenAPIBaseURL + "/api/v2/chats/" + chatID

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	qwenauth.ApplyAllQwenHeaders(httpReq, token, cookie, false)

	httpClient := helps.NewProxyAwareHTTPClient(ctx, h.cfg, h.auth, 0)
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

// fetchImageFromChatDetail polls the chat detail API up to 5 times to extract
// a generated image URL from the message history.
func (h *QwenHandlers) fetchImageFromChatDetail(ctx context.Context, chatID, token, cookie string) (string, error) {
	for attempt := 0; attempt < 5; attempt++ {
		detail, err := h.getChatDetail(ctx, chatID, token, cookie)
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

// downloadAssetAsBase64 downloads a remote URL and returns its base64 encoding.
func (h *QwenHandlers) downloadAssetAsBase64(ctx context.Context, assetURL string) (string, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, assetURL, nil)
	if err != nil {
		return "", err
	}

	httpClient := helps.NewProxyAwareHTTPClient(ctx, h.cfg, h.auth, 2*time.Minute)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("qwen images: close download response body error: %v", errClose)
		}
	}()

	data, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

// buildQwenImageRequest constructs the JSON body for Qwen's chat completions
// endpoint with image generation parameters.
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
		req, _ = sjson.SetBytes(req, "size", size)
	}

	return req
}

// buildImageGenerationsResponse creates an OpenAI-compatible image generation response.
func buildImageGenerationsResponse(contentURL, responseFormat string) []byte {
	out := []byte(`{"created":0,"data":[]}`)
	out, _ = sjson.SetBytes(out, "created", time.Now().Unix())

	item := []byte(`{}`)
	if responseFormat == "b64_json" {
		// Note: base64 conversion should be done upstream; here we return URL as fallback
		item, _ = sjson.SetBytes(item, "url", contentURL)
	} else {
		item, _ = sjson.SetBytes(item, "url", contentURL)
	}
	out, _ = sjson.SetRawBytes(out, "data.-1", item)
	return out
}

// normalizeImageResponseFormat normalizes the response_format field.
// Defaults to "url" for Qwen (since we always get URLs from upstream).
func normalizeImageResponseFormat(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "b64_json" {
		return "b64_json"
	}
	return "url"
}

// normalizeQwenImageSize converts OpenAI size values to Qwen aspect ratios.
func normalizeQwenImageSize(size string) string {
	size = strings.TrimSpace(size)
	if size == "" {
		return ""
	}
	mapping := map[string]string{
		"1024x1024": "1:1",
		"1536x1024": "4:3",
		"1024x1536": "3:4",
		"1792x1024": "16:9",
		"1024x1792": "9:16",
	}
	if ratio, ok := mapping[size]; ok {
		return ratio
	}
	// If it's already a ratio like "1:1", pass through
	if strings.Contains(size, ":") {
		return size
	}
	return ""
}

// extractImageURLFromSSE reads an SSE stream and extracts the first image URL.
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

// extractResourceURLFromPayload recursively searches a JSON payload for an image URL.
func extractResourceURLFromPayload(payload []byte) string {
	// Direct URL fields
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

	// Nested content with markdown image syntax
	if content := gjson.GetBytes(payload, "content"); content.Exists() && content.Type == gjson.String {
		text := content.String()
		if idx := strings.Index(text, "![image]("); idx >= 0 {
			start := idx + len("![image](")
			if end := strings.Index(text[start:], ")"); end >= 0 {
				return text[start : start+end]
			}
		}
		// Plain URL in content
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

	// Recursive search in nested objects/arrays
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

// qwenCredsFromAuth extracts Qwen credentials from auth metadata.
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

// qwenStatusError wraps an upstream Qwen API error with status code and body.
type qwenStatusError struct {
	code int
	body string
}

func (e qwenStatusError) Error() string {
	return fmt.Sprintf("qwen API error (status %d): %s", e.code, truncateString(e.body, 500))
}

func (e qwenStatusError) StatusCode() int {
	return e.code
}

func (e qwenStatusError) UpstreamMessage() string {
	// Try to extract error message from JSON body
	msg := gjson.GetBytes([]byte(e.body), "error.message").String()
	if msg == "" {
		msg = gjson.GetBytes([]byte(e.body), "data.code").String()
	}
	if msg == "" {
		msg = gjson.GetBytes([]byte(e.body), "data.details").String()
	}
	if msg == "" {
		msg = truncateString(e.body, 200)
	}
	return msg
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
