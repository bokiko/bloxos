'use client';

import { useState } from 'react';
import { useAuth } from '../../context/auth';

const getApiUrl = () => {
  if (typeof window === 'undefined') return 'http://localhost:3001';
  return `http://${window.location.hostname}:3001`;
};

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
