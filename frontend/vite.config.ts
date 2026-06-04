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
        // Function form (object literal is no longer accepted in vite 8 /
        // rollup 4+ TypeScript types). Equivalent to the previous map:
        // packages → bundle. Match on the node_modules path so subpath
        // imports (`react-dom/client`, `lucide-react/icons/...`) all land
        // in the same chunk as the root package.
        manualChunks(id) {
          if (!id.includes('node_modules')) return undefined
          if (/[\\/]node_modules[\\/](react|react-dom|react-router-dom)[\\/]/.test(id)) return 'react-vendor'
          if (/[\\/]node_modules[\\/](framer-motion|lucide-react|clsx)[\\/]/.test(id)) return 'ui-vendor'
          if (/[\\/]node_modules[\\/]recharts[\\/]/.test(id)) return 'chart-vendor'
          if (/[\\/]node_modules[\\/](@tanstack[\\/]react-query|axios)[\\/]/.test(id)) return 'query-vendor'
          return undefined
        },
      },
    },
  },
})
