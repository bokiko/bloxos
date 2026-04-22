"use client";

import { useState, useRef, useEffect, useMemo } from "react";
import { Layers, RotateCcw, Play, Square, Loader2, ChevronDown } from "lucide-react";
import { useToast } from "./Toast";

export interface Service {
  name: string;
  status: string;
  description: string;
}

interface ServicePanelProps {
  services: Service[];
  machineId: string;
  hubUrl: string;
}

const statusOrder: Record<string, number> = { failed: 0, active: 1, inactive: 2 };

function statusDot(status: string) {
  if (status === "active") return "bg-blox-green";
  if (status === "failed") return "bg-blox-red";
  return "bg-blox-muted";
}

function statusLabel(status: string) {
  if (status === "active") return "text-blox-green";
  if (status === "failed") return "text-blox-red";
  return "text-blox-muted";
}

function ServiceActions({
  service,
  machineId,
  hubUrl,
}: {
  service: Service;
  machineId: string;
  hubUrl: string;
}) {
  const [loading, setLoading] = useState(false);
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  const { addToast } = useToast();

  useEffect(() => {
    function handleClick(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    }
    document.addEventListener("mousedown", handleClick);
    return () => document.removeEventListener("mousedown", handleClick);
  }, []);

  async function runCommand(type: string) {
    setOpen(false);
    setLoading(true);
    try {
      const res = await fetch(`${hubUrl}/api/machines/${machineId}/command`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ type, target: service.name }),
      });
      const data = await res.json();
      if (data.success) {
        const verb = type.replace("_service", "").replace("_", " ");
        addToast("success", `${service.name} ${verb}ed`);
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

  if (service.status === "inactive") {
    return (
      <button
        onClick={() => runCommand("start_service")}
        className="flex items-center gap-1 px-2 py-1 rounded text-[10px] text-blox-green border border-blox-border hover:border-blox-green/40 hover:bg-blox-green/5 transition-colors"
        title="Start"
      >
        <Play className="w-3 h-3" />
        Start
      </button>
    );
  }

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen(!open)}
        className="flex items-center gap-1 px-2 py-1 rounded text-[10px] text-blox-blue border border-blox-border hover:border-blox-blue/40 hover:bg-blox-blue/5 transition-colors"
      >
        <RotateCcw className="w-3 h-3" />
        <ChevronDown className="w-2.5 h-2.5" />
      </button>
      {open && (
        <div className="absolute right-0 top-full mt-1 bg-blox-card border border-blox-border rounded-md shadow-xl z-50 min-w-[100px] py-1">
          <button
            onClick={() => runCommand("restart_service")}
            className="w-full text-left px-3 py-1.5 text-[11px] text-blox-text hover:bg-blox-border/50 flex items-center gap-2"
          >
            <RotateCcw className="w-3 h-3" /> Restart
          </button>
          <button
            onClick={() => runCommand("stop_service")}
            className="w-full text-left px-3 py-1.5 text-[11px] text-blox-red hover:bg-blox-border/50 flex items-center gap-2"
          >
            <Square className="w-3 h-3" /> Stop
          </button>
          <button
            onClick={() => runCommand("start_service")}
            className="w-full text-left px-3 py-1.5 text-[11px] text-blox-green hover:bg-blox-border/50 flex items-center gap-2"
          >
            <Play className="w-3 h-3" /> Start
          </button>
        </div>
      )}
    </div>
  );
}

export function ServicePanel({ services, machineId, hubUrl }: ServicePanelProps) {
  const sorted = useMemo(() => {
    return [...services].sort((a, b) => {
      const oa = statusOrder[a.status] ?? 3;
      const ob = statusOrder[b.status] ?? 3;
      if (oa !== ob) return oa - ob;
      return a.name.localeCompare(b.name);
    });
  }, [services]);

  return (
    <div className="bg-blox-card border border-blox-border rounded-lg p-5">
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-2">
          <Layers className="w-4 h-4 text-blox-blue" />
          <h3 className="text-sm font-semibold text-blox-text">Services</h3>
          <span className="text-[10px] text-blox-muted">({services.length})</span>
        </div>
      </div>
      {sorted.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-8 text-blox-muted">
          <Layers className="w-8 h-8 mb-2 opacity-20" />
          <p className="text-xs">No services reported</p>
        </div>
      ) : (
        <div className="space-y-0.5 max-h-[400px] overflow-y-auto">
          {sorted.map((s) => (
            <div
              key={s.name}
              className="flex items-center justify-between px-2 py-1.5 rounded hover:bg-blox-border/20 group"
            >
              <div className="flex items-center gap-2.5 min-w-0">
                <div className={`w-2 h-2 rounded-full flex-shrink-0 ${statusDot(s.status)}`} />
                <span className="text-xs text-blox-text font-mono truncate">{s.name}</span>
              </div>
              <div className="flex items-center gap-3">
                <span className={`text-[10px] ${statusLabel(s.status)}`}>{s.status}</span>
                <ServiceActions service={s} machineId={machineId} hubUrl={hubUrl} />
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
