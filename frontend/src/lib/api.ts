import axios from 'axios';
import type { DashboardStats, Corruption, Remediation, PaginatedResponse, Scan } from '../types/api';
import { getApiBasePath, getRouterBasePath } from './basePath';

// Create axios instance with dynamic base URL for reverse proxy support
const api = axios.create({
  get baseURL() {
    const basePath = getApiBasePath();
    return basePath ? `${basePath}/api` : '/api';
  }
});

// Add request interceptor to include auth token
api.interceptors.request.use((config) => {
    const token = localStorage.getItem('healarr_token');
    if (token) {
        config.headers['X-API-Key'] = token;
    }
    return config;
});

// Add response interceptor to handle 401 errors
api.interceptors.response.use(
    (response) => response,
    (error) => {
        if (error.response?.status === 401) {
            localStorage.removeItem('healarr_token');
            const basePath = getRouterBasePath();
            window.location.href = basePath === '/' ? '/login' : `${basePath}/login`;
        }
        return Promise.reject(error);
    }
);

export const getDashboardStats = async (): Promise<DashboardStats> => {
    const { data } = await api.get<DashboardStats>('/stats/dashboard');
    return data;
};

export const getCorruptions = async (
    page = 1,
    limit = 50,
    sortBy = 'detected_at',
    sortOrder = 'desc',
    statusFilter = 'all',
    pathId?: number
): Promise<PaginatedResponse<Corruption>> => {
    const params: Record<string, string | number> = { page, limit, sort_by: sortBy, sort_order: sortOrder, status: statusFilter };
    if (pathId !== undefined) {
        params.path_id = pathId;
    }
    const { data } = await api.get<PaginatedResponse<Corruption>>('/corruptions', { params });
    return data;
};

export const getRemediations = async (page = 1, limit = 50): Promise<PaginatedResponse<Remediation>> => {
    const { data } = await api.get<PaginatedResponse<Remediation>>('/remediations', {
        params: { page, limit },
    });
    return data;
};

export const getScans = async (
    page = 1,
    limit = 50,
    sortBy = 'started_at',
    sortOrder: 'asc' | 'desc' = 'desc'
): Promise<PaginatedResponse<Scan>> => {
    const { data } = await api.get<PaginatedResponse<Scan>>('/scans', {
        params: { page, limit, sort_by: sortBy, sort_order: sortOrder },
    });
    return data;
};

export interface ScanDetails {
    id: number;
    path: string;
    path_id: number;
    status: string;
    files_scanned: number;
    corruptions_found: number;
    started_at: string;
    completed_at: string;
    healthy_files: number;
    corrupt_files: number;
    skipped_files: number;
    inaccessible_files: number;
}

export const getScanDetails = async (scanId: number): Promise<ScanDetails> => {
    const { data } = await api.get<ScanDetails>(`/scans/${scanId}`);
    return data;
};

export interface ScanFile {
    id: number;
    file_path: string;
    status: 'healthy' | 'corrupt' | 'error' | 'skipped' | 'inaccessible';
    corruption_type: string;
    error_details: string;
    file_size: number;
    scanned_at: string;
}

export const getScanFiles = async (
    scanId: number,
    page = 1,
    limit = 50,
    status = 'all'
): Promise<PaginatedResponse<ScanFile>> => {
    const { data } = await api.get<PaginatedResponse<ScanFile>>(`/scans/${scanId}/files`, {
        params: { page, limit, status },
    });
    return data;
};

export const triggerScan = async (path_id: number) => {
    const response = await api.post('/scan', { path_id });
    return response.data;
};

export interface ScanProgress {
    id: string;
    type: string;
    path: string;
    path_id?: number;
    total_files: number;
    files_done: number;
    current_file: string;
    status: string;
    start_time: string;
    scan_db_id?: number; // Database scan record ID for navigation
}

export const getActiveScans = async (): Promise<ScanProgress[]> => {
    const { data } = await api.get<ScanProgress[]>('/scans/active');
    return data;
};

export const cancelScan = async (scanId: string) => {
    const response = await api.delete(`/scans/${scanId}`);
    return response.data;
};

export const pauseAllScans = async (): Promise<{ message: string; paused: number }> => {
    const { data } = await api.post<{ message: string; paused: number }>('/scans/pause-all');
    return data;
};

export const resumeAllScans = async (): Promise<{ message: string; resumed: number }> => {
    const { data } = await api.post<{ message: string; resumed: number }>('/scans/resume-all');
    return data;
};

export const cancelAllScans = async (): Promise<{ message: string; cancelled: number }> => {
    const { data } = await api.post<{ message: string; cancelled: number }>('/scans/cancel-all');
    return data;
};

export const rescanPath = async (scanId: number): Promise<{ message: string; path: string; type: string }> => {
    const { data } = await api.post<{ message: string; path: string; type: string }>(`/scans/${scanId}/rescan`);
    return data;
};

export interface CorruptionHistoryEvent {
    event_type: string;
    data: unknown;
    timestamp: string;
}

export const getCorruptionHistory = async (id: string): Promise<CorruptionHistoryEvent[]> => {
    const { data } = await api.get<CorruptionHistoryEvent[]>(`/corruptions/${id}/history`);
    return data;
};

export interface LogEntry {
    timestamp: string;
    level: 'INFO' | 'ERROR' | 'DEBUG';
    message: string;
}

export interface LogsResponse {
    entries: LogEntry[];
    total_lines: number;
    has_more: boolean;
    offset: number;
}

export const getRecentLogs = async (limit = 100, offset = 0): Promise<LogsResponse> => {
    const { data } = await api.get<LogsResponse>('/logs/recent', {
        params: { limit, offset }
    });
    return data;
};

export const downloadLogs = async () => {
    const response = await api.get('/logs/download', {
        responseType: 'blob',
    });
    return response.data;
};

// --- Configuration API ---

export interface ArrInstance {
    id: number;
    name: string;
    type: 'sonarr' | 'radarr' | 'whisparr-v2' | 'whisparr-v3';
    url: string;
    api_key: string;
    enabled: boolean;
}

export interface ScanPath {
    id: number;
    local_path: string;
    arr_path: string;
    arr_instance_id: number | null;
    enabled: boolean;
    auto_remediate: boolean;
    dry_run?: boolean;  // Per-path dry run mode
    detection_method?: 'zero_byte' | 'ffprobe' | 'mediainfo' | 'handbrake';
    detection_args?: string;  // JSON string from API
    detection_mode?: 'quick' | 'thorough';
    max_retries?: number;
    verification_timeout_hours?: number | null;  // NULL = use global setting
}

export const getArrInstances = async (): Promise<ArrInstance[]> => {
    const response = await api.get('/config/arr');
    return response.data;
};

export const createArrInstance = async (instance: Omit<ArrInstance, 'id'>) => {
    const response = await api.post('/config/arr', instance);
    return response.data;
};

export const updateArrInstance = async (id: number, instance: Omit<ArrInstance, 'id'>) => {
    const response = await api.put(`/config/arr/${id}`, instance);
    return response.data;
};

export const deleteArrInstance = async (id: number) => {
    await api.delete(`/config/arr/${id}`);
};

export const testArrConnection = async (url: string, api_key: string): Promise<{ success: boolean; message?: string; error?: string }> => {
    const response = await api.post('/config/arr/test', { url, api_key });
    return response.data;
};

export const getScanPaths = async (): Promise<ScanPath[]> => {
    const response = await api.get('/config/paths');
    return response.data;
};

export const createScanPath = async (path: Omit<ScanPath, 'id'>) => {
    const response = await api.post('/config/paths', path);
    return response.data;
};

export const updateScanPath = async (id: number, path: Omit<ScanPath, 'id'>) => {
    const response = await api.put(`/config/paths/${id}`, path);
    return response.data;
};

export const deleteScanPath = async (id: number) => {
    await api.delete(`/config/paths/${id}`);
};

// Path validation response
export interface PathValidation {
    accessible: boolean;
    file_count: number;
    sample_files: string[];
    error: string | null;
}

export const validateScanPath = async (id: number): Promise<PathValidation> => {
    const response = await api.get(`/config/paths/${id}/validate`);
    return response.data;
};

// --- Directory Browser API ---
export interface DirectoryEntry {
    name: string;
    path: string;
    is_dir: boolean;
}

export interface BrowseResponse {
    current_path: string;
    parent_path: string | null;
    entries: DirectoryEntry[];
    error?: string | null;
}

export const browseDirectory = async (path: string = '/'): Promise<BrowseResponse> => {
    const { data } = await api.get<BrowseResponse>('/config/browse', {
        params: { path }
    });
    return data;
};

// --- Detection Preview API ---
export interface DetectionPreview {
    method: string;
    mode: string;
    command: string;
    timeout: string;
    mode_description: string;
}

export const getDetectionPreview = async (
    method: string,
    mode: string,
    args?: string
): Promise<DetectionPreview> => {
    const params = new URLSearchParams({ method, mode });
    if (args) params.set('args', args);
    const { data } = await api.get<DetectionPreview>(`/config/detection-preview?${params}`);
    return data;
};

export interface StatsHistory {
    date: string;
    count: number;
}

export interface StatsType {
    type: string;
    count: number;
}

export const getStatsHistory = async (): Promise<StatsHistory[]> => {
    const { data } = await api.get<StatsHistory[]>('/stats/history');
    return data;
};

export const getStatsTypes = async (): Promise<StatsType[]> => {
    const { data } = await api.get<StatsType[]>('/stats/types');
    return data;
};

// --- Auth API ---
export const getAPIKey = async (): Promise<{ api_key: string }> => {
    const { data } = await api.get<{ api_key: string }>('/auth/key');
    return data;
};

export const regenerateAPIKey = async (): Promise<{ api_key: string; message: string }> => {
    const { data } = await api.post<{ api_key: string; message: string }>('/auth/regenerate');
    return data;
};

export const changePassword = async (currentPassword: string, newPassword: string): Promise<{ message: string }> => {
    const { data } = await api.post<{ message: string }>('/auth/password', {
        current_password: currentPassword,
        new_password: newPassword,
    });
    return data;
};

export const getAuthStatus = async (): Promise<{ is_setup: boolean }> => {
    const { data } = await api.get<{ is_setup: boolean }>('/auth/status');
    return data;
};

export default api;
export interface Schedule {
    id: number;
    scan_path_id: number;
    local_path: string;
    cron_expression: string;
    enabled: boolean;
}

export const getSchedules = async () => {
    const { data } = await api.get<Schedule[]>('/config/schedules');
    return data;
};

export const addSchedule = async (schedule: { scan_path_id: number; cron_expression: string }) => {
    const response = await api.post('/config/schedules', schedule);
    return response.data;
};

export const updateSchedule = async (id: number, schedule: { cron_expression?: string; enabled?: boolean }) => {
    const response = await api.put(`/config/schedules/${id}`, schedule);
    return response.data;
};

export const deleteSchedule = async (id: number) => {
    const response = await api.delete(`/config/schedules/${id}`);
    return response.data;
};

// Runtime configuration (read-only, from environment variables)
export interface RuntimeConfig {
    base_path: string;
    base_path_source: string;  // "environment", "database", or "default"
}

export const getRuntimeConfig = async (): Promise<RuntimeConfig> => {
    const { data } = await api.get<RuntimeConfig>('/config/runtime');
    return data;
};

// Settings update
export interface SettingsUpdateRequest {
    base_path: string;
}

export interface SettingsUpdateResponse {
    base_path: string;
    restart_required: boolean;
}

export const updateSettings = async (settings: SettingsUpdateRequest): Promise<SettingsUpdateResponse> => {
    const { data } = await api.put<SettingsUpdateResponse>('/config/settings', settings);
    return data;
};

// Server restart
export interface RestartResponse {
    message: string;
    status: string;
}

export const restartServer = async (): Promise<RestartResponse> => {
    const { data } = await api.post<RestartResponse>('/config/restart');
    return data;
};

export const resetSetupWizard = async (): Promise<{ message: string }> => {
    const { data } = await api.post<{ message: string }>('/setup/reset');
    return data;
};

// --- Notification API ---

export interface NotificationConfig {
    id?: number;
    name: string;
    provider_type: string;
    config: Record<string, unknown>;
    events: string[];
    enabled: boolean;
    throttle_seconds: number;
    created_at?: string;
    updated_at?: string;
}

export interface EventInfo {
    name: string;        // Event type name (e.g., "ScanStarted")
    label: string;       // Friendly display name (e.g., "Scan Started")
    description: string; // Tooltip description
}

export interface EventGroup {
    name: string;
    events: EventInfo[];
}

export interface NotificationLogEntry {
    id: number;
    notification_id: number;
    event_type: string;
    message: string;
    status: string;
    error?: string;
    sent_at: string;
}

export const getNotifications = async (): Promise<NotificationConfig[]> => {
    const { data } = await api.get<NotificationConfig[]>('/config/notifications');
    return data || [];
};

export const createNotification = async (notification: NotificationConfig): Promise<{ id: number }> => {
    const { data } = await api.post<{ id: number }>('/config/notifications', notification);
    return data;
};

export const updateNotification = async (id: number, notification: NotificationConfig): Promise<void> => {
    await api.put(`/config/notifications/${id}`, notification);
};

export const deleteNotification = async (id: number): Promise<void> => {
    await api.delete(`/config/notifications/${id}`);
};

export const testNotification = async (notification: NotificationConfig): Promise<{ success: boolean; message?: string; error?: string }> => {
    const { data } = await api.post<{ success: boolean; message?: string; error?: string }>('/config/notifications/test', notification);
    return data;
};

export const getNotificationEvents = async (): Promise<EventGroup[]> => {
    const { data } = await api.get<EventGroup[]>('/config/notifications/events');
    return data;
};

export const getNotificationLog = async (id: number, limit = 50): Promise<NotificationLogEntry[]> => {
    const { data } = await api.get<NotificationLogEntry[]>(`/config/notifications/${id}/log`, { params: { limit } });
    return data || [];
};

// --- Corruption Bulk Actions API ---

export const retryCorruptions = async (ids: string[]): Promise<{ message: string; retried: number }> => {
    const { data } = await api.post<{ message: string; retried: number }>('/corruptions/retry', { ids });
    return data;
};

export const ignoreCorruptions = async (ids: string[]): Promise<{ message: string; ignored: number }> => {
    const { data } = await api.post<{ message: string; ignored: number }>('/corruptions/ignore', { ids });
    return data;
};

export const deleteCorruptions = async (ids: string[]): Promise<{ message: string; deleted: number }> => {
    const { data } = await api.post<{ message: string; deleted: number }>('/corruptions/delete', { ids });
    return data;
};

// --- Health API ---

export interface HealthStatus {
    status: 'healthy' | 'degraded' | 'unhealthy';
    version: string;
    uptime: string;
    global_verification_timeout_hours: number;
    database: {
        status: string;
        size_bytes?: number;
        error?: string;
    };
    arr_instances: {
        online: number;
        total: number;
    };
    active_scans: number;
    pending_corruptions: number;
    websocket_clients: number;
}

export const getHealth = async (): Promise<HealthStatus> => {
    const { data } = await api.get<HealthStatus>('/health');
    return data;
};

// --- Scan All API ---

export const triggerScanAll = async (): Promise<{ message: string; started: number; skipped: number }> => {
    const { data } = await api.post<{ message: string; started: number; skipped: number }>('/scans/all');
    return data;
};

// --- Config Export/Import API ---

export interface ConfigExport {
    exported_at: string;
    version: string;
    arr_instances: Array<{
        name: string;
        type: string;
        url: string;
        api_key: string;
        enabled: boolean;
    }>;
    scan_paths: Array<{
        local_path: string;
        arr_path: string;
        arr_instance_id?: number;
        enabled: boolean;
        auto_remediate: boolean;
        detection_method: string;
        detection_args?: string;
        detection_mode: string;
        max_retries: number;
        verification_timeout_hours?: number;
    }>;
    schedules: Array<{
        scan_path_id: number;
        cron_expression: string;
        enabled: boolean;
    }>;
    notifications: Array<{
        name: string;
        provider_type: string;
        config: Record<string, unknown>;
        events: string[];
        enabled: boolean;
        throttle_seconds: number;
    }>;
}

export const exportConfig = async (): Promise<ConfigExport> => {
    const { data } = await api.get<ConfigExport>('/config/export');
    return data;
};

// Download database backup
export const downloadDatabaseBackup = async (): Promise<void> => {
    const response = await api.get('/config/backup', {
        responseType: 'blob',
    });
    
    // Get filename from Content-Disposition header or use default
    const contentDisposition = response.headers['content-disposition'];
    let filename = `healarr_backup_${new Date().toISOString().split('T')[0]}.db`;
    if (contentDisposition) {
        const filenameMatch = contentDisposition.match(/filename=([^;]+)/);
        if (filenameMatch) {
            filename = filenameMatch[1].replace(/"/g, '');
        }
    }
    
    // Create download link
    const blob = new Blob([response.data], { type: 'application/octet-stream' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
};

export interface ConfigImportResult {
    message: string;
    imported: {
        arr_instances: number;
        scan_paths: number;
    };
}

export const importConfig = async (config: Partial<ConfigExport>): Promise<ConfigImportResult> => {
    const { data } = await api.post<ConfigImportResult>('/config/import', config);
    return data;
};

// --- Update Check API ---

export interface UpdateInstructions {
    docker: string;
    linux: string;
    macos: string;
    windows: string;
}

export interface UpdateCheckResponse {
    current_version: string;
    latest_version: string;
    update_available: boolean;
    release_url: string;
    changelog: string;
    published_at: string;
    download_urls: Record<string, string>;
    docker_pull_cmd: string;
    update_instructions: UpdateInstructions;
}

export const checkForUpdates = async (): Promise<UpdateCheckResponse> => {
    const { data } = await api.get<UpdateCheckResponse>('/updates/check');
    return data;
};

// --- System Info API ---

export interface SystemConfigInfo {
    port: string;
    base_path: string;
    base_path_source: string;
    log_level: string;
    data_dir: string;
    database_path: string;
    log_dir: string;
    dry_run_mode: boolean;
    retention_days: number;
    default_max_retries: number;
    verification_timeout: string;
    verification_interval: string;
    arr_rate_limit_rps: number;
    arr_rate_limit_burst: number;
}

export interface MountInfo {
    source: string;
    destination: string;
    type?: string;
    read_only: boolean;
}

export interface SystemLinks {
    github: string;
    issues: string;
    releases: string;
    wiki: string;
    discussions: string;
}

export interface ToolStatus {
    name: string;
    available: boolean;
    path?: string;
    version?: string;
    required: boolean;
    description: string;
}

export interface SystemInfo {
    version: string;
    environment: 'docker' | 'native';
    os: string;
    arch: string;
    go_version: string;
    uptime: string;
    uptime_seconds: number;
    started_at: string;
    config: SystemConfigInfo;
    mounts?: MountInfo[];
    tools: Record<string, ToolStatus>;
    links: SystemLinks;
}

export const getSystemInfo = async (): Promise<SystemInfo> => {
    const { data } = await api.get<SystemInfo>('/system/info');
    return data;
};

// --- Setup/Onboarding API ---

export interface SetupStatus {
    needs_setup: boolean;
    has_password: boolean;
    has_api_key: boolean;
    has_instances: boolean;
    has_scan_paths: boolean;
    onboarding_dismissed: boolean;
}

export const getSetupStatus = async (): Promise<SetupStatus> => {
    const { data } = await api.get<SetupStatus>('/setup/status');
    return data;
};

export const dismissSetup = async (): Promise<{ message: string }> => {
    const { data } = await api.post<{ message: string }>('/setup/dismiss');
    return data;
};

// Import config during setup (public endpoint, no auth required)
export const importConfigPublic = async (config: Partial<ConfigExport>): Promise<ConfigImportResult> => {
    const { data } = await api.post<ConfigImportResult>('/setup/import', config);
    return data;
};

// Restore database during setup (public endpoint, no auth required)
export interface RestoreResult {
    message: string;
    restart_required: boolean;
    backup_created: string;
    note: string;
}

export const restoreDatabasePublic = async (file: File): Promise<RestoreResult> => {
    const formData = new FormData();
    formData.append('file', file);
    const { data } = await api.post<RestoreResult>('/setup/restore', formData, {
        headers: {
            'Content-Type': 'multipart/form-data',
            'X-Confirm-Restore': 'true',
        },
    });
    return data;
};

// --- ARR Root Folders API ---

export interface RootFolder {
    id: number;
    path: string;
    free_space: number;
    total_space: number;
}

export const getArrRootFolders = async (instanceId: number): Promise<RootFolder[]> => {
    const { data } = await api.get<RootFolder[]>(`/config/arr/${instanceId}/rootfolders`);
    return data;
};
