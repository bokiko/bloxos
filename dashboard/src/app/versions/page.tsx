"use client";

import { useEffect } from "react";
import Link from "next/link";
import { motion } from "framer-motion";
import {
  ArrowLeft,
  RefreshCw,
  Pause,
  Play,
  Server,
  AlertTriangle,
  CheckCircle2,
  Clock,
  KeyRound,
  ShieldCheck,
} from "lucide-react";
import { AgentBinaryInfo, useVersions } from "@/contexts/VersionsContext";
import { useAuth } from "@/contexts/AuthContext";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
} from "@/components/ui/table";
import { Badge } from "@/components/ui/badge";

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

function AgentBinaryCard({
  platform,
  testId,
  binary,
}: {
  platform: string;
  testId: string;
  binary: AgentBinaryInfo;
}) {
  const available = Boolean(binary.sha) && !binary.error;

  return (
    <div
      className={`rounded-xl border p-4 ${
        available
          ? "border-blox-border bg-blox-card"
          : "border-red-500/20 bg-red-500/5"
      }`}
      data-testid={`agent-binary-${testId}`}
    >
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-center gap-1.5 text-[10px] uppercase tracking-[0.1em] text-blox-muted/70 font-medium">
          <Server className="w-3 h-3" />
          {platform} agent binary
        </div>
        <Badge
          variant="outline"
          className={
            available
              ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-400 text-[10px]"
              : "border-red-500/30 bg-red-500/10 text-red-400 text-[10px]"
          }
        >
          {available ? "Trusted" : "Unavailable"}
        </Badge>
      </div>
      {available ? (
        <>
          <div className="flex items-baseline gap-3 mt-3">
            <code className="text-sm font-mono font-semibold text-blox-text">
              {shortSHA(binary.sha)}
            </code>
            <span className="text-[11px] text-blox-muted">
              checked {timeSince(binary.mtime)}
            </span>
          </div>
          <dl className="mt-3 grid gap-2 text-[11px]">
            <div>
              <dt className="text-blox-muted">Source</dt>
              <dd className="text-blox-text font-mono break-all">{binary.source}</dd>
            </div>
            <div>
              <dt className="text-blox-muted">Resolved path</dt>
              <dd className="text-blox-text font-mono break-all">{binary.path}</dd>
            </div>
          </dl>
        </>
      ) : (
        <p className="text-xs text-red-300 mt-3 break-words">
          {binary.error || "No trusted binary resolved for this platform."}
        </p>
      )}
    </div>
  );
}

export default function VersionsPage() {
  const { data, loading, error, refresh, pauseRollout, resumeRollout } = useVersions();
  const { hasScope } = useAuth();
  const canManageRollout = hasScope("fleet.admin");
  const binaries = data
    ? data.agent_binaries ?? {
        linux: {
          path: "",
          source: "legacy API",
          sha: data.hub_sha,
          mtime: data.hub_mtime,
          error: "Resolver details require an updated hub.",
        },
        windows: {
          path: "",
          source: "legacy API",
          sha: data.hub_windows_sha,
          mtime: data.hub_windows_mtime,
          error: "Resolver details require an updated hub.",
        },
      }
    : null;

  // Per-architecture cards from a per-arch hub (clear CPU labels); legacy
  // two-card fallback for older hubs, whose "Linux" binary is whatever the
  // hub serves without an arch key — its CPU is not reported, so the card
  // stays unlabeled. A platform the hub has no binary for still renders a
  // card — its error names the missing build.
  const binaryCards: { key: string; label: string; binary: AgentBinaryInfo }[] = [];
  if (data) {
    if (data.agent_binaries_by_arch) {
      const byArch = data.agent_binaries_by_arch;
      const linuxArches: [string, string][] = [
        ["amd64", "Linux · x86-64"],
        ["arm64", "Linux · ARM64"],
      ];
      for (const [arch, label] of linuxArches) {
        const binary = byArch.linux?.[arch];
        if (binary) binaryCards.push({ key: `linux-${arch}`, label, binary });
      }
      const windows = byArch.windows?.amd64;
      if (windows) binaryCards.push({ key: "windows-amd64", label: "Windows · x86-64", binary: windows });
    } else if (binaries) {
      binaryCards.push(
        { key: "linux", label: "Linux", binary: binaries.linux },
        { key: "windows", label: "Windows", binary: binaries.windows }
      );
    }
  }

  useEffect(() => {
    refresh();
  }, [refresh]);

  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.25, ease: "easeOut" }}
      className="min-h-screen bg-blox-bg"
    >
      {/* Top nav */}
      <header className="sticky top-0 z-50 bg-blox-bg/80 backdrop-blur-xl border-b border-blox-border/50">
        <div className="max-w-[1200px] mx-auto px-4 sm:px-6 h-12 flex items-center justify-between">
          <Link
            href="/"
            className="flex items-center gap-1.5 text-blox-muted hover:text-blox-text transition-colors text-xs"
          >
            <ArrowLeft className="w-3.5 h-3.5" />
            <span>Fleet</span>
          </Link>
          <Button
            variant="ghost"
            size="sm"
            onClick={refresh}
            disabled={loading}
            className="text-xs text-blox-muted hover:text-blox-text gap-1.5 h-8"
          >
            <RefreshCw className={`w-3.5 h-3.5 ${loading ? "animate-spin" : ""}`} />
            Refresh
          </Button>
        </div>
      </header>

      {/* Hero */}
      <section className="border-b border-blox-border/50 bg-blox-bg/40">
        <div className="max-w-[1200px] mx-auto px-4 sm:px-6 py-6">
          <div className="flex items-center gap-3">
            <h1 className="text-xl sm:text-2xl font-semibold text-blox-text tracking-tight">
              Agent Versions
            </h1>
            {data && (
              <Badge
                variant="outline"
                className="border-blox-border text-blox-muted text-[10px] tabular-nums"
              >
                {data.agents.length} agent
                {data.agents.length === 1 ? "" : "s"}
              </Badge>
            )}
          </div>
          <p className="text-[12px] text-blox-muted mt-1.5">
            Auto-update status and version visibility across the fleet.
            Protocol-v1 agents verify signed updates against their pinned key.
            Windows revalidates the staged binary&apos;s SHA and signature on service restart, but still requires manual rollback.
          </p>
        </div>
      </section>

      <main className="max-w-[1200px] mx-auto px-4 sm:px-6 py-6 space-y-6">
        {error && (
          <div className="flex items-start gap-3 rounded-xl border border-red-500/20 bg-red-500/5 px-4 py-3">
            <AlertTriangle className="w-4 h-4 text-red-400 mt-0.5 shrink-0" />
            <div className="flex-1">
              <p className="text-sm text-blox-text font-medium">Versions request failed</p>
              <p className="text-xs text-blox-muted mt-1">{error}</p>
            </div>
          </div>
        )}

        {data && (
          <>
            <div
              className={`flex items-start gap-3 rounded-xl border px-4 py-3 ${
                data.signing_enabled
                  ? "border-emerald-500/20 bg-emerald-500/5"
                  : "border-red-500/20 bg-red-500/5"
              }`}
              data-testid="signing-status-banner"
            >
              {data.signing_enabled ? (
                <ShieldCheck className="w-4 h-4 text-emerald-400 mt-0.5 shrink-0" />
              ) : (
                <AlertTriangle className="w-4 h-4 text-red-400 mt-0.5 shrink-0" />
              )}
              <div>
                <p className="text-sm text-blox-text font-medium">
                  Update signing {data.signing_enabled ? "enabled" : "disabled"}
                </p>
                <p className="text-xs text-blox-muted mt-1">
                  {data.signing_enabled
                    ? "The hub can authenticate agent update announcements."
                    : data.signing_disabled_reason || "The hub cannot produce update signatures."}
                </p>
              </div>
            </div>

            <section aria-labelledby="served-agent-binaries">
              <h2
                id="served-agent-binaries"
                className="text-sm font-medium text-blox-text mb-3"
              >
                Served agent binaries
              </h2>
              <div className={`grid gap-3 ${data.agent_binaries_by_arch ? "md:grid-cols-3" : "md:grid-cols-2"}`}>
                {binaryCards.map((card) => (
                  <AgentBinaryCard key={card.key} platform={card.label} testId={card.key} binary={card.binary} />
                ))}
              </div>
              {!data.agent_binaries_by_arch && (
                <p className="text-[11px] text-blox-muted mt-2">
                  Per-CPU details are unavailable from this hub version.
                </p>
              )}
            </section>

            {/* Hub binary status card */}
            <div className="bg-blox-card border border-blox-border rounded-xl p-5">
              <div className="flex items-center justify-between gap-4 flex-wrap">
                <div>
                  <div className="flex items-center gap-1.5 text-[10px] uppercase tracking-[0.1em] text-blox-muted/70 font-medium mb-2">
                    <Server className="w-3 h-3" />
                    Fleet rollout
                  </div>
                  <p className="text-xs text-blox-muted">
                    Control update announcements for every platform.
                  </p>
                </div>

                {/* Rollout pause/resume control */}
                <div className="flex items-center gap-2">
                  {data.rollout_paused ? (
                    <>
                      <Badge
                        variant="outline"
                        className="border-amber-500/30 bg-amber-500/10 text-amber-400 text-[10px]"
                      >
                        <Pause className="w-2.5 h-2.5 mr-1" />
                        Rollout paused
                      </Badge>
                      {canManageRollout && (
                        <Button
                          size="sm"
                          onClick={resumeRollout}
                          className="bg-blox-blue text-white hover:bg-blox-blue/90 text-xs gap-1.5"
                        >
                          <Play className="w-3 h-3" />
                          Resume rollout
                        </Button>
                      )}
                    </>
                  ) : (
                    <>
                      <Badge
                        variant="outline"
                        className="border-emerald-500/30 bg-emerald-500/10 text-emerald-400 text-[10px]"
                      >
                        <CheckCircle2 className="w-2.5 h-2.5 mr-1" />
                        Rollout active
                      </Badge>
                      {canManageRollout && (
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={pauseRollout}
                          className="text-xs border-blox-border text-blox-muted hover:text-amber-400 gap-1.5"
                        >
                          <Pause className="w-3 h-3" />
                          Pause rollout
                        </Button>
                      )}
                    </>
                  )}
                </div>
              </div>

              {data.rollout_paused && data.pause_reason && (
                <div className="mt-3 pt-3 border-t border-blox-border/30 text-[11px] text-blox-muted">
                  Reason: {data.pause_reason}
                </div>
              )}
            </div>

            {/* Agents table */}
            <div className="bg-blox-card border border-blox-border rounded-xl overflow-x-auto">
              <Table>
                <TableHeader>
                  <TableRow className="border-b-blox-border hover:bg-transparent">
                    <TableHead className="text-blox-muted text-[11px] uppercase tracking-[0.06em] font-medium">
                      Hostname
                    </TableHead>
                    <TableHead className="text-blox-muted text-[11px] uppercase tracking-[0.06em] font-medium">
                      Platform
                    </TableHead>
                    <TableHead className="text-blox-muted text-[11px] uppercase tracking-[0.06em] font-medium">
                      Running SHA
                    </TableHead>
                    <TableHead className="text-blox-muted text-[11px] uppercase tracking-[0.06em] font-medium">
                      Status
                    </TableHead>
                    <TableHead className="text-blox-muted text-[11px] uppercase tracking-[0.06em] font-medium">
                      Key pinned
                    </TableHead>
                    <TableHead className="text-blox-muted text-[11px] uppercase tracking-[0.06em] font-medium">
                      Blocked reason
                    </TableHead>
                    <TableHead className="text-blox-muted text-[11px] uppercase tracking-[0.06em] font-medium">
                      Last connect
                    </TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {data.agents.length === 0 ? (
                    <TableRow>
                      <TableCell
                        colSpan={7}
                        className="text-center py-8 text-blox-muted text-xs"
                      >
                        No agents have reported their version yet
                      </TableCell>
                    </TableRow>
                  ) : (
                    [...data.agents]
                      .sort((a, b) => a.hostname.localeCompare(b.hostname))
                      .map((agent, i) => (
                        <TableRow
                          key={agent.machine_id}
                          className={`border-b-blox-border/30 hover:bg-blox-border/10 transition-colors ${
                            i % 2 === 1 ? "bg-blox-bg/30" : ""
                          }`}
                        >
                          <TableCell className="text-xs font-medium text-blox-text">
                            {agent.hostname}
                          </TableCell>
                          <TableCell
                            className="text-xs text-blox-muted font-mono"
                            title={
                              !agent.arch
                                ? "Architecture not reported for this agent"
                                : !agent.arch_reported
                                  ? "Architecture inferred from host metrics, not reported by the agent"
                                  : undefined
                            }
                          >
                            {agent.os
                              ? `${agent.os}/${agent.arch || "unknown"}`
                              : "—"}
                          </TableCell>
                          <TableCell className="text-xs text-blox-muted font-mono tabular-nums">
                            {shortSHA(agent.running_sha)}
                          </TableCell>
                          <TableCell>
                            {agent.update_blocked_reason ? (
                              <Badge
                                variant="outline"
                                className="border-red-500/30 bg-red-500/10 text-red-400 text-[10px]"
                              >
                                <AlertTriangle className="w-2.5 h-2.5 mr-1" />
                                {agent.update_pending ? "Withheld" : "Unavailable"}
                              </Badge>
                            ) : agent.update_pending ? (
                              <Badge
                                variant="outline"
                                className="border-amber-500/30 bg-amber-500/10 text-amber-400 text-[10px]"
                              >
                                <Clock className="w-2.5 h-2.5 mr-1" />
                                Update pending
                              </Badge>
                            ) : (
                              <Badge
                                variant="outline"
                                className="border-emerald-500/30 bg-emerald-500/10 text-emerald-400 text-[10px]"
                              >
                                <CheckCircle2 className="w-2.5 h-2.5 mr-1" />
                                Up to date
                              </Badge>
                            )}
                          </TableCell>
                          <TableCell>
                            {agent.update_protocol < 1 ? (
                              <Badge
                                variant="outline"
                                className="border-blox-border text-blox-muted text-[10px]"
                              >
                                Not reported
                              </Badge>
                            ) : agent.update_key_pinned ? (
                              <Badge
                                variant="outline"
                                className="border-emerald-500/30 bg-emerald-500/10 text-emerald-400 text-[10px]"
                              >
                                <KeyRound className="w-2.5 h-2.5 mr-1" />
                                Pinned
                              </Badge>
                            ) : (
                              <Badge
                                variant="outline"
                                className="border-red-500/30 bg-red-500/10 text-red-400 text-[10px]"
                              >
                                <AlertTriangle className="w-2.5 h-2.5 mr-1" />
                                Missing
                              </Badge>
                            )}
                          </TableCell>
                          <TableCell
                            className="max-w-[360px] whitespace-normal text-xs text-blox-muted"
                            title={agent.update_blocked_reason || undefined}
                          >
                            {agent.update_blocked_reason || "—"}
                          </TableCell>
                          <TableCell className="text-xs text-blox-muted font-mono tabular-nums">
                            {timeSince(agent.reported_at)}
                          </TableCell>
                        </TableRow>
                      ))
                  )}
                </TableBody>
              </Table>
            </div>
          </>
        )}
      </main>
    </motion.div>
  );
}
