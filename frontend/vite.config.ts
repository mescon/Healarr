import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react-swc'

// https://vite.dev/config/
export default defineConfig({
  plugins: [react()],
  // Use relative paths so the app works at any base path (e.g., /healarr/)
  base: './',
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
