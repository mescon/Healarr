/* eslint-disable react-refresh/only-export-components */
import React, { createContext, useContext, useEffect, useState, useRef, useCallback } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { getWebSocketUrl } from '../lib/basePath';

interface WebSocketContextType {
    isConnected: boolean;
    lastMessage: unknown;
    reconnect: () => void;
}

export const WebSocketContext = createContext<WebSocketContextType | undefined>(undefined);

export const useWebSocket = () => {
    const context = useContext(WebSocketContext);
    if (context === undefined) {
        throw new Error('useWebSocket must be used within a WebSocketProvider');
    }
    return context;
};

export const WebSocketProvider = ({ children }: { children: React.ReactNode }) => {
    const [isConnected, setIsConnected] = useState(false);
    const [lastMessage, setLastMessage] = useState<unknown>(null);
    const wsRef = useRef<WebSocket | null>(null);
    const retryCountRef = useRef(0);
    const queryClient = useQueryClient();

    const connectRef = useRef<() => void>(() => { });

    const connect = useCallback(() => {
        // Get auth token from localStorage
        const token = localStorage.getItem('healarr_token');

        if (!token) {
            // No token yet - this is normal on initial load before login
            // Don't log an error, just silently skip connection
            return;
        }

        // Use base path aware WebSocket URL
        const wsUrl = `${getWebSocketUrl()}?token=${token}`;

        console.log('Connecting to WebSocket:', wsUrl);

        // Close existing connection if any
        if (wsRef.current) {
            wsRef.current.close();
        }

        const ws = new WebSocket(wsUrl);

        ws.onopen = () => {
            console.log('WebSocket Connected');
            setIsConnected(true);
            retryCountRef.current = 0; // Reset backoff on successful connection
        };

        ws.onclose = () => {
            console.log('WebSocket Disconnected');
            setIsConnected(false);

            // Reconnect with exponential backoff (max 30 seconds)
            if (localStorage.getItem('healarr_token')) {
                const backoff = Math.min(3000 * Math.pow(1.5, retryCountRef.current), 30000);
                retryCountRef.current++;
                console.log(`WebSocket reconnecting in ${Math.round(backoff / 1000)}s (attempt ${retryCountRef.current})`);
                setTimeout(() => connectRef.current(), backoff);
            }
        };

        ws.onerror = (error) => {
            console.error('WebSocket Error:', error);
            ws.close();
        };

        ws.onmessage = (event) => {
            try {
                const message = JSON.parse(event.data);
                setLastMessage(message);

                // Invalidate queries based on event type
                if (message.type === 'scan_started' || message.type === 'scan_completed' || message.type === 'scan_failed') {
                    queryClient.invalidateQueries({ queryKey: ['scans'] });
                    queryClient.invalidateQueries({ queryKey: ['dashboardStats'] });
                } else if (message.type === 'corruption_detected' || message.type === 'remediation_started' || message.type === 'remediation_completed') {
                    queryClient.invalidateQueries({ queryKey: ['corruptions'] });
                    queryClient.invalidateQueries({ queryKey: ['dashboardStats'] });
                }

            } catch (e) {
                console.error('Failed to parse WebSocket message:', e);
            }
        };

        wsRef.current = ws;
    }, [queryClient]);

    useEffect(() => {
        connect();

        return () => {
            if (wsRef.current) {
                wsRef.current.close();
            }
        };
    }, [queryClient, connect]);

    return (
        <WebSocketContext.Provider value={{ isConnected, lastMessage, reconnect: connect }}>
            {children}
        </WebSocketContext.Provider>
    );
};
