"use client";

/* ============================================================================
 * The one chart tooltip.
 *
 * Every chart in the product — fleet power, machine metrics, component power
 * history — hands recharts this component, so the surface, the type scale and
 * the way a missing reading is written cannot drift between them.
 *
 * The rule it exists to enforce: a null value prints "—", never 0. Recharts
 * gives a gap point a `value` of null, and `Number(null)` is 0, so a tooltip
 * that formats blindly turns "the agent sent nothing for this window" into
 * "the machine drew zero watts" — the one mistake the power features are
 * built to avoid.
 * ========================================================================== */

export interface ChartTooltipEntry {
  value?: number | string | null;
  name?: string | number;
  color?: string;
  dataKey?: string | number;
}

export interface ChartTooltipProps {
  active?: boolean;
  payload?: ChartTooltipEntry[];
  label?: string | number;
  /** Renders the x value as its heading — usually a clock time. */
  labelFormatter?: (label: string | number) => string;
  /** Returns [value, name] for one series, exactly like recharts' own. */
  formatter?: (value: number | string | null | undefined, name?: string | number) => [string, string];
}

export function ChartTooltip({ active, payload, label, labelFormatter, formatter }: ChartTooltipProps) {
  if (!active || !payload?.length) return null;

  // A hovered gap has entries whose value is null. There is nothing to say
  // about that instant, so say nothing rather than inventing a zero row.
  const rows = payload.filter((entry) => entry.value !== null && entry.value !== undefined);
  if (rows.length === 0) return null;

  return (
    <div className="mf-chart-tooltip">
      {label !== undefined && (
        <div className="mf-kicker">{labelFormatter ? labelFormatter(label) : String(label)}</div>
      )}
      {rows.map((entry, index) => {
        const [value, name] = formatter
          ? formatter(entry.value, entry.name)
          : [String(entry.value), String(entry.name ?? entry.dataKey ?? "")];
        return (
          <div className="mf-chart-tooltip-row" key={`${entry.dataKey ?? index}`}>
            <i style={{ background: entry.color }} aria-hidden="true" />
            <span className="text-text-secondary">{name}</span>
            <span className="mf-metric text-text-primary">{value}</span>
          </div>
        );
      })}
    </div>
  );
}
