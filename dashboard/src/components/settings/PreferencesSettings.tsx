"use client";

// Preferences settings panel.
//
// Appearance (the single place in the product where dark/light is chosen),
// the fleet power tariff, density / default view / default sort scalar
// choices, plus the manage surfaces for pinned machines and saved filters.

import { Trash2, LayoutGrid, List as ListIcon } from "lucide-react";
import { PowerRateSettings } from "./PowerRateSettings";
import {
  usePreferences,
  type Density,
  type DefaultView,
  type DefaultSort,
  type OverviewLayout,
  type OverviewWidgets,
} from "@/contexts/PreferencesContext";
import {
  DEFAULT_OVERVIEW_LAYOUT,
  DEFAULT_OVERVIEW_WIDGETS,
  isDefaultOverview,
} from "@/lib/overview-layout.mjs";
import { MF_BUTTON_QUIET } from "@/lib/monoform-classes";
import { useTheme, APPEARANCE_LABELS } from "@/contexts/ThemeContext";
import { useSSE } from "@/contexts/SSEContext";
import { useToast } from "@/components/Toast";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

// Each arrangement is named by what it puts first, and described by what that
// costs — the user chooses an intent, never a column width.
const ARRANGEMENTS: { value: OverviewLayout; label: string; description: string }[] = [
  {
    value: "machine-first",
    label: "Machine-first",
    description: "Power anchored, context compact beside it, the machine table high on the page.",
  },
  {
    value: "balanced",
    label: "Balanced",
    description: "Every context module in one row, with power full-width beneath it.",
  },
  {
    value: "power-focus",
    label: "Power focus",
    description: "Power only. A critical or offline machine still raises a marker.",
  },
];

const MODULES: { key: keyof OverviewWidgets; label: string }[] = [
  { key: "availability", label: "Fleet availability" },
  { key: "attention", label: "Needs attention" },
  { key: "urgent_alert", label: "Most urgent alert" },
];

const SORT_LABELS: Record<DefaultSort, string> = {
  manual: "My order",
  name: "Name (A-Z)",
  status: "Status",
  cpu: "CPU %",
  gpu_temp: "GPU Temp",
};

export function PreferencesSettings() {
  const { preferences, updateScalar, unpinMachine, deleteFilter, hubSupportsOverview } = usePreferences();
  const { appearance, setAppearance } = useTheme();
  const { machines } = useSSE();
  const { addToast } = useToast();

  const handleScalar = async (
    label: string,
    patch: Partial<
      Pick<typeof preferences, "density" | "default_view" | "default_sort" | "overview_layout" | "overview_widgets">
    >,
  ) => {
    try {
      await updateScalar(patch);
      addToast("success", `${label} saved`);
    } catch (e) {
      addToast("error", e instanceof Error ? e.message : `Failed to save ${label.toLowerCase()}`);
    }
  };

  const machineByID = new Map(machines.map((m) => [m.machine_id, m] as const));

  return (
    <div className="space-y-8">
      {/* Appearance — the only appearance control in the product. */}
      <section className="mf-panel p-5" aria-labelledby="appearance-heading">
        <h2 id="appearance-heading" className="text-sm font-semibold text-blox-text">
          Appearance
        </h2>
        <p className="mt-1 text-xs text-blox-muted">
          Same layout, same components, same colour meanings — a dark ground or a bright one.
        </p>
        <div
          className="mt-4 inline-flex rounded-[10px] border border-blox-border bg-surface-sunken p-1"
          role="group"
          aria-label="Appearance"
        >
          {(["dark", "light"] as const).map((option) => (
            <button
              key={option}
              type="button"
              onClick={() => setAppearance(option)}
              aria-pressed={appearance === option}
              className={cn(
                "min-h-8 rounded-lg px-3 text-xs font-medium transition-colors",
                appearance === option
                  ? "bg-blox-card text-blox-text shadow-sm"
                  : "text-blox-muted hover:text-blox-text",
              )}
            >
              {APPEARANCE_LABELS[option]}
            </button>
          ))}
        </div>
      </section>

      {/* Overview — what the operator keeps above the machine table. The
          durable home for the choice; the Overview's own Customize control is
          a shortcut into this same model, not a second one. */}
      <section className="mf-panel p-5" aria-labelledby="overview-heading">
        <h2 id="overview-heading" className="text-sm font-semibold text-blox-text">
          Overview
        </h2>
        <p className="mt-1 text-xs text-blox-muted">
          Fleet power and the machine table are always shown. These choose what sits between them.
        </p>

        {!hubSupportsOverview && (
          <p role="status" className="mt-3 flex items-center gap-2 text-xs text-status-warning">
            <span className="mf-status-dot mf-status-warning" aria-hidden="true" />
            This hub cannot store an overview choice yet. Update the hub to change it.
          </p>
        )}

        <fieldset disabled={!hubSupportsOverview} className="contents">
          <h3 className="mf-kicker mt-5 uppercase">Arrangement</h3>
          <div className="mt-3 grid max-w-2xl grid-cols-1 gap-3 sm:grid-cols-3">
            {ARRANGEMENTS.map((option) => (
              <button
                key={option.value}
                type="button"
                onClick={() => void handleScalar("Arrangement", { overview_layout: option.value })}
                aria-pressed={preferences.overview_layout === option.value}
                className={cn(
                  "rounded-[10px] border bg-surface-sunken p-4 text-left transition-colors disabled:opacity-50",
                  preferences.overview_layout === option.value
                    ? "border-blox-blue"
                    : "border-blox-border hover:border-blox-muted/40",
                )}
              >
                <span className="block text-sm font-medium text-blox-text">{option.label}</span>
                <span className="mt-1 block text-xs text-blox-muted">{option.description}</span>
              </button>
            ))}
          </div>

          <h3 className="mf-kicker mt-6 uppercase">Supporting modules</h3>
          <p className="mt-1 text-xs text-blox-muted">
            Hidden in Power focus. A module with nothing real to report is left out and the row closes up.
          </p>
          <div className="mt-3 flex flex-wrap items-center gap-2">
            {MODULES.map(({ key, label }) => {
              const on = preferences.overview_widgets[key];
              return (
                <Button
                  key={key}
                  size="sm"
                  variant={on ? "default" : "outline"}
                  aria-pressed={on}
                  onClick={() =>
                    void handleScalar("Overview modules", {
                      // The hub requires the whole object: a partial one cannot
                      // say whether a missing key means "off" or "unchanged".
                      overview_widgets: { ...preferences.overview_widgets, [key]: !on },
                    })
                  }
                >
                  {label}
                </Button>
              );
            })}
          </div>

          <div className="mt-5">
            <button
              type="button"
              className={MF_BUTTON_QUIET}
              disabled={
                !hubSupportsOverview ||
                isDefaultOverview(preferences.overview_layout, preferences.overview_widgets)
              }
              onClick={() =>
                void handleScalar("Overview", {
                  overview_layout: DEFAULT_OVERVIEW_LAYOUT as OverviewLayout,
                  overview_widgets: { ...DEFAULT_OVERVIEW_WIDGETS } as OverviewWidgets,
                })
              }
            >
              Reset to recommended
            </button>
          </div>
        </fieldset>
      </section>

      {/* Power rate — moved off the Overview's fleet power pane, which shows
          the cost but no longer hosts the control that sets it. */}
      <PowerRateSettings />

      {/* Density */}
      <section className="mf-panel p-5" aria-labelledby="density-heading">
        <h2 id="density-heading" className="text-sm font-semibold text-blox-text mb-1">Density</h2>
        <p className="text-xs text-blox-muted mb-4">
          How tightly the grid packs cards.
        </p>
        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 max-w-2xl">
          {(["comfortable", "compact"] as Density[]).map((d) => {
            const active = preferences.density === d;
            return (
              <button
                key={d}
                type="button"
                onClick={() => void handleScalar("Density", { density: d })}
                aria-pressed={active}
                className={cn(
                  "text-left rounded-[10px] border bg-surface-sunken p-4 transition-colors",
                  active
                    ? "border-blox-blue"
                    : "border-blox-border hover:border-blox-muted/40",
                )}
              >
                <div className="text-sm font-medium text-blox-text capitalize">{d}</div>
                <div className="text-xs text-blox-muted mt-1">
                  {d === "comfortable"
                    ? "Roomier padding, easier to scan."
                    : "Tighter padding, more cards per screen."}
                </div>
              </button>
            );
          })}
        </div>
      </section>

      {/* Default view */}
      <section className="mf-panel p-5" aria-labelledby="default-view-heading">
        <h2 id="default-view-heading" className="text-sm font-semibold text-blox-text mb-1">Default view</h2>
        <p className="text-xs text-blox-muted mb-4">
          Which layout the fleet page opens in.
        </p>
        <div className="flex items-center gap-2">
          {(["grid", "list"] as DefaultView[]).map((v) => {
            const active = preferences.default_view === v;
            const Icon = v === "grid" ? LayoutGrid : ListIcon;
            return (
              <Button
                key={v}
                size="sm"
                variant={active ? "default" : "outline"}
                onClick={() => void handleScalar("Default view", { default_view: v })}
                className="gap-1.5"
                aria-pressed={active}
              >
                <Icon className="w-3.5 h-3.5" />
                <span className="capitalize">{v}</span>
              </Button>
            );
          })}
        </div>
      </section>

      {/* Default sort */}
      <section className="mf-panel p-5" aria-labelledby="default-sort-heading">
        <h2 id="default-sort-heading" className="text-sm font-semibold text-blox-text mb-1">Default sort</h2>
        <p className="text-xs text-blox-muted mb-4">
          How machines order before you reach for the sort menu.
        </p>
        <div className="flex flex-wrap items-center gap-2">
          {(Object.keys(SORT_LABELS) as DefaultSort[]).map((s) => {
            const active = preferences.default_sort === s;
            return (
              <Button
                key={s}
                size="sm"
                variant={active ? "default" : "outline"}
                onClick={() => void handleScalar("Default sort", { default_sort: s })}
                aria-pressed={active}
              >
                {SORT_LABELS[s]}
              </Button>
            );
          })}
        </div>
      </section>

      {/* Pinned machines */}
      <section className="mf-panel p-5" aria-labelledby="pinned-machines-heading">
        <h2 id="pinned-machines-heading" className="text-sm font-semibold text-blox-text mb-1">Pinned machines</h2>
        <p className="text-xs text-blox-muted mb-4">
          These float to the top of the fleet grid.
        </p>
        {preferences.pinned_machines.length === 0 ? (
          <p className="text-xs text-blox-muted/70">
            No machines pinned. Click the star on any card to pin it.
          </p>
        ) : (
          <ul className="divide-y divide-blox-border/40 -my-2">
            {preferences.pinned_machines.map((id) => {
              const m = machineByID.get(id);
              return (
                <li key={id} className="flex items-center justify-between py-2 gap-3">
                  <div className="min-w-0">
                    <div className="text-sm text-blox-text truncate">
                      {m?.hostname || id}
                    </div>
                    {m?.ip && (
                      <div className="mf-metric text-[11px] text-blox-muted truncate">
                        {m.ip}
                      </div>
                    )}
                  </div>
                  <Button
                    size="icon-sm"
                    variant="ghost"
                    onClick={() => void unpinMachine(id)}
                    title="Unpin"
                    aria-label={`Unpin ${m?.hostname || id}`}
                    className="text-blox-muted hover:text-blox-red"
                  >
                    <Trash2 className="w-3.5 h-3.5" />
                  </Button>
                </li>
              );
            })}
          </ul>
        )}
      </section>

      {/* Saved filters */}
      <section className="mf-panel p-5" aria-labelledby="saved-filters-heading">
        <h2 id="saved-filters-heading" className="text-sm font-semibold text-blox-text mb-1">Saved filters</h2>
        <p className="text-xs text-blox-muted mb-4">
          Combinations of search, status, tag, and sort that you reuse.
        </p>
        {preferences.saved_filters.length === 0 ? (
          <p className="text-xs text-blox-muted/70">
            No saved filters yet. Apply some filters on the fleet page and use
            the bookmark button to save them.
          </p>
        ) : (
          <ul className="divide-y divide-blox-border/40 -my-2">
            {preferences.saved_filters.map((f) => (
              <li key={f.id} className="flex items-center justify-between py-2 gap-3">
                <div className="text-sm text-blox-text truncate">{f.name}</div>
                <Button
                  size="icon-sm"
                  variant="ghost"
                  onClick={() => void deleteFilter(f.id)}
                  title="Delete saved filter"
                  aria-label={`Delete saved filter ${f.name}`}
                  className="text-blox-muted hover:text-blox-red"
                >
                  <Trash2 className="w-3.5 h-3.5" />
                </Button>
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}
