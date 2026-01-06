'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';

const getApiUrl = () => {
  if (typeof window === 'undefined') return 'http://localhost:3001';
  return `http://${window.location.hostname}:3001`;
};

interface Alert {
  id: string;
  rigId: string;
  rig: { id: string; name: string };
  type: string;
  severity: string;
  title: string;
  message: string;
  data: any;
  read: boolean;
  dismissed: boolean;
  triggeredAt: string;
  readAt: string | null;
}

const AlertIcon = () => (
  <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 17h5l-1.405-1.405A2.032 2.032 0 0118 14.158V11a6.002 6.002 0 00-4-5.659V5a2 2 0 10-4 0v.341C7.67 6.165 6 8.388 6 11v3.159c0 .538-.214 1.055-.595 1.436L4 17h5m6 0v1a3 3 0 11-6 0v-1m6 0H9" />
  </svg>
);

const CheckIcon = () => (
  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
  </svg>
);

const XIcon = () => (
  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
  </svg>
);

export default function AlertsPage() {
  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [loading, setLoading] = useState(true);
  const [filter, setFilter] = useState<'all' | 'unread'>('unread');

  useEffect(() => {
    fetchAlerts();
    // Refresh every 30 seconds
    const interval = setInterval(fetchAlerts, 30000);
    return () => clearInterval(interval);
  }, [filter]);

  async function fetchAlerts() {
    try {
      const params = new URLSearchParams();
      if (filter === 'unread') params.set('unreadOnly', 'true');
      params.set('limit', '100');

      const res = await fetch(`${getApiUrl()}/api/alerts?${params}`, {
        credentials: 'include',
      });
      if (!res.ok) return;
      const data = await res.json();
      if (Array.isArray(data)) {
        setAlerts(data);
      }
    } catch (err) {
      console.error('Failed to fetch alerts');
    } finally {
      setLoading(false);
    }
  }

  async function markAsRead(alertId: string) {
    try {
      await fetch(`${getApiUrl()}/api/alerts/read`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ alertIds: [alertId] }),
      });
      fetchAlerts();
    } catch (err) {
      console.error('Failed to mark alert as read');
    }
  }

  async function markAllAsRead() {
    try {
      await fetch(`${getApiUrl()}/api/alerts/read`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ all: true }),
      });
      fetchAlerts();
    } catch (err) {
      console.error('Failed to mark all as read');
    }
  }

  async function dismissAlert(alertId: string) {
    try {
      await fetch(`${getApiUrl()}/api/alerts/${alertId}/dismiss`, {
        method: 'POST',
        credentials: 'include',
      });
      fetchAlerts();
    } catch (err) {
      console.error('Failed to dismiss alert');
    }
  }

  async function dismissAll() {
    try {
      await fetch(`${getApiUrl()}/api/alerts/dismiss-all`, {
        method: 'POST',
        credentials: 'include',
      });
      fetchAlerts();
    } catch (err) {
      console.error('Failed to dismiss all');
    }
  }

  function getSeverityStyles(severity: string) {
    switch (severity) {
      case 'CRITICAL':
        return 'bg-red-500/20 text-red-400 border-red-500/30';
      case 'ERROR':
        return 'bg-red-500/10 text-red-400 border-red-500/20';
      case 'WARNING':
        return 'bg-yellow-500/10 text-yellow-400 border-yellow-500/20';
      case 'INFO':
        return 'bg-blue-500/10 text-blue-400 border-blue-500/20';
      default:
        return 'bg-slate-500/10 text-slate-400 border-slate-500/20';
    }
  }

  function getTypeIcon(type: string) {
    switch (type) {
      case 'GPU_TEMP_HIGH':
      case 'CPU_TEMP_HIGH':
        return (
          <svg className="w-5 h-5 text-red-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M17.657 18.657A8 8 0 016.343 7.343S7 9 9 10c0-2 .5-5 2.986-7C14 5 16.09 5.777 17.656 7.343A7.975 7.975 0 0120 13a7.975 7.975 0 01-2.343 5.657z" />
          </svg>
        );
      case 'RIG_OFFLINE':
        return (
          <svg className="w-5 h-5 text-gray-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M18.364 5.636a9 9 0 010 12.728m0 0l-2.829-2.829m2.829 2.829L21 21M15.536 8.464a5 5 0 010 7.072m0 0l-2.829-2.829m-4.243 2.829a4.978 4.978 0 01-1.414-2.83m-1.414 5.658a9 9 0 01-2.167-9.238m7.824 2.167a1 1 0 111.414 1.414m-1.414-1.414L3 3m8.293 8.293l1.414 1.414" />
          </svg>
        );
      case 'HASHRATE_DROP':
        return (
          <svg className="w-5 h-5 text-yellow-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 17h8m0 0V9m0 8l-8-8-4 4-6-6" />
          </svg>
        );
      default:
        return (
          <svg className="w-5 h-5 text-blue-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
        );
    }
  }

  function formatTimeAgo(dateString: string) {
    const date = new Date(dateString);
    const diff = Date.now() - date.getTime();
    const secs = Math.floor(diff / 1000);
    if (secs < 60) return 'Just now';
    const mins = Math.floor(secs / 60);
    if (mins < 60) return `${mins}m ago`;
    const hours = Math.floor(mins / 60);
    if (hours < 24) return `${hours}h ago`;
    const days = Math.floor(hours / 24);
    return `${days}d ago`;
  }

  const unreadCount = alerts.filter(a => !a.read).length;

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="inline-block w-8 h-8 border-2 border-slate-600 border-t-blox-400 rounded-full animate-spin"></div>
        <p className="text-slate-400 ml-3">Loading alerts...</p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-col md:flex-row md:items-center justify-between gap-4">
        <div className="flex items-center gap-4">
          <div className="w-12 h-12 rounded-xl bg-orange-500/20 flex items-center justify-center">
            <AlertIcon />
          </div>
          <div>
            <h1 className="text-2xl font-bold">Alerts</h1>
            <p className="text-slate-400">
              {unreadCount > 0 ? `${unreadCount} unread alerts` : 'No unread alerts'}
            </p>
          </div>
        </div>
        <div className="flex items-center gap-3">
          {/* Filter Toggle */}
          <div className="flex items-center bg-slate-800 rounded-lg p-1">
            <button
              onClick={() => setFilter('unread')}
              className={`px-4 py-1.5 rounded-md text-sm font-medium transition-colors ${
                filter === 'unread'
                  ? 'bg-blox-600 text-white'
                  : 'text-slate-400 hover:text-white'
              }`}
            >
              Unread
            </button>
            <button
              onClick={() => setFilter('all')}
              className={`px-4 py-1.5 rounded-md text-sm font-medium transition-colors ${
                filter === 'all'
                  ? 'bg-blox-600 text-white'
                  : 'text-slate-400 hover:text-white'
              }`}
            >
              All
            </button>
          </div>
          {/* Actions */}
          {alerts.length > 0 && (
            <>
              <button
                onClick={markAllAsRead}
                className="px-4 py-2 bg-slate-700 hover:bg-slate-600 rounded-lg text-sm font-medium transition-colors flex items-center gap-2"
              >
                <CheckIcon /> Mark All Read
              </button>
              <button
                onClick={dismissAll}
                className="px-4 py-2 bg-red-500/10 hover:bg-red-500/20 text-red-400 rounded-lg text-sm font-medium transition-colors flex items-center gap-2"
              >
                <XIcon /> Dismiss All
              </button>
            </>
          )}
        </div>
      </div>

      {/* Alerts List */}
      {alerts.length === 0 ? (
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-12 text-center">
          <div className="w-16 h-16 mx-auto mb-4 rounded-full bg-green-500/20 flex items-center justify-center">
            <svg className="w-8 h-8 text-green-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
            </svg>
          </div>
          <h2 className="text-xl font-semibold mb-2">All Clear!</h2>
          <p className="text-slate-400">
            {filter === 'unread' ? 'No unread alerts' : 'No alerts found'}
          </p>
        </div>
      ) : (
        <div className="space-y-3">
          {alerts.map((alert) => (
            <div
              key={alert.id}
              className={`bg-slate-800/50 backdrop-blur-sm rounded-xl border ${
                alert.read ? 'border-slate-700/50 opacity-75' : 'border-slate-600'
              } overflow-hidden transition-all hover:border-slate-600`}
            >
              <div className="p-4 flex items-start gap-4">
                {/* Icon */}
                <div className={`w-10 h-10 rounded-lg ${getSeverityStyles(alert.severity)} flex items-center justify-center shrink-0 border`}>
                  {getTypeIcon(alert.type)}
                </div>

                {/* Content */}
                <div className="flex-1 min-w-0">
                  <div className="flex items-start justify-between gap-4">
                    <div>
                      <h3 className={`font-semibold ${!alert.read ? 'text-white' : 'text-slate-300'}`}>
                        {alert.title}
                      </h3>
                      <p className="text-sm text-slate-400 mt-1">{alert.message}</p>
                      <div className="flex items-center gap-3 mt-2 text-xs text-slate-500">
                        <Link
                          href={`/rigs/${alert.rigId}`}
                          className="hover:text-blox-400 transition-colors"
                        >
                          {alert.rig.name}
                        </Link>
                        <span>•</span>
                        <span>{formatTimeAgo(alert.triggeredAt)}</span>
                        <span>•</span>
                        <span className={`px-2 py-0.5 rounded ${getSeverityStyles(alert.severity)}`}>
                          {alert.severity}
                        </span>
                      </div>
                    </div>

                    {/* Actions */}
                    <div className="flex items-center gap-2 shrink-0">
                      {!alert.read && (
                        <button
                          onClick={() => markAsRead(alert.id)}
                          className="p-2 hover:bg-slate-700 rounded-lg transition-colors"
                          title="Mark as read"
                        >
                          <CheckIcon />
                        </button>
                      )}
                      <button
                        onClick={() => dismissAlert(alert.id)}
                        className="p-2 hover:bg-red-500/20 text-red-400 rounded-lg transition-colors"
                        title="Dismiss"
                      >
                        <XIcon />
                      </button>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
