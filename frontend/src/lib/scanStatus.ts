// Shared scan-status semantics for the UI.
//
// The backend reports two status vocabularies: DB rows go
// running -> scanning -> completed/cancelled/error/aborted/interrupted
// (and can sit at paused), while the live in-memory scanner reports
// enumerating/scanning/paused. Pages used to check `status === 'running'`,
// so a scan whose row had already advanced to 'scanning' (or was paused)
// showed a Rescan button instead of Cancel while it was still running.
// Every page must use these helpers instead of comparing raw strings.

/** Statuses that mean the scan is still alive (running, in any phase, or paused). */
export const scanStatusIsActive = (status: string): boolean =>
    status === 'running' || status === 'enumerating' || status === 'scanning' || status === 'paused';

/** User-facing label for a raw backend status. */
export const scanStatusLabel = (status: string): string => {
    switch (status) {
        case 'enumerating': return 'Counting files';
        case 'scanning': return 'Scanning';
        case 'running': return 'Running';
        case 'paused': return 'Paused';
        case 'completed': return 'Completed';
        case 'cancelled': return 'Cancelled';
        case 'interrupted': return 'Interrupted';
        case 'aborted': return 'Aborted';
        case 'error': return 'Error';
        case 'failed': return 'Failed';
        default: return status;
    }
};

/** Badge color classes per status family, matching the existing palette. */
export const scanStatusBadgeClass = (status: string): string => {
    if (status === 'completed') return 'bg-green-500/10 text-green-400 border-green-500/20';
    if (status === 'failed' || status === 'error' || status === 'aborted') return 'bg-red-500/10 text-red-400 border-red-500/20';
    if (status === 'paused') return 'bg-amber-500/10 text-amber-400 border-amber-500/20';
    if (status === 'cancelled' || status === 'interrupted') return 'bg-slate-500/10 text-slate-400 border-slate-500/20';
    if (scanStatusIsActive(status)) return 'bg-blue-500/10 text-blue-400 border-blue-500/20';
    return 'bg-slate-500/10 text-slate-400 border-slate-500/20';
};
