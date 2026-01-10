# Healarr Frontend

## Overview

The frontend is a **React 19** single-page application built with:

- **TypeScript** for type safety
- **Vite 7** for fast development and building
- **Tailwind CSS v4** for styling
- **TanStack Query** for server state management
- **Framer Motion** for animations
- **Recharts** for data visualization
- **React Router** for navigation
- **Lucide React** for icons

## Directory Structure

```
frontend/
├── src/
│   ├── main.tsx              # Entry point, router setup
│   ├── App.tsx               # Root component with routes
│   ├── index.css             # Global styles (Tailwind)
│   ├── pages/                # Route components
│   │   ├── Dashboard.tsx     # Main dashboard with stats
│   │   ├── Corruptions.tsx   # Corruption list + bulk actions
│   │   ├── Scans.tsx         # Scan list + controls
│   │   ├── ScanDetails.tsx   # Individual scan results
│   │   ├── Config.tsx        # Settings orchestrator (imports sections)
│   │   ├── config/           # Config page sections (self-contained)
│   │   │   ├── index.ts      # Barrel export
│   │   │   ├── ArrServersSection.tsx
│   │   │   ├── ScanPathsSection.tsx
│   │   │   ├── SchedulesSection.tsx
│   │   │   └── NotificationsSection.tsx
│   │   ├── Logs.tsx          # Log viewer
│   │   ├── Help.tsx          # Documentation/troubleshooting
│   │   └── Login.tsx         # Authentication
│   ├── components/
│   │   ├── layout/           # Layout components
│   │   │   ├── Sidebar.tsx   # Navigation sidebar
│   │   │   └── Layout.tsx    # Main layout wrapper
│   │   ├── ui/               # Reusable UI components
│   │   │   ├── Accordion.tsx
│   │   │   ├── Card.tsx
│   │   │   ├── ConfirmDialog.tsx  # Confirmation modal (danger/warning/info)
│   │   │   └── ...
│   │   ├── config/           # Shared config components
│   │   │   ├── index.ts      # Barrel export
│   │   │   ├── CollapsibleSection.tsx
│   │   │   └── CronTimeBuilder.tsx
│   │   ├── notifications/    # Shared notification components
│   │   │   ├── index.ts      # Barrel export
│   │   │   ├── ProviderSelect.tsx
│   │   │   ├── ProviderFields.tsx
│   │   │   ├── EventSelector.tsx
│   │   │   └── ProviderIcon.tsx
│   │   ├── charts/           # Chart components
│   │   └── RemediationJourney.tsx  # Corruption workflow modal
│   ├── lib/
│   │   ├── api.ts            # API client (axios) with all endpoints
│   │   ├── basePath.ts       # Reverse proxy path handling
│   │   ├── formatters.ts     # Date/time formatting utilities
│   │   └── utils.ts          # Utility functions
│   ├── contexts/
│   │   └── ThemeContext.tsx  # Dark/light theme
│   └── types/
│       └── api.ts            # TypeScript API types
├── public/
│   └── healarr.svg           # Logo/favicon
├── index.html                # HTML template
├── package.json              # Dependencies
├── vite.config.ts            # Vite configuration
└── tsconfig.json             # TypeScript configuration
```

## Key Files

### `lib/api.ts` - API Client

```typescript
import axios from 'axios';

const api = axios.create({
  get baseURL() {
    return getApiBasePath() + '/api';
  }
});

// Auth interceptor
api.interceptors.request.use((config) => {
  const token = localStorage.getItem('healarr_token');
  if (token) {
    config.headers['X-API-Key'] = token;
  }
  return config;
});

// Key API functions
export const getDashboardStats = () => api.get('/stats/dashboard');
export const getCorruptions = (params) => api.get('/corruptions', { params });
export const triggerScan = (pathId) => api.post('/scans', { path_id: pathId });
export const triggerScanAll = () => api.post('/scans/all');
export const pauseAllScans = () => api.post('/scans/pause-all');
export const resumeAllScans = () => api.post('/scans/resume-all');
export const cancelAllScans = () => api.post('/scans/cancel-all');
export const exportConfig = () => api.get('/config/export');
export const importConfig = (config) => api.post('/config/import', config);
export const downloadDatabaseBackup = () => api.get('/config/backup', { responseType: 'blob' });
// ... many more
```

### Clipboard Copy Pattern (HTTP-safe)

```typescript
// Used in Config.tsx for API key copy
const handleCopy = async () => {
    if (apiKeyData?.api_key) {
        try {
            // Try modern clipboard API first
            if (navigator.clipboard && window.isSecureContext) {
                await navigator.clipboard.writeText(apiKeyData.api_key);
            } else {
                // Fallback for non-secure contexts (HTTP)
                const textArea = document.createElement('textarea');
                textArea.value = apiKeyData.api_key;
                textArea.style.position = 'fixed';
                textArea.style.left = '-999999px';
                document.body.appendChild(textArea);
                textArea.focus();
                textArea.select();
                document.execCommand('copy');
                document.body.removeChild(textArea);
            }
            setCopied(true);
            setTimeout(() => setCopied(false), 2000);
        } catch (err) {
            console.error('Failed to copy:', err);
        }
    }
};
```

## Pages

### Dashboard (`Dashboard.tsx`)

Main overview showing:
- Total files scanned
- Active corruptions count
- Resolution rate
- Recent activity chart
- Quick actions

### Config (`Config.tsx`)

The Config page is a lightweight orchestrator that imports self-contained section components.
Each section manages its own data fetching via React Query hooks.

**Quick Actions Section:**
- Scan All Paths button
- Pause All Scans button
- Resume All Scans button
- Cancel All Scans button

**Modular Sections (in `pages/config/`):**
- **ArrServersSection**: *arr instance management with status indicators, test connection
- **ScanPathsSection**: Directory configuration with per-path settings, accessibility checks
- **SchedulesSection**: Cron schedule management with visual builder
- **NotificationsSection**: Notification provider configuration with log viewer

**Collapsible Sections (in `Config.tsx`):**
- **Advanced Settings**: Export/Import Config (JSON), Download Database Backup, server restart
- **About**: Version info, changelog, system status

**Security Section:**
- API Key display (click to select, copy button with HTTP fallback)
- Password change form
- Webhook URL format reference

**Shared Components (in `components/config/`):**
- **CollapsibleSection**: Animated accordion for grouping related settings
- **CronTimeBuilder**: Visual cron expression builder

**Shared Notification Components (in `components/notifications/`):**
- **ProviderSelect**: Dropdown with provider icons and categories
- **ProviderFields**: Dynamic form fields per provider type
- **EventSelector**: Multi-select for notification events
- **ProviderIcon**: Provider logo rendering with fallback

### Corruptions (`Corruptions.tsx`)

- Status filtering (all, active, resolved, failed, ignored)
- Bulk actions (retry, ignore, delete)
- Click to open `RemediationJourney` modal
- Real-time updates via WebSocket

### Scans (`Scans.tsx`)

- Active scans with progress
- Scan history
- Per-scan pause/resume/cancel
- Bulk controls

### Help (`Help.tsx`)

Built-in documentation:
- Getting started guide
- Reverse proxy setup (Caddy, Nginx, Apache subdirectory, Apache subdomain)
- Troubleshooting sections
- Password reset instructions

## State Management

### Server State (TanStack Query)

```tsx
// Fetch with caching
const { data, isLoading, refetch } = useQuery({
  queryKey: ['corruptions', page, status],
  queryFn: () => getCorruptions({ page, limit: 50, status }),
  refetchInterval: 5000, // Auto-refresh
});

// Mutations with cache invalidation
const mutation = useMutation({
  mutationFn: (id) => retryCorruption(id),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ['corruptions'] });
  },
});
```

### Client State
- **Theme**: React Context (`ThemeContext.tsx`)
- **Auth Token**: localStorage (`healarr_token`)
- **UI State**: useState in components

## Real-Time Updates

### WebSocket Connection

```tsx
useEffect(() => {
  const ws = new WebSocket(`${wsUrl}/api/ws?token=${token}`);
  
  ws.onmessage = (event) => {
    const data = JSON.parse(event.data);
    
    // Invalidate relevant queries based on event type
    switch (data.event_type) {
      case 'ScanProgress':
        queryClient.invalidateQueries({ queryKey: ['active-scans'] });
        break;
      case 'CorruptionDetected':
        queryClient.invalidateQueries({ queryKey: ['corruptions'] });
        queryClient.invalidateQueries({ queryKey: ['dashboard-stats'] });
        break;
      case 'VerificationSuccess':
      case 'VerificationFailed':
        queryClient.invalidateQueries({ queryKey: ['corruptions'] });
        break;
    }
  };
  
  return () => ws.close();
}, []);
```

## Styling

### Tailwind CSS v4

```tsx
// Dark mode support
<div className="bg-white dark:bg-slate-900 text-slate-900 dark:text-white">

// Responsive design
<div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">

// Component patterns
<button className="px-4 py-2 bg-blue-500 hover:bg-blue-600 dark:bg-blue-600 dark:hover:bg-blue-700 text-white rounded-xl transition-colors">
```

### Common UI Patterns

```tsx
// Card with glassmorphism
<div className="rounded-xl border border-slate-200 dark:border-slate-800/50 bg-white/80 dark:bg-slate-900/40 backdrop-blur-xl p-6">

// Status badges
<span className="px-2 py-1 rounded-full text-xs font-medium bg-green-100 text-green-800 dark:bg-green-900/30 dark:text-green-400">

// Input with click-to-select
<input
  type="text"
  value={apiKey}
  readOnly
  onClick={(e) => (e.target as HTMLInputElement).select()}
  onFocus={(e) => e.target.select()}
  className="... cursor-pointer"
/>
```

## Build Configuration

### `vite.config.ts`

```typescript
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../web',  // Output to Go's static file directory
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/api': 'http://localhost:3090',
      '/ws': {
        target: 'ws://localhost:3090',
        ws: true,
      },
    },
  },
});
```

## TypeScript Types

```typescript
// types/api.ts
export interface Corruption {
  id: string;
  file_path: string;
  status: 'detected' | 'queued' | 'remediating' | 'verifying' | 'resolved' | 'failed' | 'ignored';
  corruption_type: string;
  detected_at: string;
  resolved_at?: string;
  retry_count: number;
  max_retries: number;
}

export interface Instance {
  id: number;
  name: string;
  type: 'sonarr' | 'radarr' | 'whisparr-v2' | 'whisparr-v3';
  url: string;
  api_key: string;
  enabled: boolean;
}

export interface ScanPath {
  id: number;
  local_path: string;
  arr_path: string;
  instance_id: number;
  enabled: boolean;
  auto_remediate: boolean;
  dry_run: boolean;  // Per-path dry-run mode
  max_retries: number;
}
```

## Development Workflow

```bash
# Install dependencies
npm install

# Start dev server (hot reload)
npm run dev
# → Opens http://localhost:5173
# → Proxies API calls to http://localhost:3090

# Build for production
npm run build
# → Outputs to ../web/

# Type checking
npm run typecheck
```

## Adding New Features

### New Page

1. Create `src/pages/NewPage.tsx`
2. Add route in `App.tsx`:
   ```tsx
   <Route path="/new-page" element={<NewPage />} />
   ```
3. Add nav link in `Sidebar.tsx`
4. Add API functions in `lib/api.ts`
5. Add TypeScript types in `types/api.ts`

### New API Endpoint

1. Add function in `lib/api.ts`:
   ```typescript
   export const newEndpoint = async (data: NewData) => {
     const { data: response } = await api.post('/new-endpoint', data);
     return response;
   };
   ```

2. Use with TanStack Query:
   ```tsx
   const mutation = useMutation({
     mutationFn: newEndpoint,
     onSuccess: () => {
       queryClient.invalidateQueries({ queryKey: ['affected-data'] });
     },
   });
   ```

## Common Patterns

### Loading States

```tsx
if (isLoading) {
  return (
    <div className="flex items-center justify-center h-64">
      <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blue-600" />
    </div>
  );
}
```

### Error Handling

```tsx
if (error) {
  return (
    <div className="text-red-500 p-4 rounded-lg bg-red-50 dark:bg-red-900/20">
      Error: {error.message}
    </div>
  );
}
```

### Confirmation Dialogs

Use the `ConfirmDialog` component for consistent, animated confirmations:

```tsx
import ConfirmDialog from '../components/ui/ConfirmDialog';

const [showDeleteConfirm, setShowDeleteConfirm] = useState(false);
const [itemToDelete, setItemToDelete] = useState<number | null>(null);

const handleDeleteClick = (id: number) => {
  setItemToDelete(id);
  setShowDeleteConfirm(true);
};

const handleConfirmDelete = () => {
  if (itemToDelete) {
    deleteMutation.mutate(itemToDelete);
  }
  setShowDeleteConfirm(false);
  setItemToDelete(null);
};

// In JSX:
<ConfirmDialog
  isOpen={showDeleteConfirm}
  title="Delete Item"
  message="Are you sure you want to delete this item? This action cannot be undone."
  confirmLabel="Delete"
  cancelLabel="Cancel"
  variant="danger"  // 'danger' | 'warning' | 'info'
  isLoading={deleteMutation.isPending}
  onConfirm={handleConfirmDelete}
  onCancel={() => setShowDeleteConfirm(false)}
/>
```
