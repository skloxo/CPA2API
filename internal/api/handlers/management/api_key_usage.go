package management

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type apiKeyUsageEntry struct {
	Success        int64                          `json:"success"`
	Failed         int64                          `json:"failed"`
	RecentRequests []coreauth.RecentRequestBucket `json:"recent_requests"`
}

func mergeRecentRequestBuckets(dst, src []coreauth.RecentRequestBucket) []coreauth.RecentRequestBucket {
	if len(dst) == 0 {
		return src
	}
	if len(src) == 0 {
		return dst
	}
	if len(dst) != len(src) {
		n := len(dst)
		if len(src) < n {
			n = len(src)
		}
		for i := 0; i < n; i++ {
			dst[i].Success += src[i].Success
			dst[i].Failed += src[i].Failed
		}
		return dst
	}
	for i := range dst {
		dst[i].Success += src[i].Success
		dst[i].Failed += src[i].Failed
	}
	return dst
}

// GetAPIKeyUsage returns request buckets for all api_key auths, grouped by provider
// and keyed by "base_url|api_key". Success/Failed totals are the maximum of the
// current in-memory counters and the persisted SQLite totals, so the numbers survive
// process restarts without any data loss.
func (h *Handler) GetAPIKeyUsage(c *gin.Context) {
	if h == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}

	h.mu.Lock()
	manager := h.authManager
	h.mu.Unlock()
	if manager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "core auth manager unavailable"})
		return
	}

	// Load persisted SQLite totals and 24-hour hourly buckets keyed by auth_index.
	// This is a best-effort read; on failure (nil map) we gracefully fall back to memory-only data.
	sqliteTotals := h.loadSQLiteTotals(c.Request.Context())
	now := time.Now()
	currentBucketID := now.Unix() / 3600
	baseBucketID := currentBucketID - 23
	sqliteHourly := h.loadSQLiteHourlyBuckets(c.Request.Context(), baseBucketID)

	out := make(map[string]map[string]apiKeyUsageEntry)
	for _, auth := range manager.List() {
		if auth == nil {
			continue
		}
		kind, apiKey := auth.AccountInfo()
		if !strings.EqualFold(strings.TrimSpace(kind), "api_key") {
			continue
		}
		apiKey = strings.TrimSpace(apiKey)
		if apiKey == "" {
			continue
		}
		baseURL := ""
		if auth.Attributes != nil {
			baseURL = strings.TrimSpace(auth.Attributes["base_url"])
			if baseURL == "" {
				baseURL = strings.TrimSpace(auth.Attributes["base-url"])
			}
		}
		compositeKey := baseURL + "|" + apiKey
		provider := strings.ToLower(strings.TrimSpace(auth.Provider))
		if provider == "" {
			provider = "unknown"
		}

		recent := auth.RecentRequestsSnapshot(now)
		authIdx := auth.EnsureIndex()

		// Overlay SQLite 24 natural hour buckets
		if sqliteHourly != nil {
			if sqBuckets, ok := sqliteHourly[authIdx]; ok {
				for k := range recent {
					bID := currentBucketID - int64(len(recent)-1-k)
					if sqB, has := sqBuckets[bID]; has {
						if sqB.Success > recent[k].Success {
							recent[k].Success = sqB.Success
						}
						if sqB.Failed > recent[k].Failed {
							recent[k].Failed = sqB.Failed
						}
					}
				}
			}
		}

		// Merge SQLite historical totals with in-memory counters.
		// Strategy: take max(memory, sqlite) so that after a restart the
		// SQLite value wins (memory=0), and during runtime the growing
		// memory value naturally overtakes the stale sqlite snapshot.
		memSuccess := auth.Success
		memFailed := auth.Failed
		if sqliteTotals != nil {
			if sq, ok := sqliteTotals[authIdx]; ok {
				if sq.Success > memSuccess {
					memSuccess = sq.Success
				}
				if sq.Failed > memFailed {
					memFailed = sq.Failed
				}
			}
		}

		providerBucket, ok := out[provider]
		if !ok {
			providerBucket = make(map[string]apiKeyUsageEntry)
			out[provider] = providerBucket
		}
		if existing, exists := providerBucket[compositeKey]; exists {
			existing.Success += memSuccess
			existing.Failed += memFailed
			existing.RecentRequests = mergeRecentRequestBuckets(existing.RecentRequests, recent)
			providerBucket[compositeKey] = existing
			continue
		}
		providerBucket[compositeKey] = apiKeyUsageEntry{
			Success:        memSuccess,
			Failed:         memFailed,
			RecentRequests: recent,
		}
	}

	c.JSON(http.StatusOK, out)
}
