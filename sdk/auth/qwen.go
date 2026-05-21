// Package auth provides Qwen (Alibaba Cloud) authenticator for the CLI Proxy API SDK.
// It implements the Authenticator interface for email+password based authentication.
package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	qwenauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/qwen"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// qwenRefreshLead is the duration before token expiry when refresh should occur.
var qwenRefreshLead = 5 * time.Minute

// QwenAuthenticator implements the email+password login for Qwen (Alibaba Cloud).
type QwenAuthenticator struct{}

// NewQwenAuthenticator constructs a new Qwen authenticator.
func NewQwenAuthenticator() Authenticator {
	return &QwenAuthenticator{}
}

// Provider returns the provider key for qwen.
func (QwenAuthenticator) Provider() string {
	return "qwen"
}

// RefreshLead returns the duration before token expiry when refresh should occur.
func (QwenAuthenticator) RefreshLead() *time.Duration {
	return &qwenRefreshLead
}

// Login authenticates with Qwen using email and password.
// Credentials are obtained from opts.Metadata or via interactive prompt.
func (a QwenAuthenticator) Login(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}
	if opts == nil {
		opts = &LoginOptions{}
	}

	// Get credentials from metadata or prompt
	email := strings.TrimSpace(opts.Metadata["email"])
	password := strings.TrimSpace(opts.Metadata["password"])

	if email == "" && opts.Prompt != nil {
	inputEmail:
		input, err := opts.Prompt("Enter Qwen email: ")
		if err != nil {
			return nil, fmt.Errorf("qwen: failed to read email: %w", err)
		}
		email = strings.TrimSpace(input)
		if email == "" {
			goto inputEmail
		}
	}
	if password == "" && opts.Prompt != nil {
	inputPassword:
		input, err := opts.Prompt("Enter Qwen password: ")
		if err != nil {
			return nil, fmt.Errorf("qwen: failed to read password: %w", err)
		}
		password = strings.TrimSpace(input)
		if password == "" {
			goto inputPassword
		}
	}

	if email == "" || password == "" {
		return nil, fmt.Errorf("qwen: email and password are required")
	}

	// Authenticate with Qwen
	qwenAuthSvc := qwenauth.NewQwenAuth(cfg)
	result, err := qwenAuthSvc.SignIn(ctx, email, password)
	if err != nil {
		return nil, fmt.Errorf("qwen: sign-in failed: %w", err)
	}

	// Create token storage
	tokenStorage := &qwenauth.QwenTokenStorage{
		AccessToken: result.Token,
		Email:       email,
		Type:        "qwen",
	}
	if result.Expired != "" {
		tokenStorage.Expired = result.Expired
	}

	// Build metadata
	metadata := map[string]any{
		"type":         "qwen",
		"access_token": result.Token,
		"email":        email,
		// NOTE: Password is stored for token refresh. This is a security consideration.
		// Consider using a more secure credential storage mechanism in production.
		"password":  password,
		"timestamp": time.Now().UnixMilli(),
	}
	if result.Expired != "" {
		metadata["expired"] = result.Expired
	}

	// Generate filename
	fileName := qwenauth.CredentialFileName(email)

	log.Info("Qwen authentication successful!")

	return &coreauth.Auth{
		ID:       fileName,
		Provider: a.Provider(),
		FileName: fileName,
		Label:    email,
		Storage:  tokenStorage,
		Metadata: metadata,
	}, nil
}
