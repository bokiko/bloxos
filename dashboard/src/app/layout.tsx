import type { Metadata } from "next";
import "./globals.css";

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
    <html lang="en" className="dark">
      <body className="min-h-screen bg-blox-bg text-blox-text antialiased">
        {children}
      </body>
    </html>
  );
}
