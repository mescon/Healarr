import { useState, useEffect, useRef } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import { ShieldCheck, AlertOctagon, Loader2, X, Clock, AlertTriangle, EyeOff, CheckCircle2, FileSearch, TrendingUp, HandMetal, Play, ChevronDown, ScanSearch, PlayCircle, AlertCircle, ArrowRight } from 'lucide-react';
import clsx from 'clsx';
import { useQuery } from '@tanstack/react-query';
import { getDashboardStats, getActiveScans, cancelScan, getScanPaths, triggerScan, triggerScanAll, getPathHealth, type ScanProgress, type ScanPath } from '../lib/api';
import type { PathHealth } from '../types/api';
import { FolderOpen, FolderCheck, FolderX, FolderSearch, FolderMinus } from 'lucide-react';
import ActivityChart from '../components/charts/ActivityChart';
import TypeDistributionChart from '../components/charts/TypeDistributionChart';
import { useWebSocket } from '../contexts/WebSocketProvider';
import { useToast } from '../contexts/ToastContext';
import { useNavigate } from 'react-router-dom';
import ConfigWarningBanner from '../components/ConfigWarningBanner';

const StatCard = ({ title, value, subtitle, icon: Icon, color, delay, onClick }: { title: string, value: string, subtitle?: string, icon: React.ElementType, color: string, delay: number, onClick?: () => void }) => (
    <motion.div
        initial={{ opacity: 0, y: 20 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ delay, duration: 0.4 }}
        onClick={onClick}
        className={clsx(
            "relative overflow-hidden rounded-2xl p-6 backdrop-blur-xl border transition-all duration-300 hover:scale-[1.02]",
            "bg-white/80 dark:bg-slate-900/40 border-slate-200 dark:border-slate-800/50 hover:border-slate-300 dark:hover:border-slate-700/50 group",
            onClick && "cursor-pointer"
        )}
    >
        <div className={clsx("absolute -right-4 -top-4 w-24 h-24 rounded-full opacity-10 blur-2xl transition-opacity group-hover:opacity-20", color)} />

        <div className="flex items-start justify-between relative z-10">
            <div>
                <p className="text-sm font-medium text-slate-600 dark:text-slate-400 mb-1">{title}</p>
                <h3 className="text-3xl font-bold text-slate-900 dark:text-slate-100 tracking-tight">{value}</h3>
                {subtitle && <p className="text-xs text-slate-500 mt-1">{subtitle}</p>}
            </div>
            <div className={clsx("p-3 rounded-xl bg-slate-100 dark:bg-slate-800/50 border border-slate-200 dark:border-slate-700/50", color.replace('bg-', 'text-').replace('500', '400'))}>
                <Icon className="w-6 h-6" />
            </div>
        </div>
    </motion.div>
);

// Mini stat card for the status breakdown section
// urgent: highlights the card when value > 0 (for action-required items)
const MiniStatCard = ({ title, value, icon: Icon, colorClass, onClick, urgent }: { title: string, value: number, icon: React.ElementType, colorClass: string, onClick?: () => void, urgent?: boolean }) => {
    const isUrgent = urgent && value > 0;
    return (
        <div
            onClick={onClick}
            className={clsx(
                "flex items-center gap-3 p-4 rounded-xl border transition-colors",
                onClick && "cursor-pointer",
                isUrgent
                    ? "bg-red-500/10 dark:bg-red-500/10 border-red-500/30 dark:border-red-500/30 hover:bg-red-500/20 dark:hover:bg-red-500/20"
                    : "bg-slate-100 dark:bg-slate-800/30 border-slate-200 dark:border-slate-700/30 hover:bg-slate-200 dark:hover:bg-slate-800/50"
            )}
        >
            <div className={clsx("p-2 rounded-lg", colorClass.replace('text-', 'bg-').replace('400', '500/20'))}>
                <Icon className={clsx("w-5 h-5", colorClass)} />
            </div>
            <div>
                <p className="text-2xl font-bold text-slate-900 dark:text-white">{value}</p>
                <p className="text-xs text-slate-600 dark:text-slate-400">{title}</p>
            </div>
        </div>
    );
};

const getPathStatusIcon = (status: PathHealth['status']) => {
    switch (status) {
        case 'healthy': return FolderCheck;
        case 'warning': return FolderOpen;
        case 'critical': return FolderX;
        case 'disabled': return FolderMinus;
        default: return FolderSearch;
    }
};

const getPathStatusColor = (status: PathHealth['status']) => {
    switch (status) {
        case 'healthy': return 'text-emerald-500';
        case 'warning': return 'text-amber-500';
        case 'critical': return 'text-red-500';
        case 'disabled': return 'text-slate-400';
        default: return 'text-slate-500';
    }
};

const getPathStatusBg = (status: PathHealth['status']) => {
    switch (status) {
        case 'healthy': return 'bg-emerald-500/10 border-emerald-500/20';
        case 'warning': return 'bg-amber-500/10 border-amber-500/20';
        case 'critical': return 'bg-red-500/10 border-red-500/20';
        case 'disabled': return 'bg-slate-500/10 border-slate-500/20';
        default: return 'bg-slate-500/10 border-slate-500/20';
    }
};

const PathHealthCard = ({ path, formatTimeAgo, onClick }: { path: PathHealth; formatTimeAgo: (date: string) => string; onClick: () => void }) => {
    const Icon = getPathStatusIcon(path.status);
    const colorClass = getPathStatusColor(path.status);
    const bgClass = getPathStatusBg(path.status);
    const pathName = path.local_path.split('/').pop() || path.local_path;

    return (
        <div
            onClick={onClick}
            className={clsx(
                "flex items-center gap-3 p-3 rounded-xl border cursor-pointer transition-all hover:scale-[1.02]",
                bgClass
            )}
        >
            <div className={clsx("p-2 rounded-lg", bgClass)}>
                <Icon className={clsx("w-5 h-5", colorClass)} />
            </div>
            <div className="flex-1 min-w-0">
                <p className="text-sm font-medium text-slate-900 dark:text-white truncate" title={path.local_path}>
                    {pathName}
                </p>
                <p className="text-xs text-slate-500 dark:text-slate-400">
                    {path.last_scan_time ? formatTimeAgo(path.last_scan_time) : 'Never scanned'}
                </p>
            </div>
            <div className="text-right">
                {path.active_corruptions > 0 ? (
                    <p className="text-sm font-semibold text-red-500">{path.active_corruptions} active</p>
                ) : (
                    <p className="text-sm text-slate-500">{path.resolved_count} resolved</p>
                )}
            </div>
        </div>
    );
};

const ActiveScansTable = () => {
    const [scans, setScans] = useState<Record<string, ScanProgress>>({});
    const { lastMessage } = useWebSocket();
    const toast = useToast();
    const navigate = useNavigate();

    // Fetch active scans on mount
    useEffect(() => {
        getActiveScans().then(active => {
            const scanMap: Record<string, ScanProgress> = {};
            active.forEach(s => scanMap[s.id] = s);
            setScans(scanMap);
        });
    }, []);

    // Handle WS updates
    useEffect(() => {
        if (!lastMessage) return;

        if (lastMessage && typeof lastMessage === 'object' && 'type' in lastMessage) {
            const msg = lastMessage as { type: string; data: unknown };
            if (msg.type === 'ScanProgress') {
                const progress = msg.data as ScanProgress;
                // eslint-disable-next-line react-hooks/set-state-in-effect
                setScans(prev => ({ ...prev, [progress.id]: progress }));
            } else if (msg.type === 'ScanCompleted') {
                const { scan_id } = msg.data as { scan_id: string };
                setScans(prev => {
                    const next = { ...prev };
                    delete next[scan_id];
                    return next;
                });
            }
        }
    }, [lastMessage]);

    const activeScansList = Object.values(scans);

    if (activeScansList.length === 0) return null;

    return (
        <motion.div
            initial={{ opacity: 0, y: 20 }}
            animate={{ opacity: 1, y: 0 }}
            className="col-span-full rounded-2xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-6 mb-6"
        >
            <div className="flex items-center gap-3 mb-6">
                <div className="p-2 rounded-lg bg-blue-500/10 border border-blue-500/20">
                    <Loader2 className="w-5 h-5 text-blue-400 animate-spin" />
                </div>
                <h2 className="text-xl font-semibold text-slate-900 dark:text-white">Active Scans</h2>
            </div>

            <div className="overflow-x-auto">
                <table className="w-full">
                    <thead>
                        <tr className="border-b border-slate-200 dark:border-slate-800 text-left">
                            <th className="px-4 py-3 text-xs font-medium text-slate-600 dark:text-slate-400 uppercase">Type</th>
                            <th className="px-4 py-3 text-xs font-medium text-slate-600 dark:text-slate-400 uppercase">Path / File</th>
                            <th className="px-4 py-3 text-xs font-medium text-slate-600 dark:text-slate-400 uppercase">Status</th>
                            <th className="px-4 py-3 text-xs font-medium text-slate-600 dark:text-slate-400 uppercase w-1/3">Progress</th>
                            <th className="px-4 py-3 text-xs font-medium text-slate-600 dark:text-slate-400 uppercase w-24">Actions</th>
                        </tr>
                    </thead>
                    <tbody className="divide-y divide-slate-200 dark:divide-slate-800/50">
                        {activeScansList.map(scan => (
                            <tr
                                key={scan.id}
                                className={clsx(
                                    "hover:bg-slate-100 dark:hover:bg-slate-800/30 transition-colors",
                                    scan.scan_db_id && "cursor-pointer"
                                )}
                                onClick={() => {
                                    if (scan.scan_db_id) {
                                        navigate(`/scans/${scan.scan_db_id}`);
                                    }
                                }}
                            >
                                <td className="px-4 py-4">
                                    <span className={clsx(
                                        "px-2 py-1 rounded text-xs font-medium uppercase",
                                        scan.type === 'path' ? "bg-purple-500/10 text-purple-400" : "bg-blue-500/10 text-blue-400"
                                    )}>
                                        {scan.type}
                                    </span>
                                </td>
                                <td className="px-4 py-4">
                                    <div className="flex flex-col">
                                        <span className="text-sm text-slate-700 dark:text-slate-200 font-medium truncate max-w-md" title={scan.path}>
                                            {scan.path}
                                        </span>
                                        {scan.current_file && scan.current_file !== scan.path && (
                                            <span className="text-xs text-slate-500 truncate max-w-md font-mono mt-1" title={scan.current_file}>
                                                .../{scan.current_file.split('/').pop()}
                                            </span>
                                        )}
                                    </div>
                                </td>
                                <td className="px-4 py-4">
                                    <span className="text-sm text-slate-600 dark:text-slate-300 capitalize">{scan.status}</span>
                                </td>
                                <td className="px-4 py-4">
                                    <div className="space-y-2">
                                        <div className="flex justify-between text-xs text-slate-600 dark:text-slate-400">
                                            <span>{scan.files_done} / {scan.total_files} files</span>
                                            <span>{Math.round((scan.files_done / Math.max(scan.total_files, 1)) * 100)}%</span>
                                        </div>
                                        <div className="h-2 bg-slate-200 dark:bg-slate-800 rounded-full overflow-hidden">
                                            <motion.div
                                                className="h-full bg-blue-500"
                                                initial={{ width: 0 }}
                                                animate={{ width: `${(scan.files_done / Math.max(scan.total_files, 1)) * 100}%` }}
                                                transition={{ duration: 0.5 }}
                                            />
                                        </div>
                                    </div>
                                </td>
                                <td className="px-4 py-4">
                                    <button
                                        onClick={(e) => {
                                            e.stopPropagation(); // Prevent row click when clicking cancel
                                            cancelScan(scan.id).then(() => {
                                                // Remove from local state immediately
                                                setScans(prev => {
                                                    const next = { ...prev };
                                                    delete next[scan.id];
                                                    return next;
                                                });
                                                toast.success('Scan cancelled');
                                            }).catch((error) => {
                                                console.error('Failed to cancel scan:', error);
                                                toast.error('Failed to cancel scan');
                                            });
                                        }}
                                        className="p-1.5 rounded-md bg-red-500/10 hover:bg-red-500/20 text-red-400 hover:text-red-300 border border-red-500/20 hover:border-red-500/30 transition-colors cursor-pointer"
                                        title="Cancel Scan"
                                    >
                                        <X className="w-4 h-4" />
                                    </button>
                                </td>
                            </tr>
                        ))}
                    </tbody>
                </table>
            </div>
        </motion.div>
    );
};

// Quick Scan Dropdown Component
const QuickScanDropdown = () => {
    const [isOpen, setIsOpen] = useState(false);
    const [scanningPathId, setScanningPathId] = useState<number | null>(null);
    const [isScanningAll, setIsScanningAll] = useState(false);
    const dropdownRef = useRef<HTMLDivElement>(null);
    const toast = useToast();

    // Fetch scan paths when dropdown opens
    const { data: paths, isLoading, refetch } = useQuery({
        queryKey: ['scanPathsQuick'],
        queryFn: getScanPaths,
        enabled: false, // Only fetch when opened
        staleTime: 30000,
    });

    // Fetch paths when dropdown opens
    useEffect(() => {
        if (isOpen) {
            refetch();
        }
    }, [isOpen, refetch]);

    // Close dropdown when clicking outside
    useEffect(() => {
        const handleClickOutside = (event: MouseEvent) => {
            if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node)) {
                setIsOpen(false);
            }
        };

        if (isOpen) {
            document.addEventListener('mousedown', handleClickOutside);
        }
        return () => document.removeEventListener('mousedown', handleClickOutside);
    }, [isOpen]);

    const handleTriggerScan = async (pathId: number, pathName: string) => {
        setScanningPathId(pathId);
        try {
            await triggerScan(pathId);
            toast.success(`Scan started for ${pathName}`);
            setIsOpen(false);
        } catch (error) {
            toast.error(error);
        } finally {
            setScanningPathId(null);
        }
    };

    const handleScanAll = async () => {
        setIsScanningAll(true);
        try {
            const result = await triggerScanAll();
            if (result.started > 0) {
                toast.success(`Started ${result.started} scan${result.started > 1 ? 's' : ''}`);
            } else {
                toast.info('No paths available to scan');
            }
            setIsOpen(false);
        } catch (error) {
            toast.error(error);
        } finally {
            setIsScanningAll(false);
        }
    };

    const enabledPaths = paths?.filter((p: ScanPath) => p.enabled) || [];

    return (
        <div className="relative" ref={dropdownRef}>
            <button
                onClick={() => setIsOpen(!isOpen)}
                className={clsx(
                    "flex items-center gap-2 px-4 py-2 rounded-xl transition-all duration-200 cursor-pointer",
                    "bg-blue-500/10 hover:bg-blue-500/20 border border-blue-500/20 hover:border-blue-500/30",
                    "text-blue-600 dark:text-blue-400 font-medium text-sm",
                    isOpen && "bg-blue-500/20 border-blue-500/30"
                )}
            >
                <ScanSearch className="w-4 h-4" />
                <span>Quick Scan</span>
                <ChevronDown className={clsx("w-4 h-4 transition-transform", isOpen && "rotate-180")} />
            </button>

            <AnimatePresence>
                {isOpen && (
                    <motion.div
                        initial={{ opacity: 0, y: -10, scale: 0.95 }}
                        animate={{ opacity: 1, y: 0, scale: 1 }}
                        exit={{ opacity: 0, y: -10, scale: 0.95 }}
                        transition={{ duration: 0.15 }}
                        className="absolute right-0 top-full mt-2 w-80 rounded-xl border border-slate-200 dark:border-slate-700/50 bg-white dark:bg-slate-800 shadow-xl z-50 overflow-hidden"
                    >
                        <div className="p-3 border-b border-slate-200 dark:border-slate-700/50">
                            <h3 className="font-medium text-slate-900 dark:text-white text-sm">Start Scan</h3>
                            <p className="text-xs text-slate-500 dark:text-slate-400 mt-0.5">Select a path to scan or scan all</p>
                        </div>

                        <div className="max-h-64 overflow-y-auto">
                            {isLoading ? (
                                <div className="flex items-center justify-center py-8">
                                    <Loader2 className="w-5 h-5 animate-spin text-slate-400" />
                                </div>
                            ) : enabledPaths.length === 0 ? (
                                <div className="py-8 text-center text-sm text-slate-500 dark:text-slate-400">
                                    No scan paths configured
                                </div>
                            ) : (
                                <div className="py-1">
                                    {enabledPaths.map((path: ScanPath) => (
                                        <button
                                            key={path.id}
                                            onClick={() => handleTriggerScan(path.id, path.local_path)}
                                            disabled={scanningPathId === path.id}
                                            className="w-full flex items-center gap-3 px-3 py-2.5 hover:bg-slate-100 dark:hover:bg-slate-700/50 transition-colors text-left group cursor-pointer"
                                        >
                                            <div className="p-1.5 rounded-lg bg-slate-100 dark:bg-slate-700 group-hover:bg-blue-500/10 transition-colors">
                                                {scanningPathId === path.id ? (
                                                    <Loader2 className="w-4 h-4 animate-spin text-blue-500" />
                                                ) : (
                                                    <Play className="w-4 h-4 text-slate-500 group-hover:text-blue-500 transition-colors" />
                                                )}
                                            </div>
                                            <div className="flex-1 min-w-0">
                                                <p className="text-sm text-slate-700 dark:text-slate-200 truncate font-mono" title={path.local_path}>
                                                    {path.local_path}
                                                </p>
                                                <p className="text-xs text-slate-500 dark:text-slate-400 mt-0.5">
                                                    {path.detection_method === 'zero_byte' ? 'stat' : path.detection_method === 'handbrake' ? 'HandBrakeCLI' : (path.detection_method || 'ffprobe')} • {(path.detection_mode || 'quick') === 'quick' ? 'Quick - Header check' : 'Thorough - Full file decode'}
                                                </p>
                                            </div>
                                        </button>
                                    ))}
                                </div>
                            )}
                        </div>

                        {enabledPaths.length > 0 && (
                            <div className="p-2 border-t border-slate-200 dark:border-slate-700/50 bg-slate-50 dark:bg-slate-800/50">
                                <button
                                    onClick={handleScanAll}
                                    disabled={isScanningAll}
                                    className="w-full flex items-center justify-center gap-2 px-3 py-2.5 rounded-lg bg-blue-500 hover:bg-blue-600 text-white font-medium text-sm transition-colors disabled:opacity-50 cursor-pointer"
                                >
                                    {isScanningAll ? (
                                        <Loader2 className="w-4 h-4 animate-spin" />
                                    ) : (
                                        <PlayCircle className="w-4 h-4" />
                                    )}
                                    <span>Scan All Paths</span>
                                </button>
                            </div>
                        )}
                    </motion.div>
                )}
            </AnimatePresence>
        </div>
    );
};

const Dashboard = () => {
    const navigate = useNavigate();
    const { data: stats, isLoading } = useQuery({
        queryKey: ['dashboardStats'],
        queryFn: getDashboardStats,
        // Polling removed - WebSocket invalidates queries on events
    });

    const { data: pathHealth } = useQuery({
        queryKey: ['pathHealth'],
        queryFn: getPathHealth,
    });

    if (isLoading) {
        return <div className="text-slate-900 dark:text-white">Loading dashboard...</div>;
    }

    // Success rate is only meaningful when there are completed remediation attempts
    const hasRemediationData = (stats?.resolved_corruptions ?? 0) + (stats?.orphaned_corruptions ?? 0) > 0;
    const successRate = stats?.success_rate ?? 100;
    const successRateDisplay = hasRemediationData ? `${successRate}%` : 'N/A';

    // Format relative time for last scan (e.g., "3h ago", "2 days ago")
    const formatTimeAgo = (isoDate: string): string => {
        const date = new Date(isoDate);
        const now = new Date();
        const diffMs = now.getTime() - date.getTime();
        const diffMins = Math.floor(diffMs / 60000);
        const diffHours = Math.floor(diffMs / 3600000);
        const diffDays = Math.floor(diffMs / 86400000);

        if (diffMins < 1) return 'just now';
        if (diffMins < 60) return `${diffMins}m ago`;
        if (diffHours < 24) return `${diffHours}h ago`;
        if (diffDays === 1) return 'yesterday';
        if (diffDays < 7) return `${diffDays} days ago`;
        return date.toLocaleDateString();
    };

    // Last scan display values
    const lastScanDisplay = stats?.last_scan_time
        ? formatTimeAgo(stats.last_scan_time)
        : 'Never';
    const lastScanPath = stats?.last_scan_path
        ? stats.last_scan_path.split('/').pop() || stats.last_scan_path
        : 'No scans yet';
    const hasLastScan = !!stats?.last_scan_time;

    return (
        <div className="space-y-6">
            <ConfigWarningBanner />

            <div className="flex items-start justify-between">
                <div>
                    <motion.h1
                        initial={{ opacity: 0, x: -20 }}
                        animate={{ opacity: 1, x: 0 }}
                        className="text-3xl font-bold text-slate-900 dark:text-white mb-2"
                    >
                        System Overview
                    </motion.h1>
                    <motion.p
                        initial={{ opacity: 0, x: -20 }}
                        animate={{ opacity: 1, x: 0 }}
                        transition={{ delay: 0.1 }}
                        className="text-slate-600 dark:text-slate-400"
                    >
                        Real-time monitoring of media integrity and remediation status.
                    </motion.p>
                </div>
                <motion.div
                    initial={{ opacity: 0, x: 20 }}
                    animate={{ opacity: 1, x: 0 }}
                    transition={{ delay: 0.15 }}
                >
                    <QuickScanDropdown />
                </motion.div>
            </div>

            <ActiveScansTable />

            {/* Action Required Banner - shows when items need attention */}
            {((stats?.manual_intervention_corruptions ?? 0) > 0 || (stats?.orphaned_corruptions ?? 0) > 0) && (
                <motion.div
                    initial={{ opacity: 0, y: -10 }}
                    animate={{ opacity: 1, y: 0 }}
                    className="rounded-2xl border border-red-500/30 bg-red-500/10 dark:bg-red-500/10 p-4"
                >
                    <div className="flex items-center gap-3 mb-3">
                        <div className="p-2 rounded-lg bg-red-500/20">
                            <AlertCircle className="w-5 h-5 text-red-500" />
                        </div>
                        <h2 className="text-lg font-semibold text-red-600 dark:text-red-400">Action Required</h2>
                    </div>
                    <div className="flex flex-wrap gap-3">
                        {(stats?.manual_intervention_corruptions ?? 0) > 0 && (
                            <button
                                onClick={() => navigate('/corruptions?status=action_required')}
                                className="flex items-center gap-2 px-4 py-2 rounded-xl bg-white/50 dark:bg-slate-800/50 border border-red-500/20 hover:border-red-500/40 hover:bg-white/80 dark:hover:bg-slate-800/80 transition-colors cursor-pointer"
                            >
                                <HandMetal className="w-4 h-4 text-orange-500" />
                                <span className="text-sm text-slate-700 dark:text-slate-200">
                                    <strong>{stats?.manual_intervention_corruptions}</strong> need manual intervention
                                </span>
                                <ArrowRight className="w-4 h-4 text-slate-400" />
                            </button>
                        )}
                        {(stats?.orphaned_corruptions ?? 0) > 0 && (
                            <button
                                onClick={() => navigate('/corruptions?status=action_required')}
                                className="flex items-center gap-2 px-4 py-2 rounded-xl bg-white/50 dark:bg-slate-800/50 border border-red-500/20 hover:border-red-500/40 hover:bg-white/80 dark:hover:bg-slate-800/80 transition-colors cursor-pointer"
                            >
                                <AlertTriangle className="w-4 h-4 text-red-500" />
                                <span className="text-sm text-slate-700 dark:text-slate-200">
                                    <strong>{stats?.orphaned_corruptions}</strong> reached max retries
                                </span>
                                <ArrowRight className="w-4 h-4 text-slate-400" />
                            </button>
                        )}
                    </div>
                </motion.div>
            )}

            {/* Corruption Status Breakdown */}
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.1 }}
                className="rounded-2xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-6"
            >
                <div className="flex items-center justify-between mb-4">
                    <h2 className="text-lg font-semibold text-slate-900 dark:text-white">Corruption Status</h2>
                    <button 
                        onClick={() => navigate('/corruptions')}
                        className="text-sm text-blue-500 dark:text-blue-400 hover:text-blue-600 dark:hover:text-blue-300 transition-colors cursor-pointer"
                    >
                        View All →
                    </button>
                </div>
                <div className="grid grid-cols-2 md:grid-cols-6 gap-4">
                    <MiniStatCard
                        title="Awaiting Action"
                        value={stats?.pending_corruptions ?? 0}
                        icon={Clock}
                        colorClass="text-amber-400"
                        onClick={() => navigate('/corruptions?status=working')}
                    />
                    <MiniStatCard
                        title="Remediating"
                        value={stats?.in_progress_corruptions ?? 0}
                        icon={Loader2}
                        colorClass="text-blue-400"
                        onClick={() => navigate('/corruptions?status=working')}
                    />
                    <MiniStatCard
                        title="Manual Action"
                        value={stats?.manual_intervention_corruptions ?? 0}
                        icon={HandMetal}
                        colorClass="text-orange-400"
                        onClick={() => navigate('/corruptions?status=action_required')}
                        urgent
                    />
                    <MiniStatCard
                        title="Resolved"
                        value={stats?.resolved_corruptions ?? 0}
                        icon={CheckCircle2}
                        colorClass="text-emerald-400"
                        onClick={() => navigate('/corruptions?status=resolved')}
                    />
                    <MiniStatCard
                        title="Max Retries"
                        value={stats?.orphaned_corruptions ?? 0}
                        icon={AlertTriangle}
                        colorClass="text-red-400"
                        onClick={() => navigate('/corruptions?status=action_required')}
                        urgent
                    />
                    <MiniStatCard
                        title="Ignored"
                        value={stats?.ignored_corruptions ?? 0}
                        icon={EyeOff}
                        colorClass="text-slate-400"
                        onClick={() => navigate('/corruptions?status=ignored')}
                    />
                </div>
            </motion.div>

            {/* Main Stats Row */}
            <div className="grid grid-cols-1 md:grid-cols-4 gap-4">
                <StatCard
                    title="Last Scan"
                    value={lastScanDisplay}
                    subtitle={lastScanPath}
                    icon={FileSearch}
                    color={hasLastScan ? "bg-blue-500" : "bg-slate-500"}
                    delay={0.15}
                    onClick={() => hasLastScan && stats?.last_scan_id
                        ? navigate(`/scans/${stats.last_scan_id}`)
                        : navigate('/scans')
                    }
                />
                <StatCard
                    title="Corruptions Today"
                    value={stats?.corruptions_today?.toString() || "0"}
                    subtitle={`${stats?.total_corruptions || 0} total detected`}
                    icon={AlertOctagon}
                    color="bg-red-500"
                    delay={0.2}
                    onClick={() => navigate('/corruptions')}
                />
                <StatCard
                    title="Auto-Remediated"
                    value={stats?.successful_remediations?.toString() || "0"}
                    subtitle="total resolved"
                    icon={ShieldCheck}
                    color="bg-green-500"
                    delay={0.25}
                    onClick={() => navigate('/corruptions?status=resolved')}
                />
                <StatCard
                    title="Success Rate"
                    value={successRateDisplay}
                    subtitle={hasRemediationData ? "remediation success" : "no remediations yet"}
                    icon={TrendingUp}
                    color={hasRemediationData ? (successRate >= 90 ? "bg-emerald-500" : successRate >= 70 ? "bg-amber-500" : "bg-red-500") : "bg-slate-500"}
                    delay={0.3}
                />
            </div>

            {/* Path Health Section */}
            {pathHealth && pathHealth.length > 0 && (
                <motion.div
                    initial={{ opacity: 0, y: 20 }}
                    animate={{ opacity: 1, y: 0 }}
                    transition={{ delay: 0.35 }}
                    className="rounded-2xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-6"
                >
                    <div className="flex items-center justify-between mb-4">
                        <h2 className="text-lg font-semibold text-slate-900 dark:text-white">Library Health</h2>
                        <button
                            onClick={() => navigate('/config')}
                            className="text-sm text-blue-500 dark:text-blue-400 hover:text-blue-600 dark:hover:text-blue-300 transition-colors cursor-pointer"
                        >
                            Manage Paths →
                        </button>
                    </div>
                    <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-3">
                        {pathHealth.map((path) => (
                            <PathHealthCard
                                key={path.path_id}
                                path={path}
                                formatTimeAgo={formatTimeAgo}
                                onClick={() => path.last_scan_id
                                    ? navigate(`/scans/${path.last_scan_id}`)
                                    : navigate(`/corruptions?path_id=${path.path_id}`)
                                }
                            />
                        ))}
                    </div>
                </motion.div>
            )}

            {/* Analytics Section */}
            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
                <motion.div
                    initial={{ opacity: 0, y: 20 }}
                    animate={{ opacity: 1, y: 0 }}
                    transition={{ delay: 0.4 }}
                    className="lg:col-span-2 rounded-2xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-6"
                >
                    <h2 className="text-xl font-semibold text-slate-900 dark:text-white mb-6">Corruption Activity</h2>
                    <ActivityChart />
                </motion.div>

                <motion.div
                    initial={{ opacity: 0, y: 20 }}
                    animate={{ opacity: 1, y: 0 }}
                    transition={{ delay: 0.5 }}
                    className="rounded-2xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-6"
                >
                    <h2 className="text-xl font-semibold text-slate-900 dark:text-white mb-6">Corruption Types</h2>
                    <TypeDistributionChart />
                </motion.div>
            </div>
        </div>
    );
};

export default Dashboard;
