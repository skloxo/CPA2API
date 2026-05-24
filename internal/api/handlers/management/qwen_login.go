package management

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/auth/qwen"
	log "github.com/sirupsen/logrus"
)

type qwenLoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Proxy    string `json:"proxy,omitempty"`
}

// PostQwenLogin handles POST /qwen-login requests.
// It authenticates with Qwen using email/password, persists the resulting token,
// and returns a success/error JSON response matching the frontend API contract.
func (h *Handler) PostQwenLogin(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "handler not initialized"})
		return
	}

	var req qwenLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "invalid request body"})
		return
	}

	email := strings.TrimSpace(req.Email)
	password := strings.TrimSpace(req.Password)
	proxy := strings.TrimSpace(req.Proxy)
	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "email is required"})
		return
	}
	if password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "password is required"})
		return
	}

	// Sign in with Qwen
	auth := qwen.NewQwenAuth(h.cfg)
	result, err := auth.SignIn(context.Background(), email, password, proxy)
	if err != nil {
		log.Errorf("qwen login failed for %s: %v", email, err)
		c.JSON(http.StatusBadGateway, gin.H{"status": "error", "message": err.Error()})
		return
	}

	// Build token storage
	storage := &qwen.QwenTokenStorage{
		AccessToken: result.Token,
		Email:       email,
		Expired:     result.Expired,
		Password:    password,
		ProxyURL:    proxy,
	}

	// Inject proxy metadata if provided
	if proxy != "" {
		storage.SetMetadata(map[string]any{
			"proxy": proxy,
		})
	}

	// Persist to auth directory
	fileName := qwen.CredentialFileName(email)
	authFilePath := filepath.Join(h.cfg.AuthDir, fileName)
	if err := storage.SaveTokenToFile(authFilePath); err != nil {
		log.Errorf("qwen: failed to save token for %s: %v", email, err)
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "failed to save credentials"})
		return
	}

	log.Infof("qwen login successful for %s", email)
	c.JSON(http.StatusOK, gin.H{"status": "success", "email": email})
}
