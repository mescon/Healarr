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
 * - Resolved (green/emerald): VerificationSuccess, HealthCheckPassed
 * - Failed/Retrying (orange): *Failed states (temporary)
 * - Max Retries (red): MaxRetriesReached (permanent failure)
 * - Ignored (slate/gray): CorruptionIgnored
 */
export function formatCorruptionState(state: string): { label: string; colorClass: string } {
    // Resolved states (emerald/green)
    if (state === 'VerificationSuccess' || state === 'HealthCheckPassed') {
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
    if (eventType === 'VerificationSuccess' || eventType === 'HealthCheckPassed') {
        return 'bg-emerald-500/20 border-emerald-500/30 text-emerald-400';
    }
    
    // Failed events (orange for retryable, red for permanent)
    if (eventType === 'MaxRetriesReached') {
        return 'bg-red-500/20 border-red-500/30 text-red-400';
    }
    if (eventType.endsWith('Failed') || eventType === 'RetryScheduled' || eventType === 'DownloadTimeout') {
        return 'bg-orange-500/20 border-orange-500/30 text-orange-400';
    }
    
    // Ignored (slate/gray)
    if (eventType === 'CorruptionIgnored') {
        return 'bg-slate-500/20 border-slate-500/30 text-slate-400';
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
        'FileDetected': 'New file detected',
        'VerificationStarted': 'Verifying replacement file',
        'VerificationSuccess': 'Verification passed - resolved',
        'VerificationFailed': 'Replacement file also corrupt',
        'HealthCheckPassed': 'Health check passed - resolved',
        'MaxRetriesReached': 'Maximum retries exhausted',
        'RetryScheduled': 'Retry scheduled',
        'DownloadTimeout': 'Download timed out',
        'CorruptionIgnored': 'Marked as ignored',
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
