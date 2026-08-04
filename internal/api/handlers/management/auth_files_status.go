package management

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/qwen"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
)

func (h *Handler) GetAuthStatus(c *gin.Context) {
	state := strings.TrimSpace(c.Query("state"))
	if state == "" {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}
	if err := ValidateOAuthState(state); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "error": "invalid state"})
		return
	}

	_, status, ok := GetOAuthSession(state)
	if !ok {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
		return
	}
	if status != "" {
		c.JSON(http.StatusOK, gin.H{"status": "error", "error": status})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "wait"})
}

// PopulateAuthContext extracts request info and adds it to the context
func PopulateAuthContext(ctx context.Context, c *gin.Context) context.Context {
	info := &coreauth.RequestInfo{
		Query:   c.Request.URL.Query(),
		Headers: c.Request.Header,
	}
	return coreauth.WithRequestInfo(ctx, info)
}

// PostAuthFileRefresh handles POST /auth-files/:name/refresh requests.
// It retrieves the credential file by name, detects if it's a Qwen credential,
// and if it is, performs a token refresh by calling SignIn and updating the storage.
func (h *Handler) PostAuthFileRefresh(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "handler not initialized"})
		return
	}

	name := strings.TrimSpace(c.Param("name"))
	if isUnsafeAuthFileName(name) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid name"})
		return
	}
	if !strings.HasSuffix(strings.ToLower(name), ".json") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name must end with .json"})
		return
	}

	fullPath := filepath.Join(h.cfg.AuthDir, name)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusNotFound, gin.H{"error": "auth file not found"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to read file: %v", err)})
		}
		return
	}

	typeVal := gjson.GetBytes(data, "type").String()
	isQwen := strings.EqualFold(typeVal, "qwen") || strings.HasPrefix(strings.ToLower(name), "qwen-")

	if !isQwen {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only Qwen auth files are currently supported for manual refresh"})
		return
	}

	email := strings.TrimSpace(gjson.GetBytes(data, "email").String())
	password := strings.TrimSpace(gjson.GetBytes(data, "password").String())
	if email == "" || password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "auth file missing email or password required for refresh"})
		return
	}

	// Sign in with Qwen (proxy is resolved from provider config, not from credential)
	qwenAuthSvc := qwen.NewQwenAuth(h.cfg)
	result, err := qwenAuthSvc.SignIn(c.Request.Context(), email, password, "")
	if err != nil {
		log.Errorf("qwen refresh failed for %s: %v", email, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": fmt.Sprintf("Qwen sign-in failed: %v", err)})
		return
	}

	// Build token storage and save to file
	storage := &qwen.QwenTokenStorage{
		AccessToken: result.Token,
		Email:       email,
		Expired:     result.Expired,
		Password:    password,
	}

	if err := storage.SaveTokenToFile(fullPath); err != nil {
		log.Errorf("qwen: failed to save refreshed token for %s: %v", email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save token to file: %v", err)})
		return
	}

	// Reload/Re-register the credential in core auth manager memory
	if err := h.registerAuthFromFile(c.Request.Context(), fullPath, nil); err != nil {
		log.Errorf("qwen: failed to re-register auth from file: %v", err)
	}

	log.Infof("qwen token manually refreshed for %s", email)
	c.JSON(http.StatusOK, gin.H{"status": "success", "message": "token refreshed successfully", "email": email})
}
