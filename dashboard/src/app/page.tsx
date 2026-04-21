"use client";

import { useState, useMemo } from "react";
import { useFleetSSE } from "@/hooks/useFleetSSE";
import { demoMachines, MachineMetrics } from "@/lib/demo-data";
import { MachineCard } from "@/components/MachineCard";
import { MachineStatus } from "@/components/StatusBadge";
import { Bell, Plus, Wifi, WifiOff } from "lucide-react";

function getStatus(m: MachineMetrics): MachineStatus {
  const age = Date.now() - (m.last_seen || 0);
  if (age > 120_000) return "offline";
  if (m.gpu_temp && m.gpu_temp > 80) return "warning";
  const diskPct = m.disk_total_bytes > 0 ? (m.disk_used_bytes / m.disk_total_bytes) * 100 : 0;
  if (diskPct > 90) return "warning";
  const ramPct = m.ram_total_bytes > 0 ? (m.ram_used_bytes / m.ram_total_bytes) * 100 : 0;
  if (ramPct > 95) return "warning";
  return "online";
}

export default function Home() {
  const { machines: liveMachines, connected, hasReceivedData } = useFleetSSE();
  const [demoMode] = useState(true);

  const machines = hasReceivedData && liveMachines.length > 0
    ? liveMachines
    : demoMachines;

  const isDemo = !hasReceivedData || liveMachines.length === 0;

  const summary = useMemo(() => {
    let online = 0, warning = 0, offline = 0;
    for (const m of machines) {
      const s = getStatus(m);
      if (s === "online") online++;
      else if (s === "warning") warning++;
      else offline++;
    }
    return { online, warning, offline };
  }, [machines]);

  return (
    <div className="min-h-screen bg-blox-bg">
      {/* Header */}
      <header className="sticky top-0 z-50 bg-blox-bg/80 backdrop-blur-md border-b border-blox-border">
        <div className="max-w-[1600px] mx-auto px-4 sm:px-6 h-14 flex items-center justify-between">
          {/* Left: Logo */}
          <div className="flex items-center gap-3">
            <h1 className="text-lg font-bold tracking-tight">
              <span className="text-blox-blue">Blox</span>
              <span className="text-blox-text">OS</span>
            </h1>
            {isDemo && (
              <span className="text-[10px] px-2 py-0.5 rounded-full bg-blox-amber/10 text-blox-amber border border-blox-amber/20">
                Demo Mode
              </span>
            )}
          </div>

          {/* Center: Status summary */}
          <div className="hidden sm:flex items-center gap-4 text-xs">
            <span className="flex items-center gap-1.5">
              <span className="w-2 h-2 rounded-full bg-blox-green" />
              <span className="text-blox-muted">{summary.online} online</span>
            </span>
            {summary.warning > 0 && (
              <span className="flex items-center gap-1.5">
                <span className="w-2 h-2 rounded-full bg-blox-amber" />
                <span className="text-blox-muted">{summary.warning} warning</span>
              </span>
            )}
            {summary.offline > 0 && (
              <span className="flex items-center gap-1.5">
                <span className="w-2 h-2 rounded-full bg-blox-red" />
                <span className="text-blox-muted">{summary.offline} offline</span>
              </span>
            )}
          </div>

          {/* Right: Actions */}
          <div className="flex items-center gap-2">
            {/* Connection indicator */}
            <div className="flex items-center gap-1 mr-2">
              {connected ? (
                <Wifi className="w-3.5 h-3.5 text-blox-green" />
              ) : (
                <WifiOff className="w-3.5 h-3.5 text-blox-red" />
              )}
            </div>

            <button className="relative p-2 rounded-md hover:bg-blox-border/50 transition-colors">
              <Bell className="w-4 h-4 text-blox-muted" />
              <span className="absolute -top-0.5 -right-0.5 w-4 h-4 rounded-full bg-blox-red text-[9px] flex items-center justify-center text-white font-bold">
                2
              </span>
            </button>

            <button className="flex items-center gap-1.5 px-3 py-1.5 rounded-md bg-blox-blue/10 text-blox-blue text-xs hover:bg-blox-blue/20 transition-colors border border-blox-blue/20">
              <Plus className="w-3.5 h-3.5" />
              <span className="hidden sm:inline">Add Machine</span>
            </button>
          </div>
        </div>
      </header>

      {/* Grid */}
      <main className="max-w-[1600px] mx-auto px-4 sm:px-6 py-6">
        <div className="grid gap-4" style={{
          gridTemplateColumns: "repeat(auto-fill, minmax(280px, 1fr))"
        }}>
          {machines.map((m) => (
            <MachineCard
              key={m.machine_id}
              machine={m}
              onClick={() => console.log("Selected:", m.hostname)}
            />
          ))}
        </div>

        {machines.length === 0 && (
          <div className="flex flex-col items-center justify-center py-20 text-blox-muted">
            <Monitor className="w-12 h-12 mb-4 opacity-30" />
            <p className="text-sm">No machines connected</p>
            <p className="text-xs mt-1">Deploy an agent to get started</p>
          </div>
        )}
      </main>
    </div>
  );
}

function Monitor(props: React.SVGProps<SVGSVGElement>) {
  return (
    <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" {...props}>
      <rect width="20" height="14" x="2" y="3" rx="2"/>
      <line x1="8" x2="16" y1="21" y2="21"/>
      <line x1="12" x2="12" y1="17" y2="21"/>
    </svg>
  );
}
