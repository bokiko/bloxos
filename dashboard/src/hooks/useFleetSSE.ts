"use client";

import { useEffect, useRef, useState, useCallback } from "react";
import { MachineMetrics, AlertData } from "@/lib/demo-data";

const HUB_URL =
  process.env.NEXT_PUBLIC_HUB_URL || "http://localhost:4000";

export function useFleetSSE() {
  const [machines, setMachines] = useState<Map<string, MachineMetrics>>(
    new Map()
  );
  const [connected, setConnected] = useState(false);
  const [hasReceivedData, setHasReceivedData] = useState(false);
  const [alertCount, setAlertCount] = useState(0);
  const [alerts, setAlerts] = useState<AlertData[]>([]);
  const esRef = useRef<EventSource | null>(null);
  const reconnectTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const connect = useCallback(() => {
    if (esRef.current) {
      esRef.current.close();
    }

    const es = new EventSource(`${HUB_URL}/api/events`);
    esRef.current = es;

    es.onopen = () => {
      setConnected(true);
    };

    es.addEventListener("snapshot", (event) => {
      try {
        const list = JSON.parse(event.data) as MachineMetrics[];
        if (list.length > 0) {
          setHasReceivedData(true);
          setMachines(() => {
            const next = new Map<string, MachineMetrics>();
            for (const m of list) {
              next.set(m.machine_id, { ...m, last_seen: Date.now() });
            }
            return next;
          });
        }
      } catch {
        // ignore parse errors
      }
    });

    es.addEventListener("metrics", (event) => {
      try {
        const m = JSON.parse(event.data) as MachineMetrics;
        setHasReceivedData(true);
        setMachines((prev) => {
          const next = new Map(prev);
          next.set(m.machine_id, { ...m, last_seen: Date.now() });
          return next;
        });
      } catch {
        // ignore parse errors
      }
    });

    es.addEventListener("alert_count", (event) => {
      try {
        const data = JSON.parse(event.data);
        setAlertCount(data.count);
      } catch {
        // ignore
      }
    });

    es.addEventListener("alert", (event) => {
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
      setConnected(false);
      es.close();
      esRef.current = null;
      reconnectTimer.current = setTimeout(connect, 3000);
    };
  }, []);

  useEffect(() => {
    connect();

    // Fetch active alerts on mount.
    fetch(`${HUB_URL}/api/alerts`)
      .then((r) => r.json())
      .then((data: AlertData[]) => {
        setAlerts(data);
        setAlertCount(data.length);
      })
      .catch(() => {});

    return () => {
      esRef.current?.close();
      if (reconnectTimer.current) clearTimeout(reconnectTimer.current);
    };
  }, [connect]);

  return {
    machines: Array.from(machines.values()),
    connected,
    hasReceivedData,
    alertCount,
    alerts,
    setAlerts,
    setAlertCount,
  };
}
