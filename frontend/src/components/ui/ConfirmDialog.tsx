import { motion, AnimatePresence } from 'framer-motion';
import { AlertTriangle, Trash2, X } from 'lucide-react';
import clsx from 'clsx';

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
    const variantStyles = {
        danger: {
            icon: Trash2,
            iconBg: 'bg-red-500/10 border-red-500/30',
            iconColor: 'text-red-400',
            button: 'bg-red-500 hover:bg-red-600 text-white',
        },
        warning: {
            icon: AlertTriangle,
            iconBg: 'bg-amber-500/10 border-amber-500/30',
            iconColor: 'text-amber-400',
            button: 'bg-amber-500 hover:bg-amber-600 text-white',
        },
        info: {
            icon: AlertTriangle,
            iconBg: 'bg-blue-500/10 border-blue-500/30',
            iconColor: 'text-blue-400',
            button: 'bg-blue-500 hover:bg-blue-600 text-white',
        },
    };

    const styles = variantStyles[variant];
    const Icon = styles.icon;

    return (
        <AnimatePresence>
            {isOpen && (
                <motion.div
                    initial={{ opacity: 0 }}
                    animate={{ opacity: 1 }}
                    exit={{ opacity: 0 }}
                    className="fixed inset-0 bg-black/60 backdrop-blur-sm z-50 flex items-center justify-center p-4"
                    onClick={onCancel}
                    role="dialog"
                    aria-modal="true"
                    aria-labelledby="confirm-dialog-title"
                    aria-describedby="confirm-dialog-message"
                >
                    <motion.div
                        initial={{ scale: 0.95, opacity: 0 }}
                        animate={{ scale: 1, opacity: 1 }}
                        exit={{ scale: 0.95, opacity: 0 }}
                        className="bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-2xl p-6 max-w-md w-full shadow-2xl"
                        onClick={(e) => e.stopPropagation()}
                    >
                        <div className="text-center mb-6">
                            <div className={clsx(
                                "w-16 h-16 mx-auto border rounded-full flex items-center justify-center mb-4",
                                styles.iconBg
                            )}>
                                <Icon className={clsx("w-8 h-8", styles.iconColor)} aria-hidden="true" />
                            </div>
                            <h3 id="confirm-dialog-title" className="text-xl font-bold text-slate-900 dark:text-white mb-2">
                                {title}
                            </h3>
                            <p id="confirm-dialog-message" className="text-sm text-slate-600 dark:text-slate-400">
                                {message}
                            </p>
                        </div>

                        <div className="flex gap-3">
                            <button
                                onClick={onCancel}
                                disabled={isLoading}
                                className="flex-1 px-4 py-2 bg-slate-200 dark:bg-slate-700 hover:bg-slate-300 dark:hover:bg-slate-600 text-slate-900 dark:text-white rounded-lg font-medium transition-colors cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed"
                            >
                                {cancelLabel}
                            </button>
                            <button
                                onClick={onConfirm}
                                disabled={isLoading}
                                className={clsx(
                                    "flex-1 px-4 py-2 rounded-lg font-medium transition-colors cursor-pointer flex items-center justify-center gap-2 disabled:opacity-50 disabled:cursor-not-allowed",
                                    styles.button
                                )}
                            >
                                {isLoading ? (
                                    <>
                                        <div className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                                        Loading...
                                    </>
                                ) : (
                                    confirmLabel
                                )}
                            </button>
                        </div>

                        {/* Close button */}
                        <button
                            onClick={onCancel}
                            className="absolute top-4 right-4 p-1 text-slate-400 hover:text-slate-600 dark:hover:text-slate-300 transition-colors cursor-pointer"
                            aria-label="Close dialog"
                        >
                            <X className="w-5 h-5" aria-hidden="true" />
                        </button>
                    </motion.div>
                </motion.div>
            )}
        </AnimatePresence>
    );
};

export default ConfirmDialog;
