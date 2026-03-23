'use client';

import { useEffect, useState, useCallback, useRef } from 'react';
import { getApiUrl } from '../../lib/api';
import { formatTimeAgo } from '../../lib/format';

type EventSeverity = 'INFO' | 'WARNING' | 'ERROR' | 'CRITICAL';

type EventType =
  | 'RIG_ONLINE'
  | 'RIG_OFFLINE'
  | 'MINER_STARTED'
  | 'MINER_STOPPED'
  | 'MINER_ERROR'
  | 'GPU_TEMP_HIGH'
  | 'GPU_ERROR'
  | 'HASHRATE_DROP'
  | 'COMMAND_EXECUTED'
  | 'CONFIG_CHANGED';

interface RigRef {
  id: string;
  name: string;
  hostname: string;
}

interface RigEvent {
  id: string;
  type: EventType;
  severity: EventSeverity;
  message: string;
  timestamp: string;
  rigId: string;
  rig: RigRef;
}

interface EventsResponse {
  events: RigEvent[];
  total: number;
  limit: number;
  offset: number;
}

const PAGE_SIZE = 50;

const EVENT_LABELS: Record<EventType, string> = {
  RIG_ONLINE: 'Rig Online',
  RIG_OFFLINE: 'Rig Offline',
  MINER_STARTED: 'Miner Started',
  MINER_STOPPED: 'Miner Stopped',
  MINER_ERROR: 'Miner Error',
  GPU_TEMP_HIGH: 'High GPU Temp',
  GPU_ERROR: 'GPU Error',
  HASHRATE_DROP: 'Hashrate Drop',
  COMMAND_EXECUTED: 'Command Executed',
  CONFIG_CHANGED: 'Config Changed',
};

const EVENT_TYPES: EventType[] = [
  'RIG_ONLINE',
  'RIG_OFFLINE',
  'MINER_STARTED',
  'MINER_STOPPED',
  'MINER_ERROR',
  'GPU_TEMP_HIGH',
  'GPU_ERROR',
  'HASHRATE_DROP',
  'COMMAND_EXECUTED',
  'CONFIG_CHANGED',
];

const SEVERITY_LEVELS: EventSeverity[] = ['INFO', 'WARNING', 'ERROR', 'CRITICAL'];

function severityBadge(severity: EventSeverity): string {
  switch (severity) {
    case 'INFO':
      return 'bg-blue-500/15 text-blue-300 border border-blue-500/30';
    case 'WARNING':
      return 'bg-yellow-500/15 text-yellow-300 border border-yellow-500/30';
    case 'ERROR':
      return 'bg-red-500/15 text-red-300 border border-red-500/30';
    case 'CRITICAL':
      return 'bg-rose-600/20 text-rose-300 border border-rose-500/40';
    default:
      return 'bg-slate-600/30 text-slate-400 border border-slate-600/40';
  }
}

function severityDot(severity: EventSeverity): string {
  switch (severity) {
    case 'INFO':
      return 'bg-blue-400';
    case 'WARNING':
      return 'bg-yellow-400';
    case 'ERROR':
      return 'bg-red-400';
    case 'CRITICAL':
      return 'bg-rose-400 animate-pulse';
    default:
      return 'bg-slate-500';
  }
}

function eventTypeColor(type: EventType): string {
  switch (type) {
    case 'RIG_ONLINE':
      return 'text-green-400';
    case 'RIG_OFFLINE':
      return 'text-red-400';
    case 'MINER_ERROR':
    case 'GPU_ERROR':
      return 'text-red-400';
    case 'GPU_TEMP_HIGH':
    case 'HASHRATE_DROP':
      return 'text-yellow-400';
    case 'MINER_STARTED':
    case 'MINER_STOPPED':
      return 'text-blue-400';
    case 'COMMAND_EXECUTED':
    case 'CONFIG_CHANGED':
      return 'text-purple-400';
    default:
      return 'text-slate-400';
  }
}

export default function EventsPage() {
  const [events, setEvents] = useState<RigEvent[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(0);

  const [rigs, setRigs] = useState<RigRef[]>([]);
  const [rigFilter, setRigFilter] = useState('');
  const [typeFilter, setTypeFilter] = useState('');
  const [severityFilter, setSeverityFilter] = useState('');
  const [autoRefresh, setAutoRefresh] = useState(true);

  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const fetchEvents = useCallback(async (currentPage: number) => {
    const params = new URLSearchParams();
    params.set('limit', String(PAGE_SIZE));
    params.set('offset', String(currentPage * PAGE_SIZE));
    if (rigFilter) params.set('rigId', rigFilter);
    if (typeFilter) params.set('type', typeFilter);
    if (severityFilter) params.set('severity', severityFilter);

    try {
      const res = await fetch(`${getApiUrl()}/api/events?${params}`, {
        credentials: 'include',
      });
      if (!res.ok) return;
      const data: EventsResponse = await res.json();
      setEvents(data.events);
      setTotal(data.total);
    } catch {
      // silently ignore transient failures
    } finally {
      setLoading(false);
    }
  }, [rigFilter, typeFilter, severityFilter]);

  const fetchRigs = useCallback(async () => {
    try {
      const res = await fetch(`${getApiUrl()}/api/events/rigs`, { credentials: 'include' });
      if (!res.ok) return;
      const data: RigRef[] = await res.json();
      setRigs(data);
    } catch {
      // ignore
    }
  }, []);

  // Reset to page 0 when filters change
  useEffect(() => {
    setPage(0);
    setLoading(true);
    fetchEvents(0);
  }, [fetchEvents]);

  useEffect(() => {
    fetchRigs();
  }, [fetchRigs]);

  // Auto-refresh every 30 s
  useEffect(() => {
    if (intervalRef.current) clearInterval(intervalRef.current);
    if (autoRefresh) {
      intervalRef.current = setInterval(() => fetchEvents(page), 30000);
    }
    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current);
    };
  }, [autoRefresh, fetchEvents, page]);

  const totalPages = Math.ceil(total / PAGE_SIZE);

  function changePage(next: number) {
    setPage(next);
    setLoading(true);
    fetchEvents(next);
  }

  return (
    <div>
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-4 mb-6">
        <div>
          <h1 className="text-2xl font-bold">Event Log</h1>
          <p className="text-slate-400 text-sm mt-1">
            Chronological feed of rig and miner events across your farm
          </p>
        </div>
        <button
          onClick={() => setAutoRefresh((v) => !v)}
          className={`flex items-center gap-2 px-3 py-1.5 rounded-lg text-sm transition-all ${
            autoRefresh
              ? 'bg-green-500/20 text-green-400'
              : 'bg-slate-700 text-slate-400'
          }`}
          aria-pressed={autoRefresh}
          aria-label={autoRefresh ? 'Auto-refresh on' : 'Auto-refresh off'}
        >
          <span
            className={`w-2 h-2 rounded-full ${
              autoRefresh ? 'bg-green-400 animate-pulse' : 'bg-slate-500'
            }`}
          />
          {autoRefresh ? 'Live' : 'Paused'}
        </button>
      </div>

      {/* Filters */}
      <div className="flex flex-wrap gap-3 mb-6">
        {/* Rig filter */}
        <select
          value={rigFilter}
          onChange={(e) => setRigFilter(e.target.value)}
          className="bg-slate-800/70 border border-slate-700/60 rounded-lg px-3 py-2 text-sm text-slate-200 focus:outline-none focus:ring-2 focus:ring-blox-500/60"
          aria-label="Filter by rig"
        >
          <option value="">All Rigs</option>
          {rigs.map((r) => (
            <option key={r.id} value={r.id}>
              {r.name}
            </option>
          ))}
        </select>

        {/* Type filter */}
        <select
          value={typeFilter}
          onChange={(e) => setTypeFilter(e.target.value)}
          className="bg-slate-800/70 border border-slate-700/60 rounded-lg px-3 py-2 text-sm text-slate-200 focus:outline-none focus:ring-2 focus:ring-blox-500/60"
          aria-label="Filter by event type"
        >
          <option value="">All Types</option>
          {EVENT_TYPES.map((t) => (
            <option key={t} value={t}>
              {EVENT_LABELS[t]}
            </option>
          ))}
        </select>

        {/* Severity filter */}
        <select
          value={severityFilter}
          onChange={(e) => setSeverityFilter(e.target.value)}
          className="bg-slate-800/70 border border-slate-700/60 rounded-lg px-3 py-2 text-sm text-slate-200 focus:outline-none focus:ring-2 focus:ring-blox-500/60"
          aria-label="Filter by severity"
        >
          <option value="">All Severities</option>
          {SEVERITY_LEVELS.map((s) => (
            <option key={s} value={s}>
              {s}
            </option>
          ))}
        </select>

        {(rigFilter || typeFilter || severityFilter) && (
          <button
            onClick={() => {
              setRigFilter('');
              setTypeFilter('');
              setSeverityFilter('');
            }}
            className="px-3 py-2 text-sm text-slate-400 hover:text-slate-200 bg-slate-800/50 border border-slate-700/50 rounded-lg transition-colors"
            aria-label="Clear all filters"
          >
            Clear Filters
          </button>
        )}

        <span className="ml-auto text-sm text-slate-500 self-center">
          {total.toLocaleString()} events
        </span>
      </div>

      {/* Event table */}
      <div
        className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 overflow-hidden"
        role="region"
        aria-label="Event log"
        aria-live="polite"
        aria-busy={loading}
      >
        {loading ? (
          <div className="p-12 text-center">
            <div className="inline-block w-8 h-8 border-2 border-slate-600 border-t-blox-400 rounded-full animate-spin" />
            <p className="text-slate-400 mt-3">Loading events…</p>
          </div>
        ) : events.length === 0 ? (
          <div className="p-12 text-center">
            <p className="text-slate-400">
              {rigFilter || typeFilter || severityFilter
                ? 'No events match your filters.'
                : 'No events recorded yet. Events will appear as rigs report activity.'}
            </p>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full" role="table">
              <thead>
                <tr className="text-left text-xs font-medium text-slate-400 uppercase tracking-wider border-b border-slate-700/50">
                  <th className="px-5 py-3 w-36">Time</th>
                  <th className="px-5 py-3 w-28">Severity</th>
                  <th className="px-5 py-3 w-44">Type</th>
                  <th className="px-5 py-3 w-40">Rig</th>
                  <th className="px-5 py-3">Message</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-700/30" role="rowgroup">
                {events.map((ev) => (
                  <tr
                    key={ev.id}
                    className="hover:bg-slate-700/20 transition-colors"
                    role="row"
                  >
                    <td className="px-5 py-3 text-xs text-slate-500 whitespace-nowrap">
                      <time dateTime={ev.timestamp} title={new Date(ev.timestamp).toLocaleString()}>
                        {formatTimeAgo(ev.timestamp)}
                      </time>
                    </td>
                    <td className="px-5 py-3">
                      <span
                        className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-full text-xs font-medium ${severityBadge(
                          ev.severity
                        )}`}
                      >
                        <span className={`w-1.5 h-1.5 rounded-full ${severityDot(ev.severity)}`} />
                        {ev.severity}
                      </span>
                    </td>
                    <td className={`px-5 py-3 text-sm font-medium ${eventTypeColor(ev.type)}`}>
                      {EVENT_LABELS[ev.type] ?? ev.type}
                    </td>
                    <td className="px-5 py-3">
                      <a
                        href={`/rigs/${ev.rigId}`}
                        className="text-sm text-slate-300 hover:text-white transition-colors font-medium"
                      >
                        {ev.rig.name}
                      </a>
                      <p className="text-xs text-slate-600">{ev.rig.hostname}</p>
                    </td>
                    <td className="px-5 py-3 text-sm text-slate-300 max-w-sm truncate" title={ev.message}>
                      {ev.message}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        {/* Pagination */}
        {totalPages > 1 && (
          <div
            className="flex items-center justify-between px-5 py-3 border-t border-slate-700/50"
            role="navigation"
            aria-label="Pagination"
          >
            <span className="text-sm text-slate-500">
              Page {page + 1} of {totalPages}
            </span>
            <div className="flex gap-2">
              <button
                onClick={() => changePage(page - 1)}
                disabled={page === 0}
                className="px-3 py-1.5 rounded-lg text-sm bg-slate-700 hover:bg-slate-600 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
                aria-label="Previous page"
              >
                Previous
              </button>
              <button
                onClick={() => changePage(page + 1)}
                disabled={page >= totalPages - 1}
                className="px-3 py-1.5 rounded-lg text-sm bg-slate-700 hover:bg-slate-600 disabled:opacity-40 disabled:cursor-not-allowed transition-colors"
                aria-label="Next page"
              >
                Next
              </button>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
