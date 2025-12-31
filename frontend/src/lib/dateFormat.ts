import { format as dateFnsFormat } from 'date-fns';

// Available date format presets
export type DateFormatPreset = 'time-first' | 'date-first' | 'iso';

export interface DateFormatConfig {
  // Time format (HH:mm:ss)
  time: string;
  // Date format (varies by preset)
  date: string;
  // Full format for tooltips
  full: string;
  // Compact format for tables
  compact: string;
}

const DATE_FORMATS: Record<DateFormatPreset, DateFormatConfig> = {
  'time-first': {
    time: 'HH:mm:ss',
    date: 'MMM d, yyyy',
    full: 'HH:mm:ss • MMM d, yyyy',
    compact: 'HH:mm:ss',
  },
  'date-first': {
    time: 'HH:mm:ss',
    date: 'MMM d, yyyy',
    full: 'MMM d, yyyy • HH:mm:ss',
    compact: 'MMM d HH:mm',
  },
  'iso': {
    time: 'HH:mm:ss',
    date: 'yyyy-MM-dd',
    full: 'yyyy-MM-dd HH:mm:ss',
    compact: 'yyyy-MM-dd HH:mm',
  },
};

// Storage key for localStorage
const STORAGE_KEY = 'healarr_date_format';

// Default from environment variable or fallback
const getDefaultFormat = (): DateFormatPreset => {
  // Check for environment variable (injected at build time or runtime)
  const envFormat = (window as unknown as { HEALARR_DATE_FORMAT?: string }).HEALARR_DATE_FORMAT;
  if (envFormat && isValidPreset(envFormat)) {
    return envFormat as DateFormatPreset;
  }
  return 'time-first';
};

const isValidPreset = (value: string): value is DateFormatPreset => {
  return ['time-first', 'date-first', 'iso'].includes(value);
};

// Get current format preference
export const getDateFormatPreset = (): DateFormatPreset => {
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored && isValidPreset(stored)) {
    return stored;
  }
  return getDefaultFormat();
};

// Set format preference
export const setDateFormatPreset = (preset: DateFormatPreset): void => {
  localStorage.setItem(STORAGE_KEY, preset);
  // Dispatch custom event for reactivity
  window.dispatchEvent(new CustomEvent('dateFormatChange', { detail: preset }));
};

// Get the format config for current preset
export const getDateFormatConfig = (): DateFormatConfig => {
  return DATE_FORMATS[getDateFormatPreset()];
};

// Format helpers
export const formatDateTime = (date: string | Date | null | undefined, type: 'time' | 'date' | 'full' | 'compact' = 'full'): string => {
  if (!date) return '-';
  const config = getDateFormatConfig();
  const dateObj = typeof date === 'string' ? new Date(date) : date;
  // Check for invalid date
  if (isNaN(dateObj.getTime())) return '-';
  const formatStr = config[type] || config.full;
  return formatStr ? dateFnsFormat(dateObj, formatStr) : '';
};

// Format just the time portion (always HH:mm:ss)
export const formatTime = (date: string | Date | null | undefined): string => formatDateTime(date, 'time');

// Format just the date portion (respects preset style - ISO vs readable)
export const formatDate = (date: string | Date | null | undefined): string => formatDateTime(date, 'date');

// Full datetime format for tooltips
export const formatDateTimeFull = (date: string | Date | null | undefined): string => formatDateTime(date, 'full');

// Compact format for limited space
export const formatDateTimeCompact = (date: string | Date | null | undefined): string => formatDateTime(date, 'compact');

// Legacy aliases for backwards compatibility
export const formatDateTimePrimary = formatTime;
export const formatDateTimeSecondary = formatDate;

// Export preset labels for UI
export const DATE_FORMAT_LABELS: Record<DateFormatPreset, string> = {
  'time-first': 'Time First (HH:mm:ss yyyy-MM-dd)',
  'date-first': 'Date First (MMM d, yyyy HH:mm:ss)',
  'iso': 'ISO (yyyy-MM-dd HH:mm:ss)',
};

export const DATE_FORMAT_PRESETS = Object.keys(DATE_FORMATS) as DateFormatPreset[];
