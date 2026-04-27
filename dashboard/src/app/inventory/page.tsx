"use client";

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { motion } from "framer-motion";
import {
  ArrowLeft,
  RefreshCw,
  AlertTriangle,
  Box,
  Cpu,
  MemoryStick,
  HardDrive,
  Gpu,
  Network,
} from "lucide-react";
import { useInventory } from "@/contexts/InventoryContext";
import { useAuth } from "@/contexts/AuthContext";
import { Button } from "@/components/ui/button";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Badge } from "@/components/ui/badge";
import { InventorySummaryCards } from "@/components/InventorySummaryCards";
import { InventoryTable } from "@/components/InventoryTable";
import { InventoryExportMenu } from "@/components/InventoryExportMenu";
import { resolveView, type InventoryView } from "@/lib/inventory-utils";

const VIEWS: readonly InventoryView[] = [
  "machines",
  "cpus",
  "memory",
  "disks",
  "gpus",
  "nics",
] as const;

function isValidView(v: string | null): v is InventoryView {
  return v !== null && (VIEWS as readonly string[]).includes(v);
}

function viewLabel(v: InventoryView): string {
  return {
    machines: "Machines",
    cpus: "CPU",
    memory: "Memory",
    disks: "Storage",
    gpus: "GPU",
    nics: "Network",
  }[v];
}

function viewIcon(v: InventoryView) {
  const cls = "w-4 h-4";
  switch (v) {
    case "machines":
      return <Box className={cls} />;
    case "cpus":
      return <Cpu className={cls} />;
    case "memory":
      return <MemoryStick className={cls} />;
    case "disks":
      return <HardDrive className={cls} />;
    case "gpus":
      return <Gpu className={cls} />;
    case "nics":
      return <Network className={cls} />;
  }
}

function timeSince(isoOrMs: string): string {
  const ms = new Date(isoOrMs).getTime();
  if (!isFinite(ms)) return "";
  const sec = Math.floor((Date.now() - ms) / 1000);
  if (sec < 5) return "just now";
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  return `${hr}h ago`;
}

export default function InventoryPage() {
  const router = useRouter();
  const searchParams = useSearchParams();
  const { isAuthenticated } = useAuth();
  const { data, status, error, refresh } = useInventory();

  const activeView: InventoryView = (() => {
    const v = searchParams.get("view");
    return isValidView(v) ? v : "machines";
  })();

  const setActiveView = (next: InventoryView) => {
    const params = new URLSearchParams(searchParams.toString());
    if (next === "machines") {
      params.delete("view");
    } else {
      params.set("view", next);
    }
    const q = params.toString();
    router.replace(q ? `?${q}` : "?", { scroll: false });
  };

  // Fetch on mount if authenticated and idle. Deferred to next tick to
  // satisfy React 19's set-state-in-effect rule (refresh() synchronously
  // calls setStatus("loading")).
  useEffect(() => {
    if (!isAuthenticated || status !== "idle") return;
    const t = setTimeout(() => {
      void refresh();
    }, 0);
    return () => clearTimeout(t);
  }, [isAuthenticated, status, refresh]);

  // Tick once every 30s so the "last updated 2m ago" stays fresh.
  const [, setTick] = useState(0);
  useEffect(() => {
    const id = setInterval(() => setTick((n) => n + 1), 30000);
    return () => clearInterval(id);
  }, []);

  const resolved = useMemo(
    () => (data ? resolveView(activeView, data) : null),
    [data, activeView]
  );

  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.25, ease: "easeOut" }}
      className="min-h-screen bg-blox-bg"
    >
      {/* Top sticky bar */}
      <header className="sticky top-0 z-50 bg-blox-bg/80 backdrop-blur-xl border-b border-blox-border/50">
        <div className="max-w-[1400px] mx-auto px-4 sm:px-6 h-12 flex items-center justify-between">
          <Link
            href="/"
            className="flex items-center gap-1.5 text-blox-muted hover:text-blox-text transition-colors text-xs"
          >
            <ArrowLeft className="w-3.5 h-3.5" />
            <span>Fleet</span>
          </Link>
          <div className="flex items-center gap-1.5">
            {data?.generated_at && (
              <span className="hidden sm:inline text-[10px] text-blox-muted font-mono tabular-nums mr-2">
                Updated {timeSince(data.generated_at)}
              </span>
            )}
            <Button
              variant="ghost"
              size="sm"
              onClick={() => refresh()}
              disabled={status === "loading"}
              className="text-xs text-blox-muted hover:text-blox-text gap-1.5 h-8"
              title="Refresh inventory data"
            >
              <RefreshCw
                className={`w-3.5 h-3.5 ${status === "loading" ? "animate-spin" : ""}`}
              />
              <span className="hidden sm:inline">Refresh</span>
            </Button>
          </div>
        </div>
      </header>

      {/* Hero */}
      <section className="border-b border-blox-border/50 bg-blox-bg/40">
        <div className="max-w-[1400px] mx-auto px-4 sm:px-6 py-6">
          <div className="flex items-center gap-3">
            <h1 className="text-xl sm:text-2xl font-semibold text-blox-text tracking-tight">
              Hardware Inventory
            </h1>
            {data && (
              <Badge
                variant="outline"
                className="border-blox-border text-blox-muted text-[10px] tabular-nums"
              >
                {data.totals.machine_count} machine
                {data.totals.machine_count === 1 ? "" : "s"}
              </Badge>
            )}
          </div>
          <p className="text-[12px] text-blox-muted mt-1.5">
            Aggregate view of all hardware reported by agents and API-polled machines.
            Sort, filter, group, and export for capacity planning and rotation.
          </p>
        </div>
      </section>

      <main className="max-w-[1400px] mx-auto px-4 sm:px-6 py-6 space-y-6">
        {status === "loading" && !data && <LoadingSkeleton />}

        {status === "error" && (
          <div className="flex items-start gap-3 rounded-xl border border-red-500/20 bg-red-500/5 px-4 py-3">
            <AlertTriangle className="w-4 h-4 text-red-400 mt-0.5 shrink-0" />
            <div className="flex-1">
              <p className="text-sm text-blox-text font-medium">Failed to load inventory</p>
              <p className="text-xs text-blox-muted mt-1">{error}</p>
            </div>
            <Button
              variant="outline"
              size="sm"
              onClick={() => refresh()}
              className="text-xs border-blox-border text-blox-text"
            >
              Retry
            </Button>
          </div>
        )}

        {data && (
          <>
            <InventorySummaryCards totals={data.totals} />

            <div className="flex items-center justify-between gap-3 flex-wrap">
              <Tabs
                value={activeView}
                onValueChange={(v) => setActiveView(v as InventoryView)}
              >
                <TabsList variant="line" className="gap-1">
                  {VIEWS.map((v) => (
                    <TabsTrigger
                      key={v}
                      value={v}
                      className="px-3.5 py-1.5 gap-1.5 text-sm"
                    >
                      {viewIcon(v)}
                      {viewLabel(v)}
                    </TabsTrigger>
                  ))}
                </TabsList>
              </Tabs>
              {resolved && (
                <InventoryExportMenu
                  view={activeView}
                  rows={resolved.rows}
                  cols={resolved.cols}
                />
              )}
            </div>

            {resolved && (
              <InventoryTable
                key={activeView}
                rows={resolved.rows}
                cols={resolved.cols}
              />
            )}
          </>
        )}
      </main>
    </motion.div>
  );
}

function LoadingSkeleton() {
  return (
    <div className="space-y-6">
      <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-px bg-blox-border rounded-lg overflow-hidden border border-blox-border">
        {Array.from({ length: 5 }).map((_, i) => (
          <div key={i} className="bg-blox-card p-3 h-[68px]">
            <div className="h-2 bg-blox-border/60 rounded w-12 mb-2 animate-shimmer" />
            <div className="h-5 bg-blox-border/60 rounded w-20 animate-shimmer" />
          </div>
        ))}
      </div>
      <div className="bg-blox-card border border-blox-border rounded-xl overflow-hidden">
        <div className="border-b border-blox-border/50 p-3">
          <div className="flex gap-4">
            {Array.from({ length: 6 }).map((_, i) => (
              <div key={i} className="h-3 bg-blox-border/60 rounded w-16 animate-shimmer" />
            ))}
          </div>
        </div>
        {Array.from({ length: 6 }).map((_, i) => (
          <div key={i} className="border-b border-blox-border/30 p-3 last:border-b-0">
            <div className="flex gap-4">
              {Array.from({ length: 6 }).map((_, j) => (
                <div
                  key={j}
                  className="h-3 bg-blox-border/40 rounded w-16 animate-shimmer"
                />
              ))}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
