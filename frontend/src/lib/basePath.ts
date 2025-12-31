/**
 * Base path configuration for reverse proxy support.
 * The base path is detected from the current URL or fetched from the server.
 */

// Cache the base path once detected
let cachedBasePath: string | null = null;

/**
 * Detects the base path from the current URL.
 * If the app is served from /healarr/, this returns "/healarr".
 * If served from root, returns "".
 */
export function detectBasePath(): string {
  if (cachedBasePath !== null) {
    return cachedBasePath;
  }

  // Try to detect from the current script URL or document base
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
  // But we should also check if the API is available at this path
  cachedBasePath = '';
  return cachedBasePath;
}

/**
 * Gets the base path for API calls.
 * Returns the path prefix to prepend to /api endpoints.
 */
export function getApiBasePath(): string {
  return detectBasePath();
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
