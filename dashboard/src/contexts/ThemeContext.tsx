"use client";

// Phase 10 — multi-theme support.
//
// Tracks two orthogonal pieces of state:
//   themeName  — which palette to use (5 curated themes)
//   themeMode  — light / dark / system
//
// `resolvedMode` is computed from themeMode + the OS preference, with
// dark-only themes (Dracula, Tokyo Night) forced to dark regardless of
// user choice.
//
// Persistence layers:
//   1. localStorage  — instant, works pre-auth
//   2. /api/me/theme — synced after login so prefs follow the user
//
// The inline bootstrap script in layout.tsx applies the correct DOM class
// before this provider mounts to avoid a flash of unstyled content.

import {
  createContext,
  useContext,
  useEffect,
  useMemo,
  useState,
  useCallback,
  ReactNode,
} from "react";
import { HUB_URL } from "@/lib/session";
import { useAuth } from "@/contexts/AuthContext";

export type ThemeName = "bloxos" | "solarized" | "dracula" | "nord" | "tokyo-night";
export type ThemeMode = "light" | "dark" | "system";
export type ResolvedMode = "light" | "dark";

export interface ThemeMeta {
  label: string;
  description: string;
  supportedModes: readonly ThemeMode[];
  preview: {
    background: string;
    surface: string;
    accent: string;
    text: string;
  };
}

const THEME_NAMES: readonly ThemeName[] = [
  "bloxos",
  "solarized",
  "dracula",
  "nord",
  "tokyo-night",
];

const THEME_MODES: readonly ThemeMode[] = ["light", "dark", "system"];

const STORAGE_NAME = "bloxos-theme-name";
const STORAGE_MODE = "bloxos-theme-mode";
const STORAGE_LEGACY = "bloxos-theme"; // pre-Phase 10 — used only for migration.

export const THEMES: Record<ThemeName, ThemeMeta> = {
  bloxos: {
    label: "BloxOS",
    description: "Default — neutral surfaces, blue accent.",
    supportedModes: ["light", "dark", "system"],
    preview: {
      background: "#0a0a0f",
      surface: "#12121a",
      accent: "#3b82f6",
      text: "#fafafa",
    },
  },
  solarized: {
    label: "Solarized",
    description: "Warm parchment hues; precise low-contrast palette.",
    supportedModes: ["light", "dark", "system"],
    preview: {
      background: "#002b36",
      surface: "#073642",
      accent: "#268bd2",
      text: "#fdf6e3",
    },
  },
  dracula: {
    label: "Dracula",
    description: "Deep violet surfaces, high-saturation accent. Dark only.",
    supportedModes: ["dark"],
    preview: {
      background: "#282a36",
      surface: "#343746",
      accent: "#bd93f9",
      text: "#f8f8f2",
    },
  },
  nord: {
    label: "Nord",
    description: "Arctic blues and slate greys.",
    supportedModes: ["light", "dark", "system"],
    preview: {
      background: "#2e3440",
      surface: "#3b4252",
      accent: "#88c0d0",
      text: "#eceff4",
    },
  },
  "tokyo-night": {
    label: "Tokyo Night",
    description: "Indigo-on-black; high contrast accents. Dark only.",
    supportedModes: ["dark"],
    preview: {
      background: "#1a1b26",
      surface: "#24283b",
      accent: "#7aa2f7",
      text: "#c0caf5",
    },
  },
};

const DARK_ONLY_THEMES: readonly ThemeName[] = ["dracula", "tokyo-night"];

interface ThemeContextValue {
  themeName: ThemeName;
  themeMode: ThemeMode;
  resolvedMode: ResolvedMode;
  setTheme: (name: ThemeName) => void;
  setMode: (mode: ThemeMode) => void;
}

const ThemeContext = createContext<ThemeContextValue | null>(null);

function isThemeName(v: string | null | undefined): v is ThemeName {
  return !!v && (THEME_NAMES as readonly string[]).includes(v);
}

function isThemeMode(v: string | null | undefined): v is ThemeMode {
  return !!v && (THEME_MODES as readonly string[]).includes(v);
}

function readStoredThemeName(): ThemeName {
  if (typeof window === "undefined") return "bloxos";
  const stored = localStorage.getItem(STORAGE_NAME);
  if (isThemeName(stored)) return stored;
  return "bloxos";
}

function readStoredThemeMode(): ThemeMode {
  if (typeof window === "undefined") return "system";
  const stored = localStorage.getItem(STORAGE_MODE);
  if (isThemeMode(stored)) return stored;
  // Migrate from legacy `bloxos-theme` if present (pre-Phase 10).
  const legacy = localStorage.getItem(STORAGE_LEGACY);
  if (isThemeMode(legacy)) return legacy;
  return "system";
}

function getSystemMode(): ResolvedMode {
  if (typeof window === "undefined") return "dark";
  return window.matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

function applyToDocument(name: ThemeName, resolved: ResolvedMode) {
  if (typeof document === "undefined") return;
  const root = document.documentElement;
  // Strip any pre-existing theme-* class so toggles are clean.
  Array.from(root.classList).forEach((c) => {
    if (c.startsWith("theme-")) root.classList.remove(c);
  });
  // Default `bloxos` palette is the :root/.dark fallback — no class needed.
  if (name !== "bloxos") {
    root.classList.add(`theme-${name}`);
  }
  root.classList.remove("light", "dark");
  root.classList.add(resolved);
  root.style.colorScheme = resolved;
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const { isAuthenticated, authFetch } = useAuth();

  // Lazy initializers read localStorage exactly once on first client render.
  // SSR returns the default; the inline bootstrap script in layout.tsx
  // already painted the DOM, so the user never sees a mismatch.
  const [themeName, setThemeNameState] = useState<ThemeName>(readStoredThemeName);
  const [themeMode, setThemeModeState] = useState<ThemeMode>(readStoredThemeMode);

  // `systemMode` tracks the OS preference for `prefers-color-scheme`.
  // It's only ever updated by the matchMedia listener — an external system —
  // so the React 19 set-state-in-effect rule is satisfied.
  const [systemMode, setSystemMode] = useState<ResolvedMode>(getSystemMode);

  // Derived during render — no setState in an effect.
  const resolvedMode = useMemo<ResolvedMode>(() => {
    if ((DARK_ONLY_THEMES as readonly string[]).includes(themeName)) return "dark";
    if (themeMode === "system") return systemMode;
    return themeMode;
  }, [themeName, themeMode, systemMode]);

  // After login, fetch the server-side prefs and override locally if they
  // differ. This lets prefs follow the user across browsers.
  useEffect(() => {
    if (!isAuthenticated) return;
    let cancelled = false;
    (async () => {
      try {
        const res = await authFetch(`${HUB_URL}/api/me/theme`);
        if (!res.ok) return;
        const data = await res.json();
        if (cancelled) return;
        const serverName: ThemeName | null = isThemeName(data.theme_name)
          ? data.theme_name
          : null;
        const serverMode: ThemeMode | null = isThemeMode(data.theme_mode)
          ? data.theme_mode
          : null;
        if (serverName) {
          setThemeNameState((prev) => (prev !== serverName ? serverName : prev));
          localStorage.setItem(STORAGE_NAME, serverName);
        }
        if (serverMode) {
          setThemeModeState((prev) => (prev !== serverMode ? serverMode : prev));
          localStorage.setItem(STORAGE_MODE, serverMode);
        }
      } catch {
        // Network errors are non-fatal; stay on the local prefs.
      }
    })();
    return () => {
      cancelled = true;
    };
    // We deliberately depend only on isAuthenticated. Reacting to themeName/
    // themeMode would create a feedback loop where the server response writes
    // local state which retriggers this effect.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isAuthenticated]);

  // Apply theme + mode classes to <html>. This is a side-effect on an
  // external system (the DOM), which is the canonical use case for useEffect.
  useEffect(() => {
    applyToDocument(themeName, resolvedMode);
  }, [themeName, resolvedMode]);

  // Subscribe to OS-level mode changes. The setState below is in an event
  // listener — it's triggered by external state (the OS), not by React,
  // so it's allowed under React 19's set-state-in-effect rule.
  useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: dark)");
    const handler = () => setSystemMode(mq.matches ? "dark" : "light");
    mq.addEventListener("change", handler);
    return () => mq.removeEventListener("change", handler);
  }, []);

  const persistRemote = useCallback(
    async (patch: Partial<{ theme_name: ThemeName; theme_mode: ThemeMode }>) => {
      if (!isAuthenticated) return;
      try {
        await authFetch(`${HUB_URL}/api/me/theme`, {
          method: "PATCH",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(patch),
        });
      } catch {
        // Best-effort sync; local state is the source of truth for this session.
      }
    },
    [isAuthenticated, authFetch]
  );

  const setTheme = useCallback(
    (next: ThemeName) => {
      setThemeNameState(next);
      localStorage.setItem(STORAGE_NAME, next);
      // The render-time useMemo + the apply effect handle DOM updates;
      // we just persist remotely here.
      void persistRemote({ theme_name: next });
    },
    [persistRemote]
  );

  const setMode = useCallback(
    (next: ThemeMode) => {
      setThemeModeState(next);
      localStorage.setItem(STORAGE_MODE, next);
      void persistRemote({ theme_mode: next });
    },
    [persistRemote]
  );

  return (
    <ThemeContext.Provider
      value={{ themeName, themeMode, resolvedMode, setTheme, setMode }}
    >
      {children}
    </ThemeContext.Provider>
  );
}

export function useTheme(): ThemeContextValue {
  const ctx = useContext(ThemeContext);
  if (!ctx) throw new Error("useTheme must be used within ThemeProvider");
  return ctx;
}
