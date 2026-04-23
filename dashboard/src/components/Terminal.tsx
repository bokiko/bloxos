"use client";

import { useEffect, useRef, useCallback, useState } from "react";
import { Terminal as XTerm } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";

interface TerminalProps {
  sessionId: string;
  browserToken: string;
  onDisconnect?: () => void;
}

export function Terminal({ sessionId, browserToken, onDisconnect }: TerminalProps) {
  const termRef = useRef<HTMLDivElement>(null);
  const xtermRef = useRef<XTerm | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const [status, setStatus] = useState<"connecting" | "connected" | "disconnected">("connecting");

  const cleanup = useCallback(() => {
    if (wsRef.current) {
      wsRef.current.close();
      wsRef.current = null;
    }
    if (xtermRef.current) {
      xtermRef.current.dispose();
      xtermRef.current = null;
    }
    fitAddonRef.current = null;
  }, []);

  useEffect(() => {
    if (!termRef.current || !sessionId) return;

    // Create xterm instance.
    const term = new XTerm({
      cursorBlink: true,
      fontSize: 14,
      fontFamily: "'JetBrains Mono', 'Fira Code', 'Cascadia Code', 'Menlo', monospace",
      theme: {
        background: "#0a0a0f",
        foreground: "#e4e4ef",
        cursor: "#3b82f6",
        selectionBackground: "#3b82f640",
        black: "#0a0a0f",
        red: "#ef4444",
        green: "#22c55e",
        yellow: "#f59e0b",
        blue: "#3b82f6",
        magenta: "#a855f7",
        cyan: "#06b6d4",
        white: "#e4e4ef",
        brightBlack: "#6b6b80",
        brightRed: "#f87171",
        brightGreen: "#4ade80",
        brightYellow: "#fbbf24",
        brightBlue: "#60a5fa",
        brightMagenta: "#c084fc",
        brightCyan: "#22d3ee",
        brightWhite: "#f8fafc",
      },
      allowProposedApi: true,
      scrollback: 5000,
    });

    const fitAddon = new FitAddon();
    const webLinksAddon = new WebLinksAddon();
    term.loadAddon(fitAddon);
    term.loadAddon(webLinksAddon);
    term.open(termRef.current);

    xtermRef.current = term;
    fitAddonRef.current = fitAddon;

    // Fit after a brief delay to ensure the container is sized.
    setTimeout(() => {
      try { fitAddon.fit(); } catch {}
    }, 100);

    term.writeln("\x1b[36mConnecting to terminal session...\x1b[0m");

    // Connect WebSocket.
    const baseUrl = window.location.origin.replace(/^http/, "ws");
    const wsUrl = `${baseUrl}/ws/terminal/${sessionId}?role=browser&browser_token=${encodeURIComponent(browserToken)}`;
    const ws = new WebSocket(wsUrl);
    ws.binaryType = "arraybuffer";
    wsRef.current = ws;

    ws.onopen = () => {
      setStatus("connected");
      term.writeln("\x1b[32mConnected.\x1b[0m\r\n");

      // Send initial size.
      const resizeMsg = JSON.stringify({
        type: "resize",
        cols: term.cols,
        rows: term.rows,
      });
      ws.send(resizeMsg);
    };

    ws.onmessage = (event) => {
      if (event.data instanceof ArrayBuffer) {
        term.write(new Uint8Array(event.data));
      } else {
        term.write(event.data);
      }
    };

    ws.onclose = () => {
      setStatus("disconnected");
      term.writeln("\r\n\x1b[31mDisconnected.\x1b[0m");
      onDisconnect?.();
    };

    ws.onerror = () => {
      setStatus("disconnected");
      term.writeln("\r\n\x1b[31mWebSocket error.\x1b[0m");
    };

    // Terminal input -> WebSocket.
    const onData = term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(new TextEncoder().encode(data));
      }
    });

    // Handle resize.
    const onResize = term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: "resize", cols, rows }));
      }
    });

    // Window resize -> fit addon.
    const handleWindowResize = () => {
      try { fitAddon.fit(); } catch {}
    };
    window.addEventListener("resize", handleWindowResize);

    // ResizeObserver for container size changes.
    const resizeObserver = new ResizeObserver(() => {
      try { fitAddon.fit(); } catch {}
    });
    if (termRef.current) {
      resizeObserver.observe(termRef.current);
    }

    return () => {
      window.removeEventListener("resize", handleWindowResize);
      resizeObserver.disconnect();
      onData.dispose();
      onResize.dispose();
      cleanup();
    };
  }, [sessionId, browserToken, onDisconnect, cleanup]);

  return (
    <div className="relative w-full h-full">
      {status === "connecting" && (
        <div className="absolute inset-0 flex items-center justify-center z-10 bg-blox-bg/80">
          <div className="flex items-center gap-2 text-blox-muted text-sm">
            <div className="w-3 h-3 border-2 border-blox-blue border-t-transparent rounded-full animate-spin" />
            Connecting...
          </div>
        </div>
      )}
      <div
        ref={termRef}
        className="w-full h-full"
        style={{ padding: "4px" }}
      />
    </div>
  );
}
