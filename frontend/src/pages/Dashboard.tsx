import { useState, useEffect } from 'react';
import { motion } from 'framer-motion';
import { ShieldCheck, AlertOctagon, Loader2, X, Clock, AlertTriangle, EyeOff, CheckCircle2, FileSearch, TrendingUp } from 'lucide-react';
import clsx from 'clsx';
import { useQuery } from '@tanstack/react-query';
import { getDashboardStats, getActiveScans, cancelScan, type ScanProgress } from '../lib/api';
import ActivityChart from '../components/charts/ActivityChart';
import TypeDistributionChart from '../components/charts/TypeDistributionChart';
import { useWebSocket } from '../contexts/WebSocketProvider';
import { useToast } from '../contexts/ToastContext';
import { useNavigate } from 'react-router-dom';

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
const MiniStatCard = ({ title, value, icon: Icon, colorClass, onClick }: { title: string, value: number, icon: React.ElementType, colorClass: string, onClick?: () => void }) => (
    <div 
        onClick={onClick}
        className={clsx(
            "flex items-center gap-3 p-4 rounded-xl bg-slate-100 dark:bg-slate-800/30 border border-slate-200 dark:border-slate-700/30 hover:bg-slate-200 dark:hover:bg-slate-800/50 transition-colors",
            onClick && "cursor-pointer"
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

const ActiveScansTable = () => {
    const [scans, setScans] = useState<Record<string, ScanProgress>>({});
    const { lastMessage } = useWebSocket();
    const toast = useToast();

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
                            <tr key={scan.id} className="hover:bg-slate-100 dark:hover:bg-slate-800/30 transition-colors">
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
                                        onClick={() => {
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

const Dashboard = () => {
    const navigate = useNavigate();
    const { data: stats, isLoading } = useQuery({
        queryKey: ['dashboardStats'],
        queryFn: getDashboardStats,
        // Polling removed - WebSocket invalidates queries on events
    });

    if (isLoading) {
        return <div className="text-slate-900 dark:text-white">Loading dashboard...</div>;
    }

    const successRate = stats?.success_rate ?? 100;

    return (
        <div className="space-y-6">
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

            <ActiveScansTable />

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
                <div className="grid grid-cols-2 md:grid-cols-5 gap-4">
                    <MiniStatCard
                        title="Awaiting Action"
                        value={stats?.pending_corruptions ?? 0}
                        icon={Clock}
                        colorClass="text-amber-400"
                        onClick={() => navigate('/corruptions?status=pending')}
                    />
                    <MiniStatCard
                        title="Remediating"
                        value={stats?.in_progress_corruptions ?? 0}
                        icon={Loader2}
                        colorClass="text-blue-400"
                        onClick={() => navigate('/corruptions?status=in_progress')}
                    />
                    <MiniStatCard
                        title="Resolved"
                        value={stats?.resolved_corruptions ?? 0}
                        icon={CheckCircle2}
                        colorClass="text-emerald-400"
                        onClick={() => navigate('/corruptions?status=resolved')}
                    />
                    <MiniStatCard
                        title="Max Retries Reached"
                        value={stats?.orphaned_corruptions ?? 0}
                        icon={AlertTriangle}
                        colorClass="text-red-400"
                        onClick={() => navigate('/corruptions?status=orphaned')}
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
                    title="Files Scanned Today"
                    value={stats?.files_scanned_today?.toLocaleString() || "0"}
                    subtitle={`${stats?.files_scanned_week?.toLocaleString() || 0} this week`}
                    icon={FileSearch}
                    color="bg-blue-500"
                    delay={0.15}
                    onClick={() => navigate('/scans')}
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
                    value={`${successRate}%`}
                    subtitle="remediation success"
                    icon={TrendingUp}
                    color={successRate >= 90 ? "bg-emerald-500" : successRate >= 70 ? "bg-amber-500" : "bg-red-500"}
                    delay={0.3}
                />
            </div>

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
