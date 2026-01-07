'use client';

import { useState, useEffect } from 'react';
import { useRouter } from 'next/navigation';
import Link from 'next/link';

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

interface Farm {
  id: string;
  name: string;
}

export default function AddRigPage() {
  const router = useRouter();
  const [loading, setLoading] = useState(false);
  const [testing, setTesting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<{ success: boolean; message: string } | null>(null);
  const [farms, setFarms] = useState<Farm[]>([]);
  const [loadingFarms, setLoadingFarms] = useState(true);

  const [formData, setFormData] = useState({
    name: '',
    farmId: '',
    host: '',
    port: '22',
    username: 'root',
    password: '',
    privateKey: '',
    authType: 'password' as 'password' | 'key',
  });

  // Fetch farms on mount
  useEffect(() => {
    async function fetchFarms() {
      try {
        // Get user data with farms (auto-creates default farm if none)
        const res = await fetch(`${getApiUrl()}/api/auth/me`, {
          credentials: 'include',
        });
        
        if (res.ok) {
          const data = await res.json();
          if (data.farms && data.farms.length > 0) {
            setFarms(data.farms);
            setFormData(prev => ({ ...prev, farmId: data.farms[0].id }));
          }
        } else {
          setError('Please log in to add rigs');
        }
      } catch (err) {
        console.error('Failed to fetch farms:', err);
        setError('Failed to load user data');
      } finally {
        setLoadingFarms(false);
      }
    }
    fetchFarms();
  }, []);

  async function handleTestConnection() {
    setTesting(true);
    setError(null);
    setTestResult(null);

    try {
      const res = await fetch(`${getApiUrl()}/api/ssh/test`, {
        method: 'POST',
        headers: { 
          'Content-Type': 'application/json',
          'X-CSRF-Token': getCsrfToken() || '',
        },
        credentials: 'include',
        body: JSON.stringify({
          host: formData.host,
          port: parseInt(formData.port),
          username: formData.username,
          ...(formData.authType === 'password'
            ? { password: formData.password }
            : { privateKey: formData.privateKey }),
        }),
      });

      const data = await res.json();

      if (res.ok && data.success) {
        setTestResult({ success: true, message: 'Connection successful!' });
      } else {
        setTestResult({ success: false, message: data.error || data.message || 'Connection failed' });
      }
    } catch (err) {
      setTestResult({ success: false, message: 'Failed to test connection' });
    } finally {
      setTesting(false);
    }
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setLoading(true);
    setError(null);

    if (!formData.farmId) {
      setError('No farm available. Please refresh the page.');
      setLoading(false);
      return;
    }

    try {
      const res = await fetch(`${getApiUrl()}/api/ssh/add-rig`, {
        method: 'POST',
        headers: { 
          'Content-Type': 'application/json',
          'X-CSRF-Token': getCsrfToken() || '',
        },
        credentials: 'include',
        body: JSON.stringify({
          name: formData.name,
          farmId: formData.farmId,
          host: formData.host,
          port: parseInt(formData.port),
          username: formData.username,
          ...(formData.authType === 'password'
            ? { password: formData.password }
            : { privateKey: formData.privateKey }),
        }),
      });

      const data = await res.json();

      if (res.ok) {
        router.push('/rigs');
      } else {
        setError(data.error || data.message || 'Failed to add rig');
      }
    } catch (err) {
      setError('Failed to add rig. Please check your connection.');
    } finally {
      setLoading(false);
    }
  }

  const isFormValid = Boolean(
    formData.name && 
    formData.host && 
    formData.username && 
    formData.farmId &&
    (formData.authType === 'password' ? formData.password : formData.privateKey)
  );

  if (loadingFarms) {
    return (
      <div className="flex items-center justify-center min-h-[400px]">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blox-500"></div>
      </div>
    );
  }

  return (
    <div className="max-w-2xl mx-auto">
      {/* Header */}
      <div className="mb-8">
        <div className="flex items-center gap-2 text-sm text-slate-400 mb-2">
          <Link href="/rigs" className="hover:text-white transition-colors">Rigs</Link>
          <span>/</span>
          <span>Add New</span>
        </div>
        <h1 className="text-2xl font-bold">Add New Rig</h1>
        <p className="text-slate-400 mt-1">Connect to a mining rig via SSH</p>
      </div>

      <form onSubmit={handleSubmit} className="space-y-6">
        {/* Error Alert */}
        {error && (
          <div className="flex items-center gap-3 p-4 bg-red-500/10 border border-red-500/30 rounded-xl text-red-400">
            <svg className="w-5 h-5 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
            </svg>
            <span>{error}</span>
          </div>
        )}

        {/* Test Result */}
        {testResult && (
          <div className={`flex items-center gap-3 p-4 rounded-xl border ${
            testResult.success 
              ? 'bg-green-500/10 border-green-500/30 text-green-400' 
              : 'bg-red-500/10 border-red-500/30 text-red-400'
          }`}>
            {testResult.success ? (
              <svg className="w-5 h-5 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 12l2 2 4-4m6 2a9 9 0 11-18 0 9 9 0 0118 0z" />
              </svg>
            ) : (
              <svg className="w-5 h-5 flex-shrink-0" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M10 14l2-2m0 0l2-2m-2 2l-2-2m2 2l2 2m7-2a9 9 0 11-18 0 9 9 0 0118 0z" />
              </svg>
            )}
            <span>{testResult.message}</span>
          </div>
        )}

        {/* Rig Details Card */}
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 overflow-hidden">
          <div className="px-5 py-4 border-b border-slate-700/50 flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-blox-500/20 flex items-center justify-center">
              <svg className="w-4 h-4 text-blox-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 3v2m6-2v2M9 19v2m6-2v2M5 9H3m2 6H3m18-6h-2m2 6h-2M7 19h10a2 2 0 002-2V7a2 2 0 00-2-2H7a2 2 0 00-2 2v10a2 2 0 002 2zM9 9h6v6H9V9z" />
              </svg>
            </div>
            <div>
              <h2 className="font-semibold">Rig Details</h2>
              <p className="text-xs text-slate-400">Basic information about your rig</p>
            </div>
          </div>

          <div className="p-5 space-y-4">
            <label className="block">
              <span className="text-sm font-medium text-slate-300 mb-1.5 block">Rig Name</span>
              <input
                type="text"
                value={formData.name}
                onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500 focus:border-transparent transition-all"
                placeholder="e.g., Mining Rig 1"
                required
              />
              <p className="text-xs text-slate-500 mt-1.5">A friendly name to identify this rig</p>
            </label>

            {farms.length > 1 && (
              <label className="block">
                <span className="text-sm font-medium text-slate-300 mb-1.5 block">Farm</span>
                <select
                  value={formData.farmId}
                  onChange={(e) => setFormData({ ...formData, farmId: e.target.value })}
                  className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500 focus:border-transparent transition-all"
                >
                  {farms.map(farm => (
                    <option key={farm.id} value={farm.id}>{farm.name}</option>
                  ))}
                </select>
              </label>
            )}
          </div>
        </div>

        {/* SSH Connection Card */}
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 overflow-hidden">
          <div className="px-5 py-4 border-b border-slate-700/50 flex items-center gap-3">
            <div className="w-8 h-8 rounded-lg bg-green-500/20 flex items-center justify-center">
              <svg className="w-4 h-4 text-green-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M8 9l3 3-3 3m5 0h3M5 20h14a2 2 0 002-2V6a2 2 0 00-2-2H5a2 2 0 00-2 2v12a2 2 0 002 2z" />
              </svg>
            </div>
            <div>
              <h2 className="font-semibold">SSH Connection</h2>
              <p className="text-xs text-slate-400">Connection details for your rig</p>
            </div>
          </div>

          <div className="p-5 space-y-4">
            <div className="grid grid-cols-3 gap-4">
              <div className="col-span-2">
                <label className="block">
                  <span className="text-sm font-medium text-slate-300 mb-1.5 block">Host / IP Address</span>
                  <input
                    type="text"
                    value={formData.host}
                    onChange={(e) => setFormData({ ...formData, host: e.target.value })}
                    className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500 focus:border-transparent font-mono text-sm transition-all"
                    placeholder="192.168.1.100"
                    required
                  />
                </label>
              </div>
              <div>
                <label className="block">
                  <span className="text-sm font-medium text-slate-300 mb-1.5 block">Port</span>
                  <input
                    type="number"
                    value={formData.port}
                    onChange={(e) => setFormData({ ...formData, port: e.target.value })}
                    className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500 focus:border-transparent font-mono text-sm transition-all"
                    placeholder="22"
                  />
                </label>
              </div>
            </div>

            <div>
              <label className="block">
                <span className="text-sm font-medium text-slate-300 mb-1.5 block">Username</span>
                <input
                  type="text"
                  value={formData.username}
                  onChange={(e) => setFormData({ ...formData, username: e.target.value })}
                  className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500 focus:border-transparent font-mono text-sm transition-all"
                  placeholder="root"
                  required
                />
              </label>
            </div>

            {/* Auth Type Toggle */}
            <div>
              <span className="text-sm font-medium text-slate-300 mb-2 block">Authentication Method</span>
              <div className="flex rounded-lg bg-slate-900/50 p-1 border border-slate-700">
                <button
                  type="button"
                  onClick={() => setFormData({ ...formData, authType: 'password' })}
                  className={`flex-1 px-4 py-2 rounded-md text-sm font-medium transition-all ${
                    formData.authType === 'password'
                      ? 'bg-blox-600 text-white'
                      : 'text-slate-400 hover:text-white'
                  }`}
                >
                  Password
                </button>
                <button
                  type="button"
                  onClick={() => setFormData({ ...formData, authType: 'key' })}
                  className={`flex-1 px-4 py-2 rounded-md text-sm font-medium transition-all ${
                    formData.authType === 'key'
                      ? 'bg-blox-600 text-white'
                      : 'text-slate-400 hover:text-white'
                  }`}
                >
                  SSH Key
                </button>
              </div>
            </div>

            {formData.authType === 'password' ? (
              <div>
                <label className="block">
                  <span className="text-sm font-medium text-slate-300 mb-1.5 block">Password</span>
                  <input
                    type="password"
                    value={formData.password}
                    onChange={(e) => setFormData({ ...formData, password: e.target.value })}
                    className="w-full px-4 py-2.5 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500 focus:border-transparent transition-all"
                    placeholder="Enter password"
                  />
                </label>
              </div>
            ) : (
              <div>
                <label className="block">
                  <span className="text-sm font-medium text-slate-300 mb-1.5 block">Private Key</span>
                  <textarea
                    value={formData.privateKey}
                    onChange={(e) => setFormData({ ...formData, privateKey: e.target.value })}
                    className="w-full px-4 py-3 bg-slate-900/50 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500 focus:border-transparent font-mono text-xs transition-all"
                    placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;...&#10;-----END OPENSSH PRIVATE KEY-----"
                    rows={6}
                  />
                  <p className="text-xs text-slate-500 mt-1.5">Paste your private key (id_rsa, id_ed25519, etc.)</p>
                </label>
              </div>
            )}

            {/* Test Connection Button */}
            <button
              type="button"
              onClick={handleTestConnection}
              disabled={testing || !formData.host || !formData.username || (formData.authType === 'password' ? !formData.password : !formData.privateKey)}
              className="inline-flex items-center gap-2 px-4 py-2.5 bg-slate-700 hover:bg-slate-600 rounded-lg text-sm font-medium transition-colors disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {testing ? (
                <>
                  <div className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin"></div>
                  Testing...
                </>
              ) : (
                <>
                  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 10V3L4 14h7v7l9-11h-7z" />
                  </svg>
                  Test Connection
                </>
              )}
            </button>
          </div>
        </div>

        {/* Actions */}
        <div className="flex items-center justify-between pt-4">
          <Link
            href="/rigs"
            className="px-6 py-2.5 text-slate-400 hover:text-white transition-colors"
          >
            Cancel
          </Link>
          <button
            type="submit"
            disabled={loading || !isFormValid}
            className="inline-flex items-center gap-2 px-6 py-2.5 bg-gradient-to-r from-blox-500 to-blox-600 hover:from-blox-600 hover:to-blox-700 rounded-lg text-white font-medium transition-all shadow-lg shadow-blox-500/25 disabled:opacity-50 disabled:cursor-not-allowed disabled:shadow-none"
          >
            {loading ? (
              <>
                <div className="w-4 h-4 border-2 border-white/30 border-t-white rounded-full animate-spin"></div>
                Adding Rig...
              </>
            ) : (
              <>
                <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 4v16m8-8H4" />
                </svg>
                Add Rig
              </>
            )}
          </button>
        </div>
      </form>
    </div>
  );
}
