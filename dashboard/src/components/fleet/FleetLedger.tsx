"use client";

// Ledger — a still, type-led read of the fleet. Three bands answer the three
// questions an operator actually asks, in that order: does anything need me,
// what is running right now, and what do I have?
//
// Deliberate departures from the other live layouts:
//   * Nothing animates, and no container moves or resizes on update. Values are
//     swapped in place so a number stays readable while it changes.
//   * Colour is reserved for severity. A fleet with nothing wrong renders in
//     greyscale; anything coloured is something to look at. The status dot is
//     neutral for a healthy machine and only takes a semantic colour when the
//     machine is stale or offline — the status word is always spelled out, so
//     colour adds emphasis, never meaning on its own.
//   * Every figure states what it excludes. Averages are fresh-only and GPU
//     power says when it is a partial sum: the model already tracks this, so
//     show it rather than implying a completeness we do not have.
//
// All values come from the shared fleet model over real SSE data. An
// unavailable reading is "N/A" — never a fabricated zero (AGENTS.md).

import Link from "next/link";
import { useMemo, type CSSProperties } from "react";
import { useFleetData } from "./useFleetData";
import { pct, statusOf, statusVar, ramPct, diskPct, type Metric } from "./fleetModel";

function num(value: Metric, unit = ""): string {
  return value === null ? "N/A" : `${Math.round(value)}${unit}`;
}

/** Compact age since a machine last reported. Freshness is a first-class
 * column here: the model already gates averages on it, so the operator should
 * be able to see it rather than infer it from a status word. */
function since(lastSeen: number | undefined, now: number): string {
  if (!lastSeen) return "never";
  const seconds = Math.max(0, Math.round((now - lastSeen) / 1000));
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `${hours}h`;
  return `${Math.round(hours / 24)}d`;
}

function Figure({ label, value, unit, note }: { label: string; value: string; unit?: string; note: string }) {
  // An unavailable reading is real information, but it is not a measurement:
  // rendered at full size it gives dead fields the same weight as live data
  // and the eye stops on nothing. Set smaller and muted so it reads as absent.
  const unavailable = value === "N/A";
  return (
    <div className="ll-figure">
      <small>{label}</small>
      <strong className={unavailable ? "ll-absent" : undefined}>
        {value}
        {unit && <span className="ll-unit">{unit}</span>}
      </strong>
      <p>{note}</p>
    </div>
  );
}

/** A counted inventory list — one row per distinct thing, biggest first. */
function Census({ title, note, rows, empty }: {
  title: string; note: string; rows: { name: string; count: number }[]; empty: string;
}) {
  const max = rows.reduce((m, r) => Math.max(m, r.count), 0);
  return (
    <section className="ll-census">
      <h2>{title}</h2>
      <p className="ll-sub">{note}</p>
      {rows.length === 0 ? (
        <p className="ll-none">{empty}</p>
      ) : (
        <ul>
          {rows.map((r) => (
            <li key={r.name}>
              <span className="ll-census-name">{r.name}</span>
              <span className="ll-census-track" aria-hidden="true">
                <i style={{ width: `${max > 0 ? (r.count / max) * 100 : 0}%` }} />
              </span>
              <span className="ll-census-count">{r.count}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

export function FleetLedger() {
  const { machines, sorted, agg, sessionCount, sessionMachines, alertsCount, hasReceivedData, now } = useFleetData();
  const offline = Math.max(0, agg.total - agg.online);

  // Band 1 is triage: only genuine problems appear, so an empty band is a
  // meaningful "nothing needs you" rather than a blank panel.
  const issues = useMemo(() => {
    const found: { key: string; count: number; label: string; note: string }[] = [];
    if (offline > 0) {
      found.push({ key: "offline", count: offline, label: "Offline",
        note: "No report inside the offline window" });
    }
    if (agg.stale > 0) {
      found.push({ key: "stale", count: agg.stale, label: "Stale telemetry",
        note: "Connected, but readings aged out of the fresh window and are excluded from every average below" });
    }
    if (alertsCount > 0) {
      found.push({ key: "alerts", count: alertsCount, label: "Active alerts",
        note: "Raised by your alert rules" });
    }
    return found;
  }, [offline, agg.stale, alertsCount]);

  const accelerators = useMemo(() => {
    const counts = new Map<string, number>();
    for (const m of machines) {
      for (const g of m.gpus ?? []) {
        const name = (g.name || "").trim() || "Unnamed device";
        counts.set(name, (counts.get(name) ?? 0) + 1);
      }
    }
    return [...counts.entries()]
      .map(([name, count]) => ({ name, count }))
      .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name));
  }, [machines]);

  const platforms = useMemo(() => {
    const counts = new Map<string, number>();
    for (const m of machines) {
      const name = (m.os || "").trim() || "Unreported";
      counts.set(name, (counts.get(name) ?? 0) + 1);
    }
    return [...counts.entries()]
      .map(([name, count]) => ({ name, count }))
      .sort((a, b) => b.count - a.count || a.name.localeCompare(b.name));
  }, [machines]);

  return (
    <div className="ll">
      <header className="ll-head">
        <div>
          <h1>Fleet</h1>
          <p>What needs you, what is running, what you have.</p>
        </div>
        <span className="ll-head-meta">
          {agg.total} machine{agg.total === 1 ? "" : "s"} on record
        </span>
      </header>

      {/* ---- Band 1 — does anything need me? --------------------------- */}
      <section className="ll-band">
        <h2 className="ll-band-title">Needs attention</h2>
        {!hasReceivedData && agg.total === 0 ? (
          <p className="ll-quiet">Waiting for the first telemetry report.</p>
        ) : issues.length === 0 ? (
          <p className="ll-quiet">Nothing needs attention.</p>
        ) : (
          <ul className="ll-issues">
            {issues.map((issue) => (
              <li key={issue.key}>
                <span className="ll-issue-count">{issue.count}</span>
                <span className="ll-issue-text">
                  <strong>{issue.label}</strong>
                  <small>{issue.note}</small>
                </span>
              </li>
            ))}
          </ul>
        )}
      </section>

      {/* ---- Band 2 — what is running right now? ----------------------- */}
      <section className="ll-band">
        <h2 className="ll-band-title">Running now</h2>

        {agg.total > 0 && agg.online - agg.stale <= 0 && (
          <p className="ll-stale-note">
            No machine has reported inside the fresh window, so no fleet average
            can be computed. The per-machine readings below are the last values
            received, not current ones.
          </p>
        )}

        <div className="ll-figures">
          <Figure
            label="Connected"
            value={`${agg.online}`}
            unit={`/${agg.total}`}
            note={agg.total === 0 ? "No machines enrolled"
              : agg.stale > 0 ? `${agg.stale} reporting stale`
                : "All reporting fresh"}
          />
          <Figure label="CPU" value={num(agg.avgCpu)} unit={agg.avgCpu === null ? "" : "%"}
            note="Mean across fresh machines" />
          <Figure label="Memory" value={num(agg.avgRam)} unit={agg.avgRam === null ? "" : "%"}
            note="Mean across fresh machines" />
          <Figure label="GPU power" value={num(agg.gpuPowerTotal)} unit={agg.gpuPowerTotal === null ? "" : "W"}
            note={agg.gpuPowerTotal === null ? "No device reported power"
              : agg.gpuPowerComplete ? "Summed from every reporting device"
                : "Partial sum — not every device reported"} />
          <Figure label="Hottest GPU" value={num(agg.maxGpuTemp)} unit={agg.maxGpuTemp === null ? "" : "°C"}
            note="Highest fresh device reading" />
          <Figure label="AI sessions" value={sessionCount === null ? "N/A" : `${sessionCount}`}
            note={sessionCount === null ? "Monitoring unavailable"
              : `Across ${sessionMachines} machine${sessionMachines === 1 ? "" : "s"}`} />
        </div>

        <table className="ll-table">
          <thead>
            <tr>
              <th scope="col">Machine</th>
              <th scope="col">Status</th>
              <th scope="col" className="ll-right">Last report</th>
              <th scope="col" className="ll-right">CPU</th>
              <th scope="col" className="ll-right">Memory</th>
              <th scope="col" className="ll-right">Disk</th>
              <th scope="col">Accelerator</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((m) => {
              const status = statusOf(m);
              const healthy = status === "live";
              return (
                <tr key={m.machine_id}>
                  <th scope="row">
                    <Link href={`/machine/${m.machine_id}`}>{m.hostname || m.machine_id}</Link>
                    <small>{m.os || "platform unreported"}</small>
                  </th>
                  <td>
                    <span
                      className={healthy ? "ll-dot" : "ll-dot ll-dot-alert"}
                      style={healthy ? undefined : ({ "--dot": statusVar(status) } as CSSProperties)}
                      aria-hidden="true"
                    />
                    {status === "live" ? "Online" : status.charAt(0).toUpperCase() + status.slice(1)}
                  </td>
                  <td className="ll-right ll-figures-num">{since(m.last_seen, now)}</td>
                  <td className="ll-right ll-figures-num">{pct(typeof m.cpu_percent === "number" ? m.cpu_percent : null)}</td>
                  <td className="ll-right ll-figures-num">{pct(ramPct(m))}</td>
                  <td className="ll-right ll-figures-num">{pct(diskPct(m))}</td>
                  <td>{m.gpus && m.gpus.length > 0 ? m.gpus[0].name : "—"}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {sorted.length === 0 && <p className="ll-none">No machines yet.</p>}
      </section>

      {/* ---- Band 3 — what do I have? ---------------------------------- */}
      <section className="ll-band">
        <h2 className="ll-band-title">Inventory</h2>
        <div className="ll-inventory">
          <Census
            title="Accelerators"
            note="One count per physical device reported by an agent"
            rows={accelerators}
            empty="No accelerators reported."
          />
          <Census
            title="Platforms"
            note="Machines by reported operating system"
            rows={platforms}
            empty="No machines enrolled."
          />
        </div>
        <p className="ll-footnote">
          Averages above use fresh machines only, so a stale or offline machine
          never pulls a fleet figure toward a value nothing is actually
          reporting. Counts here include every enrolled machine.
        </p>
      </section>
    </div>
  );
}
