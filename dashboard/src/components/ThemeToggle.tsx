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
  const { theme, setTheme } = useTheme();

  const Icon = theme === "light" ? Sun : theme === "dark" ? Moon : Monitor;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label="Toggle theme"
            title="Toggle theme"
            className="text-blox-muted hover:text-blox-text"
          >
            <Icon className="w-4 h-4" />
          </Button>
        }
      />
      <DropdownMenuContent align="end" className="bg-blox-card border-blox-border min-w-[140px]">
        <DropdownMenuItem
          onClick={() => setTheme("light")}
          className="text-xs gap-2 text-blox-text"
        >
          <Sun className="w-3.5 h-3.5" />
          Light
          {theme === "light" && <span className="ml-auto text-[10px] text-blox-blue">●</span>}
        </DropdownMenuItem>
        <DropdownMenuItem
          onClick={() => setTheme("dark")}
          className="text-xs gap-2 text-blox-text"
        >
          <Moon className="w-3.5 h-3.5" />
          Dark
          {theme === "dark" && <span className="ml-auto text-[10px] text-blox-blue">●</span>}
        </DropdownMenuItem>
        <DropdownMenuItem
          onClick={() => setTheme("system")}
          className="text-xs gap-2 text-blox-text"
        >
          <Monitor className="w-3.5 h-3.5" />
          System
          {theme === "system" && <span className="ml-auto text-[10px] text-blox-blue">●</span>}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
