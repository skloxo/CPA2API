package executor

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
)

const (
	antigravityShortQuotaCooldownThreshold = 5 * time.Minute
	antigravityInstantRetryThreshold       = 3 * time.Second
)

type antigravity429Category string

type antigravity429DecisionKind string

const (
	antigravity429Unknown                         antigravity429Category     = "unknown"
	antigravity429RateLimited                     antigravity429Category     = "rate_limited"
	antigravity429QuotaExhausted                  antigravity429Category     = "quota_exhausted"
	antigravity429SoftRateLimit                   antigravity429Category     = "soft_rate_limit"
	antigravity429DecisionSoftRetry               antigravity429DecisionKind = "soft_retry"
	antigravity429DecisionInstantRetrySameAuth    antigravity429DecisionKind = "instant_retry_same_auth"
	antigravity429DecisionShortCooldownSwitchAuth antigravity429DecisionKind = "short_cooldown_switch_auth"
	antigravity429DecisionFullQuotaExhausted      antigravity429DecisionKind = "full_quota_exhausted"
)

type antigravity429Decision struct {
	kind       antigravity429DecisionKind
	retryAfter *time.Duration
	reason     string
}

var antigravityQuotaExhaustedKeywords = []string{
	"quota_exhausted",
	"quota exhausted",
}

func classifyAntigravity429(body []byte) antigravity429Category {
	switch decideAntigravity429(body).kind {
	case antigravity429DecisionInstantRetrySameAuth, antigravity429DecisionShortCooldownSwitchAuth:
		return antigravity429RateLimited
	case antigravity429DecisionFullQuotaExhausted:
		return antigravity429QuotaExhausted
	case antigravity429DecisionSoftRetry:
		return antigravity429SoftRateLimit
	default:
		return antigravity429Unknown
	}
}

func decideAntigravity429(body []byte) antigravity429Decision {
	decision := antigravity429Decision{kind: antigravity429DecisionSoftRetry}
	if len(body) == 0 {
		return decision
	}

	if retryAfter, parseErr := parseRetryDelay(body); parseErr == nil && retryAfter != nil {
		decision.retryAfter = retryAfter
	}

	status := strings.TrimSpace(gjson.GetBytes(body, "error.status").String())
	if !strings.EqualFold(status, "RESOURCE_EXHAUSTED") {
		return decision
	}

	details := gjson.GetBytes(body, "error.details")
	if details.Exists() && details.IsArray() {
		for _, detail := range details.Array() {
			if detail.Get("@type").String() != "type.googleapis.com/google.rpc.ErrorInfo" {
				continue
			}
			reason := strings.TrimSpace(detail.Get("reason").String())
			decision.reason = reason
			switch {
			case strings.EqualFold(reason, "QUOTA_EXHAUSTED"):
				decision.kind = antigravity429DecisionFullQuotaExhausted
				return decision
			case strings.EqualFold(reason, "RATE_LIMIT_EXCEEDED"):
				if decision.retryAfter == nil {
					decision.kind = antigravity429DecisionSoftRetry
					return decision
				}
				switch {
				case *decision.retryAfter < antigravityInstantRetryThreshold:
					decision.kind = antigravity429DecisionInstantRetrySameAuth
				case *decision.retryAfter < antigravityShortQuotaCooldownThreshold:
					decision.kind = antigravity429DecisionShortCooldownSwitchAuth
				default:
					decision.kind = antigravity429DecisionFullQuotaExhausted
				}
				return decision
			}
		}
	}

	lowerBody := strings.ToLower(string(body))
	for _, keyword := range antigravityQuotaExhaustedKeywords {
		if strings.Contains(lowerBody, keyword) {
			decision.kind = antigravity429DecisionFullQuotaExhausted
			decision.reason = "quota_exhausted"
			return decision
		}
	}

	decision.kind = antigravity429DecisionSoftRetry
	return decision
}

func newAntigravityStatusErr(statusCode int, body []byte) statusErr {
	err := statusErr{code: statusCode, msg: string(body)}
	if statusCode == http.StatusTooManyRequests {
		if retryAfter, parseErr := parseRetryDelay(body); parseErr == nil && retryAfter != nil {
			err.retryAfter = retryAfter
		}
	}
	return err
}

func antigravityRetryAttempts(auth *cliproxyauth.Auth, cfg *config.Config) int {
	retry := 0
	if cfg != nil {
		retry = cfg.RequestRetry
	}
	if auth != nil {
		if override, ok := auth.RequestRetryOverride(); ok {
			retry = override
		}
	}
	if retry < 0 {
		retry = 0
	}
	attempts := retry + 1
	if attempts < 1 {
		return 1
	}
	return attempts
}

func antigravityShouldRetryNoCapacity(statusCode int, body []byte) bool {
	if statusCode != http.StatusServiceUnavailable {
		return false
	}
	if len(body) == 0 {
		return false
	}
	msg := strings.ToLower(string(body))
	return strings.Contains(msg, "no capacity available")
}

func antigravityShouldRetryTransientResourceExhausted429(statusCode int, body []byte) bool {
	if statusCode != http.StatusTooManyRequests {
		return false
	}
	if len(body) == 0 {
		return false
	}
	if classifyAntigravity429(body) != antigravity429Unknown {
		return false
	}
	status := strings.TrimSpace(gjson.GetBytes(body, "error.status").String())
	if !strings.EqualFold(status, "RESOURCE_EXHAUSTED") {
		return false
	}
	msg := strings.ToLower(string(body))
	return strings.Contains(msg, "resource has been exhausted")
}

func antigravityShouldRetrySoftRateLimit(statusCode int, body []byte) bool {
	if statusCode != http.StatusTooManyRequests {
		return false
	}
	return decideAntigravity429(body).kind == antigravity429DecisionSoftRetry
}

func antigravitySoftRateLimitDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	base := time.Duration(attempt+1) * 500 * time.Millisecond
	if base > 3*time.Second {
		base = 3 * time.Second
	}
	return base
}

func antigravityNoCapacityRetryDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	delay := time.Duration(attempt+1) * 250 * time.Millisecond
	if delay > 2*time.Second {
		delay = 2 * time.Second
	}
	return delay
}

func antigravityTransient429RetryDelay(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	delay := time.Duration(attempt+1) * 100 * time.Millisecond
	if delay > 500*time.Millisecond {
		delay = 500 * time.Millisecond
	}
	return delay
}

func antigravityInstantRetryDelay(wait time.Duration) time.Duration {
	if wait <= 0 {
		return 0
	}
	return wait + 800*time.Millisecond
}

func antigravityWait(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
