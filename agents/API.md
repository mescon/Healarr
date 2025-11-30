# Healarr API Reference

## Overview

Healarr exposes a RESTful API on port 3090 (default). All endpoints except public ones require authentication via the `X-API-Key` header.

**Base URL**: `http://localhost:3090/api`

## Authentication

### Initial Setup

If no password is set, first request to setup endpoint:

```http
POST /api/auth/setup
Content-Type: application/json

{
  "password": "new-password"
}
```

### Login

```http
POST /api/auth/login
Content-Type: application/json

{
  "password": "your-password"
}
```

**Response:**
```json
{
  "token": "your-api-key",
  "message": "Login successful"
}
```

### Using the Token

Include in all subsequent requests:
```http
X-API-Key: your-api-key
```

Alternative methods:
- `Authorization: Bearer your-api-key`
- Query parameter: `?apikey=your-api-key` (for WebSocket)

---

## Public Endpoints (No Auth Required)

### GET /api/health

Health check endpoint.

**Response:**
```json
{
  "status": "healthy",
  "version": "1.0.0",
  "uptime": "2h30m",
  "database": {
    "status": "connected",
    "size_bytes": 438272
  },
  "arr_instances": {
    "online": 4,
    "total": 4
  },
  "active_scans": 0,
  "pending_corruptions": 3,
  "websocket_clients": 2
}
```

### GET /api/auth/status

Check authentication status.

### POST /api/webhook/:instance_id

Incoming webhooks from *arr instances.

---

## Protected Endpoints

### Dashboard & Stats

#### GET /api/stats/dashboard

Dashboard statistics.

**Response:**
```json
{
  "total_files_scanned": 15420,
  "active_corruptions": 3,
  "resolved_corruptions": 47,
  "failed_remediations": 2,
  "scans_today": 5,
  "status_breakdown": {
    "detected": 1,
    "remediating": 1,
    "verifying": 1,
    "resolved": 47,
    "failed": 2
  }
}
```

#### GET /api/stats/history

Historical statistics over time.

#### GET /api/stats/types

Corruption types breakdown.

---

### Corruptions

#### GET /api/corruptions

List corruptions with pagination.

**Query Parameters:**
| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `page` | int | 1 | Page number |
| `limit` | int | 50 | Items per page |
| `sort_by` | string | `detected_at` | Sort field |
| `sort_order` | string | `desc` | `asc` or `desc` |
| `status` | string | `all` | Filter by status |

**Response:**
```json
{
  "data": [
    {
      "id": "550e8400-e29b-41d4-a716-446655440000",
      "file_path": "/mnt/media/tv/Show/S01E01.mkv",
      "status": "detected",
      "corruption_type": "CorruptStream",
      "detected_at": "2024-01-15T10:30:00Z",
      "resolved_at": null,
      "retry_count": 0,
      "max_retries": 3,
      "instance_id": 1,
      "path_id": 1
    }
  ],
  "total": 50,
  "page": 1,
  "limit": 50
}
```

#### GET /api/corruptions/:id/history

Event history for a corruption.

#### POST /api/corruptions/retry

Bulk retry failed corruptions.

**Request:**
```json
{
  "ids": ["uuid1", "uuid2"]
}
```

#### POST /api/corruptions/ignore

Bulk ignore corruptions.

#### POST /api/corruptions/delete

Bulk delete corruptions.

---

### Scans

#### GET /api/scans

List scan history.

#### GET /api/scans/active

Currently running scans.

**Response:**
```json
[
  {
    "id": "scan-uuid",
    "path": "/mnt/media/tv",
    "path_id": 1,
    "status": "scanning",
    "total_files": 1250,
    "files_done": 450,
    "current_file": "/mnt/media/tv/Show/S01E05.mkv",
    "start_time": "2024-01-15T10:00:00Z",
    "dry_run": false
  }
]
```

#### POST /api/scans

Start a new scan.

**Request:**
```json
{
  "path_id": 1
}
```

#### POST /api/scans/all

Scan all enabled paths.

**Response:**
```json
{
  "message": "Started 4 scan(s), skipped 0 already running",
  "started": 4,
  "skipped": 0
}
```

#### POST /api/scans/pause-all

Pause all active scans.

**Response:**
```json
{
  "message": "Scans paused",
  "paused": 2
}
```

#### POST /api/scans/resume-all

Resume all paused scans.

#### POST /api/scans/cancel-all

Cancel all active scans.

#### GET /api/scans/:scan_id

Get scan details.

#### GET /api/scans/:scan_id/files

Get files scanned in a scan.

#### DELETE /api/scans/:scan_id

Cancel an active scan.

#### POST /api/scans/:scan_id/pause

Pause a specific scan.

#### POST /api/scans/:scan_id/resume

Resume a specific scan.

#### POST /api/scans/:scan_id/rescan

Re-scan using same path.

---

### Configuration

#### GET /api/config/arr

List *arr instances.

**Response:**
```json
[
  {
    "id": 1,
    "name": "Sonarr",
    "type": "sonarr",
    "url": "http://localhost:8989",
    "enabled": true,
    "webhook_url": null
  },
  {
    "id": 2,
    "name": "Whisparr",
    "type": "whisparr-v3",
    "url": "http://localhost:6969",
    "enabled": true
  }
]
```

#### POST /api/config/arr

Add an instance.

**Request:**
```json
{
  "name": "Whisparr",
  "type": "whisparr-v3",
  "url": "http://localhost:6969",
  "api_key": "your-api-key"
}
```

#### POST /api/config/arr/test

Test instance connection.

#### PUT /api/config/arr/:id

Update an instance.

#### DELETE /api/config/arr/:id

Delete an instance.

---

#### GET /api/config/paths

List scan paths.

**Response:**
```json
[
  {
    "id": 1,
    "local_path": "/mnt/media/tv",
    "arr_path": "/data/media/tv",
    "instance_id": 1,
    "enabled": true,
    "auto_remediate": true,
    "dry_run": false,
    "max_retries": 3,
    "verification_timeout": "72h"
  }
]
```

#### POST /api/config/paths

Add a scan path.

**Request:**
```json
{
  "local_path": "/mnt/media/movies",
  "arr_path": "/data/media/movies",
  "instance_id": 2,
  "enabled": true,
  "auto_remediate": false,
  "dry_run": true
}
```

#### PUT /api/config/paths/:id

Update a scan path.

#### DELETE /api/config/paths/:id

Delete a scan path.

---

#### GET /api/config/schedules

List cron schedules.

#### POST /api/config/schedules

Add a schedule.

**Request:**
```json
{
  "scan_path_id": 1,
  "cron_expression": "0 2 * * *"
}
```

#### PUT /api/config/schedules/:id

Update a schedule.

#### DELETE /api/config/schedules/:id

Delete a schedule.

---

#### GET /api/config/notifications

List notification configs.

#### POST /api/config/notifications

Add notification config.

**Request:**
```json
{
  "name": "Discord Alerts",
  "type": "discord",
  "url": "https://discord.com/api/webhooks/...",
  "events": ["CorruptionDetected", "VerificationSuccess"]
}
```

#### POST /api/config/notifications/test

Send test notification.

---

#### GET /api/config/export

Export all configuration as JSON.

#### POST /api/config/import

Import configuration from JSON.

#### GET /api/config/backup

Download database backup (encrypted SQLite file).

---

### Authentication Management

#### GET /api/auth/key

Get current API key.

#### POST /api/auth/regenerate

Regenerate API key.

#### POST /api/auth/password

Change password.

**Request:**
```json
{
  "current_password": "old-password",
  "new_password": "new-password"
}
```

---

### Logs

#### GET /api/logs/recent

Get recent log entries.

**Query Parameters:**
| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `limit` | int | 100 | Number of lines |
| `level` | string | `all` | `debug`, `info`, `error`, `all` |

#### GET /api/logs/download

Download logs as ZIP file (files renamed to .txt for Windows compatibility).

---

## WebSocket

### Connection

```javascript
const ws = new WebSocket('ws://localhost:3090/api/ws?apikey=your-token');
```

### Events

The server broadcasts events in real-time:

```javascript
ws.onmessage = (event) => {
  const data = JSON.parse(event.data);
  console.log(data.event_type, data.event_data);
};
```

**Event Types:**

| Event | Description |
|-------|-------------|
| `ScanStarted` | Scan began |
| `ScanProgress` | Scan progress update |
| `ScanCompleted` | Scan finished |
| `ScanPaused` | Scan paused |
| `CorruptionDetected` | New corruption found |
| `RemediationQueued` | Queued for remediation |
| `DeletionStarted` | File deletion started |
| `DeletionCompleted` | File deleted successfully |
| `DeletionFailed` | File deletion failed |
| `SearchStarted` | *arr search triggered |
| `SearchCompleted` | *arr search completed |
| `SearchFailed` | *arr search failed |
| `VerificationStarted` | Verification began |
| `VerificationSuccess` | Replacement file healthy |
| `VerificationFailed` | Replacement file corrupt |
| `DownloadProgress` | Download progress update |
| `DownloadTimeout` | Download timed out |
| `DownloadFailed` | Download failed |
| `RetryScheduled` | Retry scheduled |
| `MaxRetriesReached` | No more retries |

**Example Message:**
```json
{
  "id": 12345,
  "aggregate_type": "corruption",
  "aggregate_id": "550e8400-e29b-41d4-a716-446655440000",
  "event_type": "CorruptionDetected",
  "event_data": {
    "file_path": "/mnt/media/tv/Show/S01E01.mkv",
    "corruption_type": "CorruptStream"
  },
  "created_at": "2024-01-15T10:30:00Z"
}
```

---

## Error Responses

All errors return appropriate HTTP status codes:

```json
{
  "error": "Description of the error"
}
```

| Status | Meaning |
|--------|---------|
| 400 | Bad Request - Invalid parameters |
| 401 | Unauthorized - Missing or invalid token |
| 404 | Not Found - Resource doesn't exist |
| 429 | Too Many Requests - Rate limited |
| 500 | Internal Server Error |

---

## Rate Limiting

| Endpoint | Limit |
|----------|-------|
| `/api/auth/login` | 5 per minute |
| `/api/auth/setup` | 3 per hour |
| `/api/webhook/*` | 100 per minute |
| *arr API calls | 5 per second (internal) |

---

## Webhook URL Format

For incoming webhooks from *arr:

```
http://healarr:3090/api/webhook/{instance_id}?apikey={your_api_key}
```

Replace:
- `{instance_id}` - The ID of the *arr instance in Healarr
- `{your_api_key}` - Your Healarr API key
