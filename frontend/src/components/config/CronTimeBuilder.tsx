import { useState, useEffect } from 'react';
import clsx from 'clsx';

interface CronTimeBuilderProps {
    value: string;
    onChange: (cronExpression: string) => void;
}

const CronTimeBuilder = ({ value, onChange }: CronTimeBuilderProps) => {
    const [useVisual, setUseVisual] = useState(true);
    const [hour, setHour] = useState(3);
    const [minute, setMinute] = useState(0);
    const [selectedDays, setSelectedDays] = useState<number[]>([]);

    const weekdays = [
        { value: 0, label: 'Sun' },
        { value: 1, label: 'Mon' },
        { value: 2, label: 'Tue' },
        { value: 3, label: 'Wed' },
        { value: 4, label: 'Thu' },
        { value: 5, label: 'Fri' },
        { value: 6, label: 'Sat' },
    ];

    // Parse existing cron value when switching to visual mode
    useEffect(() => {
        if (value && useVisual) {
            const parts = value.split(' ');
            if (parts.length >= 5) {
                const [min, hr, , , dow] = parts;
                // Only parse simple numeric values
                if (/^\d+$/.test(min)) setMinute(parseInt(min, 10));
                if (/^\d+$/.test(hr)) setHour(parseInt(hr, 10));
                if (dow !== '*') {
                    const days = dow.split(',').map(d => parseInt(d, 10)).filter(d => !isNaN(d));
                    setSelectedDays(days);
                } else {
                    setSelectedDays([]);
                }
            }
        }
    }, [value, useVisual]);

    // Build cron expression from visual selections
    useEffect(() => {
        if (useVisual) {
            const dowPart = selectedDays.length === 0 || selectedDays.length === 7
                ? '*'
                : selectedDays.sort((a, b) => a - b).join(',');
            const expression = `${minute} ${hour} * * ${dowPart}`;
            if (expression !== value) {
                onChange(expression);
            }
        }
    }, [hour, minute, selectedDays, useVisual, onChange, value]);

    const toggleDay = (day: number) => {
        setSelectedDays(prev =>
            prev.includes(day)
                ? prev.filter(d => d !== day)
                : [...prev, day]
        );
    };

    // Generate human-readable schedule description
    const getScheduleDescription = () => {
        const timeStr = `${hour.toString().padStart(2, '0')}:${minute.toString().padStart(2, '0')}`;
        if (selectedDays.length === 0 || selectedDays.length === 7) {
            return `Every day at ${timeStr}`;
        }
        const dayNames = selectedDays
            .sort((a, b) => a - b)
            .map(d => weekdays.find(w => w.value === d)?.label)
            .join(', ');
        return `Every ${dayNames} at ${timeStr}`;
    };

    return (
        <div className="space-y-4">
            <div className="flex items-center gap-2 mb-2">
                <button
                    type="button"
                    onClick={() => setUseVisual(true)}
                    className={clsx(
                        "px-3 py-1.5 text-xs font-medium rounded-lg transition-colors",
                        useVisual
                            ? "bg-purple-500/20 text-purple-400 border border-purple-500/30"
                            : "text-slate-500 hover:text-slate-400"
                    )}
                >
                    Visual
                </button>
                <button
                    type="button"
                    onClick={() => setUseVisual(false)}
                    className={clsx(
                        "px-3 py-1.5 text-xs font-medium rounded-lg transition-colors",
                        !useVisual
                            ? "bg-purple-500/20 text-purple-400 border border-purple-500/30"
                            : "text-slate-500 hover:text-slate-400"
                    )}
                >
                    Advanced
                </button>
            </div>

            {useVisual ? (
                <div className="space-y-4">
                    {/* Time Selection */}
                    <div className="flex items-center gap-4">
                        <div className="flex-1">
                            <label className="block text-xs font-medium text-slate-600 dark:text-slate-400 mb-1">Hour</label>
                            <select
                                value={hour}
                                onChange={e => setHour(parseInt(e.target.value, 10))}
                                className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white focus:ring-2 focus:ring-purple-500 text-sm"
                            >
                                {Array.from({ length: 24 }, (_, i) => (
                                    <option key={i} value={i}>{i.toString().padStart(2, '0')}:00</option>
                                ))}
                            </select>
                        </div>
                        <div className="flex-1">
                            <label className="block text-xs font-medium text-slate-600 dark:text-slate-400 mb-1">Minute</label>
                            <select
                                value={minute}
                                onChange={e => setMinute(parseInt(e.target.value, 10))}
                                className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white focus:ring-2 focus:ring-purple-500 text-sm"
                            >
                                {[0, 15, 30, 45].map(m => (
                                    <option key={m} value={m}>:{m.toString().padStart(2, '0')}</option>
                                ))}
                            </select>
                        </div>
                    </div>

                    {/* Day Selection */}
                    <div>
                        <label className="block text-xs font-medium text-slate-600 dark:text-slate-400 mb-2">
                            Days (leave empty for every day)
                        </label>
                        <div className="flex flex-wrap gap-2">
                            {weekdays.map(day => (
                                <button
                                    key={day.value}
                                    type="button"
                                    onClick={() => toggleDay(day.value)}
                                    className={clsx(
                                        "px-3 py-1.5 text-xs font-medium rounded-lg transition-colors border",
                                        selectedDays.includes(day.value)
                                            ? "bg-purple-500/20 text-purple-400 border-purple-500/30"
                                            : "text-slate-500 hover:text-slate-400 border-slate-600 hover:border-slate-500"
                                    )}
                                >
                                    {day.label}
                                </button>
                            ))}
                        </div>
                    </div>

                    {/* Schedule Preview */}
                    <div className="p-3 rounded-lg bg-slate-100 dark:bg-slate-800/50 border border-slate-200 dark:border-slate-700/50">
                        <p className="text-xs text-slate-500 dark:text-slate-400 mb-1">Schedule Preview</p>
                        <p className="text-sm font-medium text-slate-900 dark:text-white">{getScheduleDescription()}</p>
                        <p className="text-xs text-slate-500 font-mono mt-1">Cron: {value}</p>
                    </div>
                </div>
            ) : (
                <div>
                    <label className="block text-sm font-medium text-slate-700 dark:text-slate-300 mb-2">Cron Expression</label>
                    <input
                        type="text"
                        value={value}
                        onChange={e => onChange(e.target.value)}
                        placeholder="0 3 * * *"
                        className="w-full px-3 py-2 bg-white dark:bg-slate-900 border border-slate-300 dark:border-slate-700 rounded-lg text-slate-900 dark:text-white placeholder-slate-500 focus:ring-2 focus:ring-purple-500"
                        required
                    />
                    <p className="mt-1 text-xs text-slate-500">
                        Format: Minute Hour Day Month Weekday
                    </p>
                    <p className="mt-2 text-xs text-slate-600 dark:text-slate-400">
                        Need help? Use <a href="https://crontab.guru" target="_blank" rel="noopener noreferrer" className="text-purple-400 hover:text-purple-300 underline">crontab.guru</a> to generate an expression.
                    </p>
                </div>
            )}
        </div>
    );
};

export default CronTimeBuilder;
