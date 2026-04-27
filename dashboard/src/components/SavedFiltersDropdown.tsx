"use client";

// Phase 11 — dropdown that lists saved filters and applies one on click.

import { Bookmark, ChevronDown } from "lucide-react";
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

interface SavedFiltersDropdownProps {
  onApply: (filter: Record<string, unknown>) => void;
}

export function SavedFiltersDropdown({ onApply }: SavedFiltersDropdownProps) {
  const { preferences } = usePreferences();
  if (preferences.saved_filters.length === 0) return null;

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="outline"
            size="sm"
            className="text-xs border-blox-border text-blox-text gap-1.5"
          >
            <Bookmark className="w-3 h-3" />
            Saved
            <ChevronDown className="w-3 h-3 text-blox-muted" />
          </Button>
        }
      />
      <DropdownMenuContent align="start" className="bg-blox-card border-blox-border min-w-[200px]">
        {/* Wrap DropdownMenuLabel in DropdownMenuGroup — Base UI #31. */}
        <DropdownMenuGroup>
          <DropdownMenuLabel className="text-blox-muted text-[10px] uppercase tracking-wider">
            Saved filters
          </DropdownMenuLabel>
        </DropdownMenuGroup>
        <DropdownMenuSeparator className="bg-blox-border" />
        {preferences.saved_filters.map((f) => (
          <DropdownMenuItem
            key={f.id}
            onClick={() => onApply(f.filter)}
            className="text-xs text-blox-text"
          >
            {f.name}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
