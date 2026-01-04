import { useState, useEffect } from 'react';
import { AlertTriangle, X, ExternalLink } from 'lucide-react';
import { motion, AnimatePresence } from 'framer-motion';
import clsx from 'clsx';

interface ConfigWarning {
    type: string;
    field: string;
    message: string;
    current: string;
    recommended: string;
}

interface HealthResponse {
    config_warnings?: ConfigWarning[];
}

/**
 * ConfigWarningBanner displays a warning banner when configuration issues are detected.
 * It fetches the health endpoint to check for config_warnings and displays them prominently.
 * Users can dismiss the banner (stored in sessionStorage so it reappears on new sessions).
 */
export default function ConfigWarningBanner() {
    const [warnings, setWarnings] = useState<ConfigWarning[]>([]);
    const [dismissed, setDismissed] = useState(false);
    const [loading, setLoading] = useState(true);

    useEffect(() => {
        // Check if already dismissed this session
        if (sessionStorage.getItem('configWarningDismissed') === 'true') {
            setDismissed(true);
            setLoading(false);
            return;
        }

        // Fetch health endpoint to check for warnings
        const basePath = (window as unknown as { __BASE_PATH__?: string }).__BASE_PATH__ || '';
        fetch(`${basePath}/api/health`)
            .then(res => res.json())
            .then((data: HealthResponse) => {
                if (data.config_warnings && data.config_warnings.length > 0) {
                    setWarnings(data.config_warnings);
                }
            })
            .catch(() => {
                // Silently fail - don't show errors for health check
            })
            .finally(() => setLoading(false));
    }, []);

    const handleDismiss = () => {
        sessionStorage.setItem('configWarningDismissed', 'true');
        setDismissed(true);
    };

    // Don't render anything if loading, dismissed, or no warnings
    if (loading || dismissed || warnings.length === 0) {
        return null;
    }

    return (
        <AnimatePresence>
            <motion.div
                initial={{ opacity: 0, y: -20 }}
                animate={{ opacity: 1, y: 0 }}
                exit={{ opacity: 0, y: -20 }}
                className={clsx(
                    "mb-6 rounded-xl border-2 p-4",
                    "bg-amber-50 dark:bg-amber-950/30 border-amber-300 dark:border-amber-700/50"
                )}
            >
                <div className="flex items-start gap-3">
                    <div className="flex-shrink-0 p-2 rounded-lg bg-amber-100 dark:bg-amber-900/50">
                        <AlertTriangle className="w-5 h-5 text-amber-600 dark:text-amber-400" />
                    </div>

                    <div className="flex-1 min-w-0">
                        <h3 className="font-semibold text-amber-800 dark:text-amber-200 mb-1">
                            Configuration Issue Detected
                        </h3>
                        <p className="text-sm text-amber-700 dark:text-amber-300 mb-3">
                            Your configuration may cause data loss when the container is updated.
                        </p>

                        {warnings.map((warning, idx) => (
                            <div
                                key={idx}
                                className="text-sm bg-amber-100/50 dark:bg-amber-900/30 rounded-lg p-3 mb-2 font-mono"
                            >
                                <div className="flex flex-col gap-1">
                                    <div className="flex items-center gap-2">
                                        <span className="text-amber-600 dark:text-amber-400 font-semibold">Current:</span>
                                        <code className="text-amber-800 dark:text-amber-200 break-all">{warning.current}</code>
                                    </div>
                                    <div className="flex items-center gap-2">
                                        <span className="text-emerald-600 dark:text-emerald-400 font-semibold">Recommended:</span>
                                        <code className="text-emerald-800 dark:text-emerald-200 break-all">{warning.recommended}</code>
                                    </div>
                                </div>
                            </div>
                        ))}

                        <a
                            href="https://github.com/mescon/Healarr/issues/9"
                            target="_blank"
                            rel="noopener noreferrer"
                            className="inline-flex items-center gap-1 text-sm text-amber-700 dark:text-amber-300 hover:text-amber-900 dark:hover:text-amber-100 underline mt-1"
                        >
                            Learn more about this issue
                            <ExternalLink className="w-3 h-3" />
                        </a>
                    </div>

                    <button
                        onClick={handleDismiss}
                        className="flex-shrink-0 p-1 rounded-lg hover:bg-amber-200 dark:hover:bg-amber-800/50 transition-colors"
                        aria-label="Dismiss warning"
                    >
                        <X className="w-5 h-5 text-amber-600 dark:text-amber-400" />
                    </button>
                </div>
            </motion.div>
        </AnimatePresence>
    );
}
