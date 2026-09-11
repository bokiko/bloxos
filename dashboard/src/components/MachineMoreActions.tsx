"use client";

// The machine lead's overflow menu.
//
// Recovery and deletion are real capabilities and they keep every permission
// check and confirm dialog they had — what changes is only how much of the
// lead they occupy. Four buttons of equal weight made "Delete" as prominent as
// "Reboot", and the one an operator reaches for hourly as prominent as the one
// they reach for twice a year. Terminal is the filled action, Reboot the
// outlined one, and the rest live behind this.
//
// Deliberately dumb: it renders the items it is handed and nothing else. Every
// gate stays at the call site in page.tsx, where it can be read beside the
// dialog it opens — and where a guard test can still see it.
//
// An empty list renders NO trigger. A viewer without fleet.admin gets no
// button at all rather than a menu that opens onto nothing.

import { MoreHorizontal, type LucideIcon } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { MF_BUTTON, MF_MENU, MF_MENU_ITEM } from "@/lib/monoform-classes";

export interface MoreAction {
  key: string;
  label: string;
  Icon: LucideIcon;
  onSelect: () => void;
  /** Drawn in the critical tone, below a separator. */
  destructive?: boolean;
}

export function MachineMoreActions({ items }: { items: MoreAction[] }) {
  if (items.length === 0) return null;

  const ordinary = items.filter((item) => !item.destructive);
  const destructive = items.filter((item) => item.destructive);

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <button
            type="button"
            className={`${MF_BUTTON} w-9 px-0`}
            aria-label="More machine actions"
            title="More machine actions"
          >
            <MoreHorizontal className="h-4 w-4" aria-hidden="true" />
          </button>
        }
      />
      <DropdownMenuContent align="end" className={`${MF_MENU} min-w-[230px]`}>
        {ordinary.map(({ key, label, Icon, onSelect }) => (
          <DropdownMenuItem key={key} className={MF_MENU_ITEM} onClick={onSelect}>
            <Icon className="h-3.5 w-3.5" aria-hidden="true" />
            {label}
          </DropdownMenuItem>
        ))}
        {ordinary.length > 0 && destructive.length > 0 && (
          <DropdownMenuSeparator className="bg-border-subtle" />
        )}
        {destructive.map(({ key, label, Icon, onSelect }) => (
          // Red by text, never by fill — the same rule MF_BUTTON_DANGER keeps.
          <DropdownMenuItem key={key} className={`${MF_MENU_ITEM} text-status-critical!`} onClick={onSelect}>
            <Icon className="h-3.5 w-3.5" aria-hidden="true" />
            {label}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
