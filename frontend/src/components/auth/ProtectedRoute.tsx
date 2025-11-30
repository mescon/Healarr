import { useEffect, useState } from 'react';
import { Navigate, useLocation } from 'react-router-dom';
import { getAuthStatus } from '../../lib/api';

interface ProtectedRouteProps {
    children: React.ReactNode;
}

const ProtectedRoute = ({ children }: ProtectedRouteProps) => {
    const [isChecking, setIsChecking] = useState(true);
    const [isAuthenticated, setIsAuthenticated] = useState(false);
    const [needsSetup, setNeedsSetup] = useState(false);
    const location = useLocation();

    useEffect(() => {
        const checkAuth = async () => {
            const token = localStorage.getItem('healarr_token');
            
            if (!token) {
                // No token - check if setup is needed
                try {
                    const status = await getAuthStatus();
                    setNeedsSetup(!status.is_setup);
                } catch {
                    // If we can't check status, assume setup needed
                    setNeedsSetup(true);
                }
                setIsAuthenticated(false);
                setIsChecking(false);
                return;
            }

            // Have a token - verify it's still valid by checking auth status
            try {
                const status = await getAuthStatus();
                if (!status.is_setup) {
                    // Password was reset, need to set up again
                    localStorage.removeItem('healarr_token');
                    setNeedsSetup(true);
                    setIsAuthenticated(false);
                } else {
                    setIsAuthenticated(true);
                }
            } catch {
                // Token might be invalid
                localStorage.removeItem('healarr_token');
                setIsAuthenticated(false);
            }
            
            setIsChecking(false);
        };

        checkAuth();
    }, [location.pathname]);

    if (isChecking) {
        return (
            <div className="min-h-screen bg-slate-100 dark:bg-slate-950 flex items-center justify-center">
                <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-green-500"></div>
            </div>
        );
    }

    if (!isAuthenticated) {
        // Redirect to login, preserving the attempted URL
        return <Navigate to="/login" state={{ from: location, needsSetup }} replace />;
    }

    return <>{children}</>;
};

export default ProtectedRoute;
