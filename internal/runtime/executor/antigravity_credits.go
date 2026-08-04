package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type antigravityCreditsFailureState struct {
	PermanentlyDisabled      bool
	ExplicitBalanceExhausted bool
}

type antigravityCreditsBalance struct {
	CreditAmount    float64
	MinCreditAmount float64
	PaidTierID      string
	Known           bool
}

type antigravityCreditsHintRefreshState struct {
	mu          sync.Mutex
	lastAttempt time.Time
}

var (
	antigravityCreditsFailureByAuth   sync.Map
	antigravityShortCooldownByAuth    sync.Map
	antigravityCreditsBalanceByAuth   sync.Map // auth.ID → antigravityCreditsBalance
	antigravityCreditsHintRefreshByID sync.Map // auth.ID → *antigravityCreditsHintRefreshState
)

func antigravityAuthHasCredits(auth *cliproxyauth.Auth) bool {
	if auth == nil || strings.TrimSpace(auth.ID) == "" {
		return false
	}
	if hint, ok := cliproxyauth.GetAntigravityCreditsHint(auth.ID); ok && hint.Known {
		return hint.Available
	}
	val, ok := antigravityCreditsBalanceByAuth.Load(strings.TrimSpace(auth.ID))
	if !ok {
		return true // optimistic: assume credits available when balance unknown
	}
	bal, valid := val.(antigravityCreditsBalance)
	if !valid {
		antigravityCreditsBalanceByAuth.Delete(strings.TrimSpace(auth.ID))
		return false
	}
	if !bal.Known {
		return false
	}
	available := bal.CreditAmount >= bal.MinCreditAmount
	cliproxyauth.SetAntigravityCreditsHint(strings.TrimSpace(auth.ID), cliproxyauth.AntigravityCreditsHint{
		Known:           true,
		Available:       available,
		CreditAmount:    bal.CreditAmount,
		MinCreditAmount: bal.MinCreditAmount,
		PaidTierID:      bal.PaidTierID,
		UpdatedAt:       time.Now(),
	})
	return available
}

// parseMetaFloat extracts a float64 from auth.Metadata (handles string and numeric types).
func parseMetaFloat(metadata map[string]any, key string) (float64, bool) {
	v, ok := metadata[key]
	if !ok {
		return 0, false
	}
	switch typed := v.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case uint64:
		return float64(typed), true
	case json.Number:
		if f, err := typed.Float64(); err == nil {
			return f, true
		}
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(typed), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

func injectEnabledCreditTypes(payload []byte) []byte {
	if len(payload) == 0 {
		return nil
	}
	if !gjson.ValidBytes(payload) {
		return nil
	}
	updated, err := sjson.SetRawBytes(payload, "enabledCreditTypes", []byte(`["GOOGLE_ONE_AI"]`))
	if err != nil {
		return nil
	}
	return updated
}

func antigravityCreditsRetryEnabled(cfg *config.Config) bool {
	return cfg != nil && cfg.QuotaExceeded.AntigravityCredits
}

func clearAntigravityCreditsFailureState(auth *cliproxyauth.Auth) {
	if auth == nil || strings.TrimSpace(auth.ID) == "" {
		return
	}
	antigravityCreditsFailureByAuth.Delete(strings.TrimSpace(auth.ID))
}
func markAntigravityCreditsPermanentlyDisabled(auth *cliproxyauth.Auth) {
	if auth == nil || strings.TrimSpace(auth.ID) == "" {
		return
	}
	authID := strings.TrimSpace(auth.ID)
	state := antigravityCreditsFailureState{
		PermanentlyDisabled:      true,
		ExplicitBalanceExhausted: true,
	}
	antigravityCreditsFailureByAuth.Store(authID, state)
	antigravityCreditsBalanceByAuth.Store(authID, antigravityCreditsBalance{
		CreditAmount:    0,
		MinCreditAmount: 1,
		Known:           true,
	})
	cliproxyauth.SetAntigravityCreditsHint(authID, cliproxyauth.AntigravityCreditsHint{
		Known:           true,
		Available:       false,
		CreditAmount:    0,
		MinCreditAmount: 1,
		UpdatedAt:       time.Now(),
	})
}

func clearAntigravityCreditsPermanentlyDisabled(auth *cliproxyauth.Auth) {
	if auth == nil || strings.TrimSpace(auth.ID) == "" {
		return
	}
	antigravityCreditsFailureByAuth.Delete(strings.TrimSpace(auth.ID))
}

func antigravityHasExplicitCreditsBalanceExhaustedReason(body []byte) bool {
	if len(body) == 0 {
		return false
	}
	details := gjson.GetBytes(body, "error.details")
	if !details.Exists() || !details.IsArray() {
		return false
	}
	for _, detail := range details.Array() {
		if detail.Get("@type").String() != "type.googleapis.com/google.rpc.ErrorInfo" {
			continue
		}
		reason := strings.TrimSpace(detail.Get("reason").String())
		if strings.EqualFold(reason, "INSUFFICIENT_G1_CREDITS_BALANCE") {
			return true
		}
	}
	return false
}

func (e *AntigravityExecutor) maybeRefreshAntigravityCreditsHint(ctx context.Context, auth *cliproxyauth.Auth, accessToken string) {
	if e == nil || auth == nil || !antigravityCreditsRetryEnabled(e.cfg) {
		return
	}
	if ctx != nil && ctx.Err() != nil {
		return
	}
	authID := strings.TrimSpace(auth.ID)
	if authID == "" {
		return
	}
	if hint, ok := cliproxyauth.GetAntigravityCreditsHint(authID); ok && hint.Known {
		return
	}
	if strings.TrimSpace(accessToken) == "" {
		accessToken = metaStringValue(auth.Metadata, "access_token")
	}
	if strings.TrimSpace(accessToken) == "" {
		return
	}

	state := &antigravityCreditsHintRefreshState{}
	if existing, loaded := antigravityCreditsHintRefreshByID.LoadOrStore(authID, state); loaded {
		if cast, ok := existing.(*antigravityCreditsHintRefreshState); ok && cast != nil {
			state = cast
		} else {
			antigravityCreditsHintRefreshByID.Delete(authID)
			antigravityCreditsHintRefreshByID.Store(authID, state)
		}
	}

	now := time.Now()
	if !state.mu.TryLock() {
		return
	}
	if !state.lastAttempt.IsZero() && now.Sub(state.lastAttempt) < antigravityCreditsHintRefreshInterval {
		state.mu.Unlock()
		return
	}
	state.lastAttempt = now

	refreshCtx := context.Background()
	if ctx != nil {
		if rt, ok := ctx.Value("cliproxy.roundtripper").(http.RoundTripper); ok && rt != nil {
			refreshCtx = context.WithValue(refreshCtx, "cliproxy.roundtripper", rt)
		}
	}
	refreshCtx, cancel := context.WithTimeout(refreshCtx, antigravityCreditsHintRefreshTimeout)
	authCopy := auth.Clone()

	go func(state *antigravityCreditsHintRefreshState, auth *cliproxyauth.Auth, token string) {
		defer cancel()
		defer state.mu.Unlock()
		e.updateAntigravityCreditsBalance(refreshCtx, auth, token)
	}(state, authCopy, accessToken)
}

func (e *AntigravityExecutor) updateAntigravityCreditsBalance(ctx context.Context, auth *cliproxyauth.Auth, accessToken string) {
	if auth == nil || strings.TrimSpace(auth.ID) == "" {
		return
	}
	token := strings.TrimSpace(accessToken)
	if token == "" {
		token = metaStringValue(auth.Metadata, "access_token")
	}
	if token == "" {
		return
	}

	userAgent := resolveUserAgent(auth)
	loadReqBody, errMarshal := json.Marshal(map[string]any{
		"metadata": map[string]string{
			"ideType": "ANTIGRAVITY",
		},
	})
	if errMarshal != nil {
		log.Debugf("antigravity executor: marshal loadCodeAssist request error: %v", errMarshal)
		return
	}
	baseURL := antigravityLoadCodeAssistBaseURL(auth)
	endpointURL := strings.TrimSuffix(baseURL, "/") + "/v1internal:loadCodeAssist"
	httpReq, errReq := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL, bytes.NewReader(loadReqBody))
	if errReq != nil {
		log.Debugf("antigravity executor: create loadCodeAssist request error: %v", errReq)
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+token)
	httpReq.Header.Set("Accept", "*/*")
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("User-Agent", userAgent)

	httpClient := newAntigravityHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, errDo := httpClient.Do(httpReq)
	if errDo != nil {
		log.Debugf("antigravity executor: loadCodeAssist request error: %v", errDo)
		return
	}
	defer func() {
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("antigravity executor: close loadCodeAssist response body error: %v", errClose)
		}
	}()

	bodyBytes, errRead := io.ReadAll(httpResp.Body)
	if errRead != nil || httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		log.Debugf("antigravity executor: loadCodeAssist returned status %d, err=%v", httpResp.StatusCode, errRead)
		return
	}

	authID := strings.TrimSpace(auth.ID)
	paidTierID := strings.TrimSpace(gjson.GetBytes(bodyBytes, "paidTier.id").String())

	credits := gjson.GetBytes(bodyBytes, "paidTier.availableCredits")
	if !credits.IsArray() {
		cliproxyauth.SetAntigravityCreditsHint(authID, cliproxyauth.AntigravityCreditsHint{
			Known:      true,
			Available:  false,
			PaidTierID: paidTierID,
			UpdatedAt:  time.Now(),
		})
		return
	}
	for _, credit := range credits.Array() {
		if !strings.EqualFold(credit.Get("creditType").String(), "GOOGLE_ONE_AI") {
			continue
		}
		creditAmount, errCA := strconv.ParseFloat(strings.TrimSpace(credit.Get("creditAmount").String()), 64)
		if errCA != nil {
			continue
		}
		minAmount, errMA := strconv.ParseFloat(strings.TrimSpace(credit.Get("minimumCreditAmountForUsage").String()), 64)
		if errMA != nil {
			continue
		}
		bal := antigravityCreditsBalance{
			CreditAmount:    creditAmount,
			MinCreditAmount: minAmount,
			PaidTierID:      paidTierID,
			Known:           true,
		}
		antigravityCreditsBalanceByAuth.Store(authID, bal)
		cliproxyauth.SetAntigravityCreditsHint(authID, cliproxyauth.AntigravityCreditsHint{
			Known:           true,
			Available:       creditAmount >= minAmount,
			CreditAmount:    creditAmount,
			MinCreditAmount: minAmount,
			PaidTierID:      paidTierID,
			UpdatedAt:       time.Now(),
		})
		if creditAmount >= minAmount {
			clearAntigravityCreditsPermanentlyDisabled(auth)
		}
		return
	}
}

func antigravityShouldBypassShortCooldown(ctx context.Context, cfg *config.Config) bool {
	return cliproxyauth.AntigravityCreditsRequested(ctx) && antigravityCreditsRetryEnabled(cfg)
}

func antigravityShortCooldownKey(auth *cliproxyauth.Auth, modelName string) string {
	if auth == nil {
		return ""
	}
	authID := strings.TrimSpace(auth.ID)
	modelName = strings.TrimSpace(modelName)
	if authID == "" || modelName == "" {
		return ""
	}
	return authID + "|" + modelName + "|sc"
}

func antigravityIsInShortCooldown(auth *cliproxyauth.Auth, modelName string, now time.Time) (bool, time.Duration) {
	key := antigravityShortCooldownKey(auth, modelName)
	if key == "" {
		return false, 0
	}
	value, ok := antigravityShortCooldownByAuth.Load(key)
	if !ok {
		return false, 0
	}
	until, ok := value.(time.Time)
	if !ok || until.IsZero() {
		antigravityShortCooldownByAuth.Delete(key)
		return false, 0
	}
	remaining := until.Sub(now)
	if remaining <= 0 {
		antigravityShortCooldownByAuth.Delete(key)
		return false, 0
	}
	return true, remaining
}

func markAntigravityShortCooldown(auth *cliproxyauth.Auth, modelName string, now time.Time, duration time.Duration) {
	key := antigravityShortCooldownKey(auth, modelName)
	if key == "" {
		return
	}
	antigravityShortCooldownByAuth.Store(key, now.Add(duration))
}
