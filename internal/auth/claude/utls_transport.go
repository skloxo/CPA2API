// Package claude provides authentication functionality for Anthropic's Claude API.
// This file implements a custom HTTP transport using utls to bypass TLS fingerprinting.
package claude

import (
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	tls "github.com/refraction-networking/utls"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/http2"
	"golang.org/x/net/proxy"
)

type cachedConn struct {
	conn       *http2.ClientConn
	lastActive time.Time
}

type connectionCache struct {
	mu          sync.Mutex
	connections map[string]*cachedConn
	pending     map[string]*sync.Cond
	dialer      proxy.Dialer
	stopChan    chan struct{}
}

// utlsRoundTripper implements http.RoundTripper using utls with Chrome fingerprint
// to bypass Cloudflare's TLS fingerprinting on Anthropic domains.
type utlsRoundTripper struct {
	cache *connectionCache
}

// newUtlsRoundTripper creates a new utls-based round tripper with optional proxy support
func newUtlsRoundTripper(cfg *config.SDKConfig) *utlsRoundTripper {
	var dialer proxy.Dialer = proxy.Direct
	if cfg != nil {
		proxyDialer, mode, errBuild := proxyutil.BuildDialer(cfg.ProxyURL)
		if errBuild != nil {
			log.Errorf("failed to configure proxy dialer for %q: %v", proxyutil.Redact(cfg.ProxyURL), errBuild)
		} else if mode != proxyutil.ModeInherit && proxyDialer != nil {
			dialer = proxyDialer
		}
	}

	cache := &connectionCache{
		connections: make(map[string]*cachedConn),
		pending:     make(map[string]*sync.Cond),
		dialer:      dialer,
		stopChan:    make(chan struct{}),
	}

	go cache.startScavenger()

	rt := &utlsRoundTripper{cache: cache}
	runtime.SetFinalizer(rt, func(r *utlsRoundTripper) {
		close(r.cache.stopChan)
	})

	return rt
}

func (c *connectionCache) startScavenger() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopChan:
			c.mu.Lock()
			for _, cc := range c.connections {
				_ = cc.conn.Close()
			}
			c.connections = nil
			c.mu.Unlock()
			return
		case <-ticker.C:
			c.cleanIdleConnections()
		}
	}
}

func (c *connectionCache) cleanIdleConnections() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	idleTimeout := 90 * time.Second

	for host, cc := range c.connections {
		if !cc.conn.CanTakeNewRequest() || now.Sub(cc.lastActive) > idleTimeout {
			_ = cc.conn.Close()
			delete(c.connections, host)
		}
	}
}

// getOrCreateConnection gets an existing connection or creates a new one.
// It uses a per-host locking mechanism to prevent multiple goroutines from
// creating connections to the same host simultaneously.
func (c *connectionCache) getOrCreateConnection(host, addr string) (*http2.ClientConn, error) {
	c.mu.Lock()

	// Check if connection exists and is usable
	if cc, ok := c.connections[host]; ok && cc.conn.CanTakeNewRequest() {
		cc.lastActive = time.Now()
		c.mu.Unlock()
		return cc.conn, nil
	}

	// Check if another goroutine is already creating a connection
	if cond, ok := c.pending[host]; ok {
		// Wait for the other goroutine to finish
		cond.Wait()
		// Check if connection is now available
		if cc, ok := c.connections[host]; ok && cc.conn.CanTakeNewRequest() {
			cc.lastActive = time.Now()
			c.mu.Unlock()
			return cc.conn, nil
		}
		// Connection still not available, we'll create one
	}

	// Mark this host as pending
	cond := sync.NewCond(&c.mu)
	c.pending[host] = cond
	c.mu.Unlock()

	// Create connection outside the lock
	h2Conn, err := c.createConnection(host, addr)

	c.mu.Lock()
	defer c.mu.Unlock()

	// Remove pending marker and wake up waiting goroutines
	delete(c.pending, host)
	cond.Broadcast()

	if err != nil {
		return nil, err
	}

	// Store the new connection
	c.connections[host] = &cachedConn{
		conn:       h2Conn,
		lastActive: time.Now(),
	}
	return h2Conn, nil
}

// createConnection creates a new HTTP/2 connection with Chrome TLS fingerprint.
// Chrome's TLS fingerprint is closer to Node.js/OpenSSL (which real Claude Code uses)
// than Firefox, reducing the mismatch between TLS layer and HTTP headers.
func (c *connectionCache) createConnection(host, addr string) (*http2.ClientConn, error) {
	conn, err := c.dialer.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{ServerName: host}
	tlsConn := tls.UClient(conn, tlsConfig, tls.HelloChrome_Auto)

	if err := tlsConn.Handshake(); err != nil {
		conn.Close()
		return nil, err
	}

	tr := &http2.Transport{}
	h2Conn, err := tr.NewClientConn(tlsConn)
	if err != nil {
		tlsConn.Close()
		return nil, err
	}

	return h2Conn, nil
}

// RoundTrip implements http.RoundTripper
func (t *utlsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Host
	addr := host
	if !strings.Contains(addr, ":") {
		addr += ":443"
	}

	// Get hostname without port for TLS ServerName
	hostname := req.URL.Hostname()

	h2Conn, err := t.cache.getOrCreateConnection(hostname, addr)
	if err != nil {
		return nil, err
	}

	resp, err := h2Conn.RoundTrip(req)
	if err != nil {
		// Connection failed, remove it from cache
		t.cache.mu.Lock()
		if cached, ok := t.cache.connections[hostname]; ok && cached.conn == h2Conn {
			delete(t.cache.connections, hostname)
		}
		t.cache.mu.Unlock()
		return nil, err
	}

	t.cache.mu.Lock()
	if cached, ok := t.cache.connections[hostname]; ok && cached.conn == h2Conn {
		cached.lastActive = time.Now()
	}
	t.cache.mu.Unlock()

	return resp, nil
}

// NewAnthropicHttpClient creates an HTTP client that bypasses TLS fingerprinting
// for Anthropic domains by using utls with Chrome fingerprint.
// It accepts optional SDK configuration for proxy settings.
func NewAnthropicHttpClient(cfg *config.SDKConfig) *http.Client {
	return &http.Client{
		Transport: newUtlsRoundTripper(cfg),
	}
}
