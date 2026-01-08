'use client';

import { useEffect, useState } from 'react';

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

interface OCProfile {
  id: string;
  name: string;
  vendor: string;
  powerLimit: number | null;
  coreOffset: number | null;
  memOffset: number | null;
  coreLock: number | null;
  memLock: number | null;
  fanSpeed: number | null;
  coreVddc: number | null;
  memVddc: number | null;
  _count?: { rigs: number };
}

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

export default function OCProfilesPage() {
  const [profiles, setProfiles] = useState<OCProfile[]>([]);
  const [farms, setFarms] = useState<Farm[]>([]);
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);
  const [editingProfile, setEditingProfile] = useState<OCProfile | null>(null);
  const [formData, setFormData] = useState({
    name: '',
    vendor: 'NVIDIA' as 'NVIDIA' | 'AMD',
    powerLimit: '',
    coreOffset: '',
    memOffset: '',
    coreLock: '',
    memLock: '',
    fanSpeed: '',
    farmId: '',
  });
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    fetchProfiles();
  }, []);

  async function fetchProfiles() {
    try {
      const [profilesRes, userRes] = await Promise.all([
        fetch(`${getApiUrl()}/api/oc-profiles`, { credentials: 'include' }),
        fetch(`${getApiUrl()}/api/auth/me`, { credentials: 'include' }),
      ]);
      
      if (profilesRes.ok) {
        const data = await profilesRes.json();
        if (Array.isArray(data)) {
          setProfiles(data);
        }
      }
      
      if (userRes.ok) {
        const userData = await userRes.json();
        if (userData.farms && Array.isArray(userData.farms)) {
          setFarms(userData.farms);
        }
      }
    } catch (err) {
      console.error('Failed to fetch OC profiles');
    } finally {
      setLoading(false);
    }
  }

  function openCreateModal() {
    setEditingProfile(null);
    setFormData({
      name: '',
      vendor: 'NVIDIA',
      powerLimit: '',
      coreOffset: '',
      memOffset: '',
      coreLock: '',
      memLock: '',
      fanSpeed: '',
      farmId: farms[0]?.id || '',
    });
    setShowModal(true);
  }

  function openEditModal(profile: OCProfile) {
    setEditingProfile(profile);
    setFormData({
      name: profile.name,
      vendor: profile.vendor as 'NVIDIA' | 'AMD',
      powerLimit: profile.powerLimit?.toString() || '',
      coreOffset: profile.coreOffset?.toString() || '',
      memOffset: profile.memOffset?.toString() || '',
      coreLock: profile.coreLock?.toString() || '',
      memLock: profile.memLock?.toString() || '',
      fanSpeed: profile.fanSpeed?.toString() || '',
      farmId: farms[0]?.id || '',
    });
    setShowModal(true);
  }

  async function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    setSaving(true);

    const basePayload = {
      name: formData.name,
      vendor: formData.vendor,
      powerLimit: formData.powerLimit ? parseInt(formData.powerLimit) : null,
      coreOffset: formData.coreOffset ? parseInt(formData.coreOffset) : null,
      memOffset: formData.memOffset ? parseInt(formData.memOffset) : null,
      coreLock: formData.coreLock ? parseInt(formData.coreLock) : null,
      memLock: formData.memLock ? parseInt(formData.memLock) : null,
      fanSpeed: formData.fanSpeed ? parseInt(formData.fanSpeed) : null,
    };

    // For create, include farmId; for update, don't
    const payload = editingProfile ? basePayload : { ...basePayload, farmId: formData.farmId };

    try {
      const url = editingProfile
        ? `${getApiUrl()}/api/oc-profiles/${editingProfile.id}`
        : `${getApiUrl()}/api/oc-profiles`;

      const res = await fetch(url, {
        method: editingProfile ? 'PATCH' : 'POST',
        headers: { 
          'Content-Type': 'application/json',
          'x-csrf-token': getCsrfToken(),
        },
        credentials: 'include',
        body: JSON.stringify(payload),
      });

      if (res.ok) {
        setShowModal(false);
        fetchProfiles();
      }
    } catch (err) {
      console.error('Failed to save OC profile');
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete(id: string) {
    if (!confirm('Are you sure you want to delete this OC profile?')) return;

    try {
      const res = await fetch(`${getApiUrl()}/api/oc-profiles/${id}`, {
        method: 'DELETE',
        headers: {
          'x-csrf-token': getCsrfToken(),
        },
        credentials: 'include',
      });

      if (res.ok) {
        fetchProfiles();
      }
    } catch (err) {
      console.error('Failed to delete OC profile');
    }
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20">
        <div className="inline-block w-8 h-8 border-2 border-slate-600 border-t-blox-400 rounded-full animate-spin"></div>
        <p className="text-slate-400 ml-3">Loading OC profiles...</p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-4">
          <div className="w-12 h-12 rounded-xl bg-red-500/20 flex items-center justify-center">
            <svg className="w-6 h-6 text-red-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 10V3L4 14h7v7l9-11h-7z" />
            </svg>
          </div>
          <div>
            <h1 className="text-2xl font-bold">OC Profiles</h1>
            <p className="text-slate-400">{profiles.length} profiles configured</p>
          </div>
        </div>
        <button
          onClick={openCreateModal}
          className="inline-flex items-center gap-2 px-4 py-2.5 bg-blox-600 hover:bg-blox-700 rounded-lg text-sm font-medium transition-colors"
        >
          <PlusIcon /> New Profile
        </button>
      </div>

      {/* Profiles Grid */}
      {profiles.length === 0 ? (
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-12 text-center">
          <div className="w-16 h-16 mx-auto mb-4 rounded-full bg-slate-700 flex items-center justify-center">
            <svg className="w-8 h-8 text-slate-400" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 10V3L4 14h7v7l9-11h-7z" />
            </svg>
          </div>
          <h2 className="text-xl font-semibold mb-2">No OC Profiles</h2>
          <p className="text-slate-400 mb-4">Create your first overclocking profile</p>
          <button
            onClick={openCreateModal}
            className="inline-flex items-center gap-2 px-4 py-2 bg-blox-600 hover:bg-blox-700 rounded-lg text-sm font-medium transition-colors"
          >
            <PlusIcon /> Create Profile
          </button>
        </div>
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {profiles.map((profile) => (
            <div
              key={profile.id}
              className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-5 hover:border-slate-600 transition-colors"
            >
              <div className="flex items-start justify-between mb-4">
                <div>
                  <h3 className="font-semibold text-lg">{profile.name}</h3>
                  <span className={`text-xs px-2 py-0.5 rounded ${
                    profile.vendor === 'NVIDIA' 
                      ? 'bg-green-500/20 text-green-400' 
                      : 'bg-red-500/20 text-red-400'
                  }`}>
                    {profile.vendor}
                  </span>
                </div>
                <div className="flex items-center gap-1">
                  <button
                    onClick={() => openEditModal(profile)}
                    className="p-2 hover:bg-slate-700 rounded-lg transition-colors"
                  >
                    <EditIcon />
                  </button>
                  <button
                    onClick={() => handleDelete(profile.id)}
                    className="p-2 hover:bg-red-500/20 text-red-400 rounded-lg transition-colors"
                  >
                    <TrashIcon />
                  </button>
                </div>
              </div>

              <div className="grid grid-cols-2 gap-3 text-sm">
                {profile.powerLimit && (
                  <div className="bg-slate-900/50 rounded-lg p-2">
                    <p className="text-xs text-slate-500">Power Limit</p>
                    <p className="font-medium text-yellow-400">{profile.powerLimit}W</p>
                  </div>
                )}
                {profile.coreOffset && (
                  <div className="bg-slate-900/50 rounded-lg p-2">
                    <p className="text-xs text-slate-500">Core Offset</p>
                    <p className="font-medium">{profile.coreOffset > 0 ? '+' : ''}{profile.coreOffset} MHz</p>
                  </div>
                )}
                {profile.memOffset && (
                  <div className="bg-slate-900/50 rounded-lg p-2">
                    <p className="text-xs text-slate-500">Memory Offset</p>
                    <p className="font-medium">{profile.memOffset > 0 ? '+' : ''}{profile.memOffset} MHz</p>
                  </div>
                )}
                {profile.fanSpeed !== null && (
                  <div className="bg-slate-900/50 rounded-lg p-2">
                    <p className="text-xs text-slate-500">Fan Speed</p>
                    <p className="font-medium">{profile.fanSpeed === 0 ? 'Auto' : `${profile.fanSpeed}%`}</p>
                  </div>
                )}
                {profile.coreLock && (
                  <div className="bg-slate-900/50 rounded-lg p-2">
                    <p className="text-xs text-slate-500">Core Lock</p>
                    <p className="font-medium">{profile.coreLock} MHz</p>
                  </div>
                )}
                {profile.memLock && (
                  <div className="bg-slate-900/50 rounded-lg p-2">
                    <p className="text-xs text-slate-500">Mem Lock</p>
                    <p className="font-medium">{profile.memLock} MHz</p>
                  </div>
                )}
              </div>

              {profile._count && profile._count.rigs > 0 && (
                <div className="mt-3 pt-3 border-t border-slate-700/50 text-xs text-slate-500">
                  Used by {profile._count.rigs} rig{profile._count.rigs !== 1 ? 's' : ''}
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {/* Create/Edit Modal */}
      {showModal && (
        <div className="fixed inset-0 bg-black/50 backdrop-blur-sm flex items-center justify-center z-50">
          <div className="bg-slate-800 rounded-xl border border-slate-700 w-full max-w-lg mx-4 p-6">
            <h2 className="text-xl font-bold mb-4">
              {editingProfile ? 'Edit OC Profile' : 'Create OC Profile'}
            </h2>

            <form onSubmit={handleSubmit} className="space-y-4">
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">Name</label>
                <input
                  type="text"
                  value={formData.name}
                  onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                  className="w-full px-4 py-2.5 bg-slate-900 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                  placeholder="e.g., RTX 3080 Efficient"
                  required
                />
              </div>

              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">GPU Vendor</label>
                <select
                  value={formData.vendor}
                  onChange={(e) => setFormData({ ...formData, vendor: e.target.value as 'NVIDIA' | 'AMD' })}
                  className="w-full px-4 py-2.5 bg-slate-900 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                >
                  <option value="NVIDIA">NVIDIA</option>
                  <option value="AMD">AMD</option>
                </select>
              </div>

              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-1">Power Limit (W)</label>
                  <input
                    type="number"
                    value={formData.powerLimit}
                    onChange={(e) => setFormData({ ...formData, powerLimit: e.target.value })}
                    className="w-full px-4 py-2.5 bg-slate-900 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                    placeholder="e.g., 220"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-1">Fan Speed (%)</label>
                  <input
                    type="number"
                    value={formData.fanSpeed}
                    onChange={(e) => setFormData({ ...formData, fanSpeed: e.target.value })}
                    className="w-full px-4 py-2.5 bg-slate-900 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                    placeholder="0 = Auto"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-1">Core Offset (MHz)</label>
                  <input
                    type="number"
                    value={formData.coreOffset}
                    onChange={(e) => setFormData({ ...formData, coreOffset: e.target.value })}
                    className="w-full px-4 py-2.5 bg-slate-900 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                    placeholder="e.g., -200"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-1">Memory Offset (MHz)</label>
                  <input
                    type="number"
                    value={formData.memOffset}
                    onChange={(e) => setFormData({ ...formData, memOffset: e.target.value })}
                    className="w-full px-4 py-2.5 bg-slate-900 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                    placeholder="e.g., 1200"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-1">Core Lock (MHz)</label>
                  <input
                    type="number"
                    value={formData.coreLock}
                    onChange={(e) => setFormData({ ...formData, coreLock: e.target.value })}
                    className="w-full px-4 py-2.5 bg-slate-900 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                    placeholder="Optional"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-1">Memory Lock (MHz)</label>
                  <input
                    type="number"
                    value={formData.memLock}
                    onChange={(e) => setFormData({ ...formData, memLock: e.target.value })}
                    className="w-full px-4 py-2.5 bg-slate-900 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                    placeholder="Optional"
                  />
                </div>
              </div>

              <div className="flex justify-end gap-3 pt-4">
                <button
                  type="button"
                  onClick={() => setShowModal(false)}
                  className="px-4 py-2 bg-slate-700 hover:bg-slate-600 rounded-lg text-sm font-medium transition-colors"
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  disabled={saving}
                  className="px-4 py-2 bg-blox-600 hover:bg-blox-700 rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
                >
                  {saving ? 'Saving...' : editingProfile ? 'Update' : 'Create'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}
