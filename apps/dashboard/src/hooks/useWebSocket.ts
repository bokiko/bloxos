'use client';

import { useEffect, useRef, useState, useCallback } from 'react';

const getWsUrl = () => {
  if (typeof window === 'undefined') return 'ws://localhost:3001';
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${protocol}//${window.location.hostname}:3001`;
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
        console.log('WebSocket connected');
        setIsConnected(true);
        setError(null);
        reconnectAttempts.current = 0;

        // Authenticate with token from cookie
        // Since we can't access httpOnly cookies, we'll use a different approach
        // The server will need to check the session differently for WS
        ws.send(JSON.stringify({ type: 'auth', token: getTokenFromStorage() }));
      };

      ws.onmessage = (event) => {
        try {
          const message: WebSocketMessage = JSON.parse(event.data);

          if (message.type === 'error') {
            setError(message.message || 'WebSocket error');
            return;
          }

          if (message.type === 'authenticated') {
            console.log('WebSocket authenticated');
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

        } catch (err) {
          console.error('Failed to parse WebSocket message:', err);
        }
      };

      ws.onclose = () => {
        console.log('WebSocket disconnected');
        setIsConnected(false);
        wsRef.current = null;

        // Attempt to reconnect
        if (reconnectAttempts.current < maxReconnectAttempts) {
          reconnectAttempts.current++;
          const delay = Math.min(1000 * Math.pow(2, reconnectAttempts.current), 30000);
          console.log(`Reconnecting in ${delay}ms (attempt ${reconnectAttempts.current})`);
          reconnectTimeoutRef.current = setTimeout(connect, delay);
        }
      };

      ws.onerror = (err) => {
        console.error('WebSocket error:', err);
        setError('Connection error');
      };

    } catch (err) {
      console.error('Failed to create WebSocket:', err);
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
