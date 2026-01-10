export interface DashboardStats {
    total_corruptions: number;
    active_corruptions: number;      // Deprecated: all non-resolved/non-orphaned
    pending_corruptions: number;     // Just CorruptionDetected state
    resolved_corruptions: number;
    orphaned_corruptions: number;
    ignored_corruptions: number;
    in_progress_corruptions: number;
    failed_corruptions: number;      // *Failed states
    manual_intervention_corruptions: number; // ImportBlocked or ManuallyRemoved - requires user action in *arr
    successful_remediations: number;
    active_scans: number;
    total_scans: number;
    files_scanned_today: number;
    files_scanned_week: number;
    corruptions_today: number;
    success_rate: number;
    last_scan_time?: string;         // ISO timestamp of most recent completed scan
    last_scan_path?: string;         // Path that was scanned
    last_scan_id?: number;           // ID for linking to scan details
}

export interface Corruption {
    id: string;
    state: string;
    retry_count: number;
    file_path: string;
    last_error: string;
    detected_at: string;
    last_updated_at: string;
    corruption_type: string;
    path_id?: number;

    // Enriched data from event_data (optional - may not be present for older entries)
    file_size?: number;                    // Original corrupt file size
    media_title?: string;                  // e.g., "Colony" or "The Matrix"
    media_year?: number;                   // e.g., 1999
    media_type?: 'movie' | 'series';
    season_number?: number;                // For TV shows
    episode_number?: number;               // For TV shows
    episode_title?: string;                // Episode name, e.g., "Pilot"
    arr_type?: string;                     // "sonarr", "radarr", "whisparr"
    instance_name?: string;                // e.g., "Radarr", "Radarr4K"

    // Quality and release info (from VerificationSuccess)
    quality?: string;                      // e.g., "Bluray-1080p"
    release_group?: string;                // e.g., "DEMAND"
    new_file_path?: string;                // Replacement file path
    new_file_size?: number;                // Replacement file size
    total_duration_seconds?: number;       // Time from detection to resolution
    download_duration_seconds?: number;    // Time for download only

    // Download progress info (from DownloadProgress event)
    download_progress?: number;            // 0-100 percentage
    download_size?: number;                // Total download size in bytes
    download_remaining?: number;           // Remaining bytes
    download_protocol?: string;            // "usenet" or "torrent"
    download_client?: string;              // "SABnzbd", "qBittorrent", etc.
    indexer?: string;                      // "NZBgeek", "1337x", etc.
    download_time_left?: string;           // Estimated time remaining
}

export interface Remediation {
    id: string;
    file_path: string;
    status: string;
    completed_at: string;
}

export interface Scan {
    id: number;
    path: string;
    status: string;
    files_scanned: number;
    corruptions_found: number;
    started_at: string;
    completed_at: string;
}

export interface PaginationMeta {
    page: number;
    limit: number;
    total: number;
    total_pages: number;
}

export interface PaginatedResponse<T> {
    data: T[];
    pagination: PaginationMeta;
}

export interface PathHealth {
    path_id: number;
    local_path: string;
    enabled: boolean;
    last_scan_time?: string;
    last_scan_id?: number;
    active_corruptions: number;
    total_corruptions: number;
    resolved_count: number;
    status: 'healthy' | 'warning' | 'critical' | 'unknown' | 'disabled';
}
