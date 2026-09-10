"use client";

// Docker containers for one machine. Same panel + table as ServicePanel, on
// purpose: two lists of running things should not look like two products.

import { useState, useMemo } from "react";
import { Box, RotateCcw, Play, Loader2 } from "lucide-react";
import { useToast } from "./Toast";
import { useAuth } from "@/contexts/AuthContext";
import { StatusCell, type MonoformTone } from "@/components/MonoformStatus";
import { getStoredToken } from "@/lib/session";
import { MF_PANEL_HEAD, MF_PANEL_TITLE } from "@/lib/monoform-classes";

export interface Container {
  id: string;
  name: string;
  status: string;
  image: string;
}

interface ContainerPanelProps {
  containers: Container[];
  machineId: string;
  hubUrl: string;
}

const ROW_ACTION =
  "ml-auto grid h-8 w-8 place-items-center rounded-lg text-text-tertiary transition-colors " +
  "hover:bg-surface-elevated hover:text-text-primary";

function statusTone(status: string): MonoformTone {
  if (status === "running") return "ok";
  if (status === "dead" || status === "removing") return "critical";
  return "neutral";
}

function ContainerActions({
  container,
  machineId,
  hubUrl,
}: {
  container: Container;
  machineId: string;
  hubUrl: string;
}) {
  const [loading, setLoading] = useState(false);
  const { addToast } = useToast();
  // Viewers keep the read-only list; container controls require
  // fleet.control (the hub enforces the same scope).
  const { hasScope } = useAuth();
  const canControl = hasScope("fleet.control");

  async function runCommand(type: string) {
    setLoading(true);
    try {
      const token = getStoredToken();
      const headers: Record<string, string> = { "Content-Type": "application/json" };
      if (token) headers["Authorization"] = `Bearer ${token}`;
      const res = await fetch(`${hubUrl}/api/machines/${machineId}/command`, {
        method: "POST",
        headers,
        body: JSON.stringify({ type, target: container.name }),
      });
      const data = await res.json();
      if (data.success) {
        const verb = type === "restart_container" ? "restarted" : "started";
        addToast("success", `${container.name} ${verb}`);
      } else {
        addToast("error", `Failed: ${data.error || data.output || "unknown error"}`);
      }
    } catch (err) {
      addToast("error", `Network error: ${err instanceof Error ? err.message : "unknown"}`);
    } finally {
      setLoading(false);
    }
  }

  if (loading) {
    return <Loader2 className="ml-auto w-3.5 h-3.5 text-accent animate-spin" aria-label="Working" />;
  }

  if (!canControl) {
    return null;
  }

  if (container.status === "running") {
    return (
      <button
        type="button"
        onClick={() => runCommand("restart_container")}
        className={ROW_ACTION}
        title={`Restart ${container.name}`}
        aria-label={`Restart ${container.name}`}
      >
        <RotateCcw className="w-3.5 h-3.5" />
      </button>
    );
  }

  return (
    <button
      type="button"
      onClick={() => runCommand("start_container")}
      className={ROW_ACTION}
      title={`Start ${container.name}`}
      aria-label={`Start ${container.name}`}
    >
      <Play className="w-3.5 h-3.5" />
    </button>
  );
}

export function ContainerPanel({ containers, machineId, hubUrl }: ContainerPanelProps) {
  const sorted = useMemo(() => {
    return [...containers].sort((a, b) => {
      if (a.status === "running" && b.status !== "running") return -1;
      if (a.status !== "running" && b.status === "running") return 1;
      return a.name.localeCompare(b.name);
    });
  }, [containers]);

  return (
    <section className="mf-panel overflow-hidden">
      <div className={MF_PANEL_HEAD}>
        <h2 className={MF_PANEL_TITLE}>Docker containers</h2>
        <span className="mf-kicker">
          {containers.length} container{containers.length === 1 ? "" : "s"}
        </span>
      </div>
      {sorted.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-12 text-text-tertiary">
          <Box className="mb-2 h-7 w-7 opacity-30" aria-hidden />
          <p className="text-[13px]">No containers reported</p>
        </div>
      ) : (
        <div className="mf-table-wrap max-h-[520px] overflow-x-auto overflow-y-auto">
          <table className="mf-table">
            <thead className="sticky top-0 z-10">
              <tr>
                <th>Container</th>
                <th>State</th>
                <th className="hidden sm:table-cell">Image</th>
                <th className="text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((c) => (
                <tr key={c.id || c.name}>
                  <td className="font-mono text-[13px] text-text-primary">{c.name}</td>
                  <td>
                    <StatusCell tone={statusTone(c.status)} label={c.status} />
                  </td>
                  <td
                    className="hidden max-w-[320px] truncate font-mono text-[12px] text-text-tertiary sm:table-cell"
                    title={c.image}
                  >
                    {c.image}
                  </td>
                  <td>
                    <div className="flex justify-end">
                      <ContainerActions container={c} machineId={machineId} hubUrl={hubUrl} />
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
