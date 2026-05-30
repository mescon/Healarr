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
# Stage 3: ffmpeg builder (custom build with NVIDIA codec support)
# -----------------------------------------------------------------------------
# Alpine's stock ffmpeg is built without NVIDIA codec support, so AV1 files
# silently decode in software (libdav1d) even when the host has an NVIDIA GPU
# passed through. We compile ffmpeg from source with cuvid/nvdec/nvenc enabled.
# nv-codec-headers is open-source stub headers - no CUDA toolkit is needed at
# build time. At runtime ffmpeg dlopen's the host's libnvcuvid / libcuda /
# libnvidia-encode injected by the NVIDIA Container Toolkit. Total image
# growth vs stock Alpine ffmpeg: roughly nothing - we just trade the apk
# binary for our own.
FROM alpine:3.23 AS ffmpeg-builder

ARG FFMPEG_VERSION=7.1.1
ARG NVCODEC_HEADERS_VERSION=12.2.72.0

RUN apk add --no-cache \
    build-base nasm yasm pkgconfig bash wget tar xz \
    x264-dev x265-dev libvpx-dev libass-dev opus-dev \
    dav1d-dev aom-dev lame-dev libva-dev

# NVIDIA codec header stubs (open source, ~50KB; no CUDA toolkit needed)
WORKDIR /tmp/nv
RUN wget -q "https://github.com/FFmpeg/nv-codec-headers/archive/refs/tags/n${NVCODEC_HEADERS_VERSION}.tar.gz" && \
    tar -xzf "n${NVCODEC_HEADERS_VERSION}.tar.gz" && \
    cd "nv-codec-headers-n${NVCODEC_HEADERS_VERSION}" && \
    make install PREFIX=/usr/local

# ffmpeg with the codec libs Healarr's health checks actually exercise.
# Skipping --enable-libnpp (would need CUDA libs at build time) - that is for
# image-processing filters, not the decode path we care about.
WORKDIR /tmp/ffmpeg
RUN wget -q "https://ffmpeg.org/releases/ffmpeg-${FFMPEG_VERSION}.tar.xz" && \
    tar -xJf "ffmpeg-${FFMPEG_VERSION}.tar.xz" && \
    cd "ffmpeg-${FFMPEG_VERSION}" && \
    PKG_CONFIG_PATH=/usr/local/lib/pkgconfig:/usr/lib/pkgconfig \
    ./configure \
        --prefix=/opt/ffmpeg \
        --enable-gpl --enable-nonfree --enable-version3 \
        --enable-cuda --enable-cuvid --enable-nvdec --enable-nvenc \
        --enable-vaapi \
        --enable-libx264 --enable-libx265 \
        --enable-libvpx --enable-libopus --enable-libass --enable-libmp3lame \
        --enable-libdav1d --enable-libaom \
        --disable-doc --disable-htmlpages --disable-manpages --disable-podpages --disable-txtpages \
        --extra-cflags=-I/usr/local/include \
        --extra-ldflags=-L/usr/local/lib && \
    make -j"$(nproc)" && \
    make install && \
    strip /opt/ffmpeg/bin/ffmpeg /opt/ffmpeg/bin/ffprobe

# -----------------------------------------------------------------------------
# Stage 4: Production Runtime
# -----------------------------------------------------------------------------
FROM alpine:3.23

# Runtime dependencies.
# - codec shared libs that match the builder stage's -dev packages (libx264,
#   libx265, libvpx, libass, opus, dav1d, aom, lame)
# - mediainfo: alternative health check method
# - handbrake: HandBrake CLI for the HandBrake detection method
# - ca-certificates, tzdata: HTTPS + TZ handling
# - su-exec: drop privileges from root to PUID/PGID user
# - shadow: usermod/groupmod for the PUID/PGID remap in the entrypoint
# - gcompat: glibc-compat shim so the custom ffmpeg can dlopen the host's
#   glibc-linked NVIDIA libs (libnvcuvid.so / libcuda.so / libnvidia-encode.so)
#   that the NVIDIA Container Toolkit injects at runtime. Without it the
#   dlopen fails silently on musl and we fall back to software decode.
RUN apk add --no-cache \
    mediainfo \
    handbrake \
    ca-certificates \
    tzdata \
    su-exec \
    shadow \
    gcompat \
    x264-libs x265-libs libvpx libass opus libgcc \
    dav1d aom-libs lame-libs libva

# Custom ffmpeg/ffprobe with NVIDIA cuvid/nvdec/nvenc (from ffmpeg-builder)
COPY --from=ffmpeg-builder /opt/ffmpeg/bin/ffmpeg /usr/local/bin/ffmpeg
COPY --from=ffmpeg-builder /opt/ffmpeg/bin/ffprobe /usr/local/bin/ffprobe

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
