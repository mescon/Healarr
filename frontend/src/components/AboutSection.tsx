import { useState, useEffect } from 'react';
import { useQuery } from '@tanstack/react-query';
import { motion, AnimatePresence } from 'framer-motion';
import {
    ArrowUpCircle, Check, ExternalLink, ChevronDown, Download,
    Server, Monitor, Clock, HardDrive, Github, Bug,
    CheckCircle, XCircle, AlertTriangle, Info
} from 'lucide-react';
import clsx from 'clsx';
import { checkForUpdates, getSystemInfo, type ToolStatus } from '../lib/api';

// Platform icons
const DockerIcon = ({ className }: { className?: string }) => (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor">
        <path d="M13.983 11.078h2.119a.186.186 0 00.186-.185V9.006a.186.186 0 00-.186-.186h-2.119a.185.185 0 00-.185.185v1.888c0 .102.083.185.185.185m-2.954-5.43h2.118a.186.186 0 00.186-.186V3.574a.186.186 0 00-.186-.185h-2.118a.185.185 0 00-.185.185v1.888c0 .102.082.185.185.186m0 2.716h2.118a.187.187 0 00.186-.186V6.29a.186.186 0 00-.186-.185h-2.118a.185.185 0 00-.185.185v1.887c0 .102.082.185.185.186m-2.93 0h2.12a.186.186 0 00.184-.186V6.29a.185.185 0 00-.185-.185H8.1a.185.185 0 00-.185.185v1.887c0 .102.083.185.185.186m-2.964 0h2.119a.186.186 0 00.185-.186V6.29a.185.185 0 00-.185-.185H5.136a.186.186 0 00-.186.185v1.887c0 .102.084.185.186.186m5.893 2.715h2.118a.186.186 0 00.186-.185V9.006a.186.186 0 00-.186-.186h-2.118a.185.185 0 00-.185.185v1.888c0 .102.082.185.185.185m-2.93 0h2.12a.185.185 0 00.184-.185V9.006a.185.185 0 00-.184-.186h-2.12a.185.185 0 00-.184.185v1.888c0 .102.083.185.185.185m-2.964 0h2.119a.185.185 0 00.185-.185V9.006a.185.185 0 00-.185-.186h-2.12a.186.186 0 00-.185.186v1.887c0 .102.084.185.186.185m-2.92 0h2.12a.185.185 0 00.184-.185V9.006a.185.185 0 00-.184-.186h-2.12a.185.185 0 00-.184.185v1.888c0 .102.082.185.185.185M23.763 9.89c-.065-.051-.672-.51-1.954-.51-.338.001-.676.03-1.01.087-.248-1.7-1.653-2.53-1.716-2.566l-.344-.199-.226.327c-.284.438-.49.922-.612 1.43-.23.97-.09 1.882.403 2.661-.595.332-1.55.413-1.744.42H.751a.751.751 0 00-.75.748 11.376 11.376 0 00.692 4.062c.545 1.428 1.355 2.48 2.41 3.124 1.18.723 3.1 1.137 5.275 1.137.983.003 1.963-.086 2.93-.266a12.248 12.248 0 003.823-1.389c.98-.567 1.86-1.288 2.61-2.136 1.252-1.418 1.998-2.997 2.553-4.4h.221c1.372 0 2.215-.549 2.68-1.009.309-.293.55-.65.707-1.046l.098-.288Z"/>
    </svg>
);

// TuxIcon - uses PNG image from icons folder (respects base path)
const TuxIcon = ({ className }: { className?: string }) => (
    <img src={`${import.meta.env.BASE_URL}icons/tux.png`} alt="Linux" className={className} />
);

const AppleIcon = ({ className }: { className?: string }) => (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor">
        <path d="M12.152 6.896c-.948 0-2.415-1.078-3.96-1.04-2.04.027-3.91 1.183-4.961 3.014-2.117 3.675-.546 9.103 1.519 12.09 1.013 1.454 2.208 3.09 3.792 3.039 1.52-.065 2.09-.987 3.935-.987 1.831 0 2.35.987 3.96.948 1.637-.026 2.676-1.48 3.676-2.948 1.156-1.688 1.636-3.325 1.662-3.415-.039-.013-3.182-1.221-3.22-4.857-.026-3.04 2.48-4.494 2.597-4.559-1.429-2.09-3.623-2.324-4.39-2.376-2-.156-3.675 1.09-4.61 1.09zM15.53 3.83c.843-1.012 1.4-2.427 1.245-3.83-1.207.052-2.662.805-3.532 1.818-.78.896-1.454 2.338-1.273 3.714 1.338.104 2.715-.688 3.559-1.701"/>
    </svg>
);

const WindowsIcon = ({ className }: { className?: string }) => (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor">
        <path d="M0 3.449L9.75 2.1v9.451H0m10.949-9.602L24 0v11.4H10.949M0 12.6h9.75v9.451L0 20.699M10.949 12.6H24V24l-12.9-1.801"/>
    </svg>
);

interface AboutSectionProps {
    showAsAccordion?: boolean;
}

const AboutSection = ({ showAsAccordion = false }: AboutSectionProps) => {
    const { data: updateInfo, isLoading: updateLoading, error: updateError, refetch } = useQuery({
        queryKey: ['updateCheck'],
        queryFn: checkForUpdates,
        staleTime: 300000, // 5 minutes
        retry: 1,
    });

    const { data: systemInfo, isLoading: systemLoading } = useQuery({
        queryKey: ['systemInfo'],
        queryFn: getSystemInfo,
        staleTime: 60000, // 1 minute
        retry: 1,
    });

    // Determine platform from system info
    const currentPlatform = systemInfo?.environment === 'docker' ? 'docker' : systemInfo?.os || 'unknown';

    // Update instructions collapsed state - expanded by default when update available
    const [updateInstructionsExpanded, setUpdateInstructionsExpanded] = useState(false);

    // Auto-expand when update becomes available
    useEffect(() => {
        if (updateInfo?.update_available) {
            setUpdateInstructionsExpanded(true);
        }
    }, [updateInfo?.update_available]);

    // Parse inline markdown elements (bold, links, URLs, code) into React nodes
    const parseInlineMarkdown = (text: string, keyPrefix: string): React.ReactNode[] => {
        const result: React.ReactNode[] = [];
        let remaining = text;
        let partIndex = 0;

        while (remaining.length > 0) {
            // Match markdown links [text](url), bold **text**, inline code `text`, or bare URLs
            const mdLinkMatch = remaining.match(/\[([^\]]+)\]\(([^)]+)\)/);
            const boldMatch = remaining.match(/\*\*([^*]+)\*\*/);
            const codeMatch = remaining.match(/`([^`]+)`/);
            const urlMatch = remaining.match(/(https?:\/\/[^\s<>\[\]()]+)/);

            // Find the earliest match
            const matches = [
                mdLinkMatch ? { type: 'mdLink', match: mdLinkMatch, index: mdLinkMatch.index! } : null,
                boldMatch ? { type: 'bold', match: boldMatch, index: boldMatch.index! } : null,
                codeMatch ? { type: 'code', match: codeMatch, index: codeMatch.index! } : null,
                urlMatch ? { type: 'url', match: urlMatch, index: urlMatch.index! } : null,
            ].filter(Boolean).sort((a, b) => a!.index - b!.index);

            if (matches.length === 0) {
                if (remaining) result.push(remaining);
                break;
            }

            const first = matches[0]!;

            if (first.index > 0) {
                result.push(remaining.substring(0, first.index));
            }

            if (first.type === 'mdLink') {
                const [fullMatch, linkText, linkUrl] = first.match as RegExpMatchArray;
                result.push(
                    <a
                        key={`${keyPrefix}-link-${partIndex++}`}
                        href={linkUrl}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-blue-500 hover:text-blue-400 underline"
                    >
                        {linkText}
                    </a>
                );
                remaining = remaining.substring(first.index + fullMatch.length);
            } else if (first.type === 'bold') {
                const [fullMatch, boldText] = first.match as RegExpMatchArray;
                result.push(
                    <strong key={`${keyPrefix}-bold-${partIndex++}`} className="font-semibold text-slate-700 dark:text-slate-300">
                        {boldText}
                    </strong>
                );
                remaining = remaining.substring(first.index + fullMatch.length);
            } else if (first.type === 'code') {
                const [fullMatch, codeText] = first.match as RegExpMatchArray;
                result.push(
                    <code key={`${keyPrefix}-code-${partIndex++}`} className="px-1.5 py-0.5 bg-slate-200 dark:bg-slate-800 rounded text-sm font-mono text-slate-700 dark:text-slate-300">
                        {codeText}
                    </code>
                );
                remaining = remaining.substring(first.index + fullMatch.length);
            } else if (first.type === 'url') {
                const [fullMatch] = first.match as RegExpMatchArray;
                result.push(
                    <a
                        key={`${keyPrefix}-url-${partIndex++}`}
                        href={fullMatch}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="text-blue-500 hover:text-blue-400 underline break-all"
                    >
                        {fullMatch}
                    </a>
                );
                remaining = remaining.substring(first.index + fullMatch.length);
            }
        }

        return result;
    };

    // Simple markdown-to-JSX renderer for changelog
    const renderMarkdown = (text: string) => {
        if (!text) return null;

        const lines = text.split('\n');
        const elements: React.ReactNode[] = [];
        let listItems: { key: string; content: React.ReactNode[] }[] = [];

        const flushList = () => {
            if (listItems.length > 0) {
                elements.push(
                    <ul key={`list-${elements.length}`} className="list-disc list-inside space-y-1 text-slate-600 dark:text-slate-400 ml-2">
                        {listItems.map((item) => (
                            <li key={item.key}>{item.content}</li>
                        ))}
                    </ul>
                );
                listItems = [];
            }
        };

        lines.forEach((line, index) => {
            const trimmed = line.trim();

            if (trimmed.startsWith('### ')) {
                flushList();
                elements.push(
                    <h4 key={index} className="text-md font-semibold text-slate-800 dark:text-slate-200 mt-4 mb-2">
                        {parseInlineMarkdown(trimmed.substring(4), `h4-${index}`)}
                    </h4>
                );
            } else if (trimmed.startsWith('## ')) {
                flushList();
                elements.push(
                    <h3 key={index} className="text-lg font-bold text-slate-900 dark:text-white mt-4 mb-2">
                        {parseInlineMarkdown(trimmed.substring(3), `h3-${index}`)}
                    </h3>
                );
            } else if (trimmed.startsWith('# ')) {
                flushList();
                elements.push(
                    <h2 key={index} className="text-xl font-bold text-slate-900 dark:text-white mt-4 mb-2">
                        {parseInlineMarkdown(trimmed.substring(2), `h2-${index}`)}
                    </h2>
                );
            } else if (trimmed.startsWith('- ') || trimmed.startsWith('* ')) {
                listItems.push({
                    key: `li-${index}`,
                    content: parseInlineMarkdown(trimmed.substring(2), `li-${index}`)
                });
            } else if (trimmed === '') {
                flushList();
            } else if (trimmed) {
                flushList();
                elements.push(
                    <p key={index} className="text-slate-600 dark:text-slate-400 my-2">
                        {parseInlineMarkdown(trimmed, `p-${index}`)}
                    </p>
                );
            }
        });

        flushList();
        return elements;
    };

    const isLoading = updateLoading || systemLoading;
    const error = updateError;

    // Check for missing required tools
    const missingRequiredTools = systemInfo?.tools
        ? Object.values(systemInfo.tools).filter(t => t.required && !t.available)
        : [];

    if (isLoading) {
        return (
            <div className="p-6 text-center">
                <div className="animate-spin w-6 h-6 border-2 border-slate-300 border-t-green-500 rounded-full mx-auto mb-2"></div>
                <p className="text-slate-500">Loading system information...</p>
            </div>
        );
    }

    if (error) {
        return (
            <div className="p-6 text-center">
                <p className="text-red-400 mb-2">Unable to load system information</p>
                <p className="text-slate-500 text-sm mb-4">Please check your connection</p>
                <button
                    onClick={() => refetch()}
                    className="px-4 py-2 bg-slate-700 hover:bg-slate-600 text-white rounded-lg transition-colors cursor-pointer"
                >
                    Retry
                </button>
            </div>
        );
    }

    const content = (
        <div className="space-y-6">
            {/* Missing Tools Warning */}
            {missingRequiredTools.length > 0 && (
                <div className="p-4 rounded-xl bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-700">
                    <div className="flex items-start gap-3">
                        <AlertTriangle className="w-5 h-5 text-red-500 flex-shrink-0 mt-0.5" />
                        <div>
                            <h4 className="font-semibold text-red-700 dark:text-red-400">Missing Required Tools</h4>
                            <p className="text-sm text-red-600 dark:text-red-300 mt-1">
                                The following tools are required for scans to work:
                            </p>
                            <ul className="mt-2 space-y-1">
                                {missingRequiredTools.map(tool => (
                                    <li key={tool.name} className="text-sm text-red-600 dark:text-red-300 flex items-center gap-2">
                                        <XCircle className="w-4 h-4" />
                                        <span className="font-mono">{tool.name}</span> - {tool.description}
                                    </li>
                                ))}
                            </ul>
                            <p className="text-xs text-red-500 dark:text-red-400 mt-3">
                                Install the missing tools and restart Healarr.
                            </p>
                        </div>
                    </div>
                </div>
            )}

            {/* Version Status */}
            <div className="flex items-center justify-between p-4 rounded-xl bg-slate-100 dark:bg-slate-800/50 border border-slate-200 dark:border-slate-700">
                <div className="flex items-center gap-4">
                    <div className={clsx(
                        "p-3 rounded-xl",
                        updateInfo?.update_available
                            ? "bg-amber-500/20 border border-amber-500/30"
                            : "bg-green-500/20 border border-green-500/30"
                    )}>
                        {updateInfo?.update_available ? (
                            <ArrowUpCircle className="w-6 h-6 text-amber-500" />
                        ) : (
                            <Check className="w-6 h-6 text-green-500" />
                        )}
                    </div>
                    <div>
                        <div className="flex items-center gap-2">
                            <span className="font-semibold text-slate-900 dark:text-white">
                                Current Version: {updateInfo?.current_version || 'Unknown'}
                            </span>
                            {updateInfo?.update_available && (
                                <span className="px-2 py-0.5 text-xs bg-amber-500/20 text-amber-600 dark:text-amber-400 rounded-full border border-amber-500/30">
                                    Update Available
                                </span>
                            )}
                        </div>
                        {updateInfo?.update_available && (
                            <p className="text-sm text-slate-600 dark:text-slate-400 mt-1">
                                Latest: {updateInfo.latest_version} (released {updateInfo.published_at})
                            </p>
                        )}
                        {!updateInfo?.update_available && (
                            <p className="text-sm text-green-600 dark:text-green-400 mt-1">
                                You're running the latest version
                            </p>
                        )}
                    </div>
                </div>
                {updateInfo?.release_url && (
                    <a
                        href={updateInfo.release_url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="flex items-center gap-2 px-4 py-2 bg-slate-200 dark:bg-slate-700 hover:bg-slate-300 dark:hover:bg-slate-600 text-slate-700 dark:text-slate-300 rounded-lg transition-colors"
                    >
                        <ExternalLink className="w-4 h-4" />
                        View on GitHub
                    </a>
                )}
            </div>

            {/* Detection Tools Status */}
            {systemInfo?.tools && (
                <div className="rounded-xl border border-slate-200 dark:border-slate-700 bg-white/80 dark:bg-slate-900/40 overflow-hidden">
                    <div className="px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border-b border-slate-200 dark:border-slate-700">
                        <h4 className="font-semibold text-slate-900 dark:text-white">Detection Tools</h4>
                    </div>
                    <div className="p-4">
                        <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                            {Object.values(systemInfo.tools).map((tool: ToolStatus) => (
                                <div
                                    key={tool.name}
                                    className={clsx(
                                        "flex items-center gap-3 p-3 rounded-lg border",
                                        tool.available
                                            ? "bg-green-50 dark:bg-green-900/20 border-green-200 dark:border-green-700"
                                            : tool.required
                                            ? "bg-red-50 dark:bg-red-900/20 border-red-200 dark:border-red-700"
                                            : "bg-slate-50 dark:bg-slate-800/50 border-slate-200 dark:border-slate-700"
                                    )}
                                >
                                    {tool.available ? (
                                        <CheckCircle className="w-5 h-5 text-green-500 flex-shrink-0" />
                                    ) : tool.required ? (
                                        <XCircle className="w-5 h-5 text-red-500 flex-shrink-0" />
                                    ) : (
                                        <Info className="w-5 h-5 text-slate-400 flex-shrink-0" />
                                    )}
                                    <div className="flex-1 min-w-0">
                                        <div className="flex items-center gap-2">
                                            <span className="font-mono text-sm font-medium text-slate-900 dark:text-white">
                                                {tool.name}
                                            </span>
                                            {tool.required && (
                                                <span className="text-xs px-1.5 py-0.5 bg-slate-200 dark:bg-slate-700 text-slate-600 dark:text-slate-400 rounded">
                                                    Required
                                                </span>
                                            )}
                                        </div>
                                        <div className="text-xs text-slate-500 dark:text-slate-400 truncate">
                                            {tool.available ? (
                                                <>v{tool.version || 'unknown'} • {tool.path}</>
                                            ) : (
                                                tool.description
                                            )}
                                        </div>
                                    </div>
                                </div>
                            ))}
                        </div>
                    </div>
                </div>
            )}

            {/* Changelog */}
            {updateInfo?.changelog && (
                <div className="rounded-xl border border-slate-200 dark:border-slate-700 bg-white/80 dark:bg-slate-900/40 overflow-hidden">
                    <div className="px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border-b border-slate-200 dark:border-slate-700">
                        <h4 className="font-semibold text-slate-900 dark:text-white">
                            {updateInfo.update_available ? 'What\'s New' : 'Current Release Notes'}
                        </h4>
                    </div>
                    <div className="p-4 max-h-64 overflow-y-auto prose prose-sm dark:prose-invert prose-slate">
                        {renderMarkdown(updateInfo.changelog)}
                    </div>
                </div>
            )}

            {/* Update Instructions - Always visible, collapsible */}
            <div className="rounded-xl border border-slate-200 dark:border-slate-700 bg-white/80 dark:bg-slate-900/40 overflow-hidden">
                <button
                    onClick={() => setUpdateInstructionsExpanded(!updateInstructionsExpanded)}
                    className="w-full px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border-b border-slate-200 dark:border-slate-700 flex items-center justify-between cursor-pointer hover:bg-slate-200 dark:hover:bg-slate-700/50 transition-colors"
                >
                    <h4 className="font-semibold text-slate-900 dark:text-white">How to Update</h4>
                    <ChevronDown className={clsx(
                        "w-5 h-5 text-slate-500 transition-transform duration-200",
                        updateInstructionsExpanded && "rotate-180"
                    )} />
                </button>
                <AnimatePresence initial={false}>
                    {updateInstructionsExpanded && (
                        <motion.div
                            initial={{ height: 0, opacity: 0 }}
                            animate={{ height: "auto", opacity: 1 }}
                            exit={{ height: 0, opacity: 0 }}
                            transition={{ duration: 0.2 }}
                            className="overflow-hidden"
                        >
                            <div className="p-4 space-y-4">
                                {/* Docker */}
                                <div className={clsx(
                                    "p-4 rounded-lg border",
                                    currentPlatform === 'docker'
                                        ? "bg-blue-50 dark:bg-blue-900/20 border-blue-200 dark:border-blue-700"
                                        : "bg-slate-50 dark:bg-slate-800/30 border-slate-200 dark:border-slate-700"
                                )}>
                                    <div className="flex items-center gap-2 mb-2">
                                        <DockerIcon className="w-5 h-5 text-[#2496ED]" />
                                        <h5 className="font-medium text-slate-900 dark:text-white">Docker</h5>
                                        {currentPlatform === 'docker' && (
                                            <span className="text-xs bg-blue-500/20 text-blue-600 dark:text-blue-400 px-2 py-0.5 rounded">Your Platform</span>
                                        )}
                                    </div>
                                    <pre className="text-sm text-slate-600 dark:text-slate-400 whitespace-pre-wrap font-mono bg-slate-100 dark:bg-slate-800 p-3 rounded-lg">{`# Navigate to your Healarr directory
cd /path/to/healarr

# Pull the latest image
docker compose pull healarr

# Restart with the new image
docker compose up -d

# Verify the update (check version in logs)
docker compose logs healarr | head -20`}</pre>
                                </div>

                                {/* Linux */}
                                <div className={clsx(
                                    "p-4 rounded-lg border",
                                    currentPlatform === 'linux'
                                        ? "bg-blue-50 dark:bg-blue-900/20 border-blue-200 dark:border-blue-700"
                                        : "bg-slate-50 dark:bg-slate-800/30 border-slate-200 dark:border-slate-700"
                                )}>
                                    <div className="flex items-center gap-2 mb-2">
                                        <TuxIcon className="w-5 h-5 text-slate-700 dark:text-slate-300" />
                                        <h5 className="font-medium text-slate-900 dark:text-white">Linux</h5>
                                        {currentPlatform === 'linux' && (
                                            <span className="text-xs bg-blue-500/20 text-blue-600 dark:text-blue-400 px-2 py-0.5 rounded">Your Platform</span>
                                        )}
                                    </div>
                                    <pre className="text-sm text-slate-600 dark:text-slate-400 whitespace-pre-wrap font-mono bg-slate-100 dark:bg-slate-800 p-3 rounded-lg">{`# Stop the running instance first, then:

# Download the latest release
curl -LO https://github.com/mescon/Healarr/releases/latest/download/healarr-linux-amd64.tar.gz
# or: wget https://github.com/mescon/Healarr/releases/latest/download/healarr-linux-amd64.tar.gz
tar -xzf healarr-linux-amd64.tar.gz
cd healarr && ./healarr

# Install ffprobe if needed:
# Debian/Ubuntu: sudo apt install ffmpeg
# Fedora/RHEL:   sudo dnf install ffmpeg
# Arch Linux:   sudo pacman -S ffmpeg`}</pre>
                                    {updateInfo?.download_urls?.linux_amd64 && (
                                        <a
                                            href={updateInfo.download_urls.linux_amd64}
                                            className="inline-flex items-center gap-2 mt-2 text-sm text-blue-500 hover:text-blue-400"
                                        >
                                            <Download className="w-4 h-4" />
                                            Download (amd64)
                                        </a>
                                    )}
                                    {updateInfo?.download_urls?.linux_arm64 && (
                                        <a
                                            href={updateInfo.download_urls.linux_arm64}
                                            className="inline-flex items-center gap-2 mt-2 ml-4 text-sm text-blue-500 hover:text-blue-400"
                                        >
                                            <Download className="w-4 h-4" />
                                            Download (arm64)
                                        </a>
                                    )}
                                </div>

                                {/* macOS */}
                                <div className={clsx(
                                    "p-4 rounded-lg border",
                                    currentPlatform === 'darwin'
                                        ? "bg-blue-50 dark:bg-blue-900/20 border-blue-200 dark:border-blue-700"
                                        : "bg-slate-50 dark:bg-slate-800/30 border-slate-200 dark:border-slate-700"
                                )}>
                                    <div className="flex items-center gap-2 mb-2">
                                        <AppleIcon className="w-5 h-5 text-slate-700 dark:text-slate-300" />
                                        <h5 className="font-medium text-slate-900 dark:text-white">macOS</h5>
                                        {currentPlatform === 'darwin' && (
                                            <span className="text-xs bg-blue-500/20 text-blue-600 dark:text-blue-400 px-2 py-0.5 rounded">Your Platform</span>
                                        )}
                                    </div>
                                    <pre className="text-sm text-slate-600 dark:text-slate-400 whitespace-pre-wrap font-mono bg-slate-100 dark:bg-slate-800 p-3 rounded-lg">{`# Stop the running instance first, then:

# Download the latest release (Apple Silicon)
curl -LO https://github.com/mescon/Healarr/releases/latest/download/healarr-darwin-arm64.tar.gz
tar -xzf healarr-darwin-arm64.tar.gz
cd healarr && ./healarr

# Install ffprobe if needed:
brew install ffmpeg`}</pre>
                                    {updateInfo?.download_urls?.macos_amd64 && (
                                        <a
                                            href={updateInfo.download_urls.macos_amd64}
                                            className="inline-flex items-center gap-2 mt-2 text-sm text-blue-500 hover:text-blue-400"
                                        >
                                            <Download className="w-4 h-4" />
                                            Download (Intel)
                                        </a>
                                    )}
                                    {updateInfo?.download_urls?.macos_arm64 && (
                                        <a
                                            href={updateInfo.download_urls.macos_arm64}
                                            className="inline-flex items-center gap-2 mt-2 ml-4 text-sm text-blue-500 hover:text-blue-400"
                                        >
                                            <Download className="w-4 h-4" />
                                            Download (Apple Silicon)
                                        </a>
                                    )}
                                </div>

                                {/* Windows */}
                                <div className={clsx(
                                    "p-4 rounded-lg border",
                                    currentPlatform === 'windows'
                                        ? "bg-blue-50 dark:bg-blue-900/20 border-blue-200 dark:border-blue-700"
                                        : "bg-slate-50 dark:bg-slate-800/30 border-slate-200 dark:border-slate-700"
                                )}>
                                    <div className="flex items-center gap-2 mb-2">
                                        <WindowsIcon className="w-5 h-5 text-[#0078D4]" />
                                        <h5 className="font-medium text-slate-900 dark:text-white">Windows</h5>
                                        {currentPlatform === 'windows' && (
                                            <span className="text-xs bg-blue-500/20 text-blue-600 dark:text-blue-400 px-2 py-0.5 rounded">Your Platform</span>
                                        )}
                                    </div>
                                    <pre className="text-sm text-slate-600 dark:text-slate-400 whitespace-pre-wrap font-mono bg-slate-100 dark:bg-slate-800 p-3 rounded-lg">{`# Stop the running instance first, then:

# Download the latest release from GitHub
# Extract healarr-windows-amd64.zip
# Run healarr.exe

# Install ffprobe if needed:
# Download from https://ffmpeg.org/download.html
# Add ffprobe.exe to your PATH`}</pre>
                                    {updateInfo?.download_urls?.windows_amd64 && (
                                        <a
                                            href={updateInfo.download_urls.windows_amd64}
                                            className="inline-flex items-center gap-2 mt-2 text-sm text-blue-500 hover:text-blue-400"
                                        >
                                            <Download className="w-4 h-4" />
                                            Download (.exe)
                                        </a>
                                    )}
                                </div>
                            </div>
                        </motion.div>
                    )}
                </AnimatePresence>
            </div>

            {/* System Information */}
            {systemInfo && (
                <div className="rounded-xl border border-slate-200 dark:border-slate-700 bg-white/80 dark:bg-slate-900/40 overflow-hidden">
                    <div className="px-4 py-3 bg-slate-100 dark:bg-slate-800/50 border-b border-slate-200 dark:border-slate-700">
                        <h4 className="font-semibold text-slate-900 dark:text-white">System Information</h4>
                    </div>
                    <div className="p-4 space-y-4">
                        {/* Runtime Environment */}
                        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-3">
                            <div className="flex items-center gap-3 p-3 rounded-lg bg-slate-50 dark:bg-slate-800/50">
                                <div className="p-2 rounded-lg bg-slate-200 dark:bg-slate-700">
                                    {systemInfo.environment === 'docker' ? (
                                        <DockerIcon className="w-4 h-4 text-[#2496ED]" />
                                    ) : (
                                        <Server className="w-4 h-4 text-slate-600 dark:text-slate-400" />
                                    )}
                                </div>
                                <div>
                                    <div className="text-xs text-slate-500 dark:text-slate-400">Environment</div>
                                    <div className="text-sm font-medium text-slate-900 dark:text-white capitalize">{systemInfo.environment}</div>
                                </div>
                            </div>
                            <div className="flex items-center gap-3 p-3 rounded-lg bg-slate-50 dark:bg-slate-800/50">
                                <div className="p-2 rounded-lg bg-slate-200 dark:bg-slate-700">
                                    <Monitor className="w-4 h-4 text-slate-600 dark:text-slate-400" />
                                </div>
                                <div>
                                    <div className="text-xs text-slate-500 dark:text-slate-400">Platform</div>
                                    <div className="text-sm font-medium text-slate-900 dark:text-white">{systemInfo.os}/{systemInfo.arch}</div>
                                </div>
                            </div>
                            <div className="flex items-center gap-3 p-3 rounded-lg bg-slate-50 dark:bg-slate-800/50">
                                <div className="p-2 rounded-lg bg-slate-200 dark:bg-slate-700">
                                    <Clock className="w-4 h-4 text-slate-600 dark:text-slate-400" />
                                </div>
                                <div>
                                    <div className="text-xs text-slate-500 dark:text-slate-400">Uptime</div>
                                    <div className="text-sm font-medium text-slate-900 dark:text-white">{systemInfo.uptime}</div>
                                </div>
                            </div>
                        </div>

                        {/* Configuration Details */}
                        <div className="space-y-2">
                            <h5 className="text-sm font-medium text-slate-700 dark:text-slate-300">Configuration</h5>
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-2 text-sm">
                                <div className="flex justify-between p-2 rounded bg-slate-50 dark:bg-slate-800/30">
                                    <span className="text-slate-500 dark:text-slate-400">Data Directory</span>
                                    <code className="text-slate-700 dark:text-slate-300 font-mono">{systemInfo.config.data_dir}</code>
                                </div>
                                <div className="flex justify-between p-2 rounded bg-slate-50 dark:bg-slate-800/30">
                                    <span className="text-slate-500 dark:text-slate-400">Database</span>
                                    <code className="text-slate-700 dark:text-slate-300 font-mono">{systemInfo.config.database_path}</code>
                                </div>
                                <div className="flex justify-between p-2 rounded bg-slate-50 dark:bg-slate-800/30">
                                    <span className="text-slate-500 dark:text-slate-400">Log Directory</span>
                                    <code className="text-slate-700 dark:text-slate-300 font-mono">{systemInfo.config.log_dir}</code>
                                </div>
                                <div className="flex justify-between p-2 rounded bg-slate-50 dark:bg-slate-800/30">
                                    <span className="text-slate-500 dark:text-slate-400">Log Level</span>
                                    <span className="text-slate-700 dark:text-slate-300">{systemInfo.config.log_level}</span>
                                </div>
                                <div className="flex justify-between p-2 rounded bg-slate-50 dark:bg-slate-800/30">
                                    <span className="text-slate-500 dark:text-slate-400">Retention</span>
                                    <span className="text-slate-700 dark:text-slate-300">{systemInfo.config.retention_days} days</span>
                                </div>
                                <div className="flex justify-between p-2 rounded bg-slate-50 dark:bg-slate-800/30">
                                    <span className="text-slate-500 dark:text-slate-400">Go Version</span>
                                    <span className="text-slate-700 dark:text-slate-300">{systemInfo.go_version}</span>
                                </div>
                                {systemInfo.config.dry_run_mode && (
                                    <div className="flex justify-between p-2 rounded bg-amber-50 dark:bg-amber-900/20 border border-amber-200 dark:border-amber-700 md:col-span-2">
                                        <span className="text-amber-600 dark:text-amber-400">Dry Run Mode</span>
                                        <span className="text-amber-700 dark:text-amber-300 font-medium">Enabled</span>
                                    </div>
                                )}
                            </div>
                        </div>

                        {/* Mounted Volumes (Docker only) */}
                        {systemInfo.mounts && systemInfo.mounts.length > 0 && (
                            <div className="space-y-2">
                                <h5 className="text-sm font-medium text-slate-700 dark:text-slate-300">Mounted Volumes</h5>
                                <div className="space-y-1">
                                    {systemInfo.mounts.map((mount, idx) => (
                                        <div key={idx} className="flex items-center gap-2 p-2 rounded bg-slate-50 dark:bg-slate-800/30 text-sm font-mono">
                                            <HardDrive className="w-4 h-4 text-slate-400 flex-shrink-0" />
                                            <span className="text-slate-700 dark:text-slate-300 truncate">{mount.destination}</span>
                                            {mount.read_only && (
                                                <span className="text-xs px-1.5 py-0.5 bg-slate-200 dark:bg-slate-700 text-slate-500 dark:text-slate-400 rounded">RO</span>
                                            )}
                                            <span className="text-slate-400 dark:text-slate-500 text-xs ml-auto">{mount.type}</span>
                                        </div>
                                    ))}
                                </div>
                            </div>
                        )}

                        {/* Links */}
                        <div className="space-y-2">
                            <h5 className="text-sm font-medium text-slate-700 dark:text-slate-300">Links</h5>
                            <div className="flex flex-wrap gap-2">
                                <a
                                    href={systemInfo.links.github}
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 text-sm text-slate-700 dark:text-slate-300 transition-colors"
                                >
                                    <Github className="w-4 h-4" />
                                    GitHub
                                </a>
                                <a
                                    href={systemInfo.links.issues}
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 text-sm text-slate-700 dark:text-slate-300 transition-colors"
                                >
                                    <Bug className="w-4 h-4" />
                                    Issues
                                </a>
                                <a
                                    href={systemInfo.links.releases}
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-slate-100 dark:bg-slate-800 hover:bg-slate-200 dark:hover:bg-slate-700 text-sm text-slate-700 dark:text-slate-300 transition-colors"
                                >
                                    <Download className="w-4 h-4" />
                                    Releases
                                </a>
                            </div>
                        </div>
                    </div>
                </div>
            )}
        </div>
    );

    // If shown as accordion, wrap in accordion style
    if (showAsAccordion) {
        return content;
    }

    return content;
};

export default AboutSection;
