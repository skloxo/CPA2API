#!/usr/bin/env bash
set -e

PROJECT_ROOT="/home/skloxo/aho/openclaw/project/CPA/CPA2API"
SERVICE_STATIC="/home/skloxo/services/cpa2api/static"
DEV_STATIC="/home/skloxo/aho/cpa2api/static"
SERVICE_BIN="/home/skloxo/aho/cpa2api/cpa2api"
USR_BIN="/usr/local/bin/cpa2api"
VERSION="v7.1.45-s13"
BUILD_DATE=$(date -u +'%Y-%m-%dT%H:%M:%SZ')

echo "🚀 [1/4] Building Vite Frontend SPA..."
cd "${PROJECT_ROOT}/web"
npx vite build

echo "📦 [2/4] Syncing production assets to service static directory..."
mkdir -p "${SERVICE_STATIC}" "${DEV_STATIC}"
cp -r ../static/* "${SERVICE_STATIC}/"
cp -r ../static/* "${DEV_STATIC}/"

echo "🔨 [3/4] Compiling Go Server binary with Version=${VERSION}..."
cd "${PROJECT_ROOT}"
go build -ldflags "-X github.com/router-for-me/CLIProxyAPI/v7/internal/buildinfo.Version=${VERSION} -X github.com/router-for-me/CLIProxyAPI/v7/internal/buildinfo.BuildDate=${BUILD_DATE}" -o "${SERVICE_BIN}" ./cmd/server

echo "🔄 [4/4] Updating production binary & restarting Systemd cpa2api service..."
sudo systemctl stop cpa2api 2>/dev/null || true
cp "${SERVICE_BIN}" "${USR_BIN}" 2>/dev/null || sudo cp "${SERVICE_BIN}" "${USR_BIN}"
sudo systemctl restart cpa2api

echo "✅ Dev Loop Completed! Version ${VERSION} is active and running."
