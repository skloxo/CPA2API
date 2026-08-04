package qwen

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQwenTokenStorage_ProxyURLNotSerialized(t *testing.T) {
	storage := &QwenTokenStorage{
		AccessToken: "test-token",
		Email:       "test@example.com",
		Password:    "test-password",
		ProxyURL:    "http://proxy.example.com:8080",
		Type:        "qwen",
	}

	data, err := json.Marshal(storage)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	// ProxyURL should NOT be in the JSON output (json:"-")
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	if _, ok := m["proxy_url"]; ok {
		t.Fatal("proxy_url should not be serialized in JSON (json:\"-\")")
	}

	// Verify other fields are present
	if m["access_token"] != "test-token" {
		t.Fatalf("access_token = %v, want test-token", m["access_token"])
	}
	if m["email"] != "test@example.com" {
		t.Fatalf("email = %v, want test@example.com", m["email"])
	}
}

func TestQwenTokenStorage_BackwardCompatibility(t *testing.T) {
	// Simulate old credential file with proxy_url
	oldJSON := `{
		"access_token": "old-token",
		"email": "old@example.com",
		"expired": "2026-12-31T23:59:59Z",
		"password": "old-password",
		"proxy_url": "http://old-proxy:8080",
		"type": "qwen"
	}`

	var storage QwenTokenStorage
	if err := json.Unmarshal([]byte(oldJSON), &storage); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	// proxy_url should be ignored (json:"-")
	if storage.ProxyURL != "" {
		t.Fatalf("ProxyURL should be empty (ignored), got %q", storage.ProxyURL)
	}

	// Other fields should be parsed correctly
	if storage.AccessToken != "old-token" {
		t.Fatalf("AccessToken = %q, want old-token", storage.AccessToken)
	}
	if storage.Email != "old@example.com" {
		t.Fatalf("Email = %q, want old@example.com", storage.Email)
	}
	if storage.Password != "old-password" {
		t.Fatalf("Password = %q, want old-password", storage.Password)
	}
}

func TestQwenTokenStorage_SaveTokenToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-credential.json")

	storage := &QwenTokenStorage{
		AccessToken: "save-test-token",
		Email:       "save@example.com",
		Password:    "save-password",
		ProxyURL:    "http://should-not-be-saved:8080",
	}

	if err := storage.SaveTokenToFile(path); err != nil {
		t.Fatalf("SaveTokenToFile failed: %v", err)
	}

	// Read and verify the file
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile failed: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	// proxy_url should NOT be in the file
	if _, ok := m["proxy_url"]; ok {
		t.Fatal("proxy_url should not be in saved file")
	}

	// Verify type is set
	if m["type"] != "qwen" {
		t.Fatalf("type = %v, want qwen", m["type"])
	}
}

func TestQwenTokenStorage_IsExpired(t *testing.T) {
	tests := []struct {
		name    string
		expired string
		want    bool
	}{
		{"empty means valid", "", false},
		{"future time is not expired", time.Now().Add(1 * time.Hour).Format(time.RFC3339), false},
		{"past time is expired", time.Now().Add(-1 * time.Hour).Format(time.RFC3339), true},
		{"within 5 min is expired", time.Now().Add(3 * time.Minute).Format(time.RFC3339), true},
		{"invalid format is expired", "not-a-time", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storage := &QwenTokenStorage{Expired: tt.expired}
			if got := storage.IsExpired(); got != tt.want {
				t.Fatalf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCredentialFileName(t *testing.T) {
	tests := []struct {
		name  string
		email string
		want  string
	}{
		{"normal email", "user@example.com", "qwen-userexample.com.json"},
		{"email with special chars", "user+tag@example.com", "qwen-usertagexample.com.json"},
		{"empty email generates timestamp", "", "qwen-"}, // prefix check
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CredentialFileName(tt.email)
			if tt.email == "" {
				// Empty email generates timestamp-based name
				if len(got) < len(tt.want) || got[:len(tt.want)] != tt.want {
					t.Fatalf("CredentialFileName(%q) = %q, want prefix %q", tt.email, got, tt.want)
				}
			} else {
				if got != tt.want {
					t.Fatalf("CredentialFileName(%q) = %q, want %q", tt.email, got, tt.want)
				}
			}
		})
	}
}

func TestSanitizeEmail(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"user@example.com", "userexample.com"},
		{"test+tag@gmail.com", "testtaggmail.com"},
		{"normal-user", "normal-user"},
		{"User.Name@Domain.COM", "User.NameDomain.COM"}, // dot is kept, @ is removed
		{"123@456", "123456"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeEmail(tt.input)
			if got != tt.want {
				t.Fatalf("sanitizeEmail(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSetMetadata(t *testing.T) {
	storage := &QwenTokenStorage{}

	meta := map[string]any{
		"key1": "value1",
		"key2": 42,
	}

	storage.SetMetadata(meta)

	if storage.Metadata == nil {
		t.Fatal("Metadata should not be nil after SetMetadata")
	}
	if storage.Metadata["key1"] != "value1" {
		t.Fatalf("Metadata[key1] = %v, want value1", storage.Metadata["key1"])
	}
	if storage.Metadata["key2"] != 42 {
		t.Fatalf("Metadata[key2] = %v, want 42", storage.Metadata["key2"])
	}
}
