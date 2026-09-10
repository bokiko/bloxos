"use client";

// Monoform — the one authenticated app shell.
//
// There used to be five selectable layouts and a chrome component per layout.
// There is now a single geometry: a fixed left rail carrying the whole of
// NAV_ITEMS, and a top bar carrying the page's title and the global actions.
// Both contrast modes render exactly this markup; nothing here branches on a
// preference, and nothing here branches on screen size — below 700px the CSS
// turns the rail into a horizontally scrollable strip, so there is no second
// mobile navigation to keep in sync.
//
// Global actions and their modals are owned by ShellActionsProvider, so Add
// Machine / Add API machine / the command palette / alerts are reachable from
// every authenticated route.

import { type ReactNode } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { RefreshCw, Plus, Server, Command, Bell } from "lucide-react";
import { useAuth } from "@/contexts/AuthContext";
import { useSSE } from "@/contexts/SSEContext";
import { useBranding } from "@/contexts/BrandingContext";
import { usePreferences } from "@/contexts/PreferencesContext";
import { BrandedHeader } from "@/components/BrandedHeader";
import { UserMenu } from "@/components/UserMenu";
import { ThemeToggle } from "@/components/ThemeToggle";
import { Avatar } from "@/components/Avatar";
import { NAV_ITEMS, isNavActive, type NavItem } from "@/components/shell/navItems";
import { ShellActionsProvider, useShellActions } from "@/components/shell/ShellActions";
import { PageTitleProvider, useResolvedPageTitle } from "@/components/shell/PageTitle";

export function AppShell({ children }: { children: ReactNode }) {
  return (
    <ShellActionsProvider>
      <PageTitleProvider>
        <div className="mf-shell">
          <Rail />
          <div className="mf-main">
            <TopBar />
            <main className="mf-content">{children}</main>
          </div>
        </div>
      </PageTitleProvider>
    </ShellActionsProvider>
  );
}

// Permission filtering for the navigation: an item with a `scope` is not
// rendered at all for users who lack it.
function useVisibleNav(): NavItem[] {
  const { hasScope } = useAuth();
  return NAV_ITEMS.filter((item) => !item.scope || hasScope(item.scope));
}

/* ---- Left rail ----------------------------------------------------------- */

function Rail() {
  const { branding } = useBranding();
  const nav = useVisibleNav();
  const workspace = nav.filter((item) => item.group === "workspace");
  const manage = nav.filter((item) => item.group === "manage");

  return (
    <aside className="mf-rail" aria-label="Primary navigation">
      <Link href="/" className="mf-brand" aria-label={branding.title || "BloxOS"}>
        <BrandedHeader size="compact" tone="rail" />
      </Link>

      <nav className="mf-nav-group" aria-label="Workspace">
        <div className="mf-nav-label">Workspace</div>
        {workspace.map((item) => (
          <RailLink key={item.href} item={item} />
        ))}
      </nav>

      <div className="mf-rail-footer">
        {manage.length > 0 && (
          <nav className="mf-nav-group" aria-label="Manage">
            <div className="mf-nav-label">Manage</div>
            {manage.map((item) => (
              <RailLink key={item.href} item={item} />
            ))}
          </nav>
        )}
        <RailAccount />
      </div>
    </aside>
  );
}

function RailLink({ item: { href, label, Icon } }: { item: NavItem }) {
  const pathname = usePathname();
  const active = isNavActive(pathname, href);
  return (
    <Link
      href={href}
      className="mf-nav-item"
      aria-current={active ? "page" : undefined}
      title={label}
    >
      <Icon className="w-4 h-4 shrink-0" aria-hidden="true" />
      <span className="mf-nav-copy">{label}</span>
    </Link>
  );
}

// Identity only. Account *actions* (settings, user management, sign out) live
// in the top bar's UserMenu, which stays visible at every width — so this is
// deliberately not a second dropdown.
function RailAccount() {
  const { role } = useAuth();
  const { preferences, myAvatarURL } = usePreferences();
  const displayName = preferences.display_name || "Account";
  return (
    <div className="mf-account">
      <Avatar url={myAvatarURL} name={displayName} size={28} />
      <span className="mf-user-copy">
        <span>{displayName}</span>
        {role && <span className="font-mono">{role.charAt(0).toUpperCase() + role.slice(1)}</span>}
      </span>
    </div>
  );
}

/* ---- Top bar ------------------------------------------------------------- */

function TopBar() {
  const { title, kicker } = useResolvedPageTitle();
  const { openAddMachine, openAddAPIMachine } = useShellActions();

  return (
    <header className="mf-topbar">
      <div className="mf-topbar-title">
        {kicker && <div className="mf-kicker">{kicker}</div>}
        <h1 className="mf-page-title">{title}</h1>
      </div>
      <div className="mf-topbar-actions">
        <RefreshButton />
        <CommandPaletteButton />
        <AlertsButton />
        {openAddAPIMachine && (
          <button
            type="button"
            onClick={openAddAPIMachine}
            className="mf-icon-button"
            title="Add API-polled machine (Proxmox / Synology)"
            aria-label="Add API-polled machine"
          >
            <Server className="w-4 h-4" />
          </button>
        )}
        {openAddMachine && (
          <button
            type="button"
            onClick={openAddMachine}
            className="mf-action"
            title="Add a machine via install token"
            // The visible label is one of the elements the CSS collapses when
            // the top bar narrows, so the name is on the button itself.
            aria-label="Add Machine"
          >
            <Plus className="w-4 h-4" aria-hidden="true" />
            <span className="mf-utility-label">Add Machine</span>
          </button>
        )}
        <ThemeToggle />
        <UserMenu />
      </div>
    </header>
  );
}

function RefreshButton() {
  const { hasScope } = useAuth();
  const { refreshFleet } = useSSE();
  const canControlFleet = hasScope("fleet.control");
  return (
    <button
      type="button"
      onClick={() => canControlFleet && void refreshFleet()}
      disabled={!canControlFleet}
      className="mf-icon-button"
      title={canControlFleet ? "Refresh fleet metrics" : "Refresh requires operator role"}
      aria-label="Refresh fleet metrics"
    >
      <RefreshCw className="w-4 h-4" />
    </button>
  );
}

function CommandPaletteButton() {
  const { openCommandPalette } = useShellActions();
  return (
    <button
      type="button"
      onClick={openCommandPalette}
      className="mf-icon-button"
      title="Open command palette (⌘K)"
      aria-label="Open command palette"
    >
      <Command className="w-4 h-4" />
    </button>
  );
}

// The count is the state, in text — the icon never signals "there are alerts"
// by colour alone.
function AlertsButton() {
  const { openAlerts } = useShellActions();
  const { alertCount } = useSSE();
  const label = alertCount > 0 ? `Alerts — ${alertCount} active` : "Alerts — none active";
  return (
    <button
      type="button"
      onClick={openAlerts}
      className="mf-icon-button"
      title={label}
      aria-label={label}
    >
      <Bell className="w-4 h-4" />
      {alertCount > 0 && (
        <span className="mf-badge font-mono" aria-hidden="true">
          {alertCount > 99 ? "99+" : alertCount}
        </span>
      )}
    </button>
  );
}
