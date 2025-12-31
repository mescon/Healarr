import { useState, useEffect } from 'react';
import { useNavigate } from 'react-router-dom';
import { motion } from 'framer-motion';
import { Lock, Activity, ArrowRight } from 'lucide-react';
import api, { getAuthStatus } from '../lib/api';
import { useWebSocket } from '../contexts/WebSocketProvider';

const Login = () => {
    const [password, setPassword] = useState('');
    const [isSetup, setIsSetup] = useState(false);
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState('');
    const navigate = useNavigate();
    const { reconnect } = useWebSocket();

    useEffect(() => {
        const checkStatus = async () => {
            try {
                const status = await getAuthStatus();
                setIsSetup(!status.is_setup);
            } catch (err) {
                console.error('Failed to check auth status:', err);
            }
        };
        checkStatus();
    }, []);

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        setLoading(true);
        setError('');

        // Check if there's already a token
        const existingToken = localStorage.getItem('healarr_token');
        if (existingToken) {
            navigate('/');
            setLoading(false); // Ensure loading is reset if navigated away
            return;
        }

        try {
            const endpoint = isSetup ? '/auth/setup' : '/auth/login';
            const response = await api.post(endpoint, { password });

            const token = response.data.token || response.data.api_key;

            if (token) {
                localStorage.setItem('healarr_token', token);
                // Reconnect WebSocket with the new token
                reconnect();
                navigate('/');
            } else {
                console.error('No token in response:', response.data);
                setError('Setup successful but no token received. Please try logging in.');
                setIsSetup(false);
            }
        } catch (err: unknown) {
            const error = err as { response?: { status: number; data?: { error?: string } } };
            if (error.response?.status === 401 && error.response?.data?.error === 'Setup required') {
                setIsSetup(true);
                setError('No password set. Please create one.');
            } else {
                setError(error.response?.data?.error || 'Login failed');
            }
        } finally {
            setLoading(false);
        }
    };

    return (
        <div className="min-h-screen bg-gradient-to-br from-slate-100 via-slate-50 to-slate-100 dark:from-slate-950 dark:via-slate-900 dark:to-slate-950 flex items-center justify-center p-4">
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                className="w-full max-w-md"
            >
                {/* Logo/Header */}
                <div className="text-center mb-8">
                    <div className="inline-flex items-center justify-center w-16 h-16 bg-gradient-to-br from-green-500 to-emerald-600 rounded-2xl shadow-lg shadow-green-500/20 mb-4">
                        <Activity className="text-white w-8 h-8" />
                    </div>
                    <h1 className="text-3xl font-bold text-slate-900 dark:text-white mb-2">Healarr</h1>
                    <p className="text-slate-600 dark:text-slate-400">Health Evaluation And Library Auto-Recovery for *aRR</p>
                </div>

                {/* Login Card */}
                <div className="bg-white/80 dark:bg-slate-900/50 backdrop-blur-xl border border-slate-200 dark:border-slate-800/50 rounded-2xl p-8 shadow-2xl">
                    <h2 className="text-xl font-semibold text-slate-900 dark:text-white mb-6 flex items-center gap-2">
                        <Lock className="w-5 h-5 text-green-500 dark:text-green-400" />
                        {isSetup ? 'Initial Setup' : 'Login'}
                    </h2>

                    {isSetup && (
                        <div className="mb-4 p-3 bg-blue-500/10 border border-blue-500/20 rounded-lg text-sm text-blue-600 dark:text-blue-300">
                            Create a password to secure your Healarr instance. This password will be required for all UI access.
                        </div>
                    )}

                    {error && (
                        <div className="mb-4 p-3 bg-red-500/10 border border-red-500/20 rounded-lg text-sm text-red-600 dark:text-red-300">
                            {error}
                        </div>
                    )}

                    <form onSubmit={handleSubmit} className="space-y-4">
                        <div>
                            <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                                Password
                            </label>
                            <input
                                type="password"
                                value={password}
                                onChange={(e) => setPassword(e.target.value)}
                                className="w-full px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus focus:ring-green-500/50 focus:border-green-500"
                                placeholder="Enter your password"
                                required
                                minLength={1}
                                autoComplete="current-password"
                                autoFocus
                            />
                        </div>

                        <button
                            type="submit"
                            disabled={loading}
                            className="w-full py-3 px-4 bg-gradient-to-r from-green-500 to-emerald-600 hover:from-green-600 hover:to-emerald-700 text-white font-semibold rounded-xl transition-all shadow-lg shadow-green-500/20 flex items-center justify-center gap-2 disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer"
                        >
                            {loading ? (
                                <div className="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                            ) : (
                                <>
                                    <span>{isSetup ? "Create Password" : "Login"}</span>
                                    <ArrowRight className="w-5 h-5" />
                                </>
                            )}
                        </button>
                    </form>


                </div>
            </motion.div>
        </div>
    );
};

export default Login;
