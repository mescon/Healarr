/**
 * Base path configuration for reverse proxy support.
 * The base path is injected by the server into index.html as window.__HEALARR_BASE_PATH__
 * Falls back to URL detection if not injected.
 */

// Extend Window interface for TypeScript
declare global {
  interface Window {
    __HEALARR_BASE_PATH__?: string;
  }
}

// Cache the base path once detected
let cachedBasePath: string | null = null;

/**
 * Detects the base path from server-injected value or current URL.
 * Priority:
 * 1. Server-injected window.__HEALARR_BASE_PATH__ (most reliable)
 * 2. URL detection based on known SPA routes (fallback)
 */
export function detectBasePath(): string {
  if (cachedBasePath !== null) {
    return cachedBasePath;
  }

  // First, check for server-injected base path (most reliable method)
  if (typeof window.__HEALARR_BASE_PATH__ === 'string') {
    cachedBasePath = window.__HEALARR_BASE_PATH__;
    // Normalize: remove trailing slash if present (but keep "/" as is)
    if (cachedBasePath.length > 1 && cachedBasePath.endsWith('/')) {
      cachedBasePath = cachedBasePath.slice(0, -1);
    }
    return cachedBasePath;
  }

  // Fallback: Try to detect from the current URL
  const pathname = window.location.pathname;

  // Check if we're at a known SPA route
  const spaRoutes = ['/login', '/setup', '/config', '/scans', '/corruptions', '/logs', '/help'];

  for (const route of spaRoutes) {
    const idx = pathname.indexOf(route);
    if (idx > 0) {
      // Found a SPA route with something before it - that's our base path
      cachedBasePath = pathname.substring(0, idx);
      return cachedBasePath;
    }
  }

  // Check if pathname ends with index.html
  if (pathname.endsWith('/index.html')) {
    cachedBasePath = pathname.replace('/index.html', '');
    return cachedBasePath;
  }

  // If we're at root or a direct SPA route, base path is empty
  cachedBasePath = '';
  return cachedBasePath;
}

/**
 * Gets the base path for API calls.
 * Returns the path prefix to prepend to /api endpoints.
 * Returns empty string for root deployments to avoid "//api" URLs.
 */
export function getApiBasePath(): string {
  const base = detectBasePath();
  // Return empty string for root deployments to prevent "//api" protocol-relative URLs
  return base === '/' ? '' : base;
}

/**
 * Gets the full API URL for a given endpoint.
 * @param endpoint - The API endpoint (e.g., "/stats/dashboard")
 */
export function getApiUrl(endpoint: string): string {
  const base = getApiBasePath();
  const apiPath = endpoint.startsWith('/') ? `/api${endpoint}` : `/api/${endpoint}`;
  return base ? `${base}${apiPath}` : apiPath;
}

/**
 * Gets the WebSocket URL for the given path.
 */
export function getWebSocketUrl(path: string = '/api/ws'): string {
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  const base = getApiBasePath();
  const fullPath = base ? `${base}${path}` : path;
  return `${protocol}//${window.location.host}${fullPath}`;
}

/**
 * Gets the base path for React Router.
 * Returns "/" if no base path, or the base path with trailing slash.
 */
export function getRouterBasePath(): string {
  const base = detectBasePath();
  return base || '/';
}
