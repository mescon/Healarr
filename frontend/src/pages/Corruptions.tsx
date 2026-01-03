import { useState, useRef, useEffect } from 'react';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { getCorruptions, retryCorruptions, ignoreCorruptions, deleteCorruptions } from '../lib/api';
import DataGrid from '../components/ui/DataGrid';
import RemediationJourney from '../components/RemediationJourney';
import clsx from 'clsx';
import { AlertTriangle, ArrowUpDown, Filter, RefreshCw, EyeOff, Trash2, X, AlertCircle, FolderOpen } from 'lucide-react';
import { formatCorruptionType, formatCorruptionState } from '../lib/formatters';
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
    const [statusFilter, setStatusFilter] = useState<string>(() => searchParams.get('status') || 'active');
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
                        <option value="active">Active (All Open)</option>
                        <option value="pending">Pending</option>
                        <option value="in_progress">In Progress</option>
                        <option value="manual_intervention">Manual Intervention</option>
                        <option value="failed">Failed (Retrying)</option>
                        <option value="orphaned">Max Retries</option>
                        <option value="resolved">Successfully Remediated</option>
                        <option value="ignored">Ignored</option>
                        <option value="all">All Statuses</option>
                    </select>
                </div>
            </div>

            {/* Path Filter Banner */}
            {pathIdFilter !== undefined && (
                <div className="bg-blue-500/10 border border-blue-500/30 rounded-xl p-4 flex items-center gap-3">
                    <FolderOpen className="w-5 h-5 text-blue-400 shrink-0" />
                    <div className="flex-1">
                        <p className="text-blue-300 font-medium">
                            Filtering by scan path ID: {pathIdFilter}
                        </p>
                        <p className="text-sm text-blue-400/80 mt-0.5">
                            Showing corruptions from a specific scan path.
                        </p>
                    </div>
                    <button
                        onClick={clearPathFilter}
                        className="flex items-center gap-2 px-3 py-1.5 text-sm font-medium bg-blue-500/20 hover:bg-blue-500/30 text-blue-300 rounded-lg border border-blue-500/30 transition-colors cursor-pointer"
                    >
                        <X className="w-4 h-4" />
                        Clear Filter
                    </button>
                </div>
            )}

            {/* Manual Intervention Alert Banner */}
            {manualInterventionCount > 0 && statusFilter !== 'manual_intervention' && (
                <div className="bg-purple-500/10 border border-purple-500/30 rounded-xl p-4 flex items-start gap-3">
                    <AlertCircle className="w-5 h-5 text-purple-400 mt-0.5 shrink-0" />
                    <div className="flex-1">
                        <h3 className="font-medium text-purple-300">
                            {manualInterventionCount} item{manualInterventionCount > 1 ? 's' : ''} require{manualInterventionCount === 1 ? 's' : ''} manual intervention
                        </h3>
                        <p className="text-sm text-purple-400/80 mt-1">
                            These corruptions could not be automatically remediated and need attention in Sonarr/Radarr.
                        </p>
                    </div>
                    <button
                        onClick={() => handleStatusFilterChange('manual_intervention')}
                        className="px-3 py-1.5 text-sm font-medium bg-purple-500/20 hover:bg-purple-500/30 text-purple-300 rounded-lg border border-purple-500/30 transition-colors cursor-pointer whitespace-nowrap"
                    >
                        View Items
                    </button>
                </div>
            )}

            <DataGrid
                isLoading={isLoading}
                data={data?.data || []}
                onRowClick={(row) => setSelectedCorruptionId(row.id)}
                columns={[
                    {
                        header: (
                            <input
                                type="checkbox"
                                checked={allSelected}
                                onChange={handleSelectAll}
                                className="w-4 h-4 rounded border-slate-600 bg-slate-800 text-purple-500 focus:ring-purple-500 focus:ring-offset-0 cursor-pointer"
                            />
                        ),
                        accessorKey: (row) => (
                            <input
                                type="checkbox"
                                checked={selectedIds.has(row.id)}
                                readOnly
                                className="w-4 h-4 rounded border-slate-600 bg-slate-800 text-purple-500 focus:ring-purple-500 focus:ring-offset-0 pointer-events-none"
                            />
                        ),
                        className: 'w-10',
                        stopPropagation: true,
                        onCellClick: (row, index, e) => toggleSelect(row.id, index, e)
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
                        className: 'w-32'
                    },
                    {
                        header: (
                            <button className="flex items-center gap-1 hover:text-slate-900 dark:hover:text-white" onClick={() => handleSort('file_path')}>
                                File Path <ArrowUpDown className="w-3 h-3" />
                            </button>
                        ),
                        accessorKey: (row) => {
                            // Extract meaningful parts from the path
                            const parts = row.file_path.split('/');
                            const filename = parts.pop() || '';
                            // Get the last 2-3 meaningful directory parts (e.g., "Colony/Season 01")
                            const relevantParts = parts.slice(-3).filter((p: string) => p && p !== 'mnt' && p !== 'media');
                            const contextPath = relevantParts.join(' / ');
                            
                            return (
                                <div className="flex flex-col max-w-lg">
                                    <span className="font-medium text-slate-700 dark:text-slate-300 truncate" title={filename}>
                                        {filename}
                                    </span>
                                    <span className="text-xs text-slate-500 truncate" title={row.file_path}>
                                        {contextPath}
                                    </span>
                                    {row.last_error && (
                                        <div className="mt-1 flex items-start gap-1 text-xs text-red-400 bg-red-500/10 p-1 rounded border border-red-500/20">
                                            <AlertTriangle className="w-3 h-3 mt-0.5 shrink-0" />
                                            <span className="line-clamp-2" title={row.last_error}>{row.last_error}</span>
                                        </div>
                                    )}
                                </div>
                            );
                        }
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
                        )
                    },
                    {
                        header: (
                            <button className="flex items-center gap-1 hover:text-slate-900 dark:hover:text-white" onClick={() => handleSort('state')}>
                                Status <ArrowUpDown className="w-3 h-3" />
                            </button>
                        ),
                        accessorKey: (row) => {
                            const { label, colorClass } = formatCorruptionState(row.state);
                            return (
                                <span className={clsx("px-2 py-1 rounded-full text-xs font-medium border", colorClass)}>
                                    {label}
                                </span>
                            );
                        }
                    },
                    { header: 'Retries', accessorKey: 'retry_count', className: 'text-center' },
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
                    <div className="h-6 w-px bg-slate-700" />
                    <button
                        onClick={handleBulkRetry}
                        className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-blue-500/20 hover:bg-blue-500/30 text-blue-400 border border-blue-500/30 transition-colors text-sm font-medium cursor-pointer"
                    >
                        <RefreshCw className="w-4 h-4" />
                        Retry
                    </button>
                    <button
                        onClick={handleBulkIgnore}
                        className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-slate-500/20 hover:bg-slate-500/30 text-slate-300 border border-slate-500/30 transition-colors text-sm font-medium cursor-pointer"
                    >
                        <EyeOff className="w-4 h-4" />
                        Ignore
                    </button>
                    <button
                        onClick={handleBulkDelete}
                        className="flex items-center gap-2 px-3 py-1.5 rounded-lg bg-red-500/20 hover:bg-red-500/30 text-red-400 border border-red-500/30 transition-colors text-sm font-medium cursor-pointer"
                    >
                        <Trash2 className="w-4 h-4" />
                        Delete
                    </button>
                    <div className="h-6 w-px bg-slate-700" />
                    <button
                        onClick={() => setSelectedIds(new Set())}
                        className="p-1.5 rounded-lg hover:bg-slate-800 text-slate-400 hover:text-slate-300 transition-colors cursor-pointer"
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
