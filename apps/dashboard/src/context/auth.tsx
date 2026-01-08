'use client';

import { createContext, useContext, useState, useEffect, useCallback, ReactNode } from 'react';
import { useRouter, usePathname } from 'next/navigation';
import { saveTokenToStorage, removeTokenFromStorage } from '../hooks/useWebSocket';

const getApiUrl = () => {
  if (typeof window === 'undefined') return 'http://localhost:3001';
  return `http://${window.location.hostname}:3001`;
};

export interface User {
  id: string;
  email: string;
  name: string | null;
  role: 'ADMIN' | 'USER';
}

interface AuthContextType {
  user: User | null;
  isLoading: boolean;
  login: (email: string, password: string, rememberMe?: boolean) => Promise<void>;
  register: (email: string, password: string, name?: string) => Promise<void>;
  logout: () => Promise<void>;
  refreshUser: () => Promise<void>;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const router = useRouter();
  const pathname = usePathname();

  // Public routes that don't require authentication
  const publicRoutes = ['/login', '/setup'];
  const isPublicRoute = publicRoutes.includes(pathname);

  // Check if setup is required and fetch current user
  const initAuth = useCallback(async () => {
    try {
      // First check if setup is required
      const setupRes = await fetch(`${getApiUrl()}/api/auth/setup-required`, {
        credentials: 'include',
      });
      const setupData = await setupRes.json();

      if (setupData.setupRequired) {
        // No users exist, redirect to setup
        if (pathname !== '/setup') {
          router.push('/setup');
        }
        setIsLoading(false);
        return;
      }

      // Try to get current user
      const meRes = await fetch(`${getApiUrl()}/api/auth/me`, {
        credentials: 'include',
      });

      if (meRes.ok) {
        const data = await meRes.json();
        setUser(data.user);
      } else {
        // Not authenticated
        setUser(null);
        if (!isPublicRoute) {
          router.push('/login');
        }
      }
    } catch (error) {
      console.error('Auth init error:', error);
      setUser(null);
    } finally {
      setIsLoading(false);
    }
  }, [pathname, router, isPublicRoute]);

  useEffect(() => {
    initAuth();
  }, [initAuth]);

  const login = async (email: string, password: string, rememberMe: boolean = false) => {
    const res = await fetch(`${getApiUrl()}/api/auth/login`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ email, password, rememberMe }),
    });

    if (!res.ok) {
      const data = await res.json();
      throw new Error(data.error || 'Login failed');
    }

    const data = await res.json();
    setUser(data.user);
    if (data.token) {
      saveTokenToStorage(data.token);
    }
    router.push('/');
  };

  const register = async (email: string, password: string, name?: string) => {
    const res = await fetch(`${getApiUrl()}/api/auth/register`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ email, password, name }),
    });

    if (!res.ok) {
      const data = await res.json();
      throw new Error(data.error || 'Registration failed');
    }

    const data = await res.json();
    setUser(data.user);
    if (data.token) {
      saveTokenToStorage(data.token);
    }
    router.push('/');
  };

  const logout = async () => {
    try {
      await fetch(`${getApiUrl()}/api/auth/logout`, {
        method: 'POST',
        credentials: 'include',
      });
    } catch (error) {
      console.error('Logout error:', error);
    }
    setUser(null);
    removeTokenFromStorage();
    router.push('/login');
  };

  const refreshUser = async () => {
    try {
      const res = await fetch(`${getApiUrl()}/api/auth/me`, {
        credentials: 'include',
      });
      if (res.ok) {
        const data = await res.json();
        setUser(data.user);
      }
    } catch (error) {
      console.error('Refresh user error:', error);
    }
  };

  return (
    <AuthContext.Provider value={{ user, isLoading, login, register, logout, refreshUser }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  const context = useContext(AuthContext);
  if (context === undefined) {
    throw new Error('useAuth must be used within an AuthProvider');
  }
  return context;
}
