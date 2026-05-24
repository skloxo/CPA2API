package management

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"golang.org/x/crypto/bcrypt"
)

// PostSetupManagementKey handles POST /v0/management/setup requests.
// It allows initially setting up a secure management password when none is configured,
// hashes it using bcrypt, and persists the hash to config.yaml.
func (h *Handler) PostSetupManagementKey(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Verify that setup is actually allowed (no password has ever been configured)
	secretHash := h.cfg.RemoteManagement.SecretKey
	envSecret := h.envSecret
	localPassword := h.localPassword

	if secretHash != "" || envSecret != "" || localPassword != "" {
		c.JSON(http.StatusForbidden, gin.H{"error": "setup_disabled", "message": "Management key has already been configured"})
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body", "message": "Invalid request body"})
		return
	}

	password := strings.TrimSpace(req.Password)
	if password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password_required", "message": "Password cannot be empty"})
		return
	}

	// Generate bcrypt hash of the password
	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "hash_failed", "message": fmt.Sprintf("Failed to hash password: %v", err)})
		return
	}
	hashed := string(hashedBytes)

	// Solidify/Persist the password hash into the config.yaml file
	err = config.SaveConfigPreserveCommentsUpdateNestedScalar(h.configFilePath, []string{"remote-management", "secret-key"}, hashed)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed", "message": fmt.Sprintf("Failed to save password to config file: %v", err)})
		return
	}

	// Update the in-memory configuration reference
	h.cfg.RemoteManagement.SecretKey = hashed

	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "Management password successfully configured and persisted",
	})
}

// PostChangeManagementKey handles POST /v0/management/change-password requests.
// It allows authenticated users to change the management password,
// hashes it using bcrypt, and persists the hash to config.yaml.
func (h *Handler) PostChangeManagementKey(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()

	var req struct {
		NewPassword string `json:"new_password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_body", "message": "Invalid request body"})
		return
	}

	newPassword := strings.TrimSpace(req.NewPassword)
	if newPassword == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "password_required", "message": "New password cannot be empty"})
		return
	}

	// Generate bcrypt hash of the password
	hashedBytes, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "hash_failed", "message": fmt.Sprintf("Failed to hash password: %v", err)})
		return
	}
	hashed := string(hashedBytes)

	// Solidify/Persist the password hash into the config.yaml file
	err = config.SaveConfigPreserveCommentsUpdateNestedScalar(h.configFilePath, []string{"remote-management", "secret-key"}, hashed)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "save_failed", "message": fmt.Sprintf("Failed to save password to config file: %v", err)})
		return
	}

	// Update the in-memory configuration reference
	h.cfg.RemoteManagement.SecretKey = hashed

	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "Management password successfully changed and persisted",
	})
}
