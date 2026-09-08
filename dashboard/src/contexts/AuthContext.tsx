"use client";

import { createContext, useContext, useEffect, useRef, useState, useCallback, ReactNode } from "react";
import { useRouter, usePathname } from "next/navigation";
import { ChangePasswordModal } from "@/components/ChangePasswordModal";
import { HUB_URL, dispatchAuthChanged, getStoredToken } from "@/lib/session";
import { shouldLogoutOn401, storageAuthAction, tokenRemovalApplies, decodeJWTPayload } from "@/lib/auth-session.mjs";

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
        const payload = decodeJWTPayload(stored);
        if (payload?.exp * 1000 > Date.now()) {
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

  // Session generation: incremented by every login attempt and every logout,
  // so an async login that resolves after either is discarded instead of
  // writing stale session state over the current one.
  const sessionGenRef = useRef(0);

  const login = useCallback(async (username: string, password: string): Promise<boolean> => {
    const gen = ++sessionGenRef.current;
    try {
      const res = await fetch(`${HUB_URL}/api/auth/login`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username, password }),
      });
      if (!res.ok) return false;
      const data = await res.json();
      // A logout or a newer login happened while this request was in flight —
      // discard this result rather than clobbering the current session.
      if (gen !== sessionGenRef.current) return false;

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

      // Set OR CLEAR the change-required flags — a fresh login without a
      // requirement must not inherit a previous session's forced modal.
      const pwReq = !!data.password_change_required;
      const pinReq = !!data.pin_change_required;
      setPasswordChangeRequired(pwReq);
      setPinChangeRequired(pinReq);
      if (pwReq) {
        localStorage.setItem("bloxos_pw_change_required", "true");
      } else {
        localStorage.removeItem("bloxos_pw_change_required");
      }
      if (pinReq) {
        localStorage.setItem("bloxos_pin_change_required", "true");
      } else {
        localStorage.removeItem("bloxos_pin_change_required");
      }

      // Publish the token LAST: other tabs' storage listeners key on
      // bloxos_token, and by the time it changes, role/scopes/flags are
      // already consistent — no tab may observe a mixed session.
      localStorage.setItem("bloxos_token", data.token);
      setToken(data.token);

      dispatchAuthChanged();
      return true;
    } catch {
      return false;
    }
  }, []);

  const logout = useCallback(() => {
    sessionGenRef.current++;
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

  // Cross-tab auth sync: logout elsewhere clears this tab too (and a full
  // localStorage.clear counts as logout); a token replaced by a login in
  // another tab re-syncs role/scopes/flags from storage so this tab shows
  // the new user's session instead of a stale mix.
  useEffect(() => {
    const onStorage = (e: StorageEvent) => {
      const action = storageAuthAction(e.key, e.newValue);
      if (action === "logout") {
        // An old removal event must not wipe a newer login that landed
        // meanwhile — act only when the store truly has no token now.
        if (!tokenRemovalApplies(getStoredToken())) return;
        logout();
        return;
      }
      if (action === "sync-login") {
        // Read the CURRENT stored snapshot, never the event payload — the
        // event may be older than what is in storage now.
        const storedToken = getStoredToken();
        if (!storedToken) return;
        setToken(storedToken);
        setRole(normalizeRole(localStorage.getItem("bloxos_role")));
        const rawScopes = localStorage.getItem("bloxos_scopes");
        try {
          setScopes(rawScopes ? JSON.parse(rawScopes) : []);
        } catch {
          setScopes([]);
        }
        setPasswordChangeRequired(localStorage.getItem("bloxos_pw_change_required") === "true");
        setPinChangeRequired(localStorage.getItem("bloxos_pin_change_required") === "true");
        dispatchAuthChanged();
      }
    };
    window.addEventListener("storage", onStorage);
    return () => window.removeEventListener("storage", onStorage);
  }, [logout]);

  const authFetch = useCallback(async (url: string, init?: RequestInit): Promise<Response> => {
    const currentToken = localStorage.getItem("bloxos_token");
    const headers = new Headers(init?.headers);
    if (currentToken) {
      headers.set("Authorization", `Bearer ${currentToken}`);
    }
    const res = await fetch(url, { ...init, headers });
    if (res.status === 401 && shouldLogoutOn401(currentToken, localStorage.getItem("bloxos_token"))) {
      // Any 401 is authoritative: the hub rejected this token. That covers
      // server-side revocation (deleted user, role change, rotated secret) as
      // well as expiry — cases the old local `exp` check missed, leaving the
      // client "logged in" while every request silently failed. Full logout
      // (role/scopes/flags too), but ONLY when the failing request still
      // carries the current token: an older in-flight request started before
      // a re-login must not wipe the new session.
      logout();
    }
    return res;
  }, [logout]);

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
