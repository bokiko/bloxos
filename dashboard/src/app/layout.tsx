import type { Metadata } from "next";
import "./globals.css";
import { ToastProvider } from "@/components/Toast";
import { AuthProvider } from "@/contexts/AuthContext";
import { SSEProvider } from "@/contexts/SSEContext";
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

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html lang="en" className={cn("dark", geistSans.variable, geistMono.variable)}>
      <body className="min-h-screen bg-blox-bg text-blox-text antialiased font-sans">
        <AuthProvider>
          <ErrorBoundary>
            <SSEProvider>
              <TooltipProvider>
                <ToastProvider>
                  {children}
                </ToastProvider>
              </TooltipProvider>
            </SSEProvider>
          </ErrorBoundary>
        </AuthProvider>
      </body>
    </html>
  );
}
