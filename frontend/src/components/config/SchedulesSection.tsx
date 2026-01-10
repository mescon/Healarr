import { useState } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import { Clock, Plus, Trash2, ChevronDown } from 'lucide-react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
    getScanPaths, getSchedules, addSchedule, updateSchedule, deleteSchedule
} from '../../lib/api';
import { formatCronExpression } from '../../lib/formatters';
import clsx from 'clsx';
import { useToast } from '../../contexts/ToastContext';
import CollapsibleSection from './CollapsibleSection';
import CronTimeBuilder from './CronTimeBuilder';
import ConfirmDialog from '../ui/ConfirmDialog';

const SchedulesSection = () => {
    const queryClient = useQueryClient();
    const toast = useToast();

    // Local state
    const [isAddExpanded, setIsAddExpanded] = useState(false);
    const [newSchedule, setNewSchedule] = useState<{ scan_path_id: number; cron_expression: string }>({
        scan_path_id: 0,
        cron_expression: '0 3 * * *'
    });
    const [schedulePreset, setSchedulePreset] = useState('daily');

    // Delete confirmation state
    const [deleteConfirm, setDeleteConfirm] = useState<{ isOpen: boolean; scheduleId: number | null }>({
        isOpen: false,
        scheduleId: null
    });

    // Queries
    const { data: schedules, isLoading } = useQuery({
        queryKey: ['schedules'],
        queryFn: getSchedules,
    });

    const { data: scanPaths } = useQuery({
        queryKey: ['scanPaths'],
        queryFn: getScanPaths,
    });

    // Mutations
    const addMutation = useMutation({
        mutationFn: addSchedule,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['schedules'] });
            toast.success('Schedule added successfully');
        },
        onError: (error: Error) => {
            toast.error(`Failed to add schedule: ${error.message}`);
        },
    });

    const updateMutation = useMutation({
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

    const deleteMutation = useMutation({
        mutationFn: deleteSchedule,
        onSuccess: () => {
            queryClient.invalidateQueries({ queryKey: ['schedules'] });
            toast.success('Schedule deleted');
            setDeleteConfirm({ isOpen: false, scheduleId: null });
        },
        onError: (error: unknown) => {
            const err = error as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Failed to delete schedule: ${err.response?.data?.error || err.message}`);
        }
    });

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
                break;
        }
    };

    const handleSubmit = (e: React.FormEvent) => {
        e.preventDefault();
        if (newSchedule.scan_path_id && newSchedule.cron_expression) {
            addMutation.mutate(newSchedule);
            setNewSchedule({ scan_path_id: 0, cron_expression: '0 3 * * *' });
            setIsAddExpanded(false);
        }
    };

    const handleToggle = (schedule: { id: number; enabled: boolean }) => {
        updateMutation.mutate({
            id: schedule.id,
            schedule: { enabled: !schedule.enabled }
        });
    };

    return (
        <>
            <CollapsibleSection
                id="scheduled-scans"
                icon={Clock}
                iconColor="text-purple-400"
                title="Scheduled Scans"
                defaultExpanded={false}
                delay={0.3}
            >
                {/* Add Schedule Form */}
                <div className="rounded-xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl overflow-hidden">
                    <button
                        onClick={() => setIsAddExpanded(!isAddExpanded)}
                        className="w-full px-6 py-4 flex items-center justify-between hover:bg-slate-100 dark:hover:bg-slate-800/30 transition-colors cursor-pointer"
                    >
                        <div className="flex items-center gap-3">
                            <Plus className="w-5 h-5 text-purple-400" />
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Add Schedule</h3>
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
                                                <CronTimeBuilder
                                                    value={newSchedule.cron_expression}
                                                    onChange={(expression) => setNewSchedule({ ...newSchedule, cron_expression: expression })}
                                                />
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
                    {isLoading ? (
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
                                                <div className="text-sm text-slate-600 dark:text-slate-400 mt-0.5 flex items-center gap-2">
                                                    <Clock className="w-3 h-3" />
                                                    <span>{formatCronExpression(schedule.cron_expression)}</span>
                                                    <span className="text-xs font-mono text-slate-500">({schedule.cron_expression})</span>
                                                </div>
                                            </div>
                                        </div>
                                        <div className="flex items-center gap-2">
                                            <button
                                                onClick={() => handleToggle(schedule)}
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
                                                onClick={() => setDeleteConfirm({ isOpen: true, scheduleId: schedule.id })}
                                                className="p-2 text-slate-600 dark:text-slate-400 hover:text-red-400 hover:bg-red-500/10 rounded-lg transition-colors cursor-pointer"
                                                title="Delete Schedule"
                                                aria-label="Delete schedule"
                                            >
                                                <Trash2 className="w-4 h-4" aria-hidden="true" />
                                            </button>
                                        </div>
                                    </div>
                                );
                            })}
                        </div>
                    )}
                </div>
            </CollapsibleSection>

            {/* Delete Confirmation Dialog */}
            <ConfirmDialog
                isOpen={deleteConfirm.isOpen}
                title="Delete Schedule"
                message="Are you sure you want to delete this schedule?"
                confirmLabel="Delete"
                variant="danger"
                isLoading={deleteMutation.isPending}
                onConfirm={() => {
                    if (deleteConfirm.scheduleId) {
                        deleteMutation.mutate(deleteConfirm.scheduleId);
                    }
                }}
                onCancel={() => setDeleteConfirm({ isOpen: false, scheduleId: null })}
            />
        </>
    );
};

export default SchedulesSection;
