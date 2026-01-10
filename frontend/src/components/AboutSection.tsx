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

const TuxIcon = ({ className }: { className?: string }) => (
    <svg viewBox="0 0 24 24" className={className} fill="currentColor">
        <path d="M12.504 0c-.155 0-.315.008-.48.021-4.226.333-3.105 4.807-3.17 6.298-.076 1.092-.3 1.953-1.05 3.02-.885 1.051-2.127 2.75-2.716 4.521-.278.832-.41 1.684-.287 2.489a.424.424 0 00-.11.135c-.26.268-.45.6-.663.839-.199.199-.485.267-.797.4-.313.136-.658.269-.864.68-.09.189-.136.394-.132.602 0 .199.027.4.055.536.058.399.116.728.04.97-.249.68-.28 1.145-.106 1.484.174.334.535.47.94.601.81.2 1.91.135 2.774.6.926.466 1.866.67 2.616.47.526-.116.97-.464 1.208-.946.587-.003 1.23-.269 2.26-.334.699-.058 1.574.267 2.577.2.025.134.063.198.114.333l.003.003c.391.778 1.113 1.132 1.884 1.071.771-.06 1.592-.536 2.257-1.306.631-.765 1.683-1.084 2.378-1.503.348-.199.629-.469.649-.853.023-.4-.2-.811-.714-1.376v-.097l-.003-.003c-.17-.2-.25-.535-.338-.926-.085-.401-.182-.786-.492-1.046h-.003c-.059-.054-.123-.067-.188-.135a.357.357 0 00-.19-.064c.431-1.278.264-2.55-.173-3.694-.533-1.41-1.465-2.638-2.175-3.483-.796-1.005-1.576-1.957-1.56-3.368.026-2.152.236-6.133-3.544-6.139zm.529 3.405h.013c.213 0 .396.062.584.198.19.135.33.332.438.533.105.259.158.459.166.724 0-.02.006-.04.006-.06v.105a.086.086 0 01-.004-.021l-.004-.024a1.807 1.807 0 01-.15.706.953.953 0 01-.213.335.71.71 0 00-.088-.042c-.104-.045-.198-.064-.284-.133a1.312 1.312 0 00-.22-.066c.05-.06.146-.133.183-.198.053-.128.082-.264.088-.402v-.02a1.21 1.21 0 00-.061-.4c-.045-.134-.101-.2-.183-.333-.084-.066-.167-.132-.267-.132h-.016c-.093 0-.176.03-.262.132a.8.8 0 00-.205.334 1.18 1.18 0 00-.09.4v.019c.002.089.008.179.02.267-.193-.067-.438-.135-.607-.202a1.635 1.635 0 01-.018-.2v-.02a1.772 1.772 0 01.15-.768c.082-.22.232-.406.43-.533a.985.985 0 01.594-.2zm-2.962.059h.036c.142 0 .27.048.399.135.146.129.264.288.344.465.09.199.14.4.153.667v.004c.007.134.006.2-.002.266v.08c-.03.007-.056.018-.083.024-.152.055-.274.135-.393.2.012-.09.013-.18.003-.267v-.015c-.012-.133-.04-.2-.082-.333a.613.613 0 00-.166-.267.248.248 0 00-.183-.064h-.021c-.071.006-.13.04-.186.132a.552.552 0 00-.12.27.944.944 0 00-.023.33v.015c.012.135.037.2.08.334.046.134.098.2.166.268.01.009.02.018.034.024-.07.057-.117.07-.176.136a.304.304 0 01-.131.068 2.62 2.62 0 01-.275-.402 1.772 1.772 0 01-.155-.667 1.759 1.759 0 01.08-.668 1.43 1.43 0 01.283-.535c.128-.133.26-.2.418-.2zm1.37 1.706c.332 0 .733.065 1.216.399.293.2.523.269 1.052.468h.003c.255.136.405.266.478.399v-.131a.571.571 0 01.016.47c-.123.31-.516.643-1.063.842v.002c-.268.135-.501.333-.775.465-.276.135-.588.292-1.012.267a1.139 1.139 0 01-.448-.067 3.566 3.566 0 01-.322-.198c-.195-.135-.363-.332-.612-.465v-.005h-.005c-.4-.246-.616-.512-.686-.71-.07-.268-.005-.47.193-.6.224-.135.38-.271.483-.336.104-.074.143-.102.176-.131h.002v-.003c.169-.202.436-.47.839-.601.139-.036.294-.065.466-.065zm2.8 2.142c.358 1.417 1.196 3.475 1.735 4.473.286.534.855 1.659 1.102 3.024.156-.005.33.018.513.064.646-1.671-.546-3.467-1.089-3.966-.22-.2-.232-.335-.123-.335.59.534 1.365 1.572 1.646 2.757.13.535.16 1.104.021 1.67.067.028.135.06.205.067 1.032.534 1.413.938 1.23 1.537v-.043c-.06-.003-.12 0-.18 0h-.016c.151-.467-.182-.825-1.065-1.224-.915-.4-1.646-.336-1.77.465-.008.043-.013.066-.018.135-.068.023-.139.053-.209.064-.43.268-.662.669-.793 1.187-.13.533-.17 1.156-.205 1.869v.003c-.02.334-.17.838-.319 1.35-1.5 1.072-3.58 1.538-5.348.334a2.645 2.645 0 00-.402-.533 1.45 1.45 0 00-.275-.333c.182 0 .338-.03.465-.067a.615.615 0 00.314-.334c.108-.267 0-.697-.345-1.163-.345-.467-.931-.995-1.788-1.521-.63-.4-.986-.87-1.15-1.396-.165-.534-.143-1.085-.015-1.645.245-1.07.873-2.11 1.274-2.763.107-.065.037.135-.408.974-.396.751-1.14 2.497-.122 3.854a8.123 8.123 0 01.647-2.876c.564-1.278 1.743-3.504 1.836-5.268.048.036.217.135.289.202.218.133.38.333.59.465.21.201.477.335.876.335.039.003.075.006.11.006.412 0 .73-.134.997-.268.29-.134.52-.334.74-.4h.005c.467-.135.835-.402 1.044-.7zm2.185 8.958c.037.6.343 1.245.882 1.377.588.134 1.434-.333 1.791-.765l.211-.01c.315-.007.577.01.847.268l.003.003c.208.199.305.53.391.876.085.4.154.78.409 1.066.486.527.645.906.636 1.14l.003-.007v.018l-.003-.012c-.015.262-.185.396-.498.595-.63.401-1.746.712-2.457 1.57-.618.737-1.37 1.14-2.036 1.191-.664.053-1.237-.2-1.574-.898l-.005-.003c-.21-.4-.12-1.025.056-1.69.176-.668.428-1.344.463-1.897.037-.714.076-1.335.195-1.814.12-.465.308-.797.641-.984l.045-.022zm-10.814.049h.01c.053 0 .105.005.157.014.376.055.706.333 1.023.752l.91 1.664.003.003c.243.533.754 1.064 1.189 1.637.434.598.77 1.131.729 1.57v.006c-.057.744-.48 1.148-1.125 1.294-.645.135-1.52.002-2.395-.464-.968-.536-2.118-.469-2.857-.602-.369-.066-.61-.2-.723-.4-.11-.2-.113-.602.123-1.23v-.004l.002-.003c.117-.334.03-.752-.027-1.118-.055-.401-.083-.71.043-.94.16-.334.396-.4.69-.533.294-.135.64-.202.915-.47h.002v-.002c.256-.268.445-.601.668-.838.19-.201.38-.336.663-.336zm7.159-9.074c-.435.201-.945.535-1.488.535-.542 0-.97-.267-1.28-.466-.154-.134-.28-.268-.373-.335-.164-.134-.144-.333-.074-.333.109.016.129.134.199.2.096.066.215.2.36.333.292.2.68.467 1.167.467.485 0 1.053-.267 1.398-.466.195-.135.445-.334.648-.467.156-.136.149-.267.279-.267.128.016.034.134-.147.332a8.097 8.097 0 01-.69.468zm-1.082-1.583V5.64c-.006-.02.013-.042.029-.05.074-.043.18-.027.26.004.063 0 .16.067.15.135-.006.049-.085.066-.135.066-.055 0-.092-.043-.141-.068-.052-.018-.146-.008-.163-.065zm-.551 0c-.02.058-.113.049-.166.066-.047.025-.086.068-.14.068-.05 0-.13-.02-.136-.068-.01-.066.088-.133.15-.133.08-.031.184-.047.259-.005.019.009.036.03.03.05v.02h.003z"/>
    </svg>
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
