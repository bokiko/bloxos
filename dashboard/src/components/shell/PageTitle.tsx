"use client";

// Monoform — the contextual title shown in the top bar.
//
// Every authenticated route gets a title for free: the pathname is matched
// against NAV_ITEMS. A page that knows better — a machine's hostname, a
// filtered view — calls usePageTitle() to override it for as long as that
// page is mounted. The override is tagged with the pathname that registered
// it, so a title never bleeds across a navigation even for the frame between
// the old page unmounting and the new one's effects running.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { usePathname } from "next/navigation";
import { NAV_ITEMS, isNavActive } from "@/components/shell/navItems";

export interface PageTitle {
  /** The <h1> in the top bar. */
  title: string;
  /** Optional smaller line above it (`.mf-kicker`), e.g. "Fleet". */
  kicker?: string;
}

interface Registration {
  value: PageTitle;
  pathname: string;
}

interface Setters {
  set: (entry: Registration) => void;
  clear: (entry: Registration) => void;
}

// Split in two so the setters keep a stable identity: a single context would
// change on every override and re-fire every consumer's registration effect.
const SettersContext = createContext<Setters | null>(null);
const EntryContext = createContext<Registration | null>(null);

/** Turns an unmatched path segment into something presentable ("/foo-bar" → "Foo bar"). */
function titleize(segment: string): string {
  const words = segment.replace(/[-_]+/g, " ").trim();
  if (!words) return "BloxOS";
  return words.charAt(0).toUpperCase() + words.slice(1);
}

/**
 * The route's own title, used whenever no page has overridden it.
 *
 * Machine detail deliberately does not inherit Overview's label even though
 * isNavActive() treats /machine/* as part of the "/" section for the purpose
 * of highlighting the rail.
 */
export function derivePageTitle(pathname: string): PageTitle {
  if (pathname.startsWith("/machine/")) return { title: "Machine", kicker: "Fleet" };
  const item = NAV_ITEMS.find((nav) => isNavActive(pathname, nav.href));
  if (item) return { title: item.label };
  return { title: titleize(pathname.split("/").filter(Boolean).pop() ?? "") };
}

export function PageTitleProvider({ children }: { children: ReactNode }) {
  const [entry, setEntry] = useState<Registration | null>(null);

  const set = useCallback((next: Registration) => setEntry(next), []);
  // Latest wins: a page only clears the override if it still owns it.
  const clear = useCallback((owned: Registration) => {
    setEntry((current) => (current === owned ? null : current));
  }, []);
  const setters = useMemo<Setters>(() => ({ set, clear }), [set, clear]);

  return (
    <SettersContext.Provider value={setters}>
      <EntryContext.Provider value={entry}>{children}</EntryContext.Provider>
    </SettersContext.Provider>
  );
}

/**
 * Override the top bar's title from a page rendered inside AppShell.
 *
 *   usePageTitle(machine?.hostname, "Machine");
 *
 * A falsy `title` registers nothing, so it is safe to call before the data
 * that names the page has loaded — the route-derived title shows meanwhile.
 * The override clears when the page unmounts. Calling this outside AppShell
 * is a no-op rather than an error, so pages stay renderable in isolation.
 */
export function usePageTitle(title?: string | null, kicker?: string | null): void {
  const setters = useContext(SettersContext);
  const pathname = usePathname();

  useEffect(() => {
    if (!setters || !title) return;
    const entry: Registration = {
      value: kicker ? { title, kicker } : { title },
      pathname,
    };
    setters.set(entry);
    return () => setters.clear(entry);
  }, [setters, title, kicker, pathname]);
}

/** The title the top bar should render right now. */
export function useResolvedPageTitle(): PageTitle {
  const pathname = usePathname();
  const entry = useContext(EntryContext);
  if (entry && entry.pathname === pathname) return entry.value;
  return derivePageTitle(pathname);
}
