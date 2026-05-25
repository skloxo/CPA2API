#!/bin/bash
curl -i -X POST http://127.0.0.1:9317/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer test-api-key-123" \
  -d '{
    "model": "qwen3.6-plus",
    "messages": [
      {"role": "user", "content": "Hi, list 3 colors."}
    ],
    "stream": true
  }'
