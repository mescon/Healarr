#!/bin/sh
# =============================================================================
# Healarr Docker Entrypoint
# Handles PUID/PGID for volume permissions (linuxserver.io style)
# =============================================================================

set -e

# Add custom tools directory to PATH if it exists
# This allows users to mount custom binaries (e.g., newer ffmpeg versions)
# Uses HEALARR_DATA_DIR if set, otherwise defaults to /config
TOOLS_DIR="${HEALARR_DATA_DIR:-/config}/tools"
if [ -d "$TOOLS_DIR" ]; then
    export PATH="$TOOLS_DIR:$PATH"
    echo "Custom tools directory found at $TOOLS_DIR, added to PATH"
fi

# Default values if not set
PUID=${PUID:-1000}
PGID=${PGID:-1000}

# Get current user info
CURRENT_UID=$(id -u healarr 2>/dev/null || echo "1000")
CURRENT_GID=$(id -g healarr 2>/dev/null || echo "1000")

# Only modify if PUID/PGID differ from current
if [ "$PUID" != "$CURRENT_UID" ] || [ "$PGID" != "$CURRENT_GID" ]; then
    echo "Configuring user healarr with UID:${PUID} GID:${PGID}"

    # Modify group GID if needed
    if [ "$PGID" != "$CURRENT_GID" ]; then
        groupmod -o -g "$PGID" healarr
    fi

    # Modify user UID if needed
    if [ "$PUID" != "$CURRENT_UID" ]; then
        usermod -o -u "$PUID" healarr
    fi
fi

# Ensure config directory exists and has correct permissions
mkdir -p /config/logs
chown -R healarr:healarr /config

# Print startup info
echo "Starting Healarr as UID:$(id -u healarr) GID:$(id -g healarr)"

# Execute the main application as the healarr user. gosu is the Debian-
# equivalent of Alpine's su-exec: both drop privileges to the named user
# and exec the command without leaving a shell or extra PID 1 wrapper.
exec gosu healarr /app/healarr "$@"
