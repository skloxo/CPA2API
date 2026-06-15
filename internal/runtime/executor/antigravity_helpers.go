package executor

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/cache"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/misc"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	antigravityclaude "github.com/router-for-me/CLIProxyAPI/v7/internal/translator/antigravity/claude"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v7/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var (
	randSource      = rand.New(rand.NewSource(time.Now().UnixNano()))
	randSourceMutex sync.Mutex
)

// antigravityTransport is a singleton HTTP/1.1 transport shared by all Antigravity requests.
// It is initialized once via antigravityTransportOnce to avoid leaking a new connection pool
// (and the goroutines managing it) on every request.
var (
	antigravityTransport     *http.Transport
	antigravityTransportOnce sync.Once
)

func cloneTransportWithHTTP11(base *http.Transport) *http.Transport {
	if base == nil {
		return nil
	}

	clone := base.Clone()
	clone.ForceAttemptHTTP2 = false
	// Wipe TLSNextProto to prevent implicit HTTP/2 upgrade.
	clone.TLSNextProto = make(map[string]func(authority string, c *tls.Conn) http.RoundTripper)
	if clone.TLSClientConfig == nil {
		clone.TLSClientConfig = &tls.Config{}
	} else {
		clone.TLSClientConfig = clone.TLSClientConfig.Clone()
	}
	// Actively advertise only HTTP/1.1 in the ALPN handshake.
	clone.TLSClientConfig.NextProtos = []string{"http/1.1"}
	return clone
}

// initAntigravityTransport creates the shared HTTP/1.1 transport exactly once.
func initAntigravityTransport() {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		base = &http.Transport{}
	}
	antigravityTransport = cloneTransportWithHTTP11(base)
}

// newAntigravityHTTPClient creates an HTTP client specifically for Antigravity,
// enforcing HTTP/1.1 by disabling HTTP/2 to perfectly mimic Node.js https defaults.
// The underlying Transport is a singleton to avoid leaking connection pools.
func newAntigravityHTTPClient(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, timeout time.Duration) *http.Client {
	antigravityTransportOnce.Do(initAntigravityTransport)

	client := helps.NewProxyAwareHTTPClient(ctx, cfg, auth, timeout)
	// If no transport is set, use the shared HTTP/1.1 transport.
	if client.Transport == nil {
		client.Transport = antigravityTransport
		return client
	}

	// Preserve proxy settings from proxy-aware transports while forcing HTTP/1.1.
	if transport, ok := client.Transport.(*http.Transport); ok {
		client.Transport = cloneTransportWithHTTP11(transport)
	}
	return client
}

func validateAntigravityRequestSignatures(from sdktranslator.Format, rawJSON []byte) ([]byte, error) {
	if from.String() != "claude" {
		return rawJSON, nil
	}
	// Always strip thinking blocks with invalid signatures (empty or non-Claude-format).
	before := countClaudeThinkingBlocks(rawJSON)
	rawJSON = antigravityclaude.StripEmptySignatureThinkingBlocks(rawJSON)
	logAntigravitySignatureStrip(before, countClaudeThinkingBlocks(rawJSON), "prefix_cleanup", "empty_or_non_claude_signature")
	if cache.SignatureCacheEnabled() {
		return rawJSON, nil
	}
	if !cache.SignatureBypassStrictMode() {
		// Non-strict bypass: let the translator handle invalid signatures
		// by dropping unsigned thinking blocks silently (no 400).
		return rawJSON, nil
	}
	before = countClaudeThinkingBlocks(rawJSON)
	rawJSON = antigravityclaude.StripInvalidBypassSignatureThinkingBlocks(rawJSON)
	logAntigravitySignatureStrip(before, countClaudeThinkingBlocks(rawJSON), "strict_bypass", "invalid_antigravity_claude_signature")
	return rawJSON, nil
}

func countClaudeThinkingBlocks(rawJSON []byte) int {
	messages := gjson.GetBytes(rawJSON, "messages")
	if !messages.IsArray() {
		return 0
	}

	count := 0
	messages.ForEach(func(_, message gjson.Result) bool {
		content := message.Get("content")
		if !content.IsArray() {
			return true
		}
		content.ForEach(func(_, part gjson.Result) bool {
			if part.Get("type").String() == "thinking" {
				count++
			}
			return true
		})
		return true
	})
	return count
}

func logAntigravitySignatureStrip(before, after int, stage, reason string) {
	removed := before - after
	if removed <= 0 {
		return
	}
	log.WithFields(log.Fields{
		"component":       "signature_sanitizer",
		"executor":        "antigravity",
		"target_provider": "claude",
		"action":          "drop_thinking_blocks",
		"stage":           stage,
		"reason":          reason,
		"count":           removed,
	}).Debug("antigravity executor: dropped Claude thinking blocks with invalid signatures")
}

func tokenExpiry(metadata map[string]any) time.Time {
	if metadata == nil {
		return time.Time{}
	}
	if expStr, ok := metadata["expired"].(string); ok {
		expStr = strings.TrimSpace(expStr)
		if expStr != "" {
			if parsed, errParse := time.Parse(time.RFC3339, expStr); errParse == nil {
				return parsed
			}
		}
	}
	expiresIn, hasExpires := int64Value(metadata["expires_in"])
	tsMs, hasTimestamp := int64Value(metadata["timestamp"])
	if hasExpires && hasTimestamp {
		return time.Unix(0, tsMs*int64(time.Millisecond)).Add(time.Duration(expiresIn) * time.Second)
	}
	return time.Time{}
}

func metaStringValue(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	if v, ok := metadata[key]; ok {
		switch typed := v.(type) {
		case string:
			return strings.TrimSpace(typed)
		case []byte:
			return strings.TrimSpace(string(typed))
		}
	}
	return ""
}

func int64Value(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return int64(typed), true
	case json.Number:
		if i, errParse := typed.Int64(); errParse == nil {
			return i, true
		}
	case string:
		if strings.TrimSpace(typed) == "" {
			return 0, false
		}
		if i, errParse := strconv.ParseInt(strings.TrimSpace(typed), 10, 64); errParse == nil {
			return i, true
		}
	}
	return 0, false
}

func buildBaseURL(auth *cliproxyauth.Auth) string {
	if baseURLs := antigravityBaseURLFallbackOrder(auth); len(baseURLs) > 0 {
		return baseURLs[0]
	}
	return antigravityBaseURLDaily
}

func antigravityLoadCodeAssistBaseURL(auth *cliproxyauth.Auth) string {
	if base := resolveCustomAntigravityBaseURL(auth); base != "" {
		return base
	}
	return antigravityBaseURLProd
}

func resolveHost(base string) string {
	parsed, errParse := url.Parse(base)
	if errParse != nil {
		return ""
	}
	if parsed.Host != "" {
		return parsed.Host
	}
	return strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://")
}

func resolveUserAgent(auth *cliproxyauth.Auth) string {
	return misc.AntigravityRequestUserAgent(antigravityConfiguredUserAgent(auth))
}

func resolveLoadCodeAssistUserAgent(auth *cliproxyauth.Auth) string {
	return misc.AntigravityLoadCodeAssistUserAgent(antigravityConfiguredUserAgent(auth))
}

func antigravityConfiguredUserAgent(auth *cliproxyauth.Auth) string {
	raw := ""
	if auth != nil {
		if auth.Attributes != nil {
			if ua := strings.TrimSpace(auth.Attributes["user_agent"]); ua != "" {
				raw = ua
			}
		}
		if raw == "" && auth.Metadata != nil {
			if ua, ok := auth.Metadata["user_agent"].(string); ok && strings.TrimSpace(ua) != "" {
				raw = strings.TrimSpace(ua)
			}
		}
	}
	return raw
}

var antigravityBaseURLFallbackOrder = func(auth *cliproxyauth.Auth) []string {
	if base := resolveCustomAntigravityBaseURL(auth); base != "" {
		return []string{base}
	}
	return []string{
		antigravityBaseURLDaily,
		antigravityBaseURLProd,
		// antigravitySandboxBaseURLDaily,
	}
}

func resolveCustomAntigravityBaseURL(auth *cliproxyauth.Auth) string {
	if auth == nil {
		return ""
	}
	if auth.Attributes != nil {
		if v := strings.TrimSpace(auth.Attributes["base_url"]); v != "" {
			return strings.TrimSuffix(v, "/")
		}
	}
	if auth.Metadata != nil {
		if v, ok := auth.Metadata["base_url"].(string); ok {
			v = strings.TrimSpace(v)
			if v != "" {
				return strings.TrimSuffix(v, "/")
			}
		}
	}
	return ""
}

func geminiToAntigravity(modelName string, payload []byte, projectID string) []byte {
	template := payload
	template, _ = sjson.SetBytes(template, "model", modelName)
	template, _ = sjson.SetBytes(template, "userAgent", "antigravity")

	isImageModel := strings.Contains(modelName, "image")

	var reqType string
	if isImageModel {
		reqType = "image_gen"
	} else {
		reqType = "agent"
	}
	template, _ = sjson.SetBytes(template, "requestType", reqType)

	if projectID != "" {
		template, _ = sjson.SetBytes(template, "project", projectID)
	} else {
		template, _ = sjson.DeleteBytes(template, "project")
	}

	if isImageModel {
		template, _ = sjson.SetBytes(template, "requestId", generateImageGenRequestID())
	} else {
		template, _ = sjson.SetBytes(template, "requestId", generateRequestID())
		template, _ = sjson.SetBytes(template, "request.sessionId", generateStableSessionID(payload))
	}

	template, _ = sjson.DeleteBytes(template, "request.safetySettings")
	if toolConfig := gjson.GetBytes(template, "toolConfig"); toolConfig.Exists() && !gjson.GetBytes(template, "request.toolConfig").Exists() {
		template, _ = sjson.SetRawBytes(template, "request.toolConfig", []byte(toolConfig.Raw))
		template, _ = sjson.DeleteBytes(template, "toolConfig")
	}
	return template
}

func generateRequestID() string {
	return "agent-" + uuid.NewString()
}

func generateImageGenRequestID() string {
	return fmt.Sprintf("image_gen/%d/%s/12", time.Now().UnixMilli(), uuid.NewString())
}

func generateSessionID() string {
	randSourceMutex.Lock()
	n := randSource.Int63n(9_000_000_000_000_000_000)
	randSourceMutex.Unlock()
	return "-" + strconv.FormatInt(n, 10)
}

func generateStableSessionID(payload []byte) string {
	contents := gjson.GetBytes(payload, "request.contents")
	if contents.IsArray() {
		for _, content := range contents.Array() {
			if content.Get("role").String() == "user" {
				text := content.Get("parts.0.text").String()
				if text != "" {
					h := sha256.Sum256([]byte(text))
					n := int64(binary.BigEndian.Uint64(h[:8])) & 0x7FFFFFFFFFFFFFFF
					return "-" + strconv.FormatInt(n, 10)
				}
			}
		}
	}
	return generateSessionID()
}

func antigravityProjectIDFromAuth(auth *cliproxyauth.Auth) string {
	if auth == nil || auth.Metadata == nil {
		return ""
	}
	if pid, ok := auth.Metadata["project_id"].(string); ok {
		return strings.TrimSpace(pid)
	}
	return ""
}

func missingAntigravityProjectIDError(cause error) statusErr {
	msg := "antigravity auth missing project_id"
	if cause != nil {
		msg = fmt.Sprintf("%s: %v", msg, cause)
	}
	return statusErr{code: http.StatusBadRequest, msg: msg}
}

func antigravityRequestNeedsSchemaSanitization(payload []byte) bool {
	if gjson.GetBytes(payload, "request.tools.0").Exists() {
		return true
	}
	if gjson.GetBytes(payload, "request.generationConfig.responseJsonSchema").Exists() {
		return true
	}
	if gjson.GetBytes(payload, "request.generationConfig.responseSchema").Exists() {
		return true
	}
	return false
}
