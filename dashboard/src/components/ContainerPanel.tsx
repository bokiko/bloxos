"use client";

import { useState, useMemo } from "react";
import { Box, RotateCcw, Play, Loader2 } from "lucide-react";
import { useToast } from "./Toast";
import { Button } from "@/components/ui/button";
import { getStoredToken } from "@/lib/session";

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

function statusDot(status: string) {
  if (status === "running") return "bg-emerald-500";
  if (status === "dead" || status === "removing") return "bg-red-500";
  return "bg-blox-muted/50";
}

function statusLabel(status: string) {
  if (status === "running") return "text-emerald-400";
  if (status === "dead") return "text-red-400";
  return "text-blox-muted";
}

function truncateImage(image: string): string {
  const parts = image.split("/");
  const last = parts[parts.length - 1];
  return last.length > 20 ? last.slice(0, 20) + "..." : last;
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
    return <Loader2 className="w-3.5 h-3.5 text-blox-blue animate-spin" />;
  }

  if (container.status === "running") {
    return (
      <Button
        variant="ghost"
        size="icon-xs"
        onClick={() => runCommand("restart_container")}
        className="text-blox-blue hover:bg-blox-blue/10"
        title="Restart"
      >
        <RotateCcw className="w-3 h-3" />
      </Button>
    );
  }

  return (
    <Button
      variant="ghost"
      size="icon-xs"
      onClick={() => runCommand("start_container")}
      className="text-emerald-400 hover:bg-emerald-500/10"
      title="Start"
    >
      <Play className="w-3 h-3" />
    </Button>
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
    <div className="bg-blox-card border border-blox-border rounded-xl p-5">
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-2.5">
          <div className="p-1.5 rounded-lg bg-blox-blue/10">
            <Box className="w-3.5 h-3.5 text-blox-blue" />
          </div>
          <h3 className="text-sm font-semibold text-blox-text">Docker Containers</h3>
          <span className="text-[10px] text-blox-muted font-mono tabular-nums">({containers.length})</span>
        </div>
      </div>
      {sorted.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-8 text-blox-muted">
          <Box className="w-8 h-8 mb-2 opacity-20" />
          <p className="text-xs">No containers reported</p>
        </div>
      ) : (
        <div className="space-y-0.5 max-h-[400px] overflow-y-auto">
          {sorted.map((c) => (
            <div
              key={c.id || c.name}
              className="flex items-center justify-between px-2 py-2 rounded-lg hover:bg-blox-border/20 group transition-colors"
            >
              <div className="flex items-center gap-2.5 min-w-0">
                <div className={`w-2 h-2 rounded-full flex-shrink-0 ${statusDot(c.status)} ${c.status === "running" ? "shadow-sm shadow-emerald-500/50" : ""}`} />
                <span className="text-xs text-blox-text font-mono truncate">{c.name}</span>
              </div>
              <div className="flex items-center gap-3">
                <span className={`text-[10px] font-medium ${statusLabel(c.status)}`}>{c.status}</span>
                <span className="text-[10px] text-blox-muted font-mono hidden sm:inline">{truncateImage(c.image)}</span>
                <ContainerActions container={c} machineId={machineId} hubUrl={hubUrl} />
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
