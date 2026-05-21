// Package qwen provides anti-detection fingerprinting, ssxmod cookie management,
// and browser header injection for Qwen API requests.
//
// This file supplements the existing auth.go with anti-ban measures:
//   - Dynamic browser fingerprint generation
//   - ssxmod_itna/ssxmod_itna2 cookie generation and refresh
//   - Realistic browser header injection
package qwen

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// QwenCLIEndpoint is the long-context endpoint for CLI routing.
	QwenCLIEndpoint = "https://portal.qwen.ai"
	// ssxmodRefreshInterval controls how often ssxmod cookies are regenerated.
	ssxmodRefreshInterval = 15 * time.Minute
)

// ==================== Fingerprint Generation ====================

// fingerprintTemplate holds the base fingerprint configuration.
type fingerprintTemplate struct {
	sdkVersion    string
	field3        string
	field4        string
	language      string
	tzOffset      string
	colorDepth    string
	screenInfo    string
	field9        string
	platform      string
	webglRenderer string
	vendor        string
	field11       string
	field13       string
	field14       string
	field15       string
	pluginCount   string
	field29       string
	touchInfo     string
	field32       string
	field35       string
	mode          string
}

// defaultTemplate generates a randomized fingerprint template.
func defaultTemplate() fingerprintTemplate {
	return fingerprintTemplate{
		sdkVersion:    "websdk-2.3.15d",
		field3:        "91",
		field4:        "1|15",
		language:      "zh-CN",
		tzOffset:      "-480",
		colorDepth:    "16705151|12791",
		screenInfo:    "1470|956|283|797|158|0|1470|956|1470|798|0|0",
		field9:        "5",
		platform:      "MacIntel",
		webglRenderer: "ANGLE (Apple, ANGLE Metal Renderer: Apple M4, Unspecified Version)|Google Inc. (Apple)",
		vendor:        "Google Inc.",
		field11:       "10",
		field13:       "30|30",
		field14:       "0",
		field15:       "28",
		pluginCount:   "5",
		field29:       "8",
		touchInfo:     "-1|0|0|0|0",
		field32:       "11",
		field35:       "0",
		mode:          "P",
	}
}

// generateDeviceID creates a random 20-character hex device ID.
func generateDeviceID() string {
	const hexChars = "0123456789abcdef"
	buf := make([]byte, 20)
	for i := range buf {
		n, _ := rand.Int(rand.Reader, big.NewInt(16))
		buf[i] = hexChars[n.Int64()]
	}
	return string(buf)
}

// generateHash creates a random 32-bit integer.
func generateHash() int64 {
	n, _ := rand.Int(rand.Reader, big.NewInt(4294967296))
	return n.Int64()
}

// GenerateFingerprint produces a Qwen-compatible browser fingerprint string.
func GenerateFingerprint() string {
	tpl := defaultTemplate()
	deviceID := generateDeviceID()
	ts := time.Now().UnixMilli()
	pluginHash := generateHash()
	canvasHash := generateHash()
	uaHash1 := generateHash()
	uaHash2 := generateHash()
	urlHash := generateHash()
	docHash, _ := rand.Int(rand.Reader, big.NewInt(91))
	docHashInt := docHash.Int64() + 10

	fields := []string{
		deviceID,
		tpl.sdkVersion,
		"1765348410850",
		tpl.field3,
		tpl.field4,
		tpl.language,
		tpl.tzOffset,
		tpl.colorDepth,
		tpl.screenInfo,
		tpl.field9,
		tpl.platform,
		tpl.field11,
		tpl.webglRenderer,
		tpl.field13,
		tpl.field14,
		tpl.field15,
		fmt.Sprintf("%s|%d", tpl.pluginCount, pluginHash),
		fmt.Sprintf("%d", canvasHash),
		fmt.Sprintf("%d", uaHash1),
		"1",
		"0",
		"1",
		"0",
		tpl.mode,
		"0", "0", "0",
		"416",
		tpl.vendor,
		tpl.field29,
		tpl.touchInfo,
		fmt.Sprintf("%d", uaHash2),
		tpl.field32,
		fmt.Sprintf("%d", ts),
		fmt.Sprintf("%d", urlHash),
		tpl.field35,
		fmt.Sprintf("%d", docHashInt),
	}

	return strings.Join(fields, "^")
}

// ==================== SSXMOD Cookie Manager ====================

var (
	ssxmodMu          sync.RWMutex
	ssxmodItna        string
	ssxmodItna2       string
	ssxmodLastRefresh time.Time
	ssxmodOnce        sync.Once
)

// ssxmodCustomBase64 is a custom Base64 alphabet used by Qwen's ssxmod encoding.
const ssxmodCustomBase64 = "DGi0YA7BemWnQjCl4_bR3f8SKIF9tUz/xhr2oEOgPpac=61ZqwTudLkM5vHyNXsVJ"

// InitSsxmodManager starts the background goroutine that periodically refreshes
// the ssxmod cookie values. Safe to call multiple times.
func InitSsxmodManager() {
	ssxmodOnce.Do(func() {
		refreshSsxmod()
		go func() {
			ticker := time.NewTicker(ssxmodRefreshInterval)
			defer ticker.Stop()
			for range ticker.C {
				refreshSsxmod()
			}
		}()
	})
}

// refreshSsxmod regenerates the ssxmod cookie values from a fresh fingerprint.
func refreshSsxmod() {
	fp := GenerateFingerprint()
	compressed := lzwCompressToCustomBase64(fp)

	ssxmodMu.Lock()
	ssxmodItna = fp
	ssxmodItna2 = compressed
	ssxmodLastRefresh = time.Now()
	ssxmodMu.Unlock()
}

// GetSsxmodItna returns the current ssxmod_itna cookie value.
func GetSsxmodItna() string {
	ssxmodMu.RLock()
	defer ssxmodMu.RUnlock()
	return ssxmodItna
}

// GetSsxmodItna2 returns the current ssxmod_itna2 cookie value.
func GetSsxmodItna2() string {
	ssxmodMu.RLock()
	defer ssxmodMu.RUnlock()
	return ssxmodItna2
}

// ==================== Browser Header Injection ====================

// ApplyBrowserHeaders adds realistic browser headers to the HTTP request
// to evade detection. These headers mimic a Chrome browser on macOS.
func ApplyBrowserHeaders(r *http.Request, stream bool) {
	r.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Safari/537.36")
	r.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en-US;q=0.8,en;q=0.7")
	r.Header.Set("Accept-Encoding", "gzip, deflate, br, zstd")
	r.Header.Set("sec-ch-ua", `"Microsoft Edge";v="143", "Chromium";v="143", "Not A(Brand";v="24"`)
	r.Header.Set("sec-ch-ua-mobile", "?0")
	r.Header.Set("sec-ch-ua-platform", `"macOS"`)
	r.Header.Set("source", "web")
	r.Header.Set("Version", "0.1.13")
	r.Header.Set("bx-v", "2.5.31")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Sec-Fetch-Mode", "cors")
	r.Header.Set("Sec-Fetch-Dest", "empty")
	r.Header.Set("Referer", QwenAPIBaseURL+"/c/guest")
	r.Header.Set("Origin", QwenAPIBaseURL)
	if stream {
		r.Header.Set("Accept", "text/event-stream")
	} else {
		r.Header.Set("Accept", "application/json")
	}
}

// ApplySsxCookies adds the ssxmod cookies to the request.
func ApplySsxCookies(r *http.Request) {
	itna := GetSsxmodItna()
	itna2 := GetSsxmodItna2()
	if itna == "" && itna2 == "" {
		return
	}
	existing := r.Header.Get("Cookie")
	cookie := existing
	if itna != "" {
		if cookie != "" {
			cookie += "; "
		}
		cookie += "ssxmod_itna=" + itna
	}
	if itna2 != "" {
		if cookie != "" {
			cookie += "; "
		}
		cookie += "ssxmod_itna2=" + itna2
	}
	r.Header.Set("Cookie", cookie)
}

// ApplyAllQwenHeaders sets all anti-detection headers and cookies on the request.
func ApplyAllQwenHeaders(r *http.Request, token string, cookie string, stream bool) {
	ApplyBrowserHeaders(r, stream)

	if strings.TrimSpace(token) != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}

	if strings.TrimSpace(cookie) != "" {
		r.Header.Set("Cookie", cookie)
	}

	ApplySsxCookies(r)
}

// ==================== LZW Compression ====================

// lzwCompressToCustomBase64 compresses data using LZW and encodes with Qwen's custom Base64.
func lzwCompressToCustomBase64(data string) string {
	if data == "" {
		return ""
	}

	dict := make(map[string]int)
	dictSize := 0
	for i := 0; i < 256; i++ {
		dict[string(rune(i))] = dictSize
		dictSize++
	}

	w := ""
	var codes []int
	for _, c := range data {
		wc := w + string(c)
		if _, ok := dict[wc]; ok {
			w = wc
		} else {
			if code, ok := dict[w]; ok {
				codes = append(codes, code)
			}
			dict[wc] = dictSize
			dictSize++
			w = string(c)
		}
	}
	if w != "" {
		if code, ok := dict[w]; ok {
			codes = append(codes, code)
		}
	}

	var result []byte
	for _, code := range codes {
		if code < len(ssxmodCustomBase64) {
			result = append(result, ssxmodCustomBase64[code])
		} else {
			result = append(result, ssxmodCustomBase64[code%len(ssxmodCustomBase64)])
		}
	}

	return string(result)
}
