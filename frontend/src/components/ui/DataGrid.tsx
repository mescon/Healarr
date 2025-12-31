import React from 'react';
import clsx from 'clsx';
import { ChevronLeft, ChevronRight } from 'lucide-react';

interface Column<T> {
    header: string | React.ReactNode;
    accessorKey: keyof T | ((row: T, index: number) => React.ReactNode);
    className?: string;
    stopPropagation?: boolean;
    onCellClick?: (row: T, index: number, e: React.MouseEvent) => void;
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
    };
    onRowClick?: (row: T) => void;
}

const DataGrid = <T extends { id: string | number }>({ data, columns, isLoading, pagination, onRowClick }: DataGridProps<T>) => {
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
            <div className="overflow-x-auto">
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

            {pagination && (
                <div className="flex items-center justify-between px-6 py-4 border-t border-slate-200 dark:border-slate-800/50 bg-slate-50 dark:bg-slate-900/20">
                    <span className="text-xs text-slate-500">
                        Showing {Math.min(pagination.limit, pagination.total)} of {pagination.total} results
                    </span>
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
