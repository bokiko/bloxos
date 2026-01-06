'use client';

import { useState, useEffect } from 'react';
import { useAuth } from '../../context/auth';

const getApiUrl = () => {
  if (typeof window === 'undefined') return 'http://localhost:3001';
  return `http://${window.location.hostname}:3001`;
};

interface User {
  id: string;
  email: string;
  name: string | null;
  role: 'ADMIN' | 'USER' | 'MONITOR';
  createdAt: string;
}

export default function UsersPage() {
  const { user: currentUser } = useAuth();
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  // Modal states
  const [showAddModal, setShowAddModal] = useState(false);
  const [showEditModal, setShowEditModal] = useState(false);
  const [showResetModal, setShowResetModal] = useState(false);
  const [selectedUser, setSelectedUser] = useState<User | null>(null);

  // Form states
  const [formEmail, setFormEmail] = useState('');
  const [formName, setFormName] = useState('');
  const [formPassword, setFormPassword] = useState('');
  const [formRole, setFormRole] = useState<'ADMIN' | 'USER' | 'MONITOR'>('USER');
  const [formLoading, setFormLoading] = useState(false);
  const [formError, setFormError] = useState('');

  // Check if current user is admin
  const isAdmin = currentUser?.role === 'ADMIN';

  useEffect(() => {
    if (isAdmin) {
      fetchUsers();
    }
  }, [isAdmin]);

  const fetchUsers = async () => {
    try {
      const res = await fetch(`${getApiUrl()}/api/users`, {
        credentials: 'include',
      });
      if (!res.ok) throw new Error('Failed to fetch users');
      const data = await res.json();
      setUsers(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load users');
    } finally {
      setLoading(false);
    }
  };

  const handleAddUser = async (e: React.FormEvent) => {
    e.preventDefault();
    setFormError('');
    setFormLoading(true);

    try {
      const res = await fetch(`${getApiUrl()}/api/users`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({
          email: formEmail,
          password: formPassword,
          name: formName || undefined,
          role: formRole,
        }),
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || 'Failed to create user');
      }

      await fetchUsers();
      setShowAddModal(false);
      resetForm();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to create user');
    } finally {
      setFormLoading(false);
    }
  };

  const handleEditUser = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedUser) return;
    setFormError('');
    setFormLoading(true);

    try {
      const res = await fetch(`${getApiUrl()}/api/users/${selectedUser.id}`, {
        method: 'PATCH',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({
          email: formEmail,
          name: formName || undefined,
          role: formRole,
        }),
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || 'Failed to update user');
      }

      await fetchUsers();
      setShowEditModal(false);
      resetForm();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to update user');
    } finally {
      setFormLoading(false);
    }
  };

  const handleResetPassword = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!selectedUser) return;
    setFormError('');
    setFormLoading(true);

    try {
      const res = await fetch(`${getApiUrl()}/api/users/${selectedUser.id}/reset-password`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ password: formPassword }),
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || 'Failed to reset password');
      }

      setShowResetModal(false);
      resetForm();
    } catch (err) {
      setFormError(err instanceof Error ? err.message : 'Failed to reset password');
    } finally {
      setFormLoading(false);
    }
  };

  const handleDeleteUser = async (userId: string) => {
    if (!confirm('Are you sure you want to delete this user?')) return;

    try {
      const res = await fetch(`${getApiUrl()}/api/users/${userId}`, {
        method: 'DELETE',
        credentials: 'include',
      });

      if (!res.ok) {
        const data = await res.json();
        throw new Error(data.error || 'Failed to delete user');
      }

      await fetchUsers();
    } catch (err) {
      alert(err instanceof Error ? err.message : 'Failed to delete user');
    }
  };

  const openEditModal = (user: User) => {
    setSelectedUser(user);
    setFormEmail(user.email);
    setFormName(user.name || '');
    setFormRole(user.role);
    setShowEditModal(true);
  };

  const openResetModal = (user: User) => {
    setSelectedUser(user);
    setFormPassword('');
    setShowResetModal(true);
  };

  const resetForm = () => {
    setFormEmail('');
    setFormName('');
    setFormPassword('');
    setFormRole('USER');
    setFormError('');
    setSelectedUser(null);
  };

  if (!isAdmin) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="text-center">
          <h2 className="text-xl font-semibold text-red-400">Access Denied</h2>
          <p className="text-slate-400 mt-2">You need admin privileges to access this page.</p>
        </div>
      </div>
    );
  }

  if (loading) {
    return (
      <div className="flex items-center justify-center h-64">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blox-500"></div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">User Management</h1>
          <p className="text-slate-400 mt-1">Manage system users and permissions</p>
        </div>
        <button
          onClick={() => { resetForm(); setShowAddModal(true); }}
          className="px-4 py-2 bg-blox-600 hover:bg-blox-700 text-white font-medium rounded-lg transition-colors"
        >
          + Add User
        </button>
      </div>

      {error && (
        <div className="bg-red-500/10 border border-red-500/50 text-red-400 px-4 py-3 rounded-lg">
          {error}
        </div>
      )}

      {/* Users Table */}
      <div className="bg-slate-800/50 backdrop-blur-sm border border-slate-700/50 rounded-xl overflow-hidden">
        <table className="w-full">
          <thead className="bg-slate-700/30">
            <tr>
              <th className="px-6 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">User</th>
              <th className="px-6 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">Role</th>
              <th className="px-6 py-3 text-left text-xs font-medium text-slate-400 uppercase tracking-wider">Created</th>
              <th className="px-6 py-3 text-right text-xs font-medium text-slate-400 uppercase tracking-wider">Actions</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-700/50">
            {users.map((user) => (
              <tr key={user.id} className="hover:bg-slate-700/20">
                <td className="px-6 py-4">
                  <div className="flex items-center gap-3">
                    <div className="w-10 h-10 rounded-full bg-gradient-to-br from-blox-400 to-blox-600 flex items-center justify-center text-sm font-bold uppercase">
                      {user.name?.[0] || user.email[0]}
                    </div>
                    <div>
                      <p className="font-medium">{user.name || 'No name'}</p>
                      <p className="text-sm text-slate-400">{user.email}</p>
                    </div>
                  </div>
                </td>
                <td className="px-6 py-4">
                  <span className={`px-2 py-1 text-xs font-medium rounded ${
                    user.role === 'ADMIN' 
                      ? 'bg-purple-500/20 text-purple-400' 
                      : user.role === 'USER'
                      ? 'bg-blue-500/20 text-blue-400'
                      : 'bg-slate-500/20 text-slate-400'
                  }`}>
                    {user.role}
                  </span>
                </td>
                <td className="px-6 py-4 text-sm text-slate-400">
                  {new Date(user.createdAt).toLocaleDateString()}
                </td>
                <td className="px-6 py-4 text-right">
                  <div className="flex items-center justify-end gap-2">
                    <button
                      onClick={() => openEditModal(user)}
                      className="px-3 py-1.5 text-sm bg-slate-700 hover:bg-slate-600 rounded transition-colors"
                    >
                      Edit
                    </button>
                    <button
                      onClick={() => openResetModal(user)}
                      className="px-3 py-1.5 text-sm bg-yellow-600/20 text-yellow-400 hover:bg-yellow-600/30 rounded transition-colors"
                    >
                      Reset Password
                    </button>
                    {user.id !== currentUser?.id && (
                      <button
                        onClick={() => handleDeleteUser(user.id)}
                        className="px-3 py-1.5 text-sm bg-red-600/20 text-red-400 hover:bg-red-600/30 rounded transition-colors"
                      >
                        Delete
                      </button>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {/* Add User Modal */}
      {showAddModal && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <div className="bg-slate-800 border border-slate-700 rounded-xl p-6 w-full max-w-md">
            <h2 className="text-xl font-semibold mb-4">Add New User</h2>
            <form onSubmit={handleAddUser} className="space-y-4">
              {formError && (
                <div className="bg-red-500/10 border border-red-500/50 text-red-400 px-4 py-2 rounded text-sm">
                  {formError}
                </div>
              )}
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">Email</label>
                <input
                  type="email"
                  value={formEmail}
                  onChange={(e) => setFormEmail(e.target.value)}
                  className="w-full px-4 py-2 bg-slate-700/50 border border-slate-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blox-500"
                  required
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">Name</label>
                <input
                  type="text"
                  value={formName}
                  onChange={(e) => setFormName(e.target.value)}
                  className="w-full px-4 py-2 bg-slate-700/50 border border-slate-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blox-500"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">Password</label>
                <input
                  type="password"
                  value={formPassword}
                  onChange={(e) => setFormPassword(e.target.value)}
                  className="w-full px-4 py-2 bg-slate-700/50 border border-slate-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blox-500"
                  required
                  minLength={6}
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">Role</label>
                <select
                  value={formRole}
                  onChange={(e) => setFormRole(e.target.value as 'ADMIN' | 'USER' | 'MONITOR')}
                  className="w-full px-4 py-2 bg-slate-700/50 border border-slate-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blox-500"
                >
                  <option value="USER">User</option>
                  <option value="ADMIN">Admin</option>
                  <option value="MONITOR">Monitor (Read-only)</option>
                </select>
              </div>
              <div className="flex justify-end gap-3 pt-2">
                <button
                  type="button"
                  onClick={() => { setShowAddModal(false); resetForm(); }}
                  className="px-4 py-2 bg-slate-700 hover:bg-slate-600 rounded-lg transition-colors"
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  disabled={formLoading}
                  className="px-4 py-2 bg-blox-600 hover:bg-blox-700 text-white rounded-lg transition-colors disabled:opacity-50"
                >
                  {formLoading ? 'Creating...' : 'Create User'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* Edit User Modal */}
      {showEditModal && selectedUser && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <div className="bg-slate-800 border border-slate-700 rounded-xl p-6 w-full max-w-md">
            <h2 className="text-xl font-semibold mb-4">Edit User</h2>
            <form onSubmit={handleEditUser} className="space-y-4">
              {formError && (
                <div className="bg-red-500/10 border border-red-500/50 text-red-400 px-4 py-2 rounded text-sm">
                  {formError}
                </div>
              )}
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">Email</label>
                <input
                  type="email"
                  value={formEmail}
                  onChange={(e) => setFormEmail(e.target.value)}
                  className="w-full px-4 py-2 bg-slate-700/50 border border-slate-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blox-500"
                  required
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">Name</label>
                <input
                  type="text"
                  value={formName}
                  onChange={(e) => setFormName(e.target.value)}
                  className="w-full px-4 py-2 bg-slate-700/50 border border-slate-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blox-500"
                />
              </div>
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">Role</label>
                <select
                  value={formRole}
                  onChange={(e) => setFormRole(e.target.value as 'ADMIN' | 'USER' | 'MONITOR')}
                  className="w-full px-4 py-2 bg-slate-700/50 border border-slate-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blox-500"
                  disabled={selectedUser.id === currentUser?.id}
                >
                  <option value="USER">User</option>
                  <option value="ADMIN">Admin</option>
                  <option value="MONITOR">Monitor (Read-only)</option>
                </select>
                {selectedUser.id === currentUser?.id && (
                  <p className="text-xs text-slate-500 mt-1">Cannot change your own role</p>
                )}
              </div>
              <div className="flex justify-end gap-3 pt-2">
                <button
                  type="button"
                  onClick={() => { setShowEditModal(false); resetForm(); }}
                  className="px-4 py-2 bg-slate-700 hover:bg-slate-600 rounded-lg transition-colors"
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  disabled={formLoading}
                  className="px-4 py-2 bg-blox-600 hover:bg-blox-700 text-white rounded-lg transition-colors disabled:opacity-50"
                >
                  {formLoading ? 'Saving...' : 'Save Changes'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}

      {/* Reset Password Modal */}
      {showResetModal && selectedUser && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <div className="bg-slate-800 border border-slate-700 rounded-xl p-6 w-full max-w-md">
            <h2 className="text-xl font-semibold mb-4">Reset Password</h2>
            <p className="text-slate-400 text-sm mb-4">
              Set a new password for <strong>{selectedUser.email}</strong>
            </p>
            <form onSubmit={handleResetPassword} className="space-y-4">
              {formError && (
                <div className="bg-red-500/10 border border-red-500/50 text-red-400 px-4 py-2 rounded text-sm">
                  {formError}
                </div>
              )}
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-1">New Password</label>
                <input
                  type="password"
                  value={formPassword}
                  onChange={(e) => setFormPassword(e.target.value)}
                  className="w-full px-4 py-2 bg-slate-700/50 border border-slate-600 rounded-lg text-white focus:outline-none focus:ring-2 focus:ring-blox-500"
                  required
                  minLength={6}
                  placeholder="Minimum 6 characters"
                />
              </div>
              <div className="flex justify-end gap-3 pt-2">
                <button
                  type="button"
                  onClick={() => { setShowResetModal(false); resetForm(); }}
                  className="px-4 py-2 bg-slate-700 hover:bg-slate-600 rounded-lg transition-colors"
                >
                  Cancel
                </button>
                <button
                  type="submit"
                  disabled={formLoading}
                  className="px-4 py-2 bg-yellow-600 hover:bg-yellow-700 text-white rounded-lg transition-colors disabled:opacity-50"
                >
                  {formLoading ? 'Resetting...' : 'Reset Password'}
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}
