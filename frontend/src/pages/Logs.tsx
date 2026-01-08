import { useState, useEffect, useRef, useCallback } from 'react';
import { useWebSocket } from '../contexts/WebSocketProvider';
import { useConnection } from '../contexts/ConnectionContext';
import { getRecentLogs, downloadLogs, type LogEntry } from '../lib/api';
import clsx from 'clsx';
import { format } from 'date-fns';
import { Terminal, Pause, Play, Trash2, Download, Loader2 } from 'lucide-react';
import { useToast } from '../contexts/ToastContext';

const LOGS_PER_PAGE = 100;

const Logs = () => {
    const { lastMessage, isConnected: wsConnected, reconnect: wsReconnect } = useWebSocket();
    const { isConnected: backendConnected } = useConnection();
    const [logs, setLogs] = useState<LogEntry[]>([]);
    const [isPaused, setIsPaused] = useState(false);
    const [filter, setFilter] = useState<'ALL' | 'INFO' | 'ERROR' | 'DEBUG'>('ALL');
    const [hasMore, setHasMore] = useState(false);
    const [currentOffset, setCurrentOffset] = useState(0);
    const [isLoadingMore, setIsLoadingMore] = useState(false);
    const [initialLoadDone, setInitialLoadDone] = useState(false);
    const bottomRef = useRef<HTMLDivElement>(null);
    const topRef = useRef<HTMLDivElement>(null);
    const scrollContainerRef = useRef<HTMLDivElement>(null);
    const toast = useToast();
    const prevBackendConnected = useRef(backendConnected);
    const prevWsConnected = useRef(wsConnected);

    // Load older logs when user scrolls to top
    const loadMoreLogs = useCallback(async () => {
        if (isLoadingMore || !hasMore) return;

        setIsLoadingMore(true);
        const scrollContainer = scrollContainerRef.current;
        const previousScrollHeight = scrollContainer?.scrollHeight || 0;

        try {
            // Use LOGS_PER_PAGE for offset calculation to avoid bugs when logs.length grows
            const newOffset = currentOffset + LOGS_PER_PAGE;
            const response = await getRecentLogs(LOGS_PER_PAGE, newOffset);

            if (response.entries && response.entries.length > 0) {
                // Prepend older logs
                setLogs(prev => [...response.entries, ...prev]);
                setHasMore(response.has_more);
                setCurrentOffset(newOffset);

                // Maintain scroll position after prepending
                requestAnimationFrame(() => {
                    if (scrollContainer) {
                        const newScrollHeight = scrollContainer.scrollHeight;
                        scrollContainer.scrollTop = newScrollHeight - previousScrollHeight;
                    }
                });
            }
        } catch (error) {
            console.error('Failed to load more logs:', error);
        } finally {
            setIsLoadingMore(false);
        }
    }, [isLoadingMore, hasMore, currentOffset]);

    // Intersection observer for infinite scroll
    useEffect(() => {
        if (!initialLoadDone) return;

        const observer = new IntersectionObserver(
            (entries) => {
                if (entries[0].isIntersecting && hasMore && !isLoadingMore) {
                    loadMoreLogs();
                }
            },
            { threshold: 0.1 }
        );

        if (topRef.current) {
            observer.observe(topRef.current);
        }

        return () => observer.disconnect();
    }, [initialLoadDone, hasMore, isLoadingMore, loadMoreLogs]);

    // When backend comes back online, trigger WebSocket reconnect
    useEffect(() => {
        if (backendConnected && !prevBackendConnected.current) {
            wsReconnect();
        }
        prevBackendConnected.current = backendConnected;
    }, [backendConnected, wsReconnect]);

    // When WebSocket reconnects, re-fetch recent logs to fill any gaps
    useEffect(() => {
        if (wsConnected && !prevWsConnected.current) {
            const fetchLogs = async () => {
                try {
                    const response = await getRecentLogs(LOGS_PER_PAGE, 0);
                    if (response.entries) {
                        setLogs(response.entries);
                        setHasMore(response.has_more);
                        setCurrentOffset(0);
                    }
                } catch (error) {
                    console.error('Failed to fetch recent logs after reconnect:', error);
                }
            };
            fetchLogs();
        }
        prevWsConnected.current = wsConnected;
    }, [wsConnected]);

    // Fetch recent logs on mount and scroll to bottom
    useEffect(() => {
        const fetchLogs = async () => {
            try {
                const response = await getRecentLogs(LOGS_PER_PAGE, 0);
                if (response.entries) {
                    setLogs(response.entries);
                    setHasMore(response.has_more);
                    setCurrentOffset(0);
                } else {
                    setLogs([]);
                }
                setInitialLoadDone(true);

                // Scroll to bottom after initial load
                requestAnimationFrame(() => {
                    bottomRef.current?.scrollIntoView();
                });
            } catch (error) {
                console.error('Failed to fetch recent logs:', error);
                setLogs([]);
                setInitialLoadDone(true);
            }
        };
        fetchLogs();
    }, []);

    // Handle new logs from WebSocket
    useEffect(() => {
        if (lastMessage && typeof lastMessage === 'object' && 'type' in lastMessage && !isPaused) {
            const msg = lastMessage as { type: string; data: LogEntry };
            if (msg.type === 'log') {
                setLogs(prev => [...prev, msg.data].slice(-1000));
            }
        }
    }, [lastMessage, isPaused]);

    // Auto-scroll to bottom for new logs when not paused
    useEffect(() => {
        if (!isPaused && bottomRef.current && initialLoadDone) {
            bottomRef.current.scrollIntoView({ behavior: 'smooth' });
        }
    }, [logs.length, isPaused, initialLoadDone]);

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
                        onClick={() => {
                            setLogs([]);
                            setHasMore(false);
                            setCurrentOffset(0);
                        }}
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

                <div
                    ref={scrollContainerRef}
                    className="flex-1 overflow-y-auto p-4 space-y-1 scrollbar-thin scrollbar-thumb-slate-300 dark:scrollbar-thumb-slate-800 scrollbar-track-transparent"
                >
                    {/* Top sentinel for infinite scroll */}
                    <div ref={topRef} className="h-1" />

                    {/* Loading indicator at top */}
                    {isLoadingMore && (
                        <div className="flex items-center justify-center py-2 text-slate-500">
                            <Loader2 className="w-4 h-4 animate-spin mr-2" />
                            <span className="text-xs">Loading older logs...</span>
                        </div>
                    )}

                    {/* "Load more" hint */}
                    {hasMore && !isLoadingMore && (
                        <div className="text-center py-1 text-xs text-slate-500 dark:text-slate-600">
                            ↑ Scroll up for older logs
                        </div>
                    )}

                    {filteredLogs.length === 0 ? (
                        <div className="h-full flex items-center justify-center text-slate-400 dark:text-slate-600 italic">
                            Waiting for logs...
                        </div>
                    ) : (
                        filteredLogs.map((log, idx) => (
                            <div key={`${log.timestamp}-${idx}`} className="flex gap-3 hover:bg-slate-200/50 dark:hover:bg-white/5 p-0.5 rounded -mx-2 px-2">
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
