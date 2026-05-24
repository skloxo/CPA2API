// Package executor provides dynamic model discovery for Qwen API.
//
// This module fetches available models from Qwen's /api/models endpoint,
// converts them to CPA ModelInfo format, and registers them with the
// global model registry. It supports periodic refresh.
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	qwenauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/qwen"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	log "github.com/sirupsen/logrus"
)

const (
	qwenModelsFetchTimeout    = 15 * time.Second
	qwenModelsRefreshInterval = 3 * time.Hour
	qwenModelsClientID        = "qwen-dynamic"
)

// qwenModelsResponse represents the response from Qwen's /api/models endpoint.
type qwenModelsResponse struct {
	Data []qwenModelEntry `json:"data"`
}

// qwenModelEntry represents a single model from Qwen's API.
type qwenModelEntry struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
	Info    struct {
		Meta struct {
			ChatType []string `json:"chat_type"`
		} `json:"meta"`
	} `json:"info"`
}

// QwenModelDiscovery manages dynamic model discovery from Qwen's API.
type QwenModelDiscovery struct {
	cfg         *config.Config
	mu          sync.Mutex
	cancel      context.CancelFunc
	token       string
	cookie      string
	refreshOnce sync.Once
	lastModels  []*registry.ModelInfo
}

// NewQwenModelDiscovery creates a new model discovery instance.
func NewQwenModelDiscovery(cfg *config.Config) *QwenModelDiscovery {
	return &QwenModelDiscovery{cfg: cfg}
}

// SetCredentials sets the authentication token and cookie for API requests.
// On the first call with a non-empty token, it automatically triggers
// model discovery so that the model list is populated without requiring
// an explicit Start() call.
func (d *QwenModelDiscovery) SetCredentials(token, cookie string) {
	d.mu.Lock()
	d.token = token
	d.cookie = cookie
	d.mu.Unlock()

	// Auto-start model discovery on first valid credential
	if strings.TrimSpace(token) != "" {
		d.Start(context.Background())
	}
}

// Start begins the periodic model discovery loop. Safe to call multiple times;
// only the first call takes effect.
func (d *QwenModelDiscovery) Start(ctx context.Context) {
	d.refreshOnce.Do(func() {
		ctx, cancel := context.WithCancel(ctx)
		d.cancel = cancel

		// Initial fetch
		d.refreshModels(ctx)

		// Periodic refresh
		go func() {
			ticker := time.NewTicker(qwenModelsRefreshInterval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					d.refreshModels(ctx)
				}
			}
		}()
	})
}

// Stop stops the periodic refresh.
func (d *QwenModelDiscovery) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
}

// GetDiscoveredModels returns the last successfully discovered models.
func (d *QwenModelDiscovery) GetDiscoveredModels() []*registry.ModelInfo {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.lastModels) == 0 {
		return nil
	}
	out := make([]*registry.ModelInfo, len(d.lastModels))
	copy(out, d.lastModels)
	return out
}

// refreshModels fetches models from Qwen and registers them with the global registry.
func (d *QwenModelDiscovery) refreshModels(ctx context.Context) {
	d.mu.Lock()
	token := d.token
	cookie := d.cookie
	d.mu.Unlock()

	models, err := d.fetchModels(ctx, token, cookie)
	if err != nil {
		log.Warnf("qwen model discovery: fetch failed: %v", err)
		// If we have previously discovered models, keep them
		return
	}

	if len(models) == 0 {
		log.Warnf("qwen model discovery: no models returned from API")
		return
	}

	d.mu.Lock()
	d.lastModels = models
	d.mu.Unlock()

	// Register with global registry
	registry.GetGlobalRegistry().RegisterClient(qwenModelsClientID, "qwen", models)
	log.Infof("qwen model discovery: registered %d models", len(models))
}

// fetchModels fetches available models from Qwen's API.
func (d *QwenModelDiscovery) fetchModels(ctx context.Context, token, cookie string) ([]*registry.ModelInfo, error) {
	client := &http.Client{Timeout: qwenModelsFetchTimeout}

	reqURL := qwenauth.QwenAPIBaseURL + "/api/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	qwenauth.ApplyBrowserHeaders(req, false)
	if strings.TrimSpace(token) != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if strings.TrimSpace(cookie) != "" {
		req.Header.Set("Cookie", cookie)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch models: %w", err)
	}
	decodedBody, err := decodeResponseBody(resp.Body, resp.Header.Get("Content-Encoding"))
	if err != nil {
		resp.Body.Close()
		return nil, fmt.Errorf("decode response: %w", err)
	}
	defer decodedBody.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(decodedBody)
		return nil, fmt.Errorf("models API returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(decodedBody)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var apiResp qwenModelsResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	return convertQwenModelsToRegistry(apiResp.Data), nil
}

// convertQwenModelsToRegistry converts Qwen API models to CPA ModelInfo format.
func convertQwenModelsToRegistry(entries []qwenModelEntry) []*registry.ModelInfo {
	out := make([]*registry.ModelInfo, 0, len(entries))
	for _, entry := range entries {
		if strings.TrimSpace(entry.ID) == "" {
			continue
		}

		info := &registry.ModelInfo{
			ID:      entry.ID,
			Object:  "model",
			OwnedBy: "qwen",
			Type:    "qwen",
		}
		if entry.Created > 0 {
			info.Created = entry.Created
		}
		if strings.TrimSpace(entry.OwnedBy) != "" {
			info.OwnedBy = entry.OwnedBy
		}

		// Set display name based on model ID
		info.DisplayName = qwenModelDisplayName(entry.ID)

		// Determine capabilities from chat_type metadata
		chatTypes := entry.Info.Meta.ChatType
		for _, ct := range chatTypes {
			switch ct {
			case "t2i", "image_edit":
				info.SupportedInputModalities = appendIfMissing(info.SupportedInputModalities, "TEXT")
				info.SupportedOutputModalities = appendIfMissing(info.SupportedOutputModalities, "IMAGE")
			case "t2v":
				info.SupportedInputModalities = appendIfMissing(info.SupportedInputModalities, "TEXT")
				info.SupportedOutputModalities = appendIfMissing(info.SupportedOutputModalities, "VIDEO")
			}
		}

		// Set context length hints based on model name
		info.ContextLength = qwenModelContextLength(entry.ID)

		out = append(out, info)
	}
	return out
}

// qwenModelDisplayName returns a human-readable display name for a Qwen model ID.
func qwenModelDisplayName(id string) string {
	// Return original ID directly in this version.
	// We preserve the prefix mapping logic for future iteration plans.
	return id

	/*
		switch {
		case strings.HasPrefix(id, "qwen3-max"):
			return "Qwen3 Max"
		case strings.HasPrefix(id, "qwen3-plus"):
			return "Qwen3 Plus"
		case strings.HasPrefix(id, "qwen3-turbo"):
			return "Qwen3 Turbo"
		case strings.HasPrefix(id, "qwen-max"):
			return "Qwen Max"
		case strings.HasPrefix(id, "qwen-plus"):
			return "Qwen Plus"
		case strings.HasPrefix(id, "qwen-turbo"):
			return "Qwen Turbo"
		case strings.HasPrefix(id, "qwq-max"):
			return "QwQ Max"
		case strings.HasPrefix(id, "qwq-plus"):
			return "QwQ Plus"
		case strings.HasPrefix(id, "qwen-long"):
			return "Qwen Long"
		case strings.HasPrefix(id, "qwen-vl-max"):
			return "Qwen VL Max"
		case strings.HasPrefix(id, "qwen-vl-plus"):
			return "Qwen VL Plus"
		case strings.HasPrefix(id, "qwen-audio"):
			return "Qwen Audio Turbo"
		case strings.HasPrefix(id, "qwen-coder-plus"):
			return "Qwen Coder Plus"
		case strings.HasPrefix(id, "qwen-coder-turbo"):
			return "Qwen Coder Turbo"
		default:
			return id
		}
	*/
}

// qwenModelContextLength returns the context length for known model families.
func qwenModelContextLength(id string) int {
	lower := strings.ToLower(id)
	switch {
	case strings.HasPrefix(lower, "qwen3-max"):
		return 131072
	case strings.HasPrefix(lower, "qwen3-plus"):
		return 131072
	case strings.HasPrefix(lower, "qwen3-turbo"):
		return 131072
	case strings.HasPrefix(lower, "qwen-max"):
		return 131072
	case strings.HasPrefix(lower, "qwen-plus"):
		return 131072
	case strings.HasPrefix(lower, "qwen-turbo"):
		return 131072
	case strings.HasPrefix(lower, "qwq-max"):
		return 131072
	case strings.HasPrefix(lower, "qwq-plus"):
		return 131072
	case strings.HasPrefix(lower, "qwen-long"):
		return 10000000
	case strings.HasPrefix(lower, "qwen-vl-max"):
		return 131072
	case strings.HasPrefix(lower, "qwen-vl-plus"):
		return 131072
	case strings.HasPrefix(lower, "qwen-coder"):
		return 131072
	default:
		return 32768
	}
}

func appendIfMissing(slice []string, item string) []string {
	for _, s := range slice {
		if s == item {
			return slice
		}
	}
	return append(slice, item)
}
