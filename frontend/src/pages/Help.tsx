import { useState } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import { HelpCircle, Play, Webhook, Clock, Container, Activity, Globe, ChevronDown, Heart, Zap, Bell } from 'lucide-react';
import clsx from 'clsx';

// Accordion component for collapsible sections
const Accordion = ({ 
    title, 
    icon, 
    iconBgClass, 
    children, 
    defaultOpen = false 
}: { 
    title: string; 
    icon: React.ReactNode; 
    iconBgClass: string; 
    children: React.ReactNode; 
    defaultOpen?: boolean;
}) => {
    const [isOpen, setIsOpen] = useState(defaultOpen);
    
    return (
        <div className="rounded-2xl border border-slate-200 dark:border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl overflow-hidden">
            <button
                onClick={() => setIsOpen(!isOpen)}
                className="w-full p-6 flex items-center justify-between gap-3 hover:bg-slate-100 dark:hover:bg-slate-800/20 transition-colors cursor-pointer"
            >
                <div className="flex items-center gap-3">
                    <div className={clsx("p-2 rounded-lg border", iconBgClass)}>
                        {icon}
                    </div>
                    <h2 className="text-2xl font-bold text-slate-900 dark:text-white">{title}</h2>
                </div>
                <ChevronDown className={clsx(
                    "w-5 h-5 text-slate-600 dark:text-slate-400 transition-transform duration-200",
                    isOpen && "rotate-180"
                )} />
            </button>
            <AnimatePresence>
                {isOpen && (
                    <motion.div
                        initial={{ height: 0, opacity: 0 }}
                        animate={{ height: "auto", opacity: 1 }}
                        exit={{ height: 0, opacity: 0 }}
                        transition={{ duration: 0.2 }}
                    >
                        <div className="px-6 pb-6 space-y-6">
                            {children}
                        </div>
                    </motion.div>
                )}
            </AnimatePresence>
        </div>
    );
};

const Help = () => {
    return (
        <div className="space-y-8 max-w-4xl">
            <div>
                <motion.h1
                    initial={{ opacity: 0, x: -20 }}
                    animate={{ opacity: 1, x: 0 }}
                    className="text-3xl font-bold text-slate-900 dark:text-white mb-2"
                >
                    Help & Documentation
                </motion.h1>
                <motion.p
                    initial={{ opacity: 0, x: -20 }}
                    animate={{ opacity: 1, x: 0 }}
                    transition={{ delay: 0.1 }}
                    className="text-slate-600 dark:text-slate-400"
                >
                    Learn how to use Healarr for media corruption detection and remediation.
                </motion.p>
            </div>

            {/* Scanning Guide */}
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.2 }}
                className="rounded-2xl border border-slate-200 dark:border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-6 space-y-6"
            >
                <div className="flex items-center gap-3">
                    <div className="p-2 rounded-lg bg-blue-500/10 border border-blue-500/20">
                        <HelpCircle className="w-5 h-5 text-blue-400" />
                    </div>
                    <h2 className="text-2xl font-bold text-slate-900 dark:text-white">How Scans Work</h2>
                </div>

                <p className="text-slate-700 dark:text-slate-300">
                    Healarr supports three methods to trigger scans. Use a combination for best coverage.
                </p>

                {/* Method 1: Webhooks */}
                <div className="border-l-2 border-green-500/50 pl-4 space-y-2">
                    <div className="flex items-center gap-2">
                        <Webhook className="w-5 h-5 text-green-400" />
                        <h3 className="text-xl font-semibold text-slate-900 dark:text-white">1. Webhook-Based (Recommended)</h3>
                        <span className="text-xs bg-green-500/10 text-green-400 px-2 py-1 rounded-full border border-green-500/20">Automatic</span>
                    </div>
                    <p className="text-slate-700 dark:text-slate-300">
                        When Sonarr/Radarr/Whisparr downloads or upgrades a file, it automatically sends a webhook to Healarr, which immediately scans that specific file.
                    </p>
                    <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-2">
                        <p className="text-sm font-semibold text-slate-700 dark:text-slate-300">Setup in Sonarr/Radarr/Whisparr:</p>
                        <ol className="text-sm text-slate-600 dark:text-slate-400 space-y-1 list-decimal list-inside">
                            <li>Go to the <span className="font-semibold text-slate-700 dark:text-slate-300">Config</span> page in Healarr</li>
                            <li>Find your server in the list and copy the <span className="font-semibold text-slate-700 dark:text-slate-300">Webhook URL</span> (it includes your API key)</li>
                            <li>In Sonarr/Radarr/Whisparr, go to <span className="font-mono text-blue-400">Settings → Connect</span></li>
                            <li>Add a new <span className="font-semibold text-slate-700 dark:text-slate-300">Webhook</span> connection</li>
                            <li>Paste the URL you copied</li>
                            <li>Enable events: <span className="font-semibold text-slate-700 dark:text-slate-300">On Download</span> and <span className="font-semibold text-slate-700 dark:text-slate-300">On Upgrade</span></li>
                        </ol>
                        <div className="mt-3 pt-3 border-t border-slate-300 dark:border-slate-700/50">
                            <p className="text-xs text-amber-400 font-semibold">⚠ Important:</p>
                            <p className="text-xs text-slate-600 dark:text-slate-400 mt-1">
                                If you disable an *arr server in the Config page, its webhooks will stop working immediately.
                                Healarr will return HTTP 503 (Service Unavailable) until you re-enable the server.
                            </p>
                        </div>
                    </div>
                </div>

                {/* Method 2: Manual */}
                <div className="border-l-2 border-blue-500/50 pl-4 space-y-2">
                    <div className="flex items-center gap-2">
                        <Play className="w-5 h-5 text-blue-400" />
                        <h3 className="text-xl font-semibold text-slate-900 dark:text-white">2. Manual Scans (UI)</h3>
                        <span className="text-xs bg-blue-500/10 text-blue-400 px-2 py-1 rounded-full border border-blue-500/20">On-Demand</span>
                    </div>
                    <p className="text-slate-700 dark:text-slate-300">
                        Trigger a full scan of any configured path from the Config page.
                    </p>
                    <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-2">
                        <p className="text-sm font-semibold text-slate-700 dark:text-slate-300">How to scan:</p>
                        <ol className="text-sm text-slate-600 dark:text-slate-400 space-y-1 list-decimal list-inside">
                            <li>Go to the <span className="font-semibold text-slate-700 dark:text-slate-300">Config</span> page</li>
                            <li>Find the scan path you want to process</li>
                            <li>Click the green <Play className="w-3 h-3 inline text-green-400" /> <span className="font-semibold text-green-400">Play</span> button</li>
                            <li>Monitor progress on the Dashboard or Logs page</li>
                        </ol>
                    </div>
                </div>

                {/* Method 3: Scheduled */}
                <div className="border-l-2 border-purple-500/50 pl-4 space-y-2">
                    <div className="flex items-center gap-2">
                        <Clock className="w-5 h-5 text-purple-400" />
                        <h3 className="text-xl font-semibold text-slate-900 dark:text-white">3. Scheduled Scans</h3>
                        <span className="text-xs bg-purple-500/10 text-purple-400 px-2 py-1 rounded-full border border-purple-500/20">Automated</span>
                    </div>
                    <p className="text-slate-700 dark:text-slate-300">
                        Set up periodic full scans using the built-in scheduler to catch files missed by webhooks.
                    </p>
                    <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-2">
                        <p className="text-sm font-semibold text-slate-700 dark:text-slate-300">How to schedule:</p>
                        <ol className="text-sm text-slate-600 dark:text-slate-400 space-y-1 list-decimal list-inside">
                            <li>Go to the <span className="font-semibold text-slate-700 dark:text-slate-300">Config</span> page</li>
                            <li>Scroll down to the <span className="font-semibold text-purple-400">Scheduled Scans</span> section</li>
                            <li>Click <span className="font-semibold text-slate-700 dark:text-slate-300">Add Schedule</span></li>
                            <li>Select the scan path and choose a frequency (hourly, daily, weekly, or custom cron)</li>
                            <li>Healarr will automatically run scans at the configured times</li>
                        </ol>
                        <p className="text-xs text-slate-500 mt-2">
                            Schedules can be enabled/disabled at any time without deleting them.
                        </p>
                    </div>
                </div>

                {/* Best Practices */}
                <div className="bg-blue-500/5 border border-blue-500/20 rounded-lg p-4 space-y-2">
                    <p className="text-sm font-semibold text-blue-300">💡 Recommended Strategy</p>
                    <ul className="text-sm text-slate-700 dark:text-slate-300 space-y-1 list-disc list-inside">
                        <li><span className="font-semibold">Enable webhooks</span> for real-time scanning of new downloads</li>
                        <li><span className="font-semibold">Schedule weekly full scans</span> to catch missed files or new corruption</li>
                        <li><span className="font-semibold">Monitor the Dashboard</span> for corruption trends and patterns</li>
                    </ul>
                </div>
            </motion.div>

            {/* Corruption Statuses */}
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.25 }}
                className="rounded-2xl border border-slate-200 dark:border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-6 space-y-6"
            >
                <div className="flex items-center gap-3">
                    <div className="p-2 rounded-lg bg-purple-500/10 border border-purple-500/20">
                        <Activity className="w-5 h-5 text-purple-400" />
                    </div>
                    <h2 className="text-2xl font-bold text-slate-900 dark:text-white">Understanding Corruption Statuses</h2>
                </div>

                <p className="text-slate-700 dark:text-slate-300">
                    Each detected corruption progresses through various states during the remediation process. Here's what each status means:
                </p>

                <div className="overflow-x-auto">
                    <table className="w-full text-sm">
                        <thead>
                            <tr className="border-b border-slate-300 dark:border-slate-700">
                                <th className="text-left py-2 px-3 text-slate-700 dark:text-slate-300 font-semibold">Status</th>
                                <th className="text-left py-2 px-3 text-slate-700 dark:text-slate-300 font-semibold">Color</th>
                                <th className="text-left py-2 px-3 text-slate-700 dark:text-slate-300 font-semibold">Description</th>
                            </tr>
                        </thead>
                        <tbody className="divide-y divide-slate-800">
                            <tr>
                                <td className="py-2 px-3 font-semibold text-amber-400">Pending</td>
                                <td className="py-2 px-3"><span className="inline-block w-3 h-3 rounded-full bg-amber-400"></span></td>
                                <td className="py-2 px-3 text-slate-700 dark:text-slate-300">Corruption detected, waiting to start remediation</td>
                            </tr>
                            <tr>
                                <td className="py-2 px-3 font-semibold text-blue-400">In Progress</td>
                                <td className="py-2 px-3"><span className="inline-block w-3 h-3 rounded-full bg-blue-400"></span></td>
                                <td className="py-2 px-3 text-slate-700 dark:text-slate-300">Actively being remediated (deleting, searching, downloading, or verifying)</td>
                            </tr>
                            <tr>
                                <td className="py-2 px-3 font-semibold text-green-400">Resolved</td>
                                <td className="py-2 px-3"><span className="inline-block w-3 h-3 rounded-full bg-green-400"></span></td>
                                <td className="py-2 px-3 text-slate-700 dark:text-slate-300">Successfully fixed - the replacement file passed health checks</td>
                            </tr>
                            <tr>
                                <td className="py-2 px-3 font-semibold text-orange-400">Failed (Retrying)</td>
                                <td className="py-2 px-3"><span className="inline-block w-3 h-3 rounded-full bg-orange-400"></span></td>
                                <td className="py-2 px-3 text-slate-700 dark:text-slate-300">A step failed but retries remain - will try again automatically</td>
                            </tr>
                            <tr>
                                <td className="py-2 px-3 font-semibold text-red-400">Max Retries</td>
                                <td className="py-2 px-3"><span className="inline-block w-3 h-3 rounded-full bg-red-400"></span></td>
                                <td className="py-2 px-3 text-slate-700 dark:text-slate-300">All retry attempts exhausted - requires manual intervention</td>
                            </tr>
                            <tr>
                                <td className="py-2 px-3 font-semibold text-slate-600 dark:text-slate-400">Ignored</td>
                                <td className="py-2 px-3"><span className="inline-block w-3 h-3 rounded-full bg-slate-400"></span></td>
                                <td className="py-2 px-3 text-slate-700 dark:text-slate-300">Manually marked as ignored - no further action will be taken</td>
                            </tr>
                        </tbody>
                    </table>
                </div>

                <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-3">
                    <p className="text-sm font-semibold text-slate-700 dark:text-slate-300">Remediation Workflow:</p>
                    <div className="flex items-center gap-2 flex-wrap text-xs">
                        <span className="px-2 py-1 rounded bg-amber-500/20 text-amber-400 border border-amber-500/30">Pending</span>
                        <span className="text-slate-500">→</span>
                        <span className="px-2 py-1 rounded bg-blue-500/20 text-blue-400 border border-blue-500/30">Queued</span>
                        <span className="text-slate-500">→</span>
                        <span className="px-2 py-1 rounded bg-blue-500/20 text-blue-400 border border-blue-500/30">Deleting</span>
                        <span className="text-slate-500">→</span>
                        <span className="px-2 py-1 rounded bg-purple-500/20 text-purple-400 border border-purple-500/30">Searching</span>
                        <span className="text-slate-500">→</span>
                        <span className="px-2 py-1 rounded bg-indigo-500/20 text-indigo-400 border border-indigo-500/30">Downloading</span>
                        <span className="text-slate-500">→</span>
                        <span className="px-2 py-1 rounded bg-cyan-500/20 text-cyan-400 border border-cyan-500/30">Verifying</span>
                        <span className="text-slate-500">→</span>
                        <span className="px-2 py-1 rounded bg-green-500/20 text-green-400 border border-green-500/30">Resolved</span>
                    </div>
                    <p className="text-xs text-slate-500 mt-2">
                        If any step fails, Healarr will automatically retry up to the configured limit (default: 3 attempts).
                    </p>
                </div>

                <div className="bg-blue-500/5 border border-blue-500/20 rounded-lg p-4 space-y-2">
                    <p className="text-sm font-semibold text-blue-300">💡 Tips</p>
                    <ul className="text-sm text-slate-700 dark:text-slate-300 space-y-1 list-disc list-inside">
                        <li>Click any row in the <span className="font-semibold">Corruptions</span> table to see the full remediation journey</li>
                        <li>Use the <span className="font-semibold">Retry</span> button to restart remediation for failed items</li>
                        <li>Use the <span className="font-semibold">Ignore</span> button for files you don't want to fix (e.g., intentionally corrupt test files)</li>
                        <li>The Dashboard shows a breakdown of all corruption statuses with clickable links</li>
                    </ul>
                </div>
            </motion.div>

            {/* Docker Configuration - Accordion */}
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.3 }}
            >
                <Accordion
                    title="Docker Configuration"
                    icon={<Container className="w-5 h-5 text-cyan-400" />}
                    iconBgClass="bg-cyan-500/10 border-cyan-500/20"
                >
                    <p className="text-slate-700 dark:text-slate-300">
                        Healarr can be configured using environment variables, making it easy to deploy in Docker or other containerized environments.
                    </p>

                    <div className="overflow-x-auto">
                        <table className="w-full text-sm">
                            <thead>
                                <tr className="border-b border-slate-300 dark:border-slate-700">
                                    <th className="text-left py-2 px-3 text-slate-700 dark:text-slate-300 font-semibold">Variable</th>
                                    <th className="text-left py-2 px-3 text-slate-700 dark:text-slate-300 font-semibold">Default</th>
                                    <th className="text-left py-2 px-3 text-slate-700 dark:text-slate-300 font-semibold">Description</th>
                                </tr>
                            </thead>
                            <tbody className="divide-y divide-slate-800">
                                <tr>
                                    <td className="py-2 px-3 font-mono text-cyan-400">HEALARR_PORT</td>
                                    <td className="py-2 px-3 text-slate-600 dark:text-slate-400">3090</td>
                                    <td className="py-2 px-3 text-slate-700 dark:text-slate-300">HTTP server listen port</td>
                                </tr>
                                <tr>
                                    <td className="py-2 px-3 font-mono text-cyan-400">HEALARR_BASE_PATH</td>
                                    <td className="py-2 px-3 text-slate-600 dark:text-slate-400">/</td>
                                    <td className="py-2 px-3 text-slate-700 dark:text-slate-300">URL base path for reverse proxy setups (e.g., <code className="bg-slate-800 px-1 rounded">/healarr</code> for domain.com/healarr/)</td>
                                </tr>
                                <tr>
                                    <td className="py-2 px-3 font-mono text-cyan-400">HEALARR_LOG_LEVEL</td>
                                    <td className="py-2 px-3 text-slate-600 dark:text-slate-400">info</td>
                                    <td className="py-2 px-3 text-slate-700 dark:text-slate-300">Logging verbosity: <code className="bg-slate-800 px-1 rounded">debug</code>, <code className="bg-slate-800 px-1 rounded">info</code>, <code className="bg-slate-800 px-1 rounded">error</code></td>
                                </tr>
                                <tr>
                                    <td className="py-2 px-3 font-mono text-cyan-400">HEALARR_DATA_DIR</td>
                                    <td className="py-2 px-3 text-slate-600 dark:text-slate-400">./config</td>
                                    <td className="py-2 px-3 text-slate-700 dark:text-slate-300">Base directory for all persistent data (database, logs)</td>
                                </tr>
                                <tr>
                                    <td className="py-2 px-3 font-mono text-cyan-400">HEALARR_DATABASE_PATH</td>
                                    <td className="py-2 px-3 text-slate-600 dark:text-slate-400">{`{DATA_DIR}/healarr.db`}</td>
                                    <td className="py-2 px-3 text-slate-700 dark:text-slate-300">SQLite database file path (overrides DATA_DIR)</td>
                                </tr>
                                <tr>
                                    <td className="py-2 px-3 font-mono text-cyan-400">HEALARR_VERIFICATION_TIMEOUT</td>
                                    <td className="py-2 px-3 text-slate-600 dark:text-slate-400">72h</td>
                                    <td className="py-2 px-3 text-slate-700 dark:text-slate-300">Max time to wait for *arr to replace a corrupt file</td>
                                </tr>
                                <tr>
                                    <td className="py-2 px-3 font-mono text-cyan-400">HEALARR_VERIFICATION_INTERVAL</td>
                                    <td className="py-2 px-3 text-slate-600 dark:text-slate-400">30s</td>
                                    <td className="py-2 px-3 text-slate-700 dark:text-slate-300">Polling interval when checking for file replacement</td>
                                </tr>
                                <tr>
                                    <td className="py-2 px-3 font-mono text-cyan-400">HEALARR_DEFAULT_MAX_RETRIES</td>
                                    <td className="py-2 px-3 text-slate-600 dark:text-slate-400">3</td>
                                    <td className="py-2 px-3 text-slate-700 dark:text-slate-300">Default retry limit for failed remediations (can be overridden per scan path)</td>
                                </tr>
                                <tr>
                                    <td className="py-2 px-3 font-mono text-cyan-400">HEALARR_DRY_RUN</td>
                                    <td className="py-2 px-3 text-slate-600 dark:text-slate-400">false</td>
                                    <td className="py-2 px-3 text-slate-700 dark:text-slate-300">Global dry-run mode - detects corruption but won't delete files</td>
                                </tr>
                                <tr>
                                    <td className="py-2 px-3 font-mono text-cyan-400">HEALARR_RETENTION_DAYS</td>
                                    <td className="py-2 px-3 text-slate-600 dark:text-slate-400">90</td>
                                    <td className="py-2 px-3 text-slate-700 dark:text-slate-300">Days to keep old events and scan history (0 to disable auto-pruning)</td>
                                </tr>
                                <tr>
                                    <td className="py-2 px-3 font-mono text-cyan-400">HEALARR_ARR_RATE_LIMIT_RPS</td>
                                    <td className="py-2 px-3 text-slate-600 dark:text-slate-400">5</td>
                                    <td className="py-2 px-3 text-slate-700 dark:text-slate-300">Max requests per second to *arr APIs (prevents hammering)</td>
                                </tr>
                                <tr>
                                    <td className="py-2 px-3 font-mono text-cyan-400">HEALARR_ARR_RATE_LIMIT_BURST</td>
                                    <td className="py-2 px-3 text-slate-600 dark:text-slate-400">10</td>
                                    <td className="py-2 px-3 text-slate-700 dark:text-slate-300">Burst size for *arr API rate limiting</td>
                                </tr>
                            </tbody>
                        </table>
                    </div>

                    <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-3">
                        <p className="text-sm font-semibold text-slate-700 dark:text-slate-300">Example docker-compose.yml:</p>
                        <pre className="bg-slate-900 p-3 rounded text-xs text-slate-700 dark:text-slate-300 overflow-x-auto">
{`services:
  healarr:
    image: ghcr.io/mescon/healarr:latest
    container_name: healarr
    environment:
      - HEALARR_PORT=3090
      - HEALARR_LOG_LEVEL=info
      - HEALARR_DATABASE_PATH=/config/healarr.db
      - HEALARR_VERIFICATION_TIMEOUT=72h
      - HEALARR_VERIFICATION_INTERVAL=30s
      - HEALARR_DEFAULT_MAX_RETRIES=3
      - HEALARR_RETENTION_DAYS=90       # Days to keep old data (0 to disable)
      # - HEALARR_DRY_RUN=true          # Test mode - no files deleted
      # - HEALARR_BASE_PATH=/healarr    # Uncomment for reverse proxy with subpath
    volumes:
      - ./config:/config
      - /path/to/media:/media:ro  # Or mount as /tv, /movies to match *arr paths
    ports:
      - "3090:3090"
    restart: unless-stopped`}
                        </pre>
                    </div>

                    <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-3">
                        <p className="text-sm font-semibold text-slate-700 dark:text-slate-300">Command-Line Flags:</p>
                        <p className="text-sm text-slate-600 dark:text-slate-400 mb-2">
                            All environment variables can also be set via command-line flags. Flags take precedence over environment variables.
                        </p>
                        <pre className="bg-slate-900 p-3 rounded text-xs text-slate-700 dark:text-slate-300 overflow-x-auto">
{`# View all available flags
./healarr --help

# Example: Run with custom settings
./healarr --port 8080 --log-level debug --retention-days 30

# Example: Dry-run mode for testing
./healarr --dry-run

# Example: Disable automatic data pruning
./healarr --retention-days 0`}
                        </pre>
                    </div>

                    <div className="bg-blue-500/5 border border-blue-500/20 rounded-lg p-4 space-y-2">
                        <p className="text-sm font-semibold text-blue-300">💡 Tips</p>
                        <ul className="text-sm text-slate-700 dark:text-slate-300 space-y-1 list-disc list-inside">
                            <li>Duration values accept Go duration strings: <code className="bg-slate-800 px-1 rounded">30s</code>, <code className="bg-slate-800 px-1 rounded">5m</code>, <code className="bg-slate-800 px-1 rounded">72h</code></li>
                            <li>Set <code className="bg-slate-800 px-1 rounded">HEALARR_LOG_LEVEL=debug</code> for troubleshooting, then switch back to <code className="bg-slate-800 px-1 rounded">info</code></li>
                            <li><span className="font-semibold">Pro tip:</span> Mount media with the same paths your *arr apps use (e.g., <code className="bg-slate-800 px-1 rounded">-v /host/tv:/tv:ro</code>) to avoid path translation</li>
                            <li>Mount your media paths as read-only (<code className="bg-slate-800 px-1 rounded">:ro</code>) - Healarr only reads files to check for corruption</li>
                        </ul>
                    </div>
                </Accordion>
            </motion.div>

            {/* Reverse Proxy Setup - Accordion */}
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.35 }}
            >
                <Accordion
                    title="Reverse Proxy Setup"
                    icon={<Globe className="w-5 h-5 text-orange-400" />}
                    iconBgClass="bg-orange-500/10 border-orange-500/20"
                >
                    <div className="bg-amber-500/10 border border-amber-500/30 rounded-lg p-4">
                        <p className="text-sm font-semibold text-amber-300">⚠️ WebSocket Support Required</p>
                        <p className="text-sm text-slate-700 dark:text-slate-300 mt-1">
                            Healarr uses WebSockets for real-time updates (scan progress, live stats, etc.). 
                            Your reverse proxy <span className="font-semibold">must</span> be configured to support WebSocket connections, 
                            or the UI will not receive live updates.
                        </p>
                    </div>

                    <p className="text-slate-700 dark:text-slate-300">
                        To host Healarr at a subpath like <code className="bg-slate-800 px-1 rounded">domain.com/healarr/</code>, 
                        set the environment variable <code className="bg-slate-800 px-1 rounded">HEALARR_BASE_PATH=/healarr</code> and configure your reverse proxy:
                    </p>

                    {/* Caddy */}
                    <div className="border-l-2 border-green-500/50 pl-4 space-y-2">
                        <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Caddy</h3>
                        <pre className="bg-slate-100 dark:bg-slate-900 p-3 rounded text-xs text-slate-700 dark:text-slate-300 overflow-x-auto">
{`example.com {
    # Healarr with WebSocket support (automatic in Caddy)
    handle_path /healarr/* {
        reverse_proxy healarr:3090
    }
}

# Or for root path:
healarr.example.com {
    reverse_proxy healarr:3090
}`}
                        </pre>
                    </div>

                    {/* nginx */}
                    <div className="border-l-2 border-blue-500/50 pl-4 space-y-2">
                        <h3 className="text-lg font-semibold text-slate-900 dark:text-white">nginx</h3>
                        <pre className="bg-slate-100 dark:bg-slate-900 p-3 rounded text-xs text-slate-700 dark:text-slate-300 overflow-x-auto">
{`# For subpath (/healarr/)
location /healarr/ {
    proxy_pass http://healarr:3090/healarr/;
    
    # Required for WebSockets
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    
    # Standard proxy headers
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    
    # Timeouts for long-running connections
    proxy_read_timeout 86400s;
    proxy_send_timeout 86400s;
}

# For root path (healarr.example.com)
server {
    listen 443 ssl;
    server_name healarr.example.com;
    
    location / {
        proxy_pass http://healarr:3090;
        
        # Required for WebSockets
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        
        # Standard proxy headers
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        
        # Timeouts for long-running connections
        proxy_read_timeout 86400s;
        proxy_send_timeout 86400s;
    }
}`}
                        </pre>
                    </div>

                    {/* Traefik */}
                    <div className="border-l-2 border-purple-500/50 pl-4 space-y-2">
                        <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Traefik (Docker labels)</h3>
                        <pre className="bg-slate-100 dark:bg-slate-900 p-3 rounded text-xs text-slate-700 dark:text-slate-300 overflow-x-auto">
{`services:
  healarr:
    image: healarr:latest
    environment:
      - HEALARR_BASE_PATH=/healarr
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.healarr.rule=Host(\`example.com\`) && PathPrefix(\`/healarr\`)"
      - "traefik.http.routers.healarr.entrypoints=https"  # Use your HTTPS entrypoint name
      - "traefik.http.routers.healarr.tls=true"
      - "traefik.http.services.healarr.loadbalancer.server.port=3090"
      # Strip prefix middleware (see note below)
      # - "traefik.http.middlewares.healarr-strip.stripprefix.prefixes=/healarr"
      # - "traefik.http.routers.healarr.middlewares=healarr-strip"

# For root path (subdomain):
  healarr:
    image: healarr:latest
    labels:
      - "traefik.enable=true"
      - "traefik.http.routers.healarr.rule=Host(\`healarr.example.com\`)"
      - "traefik.http.routers.healarr.entrypoints=https"  # Use your HTTPS entrypoint name
      - "traefik.http.routers.healarr.tls=true"
      - "traefik.http.services.healarr.loadbalancer.server.port=3090"`}
                        </pre>
                        <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-3 space-y-2">
                            <p className="text-xs text-slate-700 dark:text-slate-300">
                                <strong className="text-purple-400">About entrypoints:</strong> Replace <code className="bg-slate-900 px-1 rounded">https</code> with your actual entrypoint name. 
                                Common names include <code className="bg-slate-900 px-1 rounded">websecure</code>, <code className="bg-slate-900 px-1 rounded">web-secure</code>, or <code className="bg-slate-900 px-1 rounded">https</code>.
                                Check your Traefik static configuration to find yours.
                            </p>
                            <p className="text-xs text-slate-700 dark:text-slate-300">
                                <strong className="text-purple-400">About strip prefix:</strong> Since Healarr uses <code className="bg-slate-900 px-1 rounded">HEALARR_BASE_PATH=/healarr</code>, 
                                Healarr expects requests to arrive with the <code className="bg-slate-900 px-1 rounded">/healarr</code> prefix intact. 
                                <strong>Do not use the stripprefix middleware</strong> unless HEALARR_BASE_PATH is set to <code className="bg-slate-900 px-1 rounded">/</code>.
                            </p>
                        </div>
                        <p className="text-xs text-slate-500">Traefik automatically handles WebSocket upgrades.</p>
                    </div>

                    {/* Apache */}
                    <div className="border-l-2 border-red-500/50 pl-4 space-y-2">
                        <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Apache</h3>
                        <pre className="bg-slate-900 p-3 rounded text-xs text-slate-700 dark:text-slate-300 overflow-x-auto">
{`# Enable required modules:
# a2enmod proxy proxy_http proxy_wstunnel rewrite

# For subpath (/healarr/)
<VirtualHost *:443>
    ServerName example.com
    
    ProxyPreserveHost On
    
    # WebSocket support
    RewriteEngine On
    RewriteCond %{HTTP:Upgrade} websocket [NC]
    RewriteCond %{HTTP:Connection} upgrade [NC]
    RewriteRule ^/healarr/(.*) ws://healarr:3090/healarr/$1 [P,L]
    
    # Regular HTTP
    ProxyPass /healarr/ http://healarr:3090/healarr/
    ProxyPassReverse /healarr/ http://healarr:3090/healarr/
</VirtualHost>

# For subdomain (healarr.example.com)
<VirtualHost *:443>
    ServerName healarr.example.com
    
    ProxyPreserveHost On
    
    # WebSocket support
    RewriteEngine On
    RewriteCond %{HTTP:Upgrade} websocket [NC]
    RewriteCond %{HTTP:Connection} upgrade [NC]
    RewriteRule ^/(.*) ws://healarr:3090/$1 [P,L]
    
    # Regular HTTP
    ProxyPass / http://healarr:3090/
    ProxyPassReverse / http://healarr:3090/
</VirtualHost>`}
                        </pre>
                    </div>

                    <div className="bg-blue-500/5 border border-blue-500/20 rounded-lg p-4 space-y-2">
                        <p className="text-sm font-semibold text-blue-300">💡 Testing WebSocket Connection</p>
                        <ul className="text-sm text-slate-700 dark:text-slate-300 space-y-1 list-disc list-inside">
                            <li>Open the browser developer console (F12) and check the Network tab</li>
                            <li>Look for a WebSocket connection to <code className="bg-slate-800 px-1 rounded">/api/ws</code></li>
                            <li>If it shows "101 Switching Protocols", WebSockets are working</li>
                            <li>If you see errors or the connection fails, check your proxy configuration</li>
                        </ul>
                    </div>
                </Accordion>
            </motion.div>

            {/* Troubleshooting - Accordion */}
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.4 }}
            >
                <Accordion
                    title="Troubleshooting"
                    icon={<HelpCircle className="w-5 h-5 text-red-400" />}
                    iconBgClass="bg-red-500/10 border-red-500/20"
                >
                    <div className="space-y-4">
                        {/* Real-time Updates */}
                        <div className="border-l-2 border-amber-500/50 pl-4 space-y-2">
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Real-time Updates Not Working</h3>
                            <p className="text-slate-700 dark:text-slate-300">
                                If scan progress or dashboard stats aren't updating in real-time, WebSockets may not be working.
                            </p>
                            <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-3">
                                <p className="text-sm font-semibold text-slate-700 dark:text-slate-300">Things to check:</p>
                                <ul className="text-sm text-slate-600 dark:text-slate-400 space-y-1 list-disc list-inside">
                                    <li>Ensure your reverse proxy is configured for WebSocket support (see Reverse Proxy Setup above)</li>
                                    <li>Check browser console for WebSocket connection errors</li>
                                    <li>Verify <code className="bg-slate-800 px-1 rounded">/api/ws</code> endpoint is accessible</li>
                                    <li>Some firewalls or security software may block WebSocket connections</li>
                                </ul>
                            </div>
                        </div>

                        {/* Webhook Path Mapping */}
                        <div className="border-l-2 border-red-500/50 pl-4 space-y-2">
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Webhook Path Mapping Errors</h3>
                            <p className="text-slate-700 dark:text-slate-300">
                                If you see errors in logs like <span className="font-mono text-red-400">"Webhook path mapping failed"</span>,
                                it means Sonarr/Radarr/Whisparr reported a file path that doesn't match any of your configured scan paths.
                            </p>
                            <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-3">
                                <p className="text-sm font-semibold text-slate-700 dark:text-slate-300">How to fix:</p>
                                <ol className="text-sm text-slate-600 dark:text-slate-400 space-y-2 list-decimal list-inside">
                                    <li>Check the error log to see which path failed (e.g., <span className="font-mono text-blue-400">/tv/Show Name/episode.mkv</span>)</li>
                                    <li>Go to <span className="font-semibold text-slate-700 dark:text-slate-300">Config → Scan Paths</span></li>
                                    <li>Add a new scan path with:
                                        <ul className="ml-6 mt-1 space-y-1 list-disc list-inside">
                                            <li><span className="font-semibold">Local Path:</span> Path as Healarr sees it (e.g., <span className="font-mono">/media/tv</span> or <span className="font-mono">/tv</span> if using same paths as *arr)</li>
                                            <li><span className="font-semibold">*arr Path:</span> The path as Sonarr/Radarr/Whisparr sees it (e.g., <span className="font-mono">/tv</span>)</li>
                                            <li><span className="font-semibold">*arr Server:</span> Select the matching server</li>
                                        </ul>
                                    </li>
                                    <li>The paths must share a common prefix - Healarr will replace the *arr path prefix with your local path prefix</li>
                                    <li><span className="font-semibold text-green-400">Best practice:</span> Mount media with the same paths as *arr (e.g., <span className="font-mono">-v /host/tv:/tv:ro</span>) so both paths are identical</li>
                                </ol>
                                <p className="text-xs text-slate-500 mt-2">
                                    <span className="font-semibold">Example:</span> If Sonarr reports <span className="font-mono">/tv/MyShow/S01E01.mkv</span> and your
                                    scan path has *arr Path = <span className="font-mono">/tv</span>, Local Path = <span className="font-mono">/media/tv</span>,
                                    Healarr will scan <span className="font-mono">/media/tv/MyShow/S01E01.mkv</span>. Or if you mounted with <span className="font-mono">-v /host/tv:/tv:ro</span>, just use <span className="font-mono">/tv</span> for both paths.
                                </p>
                            </div>
                        </div>

                        {/* Scans Not Starting */}
                        <div className="border-l-2 border-blue-500/50 pl-4 space-y-2">
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Scans Not Starting</h3>
                            <p className="text-slate-700 dark:text-slate-300">
                                If clicking the scan button doesn't start a scan, or scheduled scans aren't running.
                            </p>
                            <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-3">
                                <p className="text-sm font-semibold text-slate-700 dark:text-slate-300">Things to check:</p>
                                <ul className="text-sm text-slate-600 dark:text-slate-400 space-y-1 list-disc list-inside">
                                    <li>Check the Logs page for error messages</li>
                                    <li>Verify the scan path exists and is accessible from the Healarr container</li>
                                    <li>Ensure ffprobe is available in the container (run <code className="bg-slate-800 px-1 rounded">ffprobe -version</code>)</li>
                                    <li>Check if another scan is already running - only one scan runs at a time</li>
                                    <li>For scheduled scans, ensure the schedule is enabled (toggle is on)</li>
                                </ul>
                            </div>
                        </div>

                        {/* Corruption Not Detected */}
                        <div className="border-l-2 border-purple-500/50 pl-4 space-y-2">
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Known Corrupt Files Not Detected</h3>
                            <p className="text-slate-700 dark:text-slate-300">
                                If you have a file you know is corrupt but Healarr reports it as healthy.
                            </p>
                            <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-3">
                                <p className="text-sm font-semibold text-slate-700 dark:text-slate-300">Possible causes:</p>
                                <ul className="text-sm text-slate-600 dark:text-slate-400 space-y-1 list-disc list-inside">
                                    <li>The corruption may be in a stream Healarr doesn't check (e.g., subtitles only)</li>
                                    <li>The file may have minor errors that ffprobe doesn't consider fatal</li>
                                    <li>The corruption might be visual/audio artifacts without header damage</li>
                                    <li>Try running <code className="bg-slate-800 px-1 rounded">ffprobe -v error -i /path/to/file</code> manually to see what errors are detected</li>
                                </ul>
                            </div>
                        </div>

                        {/* Remediation Stuck */}
                        <div className="border-l-2 border-orange-500/50 pl-4 space-y-2">
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Remediation Stuck or Taking Too Long</h3>
                            <p className="text-slate-700 dark:text-slate-300">
                                If a corruption stays in "In Progress" status for an unusually long time.
                            </p>
                            <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-3">
                                <p className="text-sm font-semibold text-slate-700 dark:text-slate-300">Things to check:</p>
                                <ul className="text-sm text-slate-600 dark:text-slate-400 space-y-1 list-disc list-inside">
                                    <li>Click the corruption row to view the Remediation Journey and see which step is pending</li>
                                    <li>If stuck on "Searching" - Sonarr/Radarr/Whisparr may not have found a replacement release</li>
                                    <li>If stuck on "Downloading" - check your download client for issues</li>
                                    <li>If stuck on "Verifying" - the download may still be in progress or post-processing</li>
                                    <li>Check <code className="bg-slate-800 px-1 rounded">HEALARR_VERIFICATION_TIMEOUT</code> - default is 72 hours</li>
                                    <li>Verify the *arr server is online and the API key is valid</li>
                                </ul>
                            </div>
                        </div>

                        {/* Connection Refused */}
                        <div className="border-l-2 border-cyan-500/50 pl-4 space-y-2">
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">*arr Server Connection Refused</h3>
                            <p className="text-slate-700 dark:text-slate-300">
                                Errors connecting to Sonarr, Radarr, or Whisparr when testing or saving server configuration.
                            </p>
                            <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-3">
                                <p className="text-sm font-semibold text-slate-700 dark:text-slate-300">Things to check:</p>
                                <ul className="text-sm text-slate-600 dark:text-slate-400 space-y-1 list-disc list-inside">
                                    <li>Verify the URL is correct (include http:// or https://)</li>
                                    <li>Check that the *arr service is running and accessible</li>
                                    <li>If using Docker, ensure Healarr can reach the *arr container (same network or use host IP)</li>
                                    <li>Verify the API key is correct (Settings → General → API Key in *arr)</li>
                                    <li>Check for SSL certificate issues if using HTTPS</li>
                                    <li>Try using the container name instead of IP if on the same Docker network</li>
                                </ul>
                            </div>
                        </div>

                        {/* Whisparr Version Mismatch */}
                        <div className="border-l-2 border-pink-500/50 pl-4 space-y-2">
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Whisparr Version Mismatch</h3>
                            <p className="text-slate-700 dark:text-slate-300">
                                API errors or unexpected behavior when using Whisparr - often caused by selecting the wrong version.
                            </p>
                            <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-3">
                                <p className="text-sm font-semibold text-slate-700 dark:text-slate-300">Understanding Whisparr versions:</p>
                                <ul className="text-sm text-slate-600 dark:text-slate-400 space-y-2 list-disc list-inside">
                                    <li><strong className="text-pink-400">Whisparr v2</strong> is based on <strong className="text-purple-400">Sonarr</strong> - uses series/episodes API structure</li>
                                    <li><strong className="text-pink-300">Whisparr v3</strong> is based on <strong className="text-yellow-400">Radarr</strong> - uses movie API structure</li>
                                </ul>
                                <p className="text-sm font-semibold text-slate-700 dark:text-slate-300 mt-3">How to check your version:</p>
                                <ol className="text-sm text-slate-600 dark:text-slate-400 space-y-1 list-decimal list-inside">
                                    <li>Open your Whisparr web UI</li>
                                    <li>Go to <span className="font-mono text-blue-400">System → Status</span></li>
                                    <li>Check the version number (v2.x.x or v3.x.x)</li>
                                </ol>
                                <p className="text-sm font-semibold text-slate-700 dark:text-slate-300 mt-3">If configured incorrectly:</p>
                                <ul className="text-sm text-slate-600 dark:text-slate-400 space-y-1 list-disc list-inside">
                                    <li>Go to Settings → *arr Instances</li>
                                    <li>Delete the misconfigured Whisparr instance</li>
                                    <li>Re-add it with the correct version type</li>
                                </ul>
                            </div>
                        </div>

                        {/* Database Issues */}
                        <div className="border-l-2 border-slate-500/50 pl-4 space-y-2">
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Database or Storage Issues</h3>
                            <p className="text-slate-700 dark:text-slate-300">
                                Errors about database locks, disk space, or data not persisting between restarts.
                            </p>
                            <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-3">
                                <p className="text-sm font-semibold text-slate-700 dark:text-slate-300">Things to check:</p>
                                <ul className="text-sm text-slate-600 dark:text-slate-400 space-y-1 list-disc list-inside">
                                    <li>Ensure the <code className="bg-slate-800 px-1 rounded">/config</code> volume is properly mounted</li>
                                    <li>Check that the container has write permissions to the config directory</li>
                                    <li>Verify there's sufficient disk space on the host</li>
                                    <li>If using SQLite, avoid network-mounted storage (NFS/SMB) for the database file</li>
                                    <li>Check for "database is locked" errors in logs - may indicate concurrent access issues</li>
                                </ul>
                            </div>
                            <div className="bg-green-500/5 border border-green-500/20 rounded-lg p-4 mt-3 space-y-2">
                                <p className="text-sm font-semibold text-green-300">Automatic Database Maintenance</p>
                                <p className="text-sm text-slate-700 dark:text-slate-300">
                                    Healarr automatically maintains its database to prevent bloat:
                                </p>
                                <ul className="text-sm text-slate-600 dark:text-slate-400 space-y-1 list-disc list-inside">
                                    <li><strong>WAL mode</strong> enabled for better concurrency and crash recovery</li>
                                    <li><strong>Integrity check</strong> runs on startup to detect corruption</li>
                                    <li><strong>Daily maintenance</strong> at 3 AM: prunes old data, vacuums, updates statistics</li>
                                    <li><strong>Automatic backups</strong> every 6 hours (last 5 kept in <code className="bg-slate-800 px-1 rounded">/config/backups/</code>)</li>
                                    <li>Set <code className="bg-slate-800 px-1 rounded">HEALARR_RETENTION_DAYS=0</code> to disable auto-pruning</li>
                                </ul>
                            </div>
                        </div>

                        {/* Forgot Password */}
                        <div className="border-l-2 border-purple-500/50 pl-4 space-y-2">
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Forgot Password</h3>
                            <p className="text-slate-700 dark:text-slate-300">
                                If you've forgotten your Healarr password, you can reset it by deleting the password from the database.
                            </p>
                            <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-3">
                                <p className="text-sm font-semibold text-slate-700 dark:text-slate-300">To reset your password:</p>
                                <ol className="text-sm text-slate-600 dark:text-slate-400 space-y-2 list-decimal list-inside">
                                    <li>Stop Healarr</li>
                                    <li>Open the database file with SQLite:
                                        <pre className="mt-1 bg-slate-900 text-slate-300 p-2 rounded text-xs overflow-x-auto">sqlite3 /path/to/healarr.db</pre>
                                    </li>
                                    <li>Delete the password setting:
                                        <pre className="mt-1 bg-slate-900 text-slate-300 p-2 rounded text-xs overflow-x-auto">DELETE FROM settings WHERE key = 'password_hash';</pre>
                                    </li>
                                    <li>Exit SQLite: <code className="bg-slate-800 px-1 rounded">.quit</code></li>
                                    <li>Restart Healarr - you'll be prompted to set a new password</li>
                                </ol>
                                <p className="text-xs text-amber-400 mt-2">
                                    <strong>Docker users:</strong> The database is typically at <code className="bg-slate-800 px-1 rounded">/config/healarr.db</code> inside the container, 
                                    or in your mounted config directory on the host. <strong>Bare-metal:</strong> Check <code className="bg-slate-800 px-1 rounded">./config/healarr.db</code> next to the executable.
                                </p>
                            </div>
                        </div>
                    </div>
                </Accordion>
            </motion.div>

            {/* Notifications & Integrations - Accordion */}
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.45 }}
            >
                <Accordion
                    title="Notifications & Integrations"
                    icon={<Bell className="w-5 h-5 text-pink-400" />}
                    iconBgClass="bg-pink-500/10 border-pink-500/20"
                >
                    <div className="space-y-6">
                        <p className="text-slate-700 dark:text-slate-300">
                            Healarr supports <span className="font-semibold text-pink-400">20+ notification services</span> through the 
                            <a href="https://containrrr.dev/shoutrrr/" target="_blank" rel="noopener noreferrer" className="text-blue-400 hover:text-blue-300 ml-1">Shoutrrr</a> library.
                            Configure notifications to stay informed about scan results, corruption detection, and remediation progress.
                        </p>

                        {/* Supported Services */}
                        <div className="border-l-2 border-pink-500/50 pl-4 space-y-3">
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Supported Notification Services</h3>
                            <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
                                <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-3">
                                    <p className="text-sm font-semibold text-slate-700 dark:text-slate-300 mb-2">📱 Popular</p>
                                    <ul className="text-xs text-slate-600 dark:text-slate-400 space-y-1">
                                        <li>🎮 Discord</li>
                                        <li>💬 Slack</li>
                                        <li>✈️ Telegram</li>
                                        <li>📧 Email (SMTP)</li>
                                    </ul>
                                </div>
                                <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-3">
                                    <p className="text-sm font-semibold text-slate-700 dark:text-slate-300 mb-2">🔔 Push</p>
                                    <ul className="text-xs text-slate-600 dark:text-slate-400 space-y-1">
                                        <li>📱 Pushover</li>
                                        <li>🔔 Gotify</li>
                                        <li>📣 ntfy</li>
                                        <li>📤 Pushbullet</li>
                                        <li>🐕 Bark</li>
                                        <li>🔗 Join</li>
                                    </ul>
                                </div>
                                <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-3">
                                    <p className="text-sm font-semibold text-slate-700 dark:text-slate-300 mb-2">👥 Team</p>
                                    <ul className="text-xs text-slate-600 dark:text-slate-400 space-y-1">
                                        <li>👥 Microsoft Teams</li>
                                        <li>💭 Google Chat</li>
                                        <li>🟣 Mattermost</li>
                                        <li>🚀 Rocket.Chat</li>
                                        <li>💧 Zulip</li>
                                    </ul>
                                </div>
                                <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-3">
                                    <p className="text-sm font-semibold text-slate-700 dark:text-slate-300 mb-2">💬 Messaging</p>
                                    <ul className="text-xs text-slate-600 dark:text-slate-400 space-y-1">
                                        <li>💬 WhatsApp</li>
                                        <li>🔒 Signal</li>
                                        <li>🔲 Matrix</li>
                                    </ul>
                                </div>
                            </div>
                        </div>

                        {/* Generic Webhook Integration */}
                        <div className="border-l-2 border-green-500/50 pl-4 space-y-3">
                            <div className="flex items-center gap-2">
                                <Zap className="w-5 h-5 text-green-400" />
                                <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Generic Webhook Integration</h3>
                            </div>
                            <p className="text-slate-700 dark:text-slate-300">
                                The <span className="font-semibold text-green-400">Generic Webhook</span> provider allows you to integrate Healarr 
                                with any HTTP endpoint, making it perfect for connecting with other tools in your media stack.
                            </p>
                            
                            <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-3">
                                <p className="text-sm font-semibold text-slate-700 dark:text-slate-300">Common Use Cases:</p>
                                <ul className="text-sm text-slate-600 dark:text-slate-400 space-y-2 list-disc list-inside">
                                    <li><span className="font-semibold">Home Automation:</span> Trigger Home Assistant automations when corruption is detected or resolved</li>
                                    <li><span className="font-semibold">Custom Dashboards:</span> Push data to Grafana, InfluxDB, or custom monitoring systems</li>
                                    <li><span className="font-semibold">Workflow Automation:</span> Integrate with n8n, Node-RED, or Huginn for complex workflows</li>
                                    <li><span className="font-semibold">Logging:</span> Send events to Loki, Elasticsearch, or other log aggregators</li>
                                    <li><span className="font-semibold">Custom Scripts:</span> Trigger scripts or containers via webhook endpoints</li>
                                </ul>
                            </div>

                            <div className="bg-green-500/5 border border-green-500/20 rounded-lg p-4 space-y-3">
                                <p className="text-sm font-semibold text-green-300">Example: JSON Webhook Payload</p>
                                <pre className="bg-slate-900 p-3 rounded text-xs text-slate-300 overflow-x-auto">{`{
  "title": "🔴 Corruption detected: episode.mkv",
  "message": "Type: video_stream_error | Path: /media/tv/Show/S01E01.mkv",
  "event": "corruption_detected",
  "timestamp": "2025-01-15T14:30:00Z",
  "source": "healarr",
  "data": {
    "file_path": "/media/tv/Show/Season 1/S01E01.mkv",
    "file_name": "S01E01.mkv",
    "corruption_type": "video_stream_error",
    "scan_path": "/media/tv",
    "healthy_files": 1250,
    "corrupt_files": 1,
    "total_files": 1251
  }
}`}</pre>
                                <p className="text-xs text-slate-500">
                                    Generic webhooks now send rich JSON payloads with full event context, making automation integrations much easier.
                                </p>
                            </div>
                        </div>

                        {/* IFTTT & Automation */}
                        <div className="border-l-2 border-purple-500/50 pl-4 space-y-3">
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Automation & Alerting</h3>
                            <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                                <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4">
                                    <div className="flex items-center gap-2 mb-2">
                                        <span className="text-lg">⚡</span>
                                        <span className="font-semibold text-slate-700 dark:text-slate-300">IFTTT</span>
                                    </div>
                                    <p className="text-xs text-slate-600 dark:text-slate-400">
                                        Connect Healarr events to hundreds of services via IFTTT applets. 
                                        Perfect for sending SMS alerts, updating spreadsheets, or triggering smart home devices.
                                    </p>
                                </div>
                                <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4">
                                    <div className="flex items-center gap-2 mb-2">
                                        <span className="text-lg">🔗</span>
                                        <span className="font-semibold text-slate-700 dark:text-slate-300">n8n / Make / Zapier</span>
                                    </div>
                                    <p className="text-xs text-slate-600 dark:text-slate-400">
                                        Use generic webhooks with any automation platform. The rich JSON payload 
                                        provides all context needed for advanced workflows and conditional logic.
                                    </p>
                                </div>
                            </div>
                        </div>

                        {/* Tips */}
                        <div className="bg-blue-500/5 border border-blue-500/20 rounded-lg p-4 space-y-2">
                            <p className="text-sm font-semibold text-blue-300">💡 Tips</p>
                            <ul className="text-sm text-slate-700 dark:text-slate-300 space-y-1 list-disc list-inside">
                                <li>Use <span className="font-semibold">throttling</span> to prevent notification spam during large scans</li>
                                <li>Create multiple notification configs with different event filters (e.g., only critical events to phone)</li>
                                <li>The <span className="font-semibold">Custom (Shoutrrr URL)</span> option lets you use any Shoutrrr-supported service directly</li>
                                <li>Check the <a href="https://containrrr.dev/shoutrrr/v0.8/services/overview/" target="_blank" rel="noopener noreferrer" className="text-blue-400 hover:text-blue-300">Shoutrrr documentation</a> for advanced URL formatting</li>
                            </ul>
                        </div>
                    </div>
                </Accordion>
            </motion.div>

            {/* Health Endpoint - Accordion */}
            <motion.div
                initial={{ opacity: 0, y: 20 }}
                animate={{ opacity: 1, y: 0 }}
                transition={{ delay: 0.5 }}
            >
                <Accordion
                    title="Health Endpoint"
                    icon={<Heart className="w-5 h-5 text-green-400" />}
                    iconBgClass="bg-green-500/10 border-green-500/20"
                >
                    <div className="space-y-4">
                        <p className="text-slate-700 dark:text-slate-300">
                            Healarr provides a health endpoint at <code className="bg-slate-800 px-2 py-1 rounded text-green-400">/api/health</code> that can be used by external monitoring tools, container orchestrators, and load balancers.
                        </p>

                        {/* Endpoint Details */}
                        <div className="border-l-2 border-green-500/50 pl-4 space-y-2">
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Endpoint Details</h3>
                            <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-3">
                                <div className="flex items-center gap-4">
                                    <span className="text-sm font-semibold text-slate-700 dark:text-slate-300">URL:</span>
                                    <code className="text-sm text-green-400 bg-slate-900 px-2 py-1 rounded">GET /api/health</code>
                                </div>
                                <div className="flex items-center gap-4">
                                    <span className="text-sm font-semibold text-slate-700 dark:text-slate-300">Auth:</span>
                                    <span className="text-sm text-slate-600 dark:text-slate-400">None required (public endpoint)</span>
                                </div>
                            </div>
                        </div>

                        {/* Response Format */}
                        <div className="border-l-2 border-blue-500/50 pl-4 space-y-2">
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Response Format</h3>
                            <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4">
                                <pre className="text-xs text-slate-700 dark:text-slate-300 overflow-x-auto">{`{
  "status": "healthy",        // "healthy", "degraded", or "unhealthy"
  "version": "1.0.1",         // Healarr version
  "uptime": "2d 5h 30m",      // Server uptime
  "database": {
    "status": "connected",    // "connected" or "error"
    "size_bytes": 3616768     // Database file size
  },
  "arr_instances": {
    "online": 4,              // Number of responsive *arr servers
    "total": 4                // Total configured *arr servers
  },
  "active_scans": 0,          // Currently running scans
  "pending_corruptions": 3,   // Corruptions awaiting remediation
  "websocket_clients": 2      // Connected UI clients
}`}</pre>
                            </div>
                        </div>

                        {/* Status Values */}
                        <div className="border-l-2 border-amber-500/50 pl-4 space-y-2">
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Status Values</h3>
                            <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-3">
                                <div className="flex items-start gap-3">
                                    <span className="w-3 h-3 rounded-full bg-green-500 mt-1.5 flex-shrink-0"></span>
                                    <div>
                                        <span className="text-sm font-semibold text-green-400">healthy</span>
                                        <p className="text-sm text-slate-600 dark:text-slate-400">All systems operational. Database connected, all *arr instances online.</p>
                                    </div>
                                </div>
                                <div className="flex items-start gap-3">
                                    <span className="w-3 h-3 rounded-full bg-yellow-500 mt-1.5 flex-shrink-0"></span>
                                    <div>
                                        <span className="text-sm font-semibold text-yellow-400">degraded</span>
                                        <p className="text-sm text-slate-600 dark:text-slate-400">Partial functionality. One or more *arr instances are offline, but database is connected. A notification is sent when this status is detected (if configured).</p>
                                    </div>
                                </div>
                                <div className="flex items-start gap-3">
                                    <span className="w-3 h-3 rounded-full bg-red-500 mt-1.5 flex-shrink-0"></span>
                                    <div>
                                        <span className="text-sm font-semibold text-red-400">unhealthy</span>
                                        <p className="text-sm text-slate-600 dark:text-slate-400">Critical failure. Database connection failed.</p>
                                    </div>
                                </div>
                            </div>
                        </div>

                        {/* Docker Health Check */}
                        <div className="border-l-2 border-purple-500/50 pl-4 space-y-2">
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Docker Health Check</h3>
                            <p className="text-slate-700 dark:text-slate-300">
                                Add a health check to your Docker container:
                            </p>
                            <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4">
                                <pre className="text-xs text-slate-700 dark:text-slate-300 overflow-x-auto">{`services:
  healarr:
    image: healarr:latest
    healthcheck:
      test: ["CMD", "curl", "-f", "http://localhost:3090/api/health"]
      interval: 30s
      timeout: 10s
      retries: 3
      start_period: 10s`}</pre>
                            </div>
                        </div>

                        {/* Monitoring Integration */}
                        <div className="border-l-2 border-cyan-500/50 pl-4 space-y-2">
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Monitoring Integration</h3>
                            <p className="text-slate-700 dark:text-slate-300">
                                Use the health endpoint with popular monitoring tools:
                            </p>
                            <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-3">
                                <div>
                                    <span className="text-sm font-semibold text-slate-700 dark:text-slate-300">Uptime Kuma:</span>
                                    <p className="text-xs text-slate-600 dark:text-slate-400 mt-1">Add HTTP(s) monitor → URL: <code className="bg-slate-900 px-1 rounded">http://healarr:3090/api/health</code> → Expected keyword: <code className="bg-slate-900 px-1 rounded">"healthy"</code></p>
                                </div>
                                <div>
                                    <span className="text-sm font-semibold text-slate-700 dark:text-slate-300">Prometheus:</span>
                                    <p className="text-xs text-slate-600 dark:text-slate-400 mt-1">Use blackbox_exporter with HTTP probe or parse JSON response for status field</p>
                                </div>
                                <div>
                                    <span className="text-sm font-semibold text-slate-700 dark:text-slate-300">Traefik:</span>
                                    <p className="text-xs text-slate-600 dark:text-slate-400 mt-1">Configure health check on <code className="bg-slate-900 px-1 rounded">/api/health</code> for load balancing</p>
                                </div>
                            </div>
                        </div>

                        {/* Notifications */}
                        <div className="border-l-2 border-pink-500/50 pl-4 space-y-2">
                            <h3 className="text-lg font-semibold text-slate-900 dark:text-white">Degraded Status Notifications</h3>
                            <p className="text-slate-700 dark:text-slate-300">
                                When the health status becomes <span className="text-yellow-400 font-semibold">degraded</span>, Healarr can send notifications to alert you.
                            </p>
                            <div className="bg-slate-100 dark:bg-slate-800/50 rounded-lg p-4 space-y-3">
                                <p className="text-sm text-slate-600 dark:text-slate-400">To enable:</p>
                                <ol className="text-sm text-slate-600 dark:text-slate-400 space-y-1 list-decimal list-inside">
                                    <li>Go to <span className="font-semibold text-slate-700 dark:text-slate-300">Config → Notifications</span></li>
                                    <li>Create or edit a notification configuration</li>
                                    <li>In the Events section, enable <span className="font-semibold text-yellow-400">SystemHealthDegraded</span> under System Events</li>
                                    <li>You'll receive alerts when any *arr instance goes offline</li>
                                </ol>
                                <p className="text-xs text-slate-500 mt-2">
                                    <span className="font-semibold">Note:</span> Notifications respect throttle settings - you won't be spammed if health checks run frequently.
                                </p>
                            </div>
                        </div>
                    </div>
                </Accordion>
            </motion.div>
        </div>
    );
};

export default Help;
