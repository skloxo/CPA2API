# CPA2API Deployment Guide

This document describes the standard workflow for deploying and updating the CPA2API production service.

## Architecture Overview

```
Source Code (dev, changes frequently)       Production Deploy (stable, never touched by dev)
────────────────────────────────────        ────────────────────────────────────────────────
/project/CPA/CPA2API/                       /home/skloxo/services/cpa2api/
  ├── cmd/                                    ├── docker-compose.yml
  ├── internal/                               ├── .env                ← version controlled here
  ├── Dockerfile                              ├── config.yaml         ← production secrets
  ├── .github/workflows/                      ├── auths/              ← account data + SQLite DB
  │     └── docker-image.yml                  ├── logs/
  └── ...                                     └── static/
            │
            │  git push tag vX.X.X
            ▼
      GitHub Actions
      (docker-image.yml)
            │
            │  builds & pushes image
            ▼
    eceasy/cli-proxy-api:vX.X.X  (DockerHub)
            │
            │  docker compose pull && up -d
            ▼
    /home/skloxo/services/cpa2api/  (production, port 8317)
```

---

## Day-to-Day Development

Work in the source code directory as usual. The production service is completely unaffected.

```bash
cd /home/skloxo/aho/openclaw/project/CPA/CPA2API
# develop, test, commit ...
```

For local testing (port 9317):
```bash
docker compose -f docker-compose.dev.yml up -d
```

---

## Releasing a New Version

### 1. Tag the release in git

```bash
git tag v7.2.3
git push origin v7.2.3
```

### 2. GitHub Actions builds and pushes the Docker image automatically

The [docker-image.yml](.github/workflows/docker-image.yml) workflow triggers on any `v*` tag and:
- Builds images for `linux/amd64` and `linux/arm64`
- Pushes to DockerHub as `eceasy/cli-proxy-api:v7.2.3` and `eceasy/cli-proxy-api:latest`

Monitor progress at: https://github.com/skloxo/CPA2API/actions

### 3. Update the production service

```bash
cd /home/skloxo/services/cpa2api

# Update version in .env
sed -i 's/^CPA_VERSION=.*/CPA_VERSION=v7.2.3/' .env

# Pull new image and restart (zero config change, data untouched)
docker compose pull
docker compose up -d

# Verify
curl http://127.0.0.1:8317/healthz
docker logs cli-proxy-api
```

---

## Fresh Deployment on a New Machine

```bash
# 1. Create deployment directory
mkdir -p /home/<user>/services/cpa2api
cd /home/<user>/services/cpa2api

# 2. Create docker-compose.yml  (copy from this repo or paste the content below)
# 3. Create .env with your version and password
cat > .env <<EOF
CPA_VERSION=v7.2.3
MANAGEMENT_PASSWORD=your-password-here
EOF

# 4. Create config.yaml based on config.example.yaml from source repo
# 5. Create auths/ directory (starts empty, will be populated at runtime)
mkdir -p auths logs static

# 6. Start the service
docker compose up -d
curl http://127.0.0.1:8317/healthz
```

### `docker-compose.yml` for new deployments

```yaml
services:
  cli-proxy-api:
    image: eceasy/cli-proxy-api:${CPA_VERSION:-latest}
    container_name: cli-proxy-api
    network_mode: host
    environment:
      MANAGEMENT_PASSWORD: ${MANAGEMENT_PASSWORD:-}
      DEPLOY: ${DEPLOY:-}
    user: "root"
    volumes:
      - ./config.yaml:/app/config.yaml
      - ./auths:/root/.cli-proxy-api
      - ./logs:/app/logs
      - ./static:/app/static
    restart: unless-stopped
```

---

## Production Directory Layout

```
/home/skloxo/services/cpa2api/
├── docker-compose.yml              # service definition (pull from this doc)
├── .env                            # CPA_VERSION + MANAGEMENT_PASSWORD
├── config.yaml                     # full production config (never commit - contains secrets)
├── auths/                          # OAuth tokens + usage.sqlite (never commit)
│   ├── qwen-*.json
│   └── usage.sqlite
├── logs/                           # runtime logs (auto-created by container)
├── static/                         # management UI static assets
└── cpa-manager-database-backup.tar.gz  # database backup (optional)
```

> **Important:** `config.yaml`, `auths/`, and `.env` contain secrets and must **never** be committed to git.

---

## Useful Commands

```bash
# Check service status
docker compose ps

# View live logs
docker compose logs -f

# Restart service (e.g. after config.yaml change)
docker compose restart

# Stop service
docker compose down

# Upgrade to a new version
sed -i 's/^CPA_VERSION=.*/CPA_VERSION=vX.X.X/' .env
docker compose pull && docker compose up -d
```
