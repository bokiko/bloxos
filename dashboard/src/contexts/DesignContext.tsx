"use client";

import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { useAuth } from "@/contexts/AuthContext";
import { useTheme, applyToDocument } from "@/contexts/ThemeContext";
import { HUB_URL, getStoredToken } from "@/lib/session";
import { userIDFromToken } from "@/lib/auth-session.mjs";
import { normalizeDesign, readDesign, writeDesign, hasDesignCache, type Layout, type DesignColor, type DesignPreferences } from "@/lib/design-prefs.mjs";

export type { Layout, DesignColor };
interface DesignValue {
  layout: Layout;
  color: DesignColor;
  ready: boolean;
  saving: boolean;
  error: string | null;
  setLayout: (layout: Layout) => void;
  setColor: (color: DesignColor) => void;
}
const Context = createContext<DesignValue | null>(null);

export function DesignProvider({ children }: { children: ReactNode }) {
  const { token, logout } = useAuth();
  const userID = userIDFromToken(token);
  const { themeName, resolvedMode } = useTheme();
  const [state, setState] = useState<{ owner: string | null; prefs: DesignPreferences; ready: boolean }>({ owner: null, prefs: normalizeDesign(null), ready: false });
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const current = useRef(normalizeDesign(null));
  const revision = useRef(0);
  const queue = useRef(Promise.resolve());
  const ready = state.ready && state.owner === userID;
  const prefs = ready ? state.prefs : normalizeDesign(null);
  const color = prefs.layout === "classic" ? "original" : prefs.colors[prefs.layout];

  // Bind both queued and in-flight requests to the login that initiated them.
  // Another tab may change storage at any time; never borrow its credentials.
  const fetchDesign = useCallback(async (init?: RequestInit) => {
    const headers = new Headers(init?.headers);
    if (token) headers.set("Authorization", `Bearer ${token}`);
    const response = await fetch(`${HUB_URL}/api/me/design`, { ...init, headers });
    if (response.status === 401 && token && token === getStoredToken()) logout();
    return response;
  }, [token, logout]);

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();
    const initialRevision = ++revision.current;
    // A microtask gives SSR and the first client render the same neutral shell.
    void Promise.resolve().then(async () => {
      if (cancelled) return;
      const cached = readDesign(localStorage, userID);
      current.current = cached;
      // Returning users can use their account-scoped choice immediately while
      // the server reconciles in the background. A cold account still waits in
      // the neutral frame instead of rendering the wrong Classic chrome.
      setState({ owner: userID, prefs: cached, ready: !token || hasDesignCache(localStorage, userID) });
      setError(null);
      setSaving(false);
      if (!token || token !== getStoredToken()) return;
      const timeout = setTimeout(() => controller.abort(), 5000);
      try {
        const response = await fetchDesign({ signal: controller.signal });
        if (!response.ok) throw new Error("Design sync unavailable; using this browser’s saved choice.");
        const remote = normalizeDesign(await response.json());
        if (cancelled || token !== getStoredToken() || revision.current !== initialRevision) return;
        current.current = remote;
        writeDesign(localStorage, userID, remote);
        setState({ owner: userID, prefs: remote, ready: true });
      } catch {
        if (!cancelled && revision.current === initialRevision) setError("Design sync unavailable; using this browser’s saved choice.");
      } finally {
        clearTimeout(timeout);
        if (!cancelled && token === getStoredToken()) setState(previous => ({ ...previous, ready: true }));
      }
    });
    return () => { cancelled = true; controller.abort(); };
  }, [token, userID, fetchDesign]);

  useEffect(() => {
    // Keep the pre-hydration choice until this account's cache/server resolves.
    // In particular, never paint the default Classic over a known saved design.
    if (!ready) return;
    const root = document.documentElement;
    root.dataset.layout = prefs.layout;
    root.dataset.designColor = color;
    if (prefs.layout === "classic") {
      applyToDocument(themeName, resolvedMode);
    } else {
      Array.from(root.classList).filter(c => c.startsWith("theme-")).forEach(c => root.classList.remove(c));
      root.classList.remove("light", "dark");
      const mode = color === "bright" ? "light" : "dark";
      root.classList.add(mode);
      root.style.colorScheme = mode;
    }
  }, [ready, prefs.layout, color, themeName, resolvedMode]);

  const update = useCallback((next: DesignPreferences) => {
    if (!ready || token !== getStoredToken()) return;
    const edit = ++revision.current;
    current.current = next;
    writeDesign(localStorage, userID, next);
    setState({ owner: userID, prefs: next, ready: true });
    setError(null);
    if (!token) return;
    setSaving(true);
    // Serialize changes so slow earlier saves cannot overwrite the last choice.
    // Check the captured login before dispatch, not only after the response.
    queue.current = queue.current.catch(() => {}).then(async () => {
      if (token !== getStoredToken()) return;
      try {
        const response = await fetchDesign({
          method: "PATCH", headers: { "Content-Type": "application/json" },
          // Each queued snapshot carries prior choices too, repairing an earlier
          // failed save when a later selection succeeds.
          body: JSON.stringify(next), signal: AbortSignal.timeout(5000),
        });
        if (!response.ok) throw new Error("save failed");
        if (token === getStoredToken() && edit === revision.current) setError(null);
      } catch {
        if (token === getStoredToken()) setError("Saved in this browser only. Select your choice again to retry account sync.");
      } finally {
        if (token === getStoredToken() && edit === revision.current) setSaving(false);
      }
    });
  }, [ready, token, userID, fetchDesign]);

  const setLayout = useCallback((layout: Layout) => {
    update({ ...current.current, layout });
  }, [update]);
  const setColor = useCallback((nextColor: DesignColor) => {
    const layout = current.current.layout;
    if (layout === "classic") return;
    update({ ...current.current, colors: { ...current.current.colors, [layout]: nextColor } });
  }, [update]);

  return <Context.Provider value={{ layout: prefs.layout, color, ready, saving, error, setLayout, setColor }}>{children}</Context.Provider>;
}

export function useDesign() {
  const context = useContext(Context);
  if (!context) throw new Error("useDesign requires DesignProvider");
  return context;
}
