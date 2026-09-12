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

import { useEffect, useMemo, useRef, useState } from "react";
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { ChartTooltip } from "@/components/charts/ChartTooltip";
import { Zap } from "lucide-react";

import { DEMO_MODE, HUB_URL, getStoredToken } from "@/lib/session";
import { freshnessNote } from "@/lib/power-freshness.mjs";
import {
  PERIOD_LABELS,
  POWER_PERIODS,
  coverageSentence,
  domainLabel,
  fleetPowerChartRows,
  fleetPowerEnergyAvailability,
  fleetPowerWarnings,
  formatWatts,
  currentReading,
  currentDomainOf,
  domainOf,
  combinedSeries,
  resolveDomainChoice,
  availableDomains,
  POWER_DOMAINS,
  normalizeFleetPowerCurrent,
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

/**
 * The current snapshot, from GET /api/fleet/power/current. Kept distinct from
 * the history type so nothing can pass one where the other belongs — that
 * substitution is what let a charted bucket be presented as a current reading.
 */
interface FleetPowerCurrent {
  generatedUnixMS: number | null;
  machinesTotal: number;
  machinesReporting: number;
  domains: Map<string, unknown>;
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
  /**
   * Retained so the Overview's props do not churn while energy accounting is
   * withheld. The pane prices nothing today; see CostRow.
   */
  rate?: PowerRate;
}

/**
 * The reader's chosen domain, per browser.
 *
 * Deliberately localStorage rather than an account preference: this is a view
 * choice, and adding an account field would mean a schema change this work does
 * not otherwise need. Every access is guarded — a cookie-blocked browser throws
 * on access, and a throw during render would take the page down.
 */
const DOMAIN_STORAGE_KEY = "bloxos.fleetPower.domain";

function readStoredDomain(): string | null {
  try {
    const raw = window.localStorage.getItem(DOMAIN_STORAGE_KEY);
    return raw && (POWER_DOMAINS as string[]).includes(raw) ? raw : null;
  } catch {
    return null;
  }
}

function writeStoredDomain(domain: string): void {
  try {
    window.localStorage.setItem(DOMAIN_STORAGE_KEY, domain);
  } catch {
    // A reader who cannot persist the choice still gets it for this session.
  }
}

export function FleetPowerPane({ period, onPeriodChange }: FleetPowerPaneProps) {
  const [state, setState] = useState<{
    period: string;
    data?: FleetPowerHistory;
    current?: FleetPowerCurrent;
    error?: string;
  }>({
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

        // The CURRENT reading is a separate request, deliberately. Reading it
        // off the charted buckets made it depend on the selected period and let
        // it reach hours backwards for the last non-null value. If this request
        // fails the readouts say unavailable — they must never fall back to the
        // history aggregate wearing a "current" label.
        let current: FleetPowerCurrent | undefined;
        try {
          const curRes = await fetch(`${HUB_URL}/api/fleet/power/current`, {
            headers: token ? { Authorization: `Bearer ${token}` } : {},
            signal: controller.signal,
          });
          if (curRes.ok) {
            current = normalizeFleetPowerCurrent(await curRes.json()) as FleetPowerCurrent;
          }
        } catch {
          // Leave it undefined: unavailable is the honest state.
        }
        if (!stopped) setState({ period, data, current });
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

  // DOMAIN CHOICE IS EXPLICIT AND LATCHED.
  //
  // It used to be automatic: any machine reporting system power took the whole
  // chart, hiding every other domain. That was survivable while only measured
  // counters existed and became actively harmful once a MODELLED system reading
  // could appear — one estimating board joining the fleet would evict the
  // measured CPU and GPU history of every other machine.
  //
  // So the reader chooses, the choice persists, and the initial default is
  // computed ONCE. Recomputing it per poll would let an arriving domain move
  // the view out from under someone mid-read.
  // SSR-SAFE: storage is read AFTER mount, never in the initialiser.
  //
  // Reading localStorage while computing initial state produces different
  // markup on the server (no window, so null) and in the browser (a stored
  // "dram", say), and React then reports a hydration mismatch and discards the
  // server render. So the first render is always the neutral default, and the
  // stored choice is applied in an effect on the client.
  const [domain, setDomain] = useState<string | null>(null);
  const latched = useRef(false);

  useEffect(() => {
    if (latched.current) return;
    const stored = readStoredDomain();
    if (stored) {
      latched.current = true;
      setDomain(stored);
      return;
    }
    // No stored choice: latch the computed default ONCE, so a domain arriving
    // on a later poll cannot move the view out from under the reader.
    if (!data) return;
    latched.current = true;
    setDomain(resolveDomainChoice(data, null) as string);
  }, [data]);

  const selected = domain ?? (resolveDomainChoice(data, null) as string);
  const chooseDomain = (next: string) => {
    latched.current = true;
    setDomain(next);
    writeStoredDomain(next);
  };

  // Series come from BOTH sources: a capped or empty history must not hide a
  // domain that is reporting right now.
  const current = state.current;
  const series = useMemo(
    () => (combinedSeries(data, current, selected) as Series[]) ?? [],
    [data, current, selected],
  );
  const rows = useMemo(() => fleetPowerChartRows(data, series), [data, series]);
  const warnings = useMemo(() => (fleetPowerWarnings(data) as string[]) ?? [], [data]);
  const coverage = data?.coverage;

  // The domain the energy row names. Energy itself is withheld (see CostRow),
  // but the row still names which domain it would have costed.
  const costDomain = selected;

  // Readouts come from the CURRENT snapshot, never from the charted history.
  // Each carries its own freshness, judged from its oldest contributor, so a
  // mostly-dark fleet cannot be made to read as current by one live machine.
  const readouts = series.map((s) => ({
    ...s,
    latest: currentReading(state.current, s.domain, s.kind) as
      | {
          watts: number;
          machines: number;
          sources: string[];
          freshness: { state: string; ageMS: number | null };
        }
      | null,
  }));


  // The selector only offers domains that have data, but the SELECTED domain is
  // always offered even when it goes quiet — a reader watching DRAM must not
  // have the control vanish underneath them the moment it stops reporting.
  // Offered domains come from BOTH sources. Deriving them from history alone
  // would let a truncated or empty history response hide a domain that is
  // reporting right now — the record cap drops the oldest rows, and a fleet
  // that only just started reporting has little history to show.
  const currentDomains = state.current
    ? (POWER_DOMAINS as string[]).filter((d) => {
        const c = currentDomainOf(state.current, d);
        return c.measured.machines > 0 || c.estimated.machines > 0;
      })
    : [];
  const offered = Array.from(
    new Set([...(availableDomains(data) as string[]), ...currentDomains, selected]),
  ).filter((d) => (POWER_DOMAINS as string[]).includes(d));

  const domainSwitch = (
    <div className="mf-segment" role="group" aria-label="Power domain">
      {(POWER_DOMAINS as string[])
        .filter((d) => offered.includes(d))
        .map((d) => (
          <button
            key={d}
            type="button"
            aria-pressed={d === selected}
            onClick={() => chooseDomain(d)}
            title={`Chart ${domainLabel(d)} power`}
          >
            {domainLabel(d)}
          </button>
        ))}
    </div>
  );

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
        {domainSwitch}
        {periodSwitch}
      </div>
      <div className="mf-power-anchor-body">
        {DEMO_MODE ? (
          <EmptyState
            title="Not in the demo data."
            tip="Power comes from real counters on real machines — RAPL, a battery, a BMC — so there is nothing here to simulate. Connect a hub to see it."
          />
        ) : series.length === 0 && !data ? (
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
            {/* The caveat the numbers cannot carry themselves. A component
                domain is not a machine's wall draw, and domains are never added
                across: CPU plus GPU omits everything else in the box. The
                whole-machine domain needs no such warning, so it does not get
                one. The window is on the segmented control in the header. */}
            {selected !== "system" && (
              <p className="mf-table-meta mt-3">Component power — not wall power</p>
            )}

            {/* WHAT IS MISSING FROM THIS DOMAIN, and why. A shrinking
                contributor count with no explanation reads as a bug; these
                counts say which machines were excluded and on what grounds.
                Unknown provenance is shown because it is deliberately excluded
                from both series — silence there would hide the exclusion. */}
            {(() => {
              const diag = state.current ? currentDomainOf(state.current, selected) : null;
              if (!diag) return null;
              const notes = [
                diag.staleMachines > 0 ? `${diag.staleMachines} stale` : null,
                diag.skewedMachines > 0 ? `${diag.skewedMachines} clock-skewed` : null,
                diag.unknownMachines > 0 ? `${diag.unknownMachines} source or scope unverified` : null,
                diag.unreadableMachines > 0 ? `${diag.unreadableMachines} unreadable` : null,
              ].filter(Boolean);
              if (notes.length === 0) return null;
              return (
                <p
                  className="mf-table-meta mt-1"
                  title="These machines reported this domain but were excluded from the current figure. Their last values are not carried forward."
                >
                  Excluded: {notes.join(" · ")}
                </p>
              );
            })()}

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
                    {/* What the number IS. Each contributor reports its own 30s
                        mean and those windows are not synchronised across
                        machines, so this is a sum of per-machine sample means —
                        not a measurement of the fleet at one instant. */}
                    <span title="Each machine reports a mean over its own 30-second window. Those windows are not synchronised across machines, so this is the sum of their sample means, not a simultaneous fleet measurement.">
                      — sum of sample means
                    </span>
                    <span>
                      ·{" "}
                      {/* The denominator is the CURRENT fleet size, not the
                          history window's. Mixing them would compare live
                          contributors against a count drawn from a different
                          span. */}
                      {r.latest
                        ? `${r.latest.machines} / ${state.current?.machinesTotal ?? coverage?.machinesTotal ?? 0} reporting`
                        : "no reading"}
                    </span>
                    {/* A value that is not current NEVER appears as a bare
                        number. The note carries the age of the OLDEST
                        contributor, because the sum is only as current as
                        that — one live machine does not refresh the rest. */}
                    {r.latest && freshnessNote(r.latest.freshness) ? (
                      <span className="text-status-warning">
                        · {freshnessNote(r.latest.freshness)}
                      </span>
                    ) : null}
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

            {/* Unknown provenance in the HISTORY, surfaced even when the
                current snapshot is unavailable. Without this, a domain whose
                only contributors carry an unrecognised backend would look
                simply empty, and the reason it is empty — that the hub refused
                to classify those readings rather than losing them — would be
                invisible. */}
            {(() => {
              const hist = domainOf(data, selected) as { unknown?: { machines: number } };
              const unknownMachines = hist?.unknown?.machines ?? 0;
              if (unknownMachines === 0) return null;
              return (
                <p
                  className="mf-table-meta mt-1"
                  title="Either the hub does not recognise the backend, or it recognises it and cannot vouch for what the sensor is wired across — a battery pack that may be supplying only part of the load, or a shunt whose rail its chip name does not identify. Excluded from both the measured and the modelled series rather than guessed into one. The stored readings are unchanged."
                >
                  {unknownMachines} machine{unknownMachines === 1 ? "" : "s"} whose power source or
                  scope is unverified — excluded from both series
                </p>
              );
            })()}

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
              aria-label={`${domainLabel(selected)} power in watts across the fleet, ${PERIOD_LABELS[period]}. ${
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

            <CostRow domain={costDomain} multiDomain={true} />
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
 * Energy and cost are WITHHELD in this release; see CostRow. The tariff itself
 * still lives in Settings and is untouched — it simply has nothing to price
 * until energy accounting can be stated honestly.
 * ------------------------------------------------------------------------- */
function CostRow({
  domain,
  multiDomain,
}: {
  domain: string;
  /** More than one domain is charted, so the row must name the one it means. */
  multiDomain: boolean;
}) {
  // Energy and cost are WITHHELD, not merely re-captioned.
  //
  // This row used to lead with a guaranteed-lower-bound phrasing over a cost
  // and a kWh figure. It never was a lower bound. A window's mean comes from the reads that
  // SUCCEEDED inside it, and the hub then weights that mean by the window's
  // whole span — one successful 300 W read in a 30 s window is credited as
  // though all thirty seconds were observed, and the unread seconds could have
  // drawn far less. The "readings cover N% of the period" caption beside it was
  // no better: it divided summed window spans by the period, and those spans
  // are not deduplicated and can include time belonging to a neighbouring
  // bucket.
  //
  // A number that wrong cannot be rescued by a caption, and this row has
  // nowhere to put the qualification it would need. So it says what it does not
  // know. Nothing here renders "0 kWh": unavailable accounting is not zero
  // energy, and that distinction is the entire point.
  const availability = fleetPowerEnergyAvailability();

  return (
    <div className="mt-3 flex flex-wrap items-baseline gap-x-2 gap-y-1 border-t border-border-subtle pt-2.5 text-[12px] leading-[1.5] text-text-tertiary">
      {multiDomain && <span className="text-text-secondary">{domainLabel(domain)}</span>}
      <span title={availability.reason}>Energy and cost unavailable</span>
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

