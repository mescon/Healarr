import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react-swc'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  // Absolute base: nested SPA routes (e.g. /scans/123) load assets from
  // /assets/* regardless of current path. Relative './' breaks on any
  // deep-link because the browser resolves it against the current URL,
  // producing /scans/assets/foo.js, which the SPA fallback returns as
  // HTML and the browser then refuses on MIME-type grounds. A
  // sub-path mount (e.g. /healarr/) would need a build-time env var
  // + server-side index.html base-href rewrite, not a static './'.
  base: '/',
  build: {
    outDir: '../web',
    emptyOutDir: true,
    rollupOptions: {
      output: {
        manualChunks: {
          // React core
          'react-vendor': ['react', 'react-dom', 'react-router-dom'],
          // UI libraries
          'ui-vendor': ['framer-motion', 'lucide-react', 'clsx'],
          // Charting
          'chart-vendor': ['recharts'],
          // Data fetching
          'query-vendor': ['@tanstack/react-query', 'axios'],
        },
      },
    },
  },
})
