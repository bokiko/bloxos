/**
 * Shared formatting and status utility functions for the dashboard.
 */

export function formatTimeAgo(input: string | Date | null): string {
  if (!input) return 'Never';
  const timestamp =
    typeof input === 'string'
      ? new Date(input).getTime()
      : input.getTime();
  const diff = Date.now() - timestamp;
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return `${secs}s ago`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

export function formatLastSeen(input: string | Date | null): string {
  if (!input) return 'Never';
  return formatTimeAgo(input);
}

export function getStatusColor(status: string): string {
  switch (status) {
    case 'ONLINE': return 'bg-green-500';
    case 'WARNING': return 'bg-yellow-500';
    case 'ERROR': return 'bg-red-500';
    case 'REBOOTING': return 'bg-blue-500';
    default: return 'bg-slate-500';
  }
}

export function getStatusBadge(status: string): string {
  switch (status) {
    case 'ONLINE': return 'bg-green-500/10 text-green-400 ring-1 ring-green-500/30';
    case 'WARNING': return 'bg-yellow-500/10 text-yellow-400 ring-1 ring-yellow-500/30';
    case 'ERROR': return 'bg-red-500/10 text-red-400 ring-1 ring-red-500/30';
    case 'REBOOTING': return 'bg-blue-500/10 text-blue-400 ring-1 ring-blue-500/30';
    default: return 'bg-slate-500/10 text-slate-400 ring-1 ring-slate-500/30';
  }
}

export function getSeverityColor(severity: string): string {
  switch (severity) {
    case 'CRITICAL': return 'bg-red-500/20 text-red-400 border-red-500/30';
    case 'ERROR': return 'bg-red-500/20 text-red-400 border-red-500/30';
    case 'WARNING': return 'bg-yellow-500/20 text-yellow-400 border-yellow-500/30';
    default: return 'bg-blue-500/20 text-blue-400 border-blue-500/30';
  }
}
