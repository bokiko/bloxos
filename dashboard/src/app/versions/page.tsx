"use client";
import { AppShell } from "@/components/shell/AppShell";

// Agent versions — what the hub serves, what each agent runs, and whether the
// rollout is allowed to announce updates.
//
// Monoform: the shell owns the title, the rail and the global actions. Every
// state on this page is a StatusMark (dot or icon plus words), because "is
// this agent blocked" must be readable without relying on the colour.

import { useEffect } from "react";
import {
  RefreshCw,
  Pause,
  Play,
  AlertTriangle,
  CheckCircle2,
  Clock,
  KeyRound,
  ShieldCheck,
} from "lucide-react";
import { AgentBinaryInfo, useVersions } from "@/contexts/VersionsContext";
import {
  agentProtocolNote,
  agentStatusLabel,
  binaryReleaseLabel,
  binaryStateLabel,
  buildBinaryCards,
} from "@/lib/versions-honesty.mjs";
import { useAuth } from "@/contexts/AuthContext";
import { StatusCell, StatusMark } from "@/components/MonoformStatus";
import {
  MF_BUTTON,
  MF_PANEL_HEAD,
  MF_PANEL_TITLE,
} from "@/lib/monoform-classes";

function shortSHA(sha: string | undefined): string {
  if (!sha) return "—";
  return sha.length > 12 ? sha.slice(0, 12) : sha;
}

function timeSince(iso: string | undefined): string {
  if (!iso) return "never";
  const ms = new Date(iso).getTime();
  if (!isFinite(ms)) return "never";
  const sec = Math.floor((Date.now() - ms) / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const d = Math.floor(hr / 24);
  return `${d}d ago`;
}

function AgentBinaryPanel({
  platform,
  testId,
  binary,
}: {
  platform: string;
  testId: string;
  binary: AgentBinaryInfo;
}) {
  // Available means only that the hub resolved bytes on a validated path.
  // Signing is a separate, hub-wide property shown by the signing banner;
  // a resolved path alone never earns a "trusted" label.
  const state = binaryStateLabel(binary);
  const available = state.available;
  const releaseLabel = binaryReleaseLabel(binary, Boolean(binary && "release" in binary));

  return (
    <section className="mf-panel" data-testid={`agent-binary-${testId}`}>
      <div className={MF_PANEL_HEAD}>
        <h3 className={MF_PANEL_TITLE}>{platform}</h3>
        <StatusCell
          tone={available ? "ok" : "critical"}
          label={state.label}
          Icon={available ? CheckCircle2 : AlertTriangle}
        />
      </div>
      {available ? (
        <dl className="px-6 py-5 space-y-3.5">
          <Field label="SHA">
            <span className="mf-metric text-[13px] text-text-primary">{shortSHA(binary.sha)}</span>
          </Field>
          <Field label="Modified">
            <span className="mf-metric text-[13px] text-text-secondary">{timeSince(binary.mtime)}</span>
          </Field>
          <Field label="Release">
            <span className="text-[13px] text-text-secondary">{releaseLabel}</span>
          </Field>
          <Field label="Source">
            <span className="font-mono text-[11px] text-text-secondary break-all">{binary.source}</span>
          </Field>
          <Field label="Path">
            <span className="font-mono text-[11px] text-text-secondary break-all">{binary.path}</span>
          </Field>
        </dl>
      ) : (
        <p className="px-6 py-5 text-[13px] leading-6 text-text-secondary break-words">{state.detail}</p>
      )}
    </section>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[86px_minmax(0,1fr)] items-baseline gap-3">
      <dt className="mf-kicker">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </div>
  );
}

export default function VersionsPage() {
  return <AppShell><VersionsContent /></AppShell>;
}

function VersionsContent() {
  const { data, loading, error, refresh, pauseRollout, resumeRollout } = useVersions();
  const { hasScope } = useAuth();
  const canManageRollout = hasScope("fleet.admin");

  // Per-architecture cards from a per-arch hub (clear CPU labels); legacy
  // two-card fallback for older hubs, whose "Linux" binary is whatever the
  // hub serves without an arch key — its CPU is not reported, so the card
  // stays unlabeled. A platform the hub has no binary for still renders a
  // card — its error names the missing build.
  const binaryCards = buildBinaryCards(data);

  useEffect(() => {
    refresh();
  }, [refresh]);

  return (
    <>
      <div className="mf-intro">
        <div className="min-w-0">
          <dl className="flex flex-wrap items-baseline gap-x-8 gap-y-3">
            <div className="flex items-baseline gap-2">
              <dt className="mf-kicker">Agents</dt>
              <dd className="mf-metric text-[19px] leading-none text-text-primary">
                {data ? data.agents.length : "—"}
              </dd>
            </div>
            <div className="flex items-baseline gap-2">
              <dt className="mf-kicker">Signing</dt>
              <dd>
                {data ? (
                  <StatusMark
                    tone={data.signing_enabled ? "ok" : "critical"}
                    label={data.signing_enabled ? "Enabled" : "Disabled"}
                    Icon={data.signing_enabled ? ShieldCheck : AlertTriangle}
                  />
                ) : (
                  <span className="text-[13px] text-text-tertiary">—</span>
                )}
              </dd>
            </div>
            <div className="flex items-baseline gap-2">
              <dt className="mf-kicker">Rollout</dt>
              <dd>
                {data ? (
                  <StatusMark
                    tone={data.rollout_paused ? "warning" : "ok"}
                    label={data.rollout_paused ? "Paused" : "Active"}
                    Icon={data.rollout_paused ? Pause : Play}
                  />
                ) : (
                  <span className="text-[13px] text-text-tertiary">—</span>
                )}
              </dd>
            </div>
          </dl>
          <div className="mt-5">
            <button type="button" onClick={refresh} disabled={loading} className={MF_BUTTON}>
              <RefreshCw className={`w-3.5 h-3.5 ${loading ? "animate-spin" : ""}`} aria-hidden />
              {loading ? "Refreshing…" : "Refresh versions"}
            </button>
          </div>
        </div>
        <p>
          Protocol-v1 agents verify signed updates against their pinned key. Protocol-v2 agents also
          enforce a signed release floor against downgrades. Windows revalidates the staged
          binary&apos;s SHA and signature on service restart, but still needs a manual rollback.
        </p>
      </div>

      <div className="space-y-8">
        {error && (
          <div
            role="alert"
            className="mf-panel flex items-start gap-3 border-status-critical/40 px-5 py-4"
          >
            <AlertTriangle className="w-4 h-4 text-status-critical mt-0.5 shrink-0" aria-hidden />
            <div>
              <p className="text-[13px] font-medium text-text-primary">Versions request failed</p>
              <p className="text-xs text-text-tertiary mt-1">{error}</p>
            </div>
          </div>
        )}

        {data && (
          <>
            <section className="mf-panel" data-testid="signing-status-banner">
              <div className={MF_PANEL_HEAD}>
                <h2 className={MF_PANEL_TITLE}>Update signing</h2>
                <StatusCell
                  tone={data.signing_enabled ? "ok" : "critical"}
                  label={data.signing_enabled ? "Enabled" : "Disabled"}
                  Icon={data.signing_enabled ? ShieldCheck : AlertTriangle}
                />
              </div>
              <p className="px-6 py-4 text-[13px] leading-6 text-text-secondary">
                {data.signing_enabled
                  ? "The hub can authenticate agent update announcements."
                  : data.signing_disabled_reason || "The hub cannot produce update signatures."}
              </p>
            </section>

            <section className="mf-panel">
              <div className={MF_PANEL_HEAD}>
                <h2 className={MF_PANEL_TITLE}>Fleet rollout</h2>
                <StatusCell
                  tone={data.rollout_paused ? "warning" : "ok"}
                  label={data.rollout_paused ? "Paused" : "Active"}
                  Icon={data.rollout_paused ? Pause : CheckCircle2}
                />
              </div>
              <div className="flex flex-wrap items-center justify-between gap-4 px-6 py-4">
                <div className="min-w-0">
                  <p className="text-[13px] text-text-secondary">
                    Controls update announcements for every platform.
                  </p>
                  {data.rollout_paused && data.pause_reason && (
                    <p className="mt-1.5 text-xs text-text-tertiary">Reason: {data.pause_reason}</p>
                  )}
                </div>
                {canManageRollout &&
                  (data.rollout_paused ? (
                    <button type="button" onClick={resumeRollout} className="mf-action inline-flex items-center gap-2">
                      <Play className="w-3.5 h-3.5" aria-hidden />
                      Resume rollout
                    </button>
                  ) : (
                    <button type="button" onClick={pauseRollout} className={MF_BUTTON}>
                      <Pause className="w-3.5 h-3.5" aria-hidden />
                      Pause rollout
                    </button>
                  ))}
              </div>
            </section>

            <section aria-labelledby="served-agent-binaries">
              <h2 id="served-agent-binaries" className="mb-4 text-[13px] font-semibold text-text-primary">
                Served agent binaries
              </h2>
              <div
                className={`grid gap-4 ${data.agent_binaries_by_arch ? "lg:grid-cols-3" : "lg:grid-cols-2"}`}
              >
                {binaryCards.map((card) => (
                  <AgentBinaryPanel
                    key={card.key}
                    platform={card.label}
                    testId={card.key}
                    binary={card.binary}
                  />
                ))}
              </div>
              {!data.agent_binaries_by_arch && (
                <p className="mt-3 text-[11px] text-text-tertiary">
                  Per-CPU details are unavailable from this hub version.
                </p>
              )}
            </section>

            <section className="mf-panel overflow-hidden">
              <div className={MF_PANEL_HEAD}>
                <h2 className={MF_PANEL_TITLE}>Reporting agents</h2>
                <span className="mf-kicker">
                  {data.agents.length} agent{data.agents.length === 1 ? "" : "s"}
                </span>
              </div>
              <div className="mf-table-wrap overflow-x-auto">
                <table className="mf-table">
                  <thead>
                    <tr>
                      <th>Hostname</th>
                      <th>Platform</th>
                      <th>Running SHA</th>
                      <th>Status</th>
                      <th>Key pinned</th>
                      <th>Blocked reason</th>
                      <th>Last connect</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.agents.length === 0 ? (
                      <tr>
                        <td colSpan={7} className="text-center text-[13px] text-text-tertiary">
                          No agents have reported their version yet
                        </td>
                      </tr>
                    ) : (
                      [...data.agents]
                        .sort((a, b) => a.hostname.localeCompare(b.hostname))
                        .map((agent) => {
                          const status = agentStatusLabel(agent, data);
                          return (
                            <tr key={agent.machine_id}>
                              <td className="text-[13px] font-medium text-text-primary">
                                {agent.hostname}
                              </td>
                              <td
                                className="mf-metric text-[12px] text-text-secondary"
                                title={
                                  !agent.arch
                                    ? "Architecture not reported for this agent"
                                    : !agent.arch_reported
                                      ? "Architecture inferred from host metrics, not reported by the agent"
                                      : undefined
                                }
                              >
                                {agent.os ? `${agent.os}/${agent.arch || "unknown"}` : "—"}
                              </td>
                              <td className="mf-metric text-[12px] text-text-secondary">
                                {shortSHA(agent.running_sha)}
                              </td>
                              <td>
                                {agent.update_blocked_reason ? (
                                  <StatusCell tone="critical" label={status.label} Icon={AlertTriangle} />
                                ) : agent.update_pending ? (
                                  <StatusCell tone="warning" label={status.label} Icon={Clock} />
                                ) : (
                                  // current-hub contract: not pending and not
                                  // blocked already means running == offered;
                                  // older hubs lack this guarantee, so show
                                  // unknown rather than infer a match.
                                  <StatusCell
                                    tone={status.kind === "current" ? "ok" : "neutral"}
                                    label={status.label}
                                  />
                                )}
                              </td>
                              <td>
                                {agent.update_protocol < 1 ? (
                                  <StatusCell tone="neutral" label="Not reported" />
                                ) : agent.update_key_pinned ? (
                                  <StatusCell tone="ok" label="Pinned" Icon={KeyRound} />
                                ) : (
                                  <StatusCell tone="critical" label="Missing" Icon={AlertTriangle} />
                                )}
                                {agentProtocolNote(agent) && (
                                  <div className="mt-1 text-[10px] text-text-tertiary">
                                    {agentProtocolNote(agent)}
                                  </div>
                                )}
                              </td>
                              <td
                                className="max-w-[340px] whitespace-normal text-[12px] text-text-secondary"
                                title={agent.update_blocked_reason || undefined}
                              >
                                {agent.update_blocked_reason || "—"}
                              </td>
                              <td className="mf-metric text-[12px] text-text-secondary">
                                {timeSince(agent.reported_at)}
                              </td>
                            </tr>
                          );
                        })
                    )}
                  </tbody>
                </table>
              </div>
            </section>
          </>
        )}
      </div>
    </>
  );
}
