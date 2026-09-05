import type { Metadata } from "next";
import "./globals.css";
import { ToastProvider } from "@/components/Toast";
import { AuthProvider } from "@/contexts/AuthContext";
import { SSEProvider } from "@/contexts/SSEContext";
import { ThemeProvider } from "@/contexts/ThemeContext";
import { BrandingProvider } from "@/contexts/BrandingContext";
import { PreferencesProvider } from "@/contexts/PreferencesContext";
import { VersionsProvider } from "@/contexts/VersionsContext";
import { InventoryProvider } from "@/contexts/InventoryContext";
import { AISessionsProvider } from "@/contexts/AISessionsContext";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { TooltipProvider } from "@/components/ui/tooltip";
import { Geist, Geist_Mono } from "next/font/google";
import { cn } from "@/lib/utils";

const geistSans = Geist({ subsets: ["latin"], variable: "--font-sans" });
const geistMono = Geist_Mono({ subsets: ["latin"], variable: "--font-mono" });

export const metadata: Metadata = {
  title: "BloxOS",
  description: "Fleet Management Dashboard",
};

/**
 * Inline script that applies the saved theme name + resolved mode to <html>
 * before React hydrates. Without this, the initial paint flashes with the
 * wrong palette or wrong mode.
 *
 * Phase 10:
 *   - Reads `bloxos-theme-name` (named theme) and `bloxos-theme-mode`
 *     (light/dark/system), with legacy `bloxos-theme` as a fallback.
 *   - Validates against the 5 curated names + 3 valid modes.
 *   - Forces dark mode for dark-only themes (Dracula, Tokyo Night).
 *   - Resolves "system" against prefers-color-scheme.
 *   - Falls back to dark BloxOS on any error.
 */
const themeBootstrapScript = `
(function() {
  try {
    var validNames = ['bloxos','solarized','dracula','nord','tokyo-night'];
    var validModes = ['light','dark','system'];
    var darkOnly = ['dracula','tokyo-night'];

    var name = localStorage.getItem('bloxos-theme-name');
    if (validNames.indexOf(name) === -1) name = 'bloxos';

    var mode = localStorage.getItem('bloxos-theme-mode');
    if (validModes.indexOf(mode) === -1) {
      var legacy = localStorage.getItem('bloxos-theme');
      mode = validModes.indexOf(legacy) !== -1 ? legacy : 'system';
    }

    var resolved = mode;
    if (darkOnly.indexOf(name) !== -1) {
      resolved = 'dark';
    } else if (mode === 'system') {
      resolved = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    }

    var root = document.documentElement;
    var classes = root.classList;
    // Strip any prior theme-* class so toggles are clean.
    Array.prototype.slice.call(classes).forEach(function(c) {
      if (c.indexOf('theme-') === 0) classes.remove(c);
    });
    if (name !== 'bloxos') classes.add('theme-' + name);
    classes.remove('light', 'dark');
    classes.add(resolved);
    root.style.colorScheme = resolved;
  } catch (e) {
    document.documentElement.classList.add('dark');
  }
})();
`.trim();

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html
      lang="en"
      suppressHydrationWarning
      className={cn(geistSans.variable, geistMono.variable)}
    >
      <head>
        <script dangerouslySetInnerHTML={{ __html: themeBootstrapScript }} />
      </head>
      <body className="min-h-screen bg-blox-bg text-blox-text antialiased font-sans">
        {/* PHASE10-NOTE: spec said `ThemeProvider > BrandingProvider > AuthProvider`,
            but the new ThemeProvider needs to call useAuth() to sync per-user
            prefs from /api/me/theme. AuthProvider must therefore be the outer
            context. BrandingProvider is auth-independent (public endpoint), so
            its position is flexible — placed inside ThemeProvider so the body
            can still observe theme classes set on <html>. */}
        <AuthProvider>
          <ThemeProvider>
            <BrandingProvider>
              {/* Phase 11 — PreferencesProvider sits inside AuthProvider
                  (it reads the JWT for user_id) but outside the SSE/data
                  layers that consume preferences (density, default view,
                  pinned machines, saved filters). */}
              <PreferencesProvider>
                <ErrorBoundary>
                  <SSEProvider>
                    <VersionsProvider>
                      <InventoryProvider>
                        {/* AI Sessions rides the shared SSE stream (subscribe)
                            and bootstraps from GET on every reconnect. */}
                        <AISessionsProvider>
                          <TooltipProvider>
                            <ToastProvider>{children}</ToastProvider>
                          </TooltipProvider>
                        </AISessionsProvider>
                      </InventoryProvider>
                    </VersionsProvider>
                  </SSEProvider>
                </ErrorBoundary>
              </PreferencesProvider>
            </BrandingProvider>
          </ThemeProvider>
        </AuthProvider>
      </body>
    </html>
  );
}
