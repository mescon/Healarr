import { useState, useRef, useEffect, useMemo } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { getCorruptions, retryCorruptions, ignoreCorruptions, deleteCorruptions, getScanPaths } from '../lib/api';
import DataGrid from '../components/ui/DataGrid';
import RemediationJourney from '../components/RemediationJourney';
import clsx from 'clsx';
import { AlertTriangle, ArrowUpDown, Filter, RefreshCw, EyeOff, Trash2, X, AlertCircle, FolderOpen, Film, Tv } from 'lucide-react';
import { formatCorruptionType, formatCorruptionState, formatBytes, formatDuration, getDownloadClientIcon, getArrIcon } from '../lib/formatters';
import { useDateFormat } from '../lib/useDateFormat';
import { useToast } from '../contexts/ToastContext';

const LIMIT_STORAGE_KEY = 'healarr_corruptions_limit';

const Corruptions = () => {
    const [searchParams, setSearchParams] = useSearchParams();
    const [selectedCorruptionId, setSelectedCorruptionId] = useState<string | null>(null);
    const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());
    const lastClickedIndex = useRef<number | null>(null);
    const [page, setPage] = useState(1);
    const [limit, setLimit] = useState(() => {
        const stored = localStorage.getItem(LIMIT_STORAGE_KEY);
        return stored ? parseInt(stored, 10) : 50;
    });
    const [sortBy, setSortBy] = useState<string>('detected_at');
    const [sortOrder, setSortOrder] = useState<'asc' | 'desc'>('desc');
    const [statusFilter, setStatusFilter] = useState<string>(() => searchParams.get('status') || 'action_required');
    const pathIdFilter = searchParams.get('path_id') ? parseInt(searchParams.get('path_id')!, 10) : undefined;
    const { formatTime, formatDate } = useDateFormat();
    const toast = useToast();
    const queryClient = useQueryClient();

    // Handle limit changes with localStorage persistence
    const handleLimitChange = (newLimit: number) => {
        setLimit(newLimit);
        setPage(1);
        localStorage.setItem(LIMIT_STORAGE_KEY, String(newLimit));
    };

    // Clear path_id filter
    const clearPathFilter = () => {
        searchParams.delete('path_id');
        setSearchParams(searchParams, { replace: true });
    };

    // Sync URL params with filter state
    useEffect(() => {
        const urlStatus = searchParams.get('status');
        if (urlStatus && urlStatus !== statusFilter) {
            setStatusFilter(urlStatus);
            setPage(1); // Reset to first page when filter changes via URL
        }
    }, [searchParams, statusFilter]);

    // Update URL when filter changes
    const handleStatusFilterChange = (newStatus: string) => {
        setStatusFilter(newStatus);
        setPage(1);
        if (newStatus === 'all') {
            searchParams.delete('status');
        } else {
            searchParams.set('status', newStatus);
        }
        setSearchParams(searchParams, { replace: true });
    };

    const { data, isLoading } = useQuery({
        queryKey: ['corruptions', page, limit, sortBy, sortOrder, statusFilter, pathIdFilter],
        queryFn: () => getCorruptions(page, limit, sortBy, sortOrder, statusFilter, pathIdFilter),
        // Polling removed - WebSocket invalidates queries on events
    });

    // Query scan paths to resolve path_id to actual path name
    const { data: scanPaths } = useQuery({
        queryKey: ['scanPaths'],
        queryFn: getScanPaths,
        staleTime: 60000, // Cache for 1 minute
    });

    // Resolve path_id to the actual path name
    const resolvedPathName = useMemo(() => {
        if (pathIdFilter === undefined || !scanPaths) return null;
        const matchingPath = scanPaths.find(p => p.id === pathIdFilter);
        return matchingPath?.local_path || null;
    }, [pathIdFilter, scanPaths]);

    // Query for manual intervention count (always fetch to show alert banner)
    const { data: manualInterventionData } = useQuery({
        queryKey: ['corruptions', 1, 1, 'detected_at', 'desc', 'manual_intervention'],
        queryFn: () => getCorruptions(1, 1, 'detected_at', 'desc', 'manual_intervention'),
    });
    const manualInterventionCount = manualInterventionData?.pagination?.total || 0;

    const handleSort = (field: string) => {
        if (sortBy === field) {
            setSortOrder(sortOrder === 'asc' ? 'desc' : 'asc');
        } else {
            setSortBy(field);
            setSortOrder('desc');
        }
    };

    // Multi-select handlers
    const handleSelectAll = () => {
        if (!data?.data) return;
        if (selectedIds.size === data.data.length) {
            setSelectedIds(new Set());
        } else {
            setSelectedIds(new Set(data.data.map((r: { id: string }) => r.id)));
        }
    };

    const toggleSelect = (id: string, index: number, e: React.MouseEvent) => {
        e.stopPropagation();
        
        if (e.shiftKey && lastClickedIndex.current !== null && data?.data) {
            // Shift+click: select range
            const start = Math.min(lastClickedIndex.current, index);
            const end = Math.max(lastClickedIndex.current, index);
            const rangeIds = data.data.slice(start, end + 1).map((r: { id: string }) => r.id);
            
            setSelectedIds(prev => {
                const next = new Set(prev);
                rangeIds.forEach((rid: string) => next.add(rid));
                return next;
            });
        } else {
            // Normal click: toggle single item
            setSelectedIds(prev => {
                const next = new Set(prev);
                if (next.has(id)) next.delete(id);
                else next.add(id);
                return next;
            });
            lastClickedIndex.current = index;
        }
    };

    // Bulk action handlers
    const handleBulkRetry = async () => {
        try {
            const ids = Array.from(selectedIds);
            const result = await retryCorruptions(ids);
            toast.success(result.message);
            setSelectedIds(new Set());
            queryClient.invalidateQueries({ queryKey: ['corruptions'] });
        } catch (error) {
            toast.error('Failed to retry corruptions');
        }
    };

    const handleBulkIgnore = async () => {
        try {
            const ids = Array.from(selectedIds);
            const result = await ignoreCorruptions(ids);
            toast.success(result.message);
            setSelectedIds(new Set());
            queryClient.invalidateQueries({ queryKey: ['corruptions'] });
        } catch (error) {
            toast.error('Failed to ignore corruptions');
        }
    };

    const handleBulkDelete = async () => {
        if (!confirm(`Are you sure you want to delete ${selectedIds.size} corruption record(s)? This cannot be undone.`)) {
            return;
        }
        try {
            const ids = Array.from(selectedIds);
            const result = await deleteCorruptions(ids);
            toast.success(result.message);
            setSelectedIds(new Set());
            queryClient.invalidateQueries({ queryKey: ['corruptions'] });
        } catch (error) {
            toast.error('Failed to delete corruptions');
        }
    };

    const allSelected = data?.data && data.data.length > 0 && selectedIds.size === data.data.length;

    return (
        <div className="space-y-6">
            <div className="flex flex-col md:flex-row md:items-end justify-between gap-4">
                <div>
                    <h1 className="text-3xl font-bold text-slate-900 dark:text-white mb-2">Corruption Report</h1>
                    <p className="text-slate-600 dark:text-slate-400">Detected media integrity issues and remediation status. Click a row to view history.</p>
                </div>

                <div className="flex items-center gap-2 bg-white dark:bg-slate-900/50 p-1 rounded-lg border border-slate-200 dark:border-slate-800">
                    <Filter className="w-4 h-4 text-slate-400 ml-2" />
                    <select
                        value={statusFilter}
                        onChange={(e) => handleStatusFilterChange(e.target.value)}
                        className="bg-white dark:bg-slate-900 text-sm text-slate-700 dark:text-slate-300 border-none focus:ring-0 cursor-pointer py-1 pr-8 rounded [&>option]:bg-white dark:[&>option]:bg-slate-900 [&>option]:text-slate-700 dark:[&>option]:text-slate-300"
                    >
                        <option value="action_required">Action Required</option>
                        <option value="working">In Progress</option>
                        <option value="resolved">Resolved</option>
                        <option value="ignored">Ignored</option>
                        <option value="all">All</option>
                    </select>
                </div>
            </div>

            {/* Path Filter Banner */}
            {pathIdFilter !== undefined && (
                <div className="bg-blue-500/10 border border-blue-500/30 rounded-xl p-4 flex items-center gap-3">
                    <FolderOpen className="w-5 h-5 text-blue-500 dark:text-blue-400 shrink-0" />
                    <div className="flex-1 min-w-0">
                        <p className="text-blue-600 dark:text-blue-300 font-medium">
                            Filtering by scan path
                        </p>
                        <p className="text-sm text-blue-500/80 dark:text-blue-400/80 mt-0.5 font-mono truncate" title={resolvedPathName || `ID: ${pathIdFilter}`}>
                            {resolvedPathName || `Path ID: ${pathIdFilter}`}
                        </p>
                    </div>
                    <button
                        onClick={clearPathFilter}
                        className="flex items-center gap-2 px-3 py-1.5 text-sm font-medium bg-blue-500/20 hover:bg-blue-500/30 text-blue-600 dark:text-blue-300 rounded-lg border border-blue-500/30 transition-colors cursor-pointer"
                    >
                        <X className="w-4 h-4" />
                        Clear Filter
                    </button>
                </div>
            )}

            {/* Manual Intervention Alert Banner */}
            {manualInterventionCount > 0 && statusFilter !== 'manual_intervention' && (
                <div className="bg-purple-500/10 border border-purple-500/30 rounded-xl p-4 flex items-start gap-3">
                    <AlertCircle className="w-5 h-5 text-purple-500 dark:text-purple-400 mt-0.5 shrink-0" />
                    <div className="flex-1">
                        <h3 className="font-medium text-purple-600 dark:text-purple-300">
                            {manualInterventionCount} item{manualInterventionCount > 1 ? 's' : ''} require{manualInterventionCount === 1 ? 's' : ''} manual intervention
                        </h3>
                        <p className="text-sm text-purple-500/80 dark:text-purple-400/80 mt-1">
                            These items are blocked in your *arr application. To resolve:
                        </p>
                        <ol className="text-sm text-purple-500/80 dark:text-purple-400/80 mt-2 ml-4 list-decimal space-y-1">
                            <li>Open your *arr app and check <span className="font-medium">Activity → Queue</span></li>
                            <li>Look for blocked imports, failed downloads, or manually removed items</li>
                            <li>Resolve the issue in *arr, then click <span className="font-medium">Retry</span> here</li>
                        </ol>
                    </div>
                    <button
                        onClick={() => handleStatusFilterChange('manual_intervention')}
                        className="px-3 py-1.5 text-sm font-medium bg-purple-500/20 hover:bg-purple-500/30 text-purple-600 dark:text-purple-300 rounded-lg border border-purple-500/30 transition-colors cursor-pointer whitespace-nowrap"
                    >
                        View Items
                    </button>
                </div>
            )}

            <DataGrid
                isLoading={isLoading}
                data={data?.data || []}
                onRowClick={(row) => setSelectedCorruptionId(row.id)}
                mobileCardTitle={(row) => {
                    // Format title for mobile cards
                    if (row.media_title) {
                        if (row.media_type === 'series' && row.season_number && row.episode_number) {
                            const s = String(row.season_number).padStart(2, '0');
                            const e = String(row.episode_number).padStart(2, '0');
                            return `${row.media_title} S${s}E${e}`;
                        } else if (row.media_year) {
                            return `${row.media_title} (${row.media_year})`;
                        }
                        return row.media_title;
                    }
                    const parts = row.file_path.split('/');
                    return parts.pop() || row.file_path;
                }}
                columns={[
                    {
                        header: (
                            <input
                                type="checkbox"
                                checked={allSelected}
                                onChange={handleSelectAll}
                                className="w-4 h-4 rounded border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-800 text-purple-500 focus:ring-purple-500 focus:ring-offset-0 cursor-pointer"
                            />
                        ),
                        accessorKey: (row) => (
                            <input
                                type="checkbox"
                                checked={selectedIds.has(row.id)}
                                readOnly
                                className="w-4 h-4 rounded border-slate-300 dark:border-slate-600 bg-white dark:bg-slate-800 text-purple-500 focus:ring-purple-500 focus:ring-offset-0 pointer-events-none"
                            />
                        ),
                        className: 'w-10',
                        stopPropagation: true,
                        onCellClick: (row, index, e) => toggleSelect(row.id, index, e),
                        hideOnMobile: true, // Bulk select doesn't work well on mobile
                    },
                    {
                        header: (
                            <button className="flex items-center gap-1 hover:text-slate-900 dark:hover:text-white" onClick={() => handleSort('detected_at')}>
                                Detected <ArrowUpDown className="w-3 h-3" />
                            </button>
                        ),
                        accessorKey: (row) => (
                            <div className="flex flex-col">
                                <span className="text-slate-700 dark:text-slate-300">{formatTime(row.detected_at)}</span>
                                <span className="text-xs text-slate-500">{formatDate(row.detected_at)}</span>
                            </div>
                        ),
                        className: 'w-32',
                        mobileLabel: 'Detected',
                    },
                    {
                        header: (
                            <button className="flex items-center gap-1 hover:text-slate-900 dark:hover:text-white" onClick={() => handleSort('file_path')}>
                                Media <ArrowUpDown className="w-3 h-3" />
                            </button>
                        ),
                        hideOnMobile: true, // We use mobileCardTitle for this
                        accessorKey: (row) => {
                            // Format media title like "Colony S01E08" or "The Matrix (1999)"
                            let displayTitle = '';
                            let subtitle = '';
                            const hasMediaInfo = row.media_title || row.media_type;

                            if (row.media_title) {
                                if (row.media_type === 'series' && row.season_number && row.episode_number) {
                                    // Format: "Colony S01E08"
                                    const s = String(row.season_number).padStart(2, '0');
                                    const e = String(row.episode_number).padStart(2, '0');
                                    displayTitle = `${row.media_title} S${s}E${e}`;
                                } else if (row.media_year) {
                                    // Format: "The Matrix (1999)"
                                    displayTitle = `${row.media_title} (${row.media_year})`;
                                } else {
                                    displayTitle = row.media_title;
                                }
                            } else {
                                // Fallback to filename
                                const parts = row.file_path.split('/');
                                displayTitle = parts.pop() || row.file_path;
                            }

                            // Build subtitle: instance name + file path context
                            const pathParts = row.file_path.split('/');
                            pathParts.pop(); // remove filename
                            const contextPath = pathParts.slice(-2).filter((p: string) => p && p !== 'mnt' && p !== 'media').join(' / ');

                            if (row.instance_name) {
                                subtitle = `${row.instance_name} • ${contextPath}`;
                            } else {
                                subtitle = contextPath;
                            }

                            return (
                                <div className="flex items-start gap-2 max-w-lg">
                                    {/* Media type icon */}
                                    {row.arr_type ? (
                                        <img src={getArrIcon(row.arr_type)} alt="" className="w-4 h-4 mt-0.5 opacity-60" />
                                    ) : row.media_type === 'series' ? (
                                        <Tv className="w-4 h-4 mt-0.5 text-slate-400" />
                                    ) : row.media_type === 'movie' ? (
                                        <Film className="w-4 h-4 mt-0.5 text-slate-400" />
                                    ) : null}
                                    <div className="flex flex-col min-w-0">
                                        <span className="font-medium text-slate-700 dark:text-slate-300 truncate" title={hasMediaInfo ? row.file_path : displayTitle}>
                                            {displayTitle}
                                        </span>
                                        <span className="text-xs text-slate-500 truncate" title={row.file_path}>
                                            {subtitle}
                                        </span>
                                        {row.last_error && (
                                            <div className="mt-1 flex items-start gap-1 text-xs text-red-400 bg-red-500/10 p-1 rounded border border-red-500/20">
                                                <AlertTriangle className="w-3 h-3 mt-0.5 shrink-0" />
                                                <span className="line-clamp-2" title={row.last_error}>{row.last_error}</span>
                                            </div>
                                        )}
                                    </div>
                                </div>
                            );
                        }
                    },
                    {
                        header: 'Size',
                        accessorKey: (row) => {
                            // Show file size if available
                            const size = row.file_size || row.new_file_size;
                            if (!size) return <span className="text-slate-500">—</span>;
                            return (
                                <span className="text-slate-700 dark:text-slate-300 text-sm">
                                    {formatBytes(size)}
                                </span>
                            );
                        },
                        className: 'w-24 text-right',
                        mobileLabel: 'Size',
                    },
                    {
                        header: (
                            <button className="flex items-center gap-1 hover:text-slate-900 dark:hover:text-white" onClick={() => handleSort('corruption_type')}>
                                Type <ArrowUpDown className="w-3 h-3" />
                            </button>
                        ),
                        accessorKey: (row) => (
                            <span className="text-slate-700 dark:text-slate-300">
                                {formatCorruptionType(row.corruption_type || 'Unknown')}
                            </span>
                        ),
                        mobileLabel: 'Type',
                    },
                    {
                        header: (
                            <button className="flex items-center gap-1 hover:text-slate-900 dark:hover:text-white" onClick={() => handleSort('state')}>
                                Status <ArrowUpDown className="w-3 h-3" />
                            </button>
                        ),
                        accessorKey: (row) => {
                            const { label, colorClass } = formatCorruptionState(row.state);

                            // For downloading state, show progress info
                            if (row.state === 'SearchCompleted' && row.download_progress !== undefined) {
                                const progress = Math.round(row.download_progress);
                                const downloaded = row.download_size && row.download_remaining
                                    ? formatBytes(row.download_size - row.download_remaining)
                                    : null;
                                const total = row.download_size ? formatBytes(row.download_size) : null;

                                return (
                                    <div className="flex flex-col">
                                        <div className="flex items-center gap-2">
                                            {row.download_client && (
                                                <img src={getDownloadClientIcon(row.download_client)} alt="" className="w-4 h-4" />
                                            )}
                                            <span className={clsx("px-2 py-0.5 rounded-full text-xs font-medium border whitespace-nowrap", colorClass)}>
                                                {progress}%
                                            </span>
                                        </div>
                                        {downloaded && total && (
                                            <span className="text-xs text-slate-500 mt-0.5">
                                                {downloaded} / {total}
                                            </span>
                                        )}
                                    </div>
                                );
                            }

                            // For resolved state, show duration
                            if (row.state === 'VerificationSuccess' && row.total_duration_seconds) {
                                return (
                                    <div className="flex flex-col">
                                        <span className={clsx("px-2 py-1 rounded-full text-xs font-medium border whitespace-nowrap", colorClass)}>
                                            {label}
                                        </span>
                                        <span className="text-xs text-slate-500 mt-0.5">
                                            took {formatDuration(row.total_duration_seconds)}
                                        </span>
                                    </div>
                                );
                            }

                            // Default status badge
                            return (
                                <span className={clsx("px-2 py-1 rounded-full text-xs font-medium border whitespace-nowrap", colorClass)}>
                                    {label}
                                </span>
                            );
                        },
                        mobileLabel: 'Status',
                        isPrimary: true, // Show status in card header on mobile
                    },
                    { header: 'Retries', accessorKey: 'retry_count', className: 'text-center w-20', mobileLabel: 'Retries' },
                ]}
                pagination={{
                    page: data?.pagination?.page || 1,
                    limit: data?.pagination?.limit || limit,
                    total: data?.pagination?.total || 0,
                    onPageChange: setPage,
                    onLimitChange: handleLimitChange,
                }}
            />

            {/* Bulk Action Bar */}
            {selectedIds.size > 0 && (
                <div className="fixed bottom-6 left-1/2 -translate-x-1/2 bg-white dark:bg-slate-900 border border-slate-200 dark:border-slate-700 rounded-xl px-6 py-3 shadow-2xl flex items-center gap-4 z-40">
                    <span className="text-slate-700 dark:text-slate-300 font-medium">
                        {selectedIds.size} selected
                    </span>
                    <div className="h-6 w-px bg-slate-300 dark:bg-slate-700" />
                    <button
                        onClick={handleBulkRetry}
                        className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-blue-500/20 hover:bg-blue-500/30 text-blue-400 border border-blue-500/30 transition-colors text-sm font-medium cursor-pointer"
                    >
                        <RefreshCw className="w-4 h-4" />
                        Retry
                    </button>
                    <button
                        onClick={handleBulkIgnore}
                        className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-slate-500/20 hover:bg-slate-500/30 text-slate-600 dark:text-slate-300 border border-slate-500/30 transition-colors text-sm font-medium cursor-pointer"
                    >
                        <EyeOff className="w-4 h-4" />
                        Ignore
                    </button>
                    <button
                        onClick={handleBulkDelete}
                        className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-red-500/20 hover:bg-red-500/30 text-red-600 dark:text-red-400 border border-red-500/30 transition-colors text-sm font-medium cursor-pointer"
                    >
                        <Trash2 className="w-4 h-4" />
                        Delete
                    </button>
                    <div className="h-6 w-px bg-slate-300 dark:bg-slate-700" />
                    <button
                        onClick={() => setSelectedIds(new Set())}
                        className="p-1.5 rounded-lg hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-300 transition-colors cursor-pointer"
                        title="Clear selection"
                    >
                        <X className="w-4 h-4" />
                    </button>
                </div>
            )}

            {selectedCorruptionId && (
                <RemediationJourney
                    corruptionId={selectedCorruptionId}
                    onClose={() => setSelectedCorruptionId(null)}
                />
            )}
        </div>
    );
};

export default Corruptions;
