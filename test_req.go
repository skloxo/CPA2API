package main

import (
	"encoding/json"
	"fmt"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/translator/qwen"
)

func main() {
	rawJSON := `{
		"model": "qwen3.6-plus",
		"messages": [
			{"role": "user", "content": "Hi, list 3 colors."},
			{"role": "assistant", "content": "Sure, here are three colors: 1. Red 2. Blue 3. Green."},
			{"role": "user", "content": "Thanks! Now tell me which one is your favorite and why."}
		]
	}`

	out := qwen.ConvertOpenAIRequestToQwen("qwen3.6-plus", []byte(rawJSON), false)

	// Pretty print
	var m map[string]interface{}
	json.Unmarshal(out, &m)
	pretty, _ := json.MarshalIndent(m, "", "  ")
	fmt.Println(string(pretty))
}
