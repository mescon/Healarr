import { useMemo } from 'react';
import { X, FileCheck, FileX, SkipForward, ShieldAlert, Cpu, Activity, Clock, ArrowRight, HelpCircle, HardDrive } from 'lucide-react';
import clsx from 'clsx';
import { motion } from 'framer-motion';
import type { ScanFile, CheckDetails } from '../lib/api';
import { formatBytes } from '../lib/formatters';
import { useModalA11y } from '../hooks/useModalA11y';
import { useDateFormat } from '../lib/useDateFormat';

interface ScanFileDetailProps {
    file: ScanFile;
    onClose: () => void;
    // Opens the remediation journey for a corrupt file that has an aggregate.
    onViewJourney?: (corruptionId: string) => void;
}

const statusMeta: Record<string, { label: string; icon: typeof FileCheck; cls: string }> = {
    healthy: { label: 'Healthy', icon: FileCheck, cls: 'text-emerald-400 bg-emerald-500/10 border-emerald-500/20' },
    corrupt: { label: 'Corrupt', icon: FileX, cls: 'text-red-400 bg-red-500/10 border-red-500/20' },
    error: { label: 'Corrupt', icon: FileX, cls: 'text-red-400 bg-red-500/10 border-red-500/20' },
    skipped: { label: 'Skipped', icon: SkipForward, cls: 'text-amber-400 bg-amber-500/10 border-amber-500/20' },
    inaccessible: { label: 'Inaccessible', icon: ShieldAlert, cls: 'text-orange-400 bg-orange-500/10 border-orange-500/20' },
};

// outcomeClass colours a check outcome line: green for passed, amber for a
// fail-safe skip, red for a flag/failure, slate for "not run".
const outcomeClass = (outcome: string): string => {
    if (outcome.startsWith('passed')) return 'text-emerald-400';
    if (outcome.startsWith('skipped') || outcome.startsWith('not run')) return 'text-amber-400';
    if (outcome.startsWith('flagged') || outcome.startsWith('failed')) return 'text-red-400';
    return 'text-slate-400';
};

const formatMs = (ms?: number): string => {
    if (ms === undefined || ms < 0) return '';
    if (ms < 1000) return `${ms} ms`;
    return `${(ms / 1000).toFixed(1)} s`;
};

const DetailRow = ({ icon: Icon, label, value, valueClass }: { icon: typeof Cpu; label: string; value: string; valueClass?: string }) => (
    <div className="flex items-start gap-2.5 py-1.5">
        <Icon className="w-4 h-4 text-slate-500 mt-0.5 flex-shrink-0" />
        <span className="text-sm text-slate-500 w-32 flex-shrink-0">{label}</span>
        <span className={clsx('text-sm font-medium', valueClass || 'text-slate-700 dark:text-slate-200')}>{value}</span>
    </div>
);

const ScanFileDetail = ({ file, onClose, onViewJourney }: ScanFileDetailProps) => {
    const ref = useModalA11y<HTMLDivElement>(true, onClose);
    const { formatTime } = useDateFormat();

    const details = useMemo<CheckDetails | null>(() => {
        if (!file.check_details) return null;
        try {
            return JSON.parse(file.check_details) as CheckDetails;
        } catch {
            return null;
        }
    }, [file.check_details]);

    const meta = statusMeta[file.status] ?? statusMeta.skipped;
    const StatusIcon = meta.icon;
    const fileName = file.file_path.split('/').pop() || file.file_path;

    return (
        <div className="fixed inset-0 z-50 flex items-center justify-center p-4 bg-black/60 backdrop-blur-sm" onClick={onClose}>
            <motion.div
                ref={ref}
                tabIndex={-1}
                role="dialog"
                aria-modal="true"
                aria-label={`Scan details for ${fileName}`}
                initial={{ opacity: 0, scale: 0.96 }}
                animate={{ opacity: 1, scale: 1 }}
                className="w-full max-w-2xl max-h-[85vh] overflow-y-auto rounded-2xl border border-slate-200 dark:border-slate-800 bg-white dark:bg-slate-900 shadow-2xl"
                onClick={(e) => e.stopPropagation()}
            >
                {/* Header */}
                <div className="flex items-start justify-between gap-4 p-6 border-b border-slate-200 dark:border-slate-800">
                    <div className="min-w-0">
                        <div className="flex items-center gap-2 mb-2">
                            <span className={clsx('inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs font-medium border', meta.cls)}>
                                <StatusIcon className="w-3.5 h-3.5" />
                                {meta.label}
                            </span>
                        </div>
                        <h2 className="text-lg font-semibold text-slate-900 dark:text-white truncate" title={file.file_path}>{fileName}</h2>
                        <p className="text-xs text-slate-500 truncate mt-0.5" title={file.file_path}>{file.file_path}</p>
                    </div>
                    <button
                        onClick={onClose}
                        className="p-1.5 rounded-lg text-slate-500 hover:text-slate-900 dark:hover:text-white hover:bg-slate-100 dark:hover:bg-slate-800 transition-colors cursor-pointer flex-shrink-0"
                        aria-label="Close"
                    >
                        <X className="w-5 h-5" />
                    </button>
                </div>

                <div className="p-6 space-y-6">
                    {/* What was checked */}
                    <section>
                        <h3 className="text-xs font-semibold uppercase tracking-wide text-slate-500 mb-2">What was checked</h3>
                        {details ? (
                            <div className="rounded-xl border border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-800/30 px-4 py-2 divide-y divide-slate-200 dark:divide-slate-800/50">
                                <DetailRow icon={Activity} label="Method" value={details.method || 'unknown'} />
                                <DetailRow icon={Activity} label="Mode" value={details.mode || 'unknown'} />
                                {details.hwaccel && (
                                    <DetailRow icon={Cpu} label="Hardware accel" value={details.hwaccel} />
                                )}
                                <DetailRow
                                    icon={FileCheck}
                                    label="Structural check"
                                    value={`${details.structural}${details.structural_ms ? `  (${formatMs(details.structural_ms)})` : ''}`}
                                    valueClass={outcomeClass(details.structural)}
                                />
                                {details.content_analysis && (
                                    <DetailRow
                                        icon={Activity}
                                        label="Content analysis"
                                        value={`${details.content_analysis}${details.content_analysis_ms ? `  (${formatMs(details.content_analysis_ms)})` : ''}`}
                                        valueClass={outcomeClass(details.content_analysis)}
                                    />
                                )}
                            </div>
                        ) : (
                            <div className="flex items-start gap-2 rounded-xl border border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-800/30 px-4 py-3 text-sm text-slate-500">
                                <HelpCircle className="w-4 h-4 mt-0.5 flex-shrink-0" />
                                <span>
                                    No check details recorded. This file was either scanned before check details were captured, or skipped before detection ran (e.g. recently modified or still being written).
                                </span>
                            </div>
                        )}
                    </section>

                    {/* Result / error */}
                    {(file.status === 'corrupt' || file.status === 'error' || file.status === 'inaccessible' || file.status === 'skipped') && (file.error_details || file.corruption_type) && (
                        <section>
                            <h3 className="text-xs font-semibold uppercase tracking-wide text-slate-500 mb-2">Result</h3>
                            <div className={clsx(
                                'rounded-xl border px-4 py-3 text-sm',
                                file.status === 'inaccessible' ? 'text-orange-300 bg-orange-500/10 border-orange-500/20' :
                                    file.status === 'skipped' ? 'text-amber-300 bg-amber-500/10 border-amber-500/20' :
                                        'text-red-300 bg-red-500/10 border-red-500/20'
                            )}>
                                {file.corruption_type && <span className="font-semibold">{file.corruption_type}: </span>}
                                {file.error_details || 'No further detail recorded.'}
                            </div>
                        </section>
                    )}

                    {/* Metadata */}
                    <section className="rounded-xl border border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-800/30 px-4 py-2 divide-y divide-slate-200 dark:divide-slate-800/50">
                        <DetailRow icon={HardDrive} label="Size" value={formatBytes(file.file_size)} />
                        <DetailRow icon={Clock} label="Scanned at" value={formatTime(file.scanned_at)} />
                    </section>

                    {/* Journey link for corrupt files that have an aggregate */}
                    {file.corruption_id && onViewJourney && (
                        <button
                            onClick={() => onViewJourney(file.corruption_id!)}
                            className="w-full flex items-center justify-center gap-2 px-4 py-2.5 rounded-lg bg-blue-500/10 hover:bg-blue-500/20 text-blue-400 hover:text-blue-300 border border-blue-500/20 hover:border-blue-500/30 transition-colors cursor-pointer font-medium"
                        >
                            View remediation journey
                            <ArrowRight className="w-4 h-4" />
                        </button>
                    )}
                </div>
            </motion.div>
        </div>
    );
};

export default ScanFileDetail;
