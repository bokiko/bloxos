'use client';

import { useEffect, useRef, useState } from 'react';

const getWsUrl = () => {
  if (typeof window === 'undefined') return 'ws://localhost:3001';
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
  return `${protocol}//${window.location.hostname}:3001`;
};

interface TerminalProps {
  rigId: string;
  onClose?: () => void;
}

export default function Terminal({ rigId, onClose }: TerminalProps) {
  const terminalRef = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState<'connecting' | 'connected' | 'disconnected' | 'error'>('connecting');
  const [error, setError] = useState<string | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const termRef = useRef<unknown>(null);
  const fitAddonRef = useRef<unknown>(null);

  useEffect(() => {
    let isMounted = true;

    async function initTerminal() {
      // Dynamically import xterm to avoid SSR issues
      const { Terminal } = await import('@xterm/xterm');
      const { FitAddon } = await import('@xterm/addon-fit');
      const { WebLinksAddon } = await import('@xterm/addon-web-links');

      if (!isMounted || !terminalRef.current) return;

      // Create terminal
      const term = new Terminal({
        cursorBlink: true,
        fontSize: 14,
        fontFamily: 'JetBrains Mono, Menlo, Monaco, Consolas, monospace',
        theme: {
          background: '#0f172a',
          foreground: '#e2e8f0',
          cursor: '#22d3ee',
          cursorAccent: '#0f172a',
          selectionBackground: '#334155',
          black: '#0f172a',
          red: '#ef4444',
          green: '#22c55e',
          yellow: '#eab308',
          blue: '#3b82f6',
          magenta: '#a855f7',
          cyan: '#22d3ee',
          white: '#e2e8f0',
          brightBlack: '#475569',
          brightRed: '#f87171',
          brightGreen: '#4ade80',
          brightYellow: '#facc15',
          brightBlue: '#60a5fa',
          brightMagenta: '#c084fc',
          brightCyan: '#67e8f9',
          brightWhite: '#f8fafc',
        },
        allowProposedApi: true,
      });

      const fitAddon = new FitAddon();
      const webLinksAddon = new WebLinksAddon();

      term.loadAddon(fitAddon);
      term.loadAddon(webLinksAddon);

      term.open(terminalRef.current);
      fitAddon.fit();

      termRef.current = term;
      fitAddonRef.current = fitAddon;

      term.writeln('\x1b[36m[BloxOS Terminal]\x1b[0m Connecting to rig...');

      // Connect WebSocket
      const ws = new WebSocket(`${getWsUrl()}/api/terminal/ws/${rigId}`);
      wsRef.current = ws;

      ws.onopen = () => {
        // Authenticate
        const token = localStorage.getItem('bloxos_token');
        if (token) {
          ws.send(JSON.stringify({ type: 'auth', token }));
        } else {
          setStatus('error');
          setError('Not authenticated');
          term.writeln('\x1b[31mError: Not authenticated\x1b[0m');
        }
      };

      ws.onmessage = (event) => {
        const message = JSON.parse(event.data);

        if (message.type === 'connected') {
          setStatus('connected');
          term.writeln('\x1b[32mConnected!\x1b[0m\r\n');
          // Send initial resize
          ws.send(JSON.stringify({ 
            type: 'resize', 
            cols: term.cols, 
            rows: term.rows 
          }));
        } else if (message.type === 'output') {
          term.write(message.data);
        } else if (message.type === 'error') {
          setStatus('error');
          setError(message.message);
          term.writeln(`\x1b[31mError: ${message.message}\x1b[0m`);
        } else if (message.type === 'disconnected') {
          setStatus('disconnected');
          term.writeln('\r\n\x1b[33mDisconnected from rig\x1b[0m');
        }
      };

      ws.onclose = () => {
        if (isMounted) {
          setStatus('disconnected');
        }
      };

      ws.onerror = () => {
        if (isMounted) {
          setStatus('error');
          setError('Connection failed');
        }
      };

      // Handle terminal input
      term.onData((data: string) => {
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: 'input', data }));
        }
      });

      // Handle resize
      const handleResize = () => {
        fitAddon.fit();
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ 
            type: 'resize', 
            cols: term.cols, 
            rows: term.rows 
          }));
        }
      };

      window.addEventListener('resize', handleResize);

      return () => {
        window.removeEventListener('resize', handleResize);
      };
    }

    initTerminal();

    return () => {
      isMounted = false;
      if (wsRef.current) {
        wsRef.current.close();
      }
      if (termRef.current) {
        (termRef.current as { dispose: () => void }).dispose();
      }
    };
  }, [rigId]);

  return (
    <div className="flex flex-col h-full bg-slate-900 rounded-lg overflow-hidden border border-slate-700">
      {/* Terminal Header */}
      <div className="flex items-center justify-between px-4 py-2 bg-slate-800 border-b border-slate-700">
        <div className="flex items-center gap-3">
          <div className="flex gap-1.5">
            <button 
              onClick={onClose}
              className="w-3 h-3 rounded-full bg-red-500 hover:bg-red-400 transition-colors"
              title="Close"
            />
            <div className="w-3 h-3 rounded-full bg-yellow-500" />
            <div className="w-3 h-3 rounded-full bg-green-500" />
          </div>
          <span className="text-sm text-slate-400">Terminal</span>
        </div>
        <div className="flex items-center gap-2">
          <span className={`px-2 py-0.5 text-xs rounded ${
            status === 'connected' ? 'bg-green-500/20 text-green-400' :
            status === 'connecting' ? 'bg-yellow-500/20 text-yellow-400' :
            status === 'error' ? 'bg-red-500/20 text-red-400' :
            'bg-slate-500/20 text-slate-400'
          }`}>
            {status === 'connected' ? 'Connected' :
             status === 'connecting' ? 'Connecting...' :
             status === 'error' ? 'Error' : 'Disconnected'}
          </span>
        </div>
      </div>

      {/* Terminal Content */}
      <div 
        ref={terminalRef} 
        className="flex-1 p-2"
        style={{ minHeight: '400px' }}
      />

      {error && (
        <div className="px-4 py-2 bg-red-500/10 border-t border-red-500/30 text-red-400 text-sm">
          {error}
        </div>
      )}
    </div>
  );
}
