import { createContext, useContext, useState, useEffect, useCallback, useRef, type ReactNode } from 'react';
import { getApiBasePath } from '../lib/basePath';

interface ConnectionContextType {
    isConnected: boolean;
    isReconnecting: boolean;
    reconnectAttempts: number;
}

const ConnectionContext = createContext<ConnectionContextType>({
    isConnected: true,
    isReconnecting: false,
    reconnectAttempts: 0,
});

export const useConnection = () => useContext(ConnectionContext);

interface ConnectionProviderProps {
    children: ReactNode;
}

export const ConnectionProvider = ({ children }: ConnectionProviderProps) => {
    const [isConnected, setIsConnected] = useState(true);
    const [isReconnecting, setIsReconnecting] = useState(false);
    const [reconnectAttempts, setReconnectAttempts] = useState(0);
    const reconnectIntervalRef = useRef<number | null>(null);
    const healthCheckIntervalRef = useRef<number | null>(null);

    const checkHealth = useCallback(async (): Promise<boolean> => {
        try {
            const basePath = getApiBasePath();
            const url = basePath ? `${basePath}/api/health` : '/api/health';
            const response = await fetch(url, {
                method: 'GET',
                headers: {
                    'Cache-Control': 'no-cache',
                },
            });
            return response.ok;
        } catch {
            return false;
        }
    }, []);

    const startReconnecting = useCallback(() => {
        if (reconnectIntervalRef.current) return;

        setIsReconnecting(true);
        setReconnectAttempts(0);

        const attemptReconnect = async () => {
            setReconnectAttempts(prev => prev + 1);
            const healthy = await checkHealth();
            
            if (healthy) {
                setIsConnected(true);
                setIsReconnecting(false);
                setReconnectAttempts(0);
                if (reconnectIntervalRef.current) {
                    clearInterval(reconnectIntervalRef.current);
                    reconnectIntervalRef.current = null;
                }
            }
        };

        // Try immediately
        attemptReconnect();
        
        // Then retry every 3 seconds
        reconnectIntervalRef.current = window.setInterval(attemptReconnect, 3000);
    }, [checkHealth]);

    // Health check every 5 seconds when connected
    // This ensures quick detection when the server restarts
    useEffect(() => {
        const runHealthCheck = async () => {
            if (!isConnected) return;
            
            const healthy = await checkHealth();
            if (!healthy) {
                setIsConnected(false);
                startReconnecting();
            }
        };

        // Initial check after a short delay
        const initialTimeout = setTimeout(runHealthCheck, 1000);

        // Regular checks at 5 second interval for quick restart detection
        healthCheckIntervalRef.current = window.setInterval(runHealthCheck, 5000);

        return () => {
            clearTimeout(initialTimeout);
            if (healthCheckIntervalRef.current) {
                clearInterval(healthCheckIntervalRef.current);
            }
        };
    }, [isConnected, checkHealth, startReconnecting]);

    // Cleanup reconnect interval on unmount
    useEffect(() => {
        return () => {
            if (reconnectIntervalRef.current) {
                clearInterval(reconnectIntervalRef.current);
            }
        };
    }, []);

    // Listen for online/offline events
    useEffect(() => {
        const handleOnline = () => {
            // Browser came online, check backend
            checkHealth().then(healthy => {
                if (healthy) {
                    setIsConnected(true);
                    setIsReconnecting(false);
                    setReconnectAttempts(0);
                    if (reconnectIntervalRef.current) {
                        clearInterval(reconnectIntervalRef.current);
                        reconnectIntervalRef.current = null;
                    }
                }
            });
        };

        const handleOffline = () => {
            setIsConnected(false);
            startReconnecting();
        };

        window.addEventListener('online', handleOnline);
        window.addEventListener('offline', handleOffline);

        return () => {
            window.removeEventListener('online', handleOnline);
            window.removeEventListener('offline', handleOffline);
        };
    }, [checkHealth, startReconnecting]);

    return (
        <ConnectionContext.Provider value={{ isConnected, isReconnecting, reconnectAttempts }}>
            {children}
        </ConnectionContext.Provider>
    );
};
