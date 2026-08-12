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
import { useVersions } from "@/contexts/VersionsContext";
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

export default function VersionsPage() {
  const { data, loading, error, refresh, pauseRollout, resumeRollout } = useVersions();
  const { hasScope } = useAuth();
  const canManageRollout = hasScope("fleet.admin");

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
            Agents check the hub&apos;s announced SHA on connect; mismatches trigger silent updates with automatic rollback on failure.
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

            {/* Hub binary status card */}
            <div className="bg-blox-card border border-blox-border rounded-xl p-5">
              <div className="flex items-center justify-between gap-4 flex-wrap">
                <div>
                  <div className="flex items-center gap-1.5 text-[10px] uppercase tracking-[0.1em] text-blox-muted/70 font-medium mb-2">
                    <Server className="w-3 h-3" />
                    Hub binary
                  </div>
                  <div className="flex items-baseline gap-3">
                    <code className="text-base font-mono font-semibold text-blox-text">
                      {shortSHA(data.hub_sha)}
                    </code>
                    <span className="text-[11px] text-blox-muted">
                      built {timeSince(data.hub_mtime)}
                    </span>
                  </div>
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
                        colSpan={6}
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
                          <TableCell className="text-xs text-blox-muted font-mono tabular-nums">
                            {shortSHA(agent.running_sha)}
                          </TableCell>
                          <TableCell>
                            {agent.update_pending && agent.update_blocked_reason ? (
                              <Badge
                                variant="outline"
                                className="border-red-500/30 bg-red-500/10 text-red-400 text-[10px]"
                              >
                                <AlertTriangle className="w-2.5 h-2.5 mr-1" />
                                Withheld
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
