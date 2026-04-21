"use client";

import { useEffect, useRef, useState, useCallback } from "react";
import { MachineMetrics } from "@/lib/demo-data";

const HUB_URL =
  process.env.NEXT_PUBLIC_HUB_URL || "http://localhost:4000";

export function useFleetSSE() {
  const [machines, setMachines] = useState<Map<string, MachineMetrics>>(
    new Map()
  );
  const [connected, setConnected] = useState(false);
  const [hasReceivedData, setHasReceivedData] = useState(false);
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
        const list: MachineMetrics[] = JSON.parse(event.data);
        if (list.length > 0) {
          setHasReceivedData(true);
          setMachines((prev) => {
            const next = new Map(prev);
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
        const m: MachineMetrics = JSON.parse(event.data);
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

    es.onerror = () => {
      setConnected(false);
      es.close();
      esRef.current = null;
      // Reconnect after 3 seconds
      reconnectTimer.current = setTimeout(connect, 3000);
    };
  }, []);

  useEffect(() => {
    connect();
    return () => {
      esRef.current?.close();
      if (reconnectTimer.current) clearTimeout(reconnectTimer.current);
    };
  }, [connect]);

  return {
    machines: Array.from(machines.values()),
    connected,
    hasReceivedData,
  };
}
