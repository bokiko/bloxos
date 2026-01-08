'use client';

import { useEffect, useState } from 'react';
import Image from 'next/image';

const getApiUrl = () => {
  if (typeof window === 'undefined') return 'http://localhost:3001';
  return `http://${window.location.hostname}:3001`;
};

// Helper to get CSRF token from cookie
function getCsrfToken(): string {
  const match = document.cookie.match(/(?:^|;\s*)csrf_token=([^;]*)/);
  return match ? decodeURIComponent(match[1]) : '';
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
}

interface FlightSheetTemplate {
  id: string;
  name: string;
  minerName: string;
  gpuType: string;
  extraArgs: string | null;
  recommended: boolean;
  coin: {
    ticker: string;
    name: string;
    algorithm: string;
    logoPath: string | null;
  };
  poolPreset: PoolPreset | null;
}

interface Wallet {
  id: string;
  name: string;
  coin: string;
  address: string;
}

interface Pool {
  id: string;
  name: string;
  coin: string;
  url: string;
}

interface Miner {
  id: string;
  name: string;
  version: string;
  algo: string;
}

interface FlightSheet {
  id: string;
  name: string;
  coin: string;
  wallet: Wallet;
  pool: Pool;
  miner: Miner;
  extraArgs: string | null;
  createdAt: string;
  _count: {
    rigs: number;
  };
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

const DuplicateIcon = () => (
  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M8 16H6a2 2 0 01-2-2V6a2 2 0 012-2h8a2 2 0 012 2v2m-6 12h8a2 2 0 002-2v-8a2 2 0 00-2-2h-8a2 2 0 00-2 2v8a2 2 0 002 2z" />
  </svg>
);

const FlightSheetIcon = () => (
  <svg className="w-8 h-8 text-slate-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
  </svg>
);

const StarIcon = () => (
  <svg className="w-4 h-4" fill="currentColor" viewBox="0 0 20 20">
    <path d="M9.049 2.927c.3-.921 1.603-.921 1.902 0l1.07 3.292a1 1 0 00.95.69h3.462c.969 0 1.371 1.24.588 1.81l-2.8 2.034a1 1 0 00-.364 1.118l1.07 3.292c.3.921-.755 1.688-1.54 1.118l-2.8-2.034a1 1 0 00-1.175 0l-2.8 2.034c-.784.57-1.838-.197-1.539-1.118l1.07-3.292a1 1 0 00-.364-1.118L2.98 8.72c-.783-.57-.38-1.81.588-1.81h3.461a1 1 0 00.951-.69l1.07-3.292z" />
  </svg>
);

const LightningIcon = () => (
  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 10V3L4 14h7v7l9-11h-7z" />
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
  DEFAULT: 'bg-slate-500',
};

function getCoinColor(coin: string) {
  return coinColors[coin.toUpperCase()] || coinColors.DEFAULT;
}

export default function FlightSheetsPage() {
  const [flightSheets, setFlightSheets] = useState<FlightSheet[]>([]);
  const [wallets, setWallets] = useState<Wallet[]>([]);
  const [pools, setPools] = useState<Pool[]>([]);
  const [miners, setMiners] = useState<Miner[]>([]);
  const [farms, setFarms] = useState<Farm[]>([]);
  const [coins, setCoins] = useState<Coin[]>([]);
  const [poolPresets, setPoolPresets] = useState<PoolPreset[]>([]);
  const [templates, setTemplates] = useState<FlightSheetTemplate[]>([]);
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);
  const [showTemplates, setShowTemplates] = useState(false);
  const [editingFlightSheet, setEditingFlightSheet] = useState<FlightSheet | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [usePoolPreset, setUsePoolPreset] = useState(true);

  // Form state
  const [formData, setFormData] = useState({
    name: '',
    coin: '',
    walletId: '',
    poolId: '',
    poolPresetId: '',
    minerId: '',
    extraArgs: '',
    farmId: '',
  });

  useEffect(() => {
    fetchData();
  }, []);

  async function fetchData() {
    try {
      const [fsRes, walletsRes, poolsRes, minersRes, farmsRes, coinsRes, templatesRes] = await Promise.all([
        fetch(`${getApiUrl()}/api/flight-sheets`, { credentials: 'include' }),
        fetch(`${getApiUrl()}/api/wallets`, { credentials: 'include' }),
        fetch(`${getApiUrl()}/api/pools`, { credentials: 'include' }),
        fetch(`${getApiUrl()}/api/miners`, { credentials: 'include' }),
        fetch(`${getApiUrl()}/api/auth/me`, { credentials: 'include' }),
        fetch(`${getApiUrl()}/api/coins`, { credentials: 'include' }),
        fetch(`${getApiUrl()}/api/templates/recommended`, { credentials: 'include' }),
      ]);

      if (fsRes.ok) {
        const fsData = await fsRes.json();
        if (Array.isArray(fsData)) setFlightSheets(fsData);
      }
      if (walletsRes.ok) {
        const walletsData = await walletsRes.json();
        if (Array.isArray(walletsData)) setWallets(walletsData);
      }
      if (poolsRes.ok) {
        const poolsData = await poolsRes.json();
        if (Array.isArray(poolsData)) setPools(poolsData);
      }
      if (minersRes.ok) {
        const minersData = await minersRes.json();
        if (Array.isArray(minersData)) setMiners(minersData);
      }
      if (farmsRes.ok) {
        const userData = await farmsRes.json();
        if (userData.farms && Array.isArray(userData.farms)) {
          setFarms(userData.farms);
        }
      }
      if (coinsRes.ok) {
        const coinsData = await coinsRes.json();
        if (Array.isArray(coinsData)) setCoins(coinsData);
      }
      if (templatesRes.ok) {
        const templatesData = await templatesRes.json();
        if (Array.isArray(templatesData)) setTemplates(templatesData);
      }
    } catch (err) {
      setError('Failed to fetch data');
    } finally {
      setLoading(false);
    }
  }

  // Fetch pool presets when coin changes
  async function fetchPoolPresets(ticker: string) {
    if (!ticker) {
      setPoolPresets([]);
      return;
    }
    try {
      const res = await fetch(`${getApiUrl()}/api/coins/${ticker}/pools`, { credentials: 'include' });
      if (res.ok) {
        const data = await res.json();
        if (Array.isArray(data)) setPoolPresets(data);
      }
    } catch (err) {
      console.error('Failed to fetch pool presets');
    }
  }

  function handleCoinChange(ticker: string) {
    setFormData({ 
      ...formData, 
      coin: ticker, 
      walletId: '', 
      poolId: '', 
      poolPresetId: '',
    });
    fetchPoolPresets(ticker);
  }

  function openCreateModal() {
    setEditingFlightSheet(null);
    setFormData({ name: '', coin: '', walletId: '', poolId: '', poolPresetId: '', minerId: '', extraArgs: '', farmId: farms[0]?.id || '' });
    setPoolPresets([]);
    setUsePoolPreset(true);
    setShowModal(true);
  }

  function openEditModal(fs: FlightSheet) {
    setEditingFlightSheet(fs);
    setFormData({
      name: fs.name,
      coin: fs.coin,
      walletId: fs.wallet.id,
      poolId: fs.pool.id,
      poolPresetId: '',
      minerId: fs.miner.id,
      extraArgs: fs.extraArgs || '',
      farmId: farms[0]?.id || '',
    });
    setUsePoolPreset(false); // When editing, use existing pool
    fetchPoolPresets(fs.coin);
    setShowModal(true);
  }

  function applyTemplate(template: FlightSheetTemplate) {
    const miner = miners.find(m => m.name.toLowerCase() === template.minerName.toLowerCase());
    setFormData({
      ...formData,
      name: `${template.coin.ticker} - ${template.gpuType}`,
      coin: template.coin.ticker,
      minerId: miner?.id || '',
      extraArgs: template.extraArgs || '',
      poolPresetId: template.poolPreset?.id || '',
    });
    fetchPoolPresets(template.coin.ticker);
    setShowTemplates(false);
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);

    try {
      // If using pool preset, we need to first create a pool from it or use poolPresetId
      let poolIdToUse = formData.poolId;
      
      // If using a pool preset for a new flight sheet, create a pool entry first
      if (usePoolPreset && formData.poolPresetId && !editingFlightSheet) {
        const preset = poolPresets.find(p => p.id === formData.poolPresetId);
        if (preset) {
          // Create a pool from the preset
          const poolRes = await fetch(`${getApiUrl()}/api/pools`, {
            method: 'POST',
            headers: { 
              'Content-Type': 'application/json',
              'x-csrf-token': getCsrfToken(),
            },
            credentials: 'include',
            body: JSON.stringify({
              name: `${preset.name} (${preset.region})`,
              coin: formData.coin,
              url: `stratum+tcp://${preset.host}:${preset.port}`,
              farmId: formData.farmId,
            }),
          });
          if (poolRes.ok) {
            const newPool = await poolRes.json();
            poolIdToUse = newPool.id;
          } else {
            throw new Error('Failed to create pool from preset');
          }
        }
      }

      const url = editingFlightSheet
        ? `${getApiUrl()}/api/flight-sheets/${editingFlightSheet.id}`
        : `${getApiUrl()}/api/flight-sheets`;
      
      // For updates, don't send farmId
      const submitData = editingFlightSheet
        ? { name: formData.name, coin: formData.coin, walletId: formData.walletId, poolId: poolIdToUse, minerId: formData.minerId, extraArgs: formData.extraArgs || undefined }
        : { ...formData, poolId: poolIdToUse, extraArgs: formData.extraArgs || undefined };
      
      // Remove poolPresetId from submit data
      const { poolPresetId, ...dataToSubmit } = submitData as typeof submitData & { poolPresetId?: string };
      
      const res = await fetch(url, {
        method: editingFlightSheet ? 'PATCH' : 'POST',
        headers: { 
          'Content-Type': 'application/json',
          'x-csrf-token': getCsrfToken(),
        },
        credentials: 'include',
        body: JSON.stringify(dataToSubmit),
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.message || data.error || 'Failed to save flight sheet');
      }

      setShowModal(false);
      fetchData();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save flight sheet');
    }
  }

  async function handleDelete(fs: FlightSheet) {
    if (!confirm(`Delete flight sheet "${fs.name}"?`)) return;

    try {
      const res = await fetch(`${getApiUrl()}/api/flight-sheets/${fs.id}`, {
        method: 'DELETE',
        headers: {
          'x-csrf-token': getCsrfToken(),
        },
        credentials: 'include',
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.message || data.error || 'Failed to delete flight sheet');
      }

      fetchData();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete flight sheet');
    }
  }

  function handleDuplicate(fs: FlightSheet) {
    setEditingFlightSheet(null);
    setFormData({
      name: `${fs.name} (Copy)`,
      coin: fs.coin,
      walletId: fs.wallet.id,
      poolId: fs.pool.id,
      poolPresetId: '',
      minerId: fs.miner.id,
      extraArgs: fs.extraArgs || '',
      farmId: farms[0]?.id || '',
    });
    setUsePoolPreset(false);
    fetchPoolPresets(fs.coin);
    setShowModal(true);
  }

  // Filter options by selected coin
  const filteredWallets = formData.coin
    ? wallets.filter(w => w.coin.toUpperCase() === formData.coin.toUpperCase())
    : wallets;
  const filteredPools = formData.coin
    ? pools.filter(p => p.coin.toUpperCase() === formData.coin.toUpperCase())
    : pools;
  
  // Group coins by type
  const gpuCoins = coins.filter(c => c.type === 'GPU');
  const cpuCoins = coins.filter(c => c.type === 'CPU');
  
  // Filter templates by coin if selected
  const filteredTemplates = formData.coin
    ? templates.filter(t => t.coin.ticker === formData.coin)
    : templates;

  return (
    <div>
      {/* Header */}
      <div className="flex justify-between items-center mb-6">
        <div>
          <h1 className="text-2xl font-bold">Flight Sheets</h1>
          <p className="text-slate-400 text-sm mt-1">Configure miner + pool + wallet combinations</p>
        </div>
        <div className="flex items-center gap-3">
          <button
            onClick={() => setShowTemplates(true)}
            className="flex items-center gap-2 px-4 py-2.5 bg-purple-500/20 hover:bg-purple-500/30 text-purple-400 rounded-lg font-medium transition-all duration-200"
          >
            <LightningIcon /> Quick Setup
          </button>
          <button
            onClick={openCreateModal}
            className="flex items-center gap-2 px-4 py-2.5 bg-gradient-to-r from-blox-500 to-blox-600 hover:from-blox-600 hover:to-blox-700 rounded-lg text-white font-medium transition-all duration-200 shadow-lg shadow-blox-500/25"
          >
            <PlusIcon /> Add Flight Sheet
          </button>
        </div>
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

      {/* Flight Sheets List */}
      {loading ? (
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-12 text-center">
          <div className="inline-block w-8 h-8 border-2 border-slate-600 border-t-blox-400 rounded-full animate-spin"></div>
          <p className="text-slate-400 mt-3">Loading flight sheets...</p>
        </div>
      ) : flightSheets.length === 0 ? (
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-12 text-center">
          <div className="w-16 h-16 mx-auto mb-4 rounded-full bg-slate-700/50 flex items-center justify-center">
            <FlightSheetIcon />
          </div>
          <p className="text-slate-400 mb-4">No flight sheets configured yet</p>
          <button
            onClick={openCreateModal}
            className="inline-block px-4 py-2 bg-blox-600 hover:bg-blox-700 rounded-lg text-white font-medium transition-colors"
          >
            Create Your First Flight Sheet
          </button>
        </div>
      ) : (
        <div className="grid gap-4">
          {flightSheets.map((fs) => {
            const coinData = coins.find(c => c.ticker.toUpperCase() === fs.coin.toUpperCase());
            return (
              <div
                key={fs.id}
                className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-5 hover:border-slate-600/50 transition-all"
              >
                <div className="flex items-start justify-between">
                  <div className="flex items-center gap-4">
                    <div className={`w-14 h-14 rounded-xl ${getCoinColor(fs.coin)} flex items-center justify-center font-bold text-white text-lg overflow-hidden`}>
                      {coinData?.logoPath ? (
                        <Image
                          src={coinData.logoPath}
                          alt={fs.coin}
                          width={56}
                          height={56}
                          className="w-full h-full object-cover"
                        />
                      ) : (
                        fs.coin.slice(0, 3)
                      )}
                    </div>
                    <div>
                      <div className="flex items-center gap-2">
                        <h3 className="font-semibold text-lg">{fs.name}</h3>
                        <span className="px-2 py-0.5 bg-slate-700 rounded text-xs text-slate-300">{fs.coin}</span>
                        {coinData && (
                          <span className={`px-2 py-0.5 rounded text-xs ${coinData.type === 'GPU' ? 'bg-blue-500/20 text-blue-400' : 'bg-green-500/20 text-green-400'}`}>
                            {coinData.type}
                          </span>
                        )}
                        {fs._count.rigs > 0 && (
                          <span className="px-2 py-0.5 bg-purple-500/20 text-purple-400 rounded text-xs">
                            {fs._count.rigs} rig{fs._count.rigs > 1 ? 's' : ''}
                          </span>
                        )}
                      </div>
                      <div className="mt-2 grid grid-cols-3 gap-4 text-sm">
                        <div>
                          <span className="text-slate-500">Miner:</span>
                          <span className="text-slate-300 ml-1">{fs.miner.name} {fs.miner.version}</span>
                          <span className="text-slate-500 ml-1">({fs.miner.algo})</span>
                        </div>
                        <div>
                          <span className="text-slate-500">Pool:</span>
                          <span className="text-slate-300 ml-1">{fs.pool.name}</span>
                        </div>
                        <div>
                          <span className="text-slate-500">Wallet:</span>
                          <span className="text-slate-300 ml-1">{fs.wallet.name}</span>
                        </div>
                      </div>
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    <button
                      onClick={() => handleDuplicate(fs)}
                      className="p-2 rounded-lg text-slate-400 hover:text-blue-400 hover:bg-blue-500/10 transition-colors"
                      title="Duplicate"
                    >
                      <DuplicateIcon />
                    </button>
                    <button
                      onClick={() => openEditModal(fs)}
                      className="p-2 rounded-lg text-slate-400 hover:text-white hover:bg-slate-700 transition-colors"
                      title="Edit"
                    >
                      <EditIcon />
                    </button>
                    <button
                      onClick={() => handleDelete(fs)}
                      className="p-2 rounded-lg text-slate-400 hover:text-red-400 hover:bg-red-500/10 transition-colors"
                      title="Delete"
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

      {/* Templates Modal */}
      {showTemplates && (
        <div className="fixed inset-0 bg-black/50 backdrop-blur-sm flex items-center justify-center z-50">
          <div className="bg-slate-800 rounded-xl border border-slate-700 p-6 w-full max-w-4xl mx-4 max-h-[90vh] overflow-y-auto">
            <div className="flex items-center justify-between mb-6">
              <div>
                <h2 className="text-xl font-bold">Quick Setup Templates</h2>
                <p className="text-slate-400 text-sm mt-1">Recommended configurations for popular coins</p>
              </div>
              <button
                onClick={() => setShowTemplates(false)}
                className="p-2 hover:bg-slate-700 rounded-lg transition-colors"
              >
                <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                </svg>
              </button>
            </div>
            
            <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
              {templates.map((template) => (
                <button
                  key={template.id}
                  onClick={() => {
                    applyTemplate(template);
                    setShowModal(true);
                  }}
                  className="text-left bg-slate-900/50 border border-slate-700 rounded-xl p-4 hover:border-blox-500/50 hover:bg-slate-800/50 transition-all group"
                >
                  <div className="flex items-start gap-3">
                    <div className={`w-12 h-12 rounded-lg ${getCoinColor(template.coin.ticker)} flex items-center justify-center overflow-hidden flex-shrink-0`}>
                      {template.coin.logoPath ? (
                        <Image
                          src={template.coin.logoPath}
                          alt={template.coin.ticker}
                          width={48}
                          height={48}
                          className="w-full h-full object-cover"
                        />
                      ) : (
                        <span className="font-bold text-white">{template.coin.ticker.slice(0, 3)}</span>
                      )}
                    </div>
                    <div className="flex-1 min-w-0">
                      <div className="flex items-center gap-2 mb-1">
                        <h3 className="font-semibold truncate">{template.name}</h3>
                        {template.recommended && (
                          <span className="text-yellow-400 flex-shrink-0">
                            <StarIcon />
                          </span>
                        )}
                      </div>
                      <div className="text-xs text-slate-400 space-y-0.5">
                        <p>Miner: {template.minerName}</p>
                        <p>Algorithm: {template.coin.algorithm}</p>
                        <p>Hardware: {template.gpuType}</p>
                        {template.poolPreset && (
                          <p>Pool: {template.poolPreset.name}</p>
                        )}
                      </div>
                    </div>
                  </div>
                </button>
              ))}
            </div>

            {templates.length === 0 && (
              <div className="text-center py-12 text-slate-400">
                <p>No templates available</p>
              </div>
            )}
          </div>
        </div>
      )}

      {/* Modal */}
      {showModal && (
        <div className="fixed inset-0 bg-black/50 backdrop-blur-sm flex items-center justify-center z-50">
          <div className="bg-slate-800 rounded-xl border border-slate-700 p-6 w-full max-w-lg mx-4 max-h-[90vh] overflow-y-auto">
            <h2 className="text-xl font-bold mb-4">
              {editingFlightSheet ? 'Edit Flight Sheet' : 'Create Flight Sheet'}
            </h2>

            <form onSubmit={handleSubmit} className="space-y-4">
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-1">Name</label>
                  <input
                    type="text"
                    value={formData.name}
                    onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                    placeholder="KAS Mining"
                    className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                    required
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-1">Coin</label>
                  <select
                    value={formData.coin}
                    onChange={(e) => handleCoinChange(e.target.value)}
                    className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                    required
                  >
                    <option value="">Select a coin...</option>
                    {gpuCoins.length > 0 && (
                      <optgroup label="GPU Coins">
                        {gpuCoins.map(c => (
                          <option key={c.id} value={c.ticker}>{c.ticker} - {c.name} ({c.algorithm})</option>
                        ))}
                      </optgroup>
                    )}
                    {cpuCoins.length > 0 && (
                      <optgroup label="CPU Coins">
                        {cpuCoins.map(c => (
                          <option key={c.id} value={c.ticker}>{c.ticker} - {c.name} ({c.algorithm})</option>
                        ))}
                      </optgroup>
                    )}
                  </select>
                </div>
              </div>

              {/* Template suggestions for selected coin */}
              {formData.coin && filteredTemplates.length > 0 && !editingFlightSheet && (
                <div className="bg-purple-500/10 border border-purple-500/30 rounded-lg p-3">
                  <p className="text-xs text-purple-400 mb-2 flex items-center gap-1">
                    <LightningIcon /> Recommended setups for {formData.coin}:
                  </p>
                  <div className="flex flex-wrap gap-2">
                    {filteredTemplates.slice(0, 3).map(t => (
                      <button
                        key={t.id}
                        type="button"
                        onClick={() => applyTemplate(t)}
                        className="px-3 py-1.5 bg-purple-500/20 hover:bg-purple-500/30 text-purple-300 rounded-lg text-xs transition-colors"
                      >
                        {t.gpuType} - {t.minerName}
                      </button>
                    ))}
                  </div>
                </div>
              )}

              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">Wallet</label>
                <select
                  value={formData.walletId}
                  onChange={(e) => setFormData({ ...formData, walletId: e.target.value })}
                  className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                  required
                >
                  <option value="">Select a wallet...</option>
                  {filteredWallets.map(w => (
                    <option key={w.id} value={w.id}>{w.name} ({w.coin})</option>
                  ))}
                </select>
                {formData.coin && filteredWallets.length === 0 && (
                  <p className="text-yellow-400 text-xs mt-1">No {formData.coin} wallets found. Add one first.</p>
                )}
              </div>

              {/* Pool Selection - Toggle between preset and custom */}
              <div>
                <div className="flex items-center justify-between mb-1">
                  <label className="block text-sm font-medium text-slate-300">Pool</label>
                  {formData.coin && poolPresets.length > 0 && !editingFlightSheet && (
                    <button
                      type="button"
                      onClick={() => setUsePoolPreset(!usePoolPreset)}
                      className="text-xs text-blox-400 hover:text-blox-300"
                    >
                      {usePoolPreset ? 'Use custom pool' : 'Use pool preset'}
                    </button>
                  )}
                </div>
                
                {usePoolPreset && poolPresets.length > 0 && !editingFlightSheet ? (
                  <>
                    <select
                      value={formData.poolPresetId}
                      onChange={(e) => setFormData({ ...formData, poolPresetId: e.target.value })}
                      className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                      required
                    >
                      <option value="">Select a pool preset...</option>
                      {['US', 'EU', 'ASIA', 'GLOBAL'].map(region => {
                        const regionPools = poolPresets.filter(p => p.region === region);
                        if (regionPools.length === 0) return null;
                        return (
                          <optgroup key={region} label={region}>
                            {regionPools.map(p => (
                              <option key={p.id} value={p.id}>
                                {p.name} - {p.host}:{p.port} {p.fee ? `(${p.fee}% fee)` : ''}
                              </option>
                            ))}
                          </optgroup>
                        );
                      })}
                    </select>
                    {formData.poolPresetId && (
                      <p className="text-xs text-slate-400 mt-1">
                        A pool entry will be created from this preset
                      </p>
                    )}
                  </>
                ) : (
                  <>
                    <select
                      value={formData.poolId}
                      onChange={(e) => setFormData({ ...formData, poolId: e.target.value })}
                      className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                      required
                    >
                      <option value="">Select a pool...</option>
                      {filteredPools.map(p => (
                        <option key={p.id} value={p.id}>{p.name} ({p.coin})</option>
                      ))}
                    </select>
                    {formData.coin && filteredPools.length === 0 && poolPresets.length === 0 && (
                      <p className="text-yellow-400 text-xs mt-1">No {formData.coin} pools found. Add one first.</p>
                    )}
                  </>
                )}
              </div>

              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">Miner</label>
                <select
                  value={formData.minerId}
                  onChange={(e) => setFormData({ ...formData, minerId: e.target.value })}
                  className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                  required
                >
                  <option value="">Select a miner...</option>
                  {miners.map(m => (
                    <option key={m.id} value={m.id}>{m.name} {m.version} ({m.algo})</option>
                  ))}
                </select>
              </div>

              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">Extra Arguments (optional)</label>
                <input
                  type="text"
                  value={formData.extraArgs}
                  onChange={(e) => setFormData({ ...formData, extraArgs: e.target.value })}
                  placeholder="--intensity 22"
                  className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500 font-mono text-sm"
                />
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
                  {editingFlightSheet ? 'Save Changes' : 'Create Flight Sheet'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}
