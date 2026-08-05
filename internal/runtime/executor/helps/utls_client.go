package helps

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	tls "github.com/refraction-networking/utls"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/proxyutil"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/http2"
	"golang.org/x/net/proxy"
)

type cachedConn struct {
	conn       *http2.ClientConn
	lastActive time.Time
}

// utlsRoundTripper implements http.RoundTripper using utls with Chrome fingerprint
// to bypass Cloudflare's TLS fingerprinting on Anthropic domains.
const utlsMaxConns = 100

type utlsRoundTripper struct {
	mu          sync.Mutex
	connections map[string]*cachedConn
	pending     map[string]*sync.Cond
	dialer      proxy.Dialer
	lastActive  time.Time
}

func newUtlsRoundTripper(proxyURL string) *utlsRoundTripper {
	var dialer proxy.Dialer = proxy.Direct
	if proxyURL != "" {
		proxyDialer, mode, errBuild := proxyutil.BuildDialer(proxyURL)
		if errBuild != nil {
			log.Errorf("utls: failed to configure proxy dialer for %q: %v", proxyutil.Redact(proxyURL), errBuild)
		} else if mode != proxyutil.ModeInherit && proxyDialer != nil {
			dialer = proxyDialer
		}
	}
	return &utlsRoundTripper{
		connections: make(map[string]*cachedConn),
		pending:     make(map[string]*sync.Cond),
		dialer:      dialer,
		lastActive:  time.Now(),
	}
}

func (t *utlsRoundTripper) getOrCreateConnection(host, addr string) (*http2.ClientConn, error) {
	t.mu.Lock()

	if cc, ok := t.connections[host]; ok && cc.conn.CanTakeNewRequest() {
		cc.lastActive = time.Now()
		t.mu.Unlock()
		return cc.conn, nil
	}

	if cond, ok := t.pending[host]; ok {
		cond.Wait()
		if cc, ok := t.connections[host]; ok && cc.conn.CanTakeNewRequest() {
			cc.lastActive = time.Now()
			t.mu.Unlock()
			return cc.conn, nil
		}
	}

	if len(t.connections) >= utlsMaxConns {
		t.mu.Unlock()
		return nil, fmt.Errorf("utls: connection pool full (%d/%d) for %s", len(t.connections), utlsMaxConns, host)
	}

	cond := sync.NewCond(&t.mu)
	t.pending[host] = cond
	t.mu.Unlock()

	h2Conn, err := t.createConnection(host, addr)

	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.pending, host)
	cond.Broadcast()

	if err != nil {
		return nil, err
	}

	t.connections[host] = &cachedConn{
		conn:       h2Conn,
		lastActive: time.Now(),
	}
	return h2Conn, nil
}

func (t *utlsRoundTripper) createConnection(host, addr string) (*http2.ClientConn, error) {
	conn, err := t.dialer.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}

	tlsConfig := &tls.Config{ServerName: host}
	tlsConn := tls.UClient(conn, tlsConfig, tls.HelloChrome_Auto)

	if err := tlsConn.Handshake(); err != nil {
		conn.Close()
		return nil, err
	}

	tr := &http2.Transport{
		ReadIdleTimeout: 30 * time.Second,
	}
	h2Conn, err := tr.NewClientConn(tlsConn)
	if err != nil {
		tlsConn.Close()
		return nil, err
	}

	return h2Conn, nil
}

func (t *utlsRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	hostname := req.URL.Hostname()
	port := req.URL.Port()
	if port == "" {
		port = "443"
	}
	addr := net.JoinHostPort(hostname, port)

	h2Conn, err := t.getOrCreateConnection(hostname, addr)
	if err != nil {
		return nil, err
	}

	resp, err := h2Conn.RoundTrip(req)
	if err != nil {
		t.mu.Lock()
		if cached, ok := t.connections[hostname]; ok && cached.conn == h2Conn {
			delete(t.connections, hostname)
		}
		t.mu.Unlock()

		// Retry once with a fresh TLS connection if the cached connection was stale/dead
		if freshH2Conn, errFresh := t.getOrCreateConnection(hostname, addr); errFresh == nil {
			respFresh, errFreshRound := freshH2Conn.RoundTrip(req)
			if errFreshRound == nil {
				t.mu.Lock()
				if cached, ok := t.connections[hostname]; ok && cached.conn == freshH2Conn {
					cached.lastActive = time.Now()
				}
				t.lastActive = time.Now()
				t.mu.Unlock()
				return respFresh, nil
			}
		}

		return nil, err
	}

	t.mu.Lock()
	if cached, ok := t.connections[hostname]; ok && cached.conn == h2Conn {
		cached.lastActive = time.Now()
	}
	t.lastActive = time.Now()
	t.mu.Unlock()

	return resp, nil
}

// utlsProtectedHosts contains the hosts that should use utls Chrome TLS fingerprint
// to bypass Cloudflare's TLS fingerprinting.
var utlsProtectedHosts = map[string]struct{}{
	"api.anthropic.com": {},
	"chatgpt.com":       {},
	"chat.qwen.ai":      {}, // Qwen API requires Chrome fingerprint through Clash proxy
}

// fallbackRoundTripper uses utls for protected HTTPS hosts and falls back to
// standard transport for all other requests.
type fallbackRoundTripper struct {
	utls     http.RoundTripper
	fallback http.RoundTripper
}

func (f *fallbackRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme == "https" {
		if _, ok := utlsProtectedHosts[strings.ToLower(req.URL.Hostname())]; ok {
			return f.utls.RoundTrip(req)
		}
	}
	return f.fallback.RoundTrip(req)
}

var (
	utlsRTsMu sync.RWMutex
	utlsRTs   = make(map[string]*utlsRoundTripper)
)

func getUtlsRoundTripper(proxyURL string) *utlsRoundTripper {
	utlsRTsMu.RLock()
	rt, ok := utlsRTs[proxyURL]
	utlsRTsMu.RUnlock()
	if ok {
		rt.mu.Lock()
		rt.lastActive = time.Now()
		rt.mu.Unlock()
		return rt
	}

	utlsRTsMu.Lock()
	defer utlsRTsMu.Unlock()
	if rt, ok = utlsRTs[proxyURL]; ok {
		rt.mu.Lock()
		rt.lastActive = time.Now()
		rt.mu.Unlock()
		return rt
	}
	rt = newUtlsRoundTripper(proxyURL)
	utlsRTs[proxyURL] = rt
	return rt
}

func init() {
	go startUtlsScavenger()
}

func startUtlsScavenger() {
	ticker := time.NewTicker(30 * time.Second)
	for range ticker.C {
		cleanIdleUtlsRTs()
	}
}

func cleanIdleUtlsRTs() {
	utlsRTsMu.Lock()
	defer utlsRTsMu.Unlock()

	now := time.Now()
	idleTimeout := 90 * time.Second

	for proxyURL, rt := range utlsRTs {
		rt.mu.Lock()
		for host, cc := range rt.connections {
			if !cc.conn.CanTakeNewRequest() || now.Sub(cc.lastActive) > idleTimeout {
				_ = cc.conn.Close()
				delete(rt.connections, host)
			}
		}

		if len(rt.connections) == 0 && len(rt.pending) == 0 && now.Sub(rt.lastActive) > idleTimeout {
			delete(utlsRTs, proxyURL)
		}
		rt.mu.Unlock()
	}
}

// NewUtlsHTTPClient creates an HTTP client using utls Chrome TLS fingerprint.
// Use this for provider requests that need a Chrome-like TLS fingerprint.
// Falls back to standard transport for non-HTTPS requests.
func NewUtlsHTTPClient(ctx context.Context, cfg *config.Config, auth *cliproxyauth.Auth, timeout time.Duration) *http.Client {
	var proxyURL string
	if auth != nil {
		proxyURL = strings.TrimSpace(auth.ProxyURL)
	}
	if proxyURL == "" && cfg != nil {
		proxyURL = strings.TrimSpace(cfg.ProxyURL)
	}

	var ctxRoundTripper http.RoundTripper
	if ctx != nil {
		ctxRoundTripper, _ = ctx.Value("cliproxy.roundtripper").(http.RoundTripper)
	}

	var utlsRT http.RoundTripper = getUtlsRoundTripper(proxyURL)
	var standardTransport http.RoundTripper = http.DefaultTransport
	if proxyURL != "" {
		if transport := getProxyTransport(proxyURL); transport != nil {
			standardTransport = transport
		}
	} else if ctxRoundTripper != nil {
		utlsRT = ctxRoundTripper
		standardTransport = ctxRoundTripper
	}

	client := &http.Client{
		Transport: &fallbackRoundTripper{
			utls:     utlsRT,
			fallback: standardTransport,
		},
	}
	if timeout > 0 {
		client.Timeout = timeout
	}
	return client
}
