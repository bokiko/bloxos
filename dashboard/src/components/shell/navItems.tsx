import { LayoutGrid, Boxes, Bot, GitCompareArrows, Settings, Users, type LucideIcon } from "lucide-react";

// The authenticated routes surfaced by the Monoform rail. There is one
// navigation rendering, so this list is the whole of it: `group` decides
// whether an item sits in the rail's main stack ("workspace") or in the rail
// footer ("manage"), and `scope`, when set, hides the item for users without
// that permission.
export interface NavItem {
  href: string;
  label: string;
  Icon: LucideIcon;
  group: "workspace" | "manage";
  scope?: string;
}

export const NAV_ITEMS: NavItem[] = [
  { href: "/", label: "Overview", Icon: LayoutGrid, group: "workspace" },
  { href: "/inventory", label: "Inventory", Icon: Boxes, group: "workspace" },
  { href: "/sessions", label: "AI Sessions", Icon: Bot, group: "workspace" },
  { href: "/versions", label: "Versions", Icon: GitCompareArrows, group: "workspace" },
  { href: "/settings", label: "Settings", Icon: Settings, group: "manage" },
  { href: "/users", label: "Users", Icon: Users, group: "manage", scope: "users.admin" },
];

// A route is active when the pathname equals it, or (for non-root routes) is
// nested under it. "/" only matches exactly and its own machine detail pages.
export function isNavActive(pathname: string, href: string): boolean {
  if (href === "/") return pathname === "/" || pathname.startsWith("/machine/");
  return pathname === href || pathname.startsWith(href + "/");
}
