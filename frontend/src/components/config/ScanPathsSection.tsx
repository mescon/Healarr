import { useState } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import { FolderOpen, Plus, Trash2, ChevronDown, Pencil, Save, Play, Check, X, Folder, Clock, Info } from 'lucide-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
    getArrInstances, getScanPaths, createScanPath, updateScanPath, deleteScanPath,
    triggerScan, getDetectionPreview, validateScanPath, getSystemInfo,
    type ScanPath
} from '../../lib/api';
import clsx from 'clsx';
import { useToast } from '../../contexts/ToastContext';
import CollapsibleSection from './CollapsibleSection';
import FileBrowser from '../ui/FileBrowser';
import ConfirmDialog from '../ui/ConfirmDialog';

// Path Validation Status Component
const PathValidationStatus = ({ pathId }: { pathId: number }) => {
    const { data, isLoading, refetch, isFetched } = useQuery({
        queryKey: ['pathValidation', pathId],
        queryFn: () => validateScanPath(pathId),
        enabled: false,
        staleTime: 30000,
    });

    if (!isFetched) {
        return (
            <button
                onClick={() => refetch()}
                className="text-slate-400 hover:text-blue-400 transition-colors"
                title="Check path accessibility"
                aria-label="Check path accessibility"
            >
                <Info className="w-4 h-4" aria-hidden="true" />
            </button>
        );
    }

    if (isLoading) {
        return <div className="w-4 h-4 border-2 border-slate-400/30 border-t-slate-400 rounded-full animate-spin" />;
    }

    if (!data?.accessible) {
        return (
            <div
                className="flex items-center gap-1 text-red-400 cursor-help"
                title={data?.error || 'Path not accessible - check permissions and mount status'}
            >
                <X className="w-4 h-4" />
                <span className="text-xs">Error</span>
            </div>
        );
    }

    return (
        <div
            className="flex items-center gap-1 text-green-400 cursor-help"
            title={`${data.file_count.toLocaleString()} media files found${data.sample_files?.length ? '\n\nSamples:\n' + data.sample_files.join('\n') : ''}`}
        >
            <Check className="w-4 h-4" />
            <span className="text-xs">{data.file_count.toLocaleString()}</span>
        </div>
    );
};

interface ScanPathsSectionProps {
    onScrollToDetectionTools?: () => void;
}

const ScanPathsSection = ({ onScrollToDetectionTools }: ScanPathsSectionProps) => {
    const queryClient = useQueryClient();
    const toast = useToast();

    // Local state
    const [isAddExpanded, setIsAddExpanded] = useState(false);
    const [editingId, setEditingId] = useState<number | null>(null);
    const [showFileBrowser, setShowFileBrowser] = useState(false);
    const [fileBrowserTarget, setFileBrowserTarget] = useState<'new' | number>('new');
    const [newPath, setNewPath] = useState<Partial<ScanPath>>({
        enabled: true,
        auto_remediate: true,
        detection_method: 'ffprobe',
        detection_mode: 'quick',
        max_retries: 3,
        verification_timeout_hours: null
    });

    // Delete confirmation state
    const [deleteConfirm, setDeleteConfirm] = useState<{ isOpen: boolean; path: ScanPath | null }>({
        isOpen: false,
        path: null
    });

    // Queries
    const { data: scanPaths, isLoading } = useQuery({
        queryKey: ['scanPaths'],
        queryFn: getScanPaths,
    });

    const { data: arrInstances } = useQuery({
        queryKey: ['arrInstances'],
        queryFn: getArrInstances,
    });

    const { data: systemInfo } = useQuery({
        queryKey: ['systemInfo'],
        queryFn: getSystemInfo,
        staleTime: 60000,
    });

    const { data: detectionPreview, isLoading: isLoadingPreview } = useQuery({
        queryKey: ['detectionPreview', newPath.detection_method || 'ffprobe', newPath.detection_mode || 'quick', newPath.detection_args || ''],
        queryFn: () => getDetectionPreview(
            newPath.detection_method || 'ffprobe',
            newPath.detection_mode || 'quick',
            newPath.detection_args || undefined
        ),
        staleTime: 60000,
    });

    // Check if a detection tool is available
    const isToolAvailable = (method: string): boolean => {
        if (method === 'zero_byte') return true;
        return systemInfo?.tools?.[method]?.available ?? true;
    };

    // Mutations
    const createMutation = useMutation({
        mutationFn: createScanPath,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['scanPaths'] });
            toast.success('Scan path added successfully');
        },
        onError: (error: Error) => {
            toast.error(`Failed to add scan path: ${error.message}`);
        },
    });

    const updateMutation = useMutation({
        mutationFn: ({ id, data }: { id: number; data: Omit<ScanPath, 'id'> }) =>
            updateScanPath(id, data),
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['scanPaths'] });
            toast.success('Scan path updated successfully');
        },
        onError: (error: Error) => {
            toast.error(`Failed to update scan path: ${error.message}`);
        },
    });

    const deleteMutation = useMutation({
        mutationFn: deleteScanPath,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['scanPaths'] });
            toast.success('Scan path deleted');
            setDeleteConfirm({ isOpen: false, path: null });
        },
        onError: (error: Error) => {
            toast.error(`Failed to delete scan path: ${error.message}`);
        },
    });

    const scanMutation = useMutation({
        mutationFn: triggerScan,
        onError: (error: unknown) => {
            const err = error as { response?: { status: number; data?: { error?: string } }; message?: string };
            if (err.response?.status === 409) {
                toast.warning('A scan is already in progress for this path. Please wait for it to complete or cancel it first.');
            } else {
                toast.error(`Failed to start scan: ${err.response?.data?.error || err.message}`);
            }
        },
    });

    const handleSubmit = (e: React.FormEvent) => {
        e.preventDefault();
        if (newPath.local_path && newPath.arr_instance_id) {
            const formData = { ...newPath };
            if (formData.detection_args && typeof formData.detection_args === 'string') {
                formData.detection_args = (formData.detection_args as string)
                    .split(',')
                    .map(arg => arg.trim())
                    .filter(arg => arg.length > 0) as unknown as string;
            }

            if (editingId) {
                updateMutation.mutate({ id: editingId, data: formData as Omit<ScanPath, 'id'> });
                setEditingId(null);
            } else {
                createMutation.mutate(formData as Omit<ScanPath, 'id'>);
            }
            resetForm();
        }
    };

    const handleEdit = (path: ScanPath) => {
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
        setEditingId(path.id);
        setIsAddExpanded(true);
    };

    const resetForm = () => {
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
        setIsAddExpanded(false);
        setEditingId(null);
    };

    return (
        <>
            <CollapsibleSection
                id="scan-paths"
                icon={FolderOpen}
                iconColor="text-green-400"
                title="Scan Paths"
                defaultExpanded={true}
                delay={0.2}
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
                                                    aria-label="Browse for folder"
                                                >
                                                    <Folder className="w-5 h-5 text-slate-600 dark:text-slate-400" aria-hidden="true" />
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

                                    {/* Verification Timeout */}
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
                                            <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                                                Detection Method
                                            </label>
                                            <div className="space-y-2">
                                                {[
                                                    { value: 'ffprobe', label: 'ffprobe', desc: 'Truncated files, missing streams', badge: 'recommended' },
                                                    { value: 'mediainfo', label: 'mediainfo', desc: 'Metadata and track info problems', badge: null },
                                                    { value: 'handbrake', label: 'HandBrakeCLI', desc: 'Files that won\'t transcode', badge: null },
                                                    { value: 'zero_byte', label: 'stat', desc: 'Empty files only', badge: null },
                                                ].map((method) => {
                                                    const available = isToolAvailable(method.value);
                                                    const isSelected = (newPath.detection_method || 'ffprobe') === method.value;
                                                    return (
                                                        <div
                                                            key={method.value}
                                                            onClick={() => {
                                                                if (available) {
                                                                    setNewPath({ ...newPath, detection_method: method.value as 'ffprobe' | 'mediainfo' | 'handbrake' | 'zero_byte' });
                                                                } else {
                                                                    onScrollToDetectionTools?.();
                                                                }
                                                            }}
                                                            className={clsx(
                                                                "flex items-center gap-3 p-3 rounded-lg border cursor-pointer transition-all",
                                                                available && isSelected
                                                                    ? "border-blue-500 bg-blue-50 dark:bg-blue-900/20"
                                                                    : available
                                                                    ? "border-slate-300 dark:border-slate-700 hover:border-blue-300 dark:hover:border-blue-700"
                                                                    : "border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-900/50 opacity-60"
                                                            )}
                                                        >
                                                            <div className={clsx(
                                                                "w-4 h-4 rounded-full border-2 flex items-center justify-center flex-shrink-0",
                                                                available && isSelected
                                                                    ? "border-blue-500"
                                                                    : "border-slate-400 dark:border-slate-600"
                                                            )}>
                                                                {available && isSelected && (
                                                                    <div className="w-2 h-2 rounded-full bg-blue-500" />
                                                                )}
                                                            </div>
                                                            <div className="flex-1 min-w-0">
                                                                <div className="flex items-center gap-2">
                                                                    <span className={clsx(
                                                                        "font-medium text-sm",
                                                                        available
                                                                            ? "text-slate-900 dark:text-white"
                                                                            : "text-slate-500 dark:text-slate-500 line-through"
                                                                    )}>
                                                                        {method.label}
                                                                    </span>
                                                                    {method.badge && available && (
                                                                        <span className={clsx(
                                                                            "text-xs px-1.5 py-0.5 rounded",
                                                                            method.badge === 'recommended'
                                                                                ? "bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-400"
                                                                                : "bg-amber-100 dark:bg-amber-900/30 text-amber-700 dark:text-amber-400"
                                                                        )}>
                                                                            {method.badge}
                                                                        </span>
                                                                    )}
                                                                    {!available && (
                                                                        <span className="text-xs px-1.5 py-0.5 rounded bg-red-100 dark:bg-red-900/30 text-red-600 dark:text-red-400">
                                                                            Not installed
                                                                        </span>
                                                                    )}
                                                                </div>
                                                                <span className={clsx(
                                                                    "text-xs",
                                                                    available
                                                                        ? "text-slate-500 dark:text-slate-400"
                                                                        : "text-slate-400 dark:text-slate-600 line-through"
                                                                )}>
                                                                    {method.desc}
                                                                </span>
                                                            </div>
                                                            {!available && (
                                                                <span className="text-xs text-blue-500 hover:text-blue-600 dark:text-blue-400 dark:hover:text-blue-300 whitespace-nowrap">
                                                                    View installation →
                                                                </span>
                                                            )}
                                                        </div>
                                                    );
                                                })}
                                            </div>
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
                                                    <label htmlFor="mode-quick" className="text-sm text-slate-700 dark:text-slate-300 cursor-pointer">Quick - Header check</label>
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
                                                    <label htmlFor="mode-thorough" className="text-sm text-slate-700 dark:text-slate-300 cursor-pointer">Thorough - Full file decode (slow)</label>
                                                </div>
                                            </div>
                                            <p className="mt-2 text-xs text-slate-500">
                                                <span className="font-semibold">Quick:</span> Checks file headers and stream info. Fast, catches most issues.
                                                <br />
                                                <span className="font-semibold">Thorough:</span> Decodes the entire file to find mid-file corruption. Much slower.
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
                                        {editingId ? (
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

                {/* Paths List */}
                <div className="rounded-xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl overflow-hidden">
                    {isLoading ? (
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
                                            <td className="px-6 py-4">
                                                <div className="flex items-center gap-3">
                                                    <span className="text-slate-700 dark:text-slate-300 font-mono text-sm">{path.local_path}</span>
                                                    <PathValidationStatus pathId={path.id} />
                                                </div>
                                            </td>
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
                                                        onClick={() => scanMutation.mutate(path.id)}
                                                        className="text-green-400 hover:text-green-300 disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
                                                        title="Scan Now"
                                                        aria-label="Scan now"
                                                        disabled={!path.enabled || scanMutation.isPending}
                                                    >
                                                        <Play className="w-4 h-4" aria-hidden="true" />
                                                    </button>
                                                    <button
                                                        onClick={() => handleEdit(path)}
                                                        className="text-blue-400 hover:text-blue-300 cursor-pointer"
                                                        title="Edit"
                                                        aria-label="Edit path"
                                                    >
                                                        <Pencil className="w-4 h-4" aria-hidden="true" />
                                                    </button>
                                                    <button
                                                        onClick={() => setDeleteConfirm({ isOpen: true, path })}
                                                        className="p-2 hover:bg-red-500/10 text-red-400 rounded-lg transition-colors cursor-pointer"
                                                        title="Delete Path"
                                                        aria-label="Delete path"
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
                        <div className="p-8 text-center text-slate-500 italic">No scan paths configured</div>
                    )}
                </div>
            </CollapsibleSection>

            {/* File Browser Modal */}
            <FileBrowser
                isOpen={showFileBrowser}
                onClose={() => setShowFileBrowser(false)}
                onSelect={(selectedPath) => {
                    if (fileBrowserTarget === 'new') {
                        setNewPath({ ...newPath, local_path: selectedPath });
                    } else {
                        const existingPath = scanPaths?.find(p => p.id === fileBrowserTarget);
                        if (existingPath) {
                            const { id: _id, ...pathData } = existingPath;
                            updateMutation.mutate({ id: fileBrowserTarget, data: { ...pathData, local_path: selectedPath } });
                        }
                    }
                }}
                initialPath={fileBrowserTarget === 'new' ? (newPath.local_path || '/') : (scanPaths?.find(p => p.id === fileBrowserTarget)?.local_path || '/')}
            />

            {/* Delete Confirmation Dialog */}
            <ConfirmDialog
                isOpen={deleteConfirm.isOpen}
                title="Remove Scan Path"
                message={`Remove scan path "${deleteConfirm.path?.local_path}"?\n\nThis will remove the path from Healarr scanning only. No files will be deleted from your disk.`}
                confirmLabel="Remove"
                variant="danger"
                isLoading={deleteMutation.isPending}
                onConfirm={() => {
                    if (deleteConfirm.path) {
                        deleteMutation.mutate(deleteConfirm.path.id);
                    }
                }}
                onCancel={() => setDeleteConfirm({ isOpen: false, path: null })}
            />
        </>
    );
};

export default ScanPathsSection;
