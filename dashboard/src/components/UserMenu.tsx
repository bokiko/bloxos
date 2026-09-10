"use client";

// Account menu.
//
// Identity, Settings, User management (permission-gated) and Sign out.
// Appearance is not chosen here — Monoform has a single appearance control,
// in Settings → Preferences.

import { useRouter } from "next/navigation";
import { Users as UsersIcon, LogOut, Settings as SettingsIcon } from "lucide-react";
import { useAuth } from "@/contexts/AuthContext";
import { usePreferences } from "@/contexts/PreferencesContext";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
} from "@/components/ui/dropdown-menu";
import { Button } from "@/components/ui/button";
import { Avatar } from "@/components/Avatar";

export function UserMenu() {
  const router = useRouter();
  const { logout, hasScope, role } = useAuth();
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
        {/* Header row: avatar + display name + role. Plain JSX, not a
            DropdownMenuItem (it isn't interactive). */}
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
          className="text-xs gap-2 text-blox-red"
        >
          <LogOut className="w-3.5 h-3.5" />
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
