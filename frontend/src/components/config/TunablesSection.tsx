import { useMemo, useState } from 'react';
import { motion } from 'framer-motion';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Save, Info, Lock } from 'lucide-react';
import { getTunables, updateTunables, type TunableValue } from '../../lib/api';
import { useToast } from '../../contexts/ToastContext';

interface TunablesSectionProps {
    /** Heading shown at the top of the card. */
    title: string;
    /** One-sentence helper text under the title. */
    subtitle?: string;
    /** Lucide icon for the header chip. */
    icon: React.ElementType;
    iconColor: string;
    /**
     * Which catalog entries to render. The list is the union of any keys
     * matching `keyPrefix` and any keys in `keys`. Order in the rendered
     * card follows the order of `keys` (then the catalog's own order for
     * anything caught by `keyPrefix`).
     */
    keyPrefix?: string;
    keys?: string[];
    /** Framer Motion delay for stagger. */
    delay?: number;
}

const humanizeKey = (key: string): string => {
    const tail = key.split('.').pop() ?? key;
    return tail
        .split('_')
        .filter((s) => s !== 'seconds')
        .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
        .join(' ');
};

/**
 * Returns a short human label describing where a tunable's current value
 * came from. Used in the per-field source badge so operators can tell at
 * a glance which fields are locked by env vs. live in the DB.
 */
const sourceLabel = (t: TunableValue): { text: string; tone: 'env' | 'db' | 'default' } => {
    if (t.source === 'env') return { text: `Set by ${t.env_var}`, tone: 'env' };
    if (t.source === 'db') return { text: 'Customized', tone: 'db' };
    return { text: 'Default', tone: 'default' };
};

const TunablesSection = ({
    title,
    subtitle,
    icon: Icon,
    iconColor,
    keyPrefix,
    keys,
    delay = 0.1,
}: TunablesSectionProps) => {
    const toast = useToast();
    const queryClient = useQueryClient();
    const { data, isLoading, error } = useQuery({
        queryKey: ['tunables'],
        queryFn: getTunables,
    });

    // Local form state: key -> value the user has typed but not yet saved.
    // Falls back to the canonical value from the server when a field has not
    // been touched. We never mutate `data` directly; the mutation refetch
    // is the source of truth after save.
    const [draft, setDraft] = useState<Record<string, number | string | boolean>>({});

    const tunables = useMemo<TunableValue[]>(() => {
        if (!data) return [];
        return data.tunables.filter((t) => {
            if (keys && keys.includes(t.key)) return true;
            if (keyPrefix && t.key.startsWith(keyPrefix)) return true;
            return false;
        });
    }, [data, keys, keyPrefix]);

    const mutation = useMutation({
        mutationFn: (updates: Record<string, number | string | boolean>) =>
            updateTunables(updates),
        onSuccess: () => {
            toast.success('Settings saved');
            setDraft({});
            void queryClient.invalidateQueries({ queryKey: ['tunables'] });
        },
        onError: (err: unknown) => {
            const e = err as { response?: { data?: { error?: string } }; message?: string };
            toast.error(`Save failed: ${e.response?.data?.error ?? e.message ?? 'unknown'}`);
        },
    });

    const handleSave = () => {
        if (Object.keys(draft).length === 0) return;
        mutation.mutate(draft);
    };

    const handleChange = (key: string, value: number | string | boolean) => {
        setDraft((prev) => ({ ...prev, [key]: value }));
    };

    if (isLoading) {
        return (
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ duration: 0.3, delay }}
                className="bg-white dark:bg-slate-900 rounded-xl p-6 shadow-sm border border-slate-200 dark:border-slate-800"
            >
                <p className="text-slate-500 dark:text-slate-400 text-sm">Loading settings…</p>
            </motion.div>
        );
    }

    if (error) {
        return (
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ duration: 0.3, delay }}
                className="bg-white dark:bg-slate-900 rounded-xl p-6 shadow-sm border border-red-200 dark:border-red-900"
            >
                <p className="text-red-600 dark:text-red-400 text-sm">Failed to load settings.</p>
            </motion.div>
        );
    }

    return (
        <motion.div
            initial={{ opacity: 0, y: 20 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.3, delay }}
            className="bg-white dark:bg-slate-900 rounded-xl p-6 shadow-sm border border-slate-200 dark:border-slate-800"
        >
            <div className="flex items-center gap-3 mb-6">
                <div className={`p-2 rounded-lg ${iconColor}`}>
                    <Icon className="w-5 h-5 text-white" />
                </div>
                <div>
                    <h2 className="text-lg font-semibold text-slate-900 dark:text-slate-100">{title}</h2>
                    {subtitle && (
                        <p className="text-sm text-slate-500 dark:text-slate-400">{subtitle}</p>
                    )}
                </div>
            </div>

            <div className="space-y-5">
                {tunables.map((t) => (
                    <TunableField
                        key={t.key}
                        tunable={t}
                        draftValue={draft[t.key]}
                        onChange={(v) => handleChange(t.key, v)}
                    />
                ))}
            </div>

            <div className="flex justify-end mt-6 pt-4 border-t border-slate-200 dark:border-slate-800">
                <button
                    type="button"
                    onClick={handleSave}
                    disabled={Object.keys(draft).length === 0 || mutation.isPending}
                    className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-indigo-600 text-white text-sm font-medium hover:bg-indigo-700 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
                >
                    <Save className="w-4 h-4" />
                    {mutation.isPending ? 'Saving…' : 'Save changes'}
                </button>
            </div>
        </motion.div>
    );
};

interface TunableFieldProps {
    tunable: TunableValue;
    draftValue: number | string | boolean | undefined;
    onChange: (value: number | string | boolean) => void;
}

const TunableField = ({ tunable, draftValue, onChange }: TunableFieldProps) => {
    const label = humanizeKey(tunable.key);
    const src = sourceLabel(tunable);
    const isLocked = tunable.source === 'env';
    // Effective value (draft if user has touched the field, else server value)
    const current = draftValue ?? tunable.value;

    return (
        <div>
            <div className="flex items-baseline justify-between mb-1">
                <label className="text-sm font-medium text-slate-800 dark:text-slate-200">
                    {label}
                    {tunable.requires_restart && (
                        <span className="ml-2 text-[10px] uppercase tracking-wider text-amber-600 dark:text-amber-400">
                            restart required
                        </span>
                    )}
                </label>
                <SourceBadge tone={src.tone} text={src.text} />
            </div>

            {tunable.description && (
                <p className="text-xs text-slate-500 dark:text-slate-400 mb-2 flex items-start gap-1">
                    <Info className="w-3 h-3 mt-0.5 flex-shrink-0" />
                    <span>{tunable.description}</span>
                </p>
            )}

            <FieldControl tunable={tunable} value={current} disabled={isLocked} onChange={onChange} />
        </div>
    );
};

const SourceBadge = ({ tone, text }: { tone: 'env' | 'db' | 'default'; text: string }) => {
    const classes: Record<typeof tone, string> = {
        env: 'bg-amber-100 dark:bg-amber-900/40 text-amber-800 dark:text-amber-300',
        db: 'bg-indigo-100 dark:bg-indigo-900/40 text-indigo-700 dark:text-indigo-300',
        default: 'bg-slate-100 dark:bg-slate-800 text-slate-600 dark:text-slate-400',
    };
    return (
        <span className={`inline-flex items-center gap-1 px-2 py-0.5 rounded text-[11px] ${classes[tone]}`}>
            {tone === 'env' && <Lock className="w-3 h-3" />}
            {text}
        </span>
    );
};

interface FieldControlProps {
    tunable: TunableValue;
    value: number | string | boolean;
    disabled: boolean;
    onChange: (value: number | string | boolean) => void;
}

const FieldControl = ({ tunable, value, disabled, onChange }: FieldControlProps) => {
    const cls =
        'w-full px-3 py-2 rounded-lg border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-800 text-sm text-slate-900 dark:text-slate-100 disabled:bg-slate-100 dark:disabled:bg-slate-900 disabled:cursor-not-allowed';

    switch (tunable.kind) {
        case 'enum':
            return (
                <select
                    className={cls}
                    disabled={disabled}
                    value={String(value)}
                    onChange={(e) => onChange(e.target.value)}
                >
                    {tunable.enum_values?.map((v) => (
                        <option key={v} value={v}>
                            {v}
                        </option>
                    ))}
                </select>
            );
        case 'bool':
            return (
                <label className="inline-flex items-center gap-2 text-sm text-slate-700 dark:text-slate-300">
                    <input
                        type="checkbox"
                        disabled={disabled}
                        checked={Boolean(value)}
                        onChange={(e) => onChange(e.target.checked)}
                        className="rounded border-slate-300 dark:border-slate-700"
                    />
                    Enabled
                </label>
            );
        case 'int':
            return (
                <input
                    type="number"
                    className={cls}
                    disabled={disabled}
                    min={tunable.min_int}
                    max={tunable.max_int}
                    step={1}
                    value={Number(value)}
                    onChange={(e) => onChange(Number(e.target.value))}
                />
            );
        case 'float':
            return (
                <input
                    type="number"
                    className={cls}
                    disabled={disabled}
                    min={tunable.min_float}
                    max={tunable.max_float}
                    step="0.1"
                    value={Number(value)}
                    onChange={(e) => onChange(Number(e.target.value))}
                />
            );
        case 'duration_seconds':
            return <DurationControl tunable={tunable} value={Number(value)} disabled={disabled} onChange={onChange} />;
        case 'string':
        default:
            return (
                <input
                    type="text"
                    className={cls}
                    disabled={disabled}
                    value={String(value)}
                    onChange={(e) => onChange(e.target.value)}
                />
            );
    }
};

interface DurationControlProps {
    tunable: TunableValue;
    value: number; // seconds
    disabled: boolean;
    onChange: (value: number) => void;
}

const DurationControl = ({ tunable, value, disabled, onChange }: DurationControlProps) => {
    // Pick the friendliest unit to display in: hours if value is a whole
    // number of hours, minutes if whole minutes, else seconds. The user can
    // change the unit independently of what we picked.
    const initialUnit: 'seconds' | 'minutes' | 'hours' =
        value > 0 && value % 3600 === 0 ? 'hours' : value > 0 && value % 60 === 0 ? 'minutes' : 'seconds';
    const [unit, setUnit] = useState<'seconds' | 'minutes' | 'hours'>(initialUnit);

    const scale = unit === 'hours' ? 3600 : unit === 'minutes' ? 60 : 1;
    const displayValue = Math.round((value / scale) * 1000) / 1000;

    const handleNumberChange = (n: number) => {
        if (Number.isNaN(n) || n < 0) return;
        onChange(Math.round(n * scale));
    };

    return (
        <div className="flex gap-2">
            <input
                type="number"
                min={0}
                step={unit === 'hours' ? 0.25 : 1}
                value={displayValue}
                disabled={disabled}
                onChange={(e) => handleNumberChange(Number(e.target.value))}
                className="flex-1 px-3 py-2 rounded-lg border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-800 text-sm text-slate-900 dark:text-slate-100 disabled:bg-slate-100 dark:disabled:bg-slate-900"
                aria-label={`${tunable.key} value`}
            />
            <select
                value={unit}
                disabled={disabled}
                onChange={(e) => setUnit(e.target.value as 'seconds' | 'minutes' | 'hours')}
                className="px-3 py-2 rounded-lg border border-slate-300 dark:border-slate-700 bg-white dark:bg-slate-800 text-sm text-slate-900 dark:text-slate-100 disabled:bg-slate-100 dark:disabled:bg-slate-900"
                aria-label="unit"
            >
                <option value="seconds">seconds</option>
                <option value="minutes">minutes</option>
                <option value="hours">hours</option>
            </select>
        </div>
    );
};

export default TunablesSection;
