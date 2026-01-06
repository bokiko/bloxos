'use client';

import { useState, useEffect } from 'react';
import { useRouter } from 'next/navigation';
import { useAuth } from '../../context/auth';

const getApiUrl = () => {
  if (typeof window === 'undefined') return 'http://localhost:3001';
  return `http://${window.location.hostname}:3001`;
};

export default function SetupPage() {
  const { register, isLoading } = useAuth();
  const router = useRouter();
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [error, setError] = useState('');
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [checkingSetup, setCheckingSetup] = useState(true);

  // Check if setup is actually required
  useEffect(() => {
    async function checkSetup() {
      try {
        const res = await fetch(`${getApiUrl()}/api/auth/setup-required`);
        const data = await res.json();
        if (!data.setupRequired) {
          // Setup not required, redirect to login
          router.push('/login');
        }
      } catch (error) {
        console.error('Failed to check setup status');
      } finally {
        setCheckingSetup(false);
      }
    }
    checkSetup();
  }, [router]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError('');

    // Validation
    if (password !== confirmPassword) {
      setError('Passwords do not match');
      return;
    }

    if (password.length < 6) {
      setError('Password must be at least 6 characters');
      return;
    }

    setIsSubmitting(true);

    try {
      await register(email, password, name || undefined);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Registration failed');
    } finally {
      setIsSubmitting(false);
    }
  };

  if (checkingSetup || isLoading) {
    return (
      <div className="min-h-screen bg-slate-900 flex items-center justify-center">
        <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-blox-500"></div>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-slate-900 flex items-center justify-center p-4">
      <div className="w-full max-w-md">
        {/* Logo */}
        <div className="text-center mb-8">
          <div className="w-16 h-16 rounded-2xl bg-gradient-to-br from-blox-400 to-blox-600 flex items-center justify-center font-bold text-3xl mx-auto mb-4">
            B
          </div>
          <h1 className="text-2xl font-bold text-white">Welcome to BloxOs</h1>
          <p className="text-slate-400 mt-1">Create your admin account to get started</p>
        </div>

        {/* Setup Form */}
        <div className="bg-slate-800/50 backdrop-blur-sm border border-slate-700/50 rounded-xl p-6">
          <form onSubmit={handleSubmit} className="space-y-4">
            {error && (
              <div className="bg-red-500/10 border border-red-500/50 text-red-400 px-4 py-3 rounded-lg text-sm">
                {error}
              </div>
            )}

            <div className="bg-blox-500/10 border border-blox-500/30 text-blox-400 px-4 py-3 rounded-lg text-sm">
              This will be the administrator account with full access.
            </div>

            <div>
              <label htmlFor="name" className="block text-sm font-medium text-slate-300 mb-2">
                Name (optional)
              </label>
              <input
                id="name"
                type="text"
                value={name}
                onChange={(e) => setName(e.target.value)}
                className="w-full px-4 py-2.5 bg-slate-700/50 border border-slate-600/50 rounded-lg text-white placeholder-slate-400 focus:outline-none focus:ring-2 focus:ring-blox-500/50 focus:border-blox-500 transition-colors"
                placeholder="Admin"
                autoComplete="name"
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
                placeholder="admin@example.com"
                required
                autoComplete="email"
              />
            </div>

            <div>
              <label htmlFor="password" className="block text-sm font-medium text-slate-300 mb-2">
                Password
              </label>
              <input
                id="password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                className="w-full px-4 py-2.5 bg-slate-700/50 border border-slate-600/50 rounded-lg text-white placeholder-slate-400 focus:outline-none focus:ring-2 focus:ring-blox-500/50 focus:border-blox-500 transition-colors"
                placeholder="Minimum 6 characters"
                required
                autoComplete="new-password"
              />
            </div>

            <div>
              <label htmlFor="confirmPassword" className="block text-sm font-medium text-slate-300 mb-2">
                Confirm Password
              </label>
              <input
                id="confirmPassword"
                type="password"
                value={confirmPassword}
                onChange={(e) => setConfirmPassword(e.target.value)}
                className="w-full px-4 py-2.5 bg-slate-700/50 border border-slate-600/50 rounded-lg text-white placeholder-slate-400 focus:outline-none focus:ring-2 focus:ring-blox-500/50 focus:border-blox-500 transition-colors"
                placeholder="Re-enter your password"
                required
                autoComplete="new-password"
              />
            </div>

            <button
              type="submit"
              disabled={isSubmitting}
              className="w-full py-2.5 px-4 bg-gradient-to-r from-blox-500 to-blox-600 hover:from-blox-600 hover:to-blox-700 text-white font-medium rounded-lg transition-all duration-200 disabled:opacity-50 disabled:cursor-not-allowed"
            >
              {isSubmitting ? 'Creating account...' : 'Create Admin Account'}
            </button>
          </form>
        </div>

        <p className="text-center text-slate-500 text-sm mt-6">
          Mining Rig Management System
        </p>
      </div>
    </div>
  );
}
