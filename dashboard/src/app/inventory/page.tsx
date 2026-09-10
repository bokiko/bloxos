"use client";
import { AppShell } from "@/components/shell/AppShell";

// Hardware inventory — one aggregate table with six views over the same fleet.
//
// Monoform: the shell owns the title, the rail and the global actions, so this
// file starts at the page lead. The view tabs write to the `view` query param,
// which is what makes a filtered view linkable.

import { useEffect, useMemo, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import {
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
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { InventorySummary } from "@/components/InventorySummaryCards";
import { InventoryTable } from "@/components/InventoryTable";
import { InventoryExportMenu } from "@/components/InventoryExportMenu";
import { resolveView, type InventoryView } from "@/lib/inventory-utils";
import { MF_BUTTON, MF_TAB } from "@/lib/monoform-classes";

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
  return <AppShell><InventoryContent /></AppShell>;
}

function InventoryContent() {
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
    <>
      <div className="mf-intro">
        {data ? (
          <InventorySummary totals={data.totals} />
        ) : (
          <span className="text-[13px] text-text-tertiary">
            {status === "error" ? "Inventory unavailable." : "Reading hardware from the fleet…"}
          </span>
        )}
        <div className="mf-intro-actions">
          <button
            type="button"
            onClick={() => refresh()}
            disabled={status === "loading"}
            className={MF_BUTTON}
            title="Refresh inventory data"
          >
            <RefreshCw
              className={`w-3.5 h-3.5 ${status === "loading" ? "animate-spin" : ""}`}
              aria-hidden
            />
            {status === "loading" ? "Refreshing…" : "Refresh inventory"}
          </button>
          {data?.generated_at && (
            <span className="mf-kicker">Collected {timeSince(data.generated_at)}</span>
          )}
        </div>
        <p>
          Every CPU, DIMM, disk, GPU and NIC the fleet reports. Any view can be sorted, grouped and
          exported.
        </p>
      </div>

      <div className="space-y-6">
        {status === "loading" && !data && <LoadingSkeleton />}

        {status === "error" && (
          <div
            role="alert"
            className="mf-panel flex items-start gap-3 border-status-critical/40 px-5 py-4"
          >
            <AlertTriangle className="w-4 h-4 text-status-critical mt-0.5 shrink-0" aria-hidden />
            <div className="flex-1">
              <p className="text-[13px] font-medium text-text-primary">Failed to load inventory</p>
              <p className="text-xs text-text-tertiary mt-1">{error}</p>
            </div>
            <button type="button" onClick={() => refresh()} className={MF_BUTTON}>
              Retry
            </button>
          </div>
        )}

        {data && (
          <>
            <div className="flex items-center justify-between gap-3 flex-wrap">
              <Tabs
                value={activeView}
                onValueChange={(v) => setActiveView(v as InventoryView)}
              >
                <TabsList variant="line" className="gap-1">
                  {VIEWS.map((v) => (
                    <TabsTrigger key={v} value={v} className={MF_TAB}>
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
      </div>
    </>
  );
}

function LoadingSkeleton() {
  return (
    <div className="mf-panel overflow-hidden" aria-busy="true" aria-label="Loading inventory">
      <div className="border-b border-border-subtle bg-[var(--mf-table-head)] px-7 py-3.5">
        <div className="flex gap-6">
          {Array.from({ length: 6 }).map((_, i) => (
            <div key={i} className="h-2.5 w-16 rounded bg-border-default/60 animate-shimmer" />
          ))}
        </div>
      </div>
      {Array.from({ length: 8 }).map((_, i) => (
        <div key={i} className="border-b border-border-subtle px-7 py-5 last:border-b-0">
          <div className="flex gap-6">
            {Array.from({ length: 6 }).map((_, j) => (
              <div key={j} className="h-3 w-16 rounded bg-border-default/40 animate-shimmer" />
            ))}
          </div>
        </div>
      ))}
    </div>
  );
}
