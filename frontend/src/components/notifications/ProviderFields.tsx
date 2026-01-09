/**
 * ProviderFields - Renders dynamic configuration fields for a notification provider.
 * Supports text, password, number, email, checkbox, select, and textarea field types.
 */
import clsx from 'clsx';
import { PROVIDER_CONFIGS, type ProviderField } from '../../lib/notificationProviders';
import { ProviderIcon } from './ProviderIcon';

export type ProviderFieldsVariant = 'config' | 'wizard';

interface ProviderFieldsProps {
    provider: string;
    config: Record<string, unknown>;
    onChange: (config: Record<string, unknown>) => void;
    variant?: ProviderFieldsVariant;
    className?: string;
    /** Whether to show the provider header with icon */
    showHeader?: boolean;
}

// Variant-specific styling
const variantStyles = {
    config: {
        input: 'bg-white dark:bg-slate-900 border-slate-300 dark:border-slate-700 focus:ring-pink-500',
        checkbox: 'text-pink-500 focus:ring-pink-500',
        label: 'text-slate-600 dark:text-slate-400',
        header: 'text-slate-700 dark:text-slate-300',
    },
    wizard: {
        input: 'bg-slate-100 dark:bg-slate-800/50 border-slate-300 dark:border-slate-700 focus:ring-green-500/50 focus:border-green-500',
        checkbox: 'text-green-500 focus:ring-green-500',
        label: 'text-slate-700 dark:text-slate-300',
        header: 'text-slate-800 dark:text-slate-200',
    },
};

export function ProviderFields({
    provider,
    config,
    onChange,
    variant = 'config',
    className,
    showHeader = true,
}: ProviderFieldsProps) {
    const providerConfig = PROVIDER_CONFIGS[provider];
    const styles = variantStyles[variant];

    if (!providerConfig) {
        return null;
    }

    const updateField = (key: string, value: unknown) => {
        onChange({ ...config, [key]: value });
    };

    const renderField = (field: ProviderField) => {
        const value = config[field.key];

        switch (field.type) {
            case 'select':
                return (
                    <select
                        value={(value as string) || field.options?.[0]?.value || ''}
                        onChange={e => updateField(
                            field.key,
                            field.numeric ? parseInt(e.target.value) : e.target.value
                        )}
                        className={clsx(
                            "w-full px-3 py-2 border rounded-lg text-slate-900 dark:text-white focus:ring-2",
                            styles.input
                        )}
                    >
                        {field.options?.map(opt => (
                            <option key={opt.value} value={opt.value}>{opt.label}</option>
                        ))}
                    </select>
                );

            case 'checkbox':
                return (
                    <div className="flex items-center gap-2">
                        <input
                            type="checkbox"
                            checked={!!value}
                            onChange={e => updateField(field.key, e.target.checked)}
                            className={clsx(
                                "w-4 h-4 bg-white dark:bg-slate-800 border-slate-300 dark:border-slate-700 rounded",
                                styles.checkbox
                            )}
                        />
                        <span className={clsx("text-sm", styles.label)}>Enable</span>
                    </div>
                );

            case 'textarea':
                return (
                    <textarea
                        value={(value as string) || ''}
                        onChange={e => updateField(field.key, e.target.value)}
                        placeholder={field.placeholder}
                        rows={3}
                        className={clsx(
                            "w-full px-3 py-2 border rounded-lg text-slate-900 dark:text-white placeholder-slate-500 focus:ring-2 font-mono text-sm",
                            styles.input
                        )}
                    />
                );

            default:
                return (
                    <input
                        type={field.type}
                        value={(value as string) || ''}
                        onChange={e => updateField(
                            field.key,
                            field.type === 'number' ? parseInt(e.target.value) || 0 : e.target.value
                        )}
                        placeholder={field.placeholder}
                        className={clsx(
                            "w-full px-3 py-2 border rounded-lg text-slate-900 dark:text-white placeholder-slate-500 focus:ring-2",
                            styles.input
                        )}
                    />
                );
        }
    };

    return (
        <div className={clsx("space-y-4", className)}>
            {showHeader && (
                <div className="flex items-center justify-between">
                    <h4 className={clsx("text-sm font-medium flex items-center gap-2", styles.header)}>
                        <ProviderIcon provider={provider} className="w-5 h-5" />
                        {providerConfig.label} Settings
                    </h4>
                    {providerConfig.description && (
                        <p className="text-xs text-slate-500">{providerConfig.description}</p>
                    )}
                </div>
            )}
            <div className={clsx(
                "grid gap-4",
                variant === 'wizard' ? 'grid-cols-1' : 'grid-cols-1 md:grid-cols-2'
            )}>
                {providerConfig.fields.map(field => (
                    <div key={field.key} className={field.type === 'textarea' ? 'md:col-span-2' : ''}>
                        <label className={clsx("block text-sm font-medium mb-2", styles.label)}>
                            {field.label}
                        </label>
                        {renderField(field)}
                    </div>
                ))}
            </div>
        </div>
    );
}

export default ProviderFields;
