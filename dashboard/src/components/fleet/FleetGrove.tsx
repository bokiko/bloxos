"use client";

// Grove Workspace body + context rail, over the shared fleet model.

import Link from "next/link";
import type { CSSProperties } from "react";
import { useFleetData } from "./useFleetData";
import { pct, pct1, statusOf, statusVar, ramPct, type Metric, type RankRow } from "./fleetModel";

function HealthCard({ label, value }: { label: string; value: Metric }) {
  return (
    <article>
      <small>{label}</small>
      <strong>
        {value === null ? "N/A" : Math.round(value)}
        {value !== null && <span className="ld-unit">%</span>}
      </strong>
      <div className="ld-bar">{value !== null && <i style={{ width: `${Math.min(100, value)}%` }} />}</div>
    </article>
  );
}

function Ranks({ title, rows }: { title: string; rows: RankRow[] }) {
  return (
    <section>
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

export function FleetGrove() {
  const { sorted, agg, hasReceivedData } = useFleetData();
  const connectivity =
    agg.total === 0
      ? hasReceivedData
        ? "No machines"
        : "Waiting for telemetry"
      : agg.online === agg.total
        ? "All machines reporting"
        : `${agg.total - agg.online} not reporting`;
  return (
    <>
      <header className="lg-top">
        <div>
          <h1>Fleet overview</h1>
          <p>A little space to see the whole picture.</p>
        </div>
      </header>

      <section className="lg-hero">
        <div>
          <div className="ld-label">Connected machines</div>
          <div className="lg-online">
            {agg.online} <span>/ {agg.total}</span>
          </div>
          <p>{connectivity}</p>
        </div>
        <div className="lg-orb" style={{ "--pct": agg.onlinePct ?? 0 } as CSSProperties}>
          <div className="lg-orb-core">
            <strong>
              {agg.onlinePct === null ? "N/A" : Math.round(agg.onlinePct)}
              {agg.onlinePct !== null && <span style={{ fontSize: 14 }}>%</span>}
            </strong>
            <small>FLEET ONLINE</small>
          </div>
        </div>
      </section>

      <div className="lg-health">
        <HealthCard label="Avg CPU" value={agg.avgCpu} />
        <HealthCard label="Avg RAM" value={agg.avgRam} />
        <HealthCard label="Avg GPU util" value={agg.avgGpuUtil} />
        <HealthCard label="Avg VRAM" value={agg.avgVram} />
      </div>

      <div className="lg-ranks">
        <Ranks title="Top GPU utilization" rows={agg.topGpu} />
        <Ranks title="Top VRAM usage" rows={agg.topVram} />
      </div>

      <section className="lg-fleet">
        <h2>Your machines</h2>
        {sorted.length === 0 && <p className="ld-note">No machines yet</p>}
        {sorted.map((m) => {
          const s = statusOf(m);
          return (
            <Link className="lg-machine" href={`/machine/${m.machine_id}`} key={m.machine_id}>
              <div>
                <strong>
                  <span className="ld-dot" style={{ "--dot": statusVar(s) } as CSSProperties} />
                  {m.hostname || m.machine_id}
                </strong>
                <small>
                  {(m.os || "unknown OS")}
                  {m.gpus && m.gpus.length > 0 ? ` · ${m.gpus[0].name}` : ""}
                </small>
              </div>
              <div className="lg-values">
                {pct(typeof m.cpu_percent === "number" ? m.cpu_percent : null)} CPU &nbsp;
                {pct(ramPct(m))} RAM
                <small>{s === "offline" ? "Offline" : "Online"}</small>
              </div>
            </Link>
          );
        })}
      </section>
      <p className="ld-note">Grove Workspace</p>
    </>
  );
}

export function FleetGroveRail() {
  const { agg, sessionCount, sessionMachines, alertsCount } = useFleetData();
  return (
    <>
      <h2>At this moment</h2>
      <article className="lg-session-hero">
        <div className="ld-label">AI Sessions</div>
        <strong>{sessionCount === null ? "N/A" : sessionCount}</strong>
        <small>
          {sessionCount === null
            ? "Monitoring unavailable"
            : sessionCount === 0
              ? "No active sessions"
              : `Running on ${sessionMachines} machine${sessionMachines !== 1 ? "s" : ""}`}
        </small>
      </article>
      <article className="lg-rail-card">
        <div className="ld-label">Max GPU temperature</div>
        <div className="lg-reading">
          {agg.maxGpuTemp === null ? "N/A" : Math.round(agg.maxGpuTemp)}
          {agg.maxGpuTemp !== null && <span className="ld-unit">°C</span>}
        </div>
        <p>Maximum across the fleet.</p>
      </article>
      <article className="lg-rail-card">
        <div className="ld-label">Fleet GPU power</div>
        <div className="lg-reading">
          {agg.gpuPowerTotal === null ? "N/A" : Math.round(agg.gpuPowerTotal)}
          {agg.gpuPowerTotal !== null && <span className="ld-unit">W</span>}
        </div>
        <p>{agg.gpuPowerTotal !== null && !agg.gpuPowerComplete ? "Partial — some devices not reporting." : "Component telemetry."}</p>
      </article>
      <article className="lg-rail-card">
        <div className="ld-label">Active alerts</div>
        <div className="lg-reading">{alertsCount}</div>
        <p>
          <span className="ld-dot" style={{ "--dot": statusVar(alertsCount === 0 ? "live" : "warning") } as CSSProperties} />
          {alertsCount === 0 ? " No active alerts." : ` ${alertsCount} needing attention.`}
        </p>
      </article>
    </>
  );
}
