'use client';

import { useEffect, useState } from 'react';
import Link from 'next/link';

const getApiUrl = () => {
  if (typeof window === 'undefined') return 'http://localhost:3001';
  return `http://${window.location.hostname}:3001`;
};

interface GPU {
  id: string;
  index: number;
  name: string;
  vendor: string;
  vram: number;
  temperature: number | null;
  fanSpeed: number | null;
  powerDraw: number | null;
  hashrate: number | null;
}

interface CPU {
  id: string;
  powerDraw: number | null;
}

interface RigGroup {
  id: string;
  name: string;
  color: string;
}

interface Rig {
  id: string;
  name: string;
  hostname: string;
  ipAddress: string | null;
  os: string | null;
  osVersion: string | null;
  status: string;
  lastSeen: string | null;
  gpus: GPU[];
  cpu: CPU | null;
  groups: RigGroup[];  // Changed to array
  flightSheetId: string | null;
  ocProfileId: string | null;
}

interface FlightSheet {
  id: string;
  name: string;
  coin: string;
}

interface OCProfile {
  id: string;
  name: string;
  vendor: string;
}

export default function RigsPage() {
  const [rigs, setRigs] = useState<Rig[]>([]);
  const [loading, setLoading] = useState(true);
  const [filter, setFilter] = useState<'all' | 'online' | 'offline'>('all');
  const [groupFilter, setGroupFilter] = useState<string | null>(null);  // Filter by group
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [selectedRigs, setSelectedRigs] = useState<Set<string>>(new Set());
  const [bulkActionLoading, setBulkActionLoading] = useState(false);
  const [showBulkMenu, setShowBulkMenu] = useState(false);
  const [flightSheets, setFlightSheets] = useState<FlightSheet[]>([]);
  const [ocProfiles, setOcProfiles] = useState<OCProfile[]>([]);
  const [rigGroups, setRigGroups] = useState<RigGroup[]>([]);
  const [bulkAssignType, setBulkAssignType] = useState<'flightsheet' | 'oc' | 'group' | null>(null);
  const REFRESH_INTERVAL = 30000;

  useEffect(() => {
    fetchRigs();
    fetchFlightSheets();
    fetchOcProfiles();
    fetchRigGroups();

    let intervalId: NodeJS.Timeout | null = null;
    if (autoRefresh) {
      intervalId = setInterval(() => {
        fetchRigs();
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
      if (!res.ok) return;
      const data = await res.json();
      if (Array.isArray(data)) {
        setRigs(data);
        setLastUpdated(new Date());
      }
    } catch (error) {
      console.error('Failed to fetch rigs:', error);
    } finally {
      setLoading(false);
    }
  }

  async function fetchFlightSheets() {
    try {
      const res = await fetch(`${getApiUrl()}/api/flight-sheets`, {
        credentials: 'include',
      });
      if (!res.ok) return;
      const data = await res.json();
      if (Array.isArray(data)) {
        setFlightSheets(data);
      }
    } catch (error) {
      console.error('Failed to fetch flight sheets:', error);
    }
  }

  async function fetchOcProfiles() {
    try {
      const res = await fetch(`${getApiUrl()}/api/oc-profiles`, {
        credentials: 'include',
      });
      if (!res.ok) return;
      const data = await res.json();
      if (Array.isArray(data)) {
        setOcProfiles(data);
      }
    } catch (error) {
      console.error('Failed to fetch OC profiles:', error);
    }
  }

  async function fetchRigGroups() {
    try {
      const res = await fetch(`${getApiUrl()}/api/rig-groups`, {
        credentials: 'include',
      });
      if (!res.ok) return;
      const data = await res.json();
      if (Array.isArray(data)) {
        setRigGroups(data);
      }
    } catch (error) {
      console.error('Failed to fetch rig groups:', error);
    }
  }

  function formatTimeAgo(date: Date | null) {
    if (!date) return '';
    const diff = Date.now() - date.getTime();
    const secs = Math.floor(diff / 1000);
    if (secs < 60) return `${secs}s ago`;
    const mins = Math.floor(secs / 60);
    return `${mins}m ago`;
  }

  async function handleDelete(e: React.MouseEvent, id: string, name: string) {
    e.stopPropagation();
    e.preventDefault();
    if (!confirm(`Are you sure you want to delete "${name}"?`)) return;

    try {
      const res = await fetch(`${getApiUrl()}/api/rigs/${id}`, {
        method: 'DELETE',
        credentials: 'include',
      });

      if (res.ok) {
        setRigs(rigs.filter((r) => r.id !== id));
        setSelectedRigs(prev => {
          const next = new Set(prev);
          next.delete(id);
          return next;
        });
      }
    } catch (error) {
      console.error('Failed to delete rig:', error);
    }
  }

  // Bulk action handlers
  async function handleBulkStartMiners() {
    if (selectedRigs.size === 0) return;
    setBulkActionLoading(true);
    try {
      const res = await fetch(`${getApiUrl()}/api/bulk/start-miners`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ rigIds: Array.from(selectedRigs) }),
      });
      const data = await res.json();
      alert(data.message);
      await fetchRigs();
    } catch (error) {
      alert('Failed to start miners');
    } finally {
      setBulkActionLoading(false);
      setShowBulkMenu(false);
    }
  }

  async function handleBulkStopMiners() {
    if (selectedRigs.size === 0) return;
    setBulkActionLoading(true);
    try {
      const res = await fetch(`${getApiUrl()}/api/bulk/stop-miners`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ rigIds: Array.from(selectedRigs) }),
      });
      const data = await res.json();
      alert(data.message);
      await fetchRigs();
    } catch (error) {
      alert('Failed to stop miners');
    } finally {
      setBulkActionLoading(false);
      setShowBulkMenu(false);
    }
  }

  async function handleBulkApplyOC() {
    if (selectedRigs.size === 0) return;
    setBulkActionLoading(true);
    try {
      const res = await fetch(`${getApiUrl()}/api/bulk/apply-oc`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ rigIds: Array.from(selectedRigs) }),
      });
      const data = await res.json();
      alert(data.message);
    } catch (error) {
      alert('Failed to apply OC');
    } finally {
      setBulkActionLoading(false);
      setShowBulkMenu(false);
    }
  }

  async function handleBulkReboot() {
    if (selectedRigs.size === 0) return;
    if (!confirm(`Reboot ${selectedRigs.size} rig(s)?`)) return;
    setBulkActionLoading(true);
    try {
      const res = await fetch(`${getApiUrl()}/api/bulk/reboot`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ rigIds: Array.from(selectedRigs) }),
      });
      const data = await res.json();
      alert(data.message);
      await fetchRigs();
    } catch (error) {
      alert('Failed to reboot rigs');
    } finally {
      setBulkActionLoading(false);
      setShowBulkMenu(false);
    }
  }

  async function handleBulkAssignFlightSheet(flightSheetId: string | null) {
    if (selectedRigs.size === 0) return;
    setBulkActionLoading(true);
    try {
      const res = await fetch(`${getApiUrl()}/api/bulk/assign-flight-sheet`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ rigIds: Array.from(selectedRigs), flightSheetId }),
      });
      const data = await res.json();
      alert(data.message);
      await fetchRigs();
    } catch (error) {
      alert('Failed to assign flight sheet');
    } finally {
      setBulkActionLoading(false);
      setBulkAssignType(null);
    }
  }

  async function handleBulkAssignOcProfile(ocProfileId: string | null) {
    if (selectedRigs.size === 0) return;
    setBulkActionLoading(true);
    try {
      const res = await fetch(`${getApiUrl()}/api/bulk/assign-oc-profile`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ rigIds: Array.from(selectedRigs), ocProfileId }),
      });
      const data = await res.json();
      alert(data.message);
      await fetchRigs();
    } catch (error) {
      alert('Failed to assign OC profile');
    } finally {
      setBulkActionLoading(false);
      setBulkAssignType(null);
    }
  }

  async function handleBulkAddToGroup(groupId: string) {
    if (selectedRigs.size === 0) return;
    setBulkActionLoading(true);
    try {
      const res = await fetch(`${getApiUrl()}/api/bulk/add-to-group`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ rigIds: Array.from(selectedRigs), groupId }),
      });
      const data = await res.json();
      alert(data.message);
      await fetchRigs();
    } catch (error) {
      alert('Failed to add to group');
    } finally {
      setBulkActionLoading(false);
      setBulkAssignType(null);
    }
  }

  async function handleBulkRemoveFromGroup(groupId: string) {
    if (selectedRigs.size === 0) return;
    setBulkActionLoading(true);
    try {
      const res = await fetch(`${getApiUrl()}/api/bulk/remove-from-group`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ rigIds: Array.from(selectedRigs), groupId }),
      });
      const data = await res.json();
      alert(data.message);
      await fetchRigs();
    } catch (error) {
      alert('Failed to remove from group');
    } finally {
      setBulkActionLoading(false);
      setBulkAssignType(null);
    }
  }

  function toggleRigSelection(rigId: string) {
    setSelectedRigs(prev => {
      const next = new Set(prev);
      if (next.has(rigId)) {
        next.delete(rigId);
      } else {
        next.add(rigId);
      }
      return next;
    });
  }

  function toggleSelectAll() {
    if (selectedRigs.size === filteredRigs.length) {
      setSelectedRigs(new Set());
    } else {
      setSelectedRigs(new Set(filteredRigs.map(r => r.id)));
    }
  }

  function getStatusColor(status: string) {
    switch (status) {
      case 'ONLINE': return 'bg-green-500';
      case 'WARNING': return 'bg-yellow-500';
      case 'ERROR': return 'bg-red-500';
      case 'REBOOTING': return 'bg-blue-500';
      default: return 'bg-slate-500';
    }
  }

  function getStatusBadge(status: string) {
    switch (status) {
      case 'ONLINE': return 'bg-green-500/10 text-green-400 ring-1 ring-green-500/30';
      case 'WARNING': return 'bg-yellow-500/10 text-yellow-400 ring-1 ring-yellow-500/30';
      case 'ERROR': return 'bg-red-500/10 text-red-400 ring-1 ring-red-500/30';
      case 'REBOOTING': return 'bg-blue-500/10 text-blue-400 ring-1 ring-blue-500/30';
      default: return 'bg-slate-500/10 text-slate-400 ring-1 ring-slate-500/30';
    }
  }

  function getTotalHashrate(gpus: GPU[]) {
    return gpus.reduce((sum, gpu) => sum + (gpu.hashrate || 0), 0);
  }

  function getTotalPower(rig: Rig) {
    const gpuPower = rig.gpus.reduce((sum, gpu) => sum + (gpu.powerDraw || 0), 0);
    const cpuPower = rig.cpu?.powerDraw || 0;
    return gpuPower + cpuPower;
  }

  function getMaxTemp(gpus: GPU[]) {
    const temps = gpus.map((g) => g.temperature).filter((t): t is number => t !== null);
    return temps.length > 0 ? Math.max(...temps) : null;
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

  const filteredRigs = rigs.filter(rig => {
    // Status filter
    if (filter === 'online' && rig.status !== 'ONLINE') return false;
    if (filter === 'offline' && rig.status === 'ONLINE') return false;
    
    // Group filter
    if (groupFilter && !rig.groups.some(g => g.id === groupFilter)) return false;
    
    return true;
  });

  const onlineCount = rigs.filter(r => r.status === 'ONLINE').length;
  const offlineCount = rigs.length - onlineCount;

  return (
    <div>
      {/* Header */}
      <div className="flex justify-between items-center mb-6">
        <div>
          <h1 className="text-2xl font-bold">Rigs</h1>
          <p className="text-slate-400 text-sm mt-1">Manage your mining rigs</p>
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
                {formatTimeAgo(lastUpdated)}
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

      {/* Bulk Actions Bar */}
      {selectedRigs.size > 0 && (
        <div className="bg-blox-600/20 border border-blox-500/30 rounded-xl p-4 mb-6 flex items-center justify-between">
          <div className="flex items-center gap-3">
            <span className="text-blox-400 font-medium">{selectedRigs.size} rig(s) selected</span>
            <button
              onClick={() => setSelectedRigs(new Set())}
              className="text-sm text-slate-400 hover:text-white"
            >
              Clear
            </button>
          </div>
          <div className="flex items-center gap-2">
            {/* Quick Actions */}
            <button
              onClick={handleBulkStartMiners}
              disabled={bulkActionLoading}
              className="px-3 py-1.5 bg-green-600 hover:bg-green-700 disabled:opacity-50 rounded-lg text-sm font-medium transition-colors flex items-center gap-2"
            >
              <svg className="w-4 h-4" fill="currentColor" viewBox="0 0 24 24"><path d="M8 5v14l11-7z" /></svg>
              Start All
            </button>
            <button
              onClick={handleBulkStopMiners}
              disabled={bulkActionLoading}
              className="px-3 py-1.5 bg-red-600 hover:bg-red-700 disabled:opacity-50 rounded-lg text-sm font-medium transition-colors flex items-center gap-2"
            >
              <svg className="w-4 h-4" fill="currentColor" viewBox="0 0 24 24"><rect x="6" y="6" width="12" height="12" /></svg>
              Stop All
            </button>
            <button
              onClick={handleBulkApplyOC}
              disabled={bulkActionLoading}
              className="px-3 py-1.5 bg-orange-600 hover:bg-orange-700 disabled:opacity-50 rounded-lg text-sm font-medium transition-colors flex items-center gap-2"
            >
              <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24"><path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M13 10V3L4 14h7v7l9-11h-7z" /></svg>
              Apply OC
            </button>
            
            {/* More Actions Dropdown */}
            <div className="relative">
              <button
                onClick={() => setShowBulkMenu(!showBulkMenu)}
                className="px-3 py-1.5 bg-slate-700 hover:bg-slate-600 rounded-lg text-sm font-medium transition-colors flex items-center gap-2"
              >
                More
                <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 9l-7 7-7-7" />
                </svg>
              </button>
              
              {showBulkMenu && (
                <div className="absolute right-0 top-full mt-2 w-56 bg-slate-800 border border-slate-700 rounded-lg shadow-xl z-50">
                  <div className="p-2">
                    <button
                      onClick={() => { setBulkAssignType('flightsheet'); setShowBulkMenu(false); }}
                      className="w-full text-left px-3 py-2 text-sm hover:bg-slate-700 rounded-lg transition-colors"
                    >
                      Assign Flight Sheet
                    </button>
                    <button
                      onClick={() => { setBulkAssignType('oc'); setShowBulkMenu(false); }}
                      className="w-full text-left px-3 py-2 text-sm hover:bg-slate-700 rounded-lg transition-colors"
                    >
                      Assign OC Profile
                    </button>
                    <button
                      onClick={() => { setBulkAssignType('group'); setShowBulkMenu(false); }}
                      className="w-full text-left px-3 py-2 text-sm hover:bg-slate-700 rounded-lg transition-colors"
                    >
                      Assign to Group
                    </button>
                    <hr className="my-2 border-slate-700" />
                    <button
                      onClick={handleBulkReboot}
                      className="w-full text-left px-3 py-2 text-sm text-red-400 hover:bg-red-500/10 rounded-lg transition-colors"
                    >
                      Reboot Rigs
                    </button>
                  </div>
                </div>
              )}
            </div>
          </div>
        </div>
      )}

      {/* Bulk Assign Modal */}
      {bulkAssignType && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <div className="bg-slate-800 border border-slate-700 rounded-xl p-6 w-96">
            <h3 className="text-lg font-semibold mb-4">
              {bulkAssignType === 'flightsheet' && 'Assign Flight Sheet'}
              {bulkAssignType === 'oc' && 'Assign OC Profile'}
              {bulkAssignType === 'group' && 'Assign to Group'}
            </h3>
            <p className="text-sm text-slate-400 mb-4">
              Applying to {selectedRigs.size} rig(s)
            </p>
            
            {bulkAssignType === 'flightsheet' && (
              <div className="space-y-2">
                <button
                  onClick={() => handleBulkAssignFlightSheet(null)}
                  className="w-full text-left px-4 py-3 bg-slate-700/50 hover:bg-slate-700 rounded-lg transition-colors"
                >
                  <span className="text-slate-400">-- Remove Flight Sheet --</span>
                </button>
                {flightSheets.map(fs => (
                  <button
                    key={fs.id}
                    onClick={() => handleBulkAssignFlightSheet(fs.id)}
                    className="w-full text-left px-4 py-3 bg-slate-700/50 hover:bg-slate-700 rounded-lg transition-colors"
                  >
                    {fs.name} <span className="text-slate-400">({fs.coin})</span>
                  </button>
                ))}
              </div>
            )}
            
            {bulkAssignType === 'oc' && (
              <div className="space-y-2">
                <button
                  onClick={() => handleBulkAssignOcProfile(null)}
                  className="w-full text-left px-4 py-3 bg-slate-700/50 hover:bg-slate-700 rounded-lg transition-colors"
                >
                  <span className="text-slate-400">-- Remove OC Profile --</span>
                </button>
                {ocProfiles.map(p => (
                  <button
                    key={p.id}
                    onClick={() => handleBulkAssignOcProfile(p.id)}
                    className="w-full text-left px-4 py-3 bg-slate-700/50 hover:bg-slate-700 rounded-lg transition-colors"
                  >
                    {p.name} <span className="text-slate-400">({p.vendor})</span>
                  </button>
                ))}
              </div>
            )}
            
            {bulkAssignType === 'group' && (
              <div className="space-y-2">
                <p className="text-sm text-slate-400 mb-2">Add to group:</p>
                {rigGroups.map(g => (
                  <button
                    key={g.id}
                    onClick={() => handleBulkAddToGroup(g.id)}
                    className="w-full text-left px-4 py-3 bg-slate-700/50 hover:bg-slate-700 rounded-lg transition-colors flex items-center gap-3"
                  >
                    <span className="w-3 h-3 rounded-full" style={{ backgroundColor: g.color }}></span>
                    {g.name}
                  </button>
                ))}
                <hr className="border-slate-700 my-2" />
                <p className="text-sm text-slate-400 mb-2">Remove from group:</p>
                {rigGroups.map(g => (
                  <button
                    key={`remove-${g.id}`}
                    onClick={() => handleBulkRemoveFromGroup(g.id)}
                    className="w-full text-left px-4 py-3 bg-red-500/10 hover:bg-red-500/20 rounded-lg transition-colors flex items-center gap-3 text-red-400"
                  >
                    <span className="w-3 h-3 rounded-full" style={{ backgroundColor: g.color }}></span>
                    {g.name}
                  </button>
                ))}
              </div>
            )}
            
            <button
              onClick={() => setBulkAssignType(null)}
              className="mt-4 w-full px-4 py-2 bg-slate-700 hover:bg-slate-600 rounded-lg text-sm transition-colors"
            >
              Cancel
            </button>
          </div>
        </div>
      )}

      {/* Filter Tabs */}
      <div className="flex flex-wrap items-center gap-2 mb-6">
        {/* Status Filters */}
        <button
          onClick={() => setFilter('all')}
          className={`px-4 py-2 rounded-lg text-sm font-medium transition-all ${
            filter === 'all'
              ? 'bg-slate-700 text-white'
              : 'text-slate-400 hover:bg-slate-800 hover:text-white'
          }`}
        >
          All ({rigs.length})
        </button>
        <button
          onClick={() => setFilter('online')}
          className={`px-4 py-2 rounded-lg text-sm font-medium transition-all ${
            filter === 'online'
              ? 'bg-green-500/20 text-green-400 ring-1 ring-green-500/30'
              : 'text-slate-400 hover:bg-slate-800 hover:text-white'
          }`}
        >
          Online ({onlineCount})
        </button>
        <button
          onClick={() => setFilter('offline')}
          className={`px-4 py-2 rounded-lg text-sm font-medium transition-all ${
            filter === 'offline'
              ? 'bg-red-500/20 text-red-400 ring-1 ring-red-500/30'
              : 'text-slate-400 hover:bg-slate-800 hover:text-white'
          }`}
        >
          Offline ({offlineCount})
        </button>

        {/* Divider */}
        <div className="w-px h-6 bg-slate-700 mx-2"></div>

        {/* Group Filter */}
        <span className="text-sm text-slate-500">Group:</span>
        <button
          onClick={() => setGroupFilter(null)}
          className={`px-3 py-1.5 rounded-lg text-sm font-medium transition-all ${
            groupFilter === null
              ? 'bg-slate-700 text-white'
              : 'text-slate-400 hover:bg-slate-800 hover:text-white'
          }`}
        >
          All Groups
        </button>
        {rigGroups.map(g => (
          <button
            key={g.id}
            onClick={() => setGroupFilter(g.id)}
            className={`px-3 py-1.5 rounded-lg text-sm font-medium transition-all flex items-center gap-2 ${
              groupFilter === g.id
                ? 'ring-1'
                : 'hover:bg-slate-800'
            }`}
            style={groupFilter === g.id 
              ? { backgroundColor: `${g.color}20`, color: g.color, borderColor: g.color }
              : {}
            }
          >
            <span className="w-2 h-2 rounded-full" style={{ backgroundColor: g.color }}></span>
            {g.name}
          </button>
        ))}
      </div>

      {loading ? (
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-12 text-center">
          <div className="inline-block w-8 h-8 border-2 border-slate-600 border-t-blox-400 rounded-full animate-spin"></div>
          <p className="text-slate-400 mt-3">Loading rigs...</p>
        </div>
      ) : filteredRigs.length === 0 ? (
        <div className="bg-slate-800/50 backdrop-blur-sm rounded-xl border border-slate-700/50 p-12 text-center">
          <div className="w-16 h-16 mx-auto mb-4 rounded-full bg-slate-700/50 flex items-center justify-center">
            <svg className="w-8 h-8 text-slate-500" fill="none" stroke="currentColor" viewBox="0 0 24 24">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={1.5} d="M9 3v2m6-2v2M9 19v2m6-2v2M5 9H3m2 6H3m18-6h-2m2 6h-2M7 19h10a2 2 0 002-2V7a2 2 0 00-2-2H7a2 2 0 00-2 2v10a2 2 0 002 2zM9 9h6v6H9V9z" />
            </svg>
          </div>
          <p className="text-slate-400 mb-4">
            {filter === 'all' ? 'No rigs found. Add your first rig to get started.' : `No ${filter} rigs.`}
          </p>
          {filter === 'all' && (
            <Link
              href="/rigs/add"
              className="inline-block px-4 py-2 bg-blox-600 hover:bg-blox-700 rounded-lg text-white font-medium transition-colors"
            >
              Add Rig
            </Link>
          )}
        </div>
      ) : (
        <div className="space-y-4">
          {/* Select All */}
          <div className="flex items-center gap-3 px-2">
            <input
              type="checkbox"
              checked={selectedRigs.size === filteredRigs.length && filteredRigs.length > 0}
              onChange={toggleSelectAll}
              className="w-4 h-4 rounded border-slate-600 bg-slate-700 text-blox-500 focus:ring-blox-500"
            />
            <span className="text-sm text-slate-400">Select All</span>
          </div>

          {/* Rig List */}
          <div className="grid gap-4">
            {filteredRigs.map((rig) => {
              const hashrate = getTotalHashrate(rig.gpus);
              const power = getTotalPower(rig);
              const maxTemp = getMaxTemp(rig.gpus);
              const isSelected = selectedRigs.has(rig.id);
              
              return (
                <div
                  key={rig.id}
                  className={`bg-slate-800/50 backdrop-blur-sm rounded-xl border p-5 transition-all duration-200 ${
                    isSelected 
                      ? 'border-blox-500/50 bg-blox-500/5' 
                      : 'border-slate-700/50 hover:border-slate-600/50 hover:bg-slate-800/80'
                  }`}
                >
                  {/* Top Row: Checkbox, Name, Status, Actions */}
                  <div className="flex items-start justify-between mb-4">
                    <div className="flex items-center gap-3">
                      <input
                        type="checkbox"
                        checked={isSelected}
                        onChange={() => toggleRigSelection(rig.id)}
                        onClick={(e) => e.stopPropagation()}
                        className="w-4 h-4 rounded border-slate-600 bg-slate-700 text-blox-500 focus:ring-blox-500"
                      />
                      <div className={`w-3 h-3 rounded-full ${getStatusColor(rig.status)} shadow-lg`}></div>
                      <Link href={`/rigs/${rig.id}`} className="group">
                        <h3 className="text-lg font-semibold group-hover:text-blox-400 transition-colors">
                          {rig.name}
                        </h3>
                        <p className="text-sm text-slate-400">
                          {rig.hostname} {rig.ipAddress && `• ${rig.ipAddress}`}
                        </p>
                      </Link>
                      {rig.groups && rig.groups.length > 0 && (
                        <div className="flex flex-wrap gap-1">
                          {rig.groups.map(g => (
                            <span 
                              key={g.id}
                              className="px-2 py-0.5 rounded text-xs font-medium"
                              style={{ backgroundColor: `${g.color}20`, color: g.color }}
                            >
                              {g.name}
                            </span>
                          ))}
                        </div>
                      )}
                    </div>
                    <div className="flex items-center gap-3">
                      <span className={`px-2.5 py-1 rounded-lg text-xs font-medium ${getStatusBadge(rig.status)}`}>
                        {rig.status}
                      </span>
                      <Link
                        href={`/rigs/${rig.id}`}
                        className="p-2 rounded-lg text-slate-500 hover:text-blox-400 hover:bg-blox-500/10 transition-colors"
                      >
                        <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M9 5l7 7-7 7" />
                        </svg>
                      </Link>
                      <button
                        onClick={(e) => handleDelete(e, rig.id, rig.name)}
                        className="p-2 rounded-lg text-slate-500 hover:text-red-400 hover:bg-red-500/10 transition-colors"
                      >
                        <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                          <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M19 7l-.867 12.142A2 2 0 0116.138 21H7.862a2 2 0 01-1.995-1.858L5 7m5 4v6m4-6v6m1-10V4a1 1 0 00-1-1h-4a1 1 0 00-1 1v3M4 7h16" />
                        </svg>
                      </button>
                    </div>
                  </div>

                  {/* Stats Row */}
                  <div className="grid grid-cols-2 md:grid-cols-5 gap-4 mb-4">
                    <div className="bg-slate-900/50 rounded-lg p-3">
                      <p className="text-xs text-slate-500 uppercase tracking-wider mb-1">GPUs</p>
                      <p className="text-lg font-semibold">{rig.gpus.length}</p>
                    </div>
                    <div className="bg-slate-900/50 rounded-lg p-3">
                      <p className="text-xs text-slate-500 uppercase tracking-wider mb-1">Hashrate</p>
                      <p className="text-lg font-semibold text-purple-400">{hashrate.toFixed(1)} <span className="text-xs text-slate-400">MH/s</span></p>
                    </div>
                    <div className="bg-slate-900/50 rounded-lg p-3">
                      <p className="text-xs text-slate-500 uppercase tracking-wider mb-1">Power</p>
                      <p className="text-lg font-semibold">{power.toFixed(0)} <span className="text-xs text-slate-400">W</span></p>
                    </div>
                    <div className="bg-slate-900/50 rounded-lg p-3">
                      <p className="text-xs text-slate-500 uppercase tracking-wider mb-1">Max Temp</p>
                      <p className={`text-lg font-semibold ${maxTemp && maxTemp > 80 ? 'text-red-400' : maxTemp && maxTemp > 70 ? 'text-yellow-400' : ''}`}>
                        {maxTemp !== null ? `${maxTemp}°C` : 'N/A'}
                      </p>
                    </div>
                    <div className="bg-slate-900/50 rounded-lg p-3">
                      <p className="text-xs text-slate-500 uppercase tracking-wider mb-1">Last Seen</p>
                      <p className="text-lg font-semibold text-slate-300">{formatLastSeen(rig.lastSeen)}</p>
                    </div>
                  </div>

                  {/* GPU Tags */}
                  {rig.gpus.length > 0 && (
                    <div className="flex flex-wrap gap-2 pt-3 border-t border-slate-700/50">
                      {rig.gpus.map((gpu) => (
                        <div
                          key={gpu.id}
                          className="flex items-center gap-2 px-3 py-1.5 bg-slate-700/50 rounded-lg text-xs"
                        >
                          <span className="text-slate-400">GPU{gpu.index}</span>
                          <span className="text-slate-300">{gpu.name.replace('NVIDIA ', '').replace('AMD ', '')}</span>
                          {gpu.temperature !== null && (
                            <span className={`font-medium ${gpu.temperature > 80 ? 'text-red-400' : gpu.temperature > 70 ? 'text-yellow-400' : 'text-green-400'}`}>
                              {gpu.temperature}°C
                            </span>
                          )}
                        </div>
                      ))}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </div>
      )}
    </div>
  );
}
