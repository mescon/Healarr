import { useState } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import { Bell, Plus, Trash2, ChevronDown, Clock, Send, CheckCircle2, AlertCircle, History, Pencil, X } from 'lucide-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
    getNotifications, createNotification, updateNotification, deleteNotification,
    testNotification, getNotificationEvents, getNotificationLog,
    type NotificationConfig, type NotificationLogEntry
} from '../../lib/api';
import { formatDistanceToNow } from '../../lib/formatters';
import { PROVIDER_CONFIGS, getProviderLabel } from '../../lib/notificationProviders';
import { ProviderSelect, ProviderFields, EventSelector, ProviderIcon } from '../../components/notifications';
import clsx from 'clsx';
import { useToast } from '../../contexts/ToastContext';
import { CollapsibleSection } from '../../components/config';
import ConfirmDialog from '../../components/ui/ConfirmDialog';

interface NotificationFormData {
    name: string;
    provider_type: string;
    config: Record<string, unknown>;
    events: string[];
    enabled: boolean;
    throttle_seconds: number;
}

const defaultFormData: NotificationFormData = {
    name: '',
    provider_type: '',
    config: {},
    events: ['CorruptionDetected', 'ScanComplete'],
    enabled: true,
    throttle_seconds: 300,
};

const NotificationsSection = () => {
    const queryClient = useQueryClient();
    const toast = useToast();

    // Local state
    const [isAddExpanded, setIsAddExpanded] = useState(false);
    const [editingId, setEditingId] = useState<number | null>(null);
    const [formData, setFormData] = useState<NotificationFormData>(defaultFormData);
    const [testResult, setTestResult] = useState<{ success: boolean; message?: string } | null>(null);
    const [isTesting, setIsTesting] = useState(false);

    // Log viewer state
    const [viewingLogId, setViewingLogId] = useState<number | null>(null);

    // Delete confirmation state
    const [deleteConfirm, setDeleteConfirm] = useState<{ isOpen: boolean; notificationId: number | null }>({
        isOpen: false,
        notificationId: null
    });

    // Queries
    const { data: notifications, isLoading } = useQuery({
        queryKey: ['notifications'],
        queryFn: getNotifications,
    });

    const { data: eventGroups } = useQuery({
        queryKey: ['notificationEvents'],
        queryFn: getNotificationEvents,
    });

    const { data: logEntries, isLoading: isLogLoading } = useQuery({
        queryKey: ['notificationLog', viewingLogId],
        queryFn: () => getNotificationLog(viewingLogId!, 20),
        enabled: viewingLogId !== null,
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
        },
    });

    const updateMutation = useMutation({
        mutationFn: ({ id, notification }: { id: number; notification: NotificationConfig }) =>
            updateNotification(id, notification),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['notifications'] });
            toast.success('Notification updated successfully');
            resetForm();
        },
        onError: (error: unknown) => {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to update notification: ${err.response?.data?.error || err.message}`);
        },
    });

    const deleteMutation = useMutation({
        mutationFn: deleteNotification,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['notifications'] });
            toast.success('Notification deleted');
            setDeleteConfirm({ isOpen: false, notificationId: null });
        },
        onError: (error: unknown) => {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to delete notification: ${err.response?.data?.error || err.message}`);
        },
    });

    const toggleMutation = useMutation({
        mutationFn: ({ id, enabled }: { id: number; enabled: boolean }) => {
            const notification = notifications?.find(n => n.id === id);
            if (!notification) throw new Error('Notification not found');
            return updateNotification(id, { ...notification, enabled });
        },
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['notifications'] });
        },
        onError: (error: unknown) => {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to toggle notification: ${err.response?.data?.error || err.message}`);
        },
    });

    // Helpers
    const resetForm = () => {
        setFormData(defaultFormData);
        setEditingId(null);
        setIsAddExpanded(false);
        setTestResult(null);
    };

    const startEdit = (notification: NotificationConfig) => {
        setFormData({
            name: notification.name,
            provider_type: notification.provider_type,
            config: notification.config || {},
            events: notification.events || [],
            enabled: notification.enabled,
            throttle_seconds: notification.throttle_seconds,
        });
        setEditingId(notification.id!);
        setIsAddExpanded(true);
        setTestResult(null);
    };

    const hasProviderConfig = () => {
        if (!formData.provider_type) return false;
        const config = PROVIDER_CONFIGS[formData.provider_type];
        if (!config) return false;
        return config.fields.every(field =>
            formData.config[field.key] !== undefined &&
            formData.config[field.key] !== ''
        );
    };

    const handleTest = async () => {
        setIsTesting(true);
        setTestResult(null);
        try {
            const result = await testNotification({
                name: formData.name || `${getProviderLabel(formData.provider_type)} Test`,
                provider_type: formData.provider_type,
                config: formData.config,
                events: formData.events,
                enabled: true,
                throttle_seconds: 0,
            });
            setTestResult(result);
            if (result.success) {
                toast.success('Test notification sent successfully!');
            } else {
                toast.error(result.error || 'Test notification failed');
            }
        } catch (error: unknown) {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            setTestResult({ success: false, message: err.response?.data?.error || err.message });
            toast.error(`Test failed: ${err.response?.data?.error || err.message}`);
        } finally {
            setIsTesting(false);
        }
    };

    const handleSubmit = (e: React.FormEvent) => {
        e.preventDefault();
        if (!formData.provider_type || !hasProviderConfig()) {
            toast.error('Please fill in all required fields');
            return;
        }

        const notificationData: NotificationConfig = {
            name: formData.name || `${getProviderLabel(formData.provider_type)} Notifications`,
            provider_type: formData.provider_type,
            config: formData.config,
            events: formData.events,
            enabled: formData.enabled,
            throttle_seconds: formData.throttle_seconds,
        };

        if (editingId) {
            updateMutation.mutate({ id: editingId, notification: notificationData });
        } else {
            createMutation.mutate(notificationData);
        }
    };

    return (
        <>
            <CollapsibleSection
                id="notifications"
                icon={Bell}
                iconColor="text-pink-400"
                title="Notifications"
                defaultExpanded={false}
                delay={0.35}
            >
                {/* Add/Edit Notification Form */}
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
                            <Plus className="w-5 h-5 text-pink-400" />
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">
                                {editingId ? 'Edit Notification' : 'Add Notification'}
                            </h3>
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
                                    {/* Provider Selection */}
                                    <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                                        <div>
                                            <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                                                Notification Service
                                            </label>
                                            <ProviderSelect
                                                value={formData.provider_type}
                                                onChange={(provider) => {
                                                    setFormData(prev => ({
                                                        ...prev,
                                                        provider_type: provider,
                                                        config: {},
                                                    }));
                                                    setTestResult(null);
                                                }}
                                                variant="config"
                                            />
                                        </div>
                                        <div>
                                            <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                                                Name (optional)
                                            </label>
                                            <input
                                                type="text"
                                                value={formData.name}
                                                onChange={(e) => setFormData(prev => ({ ...prev, name: e.target.value }))}
                                                className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white focus:ring-2 focus:ring-pink-500"
                                                placeholder={formData.provider_type ? `My ${getProviderLabel(formData.provider_type)} Alerts` : 'My Notifications'}
                                            />
                                        </div>
                                    </div>

                                    {/* Provider Fields */}
                                    {formData.provider_type && (
                                        <ProviderFields
                                            provider={formData.provider_type}
                                            config={formData.config}
                                            onChange={(config) => setFormData(prev => ({ ...prev, config }))}
                                            variant="config"
                                            showHeader={true}
                                        />
                                    )}

                                    {/* Throttle */}
                                    {formData.provider_type && (
                                        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                                            <div>
                                                <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2 flex items-center gap-2">
                                                    <Clock className="w-4 h-4" />
                                                    Throttle (seconds)
                                                </label>
                                                <input
                                                    type="number"
                                                    min="0"
                                                    value={formData.throttle_seconds}
                                                    onChange={(e) => setFormData(prev => ({
                                                        ...prev,
                                                        throttle_seconds: parseInt(e.target.value) || 0
                                                    }))}
                                                    className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white focus:ring-2 focus:ring-pink-500"
                                                />
                                                <p className="text-xs text-slate-500 mt-1">
                                                    Minimum seconds between notifications (0 = no throttling)
                                                </p>
                                            </div>
                                        </div>
                                    )}

                                    {/* Event Selection */}
                                    {formData.provider_type && (
                                        <EventSelector
                                            events={formData.events}
                                            eventGroups={eventGroups}
                                            onChange={(events) => setFormData(prev => ({ ...prev, events }))}
                                            variant="config"
                                        />
                                    )}

                                    {/* Test Result */}
                                    {testResult && (
                                        <div className={clsx(
                                            "p-3 rounded-xl flex items-center gap-2",
                                            testResult.success
                                                ? "bg-green-500/10 border border-green-500/20 text-green-600 dark:text-green-400"
                                                : "bg-red-500/10 border border-red-500/20 text-red-600 dark:text-red-400"
                                        )}>
                                            {testResult.success ? (
                                                <CheckCircle2 className="w-5 h-5" />
                                            ) : (
                                                <AlertCircle className="w-5 h-5" />
                                            )}
                                            <span className="text-sm">
                                                {testResult.success ? 'Test notification sent!' : testResult.message}
                                            </span>
                                        </div>
                                    )}

                                    {/* Actions */}
                                    <div className="flex items-center gap-3 flex-wrap">
                                        {formData.provider_type && (
                                            <button
                                                type="button"
                                                onClick={handleTest}
                                                disabled={isTesting || !hasProviderConfig()}
                                                className="flex items-center gap-2 px-4 py-2 border border-purple-500 text-purple-600 dark:text-purple-400 rounded-lg hover:bg-purple-50 dark:hover:bg-purple-900/20 transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
                                            >
                                                {isTesting ? (
                                                    <div className="w-4 h-4 border-2 border-purple-500/30 border-t-purple-500 rounded-full animate-spin" />
                                                ) : (
                                                    <Send className="w-4 h-4" />
                                                )}
                                                Test
                                            </button>
                                        )}
                                        <button
                                            type="submit"
                                            disabled={!formData.provider_type || !hasProviderConfig() || createMutation.isPending || updateMutation.isPending}
                                            className="flex items-center gap-2 px-4 py-2 bg-pink-500 hover:bg-pink-600 text-white rounded-lg transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
                                        >
                                            <Plus className="w-4 h-4" />
                                            {editingId ? 'Update Notification' : 'Add Notification'}
                                        </button>
                                        {editingId && (
                                            <button
                                                type="button"
                                                onClick={resetForm}
                                                className="flex items-center gap-2 px-4 py-2 text-slate-600 dark:text-slate-400 hover:text-slate-900 dark:hover:text-white transition-colors cursor-pointer"
                                            >
                                                <X className="w-4 h-4" />
                                                Cancel
                                            </button>
                                        )}
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
                    ) : notifications?.length === 0 ? (
                        <div className="p-8 text-center text-slate-500 italic">No notifications configured</div>
                    ) : (
                        <div className="divide-y divide-slate-200 dark:divide-slate-800/50">
                            {notifications?.map(notification => (
                                <div key={notification.id} className="p-4 hover:bg-slate-100 dark:hover:bg-slate-800/30 transition-colors">
                                    <div className="flex items-center justify-between">
                                        <div className="flex items-center gap-4">
                                            <div className={clsx(
                                                "w-2 h-2 rounded-full",
                                                notification.enabled ? "bg-green-500 shadow-[0_0_8px_rgba(34,197,94,0.5)]" : "bg-slate-600"
                                            )} />
                                            <div className="flex items-center gap-2">
                                                <ProviderIcon provider={notification.provider_type} className="w-5 h-5" />
                                                <div>
                                                    <div className="font-medium text-slate-900 dark:text-white">
                                                        {notification.name}
                                                    </div>
                                                    <div className="text-sm text-slate-600 dark:text-slate-400 flex items-center gap-2">
                                                        <span>{getProviderLabel(notification.provider_type)}</span>
                                                        <span className="text-xs text-slate-500">
                                                            ({notification.events?.length || 0} events)
                                                        </span>
                                                        {notification.throttle_seconds > 0 && (
                                                            <span className="text-xs text-slate-500 flex items-center gap-1">
                                                                <Clock className="w-3 h-3" />
                                                                {notification.throttle_seconds}s
                                                            </span>
                                                        )}
                                                    </div>
                                                </div>
                                            </div>
                                        </div>
                                        <div className="flex items-center gap-2">
                                            <button
                                                onClick={() => toggleMutation.mutate({ id: notification.id!, enabled: !notification.enabled })}
                                                className={clsx(
                                                    "px-3 py-1.5 rounded-lg text-xs font-medium transition-colors border cursor-pointer",
                                                    notification.enabled
                                                        ? "bg-green-500/10 text-green-600 dark:text-green-400 border-green-500/20 hover:bg-green-500/20"
                                                        : "bg-slate-200 dark:bg-slate-800 text-slate-600 dark:text-slate-400 border-slate-300 dark:border-slate-700 hover:bg-slate-300 dark:hover:bg-slate-700"
                                                )}
                                            >
                                                {notification.enabled ? 'Enabled' : 'Disabled'}
                                            </button>
                                            <button
                                                onClick={() => setViewingLogId(viewingLogId === notification.id ? null : notification.id!)}
                                                className="p-2 text-slate-600 dark:text-slate-400 hover:text-blue-500 hover:bg-blue-500/10 rounded-lg transition-colors cursor-pointer"
                                                title="View Log"
                                                aria-label="View notification log"
                                            >
                                                <History className="w-4 h-4" aria-hidden="true" />
                                            </button>
                                            <button
                                                onClick={() => startEdit(notification)}
                                                className="p-2 text-slate-600 dark:text-slate-400 hover:text-pink-500 hover:bg-pink-500/10 rounded-lg transition-colors cursor-pointer"
                                                title="Edit"
                                                aria-label="Edit notification"
                                            >
                                                <Pencil className="w-4 h-4" aria-hidden="true" />
                                            </button>
                                            <button
                                                onClick={() => setDeleteConfirm({ isOpen: true, notificationId: notification.id! })}
                                                className="p-2 text-slate-600 dark:text-slate-400 hover:text-red-400 hover:bg-red-500/10 rounded-lg transition-colors cursor-pointer"
                                                title="Delete"
                                                aria-label="Delete notification"
                                            >
                                                <Trash2 className="w-4 h-4" aria-hidden="true" />
                                            </button>
                                        </div>
                                    </div>

                                    {/* Log Viewer */}
                                    <AnimatePresence>
                                        {viewingLogId === notification.id && (
                                            <motion.div
                                                initial={{ height: 0, opacity: 0 }}
                                                animate={{ height: "auto", opacity: 1 }}
                                                exit={{ height: 0, opacity: 0 }}
                                                transition={{ duration: 0.2 }}
                                                className="mt-4"
                                            >
                                                <div className="rounded-lg border border-slate-200 dark:border-slate-700 bg-slate-50 dark:bg-slate-800/50 p-4">
                                                    <h4 className="text-sm font-medium text-slate-700 dark:text-slate-300 mb-3 flex items-center gap-2">
                                                        <History className="w-4 h-4" />
                                                        Recent Activity
                                                    </h4>
                                                    {isLogLoading ? (
                                                        <div className="text-sm text-slate-500">Loading...</div>
                                                    ) : logEntries?.length === 0 ? (
                                                        <div className="text-sm text-slate-500 italic">No notifications sent yet</div>
                                                    ) : (
                                                        <div className="space-y-2 max-h-48 overflow-y-auto">
                                                            {logEntries?.map((entry: NotificationLogEntry) => (
                                                                <div key={entry.id} className="flex items-start gap-2 text-sm">
                                                                    <div className={clsx(
                                                                        "w-2 h-2 rounded-full mt-1.5 flex-shrink-0",
                                                                        entry.status === 'sent' ? "bg-green-500" : "bg-red-500"
                                                                    )} />
                                                                    <div className="flex-1 min-w-0">
                                                                        <div className="flex items-center gap-2">
                                                                            <span className="text-slate-900 dark:text-white font-medium">
                                                                                {entry.event_type}
                                                                            </span>
                                                                            <span className="text-xs text-slate-500">
                                                                                {formatDistanceToNow(entry.sent_at)}
                                                                            </span>
                                                                        </div>
                                                                        {entry.error && (
                                                                            <div className="text-xs text-red-500 mt-0.5">{entry.error}</div>
                                                                        )}
                                                                    </div>
                                                                </div>
                                                            ))}
                                                        </div>
                                                    )}
                                                </div>
                                            </motion.div>
                                        )}
                                    </AnimatePresence>
                                </div>
                            ))}
                        </div>
                    )}
                </div>
            </CollapsibleSection>

            {/* Delete Confirmation Dialog */}
            <ConfirmDialog
                isOpen={deleteConfirm.isOpen}
                title="Delete Notification"
                message="Are you sure you want to delete this notification? You will no longer receive alerts from this service."
                confirmLabel="Delete"
                variant="danger"
                isLoading={deleteMutation.isPending}
                onConfirm={() => {
                    if (deleteConfirm.notificationId) {
                        deleteMutation.mutate(deleteConfirm.notificationId);
                    }
                }}
                onCancel={() => setDeleteConfirm({ isOpen: false, notificationId: null })}
            />
        </>
    );
};

export default NotificationsSection;
