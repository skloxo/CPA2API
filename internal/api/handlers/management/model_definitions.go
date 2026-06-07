package management

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
)

// GetStaticModelDefinitions returns static model metadata for a given channel.
// Channel is provided via path param (:channel) or query param (?channel=...).
func (h *Handler) GetStaticModelDefinitions(c *gin.Context) {
	channel := strings.TrimSpace(c.Param("channel"))
	if channel == "" {
		channel = strings.TrimSpace(c.Query("channel"))
	}
	if channel == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "channel is required"})
		return
	}

	chanKey := strings.ToLower(strings.TrimSpace(channel))

	if chanKey == "qwen" {
		// Try to find active Qwen credentials to trigger a real-time fetch from Qwen API
		var token, cookie, proxyURL string
		if h.authManager != nil {
			for _, a := range h.authManager.List() {
				if a != nil && strings.EqualFold(a.Provider, "qwen") && !a.Disabled {
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
					if token != "" {
						proxyURL = a.ProxyURL
						if proxyURL == "" && a.Metadata != nil {
							if p, ok := a.Metadata["proxy_url"].(string); ok {
								proxyURL = p
							} else if p, ok := a.Metadata["proxy"].(string); ok {
								proxyURL = p
							}
						}
						break
					}
				}
			}
		}

		if token == "" && h.cfg != nil && h.cfg.AuthDir != "" {
			entries, _ := os.ReadDir(h.cfg.AuthDir)
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".json") {
					continue
				}
				data, err := os.ReadFile(filepath.Join(h.cfg.AuthDir, e.Name()))
				if err == nil {
					var cred struct {
						Type        string `json:"type"`
						AccessToken string `json:"access_token"`
						Cookie      string `json:"cookie"`
						Disabled    bool   `json:"disabled"`
						ProxyURL    string `json:"proxy_url"`
						Proxy       string `json:"proxy"`
					}
					if json.Unmarshal(data, &cred) == nil {
						if strings.EqualFold(cred.Type, "qwen") && !cred.Disabled && cred.AccessToken != "" {
							token = cred.AccessToken
							cookie = cred.Cookie
							proxyURL = cred.ProxyURL
							if proxyURL == "" {
								proxyURL = cred.Proxy
							}
							break
						}
					}
				}
			}
		}

		if token != "" {
			disc := executor.GetQwenModelDiscovery(h.cfg)
			disc.SetCredentials(token, cookie, proxyURL)
			disc.ForceRefresh(c.Request.Context())
		}
	}

	models := registry.GetStaticModelDefinitionsByChannel(channel)
	if models == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown channel", "channel": channel})
		return
	}

	if chanKey == "qwen" {
		clonedModels := make([]*registry.ModelInfo, len(models))
		for i, m := range models {
			if m != nil {
				clone := *m
				clone.DisplayName = m.ID
				clonedModels[i] = &clone
			}
		}
		models = clonedModels
	}

	c.JSON(http.StatusOK, gin.H{
		"channel": chanKey,
		"models":  models,
	})
}
