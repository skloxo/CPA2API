// Package qwen provides authentication and token management for Qwen (Alibaba Cloud) API.
// It handles email+password sign-in via the Qwen web authentication endpoint,
// returning JWT tokens for API access.
package qwen

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	// QwenAPIBaseURL is the base URL for Qwen API requests.
	QwenAPIBaseURL = "https://chat.qwen.ai"
	// qwenSignInURL is the authentication endpoint for Qwen sign-in.
	qwenSignInURL = QwenAPIBaseURL + "/api/v1/auths/signin"
)

// QwenAuthResult holds the authentication result from a Qwen sign-in.
type QwenAuthResult struct {
	// Token is the JWT access token returned by Qwen.
	Token string
	// Expired is the RFC3339 timestamp when the token expires, if determinable.
	Expired string
}

// QwenAuth handles Qwen authentication flows.
type QwenAuth struct {
	cfg *config.Config
}

// NewQwenAuth creates a new QwenAuth service instance.
func NewQwenAuth(cfg *config.Config) *QwenAuth {
	return &QwenAuth{cfg: cfg}
}

// SignIn authenticates with Qwen using email and password.
// The password is SHA256 hashed before sending to the API.
// An optional proxyURL parameter can be provided to override the default proxy.
func (q *QwenAuth) SignIn(ctx context.Context, email, password, proxyURL string) (*QwenAuthResult, error) {
	email = strings.TrimSpace(email)
	password = strings.TrimSpace(password)
	if email == "" {
		return nil, fmt.Errorf("qwen: email is required")
	}
	if password == "" {
		return nil, fmt.Errorf("qwen: password is required")
	}

	hashedPassword := sha256Hash(password)

	payload := map[string]string{
		"email":    email,
		"password": hashedPassword,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("qwen: failed to marshal sign-in payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, qwenSignInURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("qwen: failed to create sign-in request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/134.0.0.0 Safari/537.36 Edg/134.0.0.0")

	var authObj *cliproxyauth.Auth
	if strings.TrimSpace(proxyURL) != "" {
		authObj = &cliproxyauth.Auth{
			ProxyURL: strings.TrimSpace(proxyURL),
		}
	}
	httpClient := helps.NewUtlsHTTPClient(q.cfg, authObj, 15*time.Second)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("qwen: sign-in request failed: %w", err)
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.Errorf("qwen sign-in: close body error: %v", errClose)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("qwen: failed to read sign-in response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("qwen: sign-in failed with status %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		Token    string `json:"token"`
		Exp      int64  `json:"exp"`
		Email    string `json:"email"`
		Username string `json:"username"`
	}
	if err = json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("qwen: failed to parse sign-in response: %w", err)
	}

	if strings.TrimSpace(result.Token) == "" {
		return nil, fmt.Errorf("qwen: empty token in sign-in response")
	}

	authResult := &QwenAuthResult{
		Token: result.Token,
	}
	if result.Exp > 0 {
		authResult.Expired = time.Unix(result.Exp, 0).UTC().Format(time.RFC3339)
	}
	return authResult, nil
}

// sha256Hash computes the SHA256 hex digest of the input string.
func sha256Hash(input string) string {
	h := sha256.Sum256([]byte(input))
	return hex.EncodeToString(h[:])
}
