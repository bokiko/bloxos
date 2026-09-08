import { LayoutGrid, Boxes, Bot, GitCompareArrows, Settings, Users, type LucideIcon } from "lucide-react";

// The authenticated routes surfaced by every non-classic layout's navigation
// (Grove sidebar, Console icon rail + tabs, Wall header). Order matches the
// study. `scope`, when set, hides the item for users without that permission.
export interface NavItem {
  href: string;
  label: string;
  Icon: LucideIcon;
  group: "workspace" | "manage";
  scope?: string;
}

export const NAV_ITEMS: NavItem[] = [
  { href: "/", label: "Fleet overview", Icon: LayoutGrid, group: "workspace" },
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
