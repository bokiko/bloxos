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

  // Notification settings
  const [notificationSettings, setNotificationSettings] = useState({
    emailEnabled: false,
    emailAddress: '',
    telegramEnabled: false,
    telegramChatId: '',
    notifyOnOffline: true,
    notifyOnHighTemp: true,
    notifyOnLowHashrate: true,
    notifyOnMinerError: true,
    tempThreshold: 85,
    hashrateDropPercent: 20,
  });
  const [notificationMessage, setNotificationMessage] = useState<{ type: 'success' | 'error'; text: string } | null>(null);
  const [notificationLoading, setNotificationLoading] = useState(false);
  const [testingNotification, setTestingNotification] = useState<'email' | 'telegram' | null>(null);

  // Electricity settings
  const [electricityRate, setElectricityRate] = useState(0.10);
  const [electricityMessage, setElectricityMessage] = useState<{ type: 'success' | 'error'; text: string } | null>(null);
  const [electricityLoading, setElectricityLoading] = useState(false);

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
    fetchNotificationSettings();
    fetchElectricitySettings();
  }, [fetchUpdates]);

  async function fetchNotificationSettings() {
    try {
      const res = await fetch(`${getApiUrl()}/api/settings/notifications`, {
        credentials: 'include',
      });
      if (res.ok) {
        const data = await res.json();
        setNotificationSettings(prev => ({ ...prev, ...data }));
      }
    } catch (err) {
      console.error('Failed to fetch notification settings');
    }
  }

  async function handleSaveNotifications() {
    setNotificationMessage(null);
    setNotificationLoading(true);

    try {
      const res = await fetch(`${getApiUrl()}/api/settings/notifications`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify(notificationSettings),
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || 'Failed to save settings');
      }

      setNotificationMessage({ type: 'success', text: 'Notification settings saved' });
    } catch (err) {
      setNotificationMessage({ type: 'error', text: err instanceof Error ? err.message : 'Save failed' });
    } finally {
      setNotificationLoading(false);
    }
  }

  async function handleTestNotification(type: 'email' | 'telegram') {
    setTestingNotification(type);
    setNotificationMessage(null);

    try {
      const res = await fetch(`${getApiUrl()}/api/settings/notifications/test`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ type }),
      });

      const data = await res.json();
      if (!res.ok) {
        throw new Error(data.error || 'Test failed');
      }

      setNotificationMessage({ type: 'success', text: `Test ${type} notification sent!` });
    } catch (err) {
      setNotificationMessage({ type: 'error', text: err instanceof Error ? err.message : 'Test failed' });
    } finally {
      setTestingNotification(null);
    }
  }

  async function fetchElectricitySettings() {
    try {
      const res = await fetch(`${getApiUrl()}/api/profit/settings`, {
        credentials: 'include',
      });
      if (res.ok) {
        const data = await res.json();
        setElectricityRate(data.rate || 0.10);
      }
    } catch (err) {
      console.error('Failed to fetch electricity settings');
    }
  }

  async function handleSaveElectricity() {
    setElectricityMessage(null);
    setElectricityLoading(true);

    try {
      const res = await fetch(`${getApiUrl()}/api/profit/settings`, {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ rate: electricityRate, currency: 'USD' }),
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || 'Failed to save settings');
      }

      setElectricityMessage({ type: 'success', text: 'Electricity rate saved' });
    } catch (err) {
      setElectricityMessage({ type: 'error', text: err instanceof Error ? err.message : 'Save failed' });
    } finally {
      setElectricityLoading(false);
    }
  }

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

      {/* Notification Settings */}
      <div className="bg-slate-800/50 backdrop-blur-sm border border-slate-700/50 rounded-xl p-6">
        <h2 className="text-lg font-semibold mb-4">Notification Settings</h2>
        <p className="text-sm text-slate-400 mb-6">Configure how you receive alerts about your rigs</p>

        {notificationMessage && (
          <div className={`px-4 py-3 rounded-lg text-sm mb-6 ${
            notificationMessage.type === 'success' 
              ? 'bg-green-500/10 border border-green-500/50 text-green-400'
              : 'bg-red-500/10 border border-red-500/50 text-red-400'
          }`}>
            {notificationMessage.text}
          </div>
        )}

        <div className="grid grid-cols-1 lg:grid-cols-2 gap-6">
          {/* Email Notifications */}
          <div className="space-y-4">
            <div className="flex items-center justify-between">
              <div>
                <h3 className="font-medium">Email Notifications</h3>
                <p className="text-sm text-slate-400">Receive alerts via email</p>
              </div>
              <button
                type="button"
                onClick={() => setNotificationSettings(prev => ({ ...prev, emailEnabled: !prev.emailEnabled }))}
                className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
                  notificationSettings.emailEnabled ? 'bg-blox-600' : 'bg-slate-600'
                }`}
              >
                <span className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                  notificationSettings.emailEnabled ? 'translate-x-6' : 'translate-x-1'
                }`} />
              </button>
            </div>

            {notificationSettings.emailEnabled && (
              <div className="space-y-3">
                <div>
                  <label className="block text-sm text-slate-400 mb-1">Email Address</label>
                  <div className="flex gap-2">
                    <input
                      type="email"
                      value={notificationSettings.emailAddress}
                      onChange={(e) => setNotificationSettings(prev => ({ ...prev, emailAddress: e.target.value }))}
                      className="flex-1 px-3 py-2 bg-slate-700/50 border border-slate-600/50 rounded-lg text-white text-sm focus:outline-none focus:ring-2 focus:ring-blox-500/50"
                      placeholder="alerts@example.com"
                    />
                    <button
                      type="button"
                      onClick={() => handleTestNotification('email')}
                      disabled={testingNotification === 'email' || !notificationSettings.emailAddress}
                      className="px-3 py-2 bg-slate-600 hover:bg-slate-500 rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
                    >
                      {testingNotification === 'email' ? 'Sending...' : 'Test'}
                    </button>
                  </div>
                </div>
              </div>
            )}
          </div>

          {/* Telegram Notifications */}
          <div className="space-y-4">
            <div className="flex items-center justify-between">
              <div>
                <h3 className="font-medium">Telegram Notifications</h3>
                <p className="text-sm text-slate-400">Receive alerts via Telegram</p>
              </div>
              <button
                type="button"
                onClick={() => setNotificationSettings(prev => ({ ...prev, telegramEnabled: !prev.telegramEnabled }))}
                className={`relative inline-flex h-6 w-11 items-center rounded-full transition-colors ${
                  notificationSettings.telegramEnabled ? 'bg-blox-600' : 'bg-slate-600'
                }`}
              >
                <span className={`inline-block h-4 w-4 transform rounded-full bg-white transition-transform ${
                  notificationSettings.telegramEnabled ? 'translate-x-6' : 'translate-x-1'
                }`} />
              </button>
            </div>

            {notificationSettings.telegramEnabled && (
              <div className="space-y-3">
                <div>
                  <label className="block text-sm text-slate-400 mb-1">Telegram Chat ID</label>
                  <div className="flex gap-2">
                    <input
                      type="text"
                      value={notificationSettings.telegramChatId}
                      onChange={(e) => setNotificationSettings(prev => ({ ...prev, telegramChatId: e.target.value }))}
                      className="flex-1 px-3 py-2 bg-slate-700/50 border border-slate-600/50 rounded-lg text-white text-sm focus:outline-none focus:ring-2 focus:ring-blox-500/50"
                      placeholder="Your chat ID"
                    />
                    <button
                      type="button"
                      onClick={() => handleTestNotification('telegram')}
                      disabled={testingNotification === 'telegram' || !notificationSettings.telegramChatId}
                      className="px-3 py-2 bg-slate-600 hover:bg-slate-500 rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
                    >
                      {testingNotification === 'telegram' ? 'Sending...' : 'Test'}
                    </button>
                  </div>
                  <p className="text-xs text-slate-500 mt-1">
                    Message @BloxOsBot on Telegram to get your Chat ID
                  </p>
                </div>
              </div>
            )}
          </div>
        </div>

        {/* Alert Types */}
        <div className="mt-6 pt-6 border-t border-slate-700/50">
          <h3 className="font-medium mb-4">Alert Types</h3>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
            <label className="flex items-center gap-3 cursor-pointer">
              <input
                type="checkbox"
                checked={notificationSettings.notifyOnOffline}
                onChange={(e) => setNotificationSettings(prev => ({ ...prev, notifyOnOffline: e.target.checked }))}
                className="w-4 h-4 rounded border-slate-600 bg-slate-700 text-blox-600 focus:ring-blox-500/50"
              />
              <div>
                <span className="text-sm font-medium">Rig Offline</span>
                <p className="text-xs text-slate-400">Alert when a rig goes offline</p>
              </div>
            </label>

            <label className="flex items-center gap-3 cursor-pointer">
              <input
                type="checkbox"
                checked={notificationSettings.notifyOnHighTemp}
                onChange={(e) => setNotificationSettings(prev => ({ ...prev, notifyOnHighTemp: e.target.checked }))}
                className="w-4 h-4 rounded border-slate-600 bg-slate-700 text-blox-600 focus:ring-blox-500/50"
              />
              <div>
                <span className="text-sm font-medium">High Temperature</span>
                <p className="text-xs text-slate-400">Alert when GPU exceeds threshold</p>
              </div>
            </label>

            <label className="flex items-center gap-3 cursor-pointer">
              <input
                type="checkbox"
                checked={notificationSettings.notifyOnLowHashrate}
                onChange={(e) => setNotificationSettings(prev => ({ ...prev, notifyOnLowHashrate: e.target.checked }))}
                className="w-4 h-4 rounded border-slate-600 bg-slate-700 text-blox-600 focus:ring-blox-500/50"
              />
              <div>
                <span className="text-sm font-medium">Low Hashrate</span>
                <p className="text-xs text-slate-400">Alert on significant hashrate drop</p>
              </div>
            </label>

            <label className="flex items-center gap-3 cursor-pointer">
              <input
                type="checkbox"
                checked={notificationSettings.notifyOnMinerError}
                onChange={(e) => setNotificationSettings(prev => ({ ...prev, notifyOnMinerError: e.target.checked }))}
                className="w-4 h-4 rounded border-slate-600 bg-slate-700 text-blox-600 focus:ring-blox-500/50"
              />
              <div>
                <span className="text-sm font-medium">Miner Errors</span>
                <p className="text-xs text-slate-400">Alert on miner crashes or errors</p>
              </div>
            </label>
          </div>
        </div>

        {/* Thresholds */}
        <div className="mt-6 pt-6 border-t border-slate-700/50">
          <h3 className="font-medium mb-4">Thresholds</h3>
          <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
            <div>
              <label className="block text-sm text-slate-400 mb-2">
                Temperature Threshold: <span className="text-white font-medium">{notificationSettings.tempThreshold}C</span>
              </label>
              <input
                type="range"
                min="60"
                max="100"
                value={notificationSettings.tempThreshold}
                onChange={(e) => setNotificationSettings(prev => ({ ...prev, tempThreshold: parseInt(e.target.value) }))}
                className="w-full h-2 bg-slate-700 rounded-lg appearance-none cursor-pointer accent-blox-500"
              />
              <div className="flex justify-between text-xs text-slate-500 mt-1">
                <span>60C</span>
                <span>100C</span>
              </div>
            </div>

            <div>
              <label className="block text-sm text-slate-400 mb-2">
                Hashrate Drop: <span className="text-white font-medium">{notificationSettings.hashrateDropPercent}%</span>
              </label>
              <input
                type="range"
                min="5"
                max="50"
                step="5"
                value={notificationSettings.hashrateDropPercent}
                onChange={(e) => setNotificationSettings(prev => ({ ...prev, hashrateDropPercent: parseInt(e.target.value) }))}
                className="w-full h-2 bg-slate-700 rounded-lg appearance-none cursor-pointer accent-blox-500"
              />
              <div className="flex justify-between text-xs text-slate-500 mt-1">
                <span>5%</span>
                <span>50%</span>
              </div>
            </div>
          </div>
        </div>

        {/* Save Button */}
        <div className="mt-6 pt-6 border-t border-slate-700/50">
          <button
            type="button"
            onClick={handleSaveNotifications}
            disabled={notificationLoading}
            className="px-6 py-2.5 bg-blox-600 hover:bg-blox-700 text-white font-medium rounded-lg transition-colors disabled:opacity-50"
          >
            {notificationLoading ? 'Saving...' : 'Save Notification Settings'}
          </button>
        </div>
      </div>

      {/* Electricity Settings (for Profit Tracking) */}
      <div className="bg-slate-800/50 backdrop-blur-sm border border-slate-700/50 rounded-xl p-6">
        <h2 className="text-lg font-semibold mb-4">Electricity Settings</h2>
        <p className="text-slate-400 text-sm mb-4">
          Configure your electricity rate for accurate profit calculations on the Profit page.
        </p>

        {electricityMessage && (
          <div className={`px-4 py-3 rounded-lg text-sm mb-4 ${
            electricityMessage.type === 'success'
              ? 'bg-green-500/10 border border-green-500/50 text-green-400'
              : 'bg-red-500/10 border border-red-500/50 text-red-400'
          }`}>
            {electricityMessage.text}
          </div>
        )}

        <div className="max-w-md">
          <label htmlFor="electricityRate" className="block text-sm font-medium text-slate-300 mb-2">
            Electricity Rate ($/kWh)
          </label>
          <div className="flex items-center gap-4">
            <input
              id="electricityRate"
              type="number"
              step="0.01"
              min="0"
              max="1"
              value={electricityRate}
              onChange={(e) => setElectricityRate(parseFloat(e.target.value) || 0)}
              className="w-32 px-4 py-2.5 bg-slate-700/50 border border-slate-600/50 rounded-lg text-white placeholder-slate-400 focus:outline-none focus:ring-2 focus:ring-blox-500/50 focus:border-blox-500 transition-colors"
            />
            <span className="text-slate-400 text-sm">USD per kWh</span>
          </div>
          <p className="text-xs text-slate-500 mt-2">
            Typical rates: $0.05-0.15 residential, $0.03-0.08 commercial/industrial
          </p>
        </div>

        <div className="mt-6 pt-6 border-t border-slate-700/50">
          <button
            type="button"
            onClick={handleSaveElectricity}
            disabled={electricityLoading}
            className="px-6 py-2.5 bg-blox-600 hover:bg-blox-700 text-white font-medium rounded-lg transition-colors disabled:opacity-50"
          >
            {electricityLoading ? 'Saving...' : 'Save Electricity Rate'}
          </button>
        </div>
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
