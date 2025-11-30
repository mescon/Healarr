import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { WebSocketProvider } from './contexts/WebSocketProvider'
import { getRouterBasePath } from './lib/basePath'
import App from './App.tsx'
import './index.css'

// Global React Query configuration for optimal performance
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: 30 * 1000, // Data considered fresh for 30 seconds
      gcTime: 5 * 60 * 1000, // Cache kept for 5 minutes
      refetchOnWindowFocus: false, // Don't refetch when window regains focus
      retry: 1, // Only retry failed requests once
    },
  },
})

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <WebSocketProvider>
        <BrowserRouter basename={getRouterBasePath()}>
          <App />
        </BrowserRouter>
      </WebSocketProvider>
    </QueryClientProvider>
  </React.StrictMode>,
)
