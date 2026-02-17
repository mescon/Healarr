# ============================================================================
# Healarr Dockerfile
# Multi-stage build for minimal production image
# ============================================================================

# -----------------------------------------------------------------------------
# Stage 1: Build Frontend
# -----------------------------------------------------------------------------
FROM node:22-alpine AS frontend-builder

WORKDIR /build/frontend
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# -----------------------------------------------------------------------------
# Stage 2: Build Backend (with embedded web assets)
# -----------------------------------------------------------------------------
FROM golang:1.25-alpine AS backend-builder

# Build argument for version (defaults to dev)
ARG VERSION=dev

WORKDIR /build

# Download Go modules first (better layer caching)
COPY go.mod go.sum ./
RUN go mod download

# Copy source
COPY cmd/ ./cmd/
COPY internal/ ./internal/

# Copy built frontend assets into internal/web/web/ for embedding
COPY --from=frontend-builder /build/web ./internal/web/web/

# Build with embedded web assets (pure Go, no CGO required)
RUN CGO_ENABLED=0 GOOS=linux go build \
    -tags embed_web \
    -ldflags="-s -w -X github.com/mescon/Healarr/internal/config.Version=${VERSION}" \
    -o healarr \
    ./cmd/server

# -----------------------------------------------------------------------------
# Stage 3: Production Runtime
# -----------------------------------------------------------------------------
FROM alpine:3.23

# Install runtime dependencies
# - ffmpeg: for video health checking (ffprobe)
# - mediainfo: alternative health check method
# - handbrake: HandBrake video transcoder (provides HandBrakeCLI)
# - ca-certificates: for HTTPS connections to *arr APIs
# - tzdata: for proper timezone handling
# - su-exec: for running as non-root user after entrypoint setup
# - shadow: for usermod/groupmod commands
RUN apk add --no-cache \
    ffmpeg \
    mediainfo \
    handbrake \
    ca-certificates \
    tzdata \
    su-exec \
    shadow

# Create default user (will be modified by entrypoint if PUID/PGID set)
RUN addgroup -g 1000 healarr && \
    adduser -u 1000 -G healarr -s /bin/sh -D healarr

WORKDIR /app

# Copy binary from backend builder (web assets and migrations are embedded)
COPY --from=backend-builder /build/healarr /app/healarr

# Copy entrypoint script
COPY docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod +x /app/docker-entrypoint.sh

# Create config directory with logs subdirectory
RUN mkdir -p /config/logs && chown -R healarr:healarr /config /app

# Security: Container starts as root to support PUID/PGID user mapping in entrypoint.
# Privileges are dropped immediately via su-exec before the application starts.
# Combined with no-new-privileges and cap_drop:ALL, the app process cannot escalate.

# Environment defaults
ENV HEALARR_PORT=3090 \
    HEALARR_DATA_DIR=/config \
    HEALARR_LOG_LEVEL=info \
    GIN_MODE=release \
    PUID=1000 \
    PGID=1000

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget -q --spider http://localhost:${HEALARR_PORT}/api/health || exit 1

EXPOSE 3090

# Volume for persistent data
VOLUME ["/config"]

# Custom binaries: Mount newer versions to $HEALARR_DATA_DIR/tools (auto-added to PATH)
# Or set environment variables for specific paths:
#   HEALARR_FFPROBE_PATH, HEALARR_FFMPEG_PATH,
#   HEALARR_MEDIAINFO_PATH, HEALARR_HANDBRAKE_PATH
# Example: docker run -v /path/to/ffmpeg-static:/config/tools ...

ENTRYPOINT ["/app/docker-entrypoint.sh"]
