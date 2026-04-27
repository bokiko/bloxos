"use client";

// Phase 10 — settings page.
//
// Sticky header with a back link to the fleet, hero with title + tagline,
// and tabs for Theme (always) and Branding (admin only).

import Link from "next/link";
import { ChevronLeft } from "lucide-react";
import { useAuth } from "@/contexts/AuthContext";
import {
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
} from "@/components/ui/tabs";
import { ThemeSettings } from "@/components/settings/ThemeSettings";
import { BrandingSettings } from "@/components/settings/BrandingSettings";

export default function SettingsPage() {
  const { hasScope } = useAuth();
  const canEditBranding = hasScope("branding.admin");
  const defaultTab = "theme";

  return (
    <div className="min-h-screen bg-blox-bg">
      <header className="sticky top-0 z-40 bg-blox-bg/80 backdrop-blur-xl border-b border-blox-border/50">
        <div className="max-w-[1100px] mx-auto px-4 sm:px-6 h-14 flex items-center gap-3">
          <Link
            href="/"
            className="inline-flex items-center gap-1 text-sm text-blox-muted hover:text-blox-text transition-colors"
          >
            <ChevronLeft className="w-4 h-4" />
            <span>Fleet</span>
          </Link>
        </div>
      </header>

      <main className="max-w-[1100px] mx-auto px-4 sm:px-6 py-10">
        <section className="mb-8">
          <h1 className="text-2xl font-bold tracking-tight text-blox-text">Settings</h1>
          <p className="text-sm text-blox-muted mt-1">
            Personal preferences and (for admins) instance branding.
          </p>
        </section>

        <Tabs defaultValue={defaultTab} className="w-full">
          <TabsList className="bg-blox-card border border-blox-border">
            <TabsTrigger value="theme">Theme</TabsTrigger>
            {canEditBranding && <TabsTrigger value="branding">Branding</TabsTrigger>}
          </TabsList>

          <TabsContent value="theme" className="mt-6">
            <ThemeSettings />
          </TabsContent>

          {canEditBranding && (
            <TabsContent value="branding" className="mt-6">
              <BrandingSettings />
            </TabsContent>
          )}
        </Tabs>
      </main>
    </div>
  );
}
