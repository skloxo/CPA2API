package cmd

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	log "github.com/sirupsen/logrus"
)

// DoQwenLogin authenticates with Qwen using email + password and saves the token.
// Unlike OAuth-based providers, Qwen uses direct email+password authentication
// with SHA256 password hashing. The obtained JWT token is saved to the auth directory
// for automatic renewal via the Refresh() mechanism.
//
// Parameters:
//   - cfg: The application configuration containing proxy and auth directory settings
//   - email: Qwen account email (if empty, prompts interactively)
//   - password: Qwen account password (if empty, prompts interactively)
//   - options: Login options
func DoQwenLogin(cfg *config.Config, email, password string, options *LoginOptions) {
	if options == nil {
		options = &LoginOptions{}
	}

	// Interactive prompts if credentials not provided
	if email == "" {
		email = promptInput("Enter Qwen email: ")
	}
	if password == "" {
		password = promptInput("Enter Qwen password: ")
	}

	email = strings.TrimSpace(email)
	password = strings.TrimSpace(password)

	if email == "" || password == "" {
		log.Errorf("Qwen login: email and password are required")
		return
	}

	manager := newAuthManager()
	authOpts := &sdkAuth.LoginOptions{
		NoBrowser: options.NoBrowser,
		Metadata: map[string]string{
			"email":    email,
			"password": password,
		},
		Prompt: options.Prompt,
	}

	record, savedPath, err := manager.Login(context.Background(), "qwen", cfg, authOpts)
	if err != nil {
		log.Errorf("Qwen authentication failed: %v", err)
		return
	}

	if savedPath != "" {
		fmt.Printf("Authentication saved to %s\n", savedPath)
	}
	if record != nil && record.Label != "" {
		fmt.Printf("Authenticated as %s\n", record.Label)
	}
	fmt.Println("Qwen authentication successful!")
}

// promptInput reads a line of user input from stdin with a prompt message.
func promptInput(prompt string) string {
	fmt.Print(prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return strings.TrimSpace(scanner.Text())
	}
	return ""
}
