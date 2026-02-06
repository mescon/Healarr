import { useState } from 'react';
import { motion } from 'framer-motion';
import { ArrowRight, ArrowLeft, AlertCircle, CheckCircle2, Bell, Send, Clock } from 'lucide-react';
import { createNotification, testNotification, type NotificationConfig } from '../../lib/api';
import { ProviderSelect, ProviderFields, EventSelector } from '../notifications';
import { PROVIDER_CONFIGS, getProviderLabel } from '../../lib/notificationProviders';
import type { NotificationsStepProps } from './types';
import { DEFAULT_NOTIFICATION_DATA, type NotificationFormData } from './types';

export default function NotificationsStep({ initialData, initialTested, eventGroups, onNext, onBack, onSkip }: NotificationsStepProps) {
    const [notificationData, setNotificationData] = useState<NotificationFormData>(initialData ?? DEFAULT_NOTIFICATION_DATA);
    const [notificationTested, setNotificationTested] = useState(initialTested ?? false);
    const [notificationTestResult, setNotificationTestResult] = useState<{ success: boolean; message?: string } | null>(null);
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState('');

    const hasProviderConfig = () => {
        if (notificationData.provider_type === 'none') return false;
        const providerConfig = PROVIDER_CONFIGS[notificationData.provider_type];
        if (!providerConfig) return false;
        return providerConfig.fields.some(field =>
            notificationData.config[field.key] !== undefined &&
            notificationData.config[field.key] !== ''
        );
    };

    const handleTest = async () => {
        if (notificationData.provider_type === 'none') return;

        setLoading(true);
        setNotificationTestResult(null);

        try {
            const name = notificationData.name || `${getProviderLabel(notificationData.provider_type)} Notification`;

            const config: NotificationConfig = {
                name,
                provider_type: notificationData.provider_type,
                config: notificationData.config,
                events: notificationData.events,
                enabled: notificationData.enabled,
                throttle_seconds: notificationData.throttle_seconds,
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

    const handleCreate = async () => {
        if (notificationData.provider_type === 'none') {
            onNext();
            return;
        }

        setLoading(true);
        setError('');

        try {
            const name = notificationData.name || `${getProviderLabel(notificationData.provider_type)} Notification`;

            await createNotification({
                name,
                provider_type: notificationData.provider_type,
                config: notificationData.config,
                events: notificationData.events,
                enabled: notificationData.enabled,
                throttle_seconds: notificationData.throttle_seconds,
            });
            onNext();
        } catch (err: unknown) {
            const error = err as { response?: { data?: { error?: string } } };
            setError(error.response?.data?.error || 'Failed to create notification');
        } finally {
            setLoading(false);
        }
    };

    return (
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

            {/* Provider Selection */}
            <div className="space-y-2">
                <label htmlFor="notification-provider" className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                    Notification Service
                </label>
                <ProviderSelect
                    value={notificationData.provider_type}
                    onChange={(provider) => {
                        setNotificationData(prev => ({
                            ...prev,
                            provider_type: provider,
                            config: {},
                            name: '',
                        }));
                        setNotificationTested(false);
                        setNotificationTestResult(null);
                    }}
                    variant="wizard"
                    includeNone={true}
                    noneLabel="Skip (set up later)"
                />
            </div>

            {notificationData.provider_type !== 'none' && (
                <motion.div
                    initial={{ opacity: 0, height: 0 }}
                    animate={{ opacity: 1, height: 'auto' }}
                    className="space-y-6"
                >
                    {/* Notification Name */}
                    <div className="space-y-2">
                        <label htmlFor="notification-name" className="block text-sm font-medium text-slate-700 dark:text-slate-300">
                            Name (optional)
                        </label>
                        <input
                            id="notification-name"
                            type="text"
                            value={notificationData.name}
                            onChange={(e) => setNotificationData(prev => ({ ...prev, name: e.target.value }))}
                            className="w-full px-4 py-3 rounded-xl bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 focus:ring-2 focus:ring-green-500/50 focus:border-green-500 transition-colors text-slate-900 dark:text-white placeholder-slate-500"
                            placeholder={`My ${getProviderLabel(notificationData.provider_type)} Alerts`}
                        />
                    </div>

                    {/* Provider-specific fields */}
                    <ProviderFields
                        provider={notificationData.provider_type}
                        config={notificationData.config}
                        onChange={(config) => setNotificationData(prev => ({ ...prev, config }))}
                        variant="wizard"
                        showHeader={true}
                    />

                    {/* Throttle */}
                    <div className="space-y-2">
                        <label htmlFor="notification-throttle" className="block text-sm font-medium text-slate-700 dark:text-slate-300 flex items-center gap-2">
                            <Clock className="w-4 h-4" />
                            Throttle (seconds)
                        </label>
                        <input
                            id="notification-throttle"
                            type="number"
                            min="0"
                            value={notificationData.throttle_seconds}
                            onChange={(e) => setNotificationData(prev => ({
                                ...prev,
                                throttle_seconds: parseInt(e.target.value) || 0
                            }))}
                            className="w-full px-4 py-3 rounded-xl bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 focus:ring-2 focus:ring-green-500/50 focus:border-green-500 transition-colors text-slate-900 dark:text-white"
                        />
                        <p className="text-xs text-slate-500">
                            Minimum seconds between notifications (0 = no throttling)
                        </p>
                    </div>

                    {/* Event Selection */}
                    <EventSelector
                        events={notificationData.events}
                        eventGroups={eventGroups}
                        onChange={(events) => setNotificationData(prev => ({ ...prev, events }))}
                        variant="wizard"
                        defaultCollapsed={true}
                        title="Events to Notify"
                    />

                    {/* Test result */}
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

                    {/* Test button */}
                    <button
                        onClick={handleTest}
                        disabled={loading || !hasProviderConfig()}
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
                    onClick={onBack}
                    className="px-4 py-3 rounded-xl border border-slate-300 dark:border-slate-600 text-slate-700 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 transition-colors flex items-center gap-2 cursor-pointer"
                >
                    <ArrowLeft className="w-4 h-4" />
                    Back
                </button>
                <button
                    onClick={handleCreate}
                    disabled={loading || (notificationData.provider_type !== 'none' && !notificationTested)}
                    className="flex-1 py-3 px-4 bg-gradient-to-r from-purple-500 to-pink-600 hover:from-purple-600 hover:to-pink-700 text-white font-semibold rounded-xl transition-all shadow-lg shadow-purple-500/20 flex items-center justify-center gap-2 disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
                >
                    {loading ? (
                        <div className="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                    ) : (
                        <>
                            <span>{notificationData.provider_type === 'none' ? 'Skip & Continue' : 'Add Notification'}</span>
                            <ArrowRight className="w-5 h-5" />
                        </>
                    )}
                </button>
            </div>

            <button
                onClick={onSkip}
                className="w-full text-sm text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-300 transition-colors cursor-pointer"
            >
                Skip for now
            </button>
        </motion.div>
    );
}
