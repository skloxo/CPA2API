#!/usr/bin/env bash
set -e

PROJECT_ROOT="/home/skloxo/aho/openclaw/project/CPA/CPA2API"
RUNTIME_DIR="/home/skloxo/aho/cpa2api"
SERVICE_STATIC="${RUNTIME_DIR}/static"
SERVICE_BIN="${RUNTIME_DIR}/cpa2api"
CONFIG_PATH="${RUNTIME_DIR}/config.yaml"

# Get current version
VERSION=$(git describe --tags --always 2>/dev/null || echo "v7.2.123")
BUILD_DATE=$(date -u +'%Y-%m-%dT%H:%M:%SZ')

echo "🚀 [1/4] Building Frontend SPA (${VERSION})..."
cd "${PROJECT_ROOT}/web"
npm run build

echo "🔨 [2/4] Compiling Go Server binary..."
cd "${PROJECT_ROOT}"
go build -ldflags "-s -w -X github.com/router-for-me/CLIProxyAPI/v7/internal/buildinfo.Version=${VERSION} -X github.com/router-for-me/CLIProxyAPI/v7/internal/buildinfo.BuildDate=${BUILD_DATE}" -o "${SERVICE_BIN}" ./cmd/server

echo "🔄 [3/4] Snappy Graceful Restart of CPA daemon..."
kill -9 $(pgrep -f "${SERVICE_BIN}") 2>/dev/null || true
nohup "${SERVICE_BIN}" -config "${CONFIG_PATH}" > /dev/null 2>&1 &
sleep 1.5

echo "🩺 [4/4] Verifying Service Health..."
HTTP_STATUS=$(curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:8317/v0/management/api-key-usage -H "Authorization: Bearer Skl3289568" || echo "000")

if [ "${HTTP_STATUS}" = "200" ]; then
  echo "✅ CPA Build & Deploy Successful! (Version: ${VERSION}, HTTP Status: 200, PID: $(pgrep -f "${SERVICE_BIN}"))"
else
  echo "⚠️ Warning: Health check returned HTTP ${HTTP_STATUS}. Checking logs..."
  tail -n 20 "${RUNTIME_DIR}/logs/"* 2>/dev/null || true
  exit 1
fi
