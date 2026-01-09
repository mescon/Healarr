/**
 * Error humanization utility
 *
 * Translates technical error codes and messages into user-friendly explanations
 * with actionable guidance.
 */

/**
 * Map of technical error patterns to human-readable messages
 */
const errorPatterns: Array<{ pattern: RegExp | string; message: string }> = [
    // Network errors
    { pattern: 'ETIMEDOUT', message: 'Connection timed out. Check if the service is running and reachable.' },
    { pattern: 'ECONNREFUSED', message: 'Connection refused. Verify the URL and check firewall settings.' },
    { pattern: 'ENOTFOUND', message: 'Server not found. Check the URL is correct.' },
    { pattern: 'ECONNRESET', message: 'Connection was reset. The server may have restarted or dropped the connection.' },
    { pattern: 'EHOSTUNREACH', message: 'Host unreachable. Check network connectivity and firewall rules.' },
    { pattern: 'ENETUNREACH', message: 'Network unreachable. Check your network connection.' },

    // SSL/TLS errors
    { pattern: /certificate|ssl|tls/i, message: 'SSL certificate error. Try using http:// instead of https://, or check the certificate is valid.' },
    { pattern: 'CERT_HAS_EXPIRED', message: 'SSL certificate has expired. Contact the server administrator.' },
    { pattern: 'UNABLE_TO_VERIFY_LEAF_SIGNATURE', message: 'Cannot verify SSL certificate. The certificate may be self-signed.' },

    // HTTP status codes
    { pattern: /\b400\b/, message: 'Bad request. Check the data you\'re sending is correct.' },
    { pattern: /\b401\b|unauthorized/i, message: 'Authentication failed. Check your API key is correct.' },
    { pattern: /\b403\b|forbidden/i, message: 'Access denied. Verify API key permissions in your *arr app.' },
    { pattern: /\b404\b|not found/i, message: 'Not found. Check the URL path is correct.' },
    { pattern: /\b408\b/, message: 'Request timed out. The server took too long to respond.' },
    { pattern: /\b429\b|too many requests/i, message: 'Too many requests. Please wait a moment before trying again.' },
    { pattern: /\b500\b|internal server error/i, message: 'Server error. The *arr application encountered a problem.' },
    { pattern: /\b502\b|bad gateway/i, message: 'Bad gateway. Check if the *arr service is running.' },
    { pattern: /\b503\b|service unavailable/i, message: 'Service unavailable. The server may be overloaded or down for maintenance.' },
    { pattern: /\b504\b|gateway timeout/i, message: 'Gateway timeout. The server took too long to respond.' },

    // *arr specific errors
    { pattern: /api.*key.*invalid/i, message: 'Invalid API key. Copy the correct key from Settings → General in your *arr app.' },
    { pattern: /root.*folder.*not.*found/i, message: 'Root folder not found. Check the path exists and is accessible.' },
    { pattern: /quality.*profile/i, message: 'Quality profile error. Verify the profile exists in your *arr settings.' },

    // File system errors
    { pattern: 'ENOENT', message: 'File or directory not found. Check the path exists.' },
    { pattern: 'EACCES', message: 'Permission denied. Check file/folder permissions.' },
    { pattern: 'EPERM', message: 'Operation not permitted. You may need elevated privileges.' },
    { pattern: 'ENOSPC', message: 'No space left on device. Free up disk space.' },
    { pattern: 'EROFS', message: 'Read-only file system. The disk may be mounted read-only.' },

    // Database errors
    { pattern: /database.*locked/i, message: 'Database is locked. Another process may be using it. Try again in a moment.' },
    { pattern: /sqlite.*busy/i, message: 'Database busy. Please wait and try again.' },

    // Generic patterns
    { pattern: /timeout/i, message: 'Operation timed out. The server may be slow or unresponsive.' },
    { pattern: /network.*error/i, message: 'Network error. Check your internet connection.' },
];

/**
 * Humanize a technical error message into a user-friendly explanation
 *
 * @param error - The error message or Error object to humanize
 * @returns A human-readable error message
 */
export function humanizeError(error: string | Error | unknown): string {
    // Extract message from Error objects
    let message: string;
    if (error instanceof Error) {
        message = error.message;
    } else if (typeof error === 'string') {
        message = error;
    } else if (error && typeof error === 'object' && 'message' in error) {
        message = String((error as { message: unknown }).message);
    } else {
        return 'An unexpected error occurred. Please try again.';
    }

    // Check each pattern for a match
    for (const { pattern, message: humanMessage } of errorPatterns) {
        if (typeof pattern === 'string') {
            if (message.includes(pattern)) {
                return humanMessage;
            }
        } else if (pattern.test(message)) {
            return humanMessage;
        }
    }

    // If no pattern matches, return the original message (cleaned up)
    // Remove common prefixes and technical jargon
    let cleaned = message
        .replace(/^Error:\s*/i, '')
        .replace(/^Failed to\s*/i, '')
        .replace(/\s*\(.*\)\s*$/, '') // Remove trailing (error codes)
        .trim();

    // Capitalize first letter
    if (cleaned.length > 0) {
        cleaned = cleaned.charAt(0).toUpperCase() + cleaned.slice(1);
    }

    return cleaned || 'An unexpected error occurred. Please try again.';
}

/**
 * Check if an error is a network-related error
 */
export function isNetworkError(error: string | Error | unknown): boolean {
    const message = error instanceof Error ? error.message : String(error);
    return /ETIMEDOUT|ECONNREFUSED|ENOTFOUND|ECONNRESET|network|timeout/i.test(message);
}

/**
 * Check if an error is an authentication error
 */
export function isAuthError(error: string | Error | unknown): boolean {
    const message = error instanceof Error ? error.message : String(error);
    return /401|403|unauthorized|forbidden|api.*key/i.test(message);
}
