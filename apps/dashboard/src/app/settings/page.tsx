'use client';

import { useState, useEffect, useCallback } from 'react';
import { useAuth } from '../../context/auth';

const getApiUrl = () => {
  if (typeof window === 'undefined') return 'http://localhost:3001';
  return `http://${window.location.hostname}:3001`;
};

interface MinerUpdate {
  minerName: string;
  displayName: string;
  currentVersion: string;
  latestVersion: string;
  releaseUrl: string;
  publishedAt: string;
  hasUpdate: boolean;
}

interface UpdatesData {
  lastCheckedAt: string | null;
  miners: MinerUpdate[];
  hasUpdates: boolean;
  updateCount: number;
}

export default function SettingsPage() {
  const { user, refreshUser } = useAuth();
  
  // Profile form
  const [name, setName] = useState(user?.name || '');
  const [email, setEmail] = useState(user?.email || '');
  const [profileMessage, setProfileMessage] = useState<{ type: 'success' | 'error'; text: string } | null>(null);
  const [profileLoading, setProfileLoading] = useState(false);

  // Password form
  const [currentPassword, setCurrentPassword] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [passwordMessage, setPasswordMessage] = useState<{ type: 'success' | 'error'; text: string } | null>(null);
  const [passwordLoading, setPasswordLoading] = useState(false);

  // Miner updates
  const [updates, setUpdates] = useState<UpdatesData | null>(null);
  const [updatesLoading, setUpdatesLoading] = useState(false);
  const [refreshingUpdates, setRefreshingUpdates] = useState(false);

  const fetchUpdates = useCallback(async (refresh = false) => {
    if (refresh) {
      setRefreshingUpdates(true);
    } else {
      setUpdatesLoading(true);
    }

    try {
      const endpoint = refresh ? '/api/updates/miners/refresh' : '/api/updates/miners/cached';
      const method = refresh ? 'POST' : 'GET';
      
      const res = await fetch(`${getApiUrl()}${endpoint}`, {
        method,
        credentials: 'include',
      });

      if (res.ok) {
        const data = await res.json();
        setUpdates(data);
      }
    } catch (err) {
      console.error('Failed to fetch updates:', err);
    } finally {
      setUpdatesLoading(false);
      setRefreshingUpdates(false);
    }
  }, []);

  useEffect(() => {
    fetchUpdates();
  }, [fetchUpdates]);

  const handleProfileUpdate = async (e: React.FormEvent) => {
    e.preventDefault();
    setProfileMessage(null);
    setProfileLoading(true);

    try {
      const res = await fetch(`${getApiUrl()}/api/auth/me`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ name, email }),
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || 'Failed to update profile');
      }

      await refreshUser();
      setProfileMessage({ type: 'success', text: 'Profile updated successfully' });
    } catch (err) {
      setProfileMessage({ type: 'error', text: err instanceof Error ? err.message : 'Update failed' });
    } finally {
      setProfileLoading(false);
    }
  };

  const handlePasswordChange = async (e: React.FormEvent) => {
    e.preventDefault();
    setPasswordMessage(null);

    if (newPassword !== confirmPassword) {
      setPasswordMessage({ type: 'error', text: 'New passwords do not match' });
      return;
    }

    if (newPassword.length < 6) {
      setPasswordMessage({ type: 'error', text: 'Password must be at least 6 characters' });
      return;
    }

    setPasswordLoading(true);

    try {
      const res = await fetch(`${getApiUrl()}/api/auth/change-password`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ currentPassword, newPassword }),
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || 'Failed to change password');
      }

      setCurrentPassword('');
      setNewPassword('');
      setConfirmPassword('');
      setPasswordMessage({ type: 'success', text: 'Password changed successfully' });
    } catch (err) {
      setPasswordMessage({ type: 'error', text: err instanceof Error ? err.message : 'Password change failed' });
    } finally {
      setPasswordLoading(false);
    }
  };

  return (
    <div className="space-y-6">
      {/* Header */}
      <div>
        <h1 className="text-2xl font-bold">Settings</h1>
        <p className="text-slate-400 mt-1">Manage your account settings</p>
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
        {/* Profile Settings */}
        <div className="bg-slate-800/50 backdrop-blur-sm border border-slate-700/50 rounded-xl p-6">
          <h2 className="text-lg font-semibold mb-4">Profile</h2>
          
          <form onSubmit={handleProfileUpdate} className="space-y-4">
            {profileMessage && (
              <div className={`px-4 py-3 rounded-lg text-sm ${
                profileMessage.type === 'success' 
                  ? 'bg-green-500/10 border border-green-500/50 text-green-400'
                  : 'bg-red-500/10 border border-red-500/50 text-red-400'
              }`}>
                {profileMessage.text}
              </div>
            )}

            <div>
              <label htmlFor="name" className="block text-sm font-medium text-slate-300 mb-2">
                Name
              </label>
              <input
                id="name"
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="w-full px-4 py-2.5 bg-slate-700/50 border border-slate-600/50 rounded-lg text-white placeholder-slate-400 focus:outline-none focus:ring-2 focus:ring-blox-500/50 focus:border-blox-500 transition-colors"
                placeholder="Your name"
              />
            </div>

            <div>
              <label htmlFor="email" className="block text-sm font-medium text-slate-300 mb-2">
                Email
              </label>
              <input
                id="email"
                type="email"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                className="w-full px-4 py-2.5 bg-slate-700/50 border border-slate-600/50 rounded-lg text-white placeholder-slate-400 focus:outline-none focus:ring-2 focus:ring-blox-500/50 focus:border-blox-500 transition-colors"
                placeholder="your@email.com"
                required
              />
            </div>

            <div className="pt-2">
              <button
                type="submit"
                disabled={profileLoading}
                className="px-4 py-2.5 bg-blox-600 hover:bg-blox-700 text-white font-medium rounded-lg transition-colors disabled:opacity-50"
              >
                {profileLoading ? 'Saving...' : 'Save Changes'}
              </button>
            </div>
          </form>
        </div>

        {/* Change Password */}
        <div className="bg-slate-800/50 backdrop-blur-sm border border-slate-700/50 rounded-xl p-6">
          <h2 className="text-lg font-semibold mb-4">Change Password</h2>
          
          <form onSubmit={handlePasswordChange} className="space-y-4">
            {passwordMessage && (
              <div className={`px-4 py-3 rounded-lg text-sm ${
                passwordMessage.type === 'success' 
                  ? 'bg-green-500/10 border border-green-500/50 text-green-400'
                  : 'bg-red-500/10 border border-red-500/50 text-red-400'
              }`}>
                {passwordMessage.text}
              </div>
            )}

            <div>
              <label htmlFor="currentPassword" className="block text-sm font-medium text-slate-300 mb-2">
                Current Password
              </label>
              <input
                id="currentPassword"
                type="password"
                value={currentPassword}
                onChange={(e) => setCurrentPassword(e.target.value)}
                className="w-full px-4 py-2.5 bg-slate-700/50 border border-slate-600/50 rounded-lg text-white placeholder-slate-400 focus:outline-none focus:ring-2 focus:ring-blox-500/50 focus:border-blox-500 transition-colors"
                placeholder="Enter current password"
                required
              />
            </div>

            <div>
              <label htmlFor="newPassword" className="block text-sm font-medium text-slate-300 mb-2">
                New Password
              </label>
              <input
                id="newPassword"
                type="password"
                value={newPassword}
                onChange={(e) => setNewPassword(e.target.value)}
                className="w-full px-4 py-2.5 bg-slate-700/50 border border-slate-600/50 rounded-lg text-white placeholder-slate-400 focus:outline-none focus:ring-2 focus:ring-blox-500/50 focus:border-blox-500 transition-colors"
                placeholder="Minimum 6 characters"
                required
              />
            </div>

            <div>
              <label htmlFor="confirmPassword" className="block text-sm font-medium text-slate-300 mb-2">
                Confirm New Password
              </label>
              <input
                id="confirmPassword"
                type="password"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                className="w-full px-4 py-2.5 bg-slate-700/50 border border-slate-600/50 rounded-lg text-white placeholder-slate-400 focus:outline-none focus:ring-2 focus:ring-blox-500/50 focus:border-blox-500 transition-colors"
                placeholder="Re-enter new password"
                required
              />
            </div>

            <div className="pt-2">
              <button
                type="submit"
                disabled={passwordLoading}
                className="px-4 py-2.5 bg-blox-600 hover:bg-blox-700 text-white font-medium rounded-lg transition-colors disabled:opacity-50"
              >
                {passwordLoading ? 'Changing...' : 'Change Password'}
              </button>
            </div>
          </form>
        </div>
      </div>

      {/* Miner Updates */}
      <div className="bg-slate-800/50 backdrop-blur-sm border border-slate-700/50 rounded-xl p-6">
        <div className="flex items-center justify-between mb-4">
          <div>
            <h2 className="text-lg font-semibold">Miner Updates</h2>
            <p className="text-sm text-slate-400 mt-1">
              {updates?.lastCheckedAt 
                ? `Last checked: ${new Date(updates.lastCheckedAt).toLocaleString()}`
                : 'Not checked yet'}
            </p>
          </div>
          {user?.role === 'ADMIN' && (
            <button
              onClick={() => fetchUpdates(true)}
              disabled={refreshingUpdates}
              className="flex items-center gap-2 px-4 py-2 bg-slate-700 hover:bg-slate-600 rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
            >
              {refreshingUpdates ? (
                <>
                  <span className="w-4 h-4 border-2 border-slate-500 border-t-white rounded-full animate-spin"></span>
                  Checking...
                </>
              ) : (
                <>
                  <svg className="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                    <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
                  </svg>
                  Check Now
                </>
              )}
            </button>
          )}
        </div>

        {updatesLoading ? (
          <div className="flex items-center justify-center py-8">
            <span className="w-6 h-6 border-2 border-slate-600 border-t-blox-400 rounded-full animate-spin"></span>
          </div>
        ) : updates?.miners && updates.miners.length > 0 ? (
          <div className="space-y-3">
            {updates.hasUpdates && (
              <div className="flex items-center gap-2 px-4 py-3 bg-amber-500/10 border border-amber-500/30 rounded-lg text-amber-400 text-sm mb-4">
                <svg className="w-5 h-5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
                  <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M12 9v2m0 4h.01m-6.938 4h13.856c1.54 0 2.502-1.667 1.732-3L13.732 4c-.77-1.333-2.694-1.333-3.464 0L3.34 16c-.77 1.333.192 3 1.732 3z" />
                </svg>
                {updates.updateCount} miner update{updates.updateCount > 1 ? 's' : ''} available
              </div>
            )}
            
            <div className="overflow-x-auto">
              <table className="w-full">
                <thead>
                  <tr className="text-left text-sm text-slate-400 border-b border-slate-700">
                    <th className="pb-3 font-medium">Miner</th>
                    <th className="pb-3 font-medium">Current</th>
                    <th className="pb-3 font-medium">Latest</th>
                    <th className="pb-3 font-medium">Status</th>
                  </tr>
                </thead>
                <tbody className="divide-y divide-slate-700/50">
                  {updates.miners.map((miner) => (
                    <tr key={miner.minerName} className="text-sm">
                      <td className="py-3 font-medium">{miner.displayName}</td>
                      <td className="py-3 font-mono text-slate-400">{miner.currentVersion}</td>
                      <td className="py-3">
                        <a 
                          href={miner.releaseUrl} 
                          target="_blank" 
                          rel="noopener noreferrer"
                          className="font-mono text-blox-400 hover:text-blox-300"
                        >
                          {miner.latestVersion}
                        </a>
                      </td>
                      <td className="py-3">
                        {miner.hasUpdate ? (
                          <span className="px-2 py-1 bg-amber-500/20 text-amber-400 text-xs font-medium rounded">
                            Update Available
                          </span>
                        ) : (
                          <span className="px-2 py-1 bg-green-500/20 text-green-400 text-xs font-medium rounded">
                            Up to Date
                          </span>
                        )}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        ) : (
          <div className="text-center py-8 text-slate-400">
            <p>No miner update information available.</p>
            {user?.role === 'ADMIN' && (
              <p className="text-sm mt-2">Click &quot;Check Now&quot; to fetch latest versions.</p>
            )}
          </div>
        )}
      </div>

      {/* Account Info */}
      <div className="bg-slate-800/50 backdrop-blur-sm border border-slate-700/50 rounded-xl p-6">
        <h2 className="text-lg font-semibold mb-4">Account Information</h2>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <div>
            <p className="text-sm text-slate-400">User ID</p>
            <p className="font-mono text-sm mt-1">{user?.id}</p>
          </div>
          <div>
            <p className="text-sm text-slate-400">Role</p>
            <p className="mt-1">
              <span className={`px-2 py-1 text-xs font-medium rounded ${
                user?.role === 'ADMIN' 
                  ? 'bg-purple-500/20 text-purple-400' 
                  : 'bg-slate-500/20 text-slate-400'
              }`}>
                {user?.role}
              </span>
            </p>
          </div>
          <div>
            <p className="text-sm text-slate-400">Email</p>
            <p className="mt-1">{user?.email}</p>
          </div>
        </div>
      </div>
    </div>
  );
}
