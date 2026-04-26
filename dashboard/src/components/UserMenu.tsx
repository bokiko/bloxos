"use client";

import { useRouter } from "next/navigation";
import { User, Users as UsersIcon, LogOut } from "lucide-react";
import { useAuth } from "@/contexts/AuthContext";
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

export function UserMenu() {
  const router = useRouter();
  const { logout, hasScope, role } = useAuth();
  const canManageUsers = hasScope("users.admin");

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Account menu"
            title="Account"
            className="text-blox-muted hover:text-blox-text"
          >
            <User className="w-4 h-4" />
          </Button>
        }
      />
      <DropdownMenuContent align="end" className="bg-blox-card border-blox-border min-w-[200px]">
        {/* Group wrapper is required by Base UI — DropdownMenuLabel is a
            MenuPrimitive.GroupLabel and must live inside a Menu.Group, or
            it throws Base UI error #31 at runtime. */}
        <DropdownMenuGroup>
          <DropdownMenuLabel className="text-blox-muted text-[10px] uppercase tracking-wider">
            {role ? `Role: ${role}` : "Account"}
          </DropdownMenuLabel>
        </DropdownMenuGroup>
        <DropdownMenuSeparator className="bg-blox-border" />

        {canManageUsers && (
          <DropdownMenuItem
            onClick={() => router.push("/users")}
            className="text-xs gap-2 text-blox-text"
          >
            <UsersIcon className="w-3.5 h-3.5" />
            User management
          </DropdownMenuItem>
        )}

        {canManageUsers && <DropdownMenuSeparator className="bg-blox-border" />}

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
