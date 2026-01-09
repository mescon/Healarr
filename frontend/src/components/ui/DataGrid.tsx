import React, { useState } from 'react';
import clsx from 'clsx';
import { ChevronLeft, ChevronRight, ChevronDown } from 'lucide-react';
import { motion, AnimatePresence } from 'framer-motion';

interface Column<T> {
    header: string | React.ReactNode;
    accessorKey: keyof T | ((row: T, index: number) => React.ReactNode);
    className?: string;
    stopPropagation?: boolean;
    onCellClick?: (row: T, index: number, e: React.MouseEvent) => void;
    // Mobile responsiveness options
    hideOnMobile?: boolean;      // Hide this column on mobile card view
    mobileLabel?: string;        // Label to show in mobile card view (defaults to header if string)
    isPrimary?: boolean;         // Show in card header (always visible on mobile)
}

interface DataGridProps<T> {
    data: T[];
    columns: Column<T>[];
    isLoading?: boolean;
    pagination?: {
        page: number;
        limit: number;
        total: number;
        onPageChange: (page: number) => void;
        onLimitChange?: (limit: number) => void;
    };
    onRowClick?: (row: T) => void;
    // Mobile options
    mobileBreakpoint?: 'sm' | 'md' | 'lg';  // Default: 'md' (768px)
    mobileCardTitle?: (row: T) => React.ReactNode;  // Custom title for mobile cards
}

const LIMIT_OPTIONS = [25, 50, 100, 250, 1000];

// Mobile Card Component for individual rows
const MobileCard = <T extends { id: string | number }>({
    row,
    rowIndex,
    columns,
    onRowClick,
    mobileCardTitle,
}: {
    row: T;
    rowIndex: number;
    columns: Column<T>[];
    onRowClick?: (row: T) => void;
    mobileCardTitle?: (row: T) => React.ReactNode;
}) => {
    const [isExpanded, setIsExpanded] = useState(false);

    const primaryColumns = columns.filter(col => col.isPrimary && !col.hideOnMobile);
    const secondaryColumns = columns.filter(col => !col.isPrimary && !col.hideOnMobile);

    const getColumnLabel = (col: Column<T>): string => {
        if (col.mobileLabel) return col.mobileLabel;
        if (typeof col.header === 'string') return col.header;
        return '';
    };

    const getCellValue = (col: Column<T>): React.ReactNode => {
        return typeof col.accessorKey === 'function'
            ? col.accessorKey(row, rowIndex)
            : (row[col.accessorKey] as React.ReactNode);
    };

    return (
        <div
            className={clsx(
                "border-b border-slate-200 dark:border-slate-800/50 last:border-b-0",
                onRowClick && "cursor-pointer"
            )}
        >
            {/* Card Header - always visible */}
            <div
                className="p-4 flex items-center justify-between hover:bg-slate-100 dark:hover:bg-slate-800/30 transition-colors"
                onClick={() => {
                    if (secondaryColumns.length > 0) {
                        setIsExpanded(!isExpanded);
                    } else {
                        onRowClick?.(row);
                    }
                }}
            >
                <div className="flex-1 min-w-0">
                    {/* Custom title or first primary column */}
                    {mobileCardTitle ? (
                        <div className="font-medium text-slate-900 dark:text-white truncate">
                            {mobileCardTitle(row)}
                        </div>
                    ) : primaryColumns.length > 0 ? (
                        <div className="font-medium text-slate-900 dark:text-white truncate">
                            {getCellValue(primaryColumns[0])}
                        </div>
                    ) : (
                        <div className="font-medium text-slate-900 dark:text-white truncate">
                            {getCellValue(columns[0])}
                        </div>
                    )}
                    {/* Additional primary columns as subtitle */}
                    {primaryColumns.slice(1).map((col, idx) => (
                        <div key={idx} className="text-sm text-slate-500 dark:text-slate-400 truncate mt-0.5">
                            {getCellValue(col)}
                        </div>
                    ))}
                </div>
                {secondaryColumns.length > 0 && (
                    <ChevronDown
                        className={clsx(
                            "w-5 h-5 text-slate-400 transition-transform flex-shrink-0 ml-2",
                            isExpanded && "rotate-180"
                        )}
                    />
                )}
            </div>

            {/* Expandable details */}
            <AnimatePresence initial={false}>
                {isExpanded && secondaryColumns.length > 0 && (
                    <motion.div
                        initial={{ height: 0, opacity: 0 }}
                        animate={{ height: "auto", opacity: 1 }}
                        exit={{ height: 0, opacity: 0 }}
                        transition={{ duration: 0.2, ease: "easeInOut" }}
                        className="overflow-hidden"
                    >
                        <div
                            className="px-4 pb-4 space-y-2 bg-slate-50 dark:bg-slate-800/20"
                            onClick={(e) => {
                                e.stopPropagation();
                                onRowClick?.(row);
                            }}
                        >
                            {secondaryColumns.map((col, idx) => {
                                const label = getColumnLabel(col);
                                const value = getCellValue(col);
                                return (
                                    <div
                                        key={idx}
                                        className="flex items-start justify-between gap-4"
                                        onClick={(e) => {
                                            if (col.stopPropagation) e.stopPropagation();
                                            col.onCellClick?.(row, rowIndex, e);
                                        }}
                                    >
                                        {label && (
                                            <span className="text-xs font-medium text-slate-500 dark:text-slate-400 uppercase flex-shrink-0">
                                                {label}
                                            </span>
                                        )}
                                        <span className="text-sm text-slate-700 dark:text-slate-300 text-right">
                                            {value}
                                        </span>
                                    </div>
                                );
                            })}
                        </div>
                    </motion.div>
                )}
            </AnimatePresence>
        </div>
    );
};

const DataGrid = <T extends { id: string | number }>({
    data,
    columns,
    isLoading,
    pagination,
    onRowClick,
    mobileBreakpoint = 'md',
    mobileCardTitle,
}: DataGridProps<T>) => {
    // Determine responsive class prefix based on breakpoint
    const breakpointClass = {
        sm: 'sm:',
        md: 'md:',
        lg: 'lg:',
    }[mobileBreakpoint];

    if (isLoading) {
        return (
            <div className="w-full h-64 flex items-center justify-center rounded-2xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl">
                <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-green-500"></div>
            </div>
        );
    }

    if (!data.length) {
        return (
            <div className="w-full h-64 flex items-center justify-center rounded-2xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl text-slate-500">
                No data available
            </div>
        );
    }

    return (
        <div className="rounded-2xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl overflow-hidden">
            {/* Desktop Table View - hidden on mobile */}
            <div className={clsx("hidden overflow-x-auto", `${breakpointClass}block`)}>
                <table className="w-full text-left text-sm text-slate-600 dark:text-slate-400">
                    <thead className="bg-slate-100 dark:bg-slate-800/50 text-xs uppercase text-slate-500 font-medium">
                        <tr>
                            {columns.map((col, idx) => (
                                <th key={idx} className={clsx("px-6 py-4", col.className)}>
                                    {col.header}
                                </th>
                            ))}
                        </tr>
                    </thead>
                    <tbody className="divide-y divide-slate-200 dark:divide-slate-800/50">
                        {data.map((row, rowIndex) => (
                            <tr
                                key={row.id}
                                className={clsx(
                                    "hover:bg-slate-100 dark:hover:bg-slate-800/30 transition-colors",
                                    onRowClick && "cursor-pointer active:bg-slate-200 dark:active:bg-slate-800/50"
                                )}
                                onClick={() => onRowClick?.(row)}
                            >
                                {columns.map((col, idx) => (
                                    <td
                                        key={idx}
                                        className={clsx("px-6 py-4", col.className, col.onCellClick && "cursor-pointer")}
                                        onClick={(e) => {
                                            if (col.stopPropagation) e.stopPropagation();
                                            col.onCellClick?.(row, rowIndex, e);
                                        }}
                                    >
                                        {typeof col.accessorKey === 'function'
                                            ? col.accessorKey(row, rowIndex)
                                            : (row[col.accessorKey] as React.ReactNode)}
                                    </td>
                                ))}
                            </tr>
                        ))}
                    </tbody>
                </table>
            </div>

            {/* Mobile Card View - visible only on mobile */}
            <div className={clsx("block", `${breakpointClass}hidden`)}>
                {data.map((row, rowIndex) => (
                    <MobileCard
                        key={row.id}
                        row={row}
                        rowIndex={rowIndex}
                        columns={columns}
                        onRowClick={onRowClick}
                        mobileCardTitle={mobileCardTitle}
                    />
                ))}
            </div>

            {pagination && (
                <div className="flex flex-col sm:flex-row items-center justify-between gap-3 px-4 sm:px-6 py-4 border-t border-slate-200 dark:border-slate-800/50 bg-slate-50 dark:bg-slate-900/20">
                    <div className="flex flex-col sm:flex-row items-center gap-2 sm:gap-4 w-full sm:w-auto">
                        <span className="text-xs text-slate-500 text-center sm:text-left">
                            Showing {Math.min(pagination.limit, pagination.total)} of {pagination.total}
                        </span>
                        {pagination.onLimitChange && (
                            <div className="flex items-center gap-2">
                                <span className="text-xs text-slate-500">Per page:</span>
                                <select
                                    value={pagination.limit}
                                    onChange={(e) => pagination.onLimitChange!(Number(e.target.value))}
                                    className="text-xs bg-white dark:bg-slate-800 border border-slate-200 dark:border-slate-700 rounded-lg px-2 py-1.5 text-slate-600 dark:text-slate-400 focus:outline-none focus:ring-2 focus:ring-green-500/50 cursor-pointer"
                                >
                                    {LIMIT_OPTIONS.map(opt => (
                                        <option key={opt} value={opt}>{opt}</option>
                                    ))}
                                </select>
                            </div>
                        )}
                    </div>
                    <div className="flex gap-2">
                        <button
                            disabled={pagination.page === 1}
                            onClick={() => pagination.onPageChange(pagination.page - 1)}
                            className="p-2 rounded-lg hover:bg-slate-200 dark:hover:bg-slate-800 disabled:opacity-50 disabled:cursor-not-allowed transition-colors cursor-pointer"
                        >
                            <ChevronLeft className="w-4 h-4" />
                        </button>
                        <button
                            disabled={pagination.page * pagination.limit >= pagination.total}
                            onClick={() => pagination.onPageChange(pagination.page + 1)}
                            className="p-2 rounded-lg hover:bg-slate-200 dark:hover:bg-slate-800 disabled:opacity-50 disabled:cursor-not-allowed transition-colors cursor-pointer"
                        >
                            <ChevronRight className="w-4 h-4" />
                        </button>
                    </div>
                </div>
            )}
        </div>
    );
};

export default DataGrid;
