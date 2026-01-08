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

interface Wallet {
  id: string;
  name: string;
  coin: string;
  address: string;
  source: string | null;
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

const WalletIcon = () => (
  <svg className="w-8 h-8 text-slate-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M3 10h18M7 15h1m4 0h1m-7 4h12a3 3 0 003-3V8a3 3 0 00-3-3H6a3 3 0 00-3 3v8a3 3 0 003 3z" />
  </svg>
);

const CheckIcon = () => (
  <svg className="w-4 h-4 text-green-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
  </svg>
);

const XIcon = () => (
  <svg className="w-4 h-4 text-red-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
  </svg>
);

export default function WalletsPage() {
  const [wallets, setWallets] = useState<Wallet[]>([]);
  const [farms, setFarms] = useState<Farm[]>([]);
  const [coins, setCoins] = useState<Coin[]>([]);
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);
  const [editingWallet, setEditingWallet] = useState<Wallet | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [addressValidation, setAddressValidation] = useState<{ valid: boolean; error?: string } | null>(null);
  const [validating, setValidating] = useState(false);

  // Form state
  const [formData, setFormData] = useState({
    name: '',
    coin: '',
    address: '',
    source: '',
    farmId: '',
  });

  // Debounced address validation
  const validateAddress = useCallback(async (ticker: string, address: string) => {
    if (!ticker || !address || address.length < 10) {
      setAddressValidation(null);
      return;
    }

    setValidating(true);
    try {
      const res = await fetch(`${getApiUrl()}/api/coins/validate-wallet`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ ticker, address }),
      });
      const data = await res.json();
      setAddressValidation(data);
    } catch {
      setAddressValidation(null);
    } finally {
      setValidating(false);
    }
  }, []);

  // Debounce address validation
  useEffect(() => {
    const timer = setTimeout(() => {
      validateAddress(formData.coin, formData.address);
    }, 500);
    return () => clearTimeout(timer);
  }, [formData.coin, formData.address, validateAddress]);

  useEffect(() => {
    fetchData();
  }, []);

  async function fetchData() {
    try {
      // Fetch coins
      const coinsRes = await fetch(`${getApiUrl()}/api/coins`, {
        credentials: 'include',
      });
      if (coinsRes.ok) {
        const coinsData = await coinsRes.json();
        setCoins(coinsData);
      }

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
      
      // Fetch wallets
      await fetchWallets();
    } catch {
      setError('Failed to load data');
    }
  }

  async function fetchWallets() {
    try {
      const res = await fetch(`${getApiUrl()}/api/wallets`, {
        credentials: 'include',
      });
      if (!res.ok) return;
      const data = await res.json();
      if (Array.isArray(data)) {
        setWallets(data);
      }
    } catch {
      setError('Failed to fetch wallets');
    } finally {
      setLoading(false);
    }
  }

  function openCreateModal() {
    setEditingWallet(null);
    setFormData({ name: '', coin: '', address: '', source: '', farmId: farms[0]?.id || '' });
    setAddressValidation(null);
    setShowModal(true);
  }

  function openEditModal(wallet: Wallet) {
    setEditingWallet(wallet);
    setFormData({
      name: wallet.name,
      coin: wallet.coin,
      address: wallet.address,
      source: wallet.source || '',
      farmId: wallet.farmId,
    });
    setAddressValidation(null);
    setShowModal(true);
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setError(null);

    if (!formData.farmId) {
      setError('No farm available. Please refresh the page.');
      return;
    }

    try {
      const url = editingWallet
        ? `${getApiUrl()}/api/wallets/${editingWallet.id}`
        : `${getApiUrl()}/api/wallets`;
      
      const res = await fetch(url, {
        method: editingWallet ? 'PATCH' : 'POST',
        headers: { 
          'Content-Type': 'application/json',
          'X-CSRF-Token': getCsrfToken() || '',
        },
        credentials: 'include',
        body: JSON.stringify(formData),
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.details || data.error || data.message || 'Failed to save wallet');
      }

      setShowModal(false);
      fetchWallets();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save wallet');
    }
  }

  async function handleDelete(wallet: Wallet) {
    if (!confirm(`Delete wallet "${wallet.name}"?`)) return;

    try {
      const res = await fetch(`${getApiUrl()}/api/wallets/${wallet.id}`, {
        method: 'DELETE',
        headers: {
          'X-CSRF-Token': getCsrfToken() || '',
        },
        credentials: 'include',
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || data.message || 'Failed to delete wallet');
      }

      fetchWallets();
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to delete wallet');
    }
  }

  // Get coin info for display
  function getCoinInfo(ticker: string): Coin | undefined {
    return coins.find(c => c.ticker === ticker.toUpperCase());
  }

  return (
    <div>
      {/* Header */}
      <div className="flex justify-between items-center mb-6">
        <div>
          <h1 className="text-2xl font-bold">Wallets</h1>
          <p className="text-slate-400 text-sm mt-1">Manage your mining wallet addresses</p>
        </div>
        <button
          onClick={openCreateModal}
          className="flex items-center gap-2 px-4 py-2.5 bg-gradient-to-r from-blox-500 to-blox-600 hover:from-blox-600 hover:to-blox-700 rounded-lg text-white font-medium transition-all duration-200 shadow-lg shadow-blox-500/25"
        >
          <PlusIcon /> Add Wallet
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

      {/* Wallets List */}
      {loading ? (
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-12 text-center">
          <div className="inline-block w-8 h-8 border-2 border-slate-600 border-t-blox-400 rounded-full animate-spin"></div>
          <p className="text-slate-400 mt-3">Loading wallets...</p>
        </div>
      ) : wallets.length === 0 ? (
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-12 text-center">
          <div className="w-16 h-16 mx-auto mb-4 rounded-full bg-slate-700/50 flex items-center justify-center">
            <WalletIcon />
          </div>
          <p className="text-slate-400 mb-4">No wallets configured yet</p>
          <button
            onClick={openCreateModal}
            className="inline-block px-4 py-2 bg-blox-600 hover:bg-blox-700 rounded-lg text-white font-medium transition-colors"
          >
            Add Your First Wallet
          </button>
        </div>
      ) : (
        <div className="grid gap-4">
          {wallets.map((wallet) => {
            const coinInfo = getCoinInfo(wallet.coin);
            return (
              <div
                key={wallet.id}
                className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-5 hover:border-slate-600/50 transition-all"
              >
                <div className="flex items-start justify-between">
                  <div className="flex items-center gap-4">
                    <div className="w-12 h-12 rounded-xl bg-slate-700 flex items-center justify-center overflow-hidden">
                      {coinInfo?.logoPath ? (
                        <Image
                          src={coinInfo.logoPath}
                          alt={wallet.coin}
                          width={48}
                          height={48}
                          className="w-full h-full object-cover"
                        />
                      ) : (
                        <span className="font-bold text-white text-sm">{wallet.coin.slice(0, 3)}</span>
                      )}
                    </div>
                    <div>
                      <div className="flex items-center gap-2">
                        <h3 className="font-semibold text-lg">{wallet.name}</h3>
                        <span className="px-2 py-0.5 bg-slate-700 rounded text-xs text-slate-300">{wallet.coin}</span>
                        {coinInfo && (
                          <span className={`px-2 py-0.5 rounded text-xs ${coinInfo.type === 'GPU' ? 'bg-green-500/20 text-green-400' : 'bg-blue-500/20 text-blue-400'}`}>
                            {coinInfo.type}
                          </span>
                        )}
                      </div>
                      <p className="text-sm text-slate-400 font-mono mt-1 break-all">{wallet.address}</p>
                      {wallet.source && (
                        <p className="text-xs text-slate-500 mt-1">Source: {wallet.source}</p>
                      )}
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    {wallet._count && wallet._count.flightSheets > 0 && (
                      <span className="text-xs text-slate-500 mr-2">
                        {wallet._count.flightSheets} flight sheet{wallet._count.flightSheets > 1 ? 's' : ''}
                      </span>
                    )}
                    <button
                      onClick={() => openEditModal(wallet)}
                      className="p-2 rounded-lg text-slate-400 hover:text-white hover:bg-slate-700 transition-colors"
                    >
                      <EditIcon />
                    </button>
                    <button
                      onClick={() => handleDelete(wallet)}
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

      {/* Modal */}
      {showModal && (
        <div className="fixed inset-0 bg-black/50 backdrop-blur-sm flex items-center justify-center z-50">
          <div className="bg-slate-800 rounded-xl border border-slate-700 p-6 w-full max-w-md mx-4">
            <h2 className="text-xl font-bold mb-4">
              {editingWallet ? 'Edit Wallet' : 'Add Wallet'}
            </h2>

            <form onSubmit={handleSubmit} className="space-y-4">
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">Name</label>
                <input
                  type="text"
                  value={formData.name}
                  onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                  placeholder="My Mining Wallet"
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
                  <option value="">Select a coin...</option>
                  <optgroup label="GPU Coins">
                    {coins.filter(c => c.type === 'GPU').map(coin => (
                      <option key={coin.id} value={coin.ticker}>
                        {coin.ticker} - {coin.name} ({coin.algorithm})
                      </option>
                    ))}
                  </optgroup>
                  <optgroup label="CPU Coins">
                    {coins.filter(c => c.type === 'CPU').map(coin => (
                      <option key={coin.id} value={coin.ticker}>
                        {coin.ticker} - {coin.name} ({coin.algorithm})
                      </option>
                    ))}
                  </optgroup>
                </select>
              </div>

              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">Address</label>
                <div className="relative">
                  <input
                    type="text"
                    value={formData.address}
                    onChange={(e) => setFormData({ ...formData, address: e.target.value })}
                    placeholder="Wallet address..."
                    className={`w-full px-4 py-2.5 pr-10 bg-slate-900/50 border rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500 font-mono text-sm ${
                      addressValidation?.valid === false 
                        ? 'border-red-500' 
                        : addressValidation?.valid === true 
                          ? 'border-green-500' 
                          : 'border-slate-700'
                    }`}
                    required
                  />
                  <div className="absolute right-3 top-1/2 -translate-y-1/2">
                    {validating ? (
                      <div className="w-4 h-4 border-2 border-slate-500 border-t-blox-400 rounded-full animate-spin" />
                    ) : addressValidation?.valid === true ? (
                      <CheckIcon />
                    ) : addressValidation?.valid === false ? (
                      <XIcon />
                    ) : null}
                  </div>
                </div>
                {addressValidation?.valid === false && (
                  <p className="mt-1 text-xs text-red-400">{addressValidation.error}</p>
                )}
                {addressValidation?.valid === true && (
                  <p className="mt-1 text-xs text-green-400">Valid address format</p>
                )}
              </div>

              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">Source (optional)</label>
                <input
                  type="text"
                  value={formData.source}
                  onChange={(e) => setFormData({ ...formData, source: e.target.value })}
                  placeholder="Binance, Coinbase, Personal, etc."
                  className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
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
                  disabled={addressValidation?.valid === false}
                  className="flex-1 px-4 py-2.5 bg-blox-600 hover:bg-blox-700 rounded-lg font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
                >
                  {editingWallet ? 'Save Changes' : 'Add Wallet'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}
