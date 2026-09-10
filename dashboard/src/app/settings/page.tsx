"use client";
import { AppShell } from "@/components/shell/AppShell";

// Settings page.
//
// Sticky header with a back link to the fleet, hero with title + tagline,
// and tabs for Profile, Preferences (which now owns the single appearance
// control), and Branding / AI Sessions / Updates for admins.

import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { ChevronLeft } from "lucide-react";
import { useAuth } from "@/contexts/AuthContext";
import {
  Tabs,
  TabsList,
  TabsTrigger,
  TabsContent,
} from "@/components/ui/tabs";
import { BrandingSettings } from "@/components/settings/BrandingSettings";
import { ProfileSettings } from "@/components/settings/ProfileSettings";
import { PreferencesSettings } from "@/components/settings/PreferencesSettings";
import { AISessionsSettings } from "@/components/settings/AISessionsSettings";
import { UpdatesSettings } from "@/components/settings/UpdatesSettings";

const SETTINGS_TABS = ["profile", "preferences", "branding", "ai-sessions", "updates"] as const;
type SettingsTab = (typeof SETTINGS_TABS)[number];

export default function SettingsPage() {
  return <AppShell><SettingsContent /></AppShell>;
}

function SettingsContent() {
  const { hasScope } = useAuth();
  const searchParams = useSearchParams();
  const canEditBranding = hasScope("branding.admin");
  const canManageAISessions = hasScope("fleet.admin");
  const requested = searchParams.get("tab") as SettingsTab | null;
  const defaultTab: SettingsTab =
    requested && SETTINGS_TABS.includes(requested) &&
    (requested !== "branding" || canEditBranding) &&
    (requested !== "ai-sessions" || canManageAISessions) &&
    (requested !== "updates" || canManageAISessions)
      ? requested
      : "profile";

  return (
    <div className="min-h-screen bg-blox-bg" data-design-page>
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
            Personal preferences and (for admins) instance branding and monitoring.
          </p>
        </section>

        <Tabs defaultValue={defaultTab} className="w-full">
          {/* The one non-`line` tab list in the product. Its ground is the
              SUNKEN surface, not the panel: shadcn's selected trigger paints
              `bg-background`, so a panel-coloured list made the selected tab
              the darker one in light and the lighter one in dark. Recessed
              list, raised selection — the same way round in both themes. */}
          <TabsList className="bg-surface-sunken border border-blox-border">
            <TabsTrigger value="profile">Profile</TabsTrigger>
            <TabsTrigger value="preferences">Preferences</TabsTrigger>
            {canEditBranding && <TabsTrigger value="branding">Branding</TabsTrigger>}
            {canManageAISessions && <TabsTrigger value="ai-sessions">AI Sessions</TabsTrigger>}
          {canManageAISessions && <TabsTrigger value="updates">Updates</TabsTrigger>}
          </TabsList>

          <TabsContent value="profile" className="mt-6">
            <ProfileSettings />
          </TabsContent>

          <TabsContent value="preferences" className="mt-6">
            <PreferencesSettings />
          </TabsContent>

          {canEditBranding && (
            <TabsContent value="branding" className="mt-6">
              <BrandingSettings />
            </TabsContent>
          )}

          {canManageAISessions && (
            <TabsContent value="ai-sessions" className="mt-6">
              <AISessionsSettings />
            </TabsContent>
          )}

          {canManageAISessions && (
            <TabsContent value="updates" className="mt-6">
              <UpdatesSettings />
            </TabsContent>
          )}
        </Tabs>
      </main>
    </div>
  );
}
