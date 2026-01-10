import { AlertTriangle, Loader2, X } from 'lucide-react';
import { useEffect, useRef } from 'react';

interface ConfirmDialogProps {
    isOpen: boolean;
    title: string;
    message: string;
    confirmLabel?: string;
    cancelLabel?: string;
    variant?: 'danger' | 'warning' | 'info';
    isLoading?: boolean;
    onConfirm: () => void;
    onCancel: () => void;
}

const ConfirmDialog = ({
    isOpen,
    title,
    message,
    confirmLabel = 'Confirm',
    cancelLabel = 'Cancel',
    variant = 'danger',
    isLoading = false,
    onConfirm,
    onCancel,
}: ConfirmDialogProps) => {
    const confirmButtonRef = useRef<HTMLButtonElement>(null);

    // Focus confirm button when dialog opens
    useEffect(() => {
        if (isOpen && confirmButtonRef.current) {
            confirmButtonRef.current.focus();
        }
    }, [isOpen]);

    // Handle escape key
    useEffect(() => {
        const handleEscape = (e: KeyboardEvent) => {
            if (e.key === 'Escape' && isOpen && !isLoading) {
                onCancel();
            }
        };
        document.addEventListener('keydown', handleEscape);
        return () => document.removeEventListener('keydown', handleEscape);
    }, [isOpen, isLoading, onCancel]);

    if (!isOpen) return null;

    const variantStyles = {
        danger: {
            icon: 'bg-red-500/10 border-red-500/20 text-red-400',
            confirm: 'bg-red-500 hover:bg-red-600 text-white',
        },
        warning: {
            icon: 'bg-amber-500/10 border-amber-500/20 text-amber-400',
            confirm: 'bg-amber-500 hover:bg-amber-600 text-white',
        },
        info: {
            icon: 'bg-blue-500/10 border-blue-500/20 text-blue-400',
            confirm: 'bg-blue-500 hover:bg-blue-600 text-white',
        },
    };

    const styles = variantStyles[variant];

    return (
        <div
            className="fixed inset-0 z-50 flex items-center justify-center p-4"
            role="dialog"
            aria-modal="true"
            aria-labelledby="confirm-dialog-title"
        >
            {/* Backdrop */}
            <div
                className="absolute inset-0 bg-black/60 backdrop-blur-sm"
                onClick={!isLoading ? onCancel : undefined}
                aria-hidden="true"
            />

            {/* Dialog */}
            <div className="relative bg-white dark:bg-slate-900 rounded-2xl border border-slate-200 dark:border-slate-800 shadow-2xl max-w-md w-full p-6">
                {/* Close button */}
                <button
                    onClick={onCancel}
                    disabled={isLoading}
                    className="absolute top-4 right-4 p-1 rounded-lg text-slate-400 hover:text-slate-600 dark:hover:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 transition-colors disabled:opacity-50"
                    aria-label="Close dialog"
                >
                    <X className="w-5 h-5" />
                </button>

                {/* Icon */}
                <div className={`inline-flex p-3 rounded-xl border ${styles.icon} mb-4`}>
                    <AlertTriangle className="w-6 h-6" />
                </div>

                {/* Content */}
                <h2
                    id="confirm-dialog-title"
                    className="text-lg font-semibold text-slate-900 dark:text-white mb-2"
                >
                    {title}
                </h2>
                <p className="text-slate-600 dark:text-slate-400 mb-6">
                    {message}
                </p>

                {/* Actions */}
                <div className="flex gap-3 justify-end">
                    <button
                        onClick={onCancel}
                        disabled={isLoading}
                        className="px-4 py-2 rounded-lg border border-slate-200 dark:border-slate-700 text-slate-700 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 transition-colors disabled:opacity-50"
                    >
                        {cancelLabel}
                    </button>
                    <button
                        ref={confirmButtonRef}
                        onClick={onConfirm}
                        disabled={isLoading}
                        className={`px-4 py-2 rounded-lg font-medium transition-colors disabled:opacity-50 flex items-center gap-2 ${styles.confirm}`}
                    >
                        {isLoading && <Loader2 className="w-4 h-4 animate-spin" />}
                        {confirmLabel}
                    </button>
                </div>
            </div>
        </div>
    );
};

export default ConfirmDialog;
