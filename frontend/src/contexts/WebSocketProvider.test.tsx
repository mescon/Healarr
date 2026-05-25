import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, act } from '@testing-library/react';
import { useEffect } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import {
    WebSocketProvider,
    useWebSocket,
    useWebSocketEvent,
    type WSMessage,
} from './WebSocketProvider';

/**
 * Minimal controllable WebSocket stand-in. jsdom has no WebSocket, so we install
 * this on the global and drive open/message events by hand from the test.
 */
class MockWebSocket {
    static readonly CONNECTING = 0;
    static readonly OPEN = 1;
    static readonly CLOSING = 2;
    static readonly CLOSED = 3;
    static instances: MockWebSocket[] = [];

    readyState = MockWebSocket.CONNECTING;
    url: string;
    onopen: ((ev: Event) => void) | null = null;
    onclose: ((ev: Event) => void) | null = null;
    onerror: ((ev: Event) => void) | null = null;
    onmessage: ((ev: MessageEvent) => void) | null = null;

    constructor(url: string) {
        this.url = url;
        MockWebSocket.instances.push(this);
    }

    close() {
        // Intentionally does not fire onclose: these tests don't exercise the
        // reconnect-on-close path, and firing it on unmount would schedule a
        // dangling backoff timer.
        this.readyState = MockWebSocket.CLOSED;
    }

    // --- test helpers ---
    fireOpen() {
        this.readyState = MockWebSocket.OPEN;
        this.onopen?.(new Event('open'));
    }
    fireMessage(payload: unknown) {
        this.onmessage?.({ data: JSON.stringify(payload) } as MessageEvent);
    }
}

function renderWithProvider(ui: React.ReactNode) {
    const queryClient = new QueryClient({
        defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    return render(
        <QueryClientProvider client={queryClient}>
            <WebSocketProvider>{ui}</WebSocketProvider>
        </QueryClientProvider>
    );
}

// Calls reconnect() on mount so the provider opens a (mock) socket.
function Connector() {
    const { reconnect } = useWebSocket();
    useEffect(() => {
        reconnect();
    }, [reconnect]);
    return null;
}

function Listener({ onMessage }: { onMessage: (m: WSMessage) => void }) {
    useWebSocketEvent(onMessage);
    return null;
}

const socket = () => MockWebSocket.instances[0];

describe('WebSocketProvider event emitter', () => {
    beforeEach(() => {
        MockWebSocket.instances = [];
        // setup.ts replaces localStorage with vi.fn() stubs, so drive getItem directly.
        vi.mocked(localStorage.getItem).mockReturnValue('test-token');
        vi.stubGlobal('WebSocket', MockWebSocket);
    });

    afterEach(() => {
        vi.unstubAllGlobals();
        vi.mocked(localStorage.getItem).mockReset();
    });

    it('delivers every message to a subscriber, including bursts in one tick', () => {
        const received: WSMessage[] = [];
        renderWithProvider(<><Connector /><Listener onMessage={(m) => received.push(m)} /></>);

        act(() => socket().fireOpen());
        act(() => {
            // Two messages back-to-back: the old single lastMessage value would
            // have only surfaced the last one. The emitter must deliver both.
            socket().fireMessage({ type: 'log', data: { line: 'a' } });
            socket().fireMessage({ type: 'log', data: { line: 'b' } });
        });

        expect(received).toHaveLength(2);
        expect((received[0].data as { line: string }).line).toBe('a');
        expect((received[1].data as { line: string }).line).toBe('b');
    });

    it('normalizes backend event envelopes to {type, data}', () => {
        const received: WSMessage[] = [];
        renderWithProvider(<><Connector /><Listener onMessage={(m) => received.push(m)} /></>);

        act(() => socket().fireOpen());
        act(() =>
            socket().fireMessage({
                type: 'event',
                data: { event_type: 'ScanProgress', event_data: { files_done: 3 } },
            })
        );

        expect(received).toHaveLength(1);
        expect(received[0].type).toBe('ScanProgress');
        expect((received[0].data as { files_done: number }).files_done).toBe(3);
    });

    it('stops delivering after the subscriber unmounts', () => {
        const received: WSMessage[] = [];
        function Harness({ listening }: { listening: boolean }) {
            return (
                <>
                    <Connector />
                    {listening && <Listener onMessage={(m) => received.push(m)} />}
                </>
            );
        }
        const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        const { rerender } = render(
            <QueryClientProvider client={queryClient}>
                <WebSocketProvider><Harness listening={true} /></WebSocketProvider>
            </QueryClientProvider>
        );

        act(() => socket().fireOpen());
        act(() => socket().fireMessage({ type: 'log', data: { line: 'a' } }));
        expect(received).toHaveLength(1);

        rerender(
            <QueryClientProvider client={queryClient}>
                <WebSocketProvider><Harness listening={false} /></WebSocketProvider>
            </QueryClientProvider>
        );

        act(() => socket().fireMessage({ type: 'log', data: { line: 'b' } }));
        expect(received).toHaveLength(1); // unchanged: handler was unsubscribed
    });

    it('isolates a throwing subscriber from the others', () => {
        const received: WSMessage[] = [];
        renderWithProvider(
            <>
                <Connector />
                <Listener onMessage={() => { throw new Error('boom'); }} />
                <Listener onMessage={(m) => received.push(m)} />
            </>
        );

        act(() => socket().fireOpen());
        act(() => socket().fireMessage({ type: 'log', data: { line: 'a' } }));

        // The healthy subscriber still receives the message.
        expect(received).toHaveLength(1);
    });

    it('always invokes the latest handler closure (no stale state)', () => {
        const seen: number[] = [];
        function Probe({ value }: { value: number }) {
            useWebSocketEvent(() => seen.push(value));
            return null;
        }
        const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        const tree = (value: number) => (
            <QueryClientProvider client={queryClient}>
                <WebSocketProvider><Connector /><Probe value={value} /></WebSocketProvider>
            </QueryClientProvider>
        );
        const { rerender } = render(tree(0));

        act(() => socket().fireOpen());
        rerender(tree(9)); // handler closure must update to the new prop value
        act(() => socket().fireMessage({ type: 'log', data: {} }));

        expect(seen).toEqual([9]);
    });
});
