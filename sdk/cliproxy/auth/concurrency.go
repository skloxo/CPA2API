package auth

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

// ConcurrencySlotManager tracks active request semaphores per Auth.ID.
type ConcurrencySlotManager struct {
	mu       sync.Mutex
	trackers map[string]*authTracker
}

type authTracker struct {
	mu          sync.Mutex
	activeCount int
	waiters     []chan struct{}
}

// NewConcurrencySlotManager constructs a ConcurrencySlotManager.
func NewConcurrencySlotManager() *ConcurrencySlotManager {
	return &ConcurrencySlotManager{
		trackers: make(map[string]*authTracker),
	}
}

func (c *ConcurrencySlotManager) getTracker(authID string) *authTracker {
	c.mu.Lock()
	defer c.mu.Unlock()
	t, ok := c.trackers[authID]
	if !ok {
		t = &authTracker{}
		c.trackers[authID] = t
	}
	return t
}

// GetMaxConcurrency parses the max concurrency limit for an Auth entry.
func GetMaxConcurrency(auth *Auth) int {
	if auth == nil {
		return 0
	}
	if auth.Attributes != nil {
		if val, ok := auth.Attributes["max_concurrency"]; ok {
			if limit, err := strconv.Atoi(strings.TrimSpace(val)); err == nil && limit > 0 {
				return limit
			}
		}
	}
	if auth.Metadata != nil {
		if val, ok := auth.Metadata["max_concurrency"]; ok {
			switch v := val.(type) {
			case float64:
				if v > 0 {
					return int(v)
				}
			case int:
				if v > 0 {
					return v
				}
			case string:
				if limit, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && limit > 0 {
					return limit
				}
			}
		}
	}
	return 0
}

// GetConcurrencyTimeout parses a custom concurrency wait timeout from options metadata,
// defaulting to 30 seconds.
func GetConcurrencyTimeout(opts cliproxyexecutor.Options) time.Duration {
	if opts.Metadata != nil {
		if val, ok := opts.Metadata["concurrency_timeout"]; ok {
			switch v := val.(type) {
			case float64:
				if v > 0 {
					return time.Duration(v) * time.Second
				}
			case int:
				if v > 0 {
					return time.Duration(v) * time.Second
				}
			case string:
				if sec, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && sec > 0 {
					return time.Duration(sec) * time.Second
				}
			}
		}
	}
	return 30 * time.Second
}

// HasAvailableSlot returns true if the auth is not at its concurrency limit.
func (c *ConcurrencySlotManager) HasAvailableSlot(auth *Auth) bool {
	limit := GetMaxConcurrency(auth)
	if limit <= 0 {
		return true
	}
	t := c.getTracker(auth.ID)
	t.mu.Lock()
	t.wakeWaiters(limit)
	available := t.activeCount < limit
	t.mu.Unlock()
	return available
}

// Acquire non-blockingly attempts to take a concurrency slot.
func (c *ConcurrencySlotManager) Acquire(auth *Auth) bool {
	limit := GetMaxConcurrency(auth)
	if limit <= 0 {
		return true
	}
	t := c.getTracker(auth.ID)
	t.mu.Lock()
	t.wakeWaiters(limit)
	if t.activeCount < limit {
		t.activeCount++
		t.mu.Unlock()
		return true
	}
	t.mu.Unlock()
	return false
}

// WaitAcquire blocks until a slot is acquired, the timeout is exceeded, or the context is done.
func (c *ConcurrencySlotManager) WaitAcquire(ctx context.Context, auth *Auth, timeout time.Duration) bool {
	limit := GetMaxConcurrency(auth)
	if limit <= 0 {
		return true
	}
	t := c.getTracker(auth.ID)
	t.mu.Lock()
	t.wakeWaiters(limit)
	if t.activeCount < limit {
		t.activeCount++
		t.mu.Unlock()
		return true
	}

	ch := make(chan struct{}, 1)
	t.waiters = append(t.waiters, ch)
	t.mu.Unlock()

	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	tCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case <-ch:
		return true
	case <-tCtx.Done():
		t.mu.Lock()
		defer t.mu.Unlock()
		select {
		case <-ch:
			return true
		default:
		}
		for i, w := range t.waiters {
			if w == ch {
				t.waiters = append(t.waiters[:i], t.waiters[i+1:]...)
				break
			}
		}
		return false
	}
}

// Release releases a held concurrency slot for the auth.
func (c *ConcurrencySlotManager) Release(auth *Auth) {
	if auth == nil {
		return
	}
	limit := GetMaxConcurrency(auth)
	t := c.getTracker(auth.ID)
	t.Release(limit)
}

func (t *authTracker) Release(limit int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.activeCount > 0 {
		t.activeCount--
	}
	t.wakeWaiters(limit)
}

func (t *authTracker) wakeWaiters(limit int) {
	for len(t.waiters) > 0 && (limit <= 0 || t.activeCount < limit) {
		next := t.waiters[0]
		t.waiters = t.waiters[1:]
		t.activeCount++
		select {
		case next <- struct{}{}:
		default:
		}
	}
}

// ActiveConcurrency returns the number of active inflight requests for a given auth ID.
func (c *ConcurrencySlotManager) ActiveConcurrency(auth *Auth) int {
	if auth == nil {
		return 0
	}
	t := c.getTracker(auth.ID)
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.activeCount
}
