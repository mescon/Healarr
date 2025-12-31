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
