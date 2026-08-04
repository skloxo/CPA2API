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
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
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
	cfg        *config.Config
	mu         sync.Mutex
	cancel     context.CancelFunc
	token      string
	cookie     string
	proxyURL   string
	running    bool
	lastModels []*registry.ModelInfo
}

var (
	qwenModelDiscoveryInstance *QwenModelDiscovery
	qwenModelDiscoveryOnce     sync.Once
)

// GetQwenModelDiscovery returns the singleton QwenModelDiscovery instance and updates its cfg pointer.
func GetQwenModelDiscovery(cfg *config.Config) *QwenModelDiscovery {
	qwenModelDiscoveryOnce.Do(func() {
		qwenModelDiscoveryInstance = &QwenModelDiscovery{}
	})
	qwenModelDiscoveryInstance.mu.Lock()
	qwenModelDiscoveryInstance.cfg = cfg
	qwenModelDiscoveryInstance.mu.Unlock()
	return qwenModelDiscoveryInstance
}

// NewQwenModelDiscovery creates a new model discovery instance.
func NewQwenModelDiscovery(cfg *config.Config) *QwenModelDiscovery {
	return GetQwenModelDiscovery(cfg)
}

// SetCredentials sets the authentication token and cookie for API requests.
// On the first call with a non-empty token, it automatically triggers
// model discovery so that the model list is populated without requiring
// an explicit Start() call.
func (d *QwenModelDiscovery) SetCredentials(token, cookie, proxyURL string) {
	d.mu.Lock()
	d.token = token
	d.cookie = cookie
	d.proxyURL = proxyURL
	d.mu.Unlock()

	// Auto-start model discovery on first valid credential
	if strings.TrimSpace(token) != "" {
		d.Start(context.Background())
	}
}

// Start begins the periodic model discovery loop. Safe to call multiple times;
// only the first call takes effect.
func (d *QwenModelDiscovery) Start(ctx context.Context) {
	d.mu.Lock()
	if d.running {
		d.mu.Unlock()
		return
	}
	d.running = true
	ctx, cancel := context.WithCancel(ctx)
	d.cancel = cancel
	d.mu.Unlock()

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
}

// Stop stops the periodic refresh.
func (d *QwenModelDiscovery) Stop() {
	d.mu.Lock()
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	d.running = false
	d.mu.Unlock()
}

// ClearCredentials clears all active token and cookie configurations.
func (d *QwenModelDiscovery) ClearCredentials() {
	d.mu.Lock()
	d.token = ""
	d.cookie = ""
	d.proxyURL = ""
	d.lastModels = nil
	d.mu.Unlock()
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

// ForceRefresh forces a synchronous refresh of Qwen models.
func (d *QwenModelDiscovery) ForceRefresh(ctx context.Context) {
	d.refreshModels(ctx)
}

// refreshModels fetches models from Qwen and registers them with the global registry.
func (d *QwenModelDiscovery) refreshModels(ctx context.Context) {
	d.mu.Lock()
	token := d.token
	cookie := d.cookie
	proxyURL := d.proxyURL
	cfg := d.cfg
	d.mu.Unlock()

	models, err := d.fetchModels(ctx, token, cookie, proxyURL)
	if err != nil {
		log.Warnf("qwen model discovery: fetch failed: %v", err)
		// If we have previously discovered models, keep them
		return
	}

	if len(models) == 0 {
		log.Warnf("qwen model discovery: no models returned from API")
		return
	}

	// Register completely raw, unfiltered models with the registry for `/model-definitions/qwen`
	registry.RegisterDiscoveredModels("qwen", models)

	models = applyQwenModelAlias(cfg, models)
	models = filterQwenModelsByConfiguredWhitelist(cfg, models)

	// Filter out excluded Qwen models based on OAuthExcludedModels
	if cfg != nil && len(cfg.OAuthExcludedModels) > 0 {
		excluded := cfg.OAuthExcludedModels["qwen"]
		if len(excluded) > 0 {
			filtered := make([]*registry.ModelInfo, 0, len(models))
			for _, m := range models {
				modelID := strings.ToLower(strings.TrimSpace(m.ID))
				blocked := false
				for _, pattern := range excluded {
					trimmedPattern := strings.ToLower(strings.TrimSpace(pattern))
					if trimmedPattern != "" && matchWildcard(trimmedPattern, modelID) {
						blocked = true
						break
					}
				}
				if !blocked {
					filtered = append(filtered, m)
				}
			}
			models = filtered
		}
	}

	d.mu.Lock()
	d.lastModels = models
	d.mu.Unlock()

	// Register with global registry only if user has not set explicit model restrictions.
	// If explicit models are configured, unregister qwen-dynamic so only exact user models are exposed.
	if hasExplicitQwenConfiguredModels(cfg) {
		registry.GetGlobalRegistry().UnregisterClient(qwenModelsClientID)
		log.Infof("qwen model discovery: restricted to %d configured models, qwen-dynamic un-registered", len(models))
	} else {
		registry.GetGlobalRegistry().RegisterClient(qwenModelsClientID, "qwen", models)
		log.Infof("qwen model discovery: registered %d default models", len(models))
	}
}

func hasExplicitQwenConfiguredModels(cfg *config.Config) bool {
	if cfg == nil || len(cfg.OpenAICompatibility) == 0 {
		return false
	}
	for i := range cfg.OpenAICompatibility {
		compat := &cfg.OpenAICompatibility[i]
		if compat.Disabled {
			continue
		}
		if strings.EqualFold(compat.Name, "qwen") || strings.EqualFold(compat.BaseURL, "qwen") {
			if len(compat.Models) > 0 {
				return true
			}
		}
	}
	return false
}

// filterQwenModelsByConfiguredWhitelist filters Qwen models if the user configured an explicit models list
// for the Qwen OpenAICompatibility entry (e.g. only qwen3.7-plus and qwen3.8-max).
func filterQwenModelsByConfiguredWhitelist(cfg *config.Config, models []*registry.ModelInfo) []*registry.ModelInfo {
	if cfg == nil || len(cfg.OpenAICompatibility) == 0 {
		return models
	}

	var targetModels []config.OpenAICompatibilityModel
	for i := range cfg.OpenAICompatibility {
		compat := &cfg.OpenAICompatibility[i]
		if compat.Disabled {
			continue
		}
		if strings.EqualFold(compat.Name, "qwen") || strings.EqualFold(compat.BaseURL, "qwen") {
			if len(compat.Models) > 0 {
				targetModels = compat.Models
				break
			}
		}
	}

	if len(targetModels) == 0 {
		return models
	}

	allowedMap := make(map[string]struct{}, len(targetModels))
	for _, m := range targetModels {
		alias := strings.TrimSpace(m.Alias)
		name := strings.TrimSpace(m.Name)
		if alias != "" {
			allowedMap[strings.ToLower(alias)] = struct{}{}
		}
		if name != "" {
			allowedMap[strings.ToLower(name)] = struct{}{}
		}
	}

	filtered := make([]*registry.ModelInfo, 0, len(targetModels))
	seen := make(map[string]bool)

	// Filter discovered models against allowed map
	for _, m := range models {
		if m == nil {
			continue
		}
		id := strings.ToLower(strings.TrimSpace(m.ID))
		if _, ok := allowedMap[id]; ok {
			filtered = append(filtered, m)
			seen[id] = true
		}
	}

	// Ensure any specified model in targetModels is present even if not in discovery
	now := time.Now().Unix()
	for _, m := range targetModels {
		modelID := strings.TrimSpace(m.Alias)
		if modelID == "" {
			modelID = strings.TrimSpace(m.Name)
		}
		if modelID == "" {
			continue
		}
		key := strings.ToLower(modelID)
		if !seen[key] {
			filtered = append(filtered, &registry.ModelInfo{
				ID:          modelID,
				Object:      "model",
				Created:     now,
				OwnedBy:     "qwen",
				Type:        "openai-compatibility",
				DisplayName: modelID,
			})
			seen[key] = true
		}
	}

	return filtered
}

// applyQwenModelAlias filters or renames Qwen models using OAuthModelAlias.
func applyQwenModelAlias(cfg *config.Config, models []*registry.ModelInfo) []*registry.ModelInfo {
	if cfg == nil || len(models) == 0 {
		return models
	}
	if len(cfg.OAuthModelAlias) == 0 {
		return models
	}
	aliases := cfg.OAuthModelAlias["qwen"]
	if len(aliases) == 0 {
		return models
	}

	type aliasEntry struct {
		alias string
		fork  bool
	}

	forward := make(map[string][]aliasEntry, len(aliases))
	for i := range aliases {
		name := strings.TrimSpace(aliases[i].Name)
		alias := strings.TrimSpace(aliases[i].Alias)
		if name == "" || alias == "" {
			continue
		}
		if strings.EqualFold(name, alias) {
			continue
		}
		key := strings.ToLower(name)
		forward[key] = append(forward[key], aliasEntry{alias: alias, fork: aliases[i].Fork})
	}
	if len(forward) == 0 {
		return models
	}

	out := make([]*registry.ModelInfo, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		if model == nil {
			continue
		}
		id := strings.TrimSpace(model.ID)
		if id == "" {
			continue
		}
		key := strings.ToLower(id)
		entries := forward[key]
		if len(entries) == 0 {
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, model)
			continue
		}

		keepOriginal := false
		for _, entry := range entries {
			if entry.fork {
				keepOriginal = true
				break
			}
		}
		if keepOriginal {
			if _, exists := seen[key]; !exists {
				seen[key] = struct{}{}
				out = append(out, model)
			}
		}

		addedAlias := false
		for _, entry := range entries {
			mappedID := strings.TrimSpace(entry.alias)
			if mappedID == "" {
				continue
			}
			if strings.EqualFold(mappedID, id) {
				continue
			}
			aliasKey := strings.ToLower(mappedID)
			if _, exists := seen[aliasKey]; exists {
				continue
			}
			seen[aliasKey] = struct{}{}
			clone := *model
			clone.ID = mappedID
			if clone.DisplayName != "" {
				clone.DisplayName = qwenModelDisplayName(mappedID)
			}
			out = append(out, &clone)
			addedAlias = true
		}

		if !keepOriginal && !addedAlias {
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, model)
		}
	}
	return out
}

// fetchModels fetches available models from Qwen's API.
func (d *QwenModelDiscovery) fetchModels(ctx context.Context, token, cookie, proxyURL string) ([]*registry.ModelInfo, error) {
	var client *http.Client
	if strings.TrimSpace(proxyURL) != "" || (d.cfg != nil && strings.TrimSpace(d.cfg.ProxyURL) != "") {
		var authObj *cliproxyauth.Auth
		if strings.TrimSpace(proxyURL) != "" {
			authObj = &cliproxyauth.Auth{ProxyURL: proxyURL}
		}
		client = helps.NewProxyAwareHTTPClient(ctx, d.cfg, authObj, qwenModelsFetchTimeout)
	} else {
		client = &http.Client{Timeout: qwenModelsFetchTimeout}
	}

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
	case strings.HasPrefix(lower, "qwen3.7"):
		return 131072
	case strings.HasPrefix(lower, "qwen3.6"):
		return 131072
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

func matchWildcard(pattern, value string) bool {
	if pattern == "" {
		return false
	}
	if !strings.Contains(pattern, "*") {
		return pattern == value
	}
	parts := strings.Split(pattern, "*")
	if prefix := parts[0]; prefix != "" {
		if !strings.HasPrefix(value, prefix) {
			return false
		}
		value = value[len(prefix):]
	}
	if suffix := parts[len(parts)-1]; suffix != "" {
		if !strings.HasSuffix(value, suffix) {
			return false
		}
		value = value[:len(value)-len(suffix)]
	}
	for i := 1; i < len(parts)-1; i++ {
		part := parts[i]
		if part == "" {
			continue
		}
		idx := strings.Index(value, part)
		if idx < 0 {
			return false
		}
		value = value[idx+len(part):]
	}
	return true
}

// ProbeQwenAuthHeartbeat sends a 0-cost GET request to /api/v2/user/info to refresh session cookies.
// It will not consume tokens or create any chat sessions on Qwen's backend.
func ProbeQwenAuthHeartbeat(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth) bool {
	if auth == nil {
		return false
	}
	token, cookie := qwenCreds(auth)
	if token == "" && cookie == "" {
		return false
	}

	reqURL := "https://chat.qwen.ai/api/v2/user/info"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return false
	}

	qwenauth.ApplyAllQwenHeaders(req, token, cookie, false)
	req.Header.Set("User-Agent", helps.GetRandomUserAgent())

	client := helps.NewUtlsHTTPClient(ctx, cfg, auth, 10*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		log.Debugf("qwen heartbeat probe error for auth %s: %v", auth.ID, err)
		return false
	}
	defer resp.Body.Close()

	if resp.StatusCode == 200 {
		log.Debugf("qwen heartbeat probe succeeded for auth %s (session refreshed)", auth.ID)
		return true
	} else if resp.StatusCode == 401 || resp.StatusCode == 429 {
		log.Warnf("qwen heartbeat probe received status %d for auth %s", resp.StatusCode, auth.ID)
		if auth.ID != "" {
			registry.GetGlobalRegistry().SetModelQuotaExceeded(auth.ID, "")
		}
		return false
	}
	return false
}
