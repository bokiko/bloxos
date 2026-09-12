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
  Package,
  ShieldCheck,
} from "lucide-react";
import { AgentBinaryInfo, useVersions } from "@/contexts/VersionsContext";
// The rollout render lives in a pure, separately tested module: the hub sends
// the controller-wide sentinel as a STRING and this page used to cast every
// value to an object, so a hub with no controller displayed a green
// "Automatic · 0 updated · 0 validated · 0 pending".
import {
  rolloutEntries,
  rolloutBadge,
  rolloutCountsLabel,
  rolloutReasonLines,
  rolloutCandidateLabel,
  rolloutRecoveryAction,
  operatorPauseLabel,
} from "@/lib/rollout-status.mjs";

/**
 * The resolution POLICY in force — not a claim about any particular platform.
 *
 * "auto" means a managed bundle is available to the resolver. It does not mean
 * every platform uses it: an explicit per-architecture override still wins, so
 * an install can be on this policy and still serve an operator-pinned binary
 * for one architecture. Labelling that "Managed" and promising that upgrading
 * the hub upgrades the fleet would be plainly wrong on such a hub.
 *
 * Each binary's own `source`, shown per platform below, stays the
 * authoritative answer to where it actually came from.
 */
const DELIVERY_LABEL: Record<string, string> = {
  auto: "Managed bundle available",
  legacy: "System paths",
  external: "Operator-managed",
  unusable: "Broken",
};

const DELIVERY_NOTE: Record<string, string> = {
  auto: "Platforms resolving to the managed bundle follow this hub release; explicit overrides remain operator-managed. See each platform's source below.",
  legacy:
    "No bundle shipped with this hub, so agents resolve from fixed system paths. Those paths are not refreshed by a hub upgrade.",
  external:
    "BLOXOS_AGENT_DELIVERY=external: you manage agent binaries yourself. A hub upgrade will not change what is offered.",
};
import {
  agentProtocolNote,
  agentStatusLabel,
  binaryReleaseLabel,
  binaryStateLabel,
  buildBinaryCards,
} from "@/lib/versions-honesty.mjs";
import { useAuth } from "@/contexts/AuthContext";
import { StatusCell, StatusMark, type MonoformTone } from "@/components/MonoformStatus";
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

  // One normalisation, shared by the per-platform rows and the fleet controls,
  // so the two can never disagree about which platforms are halted.
  const rollout = rolloutEntries(data?.agent_rollout);
  const recovery = rolloutRecoveryAction(rollout, data?.rollout_paused ?? false);

  useEffect(() => {
    refresh();
  }, [refresh]);

  return (
    <>
      <div className="mf-intro">
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
            <dt className="mf-kicker">Delivery</dt>
            <dd>
              {data?.agent_delivery ? (
                <StatusMark
                  tone={
                    data.agent_delivery === "unusable"
                      ? "critical"
                      : data.agent_delivery === "auto"
                        ? "ok"
                        : "warning"
                  }
                  label={DELIVERY_LABEL[data.agent_delivery] ?? data.agent_delivery}
                  Icon={data.agent_delivery === "unusable" ? AlertTriangle : Package}
                  title={data.agent_delivery_error || DELIVERY_NOTE[data.agent_delivery] || undefined}
                />
              ) : (
                <span className="text-[13px] text-text-tertiary">—</span>
              )}
            </dd>
          </div>
          {/* The operator pause, named as itself.
              This was "Rollout: Active/Paused", which was already loose and
              became wrong once the automatic failure breaker was removed: the
              flag now means only that nobody has pressed pause. A halted
              platform, or a hub with no controller at all, still showed
              "Active". Health is a per-platform claim and is made below. */}
          <div className="flex items-baseline gap-2">
            <dt className="mf-kicker">Operator pause</dt>
            <dd>
              {data ? (
                <StatusMark
                  tone={data.rollout_paused ? "warning" : "ok"}
                  label={operatorPauseLabel(data.rollout_paused)}
                  Icon={data.rollout_paused ? Pause : Play}
                  detail={data.rollout_paused ? data.pause_reason || undefined : undefined}
                />
              ) : (
                <span className="text-[13px] text-text-tertiary">—</span>
              )}
            </dd>
          </div>
        </dl>

        {/* Staged rollout, per platform. Without this a held or halted rollout
            is visible only in the hub log, and the page shows update_pending on
            every agent indefinitely — a rollout that stopped looks exactly like
            one still in progress. */}
        {rollout.length > 0 && (
          <dl className="mt-4 flex flex-col gap-2 border-t border-border-subtle pt-3">
            {rollout.map((entry) => {
              const badge = rolloutBadge(entry);
              const counts = rolloutCountsLabel(entry);
              const reasons = rolloutReasonLines(entry);
              // Which build these counts are about. The hub only picks up a
              // new candidate when a reservation runs, so with the pause on it
              // can serve one build while the rollout still describes the
              // previous one — and "observed on this build" would quietly
              // attach the old counts to the new binary.
              const candidate = rolloutCandidateLabel(entry);
              // Narrowed here rather than asserted: the helper is plain JS, so
              // this is the boundary where an unexpected tone would otherwise
              // reach the renderer untyped. Anything that is not "ok" renders
              // as critical, which is the safe direction for a status mark.
              const tone: MonoformTone = badge.tone === "ok" ? "ok" : "critical";
              return (
                <div key={entry.key} className="flex items-baseline gap-2">
                  <dt className="mf-kicker">{entry.platform}</dt>
                  <dd className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                    <StatusMark
                      tone={tone}
                      label={badge.label}
                      Icon={tone === "ok" ? Play : AlertTriangle}
                      detail={badge.detail || undefined}
                      title={badge.detail || undefined}
                    />
                    {candidate && (
                      <span
                        className="text-[12px] text-text-tertiary font-mono"
                        title={`Tracking candidate ${candidate.full}`}
                      >
                        build {candidate.short}
                      </span>
                    )}
                    {counts && <span className="text-[12px] text-text-tertiary">{counts}</span>}
                    {/* Named machines, because a withheld or failed count with
                        no names cannot be acted on. */}
                    {reasons.map((line) => (
                      <span key={line} className="text-[12px] text-text-tertiary">
                        {line}
                      </span>
                    ))}
                  </dd>
                </div>
              );
            })}
          </dl>
        )}
        <div className="mf-intro-actions">
          <button type="button" onClick={refresh} disabled={loading} className={MF_BUTTON}>
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? "animate-spin" : ""}`} aria-hidden />
            {loading ? "Refreshing…" : "Refresh versions"}
          </button>
        </div>
        <p>
          Signed updates, verified against each agent&apos;s pinned key. Protocol-v2 agents also
          enforce a release floor against downgrades.
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

            {/* The operator pause and platform halts are DIFFERENT things.
                This card headlined "Active" whenever nobody had pressed pause,
                which since the automatic breaker's removal says nothing about
                whether anything is rolling out. Worse, the only recovery
                control appeared when the pause was on — so a halted platform
                with the pause off offered the operator a Pause button and
                nothing else. Per-platform health is stated above; this card is
                the fleet-wide controls and says which one it is. */}
            <section className="mf-panel">
              <div className={MF_PANEL_HEAD}>
                <h2 className={MF_PANEL_TITLE}>Fleet rollout</h2>
                <StatusCell
                  tone={data.rollout_paused ? "warning" : "ok"}
                  label={`Operator pause: ${operatorPauseLabel(data.rollout_paused)}`}
                  Icon={data.rollout_paused ? Pause : CheckCircle2}
                />
              </div>
              <div className="flex flex-wrap items-center justify-between gap-4 px-6 py-4">
                <div className="min-w-0">
                  <p className="text-[13px] text-text-secondary">
                    {data.rollout_paused
                      ? "Update announcements are held for every platform until an operator resumes."
                      : "No operator hold. Each platform's own state is shown above."}
                  </p>
                  {data.rollout_paused && data.pause_reason && (
                    <p className="mt-1.5 text-xs text-text-tertiary">Reason: {data.pause_reason}</p>
                  )}
                  {recovery.retryAvailable && (
                    <p className="mt-1.5 text-xs text-text-tertiary">{recovery.detail}</p>
                  )}
                </div>
                {canManageRollout && (
                  <div className="flex flex-wrap items-center gap-2">
                    {/* One endpoint serves both: it clears the pause AND gives
                        every halted platform a new attempt, in one
                        transaction. Hence a single button when the pause is on
                        and something is halted — two would imply the operator
                        could do one without the other. */}
                    {(data.rollout_paused || recovery.retryAvailable) && (
                      <button
                        type="button"
                        onClick={resumeRollout}
                        className="mf-action inline-flex items-center gap-2"
                      >
                        <Play className="w-3.5 h-3.5" aria-hidden />
                        {recovery.label}
                      </button>
                    )}
                    {!data.rollout_paused && (
                      <button type="button" onClick={pauseRollout} className={MF_BUTTON}>
                        <Pause className="w-3.5 h-3.5" aria-hidden />
                        Pause rollout
                      </button>
                    )}
                  </div>
                )}
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
