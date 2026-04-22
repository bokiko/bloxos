"use client";

import { useState, useRef, useEffect, useMemo } from "react";
import { Layers, RotateCcw, Play, Square, Loader2, ChevronDown } from "lucide-react";
import { useToast } from "./Toast";
import {
  DropdownMenu, DropdownMenuTrigger, DropdownMenuContent,
  DropdownMenuItem, DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
import { Button } from "@/components/ui/button";

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
  if (status === "active") return "bg-emerald-500";
  if (status === "failed") return "bg-red-500";
  return "bg-blox-muted/50";
}

function statusLabel(status: string) {
  if (status === "active") return "text-emerald-400";
  if (status === "failed") return "text-red-400";
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
  const { addToast } = useToast();

  async function runCommand(type: string) {
    setLoading(true);
    try {
      const token = localStorage.getItem("bloxos_token");
      const headers: Record<string, string> = { "Content-Type": "application/json" };
      if (token) headers["Authorization"] = `Bearer ${token}`;
      const res = await fetch(`${hubUrl}/api/machines/${machineId}/command`, {
        method: "POST",
        headers,
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
      <Button
        variant="ghost"
        size="icon-xs"
        onClick={() => runCommand("start_service")}
        className="text-emerald-400 hover:bg-emerald-500/10"
        title="Start"
      >
        <Play className="w-3 h-3" />
      </Button>
    );
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button variant="ghost" size="icon-xs" className="text-blox-blue hover:bg-blox-blue/10">
            <RotateCcw className="w-3 h-3" />
          </Button>
        }
      />
      <DropdownMenuContent align="end" className="bg-blox-card border-blox-border min-w-[120px]">
        <DropdownMenuItem onClick={() => runCommand("restart_service")} className="text-xs gap-2 text-blox-text">
          <RotateCcw className="w-3 h-3" /> Restart
        </DropdownMenuItem>
        <DropdownMenuSeparator className="bg-blox-border" />
        <DropdownMenuItem onClick={() => runCommand("stop_service")} className="text-xs gap-2 text-red-400">
          <Square className="w-3 h-3" /> Stop
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => runCommand("start_service")} className="text-xs gap-2 text-emerald-400">
          <Play className="w-3 h-3" /> Start
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
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
    <div className="bg-blox-card border border-blox-border rounded-xl p-5">
      <div className="flex items-center justify-between mb-4">
        <div className="flex items-center gap-2.5">
          <div className="p-1.5 rounded-lg bg-blox-blue/10">
            <Layers className="w-3.5 h-3.5 text-blox-blue" />
          </div>
          <h3 className="text-sm font-semibold text-blox-text">Services</h3>
          <span className="text-[10px] text-blox-muted font-mono tabular-nums">({services.length})</span>
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
              className="flex items-center justify-between px-2 py-2 rounded-lg hover:bg-blox-border/20 group transition-colors"
            >
              <div className="flex items-center gap-2.5 min-w-0">
                <div className={`w-2 h-2 rounded-full flex-shrink-0 ${statusDot(s.status)} ${s.status === "active" ? "shadow-sm shadow-emerald-500/50" : ""}`} />
                <span className="text-xs text-blox-text font-mono truncate">{s.name}</span>
              </div>
              <div className="flex items-center gap-3">
                <span className={`text-[10px] font-medium ${statusLabel(s.status)}`}>{s.status}</span>
                <ServiceActions service={s} machineId={machineId} hubUrl={hubUrl} />
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
