/* eslint-disable react-refresh/only-export-components */
import React, { createContext, useContext, useEffect, useState, useRef, useCallback } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { getWebSocketUrl } from '../lib/basePath';

/**
 * Dev-only connection logging. Stripped from production builds so the console
 * stays quiet and — critically — never carries the auth token that rides in the
 * WebSocket URL's query string. Genuine errors still use console.error directly.
 */
const debug = (...args: unknown[]) => {
    if (import.meta.env.DEV) {
        console.log('[ws]', ...args);
    }
};

/**
 * A WebSocket message after normalization. Backend `event` envelopes are
 * flattened so consumers see `{ type: <event_type>, data: <event_data> }`.
 */
export interface WSMessage {
    type: string;
    data: unknown;
    _raw?: unknown;
}

type WSHandler = (message: WSMessage) => void;

interface WebSocketContextType {
    isConnected: boolean;
    /** Register a handler for every incoming message. Returns an unsubscribe fn. */
    subscribe: (handler: WSHandler) => () => void;
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

/**
 * Subscribe to every WebSocket message exactly once, decoupled from render
 * timing. The handler may freely close over current props/state — the latest
 * version is always invoked, so there are no stale closures, no dependency
 * array to maintain, and no messages dropped when several arrive in one tick
 * (the failure mode of reading a single `lastMessage` value via useEffect).
 */
export const useWebSocketEvent = (handler: WSHandler) => {
    const { subscribe } = useWebSocket();
    const handlerRef = useRef(handler);
    useEffect(() => {
        handlerRef.current = handler;
    });
    useEffect(() => subscribe((msg) => handlerRef.current(msg)), [subscribe]);
};

export const WebSocketProvider = ({ children }: { children: React.ReactNode }) => {
    const [isConnected, setIsConnected] = useState(false);
    const wsRef = useRef<WebSocket | null>(null);
    const retryCountRef = useRef(0);
    const subscribersRef = useRef<Set<WSHandler>>(new Set());
    const queryClient = useQueryClient();

    const connectRef = useRef<() => void>(() => { });

    // Stable across renders: handlers live in a ref, so subscribing never
    // forces useWebSocketEvent's effect to re-run.
    const subscribe = useCallback((handler: WSHandler) => {
        subscribersRef.current.add(handler);
        return () => {
            subscribersRef.current.delete(handler);
        };
    }, []);

    const connect = useCallback(() => {
        // Don't attempt connection on login page
        // This prevents unnecessary WebSocket errors during setup/login
        if (window.location.pathname.endsWith('/login')) {
            return;
        }

        // Get auth token from localStorage
        const token = localStorage.getItem('healarr_token');

        if (!token) {
            // No token yet - this is normal on initial load before login
            // Don't log an error, just silently skip connection
            return;
        }

        // If already connected with same token, don't reconnect
        // This prevents unnecessary disconnect/reconnect on navigation
        if (wsRef.current && wsRef.current.readyState === WebSocket.OPEN) {
            return;
        }

        // Use base path aware WebSocket URL
        const wsUrl = `${getWebSocketUrl()}?token=${token}`;

        // Log the tokenless URL only — never wsUrl, which carries the token.
        debug('connecting to', getWebSocketUrl());

        // Close existing connection if any (e.g., connecting or closing state)
        if (wsRef.current && wsRef.current.readyState !== WebSocket.CLOSED) {
            wsRef.current.close();
        }

        const ws = new WebSocket(wsUrl);

        ws.onopen = () => {
            debug('connected');
            setIsConnected(true);
            retryCountRef.current = 0; // Reset backoff on successful connection
        };

        ws.onclose = () => {
            debug('disconnected');
            setIsConnected(false);

            // Reconnect with exponential backoff (max 30 seconds)
            // Don't reconnect on login page or if token is missing
            const isOnLoginPage = window.location.pathname.endsWith('/login');
            if (!isOnLoginPage && localStorage.getItem('healarr_token')) {
                const backoff = Math.min(3000 * Math.pow(1.5, retryCountRef.current), 30000);
                retryCountRef.current++;
                debug(`reconnecting in ${Math.round(backoff / 1000)}s (attempt ${retryCountRef.current})`);
                setTimeout(() => connectRef.current(), backoff);
            }
        };

        ws.onerror = (error) => {
            console.error('WebSocket Error:', error);
            ws.close();
        };

        ws.onmessage = (event) => {
            try {
                const rawMessage = JSON.parse(event.data);

                // Transform event messages to use event_type as the type
                // Backend sends: {"type": "event", "data": {"event_type": "ScanProgress", "event_data": {...}}}
                // Transform to: {"type": "ScanProgress", "data": {...event_data fields...}}
                let message: WSMessage = rawMessage;
                if (rawMessage.type === 'event' && rawMessage.data?.event_type) {
                    const eventData = rawMessage.data.event_data || {};
                    message = {
                        type: rawMessage.data.event_type,
                        data: eventData,
                        // Keep original event metadata for debugging
                        _raw: rawMessage.data,
                    };
                }

                // Fan out to every subscriber. A subscriber throwing must not
                // skip the others or the centralized query invalidation below.
                subscribersRef.current.forEach((handler) => {
                    try {
                        handler(message);
                    } catch (err) {
                        console.error('WebSocket subscriber threw:', err);
                    }
                });

                // Invalidate queries based on event type
                const eventType = message.type;

                // Scan events - refresh scan list and stats. 'health' feeds
                // the sidebar's Active Scans count, which otherwise lags up
                // to its 30s poll behind a scan starting or finishing.
                if (eventType === 'ScanStarted' || eventType === 'ScanCompleted' || eventType === 'ScanFailed') {
                    queryClient.invalidateQueries({ queryKey: ['scans'] });
                    queryClient.invalidateQueries({ queryKey: ['dashboardStats'] });
                    queryClient.invalidateQueries({ queryKey: ['health'] });
                }

                // Health events - refresh dashboard stats
                if (eventType === 'SystemHealthDegraded' || eventType === 'InstanceUnhealthy' || eventType === 'InstanceHealthy') {
                    queryClient.invalidateQueries({ queryKey: ['dashboardStats'] });
                }

                // Corruption lifecycle events - refresh corruption list and stats
                // These are all the events that can change a corruption's status
                const corruptionEvents = [
                    'CorruptionDetected',
                    'CorruptionIgnored',
                    'RemediationQueued',
                    'DeletionStarted', 'DeletionCompleted', 'DeletionFailed',
                    'SearchStarted', 'SearchCompleted', 'SearchFailed', 'SearchExhausted',
                    'FileDetected',
                    'VerificationStarted', 'VerificationSuccess', 'VerificationFailed',
                    'DownloadTimeout', 'DownloadProgress', 'DownloadFailed',
                    'ImportBlocked', 'ManuallyRemoved', 'DownloadIgnored',
                    'RetryScheduled', 'MaxRetriesReached',
                    'StuckRemediation',
                    'NotificationSent', 'NotificationFailed'
                ];
                if (corruptionEvents.includes(eventType)) {
                    queryClient.invalidateQueries({ queryKey: ['corruptions'] });
                    queryClient.invalidateQueries({ queryKey: ['dashboardStats'] });
                }

            } catch (e) {
                console.error('Failed to parse WebSocket message:', e);
            }
        };

        wsRef.current = ws;
    }, [queryClient]);

    // Keep connectRef in sync with connect function for use in onclose handler
    useEffect(() => {
        connectRef.current = connect;
    }, [connect]);

    useEffect(() => {
        // Don't auto-connect on initial mount!
        // The connection will be triggered ONLY by:
        // 1. Login component after successful authentication (calls reconnect())
        // 2. ProtectedRoute after verifying existing token is valid (calls reconnect())
        //
        // This prevents WebSocket errors with stale tokens during setup/login.
        // The timing issue is: WebSocketProvider mounts before React Router redirects
        // to /login, so checking pathname here doesn't work reliably.

        return () => {
            if (wsRef.current) {
                wsRef.current.close();
            }
        };
    }, []);

    return (
        <WebSocketContext.Provider value={{ isConnected, subscribe, reconnect: connect }}>
            {children}
        </WebSocketContext.Provider>
    );
};
