'use client';

import { useState, useEffect, useMemo } from 'react';
import { useRouter } from 'next/navigation';
import { useAuth } from '../../context/auth';

const getApiUrl = () => {
  if (typeof window === 'undefined') return 'http://localhost:3001';
  return `http://${window.location.hostname}:3001`;
};

// Password requirements
const PASSWORD_REQUIREMENTS = {
  minLength: 12,
  requireUppercase: true,
  requireLowercase: true,
  requireNumbers: true,
  requireSpecial: true,
};

interface PasswordCheck {
  label: string;
  passed: boolean;
}

function usePasswordValidation(password: string): PasswordCheck[] {
  return useMemo(() => {
    return [
      {
        label: `At least ${PASSWORD_REQUIREMENTS.minLength} characters`,
        passed: password.length >= PASSWORD_REQUIREMENTS.minLength,
      },
      {
        label: 'One uppercase letter (A-Z)',
        passed: /[A-Z]/.test(password),
      },
      {
        label: 'One lowercase letter (a-z)',
        passed: /[a-z]/.test(password),
      },
      {
        label: 'One number (0-9)',
        passed: /\d/.test(password),
      },
      {
        label: 'One special character (!@#$%^&*)',
        passed: /[!@#$%^&*()_+\-=[\]{};:'",.<>?/\\|`~]/.test(password),
      },
    ];
  }, [password]);
}

function PasswordChecklist({ checks }: { checks: PasswordCheck[] }) {
  return (
    <div className="mt-2 space-y-1">
      {checks.map((check, index) => (
        <div key={index} className="flex items-center gap-2 text-xs">
          {check.passed ? (
            <svg className="w-4 h-4 text-green-500" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
            </svg>
          ) : (
            <svg className="w-4 h-4 text-slate-500" fill="none" viewBox="0 0 24 24" stroke="currentColor">
              <circle cx="12" cy="12" r="10" strokeWidth={2} />
            </svg>
          )}
          <span className={check.passed ? 'text-green-400' : 'text-slate-400'}>
            {check.label}
          </span>
        </div>
      ))}
    </div>
  );
}

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

  const passwordChecks = usePasswordValidation(password);
  const allChecksPassed = passwordChecks.every(check => check.passed);
  const passwordsMatch = password === confirmPassword && confirmPassword.length > 0;

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
    if (!allChecksPassed) {
      setError('Password does not meet all requirements');
      return;
    }

    if (password !== confirmPassword) {
      setError('Passwords do not match');
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
                placeholder="Create a strong password"
                required
                autoComplete="new-password"
              />
              <PasswordChecklist checks={passwordChecks} />
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
                className={`w-full px-4 py-2.5 bg-slate-700/50 border rounded-lg text-white placeholder-slate-400 focus:outline-none focus:ring-2 focus:ring-blox-500/50 transition-colors ${
                  confirmPassword.length > 0
                    ? passwordsMatch
                      ? 'border-green-500/50'
                      : 'border-red-500/50'
                    : 'border-slate-600/50'
                }`}
                placeholder="Re-enter your password"
                required
                autoComplete="new-password"
              />
              {confirmPassword.length > 0 && (
                <div className="mt-1 flex items-center gap-1 text-xs">
                  {passwordsMatch ? (
                    <>
                      <svg className="w-4 h-4 text-green-500" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M5 13l4 4L19 7" />
                      </svg>
                      <span className="text-green-400">Passwords match</span>
                    </>
                  ) : (
                    <>
                      <svg className="w-4 h-4 text-red-500" fill="none" viewBox="0 0 24 24" stroke="currentColor">
                        <path strokeLinecap="round" strokeLinejoin="round" strokeWidth={2} d="M6 18L18 6M6 6l12 12" />
                      </svg>
                      <span className="text-red-400">Passwords do not match</span>
                    </>
                  )}
                </div>
              )}
            </div>

            <button
              type="submit"
              disabled={isSubmitting || !allChecksPassed || !passwordsMatch}
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
