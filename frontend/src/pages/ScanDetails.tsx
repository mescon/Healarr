import { useState, useEffect } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, FileCheck, FileX, Loader2, Filter, HardDrive, Clock, FolderOpen, AlertCircle, X, RefreshCw, ClockArrowDown, ExternalLink, Radio, SkipForward, ShieldAlert, HelpCircle } from 'lucide-react';
import clsx from 'clsx';
import { getScanDetails, getScanFiles, cancelScan, rescanPath, type ScanFile, type ScanProgress } from '../lib/api';
import DataGrid from '../components/ui/DataGrid';
import { useDateFormat } from '../lib/useDateFormat';
import { useToast } from '../contexts/ToastContext';
import { useWebSocket } from '../contexts/WebSocketProvider';
import { formatBytes } from '../lib/formatters';

const ScanDetails = () => {
    const { id } = useParams();
    const navigate = useNavigate();
    const [page, setPage] = useState(1);
    const [statusFilter, setStatusFilter] = useState<string>('all');
    const [isActionLoading, setIsActionLoading] = useState(false);
    const [currentFile, setCurrentFile] = useState<string | null>(null);
    const [scanProgress, setScanProgress] = useState<{ filesDone: number; totalFiles: number } | null>(null);
    const limit = 50;
    const { formatCompact, formatTime } = useDateFormat();
    const toast = useToast();
    const queryClient = useQueryClient();
    const { lastMessage } = useWebSocket();

    const scanId = parseInt(id || '0', 10);

    const { data: scanDetails, isLoading: isLoadingScan } = useQuery({
        queryKey: ['scan-details', scanId],
        queryFn: () => getScanDetails(scanId),
        enabled: scanId > 0,
        refetchInterval: (query) => {
            // Refetch every 2s if scan is running
            return query.state.data?.status === 'running' ? 2000 : false;
        },
    });

    const isRunning = scanDetails?.status === 'running';

    const { data: filesData, isLoading: isLoadingFiles } = useQuery({
        queryKey: ['scan-files', scanId, page, statusFilter],
        queryFn: () => getScanFiles(scanId, page, limit, statusFilter),
        enabled: scanId > 0,
        // Auto-refresh files table every 3s when scan is running
        refetchInterval: isRunning ? 3000 : false,
    });

    // Listen for WebSocket ScanProgress events to show current file
    useEffect(() => {
        if (!lastMessage || !isRunning) return;

        if (lastMessage && typeof lastMessage === 'object' && 'type' in lastMessage) {
            const msg = lastMessage as { type: string; data: ScanProgress };
            if (msg.type === 'ScanProgress' && msg.data.scan_db_id === scanId) {
                setCurrentFile(msg.data.current_file);
                setScanProgress({
                    filesDone: msg.data.files_done,
                    totalFiles: msg.data.total_files,
                });
            } else if (msg.type === 'ScanCompleted') {
                // Clear current file indicator when scan completes
                setCurrentFile(null);
                setScanProgress(null);
                // Refresh data
                queryClient.invalidateQueries({ queryKey: ['scan-details', scanId] });
                queryClient.invalidateQueries({ queryKey: ['scan-files', scanId] });
            }
        }
    }, [lastMessage, scanId, isRunning, queryClient]);

    const handleCancel = async () => {
        setIsActionLoading(true);
        try {
            await cancelScan(String(scanId));
            toast.success('Scan cancelled');
            queryClient.invalidateQueries({ queryKey: ['scan-details', scanId] });
            queryClient.invalidateQueries({ queryKey: ['scans'] });
        } catch {
            toast.error('Failed to cancel scan');
        } finally {
            setIsActionLoading(false);
        }
    };

    const handleRescan = async () => {
        setIsActionLoading(true);
        try {
            await rescanPath(scanId);
            toast.success('Rescan started');
            queryClient.invalidateQueries({ queryKey: ['scan-details', scanId] });
            queryClient.invalidateQueries({ queryKey: ['scans'] });
        } catch {
            toast.error('Failed to start rescan');
        } finally {
            setIsActionLoading(false);
        }
    };

    const getStatusBadge = (status: string) => {
        switch (status) {
            case 'completed':
                return (
                    <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium bg-emerald-500/20 text-emerald-400 border border-emerald-500/30">
                        <FileCheck className="w-3.5 h-3.5" />
                        Completed
                    </span>
                );
            case 'running':
                return (
                    <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium bg-blue-500/20 text-blue-400 border border-blue-500/30">
                        <Loader2 className="w-3.5 h-3.5 animate-spin" />
                        Running
                        {scanProgress && (
                            <span className="ml-1 text-blue-300">
                                ({scanProgress.filesDone}/{scanProgress.totalFiles})
                            </span>
                        )}
                    </span>
                );
            case 'cancelled':
                return (
                    <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium bg-amber-500/20 text-amber-400 border border-amber-500/30">
                        <AlertCircle className="w-3.5 h-3.5" />
                        Cancelled
                    </span>
                );
            default:
                return (
                    <span className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium bg-slate-500/20 text-slate-600 dark:text-slate-400 border border-slate-500/30">
                        {status}
                    </span>
                );
        }
    };

    const getFileStatusBadge = (file: ScanFile) => {
        switch (file.status) {
            case 'healthy':
                return (
                    <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium bg-emerald-500/20 text-emerald-400">
                        <FileCheck className="w-3 h-3" />
                        Healthy
                    </span>
                );
            case 'corrupt':
            case 'error':
                return (
                    <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium bg-red-500/20 text-red-400">
                        <FileX className="w-3 h-3" />
                        Corrupt
                    </span>
                );
            case 'skipped':
                return (
                    <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium bg-amber-500/20 text-amber-400">
                        <SkipForward className="w-3 h-3" />
                        Skipped
                    </span>
                );
            case 'inaccessible':
                return (
                    <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium bg-orange-500/20 text-orange-400">
                        <ShieldAlert className="w-3 h-3" />
                        Inaccessible
                    </span>
                );
            default:
                return (
                    <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium bg-slate-500/20 text-slate-400">
                        {file.status}
                    </span>
                );
        }
    };

    if (isLoadingScan) {
        return (
            <div className="flex items-center justify-center h-64">
                <Loader2 className="w-8 h-8 text-blue-500 animate-spin" />
            </div>
        );
    }

    if (!scanDetails) {
        return (
            <div className="space-y-6">
                <button
                    onClick={() => navigate('/scans')}
                    className="flex items-center gap-2 text-slate-600 dark:text-slate-400 hover:text-slate-900 dark:text-white transition-colors"
                >
                    <ArrowLeft className="w-5 h-5" />
                    Back to Scans
                </button>
                <div className="rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900/50 p-8 text-center">
                    <AlertCircle className="w-12 h-12 text-slate-500 mx-auto mb-4" />
                    <h3 className="text-lg font-medium text-slate-900 dark:text-white mb-2">Scan Not Found</h3>
                    <p className="text-slate-600 dark:text-slate-400">The requested scan could not be found.</p>
                </div>
            </div>
        );
    }

    const hasFileData = (filesData?.pagination?.total || 0) > 0;

    return (
        <div className="space-y-6">
            {/* Header */}
            <div className="flex items-center gap-4">
                <button
                    onClick={() => navigate('/scans')}
                    className="p-2 rounded-lg bg-slate-100 dark:bg-slate-800/50 hover:bg-slate-100 dark:hover:bg-slate-800 text-slate-600 dark:text-slate-400 hover:text-slate-900 dark:text-white transition-colors cursor-pointer"
                    aria-label="Back to Scans"
                >
                    <ArrowLeft className="w-5 h-5" aria-hidden="true" />
                </button>
                <div className="flex-1">
                    <div className="flex items-center gap-3">
                        <h1 className="text-2xl font-bold text-slate-900 dark:text-white">Scan #{scanDetails.id}</h1>
                        {getStatusBadge(scanDetails.status)}
                    </div>
                    <p className="text-slate-600 dark:text-slate-400 text-sm mt-1 flex items-center gap-2">
                        <FolderOpen className="w-4 h-4" />
                        {scanDetails.path}
                    </p>
                </div>
                {/* Action Buttons */}
                <div className="flex items-center gap-2">
                    {scanDetails.status === 'running' ? (
                        <button
                            onClick={handleCancel}
                            disabled={isActionLoading}
                            className="flex items-center gap-2 px-4 py-2 rounded-lg bg-red-500/10 hover:bg-red-500/20 text-red-400 hover:text-red-300 border border-red-500/20 hover:border-red-500/30 transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
                        >
                            {isActionLoading ? (
                                <Loader2 className="w-4 h-4 animate-spin" />
                            ) : (
                                <X className="w-4 h-4" />
                            )}
                            <span className="font-medium">Cancel</span>
                        </button>
                    ) : (
                        <button
                            onClick={handleRescan}
                            disabled={isActionLoading}
                            className="flex items-center gap-2 px-4 py-2 rounded-lg bg-blue-500/10 hover:bg-blue-500/20 text-blue-400 hover:text-blue-300 border border-blue-500/20 hover:border-blue-500/30 transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
                        >
                            {isActionLoading ? (
                                <Loader2 className="w-4 h-4 animate-spin" />
                            ) : (
                                <RefreshCw className="w-4 h-4" />
                            )}
                            <span className="font-medium">Rescan</span>
                        </button>
                    )}
                </div>
            </div>

            {/* Stats Cards - File Counts */}
            <div className="grid grid-cols-2 md:grid-cols-5 gap-4">
                <div
                    onClick={() => { setStatusFilter('all'); setPage(1); }}
                    className={clsx(
                        "rounded-2xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-5 cursor-pointer transition-all hover:scale-[1.02]",
                        statusFilter === 'all' && "ring-2 ring-blue-500/50"
                    )}
                >
                    <div className="flex items-center gap-3">
                        <div className="p-2.5 rounded-xl bg-blue-500/10 border border-blue-500/20">
                            <HardDrive className="w-5 h-5 text-blue-400" />
                        </div>
                        <div>
                            <p className="text-2xl font-bold text-slate-900 dark:text-white">{scanDetails.files_scanned}</p>
                            <p className="text-xs text-slate-600 dark:text-slate-400">Files Scanned</p>
                        </div>
                    </div>
                </div>

                <div
                    onClick={() => { setStatusFilter('healthy'); setPage(1); }}
                    className={clsx(
                        "rounded-2xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-5 cursor-pointer transition-all hover:scale-[1.02]",
                        statusFilter === 'healthy' && "ring-2 ring-emerald-500/50"
                    )}
                >
                    <div className="flex items-center gap-3">
                        <div className="p-2.5 rounded-xl bg-emerald-500/10 border border-emerald-500/20">
                            <FileCheck className="w-5 h-5 text-emerald-400" />
                        </div>
                        <div>
                            <p className="text-2xl font-bold text-slate-900 dark:text-white">{scanDetails.healthy_files}</p>
                            <p className="text-xs text-slate-600 dark:text-slate-400">Healthy Files</p>
                        </div>
                    </div>
                </div>

                <div
                    onClick={() => {
                        if (scanDetails.corruptions_found > 0 && scanDetails.path_id) {
                            navigate(`/corruptions?path_id=${scanDetails.path_id}`);
                        } else {
                            setStatusFilter('corrupt');
                            setPage(1);
                        }
                    }}
                    className={clsx(
                        "rounded-2xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-5 cursor-pointer transition-all hover:scale-[1.02]",
                        statusFilter === 'corrupt' && "ring-2 ring-red-500/50"
                    )}
                    title={scanDetails.corruptions_found > 0 ? "Click to view corruptions for this scan path" : undefined}
                >
                    <div className="flex items-center gap-3">
                        <div className="p-2.5 rounded-xl bg-red-500/10 border border-red-500/20">
                            <FileX className="w-5 h-5 text-red-400" />
                        </div>
                        <div className="flex-1">
                            <div className="flex items-center gap-2">
                                <p className="text-2xl font-bold text-slate-900 dark:text-white">{scanDetails.corruptions_found}</p>
                                {scanDetails.corruptions_found > 0 && scanDetails.path_id && (
                                    <ExternalLink className="w-4 h-4 text-slate-400" />
                                )}
                            </div>
                            <p className="text-xs text-slate-600 dark:text-slate-400">Corruptions Found</p>
                        </div>
                    </div>
                </div>

                <div
                    onClick={() => { setStatusFilter('skipped'); setPage(1); }}
                    className={clsx(
                        "rounded-2xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-5 cursor-pointer transition-all hover:scale-[1.02]",
                        statusFilter === 'skipped' && "ring-2 ring-amber-500/50"
                    )}
                    title="Files skipped due to recent modification or active processing"
                >
                    <div className="flex items-center gap-3">
                        <div className="p-2.5 rounded-xl bg-amber-500/10 border border-amber-500/20">
                            <SkipForward className="w-5 h-5 text-amber-400" />
                        </div>
                        <div>
                            <p className="text-2xl font-bold text-slate-900 dark:text-white">{scanDetails.skipped_files}</p>
                            <p className="text-xs text-slate-600 dark:text-slate-400">Skipped</p>
                        </div>
                    </div>
                </div>

                <div
                    onClick={() => { setStatusFilter('inaccessible'); setPage(1); }}
                    className={clsx(
                        "rounded-2xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-5 cursor-pointer transition-all hover:scale-[1.02]",
                        statusFilter === 'inaccessible' && "ring-2 ring-orange-500/50"
                    )}
                    title="Files that could not be accessed due to permission or mount issues"
                >
                    <div className="flex items-center gap-3">
                        <div className="p-2.5 rounded-xl bg-orange-500/10 border border-orange-500/20">
                            <ShieldAlert className="w-5 h-5 text-orange-400" />
                        </div>
                        <div>
                            <p className="text-2xl font-bold text-slate-900 dark:text-white">{scanDetails.inaccessible_files}</p>
                            <p className="text-xs text-slate-600 dark:text-slate-400">Inaccessible</p>
                        </div>
                    </div>
                </div>
            </div>

            {/* Stats Cards - Time Info */}
            <div className="grid grid-cols-2 gap-4">
                <div className="rounded-2xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-5">
                    <div className="flex items-center gap-3">
                        <div className="p-2.5 rounded-xl bg-violet-500/10 border border-violet-500/20">
                            <Clock className="w-5 h-5 text-violet-400" />
                        </div>
                        <div>
                            <p className="text-sm font-medium text-slate-900 dark:text-white">
                                {formatCompact(scanDetails.started_at)}
                            </p>
                            <p className="text-xs text-slate-600 dark:text-slate-400">Started At</p>
                        </div>
                    </div>
                </div>

                <div className="rounded-2xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-5">
                    <div className="flex items-center gap-3">
                        <div className="p-2.5 rounded-xl bg-slate-500/10 border border-slate-500/20">
                            <ClockArrowDown className="w-5 h-5 text-slate-400" />
                        </div>
                        <div>
                            <p className="text-sm font-medium text-slate-900 dark:text-white">
                                {scanDetails.status === 'running' ? (
                                    <span className="text-blue-400">In Progress</span>
                                ) : scanDetails.completed_at ? (
                                    formatCompact(scanDetails.completed_at)
                                ) : (
                                    '-'
                                )}
                            </p>
                            <p className="text-xs text-slate-600 dark:text-slate-400">Ended At</p>
                        </div>
                    </div>
                </div>
            </div>

            {/* Currently Scanning Progress (only shown when scan is running) */}
            {isRunning && currentFile && (
                <div className="rounded-xl border border-blue-500/30 bg-blue-500/5 dark:bg-blue-500/10 p-4">
                    <div className="flex items-start gap-3">
                        <div className="p-2 rounded-lg bg-blue-500/20 border border-blue-500/30">
                            <Radio className="w-5 h-5 text-blue-400 animate-pulse" />
                        </div>
                        <div className="flex-1 min-w-0">
                            <div className="flex items-center justify-between mb-2">
                                <h3 className="text-sm font-medium text-blue-400">Currently Scanning</h3>
                                {scanProgress && (
                                    <span className="text-xs text-blue-300">
                                        {Math.round((scanProgress.filesDone / Math.max(scanProgress.totalFiles, 1)) * 100)}%
                                    </span>
                                )}
                            </div>
                            {scanProgress && (
                                <div className="h-1.5 bg-slate-200 dark:bg-slate-700 rounded-full overflow-hidden mb-2">
                                    <div
                                        className="h-full bg-blue-500 transition-all duration-300"
                                        style={{ width: `${(scanProgress.filesDone / Math.max(scanProgress.totalFiles, 1)) * 100}%` }}
                                    />
                                </div>
                            )}
                            <p className="text-xs text-slate-600 dark:text-slate-400 truncate font-mono" title={currentFile}>
                                {currentFile}
                            </p>
                        </div>
                    </div>
                </div>
            )}

            {/* Files Table */}
            <div className="rounded-xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900/50 overflow-hidden">
                <div className="p-4 border-b border-slate-200 dark:border-slate-800 flex items-center justify-between">
                    <h2 className="text-lg font-semibold text-slate-900 dark:text-white">Scanned Files</h2>
                    {hasFileData && (
                        <div className="flex items-center gap-2 bg-slate-100 dark:bg-slate-800/50 p-1 rounded-lg">
                            <Filter className="w-4 h-4 text-slate-600 dark:text-slate-400 ml-2" />
                            <select
                                value={statusFilter}
                                onChange={(e) => {
                                    setStatusFilter(e.target.value);
                                    setPage(1);
                                }}
                                className="bg-slate-100 dark:bg-slate-800 text-sm text-slate-700 dark:text-slate-300 border-none focus:ring-0 cursor-pointer py-1 pr-8 rounded"
                            >
                                <option value="all" className="bg-slate-100 dark:bg-slate-800">All Files</option>
                                <option value="healthy" className="bg-slate-100 dark:bg-slate-800">Healthy Only</option>
                                <option value="corrupt" className="bg-slate-100 dark:bg-slate-800">Corrupt Only</option>
                                <option value="skipped" className="bg-slate-100 dark:bg-slate-800">Skipped Only</option>
                                <option value="inaccessible" className="bg-slate-100 dark:bg-slate-800">Inaccessible Only</option>
                            </select>
                        </div>
                    )}
                </div>

                {!hasFileData ? (
                    <div className="p-8 text-center">
                        <div className="inline-flex p-4 rounded-full bg-slate-100 dark:bg-slate-800/50 mb-4">
                            {isRunning ? (
                                <Loader2 className="w-8 h-8 text-blue-500 animate-spin" />
                            ) : (
                                <HardDrive className="w-8 h-8 text-slate-500" />
                            )}
                        </div>
                        <h3 className="text-lg font-medium text-slate-900 dark:text-white mb-2">
                            {isRunning ? 'Scan In Progress' : 'No File Data Available'}
                        </h3>
                        <p className="text-slate-600 dark:text-slate-400 max-w-md mx-auto">
                            {isRunning
                                ? 'Files will appear here as they are scanned. The table updates automatically.'
                                : 'This scan was run before file-level tracking was enabled. Future scans will show individual file results here.'}
                        </p>
                    </div>
                ) : (
                    <>
                        <DataGrid
                            isLoading={isLoadingFiles}
                            data={filesData?.data || []}
                            columns={[
                                {
                                    header: 'Status',
                                    accessorKey: (row: ScanFile) => getFileStatusBadge(row),
                                    className: 'w-28'
                                },
                                {
                                    header: 'File Path',
                                    accessorKey: (row: ScanFile) => (
                                        <div className="flex flex-col max-w-xl">
                                            <span className="font-medium text-slate-700 dark:text-slate-300 truncate" title={row.file_path}>
                                                {row.file_path.split('/').pop()}
                                            </span>
                                            <span className="text-xs text-slate-500 truncate" title={row.file_path}>
                                                {row.file_path}
                                            </span>
                                            {/* Corrupt file details */}
                                            {(row.status === 'corrupt' || row.status === 'error') && row.error_details && (
                                                <div className="mt-1 text-xs text-red-400 bg-red-500/10 p-1.5 rounded border border-red-500/20">
                                                    {row.corruption_type && (
                                                        <span className="font-medium">{row.corruption_type}: </span>
                                                    )}
                                                    <span className="line-clamp-2">{row.error_details}</span>
                                                </div>
                                            )}
                                            {/* Skipped file details */}
                                            {row.status === 'skipped' && (
                                                <div className="mt-1 text-xs text-amber-400 bg-amber-500/10 p-1.5 rounded border border-amber-500/20 flex items-start gap-1.5">
                                                    <HelpCircle className="w-3 h-3 flex-shrink-0 mt-0.5" />
                                                    <div>
                                                        {row.corruption_type && (
                                                            <span className="font-medium">{row.corruption_type === 'RecentlyModified' ? 'Recently Modified' :
                                                                row.corruption_type === 'SizeChanging' ? 'Size Changing' :
                                                                row.corruption_type === 'AlreadyProcessing' ? 'Already Processing' :
                                                                row.corruption_type}: </span>
                                                        )}
                                                        <span className="line-clamp-2">{row.error_details || 'File skipped - will be checked on next scan'}</span>
                                                    </div>
                                                </div>
                                            )}
                                            {/* Inaccessible file details */}
                                            {row.status === 'inaccessible' && (
                                                <div className="mt-1 text-xs text-orange-400 bg-orange-500/10 p-1.5 rounded border border-orange-500/20 flex items-start gap-1.5">
                                                    <ShieldAlert className="w-3 h-3 flex-shrink-0 mt-0.5" />
                                                    <div>
                                                        {row.corruption_type && (
                                                            <span className="font-medium">{row.corruption_type}: </span>
                                                        )}
                                                        <span className="line-clamp-2">{row.error_details || 'File could not be accessed'}</span>
                                                    </div>
                                                </div>
                                            )}
                                        </div>
                                    )
                                },
                                {
                                    header: 'Size',
                                    accessorKey: (row: ScanFile) => (
                                        <span className="text-slate-600 dark:text-slate-400">{formatBytes(row.file_size)}</span>
                                    ),
                                    className: 'w-24'
                                },
                                {
                                    header: 'Scanned At',
                                    accessorKey: (row: ScanFile) => (
                                        <span className="text-slate-600 dark:text-slate-400 text-sm">
                                            {formatTime(row.scanned_at)}
                                        </span>
                                    ),
                                    className: 'w-28'
                                }
                            ]}
                            pagination={filesData?.pagination ? {
                                ...filesData.pagination,
                                onPageChange: setPage
                            } : undefined}
                        />
                    </>
                )}
            </div>
        </div>
    );
};

export default ScanDetails;
