'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';

const getApiUrl = () => {
  if (typeof window === 'undefined') return 'http://localhost:3001';
  return `http://${window.location.hostname}:3001`;
};

interface GPU {
  id: string;
  name: string;
  temperature: number | null;
  fanSpeed: number | null;
  powerDraw: number | null;
  hashrate: number | null;
}

interface CPU {
  id: string;
  powerDraw: number | null;
}

interface Rig {
  id: string;
  name: string;
  hostname: string;
  status: string;
  ipAddress: string | null;
  lastSeen: string | null;
  gpus: GPU[];
  cpu: CPU | null;
}

interface Stats {
  totalRigs: number;
  onlineRigs: number;
  offlineRigs: number;
  totalGpus: number;
  totalHashrate: number;
  totalPower: number;
  avgTemp: number;
  efficiency: number;
}

interface Alert {
  id: string;
  type: string;
  severity: string;
  title: string;
  message: string;
  rigId: string;
  rig: { name: string };
  triggeredAt: string;
}

// Stat Card Icons
const RigIcon = () => (
  <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9 3v2m6-2v2M9 19v2m6-2v2M5 9H3m2 6H3m18-6h-2m2 6h-2M7 19h10a2 2 0 002-2V7a2 2 0 00-2-2H7a2 2 0 00-2 2v10a2 2 0 002 2zM9 9h6v6H9V9z" />
  </svg>
);

const OnlineIcon = () => (
  <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
  </svg>
);

const GpuIcon = () => (
  <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9 17V7m0 10a2 2 0 01-2 2H5a2 2 0 01-2-2V7a2 2 0 012-2h2a2 2 0 012 2m0 10a2 2 0 002 2h2a2 2 0 002-2M9 7a2 2 0 012-2h2a2 2 0 012 2m0 10V7m0 10a2 2 0 002 2h2a2 2 0 002-2V7a2 2 0 00-2-2h-2a2 2 0 00-2 2" />
  </svg>
);

const HashrateIcon = () => (
  <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M13 10V3L4 14h7v7l9-11h-7z" />
  </svg>
);

const PowerIcon = () => (
  <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9.663 17h4.673M12 3v1m6.364 1.636l-.707.707M21 12h-1M4 12H3m3.343-5.657l-.707-.707m2.828 9.9a5 5 0 117.072 0l-.548.547A3.374 3.374 0 0014 18.469V19a2 2 0 11-4 0v-.531c0-.895-.356-1.754-.988-2.386l-.548-.547z" />
  </svg>
);

const TempIcon = () => (
  <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M12 9v3m0 0v3m0-3h3m-3 0H9m12 0a9 9 0 11-18 0 9 9 0 0118 0z" />
  </svg>
);

export default function DashboardPage() {
  const [rigs, setRigs] = useState<Rig[]>([]);
  const [stats, setStats] = useState<Stats>({
    totalRigs: 0,
    onlineRigs: 0,
    offlineRigs: 0,
    totalGpus: 0,
    totalHashrate: 0,
    totalPower: 0,
    avgTemp: 0,
    efficiency: 0,
  });
  const [recentAlerts, setRecentAlerts] = useState<Alert[]>([]);
  const [loading, setLoading] = useState(true);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const REFRESH_INTERVAL = 30000; // 30 seconds

  useEffect(() => {
    fetchRigs();
    fetchAlerts();

    // Auto-refresh interval
    let intervalId: NodeJS.Timeout | null = null;
    if (autoRefresh) {
      intervalId = setInterval(() => {
        fetchRigs();
        fetchAlerts();
      }, REFRESH_INTERVAL);
    }

    return () => {
      if (intervalId) clearInterval(intervalId);
    };
  }, [autoRefresh]);

  async function fetchRigs() {
    try {
      const res = await fetch(`${getApiUrl()}/api/rigs`, {
        credentials: 'include',
      });
      if (!res.ok) {
        console.error('Failed to fetch rigs:', res.status);
        return;
      }
      const data = await res.json();
      if (!Array.isArray(data)) {
        console.error('Unexpected response format:', data);
        return;
      }
      setRigs(data);

      // Calculate stats
      const totalRigs = data.length;
      const onlineRigs = data.filter((r: Rig) => r.status === 'ONLINE').length;
      const offlineRigs = totalRigs - onlineRigs;
      const totalGpus = data.reduce((acc: number, r: Rig) => acc + r.gpus.length, 0);
      const totalHashrate = data.reduce((acc: number, r: Rig) => {
        return acc + r.gpus.reduce((sum, gpu) => sum + (gpu.hashrate || 0), 0);
      }, 0);
      const totalPower = data.reduce((acc: number, r: Rig) => {
        const gpuPower = r.gpus.reduce((sum, gpu) => sum + (gpu.powerDraw || 0), 0);
        const cpuPower = r.cpu?.powerDraw || 0;
        return acc + gpuPower + cpuPower;
      }, 0);
      
      // Calculate average temperature
      const allTemps = data.flatMap((r: Rig) => r.gpus.map(g => g.temperature).filter((t: number | null) => t !== null));
      const avgTemp = allTemps.length > 0 ? allTemps.reduce((a: number, b: number) => a + b, 0) / allTemps.length : 0;
      
      // Calculate efficiency (kH/W)
      const efficiency = totalPower > 0 ? (totalHashrate * 1000) / totalPower : 0;

      setStats({ totalRigs, onlineRigs, offlineRigs, totalGpus, totalHashrate, totalPower, avgTemp, efficiency });
      setLastUpdated(new Date());
    } catch (error) {
      console.error('Failed to fetch rigs:', error);
    } finally {
      setLoading(false);
    }
  }

  async function fetchAlerts() {
    try {
      const res = await fetch(`${getApiUrl()}/api/alerts?limit=5`, {
        credentials: 'include',
      });
      if (!res.ok) {
        console.error('Failed to fetch alerts:', res.status);
        return;
      }
      const data = await res.json();
      if (Array.isArray(data)) {
        setRecentAlerts(data);
      }
    } catch (error) {
      console.error('Failed to fetch alerts:', error);
    }
  }

  function getStatusColor(status: string) {
    switch (status) {
      case 'ONLINE': return 'bg-green-500';
      case 'WARNING': return 'bg-yellow-500';
      case 'ERROR': return 'bg-red-500';
      default: return 'bg-slate-500';
    }
  }

  function formatLastSeen(lastSeen: string | null) {
    if (!lastSeen) return 'Never';
    const diff = Date.now() - new Date(lastSeen).getTime();
    const mins = Math.floor(diff / 60000);
    if (mins < 1) return 'Just now';
    if (mins < 60) return `${mins}m ago`;
    const hours = Math.floor(mins / 60);
    if (hours < 24) return `${hours}h ago`;
    return `${Math.floor(hours / 24)}d ago`;
  }

  function getSeverityColor(severity: string) {
    switch (severity) {
      case 'CRITICAL': return 'bg-red-500/20 text-red-400 border-red-500/30';
      case 'ERROR': return 'bg-red-500/20 text-red-400 border-red-500/30';
      case 'WARNING': return 'bg-yellow-500/20 text-yellow-400 border-yellow-500/30';
      default: return 'bg-blue-500/20 text-blue-400 border-blue-500/30';
    }
  }

  function formatTimeAgo(dateString: string) {
    const diff = Date.now() - new Date(dateString).getTime();
    const mins = Math.floor(diff / 60000);
    if (mins < 1) return 'Just now';
    if (mins < 60) return `${mins}m ago`;
    const hours = Math.floor(mins / 60);
    if (hours < 24) return `${hours}h ago`;
    return `${Math.floor(hours / 24)}d ago`;
  }

  const statCards = [
    { label: 'Total Rigs', value: stats.totalRigs, icon: RigIcon, color: 'from-slate-500 to-slate-600', textColor: 'text-slate-400' },
    { label: 'Online', value: stats.onlineRigs, icon: OnlineIcon, color: 'from-green-500 to-green-600', textColor: 'text-green-400' },
    { label: 'Total GPUs', value: stats.totalGpus, icon: GpuIcon, color: 'from-blue-500 to-blue-600', textColor: 'text-blue-400' },
    { label: 'Hashrate', value: `${stats.totalHashrate.toFixed(1)} MH/s`, icon: HashrateIcon, color: 'from-purple-500 to-purple-600', textColor: 'text-purple-400' },
    { label: 'Power', value: `${stats.totalPower.toFixed(0)} W`, icon: PowerIcon, color: 'from-yellow-500 to-yellow-600', textColor: 'text-yellow-400' },
    { label: 'Efficiency', value: `${stats.efficiency.toFixed(1)} kH/W`, icon: TempIcon, color: 'from-cyan-500 to-cyan-600', textColor: 'text-cyan-400' },
  ];

  return (
    <div>
      {/* Header */}
      <div className="flex justify-between items-center mb-8">
        <div>
          <h1 className="text-2xl font-bold">Dashboard</h1>
          <p className="text-slate-400 text-sm mt-1">Monitor your mining operations</p>
        </div>
        <div className="flex items-center gap-4">
          {/* Auto-refresh indicator */}
          <div className="flex items-center gap-2 text-sm">
            <button
              onClick={() => setAutoRefresh(!autoRefresh)}
              className={`flex items-center gap-2 px-3 py-1.5 rounded-lg transition-all ${
                autoRefresh
                  ? 'bg-green-500/20 text-green-400'
                  : 'bg-slate-700 text-slate-400'
              }`}
            >
              <span className={`w-2 h-2 rounded-full ${autoRefresh ? 'bg-green-400 animate-pulse' : 'bg-slate-500'}`}></span>
              {autoRefresh ? 'Live' : 'Paused'}
            </button>
            {lastUpdated && (
              <span className="text-slate-500 text-xs">
                Updated {formatLastSeen(lastUpdated.toISOString())}
              </span>
            )}
          </div>
          <Link
            href="/rigs/add"
            className="px-4 py-2.5 bg-gradient-to-r from-blox-500 to-blox-600 hover:from-blox-600 hover:to-blox-700 rounded-lg text-white font-medium transition-all duration-200 shadow-lg shadow-blox-500/25"
          >
            + Add Rig
          </Link>
        </div>
      </div>

      {/* Stats Cards */}
      <div className="grid grid-cols-2 md:grid-cols-3 lg:grid-cols-6 gap-4 mb-8">
        {statCards.map((stat) => {
          const Icon = stat.icon;
          return (
            <div
              key={stat.label}
              className="bg-slate-800/50 backdrop-blur-sm rounded-xl p-4 border border-slate-700/50 hover:border-slate-600/50 transition-all duration-200"
            >
              <div className="flex items-center justify-between mb-3">
                <div className={`w-10 h-10 rounded-lg bg-gradient-to-br ${stat.color} flex items-center justify-center opacity-80`}>
                  <Icon />
                </div>
              </div>
              <p className="text-slate-400 text-xs font-medium uppercase tracking-wider">{stat.label}</p>
              <p className={`text-2xl font-bold mt-1 ${stat.textColor}`}>{stat.value}</p>
            </div>
          );
        })}
      </div>

      {/* Recent Alerts */}
      {recentAlerts.length > 0 && (
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-5 mb-8">
          <div className="flex items-center justify-between mb-4">
            <h2 className="text-lg font-semibold">Recent Alerts</h2>
            <Link href="/alerts" className="text-sm text-blox-400 hover:text-blox-300">
              View All →
            </Link>
          </div>
          <div className="space-y-2">
            {recentAlerts.slice(0, 3).map((alert) => (
              <div
                key={alert.id}
                className={`flex items-center justify-between px-4 py-3 rounded-lg border ${getSeverityColor(alert.severity)}`}
              >
                <div className="flex items-center gap-3">
                  <div>
                    <p className="text-sm font-medium">{alert.title}</p>
                    <p className="text-xs opacity-70">{alert.rig.name} • {formatTimeAgo(alert.triggeredAt)}</p>
                  </div>
                </div>
                <Link
                  href={`/rigs/${alert.rigId}`}
                  className="text-xs px-3 py-1 bg-slate-700/50 hover:bg-slate-700 rounded-lg transition-colors"
                >
                  View Rig
                </Link>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Rigs Overview */}
      <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 overflow-hidden">
        <div className="px-5 py-4 border-b border-slate-700/50 flex items-center justify-between">
          <div>
            <h2 className="text-lg font-semibold">Rigs Overview</h2>
            <p className="text-slate-400 text-sm">{stats.totalRigs} total rigs</p>
          </div>
          <Link
            href="/rigs"
            className="text-sm text-blox-400 hover:text-blox-300 font-medium"
          >
            View All →
          </Link>
        </div>

        {loading ? (
          <div className="p-12 text-center">
            <div className="inline-block w-8 h-8 border-2 border-slate-600 border-t-blox-400 rounded-full animate-spin"></div>
            <p className="text-slate-400 mt-3">Loading rigs...</p>
          </div>
        ) : rigs.length === 0 ? (
          <div className="p-12 text-center">
            <div className="w-16 h-16 mx-auto mb-4 rounded-full bg-slate-700/50 flex items-center justify-center">
              <RigIcon />
            </div>
            <p className="text-slate-400 mb-4">No rigs found. Add your first rig to get started.</p>
            <Link
              href="/rigs/add"
              className="inline-block px-4 py-2 bg-blox-600 hover:bg-blox-700 rounded-lg text-white font-medium transition-colors"
            >
              Add Rig
            </Link>
          </div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead>
                <tr className="text-left text-xs font-medium text-slate-400 uppercase tracking-wider">
                  <th className="px-5 py-3">Status</th>
                  <th className="px-5 py-3">Rig Name</th>
                  <th className="px-5 py-3">IP Address</th>
                  <th className="px-5 py-3">GPUs</th>
                  <th className="px-5 py-3">Hashrate</th>
                  <th className="px-5 py-3">Power</th>
                  <th className="px-5 py-3">Temp</th>
                  <th className="px-5 py-3">Last Seen</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-700/50">
                {rigs.map((rig) => {
                  const hashrate = rig.gpus.reduce((sum, gpu) => sum + (gpu.hashrate || 0), 0);
                  const gpuPower = rig.gpus.reduce((sum, gpu) => sum + (gpu.powerDraw || 0), 0);
                  const cpuPower = rig.cpu?.powerDraw || 0;
                  const power = gpuPower + cpuPower;
                  const temps = rig.gpus.map(g => g.temperature).filter((t): t is number => t !== null);
                  const maxTemp = temps.length > 0 ? Math.max(...temps) : null;
                  
                  return (
                    <tr
                      key={rig.id}
                      className="hover:bg-slate-700/30 transition-colors cursor-pointer"
                      onClick={() => window.location.href = `/rigs/${rig.id}`}
                    >
                      <td className="px-5 py-4">
                        <div className="flex items-center gap-2">
                          <span className={`w-2.5 h-2.5 rounded-full ${getStatusColor(rig.status)}`}></span>
                          <span className={`text-xs font-medium ${
                            rig.status === 'ONLINE' ? 'text-green-400' :
                            rig.status === 'WARNING' ? 'text-yellow-400' :
                            rig.status === 'ERROR' ? 'text-red-400' : 'text-slate-400'
                          }`}>
                            {rig.status}
                          </span>
                        </div>
                      </td>
                      <td className="px-5 py-4">
                        <div>
                          <p className="font-medium">{rig.name}</p>
                          <p className="text-xs text-slate-500">{rig.hostname}</p>
                        </div>
                      </td>
                      <td className="px-5 py-4">
                        <span className="text-sm text-slate-300 font-mono">{rig.ipAddress || '-'}</span>
                      </td>
                      <td className="px-5 py-4">
                        <span className="text-sm">{rig.gpus.length}</span>
                      </td>
                      <td className="px-5 py-4">
                        <span className="text-sm font-medium text-purple-400">{hashrate.toFixed(1)} MH/s</span>
                      </td>
                      <td className="px-5 py-4">
                        <span className="text-sm">{power.toFixed(0)} W</span>
                      </td>
                      <td className="px-5 py-4">
                        <span className={`text-sm font-medium ${maxTemp && maxTemp > 80 ? 'text-red-400' : maxTemp && maxTemp > 70 ? 'text-yellow-400' : ''}`}>
                          {maxTemp !== null ? `${maxTemp}°C` : '-'}
                        </span>
                      </td>
                      <td className="px-5 py-4">
                        <span className="text-sm text-slate-400">{formatLastSeen(rig.lastSeen)}</span>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}
      </div>
    </div>
  );
}
