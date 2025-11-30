import { useState, useEffect, useCallback } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import { Folder, FolderOpen, Home, ArrowUp, X, Loader2, AlertCircle, ChevronRight } from 'lucide-react';
import { browseDirectory, type BrowseResponse } from '../../lib/api';

interface FileBrowserProps {
    isOpen: boolean;
    onClose: () => void;
    onSelect: (path: string) => void;
    initialPath?: string;
}

const FileBrowser = ({ isOpen, onClose, onSelect, initialPath = '/' }: FileBrowserProps) => {
    const [currentPath, setCurrentPath] = useState(initialPath || '/');
    const [entries, setEntries] = useState<BrowseResponse['entries']>([]);
    const [parentPath, setParentPath] = useState<string | null>(null);
    const [isLoading, setIsLoading] = useState(false);
    const [error, setError] = useState<string | null>(null);

    const loadDirectory = useCallback(async (path: string) => {
        setIsLoading(true);
        setError(null);
        try {
            const response = await browseDirectory(path);
            setCurrentPath(response.current_path);
            setParentPath(response.parent_path);
            setEntries(response.entries);
            if (response.error) {
                setError(response.error);
            }
        } catch (err) {
            setError('Failed to load directory');
            console.error('Browse error:', err);
        } finally {
            setIsLoading(false);
        }
    }, []);

    // Load initial directory when modal opens
    useEffect(() => {
        if (isOpen) {
            loadDirectory(initialPath || '/');
        }
    }, [isOpen, initialPath, loadDirectory]);

    // Handle keyboard events
    useEffect(() => {
        const handleKeyDown = (e: KeyboardEvent) => {
            if (!isOpen) return;
            if (e.key === 'Escape') {
                onClose();
            }
        };
        window.addEventListener('keydown', handleKeyDown);
        return () => window.removeEventListener('keydown', handleKeyDown);
    }, [isOpen, onClose]);

    const handleSelect = () => {
        onSelect(currentPath);
        onClose();
    };

    const navigateTo = (path: string) => {
        loadDirectory(path);
    };

    // Parse path into breadcrumb segments
    const pathSegments = currentPath.split('/').filter(Boolean);

    if (!isOpen) return null;

    return (
        <AnimatePresence>
            <motion.div
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                exit={{ opacity: 0 }}
                className="fixed inset-0 z-50 flex items-center justify-center p-4"
                onClick={onClose}
            >
                {/* Backdrop */}
                <div className="absolute inset-0 bg-black/50 backdrop-blur-sm" />

                {/* Modal */}
                <motion.div
                    initial={{ opacity: 0, scale: 0.95, y: 20 }}
                    animate={{ opacity: 1, scale: 1, y: 0 }}
                    exit={{ opacity: 0, scale: 0.95, y: 20 }}
                    transition={{ duration: 0.2, ease: 'easeOut' }}
                    className="relative bg-white dark:bg-slate-900 rounded-xl border border-slate-200 dark:border-slate-800 shadow-2xl w-full max-w-2xl max-h-[70vh] flex flex-col overflow-hidden"
                    onClick={(e) => e.stopPropagation()}
                >
                    {/* Header */}
                    <div className="flex items-center justify-between px-6 py-4 border-b border-slate-200 dark:border-slate-800">
                        <div className="flex items-center gap-3">
                            <FolderOpen className="w-5 h-5 text-blue-500" />
                            <h2 className="text-lg font-semibold text-slate-900 dark:text-white">Browse Directory</h2>
                        </div>
                        <button
                            onClick={onClose}
                            className="p-2 text-slate-400 hover:text-slate-600 dark:hover:text-slate-300 hover:bg-slate-100 dark:hover:bg-slate-800 rounded-lg transition-colors"
                        >
                            <X className="w-5 h-5" />
                        </button>
                    </div>

                    {/* Breadcrumb Navigation */}
                    <div className="flex items-center gap-1 px-6 py-3 bg-slate-50 dark:bg-slate-800/50 border-b border-slate-200 dark:border-slate-800 overflow-x-auto">
                        <button
                            onClick={() => navigateTo('/')}
                            className="flex items-center gap-1 px-2 py-1 text-sm text-slate-600 dark:text-slate-400 hover:text-blue-600 dark:hover:text-blue-400 hover:bg-slate-200 dark:hover:bg-slate-700 rounded transition-colors"
                        >
                            <Home className="w-4 h-4" />
                        </button>
                        {pathSegments.map((segment, index) => {
                            const segmentPath = '/' + pathSegments.slice(0, index + 1).join('/');
                            const isLast = index === pathSegments.length - 1;
                            return (
                                <div key={segmentPath} className="flex items-center">
                                    <ChevronRight className="w-4 h-4 text-slate-400 flex-shrink-0" />
                                    <button
                                        onClick={() => !isLast && navigateTo(segmentPath)}
                                        className={`px-2 py-1 text-sm rounded transition-colors truncate max-w-[150px] ${
                                            isLast
                                                ? 'text-slate-900 dark:text-white font-medium cursor-default'
                                                : 'text-slate-600 dark:text-slate-400 hover:text-blue-600 dark:hover:text-blue-400 hover:bg-slate-200 dark:hover:bg-slate-700'
                                        }`}
                                        title={segment}
                                    >
                                        {segment}
                                    </button>
                                </div>
                            );
                        })}
                    </div>

                    {/* Directory Contents */}
                    <div className="flex-1 overflow-y-auto min-h-[200px]">
                        {isLoading ? (
                            <div className="flex items-center justify-center h-full py-12">
                                <Loader2 className="w-8 h-8 text-blue-500 animate-spin" />
                            </div>
                        ) : error ? (
                            <div className="flex flex-col items-center justify-center h-full py-12 text-center px-6">
                                <AlertCircle className="w-12 h-12 text-amber-500 mb-3" />
                                <p className="text-slate-600 dark:text-slate-400">{error}</p>
                                <button
                                    onClick={() => loadDirectory('/')}
                                    className="mt-4 px-4 py-2 text-sm text-blue-600 dark:text-blue-400 hover:bg-blue-50 dark:hover:bg-blue-900/30 rounded-lg transition-colors"
                                >
                                    Go to root
                                </button>
                            </div>
                        ) : (
                            <div className="p-2">
                                {/* Parent directory entry */}
                                {parentPath !== null && (
                                    <button
                                        onClick={() => navigateTo(parentPath)}
                                        className="w-full flex items-center gap-3 px-4 py-3 text-left hover:bg-slate-100 dark:hover:bg-slate-800 rounded-lg transition-colors group"
                                    >
                                        <ArrowUp className="w-5 h-5 text-slate-400 group-hover:text-blue-500" />
                                        <span className="text-slate-600 dark:text-slate-400 group-hover:text-slate-900 dark:group-hover:text-white">..</span>
                                    </button>
                                )}

                                {/* Directory entries */}
                                {entries.length === 0 && !parentPath ? (
                                    <div className="flex flex-col items-center justify-center py-12 text-center">
                                        <Folder className="w-12 h-12 text-slate-300 dark:text-slate-600 mb-3" />
                                        <p className="text-slate-500 dark:text-slate-400">This directory is empty</p>
                                    </div>
                                ) : (
                                    entries.map((entry) => (
                                        <button
                                            key={entry.path}
                                            onClick={() => navigateTo(entry.path)}
                                            className="w-full flex items-center gap-3 px-4 py-3 text-left hover:bg-slate-100 dark:hover:bg-slate-800 rounded-lg transition-colors group"
                                        >
                                            <Folder className="w-5 h-5 text-amber-500 group-hover:text-amber-400 flex-shrink-0" />
                                            <span className="text-slate-700 dark:text-slate-300 group-hover:text-slate-900 dark:group-hover:text-white truncate">
                                                {entry.name}
                                            </span>
                                            <ChevronRight className="w-4 h-4 text-slate-300 dark:text-slate-600 ml-auto flex-shrink-0 opacity-0 group-hover:opacity-100 transition-opacity" />
                                        </button>
                                    ))
                                )}
                            </div>
                        )}
                    </div>

                    {/* Footer */}
                    <div className="flex items-center justify-between px-6 py-4 border-t border-slate-200 dark:border-slate-800 bg-slate-50 dark:bg-slate-800/50">
                        <div className="text-sm text-slate-500 dark:text-slate-400 truncate flex-1 mr-4">
                            <span className="font-medium">Selected:</span> {currentPath}
                        </div>
                        <div className="flex items-center gap-3">
                            <button
                                onClick={onClose}
                                className="px-4 py-2 text-sm text-slate-600 dark:text-slate-400 hover:text-slate-900 dark:hover:text-white hover:bg-slate-200 dark:hover:bg-slate-700 rounded-lg transition-colors"
                            >
                                Cancel
                            </button>
                            <button
                                onClick={handleSelect}
                                className="px-4 py-2 text-sm bg-blue-600 hover:bg-blue-700 text-white rounded-lg transition-colors font-medium"
                            >
                                Select
                            </button>
                        </div>
                    </div>
                </motion.div>
            </motion.div>
        </AnimatePresence>
    );
};

export default FileBrowser;
