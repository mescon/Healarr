
import { useNavigate, NavLink } from 'react-router-dom';
import { LayoutDashboard, Scan, AlertOctagon, Settings, Activity, Terminal, HelpCircle, LogOut, Database, Radio, Clock, Sun, Moon } from 'lucide-react';
import clsx from 'clsx';
import { useQuery } from '@tanstack/react-query';
import { getHealth } from '../../lib/api';
import { useTheme } from '../../contexts/ThemeContext';

// Format bytes to human readable
const formatBytes = (bytes: number): string => {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
};

const SystemStatus = () => {
    const { data: health, isLoading, error } = useQuery({
        queryKey: ['health'],
        queryFn: getHealth,
        refetchInterval: 30000, // Refresh every 30 seconds
    });

    if (isLoading) {
        return (
            <div className="bg-slate-100 dark:bg-slate-800/30 rounded-xl p-4 border border-slate-200 dark:border-slate-700/50">
                <div className="flex items-center justify-between mb-2">
                    <span className="text-xs text-slate-600 dark:text-slate-400">System Status</span>
                    <span className="h-2 w-2 rounded-full bg-slate-400 dark:bg-slate-600 animate-pulse"></span>
                </div>
                <div className="text-xs text-slate-500 font-mono">Loading...</div>
            </div>
        );
    }

    if (error || !health) {
        return (
            <div className="bg-slate-100 dark:bg-slate-800/30 rounded-xl p-4 border border-red-300 dark:border-red-500/20">
                <div className="flex items-center justify-between mb-2">
                    <span className="text-xs text-slate-600 dark:text-slate-400">System Status</span>
                    <span className="h-2 w-2 rounded-full bg-red-500"></span>
                </div>
                <div className="text-xs text-red-500 dark:text-red-400 font-mono">Offline</div>
            </div>
        );
    }

    const statusColor = health.status === 'healthy' ? 'green' : health.status === 'degraded' ? 'yellow' : 'red';
    const statusPingClass = statusColor === 'green' ? 'bg-green-400' : statusColor === 'yellow' ? 'bg-yellow-400' : 'bg-red-400';
    const statusDotClass = statusColor === 'green' ? 'bg-green-500' : statusColor === 'yellow' ? 'bg-yellow-500' : 'bg-red-500';

    return (
        <div className="bg-slate-100 dark:bg-slate-800/30 rounded-xl p-4 border border-slate-200 dark:border-slate-700/50 space-y-3">
            {/* Header with status */}
            <div className="flex items-center justify-between">
                <span className="text-xs text-slate-600 dark:text-slate-400 font-medium">System Status</span>
                <div className="flex items-center gap-2">
                    <span className="text-[10px] font-mono text-slate-500 capitalize">{health.status}</span>
                    <span className="flex h-2 w-2 relative">
                        <span className={`animate-ping absolute inline-flex h-2 w-2 rounded-full ${statusPingClass} opacity-75`}></span>
                        <span className={`relative inline-flex rounded-full h-2 w-2 ${statusDotClass}`}></span>
                    </span>
                </div>
            </div>

            {/* Stats grid */}
            <div className="grid grid-cols-2 gap-2">
                {/* Uptime */}
                <div className="flex items-center gap-1.5 text-[11px]">
                    <Clock className="w-3 h-3 text-slate-500" />
                    <span className="text-slate-600 dark:text-slate-400 font-mono">{health.uptime}</span>
                </div>

                {/* Database */}
                <div className="flex items-center gap-1.5 text-[11px]">
                    <Database className="w-3 h-3 text-slate-500" />
                    <span className={`font-mono ${health.database.status === 'connected' ? 'text-slate-600 dark:text-slate-400' : 'text-red-500 dark:text-red-400'}`}>
                        {health.database.size_bytes ? formatBytes(health.database.size_bytes) : health.database.status}
                    </span>
                </div>

                {/* *arr Instances */}
                <div className="flex items-center gap-1.5 text-[11px]">
                    <Radio className="w-3 h-3 text-slate-500" />
                    <span className={`font-mono ${health.arr_instances.online === health.arr_instances.total ? 'text-green-600 dark:text-green-400' : health.arr_instances.online > 0 ? 'text-yellow-600 dark:text-yellow-400' : 'text-red-500 dark:text-red-400'}`}>
                        {health.arr_instances.online}/{health.arr_instances.total} *arr
                    </span>
                </div>

                {/* Active Scans */}
                <div className="flex items-center gap-1.5 text-[11px]">
                    <Scan className="w-3 h-3 text-slate-500" />
                    <span className={`font-mono ${health.active_scans > 0 ? 'text-blue-600 dark:text-blue-400' : 'text-slate-600 dark:text-slate-400'}`}>
                        {health.active_scans} scan{health.active_scans !== 1 ? 's' : ''}
                    </span>
                </div>
            </div>
        </div>
    );
};

const Sidebar = () => {
    const navigate = useNavigate();
    const { theme, toggleTheme } = useTheme();
    const navItems = [
        { icon: LayoutDashboard, label: 'Dashboard', to: '/' },
        { icon: Scan, label: 'Scans', to: '/scans' },
        { icon: AlertOctagon, label: 'Corruptions', to: '/corruptions' },
        { icon: Terminal, label: 'Logs', to: '/logs' },
        { icon: Settings, label: 'Config', to: '/config' },
        { icon: HelpCircle, label: 'Help', to: '/help' },
    ];

    return (
        <aside className="w-64 bg-white/80 dark:bg-slate-900/50 backdrop-blur-xl border-r border-slate-200 dark:border-slate-800 h-screen flex flex-col fixed left-0 top-0 z-50">
            <div className="p-6 flex items-center gap-3 border-b border-slate-200 dark:border-slate-800/50">
                <div className="w-10 h-10 bg-gradient-to-br from-green-500 to-emerald-600 rounded-xl flex items-center justify-center shadow-lg shadow-green-500/20">
                    <Activity className="text-white w-6 h-6" />
                </div>
                <div>
                    <h1 className="text-xl font-bold bg-clip-text text-transparent bg-gradient-to-r from-slate-900 to-slate-600 dark:from-white dark:to-slate-400">
                        Healarr
                    </h1>
                    <p className="text-xs text-slate-500 font-mono">v1.0.0</p>
                </div>
            </div>

            <nav className="flex-1 p-4 space-y-2">
                {navItems.map((item) => (
                    <NavLink
                        key={item.to}
                        to={item.to}
                        className={({ isActive }) =>
                            clsx(
                                'flex items-center gap-3 px-4 py-3 rounded-xl transition-all duration-200 group',
                                isActive
                                    ? 'bg-green-500/10 text-green-600 dark:text-green-400 shadow-[0_0_20px_-5px_rgba(34,197,94,0.3)] border border-green-500/20'
                                    : 'text-slate-600 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-slate-800/50 hover:text-slate-900 dark:hover:text-slate-200 hover:border-slate-300 dark:hover:border-slate-700 border border-transparent'
                            )
                        }
                    >
                        <item.icon className="w-5 h-5" />
                        <span className="font-medium">{item.label}</span>
                    </NavLink>
                ))}
            </nav>

            <div className="p-4 border-t border-slate-200 dark:border-slate-800/50">
                <SystemStatus />

                <div className="flex gap-2 mt-4">
                    {/* Theme Toggle */}
                    <button
                        onClick={toggleTheme}
                        className="flex-1 flex items-center justify-center gap-2 px-4 py-3 rounded-xl text-slate-600 dark:text-slate-400 hover:bg-slate-100 dark:hover:bg-slate-800/50 hover:text-slate-900 dark:hover:text-slate-200 border border-transparent hover:border-slate-300 dark:hover:border-slate-700 transition-all duration-200 cursor-pointer"
                        title={theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode'}
                    >
                        {theme === 'dark' ? (
                            <Sun className="w-5 h-5" />
                        ) : (
                            <Moon className="w-5 h-5" />
                        )}
                    </button>

                    {/* Logout */}
                    <button
                        onClick={() => {
                            localStorage.removeItem('healarr_token');
                            navigate('/login');
                        }}
                        className="flex-1 flex items-center justify-center gap-2 px-4 py-3 rounded-xl text-slate-600 dark:text-slate-400 hover:bg-red-500/10 hover:text-red-600 dark:hover:text-red-400 hover:border-red-500/20 border border-transparent transition-all duration-200 cursor-pointer"
                        title="Logout"
                    >
                        <LogOut className="w-5 h-5" />
                    </button>
                </div>
            </div>
        </aside>
    );
};

export default Sidebar;
