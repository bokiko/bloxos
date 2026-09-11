"use client";

// Monoform — one visual system, two themes.
//
// There is no system mode and no theme gallery. The only user-facing choice is
// `dark` (the default) or `light`, and it is an explicit choice — the OS
// preference is deliberately not consulted, because a fleet dashboard left on
// a wall should not change appearance at sunset.
//
// The `dark` class on <html> is what Tailwind's `dark:` variants key off, so
// it is present in dark and absent in light, and `color-scheme` follows the
// theme so form controls, scrollbars and the browser's own chrome match.
//
// MIGRATION. The previous release shipped two dark-family contrast modes named
// `gray` and `dark`. `gray` no longer exists: a stored `gray`, a stored
// legacy value, and anything unrecognised all resolve to `dark`. The same rule
// is implemented three times over — here, in the pre-hydration bootstrap in
// app/layout.tsx, and in the hub's normalizer (hub/user_prefs.go) — and the
// three must agree.
//
// Persistence layers:
//   1. localStorage  — instant, works pre-auth
//   2. /api/me/theme — synced after login so the choice follows the user
//
// The inline bootstrap script in layout.tsx applies the stored theme to <html>
// before this provider mounts, so there is never a flash of the wrong theme.

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

export type AppearanceMode = "dark" | "light";

// What the user is offered them as. The stored value, the `data-appearance`
// attribute and the hub's `theme_mode` stay "dark"/"light" — this is the label
// only, so renaming what people read never touches what is persisted.
export const APPEARANCE_LABELS: Record<AppearanceMode, string> = {
  dark: "Dark",
  light: "Bright",
};

const STORAGE_KEY = "bloxos-appearance";

// The pre-Monoform key `bloxos-theme-mode` is deliberately no longer read.
// While the two modes were `gray` and `dark`, that key existed to rescue an
// existing "dark" choice from the reset. `dark` is now the default, so the
// only value the key could still rescue is the one an absent key already
// produces — reading it is dead code. A legacy "light" is NOT honoured either:
// it named a palette from the retired multi-theme gallery, its owner has been
// on a dark surface for a full release, and silently flipping them to a
// different light theme now would be a surprise, not a restoration.

type AppearanceContextValue = {
  appearance: AppearanceMode;
  setAppearance: (appearance: AppearanceMode) => void;
};

const AppearanceContext = createContext<AppearanceContextValue | null>(null);

/** Anything that is not the one non-default theme resolves to `dark`. This is
 * the single rule the bootstrap and the hub normalizer both restate. */
function normalizeAppearance(value: unknown): AppearanceMode {
  return value === "light" ? "light" : "dark";
}

function readInitialAppearance(): AppearanceMode {
  if (typeof window === "undefined") return "dark";
  try {
    return normalizeAppearance(localStorage.getItem(STORAGE_KEY));
  } catch {
    // Storage can be unavailable (private mode, blocked cookies). Default.
    return "dark";
  }
}

export function applyAppearanceToDocument(appearance: AppearanceMode) {
  if (typeof document === "undefined") return;
  const root = document.documentElement;
  const dark = appearance !== "light";
  root.dataset.appearance = appearance;
  // Tailwind's `dark:` variants key off this class, so it has to come off in
  // light mode — leaving it on is how a "light theme" ends up with dark form
  // fields and dark focus surfaces from the shadcn primitives.
  root.classList.toggle("dark", dark);
  root.style.colorScheme = dark ? "dark" : "light";
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
