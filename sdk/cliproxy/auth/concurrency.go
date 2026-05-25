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
	mu         sync.Mutex
	semaphores map[string]chan struct{}
}

// NewConcurrencySlotManager constructs a ConcurrencySlotManager.
func NewConcurrencySlotManager() *ConcurrencySlotManager {
	return &ConcurrencySlotManager{
		semaphores: make(map[string]chan struct{}),
	}
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

	c.mu.Lock()
	sem, ok := c.semaphores[auth.ID]
	c.mu.Unlock()

	if !ok {
		return true
	}
	return len(sem) < limit
}

// Acquire non-blockingly attempts to take a concurrency slot.
func (c *ConcurrencySlotManager) Acquire(auth *Auth) bool {
	limit := GetMaxConcurrency(auth)
	if limit <= 0 {
		return true
	}

	c.mu.Lock()
	sem, ok := c.semaphores[auth.ID]
	if !ok {
		sem = make(chan struct{}, limit)
		c.semaphores[auth.ID] = sem
	}
	c.mu.Unlock()

	select {
	case sem <- struct{}{}:
		return true
	default:
		return false
	}
}

// WaitAcquire blocks until a slot is acquired, the timeout is exceeded, or the context is done.
func (c *ConcurrencySlotManager) WaitAcquire(ctx context.Context, auth *Auth, timeout time.Duration) bool {
	limit := GetMaxConcurrency(auth)
	if limit <= 0 {
		return true
	}

	c.mu.Lock()
	sem, ok := c.semaphores[auth.ID]
	if !ok {
		sem = make(chan struct{}, limit)
		c.semaphores[auth.ID] = sem
	}
	c.mu.Unlock()

	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	tCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	select {
	case sem <- struct{}{}:
		return true
	case <-tCtx.Done():
		return false
	}
}

// Release releases a held concurrency slot for the auth.
func (c *ConcurrencySlotManager) Release(auth *Auth) {
	if auth == nil {
		return
	}
	limit := GetMaxConcurrency(auth)
	if limit <= 0 {
		return
	}

	c.mu.Lock()
	sem, ok := c.semaphores[auth.ID]
	c.mu.Unlock()

	if ok {
		select {
		case <-sem:
		default:
		}
	}
}
