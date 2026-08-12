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
	Cookie   string `json:"cookie,omitempty"`
	Proxy    string `json:"proxy,omitempty"`
}

// PostQwenLogin handles POST /qwen-login requests.
// It authenticates with Qwen using email/password or saves provided cookies & tokens directly.
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
	userCookie := strings.TrimSpace(req.Cookie)
	proxyURL := strings.TrimSpace(req.Proxy)
	if proxyURL == "" {
		proxyURL = h.cfg.ProxyURL
	}

	var accessToken, expired, finalCookie string
	if password != "" && !strings.HasPrefix(password, "eyJ") {
		// Sign in with Qwen via email/password if provided
		auth := qwen.NewQwenAuth(h.cfg)
		result, err := auth.SignIn(context.Background(), email, password, proxyURL)
		if err != nil && userCookie == "" {
			log.Errorf("qwen login failed for %s: %v", email, err)
			c.JSON(http.StatusBadGateway, gin.H{"status": "error", "message": err.Error()})
			return
		}
		if result != nil {
			accessToken = result.Token
			expired = result.Expired
			finalCookie = result.Cookie
		}
	} else if strings.HasPrefix(password, "eyJ") && accessToken == "" {
		accessToken = password
	}

	if userCookie != "" {
		if finalCookie != "" {
			finalCookie = finalCookie + "; " + userCookie
		} else {
			finalCookie = userCookie
		}
	}

	// Try extracting token from userCookie if accessToken is still empty
	if accessToken == "" && userCookie != "" {
		for _, part := range strings.Split(userCookie, ";") {
			part = strings.TrimSpace(part)
			if strings.HasPrefix(part, "token=") {
				accessToken = strings.TrimPrefix(part, "token=")
				break
			}
		}
	}

	if email == "" && accessToken != "" {
		// Auto-derive email from token if not explicitly provided
		if strings.Count(accessToken, ".") == 2 {
			email = "qwen-auth-" + accessToken[len(accessToken)-12:] + "@qwen.ai"
		} else {
			email = "qwen-user-" + accessToken[:minInt(16, len(accessToken))]
		}
	}

	if email == "" {
		c.JSON(http.StatusBadRequest, gin.H{"status": "error", "message": "email or token/cookie is required"})
		return
	}

	// Build token storage
	storage := &qwen.QwenTokenStorage{
		AccessToken: accessToken,
		Cookie:      finalCookie,
		Email:       email,
		Expired:     expired,
		Password:    password,
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
	c.JSON(http.StatusOK, gin.H{"status": "success", "email": email, "file": fileName})
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
