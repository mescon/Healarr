import { useState } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import { ChevronDown } from 'lucide-react';
import clsx from 'clsx';

interface CollapsibleSectionProps {
    id: string;
    icon: React.ElementType;
    iconColor: string;
    title: string;
    subtitle?: string;
    defaultExpanded?: boolean;
    children: React.ReactNode;
    delay?: number;
}

const CollapsibleSection = ({
    id,
    icon: Icon,
    iconColor,
    title,
    subtitle,
    defaultExpanded = true,
    children,
    delay = 0.1
}: CollapsibleSectionProps) => {
    const storageKey = `config-section-${id}`;

    // Initialize from localStorage or use default
    const [isExpanded, setIsExpanded] = useState(() => {
        const stored = localStorage.getItem(storageKey);
        if (stored !== null) {
            return stored === 'true';
        }
        return defaultExpanded;
    });

    // Persist state changes to localStorage
    const toggleExpanded = () => {
        const newValue = !isExpanded;
        setIsExpanded(newValue);
        localStorage.setItem(storageKey, String(newValue));
    };

    return (
        <motion.div
            initial={{ opacity: 0, y: 20 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ delay }}
            className="space-y-4"
        >
            <button
                onClick={toggleExpanded}
                className="w-full flex items-center justify-between group cursor-pointer"
            >
                <div className="flex items-center gap-3">
                    <Icon className={clsx("w-6 h-6", iconColor)} />
                    <div className="text-left">
                        <h2 className="text-2xl font-semibold text-slate-900 dark:text-white">{title}</h2>
                        {subtitle && <p className="text-sm text-slate-600 dark:text-slate-400">{subtitle}</p>}
                    </div>
                </div>
                <ChevronDown className={clsx(
                    "w-5 h-5 text-slate-600 dark:text-slate-400 transition-transform duration-200",
                    isExpanded && "rotate-180"
                )} />
            </button>

            <AnimatePresence initial={false}>
                {isExpanded && (
                    <motion.div
                        initial={{ height: 0, opacity: 0 }}
                        animate={{ height: "auto", opacity: 1 }}
                        exit={{ height: 0, opacity: 0 }}
                        transition={{ duration: 0.2, ease: "easeInOut" }}
                        className="overflow-hidden"
                    >
                        {children}
                    </motion.div>
                )}
            </AnimatePresence>
        </motion.div>
    );
};

export default CollapsibleSection;
