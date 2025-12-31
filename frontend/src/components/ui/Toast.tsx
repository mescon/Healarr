import { motion } from 'framer-motion';
import { X, CheckCircle, AlertCircle, AlertTriangle, Info } from 'lucide-react';
import { useEffect, useState } from 'react';

interface ToastProps {
    id: string;
    type: 'success' | 'error' | 'warning' | 'info';
    message: string;
    onClose: () => void;
    duration?: number;
}

const Toast = ({ type, message, onClose, duration = 5000 }: ToastProps) => {
    const [isPaused, setIsPaused] = useState(false);
    const [progress, setProgress] = useState(100);

    useEffect(() => {
        if (isPaused) return;

        const interval = setInterval(() => {
            setProgress(prev => {
                const newProgress = prev - (100 / (duration / 50));
                if (newProgress <= 0) {
                    onClose();
                    return 0;
                }
                return newProgress;
            });
        }, 50);

        return () => clearInterval(interval);
    }, [isPaused, duration, onClose]);

    const getIcon = () => {
        switch (type) {
            case 'success':
                return <CheckCircle className="w-5 h-5" />;
            case 'error':
                return <AlertCircle className="w-5 h-5" />;
            case 'warning':
                return <AlertTriangle className="w-5 h-5" />;
            case 'info':
                return <Info className="w-5 h-5" />;
        }
    };

    const getStyles = () => {
        switch (type) {
            case 'success':
                return 'bg-gradient-to-r from-green-500/90 to-emerald-500/90 text-white border-green-400/20';
            case 'error':
                return 'bg-gradient-to-r from-red-500/90 to-rose-500/90 text-white border-red-400/20';
            case 'warning':
                return 'bg-gradient-to-r from-orange-500/90 to-amber-500/90 text-white border-orange-400/20';
            case 'info':
                return 'bg-gradient-to-r from-blue-500/90 to-cyan-500/90 text-white border-blue-400/20';
        }
    };

    return (
        <motion.div
            layout
            initial={{ opacity: 0, x: 300, scale: 0.8 }}
            animate={{ opacity: 1, x: 0, scale: 1 }}
            exit={{ opacity: 0, x: 300, scale: 0.8 }}
            transition={{ type: 'spring', stiffness: 300, damping: 30 }}
            className={`relative min-w-[320px] max-w-md rounded-xl border backdrop-blur-xl shadow-2xl overflow-hidden ${getStyles()}`}
            onMouseEnter={() => setIsPaused(true)}
            onMouseLeave={() => setIsPaused(false)}
        >
            <div className="p-4 flex items-start gap-3">
                <div className="shrink-0 mt-0.5">
                    {getIcon()}
                </div>
                <div className="flex-1 min-w-0">
                    <p className="text-sm font-medium leading-relaxed break-words">
                        {message}
                    </p>
                </div>
                <button
                    onClick={onClose}
                    className="shrink-0 p-1 hover:bg-white/10 rounded-lg transition-colors"
                    aria-label="Close notification"
                >
                    <X className="w-4 h-4" />
                </button>
            </div>

            {/* Progress bar */}
            <div className="h-1 bg-black/20">
                <motion.div
                    className="h-full bg-white/40"
                    style={{ width: `${progress}%` }}
                    transition={{ duration: 0.05, ease: 'linear' }}
                />
            </div>
        </motion.div>
    );
};

export default Toast;
