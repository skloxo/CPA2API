package auth

import (
	"context"
	"strings"
	"testing"
	"time"
)

type testRefreshEvaluator struct{}

func (testRefreshEvaluator) ShouldRefresh(time.Time, *Auth) bool { return false }

func setRefreshLeadFactory(t *testing.T, provider string, factory func() *time.Duration) {
	t.Helper()
	key := strings.ToLower(strings.TrimSpace(provider))
	refreshLeadMu.Lock()
	prev, hadPrev := refreshLeadFactories[key]
	if factory == nil {
		delete(refreshLeadFactories, key)
	} else {
		refreshLeadFactories[key] = factory
	}
	refreshLeadMu.Unlock()
	t.Cleanup(func() {
		refreshLeadMu.Lock()
		if hadPrev {
			refreshLeadFactories[key] = prev
		} else {
			delete(refreshLeadFactories, key)
		}
		refreshLeadMu.Unlock()
	})
}

func TestNextRefreshCheckAt_DisabledUnschedule(t *testing.T) {
	now := time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC)
	expiry := now.Add(time.Hour)
	lead := 10 * time.Minute
	setRefreshLeadFactory(t, "disabled-schedule", func() *time.Duration {
		d := lead
		return &d
	})

	auth := &Auth{
		ID:       "a1",
		Provider: "disabled-schedule",
		Disabled: true,
		Status:   StatusDisabled,
		Metadata: map[string]any{
			"email":      "x@example.com",
			"expires_at": expiry.Format(time.RFC3339),
		},
	}

	got, ok := nextRefreshCheckAt(now, auth, 15*time.Minute)
	if !ok {
		t.Fatalf("nextRefreshCheckAt() ok = false, want true")
	}
	want := expiry.Add(-lead)
	if !got.Equal(want) {
		t.Fatalf("nextRefreshCheckAt() = %s, want %s", got, want)
	}
}

func TestNextRefreshCheckAt_APIKeyUnschedule(t *testing.T) {
	now := time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC)
	auth := &Auth{ID: "a1", Provider: "test", Attributes: map[string]string{"api_key": "k"}}
	if _, ok := nextRefreshCheckAt(now, auth, 15*time.Minute); ok {
		t.Fatalf("nextRefreshCheckAt() ok = true, want false")
	}
}

func TestNextRefreshCheckAt_NextRefreshAfterGate(t *testing.T) {
	now := time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC)
	nextAfter := now.Add(30 * time.Minute)
	auth := &Auth{
		ID:               "a1",
		Provider:         "test",
		NextRefreshAfter: nextAfter,
		Metadata:         map[string]any{"email": "x@example.com"},
	}
	got, ok := nextRefreshCheckAt(now, auth, 15*time.Minute)
	if !ok {
		t.Fatalf("nextRefreshCheckAt() ok = false, want true")
	}
	if !got.Equal(nextAfter) {
		t.Fatalf("nextRefreshCheckAt() = %s, want %s", got, nextAfter)
	}
}

func TestNextRefreshCheckAt_PreferredInterval_PicksEarliestCandidate(t *testing.T) {
	now := time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC)
	expiry := now.Add(20 * time.Minute)
	auth := &Auth{
		ID:              "a1",
		Provider:        "test",
		LastRefreshedAt: now,
		Metadata: map[string]any{
			"email":                    "x@example.com",
			"expires_at":               expiry.Format(time.RFC3339),
			"refresh_interval_seconds": 900, // 15m
		},
	}
	got, ok := nextRefreshCheckAt(now, auth, 15*time.Minute)
	if !ok {
		t.Fatalf("nextRefreshCheckAt() ok = false, want true")
	}
	want := expiry.Add(-15 * time.Minute)
	if !got.Equal(want) {
		t.Fatalf("nextRefreshCheckAt() = %s, want %s", got, want)
	}
}

func TestNextRefreshCheckAt_ProviderLead_Expiry(t *testing.T) {
	now := time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC)
	expiry := now.Add(time.Hour)
	lead := 10 * time.Minute
	setRefreshLeadFactory(t, "provider-lead-expiry", func() *time.Duration {
		d := lead
		return &d
	})

	auth := &Auth{
		ID:       "a1",
		Provider: "provider-lead-expiry",
		Metadata: map[string]any{
			"email":      "x@example.com",
			"expires_at": expiry.Format(time.RFC3339),
		},
	}

	got, ok := nextRefreshCheckAt(now, auth, 15*time.Minute)
	if !ok {
		t.Fatalf("nextRefreshCheckAt() ok = false, want true")
	}
	want := expiry.Add(-lead)
	if !got.Equal(want) {
		t.Fatalf("nextRefreshCheckAt() = %s, want %s", got, want)
	}
}

func TestNextRefreshCheckAt_RefreshEvaluatorFallback(t *testing.T) {
	now := time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC)
	interval := 15 * time.Minute
	auth := &Auth{
		ID:       "a1",
		Provider: "test",
		Metadata: map[string]any{"email": "x@example.com"},
		Runtime:  testRefreshEvaluator{},
	}
	got, ok := nextRefreshCheckAt(now, auth, interval)
	if !ok {
		t.Fatalf("nextRefreshCheckAt() ok = false, want true")
	}
	want := now.Add(interval)
	if !got.Equal(want) {
		t.Fatalf("nextRefreshCheckAt() = %s, want %s", got, want)
	}
}

func TestNextRefreshCheckAt_QwenBypassesUnauthorized(t *testing.T) {
	now := time.Date(2026, 4, 12, 0, 0, 0, 0, time.UTC)
	auth := &Auth{
		ID:       "qwen-test.json",
		Provider: "qwen",
		LastError: &Error{
			HTTPStatus: 401,
			Code:       "unauthorized",
			Message:    "simulated 401 unauthorized error",
		},
		Metadata: map[string]any{
			"email":    "user@example.com",
			"password": "hashed_or_plain_password",
		},
	}

	setRefreshLeadFactory(t, "qwen", func() *time.Duration {
		d := 5 * time.Minute
		return &d
	})

	got, ok := nextRefreshCheckAt(now, auth, 15*time.Minute)
	if !ok {
		t.Fatalf("expected nextRefreshCheckAt to return true for qwen even with 401 error")
	}
	if !got.Equal(now) {
		t.Fatalf("expected next refresh check to be scheduled immediately, got: %v", got)
	}

	// Verify shouldRefresh returns true for qwen even with 401
	manager := NewManager(nil, nil, nil)
	if !manager.shouldRefresh(auth, now) {
		t.Fatalf("expected manager.shouldRefresh to return true for qwen with 401")
	}
}

type mockQwenExecutor struct {
	schedulerProviderTestExecutor
	refreshFunc func(ctx context.Context, auth *Auth) (*Auth, error)
}

func (e *mockQwenExecutor) Refresh(ctx context.Context, auth *Auth) (*Auth, error) {
	if e.refreshFunc != nil {
		return e.refreshFunc(ctx, auth)
	}
	return auth, nil
}

type mockStore struct {
	auths     map[string]*Auth
	saveCount int
}

func (s *mockStore) List(ctx context.Context) ([]*Auth, error) {
	var list []*Auth
	for _, a := range s.auths {
		list = append(list, a)
	}
	return list, nil
}

func (s *mockStore) Save(ctx context.Context, auth *Auth) (string, error) {
	s.saveCount++
	s.auths[auth.ID] = auth
	return auth.ID, nil
}

func (s *mockStore) Delete(ctx context.Context, id string) error {
	delete(s.auths, id)
	return nil
}

func TestQwenAutoRefreshSelfHealing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store := &mockStore{
		auths: make(map[string]*Auth),
	}

	manager := NewManager(store, &RoundRobinSelector{}, nil)

	qwenExec := &mockQwenExecutor{
		schedulerProviderTestExecutor: schedulerProviderTestExecutor{provider: "qwen"},
		refreshFunc: func(ctx context.Context, auth *Auth) (*Auth, error) {
			cloned := auth.Clone()
			cloned.Metadata["access_token"] = "new_valid_token"
			delete(cloned.Metadata, "error")
			return cloned, nil
		},
	}
	manager.RegisterExecutor(qwenExec)

	auth := &Auth{
		ID:       "qwen-test-refresh.json",
		Provider: "qwen",
		LastError: &Error{
			HTTPStatus: 401,
			Code:       "unauthorized",
			Message:    "simulated 401 unauthorized error",
		},
		Metadata: map[string]any{
			"email":        "user@example.com",
			"password":     "hashed_or_plain_password",
			"access_token": "expired_token",
		},
	}

	if _, err := manager.Register(ctx, auth); err != nil {
		t.Fatalf("failed to register auth: %v", err)
	}

	setRefreshLeadFactory(t, "qwen", func() *time.Duration {
		d := 5 * time.Minute
		return &d
	})

	manager.refreshAuth(ctx, auth.ID)

	updated, ok := manager.GetByID(auth.ID)
	if !ok {
		t.Fatalf("expected auth %q to exist", auth.ID)
	}

	if updated.LastError != nil {
		t.Fatalf("expected last error to be cleared, got: %v", updated.LastError)
	}

	accessToken, ok := updated.Metadata["access_token"].(string)
	if !ok || accessToken != "new_valid_token" {
		t.Fatalf("expected access token to be 'new_valid_token', got: %v", updated.Metadata["access_token"])
	}

	if store.saveCount == 0 {
		t.Fatalf("expected store Save to be called during refresh")
	}

	savedAuth := store.auths[auth.ID]
	if savedAuth == nil {
		t.Fatalf("expected auth to be saved in store")
	}
	savedAccessToken, _ := savedAuth.Metadata["access_token"].(string)
	if savedAccessToken != "new_valid_token" {
		t.Fatalf("expected saved access token to be 'new_valid_token', got: %v", savedAccessToken)
	}
}
