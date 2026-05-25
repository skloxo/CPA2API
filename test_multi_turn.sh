#!/bin/bash
curl -i -X POST http://localhost:9317/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer test-api-key-123" \
  -d '{
    "model": "qwen3.6-plus",
    "messages": [
      {"role": "user", "content": "Hi, list 3 colors."},
      {"role": "assistant", "content": "Sure, here are three colors: 1. Red 2. Blue 3. Green."},
      {"role": "user", "content": "Thanks! Now tell me which one is your favorite and why."}
    ],
    "stream": false
  }'
