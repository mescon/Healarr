import React, { useState, useEffect, useMemo } from 'react';
import { useQuery } from '@tanstack/react-query';
import { getCorruptionHistory } from '../lib/api';
import { useDateFormat } from '../lib/useDateFormat';
import { formatCorruptionState, getEventDescription, getEventColorClass } from '../lib/formatters';
import {
    CheckCircle, AlertTriangle, Clock, Search, Trash2,
    FileSearch, Activity, Shield, FileCheck, ChevronDown, Settings, Bell, BellOff, EyeOff, XCircle, Download, RefreshCw
} from 'lucide-react';
import { motion, AnimatePresence } from 'framer-motion';
import clsx from 'clsx';

interface RemediationJourneyProps {
    corruptionId: string;
    onClose: () => void;
}

/**
 * Icon colors match parent status colors:
 * - Pending (amber): CorruptionDetected
 * - In Progress (blue): RemediationQueued, DeletionStarted, DeletionCompleted, SearchStarted, SearchCompleted, FileDetected, VerificationStarted
 * - Resolved (emerald/green): VerificationSuccess, HealthCheckPassed
 * - Failed/Retrying (orange): *Failed states (temporary)
 * - Max Retries (red): MaxRetriesReached
 * - Ignored (slate/gray): CorruptionIgnored
 */
const EventIcon = ({ type, className }: { type: string, className?: string }) => {
    const iconClass = className || "w-5 h-5";
    switch (type) {
        // Pending (amber)
        case 'CorruptionDetected': return <AlertTriangle className={clsx(iconClass, "text-amber-400")} />;
        
        // In Progress (blue)
        case 'RemediationQueued': return <Clock className={clsx(iconClass, "text-blue-400")} />;
        case 'DeletionStarted': return <Trash2 className={clsx(iconClass, "text-blue-400")} />;
        case 'DeletionCompleted': return <CheckCircle className={clsx(iconClass, "text-blue-400")} />;
        case 'SearchStarted': return <Search className={clsx(iconClass, "text-blue-400")} />;
        case 'SearchCompleted': return <Download className={clsx(iconClass, "text-blue-400")} />;
        case 'FileDetected': return <FileCheck className={clsx(iconClass, "text-blue-400")} />;
        case 'VerificationStarted': return <FileSearch className={clsx(iconClass, "text-blue-400")} />;
        
        // Resolved (emerald)
        case 'HealthCheckPassed': return <Shield className={clsx(iconClass, "text-emerald-400")} />;
        case 'VerificationSuccess': return <Shield className={clsx(iconClass, "text-emerald-400")} />;
        
        // Max Retries Reached (red - permanent failure)
        case 'MaxRetriesReached': return <XCircle className={clsx(iconClass, "text-red-400")} />;
        
        // Failed states (orange - temporary, will retry)
        case 'DeletionFailed':
        case 'SearchFailed':
        case 'VerificationFailed': return <AlertTriangle className={clsx(iconClass, "text-orange-400")} />;
        case 'RetryScheduled': return <RefreshCw className={clsx(iconClass, "text-orange-400")} />;
        case 'DownloadTimeout': return <Clock className={clsx(iconClass, "text-orange-400")} />;
        
        // Notification events (cyan for success, red for failure)
        case 'NotificationSent': return <Bell className={clsx(iconClass, "text-cyan-400")} />;
        case 'NotificationFailed': return <BellOff className={clsx(iconClass, "text-red-400")} />;
        
        // Ignored (slate)
        case 'CorruptionIgnored': return <EyeOff className={clsx(iconClass, "text-slate-600 dark:text-slate-400")} />;
        
        default: return <Activity className={clsx(iconClass, "text-slate-600 dark:text-slate-400")} />;
    }
};

// Get human-readable status label - uses the shared formatter
const getStatusLabel = (status: string): string => {
    const { label } = formatCorruptionState(status);
    return label;
};

const RemediationJourney: React.FC<RemediationJourneyProps> = ({ corruptionId, onClose }) => {
    const { formatFull } = useDateFormat();
    
    const { data: history, isLoading } = useQuery({
        queryKey: ['history', corruptionId],
        queryFn: () => getCorruptionHistory(corruptionId),
    });

    // Compute summary from history
    const summary = useMemo(() => {
        if (!history || history.length === 0) return null;
        
        // Find the CorruptionDetected event for original file
        const detected = history.find(e => e.event_type === 'CorruptionDetected');
        const originalPath = (detected?.data as Record<string, unknown>)?.file_path as string || '';
        const originalFilename = originalPath.split('/').pop() || 'Unknown';
        
        // Find FileDetected event for new file (if exists)
        const fileDetected = history.find(e => e.event_type === 'FileDetected');
        const newPath = (fileDetected?.data as Record<string, unknown>)?.file_path as string || '';
        const newFilename = newPath ? newPath.split('/').pop() : null;
        
        // Determine current status from last event
        const lastEvent = history[history.length - 1];
        const status = lastEvent?.event_type || 'Unknown';
        
        // Determine state
        const isResolved = status === 'VerificationSuccess' || status === 'HealthCheckPassed';
        const isFailed = status === 'MaxRetriesReached' || status.includes('Failed');
        const isIgnored = status === 'CorruptionIgnored';
        
        return {
            originalFilename,
            originalPath,
            newFilename,
            newPath,
            status,
            statusLabel: getStatusLabel(status),
            isResolved,
            isFailed,
            isIgnored,
            filesAreDifferent: newFilename && newFilename !== originalFilename,
        };
    }, [history]);

    // Persistent setting for showing details by default
    const [showDetailsDefault, setShowDetailsDefault] = useState(() => {
        return localStorage.getItem('healarr_remediation_details_default') === 'true';
    });

    useEffect(() => {
        localStorage.setItem('healarr_remediation_details_default', String(showDetailsDefault));
    }, [showDetailsDefault]);

    // State for individual expanded items (overrides default)
    const [expandedItems, setExpandedItems] = useState<Record<number, boolean>>({});

    const toggleItem = (idx: number) => {
        setExpandedItems(prev => {
            const current = prev[idx] ?? showDetailsDefault;
            return { ...prev, [idx]: !current };
        });
    };

    const toggleAll = () => {
        const newState = !showDetailsDefault;
        setShowDetailsDefault(newState);
        setExpandedItems({}); // Reset individual overrides
    };

    return (
        <div className="fixed inset-0 bg-black/50 dark:bg-black/80 backdrop-blur-sm flex items-center justify-center z-50 p-4" onClick={onClose}>
            <div className="bg-white dark:bg-slate-950 border border-slate-200 dark:border-slate-800 rounded-xl w-full max-w-2xl h-[85vh] flex flex-col shadow-2xl overflow-hidden" onClick={e => e.stopPropagation()}>
                {/* Header */}
                <div className="p-6 border-b border-slate-200 dark:border-slate-800 shrink-0 bg-white dark:bg-slate-900/50 backdrop-blur">
                    <div className="flex justify-between items-start">
                        <div className="flex-1 min-w-0 pr-4">
                            <div className="flex items-center gap-3 mb-2">
                                <h2 className="text-xl font-bold text-slate-900 dark:text-white">Remediation Journey</h2>
                                {summary && (
                                    <span className={clsx(
                                        "px-2.5 py-0.5 rounded-full text-xs font-medium border",
                                        summary.isResolved ? "bg-green-500/20 text-green-400 border-green-500/30" :
                                        summary.isFailed ? "bg-red-500/20 text-red-400 border-red-500/30" :
                                        summary.isIgnored ? "bg-slate-500/20 text-slate-600 dark:text-slate-400 border-slate-500/30" :
                                        "bg-yellow-500/20 text-yellow-400 border-yellow-500/30"
                                    )}>
                                        {summary.statusLabel}
                                    </span>
                                )}
                            </div>
                            
                            {summary && (
                                <div className="space-y-1.5 mt-3">
                                    <div className="flex items-start gap-2 text-sm">
                                        <span className="text-slate-500 shrink-0">Corrupted:</span>
                                        <span className="text-slate-700 dark:text-slate-300 font-mono text-xs truncate" title={summary.originalPath}>
                                            {summary.originalFilename}
                                        </span>
                                    </div>
                                    {summary.newFilename && (
                                        <div className="flex items-start gap-2 text-sm">
                                            <span className="text-slate-500 shrink-0">Replaced:</span>
                                            {summary.filesAreDifferent ? (
                                                <span className="text-green-400 font-mono text-xs truncate" title={summary.newPath}>
                                                    {summary.newFilename}
                                                </span>
                                            ) : (
                                                <span className="text-slate-600 dark:text-slate-400 font-mono text-xs italic">
                                                    (same filename)
                                                </span>
                                            )}
                                        </div>
                                    )}
                                </div>
                            )}
                        </div>
                        
                        <div className="flex items-center gap-3 shrink-0">
                            <button
                                onClick={toggleAll}
                                className={clsx(
                                    "flex items-center gap-2 px-3 py-1.5 rounded-lg text-xs font-medium transition-colors border",
                                    showDetailsDefault
                                        ? "bg-purple-500/20 border-purple-500/30 text-purple-300"
                                        : "bg-slate-800 border-slate-300 dark:border-slate-700 text-slate-600 dark:text-slate-400 hover:text-slate-700 dark:text-slate-300"
                                )}
                                title="Toggle showing details by default"
                            >
                                <Settings className="w-3 h-3" />
                                {showDetailsDefault ? "Details: On" : "Details: Off"}
                            </button>
                            <button onClick={onClose} className="text-slate-600 dark:text-slate-400 hover:text-slate-900 dark:text-white cursor-pointer p-2 hover:bg-slate-100 dark:hover:bg-slate-800 rounded-lg transition-colors">
                                ✕
                            </button>
                        </div>
                    </div>
                </div>

                {/* Content */}
                <div className="flex-1 overflow-y-auto p-8 min-h-0 bg-slate-50 dark:bg-slate-950 relative">
                    {isLoading ? (
                        <div className="flex justify-center py-12">
                            <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-purple-500"></div>
                        </div>
                    ) : (
                        <div className="relative py-4">
                            <div className="space-y-8">
                                {history?.map((event, idx) => {
                                    const hasDetails = !!(event.data && typeof event.data === 'object' && Object.keys(event.data as object).length > 0);
                                    const isExpanded = expandedItems[idx] ?? showDetailsDefault;
                                    const isFirst = idx === 0;
                                    const isLast = idx === (history.length - 1);

                                    // Special handling for DeletionCompleted to show path
                                    let primaryInfo = null;
                                    if (event.event_type === 'DeletionCompleted' && event.data && typeof event.data === 'object') {
                                        const data = event.data as Record<string, unknown>;
                                        if (data.metadata && typeof data.metadata === 'object') {
                                            const metadata = data.metadata as Record<string, unknown>;
                                            if (metadata.deleted_path) {
                                                primaryInfo = (
                                                    <div className="text-xs text-slate-600 dark:text-slate-400 font-mono mt-1 break-all">
                                                        Deleted: <span className="text-slate-700 dark:text-slate-300">{String(metadata.deleted_path)}</span>
                                                    </div>
                                                );
                                            }
                                        }
                                    }
                                    // Special handling for NotificationFailed to show error
                                    if (event.event_type === 'NotificationFailed' && event.data && typeof event.data === 'object') {
                                        const data = event.data as Record<string, unknown>;
                                        if (data.error) {
                                            primaryInfo = (
                                                <div className="text-xs text-red-400 font-mono mt-1 break-all">
                                                    Error: <span className="text-red-300">{String(data.error)}</span>
                                                </div>
                                            );
                                        }
                                    }

                                    return (
                                        <motion.div
                                            key={idx}
                                            initial={{ opacity: 0, x: -20 }}
                                            animate={{ opacity: 1, x: 0 }}
                                            transition={{ delay: idx * 0.05 }}
                                            className="relative pl-14"
                                        >
                                            {/* Line Above (connect to previous) */}
                                            {!isFirst && (
                                                <div className="absolute left-[23px] top-0 h-[50%] w-0.5 bg-slate-300 dark:bg-slate-800" />
                                            )}

                                            {/* Line Below (connect to next, spanning gap) */}
                                            {!isLast && (
                                                <div className="absolute left-[23px] top-[50%] bottom-[-2rem] w-0.5 bg-slate-300 dark:bg-slate-800" />
                                            )}

                                            {/* Node Circle - Vertically Centered to the Card */}
                                            <div className="absolute left-0 top-0 bottom-0 w-12 flex items-center justify-center pointer-events-none">
                                                <div className={clsx(
                                                    "w-3 h-3 rounded-full border-2 bg-white dark:bg-slate-950 z-10 shadow-[0_0_10px_rgba(0,0,0,0.2)] dark:shadow-[0_0_10px_rgba(0,0,0,0.5)]",
                                                    getEventColorClass(event.event_type).replace('text-', 'border-')
                                                )} />
                                            </div>

                                            {/* Content Card */}
                                            <div
                                                className={clsx(
                                                    "bg-white dark:bg-slate-900/40 border rounded-lg transition-all duration-200 overflow-hidden group",
                                                    isExpanded ? "border-slate-300 dark:border-slate-700 shadow-lg" : "border-slate-200 dark:border-slate-800/50 hover:border-slate-300 dark:hover:border-slate-700/50 hover:bg-slate-50 dark:hover:bg-slate-900/50"
                                                )}
                                            >
                                                {/* Card Header */}
                                                <div
                                                    className={clsx("p-4 flex items-center gap-3 cursor-pointer select-none", !hasDetails && "cursor-default")}
                                                    onClick={() => hasDetails && toggleItem(idx)}
                                                >
                                                    <div className={clsx("p-2 rounded-lg bg-slate-100 dark:bg-slate-950 border border-slate-200 dark:border-slate-800", getEventColorClass(event.event_type).replace('bg-', 'text-'))}>
                                                        <EventIcon type={event.event_type} className="w-5 h-5" />
                                                    </div>

                                                    <div className="flex-1 min-w-0">
                                                        <div className="flex items-center gap-2">
                                                            <h3 className="font-semibold text-slate-800 dark:text-slate-200 text-sm truncate">
                                                                {getEventDescription(event.event_type, event.data as Record<string, unknown>)}
                                                            </h3>
                                                        </div>
                                                        <time className="text-xs text-slate-500 font-mono mt-0.5 block">
                                                            {formatFull(event.timestamp)}
                                                        </time>
                                                        {primaryInfo}
                                                    </div>

                                                    {hasDetails && (
                                                        <div className={clsx("text-slate-500 transition-transform duration-200", isExpanded && "rotate-180")}>
                                                            <ChevronDown className="w-4 h-4" />
                                                        </div>
                                                    )}
                                                </div>

                                                {/* Expanded Details */}
                                                <AnimatePresence>
                                                    {isExpanded && hasDetails && (
                                                        <motion.div
                                                            initial={{ height: 0, opacity: 0 }}
                                                            animate={{ height: "auto", opacity: 1 }}
                                                            exit={{ height: 0, opacity: 0 }}
                                                            transition={{ duration: 0.2 }}
                                                        >
                                                            <div className="px-4 pb-4 pt-0">
                                                                <div className="bg-slate-100 dark:bg-slate-950/50 rounded border border-slate-200 dark:border-slate-800/50 p-3 text-xs font-mono text-slate-600 dark:text-slate-400 overflow-x-auto">
                                                                    {Object.entries(event.data as object).map(([k, v]) => (
                                                                        <div key={k} className="flex gap-2 py-0.5 border-b border-slate-200 dark:border-slate-800/30 last:border-0">
                                                                            <span className="text-slate-500 shrink-0 select-none">{k}:</span>
                                                                            <span className="text-slate-700 dark:text-slate-300 break-all">
                                                                                {typeof v === 'object' ? JSON.stringify(v) : String(v)}
                                                                            </span>
                                                                        </div>
                                                                    ))}
                                                                </div>
                                                            </div>
                                                        </motion.div>
                                                    )}
                                                </AnimatePresence>
                                            </div>
                                        </motion.div>
                                    );
                                })}

                                {history?.length === 0 && (
                                    <div className="text-center text-slate-500 py-8 italic">
                                        No history found for this corruption.
                                    </div>
                                )}
                            </div>
                        </div>
                    )}
                </div>
            </div>
        </div>
    );
};

export default RemediationJourney;
