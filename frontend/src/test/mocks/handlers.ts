import { http, HttpResponse } from 'msw';

// Mock API handlers for common endpoints
export const handlers = [
  // Health check
  http.get('/api/health', () => {
    return HttpResponse.json({
      status: 'healthy',
      version: '1.0.0-test',
      uptime: '1h 30m',
      global_verification_timeout_hours: 24,
      database: { status: 'ok', size_bytes: 1024000 },
      arr_instances: { online: 2, total: 2 },
      active_scans: 0,
      pending_corruptions: 0,
      websocket_clients: 1,
    });
  }),

  // Auth status
  http.get('/api/auth/status', () => {
    return HttpResponse.json({ is_setup: true });
  }),

  // Runtime config
  http.get('/api/config/runtime', () => {
    return HttpResponse.json({
      base_path: '/',
      base_path_source: 'default',
    });
  }),

  // Setup status
  http.get('/api/setup/status', () => {
    return HttpResponse.json({
      needs_setup: false,
      has_password: true,
      has_api_key: true,
      has_instances: true,
      has_scan_paths: true,
      onboarding_dismissed: true,
    });
  }),

  // Dashboard stats
  http.get('/api/stats/dashboard', () => {
    return HttpResponse.json({
      total_corruptions: 10,
      pending_corruptions: 2,
      resolved_corruptions: 8,
      active_scans: 0,
      total_files_scanned: 1000,
      last_scan_at: new Date().toISOString(),
    });
  }),

  // Arr instances
  http.get('/api/config/arr', () => {
    return HttpResponse.json([
      {
        id: 1,
        name: 'Test Sonarr',
        type: 'sonarr',
        url: 'http://localhost:8989',
        api_key: 'test-api-key-12345678901234567890',
        enabled: true,
      },
    ]);
  }),

  // Scan paths
  http.get('/api/config/paths', () => {
    return HttpResponse.json([
      {
        id: 1,
        local_path: '/media/tv',
        arr_path: '/media/tv',
        arr_instance_id: 1,
        enabled: true,
        auto_remediate: true,
        dry_run: false,
        detection_method: 'ffprobe',
        detection_mode: 'quick',
        max_retries: 3,
      },
    ]);
  }),

  // Notifications
  http.get('/api/config/notifications', () => {
    return HttpResponse.json([]);
  }),

  // Schedules
  http.get('/api/config/schedules', () => {
    return HttpResponse.json([]);
  }),

  // Corruptions list
  http.get('/api/corruptions', () => {
    return HttpResponse.json({
      data: [],
      total: 0,
      page: 1,
      limit: 50,
      total_pages: 0,
    });
  }),

  // Scans list
  http.get('/api/scans', () => {
    return HttpResponse.json({
      data: [],
      total: 0,
      page: 1,
      limit: 50,
      total_pages: 0,
    });
  }),

  // Active scans
  http.get('/api/scans/active', () => {
    return HttpResponse.json([]);
  }),

  // Login
  http.post('/api/auth/login', async ({ request }) => {
    const body = await request.json() as { password: string };
    if (body.password === 'correct-password') {
      return HttpResponse.json({
        token: 'test-token-12345',
        message: 'Login successful',
      });
    }
    return HttpResponse.json(
      { error: 'Invalid credentials' },
      { status: 401 }
    );
  }),

  // Notification events
  http.get('/api/config/notifications/events', () => {
    return HttpResponse.json([
      {
        name: 'Scan Events',
        events: [
          { name: 'ScanStarted', label: 'Scan Started', description: 'When a scan begins' },
          { name: 'ScanCompleted', label: 'Scan Completed', description: 'When a scan finishes' },
        ],
      },
      {
        name: 'Corruption Events',
        events: [
          { name: 'CorruptionDetected', label: 'Corruption Detected', description: 'When corruption is found' },
          { name: 'VerificationSuccess', label: 'Fix Verified', description: 'When a fix is verified' },
        ],
      },
    ]);
  }),
];
