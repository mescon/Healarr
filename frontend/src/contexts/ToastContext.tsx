import { createContext, useContext, useState, useCallback, type ReactNode } from 'react';

interface Toast {
    id: string;
    type: 'success' | 'error' | 'warning' | 'info';
    message: string;
    duration?: number;
}

interface ToastContextType {
    toasts: Toast[];
    addToast: (toast: Omit<Toast, 'id'>) => void;
    removeToast: (id: string) => void;
    success: (message: string, duration?: number) => void;
    error: (message: string, duration?: number) => void;
    warning: (message: string, duration?: number) => void;
    info: (message: string, duration?: number) => void;
}

const ToastContext = createContext<ToastContextType | undefined>(undefined);

/* eslint-disable react-refresh/only-export-components */
export const useToast = () => {
    const context = useContext(ToastContext);
    if (!context) {
        throw new Error('useToast must be used within ToastProvider');
    }
    return context;
};

interface ToastProviderProps {
    children: ReactNode;
}

export const ToastProvider = ({ children }: ToastProviderProps) => {
    const [toasts, setToasts] = useState<Toast[]>([]);

    const removeToast = useCallback((id: string) => {
        setToasts(prev => prev.filter(toast => toast.id !== id));
    }, []);

    const addToast = useCallback((toast: Omit<Toast, 'id'>) => {
        const id = `toast-${Date.now()}-${Math.random()}`;
        const newToast = { ...toast, id };

        setToasts(prev => {
            // Keep max 5 toasts
            const updated = [...prev, newToast];
            return updated.slice(-5);
        });

        // Auto-remove after duration
        const duration = toast.duration || (toast.type === 'error' ? 7000 : toast.type === 'success' ? 3000 : 5000);
        setTimeout(() => {
            removeToast(id);
        }, duration);
    }, [removeToast]);

    const success = useCallback((message: string, duration?: number) => {
        addToast({ type: 'success', message, duration });
    }, [addToast]);

    const error = useCallback((message: string, duration?: number) => {
        addToast({ type: 'error', message, duration });
    }, [addToast]);

    const warning = useCallback((message: string, duration?: number) => {
        addToast({ type: 'warning', message, duration });
    }, [addToast]);

    const info = useCallback((message: string, duration?: number) => {
        addToast({ type: 'info', message, duration });
    }, [addToast]);

    return (
        <ToastContext.Provider value={{ toasts, addToast, removeToast, success, error, warning, info }}>
            {children}
        </ToastContext.Provider>
    );
};
