import { useState } from 'react';
import { motion } from 'framer-motion';
import { ArrowRight, Upload, Database, Wand2, AlertCircle } from 'lucide-react';
import { importConfigPublic, restoreDatabasePublic } from '../../lib/api';
import type { ConfigExport } from '../../lib/api';
import type { WelcomeStepProps } from './types';

export default function WelcomeStep({ onFreshStart, onRestoreComplete, onSkip }: WelcomeStepProps) {
    const [importMode, setImportMode] = useState<'fresh' | 'restore' | null>(null);
    const [configFile, setConfigFile] = useState<File | null>(null);
    const [databaseFile, setDatabaseFile] = useState<File | null>(null);
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState('');

    const handleRestore = async () => {
        if (!configFile && !databaseFile) return;
        setLoading(true);
        setError('');

        try {
            let restartRequired = false;

            if (databaseFile) {
                const result = await restoreDatabasePublic(databaseFile);
                restartRequired = !!result.restart_required;
            }

            if (configFile) {
                const text = await configFile.text();
                const config = JSON.parse(text) as Partial<ConfigExport>;
                await importConfigPublic(config);
            }

            if (restartRequired) {
                window.location.reload();
                return;
            }

            await onRestoreComplete();
        } catch (err) {
            const errorMessage = databaseFile && configFile
                ? 'Failed to restore. Please check your files.'
                : databaseFile
                    ? 'Failed to restore database. Please check the file format.'
                    : 'Failed to import configuration. Please check the file format.';
            setError(errorMessage);
            console.error(err);
        } finally {
            setLoading(false);
        }
    };

    return (
        <motion.div
            key="welcome"
            initial={{ opacity: 0, x: 20 }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: -20 }}
            className="space-y-6"
        >
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

            <div className="text-center">
                <h2 className="text-2xl font-bold text-slate-900 dark:text-white mb-2">
                    Welcome to Healarr
                </h2>
                <p className="text-slate-600 dark:text-slate-400">
                    Let's get your instance set up in just a few steps.
                </p>
            </div>

            <div className="grid gap-4">
                <button
                    onClick={() => {
                        setImportMode('fresh');
                        onFreshStart();
                    }}
                    className="flex items-center gap-4 p-4 rounded-xl border-2 border-slate-200 dark:border-slate-700 hover:border-green-500 dark:hover:border-green-500 transition-colors text-left group cursor-pointer"
                >
                    <div className="p-3 rounded-lg bg-green-100 dark:bg-green-900/30 text-green-600 dark:text-green-400 group-hover:bg-green-500 group-hover:text-white transition-colors">
                        <Wand2 className="w-6 h-6" />
                    </div>
                    <div>
                        <h3 className="font-semibold text-slate-900 dark:text-white">Fresh Start</h3>
                        <p className="text-sm text-slate-600 dark:text-slate-400">
                            Set up a new Healarr instance from scratch
                        </p>
                    </div>
                    <ArrowRight className="w-5 h-5 ml-auto text-slate-400 group-hover:text-green-500 transition-colors" />
                </button>

                <button
                    onClick={() => setImportMode('restore')}
                    className={`flex items-center gap-4 p-4 rounded-xl border-2 transition-colors text-left group cursor-pointer ${
                        importMode === 'restore'
                            ? 'border-green-500 bg-green-50 dark:bg-green-900/20'
                            : 'border-slate-200 dark:border-slate-700 hover:border-green-500 dark:hover:border-green-500'
                    }`}
                >
                    <div className="p-3 rounded-lg bg-blue-100 dark:bg-blue-900/30 text-blue-600 dark:text-blue-400">
                        <Upload className="w-6 h-6" />
                    </div>
                    <div>
                        <h3 className="font-semibold text-slate-900 dark:text-white">Restore from Backup</h3>
                        <p className="text-sm text-slate-600 dark:text-slate-400">
                            Import config JSON, database backup, or both
                        </p>
                    </div>
                </button>
            </div>

            {importMode === 'restore' && (
                <motion.div
                    initial={{ opacity: 0, height: 0 }}
                    animate={{ opacity: 1, height: 'auto' }}
                    className="space-y-4"
                >
                    {/* Database backup upload */}
                    <div className="space-y-2">
                        <span className="text-sm font-medium text-slate-700 dark:text-slate-300 flex items-center gap-2">
                            <Database className="w-4 h-4 text-purple-400" />
                            Database Backup <span className="text-slate-500 text-xs">(optional)</span>
                        </span>
                        <label
                            htmlFor="database-upload"
                            className={`block border-2 border-dashed rounded-xl p-4 text-center transition-colors cursor-pointer hover:border-purple-400 ${
                                databaseFile ? 'border-purple-500 bg-purple-50 dark:bg-purple-900/20' : 'border-slate-300 dark:border-slate-600'
                            }`}
                        >
                            <input
                                type="file"
                                accept=".db,.sqlite,.sqlite3"
                                onChange={(e) => setDatabaseFile(e.target.files?.[0] || null)}
                                className="hidden"
                                id="database-upload"
                            />
                            <span className="text-sm text-slate-600 dark:text-slate-400">
                                {databaseFile ? (
                                    <span className="text-purple-600 dark:text-purple-400 font-medium">{databaseFile.name}</span>
                                ) : (
                                    'Click to select .db file'
                                )}
                            </span>
                        </label>
                    </div>

                    {/* Config JSON upload */}
                    <div className="space-y-2">
                        <span className="text-sm font-medium text-slate-700 dark:text-slate-300 flex items-center gap-2">
                            <Upload className="w-4 h-4 text-blue-400" />
                            Configuration JSON <span className="text-slate-500 text-xs">(optional)</span>
                        </span>
                        <label
                            htmlFor="config-upload"
                            className={`block border-2 border-dashed rounded-xl p-4 text-center transition-colors cursor-pointer hover:border-blue-400 ${
                                configFile ? 'border-blue-500 bg-blue-50 dark:bg-blue-900/20' : 'border-slate-300 dark:border-slate-600'
                            }`}
                        >
                            <input
                                type="file"
                                accept=".json"
                                onChange={(e) => setConfigFile(e.target.files?.[0] || null)}
                                className="hidden"
                                id="config-upload"
                            />
                            <span className="text-sm text-slate-600 dark:text-slate-400">
                                {configFile ? (
                                    <span className="text-blue-600 dark:text-blue-400 font-medium">{configFile.name}</span>
                                ) : (
                                    'Click to select .json file'
                                )}
                            </span>
                        </label>
                    </div>

                    <p className="text-xs text-slate-500 dark:text-slate-400 text-center">
                        You can provide either file or both. Database is restored first, then config is imported on top.
                    </p>

                    <button
                        onClick={handleRestore}
                        disabled={(!configFile && !databaseFile) || loading}
                        className="w-full py-3 px-4 bg-gradient-to-r from-green-500 to-emerald-600 hover:from-green-600 hover:to-emerald-700 text-white font-semibold rounded-xl transition-all shadow-lg shadow-green-500/20 flex items-center justify-center gap-2 disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
                    >
                        {loading ? (
                            <div className="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                        ) : (
                            <>
                                <span>
                                    {databaseFile && configFile ? 'Restore & Import' : databaseFile ? 'Restore Database' : 'Import Configuration'}
                                </span>
                                <ArrowRight className="w-5 h-5" />
                            </>
                        )}
                    </button>
                </motion.div>
            )}

            <div className="pt-4 border-t border-slate-200 dark:border-slate-700">
                <button
                    onClick={onSkip}
                    className="w-full text-sm text-slate-500 dark:text-slate-400 hover:text-slate-700 dark:hover:text-slate-300 transition-colors cursor-pointer"
                >
                    Skip setup for now (power users)
                </button>
            </div>
        </motion.div>
    );
}
