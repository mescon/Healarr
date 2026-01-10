import type { LucideIcon } from 'lucide-react';
import { Inbox } from 'lucide-react';

interface EmptyStateProps {
    icon?: LucideIcon;
    title: string;
    description?: string;
    action?: {
        label: string;
        onClick: () => void;
    };
    compact?: boolean;
}

const EmptyState = ({
    icon: Icon = Inbox,
    title,
    description,
    action,
    compact = false,
}: EmptyStateProps) => {
    if (compact) {
        return (
            <div className="p-8 text-center text-slate-500 dark:text-slate-400">
                <p className="font-medium">{title}</p>
                {description && (
                    <p className="text-sm mt-1">{description}</p>
                )}
            </div>
        );
    }

    return (
        <div className="flex flex-col items-center justify-center py-12 px-4">
            <div className="p-4 rounded-2xl bg-slate-100 dark:bg-slate-800/30 border border-slate-200 dark:border-slate-700/50 mb-4">
                <Icon className="w-8 h-8 text-slate-400 dark:text-slate-500" aria-hidden="true" />
            </div>
            <h3 className="text-lg font-medium text-slate-700 dark:text-slate-300 mb-1">
                {title}
            </h3>
            {description && (
                <p className="text-sm text-slate-500 dark:text-slate-400 text-center max-w-sm">
                    {description}
                </p>
            )}
            {action && (
                <button
                    onClick={action.onClick}
                    className="mt-4 px-4 py-2 text-sm font-medium text-green-600 dark:text-green-400 bg-green-500/10 hover:bg-green-500/20 rounded-lg border border-green-500/20 transition-colors"
                >
                    {action.label}
                </button>
            )}
        </div>
    );
};

export default EmptyState;
