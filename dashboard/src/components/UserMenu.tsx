"use client";

import { useRouter } from "next/navigation";
import { Users as UsersIcon, LogOut, Settings as SettingsIcon } from "lucide-react";
import { useAuth } from "@/contexts/AuthContext";
import { useTheme, THEMES, type ThemeName } from "@/contexts/ThemeContext";
import { usePreferences } from "@/contexts/PreferencesContext";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuGroup,
} from "@/components/ui/dropdown-menu";
import { Button } from "@/components/ui/button";
import { Avatar } from "@/components/Avatar";
import { cn } from "@/lib/utils";

const THEME_ORDER: ThemeName[] = ["bloxos", "solarized", "dracula", "nord", "tokyo-night"];

export function UserMenu() {
  const router = useRouter();
  const { logout, hasScope, role } = useAuth();
  const { themeName, setTheme } = useTheme();
  const { preferences, myAvatarURL } = usePreferences();
  const canManageUsers = hasScope("users.admin");
  const displayName = preferences.display_name || "Account";

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Account menu"
            title={displayName}
            className="text-blox-muted hover:text-blox-text"
          >
            <Avatar url={myAvatarURL} name={displayName} size={28} />
          </Button>
        }
      />
      <DropdownMenuContent align="end" className="bg-blox-card border-blox-border min-w-[240px]">
        {/* Phase 11 — header row: avatar + display name + role. Plain JSX,
            not a DropdownMenuItem (it isn't interactive). */}
        <div className="flex items-center gap-3 px-2 py-2">
          <Avatar url={myAvatarURL} name={displayName} size={32} />
          <div className="min-w-0">
            <div className="text-sm font-medium text-blox-text truncate">{displayName}</div>
            {role && (
              <div className="text-[10px] uppercase tracking-wider text-blox-muted">
                {role.charAt(0).toUpperCase() + role.slice(1)}
              </div>
            )}
          </div>
        </div>
        <DropdownMenuSeparator className="bg-blox-border" />

        {/* Phase 10 — quick theme tile row. Each tile is a 5-column grid
            preview; clicking applies the theme immediately. The "Theme"
            label is wrapped in DropdownMenuGroup to satisfy Base UI #31. */}
        <DropdownMenuGroup>
          <DropdownMenuLabel className="text-blox-muted text-[10px] uppercase tracking-wider">
            Theme
          </DropdownMenuLabel>
        </DropdownMenuGroup>
        <div className="px-1 pb-1">
          <div className="grid grid-cols-5 gap-1">
            {THEME_ORDER.map((name) => {
              const meta = THEMES[name];
              const active = themeName === name;
              return (
                <button
                  key={name}
                  type="button"
                  onClick={() => setTheme(name)}
                  title={meta.label}
                  aria-pressed={active}
                  aria-label={`Use ${meta.label} theme`}
                  className={cn(
                    "h-9 rounded-md border transition-all",
                    active
                      ? "border-blox-blue ring-1 ring-blox-blue/40"
                      : "border-blox-border hover:border-blox-blue/40"
                  )}
                  style={{
                    background: `linear-gradient(135deg, ${meta.preview.background} 0%, ${meta.preview.surface} 60%, ${meta.preview.accent} 100%)`,
                  }}
                />
              );
            })}
          </div>
        </div>
        <DropdownMenuSeparator className="bg-blox-border" />

        <DropdownMenuItem
          onClick={() => router.push("/settings")}
          className="text-xs gap-2 text-blox-text"
        >
          <SettingsIcon className="w-3.5 h-3.5" />
          Settings
        </DropdownMenuItem>

        {canManageUsers && (
          <DropdownMenuItem
            onClick={() => router.push("/users")}
            className="text-xs gap-2 text-blox-text"
          >
            <UsersIcon className="w-3.5 h-3.5" />
            User management
          </DropdownMenuItem>
        )}

        <DropdownMenuSeparator className="bg-blox-border" />

        <DropdownMenuItem
          onClick={logout}
          className="text-xs gap-2 text-red-400"
        >
          <LogOut className="w-3.5 h-3.5" />
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
