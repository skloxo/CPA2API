package helps

import (
	"math/rand"
	"time"
)

// UserAgents is a list of common browser User-Agent strings for request disguise.
var UserAgents = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:121.0) Gecko/20100101 Firefox/121.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.2 Safari/605.1.15",
}

// GetRandomUserAgent returns a random User-Agent string from the list.
func GetRandomUserAgent() string {
	return UserAgents[rand.Intn(len(UserAgents))]
}

// AntiBlockConfig holds configuration for anti-blocking behavior.
type AntiBlockConfig struct {
	MinDelay    time.Duration
	MaxDelay    time.Duration
	MaxRetries  int
	BackoffBase time.Duration
}

// DefaultAntiBlockConfig returns the default anti-block configuration.
func DefaultAntiBlockConfig() AntiBlockConfig {
	return AntiBlockConfig{
		MinDelay:    1 * time.Second,
		MaxDelay:    3 * time.Second,
		MaxRetries:  3,
		BackoffBase: 2 * time.Second,
	}
}

// RandomDelay returns a random duration between min and max.
func RandomDelay(min, max time.Duration) time.Duration {
	if min >= max {
		return min
	}
	return min + time.Duration(rand.Int63n(int64(max-min)))
}

// ExponentialBackoff returns the exponential backoff duration for a given attempt.
func ExponentialBackoff(attempt int, base time.Duration) time.Duration {
	return base * time.Duration(1<<uint(attempt))
}
