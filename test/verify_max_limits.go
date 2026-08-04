//go:build ignore

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ThinkingConfig struct {
	MaxThinkingTokens int `json:"max_thinking_tokens,omitempty"`
}

type ChatCompletionsRequest struct {
	Model    string          `json:"model"`
	Messages []ChatMessage   `json:"messages"`
	Stream   bool            `json:"stream"`
	Thinking *ThinkingConfig `json:"thinking,omitempty"`
}

func generateDummyText(size int) string {
	var sb strings.Builder
	word := "Lorem ipsum dolor sit amet, consectetur adipiscing elit. "
	for sb.Len() < size {
		sb.WriteString(word)
	}
	res := sb.String()
	if len(res) > size {
		res = res[:size]
	}
	return res
}

func runTest(client *http.Client, size int) error {
	fmt.Printf("\n======================================================\n")
	fmt.Printf("=== Testing payload size: %d characters (%d KB) ===\n", size, size/1024)
	fmt.Printf("======================================================\n")

	dummyText := generateDummyText(size)
	reqBody := ChatCompletionsRequest{
		Model: "qwen3.7-max",
		Messages: []ChatMessage{
			{Role: "user", Content: dummyText + "\n\nHello Qwen, please write a brief summary of the text above in one sentence, and also provide a very detailed reasoning/thinking explanation of how you analyzed it. Write at least 200 words of thinking process."},
		},
		Stream: true,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", "http://localhost:9317/v1/chat/completions", bytes.NewBuffer(jsonData))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer test-api-key-123")

	startTime := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("http error status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	reader := bufio.NewReader(resp.Body)
	var reasoningBuilder strings.Builder
	var contentBuilder strings.Builder
	firstChunkReceived := false
	hasReasoningContent := false

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("read error: %w", err)
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "data: [DONE]" {
			break
		}
		if strings.HasPrefix(line, "data: ") {
			dataStr := strings.TrimPrefix(line, "data: ")

			if !firstChunkReceived {
				firstChunkReceived = true
				fmt.Printf("[First chunk received in %v]\n", time.Since(startTime))
			}

			var chunk struct {
				Choices []struct {
					Delta struct {
						ReasoningContent string `json:"reasoning_content"`
						Content          string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(dataStr), &chunk); err == nil {
				if len(chunk.Choices) > 0 {
					rc := chunk.Choices[0].Delta.ReasoningContent
					c := chunk.Choices[0].Delta.Content
					if rc != "" {
						if !hasReasoningContent {
							hasReasoningContent = true
							fmt.Println("\n--- START REASONING CONTENT ---")
						}
						reasoningBuilder.WriteString(rc)
						fmt.Print(rc)
					}
					if c != "" {
						if hasReasoningContent {
							hasReasoningContent = false
							fmt.Println("\n--- END REASONING CONTENT ---")
							fmt.Println("\n--- START CONTENT ---")
						}
						contentBuilder.WriteString(c)
						fmt.Print(c)
					}
				}
			}
		}
	}

	if !hasReasoningContent && contentBuilder.Len() > 0 {
		fmt.Println("\n--- END CONTENT ---")
	}

	fmt.Printf("\nDone in %v\n", time.Since(startTime))
	fmt.Printf("Reasoning length: %d chars\n", reasoningBuilder.Len())
	fmt.Printf("Content length: %d chars\n", contentBuilder.Len())
	return nil
}

func main() {
	client := &http.Client{}
	// Sizes to test: 8K, 16K, 32K, 64K, 128K characters
	sizes := []int{8192, 16384, 32768, 65536, 128000}
	for _, size := range sizes {
		err := runTest(client, size)
		if err != nil {
			fmt.Printf("Test failed for size %d: %v\n", size, err)
		} else {
			fmt.Printf("Test Succeeded for size %d\n", size)
		}
		// sleep a bit to avoid hitting rate limits or overlapping request logs
		time.Sleep(2 * time.Second)
	}
}
