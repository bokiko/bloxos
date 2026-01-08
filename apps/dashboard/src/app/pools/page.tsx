'use client';

import { useEffect, useState, useCallback } from 'react';
import Image from 'next/image';

const getApiUrl = () => {
  if (typeof window === 'undefined') return 'http://localhost:3001';
  return `http://${window.location.hostname}:3001`;
};

// Helper to get CSRF token from cookie
function getCsrfToken(): string | null {
  if (typeof document === 'undefined') return null;
  const match = document.cookie.match(/csrf_token=([^;]+)/);
  return match ? match[1] : null;
}

interface Pool {
  id: string;
  name: string;
  coin: string;
  url: string;
  url2: string | null;
  url3: string | null;
  user: string | null;
  pass: string | null;
  farmId: string;
  createdAt: string;
  _count?: {
    flightSheets: number;
  };
}

interface Farm {
  id: string;
  name: string;
}

interface Coin {
  id: string;
  ticker: string;
  name: string;
  algorithm: string;
  type: 'GPU' | 'CPU';
  logoPath: string | null;
}

interface PoolPreset {
  id: string;
  name: string;
  region: string;
  host: string;
  port: number;
  sslPort: number | null;
  fee: number | null;
  coinId: string;
}

// Icons
const PlusIcon = () => (
  <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
  </svg>
);

const TrashIcon = () => (
  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
  </svg>
);

const EditIcon = () => (
  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
  </svg>
);

const PoolIcon = () => (
  <svg className="w-8 h-8 text-slate-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M5 12h14M5 12a2 2 0 01-2-2V6a2 2 0 012-2h14a2 2 0 012 2v4a2 2 0 01-2 2M5 12a2 2 0 00-2 2v4a2 2 0 002 2h14a2 2 0 002-2v-4a2 2 0 00-2-2m-2-4h.01M17 16h.01" />
  </svg>
);

const GlobeIcon = () => (
  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M3.055 11H5a2 2 0 012 2v1a2 2 0 002 2 2 2 0 012 2v2.945M8 3.935V5.5A2.5 2.5 0 0010.5 8h.5a2 2 0 012 2 2 2 0 104 0 2 2 0 012-2h1.064M15 20.488V18a2 2 0 012-2h3.064M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
  </svg>
);

// Coin colors
const coinColors: Record<string, string> = {
  BTC: 'bg-orange-500',
  ETH: 'bg-blue-500',
  KAS: 'bg-teal-500',
  RVN: 'bg-purple-500',
  ETC: 'bg-green-500',
  ERGO: 'bg-red-500',
  ERG: 'bg-red-500',
  FLUX: 'bg-blue-400',
  XMR: 'bg-orange-400',
  VRSC: 'bg-blue-600',
  DEFAULT: 'bg-slate-500',
};

function getCoinColor(coin: string) {
  return coinColors[coin.toUpperCase()] || coinColors.DEFAULT;
}

const regionFlags: Record<string, string> = {
  US: '🇺🇸',
  EU: '🇪🇺',
  ASIA: '🇸🇬',
  GLOBAL: '🌍',
};

export default function PoolsPage() {
  const [pools, setPools] = useState<Pool[]>([]);
  const [farms, setFarms] = useState<Farm[]>([]);
  const [coins, setCoins] = useState<Coin[]>([]);
  const [poolPresets, setPoolPresets] = useState<PoolPreset[]>([]);
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);
  const [showPresetsModal, setShowPresetsModal] = useState(false);
  const [editingPool, setEditingPool] = useState<Pool | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [selectedCoin, setSelectedCoin] = useState<string | null>(null);
  const [addingPreset, setAddingPreset] = useState<string | null>(null);
  const [searchQuery, setSearchQuery] = useState('');

  // Form state
  const [formData, setFormData] = useState({
    name: '',
    coin: '',
    url: '',
    url2: '',
    url3: '',
    user: '',
    pass: '',
    farmId: '',
  });

  const fetchPools = useCallback(async () => {
    try {
      const res = await fetch(`${getApiUrl()}/api/pools`, {
        credentials: 'include',
      });
      if (!res.ok) return;
      const data = await res.json();
      if (Array.isArray(data)) {
        setPools(data);
      }
    } catch (err) {
      setError('Failed to fetch pools');
    } finally {
      setLoading(false);
    }
  }, []);

  const fetchCoins = useCallback(async () => {
    try {
      const res = await fetch(`${getApiUrl()}/api/coins`, {
        credentials: 'include',
      });
      if (!res.ok) return;
      const data = await res.json();
      if (Array.isArray(data)) {
        setCoins(data);
      }
    } catch (err) {
      console.error('Failed to fetch coins');
    }
  }, []);

  const fetchData = useCallback(async () => {
    try {
      // Fetch user data to get farms
      const meRes = await fetch(`${getApiUrl()}/api/auth/me`, {
        credentials: 'include',
      });
      if (meRes.ok) {
        const meData = await meRes.json();
        if (meData.farms && meData.farms.length > 0) {
          setFarms(meData.farms);
          setFormData(prev => ({ ...prev, farmId: meData.farms[0].id }));
        }
      }
      
      // Fetch pools, coins, and templates
      await Promise.all([
        fetchPools(),
        fetchCoins(),
      ]);
    } catch (err) {
      setError('Failed to load data');
    }
  }, [fetchPools, fetchCoins]);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  async function fetchPoolPresets(ticker: string) {
    try {
      const res = await fetch(`${getApiUrl()}/api/coins/${ticker}/pools`, {
        credentials: 'include',
      });
      if (!res.ok) return;
      const data = await res.json();
      if (Array.isArray(data)) {
        setPoolPresets(data);
      }
    } catch (err) {
      console.error('Failed to fetch pool presets');
    }
  }

  function openCreateModal() {
    setEditingPool(null);
    setFormData({ name: '', coin: '', url: '', url2: '', url3: '', user: '', pass: '', farmId: farms[0]?.id || '' });
    setShowModal(true);
  }

  function openEditModal(pool: Pool) {
    setEditingPool(pool);
    setFormData({
      name: pool.name,
      coin: pool.coin,
      url: pool.url,
      url2: pool.url2 || '',
      url3: pool.url3 || '',
      user: pool.user || '',
      pass: pool.pass || '',
      farmId: pool.farmId,
    });
    setShowModal(true);
  }

  function openPresetsModal(coin: Coin) {
    setSelectedCoin(coin.ticker);
    fetchPoolPresets(coin.ticker);
    setShowPresetsModal(true);
  }

  async function addFromPreset(preset: PoolPreset, coin: Coin) {
    setAddingPreset(preset.id);
    try {
      const res = await fetch(`${getApiUrl()}/api/pools`, {
        method: 'POST',
        headers: { 
          'Content-Type': 'application/json',
          'X-CSRF-Token': getCsrfToken() || '',
        },
        credentials: 'include',
        body: JSON.stringify({
          name: `${preset.name} (${preset.region})`,
          coin: coin.ticker,
          url: `stratum+tcp://${preset.host}:${preset.port}`,
          url2: preset.sslPort ? `stratum+ssl://${preset.host}:${preset.sslPort}` : undefined,
          user: '%WALLET%.%WORKER%',
          pass: 'x',
          farmId: farms[0]?.id,
        }),
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || 'Failed to add pool');
      }

      await fetchPools();
      setShowPresetsModal(false);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to add pool');
    } finally {
      setAddingPreset(null);
    }
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);

    if (!formData.farmId) {
      setError('No farm available. Please refresh the page.');
      return;
    }

    // Build payload, excluding empty optional fields
    const payload: Record<string, string> = {
      name: formData.name,
      coin: formData.coin,
      url: formData.url,
      farmId: formData.farmId,
    };
    
    // Only include optional fields if they have values
    if (formData.url2) payload.url2 = formData.url2;
    if (formData.url3) payload.url3 = formData.url3;
    if (formData.user) payload.user = formData.user;
    if (formData.pass) payload.pass = formData.pass;

    try {
      const url = editingPool
        ? `${getApiUrl()}/api/pools/${editingPool.id}`
        : `${getApiUrl()}/api/pools`;
      
      const res = await fetch(url, {
        method: editingPool ? 'PATCH' : 'POST',
        headers: { 
          'Content-Type': 'application/json',
          'X-CSRF-Token': getCsrfToken() || '',
        },
        credentials: 'include',
        body: JSON.stringify(payload),
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || data.message || 'Failed to save pool');
      }

      setShowModal(false);
      fetchPools();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save pool');
    }
  }

  async function handleDelete(pool: Pool) {
    if (!confirm(`Delete pool "${pool.name}"?`)) return;

    try {
      const res = await fetch(`${getApiUrl()}/api/pools/${pool.id}`, {
        method: 'DELETE',
        headers: {
          'X-CSRF-Token': getCsrfToken() || '',
        },
        credentials: 'include',
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || data.message || 'Failed to delete pool');
      }

      fetchPools();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete pool');
    }
  }

  // Group coins by type
  const gpuCoins = coins.filter(c => c.type === 'GPU');
  const cpuCoins = coins.filter(c => c.type === 'CPU');

  // Filter pools by search
  const filteredPools = pools.filter(p => 
    p.name.toLowerCase().includes(searchQuery.toLowerCase()) ||
    p.coin.toLowerCase().includes(searchQuery.toLowerCase()) ||
    p.url.toLowerCase().includes(searchQuery.toLowerCase())
  );

  return (
    <div>
      {/* Header */}
      <div className="flex justify-between items-center mb-6">
        <div>
          <h1 className="text-2xl font-bold">Pools</h1>
          <p className="text-slate-400 text-sm mt-1">Configure mining pool connections</p>
        </div>
        <div className="flex items-center gap-3">
          <button
            onClick={() => setShowPresetsModal(true)}
            className="flex items-center gap-2 px-4 py-2.5 bg-purple-500/20 hover:bg-purple-500/30 text-purple-400 rounded-lg font-medium transition-all duration-200"
          >
            <GlobeIcon /> Browse Presets
          </button>
          <button
            onClick={openCreateModal}
            className="flex items-center gap-2 px-4 py-2.5 bg-gradient-to-r from-blox-500 to-blox-600 hover:from-blox-600 hover:to-blox-700 rounded-lg text-white font-medium transition-all duration-200 shadow-lg shadow-blox-500/25"
          >
            <PlusIcon /> Add Pool
          </button>
        </div>
      </div>

      {/* Search */}
      <div className="mb-6">
        <input
          type="text"
          placeholder="Search pools..."
          value={searchQuery}
          onChange={(e) => setSearchQuery(e.target.value)}
          className="w-full md:w-80 px-4 py-2.5 bg-slate-800/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
        />
      </div>

      {/* Error Banner */}
      {error && (
        <div className="mb-6 flex items-center justify-between p-4 bg-red-500/10 border border-red-500/30 rounded-xl text-red-400">
          <span>{error}</span>
          <button onClick={() => setError(null)} className="p-1 hover:bg-red-500/20 rounded-lg">
            <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
            </svg>
          </button>
        </div>
      )}

      {/* Pools List */}
      {loading ? (
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-12 text-center">
          <div className="inline-block w-8 h-8 border-2 border-slate-600 border-t-blox-400 rounded-full animate-spin"></div>
          <p className="text-slate-400 mt-3">Loading pools...</p>
        </div>
      ) : filteredPools.length === 0 ? (
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-12 text-center">
          <div className="w-16 h-16 mx-auto mb-4 rounded-full bg-slate-700/50 flex items-center justify-center">
            <PoolIcon />
          </div>
          <p className="text-slate-400 mb-4">
            {searchQuery ? 'No pools match your search' : 'No pools configured yet'}
          </p>
          {!searchQuery && (
            <div className="flex items-center justify-center gap-3">
              <button
                onClick={() => setShowPresetsModal(true)}
                className="px-4 py-2 bg-purple-600 hover:bg-purple-700 rounded-lg text-white font-medium transition-colors"
              >
                Browse Presets
              </button>
              <button
                onClick={openCreateModal}
                className="px-4 py-2 bg-blox-600 hover:bg-blox-700 rounded-lg text-white font-medium transition-colors"
              >
                Add Custom Pool
              </button>
            </div>
          )}
        </div>
      ) : (
        <div className="grid gap-4">
          {filteredPools.map((pool) => {
            const coinData = coins.find(c => c.ticker.toUpperCase() === pool.coin.toUpperCase());
            return (
              <div
                key={pool.id}
                className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-5 hover:border-slate-600/50 transition-all"
              >
                <div className="flex items-start justify-between">
                  <div className="flex items-center gap-4">
                    <div className={`w-12 h-12 rounded-xl ${getCoinColor(pool.coin)} flex items-center justify-center font-bold text-white overflow-hidden`}>
                      {coinData?.logoPath ? (
                        <Image
                          src={coinData.logoPath}
                          alt={pool.coin}
                          width={48}
                          height={48}
                          className="w-full h-full object-cover"
                        />
                      ) : (
                        pool.coin.slice(0, 3)
                      )}
                    </div>
                    <div className="flex-1">
                      <div className="flex items-center gap-2">
                        <h3 className="font-semibold text-lg">{pool.name}</h3>
                        <span className="px-2 py-0.5 bg-slate-700 rounded text-xs text-slate-300">{pool.coin}</span>
                        {coinData && (
                          <span className={`px-2 py-0.5 rounded text-xs ${coinData.type === 'GPU' ? 'bg-blue-500/20 text-blue-400' : 'bg-green-500/20 text-green-400'}`}>
                            {coinData.type}
                          </span>
                        )}
                      </div>
                      <p className="text-sm text-slate-400 font-mono mt-1">{pool.url}</p>
                      {pool.url2 && (
                        <p className="text-xs text-slate-500 font-mono mt-0.5">Backup: {pool.url2}</p>
                      )}
                      {pool.user && (
                        <p className="text-xs text-slate-500 mt-1">User template: {pool.user}</p>
                      )}
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    {pool._count && pool._count.flightSheets > 0 && (
                      <span className="text-xs text-slate-500 mr-2">
                        {pool._count.flightSheets} flight sheet{pool._count.flightSheets > 1 ? 's' : ''}
                      </span>
                    )}
                    <button
                      onClick={() => openEditModal(pool)}
                      className="p-2 rounded-lg text-slate-400 hover:text-white hover:bg-slate-700 transition-colors"
                    >
                      <EditIcon />
                    </button>
                    <button
                      onClick={() => handleDelete(pool)}
                      className="p-2 rounded-lg text-slate-400 hover:text-red-400 hover:bg-red-500/10 transition-colors"
                    >
                      <TrashIcon />
                    </button>
                  </div>
                </div>
              </div>
            );
          })}
        </div>
      )}

      {/* Pool Presets Modal */}
      {showPresetsModal && (
        <div className="fixed inset-0 bg-black/50 backdrop-blur-sm flex items-center justify-center z-50">
          <div className="bg-slate-800 rounded-xl border border-slate-700 p-6 w-full max-w-4xl mx-4 max-h-[90vh] overflow-y-auto">
            <div className="flex items-center justify-between mb-6">
              <div>
                <h2 className="text-xl font-bold">Pool Presets</h2>
                <p className="text-slate-400 text-sm mt-1">Pre-configured pools for popular coins</p>
              </div>
              <button
                onClick={() => { setShowPresetsModal(false); setSelectedCoin(null); setPoolPresets([]); }}
                className="p-2 hover:bg-slate-700 rounded-lg transition-colors"
              >
                <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                </svg>
              </button>
            </div>

            {!selectedCoin ? (
              <>
                {/* Coin Selection */}
                <div className="mb-6">
                  <h3 className="text-sm font-medium text-slate-400 mb-3">GPU Coins</h3>
                  <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-6 gap-3">
                    {gpuCoins.map(coin => (
                      <button
                        key={coin.id}
                        onClick={() => openPresetsModal(coin)}
                        className="flex items-center gap-3 p-3 bg-slate-900/50 border border-slate-700 rounded-lg hover:border-blox-500/50 transition-all"
                      >
                        <div className={`w-10 h-10 rounded-lg ${getCoinColor(coin.ticker)} flex items-center justify-center overflow-hidden`}>
                          {coin.logoPath ? (
                            <Image
                              src={coin.logoPath}
                              alt={coin.ticker}
                              width={40}
                              height={40}
                              className="w-full h-full object-cover"
                            />
                          ) : (
                            <span className="text-white font-bold text-sm">{coin.ticker.slice(0, 3)}</span>
                          )}
                        </div>
                        <div className="text-left">
                          <p className="font-medium">{coin.ticker}</p>
                          <p className="text-xs text-slate-400">{coin.algorithm}</p>
                        </div>
                      </button>
                    ))}
                  </div>
                </div>

                <div>
                  <h3 className="text-sm font-medium text-slate-400 mb-3">CPU Coins</h3>
                  <div className="grid grid-cols-2 md:grid-cols-4 lg:grid-cols-6 gap-3">
                    {cpuCoins.map(coin => (
                      <button
                        key={coin.id}
                        onClick={() => openPresetsModal(coin)}
                        className="flex items-center gap-3 p-3 bg-slate-900/50 border border-slate-700 rounded-lg hover:border-blox-500/50 transition-all"
                      >
                        <div className={`w-10 h-10 rounded-lg ${getCoinColor(coin.ticker)} flex items-center justify-center overflow-hidden`}>
                          {coin.logoPath ? (
                            <Image
                              src={coin.logoPath}
                              alt={coin.ticker}
                              width={40}
                              height={40}
                              className="w-full h-full object-cover"
                            />
                          ) : (
                            <span className="text-white font-bold text-sm">{coin.ticker.slice(0, 3)}</span>
                          )}
                        </div>
                        <div className="text-left">
                          <p className="font-medium">{coin.ticker}</p>
                          <p className="text-xs text-slate-400">{coin.algorithm}</p>
                        </div>
                      </button>
                    ))}
                  </div>
                </div>
              </>
            ) : (
              <>
                {/* Pool Presets for Selected Coin */}
                <button
                  onClick={() => { setSelectedCoin(null); setPoolPresets([]); }}
                  className="flex items-center gap-2 text-sm text-slate-400 hover:text-white mb-4"
                >
                  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M15 19l-7-7 7-7" />
                  </svg>
                  Back to coins
                </button>

                <div className="flex items-center gap-3 mb-6">
                  {(() => {
                    const coin = coins.find(c => c.ticker === selectedCoin);
                    return coin ? (
                      <>
                        <div className={`w-12 h-12 rounded-lg ${getCoinColor(coin.ticker)} flex items-center justify-center overflow-hidden`}>
                          {coin.logoPath ? (
                            <Image src={coin.logoPath} alt={coin.ticker} width={48} height={48} className="w-full h-full object-cover" />
                          ) : (
                            <span className="text-white font-bold">{coin.ticker.slice(0, 3)}</span>
                          )}
                        </div>
                        <div>
                          <h3 className="text-lg font-semibold">{coin.name}</h3>
                          <p className="text-sm text-slate-400">{coin.algorithm}</p>
                        </div>
                      </>
                    ) : null;
                  })()}
                </div>

                {poolPresets.length === 0 ? (
                  <div className="text-center py-8 text-slate-400">
                    <p>No pool presets available for this coin</p>
                  </div>
                ) : (
                  <div className="space-y-3">
                    {['US', 'EU', 'ASIA', 'GLOBAL'].map(region => {
                      const regionPools = poolPresets.filter(p => p.region === region);
                      if (regionPools.length === 0) return null;
                      return (
                        <div key={region}>
                          <h4 className="text-sm font-medium text-slate-400 mb-2 flex items-center gap-2">
                            <span>{regionFlags[region] || '🌍'}</span> {region}
                          </h4>
                          <div className="grid gap-2">
                            {regionPools.map(preset => {
                              const coin = coins.find(c => c.ticker === selectedCoin);
                              const isAdding = addingPreset === preset.id;
                              return (
                                <div
                                  key={preset.id}
                                  className="flex items-center justify-between p-4 bg-slate-900/50 border border-slate-700 rounded-lg"
                                >
                                  <div>
                                    <h5 className="font-medium">{preset.name}</h5>
                                    <p className="text-sm text-slate-400 font-mono">
                                      {preset.host}:{preset.port}
                                      {preset.sslPort && <span className="text-slate-500"> (SSL: {preset.sslPort})</span>}
                                    </p>
                                    {preset.fee !== null && (
                                      <p className="text-xs text-slate-500 mt-1">Fee: {preset.fee}%</p>
                                    )}
                                  </div>
                                  <button
                                    onClick={() => coin && addFromPreset(preset, coin)}
                                    disabled={isAdding}
                                    className="px-4 py-2 bg-blox-600 hover:bg-blox-700 disabled:bg-slate-700 rounded-lg text-sm font-medium transition-colors flex items-center gap-2"
                                  >
                                    {isAdding ? (
                                      <div className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin"></div>
                                    ) : (
                                      <PlusIcon />
                                    )}
                                    Add
                                  </button>
                                </div>
                              );
                            })}
                          </div>
                        </div>
                      );
                    })}
                  </div>
                )}
              </>
            )}
          </div>
        </div>
      )}

      {/* Create/Edit Modal */}
      {showModal && (
        <div className="fixed inset-0 bg-black/50 backdrop-blur-sm flex items-center justify-center z-50">
          <div className="bg-slate-800 rounded-xl border border-slate-700 p-6 w-full max-w-lg mx-4 max-h-[90vh] overflow-y-auto">
            <h2 className="text-xl font-bold mb-4">
              {editingPool ? 'Edit Pool' : 'Add Pool'}
            </h2>

            <form onSubmit={handleSubmit} className="space-y-4">
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-1">Name</label>
                  <input
                    type="text"
                    value={formData.name}
                    onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                    placeholder="2Miners KAS"
                    className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                    required
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-1">Coin</label>
                  <select
                    value={formData.coin}
                    onChange={(e) => setFormData({ ...formData, coin: e.target.value })}
                    className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                    required
                  >
                    <option value="">Select coin...</option>
                    {gpuCoins.length > 0 && (
                      <optgroup label="GPU Coins">
                        {gpuCoins.map(c => (
                          <option key={c.id} value={c.ticker}>{c.ticker} - {c.name}</option>
                        ))}
                      </optgroup>
                    )}
                    {cpuCoins.length > 0 && (
                      <optgroup label="CPU Coins">
                        {cpuCoins.map(c => (
                          <option key={c.id} value={c.ticker}>{c.ticker} - {c.name}</option>
                        ))}
                      </optgroup>
                    )}
                  </select>
                </div>
              </div>

              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">Pool URL (Primary)</label>
                <input
                  type="text"
                  value={formData.url}
                  onChange={(e) => setFormData({ ...formData, url: e.target.value })}
                  placeholder="stratum+tcp://kas.2miners.com:5555"
                  className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500 font-mono text-sm"
                  required
                />
              </div>

              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">Backup URL 1 (optional)</label>
                <input
                  type="text"
                  value={formData.url2}
                  onChange={(e) => setFormData({ ...formData, url2: e.target.value })}
                  placeholder="stratum+ssl://kas.2miners.com:15555"
                  className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500 font-mono text-sm"
                />
              </div>

              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">Backup URL 2 (optional)</label>
                <input
                  type="text"
                  value={formData.url3}
                  onChange={(e) => setFormData({ ...formData, url3: e.target.value })}
                  placeholder=""
                  className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500 font-mono text-sm"
                />
              </div>

              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-1">Username Template</label>
                  <input
                    type="text"
                    value={formData.user}
                    onChange={(e) => setFormData({ ...formData, user: e.target.value })}
                    placeholder="%WALLET%.%WORKER%"
                    className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500 font-mono text-sm"
                  />
                  <p className="text-xs text-slate-500 mt-1">Use %WALLET%, %WORKER%</p>
                </div>
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-1">Password</label>
                  <input
                    type="text"
                    value={formData.pass}
                    onChange={(e) => setFormData({ ...formData, pass: e.target.value })}
                    placeholder="x"
                    className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                  />
                </div>
              </div>

              <div className="flex gap-3 pt-4">
                <button
                  type="button"
                  onClick={() => setShowModal(false)}
                  className="flex-1 px-4 py-2.5 bg-slate-700 hover:bg-slate-600 rounded-lg font-medium transition-colors"
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  className="flex-1 px-4 py-2.5 bg-blox-600 hover:bg-blox-700 rounded-lg font-medium transition-colors"
                >
                  {editingPool ? 'Save Changes' : 'Add Pool'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}
