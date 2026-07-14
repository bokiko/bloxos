"use client";

import { createContext, useContext, useEffect, useState, useCallback, ReactNode } from "react";
import { useRouter, usePathname } from "next/navigation";
import { ChangePasswordModal } from "@/components/ChangePasswordModal";
import { HUB_URL, dispatchAuthChanged } from "@/lib/session";

export type UserRole = "admin" | "operator" | "viewer";

interface AuthContextType {
  token: string | null;
  role: UserRole | null;
  scopes: string[];
  hasScope: (scope: string) => boolean;
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

function normalizeRole(raw: string | null): UserRole | null {
  if (raw === "admin" || raw === "operator" || raw === "viewer") return raw;
  return null;
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(null);
  const [role, setRole] = useState<UserRole | null>(null);
  const [scopes, setScopes] = useState<string[]>([]);
  const [checked, setChecked] = useState(false);
  const [passwordChangeRequired, setPasswordChangeRequired] = useState(false);
  const [pinChangeRequired, setPinChangeRequired] = useState(false);
  const router = useRouter();
  const pathname = usePathname();

  // Check token on mount and refresh if expiring soon.
  useEffect(() => {
    const stored = localStorage.getItem("bloxos_token");
    if (stored) {
      try {
        const payload = JSON.parse(atob(stored.split(".")[1]));
        if (payload.exp * 1000 > Date.now()) {
          // eslint-disable-next-line react-hooks/set-state-in-effect
          setToken(stored);
          setRole(normalizeRole(localStorage.getItem("bloxos_role")));
          try {
            const storedScopes = JSON.parse(localStorage.getItem("bloxos_scopes") || "[]");
            if (Array.isArray(storedScopes)) setScopes(storedScopes.filter((s) => typeof s === "string"));
          } catch {
            setScopes([]);
          }
          // Check stored change requirements.
          const pwReq = localStorage.getItem("bloxos_pw_change_required");
          const pinReq = localStorage.getItem("bloxos_pin_change_required");
          if (pwReq === "true") setPasswordChangeRequired(true);
          if (pinReq === "true") setPinChangeRequired(true);
        } else {
          localStorage.removeItem("bloxos_token");
          localStorage.removeItem("bloxos_role");
          localStorage.removeItem("bloxos_scopes");
          localStorage.removeItem("bloxos_pw_change_required");
          localStorage.removeItem("bloxos_pin_change_required");
        }
      } catch {
        localStorage.removeItem("bloxos_token");
        localStorage.removeItem("bloxos_role");
        localStorage.removeItem("bloxos_scopes");
      }
    }
    setChecked(true);
  }, []);

  useEffect(() => {
    if (checked && !token && pathname !== "/login" && pathname !== "/setup") {
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

      const nextRole = normalizeRole(typeof data.role === "string" ? data.role : null);
      setRole(nextRole);
      if (nextRole) {
        localStorage.setItem("bloxos_role", nextRole);
      } else {
        localStorage.removeItem("bloxos_role");
      }

      const nextScopes = Array.isArray(data.scopes)
        ? data.scopes.filter((s: unknown): s is string => typeof s === "string")
        : [];
      setScopes(nextScopes);
      localStorage.setItem("bloxos_scopes", JSON.stringify(nextScopes));

      dispatchAuthChanged();

      // Check if password/PIN change is required (Finding #2).
      if (data.password_change_required) {
        setPasswordChangeRequired(true);
        localStorage.setItem("bloxos_pw_change_required", "true");
      }
      if (data.pin_change_required) {
        setPinChangeRequired(true);
        localStorage.setItem("bloxos_pin_change_required", "true");
      }

      return true;
    } catch {
      return false;
    }
  }, []);

  const logout = useCallback(() => {
    localStorage.removeItem("bloxos_token");
    localStorage.removeItem("bloxos_role");
    localStorage.removeItem("bloxos_scopes");
    localStorage.removeItem("bloxos_pw_change_required");
    localStorage.removeItem("bloxos_pin_change_required");
    setToken(null);
    setRole(null);
    setScopes([]);
    setPasswordChangeRequired(false);
    setPinChangeRequired(false);
    dispatchAuthChanged();
    router.push("/login");
  }, [router]);

  const hasScope = useCallback((scope: string) => scopes.includes(scope), [scopes]);

  const authFetch = useCallback(async (url: string, init?: RequestInit): Promise<Response> => {
    const currentToken = localStorage.getItem("bloxos_token");
    const headers = new Headers(init?.headers);
    if (currentToken) {
      headers.set("Authorization", `Bearer ${currentToken}`);
    }
    const res = await fetch(url, { ...init, headers });
    if (res.status === 401) {
      // Any 401 is authoritative: the hub rejected this token. That covers
      // server-side revocation (deleted user, role change, rotated secret) as
      // well as expiry — cases the old local `exp` check missed, leaving the
      // client "logged in" while every request silently failed.
      localStorage.removeItem("bloxos_token");
      setToken(null);
      dispatchAuthChanged();
      router.push("/login");
    }
    return res;
  }, [router]);

  const handlePasswordChanged = useCallback(() => {
    setPasswordChangeRequired(false);
    localStorage.removeItem("bloxos_pw_change_required");
  }, []);

  const handlePinChanged = useCallback(() => {
    setPinChangeRequired(false);
    localStorage.removeItem("bloxos_pin_change_required");
  }, []);

  if (!checked) {
    return (
      <div className="min-h-screen bg-blox-bg flex items-center justify-center">
        <div className="text-blox-muted text-sm">Loading...</div>
      </div>
    );
  }

  return (
    <AuthContext.Provider value={{ token, role, scopes, hasScope, login, logout, isAuthenticated: !!token, authFetch }}>
      {/* Force password change modal (Finding #2) */}
      {token && passwordChangeRequired && (
        <ChangePasswordModal type="password" onComplete={handlePasswordChanged} />
      )}
      {/* Force PIN change modal after password is changed (Finding #2) */}
      {token && !passwordChangeRequired && pinChangeRequired && (
        <ChangePasswordModal type="pin" onComplete={handlePinChanged} />
      )}
      {children}
    </AuthContext.Provider>
  );
}
