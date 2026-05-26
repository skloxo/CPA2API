//go:build ignore

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	qwenauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/qwen"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor/helps"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type Creds struct {
	AccessToken string `json:"access_token"`
	Cookie      string `json:"cookie"`
}

func main() {
	// Read credentials
	data, err := os.ReadFile("/tmp/cpa2api-auth/qwen-skloxogmail.com.json")
	if err != nil {
		fmt.Printf("Error reading credentials: %v\n", err)
		return
	}

	var creds Creds
	if err := json.Unmarshal(data, &creds); err != nil {
		fmt.Printf("Error unmarshaling credentials: %v\n", err)
		return
	}

	auth := &cliproxyauth.Auth{
		ID:       "qwen-skloxogmail.com",
		Provider: "qwen",
		Metadata: map[string]any{
			"access_token": creds.AccessToken,
			"cookie":       creds.Cookie,
		},
	}

	cfg := &config.Config{
		SDKConfig: config.SDKConfig{
			ProxyURL: "http://127.0.0.1:7897",
		},
	}

	ctx := context.Background()
	httpClient := helps.NewUtlsHTTPClient(cfg, auth, 0)
	qwenauth.InitSsxmodManager()

	// Fetch models
	reqModels, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://chat.qwen.ai/api/models", nil)
	qwenauth.ApplyAllQwenHeaders(reqModels, creds.AccessToken, creds.Cookie, false)
	reqModels.Header.Set("Accept-Encoding", "identity")
	respModels, err := httpClient.Do(reqModels)
	if err == nil {
		bodyModels, _ := io.ReadAll(respModels.Body)
		respModels.Body.Close()

		var apiResp struct {
			Data []struct {
				ID   string `json:"id"`
				Info struct {
					Meta struct {
						ChatType []string `json:"chat_type"`
					} `json:"meta"`
				} `json:"info"`
			} `json:"data"`
		}
		if err := json.Unmarshal(bodyModels, &apiResp); err == nil {
			fmt.Println("Available Qwen Models:")
			for _, entry := range apiResp.Data {
				fmt.Printf("- ID: %s, ChatType: %v\n", entry.ID, entry.Info.Meta.ChatType)
			}
		} else {
			fmt.Printf("Unmarshal error: %v\n", err)
		}
	} else {
		fmt.Printf("Failed to fetch models: %v\n", err)
	}

	models := []string{
		"qwen-vl-max",
		"qwen3-vl-plus",
		"qwen3.7-max",
		"qwen3.6-plus",
	}

	for _, modelName := range models {
		fmt.Printf("\n=========================================\n")
		fmt.Printf("Testing Model: %s\n", modelName)
		fmt.Printf("=========================================\n")

		// 1. Create a new chat
		urlNew := "https://chat.qwen.ai/api/v2/chats/new"
		reqNew, err := http.NewRequestWithContext(ctx, http.MethodPost, urlNew, bytes.NewReader([]byte("{}")))
		if err != nil {
			fmt.Printf("Error creating chat request: %v\n", err)
			continue
		}
		qwenauth.ApplyAllQwenHeaders(reqNew, creds.AccessToken, creds.Cookie, false)
		reqNew.Header.Set("Accept-Encoding", "identity")
		reqNew.Header.Set("Content-Type", "application/json")

		respNew, err := httpClient.Do(reqNew)
		if err != nil {
			fmt.Printf("Chat creation request error: %v\n", err)
			continue
		}

		bodyNew, err := io.ReadAll(respNew.Body)
		respNew.Body.Close()
		if err != nil {
			fmt.Printf("Error reading chat creation body: %v\n", err)
			continue
		}

		var newChatResp struct {
			Success bool `json:"success"`
			Data    struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(bodyNew, &newChatResp); err != nil {
			fmt.Printf("Failed to unmarshal chat creation response: %v\n", err)
			continue
		}
		chatID := newChatResp.Data.ID
		if chatID == "" {
			fmt.Printf("No chat ID returned! Body: %s\n", string(bodyNew))
			continue
		}
		fmt.Printf("Created Chat ID: %s\n", chatID)

		// 2. Try T2T first
		fid := uuid.New().String()
		childID := uuid.New().String()
		ts := time.Now().UnixMilli()
		urlDraw := "https://chat.qwen.ai/api/v2/chat/completions?chat_id=" + chatID

		reqBodyT2T := fmt.Sprintf(`{
			"stream": true,
			"version": "2.1",
			"incremental_output": true,
			"chat_mode": "normal",
			"chat_id": "%s",
			"model": "%s",
			"parent_id": null,
			"timestamp": %d,
			"messages": [
				{
					"fid": "%s",
					"parentId": null,
					"childrenIds": ["%s"],
					"role": "user",
					"content": "hi, say test",
					"user_action": "chat",
					"files": [],
					"timestamp": %d,
					"models": ["%s"],
					"chat_type": "t2t",
					"feature_config": {
						"thinking_enabled": true,
						"output_schema": "phase",
						"thinking_mode": "Auto"
					},
					"extra": {"meta": {"subChatType": "t2t"}},
					"sub_chat_type": "t2t",
					"parent_id": null
				}
			]
		}`, chatID, modelName, ts, fid, childID, ts, modelName)

		reqT2T, _ := http.NewRequestWithContext(ctx, http.MethodPost, urlDraw, bytes.NewReader([]byte(reqBodyT2T)))
		reqT2T.Header.Set("Content-Type", "application/json")
		reqT2T.Header.Set("Accept", "text/event-stream")
		qwenauth.ApplyAllQwenHeaders(reqT2T, creds.AccessToken, creds.Cookie, true)
		respT2T, errT2T := httpClient.Do(reqT2T)
		if errT2T == nil {
			fmt.Printf("T2T response status: %d\n", respT2T.StatusCode)
			scanner := bufio.NewScanner(respT2T.Body)
			lineCount := 0
			for scanner.Scan() {
				line := scanner.Text()
				lineCount++
				if lineCount <= 5 {
					fmt.Printf("T2T line %d: %s\n", lineCount, line)
				} else {
					break
				}
			}
			respT2T.Body.Close()
		} else {
			fmt.Printf("T2T request error: %v\n", errT2T)
		}

		// 3. Try drawing with SSE stream
		var chatIDDraw string
		urlNewDraw := "https://chat.qwen.ai/api/v2/chats/new"
		reqNewDraw, err := http.NewRequestWithContext(ctx, http.MethodPost, urlNewDraw, bytes.NewReader([]byte("{}")))
		if err == nil {
			qwenauth.ApplyAllQwenHeaders(reqNewDraw, creds.AccessToken, creds.Cookie, false)
			reqNewDraw.Header.Set("Accept-Encoding", "identity")
			reqNewDraw.Header.Set("Content-Type", "application/json")
			respNewDraw, errDrawChat := httpClient.Do(reqNewDraw)
			if errDrawChat == nil {
				bodyNewDraw, _ := io.ReadAll(respNewDraw.Body)
				respNewDraw.Body.Close()
				var newChatDrawResp struct {
					Success bool `json:"success"`
					Data    struct {
						ID string `json:"id"`
					} `json:"data"`
				}
				if err := json.Unmarshal(bodyNewDraw, &newChatDrawResp); err == nil {
					chatIDDraw = newChatDrawResp.Data.ID
				}
			}
		}
		if chatIDDraw == "" {
			chatIDDraw = uuid.New().String()
		}
		fmt.Printf("Created Draw Chat ID: %s\n", chatIDDraw)

		fidDraw := uuid.New().String()
		childIDDraw := uuid.New().String()
		urlDrawRequest := "https://chat.qwen.ai/api/v2/chat/completions?chat_id=" + chatIDDraw

		reqBody := fmt.Sprintf(`{
			"stream": true,
			"version": "2.1",
			"incremental_output": true,
			"chat_mode": "normal",
			"chat_id": "%s",
			"model": "%s",
			"parent_id": null,
			"timestamp": %d,
			"messages": [
				{
					"fid": "%s",
					"parentId": null,
					"childrenIds": ["%s"],
					"role": "user",
					"content": "画一只可爱的小猫",
					"user_action": "chat",
					"files": [],
					"timestamp": %d,
					"models": ["%s"],
					"chat_type": "t2i",
					"feature_config": {
						"thinking_enabled": true,
						"output_schema": "phase",
						"thinking_mode": "Auto"
					},
					"extra": {"meta": {"subChatType": "t2i"}},
					"sub_chat_type": "t2i",
					"parent_id": null
				}
			]
		}`, chatIDDraw, modelName, ts, fidDraw, childIDDraw, ts, modelName)

		reqDraw, err := http.NewRequestWithContext(ctx, http.MethodPost, urlDrawRequest, bytes.NewReader([]byte(reqBody)))
		if err != nil {
			fmt.Printf("Error creating draw request: %v\n", err)
			continue
		}
		reqDraw.Header.Set("Content-Type", "application/json")
		reqDraw.Header.Set("Accept", "text/event-stream")
		qwenauth.ApplyAllQwenHeaders(reqDraw, creds.AccessToken, creds.Cookie, true)

		respDraw, err := httpClient.Do(reqDraw)
		if err != nil {
			fmt.Printf("Draw request error: %v\n", err)
			continue
		}

		fmt.Printf("Draw response status: %d\n", respDraw.StatusCode)
		if respDraw.StatusCode != 200 {
			bodyDraw, _ := io.ReadAll(respDraw.Body)
			fmt.Printf("Error response body: %s\n", string(bodyDraw))
			respDraw.Body.Close()
			continue
		}

		// Read SSE stream
		scanner := bufio.NewScanner(respDraw.Body)
		lineCount := 0
		for scanner.Scan() {
			line := scanner.Text()
			lineCount++
			if lineCount <= 10 || (line != "" && line != "data: [DONE]") {
				// Print first few lines and any non-empty lines to see what we get
				fmt.Printf("Line %d: %s\n", lineCount, line)
			}
			if lineCount > 100 {
				fmt.Println("... stream too long, truncating print ...")
				break
			}
		}
		respDraw.Body.Close()
	}
}
