"use client";

import { createContext, useContext, useEffect, useState, useCallback, ReactNode } from "react";
import { useRouter, usePathname } from "next/navigation";

interface AuthContextType {
  token: string | null;
  login: (username: string, password: string) => Promise<boolean>;
  logout: () => void;
  isAuthenticated: boolean;
  authFetch: (url: string, init?: RequestInit) => Promise<Response>;
}

const AuthContext = createContext<AuthContextType | null>(null);

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}

const HUB_URL = process.env.NEXT_PUBLIC_HUB_URL || "http://localhost:4000";

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(null);
  const [checked, setChecked] = useState(false);
  const router = useRouter();
  const pathname = usePathname();

  useEffect(() => {
    const stored = localStorage.getItem("bloxos_token");
    if (stored) {
      // Check if expired by decoding JWT payload.
      try {
        const payload = JSON.parse(atob(stored.split(".")[1]));
        if (payload.exp * 1000 > Date.now()) {
          setToken(stored);
        } else {
          localStorage.removeItem("bloxos_token");
        }
      } catch {
        localStorage.removeItem("bloxos_token");
      }
    }
    setChecked(true);
  }, []);

  useEffect(() => {
    if (checked && !token && pathname !== "/login") {
      router.push("/login");
    }
  }, [checked, token, pathname, router]);

  const login = useCallback(async (username: string, password: string): Promise<boolean> => {
    try {
      const res = await fetch(`${HUB_URL}/api/auth/login`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username, password }),
      });
      if (!res.ok) return false;
      const data = await res.json();
      localStorage.setItem("bloxos_token", data.token);
      setToken(data.token);
      return true;
    } catch {
      return false;
    }
  }, []);

  const logout = useCallback(() => {
    localStorage.removeItem("bloxos_token");
    setToken(null);
    router.push("/login");
  }, [router]);

  const authFetch = useCallback(async (url: string, init?: RequestInit): Promise<Response> => {
    const currentToken = localStorage.getItem("bloxos_token");
    const headers = new Headers(init?.headers);
    if (currentToken) {
      headers.set("Authorization", `Bearer ${currentToken}`);
    }
    const res = await fetch(url, { ...init, headers });
    if (res.status === 401) {
      localStorage.removeItem("bloxos_token");
      setToken(null);
      router.push("/login");
    }
    return res;
  }, [router]);

  if (!checked) {
    return (
      <div className="min-h-screen bg-blox-bg flex items-center justify-center">
        <div className="text-blox-muted text-sm">Loading...</div>
      </div>
    );
  }

  return (
    <AuthContext.Provider value={{ token, login, logout, isAuthenticated: !!token, authFetch }}>
      {children}
    </AuthContext.Provider>
  );
}
