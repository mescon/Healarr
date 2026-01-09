/**
 * Format corruption types into human-friendly labels
 */
export function formatCorruptionType(type: string): string {
    const typeMap: Record<string, string> = {
        'CorruptHeader': 'Corrupt Header',
        'TruncatedFile': 'Truncated File',
        'EmptyFile': 'Empty File',
        'StreamError': 'Stream Error',
        'CodecError': 'Codec Error',
        'InvalidFormat': 'Invalid Format',
        'BitrateError': 'Bitrate Error',
        'Unknown': 'Unknown Error',
    };

    return typeMap[type] || type.replace(/([A-Z])/g, ' $1').trim();
}

/**
 * Format corruption state (event type) into human-friendly label and color
 * This is the single source of truth for state display across the app
 * 
 * Color scheme based on parent status:
 * - Pending (amber): CorruptionDetected
 * - In Progress (blue): RemediationQueued, DeletionStarted, DeletionCompleted, SearchStarted, SearchCompleted, FileDetected, VerificationStarted
 * - Resolved (green/emerald): VerificationSuccess
 * - Failed/Retrying (orange): *Failed states (temporary)
 * - Max Retries (red): MaxRetriesReached (permanent failure)
 * - Ignored (slate/gray): CorruptionIgnored
 */
export function formatCorruptionState(state: string): { label: string; colorClass: string } {
    // Resolved states (emerald/green)
    if (state === 'VerificationSuccess') {
        return { label: 'Resolved', colorClass: 'bg-emerald-500/10 text-emerald-400 border-emerald-500/20' };
    }
    
    // Max retries reached - permanent failure (red)
    if (state === 'MaxRetriesReached') {
        return { label: 'Max Retries', colorClass: 'bg-red-500/10 text-red-400 border-red-500/20' };
    }
    
    // Temporary failures - will retry (orange)
    if (state.endsWith('Failed')) {
        const failType = state.replace('Failed', '');
        return { 
            label: `${failType} Failed`, 
            colorClass: 'bg-orange-500/10 text-orange-400 border-orange-500/20' 
        };
    }
    
    // Ignored (slate/gray)
    if (state === 'CorruptionIgnored') {
        return { label: 'Ignored', colorClass: 'bg-slate-500/10 text-slate-400 border-slate-500/20' };
    }

    // No replacement found - can be retried (amber - needs attention but not permanent)
    if (state === 'SearchExhausted') {
        return { label: 'No Replacement Found', colorClass: 'bg-amber-500/10 text-amber-400 border-amber-500/20' };
    }

    // Stuck remediation - item hasn't progressed in 24+ hours (orange - needs attention)
    if (state === 'StuckRemediation') {
        return { label: 'Stuck Remediation', colorClass: 'bg-orange-500/10 text-orange-400 border-orange-500/20' };
    }

    // Manual intervention required (purple/magenta - needs user attention)
    if (state === 'ImportBlocked') {
        return { label: '⚠️ Import Blocked', colorClass: 'bg-purple-500/10 text-purple-400 border-purple-500/20' };
    }
    if (state === 'ManuallyRemoved') {
        return { label: '⚠️ Manually Removed', colorClass: 'bg-purple-500/10 text-purple-400 border-purple-500/20' };
    }
    if (state === 'DownloadIgnored') {
        return { label: '⚠️ Download Ignored', colorClass: 'bg-purple-500/10 text-purple-400 border-purple-500/20' };
    }

    // Pending - just detected (amber)
    if (state === 'CorruptionDetected') {
        return { label: 'Pending', colorClass: 'bg-amber-500/10 text-amber-400 border-amber-500/20' };
    }
    
    // In Progress states (blue) - all part of active remediation workflow
    if (state === 'RemediationQueued') {
        return { label: 'Queued', colorClass: 'bg-blue-500/10 text-blue-400 border-blue-500/20' };
    }
    if (state === 'DeletionStarted') {
        return { label: 'Deleting', colorClass: 'bg-blue-500/10 text-blue-400 border-blue-500/20' };
    }
    if (state === 'DeletionCompleted') {
        return { label: 'Awaiting Download', colorClass: 'bg-blue-500/10 text-blue-400 border-blue-500/20' };
    }
    if (state === 'SearchStarted') {
        return { label: 'Searching', colorClass: 'bg-blue-500/10 text-blue-400 border-blue-500/20' };
    }
    if (state === 'SearchCompleted') {
        return { label: 'Downloading', colorClass: 'bg-blue-500/10 text-blue-400 border-blue-500/20' };
    }
    if (state === 'FileDetected') {
        return { label: 'Verifying', colorClass: 'bg-blue-500/10 text-blue-400 border-blue-500/20' };
    }
    if (state === 'VerificationStarted') {
        return { label: 'Verifying', colorClass: 'bg-blue-500/10 text-blue-400 border-blue-500/20' };
    }
    if (state === 'RetryScheduled') {
        return { label: 'Retry Scheduled', colorClass: 'bg-orange-500/10 text-orange-400 border-orange-500/20' };
    }
    if (state === 'DownloadTimeout') {
        return { label: 'Download Timeout', colorClass: 'bg-orange-500/10 text-orange-400 border-orange-500/20' };
    }
    
    // Default fallback (amber for unknown in-progress states)
    return { 
        label: state.replace(/([A-Z])/g, ' $1').trim(), 
        colorClass: 'bg-amber-500/10 text-amber-400 border-amber-500/20' 
    };
}

/**
 * Get the color class for an event type in the Remediation Journey timeline
 * Colors match the parent status from the help page workflow diagram
 */
export function getEventColorClass(eventType: string): string {
    // Pending status events (amber)
    if (eventType === 'CorruptionDetected') {
        return 'bg-amber-500/20 border-amber-500/30 text-amber-400';
    }
    
    // In Progress status events (blue)
    if (eventType === 'RemediationQueued' || 
        eventType === 'DeletionStarted' || 
        eventType === 'DeletionCompleted' ||
        eventType === 'SearchStarted' ||
        eventType === 'SearchCompleted' ||
        eventType === 'FileDetected' ||
        eventType === 'VerificationStarted') {
        return 'bg-blue-500/20 border-blue-500/30 text-blue-400';
    }
    
    // Resolved status events (emerald/green)
    if (eventType === 'VerificationSuccess') {
        return 'bg-emerald-500/20 border-emerald-500/30 text-emerald-400';
    }
    
    // Failed events (orange for retryable, red for permanent)
    if (eventType === 'MaxRetriesReached') {
        return 'bg-red-500/20 border-red-500/30 text-red-400';
    }
    if (eventType.endsWith('Failed') || eventType === 'RetryScheduled' || eventType === 'DownloadTimeout') {
        return 'bg-orange-500/20 border-orange-500/30 text-orange-400';
    }

    // No replacement found (amber - can be retried)
    if (eventType === 'SearchExhausted') {
        return 'bg-amber-500/20 border-amber-500/30 text-amber-400';
    }

    // Stuck remediation (orange - needs attention)
    if (eventType === 'StuckRemediation') {
        return 'bg-orange-500/20 border-orange-500/30 text-orange-400';
    }

    // Ignored (slate/gray)
    if (eventType === 'CorruptionIgnored') {
        return 'bg-slate-500/20 border-slate-500/30 text-slate-400';
    }

    // Manual intervention required (purple - needs user attention)
    if (eventType === 'ImportBlocked' ||
        eventType === 'ManuallyRemoved' ||
        eventType === 'DownloadIgnored') {
        return 'bg-purple-500/20 border-purple-500/30 text-purple-400';
    }

    // Notification events (cyan)
    if (eventType === 'NotificationSent') {
        return 'bg-cyan-500/20 border-cyan-500/30 text-cyan-400';
    }
    if (eventType === 'NotificationFailed') {
        return 'bg-red-500/20 border-red-500/30 text-red-400';
    }
    
    // Default
    return 'bg-slate-800 border-slate-700 text-slate-400';
}

/**
 * Get detailed event description for Remediation Journey timeline
 * These are more specific/descriptive than the status labels
 */
export function getEventDescription(eventType: string, data?: Record<string, unknown>): string {
    // Handle notification events specially
    if (eventType === 'NotificationSent' && data?.provider) {
        return `Notification sent via ${data.provider}`;
    }
    if (eventType === 'NotificationFailed' && data?.provider) {
        return `Notification failed via ${data.provider}`;
    }
    
    const descriptions: Record<string, string> = {
        'CorruptionDetected': 'Corruption detected',
        'RemediationQueued': 'Remediation queued',
        'DeletionStarted': 'Deleting corrupt file',
        'DeletionCompleted': 'Corrupt file deleted',
        'DeletionFailed': 'File deletion failed',
        'SearchStarted': 'Searching for replacement',
        'SearchCompleted': 'Replacement found, downloading',
        'SearchFailed': 'Search for replacement failed',
        'SearchExhausted': 'No replacement found - check indexers or retry manually',
        'StuckRemediation': 'Item stuck for 24+ hours - check *arr queue or retry',
        'FileDetected': 'New file detected',
        'VerificationStarted': 'Verifying replacement file',
        'VerificationSuccess': 'Verification passed - resolved',
        'VerificationFailed': 'Replacement file also corrupt',
        'MaxRetriesReached': 'Maximum retries exhausted',
        'RetryScheduled': 'Retry scheduled',
        'DownloadTimeout': 'Download timed out',
        'CorruptionIgnored': 'Marked as ignored',
        'ImportBlocked': '⚠️ Import blocked - check *arr Activity → Queue for errors',
        'ManuallyRemoved': '⚠️ Removed from queue - re-add in *arr or retry here',
        'DownloadIgnored': '⚠️ Download ignored - unblock in *arr Activity → Queue',
    };
    
    return descriptions[eventType] || eventType.replace(/([A-Z])/g, ' $1').trim();
}

/**
 * Format file sizes into human-readable format
 */
export function formatFileSize(bytes: number): string {
    if (bytes === 0) return '0 Bytes';
    const k = 1024;
    const sizes = ['Bytes', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return Math.round((bytes / Math.pow(k, i)) * 100) / 100 + ' ' + sizes[i];
}

/**
 * Format bytes into compact human-readable format (e.g., "4.2 GB")
 */
export function formatBytes(bytes: number): string {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return `${parseFloat((bytes / Math.pow(k, i)).toFixed(1))} ${sizes[i]}`;
}

/**
 * Format duration in seconds to human-readable format
 * Examples: "45s", "12m", "2h 30m", "1d 4h"
 */
export function formatDuration(seconds: number): string {
    if (seconds <= 0) return '0s';

    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);
    const mins = Math.floor((seconds % 3600) / 60);
    const secs = Math.floor(seconds % 60);

    if (days > 0) {
        return hours > 0 ? `${days}d ${hours}h` : `${days}d`;
    }
    if (hours > 0) {
        return mins > 0 ? `${hours}h ${mins}m` : `${hours}h`;
    }
    if (mins > 0) {
        return `${mins}m`;
    }
    return `${secs}s`;
}

export type QualityTier = 'uhd' | 'fhd' | 'hd' | 'sd' | 'unknown';

/**
 * Parse quality string (e.g., "Bluray-1080p") and return display info
 */
export function formatQuality(quality: string): { label: string; tier: QualityTier; colorClass: string } {
    if (!quality) {
        return { label: 'Unknown', tier: 'unknown', colorClass: 'bg-slate-500/10 text-slate-400 border-slate-500/20' };
    }

    const qualityLower = quality.toLowerCase();

    // Detect resolution tier
    let tier: QualityTier = 'unknown';
    if (qualityLower.includes('2160p') || qualityLower.includes('4k') || qualityLower.includes('uhd')) {
        tier = 'uhd';
    } else if (qualityLower.includes('1080p') || qualityLower.includes('1080i')) {
        tier = 'fhd';
    } else if (qualityLower.includes('720p')) {
        tier = 'hd';
    } else if (qualityLower.includes('480p') || qualityLower.includes('576p') ||
               qualityLower.includes('dvd') || qualityLower.includes('sd')) {
        tier = 'sd';
    }

    // Format the label nicely
    // "Bluray-1080p" → "1080p Bluray"
    // "WEBDL-720p" → "720p WEB-DL"
    let label = quality;
    const parts = quality.split('-');
    if (parts.length === 2) {
        const [source, resolution] = parts;
        const niceSources: Record<string, string> = {
            'bluray': 'Bluray',
            'webdl': 'WEB-DL',
            'webrip': 'WEBRip',
            'hdtv': 'HDTV',
            'dvd': 'DVD',
            'raw': 'Raw',
        };
        const niceSource = niceSources[source.toLowerCase()] || source;
        label = `${resolution} ${niceSource}`;
    }

    // Color classes based on tier
    const tierColors: Record<QualityTier, string> = {
        'uhd': 'bg-purple-500/10 text-purple-400 border-purple-500/20',
        'fhd': 'bg-blue-500/10 text-blue-400 border-blue-500/20',
        'hd': 'bg-green-500/10 text-green-400 border-green-500/20',
        'sd': 'bg-slate-500/10 text-slate-400 border-slate-500/20',
        'unknown': 'bg-slate-500/10 text-slate-400 border-slate-500/20',
    };

    return { label, tier, colorClass: tierColors[tier] };
}

/**
 * Get icon for download protocol
 */
export function getProtocolIcon(protocol: string): string {
    if (protocol?.toLowerCase() === 'usenet') {
        return '/icons/download-clients/usenet.svg';
    }
    if (protocol?.toLowerCase() === 'torrent') {
        return '/icons/download-clients/torrent.svg';
    }
    return '/icons/download-clients/generic.svg';
}

/**
 * Get icon for download client
 */
export function getDownloadClientIcon(client: string): string {
    if (!client) return '/icons/download-clients/generic.svg';

    const clientLower = client.toLowerCase();
    const iconMap: Record<string, string> = {
        'sabnzbd': 'sabnzbd.svg',
        'nzbget': 'nzbget.svg',
        'qbittorrent': 'qbittorrent.svg',
        'deluge': 'deluge.svg',
        'transmission': 'transmission.svg',
        'rtorrent': 'rutorrent.svg',
        'rutorrent': 'rutorrent.svg',
        'flood': 'flood.svg',
        'aria2': 'aria2.svg',
        'download station': 'download-station.png',
        'downloadstation': 'download-station.png',
    };

    for (const [key, icon] of Object.entries(iconMap)) {
        if (clientLower.includes(key)) {
            return `/icons/download-clients/${icon}`;
        }
    }

    return '/icons/download-clients/generic.svg';
}

/**
 * Get icon for *arr instance type
 */
export function getArrIcon(arrType: string): string {
    if (!arrType) return '/icons/arr-apps/arr-generic.svg';

    const typeMap: Record<string, string> = {
        'sonarr': 'sonarr.svg',
        'radarr': 'radarr.svg',
        'whisparr': 'whisparr.svg',
        'whisparr-v2': 'whisparr.svg',
        'whisparr-v3': 'whisparr.svg',
    };

    return `/icons/arr-apps/${typeMap[arrType.toLowerCase()] || 'arr-generic.svg'}`;
}
