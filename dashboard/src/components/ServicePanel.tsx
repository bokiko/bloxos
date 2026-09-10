"use client";

// systemd units for one machine.
//
// Monoform: the same panel + table the rest of the product uses, so a service
// list and an agent list read identically. State is a dot plus the word, never
// the dot alone. Controls are hidden without `fleet.control` — the hub
// enforces the same scope, so this only removes guaranteed-failing
// affordances.

import { useState, useMemo } from "react";
import { Layers, RotateCcw, Play, Square, Loader2 } from "lucide-react";
import { useToast } from "./Toast";
import { useAuth } from "@/contexts/AuthContext";
import {
  DropdownMenu, DropdownMenuTrigger, DropdownMenuContent,
  DropdownMenuItem, DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
import { StatusCell, type MonoformTone } from "@/components/MonoformStatus";
import { getStoredToken } from "@/lib/session";
import { commandFeedback } from "@/lib/command-feedback.mjs";
import { MF_MENU, MF_MENU_ITEM, MF_PANEL_HEAD, MF_PANEL_TITLE } from "@/lib/monoform-classes";

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

const ROW_ACTION =
  "ml-auto grid h-8 w-8 place-items-center rounded-lg text-text-tertiary transition-colors " +
  "hover:bg-surface-elevated hover:text-text-primary";

function statusTone(status: string): MonoformTone {
  if (status === "active") return "ok";
  if (status === "failed") return "critical";
  return "neutral";
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
  // Viewers keep the read-only list; service controls require fleet.control
  // (the hub enforces the same scope — this only removes guaranteed-failing
  // affordances).
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
        body: JSON.stringify({ type, target: service.name }),
      });
      const data = await res.json();
      const verb = type === "stop_service" ? "stopped" : type === "start_service" ? "started" : "restarted";
      const feedback = commandFeedback(res.ok, data, `${service.name} ${verb}`);
      addToast(feedback.type, feedback.message);
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

  if (service.status === "inactive") {
    return (
      <button
        type="button"
        onClick={() => runCommand("start_service")}
        className={ROW_ACTION}
        title={`Start ${service.name}`}
        aria-label={`Start ${service.name}`}
      >
        <Play className="w-3.5 h-3.5" />
      </button>
    );
  }

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <button type="button" aria-label={`Actions for ${service.name}`} className={ROW_ACTION}>
            <RotateCcw className="w-3.5 h-3.5" />
          </button>
        }
      />
      <DropdownMenuContent align="end" className={`${MF_MENU} min-w-[140px]`}>
        <DropdownMenuItem onClick={() => runCommand("restart_service")} className={MF_MENU_ITEM}>
          <RotateCcw className="w-3.5 h-3.5" aria-hidden /> Restart
        </DropdownMenuItem>
        <DropdownMenuItem onClick={() => runCommand("start_service")} className={MF_MENU_ITEM}>
          <Play className="w-3.5 h-3.5" aria-hidden /> Start
        </DropdownMenuItem>
        <DropdownMenuSeparator className="bg-border-subtle" />
        <DropdownMenuItem
          onClick={() => runCommand("stop_service")}
          className={`${MF_MENU_ITEM} text-status-critical!`}
        >
          <Square className="w-3.5 h-3.5" aria-hidden /> Stop
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
    <section className="mf-panel overflow-hidden">
      <div className={MF_PANEL_HEAD}>
        <h2 className={MF_PANEL_TITLE}>Services</h2>
        <span className="mf-kicker">
          {services.length} unit{services.length === 1 ? "" : "s"}
        </span>
      </div>
      {sorted.length === 0 ? (
        <div className="flex flex-col items-center justify-center py-12 text-text-tertiary">
          <Layers className="mb-2 h-7 w-7 opacity-30" aria-hidden />
          <p className="text-[13px]">No services reported</p>
        </div>
      ) : (
        <div className="mf-table-wrap max-h-[520px] overflow-x-auto overflow-y-auto">
          <table className="mf-table">
            <thead className="sticky top-0 z-10">
              <tr>
                <th>Unit</th>
                <th>State</th>
                <th className="hidden lg:table-cell">Description</th>
                <th className="text-right">Actions</th>
              </tr>
            </thead>
            <tbody>
              {sorted.map((s) => (
                <tr key={s.name}>
                  <td className="font-mono text-[13px] text-text-primary">{s.name}</td>
                  <td>
                    <StatusCell tone={statusTone(s.status)} label={s.status} />
                  </td>
                  <td
                    className="hidden max-w-[420px] truncate text-[12px] text-text-tertiary lg:table-cell"
                    title={s.description || undefined}
                  >
                    {s.description || "—"}
                  </td>
                  <td>
                    <div className="flex justify-end">
                      <ServiceActions service={s} machineId={machineId} hubUrl={hubUrl} />
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
