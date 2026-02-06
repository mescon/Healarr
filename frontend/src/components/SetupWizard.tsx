import { useState, useEffect, useCallback } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import { Check, X, CheckCircle2 } from 'lucide-react';
import {
    getSetupStatus,
    dismissSetup,
    getArrRootFolders,
    getArrInstances,
    getScanPaths,
    getNotifications,
    getNotificationEventsPublic,
} from '../lib/api';
import type { RootFolder, EventGroup } from '../lib/api';
import {
    STEPS,
    DEFAULT_PATH_DATA,
    type WizardStep,
    type ArrFormData,
    type PathFormData,
    type NotificationFormData,
} from './setup/types';
import WelcomeStep from './setup/WelcomeStep';
import PasswordStep from './setup/PasswordStep';
import ArrStep from './setup/ArrStep';
import PathStep from './setup/PathStep';
import NotificationsStep from './setup/NotificationsStep';
import CompleteStep from './setup/CompleteStep';

interface SetupWizardProps {
    onComplete: (token?: string) => void;
    onSkip: () => void;
}

export default function SetupWizard({ onComplete, onSkip }: SetupWizardProps) {
    const [step, setStep] = useState<WizardStep>('welcome');

    // Auth token for completing setup
    const [authToken, setAuthToken] = useState<string | null>(null);

    // Track restored/imported counts for user feedback
    const [restoredCounts, setRestoredCounts] = useState<{
        instances: number;
        paths: number;
        notifications: number;
    } | null>(null);

    // Initial data for steps (populated after restore)
    const [arrInitialData, setArrInitialData] = useState<ArrFormData | undefined>();
    const [arrInitialTested, setArrInitialTested] = useState<boolean | undefined>();
    const [createdArrId, setCreatedArrId] = useState<number | null>(null);
    const [pathInitialData, setPathInitialData] = useState<PathFormData | undefined>();
    const [notificationInitialData, setNotificationInitialData] = useState<NotificationFormData | undefined>();
    const [notificationInitialTested, setNotificationInitialTested] = useState<boolean | undefined>();

    // Root folders (shared between arr creation and path step)
    const [rootFolders, setRootFolders] = useState<RootFolder[]>([]);
    const [loadingRootFolders, setLoadingRootFolders] = useState(false);

    // Notification event groups
    const [eventGroups, setEventGroups] = useState<EventGroup[]>([]);

    // Route to the appropriate step based on setup status
    const routeToNextStep = useCallback((status: { has_password: boolean; has_instances: boolean; has_scan_paths: boolean }) => {
        if (!status.has_password) {
            setStep('password');
        } else if (!status.has_instances) {
            setStep('arr');
        } else if (!status.has_scan_paths) {
            setStep('path');
        } else {
            setStep('notifications');
        }
    }, []);

    // Load existing data from database after restore to pre-populate wizard
    const loadExistingDataIntoWizard = useCallback(async (): Promise<void> => {
        try {
            const [instances, paths, notifications] = await Promise.all([
                getArrInstances().catch(() => []),
                getScanPaths().catch(() => []),
                getNotifications().catch(() => []),
            ]);

            // Pre-populate arr instance if available
            if (instances.length > 0) {
                const first = instances[0];
                setArrInitialData({
                    type: first.type as ArrFormData['type'],
                    name: first.name || '',
                    url: first.url,
                    api_key: first.api_key,
                });
                setCreatedArrId(first.id);
                setArrInitialTested(true);
            }

            // Pre-populate scan path if available
            if (paths.length > 0) {
                const first = paths[0];
                setPathInitialData({
                    local_path: first.local_path,
                    arr_path: first.arr_path,
                    arr_instance_id: first.arr_instance_id,
                });
            }

            // Pre-populate notification if available
            if (notifications.length > 0) {
                const first = notifications[0];
                setNotificationInitialData({
                    name: first.name,
                    provider_type: first.provider_type,
                    config: first.config || {},
                    events: first.events || ['CorruptionDetected', 'ScanComplete'],
                    enabled: first.enabled ?? true,
                    throttle_seconds: first.throttle_seconds ?? 300,
                });
                setNotificationInitialTested(true);
            }

            setRestoredCounts({
                instances: instances.length,
                paths: paths.length,
                notifications: notifications.length,
            });
        } catch (err) {
            console.error('Failed to load existing data:', err);
        }
    }, []);

    // Check setup status on load and determine starting step
    useEffect(() => {
        const checkStatus = async () => {
            try {
                const status = await getSetupStatus();
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
            // Auto-fill arr_path if only one root folder and no initial data
            if (folders.length === 1 && !pathInitialData) {
                setPathInitialData(prev => ({
                    ...(prev ?? DEFAULT_PATH_DATA),
                    arr_path: folders[0].path,
                }));
            }
        } catch (err) {
            console.error('Failed to load root folders:', err);
        } finally {
            setLoadingRootFolders(false);
        }
    }, [pathInitialData]);

    useEffect(() => {
        if (createdArrId) {
            loadRootFolders(createdArrId);
        }
    }, [createdArrId, loadRootFolders]);

    // Load notification events when reaching notifications step
    useEffect(() => {
        if (step === 'notifications' && eventGroups.length === 0) {
            getNotificationEventsPublic()
                .then(setEventGroups)
                .catch(err => console.error('Failed to load notification events:', err));
        }
    }, [step, eventGroups.length]);

    // Step callbacks

    const handleFreshStart = () => setStep('password');

    const handleRestoreComplete = async () => {
        await loadExistingDataIntoWizard();
        const status = await getSetupStatus();
        routeToNextStep(status);
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

    const handlePasswordNext = async (token?: string) => {
        if (token) {
            setAuthToken(token);
            localStorage.setItem('healarr_token', token);
        }
        const status = await getSetupStatus();
        routeToNextStep(status);
    };

    const handleArrNext = (newArrId: number) => {
        setCreatedArrId(newArrId);
        setStep('path');
    };

    const handlePathNext = () => setStep('notifications');
    const handleNotificationsNext = () => setStep('complete');
    const handleComplete = () => onComplete(authToken || undefined);

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

    return (
        <div className="min-h-screen bg-gradient-to-br from-slate-100 via-slate-50 to-slate-100 dark:from-slate-950 dark:via-slate-900 dark:to-slate-950 flex items-center justify-center p-4">
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                className="w-full max-w-lg"
            >
                {/* Logo/Header */}
                <div className="text-center mb-6">
                    <img src={`${import.meta.env.BASE_URL}healarr.svg`} alt="Healarr" className="w-16 h-16 mb-4 mx-auto" />
                    <h1 className="text-3xl font-bold text-slate-900 dark:text-white mb-1">Healarr</h1>
                    <p className="text-slate-600 dark:text-slate-400 text-sm">Setup Wizard</p>
                </div>

                {/* Card */}
                <div className="bg-white/80 dark:bg-slate-900/50 backdrop-blur-xl border border-slate-200 dark:border-slate-800/50 rounded-2xl p-8 shadow-2xl">
                    {step !== 'welcome' && renderStepIndicator()}

                    {/* Show restored counts banner after config/db import */}
                    {restoredCounts && step !== 'welcome' && step !== 'complete' && (
                        <div className="mb-4 p-3 bg-green-500/10 border border-green-500/20 rounded-lg text-sm text-green-600 dark:text-green-300 flex items-center gap-2">
                            <CheckCircle2 className="w-4 h-4 flex-shrink-0" />
                            <span>
                                Restored: {restoredCounts.instances} *arr instance{restoredCounts.instances !== 1 ? 's' : ''}, {restoredCounts.paths} scan path{restoredCounts.paths !== 1 ? 's' : ''}, {restoredCounts.notifications} notification{restoredCounts.notifications !== 1 ? 's' : ''}
                            </span>
                            <span className="ml-auto text-xs text-green-500/70">You can review and modify</span>
                        </div>
                    )}

                    <AnimatePresence mode="wait">
                        {step === 'welcome' && (
                            <WelcomeStep
                                key="welcome"
                                onFreshStart={handleFreshStart}
                                onRestoreComplete={handleRestoreComplete}
                                onSkip={handleDismiss}
                            />
                        )}
                        {step === 'password' && (
                            <PasswordStep
                                key="password"
                                onNext={handlePasswordNext}
                                onBack={() => setStep('welcome')}
                            />
                        )}
                        {step === 'arr' && (
                            <ArrStep
                                key="arr"
                                initialData={arrInitialData}
                                initialTested={arrInitialTested}
                                onNext={handleArrNext}
                                onBack={() => setStep('password')}
                                onSkip={() => setStep('path')}
                            />
                        )}
                        {step === 'path' && (
                            <PathStep
                                key="path"
                                initialData={pathInitialData}
                                rootFolders={rootFolders}
                                loadingRootFolders={loadingRootFolders}
                                arrInstanceId={createdArrId}
                                onNext={handlePathNext}
                                onBack={() => setStep('arr')}
                                onSkip={() => setStep('notifications')}
                            />
                        )}
                        {step === 'notifications' && (
                            <NotificationsStep
                                key="notifications"
                                initialData={notificationInitialData}
                                initialTested={notificationInitialTested}
                                eventGroups={eventGroups}
                                onNext={handleNotificationsNext}
                                onBack={() => setStep('path')}
                                onSkip={() => setStep('complete')}
                            />
                        )}
                        {step === 'complete' && (
                            <CompleteStep
                                key="complete"
                                onComplete={handleComplete}
                            />
                        )}
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
