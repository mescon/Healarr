import { useState } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Bookmark, Plus, Trash2, Pencil, Save, X, Lock } from 'lucide-react';
import {
    getScanPresets,
    createScanPreset,
    updateScanPreset,
    deleteScanPreset,
    type ScanPreset,
} from '../../lib/api';
import { useToast } from '../../contexts/ToastContext';
import ConfirmDialog from '../ui/ConfirmDialog';

type PresetDraft = Omit<ScanPreset, 'id' | 'is_builtin'>;

const HWACCEL_OPTIONS: ScanPreset['hwaccel'][] = [
    null,
    'auto',
    'off',
    'cuda',
    'vaapi',
    'qsv',
    'videotoolbox',
    'vdpau',
    'drm',
];

const emptyDraft = (): PresetDraft => ({
    name: '',
    description: '',
    detection_method: 'ffprobe',
    detection_mode: 'quick',
    detection_args: null,
    thorough_duration_seconds: null,
    thorough_timeout_seconds: null,
    hwaccel: null,
});

/**
 * Lists every scan preset and lets the operator create, edit, or delete
 * custom ones. Built-in presets (is_builtin) render with a lock icon and
 * disabled edit/delete - migration 009 owns them, the handler returns 403
 * for any mutation attempt. The UI mirrors that contract so the operator
 * never sees a server-side rejection they could have predicted.
 */
const PresetsSection = () => {
    const toast = useToast();
    const queryClient = useQueryClient();

    const { data: presets, isLoading } = useQuery({
        queryKey: ['scanPresets'],
        queryFn: getScanPresets,
    });

    const [isAddOpen, setIsAddOpen] = useState(false);
    const [editingId, setEditingId] = useState<number | null>(null);
    const [draft, setDraft] = useState<PresetDraft>(emptyDraft());
    const [deleteConfirm, setDeleteConfirm] = useState<{ open: boolean; preset: ScanPreset | null }>({
        open: false,
        preset: null,
    });

    const createMutation = useMutation({
        mutationFn: createScanPreset,
        onSuccess: () => {
            toast.success('Preset created');
            void queryClient.invalidateQueries({ queryKey: ['scanPresets'] });
            resetForm();
        },
        onError: (err: unknown) => toast.error(extractError(err, 'Failed to create preset')),
    });

    const updateMutation = useMutation({
        mutationFn: ({ id, data }: { id: number; data: PresetDraft }) => updateScanPreset(id, data),
        onSuccess: () => {
            toast.success('Preset updated');
            void queryClient.invalidateQueries({ queryKey: ['scanPresets'] });
            resetForm();
        },
        onError: (err: unknown) => toast.error(extractError(err, 'Failed to update preset')),
    });

    const deleteMutation = useMutation({
        mutationFn: (id: number) => deleteScanPreset(id),
        onSuccess: () => {
            toast.success('Preset deleted');
            void queryClient.invalidateQueries({ queryKey: ['scanPresets'] });
        },
        onError: (err: unknown) => toast.error(extractError(err, 'Failed to delete preset')),
    });

    const resetForm = () => {
        setDraft(emptyDraft());
        setIsAddOpen(false);
        setEditingId(null);
    };

    const startEdit = (p: ScanPreset) => {
        setDraft({
            name: p.name,
            description: p.description,
            detection_method: p.detection_method,
            detection_mode: p.detection_mode,
            detection_args: p.detection_args,
            thorough_duration_seconds: p.thorough_duration_seconds,
            thorough_timeout_seconds: p.thorough_timeout_seconds,
            hwaccel: p.hwaccel,
        });
        setEditingId(p.id);
        setIsAddOpen(true);
    };

    const handleSubmit = (e: React.FormEvent) => {
        e.preventDefault();
        if (!draft.name.trim()) {
            toast.error('Name is required');
            return;
        }
        if (editingId !== null) {
            updateMutation.mutate({ id: editingId, data: draft });
        } else {
            createMutation.mutate(draft);
        }
    };

    const handleDelete = (p: ScanPreset) => {
        if (p.is_builtin) return;
        setDeleteConfirm({ open: true, preset: p });
    };

    return (
        <div className="space-y-4">
            <div className="flex items-center gap-3">
                <div className="p-2 rounded-lg bg-indigo-500/10 border border-indigo-500/20">
                    <Bookmark className="w-5 h-5 text-indigo-400" />
                </div>
                <div className="flex-1">
                    <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Scan presets</h3>
                    <p className="text-sm text-slate-600 dark:text-slate-400">
                        Named bundles of scan settings you can apply to a scan path with one click.
                        Built-in presets are read-only; create your own custom ones below.
                    </p>
                </div>
                {!isAddOpen && (
                    <button
                        type="button"
                        onClick={() => { resetForm(); setIsAddOpen(true); }}
                        className="inline-flex items-center gap-2 px-3 py-1.5 rounded-lg bg-indigo-600 text-white text-sm hover:bg-indigo-700"
                    >
                        <Plus className="w-4 h-4" />
                        Custom preset
                    </button>
                )}
            </div>

            {isLoading && (
                <p className="text-sm text-slate-500">Loading presets…</p>
            )}

            <AnimatePresence>
                {isAddOpen && (
                    <motion.form
                        initial={{ opacity: 0, height: 0 }}
                        animate={{ opacity: 1, height: 'auto' }}
                        exit={{ opacity: 0, height: 0 }}
                        onSubmit={handleSubmit}
                        className="space-y-3 bg-slate-50 dark:bg-slate-900/60 border border-slate-200 dark:border-slate-800 rounded-lg p-4"
                    >
                        <div className="grid grid-cols-2 gap-3">
                            <div>
                                <label htmlFor="preset-name" className="block text-xs font-medium text-slate-700 dark:text-slate-300 mb-1">Name *</label>
                                <input
                                    id="preset-name"
                                    type="text"
                                    value={draft.name}
                                    onChange={(e) => setDraft({ ...draft, name: e.target.value })}
                                    className="w-full px-3 py-1.5 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded text-sm"
                                    required
                                />
                            </div>
                            <div>
                                <label htmlFor="preset-method" className="block text-xs font-medium text-slate-700 dark:text-slate-300 mb-1">Detection method</label>
                                <select
                                    id="preset-method"
                                    value={draft.detection_method}
                                    onChange={(e) => setDraft({ ...draft, detection_method: e.target.value as ScanPreset['detection_method'] })}
                                    className="w-full px-3 py-1.5 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded text-sm"
                                >
                                    <option value="zero_byte">zero_byte</option>
                                    <option value="ffprobe">ffprobe</option>
                                    <option value="mediainfo">mediainfo</option>
                                    <option value="handbrake">handbrake</option>
                                </select>
                            </div>
                        </div>

                        <div>
                            <label htmlFor="preset-desc" className="block text-xs font-medium text-slate-700 dark:text-slate-300 mb-1">Description</label>
                            <input
                                id="preset-desc"
                                type="text"
                                value={draft.description}
                                onChange={(e) => setDraft({ ...draft, description: e.target.value })}
                                className="w-full px-3 py-1.5 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded text-sm"
                            />
                        </div>

                        <div className="grid grid-cols-3 gap-3">
                            <div>
                                <label htmlFor="preset-mode" className="block text-xs font-medium text-slate-700 dark:text-slate-300 mb-1">Mode</label>
                                <select
                                    id="preset-mode"
                                    value={draft.detection_mode}
                                    onChange={(e) => setDraft({ ...draft, detection_mode: e.target.value as ScanPreset['detection_mode'] })}
                                    className="w-full px-3 py-1.5 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded text-sm"
                                >
                                    <option value="quick">quick</option>
                                    <option value="thorough">thorough</option>
                                </select>
                            </div>
                            <div>
                                <label htmlFor="preset-duration" className="block text-xs font-medium text-slate-700 dark:text-slate-300 mb-1">Thorough duration (sec)</label>
                                <input
                                    id="preset-duration"
                                    type="number"
                                    value={draft.thorough_duration_seconds ?? ''}
                                    placeholder="inherit"
                                    onChange={(e) => setDraft({ ...draft, thorough_duration_seconds: e.target.value === '' ? null : parseInt(e.target.value) })}
                                    className="w-full px-3 py-1.5 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded text-sm"
                                />
                            </div>
                            <div>
                                <label htmlFor="preset-timeout" className="block text-xs font-medium text-slate-700 dark:text-slate-300 mb-1">Thorough timeout (sec)</label>
                                <input
                                    id="preset-timeout"
                                    type="number"
                                    value={draft.thorough_timeout_seconds ?? ''}
                                    placeholder="inherit"
                                    onChange={(e) => setDraft({ ...draft, thorough_timeout_seconds: e.target.value === '' ? null : parseInt(e.target.value) })}
                                    className="w-full px-3 py-1.5 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded text-sm"
                                />
                            </div>
                        </div>

                        <div>
                            <label htmlFor="preset-hwaccel" className="block text-xs font-medium text-slate-700 dark:text-slate-300 mb-1">Hardware acceleration</label>
                            <select
                                id="preset-hwaccel"
                                value={draft.hwaccel ?? ''}
                                onChange={(e) => setDraft({ ...draft, hwaccel: e.target.value === '' ? null : (e.target.value as NonNullable<ScanPreset['hwaccel']>) })}
                                className="w-full px-3 py-1.5 bg-white dark:bg-slate-800 border border-slate-300 dark:border-slate-700 rounded text-sm"
                            >
                                {HWACCEL_OPTIONS.map((v) => (
                                    <option key={v ?? 'inherit'} value={v ?? ''}>
                                        {v ?? 'inherit global'}
                                    </option>
                                ))}
                            </select>
                        </div>

                        <div className="flex items-center gap-2">
                            <button
                                type="submit"
                                disabled={createMutation.isPending || updateMutation.isPending}
                                className="inline-flex items-center gap-2 px-3 py-1.5 rounded bg-indigo-600 text-white text-sm hover:bg-indigo-700 disabled:opacity-50"
                            >
                                <Save className="w-4 h-4" />
                                {editingId !== null ? 'Save changes' : 'Create preset'}
                            </button>
                            <button
                                type="button"
                                onClick={resetForm}
                                className="inline-flex items-center gap-2 px-3 py-1.5 rounded border border-slate-300 dark:border-slate-700 text-sm text-slate-700 dark:text-slate-300"
                            >
                                <X className="w-4 h-4" />
                                Cancel
                            </button>
                        </div>
                    </motion.form>
                )}
            </AnimatePresence>

            <div className="space-y-2">
                {(presets ?? []).map((p) => (
                    <div
                        key={p.id}
                        className="flex items-start gap-3 p-3 rounded-lg border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900"
                    >
                        <div className="flex-1">
                            <div className="flex items-center gap-2">
                                <h4 className="text-sm font-semibold text-slate-900 dark:text-slate-100">{p.name}</h4>
                                {p.is_builtin && (
                                    <span className="inline-flex items-center gap-1 px-1.5 py-0.5 rounded text-[10px] bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-400">
                                        <Lock className="w-2.5 h-2.5" />
                                        built-in
                                    </span>
                                )}
                            </div>
                            {p.description && (
                                <p className="text-xs text-slate-500 dark:text-slate-400 mt-0.5">{p.description}</p>
                            )}
                            <p className="text-[11px] text-slate-400 dark:text-slate-500 mt-1 font-mono">
                                {p.detection_method}/{p.detection_mode}
                                {p.thorough_duration_seconds !== null && ` · duration=${p.thorough_duration_seconds}s`}
                                {p.thorough_timeout_seconds !== null && ` · timeout=${p.thorough_timeout_seconds}s`}
                                {p.hwaccel && ` · hwaccel=${p.hwaccel}`}
                            </p>
                        </div>
                        <div className="flex items-center gap-1">
                            <button
                                type="button"
                                disabled={p.is_builtin}
                                onClick={() => startEdit(p)}
                                title={p.is_builtin ? 'Built-in presets cannot be edited' : 'Edit'}
                                className="p-1.5 rounded text-slate-500 hover:text-slate-700 dark:hover:text-slate-200 disabled:opacity-30 disabled:cursor-not-allowed"
                            >
                                <Pencil className="w-4 h-4" />
                            </button>
                            <button
                                type="button"
                                disabled={p.is_builtin}
                                onClick={() => handleDelete(p)}
                                title={p.is_builtin ? 'Built-in presets cannot be deleted' : 'Delete'}
                                className="p-1.5 rounded text-red-500 hover:text-red-700 disabled:opacity-30 disabled:cursor-not-allowed"
                            >
                                <Trash2 className="w-4 h-4" />
                            </button>
                        </div>
                    </div>
                ))}
            </div>

            <ConfirmDialog
                isOpen={deleteConfirm.open}
                title="Delete preset?"
                message={`Delete the preset "${deleteConfirm.preset?.name ?? ''}"? Scan paths that previously used it keep their resolved field values (this only removes the named bundle).`}
                confirmLabel="Delete"
                onConfirm={() => {
                    if (deleteConfirm.preset) deleteMutation.mutate(deleteConfirm.preset.id);
                    setDeleteConfirm({ open: false, preset: null });
                }}
                onCancel={() => setDeleteConfirm({ open: false, preset: null })}
            />
        </div>
    );
};

const extractError = (err: unknown, fallback: string): string => {
    const e = err as { response?: { data?: { error?: string } }; message?: string };
    return e.response?.data?.error ?? e.message ?? fallback;
};

export default PresetsSection;
