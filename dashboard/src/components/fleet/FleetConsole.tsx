"use client";

// Precision Console body — ticker + machine table + instrument row, over the
// shared fleet model. Real SSE data only; unavailable readings render "N/A".

import Link from "next/link";
import type { CSSProperties } from "react";
import { useFleetData } from "./useFleetData";
import { pct, pct1, statusOf, statusVar, ramPct, diskPct, type Metric, type RankRow } from "./fleetModel";

function Cell({ label, value, unit, sub }: { label: string; value: string; unit?: string; sub: string }) {
  return (
    <div className="lc-ticker-cell">
      <small>{label}</small>
      <strong>
        {value}
        {unit && <span className="ld-unit">{unit}</span>}
      </strong>
      <p>{sub}</p>
    </div>
  );
}

function Instrument({ title, rows }: { title: string; rows: RankRow[] }) {
  return (
    <section className="lc-instrument">
      <h2>{title}</h2>
      {rows.length === 0 && <p className="ld-note">No data reported</p>}
      {rows.map((r) => (
        <div className="ld-rank-item" key={r.machineId}>
          <div>
            <span>{r.name}</span>
            <span>{pct1(r.value)}</span>
          </div>
          <div className="ld-bar">
            <i style={{ width: `${Math.min(100, r.value)}%` }} />
          </div>
        </div>
      ))}
    </section>
  );
}

function num(value: Metric, unit = "%"): string {
  return value === null ? "N/A" : `${Math.round(value)}${unit}`;
}

export function FleetConsole() {
  const { sorted, agg, sessionCount, sessionMachines, alertsCount, hasReceivedData } = useFleetData();
  const onlineSub =
    agg.total === 0 ? (hasReceivedData ? "No machines" : "Waiting for telemetry") : agg.online === agg.total ? "All reporting" : "Some offline";

  return (
    <>
      <header className="lc-heading">
        <div>
          <h1>Fleet control</h1>
          <p>Machine-level detail. Shared resource visibility.</p>
        </div>
        <span>WORKSPACE / ALL MACHINES</span>
      </header>

      <div className="lc-ticker">
        <Cell label="Online" value={`${agg.online}`} unit={`/ ${agg.total}`} sub={onlineSub} />
        <Cell label="Avg CPU" value={num(agg.avgCpu, "")} unit={agg.avgCpu === null ? "" : "%"} sub="Fleet average" />
        <Cell label="Avg RAM" value={num(agg.avgRam, "")} unit={agg.avgRam === null ? "" : "%"} sub="Fleet average" />
        <Cell label="GPU power" value={agg.gpuPowerTotal === null ? "N/A" : `${Math.round(agg.gpuPowerTotal)}`} unit={agg.gpuPowerTotal === null ? "" : "W"} sub={agg.gpuPowerTotal !== null && !agg.gpuPowerComplete ? "Partial telemetry" : "Component telemetry"} />
        <Cell label="AI Sessions" value={sessionCount === null ? "N/A" : `${sessionCount}`} sub={sessionCount === null ? "Unavailable" : `On ${sessionMachines} machine${sessionMachines !== 1 ? "s" : ""}`} />
        <Cell label="Alerts" value={`${alertsCount}`} sub={alertsCount === 0 ? "No active alerts" : "Need attention"} />
      </div>

      <section className="lc-table-wrap">
        <div className="lc-table-title">
          <h2>Machine inventory</h2>
          <span>{sorted.length} RECORDS</span>
        </div>
        <table className="lc-table">
          <thead>
            <tr>
              <th>Machine / Platform</th>
              <th>Status</th>
              <th>CPU</th>
              <th>RAM</th>
              <th>Disk</th>
              <th>Hardware</th>
            </tr>
          </thead>
          <tbody>
            {sorted.map((m) => {
              const s = statusOf(m);
              const meter = (v: Metric) => (
                <>
                  {pct(v)}
                  <div className="lc-meter">{v !== null && <i style={{ width: `${Math.min(100, v)}%` }} />}</div>
                </>
              );
              return (
                <tr key={m.machine_id}>
                  <td>
                    <Link href={`/machine/${m.machine_id}`}>
                      {m.hostname || m.machine_id}
                      <small>{m.os || "unknown OS"}</small>
                    </Link>
                  </td>
                  <td>
                    <span className="ld-dot" style={{ "--dot": statusVar(s) } as CSSProperties} />{" "}
                    {s === "live" ? "Online" : s.charAt(0).toUpperCase() + s.slice(1)}
                  </td>
                  <td className="lc-number">{meter(typeof m.cpu_percent === "number" ? m.cpu_percent : null)}</td>
                  <td className="lc-number">{meter(ramPct(m))}</td>
                  <td className="lc-number">{meter(diskPct(m))}</td>
                  <td>{m.gpus && m.gpus.length > 0 ? m.gpus[0].name : "CPU worker"}</td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {sorted.length === 0 && <div className="lc-empty">No machines yet</div>}
      </section>

      <div className="lc-lower">
        <section className="lc-instrument">
          <h2>GPU telemetry</h2>
          <div className="lc-thermal">
            <div className="lc-thermal-reading">
              {agg.maxGpuTemp === null ? "N/A" : Math.round(agg.maxGpuTemp)}
              {agg.maxGpuTemp !== null && <span className="ld-unit">°C</span>}
            </div>
            <p>
              MAX GPU TEMPERATURE
              <br />
              Avg GPU utilization &nbsp; {num(agg.avgGpuUtil)}
              <br />
              Avg VRAM usage &nbsp; {num(agg.avgVram)}
            </p>
          </div>
        </section>
        <Instrument title="Top GPU utilization" rows={agg.topGpu} />
        <Instrument title="Top VRAM usage" rows={agg.topVram} />
      </div>

      <div className="lc-bottom">
        <span>
          {agg.total} MACHINES / {agg.online} ONLINE
        </span>
        <span>PRECISION CONSOLE</span>
      </div>
    </>
  );
}
