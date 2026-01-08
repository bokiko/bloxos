'use client';

import { useEffect, useRef, useState, useCallback } from 'react';

const getWsUrl = () => {
  if (typeof window === 'undefined') return 'ws://localhost:3001';
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${protocol}//${window.location.hostname}:3001`;
};

// Use console.warn instead of console.error to avoid Next.js 15 error overlay
const logError = (message: string, ...args: unknown[]) => {
  if (process.env.NODE_ENV === 'development') {
    console.warn(`[WS] ${message}`, ...args);
  } else {
    console.error(message, ...args);
  }
};

interface WebSocketMessage {
  type: string;
  event?: string;
  data?: unknown;
  message?: string;
}

interface UseWebSocketOptions {
  onRigsUpdate?: (rigs: unknown[]) => void;
  onStatsUpdate?: (stats: unknown) => void;
  onAlertUpdate?: (alert: unknown) => void;
}

export function useWebSocket(options: UseWebSocketOptions = {}) {
  const [isConnected, setIsConnected] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectTimeoutRef = useRef<NodeJS.Timeout | null>(null);
  const reconnectAttempts = useRef(0);
  const maxReconnectAttempts = 5;

  const connect = useCallback(() => {
    if (wsRef.current?.readyState === WebSocket.OPEN) return;

    try {
      const ws = new WebSocket(`${getWsUrl()}/api/ws`);
      wsRef.current = ws;

      ws.onopen = () => {
        setIsConnected(true);
        setError(null);
        reconnectAttempts.current = 0;

        // Authenticate with token from cookie
        const token = getTokenFromStorage();
        if (token) {
          ws.send(JSON.stringify({ type: 'auth', token }));
        }
      };

      ws.onmessage = (event) => {
        try {
          const message: WebSocketMessage = JSON.parse(event.data);

          if (message.type === 'error') {
            setError(message.message || 'WebSocket error');
            return;
          }

          if (message.type === 'authenticated') {
            return;
          }

          // Handle different event types
          if (message.event === 'rigs' || message.type === 'initial' && message.event === 'rigs') {
            options.onRigsUpdate?.(message.data as unknown[]);
          }

          if (message.event === 'stats' || message.type === 'initial' && message.event === 'stats') {
            options.onStatsUpdate?.(message.data);
          }

          if (message.event === 'alert') {
            options.onAlertUpdate?.(message.data);
          }

          if (message.event === 'rig-update') {
            options.onRigsUpdate?.(message.data as unknown[]);
          }

        } catch {
          // Silently ignore parse errors
        }
      };

      ws.onclose = () => {
        setIsConnected(false);
        wsRef.current = null;

        // Attempt to reconnect
        if (reconnectAttempts.current < maxReconnectAttempts) {
          reconnectAttempts.current++;
          const delay = Math.min(1000 * Math.pow(2, reconnectAttempts.current), 30000);
          reconnectTimeoutRef.current = setTimeout(connect, delay);
        }
      };

      ws.onerror = () => {
        // WebSocket errors are normal during development - don't show overlay
        setError('Connection error');
      };

    } catch {
      setError('Failed to connect');
    }
  }, [options]);

  const disconnect = useCallback(() => {
    if (reconnectTimeoutRef.current) {
      clearTimeout(reconnectTimeoutRef.current);
    }
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
    setIsConnected(false);
  }, []);

  const send = useCallback((data: unknown) => {
    if (wsRef.current?.readyState === WebSocket.OPEN) {
      wsRef.current.send(JSON.stringify(data));
    }
  }, []);

  useEffect(() => {
    connect();
    return () => disconnect();
  }, [connect, disconnect]);

  return {
    isConnected,
    error,
    send,
    reconnect: connect,
  };
}

// Helper to get token - for now we'll store it in localStorage on login
function getTokenFromStorage(): string | null {
  if (typeof window === 'undefined') return null;
  return localStorage.getItem('bloxos_token');
}

// Export helper to save token
export function saveTokenToStorage(token: string) {
  if (typeof window !== 'undefined') {
    localStorage.setItem('bloxos_token', token);
  }
}

export function removeTokenFromStorage() {
  if (typeof window !== 'undefined') {
    localStorage.removeItem('bloxos_token');
  }
}
