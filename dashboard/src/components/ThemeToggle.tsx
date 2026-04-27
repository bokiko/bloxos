"use client";

import { Sun, Moon, Monitor } from "lucide-react";
import { useTheme } from "@/contexts/ThemeContext";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
} from "@/components/ui/dropdown-menu";

export function ThemeToggle() {
  // Phase 10 — toggles only the light/dark/system mode. Named-theme switching
  // happens in the user menu and the /settings page.
  const { themeMode, setMode } = useTheme();

  const Icon = themeMode === "light" ? Sun : themeMode === "dark" ? Moon : Monitor;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Toggle theme mode"
            title="Toggle theme mode"
            className="text-blox-muted hover:text-blox-text"
          >
            <Icon className="w-4 h-4" />
          </Button>
        }
      />
      <DropdownMenuContent align="end" className="bg-blox-card border-blox-border min-w-[140px]">
        <DropdownMenuItem
          onClick={() => setMode("light")}
          className="text-xs gap-2 text-blox-text"
        >
          <Sun className="w-3.5 h-3.5" />
          Light
          {themeMode === "light" && <span className="ml-auto text-[10px] text-blox-blue">●</span>}
        </DropdownMenuItem>
        <DropdownMenuItem
          onClick={() => setMode("dark")}
          className="text-xs gap-2 text-blox-text"
        >
          <Moon className="w-3.5 h-3.5" />
          Dark
          {themeMode === "dark" && <span className="ml-auto text-[10px] text-blox-blue">●</span>}
        </DropdownMenuItem>
        <DropdownMenuItem
          onClick={() => setMode("system")}
          className="text-xs gap-2 text-blox-text"
        >
          <Monitor className="w-3.5 h-3.5" />
          System
          {themeMode === "system" && <span className="ml-auto text-[10px] text-blox-blue">●</span>}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
