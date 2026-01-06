# Healarr

**Health Evaluation And Library Auto-Recovery for *aRR**

Healarr monitors your media library for corrupted files and automatically triggers re-downloads through Sonarr, Radarr, or Whisparr. It detects issues using ffprobe, MediaInfo, or HandBrake, then orchestrates the complete remediation workflow.

![License](https://img.shields.io/badge/license-GPLv3-blue.svg)
![Go Version](https://img.shields.io/badge/go-1.25%2B-00ADD8.svg)
![React](https://img.shields.io/badge/react-19-61DAFB.svg)
[![CodeRabbit PR Reviews](https://img.shields.io/coderabbit/prs/github/mescon/Healarr?label=CodeRabbit)](https://github.com/mescon/Healarr/pulls?q=is%3Apr+reviewed-by%3Acoderabbitai)
[![codecov](https://codecov.io/gh/mescon/Healarr/graph/badge.svg?token=Y1X1MRQTGX)](https://codecov.io/gh/mescon/Healarr)
[![Snyk Security](https://img.shields.io/badge/Snyk-monitored-purple?logo=snyk)](https://app.snyk.io/org/mescon)
[![Quality Gate Status](https://sonarcloud.io/api/project_badges/measure?project=mescon_Healarr&metric=alert_status)](https://sonarcloud.io/summary/new_code?id=mescon_Healarr)

## Features

- 🔍 **Multi-Method Detection** - ffprobe, MediaInfo, or HandBrake-based health checks
- 🔄 **Automatic Remediation** - Deletes corrupt files and triggers *arr search
- ✅ **Verification** - Confirms new downloads are healthy before marking resolved
- 📊 **Dashboard** - Real-time stats, charts, and corruption type breakdown
- 🔔 **Notifications** - Discord, Slack, Telegram, Pushover, Gotify, ntfy, Email, webhooks
- 📅 **Scheduled Scans** - Cron-based automatic scanning
- 🌐 **Webhook Integration** - Scan files immediately when *arr downloads complete
- 🎨 **Modern UI** - Dark/light themes, responsive design
- 🗄️ **Database Maintenance** - Automatic pruning, integrity checks, and optimization

### Supported *arr Applications

| App | Version | API Style |
|-----|---------|-----------|
| Sonarr | v3+ | Series/Episodes |
| Radarr | v3+ | Movies |
| Whisparr | v2 | Series/Episodes (Sonarr-based) |
| Whisparr | v3 | Movies (Radarr-based) |

## Installation

### Option 1: Docker (Recommended)

Docker is the easiest way to run Healarr - all dependencies are included.

#### Docker Compose

```yaml
services:
  healarr:
    image: ghcr.io/mescon/healarr:latest
    container_name: healarr
    restart: unless-stopped
    ports:
      - "3090:3090"
    environment:
      - TZ=Europe/London
    volumes:
      - /path/to/config:/config
      - /path/to/media:/media:ro  # Read-only access to your media
```

> **💡 Tip: Matching paths with your *arr apps**  
> If you mount media using the same internal path that Sonarr/Radarr uses, you won't need to configure path translation. For example, if Sonarr sees `/tv`, mount your media as `-v /path/to/tv:/tv:ro` and Healarr will see the same paths as Sonarr.

```bash
docker compose up -d
```

#### Docker Run

```bash
docker run -d \
  --name healarr \
  -p 3090:3090 \
  -v /path/to/config:/config \
  -v /path/to/media:/media:ro \
  -e TZ=Europe/London \
  ghcr.io/mescon/healarr:latest
```

Then open `http://localhost:3090` and set up your password.

---

### Option 2: Pre-built Binaries (Linux/Windows/macOS)

Download the latest release from [GitHub Releases](https://github.com/mescon/Healarr/releases).

#### Prerequisites

You need **at least one** of these tools installed for health checking:

| Tool | Linux | Windows | macOS |
|------|-------|---------|-------|
| **ffprobe** (recommended) | `apt install ffmpeg` | [ffmpeg.org](https://ffmpeg.org/download.html) | `brew install ffmpeg` |
| **MediaInfo** | `apt install mediainfo` | [mediaarea.net](https://mediaarea.net/en/MediaInfo/Download/Windows) | `brew install mediainfo` |
| **HandBrakeCLI** | `apt install handbrake-cli` | [handbrake.fr](https://handbrake.fr/downloads2.php) | `brew install handbrake` |

#### Linux

```bash
# Download and extract
wget https://github.com/mescon/Healarr/releases/latest/download/healarr-linux-amd64.tar.gz
tar -xzf healarr-linux-amd64.tar.gz
cd healarr

# Run (config directory created automatically)
./healarr
```

**Run as a systemd service:**

```bash
# Create service file
sudo tee /etc/systemd/system/healarr.service << 'EOF'
[Unit]
Description=Healarr Media Health Monitor
After=network.target

[Service]
Type=simple
User=healarr
Group=healarr
WorkingDirectory=/opt/healarr
ExecStart=/opt/healarr/healarr
Environment=HEALARR_DATA_DIR=/opt/healarr/config
Environment=HEALARR_LOG_LEVEL=info
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
EOF

# Enable and start
sudo systemctl daemon-reload
sudo systemctl enable --now healarr
```

#### Windows

1. Download `healarr-windows-amd64.zip` from [Releases](https://github.com/mescon/Healarr/releases)
2. Extract to a folder (e.g., `C:\Healarr`)
3. Install ffmpeg:
   - Download from [ffmpeg.org](https://ffmpeg.org/download.html#build-windows)
   - Extract and add the `bin` folder to your PATH, or place `ffprobe.exe` in the same folder as `healarr.exe`
4. Run `healarr.exe` (or double-click)
5. Open `http://localhost:3090` in your browser

**Run as a Windows Service (optional):**

Using [NSSM](https://nssm.cc/):
```powershell
nssm install Healarr C:\Healarr\healarr.exe
nssm set Healarr AppDirectory C:\Healarr
nssm set Healarr AppEnvironmentExtra HEALARR_DATA_DIR=C:\Healarr\config
nssm start Healarr
```

#### macOS

```bash
# Download and extract
curl -LO https://github.com/mescon/Healarr/releases/latest/download/healarr-darwin-amd64.tar.gz
tar -xzf healarr-darwin-amd64.tar.gz
cd healarr

# Install ffmpeg if not already installed
brew install ffmpeg

# Run
./healarr
```

For Apple Silicon (M1/M2/M3), download `healarr-darwin-arm64.tar.gz` instead.

---

### Option 3: Build from Source

```bash
# Prerequisites: Go 1.25+, Node.js 22+

git clone https://github.com/mescon/Healarr.git
cd Healarr

# Build frontend
cd frontend && npm ci && npm run build && cd ..

# Build backend
go build -o healarr ./cmd/server

# Run
./healarr
```

#### Cross-compile for different platforms:

```bash
# Linux AMD64
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o healarr-linux-amd64 ./cmd/server

# Linux ARM64 (Raspberry Pi 4, etc.)
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o healarr-linux-arm64 ./cmd/server

# Windows AMD64
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o healarr-windows-amd64.exe ./cmd/server

# macOS AMD64
GOOS=darwin GOARCH=amd64 CGO_ENABLED=0 go build -o healarr-darwin-amd64 ./cmd/server

# macOS ARM64 (Apple Silicon)
GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build -o healarr-darwin-arm64 ./cmd/server
```

> **Note:** Building with `CGO_ENABLED=0` uses pure-Go SQLite which is slightly slower but fully portable. The Docker image uses CGO for better performance.

## Configuration

All configuration options can be set via environment variables or command-line flags. Command-line flags take precedence over environment variables.

### Command-Line Flags

```bash
./healarr --help
```

| Flag | Environment Variable | Default | Description |
|------|---------------------|---------|-------------|
| `--port` | `HEALARR_PORT` | `3090` | HTTP server port |
| `--data-dir` | `HEALARR_DATA_DIR` | `./config` | Base directory for persistent data |
| `--database-path` | `HEALARR_DATABASE_PATH` | `{data-dir}/healarr.db` | Database file path |
| `--log-level` | `HEALARR_LOG_LEVEL` | `info` | Log level: `debug`, `info`, `error` |
| `--base-path` | `HEALARR_BASE_PATH` | `/` | URL base path for reverse proxy |
| `--web-dir` | `HEALARR_WEB_DIR` | auto-detect | Web assets directory |
| `--dry-run` | `HEALARR_DRY_RUN` | `false` | Dry run mode (no files deleted) |
| `--retention-days` | `HEALARR_RETENTION_DAYS` | `90` | Days to keep old data (0 = disable pruning) |
| `--max-retries` | `HEALARR_DEFAULT_MAX_RETRIES` | `3` | Default max remediation attempts |
| `--verification-timeout` | `HEALARR_VERIFICATION_TIMEOUT` | `72h` | Max time to wait for file replacement |
| `--verification-interval` | `HEALARR_VERIFICATION_INTERVAL` | `30s` | Polling interval for verification |
| `--stale-threshold` | `HEALARR_STALE_THRESHOLD` | `24h` | Auto-fix items Healarr lost track of |
| `--arr-rate-limit` | `HEALARR_ARR_RATE_LIMIT_RPS` | `5` | Max requests/second to *arr APIs |
| `--arr-rate-burst` | `HEALARR_ARR_RATE_LIMIT_BURST` | `10` | Burst size for rate limiting |
| `--version` / `-v` | - | - | Print version and exit |

**Examples:**

```bash
# Run with custom port and debug logging
./healarr --port 8080 --log-level debug

# Disable automatic data pruning
./healarr --retention-days 0

# Run in dry-run mode (no files deleted)
./healarr --dry-run
```

### Environment Variables (Docker)

For Docker deployments, environment variables are typically more convenient:

```yaml
services:
  healarr:
    image: ghcr.io/mescon/healarr:latest
    environment:
      - TZ=Europe/London
      - HEALARR_LOG_LEVEL=info
      - HEALARR_RETENTION_DAYS=90
      - HEALARR_DRY_RUN=false
```

### Data Directory Structure

Healarr stores all persistent data in a `config` directory, making it easy to back up and mount as a Docker volume:

```
./config (or /config in Docker)
├── healarr.db      # SQLite database
├── backups/        # Automatic database backups (every 6 hours, last 5 kept)
└── logs/
    └── healarr.log # Application logs (auto-rotated, 100MB max, 7 days retention)
```

### Database Maintenance

Healarr automatically maintains the SQLite database for optimal performance:

**On Startup:**
- Configures WAL mode for better concurrent access
- Enables incremental auto-vacuum to reclaim space
- Runs integrity check to detect corruption early

**Daily Maintenance (3 AM local time):**
- Prunes old events and scan history (configurable via `-retention-days`)
- Removes orphaned corruption records
- Runs incremental vacuum to defragment
- Updates query planner statistics
- Checkpoints WAL to main database

**Automatic Backups:**
- Creates backup on startup
- Scheduled backups every 6 hours
- Keeps last 5 backups (older ones automatically deleted)

**Docker:** Mount a volume to `/config`:
```yaml
volumes:
  - ./config:/config
```

**Bare-metal:** The `config` directory is created next to the executable. Override with:
```bash
HEALARR_DATA_DIR=/opt/healarr/config ./healarr
```

### Setting Up *arr Instances

1. Go to **Config** → **\*arr Instances**
2. Click **Add Instance**
3. Enter:
   - **Type**: Sonarr / Radarr / Whisparr v2 / Whisparr v3
   - **Name**: Friendly name
   - **URL**: e.g., `http://sonarr:8989`
   - **API Key**: From *arr Settings → General
4. Click **Test Connection**, then **Save**

### Setting Up Scan Paths

1. Go to **Config** → **Scan Paths**
2. Click **Add Path**
3. Enter:
   - **Local Path**: Path as Healarr sees it (e.g., `/media/tv` or `/tv` if you use the same paths as *arr)
   - **\*arr Path**: Path as your *arr sees it (e.g., `/tv`)
   - **\*arr Instance**: Select the matching instance
4. Save and run your first scan!

> **💡 Pro tip:** If you mount media with the same path as your *arr apps (e.g., Sonarr sees `/tv` and you mount `-v /host/tv:/tv:ro`), set both Local Path and *arr Path to the same value. This eliminates path translation issues.

### Webhook Integration (Recommended)

For instant scanning when downloads complete:

1. In Healarr: **Config** → copy the webhook URL for your instance
2. In Sonarr/Radarr: **Settings** → **Connect** → **Add** → **Webhook**
3. Paste the URL, enable "On Import" and "On Upgrade"
4. Save and test

## Detection Methods

| Method | Speed | Accuracy | Best For |
|--------|-------|----------|----------|
| **ffprobe** (default) | Fast | Good | General use |
| **MediaInfo** | Fast | Good | Metadata issues |
| **HandBrake** | Slow | Excellent | Deep analysis |

Configure per scan path in Config.

## Notifications

Healarr can notify you about:
- New corruptions detected
- Remediation started/completed/failed
- Verification success/failure
- Scan completed

Supported providers: Discord, Slack, Telegram, Pushover, Gotify, ntfy, Email (SMTP), Custom webhooks

## Reverse Proxy

### Caddy
```caddyfile
healarr.example.com {
    reverse_proxy healarr:3090
}
```

### nginx
```nginx
location /healarr/ {
    proxy_pass http://healarr:3090/;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
}
```

Set `HEALARR_BASE_PATH=/healarr` when using a subpath.

## Troubleshooting

### Forgot Password

```bash
# Linux/macOS (adjust path as needed)
sqlite3 ./config/healarr.db "DELETE FROM settings WHERE key = 'password_hash';"

# Windows (using sqlite3.exe or DB Browser for SQLite)
sqlite3.exe C:\Healarr\config\healarr.db "DELETE FROM settings WHERE key = 'password_hash';"
```
Restart Healarr - you'll be prompted to set a new password.

### ffprobe/MediaInfo Not Found

> **Tip:** Healarr displays tool availability in **Config → About** and **Help → About**. A warning banner appears when required tools are missing.

**Linux:** Install via package manager:
```bash
# Debian/Ubuntu
sudo apt install ffmpeg mediainfo

# RHEL/Fedora
sudo dnf install ffmpeg mediainfo

# Arch
sudo pacman -S ffmpeg mediainfo
```

**Windows:** Ensure the tools are in your PATH or in the same directory as `healarr.exe`.

**macOS:**
```bash
brew install ffmpeg mediainfo
```

### Whisparr Version Mismatch

If you get API errors with Whisparr, check your version:
- **Whisparr v2.x** → Select "Whisparr v2 (Sonarr-based)"
- **Whisparr v3.x** → Select "Whisparr v3 (Radarr-based)"

### Path Not Found

Ensure your scan path's "Local Path" matches how Healarr sees the files (check your volume mounts).

## License

GNU General Public License v3.0 - see [LICENSE](LICENSE)

## Acknowledgments

- [Sonarr](https://sonarr.tv/), [Radarr](https://radarr.video/), [Whisparr](https://whisparr.com/)
- The *arr community
- [/r/selfhosted](https://reddit.com/r/selfhosted) and [/r/DataHoarder](https://reddit.com/r/DataHoarder) communities
- Icons from [dashboard-icons](https://github.com/homarr-labs/dashboard-icons) by homarr-labs
