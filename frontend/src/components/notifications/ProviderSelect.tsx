/**
 * ProviderSelect - Categorized dropdown for selecting notification providers.
 * Supports both Config page styling (pink accent) and Wizard styling (green accent).
 */
import { useState, useEffect, useRef } from 'react';
import { ChevronDown } from 'lucide-react';
import clsx from 'clsx';
import { PROVIDER_CONFIGS, PROVIDER_CATEGORIES } from '../../lib/notificationProviders';
import { ProviderIcon } from './ProviderIcon';

export type ProviderSelectVariant = 'config' | 'wizard';

interface ProviderSelectProps {
    value: string;
    onChange: (value: string) => void;
    variant?: ProviderSelectVariant;
    className?: string;
    /** Optional: include a "none" option at the top */
    includeNone?: boolean;
    noneLabel?: string;
}

// Variant-specific styling
const variantStyles = {
    config: {
        button: 'bg-white dark:bg-slate-900 border-slate-300 dark:border-slate-700 focus:ring-pink-500',
        selected: 'bg-pink-50 dark:bg-pink-900/20',
        header: 'bg-slate-50 dark:bg-slate-800/50',
    },
    wizard: {
        button: 'bg-slate-100 dark:bg-slate-800/50 border-slate-300 dark:border-slate-700 focus:ring-green-500/50 focus:border-green-500',
        selected: 'bg-green-50 dark:bg-green-900/20',
        header: 'bg-slate-100 dark:bg-slate-800/50',
    },
};

export function ProviderSelect({
    value,
    onChange,
    variant = 'config',
    className,
    includeNone = false,
    noneLabel = 'Skip (no notifications)',
}: ProviderSelectProps) {
    const [isOpen, setIsOpen] = useState(false);
    const dropdownRef = useRef<HTMLDivElement>(null);
    const styles = variantStyles[variant];

    // Close on click outside
    useEffect(() => {
        const handleClickOutside = (event: MouseEvent) => {
            if (dropdownRef.current && !dropdownRef.current.contains(event.target as Node)) {
                setIsOpen(false);
            }
        };
        document.addEventListener('mousedown', handleClickOutside);
        return () => document.removeEventListener('mousedown', handleClickOutside);
    }, []);

    const selectedConfig = PROVIDER_CONFIGS[value];
    const displayLabel = value === 'none' ? noneLabel : selectedConfig?.label || 'Select provider';

    return (
        <div ref={dropdownRef} className={clsx("relative", className)}>
            {/* Selected value button */}
            <button
                type="button"
                onClick={() => setIsOpen(!isOpen)}
                className={clsx(
                    "w-full px-3 py-2 border rounded-lg text-slate-900 dark:text-white focus:ring-2 flex items-center justify-between cursor-pointer",
                    styles.button
                )}
            >
                <div className="flex items-center gap-2">
                    {value !== 'none' ? (
                        <ProviderIcon provider={value} className="w-5 h-5" />
                    ) : (
                        <span className="text-lg">⏭️</span>
                    )}
                    <span>{displayLabel}</span>
                </div>
                <ChevronDown className={clsx("w-4 h-4 transition-transform", isOpen && "rotate-180")} />
            </button>

            {/* Dropdown menu */}
            {isOpen && (
                <div className="absolute z-50 w-full mt-1 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg shadow-lg max-h-80 overflow-y-auto">
                    {/* None/Skip option */}
                    {includeNone && (
                        <button
                            type="button"
                            onClick={() => {
                                onChange('none');
                                setIsOpen(false);
                            }}
                            className={clsx(
                                "w-full px-3 py-2 flex items-center gap-2 hover:bg-slate-100 dark:hover:bg-slate-800 cursor-pointer text-left",
                                value === 'none' && styles.selected
                            )}
                        >
                            <span className="text-lg">⏭️</span>
                            <span className="text-slate-900 dark:text-white">{noneLabel}</span>
                        </button>
                    )}

                    {/* Categorized providers */}
                    {PROVIDER_CATEGORIES.map(category => {
                        const providers = Object.entries(PROVIDER_CONFIGS).filter(
                            ([, config]) => config.category === category.key
                        );
                        if (providers.length === 0) return null;
                        return (
                            <div key={category.key}>
                                <div className={clsx(
                                    "px-3 py-2 text-xs font-semibold text-slate-500 dark:text-slate-400 sticky top-0",
                                    styles.header
                                )}>
                                    {category.emoji} {category.label}
                                </div>
                                {providers.map(([key, config]) => (
                                    <button
                                        key={key}
                                        type="button"
                                        onClick={() => {
                                            onChange(key);
                                            setIsOpen(false);
                                        }}
                                        className={clsx(
                                            "w-full px-3 py-2 flex items-center gap-2 hover:bg-slate-100 dark:hover:bg-slate-800 cursor-pointer text-left",
                                            value === key && styles.selected
                                        )}
                                    >
                                        <ProviderIcon provider={key} className="w-5 h-5" />
                                        <span className="text-slate-900 dark:text-white">{config.label}</span>
                                    </button>
                                ))}
                            </div>
                        );
                    })}
                </div>
            )}
        </div>
    );
}

export default ProviderSelect;
