/**
 * EventSelector - Component for selecting which notification events to subscribe to.
 * Displays events grouped by category with group-level toggle and individual event toggles.
 */
import { useState } from 'react';
import { ChevronDown } from 'lucide-react';
import clsx from 'clsx';
import type { EventGroup } from '../../lib/api';

export type EventSelectorVariant = 'config' | 'wizard';

interface EventSelectorProps {
    events: string[];
    eventGroups: EventGroup[] | undefined;
    onChange: (events: string[]) => void;
    variant?: EventSelectorVariant;
    className?: string;
    /** Whether to start collapsed (wizard mode) */
    defaultCollapsed?: boolean;
    /** Title to show above the selector */
    title?: string;
}

// Variant-specific styling
const variantStyles = {
    config: {
        group: 'bg-slate-100 dark:bg-slate-800/50',
        checkbox: 'text-pink-500 focus:ring-pink-500',
        eventSelected: 'bg-pink-500/20 border-pink-500/50 text-pink-600 dark:text-pink-300',
        eventUnselected: 'bg-slate-100 dark:bg-slate-800 border-slate-300 dark:border-slate-700 text-slate-600 dark:text-slate-400 hover:border-slate-400 dark:hover:border-slate-600',
        label: 'text-slate-700 dark:text-slate-300',
    },
    wizard: {
        group: 'bg-slate-100 dark:bg-slate-800/30',
        checkbox: 'text-green-500 focus:ring-green-500',
        eventSelected: 'bg-green-500/20 border-green-500/50 text-green-600 dark:text-green-300',
        eventUnselected: 'bg-slate-100 dark:bg-slate-800 border-slate-300 dark:border-slate-700 text-slate-600 dark:text-slate-400 hover:border-slate-400 dark:hover:border-slate-600',
        label: 'text-slate-800 dark:text-slate-200',
    },
};

export function EventSelector({
    events,
    eventGroups,
    onChange,
    variant = 'config',
    className,
    defaultCollapsed = false,
    title = 'Events to Notify',
}: EventSelectorProps) {
    const [isCollapsed, setIsCollapsed] = useState(defaultCollapsed);
    const styles = variantStyles[variant];

    const toggleEvent = (event: string) => {
        if (events.includes(event)) {
            onChange(events.filter(e => e !== event));
        } else {
            onChange([...events, event]);
        }
    };

    const toggleEventGroup = (eventInfos: { name: string }[]) => {
        const eventNames = eventInfos.map(e => e.name);
        const allSelected = eventNames.every(e => events.includes(e));
        if (allSelected) {
            onChange(events.filter(e => !eventNames.includes(e)));
        } else {
            onChange([...new Set([...events, ...eventNames])]);
        }
    };

    if (!eventGroups || eventGroups.length === 0) {
        return (
            <div className={clsx("text-sm text-slate-500", className)}>
                Loading events...
            </div>
        );
    }

    // Selected count for header
    const totalEvents = eventGroups.reduce((sum, g) => sum + g.events.length, 0);
    const selectedCount = events.length;

    return (
        <div className={clsx("space-y-4", className)}>
            {/* Collapsible header (for wizard mode) */}
            {defaultCollapsed && (
                <button
                    type="button"
                    onClick={() => setIsCollapsed(!isCollapsed)}
                    className="w-full flex items-center justify-between text-left cursor-pointer"
                >
                    <h4 className={clsx("text-sm font-medium", styles.label)}>
                        {title}
                        <span className="ml-2 text-xs text-slate-500">
                            ({selectedCount}/{totalEvents} selected)
                        </span>
                    </h4>
                    <ChevronDown className={clsx(
                        "w-4 h-4 text-slate-500 transition-transform",
                        !isCollapsed && "rotate-180"
                    )} />
                </button>
            )}

            {/* Non-collapsible title (for config mode) */}
            {!defaultCollapsed && title && (
                <h4 className={clsx("text-sm font-medium", styles.label)}>{title}</h4>
            )}

            {/* Event groups */}
            {(!defaultCollapsed || !isCollapsed) && (
                <div className="space-y-3">
                    {eventGroups.map(group => (
                        <div key={group.name} className={clsx("rounded-lg p-4", styles.group)}>
                            <div className="flex items-center gap-3 mb-3">
                                <input
                                    type="checkbox"
                                    checked={group.events.every(e => events.includes(e.name))}
                                    onChange={() => toggleEventGroup(group.events)}
                                    className={clsx(
                                        "w-4 h-4 bg-white dark:bg-slate-800 border-slate-300 dark:border-slate-700 rounded",
                                        styles.checkbox
                                    )}
                                />
                                <span className={clsx("text-sm font-medium", styles.label)}>
                                    {group.name}
                                </span>
                            </div>
                            <div className="flex flex-wrap gap-2 ml-7">
                                {group.events.map(event => (
                                    <button
                                        key={event.name}
                                        type="button"
                                        onClick={() => toggleEvent(event.name)}
                                        title={event.description}
                                        className={clsx(
                                            "px-2 py-1 text-xs rounded-lg border transition-colors cursor-pointer",
                                            events.includes(event.name)
                                                ? styles.eventSelected
                                                : styles.eventUnselected
                                        )}
                                    >
                                        {event.label}
                                    </button>
                                ))}
                            </div>
                        </div>
                    ))}
                </div>
            )}
        </div>
    );
}

export default EventSelector;
