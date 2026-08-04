package auth

import (
	"context"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func TestGetMaxConcurrency(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		auth *Auth
		want int
	}{
		{
			name: "nil auth",
			auth: nil,
			want: 0,
		},
		{
			name: "no concurrency limits set",
			auth: &Auth{ID: "test"},
			want: 0,
		},
		{
			name: "attributes limit set",
			auth: &Auth{ID: "test", Attributes: map[string]string{"max_concurrency": "5"}},
			want: 5,
		},
		{
			name: "metadata limit set (int)",
			auth: &Auth{ID: "test", Metadata: map[string]any{"max_concurrency": 3}},
			want: 3,
		},
		{
			name: "metadata limit set (float64)",
			auth: &Auth{ID: "test", Metadata: map[string]any{"max_concurrency": float64(4)}},
			want: 4,
		},
		{
			name: "metadata limit set (string)",
			auth: &Auth{ID: "test", Metadata: map[string]any{"max_concurrency": "10"}},
			want: 10,
		},
		{
			name: "both set (attributes takes precedence)",
			auth: &Auth{
				ID:         "test",
				Attributes: map[string]string{"max_concurrency": "5"},
				Metadata:   map[string]any{"max_concurrency": 10},
			},
			want: 5,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := GetMaxConcurrency(tc.auth)
			if got != tc.want {
				t.Errorf("GetMaxConcurrency() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestConcurrencySlotManager_AcquireRelease(t *testing.T) {
	t.Parallel()

	mgr := NewConcurrencySlotManager()
	auth := &Auth{ID: "auth1", Attributes: map[string]string{"max_concurrency": "2"}}

	// First acquire
	if !mgr.Acquire(auth) {
		t.Fatal("expected first acquire to succeed")
	}
	if !mgr.HasAvailableSlot(auth) {
		t.Fatal("expected 1 available slot remaining out of 2")
	}

	// Second acquire
	if !mgr.Acquire(auth) {
		t.Fatal("expected second acquire to succeed")
	}
	if mgr.HasAvailableSlot(auth) {
		t.Fatal("expected 0 available slots remaining")
	}

	// Third acquire should fail (non-blocking)
	if mgr.Acquire(auth) {
		t.Fatal("expected third acquire to fail")
	}

	// WaitAcquire with short timeout should fail
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if mgr.WaitAcquire(ctx, auth, 10*time.Millisecond) {
		t.Fatal("expected WaitAcquire to fail on limit")
	}

	// Release one slot
	mgr.Release(auth)
	if !mgr.HasAvailableSlot(auth) {
		t.Fatal("expected slot to be available after release")
	}

	// WaitAcquire should succeed now
	if !mgr.WaitAcquire(ctx, auth, 10*time.Millisecond) {
		t.Fatal("expected WaitAcquire to succeed after release")
	}
}

func TestScheduler_SlotPrioritySelection(t *testing.T) {
	t.Parallel()

	concurrency := NewConcurrencySlotManager()
	authA := &Auth{ID: "auth-a", Provider: "gemini", Attributes: map[string]string{"max_concurrency": "1"}}
	authB := &Auth{ID: "auth-b", Provider: "gemini", Attributes: map[string]string{"max_concurrency": "1"}}

	scheduler := newSchedulerForTest(&RoundRobinSelector{}, authA, authB)
	scheduler.concurrency = concurrency

	// Make auth-a busy
	if !concurrency.Acquire(authA) {
		t.Fatal("failed to acquire slot for auth-a")
	}

	// Because auth-a is busy (at limit), pickSingle should return auth-b (which has an available slot),
	// even though RoundRobin strategy would ordinarily rotation-pick auth-a or auth-b.
	got, err := scheduler.pickSingle(context.Background(), "gemini", "", cliproxyexecutor.Options{}, nil)
	if err != nil {
		t.Fatalf("pickSingle failed: %v", err)
	}
	if got.ID != "auth-b" {
		t.Fatalf("expected to pick auth-b because auth-a is busy, got %s", got.ID)
	}

	// Now make auth-b busy too
	if !concurrency.Acquire(authB) {
		t.Fatal("failed to acquire slot for auth-b")
	}

	// Both are busy. pickSingle should fall back to picking one (based on selection order)
	// rather than failing, so the caller can block-wait on it.
	gotFallback, errFallback := scheduler.pickSingle(context.Background(), "gemini", "", cliproxyexecutor.Options{}, nil)
	if errFallback != nil {
		t.Fatalf("pickSingle fallback failed: %v", errFallback)
	}
	if gotFallback == nil {
		t.Fatal("expected fallback pick to return an auth")
	}
}
