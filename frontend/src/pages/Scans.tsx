import { useQuery, useQueryClient } from '@tanstack/react-query';
import { getScans, cancelScan, rescanPath } from '../lib/api';
import DataGrid from '../components/ui/DataGrid';
import clsx from 'clsx';
import { useState } from 'react';
import { ArrowUpDown, X, RefreshCw, Loader2 } from 'lucide-react';
import { useDateFormat } from '../lib/useDateFormat';
import { useToast } from '../contexts/ToastContext';
import { useNavigate } from 'react-router-dom';

const LIMIT_STORAGE_KEY = 'healarr_scans_limit';

const Scans = () => {
    const [page, setPage] = useState(1);
    const [limit, setLimit] = useState(() => {
        const stored = localStorage.getItem(LIMIT_STORAGE_KEY);
        return stored ? parseInt(stored, 10) : 50;
    });
    const [sortBy, setSortBy] = useState<string>('started_at');
    const [sortOrder, setSortOrder] = useState<'asc' | 'desc'>('desc');
    const [loadingAction, setLoadingAction] = useState<number | null>(null);
    const navigate = useNavigate();
    const { formatTime, formatDate } = useDateFormat();
    const toast = useToast();
    const queryClient = useQueryClient();

    // Handle limit changes with localStorage persistence
    const handleLimitChange = (newLimit: number) => {
        setLimit(newLimit);
        setPage(1);
        localStorage.setItem(LIMIT_STORAGE_KEY, String(newLimit));
    };

    const { data, isLoading } = useQuery({
        queryKey: ['scans', page, limit, sortBy, sortOrder],
        queryFn: () => getScans(page, limit, sortBy, sortOrder),
        // Polling removed - WebSocket invalidates queries on events
    });

    const handleSort = (field: string) => {
        if (sortBy === field) {
            setSortOrder(sortOrder === 'asc' ? 'desc' : 'asc');
        } else {
            setSortBy(field);
            setSortOrder('desc');
        }
    };

    const handleCancel = async (e: React.MouseEvent, scanId: string) => {
        e.stopPropagation();
        setLoadingAction(parseInt(scanId));
        try {
            await cancelScan(scanId);
            toast.success('Scan cancelled');
            queryClient.invalidateQueries({ queryKey: ['scans'] });
        } catch {
            toast.error('Failed to cancel scan');
        } finally {
            setLoadingAction(null);
        }
    };

    const handleRescan = async (e: React.MouseEvent, scanId: number) => {
        e.stopPropagation();
        setLoadingAction(scanId);
        try {
            await rescanPath(scanId);
            toast.success('Rescan started');
            queryClient.invalidateQueries({ queryKey: ['scans'] });
        } catch {
            toast.error('Failed to start rescan');
        } finally {
            setLoadingAction(null);
        }
    };

    return (
        <div className="space-y-6">
            <div>
                <h1 className="text-3xl font-bold text-slate-900 dark:text-white mb-2">Scan History</h1>
                <p className="text-slate-600 dark:text-slate-400">View all completed and in-progress media scans</p>
            </div>

            <DataGrid
                isLoading={isLoading}
                data={data?.data || []}
                onRowClick={(row) => navigate(`/scans/${row.id}`)}
                mobileCardTitle={(row) => row.path}
                columns={[
                    {
                        header: (
                            <button className="flex items-center gap-1 hover:text-slate-900 dark:hover:text-white" onClick={() => handleSort('started_at')}>
                                Started <ArrowUpDown className="w-3 h-3" />
                            </button>
                        ),
                        accessorKey: (row) => (
                            <div className="flex flex-col">
                                <span className="text-slate-700 dark:text-slate-300">{formatTime(row.started_at)}</span>
                                <span className="text-xs text-slate-500">{formatDate(row.started_at)}</span>
                            </div>
                        ),
                        className: 'w-32',
                        mobileLabel: 'Started',
                    },
                    {
                        header: (
                            <button className="flex items-center gap-1 hover:text-slate-900 dark:hover:text-white" onClick={() => handleSort('path')}>
                                Path <ArrowUpDown className="w-3 h-3" />
                            </button>
                        ),
                        accessorKey: 'path',
                        hideOnMobile: true,  // Shown via mobileCardTitle instead
                    },
                    {
                        header: (
                            <button className="flex items-center gap-1 hover:text-slate-900 dark:hover:text-white" onClick={() => handleSort('status')}>
                                Status <ArrowUpDown className="w-3 h-3" />
                            </button>
                        ),
                        accessorKey: (row) => (
                            <span className={clsx(
                                "px-2 py-1 rounded-full text-xs font-medium border",
                                row.status === 'completed' ? "bg-green-500/10 text-green-400 border-green-500/20" :
                                    row.status === 'failed' ? "bg-red-500/10 text-red-400 border-red-500/20" :
                                        "bg-blue-500/10 text-blue-400 border-blue-500/20"
                            )}>
                                {row.status}
                            </span>
                        ),
                        mobileLabel: 'Status',
                        isPrimary: true,
                    },
                    {
                        header: (
                            <button className="flex items-center gap-1 hover:text-slate-900 dark:hover:text-white" onClick={() => handleSort('files_scanned')}>
                                Files Scanned <ArrowUpDown className="w-3 h-3" />
                            </button>
                        ),
                        accessorKey: 'files_scanned',
                        className: 'text-center',
                        mobileLabel: 'Files',
                    },
                    {
                        header: (
                            <button className="flex items-center gap-1 hover:text-slate-900 dark:hover:text-white" onClick={() => handleSort('corruptions_found')}>
                                Corruptions Found <ArrowUpDown className="w-3 h-3" />
                            </button>
                        ),
                        accessorKey: 'corruptions_found',
                        className: 'text-center',
                        mobileLabel: 'Corruptions',
                    },
                    {
                        header: 'Actions',
                        accessorKey: (row) => (
                            <div className="flex items-center gap-2" onClick={(e) => e.stopPropagation()}>
                                {row.status === 'running' ? (
                                    <button
                                        onClick={(e) => handleCancel(e, String(row.id))}
                                        disabled={loadingAction === row.id}
                                        className="p-1.5 rounded-md bg-red-500/10 hover:bg-red-500/20 text-red-400 hover:text-red-300 border border-red-500/20 hover:border-red-500/30 transition-colors cursor-pointer disabled:opacity-50"
                                        title="Cancel Scan"
                                    >
                                        {loadingAction === row.id ? (
                                            <Loader2 className="w-4 h-4 animate-spin" />
                                        ) : (
                                            <X className="w-4 h-4" />
                                        )}
                                    </button>
                                ) : (
                                    <button
                                        onClick={(e) => handleRescan(e, row.id)}
                                        disabled={loadingAction === row.id}
                                        className="p-1.5 rounded-md bg-blue-500/10 hover:bg-blue-500/20 text-blue-400 hover:text-blue-300 border border-blue-500/20 hover:border-blue-500/30 transition-colors cursor-pointer disabled:opacity-50"
                                        title="Rescan"
                                    >
                                        {loadingAction === row.id ? (
                                            <Loader2 className="w-4 h-4 animate-spin" />
                                        ) : (
                                            <RefreshCw className="w-4 h-4" />
                                        )}
                                    </button>
                                )}
                            </div>
                        ),
                        className: 'w-20',
                        hideOnMobile: true,
                    },
                ]}
                pagination={{
                    page: data?.pagination?.page || 1,
                    limit: data?.pagination?.limit || limit,
                    total: data?.pagination?.total || 0,
                    onPageChange: setPage,
                    onLimitChange: handleLimitChange,
                }}
            />
        </div>
    );
};

export default Scans;
