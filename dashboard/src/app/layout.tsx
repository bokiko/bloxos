import type { Metadata } from "next";
import "./globals.css";
import { ToastProvider } from "@/components/Toast";
import { AuthProvider } from "@/contexts/AuthContext";
import { SSEProvider } from "@/contexts/SSEContext";
import { ThemeProvider } from "@/contexts/ThemeContext";
import { VersionsProvider } from "@/contexts/VersionsContext";
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
 * Inline script that applies the saved theme class to <html> before React
 * hydrates. Without this, the initial paint flashes with the wrong theme.
 *
 * The script reads localStorage synchronously and falls back to the OS
 * preference. If anything throws, default to dark (current behavior).
 */
const themeBootstrapScript = `
(function() {
  try {
    var stored = localStorage.getItem('bloxos-theme');
    var theme = stored || 'system';
    var resolved = theme;
    if (theme === 'system') {
      resolved = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    }
    document.documentElement.classList.remove('light', 'dark');
    document.documentElement.classList.add(resolved);
    document.documentElement.style.colorScheme = resolved;
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
        <ThemeProvider>
          <AuthProvider>
            <ErrorBoundary>
              <SSEProvider>
                <VersionsProvider>
                  <TooltipProvider>
                    <ToastProvider>{children}</ToastProvider>
                  </TooltipProvider>
                </VersionsProvider>
              </SSEProvider>
            </ErrorBoundary>
          </AuthProvider>
        </ThemeProvider>
      </body>
    </html>
  );
}
