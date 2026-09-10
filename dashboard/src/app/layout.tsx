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
 * Inline script that applies the saved Monoform appearance to <html> before
 * React hydrates. Without this, the initial paint flashes a light page.
 *
 * Reads `bloxos-appearance` ("gray" | "dark"), falling back to the legacy
 * `bloxos-theme-mode` key so an existing dark choice survives the reset, and
 * clears anything the retired multi-theme / multi-layout system left behind.
 */
const appearanceBootstrapScript = `
(function () {
  try {
    var mode = localStorage.getItem('bloxos-appearance');
    if (mode !== 'dark' && mode !== 'gray') {
      mode = localStorage.getItem('bloxos-theme-mode') === 'dark' ? 'dark' : 'gray';
    }
    var root = document.documentElement;
    root.dataset.appearance = mode;
    root.classList.add('dark');
    root.style.colorScheme = 'dark';
    Array.prototype.slice.call(root.classList).forEach(function (c) {
      if (c.indexOf('theme-') === 0) root.classList.remove(c);
    });
    delete root.dataset.layout;
    delete root.dataset.designColor;
  } catch (_) {
    document.documentElement.dataset.appearance = 'gray';
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
        <script dangerouslySetInnerHTML={{ __html: appearanceBootstrapScript }} />
      </head>
      <body className="min-h-screen bg-blox-bg text-blox-text antialiased font-sans">
        {/* ThemeProvider calls useAuth() to sync the per-user appearance from
            /api/me/theme, so AuthProvider must be the outer context.
            BrandingProvider is auth-independent (public endpoint), so its
            position is flexible — placed inside ThemeProvider so the body can
            still observe the appearance state set on <html>. */}
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
