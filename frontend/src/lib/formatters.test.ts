import { describe, it, expect } from 'vitest';
import {
  formatCorruptionType,
  formatCorruptionState,
  formatFileSize,
  formatBytes,
  formatDuration,
  formatQuality,
  formatCronExpression,
  formatDistanceToNow,
} from './formatters';

describe('formatCorruptionType', () => {
  it('maps known types to friendly labels', () => {
    expect(formatCorruptionType('CorruptHeader')).toBe('Corrupt Header');
    expect(formatCorruptionType('TruncatedFile')).toBe('Incomplete File');
    expect(formatCorruptionType('EmptyFile')).toBe('Empty File');
    expect(formatCorruptionType('StreamError')).toBe('Playback Error');
    expect(formatCorruptionType('CodecError')).toBe('Format Error');
    expect(formatCorruptionType('InvalidFormat')).toBe('Invalid Format');
    expect(formatCorruptionType('BitrateError')).toBe('Bitrate Error');
    expect(formatCorruptionType('Unknown')).toBe('Unknown Issue');
  });

  it('formats unknown types with space separation', () => {
    expect(formatCorruptionType('SomeNewError')).toBe('Some New Error');
    expect(formatCorruptionType('MultiWordErrorType')).toBe('Multi Word Error Type');
  });
});

describe('formatCorruptionState', () => {
  it('returns correct label and color for resolved states', () => {
    const result = formatCorruptionState('VerificationSuccess');
    expect(result.label).toBe('Resolved');
    expect(result.colorClass).toContain('emerald');
  });

  it('returns correct label and color for max retries', () => {
    const result = formatCorruptionState('MaxRetriesReached');
    expect(result.label).toBe('Failed - Needs Review');
    expect(result.colorClass).toContain('red');
  });

  it('returns correct label and color for failed states', () => {
    const result = formatCorruptionState('DeletionFailed');
    expect(result.label).toBe('Deletion Failed');
    expect(result.colorClass).toContain('orange');
  });

  it('returns correct label and color for pending states', () => {
    const result = formatCorruptionState('CorruptionDetected');
    expect(result.label).toBe('Pending');
    expect(result.colorClass).toContain('amber');
  });

  it('returns correct label and color for in-progress states', () => {
    const queued = formatCorruptionState('RemediationQueued');
    expect(queued.label).toBe('Queued');
    expect(queued.colorClass).toContain('blue');

    const deleting = formatCorruptionState('DeletionStarted');
    expect(deleting.label).toBe('Deleting');
    expect(deleting.colorClass).toContain('blue');
  });

  it('returns correct label for ignored state', () => {
    const result = formatCorruptionState('CorruptionIgnored');
    expect(result.label).toBe('Ignored');
    expect(result.colorClass).toContain('slate');
  });
});

describe('formatFileSize', () => {
  it('formats zero bytes', () => {
    expect(formatFileSize(0)).toBe('0 Bytes');
  });

  it('formats bytes', () => {
    expect(formatFileSize(500)).toBe('500 Bytes');
  });

  it('formats kilobytes', () => {
    expect(formatFileSize(1024)).toBe('1 KB');
    expect(formatFileSize(1536)).toBe('1.5 KB');
  });

  it('formats megabytes', () => {
    expect(formatFileSize(1048576)).toBe('1 MB');
    expect(formatFileSize(1572864)).toBe('1.5 MB');
  });

  it('formats gigabytes', () => {
    expect(formatFileSize(1073741824)).toBe('1 GB');
    expect(formatFileSize(4.5 * 1073741824)).toBe('4.5 GB');
  });

  it('formats terabytes', () => {
    expect(formatFileSize(1099511627776)).toBe('1 TB');
  });
});

describe('formatBytes', () => {
  it('formats zero bytes', () => {
    expect(formatBytes(0)).toBe('0 B');
  });

  it('formats with compact units', () => {
    expect(formatBytes(500)).toBe('500 B');
    expect(formatBytes(1024)).toBe('1 KB');
    expect(formatBytes(1048576)).toBe('1 MB');
    expect(formatBytes(1073741824)).toBe('1 GB');
    expect(formatBytes(1099511627776)).toBe('1 TB');
  });

  it('formats with one decimal place', () => {
    expect(formatBytes(1500)).toBe('1.5 KB');
    expect(formatBytes(4500000000)).toBe('4.2 GB');
  });
});

describe('formatDuration', () => {
  it('formats zero and negative seconds', () => {
    expect(formatDuration(0)).toBe('0s');
    expect(formatDuration(-5)).toBe('0s');
  });

  it('formats seconds', () => {
    expect(formatDuration(45)).toBe('45s');
    expect(formatDuration(59)).toBe('59s');
  });

  it('formats minutes', () => {
    expect(formatDuration(60)).toBe('1m');
    expect(formatDuration(120)).toBe('2m');
    expect(formatDuration(90)).toBe('1m'); // Shows only minutes when under an hour
  });

  it('formats hours and minutes', () => {
    expect(formatDuration(3600)).toBe('1h');
    expect(formatDuration(5400)).toBe('1h 30m');
    expect(formatDuration(7200)).toBe('2h');
  });

  it('formats days and hours', () => {
    expect(formatDuration(86400)).toBe('1d');
    expect(formatDuration(100800)).toBe('1d 4h');
    expect(formatDuration(172800)).toBe('2d');
  });
});

describe('formatQuality', () => {
  it('returns unknown for empty input', () => {
    const result = formatQuality('');
    expect(result.label).toBe('Unknown');
    expect(result.tier).toBe('unknown');
  });

  it('identifies UHD quality', () => {
    expect(formatQuality('Bluray-2160p').tier).toBe('uhd');
    expect(formatQuality('WEBDL-4K').tier).toBe('uhd');
  });

  it('identifies FHD quality', () => {
    expect(formatQuality('Bluray-1080p').tier).toBe('fhd');
    expect(formatQuality('WEBDL-1080i').tier).toBe('fhd');
  });

  it('identifies HD quality', () => {
    expect(formatQuality('HDTV-720p').tier).toBe('hd');
  });

  it('identifies SD quality', () => {
    expect(formatQuality('DVD-480p').tier).toBe('sd');
    expect(formatQuality('SDTV').tier).toBe('sd');
  });

  it('formats labels nicely', () => {
    expect(formatQuality('Bluray-1080p').label).toBe('1080p Bluray');
    expect(formatQuality('WEBDL-720p').label).toBe('720p WEB-DL');
    expect(formatQuality('HDTV-1080p').label).toBe('1080p HDTV');
  });
});

describe('formatCronExpression', () => {
  it('returns invalid for empty input', () => {
    expect(formatCronExpression('')).toBe('Invalid schedule');
  });

  it('returns raw for incomplete cron', () => {
    expect(formatCronExpression('0 3 *')).toBe('0 3 *');
  });

  it('formats every minute', () => {
    expect(formatCronExpression('* * * * *')).toBe('Every minute');
  });

  it('formats every X minutes', () => {
    expect(formatCronExpression('*/15 * * * *')).toBe('Every 15 minutes');
    expect(formatCronExpression('*/5 * * * *')).toBe('Every 5 minutes');
  });

  it('formats every hour at specific minute', () => {
    expect(formatCronExpression('30 * * * *')).toBe('Every hour at :30');
    expect(formatCronExpression('0 * * * *')).toBe('Every hour at :00');
  });

  it('formats daily at specific time', () => {
    expect(formatCronExpression('0 3 * * *')).toBe('Every day at 03:00');
    expect(formatCronExpression('30 14 * * *')).toBe('Every day at 14:30');
  });

  it('formats weekly schedules', () => {
    expect(formatCronExpression('0 3 * * 0')).toBe('Every Sun at 03:00');
    expect(formatCronExpression('0 3 * * 1')).toBe('Every Mon at 03:00');
  });

  it('formats monthly schedules', () => {
    expect(formatCronExpression('0 3 15 * *')).toBe('Monthly on day 15 at 03:00');
    expect(formatCronExpression('0 3 1 * *')).toBe('Monthly on day 1 at 03:00');
  });
});

describe('formatDistanceToNow', () => {
  it('returns never for null/undefined', () => {
    expect(formatDistanceToNow(null)).toBe('never');
    expect(formatDistanceToNow(undefined)).toBe('never');
  });

  it('returns invalid date for invalid input', () => {
    expect(formatDistanceToNow('not-a-date')).toBe('invalid date');
  });

  it('formats just now', () => {
    const now = new Date();
    expect(formatDistanceToNow(now)).toBe('just now');
  });

  it('formats seconds ago', () => {
    const date = new Date(Date.now() - 30 * 1000);
    expect(formatDistanceToNow(date)).toBe('30s ago');
  });

  it('formats minutes ago', () => {
    const date = new Date(Date.now() - 5 * 60 * 1000);
    expect(formatDistanceToNow(date)).toBe('5m ago');
  });

  it('formats hours ago', () => {
    const date = new Date(Date.now() - 3 * 60 * 60 * 1000);
    expect(formatDistanceToNow(date)).toBe('3h ago');
  });

  it('formats yesterday', () => {
    const date = new Date(Date.now() - 25 * 60 * 60 * 1000);
    expect(formatDistanceToNow(date)).toBe('yesterday');
  });

  it('formats days ago', () => {
    const date = new Date(Date.now() - 4 * 24 * 60 * 60 * 1000);
    expect(formatDistanceToNow(date)).toBe('4 days ago');
  });

  it('formats weeks ago', () => {
    const date = new Date(Date.now() - 14 * 24 * 60 * 60 * 1000);
    expect(formatDistanceToNow(date)).toBe('2 weeks ago');
  });

  it('formats months ago', () => {
    const date = new Date(Date.now() - 60 * 24 * 60 * 60 * 1000);
    expect(formatDistanceToNow(date)).toBe('2 months ago');
  });

  it('formats years ago', () => {
    const date = new Date(Date.now() - 400 * 24 * 60 * 60 * 1000);
    expect(formatDistanceToNow(date)).toBe('1 years ago');
  });
});
