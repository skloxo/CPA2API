# ─── Build Stage ──────────────────────────────────────────────────────────────
FROM golang:1.26-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src

# Cache dependency downloads
COPY go.mod go.sum ./
ENV GOPROXY=https://goproxy.cn,direct
RUN go mod download

# Copy source and build
COPY . .
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -buildvcs=false -ldflags="-s -w -X 'main.Version=${VERSION}' -X 'main.Commit=${COMMIT}' -X 'main.BuildDate=${BUILD_DATE}'" -o /app/cpa2api ./cmd/server/

# ─── Runtime Stage ────────────────────────────────────────────────────────────
FROM alpine:3.21

# Runtime dependencies
RUN apk add --no-cache ca-certificates tzdata wget

# Non-root user
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /app

# Copy binary and set permissions
COPY --from=builder /app/cpa2api /app/cpa2api
RUN chmod +x /app/cpa2api && chown appuser:appgroup /app/cpa2api

USER appuser

EXPOSE 9317

ENTRYPOINT ["/app/cpa2api"]
CMD ["-config", "/app/config.yaml"]
