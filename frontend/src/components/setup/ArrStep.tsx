import { useState } from 'react';
import { motion } from 'framer-motion';
import { ArrowRight, ArrowLeft, Server, AlertCircle, CheckCircle2, ExternalLink } from 'lucide-react';
import { testArrConnection, createArrInstance } from '../../lib/api';
import type { ArrStepProps } from './types';
import { DEFAULT_ARR_DATA, type ArrFormData } from './types';

export default function ArrStep({ initialData, initialTested, onNext, onBack, onSkip }: ArrStepProps) {
    const [arrData, setArrData] = useState<ArrFormData>(initialData ?? DEFAULT_ARR_DATA);
    const [arrTested, setArrTested] = useState(initialTested ?? false);
    const [arrTestResult, setArrTestResult] = useState<{ success: boolean; message?: string } | null>(null);
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState('');

    const handleTest = async () => {
        if (!arrData.url || !arrData.api_key) {
            setError('URL and API key are required');
            return;
        }

        setLoading(true);
        setError('');
        setArrTestResult(null);

        try {
            const result = await testArrConnection(arrData.url, arrData.api_key);
            setArrTestResult(result);
            setArrTested(result.success);
        } catch (err: unknown) {
            const error = err as { response?: { data?: { error?: string } } };
            setArrTestResult({ success: false, message: error.response?.data?.error || 'Connection failed' });
        } finally {
            setLoading(false);
        }
    };

    const handleCreate = async () => {
        if (!arrTested) {
            setError('Please test the connection first');
            return;
        }

        setLoading(true);
        setError('');

        try {
            const result = await createArrInstance({
                name: arrData.name || `${arrData.type}-${Date.now()}`,
                type: arrData.type,
                url: arrData.url,
                api_key: arrData.api_key,
                enabled: true,
            });
            onNext(result.id);
        } catch (err: unknown) {
            const error = err as { response?: { data?: { error?: string } } };
            setError(error.response?.data?.error || 'Failed to create instance');
        } finally {
            setLoading(false);
        }
    };

    return (
        <motion.div
            key="arr"
            initial={{ opacity: 0, x: 20 }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: -20 }}
            className="space-y-6"
        >
            <div className="text-center">
                <h2 className="text-2xl font-bold text-slate-900 dark:text-white mb-2">
                    Connect Your *arr Instance
                </h2>
                <p className="text-slate-600 dark:text-slate-400">
                    Connect Sonarr, Radarr, or Whisparr to enable automatic media healing.
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
                    <label htmlFor="arr-type" className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                        Instance Type
                    </label>
                    <select
                        id="arr-type"
                        value={arrData.type}
                        onChange={(e) => setArrData(prev => ({ ...prev, type: e.target.value as ArrFormData['type'] }))}
                        className="w-full px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-green-500/50 focus:border-green-500"
                    >
                        <option value="sonarr">Sonarr</option>
                        <option value="radarr">Radarr</option>
                        <option value="whisparr-v2">Whisparr v2</option>
                        <option value="whisparr-v3">Whisparr v3</option>
                    </select>
                </div>

                <div>
                    <label htmlFor="arr-name" className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                        Name (optional)
                    </label>
                    <input
                        id="arr-name"
                        type="text"
                        value={arrData.name}
                        onChange={(e) => setArrData(prev => ({ ...prev, name: e.target.value }))}
                        className="w-full px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-green-500/50 focus:border-green-500"
                        placeholder={`My ${arrData.type}`}
                    />
                </div>

                <div>
                    <label htmlFor="arr-url" className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                        URL
                    </label>
                    <input
                        id="arr-url"
                        type="url"
                        value={arrData.url}
                        onChange={(e) => {
                            setArrData(prev => ({ ...prev, url: e.target.value }));
                            setArrTested(false);
                        }}
                        className="w-full px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-green-500/50 focus:border-green-500"
                        placeholder="http://localhost:8989"
                    />
                </div>

                <div>
                    <label htmlFor="arr-api-key" className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                        API Key
                        <a
                            href="https://wiki.servarr.com/sonarr/settings#security"
                            target="_blank"
                            rel="noopener noreferrer"
                            className="inline-flex items-center gap-1 ml-2 text-green-500 hover:text-green-600"
                        >
                            <span className="text-xs">Where to find this?</span>
                            <ExternalLink className="w-3 h-3" />
                        </a>
                    </label>
                    <input
                        id="arr-api-key"
                        type="text"
                        value={arrData.api_key}
                        onChange={(e) => {
                            setArrData(prev => ({ ...prev, api_key: e.target.value }));
                            setArrTested(false);
                        }}
                        className="w-full px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-green-500/50 focus:border-green-500 font-mono"
                        placeholder="xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
                    />
                </div>

                {arrTestResult && (
                    <div className={`p-3 rounded-lg flex items-center gap-2 ${
                        arrTestResult.success
                            ? 'bg-green-100 dark:bg-green-900/30 text-green-700 dark:text-green-300'
                            : 'bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-300'
                    }`}>
                        {arrTestResult.success ? (
                            <CheckCircle2 className="w-5 h-5" />
                        ) : (
                            <AlertCircle className="w-5 h-5" />
                        )}
                        <span className="text-sm">
                            {arrTestResult.success ? 'Connection successful!' : arrTestResult.message}
                        </span>
                    </div>
                )}

                <button
                    onClick={handleTest}
                    disabled={loading || !arrData.url || !arrData.api_key}
                    className="w-full py-2 px-4 border border-green-500 text-green-500 rounded-xl hover:bg-green-50 dark:hover:bg-green-900/20 transition-colors disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed flex items-center justify-center gap-2"
                >
                    {loading && !arrTested ? (
                        <div className="w-4 h-4 border-2 border-green-500/30 border-t-green-500 rounded-full animate-spin" />
                    ) : (
                        <Server className="w-4 h-4" />
                    )}
                    Test Connection
                </button>
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
                    onClick={handleCreate}
                    disabled={loading || !arrTested}
                    className="flex-1 py-3 px-4 bg-gradient-to-r from-green-500 to-emerald-600 hover:from-green-600 hover:to-emerald-700 text-white font-semibold rounded-xl transition-all shadow-lg shadow-green-500/20 flex items-center justify-center gap-2 disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
                >
                    {loading ? (
                        <div className="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                    ) : (
                        <>
                            <span>Add Instance</span>
                            <ArrowRight className="w-5 h-5" />
                        </>
                    )}
                </button>
            </div>

            <button
                onClick={onSkip}
                className="w-full text-sm text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-300 transition-colors cursor-pointer"
            >
                Skip for now
            </button>
        </motion.div>
    );
}
