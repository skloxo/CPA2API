package management

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"
)

const (
	latestReleaseURL       = "https://api.github.com/repos/skloxo/CPA2API/releases/latest"
	latestReleaseUserAgent = "CLIProxyAPI"
)

func (h *Handler) GetConfig(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(200, gin.H{})
		return
	}
	c.JSON(200, new(*h.cfg))
}

type releaseInfo struct {
	TagName string `json:"tag_name"`
	Name    string `json:"name"`
}

// GetLatestVersion returns the latest release version from GitHub without downloading assets.
func (h *Handler) GetLatestVersion(c *gin.Context) {
	client := &http.Client{Timeout: 10 * time.Second}
	proxyURL := ""
	if h != nil && h.cfg != nil {
		proxyURL = strings.TrimSpace(h.cfg.ProxyURL)
	}
	if proxyURL != "" {
		sdkCfg := &sdkconfig.SDKConfig{ProxyURL: proxyURL}
		util.SetProxy(sdkCfg, client)
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "request_create_failed", "message": err.Error()})
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", latestReleaseUserAgent)

	resp, err := client.Do(req)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "request_failed", "message": err.Error()})
		return
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.WithError(errClose).Debug("failed to close latest version response body")
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		c.JSON(http.StatusOK, gin.H{"latest-version": "No release published yet on skloxo/CPA2API. Please publish a GitHub Release!"})
		return
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		c.JSON(http.StatusBadGateway, gin.H{"error": "unexpected_status", "message": fmt.Sprintf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))})
		return
	}

	var info releaseInfo
	if errDecode := json.NewDecoder(resp.Body).Decode(&info); errDecode != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "decode_failed", "message": errDecode.Error()})
		return
	}

	version := strings.TrimSpace(info.TagName)
	if version == "" {
		version = strings.TrimSpace(info.Name)
	}
	if version == "" {
		c.JSON(http.StatusBadGateway, gin.H{"error": "invalid_response", "message": "missing release version"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"latest-version": version})
}

func WriteConfig(path string, data []byte) error {
	data = config.NormalizeCommentIndentation(data)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, errWrite := f.Write(data); errWrite != nil {
		_ = f.Close()
		return errWrite
	}
	if errSync := f.Sync(); errSync != nil {
		_ = f.Close()
		return errSync
	}
	return f.Close()
}

func (h *Handler) PutConfigYAML(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_yaml", "message": "cannot read request body"})
		return
	}
	var cfg config.Config
	if err = yaml.Unmarshal(body, &cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_yaml", "message": err.Error()})
		return
	}
	// Validate config using LoadConfigOptional with optional=false to enforce parsing
	tmpFile, err := os.CreateTemp("", "config-validate-*.yaml")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "write_failed", "message": err.Error()})
		return
	}
	tempFile := tmpFile.Name()
	if _, errWrite := tmpFile.Write(body); errWrite != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tempFile)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "write_failed", "message": errWrite.Error()})
		return
	}
	if errClose := tmpFile.Close(); errClose != nil {
		_ = os.Remove(tempFile)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "write_failed", "message": errClose.Error()})
		return
	}
	defer func() {
		_ = os.Remove(tempFile)
	}()
	_, err = config.LoadConfigOptional(tempFile, false)
	if err != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": "invalid_config", "message": err.Error()})
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if WriteConfig(h.configFilePath, body) != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "write_failed", "message": "failed to write config"})
		return
	}
	// Reload into handler to keep memory in sync
	newCfg, err := config.LoadConfig(h.configFilePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "reload_failed", "message": err.Error()})
		return
	}
	h.cfg = newCfg
	c.JSON(http.StatusOK, gin.H{"ok": true, "changed": []string{"config"}})
}

// GetConfigYAML returns the raw config.yaml file bytes without re-encoding.
// It preserves comments and original formatting/styles.
func (h *Handler) GetConfigYAML(c *gin.Context) {
	data, err := os.ReadFile(h.configFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "not_found", "message": "config file not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "read_failed", "message": err.Error()})
		return
	}
	c.Header("Content-Type", "application/yaml; charset=utf-8")
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	// Write raw bytes as-is
	_, _ = c.Writer.Write(data)
}

// Debug
func (h *Handler) GetDebug(c *gin.Context) { c.JSON(200, gin.H{"debug": h.cfg.Debug}) }
func (h *Handler) PutDebug(c *gin.Context) { h.updateBoolField(c, func(v bool) { h.cfg.Debug = v }) }

// UsageStatisticsEnabled
func (h *Handler) GetUsageStatisticsEnabled(c *gin.Context) {
	c.JSON(200, gin.H{"usage-statistics-enabled": h.cfg.UsageStatisticsEnabled})
}
func (h *Handler) PutUsageStatisticsEnabled(c *gin.Context) {
	h.updateBoolField(c, func(v bool) { h.cfg.UsageStatisticsEnabled = v })
}

// UsageStatisticsEnabled
func (h *Handler) GetLoggingToFile(c *gin.Context) {
	c.JSON(200, gin.H{"logging-to-file": h.cfg.LoggingToFile})
}
func (h *Handler) PutLoggingToFile(c *gin.Context) {
	h.updateBoolField(c, func(v bool) { h.cfg.LoggingToFile = v })
}

// LogsMaxTotalSizeMB
func (h *Handler) GetLogsMaxTotalSizeMB(c *gin.Context) {
	c.JSON(200, gin.H{"logs-max-total-size-mb": h.cfg.LogsMaxTotalSizeMB})
}
func (h *Handler) PutLogsMaxTotalSizeMB(c *gin.Context) {
	var body struct {
		Value *int `json:"value"`
	}
	if errBindJSON := c.ShouldBindJSON(&body); errBindJSON != nil || body.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	value := *body.Value
	if value < 0 {
		value = 0
	}
	h.cfg.LogsMaxTotalSizeMB = value
	h.persist(c)
}

// ErrorLogsMaxFiles
func (h *Handler) GetErrorLogsMaxFiles(c *gin.Context) {
	c.JSON(200, gin.H{"error-logs-max-files": h.cfg.ErrorLogsMaxFiles})
}
func (h *Handler) PutErrorLogsMaxFiles(c *gin.Context) {
	var body struct {
		Value *int `json:"value"`
	}
	if errBindJSON := c.ShouldBindJSON(&body); errBindJSON != nil || body.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	value := *body.Value
	if value < 0 {
		value = 10
	}
	h.cfg.ErrorLogsMaxFiles = value
	h.persist(c)
}

// Request log
func (h *Handler) GetRequestLog(c *gin.Context) { c.JSON(200, gin.H{"request-log": h.cfg.RequestLog}) }
func (h *Handler) PutRequestLog(c *gin.Context) {
	h.updateBoolField(c, func(v bool) { h.cfg.RequestLog = v })
}

// Websocket auth
func (h *Handler) GetWebsocketAuth(c *gin.Context) {
	c.JSON(200, gin.H{"ws-auth": h.cfg.WebsocketAuth})
}
func (h *Handler) PutWebsocketAuth(c *gin.Context) {
	h.updateBoolField(c, func(v bool) { h.cfg.WebsocketAuth = v })
}

// Request retry
func (h *Handler) GetRequestRetry(c *gin.Context) {
	c.JSON(200, gin.H{"request-retry": h.cfg.RequestRetry})
}
func (h *Handler) PutRequestRetry(c *gin.Context) {
	h.updateIntField(c, func(v int) { h.cfg.RequestRetry = v })
}

// Max retry interval
func (h *Handler) GetMaxRetryInterval(c *gin.Context) {
	c.JSON(200, gin.H{"max-retry-interval": h.cfg.MaxRetryInterval})
}
func (h *Handler) PutMaxRetryInterval(c *gin.Context) {
	h.updateIntField(c, func(v int) { h.cfg.MaxRetryInterval = v })
}

// ForceModelPrefix
func (h *Handler) GetForceModelPrefix(c *gin.Context) {
	c.JSON(200, gin.H{"force-model-prefix": h.cfg.ForceModelPrefix})
}
func (h *Handler) PutForceModelPrefix(c *gin.Context) {
	h.updateBoolField(c, func(v bool) { h.cfg.ForceModelPrefix = v })
}

func normalizeRoutingStrategy(strategy string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(strategy))
	switch normalized {
	case "", "round-robin", "roundrobin", "rr":
		return "round-robin", true
	case "fill-first", "fillfirst", "ff":
		return "fill-first", true
	default:
		return "", false
	}
}

// RoutingStrategy
func (h *Handler) GetRoutingStrategy(c *gin.Context) {
	strategy, ok := normalizeRoutingStrategy(h.cfg.Routing.Strategy)
	if !ok {
		c.JSON(200, gin.H{"strategy": strings.TrimSpace(h.cfg.Routing.Strategy)})
		return
	}
	c.JSON(200, gin.H{"strategy": strategy})
}
func (h *Handler) PutRoutingStrategy(c *gin.Context) {
	var body struct {
		Value *string `json:"value"`
	}
	if errBindJSON := c.ShouldBindJSON(&body); errBindJSON != nil || body.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	normalized, ok := normalizeRoutingStrategy(*body.Value)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid strategy"})
		return
	}
	h.cfg.Routing.Strategy = normalized
	h.persist(c)
}

// Proxy URL
func (h *Handler) GetProxyURL(c *gin.Context) { c.JSON(200, gin.H{"proxy-url": h.cfg.ProxyURL}) }
func (h *Handler) PutProxyURL(c *gin.Context) {
	h.updateStringField(c, func(v string) { h.cfg.ProxyURL = v })
}
func (h *Handler) DeleteProxyURL(c *gin.Context) {
	h.cfg.ProxyURL = ""
	h.persist(c)
}

type gitHubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type gitHubRelease struct {
	TagName string        `json:"tag_name"`
	Name    string        `json:"name"`
	Assets  []gitHubAsset `json:"assets"`
}

func matchAsset(assets []gitHubAsset, goos, goarch string) (string, string) {
	archStr := goarch
	if goarch == "arm64" {
		archStr = "aarch64"
	}

	expectedExt := ".tar.gz"
	if goos == "windows" {
		expectedExt = ".zip"
	}

	for _, asset := range assets {
		nameLower := strings.ToLower(asset.Name)
		if strings.Contains(nameLower, strings.ToLower(goos)) &&
			strings.Contains(nameLower, strings.ToLower(archStr)) &&
			strings.HasSuffix(nameLower, expectedExt) {
			return asset.BrowserDownloadURL, asset.Name
		}
	}
	return "", ""
}

func extractZipBinary(zipData []byte) ([]byte, error) {
	r, errReader := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if errReader != nil {
		return nil, errReader
	}
	for _, f := range r.File {
		name := strings.ToLower(f.Name)
		base := filepath.Base(name)
		if base == "cli-proxy-api" || base == "cli-proxy-api.exe" {
			rc, errOpen := f.Open()
			if errOpen != nil {
				return nil, errOpen
			}
			defer func() {
				if errClose := rc.Close(); errClose != nil {
					log.WithError(errClose).Warn("failed to close zip file reader")
				}
			}()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("binary not found in zip archive")
}

func extractTarGzBinary(tarGzData []byte) ([]byte, error) {
	gr, errGzip := gzip.NewReader(bytes.NewReader(tarGzData))
	if errGzip != nil {
		return nil, errGzip
	}
	defer func() {
		if errClose := gr.Close(); errClose != nil {
			log.WithError(errClose).Warn("failed to close gzip reader")
		}
	}()

	tr := tar.NewReader(gr)
	for {
		hdr, errTar := tr.Next()
		if errTar == io.EOF {
			break
		}
		if errTar != nil {
			return nil, errTar
		}
		name := strings.ToLower(hdr.Name)
		base := filepath.Base(name)
		if base == "cli-proxy-api" || base == "cli-proxy-api.exe" {
			return io.ReadAll(tr)
		}
	}
	return nil, fmt.Errorf("binary not found in tar.gz archive")
}

// UpgradeVersion handles downloading the latest release binary, updating the local binary,
// updating .env file (if it exists), and restarting.
func (h *Handler) UpgradeVersion(c *gin.Context) {
	client := &http.Client{Timeout: 60 * time.Second}
	proxyURL := ""
	h.mu.Lock()
	if h.cfg != nil {
		proxyURL = strings.TrimSpace(h.cfg.ProxyURL)
	}
	h.mu.Unlock()
	if proxyURL != "" {
		sdkCfg := &sdkconfig.SDKConfig{ProxyURL: proxyURL}
		util.SetProxy(sdkCfg, client)
	}

	// 1. Fetch latest release info
	req, errReq := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, latestReleaseURL, nil)
	if errReq != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "request_create_failed", "message": errReq.Error()})
		return
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", latestReleaseUserAgent)

	resp, errResp := client.Do(req)
	if errResp != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "request_failed", "message": errResp.Error()})
		return
	}
	defer func() {
		if errClose := resp.Body.Close(); errClose != nil {
			log.WithError(errClose).Debug("failed to close latest version response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		c.JSON(http.StatusBadGateway, gin.H{"error": "unexpected_status", "message": fmt.Sprintf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))})
		return
	}

	var release gitHubRelease
	if errDecode := json.NewDecoder(resp.Body).Decode(&release); errDecode != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "decode_failed", "message": errDecode.Error()})
		return
	}

	targetVersion := strings.TrimSpace(release.TagName)
	if targetVersion == "" {
		targetVersion = strings.TrimSpace(release.Name)
	}
	if targetVersion == "" {
		c.JSON(http.StatusBadGateway, gin.H{"error": "invalid_response", "message": "missing release version"})
		return
	}

	// 2. Find matching asset for GOOS/GOARCH
	downloadURL, assetName := matchAsset(release.Assets, runtime.GOOS, runtime.GOARCH)
	if downloadURL == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "no_matching_asset",
			"message": fmt.Sprintf("No release asset found matching platform: %s_%s", runtime.GOOS, runtime.GOARCH),
		})
		return
	}

	log.Infof("Upgrading: downloading asset %s from %s", assetName, downloadURL)

	// 3. Download the asset
	reqDownload, errReqDown := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, downloadURL, nil)
	if errReqDown != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "request_create_failed", "message": errReqDown.Error()})
		return
	}
	reqDownload.Header.Set("User-Agent", latestReleaseUserAgent)

	respDownload, errRespDown := client.Do(reqDownload)
	if errRespDown != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "download_failed", "message": errRespDown.Error()})
		return
	}
	defer func() {
		if errClose := respDownload.Body.Close(); errClose != nil {
			log.WithError(errClose).Debug("failed to close download response body")
		}
	}()

	if respDownload.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(respDownload.Body, 1024))
		c.JSON(http.StatusBadGateway, gin.H{"error": "download_unexpected_status", "message": fmt.Sprintf("status %d: %s", respDownload.StatusCode, strings.TrimSpace(string(body)))})
		return
	}

	archiveData, errReadArchive := io.ReadAll(respDownload.Body)
	if errReadArchive != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "read_failed", "message": errReadArchive.Error()})
		return
	}

	// 4. Extract the binary
	var binaryData []byte
	var errExtract error
	if strings.HasSuffix(strings.ToLower(assetName), ".zip") {
		binaryData, errExtract = extractZipBinary(archiveData)
	} else {
		binaryData, errExtract = extractTarGzBinary(archiveData)
	}
	if errExtract != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "extraction_failed", "message": errExtract.Error()})
		return
	}

	// 5. Replace current running executable
	execPath, errExecPath := os.Executable()
	if errExecPath != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "executable_path_failed", "message": errExecPath.Error()})
		return
	}

	// On Windows, we need to rename the old executable first before writing the new one.
	// On Unix, renaming the temp file directly over the target path is atomic and safe.
	execDir := filepath.Dir(execPath)
	tmpFile, errTmp := os.CreateTemp(execDir, "cli-proxy-api-upgrade-*")
	if errTmp != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "temp_file_failed", "message": errTmp.Error()})
		return
	}
	tmpPath := tmpFile.Name()

	defer func() {
		// Cleanup tmp file if it still exists
		_ = os.Remove(tmpPath)
	}()

	if _, errWrite := tmpFile.Write(binaryData); errWrite != nil {
		_ = tmpFile.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "write_failed", "message": errWrite.Error()})
		return
	}

	if errChmod := tmpFile.Chmod(0755); errChmod != nil {
		_ = tmpFile.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "chmod_failed", "message": errChmod.Error()})
		return
	}

	if errClose := tmpFile.Close(); errClose != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "close_failed", "message": errClose.Error()})
		return
	}

	if runtime.GOOS == "windows" {
		oldPath := execPath + ".old"
		_ = os.Remove(oldPath) // remove previous backup if it exists
		if errRenameOld := os.Rename(execPath, oldPath); errRenameOld != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "rename_running_failed", "message": errRenameOld.Error()})
			return
		}
		if errRenameNew := os.Rename(tmpPath, execPath); errRenameNew != nil {
			// rollback rename
			_ = os.Rename(oldPath, execPath)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "rename_new_failed", "message": errRenameNew.Error()})
			return
		}
	} else {
		if errRename := os.Rename(tmpPath, execPath); errRename != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "replace_failed", "message": errRename.Error()})
			return
		}
	}

	// 6. Automatically update host .env file if mounted at /app/.env or present locally
	envPath := "/app/.env"
	if _, errStat := os.Stat(envPath); os.IsNotExist(errStat) {
		// Fallback to local .env in current working directory if not in docker
		envPath = ".env"
	}

	envUpdated := false
	if _, errStat := os.Stat(envPath); errStat == nil {
		envData, errReadEnv := os.ReadFile(envPath)
		if errReadEnv == nil {
			lines := strings.Split(string(envData), "\n")
			for i, line := range lines {
				trimmedLine := strings.TrimSpace(line)
				if strings.HasPrefix(trimmedLine, "CPA_VERSION=") {
					lines[i] = fmt.Sprintf("CPA_VERSION=%s", targetVersion)
					envUpdated = true
				}
			}
			if envUpdated {
				newEnvData := []byte(strings.Join(lines, "\n"))
				// Write directly to preserve inode for Docker bind mount
				fEnv, errOpenEnv := os.OpenFile(envPath, os.O_WRONLY|os.O_TRUNC, 0644)
				if errOpenEnv == nil {
					_, errWriteEnv := fEnv.Write(newEnvData)
					_ = fEnv.Close()
					if errWriteEnv == nil {
						log.Infof("Successfully updated .env file at %s to CPA_VERSION=%s", envPath, targetVersion)
					}
				}
			}
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"ok":          true,
		"version":     targetVersion,
		"env_updated": envUpdated,
		"message":     "Upgrade successful! Server is restarting...",
	})

	// 7. Restart the process after a short delay
	go func() {
		time.Sleep(1 * time.Second)
		log.Infof("Server exiting to trigger restart for upgrade to %s", targetVersion)
		os.Exit(0)
	}()
}
