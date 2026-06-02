/**
 * ProviderSelect - Categorized dropdown for selecting notification providers.
 * Supports both Config page styling (pink accent) and Wizard styling (green accent).
 */
import { useState, useEffect, useRef, useLayoutEffect, useCallback } from 'react';
import { createPortal } from 'react-dom';
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
    const triggerRef = useRef<HTMLDivElement>(null);
    const menuRef = useRef<HTMLDivElement>(null);
    const styles = variantStyles[variant];

    // The dropdown is rendered into document.body via a portal so it
    // escapes overflow:hidden ancestors (the Config-page card and the
    // Framer Motion height-animated container both clip otherwise).
    // Position is computed from the trigger's bounding rect each time
    // the menu opens, and refreshed on scroll / resize while open.
    const [menuPos, setMenuPos] = useState<{ top: number; left: number; width: number } | null>(null);

    const updateMenuPosition = useCallback(() => {
        if (!triggerRef.current) return;
        const rect = triggerRef.current.getBoundingClientRect();
        setMenuPos({
            top: rect.bottom + 4, // 4px gap matches the previous mt-1
            left: rect.left,
            width: rect.width,
        });
    }, []);

    // useLayoutEffect + setState is intentional: this is the standard
    // "measure DOM after layout then position the portal" pattern for
    // popovers/dropdowns. The setState fires synchronously after layout
    // so the menu paints in the right place on the first frame instead
    // of flashing at (0,0).
    useLayoutEffect(() => {
        if (!isOpen) {
            // eslint-disable-next-line react-hooks/set-state-in-effect
            setMenuPos(null);
            return;
        }
        updateMenuPosition();
        const onScroll = () => updateMenuPosition();
        const onResize = () => updateMenuPosition();
        window.addEventListener('scroll', onScroll, true); // capture-phase: catches nested scroll
        window.addEventListener('resize', onResize);
        return () => {
            window.removeEventListener('scroll', onScroll, true);
            window.removeEventListener('resize', onResize);
        };
    }, [isOpen, updateMenuPosition]);

    // Close on click outside. With the portal in place the menu is not
    // a DOM child of the trigger, so we check BOTH refs - a click is
    // outside only if it hit neither.
    useEffect(() => {
        if (!isOpen) return;
        const handleClickOutside = (event: MouseEvent) => {
            const target = event.target as Node;
            if (triggerRef.current?.contains(target)) return;
            if (menuRef.current?.contains(target)) return;
            setIsOpen(false);
        };
        document.addEventListener('mousedown', handleClickOutside);
        return () => document.removeEventListener('mousedown', handleClickOutside);
    }, [isOpen]);

    const selectedConfig = PROVIDER_CONFIGS[value];
    const displayLabel = value === 'none' ? noneLabel : selectedConfig?.label || 'Select provider';

    return (
        <div ref={triggerRef} className={clsx("relative", className)}>
            {/* Selected value button */}
            <button
                type="button"
                onClick={() => setIsOpen(!isOpen)}
                onKeyDown={(e) => {
                    if (e.key === 'Escape') setIsOpen(false);
                    if (e.key === 'ArrowDown' && !isOpen) {
                        e.preventDefault();
                        setIsOpen(true);
                    }
                }}
                aria-expanded={isOpen}
                aria-haspopup="listbox"
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

            {/* Dropdown menu - rendered into document.body via a portal so
                it escapes any overflow:hidden ancestor (the Config-page
                card uses overflow-hidden to clip its rounded corners, and
                the Framer-Motion height-animated wrapper also clips
                during/after the expand animation). Position is computed
                from the trigger's bounding rect in updateMenuPosition. */}
            {isOpen && menuPos && createPortal(
                <div
                    ref={menuRef}
                    role="listbox"
                    aria-label="Notification providers"
                    className="fixed z-50 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg shadow-lg max-h-80 overflow-y-auto"
                    style={{ top: menuPos.top, left: menuPos.left, width: menuPos.width }}
                >
                    {/* None/Skip option */}
                    {includeNone && (
                        <button
                            type="button"
                            role="option"
                            aria-selected={value === 'none'}
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
                                        role="option"
                                        aria-selected={value === key}
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
                </div>,
                document.body,
            )}
        </div>
    );
}

export default ProviderSelect;
