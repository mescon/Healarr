import { useState, useEffect, useRef } from 'react';
import { useLocation } from 'react-router-dom';
import { motion, AnimatePresence } from 'framer-motion';
import { Settings, Server, FolderOpen, Plus, Trash2, ChevronDown, Pencil, Save, Play, Copy, RefreshCw, Shield, Lock, Activity, Clock, Monitor, Globe, Bell, Send, Check, X, History, Wrench, Download, Upload, PlayCircle, Database, Pause, Square, RotateCcw, Folder, Info, ExternalLink, ArrowUpCircle } from 'lucide-react';
import FileBrowser from '../components/ui/FileBrowser';
import { useDateFormat, type DateFormatPreset } from '../lib/useDateFormat';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
    getArrInstances, createArrInstance, updateArrInstance, deleteArrInstance,
    getScanPaths, createScanPath, updateScanPath, deleteScanPath, triggerScan,
    getAPIKey, regenerateAPIKey, changePassword, testArrConnection,
    getSchedules, addSchedule, updateSchedule, deleteSchedule,
    getRuntimeConfig, updateSettings, restartServer,
    getNotifications, createNotification, updateNotification, deleteNotification,
    testNotification, getNotificationEvents, getNotificationLog,
    triggerScanAll, exportConfig, importConfig, downloadDatabaseBackup,
    pauseAllScans, resumeAllScans, cancelAllScans, getDetectionPreview,
    checkForUpdates,
    type ArrInstance, type ScanPath, type NotificationConfig, type NotificationLogEntry, type ConfigExport
} from '../lib/api';
import clsx from 'clsx';
import { useToast } from '../contexts/ToastContext';
import ConfigWarningBanner from '../components/ConfigWarningBanner';

const ServerStatus = ({ url, apiKey, isManuallyTesting }: { url: string; apiKey: string; isManuallyTesting?: boolean }) => {
    const { data, isLoading, isError, isFetching } = useQuery({
        queryKey: ['serverStatus', url, apiKey],
        queryFn: () => testArrConnection(url, apiKey),
        retry: false,
        refetchInterval: 600000, // Check every 10 minutes (was 1 minute)
        refetchOnWindowFocus: true, // Refresh when user returns to page
        staleTime: 60000, // Show cached result for 1 minute before showing "Checking..."
    });

    if (isLoading || isFetching || isManuallyTesting) {
        return (
            <div className="flex items-center gap-2">
                <div className="w-2 h-2 rounded-full bg-yellow-500 animate-pulse" />
                <span className="text-sm text-slate-600 dark:text-slate-400">Checking...</span>
            </div>
        );
    }

    if (isError || !data?.success) {
        return (
            <div className="flex items-center gap-2" title={data?.error || 'Connection failed'}>
                <div className="w-2 h-2 rounded-full bg-red-500" />
                <span className="text-sm text-red-400">Offline</span>
            </div>
        );
    }

    return (
        <div className="flex items-center gap-2">
            <div className="w-2 h-2 rounded-full bg-green-500" />
            <span className="text-sm text-green-400">Online</span>
        </div>
    );
};

// Quick Actions Section Component
interface QuickActionsSectionProps {
    toast: ReturnType<typeof useToast>;
}

const QuickActionsSection = ({ toast }: QuickActionsSectionProps) => {
    const scanAllMutation = useMutation({
        mutationFn: triggerScanAll,
        onSuccess: (data) => {
            toast.success(`Started ${data.started} scan(s), skipped ${data.skipped} already running`);
        },
        onError: (error: unknown) => {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to start scans: ${err.response?.data?.error || err.message}`);
        }
    });

    const pauseAllMutation = useMutation({
        mutationFn: pauseAllScans,
        onSuccess: (data) => {
            toast.success(`Paused ${data.paused} scan(s)`);
        },
        onError: (error: unknown) => {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to pause scans: ${err.response?.data?.error || err.message}`);
        }
    });

    const resumeAllMutation = useMutation({
        mutationFn: resumeAllScans,
        onSuccess: (data) => {
            toast.success(`Resumed ${data.resumed} scan(s)`);
        },
        onError: (error: unknown) => {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to resume scans: ${err.response?.data?.error || err.message}`);
        }
    });

    const cancelAllMutation = useMutation({
        mutationFn: cancelAllScans,
        onSuccess: (data) => {
            toast.success(`Cancelled ${data.cancelled} scan(s)`);
        },
        onError: (error: unknown) => {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to cancel scans: ${err.response?.data?.error || err.message}`);
        }
    });

    return (
        <motion.div
            initial={{ opacity: 0, y: 20 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ delay: 0.05 }}
        >
            <div className="rounded-xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-4">
                <div className="flex items-center justify-between flex-wrap gap-4">
                    <div className="flex items-center gap-3">
                        <div className="p-2 rounded-lg bg-cyan-500/10 border border-cyan-500/20">
                            <PlayCircle className="w-5 h-5 text-cyan-400" />
                        </div>
                        <div>
                            <h3 className="text-sm font-semibold text-slate-900 dark:text-white">Quick Actions</h3>
                            <p className="text-xs text-slate-500">Scan controls</p>
                        </div>
                    </div>

                    <div className="flex items-center gap-3 flex-wrap">
                        <button
                            onClick={() => scanAllMutation.mutate()}
                            disabled={scanAllMutation.isPending}
                            className="flex items-center gap-2 px-4 py-2 bg-green-500/10 hover:bg-green-500/20 text-green-400 rounded-lg transition-colors border border-green-500/20 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
                        >
                            <Play className="w-4 h-4" />
                            {scanAllMutation.isPending ? 'Starting...' : 'Scan All Paths'}
                        </button>

                        <button
                            onClick={() => pauseAllMutation.mutate()}
                            disabled={pauseAllMutation.isPending}
                            className="flex items-center gap-2 px-4 py-2 bg-yellow-500/10 hover:bg-yellow-500/20 text-yellow-400 rounded-lg transition-colors border border-yellow-500/20 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
                        >
                            <Pause className="w-4 h-4" />
                            {pauseAllMutation.isPending ? 'Pausing...' : 'Pause All'}
                        </button>

                        <button
                            onClick={() => resumeAllMutation.mutate()}
                            disabled={resumeAllMutation.isPending}
                            className="flex items-center gap-2 px-4 py-2 bg-blue-500/10 hover:bg-blue-500/20 text-blue-400 rounded-lg transition-colors border border-blue-500/20 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
                        >
                            <RotateCcw className="w-4 h-4" />
                            {resumeAllMutation.isPending ? 'Resuming...' : 'Resume All'}
                        </button>

                        <button
                            onClick={() => cancelAllMutation.mutate()}
                            disabled={cancelAllMutation.isPending}
                            className="flex items-center gap-2 px-4 py-2 bg-red-500/10 hover:bg-red-500/20 text-red-400 rounded-lg transition-colors border border-red-500/20 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
                        >
                            <Square className="w-4 h-4" />
                            {cancelAllMutation.isPending ? 'Cancelling...' : 'Cancel All'}
                        </button>
                    </div>
                </div>
            </div>
        </motion.div>
    );
};

// Data Management Section Component (for Advanced accordion)
interface DataManagementSectionProps {
    toast: ReturnType<typeof useToast>;
    queryClient: ReturnType<typeof useQueryClient>;
}

const DataManagementSection = ({ toast, queryClient }: DataManagementSectionProps) => {
    const fileInputRef = useRef<HTMLInputElement>(null);
    const [isDownloadingBackup, setIsDownloadingBackup] = useState(false);

    const handleExport = async () => {
        try {
            const config = await exportConfig();
            const blob = new Blob([JSON.stringify(config, null, 2)], { type: 'application/json' });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = `healarr-config-${new Date().toISOString().split('T')[0]}.json`;
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            URL.revokeObjectURL(url);
            toast.success('Configuration exported successfully');
        } catch (error: unknown) {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to export config: ${err.response?.data?.error || err.message}`);
        }
    };

    const handleImportClick = () => {
        fileInputRef.current?.click();
    };

    const handleDownloadBackup = async () => {
        setIsDownloadingBackup(true);
        try {
            await downloadDatabaseBackup();
            toast.success('Database backup downloaded successfully');
        } catch (error: unknown) {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to download backup: ${err.response?.data?.error || err.message}`);
        } finally {
            setIsDownloadingBackup(false);
        }
    };

    const handleImportFile = async (e: React.ChangeEvent<HTMLInputElement>) => {
        const file = e.target.files?.[0];
        if (!file) return;

        try {
            const text = await file.text();
            const config: ConfigExport = JSON.parse(text);

            // Validate it looks like a Healarr config
            if (!config.version || (!config.arr_instances && !config.scan_paths)) {
                throw new Error('Invalid configuration file');
            }

            // Ask for confirmation
            const arrCount = config.arr_instances?.length || 0;
            const pathCount = config.scan_paths?.length || 0;
            if (!confirm(`Import ${arrCount} *arr instance(s) and ${pathCount} scan path(s)?\n\nNote: This will ADD to your existing configuration, not replace it.`)) {
                return;
            }

            const result = await importConfig(config);
            toast.success(`Imported ${result.imported.arr_instances} instance(s) and ${result.imported.scan_paths} path(s)`);

            // Refresh data
            queryClient.invalidateQueries({ queryKey: ['arrInstances'] });
            queryClient.invalidateQueries({ queryKey: ['scanPaths'] });
        } catch (error: unknown) {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to import config: ${err.response?.data?.error || err.message || 'Invalid file'}`);
        }

        // Reset input
        e.target.value = '';
    };

    return (
        <div className="space-y-4">
            <div className="flex items-center gap-3 mb-4">
                <div className="p-2 rounded-lg bg-amber-500/10 border border-amber-500/20">
                    <Database className="w-5 h-5 text-amber-400" />
                </div>
                <div>
                    <h4 className="text-sm font-semibold text-slate-900 dark:text-white">Data Management</h4>
                    <p className="text-xs text-slate-500">Export, import, and backup your configuration</p>
                </div>
            </div>

            <div className="flex items-center gap-3 flex-wrap">
                <button
                    onClick={handleExport}
                    className="flex items-center gap-2 px-4 py-2 bg-blue-500/10 hover:bg-blue-500/20 text-blue-400 rounded-lg transition-colors border border-blue-500/20 cursor-pointer"
                >
                    <Download className="w-4 h-4" />
                    Export Config
                </button>

                <button
                    onClick={handleImportClick}
                    className="flex items-center gap-2 px-4 py-2 bg-purple-500/10 hover:bg-purple-500/20 text-purple-400 rounded-lg transition-colors border border-purple-500/20 cursor-pointer"
                >
                    <Upload className="w-4 h-4" />
                    Import Config
                </button>

                <button
                    onClick={handleDownloadBackup}
                    disabled={isDownloadingBackup}
                    className="flex items-center gap-2 px-4 py-2 bg-amber-500/10 hover:bg-amber-500/20 text-amber-400 rounded-lg transition-colors border border-amber-500/20 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
                >
                    <Database className="w-4 h-4" />
                    {isDownloadingBackup ? 'Downloading...' : 'Download DB Backup'}
                </button>

                <input
                    type="file"
                    ref={fileInputRef}
                    onChange={handleImportFile}
                    accept=".json"
                    className="hidden"
                />
            </div>
        </div>
    );
};

const Config = () => {
    const queryClient = useQueryClient();
    const toast = useToast();
    const location = useLocation();
    const { preset: dateFormatPreset, setDateFormatPreset } = useDateFormat();
    const aboutSectionRef = useRef<HTMLDivElement>(null);

    // Collapsible state
    const [isAddArrExpanded, setIsAddArrExpanded] = useState(false);
    const [isAddPathExpanded, setIsAddPathExpanded] = useState(false);
    const [isAddScheduleExpanded, setIsAddScheduleExpanded] = useState(false);
    const [isAdvancedExpanded, setIsAdvancedExpanded] = useState(false);
    const [isAboutExpanded, setIsAboutExpanded] = useState(false);

    // Handle hash navigation (e.g., /config#about)
    useEffect(() => {
        if (location.hash === '#about') {
            setIsAboutExpanded(true);
            // Small delay to ensure the section is rendered before scrolling
            setTimeout(() => {
                aboutSectionRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
            }, 100);
        }
    }, [location.hash]);

    // --- Queries ---
    const { data: runtimeConfig } = useQuery({
        queryKey: ['runtimeConfig'],
        queryFn: getRuntimeConfig,
        staleTime: Infinity, // Runtime config doesn't change without restart
    });

    const { data: apiKeyData, refetch: refetchApiKey } = useQuery({
        queryKey: ['apiKey'],
        queryFn: getAPIKey,
    });

    const { data: arrInstances, isLoading: isLoadingArr } = useQuery({
        queryKey: ['arrInstances'],
        queryFn: getArrInstances,
    });

    const { data: scanPaths, isLoading: isLoadingPaths } = useQuery({
        queryKey: ['scanPaths'],
        queryFn: getScanPaths,
    });

    const { data: schedules, isLoading: isLoadingSchedules } = useQuery({
        queryKey: ['schedules'],
        queryFn: getSchedules,
    });

    // --- Mutations ---
    const createArrMutation = useMutation({
        mutationFn: createArrInstance,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['arrInstances'] });
            toast.success('Server added successfully');
            // Debounce status refresh to avoid rapid-fire requests
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: ['serverStatus'] });
            }, 500);
        },
        onError: (error: Error) => {
            toast.error(`Failed to add server: ${error.message}`);
        },
    });

    const updateArrMutation = useMutation({
        mutationFn: ({ id, data }: { id: number; data: Omit<ArrInstance, 'id'> }) =>
            updateArrInstance(id, data),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['arrInstances'] });
            toast.success('Server updated successfully');
            // Debounce status refresh to avoid rapid-fire requests
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: ['serverStatus'] });
            }, 500);
        },
        onError: (error: Error) => {
            toast.error(`Failed to update server: ${error.message}`);
        },
    });

    const deleteArrMutation = useMutation({
        mutationFn: deleteArrInstance,
        onSuccess: () => queryClient.invalidateQueries({ queryKey: ['arrInstances'] }),
    });

    const createPathMutation = useMutation({
        mutationFn: createScanPath,
        onSuccess: () => queryClient.invalidateQueries({ queryKey: ['scanPaths'] }),
    });

    const updatePathMutation = useMutation({
        mutationFn: ({ id, data }: { id: number; data: Omit<ScanPath, 'id'> }) =>
            updateScanPath(id, data),
        onSuccess: () => queryClient.invalidateQueries({ queryKey: ['scanPaths'] }),
    });

    const deletePathMutation = useMutation({
        mutationFn: deleteScanPath,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['scanPaths'] });
        },
    });

    const scanPathMutation = useMutation({
        mutationFn: triggerScan,
        onSuccess: () => {
            // Optionally show a toast notification here
        },
        onError: (error: unknown) => {
            const err = error as { response?: { status: number; data?: { error?: string } }; message?: string };
            if (err.response?.status === 409) {
                toast.warning('A scan is already in progress for this path. Please wait for it to complete or cancel it first.');
            } else {
                toast.error(`Failed to start scan: ${err.response?.data?.error || err.message}`);
            }
        },
    });

    const addScheduleMutation = useMutation({
        mutationFn: addSchedule,
        onSuccess: () => queryClient.invalidateQueries({ queryKey: ['schedules'] }),
    });

    const updateScheduleMutation = useMutation({
        mutationFn: ({ id, schedule }: { id: number; schedule: { cron_expression?: string; enabled?: boolean } }) =>
            updateSchedule(id, schedule),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['schedules'] });
        },
        onError: (error: unknown) => {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to update schedule: ${err.response?.data?.error || err.message}`);
        }
    });

    const deleteScheduleMutation = useMutation({
        mutationFn: deleteSchedule,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['schedules'] });
        },
        onError: (error: unknown) => {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to delete schedule: ${err.response?.data?.error || err.message}`);
        }
    });
    // --- Forms State ---
    const [editingArrId, setEditingArrId] = useState<number | null>(null);
    const [editingPathId, setEditingPathId] = useState<number | null>(null);
    const [newArr, setNewArr] = useState<Partial<ArrInstance>>({ type: 'sonarr', enabled: true });
    const [newPath, setNewPath] = useState<Partial<ScanPath>>({
        enabled: true,
        auto_remediate: true,
        detection_method: 'ffprobe',
        detection_mode: 'quick',
        max_retries: 3,
        verification_timeout_hours: null
    });
    const [showFileBrowser, setShowFileBrowser] = useState(false);
    const [fileBrowserTarget, setFileBrowserTarget] = useState<'new' | number>('new');

    // Detection preview - fetched when method, mode, or args change
    const { data: detectionPreview, isLoading: isLoadingPreview } = useQuery({
        queryKey: ['detectionPreview', newPath.detection_method || 'ffprobe', newPath.detection_mode || 'quick', newPath.detection_args || ''],
        queryFn: () => getDetectionPreview(
            newPath.detection_method || 'ffprobe',
            newPath.detection_mode || 'quick',
            newPath.detection_args || undefined
        ),
        staleTime: 60000, // Cache for 1 minute
    });

    const [newSchedule, setNewSchedule] = useState<{ scan_path_id: number; cron_expression: string }>({
        scan_path_id: 0,
        cron_expression: '0 3 * * *' // Default to 3 AM daily
    });
    const [schedulePreset, setSchedulePreset] = useState('daily');

    const handlePresetChange = (preset: string) => {
        setSchedulePreset(preset);
        switch (preset) {
            case 'hourly':
                setNewSchedule(prev => ({ ...prev, cron_expression: '0 * * * *' }));
                break;
            case 'every_6h':
                setNewSchedule(prev => ({ ...prev, cron_expression: '0 */6 * * *' }));
                break;
            case 'every_12h':
                setNewSchedule(prev => ({ ...prev, cron_expression: '0 */12 * * *' }));
                break;
            case 'daily':
                setNewSchedule(prev => ({ ...prev, cron_expression: '0 3 * * *' }));
                break;
            case 'weekly':
                setNewSchedule(prev => ({ ...prev, cron_expression: '0 3 * * 0' }));
                break;
            case 'custom':
                // Keep current or clear it
                break;
        }
    };
    const [testStatus, setTestStatus] = useState<{ success?: boolean; message?: string }>({});
    const [isTesting, setIsTesting] = useState(false);
    const [manualTestingServer, setManualTestingServer] = useState<string | null>(null);

    const handleTestConnection = async () => {
        if (!newArr.url || !newArr.api_key) {
            setTestStatus({ success: false, message: 'URL and API Key required' });
            return;
        }
        setIsTesting(true);
        try {
            const result = await testArrConnection(newArr.url, newArr.api_key);
            setTestStatus({
                success: result.success,
                message: result.success ? 'Connection successful' : result.error
            });
        } catch {
            setTestStatus({ success: false, message: 'Connection failed' });
        } finally {
            setIsTesting(false);
        }
    };

    const handleManualTest = async (arr: ArrInstance) => {
        // Set manual testing state for immediate UI feedback
        const testKey = `${arr.url}-${arr.api_key}`;
        setManualTestingServer(testKey);

        try {
            // Add a minimum delay so users can see the "Checking..." state
            await Promise.all([
                queryClient.refetchQueries({ queryKey: ['serverStatus', arr.url, arr.api_key], exact: true }),
                new Promise(resolve => setTimeout(resolve, 1000)) // Minimum 1 second
            ]);
        } finally {
            // Small delay before clearing to ensure the final state is visible
            setTimeout(() => setManualTestingServer(null), 100);
        }
    };

    const handleCreateArr = (e: React.FormEvent) => {
        e.preventDefault();

        // Validate required fields with user feedback
        const missingFields: string[] = [];
        if (!newArr.name?.trim()) missingFields.push('Name');
        if (!newArr.url?.trim()) missingFields.push('URL');
        if (!newArr.api_key?.trim()) missingFields.push('API Key');
        if (!newArr.type) missingFields.push('Type');

        if (missingFields.length > 0) {
            toast.error(`Please fill in required fields: ${missingFields.join(', ')}`);
            return;
        }

        if (editingArrId) {
            updateArrMutation.mutate({ id: editingArrId, data: newArr as Omit<ArrInstance, 'id'> });
            setEditingArrId(null);
        } else {
            createArrMutation.mutate(newArr as Omit<ArrInstance, 'id'>);
        }
        setNewArr({ type: 'sonarr', enabled: true, name: '', url: '', api_key: '' });
        setTestStatus({});
        setIsAddArrExpanded(false);
    };

    const handleCreatePath = (e: React.FormEvent) => {
        e.preventDefault();
        if (newPath.local_path && newPath.arr_instance_id) {
            // Convert comma-separated args to array
            const formData = { ...newPath };
            if (formData.detection_args && typeof formData.detection_args === 'string') {
                formData.detection_args = (formData.detection_args as string)
                    .split(',')
                    .map(arg => arg.trim())
                    .filter(arg => arg.length > 0) as unknown as string;
            }

            if (editingPathId) {
                updatePathMutation.mutate({ id: editingPathId, data: formData as Omit<ScanPath, 'id'> });
                setEditingPathId(null);
            } else {
                createPathMutation.mutate(formData as Omit<ScanPath, 'id'>);
            }
            setNewPath({
                enabled: true,
                auto_remediate: true,
                local_path: '',
                arr_path: '',
                arr_instance_id: null,
                detection_method: 'ffprobe',
                detection_mode: 'quick'
            });
            setIsAddPathExpanded(false);
        }
    };

    const handleCreateSchedule = (e: React.FormEvent) => {
        e.preventDefault();
        if (newSchedule.scan_path_id && newSchedule.cron_expression) {
            addScheduleMutation.mutate(newSchedule);
            setNewSchedule({ scan_path_id: 0, cron_expression: '0 3 * * *' });
            setIsAddScheduleExpanded(false);
        }
    };

    const handleToggleSchedule = (schedule: { id: number; enabled: boolean }) => {
        updateScheduleMutation.mutate({
            id: schedule.id,
            schedule: { enabled: !schedule.enabled }
        });
    };

    const handleEditArr = (arr: ArrInstance) => {
        setNewArr({
            name: arr.name,
            type: arr.type,
            url: arr.url,
            api_key: arr.api_key,
            enabled: arr.enabled
        });
        setEditingArrId(arr.id);
        setIsAddArrExpanded(true);
    };

    const handleEditPath = (path: ScanPath) => {
        // Convert JSON array string to comma-separated string for editing
        let detectionArgsStr = '';
        if (path.detection_args) {
            try {
                const argsArray = JSON.parse(path.detection_args);
                if (Array.isArray(argsArray)) {
                    detectionArgsStr = argsArray.join(', ');
                }
            } catch {
                detectionArgsStr = path.detection_args;
            }
        }

        setNewPath({
            local_path: path.local_path,
            arr_path: path.arr_path,
            arr_instance_id: path.arr_instance_id,
            enabled: path.enabled,
            auto_remediate: path.auto_remediate,
            detection_method: path.detection_method || 'ffprobe',
            detection_mode: path.detection_mode || 'quick',
            detection_args: detectionArgsStr,
            max_retries: path.max_retries ?? 3,
            verification_timeout_hours: path.verification_timeout_hours ?? null
        });
        setEditingPathId(path.id);
        setIsAddPathExpanded(true);
    };

    const handleCancelEdit = () => {
        setEditingArrId(null);
        setEditingPathId(null);
        setNewArr({ type: 'sonarr', enabled: true, name: '', url: '', api_key: '' });
        setTestStatus({});
        setNewPath({
            enabled: true,
            auto_remediate: true,
            local_path: '',
            arr_path: '',
            arr_instance_id: null,
            detection_method: 'ffprobe',
            detection_mode: 'quick',
            max_retries: 3,
            verification_timeout_hours: null
        });
    };

    return (
        <div className="space-y-8">
            <ConfigWarningBanner />

            <div>
                <h1 className="text-3xl font-bold text-slate-900 dark:text-white mb-2 flex items-center gap-3">
                    <Settings className="w-8 h-8 text-slate-600 dark:text-slate-400" />
                    Configuration
                </h1>
                <p className="text-slate-600 dark:text-slate-400">Manage media servers and scan paths.</p>
            </div>

            {/* Quick Actions */}
            <QuickActionsSection toast={toast} />

            {/* *arr Servers Section */}
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.1 }}
                className="space-y-4"
            >
                <div className="flex items-center gap-3 mb-6">
                    <Server className="w-6 h-6 text-blue-400" />
                    <h2 className="text-2xl font-semibold text-slate-900 dark:text-white">*arr Servers</h2>
                </div>

                {/* Add *arr Server Form */}
                <div className="rounded-xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl overflow-hidden">
                    <button
                        onClick={() => {
                            if (!isAddArrExpanded && editingArrId) {
                                handleCancelEdit();
                            }
                            setIsAddArrExpanded(!isAddArrExpanded);
                        }}
                        className="w-full px-6 py-4 flex items-center justify-between hover:bg-slate-100 dark:hover:bg-slate-800/30 transition-colors cursor-pointer"
                    >
                        <div className="flex items-center gap-3">
                            {editingArrId ? (
                                <>
                                    <Pencil className="w-5 h-5 text-yellow-400" />
                                    <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Edit Server</h3>
                                </>
                            ) : (
                                <>
                                    <Plus className="w-5 h-5 text-blue-400" />
                                    <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Add Server</h3>
                                </>
                            )}
                        </div>
                        <ChevronDown className={clsx(
                            "w-5 h-5 text-slate-600 dark:text-slate-400 transition-transform duration-200",
                            isAddArrExpanded && "rotate-180"
                        )} />
                    </button>

                    <AnimatePresence>
                        {isAddArrExpanded && (
                            <motion.div
                                initial={{ height: 0, opacity: 0 }}
                                animate={{ height: "auto", opacity: 1 }}
                                exit={{ height: 0, opacity: 0 }}
                                transition={{ duration: 0.2, ease: "easeInOut" }}
                            >
                                <form onSubmit={handleCreateArr} className="px-6 pb-6 space-y-4 border-t border-slate-200 dark:border-slate-800/50 pt-4">
                                    <div className="grid grid-cols-2 gap-4">
                                        <div>
                                            <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">Type</label>
                                            <select
                                                value={newArr.type || 'sonarr'}
                                                onChange={e => setNewArr({ ...newArr, type: e.target.value as 'sonarr' | 'radarr' | 'whisparr-v2' | 'whisparr-v3' })}
                                                className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white focus:ring-2 focus:ring-blue-500"
                                            >
                                                <option value="sonarr">Sonarr</option>
                                                <option value="radarr">Radarr</option>
                                                <option value="whisparr-v2">Whisparr v2 (Sonarr-based)</option>
                                                <option value="whisparr-v3">Whisparr v3 (Radarr-based)</option>
                                            </select>
                                        </div>
                                        <div>
                                            <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">Name <span className="text-red-400">*</span></label>
                                            <input
                                                type="text"
                                                required
                                                value={newArr.name || ''}
                                                onChange={e => setNewArr({ ...newArr, name: e.target.value })}
                                                placeholder="My Sonarr"
                                                className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white placeholder-slate-500 focus:ring-2 focus:ring-blue-500"
                                            />
                                        </div>
                                        <div>
                                            <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">URL <span className="text-red-400">*</span></label>
                                            <input
                                                type="url"
                                                required
                                                value={newArr.url || ''}
                                                onChange={e => setNewArr({ ...newArr, url: e.target.value })}
                                                placeholder="http://localhost:8989"
                                                className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white placeholder-slate-500 focus:ring-2 focus:ring-blue-500"
                                            />
                                        </div>
                                        <div>
                                            <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">API Key <span className="text-red-400">*</span></label>
                                            <input
                                                type="text"
                                                required
                                                value={newArr.api_key || ''}
                                                onChange={e => setNewArr({ ...newArr, api_key: e.target.value })}
                                                placeholder="API key"
                                                className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white placeholder-slate-500 focus:ring-2 focus:ring-blue-500"
                                            />
                                            <p className="mt-1 text-xs text-slate-500">Find in *arr Settings → General. Required for webhooks even if local auth is disabled.</p>
                                        </div>
                                    </div>
                                    <div className="flex items-center gap-3 pb-2">
                                        <input
                                            type="checkbox"
                                            id="arr-enabled"
                                            checked={newArr.enabled || false}
                                            onChange={e => setNewArr({ ...newArr, enabled: e.target.checked })}
                                            className="w-4 h-4 text-blue-500 bg-white dark:bg-slate-800 border-slate-300 dark:border-slate-700 rounded focus:ring-blue-500"
                                        />
                                        <label htmlFor="arr-enabled" className="text-sm text-slate-700 dark:text-slate-300">Enabled</label>
                                    </div>
                                    <div className="flex items-center justify-between">
                                        <div className="flex items-center gap-3">
                                            <button
                                                type="button"
                                                onClick={handleTestConnection}
                                                disabled={isTesting || !newArr.url || !newArr.api_key}
                                                className="px-4 py-2 bg-slate-200 dark:bg-slate-800 hover:bg-slate-300 dark:hover:bg-slate-700 text-slate-700 dark:text-slate-300 rounded-lg transition-colors disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
                                            >
                                                {isTesting ? 'Testing...' : 'Test Connection'}
                                            </button>
                                            {testStatus.message && (
                                                <span className={clsx(
                                                    "text-sm",
                                                    testStatus.success ? "text-green-400" : "text-red-400"
                                                )}>
                                                    {testStatus.message}
                                                </span>
                                            )}
                                        </div>
                                        <button
                                            type="submit"
                                            className="flex items-center gap-2 px-4 py-2 bg-blue-500 hover:bg-blue-600 text-slate-900 dark:text-white rounded-lg transition-colors cursor-pointer"
                                        >
                                            {editingArrId ? (
                                                <>
                                                    <Save className="w-4 h-4" />
                                                    Update Server
                                                </>
                                            ) : (
                                                <>
                                                    <Plus className="w-4 h-4" />
                                                    Add Server
                                                </>
                                            )}
                                        </button>
                                    </div>
                                </form>
                            </motion.div>
                        )}
                    </AnimatePresence>
                </div>

                {/* *arr Servers List */}
                <div className="rounded-xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl overflow-hidden">
                    {isLoadingArr ? (
                        <div className="p-8 text-center text-slate-600 dark:text-slate-400">Loading servers...</div>
                    ) : arrInstances && arrInstances.length > 0 ? (
                        <div className="overflow-x-auto">
                            <table className="w-full">
                                <thead>
                                    <tr className="border-b border-slate-200 dark:border-slate-800">
                                        <th className="px-6 py-3 text-left text-xs font-medium text-slate-600 dark:text-slate-400 uppercase">Type</th>
                                        <th className="px-6 py-3 text-left text-xs font-medium text-slate-600 dark:text-slate-400 uppercase">Name</th>
                                        <th className="px-6 py-3 text-left text-xs font-medium text-slate-600 dark:text-slate-400 uppercase">URL</th>
                                        <th className="px-6 py-3 text-left text-xs font-medium text-slate-600 dark:text-slate-400 uppercase">Enabled</th>
                                        <th className="px-6 py-3 text-left text-xs font-medium text-slate-600 dark:text-slate-400 uppercase">Status</th>
                                        <th className="px-6 py-3 text-left text-xs font-medium text-slate-600 dark:text-slate-400 uppercase">Webhook URL</th>
                                        <th className="px-6 py-3"></th>
                                    </tr>
                                </thead>
                                <tbody className="divide-y divide-slate-800/50">
                                    {arrInstances.map((arr) => (
                                        <tr key={arr.id} className="hover:bg-slate-100 dark:hover:bg-slate-800/30">
                                            <td className="px-6 py-4">
                                                <span className={clsx(
                                                    "px-2 py-1 rounded text-xs font-medium",
                                                    arr.type === 'sonarr' ? "bg-purple-500/10 text-purple-400" :
                                                    arr.type === 'radarr' ? "bg-yellow-500/10 text-yellow-400" :
                                                    arr.type === 'whisparr-v2' ? "bg-pink-500/10 text-pink-400" :
                                                    "bg-pink-500/10 text-pink-300"
                                                )}>
                                                    {arr.type === 'whisparr-v2' ? 'Whisparr v2' : arr.type === 'whisparr-v3' ? 'Whisparr v3' : arr.type}
                                                </span>
                                            </td>
                                            <td className="px-6 py-4 text-slate-700 dark:text-slate-300">{arr.name}</td>
                                            <td className="px-6 py-4 text-xs text-slate-600 dark:text-slate-400 font-mono">{arr.url}</td>
                                            <td className="px-6 py-4">
                                                {arr.enabled ? (
                                                    <span className="text-xs bg-green-500/10 text-green-400 px-2 py-1 rounded-full border border-green-500/20">Enabled</span>
                                                ) : (
                                                    <span className="text-xs bg-slate-700/50 text-slate-600 dark:text-slate-400 px-2 py-1 rounded-full border border-slate-600/50">Disabled</span>
                                                )}
                                            </td>
                                            <td className="px-6 py-4">
                                                <ServerStatus
                                                    url={arr.url}
                                                    apiKey={arr.api_key}
                                                    isManuallyTesting={manualTestingServer === `${arr.url}-${arr.api_key}`}
                                                />
                                            </td>
                                            <td className="px-6 py-4">
                                                <div className="relative group">
                                                    <input
                                                        type="text"
                                                        readOnly
                                                        value={`${window.location.origin}/api/webhook/${arr.id}?apikey=${apiKeyData?.api_key || '...'}`}
                                                        onClick={(e) => e.currentTarget.select()}
                                                        className="w-full max-w-md bg-white dark:bg-slate-900/50 border border-slate-300 dark:border-slate-700 rounded px-2 py-1 text-xs text-slate-600 dark:text-slate-400 font-mono focus:outline-none focus:border-blue-500 focus:text-blue-300 cursor-pointer"
                                                    />
                                                    <div className="absolute inset-y-0 right-2 flex items-center opacity-0 group-hover:opacity-100 pointer-events-none">
                                                        <Copy className="w-3 h-3 text-slate-500" />
                                                    </div>
                                                </div>
                                            </td>
                                            <td className="px-6 py-4 text-right">
                                                <div className="flex items-center justify-end gap-2">
                                                    <button
                                                        onClick={() => handleManualTest(arr)}
                                                        className="text-slate-600 dark:text-slate-400 hover:text-slate-700 dark:text-slate-300 cursor-pointer"
                                                        title="Test Connection"
                                                    >
                                                        <Activity className="w-4 h-4" />
                                                    </button>
                                                    <button
                                                        onClick={() => handleEditArr(arr)}
                                                        className="text-blue-400 hover:text-blue-300 cursor-pointer"
                                                        title="Edit"
                                                    >
                                                        <Pencil className="w-4 h-4" />
                                                    </button>
                                                    <button
                                                        onClick={() => {
                                                            if (window.confirm(`Are you sure you want to delete "${arr.name}"? This action cannot be undone.`)) {
                                                                deleteArrMutation.mutate(arr.id);
                                                            }
                                                        }}
                                                        className="p-2 hover:bg-red-500/10 text-red-400 rounded-lg transition-colors cursor-pointer"
                                                        title="Delete Server"
                                                    >
                                                        <Trash2 className="w-4 h-4" />
                                                    </button>
                                                </div>
                                            </td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                        </div>
                    ) : (
                        <div className="p-8 text-center text-slate-500 italic">No servers configured</div>
                    )}
                </div>
            </motion.div>

            {/* Scan Paths Section */}
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.2 }}
                className="space-y-4"
            >
                <div className="flex items-center gap-3 mb-6">
                    <FolderOpen className="w-6 h-6 text-green-400" />
                    <h2 className="text-2xl font-semibold text-slate-900 dark:text-white">Scan Paths</h2>
                </div>

                {/* Add Scan Path Form */}
                <div className="rounded-xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl overflow-hidden">
                    <button
                        onClick={() => {
                            if (!isAddPathExpanded && editingPathId) {
                                handleCancelEdit();
                            }
                            setIsAddPathExpanded(!isAddPathExpanded);
                        }}
                        className="w-full px-6 py-4 flex items-center justify-between hover:bg-slate-100 dark:hover:bg-slate-800/30 transition-colors cursor-pointer"
                    >
                        <div className="flex items-center gap-3">
                            {editingPathId ? (
                                <>
                                    <Pencil className="w-5 h-5 text-yellow-400" />
                                    <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Edit Scan Path</h3>
                                </>
                            ) : (
                                <>
                                    <Plus className="w-5 h-5 text-green-400" />
                                    <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Add Scan Path</h3>
                                </>
                            )}
                        </div>
                        <ChevronDown className={clsx(
                            "w-5 h-5 text-slate-600 dark:text-slate-400 transition-transform duration-200",
                            isAddPathExpanded && "rotate-180"
                        )} />
                    </button>

                    <AnimatePresence>
                        {isAddPathExpanded && (
                            <motion.div
                                initial={{ height: 0, opacity: 0 }}
                                animate={{ height: "auto", opacity: 1 }}
                                exit={{ height: 0, opacity: 0 }}
                                transition={{ duration: 0.2, ease: "easeInOut" }}
                            >
                                <form onSubmit={handleCreatePath} className="px-6 pb-6 space-y-4 border-t border-slate-200 dark:border-slate-800/50 pt-4">
                                    <div className="grid grid-cols-2 gap-4">
                                        <div>
                                            <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">Local Path</label>
                                            <div className="flex gap-2">
                                                <input
                                                    type="text"
                                                    value={newPath.local_path || ''}
                                                    onChange={e => setNewPath({ ...newPath, local_path: e.target.value })}
                                                    placeholder="/media/tv or /tv"
                                                    className="flex-1 px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white placeholder-slate-500 focus:ring-2 focus:ring-blue-500"
                                                />
                                                <button
                                                    type="button"
                                                    onClick={() => {
                                                        setFileBrowserTarget('new');
                                                        setShowFileBrowser(true);
                                                    }}
                                                    className="px-3 py-2 bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 border border-slate-300 dark:border-slate-700 rounded-lg transition-colors"
                                                    title="Browse..."
                                                >
                                                    <Folder className="w-5 h-5 text-slate-600 dark:text-slate-400" />
                                                </button>
                                            </div>
                                        </div>
                                        <div>
                                            <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">*arr Path <span className="text-slate-500 font-normal">(optional)</span></label>
                                            <input
                                                type="text"
                                                value={newPath.arr_path || ''}
                                                onChange={e => setNewPath({ ...newPath, arr_path: e.target.value })}
                                                placeholder="Leave empty if same as local path"
                                                className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white placeholder-slate-500 focus:ring-2 focus:ring-blue-500"
                                            />
                                            <p className="mt-1 text-xs text-slate-500">Only fill this in if your *arr app sees media at a different path than Healarr</p>
                                        </div>
                                        <div>
                                            <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">*arr Server</label>
                                            <select
                                                value={newPath.arr_instance_id || ''}
                                                onChange={e => setNewPath({ ...newPath, arr_instance_id: e.target.value ? parseInt(e.target.value) : null })}
                                                className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white focus:ring-2 focus:ring-blue-500"
                                                required
                                            >
                                                <option value="">Select a server...</option>
                                                {arrInstances?.map(arr => (
                                                    <option key={arr.id} value={arr.id}>{arr.name}</option>
                                                ))}
                                            </select>
                                        </div>
                                    </div>
                                    <div className="flex items-center gap-6 pb-2">
                                        <div className="flex items-center gap-3">
                                            <input
                                                type="checkbox"
                                                id="path-enabled"
                                                checked={newPath.enabled || false}
                                                onChange={e => setNewPath({ ...newPath, enabled: e.target.checked })}
                                                className="w-4 h-4 text-blue-500 bg-white dark:bg-slate-800 border-slate-300 dark:border-slate-700 rounded focus:ring-blue-500"
                                            />
                                            <label htmlFor="path-enabled" className="text-sm text-slate-700 dark:text-slate-300">Enabled</label>
                                        </div>
                                        <div className="flex items-center gap-3">
                                            <input
                                                type="checkbox"
                                                id="path-auto-remediate"
                                                checked={newPath.auto_remediate || false}
                                                onChange={e => setNewPath({ ...newPath, auto_remediate: e.target.checked })}
                                                className="w-4 h-4 text-blue-500 bg-white dark:bg-slate-800 border-slate-300 dark:border-slate-700 rounded focus:ring-blue-500"
                                            />
                                            <label htmlFor="path-auto-remediate" className="text-sm text-slate-700 dark:text-slate-300">Auto Remediate</label>
                                        </div>
                                        <div className="flex items-center gap-3">
                                            <label htmlFor="path-max-retries" className="text-sm text-slate-700 dark:text-slate-300">Max Retries:</label>
                                            <input
                                                type="number"
                                                id="path-max-retries"
                                                min="0"
                                                max="10"
                                                value={newPath.max_retries ?? 3}
                                                onChange={e => setNewPath({ ...newPath, max_retries: parseInt(e.target.value) || 0 })}
                                                className="w-20 px-3 py-1 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white text-center focus:ring-2 focus:ring-blue-500"
                                            />
                                        </div>
                                    </div>

                                    {/* Verification Timeout - for hard-to-find items */}
                                    <div className="flex items-center gap-4 pb-2">
                                        <label htmlFor="path-verification-timeout" className="text-sm text-slate-700 dark:text-slate-300">Verification Timeout:</label>
                                        <select
                                            id="path-verification-timeout"
                                            value={newPath.verification_timeout_hours ?? ''}
                                            onChange={e => setNewPath({ ...newPath, verification_timeout_hours: e.target.value ? parseInt(e.target.value) : null })}
                                            className="w-48 px-3 py-1.5 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white focus:ring-2 focus:ring-blue-500"
                                        >
                                            <option value="">Use global default</option>
                                            <option value="24">1 day</option>
                                            <option value="72">3 days</option>
                                            <option value="168">1 week</option>
                                            <option value="336">2 weeks</option>
                                            <option value="720">1 month</option>
                                            <option value="2160">3 months</option>
                                            <option value="4320">6 months</option>
                                        </select>
                                        <p className="text-xs text-slate-500">
                                            How long to keep searching for replacements. Use longer timeouts for rare/hard-to-find content.
                                        </p>
                                    </div>

                                    {/* Detection Configuration */}
                                    <div className="space-y-4 pt-4 border-t border-slate-200 dark:border-slate-800">
                                        <div>
                                            <h4 className="text-sm font-medium text-slate-700 dark:text-slate-300">Detection Settings</h4>
                                            <p className="text-xs text-slate-500 mt-1">Configure how files are checked for corruption</p>
                                        </div>

                                        {/* Detection Method */}
                                        <div>
                                            <label htmlFor="path-detection-method" className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                                                Detection Method
                                            </label>
                                            <select
                                                id="path-detection-method"
                                                value={newPath.detection_method || 'ffprobe'}
                                                onChange={e => setNewPath({ ...newPath, detection_method: e.target.value as 'ffprobe' | 'mediainfo' | 'handbrake' | 'zero_byte' })}
                                                className="w-full px-4 py-2 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-blue-500 cursor-pointer"
                                            >
                                                <option value="ffprobe">FFprobe - Fast header/stream check (recommended)</option>
                                                <option value="mediainfo">MediaInfo - Detailed metadata analysis</option>
                                                <option value="handbrake">HandBrake - Deep stream validation (slow)</option>
                                                <option value="zero_byte">Zero Byte - Quick file size check only</option>
                                            </select>
                                            <p className="mt-2 text-xs text-slate-500">
                                                <span className="font-semibold">FFprobe:</span> Uses ffmpeg's ffprobe to check container and codec validity. Fast and reliable for most media.
                                                <br />
                                                <span className="font-semibold">MediaInfo:</span> Provides comprehensive metadata analysis. Good for detailed inspection.
                                                <br />
                                                <span className="font-semibold">HandBrake:</span> Performs deep stream analysis. Thorough but slow, use for suspect files.
                                                <br />
                                                <span className="font-semibold">Zero Byte:</span> Only checks if file is empty (0 bytes). Fastest, but least thorough.
                                            </p>
                                        </div>

                                        {/* Detection Mode */}
                                        <div>
                                            <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">Detection Mode</label>
                                            <div className="flex gap-4">
                                                <div className="flex items-center gap-2">
                                                    <input
                                                        type="radio"
                                                        id="mode-quick"
                                                        name="detection-mode"
                                                        value="quick"
                                                        checked={(newPath.detection_mode || 'quick') === 'quick'}
                                                        onChange={e => setNewPath({ ...newPath, detection_mode: e.target.value as 'quick' | 'thorough' })}
                                                        className="w-4 h-4 text-blue-500 bg-white dark:bg-slate-800 border-slate-300 dark:border-slate-700 focus:ring-blue-500"
                                                    />
                                                    <label htmlFor="mode-quick" className="text-sm text-slate-700 dark:text-slate-300 cursor-pointer">Quick</label>
                                                </div>
                                                <div className="flex items-center gap-2">
                                                    <input
                                                        type="radio"
                                                        id="mode-thorough"
                                                        name="detection-mode"
                                                        value="thorough"
                                                        checked={newPath.detection_mode === 'thorough'}
                                                        onChange={e => setNewPath({ ...newPath, detection_mode: e.target.value as 'quick' | 'thorough' })}
                                                        className="w-4 h-4 text-blue-500 bg-white dark:bg-slate-800 border-slate-300 dark:border-slate-700 focus:ring-blue-500"
                                                    />
                                                    <label htmlFor="mode-thorough" className="text-sm text-slate-700 dark:text-slate-300 cursor-pointer">Thorough</label>
                                                </div>
                                            </div>
                                            <p className="mt-2 text-xs text-slate-500">
                                                <span className="font-semibold">Quick:</span> Checks headers and basic structure only. Fast, good for most cases.
                                                <br />
                                                <span className="font-semibold">Thorough:</span> May perform additional deep validation depending on method. Slower but more accurate.
                                            </p>
                                        </div>

                                        {/* Custom Arguments */}
                                        {newPath.detection_method !== 'zero_byte' && (
                                            <div>
                                                <label htmlFor="path-detection-args" className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                                                    Custom Arguments <span className="text-slate-500 font-normal">(optional, comma-separated)</span>
                                                </label>
                                                <input
                                                    type="text"
                                                    id="path-detection-args"
                                                    value={newPath.detection_args || ''}
                                                    onChange={e => setNewPath({ ...newPath, detection_args: e.target.value })}
                                                    placeholder="e.g. --verbose, --threads 2"
                                                    className="w-full px-4 py-2 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500"
                                                />
                                                <p className="mt-1 text-xs text-slate-500">
                                                    Enter custom arguments to pass to the detection tool (will be split by commas)
                                                </p>
                                            </div>
                                        )}

                                        {/* Command Preview */}
                                        <div className="mt-4 p-4 rounded-lg bg-slate-950 border border-slate-700">
                                            <div className="flex items-center justify-between mb-2">
                                                <span className="text-xs font-medium text-slate-400 uppercase tracking-wide">Command Preview</span>
                                                {isLoadingPreview && (
                                                    <span className="text-xs text-slate-500 animate-pulse">Loading...</span>
                                                )}
                                            </div>
                                            <code className="block text-sm text-green-400 font-mono break-all">
                                                {detectionPreview?.command || 'Loading...'}
                                            </code>
                                            {detectionPreview && (
                                                <div className="mt-3 pt-3 border-t border-slate-800 space-y-2">
                                                    <div className="flex items-center gap-2 text-xs">
                                                        <Clock className="w-3 h-3 text-slate-500" />
                                                        <span className="text-slate-500">Timeout:</span>
                                                        <span className="text-slate-400">{detectionPreview.timeout}</span>
                                                    </div>
                                                    <p className="text-xs text-slate-500 leading-relaxed">
                                                        {detectionPreview.mode_description}
                                                    </p>
                                                </div>
                                            )}
                                        </div>
                                    </div>

                                    <button
                                        type="submit"
                                        className="flex items-center gap-2 px-4 py-2 bg-blue-500 hover:bg-blue-600 text-slate-900 dark:text-white rounded-lg transition-colors cursor-pointer"
                                    >
                                        {editingPathId ? (
                                            <>
                                                <Save className="w-4 h-4" />
                                                Update Path
                                            </>
                                        ) : (
                                            <>
                                                <Plus className="w-4 h-4" />
                                                Add Path
                                            </>
                                        )}
                                    </button>
                                </form>
                            </motion.div>
                        )}
                    </AnimatePresence>
                </div>

                {/* Scan Paths List */}
                <div className="rounded-xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl overflow-hidden">
                    {isLoadingPaths ? (
                        <div className="p-8 text-center text-slate-600 dark:text-slate-400">Loading paths...</div>
                    ) : scanPaths && scanPaths.length > 0 ? (
                        <div className="overflow-x-auto">
                            <table className="w-full">
                                <thead>
                                    <tr className="border-b border-slate-200 dark:border-slate-800">
                                        <th className="px-6 py-3 text-left text-xs font-medium text-slate-600 dark:text-slate-400 uppercase">Local Path</th>
                                        <th className="px-6 py-3 text-left text-xs font-medium text-slate-600 dark:text-slate-400 uppercase">*arr Path</th>
                                        <th className="px-6 py-3 text-left text-xs font-medium text-slate-600 dark:text-slate-400 uppercase">Status</th>
                                        <th className="px-6 py-3 text-left text-xs font-medium text-slate-600 dark:text-slate-400 uppercase">Auto Remediate</th>
                                        <th className="px-6 py-3 text-left text-xs font-medium text-slate-600 dark:text-slate-400 uppercase">Retries</th>
                                        <th className="px-6 py-3 text-left text-xs font-medium text-slate-600 dark:text-slate-400 uppercase">Timeout</th>
                                        <th className="px-6 py-3"></th>
                                    </tr>
                                </thead>
                                <tbody className="divide-y divide-slate-800/50">
                                    {scanPaths.map((path) => (
                                        <tr key={path.id} className="hover:bg-slate-100 dark:hover:bg-slate-800/30">
                                            <td className="px-6 py-4 text-slate-700 dark:text-slate-300 font-mono text-sm">{path.local_path}</td>
                                            <td className="px-6 py-4 text-slate-600 dark:text-slate-400 font-mono text-sm">
                                                {path.arr_path === path.local_path ? (
                                                    <span className="text-slate-500 italic">Same as local</span>
                                                ) : (
                                                    path.arr_path
                                                )}
                                            </td>
                                            <td className="px-6 py-4">
                                                {path.enabled ? (
                                                    <span className="text-xs bg-green-500/10 text-green-400 px-2 py-1 rounded-full border border-green-500/20">Enabled</span>
                                                ) : (
                                                    <span className="text-xs text-slate-500">Disabled</span>
                                                )}
                                            </td>
                                            <td className="px-6 py-4">
                                                {path.auto_remediate ? (
                                                    <span className="text-xs bg-green-500/10 text-green-400 px-2 py-1 rounded-full border border-green-500/20">Enabled</span>
                                                ) : (
                                                    <span className="text-xs text-slate-500">Disabled</span>
                                                )}
                                            </td>
                                            <td className="px-6 py-4 text-slate-600 dark:text-slate-400 font-mono text-sm">
                                                {path.max_retries ?? 3}
                                            </td>
                                            <td className="px-6 py-4 text-slate-600 dark:text-slate-400 text-sm">
                                                {(() => {
                                                    const hours = path.verification_timeout_hours ?? 72;
                                                    const isDefault = !path.verification_timeout_hours;
                                                    const formatted = hours >= 720 
                                                        ? `${Math.round(hours / 720)}mo`
                                                        : hours >= 168
                                                            ? `${Math.round(hours / 168)}w`
                                                            : hours >= 24
                                                                ? `${Math.round(hours / 24)}d`
                                                                : `${hours}h`;
                                                    return isDefault ? (
                                                        <span className="text-slate-500" title="Using default (72h)">{formatted}</span>
                                                    ) : formatted;
                                                })()}
                                            </td>
                                            <td className="px-6 py-4 text-right">
                                                <div className="flex items-center justify-end gap-2">
                                                    <button
                                                        onClick={() => scanPathMutation.mutate(path.id)}
                                                        className="text-green-400 hover:text-green-300 disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
                                                        title="Scan Now"
                                                        disabled={!path.enabled || scanPathMutation.isPending}
                                                    >
                                                        <Play className="w-4 h-4" />
                                                    </button>
                                                    <button
                                                        onClick={() => handleEditPath(path)}
                                                        className="text-blue-400 hover:text-blue-300 cursor-pointer"
                                                        title="Edit"
                                                    >
                                                        <Pencil className="w-4 h-4" />
                                                    </button>
                                                    <button
                                                        onClick={() => {
                                                            if (window.confirm(`Remove scan path "${path.local_path}"?\n\nThis will remove the path from Healarr scanning only. No files will be deleted from your disk.`)) {
                                                                deletePathMutation.mutate(path.id);
                                                            }
                                                        }}
                                                        className="p-2 hover:bg-red-500/10 text-red-400 rounded-lg transition-colors cursor-pointer"
                                                        title="Delete Path"
                                                    >
                                                        <Trash2 className="w-4 h-4" />
                                                    </button>
                                                </div>
                                            </td>
                                        </tr>
                                    ))}
                                </tbody>
                            </table>
                        </div>
                    ) : (
                        <div className="p-8 text-center text-slate-500 italic">No scan paths configured</div>
                    )}
                </div>
            </motion.div>

            {/* Scheduled Scans Section */}
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.3 }}
                className="space-y-4"
            >
                <div className="flex items-center gap-3 mb-6">
                    <Clock className="w-6 h-6 text-purple-400" />
                    <h2 className="text-2xl font-semibold text-slate-900 dark:text-white">Scheduled Scans</h2>
                </div>

                {/* Add Schedule Form */}
                <div className="rounded-xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl overflow-hidden">
                    <button
                        onClick={() => setIsAddScheduleExpanded(!isAddScheduleExpanded)}
                        className="w-full px-6 py-4 flex items-center justify-between hover:bg-slate-100 dark:hover:bg-slate-800/30 transition-colors cursor-pointer"
                    >
                        <div className="flex items-center gap-3">
                            <Plus className="w-5 h-5 text-purple-400" />
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Add Schedule</h3>
                        </div>
                        <ChevronDown className={clsx(
                            "w-5 h-5 text-slate-600 dark:text-slate-400 transition-transform duration-200",
                            isAddScheduleExpanded && "rotate-180"
                        )} />
                    </button>

                    <AnimatePresence>
                        {isAddScheduleExpanded && (
                            <motion.div
                                initial={{ height: 0, opacity: 0 }}
                                animate={{ height: "auto", opacity: 1 }}
                                exit={{ height: 0, opacity: 0 }}
                                transition={{ duration: 0.2, ease: "easeInOut" }}
                            >
                                <form onSubmit={handleCreateSchedule} className="px-6 pb-6 space-y-4 border-t border-slate-200 dark:border-slate-800/50 pt-4">
                                    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                                        <div>
                                            <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">Scan Path</label>
                                            <select
                                                value={newSchedule.scan_path_id || ''}
                                                onChange={e => setNewSchedule({ ...newSchedule, scan_path_id: parseInt(e.target.value) })}
                                                className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white focus:ring-2 focus:ring-purple-500"
                                                required
                                            >
                                                <option value="">Select a path...</option>
                                                {scanPaths?.map(path => (
                                                    <option key={path.id} value={path.id}>{path.local_path}</option>
                                                ))}
                                            </select>
                                        </div>
                                        <div className="space-y-4">
                                            <div>
                                                <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">Frequency</label>
                                                <select
                                                    value={schedulePreset}
                                                    onChange={e => handlePresetChange(e.target.value)}
                                                    className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white focus:ring-2 focus:ring-purple-500"
                                                >
                                                    <option value="hourly">Hourly</option>
                                                    <option value="every_6h">Every 6 Hours</option>
                                                    <option value="every_12h">Every 12 Hours</option>
                                                    <option value="daily">Daily (3 AM)</option>
                                                    <option value="weekly">Weekly (Sunday 3 AM)</option>
                                                    <option value="custom">Custom Cron Expression</option>
                                                </select>
                                            </div>

                                            {schedulePreset === 'custom' && (
                                                <div>
                                                    <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">Cron Expression</label>
                                                    <input
                                                        type="text"
                                                        value={newSchedule.cron_expression}
                                                        onChange={e => setNewSchedule({ ...newSchedule, cron_expression: e.target.value })}
                                                        placeholder="0 3 * * *"
                                                        className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white placeholder-slate-500 focus:ring-2 focus:ring-purple-500"
                                                        required
                                                    />
                                                    <p className="mt-1 text-xs text-slate-500">
                                                        Format: Minute Hour Day Month Weekday
                                                    </p>
                                                    <p className="mt-2 text-xs text-slate-600 dark:text-slate-400">
                                                        Need help? Use <a href="https://crontab.guru" target="_blank" rel="noopener noreferrer" className="text-purple-400 hover:text-purple-300 underline">crontab.guru</a> to generate an expression.
                                                    </p>
                                                </div>
                                            )}
                                            {schedulePreset !== 'custom' && (
                                                <div className="text-xs text-slate-500 font-mono mt-2">
                                                    Expression: {newSchedule.cron_expression}
                                                </div>
                                            )}
                                        </div>
                                    </div>
                                    <button
                                        type="submit"
                                        className="flex items-center gap-2 px-4 py-2 bg-purple-500 hover:bg-purple-600 text-slate-900 dark:text-white rounded-lg transition-colors cursor-pointer"
                                    >
                                        <Plus className="w-4 h-4" />
                                        Add Schedule
                                    </button>
                                </form>
                            </motion.div>
                        )}
                    </AnimatePresence>
                </div>

                {/* Schedules List */}
                <div className="rounded-xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl overflow-hidden">
                    {isLoadingSchedules ? (
                        <div className="p-8 text-center text-slate-600 dark:text-slate-400">Loading schedules...</div>
                    ) : schedules?.length === 0 ? (
                        <div className="p-8 text-center text-slate-500 italic">No scheduled scans configured</div>
                    ) : (
                        <div className="divide-y divide-slate-800/50">
                            {schedules?.map(schedule => {
                                const path = scanPaths?.find(p => p.id === schedule.scan_path_id);
                                return (
                                    <div key={schedule.id} className="p-4 flex items-center justify-between hover:bg-slate-100 dark:hover:bg-slate-800/30 transition-colors">
                                        <div className="flex items-center gap-4">
                                            <div className={clsx(
                                                "w-2 h-2 rounded-full",
                                                schedule.enabled ? "bg-green-500 shadow-[0_0_8px_rgba(34,197,94,0.5)]" : "bg-slate-600"
                                            )} />
                                            <div>
                                                <div className="font-medium text-slate-900 dark:text-white flex items-center gap-2">
                                                    {path?.local_path || `Path ID: ${schedule.scan_path_id}`}
                                                    {!path && <span className="text-xs text-red-400">(Path not found)</span>}
                                                </div>
                                                <div className="text-sm text-slate-600 dark:text-slate-400 font-mono mt-0.5 flex items-center gap-2">
                                                    <Clock className="w-3 h-3" />
                                                    {schedule.cron_expression}
                                                </div>
                                            </div>
                                        </div>
                                        <div className="flex items-center gap-2">
                                            <button
                                                onClick={() => handleToggleSchedule(schedule)}
                                                className={clsx(
                                                    "px-3 py-1.5 rounded-lg text-xs font-medium transition-colors border cursor-pointer",
                                                    schedule.enabled
                                                        ? "bg-green-500/10 text-green-400 border-green-500/20 hover:bg-green-500/20"
                                                        : "bg-slate-200 dark:bg-slate-800 text-slate-600 dark:text-slate-400 border-slate-300 dark:border-slate-700 hover:bg-slate-300 dark:hover:bg-slate-700 hover:text-slate-900 dark:hover:text-white"
                                                )}
                                            >
                                                {schedule.enabled ? 'Enabled' : 'Disabled'}
                                            </button>
                                            <button
                                                onClick={() => {
                                                    if (confirm('Are you sure you want to delete this schedule?')) {
                                                        deleteScheduleMutation.mutate(schedule.id);
                                                    }
                                                }}
                                                className="p-2 text-slate-600 dark:text-slate-400 hover:text-red-400 hover:bg-red-500/10 rounded-lg transition-colors cursor-pointer"
                                                title="Delete Schedule"
                                            >
                                                <Trash2 className="w-4 h-4" />
                                            </button>
                                        </div>
                                    </div>
                                );
                            })}
                        </div>
                    )}
                </div>
            </motion.div>

            {/* Notifications Section */}
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.35 }}
                className="space-y-4"
            >
                <div className="flex items-center gap-3 mb-4">
                    <div className="p-2 rounded-lg bg-pink-500/10 border border-pink-500/20">
                        <Bell className="w-5 h-5 text-pink-400" />
                    </div>
                    <div>
                        <h2 className="text-2xl font-bold text-slate-900 dark:text-white">Notifications</h2>
                        <p className="text-sm text-slate-600 dark:text-slate-400">Configure alerts for scan events</p>
                    </div>
                </div>

                <NotificationsSection />
            </motion.div>

            {/* Advanced Settings Accordion */}
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.4 }}
                className="space-y-4"
            >
                <div className="rounded-xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl overflow-hidden">
                    <button
                        onClick={() => setIsAdvancedExpanded(!isAdvancedExpanded)}
                        className="w-full px-6 py-4 flex items-center justify-between hover:bg-slate-100 dark:hover:bg-slate-800/30 transition-colors cursor-pointer"
                    >
                        <div className="flex items-center gap-3">
                            <div className="p-2 rounded-lg bg-slate-500/10 border border-slate-500/20">
                                <Wrench className="w-5 h-5 text-slate-600 dark:text-slate-400" />
                            </div>
                            <div className="text-left">
                                <h2 className="text-xl font-bold text-slate-900 dark:text-white">Advanced Settings</h2>
                                <p className="text-sm text-slate-600 dark:text-slate-400">Display, server configuration, and security</p>
                            </div>
                        </div>
                        <ChevronDown className={clsx(
                            "w-5 h-5 text-slate-600 dark:text-slate-400 transition-transform duration-200",
                            isAdvancedExpanded && "rotate-180"
                        )} />
                    </button>

                    <AnimatePresence>
                        {isAdvancedExpanded && (
                            <motion.div
                                initial={{ height: 0, opacity: 0 }}
                                animate={{ height: "auto", opacity: 1 }}
                                exit={{ height: 0, opacity: 0 }}
                                transition={{ duration: 0.2, ease: "easeInOut" }}
                                className="border-t border-slate-200 dark:border-slate-800/50"
                            >
                                <div className="p-6 space-y-8">
                                    {/* Display Settings */}
                                    <div className="space-y-4">
                                        <div className="flex items-center gap-3">
                                            <div className="p-2 rounded-lg bg-cyan-500/10 border border-cyan-500/20">
                                                <Monitor className="w-5 h-5 text-cyan-400" />
                                            </div>
                                            <div>
                                                <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Display Settings</h3>
                                                <p className="text-sm text-slate-600 dark:text-slate-400">Customize how information is displayed</p>
                                            </div>
                                        </div>

                                        <div className="bg-slate-100 dark:bg-slate-800/30 border border-slate-300 dark:border-slate-700/50 rounded-xl p-4">
                                            <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                                                Date & Time Format
                                            </label>
                                            <p className="text-xs text-slate-600 dark:text-slate-400 mb-3">
                                                Choose how dates are displayed throughout the interface.
                                            </p>
                                            <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
                                                {([
                                                    { value: 'time-first' as DateFormatPreset, label: 'Readable', example: '14:30:45 • Jan 15, 2025', description: 'Human-friendly format' },
                                                    { value: 'date-first' as DateFormatPreset, label: 'Readable Alt', example: 'Jan 15, 2025 • 14:30:45', description: 'Date-first in tooltips' },
                                                    { value: 'iso' as DateFormatPreset, label: 'ISO Format', example: '2025-01-15 14:30:45', description: 'Technical/sortable format' },
                                                ]).map((option) => (
                                                    <button
                                                        key={option.value}
                                                        onClick={() => {
                                                            setDateFormatPreset(option.value);
                                                            toast.success(`Date format set to "${option.label}"`);
                                                        }}
                                                        className={clsx(
                                                            "p-3 rounded-lg border transition-all text-left cursor-pointer",
                                                            dateFormatPreset === option.value
                                                                ? "bg-cyan-500/10 border-cyan-500/50 ring-2 ring-cyan-500/30"
                                                                : "bg-slate-100 dark:bg-slate-800/50 border-slate-300 dark:border-slate-700 hover:border-slate-600 hover:bg-slate-100 dark:hover:bg-slate-800"
                                                        )}
                                                    >
                                                        <div className="font-medium text-slate-900 dark:text-white text-sm mb-1">{option.label}</div>
                                                        <div className="text-xs text-slate-600 dark:text-slate-400 font-mono">{option.example}</div>
                                                        <div className="text-xs text-slate-500 mt-1">{option.description}</div>
                                                    </button>
                                                ))}
                                            </div>
                                        </div>
                                    </div>

                                    {/* Server Settings */}
                                    <div className="space-y-4">
                                        <div className="flex items-center gap-3">
                                            <div className="p-2 rounded-lg bg-orange-500/10 border border-orange-500/20">
                                                <Globe className="w-5 h-5 text-orange-400" />
                                            </div>
                                            <div>
                                                <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Server Settings</h3>
                                                <p className="text-sm text-slate-600 dark:text-slate-400">Runtime configuration for reverse proxy support</p>
                                            </div>
                                        </div>

                                        <ServerSettingsSection runtimeConfig={runtimeConfig} queryClient={queryClient} toast={toast} />
                                    </div>

                                    {/* Security */}
                                    <div className="space-y-4">
                                        <div className="flex items-center gap-3">
                                            <div className="p-2 rounded-lg bg-purple-500/10 border border-purple-500/20">
                                                <Shield className="w-5 h-5 text-purple-400" />
                                            </div>
                                            <div>
                                                <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Security</h3>
                                                <p className="text-sm text-slate-600 dark:text-slate-400">API key and password management</p>
                                            </div>
                                        </div>

                                        <SecuritySection apiKeyData={apiKeyData} refetchApiKey={refetchApiKey} />
                                    </div>

                                    {/* Data Management */}
                                    <DataManagementSection toast={toast} queryClient={queryClient} />
                                </div>
                            </motion.div>
                        )}
                    </AnimatePresence>
                </div>
            </motion.div>

            {/* About Section */}
            <motion.div
                ref={aboutSectionRef}
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.45 }}
                className="space-y-4"
                id="about"
            >
                <div className="rounded-xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl overflow-hidden">
                    <button
                        onClick={() => setIsAboutExpanded(!isAboutExpanded)}
                        className="w-full px-6 py-4 flex items-center justify-between hover:bg-slate-100 dark:hover:bg-slate-800/30 transition-colors cursor-pointer"
                    >
                        <div className="flex items-center gap-3">
                            <div className="p-2 rounded-lg bg-green-500/10 border border-green-500/20">
                                <Info className="w-5 h-5 text-green-400" />
                            </div>
                            <div className="text-left">
                                <h2 className="text-xl font-bold text-slate-900 dark:text-white">About</h2>
                                <p className="text-sm text-slate-600 dark:text-slate-400">Version info, changelog, and updates</p>
                            </div>
                        </div>
                        <ChevronDown className={clsx(
                            "w-5 h-5 text-slate-600 dark:text-slate-400 transition-transform duration-200",
                            isAboutExpanded && "rotate-180"
                        )} />
                    </button>

                    <AnimatePresence>
                        {isAboutExpanded && (
                            <motion.div
                                initial={{ height: 0, opacity: 0 }}
                                animate={{ height: "auto", opacity: 1 }}
                                exit={{ height: 0, opacity: 0 }}
                                transition={{ duration: 0.2 }}
                                className="overflow-hidden"
                            >
                                <div className="p-6 border-t border-slate-200 dark:border-slate-800">
                                    <AboutSection />
                                </div>
                            </motion.div>
                        )}
                    </AnimatePresence>
                </div>
            </motion.div>

            {/* File Browser Modal */}
            <FileBrowser
                isOpen={showFileBrowser}
                onClose={() => setShowFileBrowser(false)}
                onSelect={(selectedPath) => {
                    if (fileBrowserTarget === 'new') {
                        setNewPath({ ...newPath, local_path: selectedPath });
                    } else {
                        // For editing existing paths, update via mutation
                        const existingPath = scanPaths?.find(p => p.id === fileBrowserTarget);
                        if (existingPath) {
                            const { id: _id, ...pathData } = existingPath;
                            updatePathMutation.mutate({ id: fileBrowserTarget, data: { ...pathData, local_path: selectedPath } });
                        }
                    }
                }}
                initialPath={fileBrowserTarget === 'new' ? (newPath.local_path || '/') : (scanPaths?.find(p => p.id === fileBrowserTarget)?.local_path || '/')}
            />
        </div>
    );
};

interface SecuritySectionProps {
    apiKeyData: { api_key: string } | undefined;
    refetchApiKey: () => Promise<unknown>;
}

const SecuritySection = ({ apiKeyData, refetchApiKey }: SecuritySectionProps) => {
    const queryClient = useQueryClient();
    const [copied, setCopied] = useState(false);

    // Password change state
    const [currentPassword, setCurrentPassword] = useState('');
    const [newPassword, setNewPassword] = useState('');
    const [confirmPassword, setConfirmPassword] = useState('');
    const [passwordMessage, setPasswordMessage] = useState({ text: '', type: '' });

    const regenerateMutation = useMutation({
        mutationFn: regenerateAPIKey,
        onSuccess: async (data) => {
            // Immediately update the cache with the new key from the response
            queryClient.setQueryData(['apiKey'], { api_key: data.api_key });
            // Force refetch to be absolutely sure
            await refetchApiKey();
        },
    });

    const passwordMutation = useMutation({
        mutationFn: async () => {
            return await changePassword(currentPassword, newPassword);
        },
        onSuccess: () => {
            setPasswordMessage({ text: 'Password changed successfully', type: 'success' });
            setCurrentPassword('');
            setNewPassword('');
            setConfirmPassword('');
            setTimeout(() => setPasswordMessage({ text: '', type: '' }), 3000);
        },
        onError: (error: unknown) => {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            setPasswordMessage({ text: err.response?.data?.error || 'Failed to change password', type: 'error' });
        }
    });

    const handleCopy = async () => {
        if (apiKeyData?.api_key) {
            try {
                // Try modern clipboard API first
                if (navigator.clipboard && window.isSecureContext) {
                    await navigator.clipboard.writeText(apiKeyData.api_key);
                } else {
                    // Fallback for non-secure contexts (HTTP)
                    const textArea = document.createElement('textarea');
                    textArea.value = apiKeyData.api_key;
                    textArea.style.position = 'fixed';
                    textArea.style.left = '-999999px';
                    textArea.style.top = '-999999px';
                    document.body.appendChild(textArea);
                    textArea.focus();
                    textArea.select();
                    document.execCommand('copy');
                    document.body.removeChild(textArea);
                }
                setCopied(true);
                setTimeout(() => setCopied(false), 2000);
            } catch (err) {
                console.error('Failed to copy:', err);
            }
        }
    };

    const handleRegenerate = () => {
        if (confirm('Are you sure? This will invalidate your current API key. You will need to update all webhook URLs in Sonarr/Radarr!')) {
            regenerateMutation.mutate();
        }
    };

    const handlePasswordChange = (e: React.FormEvent) => {
        e.preventDefault();
        if (newPassword.length < 8) {
            setPasswordMessage({ text: 'New password must be at least 8 characters', type: 'error' });
            return;
        }
        if (newPassword !== confirmPassword) {
            setPasswordMessage({ text: 'Passwords do not match', type: 'error' });
            return;
        }
        passwordMutation.mutate();
    };

    return (
        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
            {/* API Key Section */}
            <div className="rounded-xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-6 h-full">
                <div className="space-y-4">
                    <div>
                        <h3 className="text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">Webhook API Key</h3>
                        <p className="text-xs text-slate-500 mb-4">
                            Use this API key in your Sonarr/Radarr webhook URLs. Add <code className="bg-slate-200 dark:bg-slate-800 px-1 rounded text-purple-600 dark:text-purple-300">?apikey=YOUR_KEY</code> to the webhook URL.
                        </p>
                    </div>

                    <div className="flex gap-2">
                        <div className="flex-1 relative">
                            <input
                                type="text"
                                value={apiKeyData?.api_key || 'Loading...'}
                                readOnly
                                onClick={(e) => (e.target as HTMLInputElement).select()}
                                onFocus={(e) => e.target.select()}
                                className="w-full px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-700 dark:text-slate-300 font-mono text-sm cursor-pointer select-all"
                            />
                        </div>
                        <button
                            onClick={handleCopy}
                            className="px-4 py-3 bg-blue-500 hover:bg-blue-600 text-slate-900 dark:text-white rounded-xl font-medium transition-colors flex items-center gap-2 cursor-pointer"
                        >
                            <Copy className="w-4 h-4" />
                            {copied ? 'Copied!' : 'Copy'}
                        </button>
                        <button
                            onClick={handleRegenerate}
                            disabled={regenerateMutation.isPending}
                            className="px-4 py-3 bg-orange-500 hover:bg-orange-600 disabled:bg-orange-500/50 disabled:cursor-not-allowed text-slate-900 dark:text-white rounded-xl font-medium transition-colors flex items-center gap-2 cursor-pointer"
                        >
                            <RefreshCw className={clsx("w-4 h-4", regenerateMutation.isPending && "animate-spin")} />
                            Regenerate
                        </button>
                    </div>

                    <div className="bg-amber-100 dark:bg-yellow-500/10 border border-amber-300 dark:border-yellow-500/20 rounded-lg p-3">
                        <p className="text-xs text-amber-800 dark:text-yellow-300">
                            <strong>Webhook URL Format:</strong><br />
                            <code className="text-xs">http://sokaris:3090/api/webhook/{'{'}instance_id{'}'}</code> + <code>?apikey={apiKeyData?.api_key || 'YOUR_KEY'}</code>
                        </p>
                    </div>
                </div>
            </div>

            {/* Password Change Section */}
            <div className="rounded-xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-6 h-full">
                <h3 className="text-sm font-medium text-slate-700 dark:text-slate-300 mb-2 flex items-center gap-2">
                    <Lock className="w-4 h-4 text-slate-600 dark:text-slate-400" />
                    Change Password
                </h3>
                <p className="text-xs text-slate-500 mb-4">
                    Update the password used to access the Healarr dashboard.
                </p>

                <form onSubmit={handlePasswordChange} className="space-y-3">
                    <div>
                        <input
                            type="password"
                            autoComplete="current-password"
                            value={currentPassword}
                            onChange={(e) => setCurrentPassword(e.target.value)}
                            placeholder="Current Password"
                            className="w-full px-4 py-2 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-purple-500/50 focus:border-purple-500 text-sm"
                            required
                        />
                    </div>
                    <div>
                        <input
                            type="password"
                            autoComplete="new-password"
                            value={newPassword}
                            onChange={(e) => setNewPassword(e.target.value)}
                            placeholder="New Password (min 8 chars)"
                            className="w-full px-4 py-2 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-purple-500/50 focus:border-purple-500 text-sm"
                            required
                            minLength={8}
                        />
                    </div>
                    <div>
                        <input
                            type="password"
                            autoComplete="new-password"
                            value={confirmPassword}
                            onChange={(e) => setConfirmPassword(e.target.value)}
                            placeholder="Confirm New Password"
                            className="w-full px-4 py-2 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-purple-500/50 focus:border-purple-500 text-sm"
                            required
                        />
                    </div>

                    {passwordMessage.text && (
                        <div className={clsx(
                            "p-2 rounded-lg text-xs",
                            passwordMessage.type === 'success' ? "bg-green-500/10 text-green-600 dark:text-green-300 border border-green-500/20" : "bg-red-500/10 text-red-600 dark:text-red-300 border border-red-500/20"
                        )}>
                            {passwordMessage.text}
                        </div>
                    )}

                    <button
                        type="submit"
                        disabled={passwordMutation.isPending}
                        className="w-full px-4 py-2 bg-purple-500 hover:bg-purple-600 disabled:bg-purple-500/50 disabled:cursor-not-allowed text-slate-900 dark:text-white rounded-xl font-medium transition-colors text-sm cursor-pointer"
                    >
                        {passwordMutation.isPending ? 'Updating...' : 'Update Password'}
                    </button>
                </form>
            </div>
        </div>
    );
};

interface ServerSettingsSectionProps {
    runtimeConfig: { base_path: string; base_path_source: string } | undefined;
    queryClient: ReturnType<typeof useQueryClient>;
    toast: ReturnType<typeof useToast>;
}

const ServerSettingsSection = ({ runtimeConfig, queryClient, toast }: ServerSettingsSectionProps) => {
    const [isEditing, setIsEditing] = useState(false);
    const [basePath, setBasePath] = useState(runtimeConfig?.base_path || '/');
    const [showRestartConfirm, setShowRestartConfirm] = useState(false);
    const [pendingRestart, setPendingRestart] = useState(false);

    // Sync state when runtimeConfig changes
    useEffect(() => {
        if (runtimeConfig?.base_path) {
            setBasePath(runtimeConfig.base_path);
        }
    }, [runtimeConfig?.base_path]);

    const updateSettingsMutation = useMutation({
        mutationFn: updateSettings,
        onSuccess: (data) => {
            queryClient.invalidateQueries({ queryKey: ['runtimeConfig'] });
            toast.success('Base path saved! A restart is required for changes to take effect.');
            setIsEditing(false);
            if (data.restart_required) {
                setPendingRestart(true);
            }
        },
        onError: (error: unknown) => {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to save settings: ${err.response?.data?.error || err.message}`);
        }
    });

    const restartMutation = useMutation({
        mutationFn: restartServer,
        onSuccess: () => {
            toast.success('Restart initiated! Healarr will be back shortly...');
            setShowRestartConfirm(false);
            // Show a loading state while the server restarts
            setTimeout(() => {
                window.location.reload();
            }, 3000);
        },
        onError: () => {
            // Server likely restarted before response - this is expected
            toast.success('Restart initiated! Healarr will be back shortly...');
            setShowRestartConfirm(false);
            setTimeout(() => {
                window.location.reload();
            }, 3000);
        }
    });

    const handleSave = () => {
        updateSettingsMutation.mutate({ base_path: basePath });
    };

    const handleCancel = () => {
        setBasePath(runtimeConfig?.base_path || '/');
        setIsEditing(false);
    };

    const handleRestart = () => {
        restartMutation.mutate();
    };

    const isEnvOverride = runtimeConfig?.base_path_source === 'environment';

    return (
        <div className="bg-slate-100 dark:bg-slate-800/30 border border-slate-300 dark:border-slate-700/50 rounded-2xl p-6">
            <div className="space-y-4">
                <div className="p-4 bg-slate-100 dark:bg-slate-800/50 rounded-xl border border-slate-300 dark:border-slate-700">
                    <div className="flex items-start justify-between gap-4">
                        <div className="flex-1">
                            <div className="text-sm font-medium text-slate-700 dark:text-slate-300 mb-1">Base Path</div>
                            <div className="text-xs text-slate-500 mb-3">
                                URL prefix for reverse proxy setups (e.g., /healarr for domain.com/healarr)
                            </div>

                            {isEditing ? (
                                <div className="flex items-center gap-2">
                                    <input
                                        type="text"
                                        value={basePath}
                                        onChange={(e) => setBasePath(e.target.value)}
                                        placeholder="/"
                                        className="flex-1 px-3 py-2 bg-white dark:bg-slate-900 border border-slate-600 rounded-lg text-slate-900 dark:text-white font-mono text-sm focus:outline-none focus:ring-2 focus:ring-orange-500/50 focus:border-orange-500"
                                    />
                                    <button
                                        onClick={handleSave}
                                        disabled={updateSettingsMutation.isPending}
                                        className="px-3 py-2 bg-green-500 hover:bg-green-600 disabled:bg-green-500/50 text-slate-900 dark:text-white rounded-lg text-sm font-medium transition-colors cursor-pointer flex items-center gap-1"
                                    >
                                        <Save className="w-4 h-4" />
                                        {updateSettingsMutation.isPending ? 'Saving...' : 'Save'}
                                    </button>
                                    <button
                                        onClick={handleCancel}
                                        className="px-3 py-2 bg-slate-700 hover:bg-slate-600 text-slate-700 dark:text-slate-300 rounded-lg text-sm font-medium transition-colors cursor-pointer"
                                    >
                                        Cancel
                                    </button>
                                </div>
                            ) : (
                                <div className="flex items-center gap-3">
                                    <code className="px-3 py-1.5 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-sm font-mono text-orange-400">
                                        {runtimeConfig?.base_path || '/'}
                                    </code>
                                    <span className={clsx(
                                        "text-xs px-2 py-1 rounded-full border",
                                        isEnvOverride
                                            ? "bg-blue-500/10 text-blue-400 border-blue-500/20"
                                            : runtimeConfig?.base_path_source === 'database'
                                                ? "bg-purple-500/10 text-purple-400 border-purple-500/20"
                                                : "bg-slate-700/50 text-slate-600 dark:text-slate-400 border-slate-600/50"
                                    )}>
                                        {isEnvOverride ? 'from env' : runtimeConfig?.base_path_source === 'database' ? 'custom' : 'default'}
                                    </span>
                                    {!isEnvOverride && (
                                        <button
                                            onClick={() => setIsEditing(true)}
                                            className="text-slate-600 dark:text-slate-400 hover:text-orange-400 transition-colors cursor-pointer"
                                            title="Edit base path"
                                        >
                                            <Pencil className="w-4 h-4" />
                                        </button>
                                    )}
                                </div>
                            )}
                        </div>
                    </div>
                </div>

                {isEnvOverride && (
                    <div className="bg-blue-500/5 border border-blue-500/20 rounded-lg p-3">
                        <p className="text-xs text-blue-300/80">
                            <strong>Environment Override:</strong> Base path is set via HEALARR_BASE_PATH environment variable.
                            To edit it here, remove the environment variable and restart.
                        </p>
                    </div>
                )}

                {pendingRestart && (
                    <div className="bg-orange-500/10 border border-orange-500/30 rounded-lg p-4">
                        <div className="flex items-center justify-between">
                            <div>
                                <div className="text-sm font-medium text-orange-300">Restart Required</div>
                                <p className="text-xs text-orange-300/70 mt-1">
                                    Settings have been saved. Restart Healarr for changes to take effect.
                                </p>
                            </div>
                            <button
                                onClick={() => setShowRestartConfirm(true)}
                                className="px-4 py-2 bg-orange-500 hover:bg-orange-600 text-slate-900 dark:text-white rounded-lg text-sm font-medium transition-colors cursor-pointer flex items-center gap-2"
                            >
                                <RefreshCw className="w-4 h-4" />
                                Restart Now
                            </button>
                        </div>
                    </div>
                )}

                {/* Restart Confirmation Modal */}
                <AnimatePresence>
                    {showRestartConfirm && (
                        <motion.div
                            initial={{ opacity: 0 }}
                            animate={{ opacity: 1 }}
                            exit={{ opacity: 0 }}
                            className="fixed inset-0 bg-black/60 backdrop-blur-sm z-50 flex items-center justify-center p-4"
                            onClick={() => setShowRestartConfirm(false)}
                        >
                            <motion.div
                                initial={{ scale: 0.95, opacity: 0 }}
                                animate={{ scale: 1, opacity: 1 }}
                                exit={{ scale: 0.95, opacity: 0 }}
                                className="bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-2xl p-6 max-w-md w-full shadow-2xl"
                                onClick={(e) => e.stopPropagation()}
                            >
                                <div className="text-center mb-6">
                                    <div className="w-16 h-16 mx-auto bg-orange-500/10 border border-orange-500/30 rounded-full flex items-center justify-center mb-4">
                                        <RefreshCw className="w-8 h-8 text-orange-400" />
                                    </div>
                                    <h3 className="text-xl font-bold text-slate-900 dark:text-white mb-2">Restart Healarr?</h3>
                                    <p className="text-sm text-slate-600 dark:text-slate-400">
                                        The server will restart to apply the new base path. 
                                        This page will automatically reload once Healarr is back online.
                                    </p>
                                </div>

                                <div className="flex gap-3">
                                    <button
                                        onClick={() => setShowRestartConfirm(false)}
                                        className="flex-1 px-4 py-2 bg-slate-700 hover:bg-slate-600 text-slate-900 dark:text-white rounded-lg font-medium transition-colors cursor-pointer"
                                    >
                                        Cancel
                                    </button>
                                    <button
                                        onClick={handleRestart}
                                        disabled={restartMutation.isPending}
                                        className="flex-1 px-4 py-2 bg-orange-500 hover:bg-orange-600 disabled:bg-orange-500/50 text-slate-900 dark:text-white rounded-lg font-medium transition-colors cursor-pointer flex items-center justify-center gap-2"
                                    >
                                        {restartMutation.isPending ? (
                                            <>
                                                <RefreshCw className="w-4 h-4 animate-spin" />
                                                Restarting...
                                            </>
                                        ) : (
                                            <>
                                                <RefreshCw className="w-4 h-4" />
                                                Restart
                                            </>
                                        )}
                                    </button>
                                </div>
                            </motion.div>
                        </motion.div>
                    )}
                </AnimatePresence>

                <div className="text-xs text-slate-500">
                    <strong>Tip:</strong> You can also set the base path via the <code className="bg-slate-200 dark:bg-slate-800 px-1 rounded text-slate-600 dark:text-slate-400">HEALARR_BASE_PATH</code> environment variable.
                    Environment variables take precedence over database settings.
                </div>
            </div>
        </div>
    );
};

// Provider configuration schemas - all Shoutrrr-supported services
const PROVIDER_CONFIGS = {
    // Popular services
    discord: {
        label: 'Discord',
        icon: '🎮',
        category: 'popular',
        fields: [
            { key: 'webhook_url', label: 'Webhook URL', type: 'text', placeholder: 'https://discord.com/api/webhooks/...' }
        ]
    },
    slack: {
        label: 'Slack',
        icon: '💬',
        category: 'popular',
        fields: [
            { key: 'webhook_url', label: 'Webhook URL', type: 'text', placeholder: 'https://hooks.slack.com/services/...' }
        ]
    },
    telegram: {
        label: 'Telegram',
        icon: '✈️',
        category: 'popular',
        fields: [
            { key: 'bot_token', label: 'Bot Token', type: 'text', placeholder: '123456789:ABC...' },
            { key: 'chat_id', label: 'Chat ID', type: 'text', placeholder: '-1001234567890 or @channel' }
        ]
    },
    email: {
        label: 'Email (SMTP)',
        icon: '📧',
        category: 'popular',
        fields: [
            { key: 'host', label: 'SMTP Host', type: 'text', placeholder: 'smtp.example.com' },
            { key: 'port', label: 'Port', type: 'number', placeholder: '587' },
            { key: 'username', label: 'Username', type: 'text', placeholder: 'Optional' },
            { key: 'password', label: 'Password', type: 'password', placeholder: 'Optional' },
            { key: 'from', label: 'From Address', type: 'email', placeholder: 'healarr@example.com' },
            { key: 'to', label: 'To Address', type: 'email', placeholder: 'you@example.com' },
            { key: 'tls', label: 'Use TLS', type: 'checkbox' }
        ]
    },
    // Push notification services
    pushover: {
        label: 'Pushover',
        icon: '📱',
        category: 'push',
        fields: [
            { key: 'user_key', label: 'User Key', type: 'text', placeholder: 'Your Pushover user key' },
            { key: 'app_token', label: 'App Token', type: 'text', placeholder: 'Your Pushover app token' },
            { key: 'priority', label: 'Priority', type: 'select', numeric: true, options: [
                { value: '-2', label: 'Lowest' },
                { value: '-1', label: 'Low' },
                { value: '0', label: 'Normal' },
                { value: '1', label: 'High' },
                { value: '2', label: 'Emergency' }
            ]},
            { key: 'sound', label: 'Sound', type: 'text', placeholder: 'pushover (optional)' }
        ]
    },
    gotify: {
        label: 'Gotify',
        icon: '🔔',
        category: 'push',
        fields: [
            { key: 'server_url', label: 'Server URL', type: 'text', placeholder: 'https://gotify.example.com' },
            { key: 'app_token', label: 'App Token', type: 'text', placeholder: 'Your Gotify app token' },
            { key: 'priority', label: 'Priority (1-10)', type: 'number', placeholder: '5' }
        ]
    },
    ntfy: {
        label: 'ntfy',
        icon: '📣',
        category: 'push',
        fields: [
            { key: 'server_url', label: 'Server URL (optional)', type: 'text', placeholder: 'https://ntfy.sh' },
            { key: 'topic', label: 'Topic', type: 'text', placeholder: 'healarr-alerts' },
            { key: 'priority', label: 'Priority (1-5)', type: 'number', placeholder: '3' }
        ]
    },
    pushbullet: {
        label: 'Pushbullet',
        icon: '📤',
        category: 'push',
        fields: [
            { key: 'api_token', label: 'Access Token', type: 'text', placeholder: 'Your Pushbullet access token' },
            { key: 'targets', label: 'Targets (optional)', type: 'text', placeholder: 'device/channel/email' }
        ]
    },
    bark: {
        label: 'Bark',
        icon: '🐕',
        category: 'push',
        fields: [
            { key: 'device_key', label: 'Device Key', type: 'text', placeholder: 'Your Bark device key' },
            { key: 'server_url', label: 'Server URL (optional)', type: 'text', placeholder: 'api.day.app' }
        ]
    },
    join: {
        label: 'Join',
        icon: '🔗',
        category: 'push',
        fields: [
            { key: 'api_key', label: 'API Key', type: 'text', placeholder: 'Your Join API key' },
            { key: 'devices', label: 'Devices', type: 'text', placeholder: 'device1,device2 or group.all' }
        ]
    },
    // Messaging apps
    whatsapp: {
        label: 'WhatsApp',
        icon: '💬',
        category: 'messaging',
        fields: [
            { key: 'phone', label: 'Phone Number', type: 'text', placeholder: '+1234567890 (with country code)' },
            { key: 'api_key', label: 'API Key', type: 'text', placeholder: 'Your CallMeBot API key' },
            { key: 'api_url', label: 'API URL (optional)', type: 'text', placeholder: 'https://api.callmebot.com/whatsapp.php' }
        ]
    },
    signal: {
        label: 'Signal',
        icon: '🔒',
        category: 'messaging',
        fields: [
            { key: 'number', label: 'Sender Number', type: 'text', placeholder: '+1234567890 (your registered Signal number)' },
            { key: 'recipient', label: 'Recipient', type: 'text', placeholder: '+1234567890 or group ID' },
            { key: 'api_url', label: 'Signal REST API URL', type: 'text', placeholder: 'http://localhost:8080' }
        ]
    },
    matrix: {
        label: 'Matrix',
        icon: '🔲',
        category: 'messaging',
        fields: [
            { key: 'home_server', label: 'Homeserver URL', type: 'text', placeholder: 'https://matrix.org' },
            { key: 'user', label: 'User ID', type: 'text', placeholder: '@user:matrix.org' },
            { key: 'password', label: 'Password/Token', type: 'password', placeholder: 'Password or access token' },
            { key: 'rooms', label: 'Room IDs', type: 'text', placeholder: '!roomid:server,#alias:server' }
        ]
    },
    // Team collaboration
    teams: {
        label: 'Microsoft Teams',
        icon: '👥',
        category: 'team',
        fields: [
            { key: 'webhook_url', label: 'Webhook URL', type: 'text', placeholder: 'https://xxx.webhook.office.com/webhookb2/...' }
        ]
    },
    googlechat: {
        label: 'Google Chat',
        icon: '💭',
        category: 'team',
        fields: [
            { key: 'webhook_url', label: 'Webhook URL', type: 'text', placeholder: 'https://chat.googleapis.com/v1/spaces/...' }
        ]
    },
    mattermost: {
        label: 'Mattermost',
        icon: '🟣',
        category: 'team',
        fields: [
            { key: 'webhook_url', label: 'Webhook URL', type: 'text', placeholder: 'https://mattermost.example.com/hooks/xxx' },
            { key: 'channel', label: 'Channel (optional)', type: 'text', placeholder: 'town-square' },
            { key: 'username', label: 'Username (optional)', type: 'text', placeholder: 'Healarr' }
        ]
    },
    rocketchat: {
        label: 'Rocket.Chat',
        icon: '🚀',
        category: 'team',
        fields: [
            { key: 'webhook_url', label: 'Webhook URL', type: 'text', placeholder: 'https://rocketchat.example.com/hooks/xxx' },
            { key: 'channel', label: 'Channel (optional)', type: 'text', placeholder: '#general' },
            { key: 'username', label: 'Username (optional)', type: 'text', placeholder: 'Healarr' }
        ]
    },
    zulip: {
        label: 'Zulip',
        icon: '💧',
        category: 'team',
        fields: [
            { key: 'host', label: 'Server Host', type: 'text', placeholder: 'yourorg.zulipchat.com' },
            { key: 'bot_email', label: 'Bot Email', type: 'text', placeholder: 'bot@yourorg.zulipchat.com' },
            { key: 'bot_key', label: 'Bot API Key', type: 'password', placeholder: 'Your bot API key' },
            { key: 'stream', label: 'Stream', type: 'text', placeholder: 'general' },
            { key: 'topic', label: 'Topic', type: 'text', placeholder: 'Healarr Alerts' }
        ]
    },
    // Automation & alerting
    ifttt: {
        label: 'IFTTT',
        icon: '⚡',
        category: 'automation',
        fields: [
            { key: 'webhook_key', label: 'Webhook Key', type: 'text', placeholder: 'Your IFTTT webhook key' },
            { key: 'event', label: 'Event Name', type: 'text', placeholder: 'healarr_alert' }
        ]
    },
    // Integrations (Generic Webhook)
    generic: {
        label: 'Generic Webhook',
        icon: '🌐',
        category: 'integration',
        description: 'Send notifications to any HTTP endpoint. Perfect for integrating with other tools in your media stack.',
        fields: [
            { key: 'webhook_url', label: 'Webhook URL', type: 'text', placeholder: 'https://your-service.com/webhook' },
            { key: 'method', label: 'HTTP Method', type: 'select', options: [
                { value: 'POST', label: 'POST' },
                { value: 'GET', label: 'GET' },
                { value: 'PUT', label: 'PUT' }
            ]},
            { key: 'content_type', label: 'Content-Type', type: 'select', options: [
                { value: 'application/json', label: 'application/json' },
                { value: 'text/plain', label: 'text/plain' },
                { value: 'application/x-www-form-urlencoded', label: 'form-urlencoded' }
            ]},
            { key: 'template', label: 'Template', type: 'select', options: [
                { value: '', label: 'None (plain text body)' },
                { value: 'json', label: 'JSON (title + message)' }
            ]},
            { key: 'message_key', label: 'Message Key (JSON)', type: 'text', placeholder: 'message' },
            { key: 'title_key', label: 'Title Key (JSON)', type: 'text', placeholder: 'title' },
            { key: 'custom_headers', label: 'Custom Headers', type: 'textarea', placeholder: 'Authorization=Bearer xxx\nX-Custom-Header=value' },
            { key: 'extra_data', label: 'Extra JSON Data', type: 'textarea', placeholder: 'source=healarr\npriority=high' }
        ]
    },
    // Raw Shoutrrr URL
    custom: {
        label: 'Custom (Shoutrrr URL)',
        icon: '🔧',
        category: 'advanced',
        description: 'Provide a raw Shoutrrr URL for any supported service.',
        fields: [
            { key: 'url', label: 'Shoutrrr URL', type: 'text', placeholder: 'protocol://...' }
        ]
    }
};

// About Section - Version info, changelog, and update instructions
const AboutSection = () => {
    const { data: updateInfo, isLoading, error, refetch } = useQuery({
        queryKey: ['updateCheck'],
        queryFn: checkForUpdates,
        staleTime: 300000, // 5 minutes
        retry: 1,
    });

    // Parse inline markdown elements (bold, links, URLs) into React nodes
    const parseInlineMarkdown = (text: string, keyPrefix: string): React.ReactNode[] => {
        const result: React.ReactNode[] = [];
        let remaining = text;
        let partIndex = 0;

        while (remaining.length > 0) {
            // Match markdown links [text](url), bold **text**, or bare URLs
            const mdLinkMatch = remaining.match(/\[([^\]]+)\]\(([^)]+)\)/);
            const boldMatch = remaining.match(/\*\*([^*]+)\*\*/);
            const urlMatch = remaining.match(/(https?:\/\/[^\s<>\[\]()]+)/);

            // Find the earliest match
            const matches = [
                mdLinkMatch ? { type: 'mdLink', match: mdLinkMatch, index: mdLinkMatch.index! } : null,
                boldMatch ? { type: 'bold', match: boldMatch, index: boldMatch.index! } : null,
                urlMatch ? { type: 'url', match: urlMatch, index: urlMatch.index! } : null,
            ].filter(Boolean).sort((a, b) => a!.index - b!.index);

            if (matches.length === 0) {
                // No more matches, add remaining text
                if (remaining) result.push(remaining);
                break;
            }

            const first = matches[0]!;

            // Add text before the match
            if (first.index > 0) {
                result.push(remaining.substring(0, first.index));
            }

            if (first.type === 'mdLink') {
                const [fullMatch, linkText, linkUrl] = first.match as RegExpMatchArray;
                result.push(
                    <a
                        key={`${keyPrefix}-link-${partIndex++}`}
                        href={linkUrl}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-blue-500 hover:text-blue-400 underline"
                    >
                        {linkText}
                    </a>
                );
                remaining = remaining.substring(first.index + fullMatch.length);
            } else if (first.type === 'bold') {
                const [fullMatch, boldText] = first.match as RegExpMatchArray;
                result.push(
                    <strong key={`${keyPrefix}-bold-${partIndex++}`} className="font-semibold text-slate-700 dark:text-slate-300">
                        {boldText}
                    </strong>
                );
                remaining = remaining.substring(first.index + fullMatch.length);
            } else if (first.type === 'url') {
                const [fullMatch] = first.match as RegExpMatchArray;
                result.push(
                    <a
                        key={`${keyPrefix}-url-${partIndex++}`}
                        href={fullMatch}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-blue-500 hover:text-blue-400 underline break-all"
                    >
                        {fullMatch}
                    </a>
                );
                remaining = remaining.substring(first.index + fullMatch.length);
            }
        }

        return result;
    };

    // Simple markdown-to-JSX renderer for changelog
    const renderMarkdown = (text: string) => {
        if (!text) return null;

        const lines = text.split('\n');
        const elements: React.ReactNode[] = [];
        let listItems: { key: string; content: React.ReactNode[] }[] = [];

        const flushList = () => {
            if (listItems.length > 0) {
                elements.push(
                    <ul key={`list-${elements.length}`} className="list-disc list-inside space-y-1 text-slate-600 dark:text-slate-400 ml-2">
                        {listItems.map((item) => (
                            <li key={item.key}>{item.content}</li>
                        ))}
                    </ul>
                );
                listItems = [];
            }
        };

        lines.forEach((line, index) => {
            const trimmed = line.trim();

            // Headers
            if (trimmed.startsWith('### ')) {
                flushList();
                elements.push(
                    <h4 key={index} className="text-md font-semibold text-slate-800 dark:text-slate-200 mt-4 mb-2">
                        {parseInlineMarkdown(trimmed.substring(4), `h4-${index}`)}
                    </h4>
                );
            } else if (trimmed.startsWith('## ')) {
                flushList();
                elements.push(
                    <h3 key={index} className="text-lg font-bold text-slate-900 dark:text-white mt-4 mb-2">
                        {parseInlineMarkdown(trimmed.substring(3), `h3-${index}`)}
                    </h3>
                );
            } else if (trimmed.startsWith('# ')) {
                flushList();
                elements.push(
                    <h2 key={index} className="text-xl font-bold text-slate-900 dark:text-white mt-4 mb-2">
                        {parseInlineMarkdown(trimmed.substring(2), `h2-${index}`)}
                    </h2>
                );
            }
            // List items
            else if (trimmed.startsWith('- ') || trimmed.startsWith('* ')) {
                listItems.push({
                    key: `li-${index}`,
                    content: parseInlineMarkdown(trimmed.substring(2), `li-${index}`)
                });
            }
            // Empty lines
            else if (trimmed === '') {
                flushList();
            }
            // Regular text
            else if (trimmed) {
                flushList();
                elements.push(
                    <p key={index} className="text-slate-600 dark:text-slate-400 my-2">
                        {parseInlineMarkdown(trimmed, `p-${index}`)}
                    </p>
                );
            }
        });

        flushList();
        return elements;
    };

    // Detect platform for highlighting the relevant instructions
    const detectPlatform = (): 'docker' | 'linux' | 'macos' | 'windows' | 'unknown' => {
        // Check if running in Docker (common indicators)
        // For a web app, we can't easily detect Docker, so we'll show all options
        const ua = navigator.userAgent.toLowerCase();
        if (ua.includes('win')) return 'windows';
        if (ua.includes('mac')) return 'macos';
        if (ua.includes('linux')) return 'linux';
        return 'unknown';
    };

    const currentPlatform = detectPlatform();

    if (isLoading) {
        return (
            <div className="p-6 text-center">
                <div className="animate-spin w-6 h-6 border-2 border-slate-300 border-t-green-500 rounded-full mx-auto mb-2"></div>
                <p className="text-slate-500">Checking for updates...</p>
            </div>
        );
    }

    if (error) {
        return (
            <div className="p-6 text-center">
                <p className="text-red-400 mb-2">Unable to check for updates</p>
                <p className="text-slate-500 text-sm mb-4">Please check your internet connection</p>
                <button
                    onClick={() => refetch()}
                    className="px-4 py-2 bg-slate-700 hover:bg-slate-600 text-white rounded-lg transition-colors cursor-pointer"
                >
                    Retry
                </button>
            </div>
        );
    }

    return (
        <div className="space-y-6">
            {/* Version Status */}
            <div className="flex items-center justify-between p-4 rounded-xl bg-slate-100 dark:bg-slate-800/50 border border-slate-200 dark:border-slate-700">
                <div className="flex items-center gap-4">
                    <div className={clsx(
                        "p-3 rounded-xl",
                        updateInfo?.update_available
                            ? "bg-amber-500/20 border border-amber-500/30"
                            : "bg-green-500/20 border border-green-500/30"
                    )}>
                        {updateInfo?.update_available ? (
                            <ArrowUpCircle className="w-6 h-6 text-amber-500" />
                        ) : (
                            <Check className="w-6 h-6 text-green-500" />
                        )}
                    </div>
                    <div>
                        <div className="flex items-center gap-2">
                            <span className="font-semibold text-slate-900 dark:text-white">
                                Current Version: {updateInfo?.current_version || 'Unknown'}
                            </span>
                            {updateInfo?.update_available && (
                                <span className="px-2 py-0.5 text-xs bg-amber-500/20 text-amber-600 dark:text-amber-400 rounded-full border border-amber-500/30">
                                    Update Available
                                </span>
                            )}
                        </div>
                        {updateInfo?.update_available && (
                            <p className="text-sm text-slate-600 dark:text-slate-400 mt-1">
                                Latest: {updateInfo.latest_version} (released {updateInfo.published_at})
                            </p>
                        )}
                        {!updateInfo?.update_available && (
                            <p className="text-sm text-green-600 dark:text-green-400 mt-1">
                                You're running the latest version
                            </p>
                        )}
                    </div>
                </div>
                {updateInfo?.release_url && (
                    <a
                        href={updateInfo.release_url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="flex items-center gap-2 px-4 py-2 bg-slate-200 dark:bg-slate-700 hover:bg-slate-300 dark:hover:bg-slate-600 text-slate-700 dark:text-slate-300 rounded-lg transition-colors"
                    >
                        <ExternalLink className="w-4 h-4" />
                        View on GitHub
                    </a>
                )}
            </div>

            {/* Changelog */}
            {updateInfo?.changelog && (
                <div className="rounded-xl border border-slate-200 dark:border-slate-700 bg-white/80 dark:bg-slate-900/40 overflow-hidden">
                    <div className="px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border-b border-slate-200 dark:border-slate-700">
                        <h4 className="font-semibold text-slate-900 dark:text-white">
                            {updateInfo.update_available ? 'What\'s New' : 'Current Release Notes'}
                        </h4>
                    </div>
                    <div className="p-4 max-h-64 overflow-y-auto prose prose-sm dark:prose-invert prose-slate">
                        {renderMarkdown(updateInfo.changelog)}
                    </div>
                </div>
            )}

            {/* Update Instructions */}
            {updateInfo?.update_available && (
                <div className="rounded-xl border border-slate-200 dark:border-slate-700 bg-white/80 dark:bg-slate-900/40 overflow-hidden">
                    <div className="px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border-b border-slate-200 dark:border-slate-700">
                        <h4 className="font-semibold text-slate-900 dark:text-white">How to Update</h4>
                    </div>
                    <div className="p-4 space-y-4">
                        {/* Docker */}
                        <div className={clsx(
                            "p-4 rounded-lg border",
                            "bg-slate-50 dark:bg-slate-800/30 border-slate-200 dark:border-slate-700"
                        )}>
                            <div className="flex items-center gap-2 mb-2">
                                <span className="text-lg">🐳</span>
                                <h5 className="font-medium text-slate-900 dark:text-white">Docker</h5>
                                <span className="text-xs text-slate-500">(Recommended)</span>
                            </div>
                            <pre className="text-sm text-slate-600 dark:text-slate-400 whitespace-pre-wrap font-mono bg-slate-100 dark:bg-slate-800 p-3 rounded-lg">
                                {updateInfo.update_instructions?.docker || 'docker compose pull && docker compose up -d'}
                            </pre>
                        </div>

                        {/* Linux */}
                        <div className={clsx(
                            "p-4 rounded-lg border",
                            currentPlatform === 'linux'
                                ? "bg-blue-50 dark:bg-blue-900/20 border-blue-200 dark:border-blue-700"
                                : "bg-slate-50 dark:bg-slate-800/30 border-slate-200 dark:border-slate-700"
                        )}>
                            <div className="flex items-center gap-2 mb-2">
                                <span className="text-lg">🐧</span>
                                <h5 className="font-medium text-slate-900 dark:text-white">Linux</h5>
                                {currentPlatform === 'linux' && (
                                    <span className="text-xs bg-blue-500/20 text-blue-600 dark:text-blue-400 px-2 py-0.5 rounded">Your Platform</span>
                                )}
                            </div>
                            <pre className="text-sm text-slate-600 dark:text-slate-400 whitespace-pre-wrap font-mono bg-slate-100 dark:bg-slate-800 p-3 rounded-lg">
                                {updateInfo.update_instructions?.linux}
                            </pre>
                            {updateInfo.download_urls?.linux_amd64 && (
                                <a
                                    href={updateInfo.download_urls.linux_amd64}
                                    className="inline-flex items-center gap-2 mt-2 text-sm text-blue-500 hover:text-blue-400"
                                >
                                    <Download className="w-4 h-4" />
                                    Download (amd64)
                                </a>
                            )}
                            {updateInfo.download_urls?.linux_arm64 && (
                                <a
                                    href={updateInfo.download_urls.linux_arm64}
                                    className="inline-flex items-center gap-2 mt-2 ml-4 text-sm text-blue-500 hover:text-blue-400"
                                >
                                    <Download className="w-4 h-4" />
                                    Download (arm64)
                                </a>
                            )}
                        </div>

                        {/* macOS */}
                        <div className={clsx(
                            "p-4 rounded-lg border",
                            currentPlatform === 'macos'
                                ? "bg-blue-50 dark:bg-blue-900/20 border-blue-200 dark:border-blue-700"
                                : "bg-slate-50 dark:bg-slate-800/30 border-slate-200 dark:border-slate-700"
                        )}>
                            <div className="flex items-center gap-2 mb-2">
                                <span className="text-lg">🍎</span>
                                <h5 className="font-medium text-slate-900 dark:text-white">macOS</h5>
                                {currentPlatform === 'macos' && (
                                    <span className="text-xs bg-blue-500/20 text-blue-600 dark:text-blue-400 px-2 py-0.5 rounded">Your Platform</span>
                                )}
                            </div>
                            <pre className="text-sm text-slate-600 dark:text-slate-400 whitespace-pre-wrap font-mono bg-slate-100 dark:bg-slate-800 p-3 rounded-lg">
                                {updateInfo.update_instructions?.macos}
                            </pre>
                            {updateInfo.download_urls?.macos_amd64 && (
                                <a
                                    href={updateInfo.download_urls.macos_amd64}
                                    className="inline-flex items-center gap-2 mt-2 text-sm text-blue-500 hover:text-blue-400"
                                >
                                    <Download className="w-4 h-4" />
                                    Download (Intel)
                                </a>
                            )}
                            {updateInfo.download_urls?.macos_arm64 && (
                                <a
                                    href={updateInfo.download_urls.macos_arm64}
                                    className="inline-flex items-center gap-2 mt-2 ml-4 text-sm text-blue-500 hover:text-blue-400"
                                >
                                    <Download className="w-4 h-4" />
                                    Download (Apple Silicon)
                                </a>
                            )}
                        </div>

                        {/* Windows */}
                        <div className={clsx(
                            "p-4 rounded-lg border",
                            currentPlatform === 'windows'
                                ? "bg-blue-50 dark:bg-blue-900/20 border-blue-200 dark:border-blue-700"
                                : "bg-slate-50 dark:bg-slate-800/30 border-slate-200 dark:border-slate-700"
                        )}>
                            <div className="flex items-center gap-2 mb-2">
                                <span className="text-lg">🪟</span>
                                <h5 className="font-medium text-slate-900 dark:text-white">Windows</h5>
                                {currentPlatform === 'windows' && (
                                    <span className="text-xs bg-blue-500/20 text-blue-600 dark:text-blue-400 px-2 py-0.5 rounded">Your Platform</span>
                                )}
                            </div>
                            <pre className="text-sm text-slate-600 dark:text-slate-400 whitespace-pre-wrap font-mono bg-slate-100 dark:bg-slate-800 p-3 rounded-lg">
                                {updateInfo.update_instructions?.windows}
                            </pre>
                            {updateInfo.download_urls?.windows_amd64 && (
                                <a
                                    href={updateInfo.download_urls.windows_amd64}
                                    className="inline-flex items-center gap-2 mt-2 text-sm text-blue-500 hover:text-blue-400"
                                >
                                    <Download className="w-4 h-4" />
                                    Download (.exe)
                                </a>
                            )}
                        </div>
                    </div>
                </div>
            )}
        </div>
    );
};

const NotificationsSection = () => {
    const queryClient = useQueryClient();
    const toast = useToast();
    const [isAddExpanded, setIsAddExpanded] = useState(false);
    const [editingId, setEditingId] = useState<number | null>(null);
    const [showLogFor, setShowLogFor] = useState<number | null>(null);

    // Form state
    const [formData, setFormData] = useState<{
        name: string;
        provider_type: string;
        config: Record<string, unknown>;
        events: string[];
        enabled: boolean;
        throttle_seconds: number;
    }>({
        name: '',
        provider_type: 'discord',
        config: {},
        events: [],
        enabled: true,
        throttle_seconds: 5
    });

    // Queries
    const { data: notifications, isLoading } = useQuery({
        queryKey: ['notifications'],
        queryFn: getNotifications
    });

    const { data: eventGroups } = useQuery({
        queryKey: ['notificationEvents'],
        queryFn: getNotificationEvents
    });

    const { data: logEntries } = useQuery({
        queryKey: ['notificationLog', showLogFor],
        queryFn: () => showLogFor ? getNotificationLog(showLogFor) : Promise.resolve([]),
        enabled: !!showLogFor
    });

    // Mutations
    const createMutation = useMutation({
        mutationFn: createNotification,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['notifications'] });
            toast.success('Notification created successfully');
            resetForm();
        },
        onError: (error: unknown) => {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to create notification: ${err.response?.data?.error || err.message}`);
        }
    });

    const updateMutation = useMutation({
        mutationFn: ({ id, data }: { id: number; data: NotificationConfig }) => updateNotification(id, data),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['notifications'] });
            toast.success('Notification updated successfully');
            resetForm();
        },
        onError: (error: unknown) => {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to update notification: ${err.response?.data?.error || err.message}`);
        }
    });

    const deleteMutation = useMutation({
        mutationFn: deleteNotification,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['notifications'] });
            toast.success('Notification deleted');
        },
        onError: (error: unknown) => {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to delete notification: ${err.response?.data?.error || err.message}`);
        }
    });

    const [isTesting, setIsTesting] = useState(false);
    const [testResult, setTestResult] = useState<{ success?: boolean; message?: string }>({});

    const handleTest = async () => {
        setIsTesting(true);
        setTestResult({});
        try {
            const result = await testNotification({
                name: formData.name,
                provider_type: formData.provider_type,
                config: formData.config,
                events: formData.events,
                enabled: formData.enabled,
                throttle_seconds: formData.throttle_seconds
            });
            setTestResult({
                success: result.success,
                message: result.success ? 'Test notification sent!' : result.error
            });
        } catch (error: unknown) {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            setTestResult({ success: false, message: err.response?.data?.error || err.message });
        } finally {
            setIsTesting(false);
        }
    };

    const resetForm = () => {
        setFormData({
            name: '',
            provider_type: 'discord',
            config: {},
            events: [],
            enabled: true,
            throttle_seconds: 5
        });
        setEditingId(null);
        setIsAddExpanded(false);
        setTestResult({});
    };

    const handleEdit = (notification: NotificationConfig) => {
        setFormData({
            name: notification.name,
            provider_type: notification.provider_type,
            config: notification.config,
            events: notification.events,
            enabled: notification.enabled,
            throttle_seconds: notification.throttle_seconds
        });
        setEditingId(notification.id!);
        setIsAddExpanded(true);
    };

    const handleSubmit = (e: React.FormEvent) => {
        e.preventDefault();
        const payload: NotificationConfig = {
            ...formData
        };
        if (editingId) {
            updateMutation.mutate({ id: editingId, data: payload });
        } else {
            createMutation.mutate(payload);
        }
    };

    const toggleEvent = (event: string) => {
        setFormData(prev => ({
            ...prev,
            events: prev.events.includes(event)
                ? prev.events.filter(e => e !== event)
                : [...prev.events, event]
        }));
    };

    const toggleEventGroup = (events: string[]) => {
        const allSelected = events.every(e => formData.events.includes(e));
        if (allSelected) {
            setFormData(prev => ({
                ...prev,
                events: prev.events.filter(e => !events.includes(e))
            }));
        } else {
            setFormData(prev => ({
                ...prev,
                events: [...new Set([...prev.events, ...events])]
            }));
        }
    };

    const updateConfigField = (key: string, value: unknown) => {
        setFormData(prev => ({
            ...prev,
            config: { ...prev.config, [key]: value }
        }));
    };

    const providerConfig = PROVIDER_CONFIGS[formData.provider_type as keyof typeof PROVIDER_CONFIGS];

    return (
        <div className="space-y-4">
            {/* Add/Edit Form */}
            <div className="rounded-xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl overflow-hidden">
                <button
                    onClick={() => {
                        if (isAddExpanded && editingId) {
                            resetForm();
                        } else {
                            setIsAddExpanded(!isAddExpanded);
                        }
                    }}
                    className="w-full px-6 py-4 flex items-center justify-between hover:bg-slate-100 dark:hover:bg-slate-800/30 transition-colors cursor-pointer"
                >
                    <div className="flex items-center gap-3">
                        {editingId ? (
                            <>
                                <Pencil className="w-5 h-5 text-yellow-400" />
                                <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Edit Notification</h3>
                            </>
                        ) : (
                            <>
                                <Plus className="w-5 h-5 text-pink-400" />
                                <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Add Notification</h3>
                            </>
                        )}
                    </div>
                    <ChevronDown className={clsx(
                        "w-5 h-5 text-slate-600 dark:text-slate-400 transition-transform duration-200",
                        isAddExpanded && "rotate-180"
                    )} />
                </button>

                <AnimatePresence>
                    {isAddExpanded && (
                        <motion.div
                            initial={{ height: 0, opacity: 0 }}
                            animate={{ height: "auto", opacity: 1 }}
                            exit={{ height: 0, opacity: 0 }}
                            transition={{ duration: 0.2, ease: "easeInOut" }}
                        >
                            <form onSubmit={handleSubmit} className="px-6 pb-6 space-y-6 border-t border-slate-200 dark:border-slate-800/50 pt-4">
                                {/* Basic Settings */}
                                <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                                    <div>
                                        <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">Name</label>
                                        <input
                                            type="text"
                                            value={formData.name}
                                            onChange={e => setFormData({ ...formData, name: e.target.value })}
                                            placeholder="My Discord Alerts"
                                            className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white placeholder-slate-500 focus:ring-2 focus:ring-pink-500"
                                            required
                                        />
                                    </div>
                                    <div>
                                        <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">Provider</label>
                                        <select
                                            value={formData.provider_type}
                                            onChange={e => setFormData({ ...formData, provider_type: e.target.value, config: {} })}
                                            className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white focus:ring-2 focus:ring-pink-500"
                                        >
                                            <optgroup label="📱 Popular">
                                                {Object.entries(PROVIDER_CONFIGS).filter(([, config]) => config.category === 'popular').map(([key, config]) => (
                                                    <option key={key} value={key}>{config.icon} {config.label}</option>
                                                ))}
                                            </optgroup>
                                            <optgroup label="🔔 Push Notifications">
                                                {Object.entries(PROVIDER_CONFIGS).filter(([, config]) => config.category === 'push').map(([key, config]) => (
                                                    <option key={key} value={key}>{config.icon} {config.label}</option>
                                                ))}
                                            </optgroup>
                                            <optgroup label="💬 Messaging">
                                                {Object.entries(PROVIDER_CONFIGS).filter(([, config]) => config.category === 'messaging').map(([key, config]) => (
                                                    <option key={key} value={key}>{config.icon} {config.label}</option>
                                                ))}
                                            </optgroup>
                                            <optgroup label="👥 Team Collaboration">
                                                {Object.entries(PROVIDER_CONFIGS).filter(([, config]) => config.category === 'team').map(([key, config]) => (
                                                    <option key={key} value={key}>{config.icon} {config.label}</option>
                                                ))}
                                            </optgroup>
                                            <optgroup label="⚡ Automation">
                                                {Object.entries(PROVIDER_CONFIGS).filter(([, config]) => config.category === 'automation').map(([key, config]) => (
                                                    <option key={key} value={key}>{config.icon} {config.label}</option>
                                                ))}
                                            </optgroup>
                                            <optgroup label="🌐 Integration">
                                                {Object.entries(PROVIDER_CONFIGS).filter(([, config]) => config.category === 'integration').map(([key, config]) => (
                                                    <option key={key} value={key}>{config.icon} {config.label}</option>
                                                ))}
                                            </optgroup>
                                            <optgroup label="🔧 Advanced">
                                                {Object.entries(PROVIDER_CONFIGS).filter(([, config]) => config.category === 'advanced').map(([key, config]) => (
                                                    <option key={key} value={key}>{config.icon} {config.label}</option>
                                                ))}
                                            </optgroup>
                                        </select>
                                    </div>
                                    <div>
                                        <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">Throttle (seconds)</label>
                                        <input
                                            type="number"
                                            min="0"
                                            value={formData.throttle_seconds}
                                            onChange={e => setFormData({ ...formData, throttle_seconds: parseInt(e.target.value) || 0 })}
                                            className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white focus:ring-2 focus:ring-pink-500"
                                        />
                                        <p className="text-xs text-slate-500 mt-1">Minimum seconds between notifications</p>
                                    </div>
                                </div>

                                {/* Provider-specific fields */}
                                <div className="space-y-4">
                                    <div className="flex items-center justify-between">
                                        <h4 className="text-sm font-medium text-slate-700 dark:text-slate-300">{providerConfig?.icon} {providerConfig?.label} Settings</h4>
                                        {'description' in providerConfig && providerConfig.description && (
                                            <p className="text-xs text-slate-500">{providerConfig.description}</p>
                                        )}
                                    </div>
                                    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                                        {providerConfig?.fields.map(field => (
                                            <div key={field.key} className={field.type === 'textarea' ? 'md:col-span-2' : ''}>
                                                <label className="block text-sm font-medium text-slate-600 dark:text-slate-400 mb-2">{field.label}</label>
                                                {field.type === 'select' ? (
                                                    <select
                                                        value={(formData.config[field.key] as string) || (('options' in field && field.options?.[0]?.value) || '')}
                                                        onChange={e => updateConfigField(field.key, 'numeric' in field && field.numeric ? parseInt(e.target.value) : e.target.value)}
                                                        className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white focus:ring-2 focus:ring-pink-500"
                                                    >
                                                        {'options' in field && field.options?.map((opt: { value: string; label: string }) => (
                                                            <option key={opt.value} value={opt.value}>{opt.label}</option>
                                                        ))}
                                                    </select>
                                                ) : field.type === 'checkbox' ? (
                                                    <div className="flex items-center gap-2">
                                                        <input
                                                            type="checkbox"
                                                            checked={!!formData.config[field.key]}
                                                            onChange={e => updateConfigField(field.key, e.target.checked)}
                                                            className="w-4 h-4 text-pink-500 bg-white dark:bg-slate-800 border-slate-300 dark:border-slate-700 rounded focus:ring-pink-500"
                                                        />
                                                        <span className="text-sm text-slate-600 dark:text-slate-400">Enable</span>
                                                    </div>
                                                ) : field.type === 'textarea' ? (
                                                    <textarea
                                                        value={(formData.config[field.key] as string) || ''}
                                                        onChange={e => updateConfigField(field.key, e.target.value)}
                                                        placeholder={field.placeholder}
                                                        rows={3}
                                                        className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white placeholder-slate-500 focus:ring-2 focus:ring-pink-500 font-mono text-sm"
                                                    />
                                                ) : (
                                                    <input
                                                        type={field.type}
                                                        value={(formData.config[field.key] as string) || ''}
                                                        onChange={e => updateConfigField(field.key, field.type === 'number' ? parseInt(e.target.value) || 0 : e.target.value)}
                                                        placeholder={field.placeholder}
                                                        className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white placeholder-slate-500 focus:ring-2 focus:ring-pink-500"
                                                    />
                                                )}
                                            </div>
                                        ))}
                                    </div>
                                </div>

                                {/* Event Selection */}
                                <div className="space-y-4">
                                    <h4 className="text-sm font-medium text-slate-700 dark:text-slate-300">Events to Notify</h4>
                                    <div className="space-y-3">
                                        {eventGroups?.map(group => (
                                            <div key={group.name} className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4">
                                                <div className="flex items-center gap-3 mb-3">
                                                    <input
                                                        type="checkbox"
                                                        checked={group.events.every(e => formData.events.includes(e))}
                                                        onChange={() => toggleEventGroup(group.events)}
                                                        className="w-4 h-4 text-pink-500 bg-white dark:bg-slate-800 border-slate-300 dark:border-slate-700 rounded focus:ring-pink-500"
                                                    />
                                                    <span className="text-sm font-medium text-slate-700 dark:text-slate-300">{group.name}</span>
                                                </div>
                                                <div className="flex flex-wrap gap-2 ml-7">
                                                    {group.events.map(event => (
                                                        <button
                                                            key={event}
                                                            type="button"
                                                            onClick={() => toggleEvent(event)}
                                                            className={clsx(
                                                                "px-2 py-1 text-xs rounded-lg border transition-colors cursor-pointer",
                                                                formData.events.includes(event)
                                                                    ? "bg-pink-500/20 border-pink-500/50 text-pink-600 dark:text-pink-300"
                                                                    : "bg-slate-100 dark:bg-slate-800 border-slate-300 dark:border-slate-700 text-slate-600 dark:text-slate-400 hover:border-slate-400 dark:hover:border-slate-600"
                                                            )}
                                                        >
                                                            {event}
                                                        </button>
                                                    ))}
                                                </div>
                                            </div>
                                        ))}
                                    </div>
                                </div>

                                {/* Enable/Disable */}
                                <div className="flex items-center gap-3">
                                    <input
                                        type="checkbox"
                                        id="notif-enabled"
                                        checked={formData.enabled}
                                        onChange={e => setFormData({ ...formData, enabled: e.target.checked })}
                                        className="w-4 h-4 text-pink-500 bg-white dark:bg-slate-800 border-slate-300 dark:border-slate-700 rounded focus:ring-pink-500"
                                    />
                                    <label htmlFor="notif-enabled" className="text-sm text-slate-700 dark:text-slate-300">Enabled</label>
                                </div>

                                {/* Actions */}
                                <div className="flex items-center justify-between pt-4 border-t border-slate-200 dark:border-slate-800">
                                    <div className="flex items-center gap-3">
                                        <button
                                            type="button"
                                            onClick={handleTest}
                                            disabled={isTesting || !formData.name}
                                            className="px-4 py-2 bg-slate-200 dark:bg-slate-800 hover:bg-slate-300 dark:hover:bg-slate-700 text-slate-700 dark:text-slate-300 rounded-lg transition-colors disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer flex items-center gap-2"
                                        >
                                            <Send className="w-4 h-4" />
                                            {isTesting ? 'Sending...' : 'Test'}
                                        </button>
                                        {testResult.message && (
                                            <span className={clsx(
                                                "text-sm flex items-center gap-1",
                                                testResult.success ? "text-green-400" : "text-red-400"
                                            )}>
                                                {testResult.success ? <Check className="w-4 h-4" /> : <X className="w-4 h-4" />}
                                                {testResult.message}
                                            </span>
                                        )}
                                    </div>
                                    <div className="flex items-center gap-2">
                                        {editingId && (
                                            <button
                                                type="button"
                                                onClick={resetForm}
                                                className="px-4 py-2 bg-slate-700 hover:bg-slate-600 text-slate-700 dark:text-slate-300 rounded-lg transition-colors cursor-pointer"
                                            >
                                                Cancel
                                            </button>
                                        )}
                                        <button
                                            type="submit"
                                            disabled={createMutation.isPending || updateMutation.isPending}
                                            className="flex items-center gap-2 px-4 py-2 bg-pink-500 hover:bg-pink-600 disabled:bg-pink-500/50 text-slate-900 dark:text-white rounded-lg transition-colors cursor-pointer"
                                        >
                                            {editingId ? (
                                                <>
                                                    <Save className="w-4 h-4" />
                                                    Update
                                                </>
                                            ) : (
                                                <>
                                                    <Plus className="w-4 h-4" />
                                                    Create
                                                </>
                                            )}
                                        </button>
                                    </div>
                                </div>
                            </form>
                        </motion.div>
                    )}
                </AnimatePresence>
            </div>

            {/* Notifications List */}
            <div className="rounded-xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl overflow-hidden">
                {isLoading ? (
                    <div className="p-8 text-center text-slate-600 dark:text-slate-400">Loading notifications...</div>
                ) : notifications && notifications.length > 0 ? (
                    <div className="divide-y divide-slate-800/50">
                        {notifications.map(notification => {
                            const provider = PROVIDER_CONFIGS[notification.provider_type as keyof typeof PROVIDER_CONFIGS];
                            return (
                                <div key={notification.id} className="p-4 hover:bg-slate-100 dark:hover:bg-slate-800/30 transition-colors">
                                    <div className="flex items-center justify-between">
                                        <div className="flex items-center gap-4">
                                            <div className={clsx(
                                                "w-3 h-3 rounded-full",
                                                notification.enabled
                                                    ? "bg-green-500 shadow-[0_0_8px_rgba(34,197,94,0.5)]"
                                                    : "bg-slate-600"
                                            )} />
                                            <div>
                                                <div className="flex items-center gap-2">
                                                    <span className="text-lg">{provider?.icon || '📢'}</span>
                                                    <span className="font-medium text-slate-900 dark:text-white">{notification.name}</span>
                                                    <span className="text-xs text-slate-600 dark:text-slate-500 bg-slate-200 dark:bg-slate-800 px-2 py-0.5 rounded">
                                                        {provider?.label || notification.provider_type}
                                                    </span>
                                                </div>
                                                <div className="text-xs text-slate-500 mt-1">
                                                    {notification.events.length} events • {notification.throttle_seconds}s throttle
                                                </div>
                                            </div>
                                        </div>
                                        <div className="flex items-center gap-2">
                                            <button
                                                onClick={() => setShowLogFor(showLogFor === notification.id ? null : notification.id!)}
                                                className={clsx(
                                                    "p-2 rounded-lg transition-colors cursor-pointer",
                                                    showLogFor === notification.id
                                                        ? "bg-pink-500/20 text-pink-400"
                                                        : "text-slate-600 dark:text-slate-400 hover:text-slate-700 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800"
                                                )}
                                                title="View Log"
                                            >
                                                <History className="w-4 h-4" />
                                            </button>
                                            <button
                                                onClick={() => handleEdit(notification)}
                                                className="p-2 text-blue-400 hover:text-blue-300 hover:bg-blue-500/10 rounded-lg transition-colors cursor-pointer"
                                                title="Edit"
                                            >
                                                <Pencil className="w-4 h-4" />
                                            </button>
                                            <button
                                                onClick={() => {
                                                    if (confirm(`Delete notification "${notification.name}"?`)) {
                                                        deleteMutation.mutate(notification.id!);
                                                    }
                                                }}
                                                className="p-2 text-red-400 hover:text-red-300 hover:bg-red-500/10 rounded-lg transition-colors cursor-pointer"
                                                title="Delete"
                                            >
                                                <Trash2 className="w-4 h-4" />
                                            </button>
                                        </div>
                                    </div>

                                    {/* Log entries */}
                                    <AnimatePresence>
                                        {showLogFor === notification.id && (
                                            <motion.div
                                                initial={{ height: 0, opacity: 0 }}
                                                animate={{ height: "auto", opacity: 1 }}
                                                exit={{ height: 0, opacity: 0 }}
                                                className="mt-4 overflow-hidden"
                                            >
                                                <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 max-h-64 overflow-y-auto">
                                                    <h4 className="text-sm font-medium text-slate-700 dark:text-slate-300 mb-3">Recent Activity</h4>
                                                    {logEntries && logEntries.length > 0 ? (
                                                        <div className="space-y-2">
                                                            {logEntries.map((entry: NotificationLogEntry) => (
                                                                <div key={entry.id} className="flex items-start gap-3 text-xs">
                                                                    <div className={clsx(
                                                                        "w-2 h-2 mt-1.5 rounded-full flex-shrink-0",
                                                                        entry.status === 'sent' ? "bg-green-500" : "bg-red-500"
                                                                    )} />
                                                                    <div className="flex-1 min-w-0">
                                                                        <div className="flex items-center gap-2">
                                                                            <span className="text-slate-600 dark:text-slate-400">{entry.event_type}</span>
                                                                            <span className="text-slate-600">•</span>
                                                                            <span className="text-slate-500">{new Date(entry.sent_at).toLocaleString()}</span>
                                                                        </div>
                                                                        {entry.error && (
                                                                            <div className="text-red-400 mt-0.5">{entry.error}</div>
                                                                        )}
                                                                    </div>
                                                                </div>
                                                            ))}
                                                        </div>
                                                    ) : (
                                                        <div className="text-slate-500 text-sm">No activity yet</div>
                                                    )}
                                                </div>
                                            </motion.div>
                                        )}
                                    </AnimatePresence>
                                </div>
                            );
                        })}
                    </div>
                ) : (
                    <div className="p-8 text-center text-slate-500 italic">No notifications configured</div>
                )}
            </div>
        </div>
    );
};

export default Config;
