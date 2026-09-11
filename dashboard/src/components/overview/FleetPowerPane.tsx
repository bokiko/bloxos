"use client";

/* ============================================================================
 * Monoform — "Fleet power".
 *
 * GET /api/fleet/power/history (hub/fleet_power.go) buckets every machine's
 * stored power windows into one time series and reports, alongside it, exactly
 * how much of the fleet is behind that series.
 *
 * Watts are money here — this fleet mines — so the temptation is to print one
 * big number. The pane refuses to, in three specific ways:
 *
 *   1. A MEASURED reading and a MODELLED one are never added. A machine with
 *      no power counter can report an estimate (proto/powerhistory,
 *      SourceEstimateUtil); that estimate is a second line, a second readout
 *      and a second cost, and it says "modelled, not measured" in words every
 *      time. There is no code path in this file, or in lib/fleet-power.mjs,
 *      that produces their sum.
 *   2. A partial sum is never labelled a total. Coverage — "4 / 6 reporting" —
 *      sits directly under the number it qualifies, always.
 *   3. Where no machine has a whole-platform counter, the pane does NOT fall
 *      back to adding CPU and GPU watts together and calling it fleet power.
 *      It charts them as two separate component lines and says so.
 *
 * WHAT IS NOT HERE ANY MORE
 * The pane used to argue all three of those points in prose: a coverage
 * sentence, a shortfall sentence, a two-clause chart caption and a
 * three-line "a floor, not a bill" note under the cost. Four paragraphs is
 * not a dashboard. Every one of those facts survives — as a figure, as a
 * legend, or on a `title` tooltip — but none of them costs a line of the
 * viewport on every visit. The long-form version lives in docs/power-history.md.
 * The per-kWh tariff control moved to Settings → Preferences for the same
 * reason: it is set once and read never.
 *
 * The lines are flat strokes on a hairline grid: no fill, no gradient, no
 * glow, no gauge. Measured is solid, estimated is dashed, and each carries a
 * swatch beside its own readout — that pairing is the legend, and it is also
 * why the state is never conveyed by colour alone. Blue is the product's one
 * accent and violet its GPU tone; neither is a status colour, which is why
 * green/amber/red appear here only for a real warning, with words.
 * ========================================================================== */

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { ChartTooltip } from "@/components/charts/ChartTooltip";
import { Zap } from "lucide-react";

import { DEMO_MODE, HUB_URL, getStoredToken } from "@/lib/session";
import {
  PERIOD_LABELS,
  POWER_PERIODS,
  coverageSentence,
  domainLabel,
  domainOf,
  fleetPowerChartRows,
  fleetPowerCost,
  fleetPowerMode,
  fleetPowerSeries,
  fleetPowerWarnings,
  formatKWh,
  formatMoney,
  formatWatts,
  latestReading,
  normalizeFleetPower,
  shortfallSentence,
} from "@/lib/fleet-power.mjs";
import { MF_PANEL_HEAD, MF_PANEL_TITLE } from "@/lib/monoform-classes";
import type { PowerPeriod, PowerRate } from "./useWorkspacePrefs";

export type { PowerPeriod, PowerRate };

/* Strokes. In whole-machine mode the two lines are one quantity told two ways,
   so they differ by KIND: solid product blue for what was measured, dashed
   violet for what was modelled. In component mode they are two different
   quantities, so they take the table's domain tones — CPU blue, GPU violet —
   and an estimate of either stays dashed in its own domain's colour. */
const STROKE: Record<string, string> = {
  "system:measured": "var(--mf-blue)",
  "system:estimated": "var(--mf-violet)",
  "cpu:measured": "var(--mf-blue)",
  "cpu:estimated": "var(--mf-blue)",
  "gpu:measured": "var(--mf-violet)",
  "gpu:estimated": "var(--mf-violet)",
};

const axisTick = {
  fontSize: 10,
  fill: "var(--text-tertiary)",
  fontFamily: "var(--font-mono)",
} as const;

interface Series {
  key: string;
  domain: string;
  kind: "measured" | "estimated";
  label: string;
}

interface FleetPowerHistory {
  period: PowerPeriod;
  coverage: {
    machinesTotal: number;
    machinesReporting: number;
    machinesAPIPolled: number;
    degradedMachines: number;
    gapsDeclared: number;
    gapBuckets: number;
    truncated: boolean;
  };
}

export interface FleetPowerPaneProps {
  period: PowerPeriod;
  onPeriodChange: (period: PowerPeriod) => void;
  /** Read-only here. The control that sets it lives in Settings → Preferences
   * (components/settings/PowerRateSettings.tsx); a tariff is typed once and
   * read on every visit, so it does not belong on the pane. */
  rate: PowerRate;
}

export function FleetPowerPane({ period, onPeriodChange, rate }: FleetPowerPaneProps) {
  const [state, setState] = useState<{ period: string; data?: FleetPowerHistory; error?: string }>({
    period,
  });

  useEffect(() => {
    // Demo mode has no hub. Fabricating a power series to fill the pane would
    // be exactly the invention the rest of this file exists to avoid, so the
    // pane says what it is instead.
    if (DEMO_MODE) return;
    let stopped = false;
    let active: AbortController | undefined;

    const load = async () => {
      if (active) return;
      const controller = new AbortController();
      active = controller;
      const timeout = setTimeout(() => controller.abort(), 10000);
      try {
        const token = getStoredToken();
        const res = await fetch(
          `${HUB_URL}/api/fleet/power/history?period=${encodeURIComponent(period)}`,
          { headers: token ? { Authorization: `Bearer ${token}` } : {}, signal: controller.signal },
        );
        if (!res.ok) {
          throw new Error(
            res.status === 404
              ? "Fleet power is not available on this hub yet."
              : "Could not refresh fleet power.",
          );
        }
        const data = normalizeFleetPower(await res.json()) as FleetPowerHistory;
        if (!stopped) setState({ period, data });
      } catch (error) {
        if (!stopped) {
          setState((previous) => ({
            period,
            // Keep the last good window for THIS period on the screen and mark
            // it stale, rather than blanking a real reading over one bad poll.
            data: previous.period === period ? previous.data : undefined,
            error: error instanceof Error ? error.message : "Could not refresh fleet power.",
          }));
        }
      } finally {
        clearTimeout(timeout);
        active = undefined;
      }
    };

    void load();
    // One 30-second bucket is the finest the agents produce, so polling
    // faster than that only re-renders the same numbers.
    const refresh = setInterval(() => void load(), 30000);
    return () => {
      stopped = true;
      active?.abort();
      clearInterval(refresh);
    };
  }, [period]);

  const data = state.period === period ? state.data : undefined;
  const error = state.period === period ? state.error : undefined;

  const mode = fleetPowerMode(data) as "system" | "component" | "none";
  const series = useMemo(() => (fleetPowerSeries(data) as Series[]) ?? [], [data]);
  const rows = useMemo(() => fleetPowerChartRows(data, series), [data, series]);
  const warnings = useMemo(() => (fleetPowerWarnings(data) as string[]) ?? [], [data]);
  const coverage = data?.coverage;

  // The costed domain is whatever the pane is leading with. In component mode
  // that is CPU package power, which is a real cost of a real component — not
  // the machine's wall draw, and the copy under it says so.
  const costDomain = mode === "component" ? "cpu" : "system";
  const cost = fleetPowerCost(data, costDomain, rate, period);

  const readouts = series.map((s) => ({
    ...s,
    latest: latestReading(data, s.domain, s.kind) as
      | { watts: number; machines: number; timestamp: number }
      | null,
  }));


  const periodSwitch = (
    <div className="mf-segment" role="group" aria-label="Power window">
      {(POWER_PERIODS as PowerPeriod[]).map((key) => (
        <button
          key={key}
          type="button"
          aria-pressed={key === period}
          onClick={() => onPeriodChange(key)}
          title={`Chart the ${PERIOD_LABELS[key]}`}
        >
          {key}
        </button>
      ))}
    </div>
  );

  return (
    <section className="mf-panel mf-power-anchor min-w-0 overflow-hidden" aria-label="Fleet power">
      <div className={MF_PANEL_HEAD}>
        <h2 className={MF_PANEL_TITLE}>Fleet power</h2>
        {periodSwitch}
      </div>
      <div className="mf-power-anchor-body">
        {DEMO_MODE ? (
          <EmptyState
            title="Not in the demo data."
            tip="Power comes from real counters on real machines — RAPL, a battery, a BMC — so there is nothing here to simulate. Connect a hub to see it."
          />
        ) : mode === "none" ? (
          <EmptyState
            // The error IS the title. It used to be the body under a
            // "Fleet power unavailable." heading, which said the same thing
            // twice and made a two-line box out of one fact.
            title={
              error && !data
                ? error
                : !data
                  ? "Loading…"
                  : coverage && coverage.machinesTotal === 0
                    ? "No machines in this fleet."
                    : "No machine reports power."
            }
            hint={coverage && coverage.machinesTotal > 0 ? coverageShort(coverage) : undefined}
            tip={
              coverage && coverage.machinesTotal > 0
                ? "The agent reports power only where the hardware can measure it — RAPL, a battery, a BMC, or a board sensor. A machine with no counter at all reports nothing rather than a guess."
                : undefined
            }
          />
        ) : (
          <>
            {/* The one caveat the numbers cannot carry themselves: two
                component lines are not a machine's wall draw, and must not be
                added into one. Whole-machine mode needs no such warning — its
                readout is already labelled "whole-machine" — and the window is
                on the segmented control in the header, so neither is repeated
                here. */}
            {mode === "component" && (
              <p className="mf-table-meta mt-3">Component power — not wall power</p>
            )}

            {/* THE READOUTS, which are also the chart's legend: each number
                carries the swatch of the line it came from, that line's name,
                and that line's own machine count. They sit side by side and
                are never totalled — "142 W measured" beside "≈18 W estimated"
                is two facts, and 160 W is not a third. */}
            <div
              className="mt-3 flex flex-wrap items-start gap-x-7 gap-y-3"
              title={coverage ? coverageDetail(coverage) : undefined}
            >
              {readouts.map((r) => (
                <div key={r.key} className="min-w-0">
                  <p className="mf-metric text-[21px] leading-none text-text-primary">
                    {r.kind === "estimated" && r.latest ? "≈ " : ""}
                    {formatWatts(r.latest?.watts ?? null)}
                  </p>
                  <p className="mt-1.5 flex flex-wrap items-center gap-x-1.5 text-[11px] leading-[1.4] text-text-tertiary">
                    <SeriesMark seriesKey={r.key} estimated={r.kind === "estimated"} />
                    {/* The label wears a text token; the swatch beside it is
                        the only thing carrying the series colour. */}
                    <span className="text-text-secondary">{r.label}</span>
                    {r.kind === "estimated" ? <span>— modelled, not measured</span> : null}
                    <span>
                      ·{" "}
                      {r.latest
                        ? `${r.latest.machines} / ${coverage?.machinesTotal ?? 0} reporting`
                        : "no reading"}
                    </span>
                  </p>
                </div>
              ))}
            </div>

            {/* COVERAGE, where no readout is carrying it. Every series being
                silent in this window would otherwise leave a chart of older
                buckets with nothing saying how much of the fleet is behind it. */}
            {coverage && !readouts.some((r) => r.latest) && (
              <p className="mt-2 text-[12px] text-text-secondary" title={coverageDetail(coverage)}>
                {coverageShort(coverage)}
              </p>
            )}

            {error && (
              <p role="status" className="mt-2 flex items-start gap-2 text-[12px] text-status-warning">
                <span className="mf-status-dot mf-status-warning mt-1.5" aria-hidden="true" />
                {/* The poll failed but the last good window for this period is
                    still on the screen, so the amber line has to say the
                    numbers above it are old — in four words, not a clause. */}
                <span>{error} Readings may be stale.</span>
              </p>
            )}
            {warnings.map((warning) => (
              <p
                key={warning}
                role="status"
                className="mt-2 flex items-start gap-2 text-[12px] text-status-warning"
              >
                <span className="mf-status-dot mf-status-warning mt-1.5" aria-hidden="true" />
                <span>{warning}</span>
              </p>
            ))}

            {/* The chart's accessible name is where the long form still
                lives: a screen reader gets the whole coverage sentence and
                the shortfall, which sighted readers get from the swatch row's
                tooltip rather than from a paragraph. */}
            <div
              className="mt-3 h-[120px]"
              role="img"
              aria-label={`${mode === "system" ? "Whole-machine" : "Component"} power in watts across the fleet, ${PERIOD_LABELS[period]}. ${
                coverage ? coverageDetail(coverage) : ""
              } Unreported buckets are left blank — missing data is not zero power.`}
            >
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={rows} margin={{ top: 4, right: 8, bottom: 0, left: 0 }}>
                  <CartesianGrid stroke="var(--border-subtle)" vertical={false} />
                  <XAxis
                    dataKey="timestamp"
                    type="number"
                    domain={["dataMin", "dataMax"]}
                    tickFormatter={formatTime}
                    tick={axisTick}
                    axisLine={false}
                    tickLine={false}
                    minTickGap={32}
                  />
                  <YAxis unit=" W" tick={axisTick} axisLine={false} tickLine={false} width={56} />
                  <Tooltip
                    cursor={{ stroke: "var(--border-strong)", strokeWidth: 1 }}
                    content={
                      <ChartTooltip
                        labelFormatter={(label) => formatTime(Number(label))}
                        formatter={(value, name) => [
                          formatWatts(typeof value === "number" ? value : null),
                          String(name ?? ""),
                        ]}
                      />
                    }
                  />
                  {series.map((s) => (
                    <Line
                      key={s.key}
                      dataKey={s.key}
                      name={s.kind === "estimated" ? `${s.label} (modelled)` : s.label}
                      stroke={STROKE[s.key] ?? "var(--mf-blue)"}
                      strokeWidth={1.5}
                      strokeDasharray={s.kind === "estimated" ? "4 3" : undefined}
                      dot={false}
                      // A bucket nobody observed is a break in the line, not a
                      // straight segment drawn across time nobody measured.
                      connectNulls={false}
                      isAnimationActive={false}
                    />
                  ))}
                </LineChart>
              </ResponsiveContainer>
            </div>

            <CostRow
              cost={cost}
              domain={costDomain}
              period={period}
              multiDomain={mode === "component"}
              complete={domainOf(data, costDomain).complete === true}
            />
          </>
        )}
      </div>
    </section>
  );
}

/* ---------------------------------------------------------------------------
 * Cost — one line.
 *
 * Energy is summed over OBSERVED windows only (the hub does not interpolate),
 * so every figure is a floor and keeps the words "at least". WHY it is a floor
 * used to be a three-line note under the row; it is a tooltip now, because the
 * two words that matter are on the line itself and the rest is read once.
 *
 * The tariff has no default — an invented rate would put a confident, specific
 * price under a chart of real watts — so with none stated the row shows the
 * energy and links to the place the rate is set.
 * ------------------------------------------------------------------------- */

interface CostKind {
  energyKWh: number;
  cost: number | null;
  machines: number;
  observedFraction: number | null;
}

function CostRow({
  cost,
  domain,
  period,
  multiDomain,
  complete,
}: {
  cost: { currency: string; perKwh: number | null; measured: CostKind; estimated: CostKind };
  domain: string;
  period: PowerPeriod;
  /** More than one domain is charted, so the cost must name the one it costed. */
  multiDomain: boolean;
  complete: boolean;
}) {
  const showEstimated = cost.estimated.machines > 0;
  const partial = cost.measured.observedFraction !== null && cost.measured.observedFraction < 0.995;

  // The whole of the old "A floor, not a bill" paragraph, on the row's title.
  const why = [
    "Energy is summed over observed windows only; unobserved time counts as nothing and is never estimated.",
    !complete ? "Not every machine reports power." : null,
    partial
      ? `Readings cover ${Math.round((cost.measured.observedFraction ?? 0) * 100)}% of the ${PERIOD_LABELS[period]}.`
      : null,
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <div className="mt-3 flex flex-wrap items-baseline gap-x-2 gap-y-1 border-t border-border-subtle pt-2.5 text-[12px] leading-[1.5] text-text-tertiary">
      {multiDomain && <span className="text-text-secondary">{domainLabel(domain)}</span>}
      {cost.perKwh === null ? (
        <>
          <span className="mf-metric text-text-primary">{formatKWh(cost.measured.energyKWh)}</span>
          <Link href="/settings?tab=preferences" className="text-accent hover:underline">
            Set a rate
          </Link>
        </>
      ) : (
        <span className="text-text-secondary" title={why}>
          At least{" "}
          <span className="mf-metric text-text-primary">
            {formatMoney(cost.measured.cost, cost.currency)}
          </span>
          {showEstimated ? (
            <>
              {" · "}
              <span className="mf-metric text-text-primary">
                ≈{formatMoney(cost.estimated.cost, cost.currency)}
              </span>{" "}
              modelled, not measured
            </>
          ) : null}
          <span className="text-text-tertiary"> · {formatKWh(cost.measured.energyKWh)}</span>
        </span>
      )}
    </div>
  );
}

/* ---------------------------------------------------------------------------
 * The series swatch — the chart's legend, carried beside each readout.
 *
 * A 14px rule in the line's own stroke, dashed exactly as the line is dashed.
 * It is the ONLY thing in the readout wearing the series colour; every word
 * beside it wears a text token, and "modelled, not measured" is written out,
 * so a reader who cannot tell blue from violet loses nothing.
 * ------------------------------------------------------------------------- */

function SeriesMark({ seriesKey, estimated }: { seriesKey: string; estimated: boolean }) {
  return (
    <svg width="14" height="8" viewBox="0 0 14 8" aria-hidden="true" className="shrink-0">
      <line
        x1="0"
        y1="4"
        x2="14"
        y2="4"
        stroke={STROKE[seriesKey] ?? "var(--mf-blue)"}
        strokeWidth="2"
        strokeDasharray={estimated ? "4 3" : undefined}
      />
    </svg>
  );
}

/* ---------------------------------------------------------------------------
 * Coverage, at two lengths.
 *
 * `coverageShort` is what goes on the screen; `coverageDetail` is the full
 * sentence plus the shortfall, for the tooltip and for the chart's accessible
 * name. Both are derived from the same numbers, so they cannot disagree.
 * ------------------------------------------------------------------------- */

interface Coverage {
  machinesTotal: number;
  machinesReporting: number;
  machinesAPIPolled: number;
}

/** "4 / 6 reporting". */
function coverageShort(coverage: Coverage): string {
  return `${coverage.machinesReporting} / ${coverage.machinesTotal} reporting`;
}

function coverageDetail(coverage: Coverage): string {
  const shortfall = shortfallSentence(coverage) as string | null;
  return shortfall
    ? `${coverageSentence(coverage)} ${shortfall}.`
    : (coverageSentence(coverage) as string);
}

/* ---------------------------------------------------------------------------
 * Empty state
 *
 * One short line, and a second one only where there is a number to give. It is
 * sized to its content and CENTRED in whatever height the paired-panel row
 * gives the pane: a dashed frame stretched to an arbitrary height reads as a
 * chart that failed to load, which is a different and wrong claim.
 *
 * The paragraph that used to explain WHERE power readings come from is on the
 * frame's tooltip. It answers a question an operator asks once.
 * ------------------------------------------------------------------------- */

function EmptyState({ title, hint, tip }: { title: string; hint?: string; tip?: string }) {
  return (
    <div className="flex h-full flex-col justify-center">
      <div
        className="mt-4 flex items-center gap-3 rounded-[10px] border border-dashed border-border-subtle px-4 py-4"
        title={tip}
      >
        <Zap className="h-4 w-4 shrink-0 text-text-tertiary" aria-hidden="true" />
        <div className="min-w-0">
          <p className="text-[13px] text-text-primary">{title}</p>
          {hint && <p className="mt-0.5 text-[12px] text-text-tertiary">{hint}</p>}
        </div>
      </div>
    </div>
  );
}

function formatTime(timestamp: number): string {
  return new Date(timestamp).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

