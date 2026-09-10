"use client";

/* ============================================================================
 * Monoform — "Fleet power".
 *
 * This pane replaced "Capacity over time", which stated honestly that no
 * fleet-wide history existed. One does now: GET /api/fleet/power/history
 * (hub/fleet_power.go) buckets every machine's stored power windows into one
 * time series and reports, alongside it, exactly how much of the fleet is
 * behind that series.
 *
 * Watts are money here — this fleet mines — so the temptation is to print one
 * big number. The pane refuses to, in three specific ways:
 *
 *   1. A MEASURED reading and a MODELLED one are never added. A machine with
 *      no power counter can report an estimate (proto/powerhistory,
 *      SourceEstimateUtil); that estimate is a second line, a second readout
 *      and a second cost, and it says "estimated" in words every time. There
 *      is no code path in this file, or in lib/fleet-power.mjs, that produces
 *      their sum.
 *   2. A partial sum is never labelled a total. Coverage — "4 of 6 machines
 *      reporting power" — sits directly under the number, always, and names
 *      the API-polled machines that cannot report at all separately from the
 *      ones that simply are not.
 *   3. Where no machine has a whole-platform counter, the pane does NOT fall
 *      back to adding CPU and GPU watts together and calling it fleet power.
 *      It charts them as two separate component lines and says so.
 *
 * The lines are flat strokes on a hairline grid: no fill, no gradient, no
 * glow, no gauge. Measured is solid, estimated is dashed, and both carry a
 * text label — the state is never conveyed by colour alone. Blue is the
 * product's one accent and violet is its GPU tone; neither is a status colour,
 * which is why green/amber/red appear here only for a real warning, with words.
 * ========================================================================== */

import { useEffect, useMemo, useState } from "react";
import { CartesianGrid, Line, LineChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts";
import { Zap } from "lucide-react";

import { DEMO_MODE, HUB_URL, getStoredToken } from "@/lib/session";
import { MF_INPUT } from "@/lib/monoform-classes";
import { POWER_CURRENCIES, POWER_MAX_RATE } from "@/lib/workspace-prefs.mjs";
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
import { Disclosure } from "./Disclosure";
import type { PowerPeriod, PowerRate } from "./useWorkspacePrefs";

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
  open: boolean;
  onToggle: () => void;
  period: PowerPeriod;
  onPeriodChange: (period: PowerPeriod) => void;
  rate: PowerRate;
  onRateChange: (rate: PowerRate) => void;
}

export function FleetPowerPane({
  open,
  onToggle,
  period,
  onPeriodChange,
  rate,
  onRateChange,
}: FleetPowerPaneProps) {
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

  const summary = summaryFor(readouts, coverage);

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
    <section
      className="mf-section min-w-0"
      aria-label="Fleet power"
      data-open={open ? "true" : "false"}
    >
      <Disclosure
        id="capacity"
        label="Fleet power"
        open={open}
        onToggle={onToggle}
        summary={summary}
        actions={open ? periodSwitch : null}
      >
        {DEMO_MODE ? (
          <EmptyState
            title="Fleet power is not part of the demo data."
            body="Power comes from real counters on real machines — RAPL, a battery, a BMC — so there is nothing here to simulate. Connect a hub to see it."
          />
        ) : mode === "none" ? (
          <EmptyState
            title={
              error && !data
                ? "Fleet power unavailable."
                : !data
                  ? "Loading fleet power…"
                  : coverage && coverage.machinesTotal === 0
                    ? "No machines in this fleet."
                    : "No machine is reporting power."
            }
            body={
              error && !data
                ? error
                : !data
                  ? "Reading the last window of power history."
                  : coverage && coverage.machinesTotal === 0
                    ? "Add a machine and its agent will start reporting power once it finds a counter."
                    : "The agent reports power only where the hardware can measure it — RAPL, a battery, a BMC, or a board sensor. A machine with no counter at all reports nothing rather than a guess."
            }
            footer={coverage ? (coverageSentence(coverage) as string) : undefined}
          />
        ) : (
          <>
            <p className="mf-table-meta mt-3">
              {mode === "system"
                ? `Whole-machine power, ${PERIOD_LABELS[period]}`
                : `Component power, ${PERIOD_LABELS[period]} — not wall power`}
            </p>

            {/* THE READOUTS. One per line on the chart, each with its own
                machine count. They sit side by side and are never totalled:
                "142 W measured" beside "≈18 W estimated" is two facts, and
                160 W is not a third. */}
            <div className="mt-4 flex flex-wrap items-start gap-x-8 gap-y-3">
              {readouts.map((r) => (
                <div key={r.key} className="min-w-0">
                  <p className="mf-metric text-[21px] leading-none text-text-primary">
                    {r.kind === "estimated" && r.latest ? "≈ " : ""}
                    {formatWatts(r.latest?.watts ?? null)}
                  </p>
                  <p className="mt-1.5 text-[11px] leading-[1.5] text-text-tertiary">
                    <span className="text-text-secondary">{r.label}</span>
                    {r.kind === "estimated" ? " — modelled, not measured" : ""}
                    <br />
                    {r.latest
                      ? `${r.latest.machines} of ${coverage?.machinesTotal ?? 0} machines`
                      : "no reading in this window"}
                  </p>
                </div>
              ))}
            </div>

            {/* COVERAGE. Directly under the numbers, always — this is the line
                that stops a partial sum reading as a fleet total. */}
            {coverage && (
              <p className="mt-3 text-[12px] leading-[1.5] text-text-secondary">
                {coverageSentence(coverage)}
                {shortfallSentence(coverage) ? (
                  <span className="text-text-tertiary"> {shortfallSentence(coverage)}.</span>
                ) : null}
              </p>
            )}

            {error && (
              <p role="status" className="mt-2 flex items-start gap-2 text-[12px] text-status-warning">
                <span className="mf-status-dot mf-status-warning mt-1.5" aria-hidden="true" />
                <span>{error} The readings above may be stale.</span>
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

            <div
              className="mt-4 h-[132px]"
              role="img"
              aria-label={`${mode === "system" ? "Whole-machine" : "Component"} power in watts across the fleet, ${PERIOD_LABELS[period]}. ${
                coverage ? coverageSentence(coverage) : ""
              }`}
            >
              <ResponsiveContainer width="100%" height="100%">
                <LineChart data={rows} margin={{ top: 4, right: 8, bottom: 0, left: 0 }}>
                  <CartesianGrid stroke="var(--border-subtle)" strokeDasharray="2 4" vertical={false} />
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
                    labelFormatter={(label) => formatTime(Number(label))}
                    formatter={(value, name) => [
                      formatWatts(typeof value === "number" ? value : null),
                      String(name),
                    ]}
                    cursor={{ stroke: "var(--border-strong)", strokeWidth: 1 }}
                    contentStyle={{
                      background: "var(--surface-overlay)",
                      border: "1px solid var(--border-default)",
                      borderRadius: 10,
                      color: "var(--text-primary)",
                      fontSize: 11,
                    }}
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

            <p className="mt-2 text-[11px] leading-[1.5] text-text-tertiary">
              {series.some((s) => s.kind === "estimated")
                ? "Solid line: measured by a counter · dashed line: modelled from utilisation. They are never added together."
                : "Solid line: measured by a counter."}{" "}
              Unreported buckets are left blank — missing data is not zero power.
            </p>

            <CostRow
              cost={cost}
              domain={costDomain}
              period={period}
              rate={rate}
              onRateChange={onRateChange}
              complete={domainOf(data, costDomain).complete === true}
            />
          </>
        )}
      </Disclosure>
    </section>
  );
}

/* ---------------------------------------------------------------------------
 * Cost
 *
 * Energy is summed over OBSERVED windows only (the hub does not interpolate),
 * so every figure here is a floor and is labelled "at least". The tariff has
 * no default: an invented rate would put a confident, specific price under a
 * chart of real watts, so until the operator states one the row asks for it.
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
  rate,
  onRateChange,
  complete,
}: {
  cost: { currency: string; perKwh: number | null; measured: CostKind; estimated: CostKind };
  domain: string;
  period: PowerPeriod;
  rate: PowerRate;
  onRateChange: (rate: PowerRate) => void;
  complete: boolean;
}) {
  // While the field is being typed in, its own text wins: "0." must survive
  // the keystroke, and parsing it back to 0 and re-rendering "0" would eat the
  // decimal point. `null` means "follow the stored rate", which is the state
  // the field returns to on blur — so a rate changed elsewhere (a different
  // user signing in) is picked up without an effect that fights the cursor.
  const [draft, setDraft] = useState<string | null>(null);
  const text = draft ?? (rate.per_kwh === null ? "" : String(rate.per_kwh));

  const commit = (next: string) => {
    setDraft(next);
    const value = Number.parseFloat(next);
    onRateChange({
      currency: rate.currency,
      per_kwh: next.trim() !== "" && Number.isFinite(value) ? value : null,
    });
  };

  const showEstimated = cost.estimated.machines > 0;
  const partial = cost.measured.observedFraction !== null && cost.measured.observedFraction < 0.995;

  return (
    <div className="mt-4 border-t border-border-subtle pt-3">
      <div className="flex flex-wrap items-center justify-between gap-x-5 gap-y-2.5">
        <div className="min-w-0">
          {cost.perKwh === null ? (
            <p className="text-[12px] leading-[1.5] text-text-secondary">
              {formatKWh(cost.measured.energyKWh)} of {domainLabel(domain)} energy over the{" "}
              {PERIOD_LABELS[period]}. Enter a rate to cost it.
            </p>
          ) : (
            <p className="text-[12px] leading-[1.5] text-text-secondary">
              At least{" "}
              <span className="mf-metric text-text-primary">
                {formatMoney(cost.measured.cost, cost.currency)}
              </span>{" "}
              measured
              {showEstimated ? (
                <>
                  {" · "}
                  <span className="mf-metric text-text-primary">
                    ≈{formatMoney(cost.estimated.cost, cost.currency)}
                  </span>{" "}
                  estimated
                </>
              ) : null}
              <span className="text-text-tertiary">
                {" "}
                — {formatKWh(cost.measured.energyKWh)}
                {showEstimated ? ` + ${formatKWh(cost.estimated.energyKWh)} modelled` : ""} over the{" "}
                {PERIOD_LABELS[period]}
              </span>
            </p>
          )}
          {/* Why "at least". Both reasons are separate facts and both matter:
              a machine that reported for half the window, and a fleet where
              only some machines report at all. */}
          {(partial || !complete) && (
            <p className="mt-1 text-[11px] leading-[1.5] text-text-tertiary">
              A floor, not a bill:{" "}
              {[
                !complete ? "not every machine reports power" : null,
                partial
                  ? `readings cover ${Math.round((cost.measured.observedFraction ?? 0) * 100)}% of the window`
                  : null,
              ]
                .filter(Boolean)
                .join(" · ")}
              . Unobserved time is counted as nothing, never estimated.
            </p>
          )}
        </div>

        <div className="flex shrink-0 items-center gap-1.5">
          <label htmlFor="mf-power-rate" className="text-[11px] text-text-tertiary">
            Rate
          </label>
          <input
            id="mf-power-rate"
            type="number"
            inputMode="decimal"
            min={0}
            max={POWER_MAX_RATE}
            step="0.001"
            placeholder="0.00"
            value={text}
            onChange={(event) => commit(event.target.value)}
            onBlur={() => setDraft(null)}
            className={`${MF_INPUT} mf-metric h-8 w-[84px] border px-2 text-right`}
          />
          <select
            aria-label="Currency"
            value={rate.currency}
            onChange={(event) => onRateChange({ currency: event.target.value, per_kwh: rate.per_kwh })}
            className={`${MF_INPUT} h-8 border px-1.5`}
          >
            {(POWER_CURRENCIES as string[]).map((code) => (
              <option key={code} value={code}>
                {code}
              </option>
            ))}
          </select>
          <span className="text-[11px] text-text-tertiary">/kWh</span>
        </div>
      </div>
    </div>
  );
}

/* ---------------------------------------------------------------------------
 * Empty state
 *
 * Sized to its content and CENTRED in whatever height the paired-panel row
 * gives the pane, exactly as the pane it replaced was: a dashed frame stretched
 * to an arbitrary height reads as a chart that failed to load, which is a
 * different and wrong claim.
 * ------------------------------------------------------------------------- */

function EmptyState({ title, body, footer }: { title: string; body: string; footer?: string }) {
  return (
    <div className="flex h-full flex-col justify-center">
      <div className="mt-5 flex items-start gap-4 rounded-[10px] border border-dashed border-border-subtle px-5 py-6">
        <Zap className="mt-0.5 h-4 w-4 shrink-0 text-text-tertiary" aria-hidden="true" />
        <div className="min-w-0">
          <p className="text-[13px] font-medium text-text-primary">{title}</p>
          <p className="mt-1.5 max-w-[62ch] text-[12px] leading-[1.55] text-text-tertiary">{body}</p>
          {footer && <p className="mt-2 text-[12px] text-text-tertiary">{footer}</p>}
        </div>
      </div>
    </div>
  );
}

function formatTime(timestamp: number): string {
  return new Date(timestamp).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

/**
 * The one line a folded pane still says. It names the kind of every number it
 * shows, so a collapsed estimate can never be mistaken for a measurement.
 */
function summaryFor(
  readouts: { kind: string; latest: { watts: number } | null }[],
  coverage?: { machinesTotal: number; machinesReporting: number },
): string {
  const measured = readouts.find((r) => r.kind === "measured" && r.latest);
  const estimated = readouts.find((r) => r.kind === "estimated" && r.latest);
  const parts: string[] = [];
  if (measured?.latest) parts.push(formatWatts(measured.latest.watts) as string);
  if (estimated?.latest) parts.push(`≈${formatWatts(estimated.latest.watts)} est`);
  if (parts.length === 0) return "no power data";
  if (coverage) parts.push(`${coverage.machinesReporting}/${coverage.machinesTotal}`);
  return parts.join(" · ");
}
