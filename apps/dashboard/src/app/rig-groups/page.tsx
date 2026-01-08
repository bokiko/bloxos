'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';

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

interface Rig {
  id: string;
  name: string;
  status: string;
}

interface RigGroup {
  id: string;
  name: string;
  color: string;
  description: string | null;
  rigs: Rig[];
  _count: { rigs: number };
}

const COLORS = [
  '#6366f1', // Indigo
  '#8b5cf6', // Violet
  '#ec4899', // Pink
  '#f43f5e', // Rose
  '#ef4444', // Red
  '#f97316', // Orange
  '#eab308', // Yellow
  '#22c55e', // Green
  '#14b8a6', // Teal
  '#06b6d4', // Cyan
  '#3b82f6', // Blue
];

export default function RigGroupsPage() {
  const [groups, setGroups] = useState<RigGroup[]>([]);
  const [farms, setFarms] = useState<Farm[]>([]);
  const [loading, setLoading] = useState(true);
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [editingGroup, setEditingGroup] = useState<RigGroup | null>(null);
  const [formData, setFormData] = useState({ name: '', color: '#6366f1', description: '', farmId: '' });
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    fetchGroups();
  }, []);

  async function fetchGroups() {
    try {
      const [groupsRes, userRes] = await Promise.all([
        fetch(`${getApiUrl()}/api/rig-groups`, { credentials: 'include' }),
        fetch(`${getApiUrl()}/api/auth/me`, { credentials: 'include' }),
      ]);
      
      if (groupsRes.ok) {
        const data = await groupsRes.json();
        if (Array.isArray(data)) {
          setGroups(data);
        }
      }
      
      if (userRes.ok) {
        const userData = await userRes.json();
        if (userData.farms && Array.isArray(userData.farms)) {
          setFarms(userData.farms);
        }
      }
    } catch (error) {
      console.error('Failed to fetch rig groups:', error);
    } finally {
      setLoading(false);
    }
  }

  async function handleCreate() {
    if (!formData.name.trim()) return;
    setSaving(true);

    try {
      const res = await fetch(`${getApiUrl()}/api/rig-groups`, {
        method: 'POST',
        headers: { 
          'Content-Type': 'application/json',
          'x-csrf-token': getCsrfToken(),
        },
        credentials: 'include',
        body: JSON.stringify({
          name: formData.name,
          color: formData.color,
          description: formData.description || undefined,
          farmId: formData.farmId || farms[0]?.id,
        }),
      });

      if (res.ok) {
        await fetchGroups();
        setShowCreateModal(false);
        setFormData({ name: '', color: '#6366f1', description: '', farmId: farms[0]?.id || '' });
      }
    } catch (error) {
      console.error('Failed to create group:', error);
    } finally {
      setSaving(false);
    }
  }

  async function handleUpdate() {
    if (!editingGroup || !formData.name.trim()) return;
    setSaving(true);

    try {
      const res = await fetch(`${getApiUrl()}/api/rig-groups/${editingGroup.id}`, {
        method: 'PATCH',
        headers: { 
          'Content-Type': 'application/json',
          'x-csrf-token': getCsrfToken(),
        },
        credentials: 'include',
        body: JSON.stringify({
          name: formData.name,
          color: formData.color,
          description: formData.description || undefined,
        }),
      });

      if (res.ok) {
        await fetchGroups();
        setEditingGroup(null);
        setFormData({ name: '', color: '#6366f1', description: '', farmId: farms[0]?.id || '' });
      }
    } catch (error) {
      console.error('Failed to update group:', error);
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete(id: string, name: string) {
    if (!confirm(`Delete group "${name}"? Rigs in this group will be unassigned.`)) return;

    try {
      const res = await fetch(`${getApiUrl()}/api/rig-groups/${id}`, {
        method: 'DELETE',
        headers: {
          'x-csrf-token': getCsrfToken(),
        },
        credentials: 'include',
      });

      if (res.ok) {
        await fetchGroups();
      }
    } catch (error) {
      console.error('Failed to delete group:', error);
    }
  }

  function openEditModal(group: RigGroup) {
    setEditingGroup(group);
    setFormData({
      name: group.name,
      color: group.color,
      description: group.description || '',
      farmId: farms[0]?.id || '',
    });
  }

  function getStatusColor(status: string) {
    switch (status) {
      case 'ONLINE': return 'bg-green-500';
      case 'WARNING': return 'bg-yellow-500';
      case 'ERROR': return 'bg-red-500';
      default: return 'bg-slate-500';
    }
  }

  return (
    <div>
      {/* Header */}
      <div className="flex justify-between items-center mb-6">
        <div>
          <h1 className="text-2xl font-bold">Rig Groups</h1>
          <p className="text-slate-400 text-sm mt-1">Organize your rigs into groups</p>
        </div>
        <button
          onClick={() => setShowCreateModal(true)}
          className="px-4 py-2.5 bg-gradient-to-r from-blox-500 to-blox-600 hover:from-blox-600 hover:to-blox-700 rounded-lg text-white font-medium transition-all duration-200 shadow-lg shadow-blox-500/25"
        >
          + Create Group
        </button>
      </div>

      {/* Groups Grid */}
      {loading ? (
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-12 text-center">
          <div className="inline-block w-8 h-8 border-2 border-slate-600 border-t-blox-400 rounded-full animate-spin"></div>
          <p className="text-slate-400 mt-3">Loading groups...</p>
        </div>
      ) : groups.length === 0 ? (
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-12 text-center">
          <div className="w-16 h-16 mx-auto mb-4 rounded-full bg-slate-700/50 flex items-center justify-center">
            <svg className="w-8 h-8 text-slate-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M19 11H5m14 0a2 2 0 012 2v6a2 2 0 01-2 2H5a2 2 0 01-2-2v-6a2 2 0 012-2m14 0V9a2 2 0 00-2-2M5 11V9a2 2 0 012-2m0 0V5a2 2 0 012-2h6a2 2 0 012 2v2M7 7h10" />
            </svg>
          </div>
          <p className="text-slate-400 mb-4">No rig groups yet. Create one to organize your rigs.</p>
          <button
            onClick={() => setShowCreateModal(true)}
            className="inline-block px-4 py-2 bg-blox-600 hover:bg-blox-700 rounded-lg text-white font-medium transition-colors"
          >
            Create Group
          </button>
        </div>
      ) : (
        <div className="grid md:grid-cols-2 lg:grid-cols-3 gap-4">
          {groups.map((group) => (
            <div
              key={group.id}
              className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-5 hover:border-slate-600/50 transition-all"
            >
              {/* Header */}
              <div className="flex items-start justify-between mb-4">
                <div className="flex items-center gap-3">
                  <div
                    className="w-4 h-4 rounded-full"
                    style={{ backgroundColor: group.color }}
                  ></div>
                  <div>
                    <h3 className="text-lg font-semibold">{group.name}</h3>
                    <p className="text-sm text-slate-400">{group._count.rigs} rig(s)</p>
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <button
                    onClick={() => openEditModal(group)}
                    className="p-2 rounded-lg text-slate-500 hover:text-blox-400 hover:bg-blox-500/10 transition-colors"
                  >
                    <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M11 5H6a2 2 0 00-2 2v11a2 2 0 002 2h11a2 2 0 002-2v-5m-1.414-9.414a2 2 0 112.828 2.828L11.828 15H9v-2.828l8.586-8.586z" />
                    </svg>
                  </button>
                  <button
                    onClick={() => handleDelete(group.id, group.name)}
                    className="p-2 rounded-lg text-slate-500 hover:text-red-400 hover:bg-red-500/10 transition-colors"
                  >
                    <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                      <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                    </svg>
                  </button>
                </div>
              </div>

              {/* Description */}
              {group.description && (
                <p className="text-sm text-slate-400 mb-4">{group.description}</p>
              )}

              {/* Rigs in group */}
              {group.rigs.length > 0 ? (
                <div className="space-y-2">
                  {group.rigs.slice(0, 5).map((rig) => (
                    <Link
                      key={rig.id}
                      href={`/rigs/${rig.id}`}
                      className="flex items-center gap-2 px-3 py-2 bg-slate-900/50 rounded-lg hover:bg-slate-700/50 transition-colors"
                    >
                      <div className={`w-2 h-2 rounded-full ${getStatusColor(rig.status)}`}></div>
                      <span className="text-sm">{rig.name}</span>
                    </Link>
                  ))}
                  {group.rigs.length > 5 && (
                    <p className="text-xs text-slate-500 px-3">+{group.rigs.length - 5} more</p>
                  )}
                </div>
              ) : (
                <p className="text-sm text-slate-500 italic">No rigs in this group</p>
              )}
            </div>
          ))}
        </div>
      )}

      {/* Create/Edit Modal */}
      {(showCreateModal || editingGroup) && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <div className="bg-slate-800 border border-slate-700 rounded-xl p-6 w-96">
            <h3 className="text-lg font-semibold mb-4">
              {editingGroup ? 'Edit Group' : 'Create Group'}
            </h3>

            <div className="space-y-4">
              {/* Name */}
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-2">Name</label>
                <input
                  type="text"
                  value={formData.name}
                  onChange={(e) => setFormData({ ...formData, name: e.target.value })}
                  placeholder="e.g., GPU Farm 1"
                  className="w-full px-4 py-2.5 bg-slate-900 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500"
                />
              </div>

              {/* Color */}
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-2">Color</label>
                <div className="flex flex-wrap gap-2">
                  {COLORS.map((color) => (
                    <button
                      key={color}
                      onClick={() => setFormData({ ...formData, color })}
                      className={`w-8 h-8 rounded-lg transition-all ${
                        formData.color === color ? 'ring-2 ring-white ring-offset-2 ring-offset-slate-800' : ''
                      }`}
                      style={{ backgroundColor: color }}
                    />
                  ))}
                </div>
              </div>

              {/* Description */}
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-2">Description (optional)</label>
                <textarea
                  value={formData.description}
                  onChange={(e) => setFormData({ ...formData, description: e.target.value })}
                  placeholder="Optional description..."
                  rows={3}
                  className="w-full px-4 py-2.5 bg-slate-900 border border-slate-700 rounded-lg focus:outline-none focus:ring-2 focus:ring-blox-500 resize-none"
                />
              </div>
            </div>

            {/* Actions */}
            <div className="flex gap-3 mt-6">
              <button
                onClick={() => {
                  setShowCreateModal(false);
                  setEditingGroup(null);
                  setFormData({ name: '', color: '#6366f1', description: '', farmId: farms[0]?.id || '' });
                }}
                className="flex-1 px-4 py-2.5 bg-slate-700 hover:bg-slate-600 rounded-lg font-medium transition-colors"
              >
                Cancel
              </button>
              <button
                onClick={editingGroup ? handleUpdate : handleCreate}
                disabled={saving || !formData.name.trim()}
                className="flex-1 px-4 py-2.5 bg-blox-600 hover:bg-blox-700 disabled:opacity-50 rounded-lg font-medium transition-colors"
              >
                {saving ? 'Saving...' : editingGroup ? 'Update' : 'Create'}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
