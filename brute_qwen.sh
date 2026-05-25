#!/bin/bash
for pwd in "gug938123541com" "gug938" "gug938123541" "gug938123541com." "Gug938123541com" "Gug938123541com."; do
  echo "Trying password: $pwd"
  curl -s -X POST -H 'Authorization: Bearer skloxoskloxoskloxo' \
    -H 'Content-Type: application/json' \
    -d "{\"email\":\"gug938@123541.com\",\"password\":\"$pwd\"}" \
    http://127.0.0.1:9317/v0/management/qwen-login
  echo ""
done
