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
# Switched from "Alpine + custom-compiled ffmpeg + gcompat" (the build that
# shipped in v1.3.4) to Debian-slim + jellyfin-ffmpeg. Reason: on hosts
# using the NVIDIA Container Toolkit, the toolkit's userspace-driver-lib
# injection (libcuda.so / libnvcuvid.so / libnvidia-encode.so) expects a
# glibc layout. On the Alpine + gcompat container the libraries weren't
# being injected at all - they're absent from /usr/lib inside a running
# container despite runtime: nvidia and NVIDIA_DRIVER_CAPABILITIES=all
# being set. Every cuvid decoder SIGSEGV'd in 1-2 seconds. The same
# toolkit + same host works perfectly for jellyfin-ffmpeg in Tdarr
# (Debian based). See #276 for the diagnosis.
#
# jellyfin-ffmpeg is the same focused build Tdarr/Jellyfin/Emby use - it
# tracks ffmpeg HEAD, ships NVIDIA cuvid/nvdec/nvenc + VAAPI + libplacebo,
# and produces a much leaner footprint than Debian's stock apt ffmpeg
# (which would pull in libllvm + Mesa + Wayland transitively). Total
# image growth vs the Alpine build is ~150-300 MB depending on
# jellyfin-ffmpeg's deps; the trade is paid for by NVDEC actually
# working out of the box on every NVIDIA host instead of silently
# falling back to software.
FROM debian:bookworm-slim

# Add the jellyfin apt repo. The package is named jellyfin-ffmpegN where N
# is the major ffmpeg version (8 = ffmpeg 8.x, 7 = ffmpeg 7.x). The
# repo only keeps the current major, so this version pin needs a bump
# every ~year as upstream majors out.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        curl gnupg ca-certificates && \
    install -d /usr/share/keyrings && \
    curl -fsSL https://repo.jellyfin.org/jellyfin_team.gpg.key \
        | gpg --dearmor -o /usr/share/keyrings/jellyfin.gpg && \
    echo "deb [arch=amd64 signed-by=/usr/share/keyrings/jellyfin.gpg] https://repo.jellyfin.org/debian bookworm main" \
        > /etc/apt/sources.list.d/jellyfin.list && \
    apt-get update && \
    apt-get install -y --no-install-recommends \
        jellyfin-ffmpeg8 \
        mediainfo \
        handbrake-cli \
        tzdata \
        gosu \
        passwd \
        wget && \
    apt-get purge -y curl gnupg && \
    apt-get autoremove -y && \
    rm -rf /var/lib/apt/lists/*

# jellyfin-ffmpeg installs to /usr/lib/jellyfin-ffmpeg/{ffmpeg,ffprobe}. Symlink
# into /usr/local/bin so callers that look up "ffmpeg" / "ffprobe" via PATH
# find it. Healarr also honors HEALARR_FFMPEG_PATH / HEALARR_FFPROBE_PATH if
# the operator wants to point at a different binary.
RUN ln -sf /usr/lib/jellyfin-ffmpeg/ffmpeg /usr/local/bin/ffmpeg && \
    ln -sf /usr/lib/jellyfin-ffmpeg/ffprobe /usr/local/bin/ffprobe

# Create default user (will be modified by entrypoint if PUID/PGID set).
RUN groupadd -g 1000 healarr && \
    useradd -u 1000 -g healarr -s /bin/sh -m healarr

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
