'use client';

import { useEffect, useState, useCallback } from 'react';
import Link from 'next/link';
import { useWebSocket } from '../hooks/useWebSocket';

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
  hashrate: number | null;
}

interface FlightSheet {
  id: string;
  name: string;
  coin: string;
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
  flightSheet: FlightSheet | null;
}

interface Stats {
  totalRigs: number;
  onlineRigs: number;
  offlineRigs: number;
  totalGpus: number;
  totalHashrate: number;
  totalPower: number;
  avgTemp: number;
  maxTemp: number;
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

interface HashrateHistory {
  timestamp: Date;
  hashrate: number;
  power: number;
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
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" />
  </svg>
);

const OfflineIcon = () => (
  <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M18.364 18.364A9 9 0 005.636 5.636m12.728 12.728A9 9 0 015.636 5.636m12.728 12.728L5.636 5.636" />
  </svg>
);

// Simple sparkline component
function Sparkline({ data, color, height = 40 }: { data: number[]; color: string; height?: number }) {
  if (data.length < 2) return null;
  
  const max = Math.max(...data);
  const min = Math.min(...data);
  const range = max - min || 1;
  
  const points = data.map((value, index) => {
    const x = (index / (data.length - 1)) * 100;
    const y = height - ((value - min) / range) * height;
    return `${x},${y}`;
  }).join(' ');
  
  return (
    <svg width="100%" height={height} className="overflow-visible">
      <polyline
        points={points}
        fill="none"
        stroke={color}
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

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
    maxTemp: 0,
    efficiency: 0,
  });
  const [recentAlerts, setRecentAlerts] = useState<Alert[]>([]);
  const [loading, setLoading] = useState(true);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [hashrateHistory, setHashrateHistory] = useState<number[]>([]);
  const [powerHistory, setPowerHistory] = useState<number[]>([]);
  const REFRESH_INTERVAL = 30000; // 30 seconds
  const MAX_HISTORY = 20;

  // WebSocket handler for real-time rig updates
  const handleRigsUpdate = useCallback((data: unknown) => {
    if (Array.isArray(data)) {
      processRigData(data as Rig[]);
    }
  }, []);

  // Connect to WebSocket for real-time updates
  const { isConnected } = useWebSocket({
    onRigsUpdate: handleRigsUpdate,
  });

  function processRigData(data: Rig[]) {
    setRigs(data);

    // Calculate stats
    const totalRigs = data.length;
    const onlineRigs = data.filter((r: Rig) => r.status === 'ONLINE').length;
    const offlineRigs = totalRigs - onlineRigs;
    const totalGpus = data.reduce((acc: number, r: Rig) => acc + r.gpus.length, 0);
    const totalHashrate = data.reduce((acc: number, r: Rig) => {
      const gpuHashrate = r.gpus.reduce((sum, gpu) => sum + (gpu.hashrate || 0), 0);
      const cpuHashrate = r.cpu?.hashrate || 0;
      return acc + gpuHashrate + cpuHashrate;
    }, 0);
    const totalPower = data.reduce((acc: number, r: Rig) => {
      const gpuPower = r.gpus.reduce((sum, gpu) => sum + (gpu.powerDraw || 0), 0);
      const cpuPower = r.cpu?.powerDraw || 0;
      return acc + gpuPower + cpuPower;
    }, 0);
    
    // Calculate average and max temperature
    const allTemps = data.flatMap((r: Rig) => r.gpus.map(g => g.temperature).filter((t: number | null) => t !== null)) as number[];
    const avgTemp = allTemps.length > 0 ? allTemps.reduce((a: number, b: number) => a + b, 0) / allTemps.length : 0;
    const maxTemp = allTemps.length > 0 ? Math.max(...allTemps) : 0;
    
    // Calculate efficiency (kH/W)
    const efficiency = totalPower > 0 ? (totalHashrate * 1000) / totalPower : 0;

    setStats({ totalRigs, onlineRigs, offlineRigs, totalGpus, totalHashrate, totalPower, avgTemp, maxTemp, efficiency });
    setLastUpdated(new Date());
    
    // Update history for sparklines
    setHashrateHistory(prev => [...prev.slice(-(MAX_HISTORY - 1)), totalHashrate]);
    setPowerHistory(prev => [...prev.slice(-(MAX_HISTORY - 1)), totalPower]);
    
    setLoading(false);
  }

  const fetchRigs = useCallback(async () => {
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
      processRigData(data);
    } catch (error) {
      console.error('Failed to fetch rigs:', error);
    } finally {
      setLoading(false);
    }
  }, []);

  const fetchAlerts = useCallback(async () => {
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
  }, []);

  useEffect(() => {
    fetchRigs();
    fetchAlerts();

    // Auto-refresh interval (fallback when WebSocket not connected)
    let intervalId: NodeJS.Timeout | null = null;
    if (autoRefresh && !isConnected) {
      intervalId = setInterval(() => {
        fetchRigs();
        fetchAlerts();
      }, REFRESH_INTERVAL);
    }

    return () => {
      if (intervalId) clearInterval(intervalId);
    };
  }, [autoRefresh, isConnected, fetchRigs, fetchAlerts]);

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

  // Group rigs by coin
  const rigsByCoin = rigs.reduce((acc, rig) => {
    const coin = rig.flightSheet?.coin || 'Idle';
    if (!acc[coin]) acc[coin] = [];
    acc[coin].push(rig);
    return acc;
  }, {} as Record<string, Rig[]>);

  const statCards = [
    { label: 'Total Rigs', value: stats.totalRigs, icon: RigIcon, color: 'from-slate-500 to-slate-600', textColor: 'text-slate-400' },
    { label: 'Online', value: stats.onlineRigs, icon: OnlineIcon, color: 'from-green-500 to-green-600', textColor: 'text-green-400' },
    { label: 'Offline', value: stats.offlineRigs, icon: OfflineIcon, color: 'from-red-500 to-red-600', textColor: 'text-red-400' },
    { label: 'Total GPUs', value: stats.totalGpus, icon: GpuIcon, color: 'from-blue-500 to-blue-600', textColor: 'text-blue-400' },
  ];

  const performanceCards = [
    { 
      label: 'Total Hashrate', 
      value: `${stats.totalHashrate.toFixed(1)}`, 
      unit: 'MH/s',
      icon: HashrateIcon, 
      color: 'from-purple-500 to-purple-600', 
      textColor: 'text-purple-400',
      history: hashrateHistory,
      chartColor: '#a855f7',
    },
    { 
      label: 'Total Power', 
      value: `${stats.totalPower.toFixed(0)}`, 
      unit: 'W',
      icon: PowerIcon, 
      color: 'from-yellow-500 to-yellow-600', 
      textColor: 'text-yellow-400',
      history: powerHistory,
      chartColor: '#eab308',
    },
    { 
      label: 'Efficiency', 
      value: `${stats.efficiency.toFixed(2)}`, 
      unit: 'kH/W',
      icon: TempIcon, 
      color: 'from-cyan-500 to-cyan-600', 
      textColor: 'text-cyan-400',
      history: [],
      chartColor: '#06b6d4',
    },
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
                isConnected
                  ? 'bg-green-500/20 text-green-400'
                  : autoRefresh
                  ? 'bg-yellow-500/20 text-yellow-400'
                  : 'bg-slate-700 text-slate-400'
              }`}
            >
              <span className={`w-2 h-2 rounded-full ${isConnected ? 'bg-green-400 animate-pulse' : autoRefresh ? 'bg-yellow-400 animate-pulse' : 'bg-slate-500'}`}></span>
              {isConnected ? 'Live' : autoRefresh ? 'Polling' : 'Paused'}
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

      {/* Stats Cards - Row 1 */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4 mb-6">
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
              <p className={`text-3xl font-bold mt-1 ${stat.textColor}`}>{stat.value}</p>
            </div>
          );
        })}
      </div>

      {/* Performance Cards with Charts */}
      <div className="grid grid-cols-1 md:grid-cols-3 gap-4 mb-8">
        {performanceCards.map((stat) => {
          const Icon = stat.icon;
          return (
            <div
              key={stat.label}
              className="bg-slate-800/50 backdrop-blur-sm rounded-xl p-5 border border-slate-700/50 hover:border-slate-600/50 transition-all duration-200"
            >
              <div className="flex items-center justify-between mb-4">
                <div className="flex items-center gap-3">
                  <div className={`w-10 h-10 rounded-lg bg-gradient-to-br ${stat.color} flex items-center justify-center opacity-80`}>
                    <Icon />
                  </div>
                  <div>
                    <p className="text-slate-400 text-xs font-medium uppercase tracking-wider">{stat.label}</p>
                    <p className={`text-2xl font-bold ${stat.textColor}`}>
                      {stat.value} <span className="text-sm text-slate-400">{stat.unit}</span>
                    </p>
                  </div>
                </div>
              </div>
              {stat.history.length > 1 && (
                <div className="h-10 mt-2">
                  <Sparkline data={stat.history} color={stat.chartColor} />
                </div>
              )}
            </div>
          );
        })}
      </div>

      {/* Temperature Card */}
      <div className="grid grid-cols-1 md:grid-cols-2 gap-4 mb-8">
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl p-5 border border-slate-700/50">
          <h3 className="text-lg font-semibold mb-4">Temperature Overview</h3>
          <div className="grid grid-cols-2 gap-4">
            <div className="bg-slate-900/50 rounded-lg p-4 text-center">
              <p className="text-xs text-slate-400 uppercase mb-1">Average</p>
              <p className={`text-3xl font-bold ${stats.avgTemp > 75 ? 'text-yellow-400' : stats.avgTemp > 85 ? 'text-red-400' : 'text-green-400'}`}>
                {stats.avgTemp.toFixed(0)}°C
              </p>
            </div>
            <div className="bg-slate-900/50 rounded-lg p-4 text-center">
              <p className="text-xs text-slate-400 uppercase mb-1">Max</p>
              <p className={`text-3xl font-bold ${stats.maxTemp > 80 ? 'text-red-400' : stats.maxTemp > 70 ? 'text-yellow-400' : 'text-green-400'}`}>
                {stats.maxTemp.toFixed(0)}°C
              </p>
            </div>
          </div>
          {/* Temperature bar */}
          <div className="mt-4">
            <div className="flex justify-between text-xs text-slate-500 mb-1">
              <span>0°C</span>
              <span>50°C</span>
              <span>100°C</span>
            </div>
            <div className="h-3 bg-slate-700 rounded-full overflow-hidden">
              <div 
                className="h-full bg-gradient-to-r from-green-500 via-yellow-500 to-red-500 rounded-full transition-all"
                style={{ width: `${Math.min(stats.maxTemp, 100)}%` }}
              ></div>
            </div>
          </div>
        </div>

        {/* Mining by Coin */}
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl p-5 border border-slate-700/50">
          <h3 className="text-lg font-semibold mb-4">Mining by Coin</h3>
          {Object.keys(rigsByCoin).length === 0 ? (
            <p className="text-slate-400 text-center py-4">No rigs configured</p>
          ) : (
            <div className="space-y-3">
              {Object.entries(rigsByCoin).map(([coin, coinRigs]) => {
                const hashrate = coinRigs.reduce((sum, r) => 
                  sum + r.gpus.reduce((s, g) => s + (g.hashrate || 0), 0) + (r.cpu?.hashrate || 0), 0
                );
                const onlineCount = coinRigs.filter(r => r.status === 'ONLINE').length;
                return (
                  <div key={coin} className="flex items-center justify-between p-3 bg-slate-900/50 rounded-lg">
                    <div className="flex items-center gap-3">
                      <span className={`px-2 py-1 rounded text-xs font-medium ${coin === 'Idle' ? 'bg-slate-600 text-slate-300' : 'bg-purple-500/20 text-purple-400'}`}>
                        {coin}
                      </span>
                      <span className="text-sm text-slate-400">
                        {onlineCount}/{coinRigs.length} rigs
                      </span>
                    </div>
                    <span className="text-purple-400 font-medium">
                      {hashrate.toFixed(1)} MH/s
                    </span>
                  </div>
                );
              })}
            </div>
          )}
        </div>
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
                  <th className="px-5 py-3">Coin</th>
                  <th className="px-5 py-3">GPUs</th>
                  <th className="px-5 py-3">Hashrate</th>
                  <th className="px-5 py-3">Power</th>
                  <th className="px-5 py-3">Temp</th>
                  <th className="px-5 py-3">Last Seen</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-700/50">
                {rigs.slice(0, 10).map((rig) => {
                  const hashrate = rig.gpus.reduce((sum, gpu) => sum + (gpu.hashrate || 0), 0) + (rig.cpu?.hashrate || 0);
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
                        <span className={`px-2 py-1 rounded text-xs font-medium ${rig.flightSheet ? 'bg-purple-500/20 text-purple-400' : 'bg-slate-600 text-slate-300'}`}>
                          {rig.flightSheet?.coin || 'Idle'}
                        </span>
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
            {rigs.length > 10 && (
              <div className="px-5 py-3 border-t border-slate-700/50 text-center">
                <Link href="/rigs" className="text-sm text-blox-400 hover:text-blox-300">
                  View all {rigs.length} rigs →
                </Link>
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}
