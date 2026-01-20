import { describe, it, expect } from 'vitest';
import { humanizeError, isNetworkError, isAuthError } from './errors';

describe('humanizeError', () => {
  describe('network errors', () => {
    it('humanizes ETIMEDOUT', () => {
      expect(humanizeError('ETIMEDOUT')).toBe(
        'Connection timed out. Check if the service is running and reachable.'
      );
    });

    it('humanizes ECONNREFUSED', () => {
      expect(humanizeError('ECONNREFUSED')).toBe(
        'Connection refused. Verify the URL and check firewall settings.'
      );
    });

    it('humanizes ENOTFOUND', () => {
      expect(humanizeError('ENOTFOUND')).toBe(
        'Server not found. Check the URL is correct.'
      );
    });

    it('humanizes ECONNRESET', () => {
      expect(humanizeError('ECONNRESET')).toBe(
        'Connection was reset. The server may have restarted or dropped the connection.'
      );
    });
  });

  describe('SSL errors', () => {
    it('humanizes certificate errors', () => {
      expect(humanizeError('SSL certificate error')).toBe(
        'SSL certificate error. Try using http:// instead of https://, or check the certificate is valid.'
      );
    });

    it('humanizes TLS errors', () => {
      expect(humanizeError('TLS handshake failed')).toBe(
        'SSL certificate error. Try using http:// instead of https://, or check the certificate is valid.'
      );
    });
  });

  describe('HTTP status codes', () => {
    it('humanizes 401 errors', () => {
      expect(humanizeError('Error 401')).toBe(
        'Authentication failed. Check your API key is correct.'
      );
      expect(humanizeError('Unauthorized')).toBe(
        'Authentication failed. Check your API key is correct.'
      );
    });

    it('humanizes 403 errors', () => {
      expect(humanizeError('Error 403')).toBe(
        'Access denied. Verify API key permissions in your *arr app.'
      );
      expect(humanizeError('Forbidden')).toBe(
        'Access denied. Verify API key permissions in your *arr app.'
      );
    });

    it('humanizes 404 errors', () => {
      expect(humanizeError('Error 404')).toBe(
        'Not found. Check the URL path is correct.'
      );
      expect(humanizeError('Not Found')).toBe(
        'Not found. Check the URL path is correct.'
      );
    });

    it('humanizes 429 errors', () => {
      expect(humanizeError('Error 429')).toBe(
        'Too many requests. Please wait a moment before trying again.'
      );
    });

    it('humanizes 500 errors', () => {
      expect(humanizeError('Error 500')).toBe(
        'Server error. The *arr application encountered a problem.'
      );
      expect(humanizeError('Internal Server Error')).toBe(
        'Server error. The *arr application encountered a problem.'
      );
    });

    it('humanizes 502 errors', () => {
      expect(humanizeError('Error 502')).toBe(
        'Bad gateway. Check if the *arr service is running.'
      );
    });

    it('humanizes 503 errors', () => {
      expect(humanizeError('Error 503')).toBe(
        'Service unavailable. The server may be overloaded or down for maintenance.'
      );
    });
  });

  describe('arr-specific errors', () => {
    it('humanizes invalid API key errors', () => {
      expect(humanizeError('api key invalid')).toBe(
        'Invalid API key. Copy the correct key from Settings → General in your *arr app.'
      );
    });

    it('humanizes root folder errors', () => {
      expect(humanizeError('root folder not found')).toBe(
        'Root folder not found. Check the path exists and is accessible.'
      );
    });
  });

  describe('file system errors', () => {
    it('humanizes ENOENT', () => {
      expect(humanizeError('ENOENT')).toBe(
        'File or directory not found. Check the path exists.'
      );
    });

    it('humanizes EACCES', () => {
      expect(humanizeError('EACCES')).toBe(
        'Permission denied. Check file/folder permissions.'
      );
    });

    it('humanizes ENOSPC', () => {
      expect(humanizeError('ENOSPC')).toBe(
        'No space left on device. Free up disk space.'
      );
    });
  });

  describe('database errors', () => {
    it('humanizes database locked errors', () => {
      expect(humanizeError('database locked')).toBe(
        'Database is locked. Another process may be using it. Try again in a moment.'
      );
    });

    it('humanizes sqlite busy errors', () => {
      expect(humanizeError('sqlite busy')).toBe(
        'Database busy. Please wait and try again.'
      );
    });
  });

  describe('fallback behavior', () => {
    it('returns cleaned message for unknown errors', () => {
      expect(humanizeError('Error: Something bad happened')).toBe(
        'Something bad happened'
      );
    });

    it('removes Failed to prefix', () => {
      expect(humanizeError('Failed to connect')).toBe('Connect');
    });

    it('handles Error objects', () => {
      const error = new Error('Test error message');
      expect(humanizeError(error)).toBe('Test error message');
    });

    it('handles objects with message property', () => {
      expect(humanizeError({ message: 'Object error' })).toBe('Object error');
    });

    it('returns generic message for non-string/error inputs', () => {
      expect(humanizeError(null)).toBe('An unexpected error occurred. Please try again.');
      expect(humanizeError(123)).toBe('An unexpected error occurred. Please try again.');
    });

    it('capitalizes first letter', () => {
      expect(humanizeError('lowercase message')).toBe('Lowercase message');
    });
  });
});

describe('isNetworkError', () => {
  it('returns true for ETIMEDOUT', () => {
    expect(isNetworkError('ETIMEDOUT')).toBe(true);
  });

  it('returns true for ECONNREFUSED', () => {
    expect(isNetworkError('ECONNREFUSED')).toBe(true);
  });

  it('returns true for ENOTFOUND', () => {
    expect(isNetworkError('ENOTFOUND')).toBe(true);
  });

  it('returns true for ECONNRESET', () => {
    expect(isNetworkError('ECONNRESET')).toBe(true);
  });

  it('returns true for network-related messages', () => {
    expect(isNetworkError('Network error occurred')).toBe(true);
    expect(isNetworkError('Request timeout')).toBe(true);
  });

  it('returns false for non-network errors', () => {
    expect(isNetworkError('Invalid input')).toBe(false);
    expect(isNetworkError('Permission denied')).toBe(false);
  });

  it('handles Error objects', () => {
    expect(isNetworkError(new Error('ECONNREFUSED'))).toBe(true);
    expect(isNetworkError(new Error('Permission denied'))).toBe(false);
  });
});

describe('isAuthError', () => {
  it('returns true for 401 status', () => {
    expect(isAuthError('401 Unauthorized')).toBe(true);
    expect(isAuthError('Error 401')).toBe(true);
  });

  it('returns true for 403 status', () => {
    expect(isAuthError('403 Forbidden')).toBe(true);
    expect(isAuthError('Error 403')).toBe(true);
  });

  it('returns true for unauthorized messages', () => {
    expect(isAuthError('Unauthorized access')).toBe(true);
  });

  it('returns true for forbidden messages', () => {
    expect(isAuthError('Forbidden')).toBe(true);
  });

  it('returns true for API key errors', () => {
    expect(isAuthError('Invalid api key')).toBe(true);
  });

  it('returns false for non-auth errors', () => {
    expect(isAuthError('Connection refused')).toBe(false);
    expect(isAuthError('Not found')).toBe(false);
  });

  it('handles Error objects', () => {
    expect(isAuthError(new Error('401'))).toBe(true);
    expect(isAuthError(new Error('Connection refused'))).toBe(false);
  });
});
