import { useState, useEffect, useRef } from 'react';
import { useLocation } from 'react-router-dom';
import { motion, AnimatePresence } from 'framer-motion';
import { Settings, ChevronDown, Pencil, Save, Copy, RefreshCw, Shield, Lock, Monitor, Globe, Database, Pause, Square, RotateCcw, Info, Wand2, Download, Upload, Play, PlayCircle, Wrench } from 'lucide-react';
import { useDateFormat, type DateFormatPreset } from '../lib/useDateFormat';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
    getAPIKey, regenerateAPIKey, changePassword,
    getRuntimeConfig, updateSettings, restartServer, resetSetupWizard,
    triggerScanAll, exportConfig, importConfig, downloadDatabaseBackup,
    pauseAllScans, resumeAllScans, cancelAllScans,
    type ConfigExport
} from '../lib/api';
import clsx from 'clsx';
import { useToast } from '../contexts/ToastContext';
import ConfigWarningBanner from '../components/ConfigWarningBanner';
import AboutSection from '../components/AboutSection';
import { ArrServersSection, ScanPathsSection, SchedulesSection } from '../components/config';

// Notifications Section - imported directly as it has its own complex structure
import NotificationsSection from './config/NotificationsSection';

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

            if (!config.version || (!config.arr_instances && !config.scan_paths)) {
                throw new Error('Invalid configuration file');
            }

            const arrCount = config.arr_instances?.length || 0;
            const pathCount = config.scan_paths?.length || 0;
            if (!confirm(`Import ${arrCount} *arr instance(s) and ${pathCount} scan path(s)?\n\nNote: This will ADD to your existing configuration, not replace it.`)) {
                return;
            }

            const result = await importConfig(config);
            toast.success(`Imported ${result.imported.arr_instances} instance(s) and ${result.imported.scan_paths} path(s)`);

            queryClient.invalidateQueries({ queryKey: ['arrInstances'] });
            queryClient.invalidateQueries({ queryKey: ['scanPaths'] });
        } catch (error: unknown) {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to import config: ${err.response?.data?.error || err.message || 'Invalid file'}`);
        }

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

// Setup Wizard Reset Section Component
interface SetupWizardResetSectionProps {
    toast: ReturnType<typeof useToast>;
}

const SetupWizardResetSection = ({ toast }: SetupWizardResetSectionProps) => {
    const [isResetting, setIsResetting] = useState(false);

    const handleResetWizard = async () => {
        setIsResetting(true);
        try {
            await resetSetupWizard();
            toast.success('Setup wizard will appear on next page load');
            setTimeout(() => {
                window.location.reload();
            }, 500);
        } catch (error: unknown) {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to reset wizard: ${err.response?.data?.error || err.message}`);
            setIsResetting(false);
        }
    };

    return (
        <div className="space-y-4">
            <div className="flex items-center gap-3 mb-4">
                <div className="p-2 rounded-lg bg-emerald-500/10 border border-emerald-500/20">
                    <Wand2 className="w-5 h-5 text-emerald-400" />
                </div>
                <div>
                    <h4 className="text-sm font-semibold text-slate-900 dark:text-white">Setup Wizard</h4>
                    <p className="text-xs text-slate-500">Restart the guided setup process</p>
                </div>
            </div>

            <div className="bg-slate-100 dark:bg-slate-800/30 border border-slate-300 dark:border-slate-700/50 rounded-xl p-4">
                <p className="text-sm text-slate-600 dark:text-slate-400 mb-3">
                    Re-run the setup wizard to review or modify your configuration.
                    Your existing settings will be pre-populated, allowing you to make changes or verify your setup.
                </p>
                <button
                    onClick={handleResetWizard}
                    disabled={isResetting}
                    className="flex items-center gap-2 px-4 py-2 bg-emerald-500/10 hover:bg-emerald-500/20 text-emerald-400 rounded-lg transition-colors border border-emerald-500/20 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
                >
                    {isResetting ? (
                        <RefreshCw className="w-4 h-4 animate-spin" />
                    ) : (
                        <Wand2 className="w-4 h-4" />
                    )}
                    Restart Setup Wizard
                </button>
            </div>
        </div>
    );
};

// Security Section Component
interface SecuritySectionProps {
    apiKeyData: { api_key: string } | undefined;
    refetchApiKey: () => Promise<unknown>;
}

const SecuritySection = ({ apiKeyData, refetchApiKey }: SecuritySectionProps) => {
    const queryClient = useQueryClient();
    const [copied, setCopied] = useState(false);
    const [currentPassword, setCurrentPassword] = useState('');
    const [newPassword, setNewPassword] = useState('');
    const [confirmPassword, setConfirmPassword] = useState('');
    const [passwordMessage, setPasswordMessage] = useState({ text: '', type: '' });

    const regenerateMutation = useMutation({
        mutationFn: regenerateAPIKey,
        onSuccess: async (data) => {
            queryClient.setQueryData(['apiKey'], { api_key: data.api_key });
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
                if (navigator.clipboard && window.isSecureContext) {
                    await navigator.clipboard.writeText(apiKeyData.api_key);
                } else {
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
                            <code className="text-xs">{window.location.origin}/api/webhook/{'{'}instance_id{'}'}</code> + <code>?apikey={apiKeyData?.api_key || 'YOUR_KEY'}</code>
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

// Server Settings Section Component
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
            setTimeout(() => {
                window.location.reload();
            }, 3000);
        },
        onError: () => {
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

// Main Config Component
const Config = () => {
    const queryClient = useQueryClient();
    const toast = useToast();
    const location = useLocation();
    const { preset: dateFormatPreset, setDateFormatPreset } = useDateFormat();
    const aboutSectionRef = useRef<HTMLDivElement>(null);

    // Collapsible state
    const [isAdvancedExpanded, setIsAdvancedExpanded] = useState(false);
    const [isAboutExpanded, setIsAboutExpanded] = useState(false);

    // Handle hash navigation (e.g., /config#about)
    useEffect(() => {
        if (location.hash === '#about') {
            setIsAboutExpanded(true);
            setTimeout(() => {
                aboutSectionRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
            }, 100);
        }
    }, [location.hash]);

    // Helper to scroll to About section (Detection Tools)
    const scrollToDetectionTools = () => {
        setIsAboutExpanded(true);
        setTimeout(() => {
            aboutSectionRef.current?.scrollIntoView({ behavior: 'smooth', block: 'start' });
        }, 100);
    };

    // Queries
    const { data: runtimeConfig } = useQuery({
        queryKey: ['runtimeConfig'],
        queryFn: getRuntimeConfig,
        staleTime: Infinity,
    });

    const { data: apiKeyData, refetch: refetchApiKey } = useQuery({
        queryKey: ['apiKey'],
        queryFn: getAPIKey,
    });

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

            {/* Media Managers Section */}
            <ArrServersSection />

            {/* Scan Paths Section */}
            <ScanPathsSection onScrollToDetectionTools={scrollToDetectionTools} />

            {/* Scheduled Scans Section */}
            <SchedulesSection />

            {/* Notifications Section */}
            <NotificationsSection />

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

                                    {/* Setup Wizard */}
                                    <SetupWizardResetSection toast={toast} />
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
        </div>
    );
};

export default Config;
