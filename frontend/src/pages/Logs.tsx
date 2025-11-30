import { useState, useEffect, useRef } from 'react';
import { useWebSocket } from '../contexts/WebSocketProvider';
import { useConnection } from '../contexts/ConnectionContext';
import { getRecentLogs, downloadLogs, type LogEntry } from '../lib/api';
import clsx from 'clsx';
import { format } from 'date-fns';
import { Terminal, Pause, Play, Trash2, Download } from 'lucide-react';
import { useToast } from '../contexts/ToastContext';

const Logs = () => {
    const { lastMessage, isConnected: wsConnected, reconnect: wsReconnect } = useWebSocket();
    const { isConnected: backendConnected } = useConnection();
    const [logs, setLogs] = useState<LogEntry[]>([]);
    const [isPaused, setIsPaused] = useState(false);
    const [filter, setFilter] = useState<'ALL' | 'INFO' | 'ERROR' | 'DEBUG'>('ALL');
    const bottomRef = useRef<HTMLDivElement>(null);
    const toast = useToast();
    const prevBackendConnected = useRef(backendConnected);
    const prevWsConnected = useRef(wsConnected);

    // When backend comes back online, trigger WebSocket reconnect
    useEffect(() => {
        if (backendConnected && !prevBackendConnected.current) {
            // Backend just came back online, reconnect WebSocket
            wsReconnect();
        }
        prevBackendConnected.current = backendConnected;
    }, [backendConnected, wsReconnect]);

    // When WebSocket reconnects, re-fetch recent logs to fill any gaps
    useEffect(() => {
        if (wsConnected && !prevWsConnected.current) {
            // WebSocket just reconnected, fetch recent logs
            const fetchLogs = async () => {
                try {
                    const recentLogs = await getRecentLogs();
                    if (Array.isArray(recentLogs)) {
                        setLogs(recentLogs);
                    }
                } catch (error) {
                    console.error('Failed to fetch recent logs after reconnect:', error);
                }
            };
            fetchLogs();
        }
        prevWsConnected.current = wsConnected;
    }, [wsConnected]);


    // Fetch recent logs on mount
    useEffect(() => {
        const fetchLogs = async () => {
            try {
                const recentLogs = await getRecentLogs();
                // Ensure we always set an array
                if (Array.isArray(recentLogs)) {
                    setLogs(recentLogs);
                } else {
                    console.error('Recent logs is not an array:', recentLogs);
                    setLogs([]);
                }
            } catch (error) {
                console.error('Failed to fetch recent logs:', error);
                setLogs([]); // Ensure logs is always an array
            }
        };
        fetchLogs();
    }, []);

    useEffect(() => {
        if (lastMessage && typeof lastMessage === 'object' && 'type' in lastMessage && !isPaused) {
            const msg = lastMessage as { type: string; data: LogEntry };
            if (msg.type === 'log') {
                // eslint-disable-next-line react-hooks/set-state-in-effect
                setLogs(prev => [...prev, msg.data].slice(-1000)); // Keep last 1000 logs
            }
        }
    }, [lastMessage, isPaused]);

    useEffect(() => {
        if (!isPaused && bottomRef.current) {
            bottomRef.current.scrollIntoView({ behavior: 'smooth' });
        }
    }, [logs, isPaused]);

    const formatLogTime = (timestamp: string) => {
        try {
            const date = new Date(timestamp);
            if (isNaN(date.getTime())) {
                return timestamp || 'Invalid Date';
            }
            return format(date, 'HH:mm:ss.SSS');
        } catch {
            return timestamp || 'Error';
        }
    };

    const handleDownload = async () => {
        try {
            const blob = await downloadLogs();
            const url = window.URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = `healarr-logs-${new Date().toISOString().split('T')[0]}.zip`;
            document.body.appendChild(a);
            a.click();
            window.URL.revokeObjectURL(url);
            document.body.removeChild(a);
        } catch (error: unknown) {
            const e = error as Error;
            console.error('Failed to download logs:', e);
            toast.error('Failed to download logs');
        }
    };

    const filteredLogs = logs.filter(log => filter === 'ALL' || log.level === filter);

    return (
        <div className="h-[calc(100vh-100px)] flex flex-col space-y-4">
            <div className="flex justify-between items-end">
                <div>
                    <h1 className="text-3xl font-bold text-slate-900 dark:text-white mb-2 flex items-center gap-3">
                        <Terminal className="w-8 h-8 text-slate-400" />
                        Live Logs
                    </h1>
                    <p className="text-slate-600 dark:text-slate-400">Real-time server logs and events.</p>
                </div>
                <div className="flex items-center gap-2">
                    <div className="flex bg-white dark:bg-slate-900/50 p-1 rounded-lg border border-slate-200 dark:border-slate-800">
                        {['ALL', 'INFO', 'DEBUG', 'ERROR'].map((f) => (
                            <button
                                key={f}
                                onClick={() => setFilter(f as 'ALL' | 'INFO' | 'ERROR' | 'DEBUG')}
                                className={clsx(
                                    "px-3 py-1.5 rounded-md text-xs font-medium transition-all cursor-pointer",
                                    filter === f
                                        ? "bg-blue-500/20 text-blue-500 dark:text-blue-400 shadow-sm"
                                        : "text-slate-600 dark:text-slate-400 hover:text-slate-900 dark:hover:text-slate-200 hover:bg-slate-100 dark:hover:bg-slate-800"
                                )}
                            >
                                {f}
                            </button>
                        ))}
                    </div>
                    <button
                        onClick={() => setIsPaused(!isPaused)}
                        className={clsx(
                            "p-2 rounded-lg border transition-colors cursor-pointer",
                            isPaused
                                ? "bg-yellow-500/10 border-yellow-500/20 text-yellow-500 dark:text-yellow-400 hover:bg-yellow-500/20"
                                : "bg-white dark:bg-slate-900/50 border-slate-200 dark:border-slate-800 text-slate-600 dark:text-slate-400 hover:text-slate-900 dark:hover:text-white hover:bg-slate-100 dark:hover:bg-slate-800"
                        )}
                        title={isPaused ? "Resume Auto-scroll" : "Pause Auto-scroll"}
                    >
                        {isPaused ? <Play className="w-5 h-5" /> : <Pause className="w-5 h-5" />}
                    </button>
                    <button
                        onClick={handleDownload}
                        className="p-2 rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900/50 text-slate-600 dark:text-slate-400 hover:text-blue-500 dark:hover:text-blue-400 hover:bg-blue-500/10 transition-colors cursor-pointer"
                        title="Download Logs"
                    >
                        <Download className="w-5 h-5" />
                    </button>
                    <button
                        onClick={() => setLogs([])}
                        className="p-2 rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900/50 text-slate-600 dark:text-slate-400 hover:text-red-500 dark:hover:text-red-400 hover:bg-red-500/10 transition-colors cursor-pointer"
                        title="Clear Logs"
                    >
                        <Trash2 className="w-5 h-5" />
                    </button>
                </div>
            </div>

            <div className="flex-1 bg-slate-50 dark:bg-slate-950 rounded-2xl border border-slate-200 dark:border-slate-800 overflow-hidden flex flex-col font-mono text-sm relative shadow-inner">
                {!backendConnected && (
                    <div className="absolute top-0 left-0 right-0 bg-red-500/10 text-red-500 dark:text-red-400 text-xs py-1 text-center border-b border-red-500/20 z-10">
                        Backend disconnected. Waiting for server...
                    </div>
                )}
                {backendConnected && !wsConnected && (
                    <div className="absolute top-0 left-0 right-0 bg-yellow-500/10 text-yellow-600 dark:text-yellow-400 text-xs py-1 text-center border-b border-yellow-500/20 z-10">
                        Reconnecting to log stream...
                    </div>
                )}

                <div className="flex-1 overflow-y-auto p-4 space-y-1 scrollbar-thin scrollbar-thumb-slate-300 dark:scrollbar-thumb-slate-800 scrollbar-track-transparent">
                    {filteredLogs.length === 0 ? (
                        <div className="h-full flex items-center justify-center text-slate-400 dark:text-slate-600 italic">
                            Waiting for logs...
                        </div>
                    ) : (
                        filteredLogs.map((log, idx) => (
                            <div key={idx} className="flex gap-3 hover:bg-slate-200/50 dark:hover:bg-white/5 p-0.5 rounded -mx-2 px-2">
                                <span className="text-slate-500 shrink-0 select-none w-32">
                                    {formatLogTime(log.timestamp)}
                                </span>
                                <span className={clsx(
                                    "shrink-0 w-12 font-bold select-none",
                                    log.level === 'INFO' && "text-blue-500 dark:text-blue-400",
                                    log.level === 'ERROR' && "text-red-500 dark:text-red-400",
                                    log.level === 'DEBUG' && "text-slate-500",
                                )}>
                                    {log.level}
                                </span>
                                <span className="text-slate-700 dark:text-slate-300 break-all whitespace-pre-wrap">{log.message}</span>
                            </div>
                        ))
                    )}
                    <div ref={bottomRef} />
                </div>
            </div>
        </div>
    );
};

export default Logs;
