import { motion } from 'framer-motion';
import { ArrowRight, Check, Info, Clock, PlayCircle, Webhook } from 'lucide-react';
import type { CompleteStepProps } from './types';

export default function CompleteStep({ onComplete }: CompleteStepProps) {
    return (
        <motion.div
            key="complete"
            initial={{ opacity: 0, scale: 0.95 }}
            animate={{ opacity: 1, scale: 1 }}
            className="space-y-6 text-center"
        >
            <div className="inline-flex items-center justify-center w-20 h-20 bg-gradient-to-br from-green-500 to-emerald-600 rounded-full shadow-lg shadow-green-500/30">
                <Check className="w-10 h-10 text-white" />
            </div>

            <div>
                <h2 className="text-2xl font-bold text-slate-900 dark:text-white mb-2">
                    Setup Complete!
                </h2>
                <p className="text-slate-600 dark:text-slate-400">
                    Healarr is ready to monitor your media library.
                </p>
            </div>

            <div className="bg-slate-100 dark:bg-slate-800/50 rounded-xl p-4 text-left space-y-2">
                <h3 className="font-medium text-slate-900 dark:text-white mb-3">What's next?</h3>
                <ul className="space-y-2 text-sm text-slate-600 dark:text-slate-400">
                    <li className="flex items-start gap-2">
                        <Check className="w-4 h-4 text-green-500 mt-0.5 flex-shrink-0" />
                        <span>Run your first scan from the Dashboard</span>
                    </li>
                    <li className="flex items-start gap-2">
                        <Check className="w-4 h-4 text-green-500 mt-0.5 flex-shrink-0" />
                        <span>Configure additional scan paths in Settings</span>
                    </li>
                    <li className="flex items-start gap-2">
                        <Check className="w-4 h-4 text-green-500 mt-0.5 flex-shrink-0" />
                        <span>Set up notifications to stay informed</span>
                    </li>
                    <li className="flex items-start gap-2">
                        <Check className="w-4 h-4 text-green-500 mt-0.5 flex-shrink-0" />
                        <span>Create scan schedules for automated monitoring</span>
                    </li>
                </ul>
            </div>

            {/* Scanning Workflow Help */}
            <div className="bg-blue-500/10 border border-blue-500/20 rounded-xl p-4 text-left">
                <div className="flex items-center gap-2 mb-3">
                    <Info className="w-4 h-4 text-blue-500 flex-shrink-0" />
                    <h3 className="font-medium text-slate-900 dark:text-white text-sm">How Scanning Works</h3>
                </div>
                <div className="space-y-2 text-sm text-slate-600 dark:text-slate-400">
                    <div className="flex items-start gap-2">
                        <Webhook className="w-4 h-4 text-purple-500 mt-0.5 flex-shrink-0" />
                        <div>
                            <span className="font-medium text-slate-700 dark:text-slate-300">Webhooks</span>
                            <span className="text-slate-500 dark:text-slate-400"> — Configure your *arr apps to send webhooks when files are imported. Healarr scans them automatically in real-time.</span>
                        </div>
                    </div>
                    <div className="flex items-start gap-2">
                        <Clock className="w-4 h-4 text-amber-500 mt-0.5 flex-shrink-0" />
                        <div>
                            <span className="font-medium text-slate-700 dark:text-slate-300">Scheduled Scans</span>
                            <span className="text-slate-500 dark:text-slate-400"> — Set up cron schedules to periodically scan your entire library for corrupted files.</span>
                        </div>
                    </div>
                    <div className="flex items-start gap-2">
                        <PlayCircle className="w-4 h-4 text-green-500 mt-0.5 flex-shrink-0" />
                        <div>
                            <span className="font-medium text-slate-700 dark:text-slate-300">Manual Scans</span>
                            <span className="text-slate-500 dark:text-slate-400"> — Run on-demand scans from the Dashboard whenever you want.</span>
                        </div>
                    </div>
                </div>
                <p className="mt-3 text-xs text-slate-500 dark:text-slate-500">
                    Tip: Use webhooks for new imports and scheduled scans for existing files.
                </p>
            </div>

            <button
                onClick={onComplete}
                className="w-full py-3 px-4 bg-gradient-to-r from-green-500 to-emerald-600 hover:from-green-600 hover:to-emerald-700 text-white font-semibold rounded-xl transition-all shadow-lg shadow-green-500/20 flex items-center justify-center gap-2"
            >
                <span>Go to Dashboard</span>
                <ArrowRight className="w-5 h-5" />
            </button>
        </motion.div>
    );
}
