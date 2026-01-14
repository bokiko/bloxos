'use client';

import { useEffect, useState, useCallback } from 'react';

const getApiUrl = () => {
  if (typeof window === 'undefined') return 'http://localhost:3001';
  return `http://${window.location.hostname}:3001`;
};

interface ProfitSummary {
  period: string;
  startDate: string;
  endDate: string;
  totals: {
    revenue: number;
    electricityCost: number;
    profit: number;
    powerKwh: number;
  };
  rigsCount: number;
  snapshotsCount: number;
  daily: Array<{
    date: string;
    revenue: number;
    cost: number;
    profit: number;
  }>;
}

interface RigProfit {
  rigId: string;
  rigName: string;
  status: string;
  coin: string | null;
  revenue: number;
  electricityCost: number;
  profit: number;
  avgHashrate: number;
  daysTracked: number;
}

interface CoinPrice {
  ticker: string;
  priceUsd: number;
  priceChange24h: number | null;
  fetchedAt: string;
}

interface ElectricitySettings {
  rate: number;
  currency: string;
}

// Icons
const ChartIcon = () => (
  <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 19v-6a2 2 0 00-2-2H5a2 2 0 00-2 2v6a2 2 0 002 2h2a2 2 0 002-2zm0 0V9a2 2 0 012-2h2a2 2 0 012 2v10m-6 0a2 2 0 002 2h2a2 2 0 002-2m0 0V5a2 2 0 012-2h2a2 2 0 012 2v14a2 2 0 01-2 2h-2a2 2 0 01-2-2z" />
  </svg>
);

const DollarIcon = () => (
  <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 8c-1.657 0-3 .895-3 2s1.343 2 3 2 3 .895 3 2-1.343 2-3 2m0-8c1.11 0 2.08.402 2.599 1M12 8V7m0 1v8m0 0v1m0-1c-1.11 0-2.08-.402-2.599-1M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
  </svg>
);

const BoltIcon = () => (
  <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 10V3L4 14h7v7l9-11h-7z" />
  </svg>
);

const TrendingUpIcon = () => (
  <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 7h8m0 0v8m0-8l-8 8-4-4-6 6" />
  </svg>
);

const TrendingDownIcon = () => (
  <svg className="w-6 h-6" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 17h8m0 0V9m0 8l-8-8-4 4-6-6" />
  </svg>
);

export default function ProfitPage() {
  const [summary, setSummary] = useState<ProfitSummary | null>(null);
  const [rigProfits, setRigProfits] = useState<RigProfit[]>([]);
  const [prices, setPrices] = useState<CoinPrice[]>([]);
  const [settings, setSettings] = useState<ElectricitySettings | null>(null);
  const [loading, setLoading] = useState(true);
  const [period, setPeriod] = useState<'day' | 'week' | 'month'>('month');

  const fetchData = useCallback(async () => {
    setLoading(true);
    try {
      const [summaryRes, rigsRes, pricesRes, settingsRes] = await Promise.all([
        fetch(`${getApiUrl()}/api/profit/summary?period=${period}`, { credentials: 'include' }),
        fetch(`${getApiUrl()}/api/profit/by-rig?period=${period}`, { credentials: 'include' }),
        fetch(`${getApiUrl()}/api/profit/prices`, { credentials: 'include' }),
        fetch(`${getApiUrl()}/api/profit/settings`, { credentials: 'include' }),
      ]);

      if (summaryRes.ok) setSummary(await summaryRes.json());
      if (rigsRes.ok) {
        const data = await rigsRes.json();
        setRigProfits(data.rigs || []);
      }
      if (pricesRes.ok) setPrices(await pricesRes.json());
      if (settingsRes.ok) setSettings(await settingsRes.json());
    } catch (err) {
      console.error('Failed to fetch profit data:', err);
    } finally {
      setLoading(false);
    }
  }, [period]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  const formatCurrency = (value: number) => {
    return new Intl.NumberFormat('en-US', {
      style: 'currency',
      currency: 'USD',
      minimumFractionDigits: 2,
      maximumFractionDigits: 2,
    }).format(value);
  };

  const formatNumber = (value: number, decimals = 2) => {
    return value.toFixed(decimals);
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-indigo-500"></div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold text-white">Profit Tracking</h1>
          <p className="text-slate-400 mt-1">Monitor your mining revenue and costs</p>
        </div>

        {/* Period Selector */}
        <div className="flex gap-2">
          {(['day', 'week', 'month'] as const).map((p) => (
            <button
              key={p}
              onClick={() => setPeriod(p)}
              className={`px-4 py-2 rounded-lg text-sm font-medium transition-colors ${
                period === p
                  ? 'bg-indigo-600 text-white'
                  : 'bg-slate-700 text-slate-300 hover:bg-slate-600'
              }`}
            >
              {p === 'day' ? '24h' : p === 'week' ? '7d' : '30d'}
            </button>
          ))}
        </div>
      </div>

      {/* Stats Cards */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-4 gap-4">
        {/* Revenue */}
        <div className="bg-slate-800 rounded-xl p-6 border border-slate-700">
          <div className="flex items-center justify-between">
            <div>
              <p className="text-slate-400 text-sm">Revenue</p>
              <p className="text-2xl font-bold text-green-400 mt-1">
                {formatCurrency(summary?.totals.revenue || 0)}
              </p>
            </div>
            <div className="p-3 bg-green-500/10 rounded-lg">
              <DollarIcon />
            </div>
          </div>
        </div>

        {/* Electricity Cost */}
        <div className="bg-slate-800 rounded-xl p-6 border border-slate-700">
          <div className="flex items-center justify-between">
            <div>
              <p className="text-slate-400 text-sm">Electricity Cost</p>
              <p className="text-2xl font-bold text-orange-400 mt-1">
                {formatCurrency(summary?.totals.electricityCost || 0)}
              </p>
              <p className="text-xs text-slate-500 mt-1">
                {formatNumber(summary?.totals.powerKwh || 0)} kWh @ ${settings?.rate || 0.10}/kWh
              </p>
            </div>
            <div className="p-3 bg-orange-500/10 rounded-lg">
              <BoltIcon />
            </div>
          </div>
        </div>

        {/* Net Profit */}
        <div className="bg-slate-800 rounded-xl p-6 border border-slate-700">
          <div className="flex items-center justify-between">
            <div>
              <p className="text-slate-400 text-sm">Net Profit</p>
              <p className={`text-2xl font-bold mt-1 ${(summary?.totals.profit || 0) >= 0 ? 'text-emerald-400' : 'text-red-400'}`}>
                {formatCurrency(summary?.totals.profit || 0)}
              </p>
            </div>
            <div className={`p-3 rounded-lg ${(summary?.totals.profit || 0) >= 0 ? 'bg-emerald-500/10' : 'bg-red-500/10'}`}>
              {(summary?.totals.profit || 0) >= 0 ? <TrendingUpIcon /> : <TrendingDownIcon />}
            </div>
          </div>
        </div>

        {/* Active Rigs */}
        <div className="bg-slate-800 rounded-xl p-6 border border-slate-700">
          <div className="flex items-center justify-between">
            <div>
              <p className="text-slate-400 text-sm">Active Rigs</p>
              <p className="text-2xl font-bold text-white mt-1">{summary?.rigsCount || 0}</p>
              <p className="text-xs text-slate-500 mt-1">
                {summary?.snapshotsCount || 0} data points
              </p>
            </div>
            <div className="p-3 bg-indigo-500/10 rounded-lg">
              <ChartIcon />
            </div>
          </div>
        </div>
      </div>

      {/* Daily Profit Chart */}
      {summary && summary.daily.length > 0 && (
        <div className="bg-slate-800 rounded-xl p-6 border border-slate-700">
          <h2 className="text-lg font-semibold text-white mb-4">Daily Profit</h2>
          <div className="h-64 flex items-end gap-1">
            {summary.daily.map((day, i) => {
              const maxValue = Math.max(...summary.daily.map(d => Math.abs(d.profit)), 1);
              const height = Math.abs(day.profit) / maxValue * 100;
              const isPositive = day.profit >= 0;

              return (
                <div
                  key={i}
                  className="flex-1 flex flex-col items-center justify-end group relative"
                >
                  <div
                    className={`w-full rounded-t transition-all ${
                      isPositive ? 'bg-emerald-500' : 'bg-red-500'
                    } group-hover:opacity-80`}
                    style={{ height: `${Math.max(height, 2)}%` }}
                  />
                  <div className="absolute bottom-full mb-2 hidden group-hover:block bg-slate-700 px-2 py-1 rounded text-xs text-white whitespace-nowrap z-10">
                    {new Date(day.date).toLocaleDateString()}: {formatCurrency(day.profit)}
                  </div>
                </div>
              );
            })}
          </div>
          <div className="flex justify-between mt-2 text-xs text-slate-500">
            <span>{summary.daily[0]?.date}</span>
            <span>{summary.daily[summary.daily.length - 1]?.date}</span>
          </div>
        </div>
      )}

      {/* Per-Rig Breakdown */}
      <div className="bg-slate-800 rounded-xl border border-slate-700 overflow-hidden">
        <div className="px-6 py-4 border-b border-slate-700">
          <h2 className="text-lg font-semibold text-white">Profit by Rig</h2>
        </div>
        <div className="overflow-x-auto">
          <table className="w-full">
            <thead className="bg-slate-700/50">
              <tr>
                <th className="text-left px-6 py-3 text-xs font-medium text-slate-400 uppercase tracking-wider">Rig</th>
                <th className="text-left px-6 py-3 text-xs font-medium text-slate-400 uppercase tracking-wider">Coin</th>
                <th className="text-right px-6 py-3 text-xs font-medium text-slate-400 uppercase tracking-wider">Hashrate</th>
                <th className="text-right px-6 py-3 text-xs font-medium text-slate-400 uppercase tracking-wider">Revenue</th>
                <th className="text-right px-6 py-3 text-xs font-medium text-slate-400 uppercase tracking-wider">Cost</th>
                <th className="text-right px-6 py-3 text-xs font-medium text-slate-400 uppercase tracking-wider">Profit</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-700">
              {rigProfits.length === 0 ? (
                <tr>
                  <td colSpan={6} className="px-6 py-8 text-center text-slate-400">
                    No profit data yet. Mining data will appear once rigs start reporting.
                  </td>
                </tr>
              ) : (
                rigProfits.map((rig) => (
                  <tr key={rig.rigId} className="hover:bg-slate-700/30">
                    <td className="px-6 py-4">
                      <div className="flex items-center">
                        <div className={`w-2 h-2 rounded-full mr-3 ${
                          rig.status === 'ONLINE' ? 'bg-green-500' :
                          rig.status === 'WARNING' ? 'bg-yellow-500' : 'bg-red-500'
                        }`} />
                        <span className="text-white font-medium">{rig.rigName}</span>
                      </div>
                    </td>
                    <td className="px-6 py-4 text-slate-300">{rig.coin || '-'}</td>
                    <td className="px-6 py-4 text-right text-slate-300">
                      {formatNumber(rig.avgHashrate)} MH/s
                    </td>
                    <td className="px-6 py-4 text-right text-green-400">
                      {formatCurrency(rig.revenue)}
                    </td>
                    <td className="px-6 py-4 text-right text-orange-400">
                      {formatCurrency(rig.electricityCost)}
                    </td>
                    <td className={`px-6 py-4 text-right font-medium ${
                      rig.profit >= 0 ? 'text-emerald-400' : 'text-red-400'
                    }`}>
                      {formatCurrency(rig.profit)}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>
      </div>

      {/* Coin Prices */}
      {prices.length > 0 && (
        <div className="bg-slate-800 rounded-xl border border-slate-700 overflow-hidden">
          <div className="px-6 py-4 border-b border-slate-700">
            <h2 className="text-lg font-semibold text-white">Current Coin Prices</h2>
          </div>
          <div className="grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6 gap-4 p-6">
            {prices.slice(0, 12).map((coin) => (
              <div key={coin.ticker} className="bg-slate-700/50 rounded-lg p-4">
                <p className="text-sm font-medium text-white">{coin.ticker}</p>
                <p className="text-lg font-bold text-white mt-1">
                  ${coin.priceUsd < 1 ? coin.priceUsd.toFixed(6) : coin.priceUsd.toFixed(2)}
                </p>
                {coin.priceChange24h !== null && (
                  <p className={`text-xs mt-1 ${
                    coin.priceChange24h >= 0 ? 'text-green-400' : 'text-red-400'
                  }`}>
                    {coin.priceChange24h >= 0 ? '+' : ''}{coin.priceChange24h.toFixed(2)}%
                  </p>
                )}
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Info Notice */}
      <div className="bg-slate-800/50 rounded-xl p-4 border border-slate-700">
        <p className="text-sm text-slate-400">
          <span className="text-yellow-400 font-medium">Note:</span> Revenue estimates are based on hashrate and current coin prices.
          Actual pool payouts may vary. Electricity costs are calculated from reported GPU power draw at ${settings?.rate || 0.10}/kWh.
        </p>
      </div>
    </div>
  );
}
