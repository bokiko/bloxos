"use client";

import { useDesign, type Layout } from "@/contexts/DesignContext";
import { DESIGN_COLORS } from "@/lib/design-prefs.mjs";
import { cn } from "@/lib/utils";

const designs: { id: Layout; name: string; description: string }[] = [
  { id: "classic", name: "Classic", description: "The familiar BloxOS dashboard and its original palettes." },
  { id: "wall", name: "Operations Wall", description: "An open canvas of fleet health, resources and machines." },
  { id: "grove", name: "Grove Workspace", description: "Sidebar navigation, central analytics and a live context rail." },
  { id: "console", name: "Precision Console", description: "A compact, table-first workspace with telemetry instruments." },
  { id: "ledger", name: "Ledger", description: "A still, type-led read of the fleet. Nothing animates and colour marks only what needs attention." },
];

function DesignPreview({ layout }: { layout: Layout }) {
  return <svg viewBox="0 0 240 120" aria-hidden="true" className="w-full rounded-md mb-3 bg-blox-bg text-blox-blue border border-blox-border">
    {layout === "ledger" ? <>
      {/* Rules and text, no cards: the layout's whole idea in one thumbnail. */}
      <rect x="7" y="10" width="70" height="9" rx="2" fill="currentColor" opacity=".55" />
      <rect x="7" y="27" width="226" height="1" fill="currentColor" opacity=".35" />
      {[36,52,68].map((y, i) => <g key={y}>
        <rect x="7" y={y} width={i === 0 ? 54 : 44} height="6" rx="2" fill="currentColor" opacity=".3" />
        <rect x="96" y={y} width="137" height="6" rx="2" fill="currentColor" opacity=".16" />
      </g>)}
      <rect x="7" y="84" width="226" height="1" fill="currentColor" opacity=".35" />
      {[93,104].map(y => <rect key={y} x="7" y={y} width="150" height="5" rx="2" fill="currentColor" opacity=".16" />)}
    </> : layout === "grove" ? <>
      <rect x="7" y="7" width="37" height="106" rx="5" fill="currentColor" opacity=".25" />
      <rect x="51" y="7" width="128" height="43" rx="7" fill="currentColor" opacity=".45" />
      <rect x="186" y="7" width="47" height="106" rx="5" fill="currentColor" opacity=".2" />
      {[57,77,97].map(y => <rect key={y} x="51" y={y} width="128" height="14" rx="4" fill="currentColor" opacity=".2" />)}
    </> : layout === "console" ? <>
      <rect x="7" y="7" width="17" height="106" rx="3" fill="currentColor" opacity=".5" />
      <rect x="31" y="7" width="202" height="14" rx="3" fill="currentColor" opacity=".25" />
      {[28,44,60,76].map(y => <rect key={y} x="31" y={y} width="202" height="10" rx="2" fill="currentColor" opacity=".2" />)}
      {[31,101,171].map(x => <rect key={x} x={x} y="93" width="62" height="20" rx="3" fill="currentColor" opacity=".4" />)}
    </> : <>
      <rect x="7" y="7" width="226" height="12" rx="4" fill="currentColor" opacity=".2" />
      <rect x="7" y="26" width="110" height="40" rx={layout === "wall" ? 10 : 4} fill="currentColor" opacity=".5" />
      {[124,182].map(x => <rect key={x} x={x} y="26" width="51" height="40" rx="7" fill="currentColor" opacity=".25" />)}
      <rect x="7" y="73" width="226" height="40" rx="7" fill="currentColor" opacity=".2" />
    </>}
  </svg>;
}

export function DesignSettings({ compact = false }: { compact?: boolean }) {
  const { layout, color, ready, saving, error, setLayout, setColor } = useDesign();
  return <section aria-label="Dashboard design" className={compact ? "px-2 py-2 space-y-2" : "space-y-3"}>
    <h2 className="text-sm font-semibold text-blox-text">Design</h2>
    {!compact && <p className="text-xs text-blox-muted">Choose a layout, then its color. Your choice follows your account.</p>}
    <div className={compact ? "grid grid-cols-2 gap-1" : "grid grid-cols-1 sm:grid-cols-2 xl:grid-cols-4 gap-3"}>
      {designs.map(design => <button key={design.id} type="button" disabled={!ready}
        aria-label={`Use ${design.name} design`} aria-pressed={layout === design.id} onClick={() => setLayout(design.id)}
        className={cn("text-left rounded-lg border transition-colors disabled:opacity-50", compact ? "px-2 py-2 text-xs" : "p-3", layout === design.id ? "border-blox-blue bg-blox-blue/10 ring-1 ring-blox-blue/40" : "border-blox-border bg-blox-card hover:border-blox-blue/50")}>
        {!compact && <DesignPreview layout={design.id} />}
        <span className="font-medium text-blox-text">{design.name}</span>
        {!compact && <p className="text-xs text-blox-muted mt-1">{design.description}</p>}
      </button>)}
    </div>
    {layout !== "classic" && layout !== "ledger" && <div role="group" aria-label="Design color" className="flex flex-wrap gap-2">
      {DESIGN_COLORS.map(option => <button key={option} type="button" aria-pressed={color === option}
        aria-label={`Use ${option} design color`} onClick={() => setColor(option)} disabled={!ready}
        className={cn("px-3 py-2 text-xs rounded-md border capitalize", color === option ? "border-blox-blue text-blox-blue bg-blox-blue/10" : "border-blox-border text-blox-muted")}>{option}</button>)}
    </div>}
    {saving && <p role="status" className="text-xs text-blox-muted">Saving design…</p>}
    {error && <p role="status" className="text-xs text-status-warning max-w-xs">{error}</p>}
  </section>;
}
