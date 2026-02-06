import { useState } from 'react';
import { motion } from 'framer-motion';
import { ArrowRight, ArrowLeft, AlertCircle, Eye, EyeOff } from 'lucide-react';
import api, { getSetupStatus } from '../../lib/api';
import type { PasswordStepProps } from './types';

export default function PasswordStep({ onNext, onBack }: PasswordStepProps) {
    const [password, setPassword] = useState('');
    const [confirmPassword, setConfirmPassword] = useState('');
    const [showPassword, setShowPassword] = useState(false);
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState('');

    const handleSubmit = async () => {
        setLoading(true);
        setError('');

        try {
            // Check if password is already set (e.g., from restore or previous wizard attempt)
            const status = await getSetupStatus();
            if (status.has_password) {
                onNext(undefined);
                return;
            }

            if (password !== confirmPassword) {
                setError('Passwords do not match');
                setLoading(false);
                return;
            }
            if (password.length < 1) {
                setError('Password is required');
                setLoading(false);
                return;
            }

            const response = await api.post('/auth/setup', { password });
            const token = response.data.token || response.data.api_key;
            onNext(token || undefined);
        } catch (err: unknown) {
            const error = err as { response?: { data?: { error?: string } } };
            setError(error.response?.data?.error || 'Failed to set password');
        } finally {
            setLoading(false);
        }
    };

    return (
        <motion.div
            key="password"
            initial={{ opacity: 0, x: 20 }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: -20 }}
            className="space-y-6"
        >
            <div className="text-center">
                <h2 className="text-2xl font-bold text-slate-900 dark:text-white mb-2">
                    Secure Your Instance
                </h2>
                <p className="text-slate-600 dark:text-slate-400">
                    Create a password to protect your Healarr dashboard.
                </p>
            </div>

            {error && (
                <motion.div
                    initial={{ opacity: 0, y: -10 }}
                    animate={{ opacity: 1, y: 0 }}
                    className="p-3 bg-red-500/10 border border-red-500/20 rounded-lg text-sm text-red-600 dark:text-red-300 flex items-center gap-2"
                >
                    <AlertCircle className="w-4 h-4 flex-shrink-0" />
                    {error}
                </motion.div>
            )}

            <div className="space-y-4">
                <div>
                    <label htmlFor="setup-password" className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                        Password
                    </label>
                    <div className="relative">
                        <input
                            id="setup-password"
                            type={showPassword ? 'text' : 'password'}
                            value={password}
                            onChange={(e) => setPassword(e.target.value)}
                            className="w-full px-4 py-3 pr-12 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-green-500/50 focus:border-green-500"
                            placeholder="Enter your password"
                        />
                        <button
                            type="button"
                            onClick={() => setShowPassword(!showPassword)}
                            className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-400 hover:text-slate-600 dark:hover:text-slate-300"
                        >
                            {showPassword ? <EyeOff className="w-5 h-5" /> : <Eye className="w-5 h-5" />}
                        </button>
                    </div>
                </div>

                <div>
                    <label htmlFor="setup-confirm-password" className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                        Confirm Password
                    </label>
                    <input
                        id="setup-confirm-password"
                        type={showPassword ? 'text' : 'password'}
                        value={confirmPassword}
                        onChange={(e) => setConfirmPassword(e.target.value)}
                        className="w-full px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-green-500/50 focus:border-green-500"
                        placeholder="Confirm your password"
                    />
                </div>
            </div>

            <div className="flex gap-3">
                <button
                    onClick={onBack}
                    className="px-4 py-3 rounded-xl border border-slate-300 dark:border-slate-600 text-slate-700 dark:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 transition-colors flex items-center gap-2 cursor-pointer"
                >
                    <ArrowLeft className="w-4 h-4" />
                    Back
                </button>
                <button
                    onClick={handleSubmit}
                    disabled={loading || !password || password !== confirmPassword}
                    className="flex-1 py-3 px-4 bg-gradient-to-r from-green-500 to-emerald-600 hover:from-green-600 hover:to-emerald-700 text-white font-semibold rounded-xl transition-all shadow-lg shadow-green-500/20 flex items-center justify-center gap-2 disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
                >
                    {loading ? (
                        <div className="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                    ) : (
                        <>
                            <span>Continue</span>
                            <ArrowRight className="w-5 h-5" />
                        </>
                    )}
                </button>
            </div>
        </motion.div>
    );
}
