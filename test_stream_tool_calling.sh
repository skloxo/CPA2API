#!/bin/bash
curl -i -X POST http://localhost:9317/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer test-api-key-123" \
  -d '{
    "model": "qwen3.6-plus",
    "messages": [
      {"role": "user", "content": "What is the weather like in Boston?"}
    ],
    "tools": [
      {
        "type": "function",
        "function": {
          "name": "get_current_weather",
          "description": "Get the current weather in a given location",
          "parameters": {
            "type": "object",
            "properties": {
              "location": {
                "type": "string",
                "description": "The city and state, e.g. Boston, MA"
              }
            },
            "required": ["location"]
          }
        }
      }
    ],
    "stream": true
  }'
