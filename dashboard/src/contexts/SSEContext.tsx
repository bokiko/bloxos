"use client";

import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  useCallback,
  ReactNode,
} from "react";
import { MachineMetrics, AlertData } from "@/lib/demo-data";
import {
  AUTH_CHANGED_EVENT,
  HUB_URL,
  getStoredToken,
} from "@/lib/session";

interface SSEContextType {
  machines: MachineMetrics[];
  getMachine: (id: string) => MachineMetrics | undefined;
  connected: boolean;
  hasReceivedData: boolean;
  alertCount: number;
  alerts: AlertData[];
  setAlerts: React.Dispatch<React.SetStateAction<AlertData[]>>;
  setAlertCount: React.Dispatch<React.SetStateAction<number>>;
}

const SSEContext = createContext<SSEContextType | null>(null);

export function useSSE() {
  const ctx = useContext(SSEContext);
  if (!ctx) throw new Error("useSSE must be used within SSEProvider");
  return ctx;
}

// Fetch a short-lived SSE-scoped token (Finding #8).
async function fetchSSEToken(): Promise<string | null> {
  const apiToken = typeof window !== "undefined" ? localStorage.getItem("bloxos_token") : null;
  if (!apiToken) return null;
  try {
    const res = await fetch(`${HUB_URL}/api/auth/sse-token`, {
      method: "POST",
      headers: { Authorization: `Bearer ${apiToken}` },
    });
    if (!res.ok) return null;
    const data = await res.json();
    return data.token || null;
  } catch {
    return null;
  }
}

export function SSEProvider({ children }: { children: ReactNode }) {
  const [machineMap, setMachineMap] = useState<Map<string, MachineMetrics>>(
    new Map()
  );
  const [connected, setConnected] = useState(false);
  const [hasReceivedData, setHasReceivedData] = useState(false);
  const [alertCount, setAlertCount] = useState(0);
  const [alerts, setAlerts] = useState<AlertData[]>([]);
  const esRef = useRef<EventSource | null>(null);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const backoffRef = useRef(3000);
  const mountedRef = useRef(true);
  // Refresh SSE token before it expires (every 4 min for a 5-min token).
  const sseTokenRefreshTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const connectRef = useRef<() => void>(() => {});

  const disconnect = useCallback((clearData = false) => {
    esRef.current?.close();
    esRef.current = null;
    if (reconnectTimer.current) clearTimeout(reconnectTimer.current);
    if (sseTokenRefreshTimer.current) clearTimeout(sseTokenRefreshTimer.current);
    setConnected(false);
    if (clearData) {
      setHasReceivedData(false);
      setMachineMap(new Map());
      setAlerts([]);
      setAlertCount(0);
    }
  }, []);

  const connect = useCallback(async () => {
    if (!mountedRef.current) return;

    disconnect();
    const token = getStoredToken();

    // Don't connect if no token.
    if (!token) return;

    // Get SSE-scoped token (Finding #8).
    const sseToken = await fetchSSEToken();
    if (!sseToken) {
      reconnectTimer.current = setTimeout(() => {
        backoffRef.current = Math.min(backoffRef.current * 2, 30000);
        connectRef.current();
      }, backoffRef.current);
      return;
    }

    const sseUrl = `${HUB_URL}/api/events?token=${encodeURIComponent(sseToken)}`;

    let es: EventSource;
    try {
      es = new EventSource(sseUrl);
    } catch {
      reconnectTimer.current = setTimeout(() => {
        backoffRef.current = Math.min(backoffRef.current * 2, 30000);
        connectRef.current();
      }, backoffRef.current);
      return;
    }
    esRef.current = es;

    es.onopen = () => {
      if (!mountedRef.current) return;
      setConnected(true);
      backoffRef.current = 3000;

      if (sseTokenRefreshTimer.current) clearTimeout(sseTokenRefreshTimer.current);
      sseTokenRefreshTimer.current = setTimeout(() => {
        if (mountedRef.current) {
          connectRef.current();
        }
      }, 4 * 60 * 1000);
    };

    es.addEventListener("snapshot", (event) => {
      if (!mountedRef.current) return;
      try {
        const list = JSON.parse(event.data) as MachineMetrics[];
        setHasReceivedData(true);
        setMachineMap(() => {
          const next = new Map<string, MachineMetrics>();
          for (const m of list) {
            next.set(m.machine_id, { ...m, last_seen: Date.now() });
          }
          return next;
        });
      } catch {
        // ignore parse errors
      }
    });

    es.addEventListener("metrics", (event) => {
      if (!mountedRef.current) return;
      try {
        const m = JSON.parse(event.data) as MachineMetrics;
        setHasReceivedData(true);
        setMachineMap((prev) => {
          const next = new Map(prev);
          next.set(m.machine_id, { ...m, last_seen: Date.now() });
          return next;
        });
      } catch {
        // ignore parse errors
      }
    });

    es.addEventListener("services", (event) => {
      if (!mountedRef.current) return;
      try {
        const msg = JSON.parse(event.data);
        if (msg.machine_id && msg.services) {
          setMachineMap((prev) => {
            const existing = prev.get(msg.machine_id);
            if (!existing) return prev;
            const next = new Map(prev);
            next.set(msg.machine_id, {
              ...existing,
              ...({ _services: msg.services } as Record<string, unknown>),
            } as MachineMetrics);
            return next;
          });
        }
      } catch {
        // ignore
      }
    });

    es.addEventListener("containers", (event) => {
      if (!mountedRef.current) return;
      try {
        const msg = JSON.parse(event.data);
        if (msg.machine_id && msg.containers) {
          setMachineMap((prev) => {
            const existing = prev.get(msg.machine_id);
            if (!existing) return prev;
            const next = new Map(prev);
            next.set(msg.machine_id, {
              ...existing,
              ...({ _containers: msg.containers } as Record<string, unknown>),
            } as MachineMetrics);
            return next;
          });
        }
      } catch {
        // ignore
      }
    });

    es.addEventListener("alert_count", (event) => {
      if (!mountedRef.current) return;
      try {
        const data = JSON.parse(event.data);
        setAlertCount(data.count);
      } catch {
        // ignore
      }
    });

    es.addEventListener("alert", (event) => {
      if (!mountedRef.current) return;
      try {
        const alert = JSON.parse(event.data) as AlertData;
        if (alert.status === "active") {
          setAlerts((prev) => [alert, ...prev]);
          setAlertCount((prev) => prev + 1);
        } else if (alert.status === "resolved") {
          setAlerts((prev) => prev.filter((a) => a.id !== alert.id));
          setAlertCount((prev) => Math.max(0, prev - 1));
        }
      } catch {
        // ignore
      }
    });

    es.onerror = () => {
      if (!mountedRef.current) return;
      setConnected(false);
      es.close();
      esRef.current = null;

      const delay = backoffRef.current;
      backoffRef.current = Math.min(backoffRef.current * 2, 30000);
      reconnectTimer.current = setTimeout(() => connectRef.current(), delay);
    };
  }, [disconnect]);

  useEffect(() => {
    connectRef.current = () => {
      void connect();
    };
  }, [connect]);

  useEffect(() => {
    mountedRef.current = true;
    connectRef.current();

    // Fetch active alerts on mount
    const token = getStoredToken();
    if (token) {
      fetch(`${HUB_URL}/api/alerts`, {
        headers: { Authorization: `Bearer ${token}` },
      })
        .then((r) => r.json())
        .then((data) => {
          if (!Array.isArray(data)) return;
          if (mountedRef.current) {
            setAlerts(data);
            setAlertCount(data.length);
          }
        })
        .catch(() => {});
    }

    const onStorage = (e: StorageEvent) => {
      if (e.key === "bloxos_token") {
        backoffRef.current = 3000;
        connectRef.current();
      }
    };
    const onAuthChanged = () => {
      backoffRef.current = 3000;
      if (getStoredToken()) {
        connectRef.current();
      } else {
        disconnect(true);
      }
    };
    window.addEventListener("storage", onStorage);
    window.addEventListener(AUTH_CHANGED_EVENT, onAuthChanged);

    return () => {
      mountedRef.current = false;
      window.removeEventListener("storage", onStorage);
      window.removeEventListener(AUTH_CHANGED_EVENT, onAuthChanged);
      disconnect();
    };
  }, [disconnect]);

  const getMachine = useCallback(
    (id: string) => machineMap.get(id),
    [machineMap]
  );

  const machines = Array.from(machineMap.values());

  return (
    <SSEContext.Provider
      value={{
        machines,
        getMachine,
        connected,
        hasReceivedData,
        alertCount,
        alerts,
        setAlerts,
        setAlertCount,
      }}
    >
      {children}
    </SSEContext.Provider>
  );
}
