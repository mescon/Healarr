import { useState } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import { ArrowLeft, FileCheck, FileX, Loader2, Filter, HardDrive, Clock, FolderOpen, AlertCircle, X, RefreshCw } from 'lucide-react';
import { getScanDetails, getScanFiles, cancelScan, rescanPath, type ScanFile } from '../lib/api';
import DataGrid from '../components/ui/DataGrid';
import { useDateFormat } from '../lib/useDateFormat';
import { useToast } from '../contexts/ToastContext';

const formatFileSize = (bytes: number): string => {
    if (bytes === 0) return '0 B';
    const k = 1024;
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i];
};

const ScanDetails = () => {
    const { id } = useParams();
    const navigate = useNavigate();
    const [page, setPage] = useState(1);
    const [statusFilter, setStatusFilter] = useState<string>('all');
    const [isActionLoading, setIsActionLoading] = useState(false);
    const limit = 50;
    const { formatCompact, formatTime } = useDateFormat();
    const toast = useToast();
    const queryClient = useQueryClient();

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

    const { data: filesData, isLoading: isLoadingFiles } = useQuery({
        queryKey: ['scan-files', scanId, page, statusFilter],
        queryFn: () => getScanFiles(scanId, page, limit, statusFilter),
        enabled: scanId > 0,
    });

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
        if (file.status === 'healthy') {
            return (
                <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium bg-emerald-500/20 text-emerald-400">
                    <FileCheck className="w-3 h-3" />
                    Healthy
                </span>
            );
        }
        return (
            <span className="inline-flex items-center gap-1 px-2 py-0.5 rounded text-xs font-medium bg-red-500/20 text-red-400">
                <FileX className="w-3 h-3" />
                Corrupt
            </span>
        );
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
                >
                    <ArrowLeft className="w-5 h-5" />
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

            {/* Stats Cards */}
            <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
                <div className="rounded-2xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-5">
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

                <div className="rounded-2xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-5">
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

                <div className="rounded-2xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-5">
                    <div className="flex items-center gap-3">
                        <div className="p-2.5 rounded-xl bg-red-500/10 border border-red-500/20">
                            <FileX className="w-5 h-5 text-red-400" />
                        </div>
                        <div>
                            <p className="text-2xl font-bold text-slate-900 dark:text-white">{scanDetails.corruptions_found}</p>
                            <p className="text-xs text-slate-600 dark:text-slate-400">Corruptions Found</p>
                        </div>
                    </div>
                </div>

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
            </div>

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
                            </select>
                        </div>
                    )}
                </div>

                {!hasFileData ? (
                    <div className="p-8 text-center">
                        <div className="inline-flex p-4 rounded-full bg-slate-100 dark:bg-slate-800/50 mb-4">
                            <HardDrive className="w-8 h-8 text-slate-500" />
                        </div>
                        <h3 className="text-lg font-medium text-slate-900 dark:text-white mb-2">No File Data Available</h3>
                        <p className="text-slate-600 dark:text-slate-400 max-w-md mx-auto">
                            {scanDetails.status === 'running'
                                ? 'This scan is currently in progress. File data will appear once the scan completes.'
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
                                            {row.status === 'corrupt' && row.error_details && (
                                                <div className="mt-1 text-xs text-red-400 bg-red-500/10 p-1.5 rounded border border-red-500/20">
                                                    {row.corruption_type && (
                                                        <span className="font-medium">{row.corruption_type}: </span>
                                                    )}
                                                    <span className="line-clamp-2">{row.error_details}</span>
                                                </div>
                                            )}
                                        </div>
                                    )
                                },
                                {
                                    header: 'Size',
                                    accessorKey: (row: ScanFile) => (
                                        <span className="text-slate-600 dark:text-slate-400">{formatFileSize(row.file_size)}</span>
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
