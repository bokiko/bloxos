"use client";

// Monoform — one visual system, two contrast modes.
//
// There is no light mode, no system mode and no theme gallery. The only
// user-facing choice is `gray` (default) or `dark`, and both are dark-family
// surfaces: the document keeps the `dark` class permanently so component
// styles have a single branch to reason about.
//
// Persistence layers:
//   1. localStorage  — instant, works pre-auth
//   2. /api/me/theme — synced after login so the choice follows the user
//
// The inline bootstrap script in layout.tsx applies the stored appearance to
// <html> before this provider mounts, so there is never a flash of a light
// page.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from "react";
import { HUB_URL } from "@/lib/session";
import { useAuth } from "@/contexts/AuthContext";

export type AppearanceMode = "gray" | "dark";

const STORAGE_KEY = "bloxos-appearance";
// Pre-Monoform key. Read once so an existing "dark" choice survives the reset.
const LEGACY_MODE_KEY = "bloxos-theme-mode";

type AppearanceContextValue = {
  appearance: AppearanceMode;
  setAppearance: (appearance: AppearanceMode) => void;
};

const AppearanceContext = createContext<AppearanceContextValue | null>(null);

function normalizeAppearance(value: unknown): AppearanceMode {
  return value === "dark" ? "dark" : "gray";
}

function readInitialAppearance(): AppearanceMode {
  if (typeof window === "undefined") return "gray";
  try {
    const current = localStorage.getItem(STORAGE_KEY);
    if (current === "gray" || current === "dark") return current;
    return localStorage.getItem(LEGACY_MODE_KEY) === "dark" ? "dark" : "gray";
  } catch {
    // Storage can be unavailable (private mode, blocked cookies). Default.
    return "gray";
  }
}

export function applyAppearanceToDocument(appearance: AppearanceMode) {
  if (typeof document === "undefined") return;
  const root = document.documentElement;
  root.dataset.appearance = appearance;
  root.classList.add("dark");
  root.style.colorScheme = "dark";
  // Strip anything the retired multi-theme / multi-layout system may have left
  // on <html> from a previous session.
  Array.from(root.classList)
    .filter((className) => className.startsWith("theme-"))
    .forEach((className) => root.classList.remove(className));
  delete root.dataset.layout;
  delete root.dataset.designColor;
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const { isAuthenticated, authFetch } = useAuth();
  const [appearance, setAppearanceState] =
    useState<AppearanceMode>(readInitialAppearance);

  useEffect(() => {
    applyAppearanceToDocument(appearance);
  }, [appearance]);

  useEffect(() => {
    if (!isAuthenticated) return;
    let cancelled = false;
    void (async () => {
      try {
        const response = await authFetch(`${HUB_URL}/api/me/theme`);
        if (!response.ok) return;
        const remote = await response.json();
        if (!cancelled) setAppearanceState(normalizeAppearance(remote.theme_mode));
      } catch {
        // A local contrast setting remains valid if synchronization is unavailable.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [authFetch, isAuthenticated]);

  const setAppearance = useCallback(
    (next: AppearanceMode) => {
      const normalized = normalizeAppearance(next);
      setAppearanceState(normalized);
      try {
        localStorage.setItem(STORAGE_KEY, normalized);
      } catch {
        // Non-fatal: the choice still applies for this session.
      }
      applyAppearanceToDocument(normalized);
      if (!isAuthenticated) return;
      void authFetch(`${HUB_URL}/api/me/theme`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ theme_name: "monoform", theme_mode: normalized }),
      }).catch(() => {
        // Best-effort sync; local state is the source of truth for this session.
      });
    },
    [authFetch, isAuthenticated]
  );

  return (
    <AppearanceContext.Provider value={{ appearance, setAppearance }}>
      {children}
    </AppearanceContext.Provider>
  );
}

export function useTheme(): AppearanceContextValue {
  const context = useContext(AppearanceContext);
  if (!context) throw new Error("useTheme must be used within ThemeProvider");
  return context;
}
