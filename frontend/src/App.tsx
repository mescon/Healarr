import { lazy, Suspense } from 'react';
import { Routes, Route, Navigate } from 'react-router-dom';
import Layout from './components/layout/Layout';
import ProtectedRoute from './components/auth/ProtectedRoute';
import { ToastProvider } from './contexts/ToastContext';
import { ThemeProvider } from './contexts/ThemeContext';
import { ConnectionProvider } from './contexts/ConnectionContext';
import ToastContainer from './components/ui/ToastContainer';
import ConnectionOverlay from './components/ui/ConnectionOverlay';

// Lazy load page components for better initial bundle size
const Dashboard = lazy(() => import('./pages/Dashboard'));
const Scans = lazy(() => import('./pages/Scans'));
const ScanDetails = lazy(() => import('./pages/ScanDetails'));
const Corruptions = lazy(() => import('./pages/Corruptions'));
const Logs = lazy(() => import('./pages/Logs'));
const Config = lazy(() => import('./pages/Config'));
const Help = lazy(() => import('./pages/Help'));
const Login = lazy(() => import('./pages/Login'));

// Loading fallback for lazy-loaded components
const PageLoader = () => (
  <div className="flex items-center justify-center h-64">
    <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-green-500"></div>
  </div>
);

function App() {
  return (
    <ThemeProvider>
      <ConnectionProvider>
        <ToastProvider>
          <Suspense fallback={<PageLoader />}>
            <Routes>
              <Route path="/login" element={<Login />} />
              <Route path="/" element={
                <ProtectedRoute>
                  <Layout />
                </ProtectedRoute>
              }>
                <Route index element={<Dashboard />} />
                <Route path="scans" element={<Scans />} />
                <Route path="scans/:id" element={<ScanDetails />} />
                <Route path="corruptions" element={<Corruptions />} />
                <Route path="logs" element={<Logs />} />
                <Route path="config" element={<Config />} />
                <Route path="help" element={<Help />} />
                <Route path="*" element={<Navigate to="/" replace />} />
              </Route>
            </Routes>
          </Suspense>
          <ToastContainer />
          <ConnectionOverlay />
        </ToastProvider>
      </ConnectionProvider>
    </ThemeProvider>
  );
}

export default App;
