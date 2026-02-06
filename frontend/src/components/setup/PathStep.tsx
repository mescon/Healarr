import { useState } from 'react';
import { motion } from 'framer-motion';
import { ArrowRight, ArrowLeft, FolderOpen, AlertCircle } from 'lucide-react';
import { createScanPath } from '../../lib/api';
import FileBrowser from '../ui/FileBrowser';
import type { PathStepProps } from './types';
import { DEFAULT_PATH_DATA, type PathFormData } from './types';

export default function PathStep({ initialData, rootFolders, loadingRootFolders, arrInstanceId, onNext, onBack, onSkip }: PathStepProps) {
    const [pathData, setPathData] = useState<PathFormData>(initialData ?? DEFAULT_PATH_DATA);
    const [fileBrowserOpen, setFileBrowserOpen] = useState(false);
    const [loading, setLoading] = useState(false);
    const [error, setError] = useState('');

    const handleCreate = async () => {
        if (!pathData.local_path || !pathData.arr_path) {
            setError('Both local and remote paths are required');
            return;
        }

        setLoading(true);
        setError('');

        try {
            await createScanPath({
                local_path: pathData.local_path,
                arr_path: pathData.arr_path,
                arr_instance_id: pathData.arr_instance_id ?? arrInstanceId,
                enabled: true,
                auto_remediate: true,
            });
            onNext();
        } catch (err: unknown) {
            const error = err as { response?: { data?: { error?: string } } };
            setError(error.response?.data?.error || 'Failed to create scan path');
        } finally {
            setLoading(false);
        }
    };

    return (
        <motion.div
            key="path"
            initial={{ opacity: 0, x: 20 }}
            animate={{ opacity: 1, x: 0 }}
            exit={{ opacity: 0, x: -20 }}
            className="space-y-6"
        >
            <div className="text-center">
                <h2 className="text-2xl font-bold text-slate-900 dark:text-white mb-2">
                    Add a Scan Path
                </h2>
                <p className="text-slate-600 dark:text-slate-400">
                    Configure where Healarr should look for media files to monitor.
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
                    <label htmlFor="path-local" className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                        Local Path (where files are on this server)
                    </label>
                    <div className="flex gap-2">
                        <input
                            id="path-local"
                            type="text"
                            value={pathData.local_path}
                            onChange={(e) => setPathData(prev => ({ ...prev, local_path: e.target.value }))}
                            className="flex-1 px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-green-500/50 focus:border-green-500 font-mono"
                            placeholder="/data/media/tv"
                        />
                        <button
                            type="button"
                            onClick={() => setFileBrowserOpen(true)}
                            className="px-4 py-3 bg-slate-200 dark:bg-slate-700 hover:bg-slate-300 dark:hover:bg-slate-600 border border-slate-300 dark:border-slate-600 rounded-xl text-slate-700 dark:text-slate-300 transition-colors flex items-center gap-2"
                            title="Browse directories"
                        >
                            <FolderOpen className="w-5 h-5" />
                            <span className="hidden sm:inline">Browse</span>
                        </button>
                    </div>
                </div>

                <div>
                    <label htmlFor="path-remote" className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">
                        Remote Path (as seen by *arr)
                        {rootFolders.length > 0 && (
                            <span className="ml-2 text-slate-500 dark:text-slate-400 font-normal">
                                ({rootFolders.length} root folder{rootFolders.length !== 1 ? 's' : ''} detected)
                            </span>
                        )}
                    </label>
                    {loadingRootFolders ? (
                        <div className="flex items-center gap-2 text-slate-500 dark:text-slate-400">
                            <div className="w-4 h-4 border-2 border-slate-400/30 border-t-slate-400 rounded-full animate-spin" />
                            Loading root folders...
                        </div>
                    ) : rootFolders.length > 0 ? (
                        <select
                            id="path-remote"
                            value={pathData.arr_path}
                            onChange={(e) => setPathData(prev => ({ ...prev, arr_path: e.target.value }))}
                            className="w-full px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white focus:outline-none focus:ring-2 focus:ring-green-500/50 focus:border-green-500 font-mono"
                        >
                            <option value="">Select a root folder...</option>
                            {rootFolders.map(folder => (
                                <option key={folder.id} value={folder.path}>
                                    {folder.path}
                                </option>
                            ))}
                        </select>
                    ) : (
                        <input
                            id="path-remote"
                            type="text"
                            value={pathData.arr_path}
                            onChange={(e) => setPathData(prev => ({ ...prev, arr_path: e.target.value }))}
                            className="w-full px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border border-slate-300 dark:border-slate-700 rounded-xl text-slate-900 dark:text-white placeholder-slate-400 dark:placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-green-500/50 focus:border-green-500 font-mono"
                            placeholder="/tv"
                        />
                    )}
                    <p className="mt-1 text-xs text-slate-500 dark:text-slate-400">
                        This path should match the root folder configured in your *arr instance.
                    </p>
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
                    onClick={handleCreate}
                    disabled={loading || !pathData.local_path || !pathData.arr_path}
                    className="flex-1 py-3 px-4 bg-gradient-to-r from-green-500 to-emerald-600 hover:from-green-600 hover:to-emerald-700 text-white font-semibold rounded-xl transition-all shadow-lg shadow-green-500/20 flex items-center justify-center gap-2 disabled:opacity-50 cursor-pointer disabled:cursor-not-allowed"
                >
                    {loading ? (
                        <div className="w-5 h-5 border-2 border-white/30 border-t-white rounded-full animate-spin" />
                    ) : (
                        <>
                            <span>Add Path</span>
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

            <FileBrowser
                isOpen={fileBrowserOpen}
                onClose={() => setFileBrowserOpen(false)}
                onSelect={(path) => setPathData(prev => ({ ...prev, local_path: path }))}
                initialPath={pathData.local_path || '/'}
            />
        </motion.div>
    );
}
