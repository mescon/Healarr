import { useState } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import { Server, Plus, Trash2, ChevronDown, Pencil, Save, Copy, Activity } from 'lucide-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
    getArrInstances, createArrInstance, updateArrInstance, deleteArrInstance,
    getAPIKey, testArrConnection,
    type ArrInstance
} from '../../lib/api';
import clsx from 'clsx';
import { useToast } from '../../contexts/ToastContext';
import CollapsibleSection from './CollapsibleSection';
import ConfirmDialog from '../ui/ConfirmDialog';

// Server Status Component
const ServerStatus = ({ url, apiKey, isManuallyTesting }: { url: string; apiKey: string; isManuallyTesting?: boolean }) => {
    const { data, isLoading, isError, isFetching } = useQuery({
        queryKey: ['serverStatus', url, apiKey],
        queryFn: () => testArrConnection(url, apiKey),
        retry: false,
        refetchInterval: 600000, // Check every 10 minutes
        refetchOnWindowFocus: true,
        staleTime: 60000, // Show cached result for 1 minute
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

const ArrServersSection = () => {
    const queryClient = useQueryClient();
    const toast = useToast();

    // Local state
    const [isAddExpanded, setIsAddExpanded] = useState(false);
    const [editingId, setEditingId] = useState<number | null>(null);
    const [newArr, setNewArr] = useState<Partial<ArrInstance>>({ type: 'sonarr', enabled: true });
    const [testStatus, setTestStatus] = useState<{ success?: boolean; message?: string }>({});
    const [isTesting, setIsTesting] = useState(false);
    const [manualTestingServer, setManualTestingServer] = useState<string | null>(null);

    // Delete confirmation state
    const [deleteConfirm, setDeleteConfirm] = useState<{ isOpen: boolean; arr: ArrInstance | null }>({
        isOpen: false,
        arr: null
    });

    // Queries
    const { data: arrInstances, isLoading } = useQuery({
        queryKey: ['arrInstances'],
        queryFn: getArrInstances,
    });

    const { data: apiKeyData } = useQuery({
        queryKey: ['apiKey'],
        queryFn: getAPIKey,
    });

    // Mutations
    const createMutation = useMutation({
        mutationFn: createArrInstance,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['arrInstances'] });
            toast.success('Server added successfully');
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: ['serverStatus'] });
            }, 500);
        },
        onError: (error: Error) => {
            toast.error(`Failed to add server: ${error.message}`);
        },
    });

    const updateMutation = useMutation({
        mutationFn: ({ id, data }: { id: number; data: Omit<ArrInstance, 'id'> }) =>
            updateArrInstance(id, data),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['arrInstances'] });
            toast.success('Server updated successfully');
            setTimeout(() => {
                queryClient.invalidateQueries({ queryKey: ['serverStatus'] });
            }, 500);
        },
        onError: (error: Error) => {
            toast.error(`Failed to update server: ${error.message}`);
        },
    });

    const deleteMutation = useMutation({
        mutationFn: deleteArrInstance,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['arrInstances'] });
            toast.success('Server deleted');
            setDeleteConfirm({ isOpen: false, arr: null });
        },
        onError: (error: Error) => {
            toast.error(`Failed to delete server: ${error.message}`);
        },
    });

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
        const testKey = `${arr.url}-${arr.api_key}`;
        setManualTestingServer(testKey);

        try {
            await Promise.all([
                queryClient.refetchQueries({ queryKey: ['serverStatus', arr.url, arr.api_key], exact: true }),
                new Promise(resolve => setTimeout(resolve, 1000))
            ]);
        } finally {
            setTimeout(() => setManualTestingServer(null), 100);
        }
    };

    const handleSubmit = (e: React.FormEvent) => {
        e.preventDefault();

        const missingFields: string[] = [];
        if (!newArr.name?.trim()) missingFields.push('Name');
        if (!newArr.url?.trim()) missingFields.push('URL');
        if (!newArr.api_key?.trim()) missingFields.push('API Key');
        if (!newArr.type) missingFields.push('Type');

        if (missingFields.length > 0) {
            toast.error(`Please fill in required fields: ${missingFields.join(', ')}`);
            return;
        }

        if (editingId) {
            updateMutation.mutate({ id: editingId, data: newArr as Omit<ArrInstance, 'id'> });
            setEditingId(null);
        } else {
            createMutation.mutate(newArr as Omit<ArrInstance, 'id'>);
        }
        resetForm();
    };

    const handleEdit = (arr: ArrInstance) => {
        setNewArr({
            name: arr.name,
            type: arr.type,
            url: arr.url,
            api_key: arr.api_key,
            enabled: arr.enabled
        });
        setEditingId(arr.id);
        setIsAddExpanded(true);
    };

    const resetForm = () => {
        setNewArr({ type: 'sonarr', enabled: true, name: '', url: '', api_key: '' });
        setTestStatus({});
        setIsAddExpanded(false);
        setEditingId(null);
    };

    return (
        <>
            <CollapsibleSection
                id="arr-servers"
                icon={Server}
                iconColor="text-blue-400"
                title="Media Managers"
                subtitle="Connect your Sonarr, Radarr, or other *arr apps"
                defaultExpanded={true}
                delay={0.1}
            >
                {/* Add/Edit Form */}
                <div className="rounded-xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl overflow-hidden">
                    <button
                        onClick={() => {
                            if (!isAddExpanded && editingId) {
                                resetForm();
                            }
                            setIsAddExpanded(!isAddExpanded);
                        }}
                        className="w-full px-6 py-4 flex items-center justify-between hover:bg-slate-100 dark:hover:bg-slate-800/30 transition-colors cursor-pointer"
                    >
                        <div className="flex items-center gap-3">
                            {editingId ? (
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
                                <form onSubmit={handleSubmit} className="px-6 pb-6 space-y-4 border-t border-slate-200 dark:border-slate-800/50 pt-4">
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
                                            {editingId ? (
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

                {/* Servers List */}
                <div className="rounded-xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl overflow-hidden">
                    {isLoading ? (
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
                                        <th className="px-6 py-3 text-left text-xs font-medium text-slate-600 dark:text-slate-400 uppercase cursor-help" title="Add this URL to Sonarr/Radarr's Connect settings to notify Healarr of new imports">Webhook URL</th>
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
                                                        aria-label="Test connection"
                                                    >
                                                        <Activity className="w-4 h-4" aria-hidden="true" />
                                                    </button>
                                                    <button
                                                        onClick={() => handleEdit(arr)}
                                                        className="text-blue-400 hover:text-blue-300 cursor-pointer"
                                                        title="Edit"
                                                        aria-label="Edit server"
                                                    >
                                                        <Pencil className="w-4 h-4" aria-hidden="true" />
                                                    </button>
                                                    <button
                                                        onClick={() => setDeleteConfirm({ isOpen: true, arr })}
                                                        className="p-2 hover:bg-red-500/10 text-red-400 rounded-lg transition-colors cursor-pointer"
                                                        title="Delete Server"
                                                        aria-label="Delete server"
                                                    >
                                                        <Trash2 className="w-4 h-4" aria-hidden="true" />
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
            </CollapsibleSection>

            {/* Delete Confirmation Dialog */}
            <ConfirmDialog
                isOpen={deleteConfirm.isOpen}
                title="Delete Server"
                message={`Are you sure you want to delete "${deleteConfirm.arr?.name}"? This action cannot be undone.`}
                confirmLabel="Delete"
                variant="danger"
                isLoading={deleteMutation.isPending}
                onConfirm={() => {
                    if (deleteConfirm.arr) {
                        deleteMutation.mutate(deleteConfirm.arr.id);
                    }
                }}
                onCancel={() => setDeleteConfirm({ isOpen: false, arr: null })}
            />
        </>
    );
};

export default ArrServersSection;
