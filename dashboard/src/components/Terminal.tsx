"use client";

import { useEffect, useRef, useCallback, useState } from "react";
import { Terminal as XTerm, ITheme } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { useTheme, type AppearanceMode } from "@/contexts/ThemeContext";
import { getHubWsBaseUrl } from "@/lib/session";
import "@xterm/xterm/css/xterm.css";

interface TerminalProps {
  sessionId: string;
  browserToken: string;
  onDisconnect?: () => void;
}

/* ============================================================================
 * Terminal palette
 *
 * The terminal follows the product theme, drawn from the same tokens the rest
 * of the UI uses, so terminal output sits in the same colour world as the
 * panel around it. ANSI semantics are preserved in both — red errors stay red,
 * green diffs stay green — but the light palette is a real light terminal, not
 * the dark one on a pale background: every hue is the darker, light-ground
 * member of its family, and the ANSI "bright" variants get DARKER rather than
 * lighter, because on white it is depth, not lift, that reads as emphasis.
 * ============================================================================ */

// Mirrors the `--mf-*` tokens in app/monoform.css. xterm.js needs concrete
// values, so the two token blocks are duplicated here. Cyan has no Monoform
// token — it is derived, because collapsing it onto blue would cost the ANSI
// distinction that makes terminal output readable.
const PALETTES: Record<AppearanceMode, ITheme> = {
  light: {
    // The well is the table-head tone rather than pure white, so the terminal
    // still reads as recessed inside a white panel.
    background: "#f7f7f5",
    foreground: "#191918",
    cursor: "#3149d9",
    selectionBackground: "rgba(49, 73, 217, 0.20)",
    black: "#191918",
    red: "#c62828",
    green: "#0d7346",
    yellow: "#8a5300",
    blue: "#3149d9",
    magenta: "#6244c4",
    cyan: "#0d6a72",
    // ANSI "white" is a foreground, not the page: on a light ground it has to
    // become a readable grey, and brightWhite the near-black emphasis tone.
    white: "#67675f",
    brightBlack: "#84847d",
    brightRed: "#9c1c1c",
    brightGreen: "#095b37",
    brightYellow: "#6b4000",
    brightBlue: "#2739b4",
    brightMagenta: "#4d329f",
    brightCyan: "#095157",
    brightWhite: "#191918",
  },
  dark: {
    background: "#090909",
    foreground: "#f4f4f2",
    cursor: "#708bff",
    selectionBackground: "rgba(112, 139, 255, 0.28)",
    black: "#090909",
    red: "#ea7474",
    green: "#63c695",
    yellow: "#edaa57",
    blue: "#708bff",
    magenta: "#aa97ed",
    cyan: "#6cc2ce",
    white: "#f4f4f2",
    brightBlack: "#969691",
    brightRed: "#f08a8a",
    brightGreen: "#7bd0a4",
    brightYellow: "#f2bb74",
    brightBlue: "#8b9fff",
    brightMagenta: "#b6a6f0",
    brightCyan: "#82cdd7",
    brightWhite: "#ffffff",
  },
};

function terminalTheme(appearance: AppearanceMode): ITheme {
  return PALETTES[appearance] ?? PALETTES.dark;
}

export function Terminal({ sessionId, browserToken, onDisconnect }: TerminalProps) {
  const termRef = useRef<HTMLDivElement>(null);
  const xtermRef = useRef<XTerm | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const [status, setStatus] = useState<"connecting" | "connected" | "disconnected">("connecting");
  // The terminal repaints live when the theme changes — see the effect below.
  const { appearance } = useTheme();

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

  // Initial mount + WebSocket lifecycle. Theme is set on init using the
  // resolved theme; live theme switches are handled by a separate effect.
  useEffect(() => {
    if (!termRef.current || !sessionId) return;

    const term = new XTerm({
      cursorBlink: true,
      fontSize: 14,
      fontFamily:
        "'JetBrains Mono', 'Fira Code', 'Cascadia Code', 'Menlo', monospace",
      theme: terminalTheme(appearance),
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

    setTimeout(() => {
      try {
        fitAddon.fit();
      } catch {}
    }, 100);

    term.writeln("\x1b[36mConnecting to terminal session…\x1b[0m");

    // Use the hub base URL (HUB_URL when set), not the dashboard's own origin,
    // so the terminal works in split-origin deployments where the dashboard
    // and hub are served from different hosts.
    const baseUrl = getHubWsBaseUrl();
    const wsUrl = `${baseUrl}/ws/terminal/${sessionId}?role=browser&browser_token=${encodeURIComponent(
      browserToken
    )}`;
    const ws = new WebSocket(wsUrl);
    ws.binaryType = "arraybuffer";
    wsRef.current = ws;

    ws.onopen = () => {
      setStatus("connected");
      term.writeln("\x1b[32mConnected.\x1b[0m\r\n");
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

    const onData = term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(new TextEncoder().encode(data));
      }
    });

    const onResize = term.onResize(({ cols, rows }) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: "resize", cols, rows }));
      }
    });

    const handleWindowResize = () => {
      try {
        fitAddon.fit();
      } catch {}
    };
    window.addEventListener("resize", handleWindowResize);

    const resizeObserver = new ResizeObserver(() => {
      try {
        fitAddon.fit();
      } catch {}
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
    // The dependency on `appearance` is intentionally omitted here: changing
    // the appearance should NOT re-create the WebSocket. Live palette updates
    // are handled by the separate effect below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, browserToken, onDisconnect, cleanup]);

  // Live appearance updates — swap the palette without re-creating the terminal.
  useEffect(() => {
    if (!xtermRef.current) return;
    xtermRef.current.options.theme = terminalTheme(appearance);
  }, [appearance]);

  return (
    <div className="relative w-full h-full">
      {status === "connecting" && (
        <div className="absolute inset-0 flex items-center justify-center z-10 bg-blox-bg/80 backdrop-blur-sm">
          <div className="flex items-center gap-2 text-blox-muted text-sm">
            <div className="w-3 h-3 border-2 border-blox-blue border-t-transparent rounded-full animate-spin" />
            Connecting…
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
