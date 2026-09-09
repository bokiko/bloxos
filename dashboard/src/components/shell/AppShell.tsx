"use client";

// Shared authenticated navigation shell. Classic is a pure passthrough so the
// existing pages (their own headers, modals and behavior) are untouched. The
// live layouts render persistent navigation chrome — Wall header, Grove
// sidebar (+ optional context rail), Console icon rail + tabs, Ledger top
// rule — around the page content, so navigation does not vanish on a machine
// click. EVERY non-classic layout must mount children through some chrome
// here; a layout missing from the list below renders an empty shell. The chrome's
// global actions and modals are owned by ShellActionsProvider and reachable on
// every route, including mobile.

import { type ReactNode } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { RefreshCw, Plus, Server, Command } from "lucide-react";
import { useDesign } from "@/contexts/DesignContext";
import { useAuth } from "@/contexts/AuthContext";
import { useSSE } from "@/contexts/SSEContext";
import { BrandedHeader } from "@/components/BrandedHeader";
import { BloxosMark } from "@/components/BloxosMark";
import { UserMenu } from "@/components/UserMenu";
import { useBranding } from "@/contexts/BrandingContext";
import { Button } from "@/components/ui/button";
import { NAV_ITEMS, isNavActive } from "@/components/shell/navItems";
import { ShellActionsProvider, useShellActions } from "@/components/shell/ShellActions";

export function AppShell({ children, rail }: { children: ReactNode; rail?: ReactNode }) {
  const { layout, ready } = useDesign();

  // Bounded neutral frame while the design preference resolves. Identical on
  // server and first client render (hydration-safe); the microtask cache flips
  // `ready` before paint for returning users.
  if (!ready) {
    return (
      <div className="min-h-screen bg-blox-bg flex items-center justify-center" aria-busy="true">
        <div className="h-6 w-6 rounded-full border-2 border-blox-border border-t-blox-blue animate-spin" aria-label="Loading" />
      </div>
    );
  }

  if (layout === "classic") return <>{children}</>;

  return (
    <ShellActionsProvider>
      {layout === "wall" && <WallChrome>{children}</WallChrome>}
      {layout === "grove" && <GroveChrome rail={rail}>{children}</GroveChrome>}
      {layout === "console" && <ConsoleChrome>{children}</ConsoleChrome>}
      {layout === "ledger" && <LedgerChrome>{children}</LedgerChrome>}
    </ShellActionsProvider>
  );
}

function useVisibleNav() {
  const { hasScope } = useAuth();
  return NAV_ITEMS.filter((item) => !item.scope || hasScope(item.scope));
}

/* ---- Shared action cluster ------------------------------------------------ */
function ShellRefreshButton() {
  const { hasScope } = useAuth();
  const { refreshFleet } = useSSE();
  const canControlFleet = hasScope("fleet.control");
  return (
    <button
      type="button"
      onClick={() => canControlFleet && void refreshFleet()}
      disabled={!canControlFleet}
      title={canControlFleet ? "Refresh fleet metrics" : "Refresh requires operator role"}
      aria-label="Refresh fleet metrics"
      className="inline-flex items-center justify-center w-9 h-9 rounded-md text-text-secondary hover:text-text-primary hover:bg-border-subtle disabled:opacity-40 transition-colors"
    >
      <RefreshCw className="w-4 h-4" />
    </button>
  );
}

function ShellActionCluster() {
  const { openAddMachine, openAddAPIMachine, openCommandPalette } = useShellActions();
  return (
    <>
      <ShellRefreshButton />
      <button
        type="button"
        onClick={openCommandPalette}
        title="Open command palette (⌘K)"
        aria-label="Open command palette"
        className="inline-flex items-center justify-center w-9 h-9 rounded-md text-text-secondary hover:text-text-primary hover:bg-border-subtle transition-colors"
      >
        <Command className="w-4 h-4" />
      </button>
      {openAddAPIMachine && (
        <Button
          variant="ghost"
          size="sm"
          onClick={openAddAPIMachine}
          className="text-text-secondary hover:text-text-primary gap-1.5 text-xs"
          title="Add API-polled machine (Proxmox / Synology)"
        >
          <Server className="w-3.5 h-3.5" />
          <span className="hidden sm:inline">API</span>
        </Button>
      )}
      {openAddMachine && (
        <Button
          size="sm"
          onClick={openAddMachine}
          className="bg-accent text-accent-foreground hover:bg-accent-hover gap-1.5 text-xs"
          title="Add a machine via install token"
        >
          <Plus className="w-3.5 h-3.5" />
          <span className="hidden sm:inline">Add Machine</span>
          <span className="sm:hidden">Add</span>
        </Button>
      )}
      <UserMenu />
    </>
  );
}

/* ---- 01 Operations Wall: top header ------------------------------------- */
function WallChrome({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const nav = useVisibleNav();
  return (
    <div className="ld-shell lw">
      <header className="lw-head">
        <BrandedHeader size="compact" />
        <nav className="lw-nav" aria-label="Primary">
          {nav.map(({ href, label, Icon }) => (
            <Link
              key={href}
              href={href}
              aria-label={label}
              title={label}
              className={isNavActive(pathname, href) ? "is-active" : ""}
            >
              <Icon className="w-3.5 h-3.5" />
              <span className="hidden md:inline">{label.replace(" overview", "")}</span>
            </Link>
          ))}
        </nav>
        <div className="lw-actions">
          <ShellActionCluster />
        </div>
      </header>
      <div className="lw-content">{children}</div>
    </div>
  );
}

/* ---- 02 Grove Workspace: sidebar + optional context rail ---------------- */
function GroveChrome({ children, rail }: { children: ReactNode; rail?: ReactNode }) {
  const pathname = usePathname();
  const nav = useVisibleNav();
  const workspace = nav.filter((n) => n.group === "workspace");
  const manage = nav.filter((n) => n.group === "manage");
  return (
    <div className="ld-shell">
      {/* Mobile top bar: the sidebar is hidden below 700px, so the full nav and
          the shell actions live here instead — never dropped. */}
      <header className="lg-mobilebar">
        <BrandedHeader size="compact" />
        <div className="lg-actions">
          <ShellActionCluster />
        </div>
        <nav className="lg-mnav" aria-label="Primary">
          {nav.map(({ href, label, Icon }) => (
            <Link key={href} href={href} aria-label={label} className={isNavActive(pathname, href) ? "is-active" : ""}>
              <Icon className="w-3.5 h-3.5" />
              {label.replace(" overview", "")}
            </Link>
          ))}
        </nav>
      </header>
      <div className={`lg${rail ? "" : " lg-norail"}`}>
      <aside className="lg-sidebar" aria-label="Primary navigation">
        <div className="lg-brand">
          <BrandedHeader size="compact" />
        </div>
        <div className="lg-navlabel">WORKSPACE</div>
        {workspace.map(({ href, label, Icon }) => (
          <Link key={href} href={href} className={`lg-navitem${isNavActive(pathname, href) ? " is-active" : ""}`}>
            <Icon className="lg-navicon w-4 h-4" />
            {label}
          </Link>
        ))}
        {manage.length > 0 && <div className="lg-navlabel">MANAGE</div>}
        {manage.map(({ href, label, Icon }) => (
          <Link key={href} href={href} className={`lg-navitem${isNavActive(pathname, href) ? " is-active" : ""}`}>
            <Icon className="lg-navicon w-4 h-4" />
            {label}
          </Link>
        ))}
        <div className="lg-sidebar-foot">
          <div className="lg-actions">
            <ShellActionCluster />
          </div>
        </div>
      </aside>
      <div className="lg-main">{children}</div>
      {rail && <aside className="lg-rail">{rail}</aside>}
      </div>
    </div>
  );
}

/* ---- 03 Precision Console: icon rail + tabs ----------------------------- */
function LedgerChrome({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const nav = useVisibleNav();
  const { logoUrl, branding } = useBranding();
  return (
    <div className="ld-shell ll-shell">
      <header className="ll-topbar">
        <Link href="/" className="ll-brand" aria-label={branding.title || "Fleet"}>
          {logoUrl ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={logoUrl} alt={branding.title || "BloxOS"} className="ld-mark w-6 h-6 object-contain" />
          ) : (
            <BloxosMark className="ld-mark w-6 h-6" />
          )}
          <span>{branding.title || "BloxOS"}</span>
        </Link>
        <nav className="ll-nav" aria-label="Primary">
          {nav.map(({ href, label }) => (
            <Link key={href} href={href} className={isNavActive(pathname, href) ? "is-active" : ""}>
              {label.replace(" overview", "")}
            </Link>
          ))}
        </nav>
        <div className="ll-actions">
          <ShellActionCluster />
        </div>
      </header>
      <main className="ll-content">{children}</main>
    </div>
  );
}

function ConsoleChrome({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const nav = useVisibleNav();
  const { logoUrl, branding } = useBranding();
  return (
    <div className="ld-shell lc">
      <aside className="lc-rail" aria-label="Primary navigation">
        <Link href="/" className="lc-mark" aria-label={branding.title || "Fleet"}>
          {logoUrl ? (
            // eslint-disable-next-line @next/next/no-img-element
            <img src={logoUrl} alt={branding.title || "BloxOS"} className="ld-mark w-8 h-8 object-contain" />
          ) : (
            <BloxosMark className="ld-mark w-8 h-8" />
          )}
        </Link>
        {nav.map(({ href, label, Icon }) => (
          <Link key={href} href={href} className={isNavActive(pathname, href) ? "is-active" : ""} title={label} aria-label={label}>
            <Icon className="w-4 h-4" />
          </Link>
        ))}
      </aside>
      <div className="lc-body">
        <header className="lc-head">
          <BrandedHeader size="compact" />
          <nav className="lc-tabs" aria-label="Primary">
            {nav.map(({ href, label }) => (
              <Link key={href} href={href} className={isNavActive(pathname, href) ? "is-active" : ""}>
                {label.replace(" overview", "")}
              </Link>
            ))}
          </nav>
          <div className="lc-actions">
            <ShellActionCluster />
          </div>
        </header>
        <main className="lc-content">{children}</main>
      </div>
    </div>
  );
}
