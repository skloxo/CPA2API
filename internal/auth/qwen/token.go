// Package qwen provides authentication and token management for Qwen (Alibaba Cloud) API.
// This file implements the TokenStorage interface for persisting Qwen credentials to disk.
package qwen

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/misc"
)

// QwenTokenStorage stores authentication information for Qwen API access.
// It persists the JWT access token, user email, and expiration metadata.
type QwenTokenStorage struct {
	// AccessToken is the JWT access token for Qwen API authentication.
	AccessToken string `json:"access_token"`
	// Email is the user's Qwen account email address.
	Email string `json:"email"`
	// Expired is the RFC3339 timestamp when the access token expires.
	Expired string `json:"expired,omitempty"`
	// Type indicates the authentication provider type, always "qwen" for this storage.
	Type string `json:"type"`

	// Metadata holds arbitrary key-value pairs injected via hooks.
	// It is not exported to JSON directly to allow flattening during serialization.
	Metadata map[string]any `json:"-"`
}

// SetMetadata allows external callers to inject metadata into the storage before saving.
func (ts *QwenTokenStorage) SetMetadata(meta map[string]any) {
	ts.Metadata = meta
}

// SaveTokenToFile serializes the Qwen token storage to a JSON file.
// It implements the auth.TokenStorage interface.
func (ts *QwenTokenStorage) SaveTokenToFile(authFilePath string) error {
	misc.LogSavingCredentials(authFilePath)
	ts.Type = "qwen"

	if err := os.MkdirAll(filepath.Dir(authFilePath), 0700); err != nil {
		return fmt.Errorf("qwen: failed to create directory: %w", err)
	}

	f, err := os.Create(authFilePath)
	if err != nil {
		return fmt.Errorf("qwen: failed to create token file: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	// Merge metadata using helper
	data, errMerge := misc.MergeMetadata(ts, ts.Metadata)
	if errMerge != nil {
		return fmt.Errorf("qwen: failed to merge metadata: %w", errMerge)
	}

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(data); err != nil {
		return fmt.Errorf("qwen: failed to write token to file: %w", err)
	}
	return nil
}

// IsExpired checks if the token has expired.
func (ts *QwenTokenStorage) IsExpired() bool {
	if ts.Expired == "" {
		return false // No expiry set, assume valid
	}
	t, err := time.Parse(time.RFC3339, ts.Expired)
	if err != nil {
		return true // Has expiry string but can't parse
	}
	// Consider expired if within 5 minutes of expiry
	return time.Now().Add(5 * time.Minute).After(t)
}

// CredentialFileName generates a filename for Qwen credentials based on the user email.
func CredentialFileName(email string) string {
	email = sanitizeEmail(email)
	if email == "" {
		return fmt.Sprintf("qwen-%d.json", time.Now().UnixMilli())
	}
	return fmt.Sprintf("qwen-%s.json", email)
}

// sanitizeEmail removes characters that are unsafe for filenames.
func sanitizeEmail(email string) string {
	result := make([]byte, 0, len(email))
	for i := 0; i < len(email); i++ {
		c := email[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '-' || c == '_' {
			result = append(result, c)
		}
	}
	return string(result)
}
