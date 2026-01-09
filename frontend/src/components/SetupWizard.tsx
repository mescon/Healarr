import { useState, useEffect, useCallback } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import {
    Activity,
    ArrowRight,
    ArrowLeft,
    Server,
    Check,
    Upload,
    Database,
    Wand2,
    X,
    AlertCircle,
    CheckCircle2,
    ExternalLink,
    Eye,
    EyeOff,
    FolderOpen,
    Bell,
    Send,
    Info,
    Webhook,
    Clock,
    PlayCircle,
} from 'lucide-react';
import FileBrowser from './ui/FileBrowser';
import type { RootFolder, ConfigExport } from '../lib/api';
import api, {
    getSetupStatus,
    dismissSetup,
    importConfigPublic,
    restoreDatabasePublic,
    testArrConnection,
    createArrInstance,
    createScanPath,
    getArrRootFolders,
    createNotification,
    testNotification,
    type NotificationConfig,
} from '../lib/api';

interface SetupWizardProps {
    onComplete: (token?: string) => void;
    onSkip: () => void;
}

type WizardStep = 'welcome' | 'password' | 'arr' | 'path' | 'notifications' | 'complete';

interface ArrFormData {
    name: string;
    type: 'sonarr' | 'radarr' | 'whisparr-v2' | 'whisparr-v3';
    url: string;
    api_key: string;
}

interface PathFormData {
    local_path: string;
    arr_path: string;
    arr_instance_id: number | null;
}

const STEPS: WizardStep[] = ['welcome', 'password', 'arr', 'path', 'notifications', 'complete'];

export default function SetupWizard({ onComplete, onSkip }: SetupWizardProps) {
    const [step, setStep] = useState<WizardStep>('welcome');
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState('');
    // Note: setupStatus is intentionally not stored - we only need it once during mount

    // Password step
    const [password, setPassword] = useState('');
    const [confirmPassword, setConfirmPassword] = useState('');
    const [showPassword, setShowPassword] = useState(false);

    // ARR Instance step
    const [arrData, setArrData] = useState<ArrFormData>({
        name: '',
        type: 'sonarr',
        url: '',
        api_key: '',
    });
    const [arrTested, setArrTested] = useState(false);
    const [arrTestResult, setArrTestResult] = useState<{ success: boolean; message?: string } | null>(null);
    const [createdArrId, setCreatedArrId] = useState<number | null>(null);

    // Path step
    const [pathData, setPathData] = useState<PathFormData>({
        local_path: '',
        arr_path: '',
        arr_instance_id: null,
    });
    const [rootFolders, setRootFolders] = useState<RootFolder[]>([]);
    const [loadingRootFolders, setLoadingRootFolders] = useState(false);
    const [fileBrowserOpen, setFileBrowserOpen] = useState(false);

    // Notifications step
    const [notificationProvider, setNotificationProvider] = useState<'discord' | 'slack' | 'telegram' | 'none'>('none');
    const [notificationConfig, setNotificationConfig] = useState<Record<string, string>>({});
    const [notificationTested, setNotificationTested] = useState(false);
    const [notificationTestResult, setNotificationTestResult] = useState<{ success: boolean; message?: string } | null>(null);

    // Import/restore - support both config JSON and database backup
    const [configFile, setConfigFile] = useState<File | null>(null);
    const [databaseFile, setDatabaseFile] = useState<File | null>(null);
    const [importMode, setImportMode] = useState<'fresh' | 'restore' | null>(null);

    // Auth token for completing setup
    const [authToken, setAuthToken] = useState<string | null>(null);

    // Check setup status on load and determine starting step
    useEffect(() => {
        const checkStatus = async () => {
            try {
                const status = await getSetupStatus();
                // If password is already set, skip to next needed step
                if (status.has_password && !status.has_instances) {
                    setStep('arr');
                } else if (status.has_password && status.has_instances && !status.has_scan_paths) {
                    setStep('path');
                } else if (status.has_password && status.has_instances && status.has_scan_paths) {
                    setStep('notifications');
                }
            } catch (err) {
                console.error('Failed to check setup status:', err);
            }
        };
        checkStatus();
    }, []);

    // Load root folders when ARR instance is created
    const loadRootFolders = useCallback(async (instanceId: number) => {
        setLoadingRootFolders(true);
        try {
            const folders = await getArrRootFolders(instanceId);
            setRootFolders(folders);
            // Auto-fill arr_path if only one root folder
            if (folders.length === 1) {
                setPathData(prev => ({ ...prev, arr_path: folders[0].path }));
            }
        } catch (err) {
            console.error('Failed to load root folders:', err);
        } finally {
            setLoadingRootFolders(false);
        }
    }, []);

    useEffect(() => {
        if (createdArrId) {
            loadRootFolders(createdArrId);
            setPathData(prev => ({ ...prev, arr_instance_id: createdArrId }));
        }
    }, [createdArrId, loadRootFolders]);

    // Route to the appropriate step based on setup status
    const routeToNextStep = (status: { has_password: boolean; has_instances: boolean; has_scan_paths: boolean }) => {
        if (!status.has_password) {
            setStep('password');
        } else if (!status.has_instances) {
            setStep('arr');
        } else if (!status.has_scan_paths) {
            setStep('path');
        } else {
            setStep('notifications');
        }
    };

    const handleRestore = async () => {
        if (!configFile && !databaseFile) return;
        setLoading(true);
        setError('');

        try {
            let restartRequired = false;

            // Step 1: Restore database if provided
            if (databaseFile) {
                const result = await restoreDatabasePublic(databaseFile);
                restartRequired = !!result.restart_required;
            }

            // Step 2: Import config if provided (always runs, even if restart needed)
            if (configFile) {
                const text = await configFile.text();
                const config = JSON.parse(text) as Partial<ConfigExport>;
                await importConfigPublic(config);
            }

            // Reload if database restore requires it, otherwise route to next step
            if (restartRequired) {
                window.location.reload();
                return;
            }

            const status = await getSetupStatus();
            routeToNextStep(status);
        } catch (err) {
            const errorMessage = databaseFile && configFile
                ? 'Failed to restore. Please check your files.'
                : databaseFile
                    ? 'Failed to restore database. Please check the file format.'
                    : 'Failed to import configuration. Please check the file format.';
            setError(errorMessage);
            console.error(err);
        } finally {
            setLoading(false);
        }
    };

    const handleSetupPassword = async () => {
        if (password !== confirmPassword) {
            setError('Passwords do not match');
            return;
        }
        if (password.length < 1) {
            setError('Password is required');
            return;
        }

        setLoading(true);
        setError('');

        try {
            const response = await api.post('/auth/setup', { password });
            const token = response.data.token || response.data.api_key;
            if (token) {
                setAuthToken(token);
                localStorage.setItem('healarr_token', token);
            }
            // Check status to route to next needed step (may have imported instances)
            const status = await getSetupStatus();
            routeToNextStep(status);
        } catch (err: unknown) {
            const error = err as { response?: { data?: { error?: string } } };
            setError(error.response?.data?.error || 'Failed to set password');
        } finally {
            setLoading(false);
        }
    };

    const handleTestArr = async () => {
        if (!arrData.url || !arrData.api_key) {
            setError('URL and API key are required');
            return;
        }

        setLoading(true);
        setError('');
        setArrTestResult(null);

        try {
            const result = await testArrConnection(arrData.url, arrData.api_key);
            setArrTestResult(result);
            setArrTested(result.success);
        } catch (err: unknown) {
            const error = err as { response?: { data?: { error?: string } } };
            setArrTestResult({ success: false, message: error.response?.data?.error || 'Connection failed' });
        } finally {
            setLoading(false);
        }
    };

    const handleCreateArr = async () => {
        if (!arrTested) {
            setError('Please test the connection first');
            return;
        }

        setLoading(true);
        setError('');

        try {
            const result = await createArrInstance({
                name: arrData.name || `${arrData.type}-${Date.now()}`,
                type: arrData.type,
                url: arrData.url,
                api_key: arrData.api_key,
                enabled: true,
            });
            setCreatedArrId(result.id);
            setStep('path');
        } catch (err: unknown) {
            const error = err as { response?: { data?: { error?: string } } };
            setError(error.response?.data?.error || 'Failed to create instance');
        } finally {
            setLoading(false);
        }
    };

    const handleCreatePath = async () => {
        if (!pathData.local_path || !pathData.arr_path) {
            setError('Both local and remote paths are required');
            return;
        }

        setLoading(true);
        setError('');

        try {
            await createScanPath({
                local_path: pathData.local_path,
                arr_path: pathData.arr_path,
                arr_instance_id: pathData.arr_instance_id,
                enabled: true,
                auto_remediate: true,
            });
            setStep('notifications');
        } catch (err: unknown) {
            const error = err as { response?: { data?: { error?: string } } };
            setError(error.response?.data?.error || 'Failed to create scan path');
        } finally {
            setLoading(false);
        }
    };

    const handleDismiss = async () => {
        try {
            await dismissSetup();
            onSkip();
        } catch (err) {
            console.error('Failed to dismiss setup:', err);
            onSkip();
        }
    };

    const handleComplete = () => {
        onComplete(authToken || undefined);
    };

    const currentStepIndex = STEPS.indexOf(step);

    const renderStepIndicator = () => (
        <div className="flex items-center justify-center gap-2 mb-8">
            {STEPS.map((s, idx) => (
                <div key={s} className="flex items-center">
                    <div
                        className={`
                            w-8 h-8 rounded-full flex items-center justify-center text-sm font-medium
                            transition-colors duration-300
                            ${idx < currentStepIndex
                                ? 'bg-green-500 text-white'
                                : idx === currentStepIndex
                                    ? 'bg-green-500 text-white ring-4 ring-green-500/20'
                                    : 'bg-slate-200 dark:bg-slate-700 text-slate-500 dark:text-slate-400'
                            }
                        `}
                    >
                        {idx < currentStepIndex ? <Check className="w-4 h-4" /> : idx + 1}
                    </div>
                    {idx < STEPS.length - 1 && (
                        <div
                            className={`w-12 h-1 mx-1 rounded-full transition-colors duration-300 ${
                                idx < currentStepIndex
                                    ? 'bg-green-500'
                                    : 'bg-slate-200 dark:bg-slate-700'
                            }`}
                        />
                    )}
                </div>
            ))}
        </div>
    );

    const renderWelcome = () => (
        <motion.div
            key="welcome"
            initial={{ opacity: 0, x: 20 }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: -20 }}
            className="space-y-6"
        >
            <div className="text-center">
                <h2 className="text-2xl font-bold text-slate-900 dark:text-white mb-2">
                    Welcome to Healarr
                </h2>
                <p className="text-slate-600 dark:text-slate-400">
                    Let's get your instance set up in just a few steps.
                </p>
            </div>

            <div className="grid gap-4">
                <button
                    onClick={() => {
                        setImportMode('fresh');
                        setStep('password');
                    }}
                    className="flex items-center gap-4 p-4 rounded-xl border-2 border-slate-200 dark:border-slate-700 hover:border-green-500 dark:hover:border-green-500 transition-colors text-left group cursor-pointer"
                >
                    <div className="p-3 rounded-lg bg-green-100 dark:bg-green-900/30 text-green-600 dark:text-green-400 group-hover:bg-green-500 group-hover:text-white transition-colors">
                        <Wand2 className="w-6 h-6" />
                    </div>
                    <div>
                        <h3 className="font-semibold text-slate-900 dark:text-white">Fresh Start</h3>
                        <p className="text-sm text-slate-600 dark:text-slate-400">
                            Set up a new Healarr instance from scratch
                        </p>
                    </div>
                    <ArrowRight className="w-5 h-5 ml-auto text-slate-400 group-hover:text-green-500 transition-colors" />
                </button>

                <button
                    onClick={() => setImportMode('restore')}
                    className={`flex items-center gap-4 p-4 rounded-xl border-2 transition-colors text-left group cursor-pointer ${
                        importMode === 'restore'
                            ? 'border-green-500 bg-green-50 dark:bg-green-900/20'
                            : 'border-slate-200 dark:border-slate-700 hover:border-green-500 dark:hover:border-green-500'
                    }`}
                >
                    <div className="p-3 rounded-lg bg-blue-100 dark:bg-blue-900/30 text-blue-600 dark:text-blue-400">
                        <Upload className="w-6 h-6" />
                    </div>
                    <div>
                        <h3 className="font-semibold text-slate-900 dark:text-white">Restore from Backup</h3>
                        <p className="text-sm text-slate-600 dark:text-slate-400">
                            Import config JSON, database backup, or both
                        </p>
                    </div>
                </button>
            </div>

            {importMode === 'restore' && (
                <motion.div
                    initial={{ opacity: 0, height: 0 }}
                    animate={{ opacity: 1, height: 'auto' }}
                    className="space-y-4"
                >
                    {/* Database backup upload */}
                    <div className="space-y-2">
                        <span className="text-sm font-medium text-slate-700 dark:text-slate-300 flex items-center gap-2">
                            <Database className="w-4 h-4 text-purple-400" />
                            Database Backup <span className="text-slate-500 text-xs">(optional)</span>
                        </span>
                        <label
                            htmlFor="database-upload"
                            className={`block border-2 border-dashed rounded-xl p-4 text-center transition-colors cursor-pointer hover:border-purple-400 ${
                                databaseFile ? 'border-purple-500 bg-purple-50 dark:bg-purple-900/20' : 'border-slate-300 dark:border-slate-600'
                            }`}
                        >
                            <input
                                type="file"
                                accept=".db,.sqlite,.sqlite3"
                                onChange={(e) => setDatabaseFile(e.target.files?.[0] || null)}
                                className="hidden"
                                id="database-upload"
                            />
                            <span className="text-sm text-slate-600 dark:text-slate-400">
                                {databaseFile ? (
                                    <span className="text-purple-600 dark:text-purple-400 font-medium">{databaseFile.name}</span>
                                ) : (
                                    'Click to select .db file'
                                )}
                            </span>
                        </label>
                    </div>

                    {/* Config JSON upload */}
                    <div className="space-y-2">
                        <span className="text-sm font-medium text-slate-700 dark:text-slate-300 flex items-center gap-2">
                            <Upload className="w-4 h-4 text-blue-400" />
                            Configuration JSON <span className="text-slate-500 text-xs">(optional)</span>
                        </span>
                        <label
                            htmlFor="config-upload"
                            className={`block border-2 border-dashed rounded-xl p-4 text-center transition-colors cursor-pointer hover:border-blue-400 ${
                                configFile ? 'border-blue-500 bg-blue-50 dark:bg-blue-900/20' : 'border-slate-300 dark:border-slate-600'
                            }`}
                        >
                            <input
                                type="file"
                                accept=".json"
                                onChange={(e) => setConfigFile(e.target.files?.[0] || null)}
                                className="hidden"
                                id="config-upload"
                            />
                            <span className="text-sm text-slate-600 dark:text-slate-400">
                                {configFile ? (
                                    <span className="text-blue-600 dark:text-blue-400 font-medium">{configFile.name}</span>
                                ) : (
                                    'Click to select .json file'
                                )}
                            </span>
                        </label>
                    </div>

                    <p className="text-xs text-slate-500 dark:text-slate-400 text-center">
                        You can provide either file or both. Database is restored first, then config is imported on top.
                    </p>

                    <button
                        onClick={handleRestore}
                        disabled={(!configFile && !databaseFile) || loading}
                        className="w-full py-3 px-4 bg-gradient-to-r from-green-500 to-emerald-600 hover:from-green-600 hover:to-emerald-700 text-white font-semibold rounded-xl transition-all shadow-lg shadow-green-500/20 flex items-center justify-center gap-2 disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
                    >
                        {loading ? (
                            <div className="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                        ) : (
                            <>
                                <span>
                                    {databaseFile && configFile ? 'Restore & Import' : databaseFile ? 'Restore Database' : 'Import Configuration'}
                                </span>
                                <ArrowRight className="w-5 h-5" />
                            </>
                        )}
                    </button>
                </motion.div>
            )}

            <div className="pt-4 border-t border-slate-200 dark:border-slate-700">
                <button
                    onClick={handleDismiss}
                    className="w-full text-sm text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-300 transition-colors cursor-pointer"
                >
                    Skip setup for now (power users)
                </button>
            </div>
        </motion.div>
    );

    const renderPassword = () => (
        <motion.div
            key="password"
            initial={{ opacity: 0, x: 20 }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: -20 }}
            className="space-y-6"
        >
            <div className="text-center">
                <h2 className="text-2xl font-bold text-slate-900 dark:text-white mb-2">
                    Secure Your Instance
                </h2>
                <p className="text-slate-600 dark:text-slate-400">
                    Create a password to protect your Healarr dashboard.
                </p>
            </div>

            <div className="space-y-4">
                <div>
                    <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                        Password
                    </label>
                    <div className="relative">
                        <input
                            type={showPassword ? 'text' : 'password'}
                            value={password}
                            onChange={(e) => setPassword(e.target.value)}
                            className="w-full px-4 py-3 pr-12 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-green-500/50 focus:border-green-500"
                            placeholder="Enter your password"
                        />
                        <button
                            type="button"
                            onClick={() => setShowPassword(!showPassword)}
                            className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-400 hover:text-slate-600 dark:hover:text-slate-300"
                        >
                            {showPassword ? <EyeOff className="w-5 h-5" /> : <Eye className="w-5 h-5" />}
                        </button>
                    </div>
                </div>

                <div>
                    <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                        Confirm Password
                    </label>
                    <input
                        type={showPassword ? 'text' : 'password'}
                        value={confirmPassword}
                        onChange={(e) => setConfirmPassword(e.target.value)}
                        className="w-full px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-green-500/50 focus:border-green-500"
                        placeholder="Confirm your password"
                    />
                </div>
            </div>

            <div className="flex gap-3">
                <button
                    onClick={() => setStep('welcome')}
                    className="px-4 py-3 rounded-xl border border-slate-300 dark:border-slate-600 text-slate-700 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 transition-colors flex items-center gap-2 cursor-pointer"
                >
                    <ArrowLeft className="w-4 h-4" />
                    Back
                </button>
                <button
                    onClick={handleSetupPassword}
                    disabled={loading || !password || password !== confirmPassword}
                    className="flex-1 py-3 px-4 bg-gradient-to-r from-green-500 to-emerald-600 hover:from-green-600 hover:to-emerald-700 text-white font-semibold rounded-xl transition-all shadow-lg shadow-green-500/20 flex items-center justify-center gap-2 disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
                >
                    {loading ? (
                        <div className="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                    ) : (
                        <>
                            <span>Continue</span>
                            <ArrowRight className="w-5 h-5" />
                        </>
                    )}
                </button>
            </div>
        </motion.div>
    );

    const renderArr = () => (
        <motion.div
            key="arr"
            initial={{ opacity: 0, x: 20 }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: -20 }}
            className="space-y-6"
        >
            <div className="text-center">
                <h2 className="text-2xl font-bold text-slate-900 dark:text-white mb-2">
                    Connect Your *arr Instance
                </h2>
                <p className="text-slate-600 dark:text-slate-400">
                    Connect Sonarr, Radarr, or Whisparr to enable automatic media healing.
                </p>
            </div>

            <div className="space-y-4">
                <div>
                    <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                        Instance Type
                    </label>
                    <select
                        value={arrData.type}
                        onChange={(e) => setArrData(prev => ({ ...prev, type: e.target.value as ArrFormData['type'] }))}
                        className="w-full px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-green-500/50 focus:border-green-500"
                    >
                        <option value="sonarr">Sonarr</option>
                        <option value="radarr">Radarr</option>
                        <option value="whisparr-v2">Whisparr v2</option>
                        <option value="whisparr-v3">Whisparr v3</option>
                    </select>
                </div>

                <div>
                    <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                        Name (optional)
                    </label>
                    <input
                        type="text"
                        value={arrData.name}
                        onChange={(e) => setArrData(prev => ({ ...prev, name: e.target.value }))}
                        className="w-full px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-green-500/50 focus:border-green-500"
                        placeholder={`My ${arrData.type}`}
                    />
                </div>

                <div>
                    <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                        URL
                    </label>
                    <input
                        type="url"
                        value={arrData.url}
                        onChange={(e) => {
                            setArrData(prev => ({ ...prev, url: e.target.value }));
                            setArrTested(false);
                        }}
                        className="w-full px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-green-500/50 focus:border-green-500"
                        placeholder="http://localhost:8989"
                    />
                </div>

                <div>
                    <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                        API Key
                        <a
                            href="https://wiki.servarr.com/sonarr/settings#security"
                            target="_blank"
                            rel="noopener noreferrer"
                            className="inline-flex items-center gap-1 ml-2 text-green-500 hover:text-green-600"
                        >
                            <span className="text-xs">Where to find this?</span>
                            <ExternalLink className="w-3 h-3" />
                        </a>
                    </label>
                    <input
                        type="text"
                        value={arrData.api_key}
                        onChange={(e) => {
                            setArrData(prev => ({ ...prev, api_key: e.target.value }));
                            setArrTested(false);
                        }}
                        className="w-full px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-green-500/50 focus:border-green-500 font-mono"
                        placeholder="xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
                    />
                </div>

                {arrTestResult && (
                    <div className={`p-3 rounded-lg flex items-center gap-2 ${
                        arrTestResult.success
                            ? 'bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-300'
                            : 'bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-300'
                    }`}>
                        {arrTestResult.success ? (
                            <CheckCircle2 className="w-5 h-5" />
                        ) : (
                            <AlertCircle className="w-5 h-5" />
                        )}
                        <span className="text-sm">
                            {arrTestResult.success ? 'Connection successful!' : arrTestResult.message}
                        </span>
                    </div>
                )}

                <button
                    onClick={handleTestArr}
                    disabled={loading || !arrData.url || !arrData.api_key}
                    className="w-full py-2 px-4 border border-green-500 text-green-500 rounded-xl hover:bg-green-50 dark:hover:bg-green-900/20 transition-colors disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed flex items-center justify-center gap-2"
                >
                    {loading && !arrTested ? (
                        <div className="w-4 h-4 border-2 border-green-500/30 border-t-green-500 rounded-full animate-spin" />
                    ) : (
                        <Server className="w-4 h-4" />
                    )}
                    Test Connection
                </button>
            </div>

            <div className="flex gap-3">
                <button
                    onClick={() => setStep('password')}
                    className="px-4 py-3 rounded-xl border border-slate-300 dark:border-slate-600 text-slate-700 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 transition-colors flex items-center gap-2 cursor-pointer"
                >
                    <ArrowLeft className="w-4 h-4" />
                    Back
                </button>
                <button
                    onClick={handleCreateArr}
                    disabled={loading || !arrTested}
                    className="flex-1 py-3 px-4 bg-gradient-to-r from-green-500 to-emerald-600 hover:from-green-600 hover:to-emerald-700 text-white font-semibold rounded-xl transition-all shadow-lg shadow-green-500/20 flex items-center justify-center gap-2 disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
                >
                    {loading ? (
                        <div className="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                    ) : (
                        <>
                            <span>Add Instance</span>
                            <ArrowRight className="w-5 h-5" />
                        </>
                    )}
                </button>
            </div>

            <button
                onClick={() => setStep('path')}
                className="w-full text-sm text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-300 transition-colors cursor-pointer"
            >
                Skip for now
            </button>
        </motion.div>
    );

    const renderPath = () => (
        <motion.div
            key="path"
            initial={{ opacity: 0, x: 20 }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: -20 }}
            className="space-y-6"
        >
            <div className="text-center">
                <h2 className="text-2xl font-bold text-slate-900 dark:text-white mb-2">
                    Add a Scan Path
                </h2>
                <p className="text-slate-600 dark:text-slate-400">
                    Configure where Healarr should look for media files to monitor.
                </p>
            </div>

            <div className="space-y-4">
                <div>
                    <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                        Local Path (where files are on this server)
                    </label>
                    <div className="flex gap-2">
                        <input
                            type="text"
                            value={pathData.local_path}
                            onChange={(e) => setPathData(prev => ({ ...prev, local_path: e.target.value }))}
                            className="flex-1 px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-green-500/50 focus:border-green-500 font-mono"
                            placeholder="/data/media/tv"
                        />
                        <button
                            type="button"
                            onClick={() => setFileBrowserOpen(true)}
                            className="px-4 py-3 bg-slate-200 dark:bg-slate-700 hover:bg-slate-300 dark:hover:bg-slate-600 border border-slate-300 dark:border-slate-600 rounded-xl text-slate-700 dark:text-slate-300 transition-colors flex items-center gap-2"
                            title="Browse directories"
                        >
                            <FolderOpen className="w-5 h-5" />
                            <span className="hidden sm:inline">Browse</span>
                        </button>
                    </div>
                </div>

                <div>
                    <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                        Remote Path (as seen by *arr)
                        {rootFolders.length > 0 && (
                            <span className="ml-2 text-slate-500 dark:text-slate-400 font-normal">
                                ({rootFolders.length} root folder{rootFolders.length !== 1 ? 's' : ''} detected)
                            </span>
                        )}
                    </label>
                    {loadingRootFolders ? (
                        <div className="flex items-center gap-2 text-slate-500 dark:text-slate-400">
                            <div className="w-4 h-4 border-2 border-slate-400/30 border-t-slate-400 rounded-full animate-spin" />
                            Loading root folders...
                        </div>
                    ) : rootFolders.length > 0 ? (
                        <select
                            value={pathData.arr_path}
                            onChange={(e) => setPathData(prev => ({ ...prev, arr_path: e.target.value }))}
                            className="w-full px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-green-500/50 focus:border-green-500 font-mono"
                        >
                            <option value="">Select a root folder...</option>
                            {rootFolders.map(folder => (
                                <option key={folder.id} value={folder.path}>
                                    {folder.path}
                                </option>
                            ))}
                        </select>
                    ) : (
                        <input
                            type="text"
                            value={pathData.arr_path}
                            onChange={(e) => setPathData(prev => ({ ...prev, arr_path: e.target.value }))}
                            className="w-full px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-green-500/50 focus:border-green-500 font-mono"
                            placeholder="/tv"
                        />
                    )}
                    <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
                        This path should match the root folder configured in your *arr instance.
                    </p>
                </div>
            </div>

            <div className="flex gap-3">
                <button
                    onClick={() => setStep('arr')}
                    className="px-4 py-3 rounded-xl border border-slate-300 dark:border-slate-600 text-slate-700 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 transition-colors flex items-center gap-2 cursor-pointer"
                >
                    <ArrowLeft className="w-4 h-4" />
                    Back
                </button>
                <button
                    onClick={handleCreatePath}
                    disabled={loading || !pathData.local_path || !pathData.arr_path}
                    className="flex-1 py-3 px-4 bg-gradient-to-r from-green-500 to-emerald-600 hover:from-green-600 hover:to-emerald-700 text-white font-semibold rounded-xl transition-all shadow-lg shadow-green-500/20 flex items-center justify-center gap-2 disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
                >
                    {loading ? (
                        <div className="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                    ) : (
                        <>
                            <span>Add Path</span>
                            <ArrowRight className="w-5 h-5" />
                        </>
                    )}
                </button>
            </div>

            <button
                onClick={() => setStep('notifications')}
                className="w-full text-sm text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-300 transition-colors cursor-pointer"
            >
                Skip for now
            </button>

            <FileBrowser
                isOpen={fileBrowserOpen}
                onClose={() => setFileBrowserOpen(false)}
                onSelect={(path) => setPathData(prev => ({ ...prev, local_path: path }))}
                initialPath={pathData.local_path || '/'}
            />
        </motion.div>
    );

    const handleTestNotification = async () => {
        if (notificationProvider === 'none') return;

        setLoading(true);
        setNotificationTestResult(null);

        try {
            const config: NotificationConfig = {
                name: `${notificationProvider.charAt(0).toUpperCase() + notificationProvider.slice(1)} Notification`,
                provider_type: notificationProvider,
                config: notificationConfig as Record<string, unknown>,
                events: ['CorruptionDetected'],
                enabled: true,
                throttle_seconds: 0,
            };

            const result = await testNotification(config);
            setNotificationTestResult(result);
            if (result.success) {
                setNotificationTested(true);
            }
        } catch (err: unknown) {
            const error = err as { response?: { data?: { error?: string } } };
            setNotificationTestResult({
                success: false,
                message: error.response?.data?.error || 'Test failed'
            });
        } finally {
            setLoading(false);
        }
    };

    const handleCreateNotification = async () => {
        if (notificationProvider === 'none') {
            setStep('complete');
            return;
        }

        setLoading(true);
        setError('');

        try {
            await createNotification({
                name: `${notificationProvider.charAt(0).toUpperCase() + notificationProvider.slice(1)} Notification`,
                provider_type: notificationProvider,
                config: notificationConfig as Record<string, unknown>,
                events: ['CorruptionDetected', 'ScanComplete', 'RemediationComplete'],
                enabled: true,
                throttle_seconds: 300,
            });
            setStep('complete');
        } catch (err: unknown) {
            const error = err as { response?: { data?: { error?: string } } };
            setError(error.response?.data?.error || 'Failed to create notification');
        } finally {
            setLoading(false);
        }
    };

    const renderNotifications = () => (
        <motion.div
            key="notifications"
            initial={{ opacity: 0, x: 20 }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: -20 }}
            className="space-y-6"
        >
            <div className="text-center mb-6">
                <div className="w-16 h-16 mx-auto rounded-2xl bg-gradient-to-br from-purple-500 to-pink-500 flex items-center justify-center mb-4">
                    <Bell className="w-8 h-8 text-white" />
                </div>
                <h2 className="text-2xl font-bold text-slate-900 dark:text-white">
                    Set Up Notifications
                </h2>
                <p className="text-slate-600 dark:text-slate-400 mt-2">
                    Get notified when corrupted files are detected
                </p>
            </div>

            {error && (
                <div className="p-4 rounded-xl bg-red-500/10 border border-red-500/20 flex items-center gap-3 text-red-400">
                    <AlertCircle className="w-5 h-5 flex-shrink-0" />
                    <span>{error}</span>
                </div>
            )}

            <div className="space-y-4">
                <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                    Notification Service
                </label>
                <div className="grid grid-cols-2 gap-3">
                    {[
                        { id: 'none' as const, label: 'Skip', icon: X, desc: 'Set up later' },
                        { id: 'discord' as const, label: 'Discord', icon: Bell, desc: 'Webhook URL' },
                        { id: 'slack' as const, label: 'Slack', icon: Bell, desc: 'Webhook URL' },
                        { id: 'telegram' as const, label: 'Telegram', icon: Send, desc: 'Bot Token' },
                    ].map((option) => (
                        <button
                            key={option.id}
                            onClick={() => {
                                setNotificationProvider(option.id);
                                setNotificationConfig({});
                                setNotificationTested(false);
                                setNotificationTestResult(null);
                            }}
                            className={`p-3 rounded-xl border-2 transition-all text-left cursor-pointer ${
                                notificationProvider === option.id
                                    ? 'border-purple-500 bg-purple-50 dark:bg-purple-900/20'
                                    : 'border-slate-200 dark:border-slate-700 hover:border-purple-300 dark:hover:border-purple-700'
                            }`}
                        >
                            <div className="flex items-center gap-2">
                                <option.icon className={`w-5 h-5 ${notificationProvider === option.id ? 'text-purple-500' : 'text-slate-400'}`} />
                                <span className={`font-medium ${notificationProvider === option.id ? 'text-purple-700 dark:text-purple-300' : 'text-slate-700 dark:text-slate-300'}`}>
                                    {option.label}
                                </span>
                            </div>
                            <span className="text-xs text-slate-500 mt-1 block">{option.desc}</span>
                        </button>
                    ))}
                </div>
            </div>

            {notificationProvider !== 'none' && (
                <motion.div
                    initial={{ opacity: 0, height: 0 }}
                    animate={{ opacity: 1, height: 'auto' }}
                    className="space-y-4"
                >
                    {notificationProvider === 'discord' && (
                        <div className="space-y-2">
                            <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                                Webhook URL
                            </label>
                            <input
                                type="text"
                                value={notificationConfig.webhook_url || ''}
                                onChange={(e) => setNotificationConfig({ ...notificationConfig, webhook_url: e.target.value })}
                                className="w-full px-4 py-3 rounded-xl bg-slate-100 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 focus:ring-2 focus:ring-purple-500 focus:border-transparent transition-colors"
                                placeholder="https://discord.com/api/webhooks/..."
                            />
                            <p className="text-xs text-slate-500">
                                Create a webhook in Discord: Server Settings → Integrations → Webhooks
                            </p>
                        </div>
                    )}

                    {notificationProvider === 'slack' && (
                        <div className="space-y-2">
                            <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                                Webhook URL
                            </label>
                            <input
                                type="text"
                                value={notificationConfig.webhook_url || ''}
                                onChange={(e) => setNotificationConfig({ ...notificationConfig, webhook_url: e.target.value })}
                                className="w-full px-4 py-3 rounded-xl bg-slate-100 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 focus:ring-2 focus:ring-purple-500 focus:border-transparent transition-colors"
                                placeholder="https://hooks.slack.com/services/..."
                            />
                        </div>
                    )}

                    {notificationProvider === 'telegram' && (
                        <>
                            <div className="space-y-2">
                                <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                                    Bot Token
                                </label>
                                <input
                                    type="text"
                                    value={notificationConfig.bot_token || ''}
                                    onChange={(e) => setNotificationConfig({ ...notificationConfig, bot_token: e.target.value })}
                                    className="w-full px-4 py-3 rounded-xl bg-slate-100 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 focus:ring-2 focus:ring-purple-500 focus:border-transparent transition-colors"
                                    placeholder="123456789:ABC..."
                                />
                            </div>
                            <div className="space-y-2">
                                <label className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                                    Chat ID
                                </label>
                                <input
                                    type="text"
                                    value={notificationConfig.chat_id || ''}
                                    onChange={(e) => setNotificationConfig({ ...notificationConfig, chat_id: e.target.value })}
                                    className="w-full px-4 py-3 rounded-xl bg-slate-100 dark:bg-slate-800 border border-slate-300 dark:border-slate-700 focus:ring-2 focus:ring-purple-500 focus:border-transparent transition-colors"
                                    placeholder="-1001234567890"
                                />
                            </div>
                        </>
                    )}

                    {notificationTestResult && (
                        <div className={`p-3 rounded-xl flex items-center gap-2 ${
                            notificationTestResult.success
                                ? 'bg-green-500/10 border border-green-500/20 text-green-400'
                                : 'bg-red-500/10 border border-red-500/20 text-red-400'
                        }`}>
                            {notificationTestResult.success ? (
                                <CheckCircle2 className="w-5 h-5" />
                            ) : (
                                <AlertCircle className="w-5 h-5" />
                            )}
                            <span className="text-sm">
                                {notificationTestResult.success ? 'Test notification sent!' : notificationTestResult.message}
                            </span>
                        </div>
                    )}

                    <button
                        onClick={handleTestNotification}
                        disabled={loading || !notificationConfig.webhook_url && !notificationConfig.bot_token}
                        className="w-full py-2 px-4 border border-purple-500 text-purple-500 rounded-xl hover:bg-purple-50 dark:hover:bg-purple-900/20 transition-colors disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed flex items-center justify-center gap-2"
                    >
                        {loading ? (
                            <div className="w-4 h-4 border-2 border-purple-500/30 border-t-purple-500 rounded-full animate-spin" />
                        ) : (
                            <Send className="w-4 h-4" />
                        )}
                        Test Notification
                    </button>
                </motion.div>
            )}

            <div className="flex gap-3">
                <button
                    onClick={() => setStep('path')}
                    className="px-4 py-3 rounded-xl border border-slate-300 dark:border-slate-600 text-slate-700 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 transition-colors flex items-center gap-2 cursor-pointer"
                >
                    <ArrowLeft className="w-4 h-4" />
                    Back
                </button>
                <button
                    onClick={handleCreateNotification}
                    disabled={loading || (notificationProvider !== 'none' && !notificationTested)}
                    className="flex-1 py-3 px-4 bg-gradient-to-r from-purple-500 to-pink-600 hover:from-purple-600 hover:to-pink-700 text-white font-semibold rounded-xl transition-all shadow-lg shadow-purple-500/20 flex items-center justify-center gap-2 disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
                >
                    {loading ? (
                        <div className="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                    ) : (
                        <>
                            <span>{notificationProvider === 'none' ? 'Skip & Continue' : 'Add Notification'}</span>
                            <ArrowRight className="w-5 h-5" />
                        </>
                    )}
                </button>
            </div>

            <button
                onClick={() => setStep('complete')}
                className="w-full text-sm text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-300 transition-colors cursor-pointer"
            >
                Skip for now
            </button>
        </motion.div>
    );

    const renderComplete = () => (
        <motion.div
            key="complete"
            initial={{ opacity: 0, scale: 0.95 }}
            animate={{ opacity: 1, scale: 1 }}
            className="space-y-6 text-center"
        >
            <div className="inline-flex items-center justify-center w-20 h-20 bg-gradient-to-br from-green-500 to-emerald-600 rounded-full shadow-lg shadow-green-500/30">
                <Check className="w-10 h-10 text-white" />
            </div>

            <div>
                <h2 className="text-2xl font-bold text-slate-900 dark:text-white mb-2">
                    Setup Complete!
                </h2>
                <p className="text-slate-600 dark:text-slate-400">
                    Healarr is ready to monitor your media library.
                </p>
            </div>

            <div className="bg-slate-100 dark:bg-slate-800/50 rounded-xl p-4 text-left space-y-2">
                <h3 className="font-medium text-slate-900 dark:text-white mb-3">What's next?</h3>
                <ul className="space-y-2 text-sm text-slate-600 dark:text-slate-400">
                    <li className="flex items-start gap-2">
                        <Check className="w-4 h-4 text-green-500 mt-0.5 flex-shrink-0" />
                        <span>Run your first scan from the Dashboard</span>
                    </li>
                    <li className="flex items-start gap-2">
                        <Check className="w-4 h-4 text-green-500 mt-0.5 flex-shrink-0" />
                        <span>Configure additional scan paths in Settings</span>
                    </li>
                    <li className="flex items-start gap-2">
                        <Check className="w-4 h-4 text-green-500 mt-0.5 flex-shrink-0" />
                        <span>Set up notifications to stay informed</span>
                    </li>
                    <li className="flex items-start gap-2">
                        <Check className="w-4 h-4 text-green-500 mt-0.5 flex-shrink-0" />
                        <span>Create scan schedules for automated monitoring</span>
                    </li>
                </ul>
            </div>

            {/* Scanning Workflow Help */}
            <div className="bg-blue-500/10 border border-blue-500/20 rounded-xl p-4 text-left">
                <div className="flex items-center gap-2 mb-3">
                    <Info className="w-4 h-4 text-blue-500 flex-shrink-0" />
                    <h3 className="font-medium text-slate-900 dark:text-white text-sm">How Scanning Works</h3>
                </div>
                <div className="space-y-2 text-sm text-slate-600 dark:text-slate-400">
                    <div className="flex items-start gap-2">
                        <Webhook className="w-4 h-4 text-purple-500 mt-0.5 flex-shrink-0" />
                        <div>
                            <span className="font-medium text-slate-700 dark:text-slate-300">Webhooks</span>
                            <span className="text-slate-500 dark:text-slate-400"> — Configure your *arr apps to send webhooks when files are imported. Healarr scans them automatically in real-time.</span>
                        </div>
                    </div>
                    <div className="flex items-start gap-2">
                        <Clock className="w-4 h-4 text-amber-500 mt-0.5 flex-shrink-0" />
                        <div>
                            <span className="font-medium text-slate-700 dark:text-slate-300">Scheduled Scans</span>
                            <span className="text-slate-500 dark:text-slate-400"> — Set up cron schedules to periodically scan your entire library for corrupted files.</span>
                        </div>
                    </div>
                    <div className="flex items-start gap-2">
                        <PlayCircle className="w-4 h-4 text-green-500 mt-0.5 flex-shrink-0" />
                        <div>
                            <span className="font-medium text-slate-700 dark:text-slate-300">Manual Scans</span>
                            <span className="text-slate-500 dark:text-slate-400"> — Run on-demand scans from the Dashboard whenever you want.</span>
                        </div>
                    </div>
                </div>
                <p className="mt-3 text-xs text-slate-500 dark:text-slate-500">
                    Tip: Use webhooks for new imports and scheduled scans for existing files.
                </p>
            </div>

            <button
                onClick={handleComplete}
                className="w-full py-3 px-4 bg-gradient-to-r from-green-500 to-emerald-600 hover:from-green-600 hover:to-emerald-700 text-white font-semibold rounded-xl transition-all shadow-lg shadow-green-500/20 flex items-center justify-center gap-2"
            >
                <span>Go to Dashboard</span>
                <ArrowRight className="w-5 h-5" />
            </button>
        </motion.div>
    );

    return (
        <div className="min-h-screen bg-gradient-to-br from-slate-100 via-slate-50 to-slate-100 dark:from-slate-950 dark:via-slate-900 dark:to-slate-950 flex items-center justify-center p-4">
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                className="w-full max-w-lg"
            >
                {/* Logo/Header */}
                <div className="text-center mb-6">
                    <div className="inline-flex items-center justify-center w-16 h-16 bg-gradient-to-br from-green-500 to-emerald-600 rounded-2xl shadow-lg shadow-green-500/20 mb-4">
                        <Activity className="text-white w-8 h-8" />
                    </div>
                    <h1 className="text-3xl font-bold text-slate-900 dark:text-white mb-1">Healarr</h1>
                    <p className="text-slate-600 dark:text-slate-400 text-sm">Setup Wizard</p>
                </div>

                {/* Card */}
                <div className="bg-white/80 dark:bg-slate-900/50 backdrop-blur-xl border border-slate-200 dark:border-slate-800/50 rounded-2xl p-8 shadow-2xl">
                    {step !== 'welcome' && renderStepIndicator()}

                    {error && (
                        <motion.div
                            initial={{ opacity: 0, y: -10 }}
                            animate={{ opacity: 1, y: 0 }}
                            className="mb-4 p-3 bg-red-500/10 border border-red-500/20 rounded-lg text-sm text-red-600 dark:text-red-300 flex items-center gap-2"
                        >
                            <AlertCircle className="w-4 h-4 flex-shrink-0" />
                            {error}
                        </motion.div>
                    )}

                    <AnimatePresence mode="wait">
                        {step === 'welcome' && renderWelcome()}
                        {step === 'password' && renderPassword()}
                        {step === 'arr' && renderArr()}
                        {step === 'path' && renderPath()}
                        {step === 'notifications' && renderNotifications()}
                        {step === 'complete' && renderComplete()}
                    </AnimatePresence>
                </div>

                {/* Dismiss button for non-welcome steps */}
                {step !== 'welcome' && step !== 'complete' && (
                    <button
                        onClick={handleDismiss}
                        className="w-full mt-4 text-sm text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-300 transition-colors flex items-center justify-center gap-1"
                    >
                        <X className="w-4 h-4" />
                        Exit Setup Wizard
                    </button>
                )}
            </motion.div>
        </div>
    );
}
