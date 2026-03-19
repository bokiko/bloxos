/**
 * Shared formatting and status utility functions for the dashboard.
 */

export function formatTimeAgo(date: string | Date | null): string {
  if (!date) return '';
  const timestamp = typeof date === 'string' ? new Date(date).getTime() : date.getTime();
  const diff = Date.now() - timestamp;
  const secs = Math.floor(diff / 1000);
  if (secs < 60) return 'Just now';
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  const days = Math.floor(hours / 24);
  return `${days}d ago`;
}

export function formatLastSeen(lastSeen: string | null): string {
  if (!lastSeen) return 'Never';
  const diff = Date.now() - new Date(lastSeen).getTime();
  const mins = Math.floor(diff / 60000);
  if (mins < 1) return 'Just now';
  if (mins < 60) return `${mins}m ago`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h ago`;
  return `${Math.floor(hours / 24)}d ago`;
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

export function getSeverityColor(severity: string): string {
  switch (severity) {
    case 'CRITICAL': return 'bg-red-500/20 text-red-400 border-red-500/30';
    case 'ERROR': return 'bg-red-500/20 text-red-400 border-red-500/30';
    case 'WARNING': return 'bg-yellow-500/20 text-yellow-400 border-yellow-500/30';
    default: return 'bg-blue-500/20 text-blue-400 border-blue-500/30';
  }
}
