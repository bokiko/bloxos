import type { Metadata } from "next";
import "./globals.css";
import { ToastProvider } from "@/components/Toast";
import { AuthProvider } from "@/contexts/AuthContext";
import { SSEProvider } from "@/contexts/SSEContext";
import { ErrorBoundary } from "@/components/ErrorBoundary";
import { Geist } from "next/font/google";
import { cn } from "@/lib/utils";

const geist = Geist({subsets:['latin'],variable:'--font-sans'});

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
    <html lang="en" className={cn("dark", "font-sans", geist.variable)}>
      <body className="min-h-screen bg-blox-bg text-blox-text antialiased">
        <AuthProvider>
          <ErrorBoundary>
            <SSEProvider>
              <ToastProvider>
                {children}
              </ToastProvider>
            </SSEProvider>
          </ErrorBoundary>
        </AuthProvider>
      </body>
    </html>
  );
}
