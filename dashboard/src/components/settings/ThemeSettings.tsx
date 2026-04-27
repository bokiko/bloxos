"use client";

// Phase 10 — theme settings panel.
//
// Top: mode picker (Light / Dark / System). Below: 5 theme cards with a
// preview tile, label, description, and a "Dark only" tag where relevant.

import { Sun, Moon, MonitorSmartphone, Check } from "lucide-react";
import {
  THEMES,
  type ThemeName,
  type ThemeMode,
  useTheme,
} from "@/contexts/ThemeContext";
import { cn } from "@/lib/utils";

const MODE_OPTIONS: { value: ThemeMode; label: string; Icon: typeof Sun }[] = [
  { value: "light", label: "Light", Icon: Sun },
  { value: "dark", label: "Dark", Icon: Moon },
  { value: "system", label: "System", Icon: MonitorSmartphone },
];

const ORDER: ThemeName[] = ["bloxos", "solarized", "dracula", "nord", "tokyo-night"];

export function ThemeSettings() {
  const { themeName, themeMode, setTheme, setMode } = useTheme();

  return (
    <div className="space-y-8">
      <section>
        <h2 className="text-sm font-semibold text-blox-text mb-1">Mode</h2>
        <p className="text-xs text-blox-muted mb-3">
          Choose light, dark, or follow your operating system.
        </p>
        <div className="inline-flex rounded-lg border border-blox-border bg-blox-card p-1 gap-1">
          {MODE_OPTIONS.map(({ value, label, Icon }) => {
            const active = themeMode === value;
            return (
              <button
                key={value}
                type="button"
                onClick={() => setMode(value)}
                className={cn(
                  "inline-flex items-center gap-2 px-3 py-1.5 rounded-md text-xs font-medium transition-colors",
                  active
                    ? "bg-blox-blue/15 text-blox-blue"
                    : "text-blox-muted hover:text-blox-text"
                )}
                aria-pressed={active}
              >
                <Icon className="w-3.5 h-3.5" />
                {label}
              </button>
            );
          })}
        </div>
      </section>

      <section>
        <h2 className="text-sm font-semibold text-blox-text mb-1">Theme</h2>
        <p className="text-xs text-blox-muted mb-4">
          Five curated palettes. Dark-only themes ignore the mode selector.
        </p>
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3">
          {ORDER.map((name) => {
            const meta = THEMES[name];
            const active = themeName === name;
            const darkOnly = !meta.supportedModes.includes("light");
            return (
              <button
                key={name}
                type="button"
                onClick={() => setTheme(name)}
                aria-pressed={active}
                className={cn(
                  "text-left rounded-xl border bg-blox-card p-3 transition-all",
                  active
                    ? "border-blox-blue ring-2 ring-blox-blue/40"
                    : "border-blox-border hover:border-blox-blue/40"
                )}
              >
                <div
                  className="rounded-md h-16 mb-3 overflow-hidden flex"
                  style={{
                    background: meta.preview.background,
                  }}
                >
                  <div
                    className="flex-1 m-1 rounded"
                    style={{ background: meta.preview.surface }}
                  />
                  <div
                    className="w-3 m-1 rounded"
                    style={{ background: meta.preview.accent }}
                  />
                  <div
                    className="w-1 m-1 rounded"
                    style={{ background: meta.preview.text, opacity: 0.6 }}
                  />
                </div>
                <div className="flex items-center justify-between gap-2 mb-1">
                  <span className="text-sm font-semibold text-blox-text">
                    {meta.label}
                  </span>
                  <div className="flex items-center gap-1.5">
                    {darkOnly && (
                      <span className="text-[10px] uppercase tracking-wider text-blox-muted bg-blox-border/40 rounded px-1.5 py-0.5">
                        Dark only
                      </span>
                    )}
                    {active && (
                      <Check className="w-3.5 h-3.5 text-blox-blue" />
                    )}
                  </div>
                </div>
                <p className="text-xs text-blox-muted leading-relaxed">
                  {meta.description}
                </p>
              </button>
            );
          })}
        </div>
      </section>
    </div>
  );
}
