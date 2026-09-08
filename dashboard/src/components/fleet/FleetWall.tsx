"use client";

// Operations Wall body — bento grid of fleet readings over the shared model.
// Real SSE data only; unavailable aggregates render "N/A", never zero.

import Link from "next/link";
import { Bot, ShieldCheck } from "lucide-react";
import type { CSSProperties } from "react";
import { useFleetData } from "./useFleetData";
import { pct, pct1, statusOf, statusVar, ramPct, diskPct, type Metric, type RankRow } from "./fleetModel";

function ResourceRow({ label, value }: { label: string; value: Metric }) {
  return (
    <div className="lw-resource-row">
      <span>{label}</span>
      <div className="ld-bar">{value !== null && <i style={{ width: `${Math.min(100, value)}%` }} />}</div>
      <strong>{pct(value)}</strong>
    </div>
  );
}

function Ranks({ title, rows }: { title: string; rows: RankRow[] }) {
  return (
    <div>
      <h2>{title}</h2>
      {rows.length === 0 && <div className="lw-empty">No data reported</div>}
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
    </div>
  );
}

export function FleetWall() {
  const { sorted, agg, sessionCount, sessionMachines, alertsCount, hasReceivedData } = useFleetData();
  const connectivity =
    agg.total === 0
      ? hasReceivedData
        ? "No machines"
        : "Waiting for telemetry"
      : agg.stale > 0
        ? `${agg.online} connected, ${agg.stale} stale`
        : agg.online === agg.total
          ? "All systems connected"
          : `${agg.total - agg.online} not reporting`;
  const connectivityStatus =
    agg.total === 0 ? "offline" : agg.online < agg.total ? "warning" : agg.stale > 0 ? "stale" : "live";

  return (
    <>
      <div className="lw-intro">
        <div>
          <h1>A clear view of your fleet.</h1>
          <p>Compute, activity and health — together in one place.</p>
        </div>
        <span className="lw-scope">All machines&nbsp; · &nbsp;{agg.total}</span>
      </div>

      <div className="lw-grid">
        <article className="lw-tile lw-fleet">
          <h2 className="ld-label">Connected machines</h2>
          <div className="lw-fleet-reading">
            <strong className="lw-massive">{agg.online}</strong>
            <span className="lw-denominator">/ {agg.total} connected</span>
          </div>
          <div className="lw-fleet-bottom">
            <span className="ld-dot" style={{ "--dot": statusVar(connectivityStatus) } as CSSProperties} />
            {connectivity}
          </div>
          <div
            className="lw-orbit"
            aria-label={`${pct(agg.onlinePct)} fleet connected`}
            style={{ "--pct": agg.onlinePct ?? 0 } as CSSProperties}
          >
            <span>
              {pct(agg.onlinePct)}
              <small>CONNECTED</small>
            </span>
          </div>
        </article>

        <article className="lw-tile lw-sessions">
          <h2 className="ld-label">AI Sessions</h2>
          <div className="lw-card-icon" aria-hidden="true">
            <Bot />
          </div>
          <strong className="lw-stat-big">{sessionCount === null ? "N/A" : sessionCount}</strong>
          <p>
            {sessionCount === null
              ? "Monitoring unavailable"
              : sessionCount === 0
                ? "No active sessions"
                : `Running on ${sessionMachines} machine${sessionMachines !== 1 ? "s" : ""}`}
          </p>
        </article>

        <article className="lw-tile lw-alerts">
          <h2 className="ld-label">Active alerts</h2>
          <div className="lw-card-icon" aria-hidden="true">
            <ShieldCheck />
          </div>
          <strong className="lw-stat-big">{alertsCount}</strong>
          <p>{alertsCount === 0 ? "No active alerts" : `${alertsCount} needing attention`}</p>
        </article>

        <article className="lw-tile lw-resources">
          <h2 className="ld-label">Fleet resource utilization</h2>
          <div className="lw-resource-bars">
            <ResourceRow label="CPU" value={agg.avgCpu} />
            <ResourceRow label="RAM" value={agg.avgRam} />
            <ResourceRow label="GPU util" value={agg.avgGpuUtil} />
            <ResourceRow label="VRAM" value={agg.avgVram} />
          </div>
        </article>

        <article className="lw-tile lw-ranks">
          <Ranks title="Top GPU utilization" rows={agg.topGpu} />
          <Ranks title="Top VRAM usage" rows={agg.topVram} />
        </article>

        <article className="lw-tile lw-foot-tile">
          <h2 className="ld-label">Max GPU temp</h2>
          <div className="lw-big">
            {agg.maxGpuTemp === null ? "N/A" : Math.round(agg.maxGpuTemp)}
            {agg.maxGpuTemp !== null && <span className="ld-unit">°C</span>}
          </div>
          <p>Hottest GPU across your fleet</p>
        </article>

        <article className="lw-tile lw-foot-tile">
          <h2 className="ld-label">GPU power</h2>
          <div className="lw-big">
            {agg.gpuPowerTotal === null ? "N/A" : Math.round(agg.gpuPowerTotal)}
            {agg.gpuPowerTotal !== null && <span className="ld-unit">W</span>}
          </div>
          <p>
            {agg.gpuPowerTotal === null
              ? "Component telemetry"
              : agg.gpuPowerComplete
                ? "Fleet total · component telemetry"
                : "Reported so far · partial telemetry"}
          </p>
        </article>

        <article className="lw-tile lw-machines">
          <h2 className="ld-label">Machine register</h2>
          <div className="lw-machine lw-machine-head">
            <span>HOST / PLATFORM</span>
            <span>CPU</span>
            <span>RAM</span>
            <span>DISK</span>
          </div>
          {sorted.length === 0 && <div className="lw-empty">No machines yet</div>}
          {sorted.map((m) => {
            const s = statusOf(m);
            return (
              <Link className="lw-machine" href={`/machine/${m.machine_id}`} key={m.machine_id}>
                <span>
                  <span className="ld-dot" style={{ "--dot": statusVar(s), marginRight: 7 } as CSSProperties} />
                  {m.hostname || m.machine_id}
                  <small>
                    {(m.os || "unknown OS")}
                    {m.gpus && m.gpus.length > 0 ? ` · ${m.gpus[0].name}` : ""}
                  </small>
                </span>
                <span>{pct(typeof m.cpu_percent === "number" ? m.cpu_percent : null)}</span>
                <span>{pct(ramPct(m))}</span>
                <span>{pct(diskPct(m))}</span>
              </Link>
            );
          })}
        </article>
      </div>
      <p className="ld-note" style={{ maxWidth: 1480, margin: "23px auto 0" }}>
        Operations Wall
      </p>
    </>
  );
}
