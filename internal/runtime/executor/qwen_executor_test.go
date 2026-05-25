package executor

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestQwenPreheating(t *testing.T) {
	var requestCount int
	var mu sync.Mutex

	cfg := &config.Config{}
	e := NewQwenExecutor(cfg)

	// Override createChatID via the test hook
	e.createChatIDF = func(ctx context.Context, auth *cliproxyauth.Auth) (string, error) {
		mu.Lock()
		requestCount++
		count := requestCount
		mu.Unlock()

		return fmt.Sprintf("chat-id-%d", count), nil
	}

	auth := &cliproxyauth.Auth{
		ID: "test-auth-id",
		Metadata: map[string]any{
			"access_token": "test-token",
		},
	}

	// 1. Initial resolveChatID should start preheating and immediately return a chat ID.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	chatID, err := e.resolveChatID(ctx, auth)
	if err != nil {
		t.Fatalf("resolveChatID failed: %v", err)
	}
	if chatID == "" {
		t.Fatal("expected non-empty chatID")
	}

	// 2. Wait for background worker to fill the pool (up to 5)
	time.Sleep(500 * time.Millisecond)

	q := e.getPreheatQueue(auth)
	if q == nil {
		t.Fatal("expected preheat queue to be initialized")
	}

	q.mu.Lock()
	poolSize := len(q.chatIDs)
	q.mu.Unlock()

	// Pool should have populated preheated chat IDs (up to 5)
	if poolSize == 0 {
		t.Fatal("expected preheat pool to contain chat IDs")
	}

	// 3. Popping from the queue should work
	poppedID := q.Pop()
	if poppedID == "" {
		t.Fatal("expected poppedID to be non-empty")
	}

	// 4. Inactivity worker cleanup test
	q.mu.Lock()
	q.lastActive = time.Now().Add(-6 * time.Minute)
	q.mu.Unlock()

	// Give the ticker time to fire and worker to terminate
	time.Sleep(6 * time.Second)

	e.preheatMu.Lock()
	_, exists := e.pools[auth.ID]
	e.preheatMu.Unlock()

	if exists {
		t.Fatal("expected preheat queue to be removed from pools map after inactivity")
	}
}
