import { useConnection } from '../../contexts/ConnectionContext';
import { WifiOff, RefreshCw } from 'lucide-react';

const ConnectionOverlay = () => {
    const { isConnected, isReconnecting, reconnectAttempts } = useConnection();

    if (isConnected) return null;

    return (
        <div className="fixed inset-0 z-[100] flex items-center justify-center">
            {/* Dimmed backdrop */}
            <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" />
            
            {/* Connection status card */}
            <div className="relative bg-slate-800 border border-slate-700 rounded-xl p-8 shadow-2xl max-w-md mx-4">
                <div className="flex flex-col items-center text-center">
                    {/* Icon with animation */}
                    <div className="relative mb-6">
                        <div className="w-20 h-20 rounded-full bg-red-500/20 flex items-center justify-center">
                            <WifiOff className="w-10 h-10 text-red-400" />
                        </div>
                        {isReconnecting && (
                            <div className="absolute -bottom-1 -right-1 w-8 h-8 rounded-full bg-slate-700 flex items-center justify-center border-2 border-slate-800">
                                <RefreshCw className="w-4 h-4 text-blue-400 animate-spin" />
                            </div>
                        )}
                    </div>

                    {/* Title */}
                    <h2 className="text-xl font-semibold text-white mb-2">
                        Connection Lost
                    </h2>

                    {/* Description */}
                    <p className="text-slate-400 mb-4">
                        Unable to connect to the Healarr server.
                        {isReconnecting && ' Attempting to reconnect...'}
                    </p>

                    {/* Reconnect status */}
                    {isReconnecting && (
                        <div className="flex items-center gap-2 text-sm text-slate-500">
                            <div className="flex gap-1">
                                <span className="w-2 h-2 bg-blue-500 rounded-full animate-bounce" style={{ animationDelay: '0ms' }} />
                                <span className="w-2 h-2 bg-blue-500 rounded-full animate-bounce" style={{ animationDelay: '150ms' }} />
                                <span className="w-2 h-2 bg-blue-500 rounded-full animate-bounce" style={{ animationDelay: '300ms' }} />
                            </div>
                            <span>Attempt {reconnectAttempts}</span>
                        </div>
                    )}

                    {/* Progress bar animation */}
                    {isReconnecting && (
                        <div className="w-full mt-6 h-1 bg-slate-700 rounded-full overflow-hidden">
                            <div 
                                className="h-full bg-blue-500 rounded-full animate-pulse"
                                style={{
                                    width: '100%',
                                    animation: 'reconnect-progress 3s ease-in-out infinite',
                                }}
                            />
                        </div>
                    )}

                    {/* Help text */}
                    <p className="text-xs text-slate-600 mt-4">
                        Please check that the server is running and your network connection is stable.
                    </p>
                </div>
            </div>

            {/* CSS for progress animation */}
            <style>{`
                @keyframes reconnect-progress {
                    0% { transform: translateX(-100%); }
                    50% { transform: translateX(0%); }
                    100% { transform: translateX(100%); }
                }
            `}</style>
        </div>
    );
};

export default ConnectionOverlay;
