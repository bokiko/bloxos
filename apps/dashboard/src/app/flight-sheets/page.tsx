'use client';

import { useEffect, useState } from 'react';

const getApiUrl = () => {
  if (typeof window === 'undefined') return 'http://localhost:3001';
  return `http://${window.location.hostname}:3001`;
};

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

const FlightSheetIcon = () => (
  <svg className="w-8 h-8 text-slate-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9 12h6m-6 4h6m2 5H7a2 2 0 01-2-2V5a2 2 0 012-2h5.586a1 1 0 01.707.293l5.414 5.414a1 1 0 01.293.707V19a2 2 0 01-2 2z" />
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
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);
  const [editingFlightSheet, setEditingFlightSheet] = useState<FlightSheet | null>(null);
  const [error, setError] = useState<string | null>(null);

  // Form state
  const [formData, setFormData] = useState({
    name: '',
    coin: '',
    walletId: '',
    poolId: '',
    minerId: '',
    extraArgs: '',
  });

  useEffect(() => {
    fetchData();
  }, []);

  async function fetchData() {
    try {
      const [fsRes, walletsRes, poolsRes, minersRes] = await Promise.all([
        fetch(`${getApiUrl()}/api/flight-sheets`, { credentials: 'include' }),
        fetch(`${getApiUrl()}/api/wallets`, { credentials: 'include' }),
        fetch(`${getApiUrl()}/api/pools`, { credentials: 'include' }),
        fetch(`${getApiUrl()}/api/miners`, { credentials: 'include' }),
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
    } catch (err) {
      setError('Failed to fetch data');
    } finally {
      setLoading(false);
    }
  }

  function openCreateModal() {
    setEditingFlightSheet(null);
    setFormData({ name: '', coin: '', walletId: '', poolId: '', minerId: '', extraArgs: '' });
    setShowModal(true);
  }

  function openEditModal(fs: FlightSheet) {
    setEditingFlightSheet(fs);
    setFormData({
      name: fs.name,
      coin: fs.coin,
      walletId: fs.wallet.id,
      poolId: fs.pool.id,
      minerId: fs.miner.id,
      extraArgs: fs.extraArgs || '',
    });
    setShowModal(true);
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);

    try {
      const url = editingFlightSheet
        ? `${getApiUrl()}/api/flight-sheets/${editingFlightSheet.id}`
        : `${getApiUrl()}/api/flight-sheets`;
      
      const res = await fetch(url, {
        method: editingFlightSheet ? 'PATCH' : 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify(formData),
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.message || 'Failed to save flight sheet');
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
        credentials: 'include',
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.message || 'Failed to delete flight sheet');
      }

      fetchData();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete flight sheet');
    }
  }

  // Filter options by selected coin
  const filteredWallets = formData.coin
    ? wallets.filter(w => w.coin === formData.coin)
    : wallets;
  const filteredPools = formData.coin
    ? pools.filter(p => p.coin === formData.coin)
    : pools;

  return (
    <div>
      {/* Header */}
      <div className="flex justify-between items-center mb-6">
        <div>
          <h1 className="text-2xl font-bold">Flight Sheets</h1>
          <p className="text-slate-400 text-sm mt-1">Configure miner + pool + wallet combinations</p>
        </div>
        <button
          onClick={openCreateModal}
          className="flex items-center gap-2 px-4 py-2.5 bg-gradient-to-r from-blox-500 to-blox-600 hover:from-blox-600 hover:to-blox-700 rounded-lg text-white font-medium transition-all duration-200 shadow-lg shadow-blox-500/25"
        >
          <PlusIcon /> Add Flight Sheet
        </button>
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
          {flightSheets.map((fs) => (
            <div
              key={fs.id}
              className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-5 hover:border-slate-600/50 transition-all"
            >
              <div className="flex items-start justify-between">
                <div className="flex items-center gap-4">
                  <div className={`w-14 h-14 rounded-xl ${getCoinColor(fs.coin)} flex items-center justify-center font-bold text-white text-lg`}>
                    {fs.coin.slice(0, 3)}
                  </div>
                  <div>
                    <div className="flex items-center gap-2">
                      <h3 className="font-semibold text-lg">{fs.name}</h3>
                      <span className="px-2 py-0.5 bg-slate-700 rounded text-xs text-slate-300">{fs.coin}</span>
                      {fs._count.rigs > 0 && (
                        <span className="px-2 py-0.5 bg-green-500/20 text-green-400 rounded text-xs">
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
                    onClick={() => openEditModal(fs)}
                    className="p-2 rounded-lg text-slate-400 hover:text-white hover:bg-slate-700 transition-colors"
                  >
                    <EditIcon />
                  </button>
                  <button
                    onClick={() => handleDelete(fs)}
                    className="p-2 rounded-lg text-slate-400 hover:text-red-400 hover:bg-red-500/10 transition-colors"
                  >
                    <TrashIcon />
                  </button>
                </div>
              </div>
            </div>
          ))}
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
                    placeholder="ETH Mining"
                    className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                    required
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-1">Coin</label>
                  <input
                    type="text"
                    value={formData.coin}
                    onChange={(e) => setFormData({ ...formData, coin: e.target.value.toUpperCase(), walletId: '', poolId: '' })}
                    placeholder="ETH"
                    className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500 uppercase"
                    required
                  />
                </div>
              </div>

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

              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">Pool</label>
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
                {formData.coin && filteredPools.length === 0 && (
                  <p className="text-yellow-400 text-xs mt-1">No {formData.coin} pools found. Add one first.</p>
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
