"use client";

// Monoform — the controls for the single Machine fleet work surface.
//
// Every control the old "Manage machines" bar carried is here: search, the
// ⌘K shortcut (which asks the shell-owned palette to open), saved filters,
// state filter, tag filter, sort, save-current-filter, table/cards, arrange,
// and the result count. Purely presentational — all state lives in the page.

import { Search, Filter, ArrowUpDown, ChevronDown, LayoutGrid, Rows3 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuLabel,
} from "@/components/ui/dropdown-menu";
import { SaveFilterButton } from "@/components/SaveFilterButton";
import { SavedFiltersDropdown } from "@/components/SavedFiltersDropdown";

export type SortOption = "manual" | "name" | "status" | "cpu" | "gpu_temp";
export type StatusFilter = "all" | "live" | "warning" | "critical" | "offline" | "stale";
export type ViewMode = "grid" | "list";

export const SORT_LABELS: Record<SortOption, string> = {
  manual: "My order",
  name: "Name (A-Z)",
  status: "Status",
  cpu: "CPU %",
  gpu_temp: "GPU Temp",
};

const STATUS_OPTIONS: { value: StatusFilter; label: string; tone: string }[] = [
  { value: "all", label: "All states", tone: "text-text-primary" },
  { value: "live", label: "Live", tone: "text-status-ok" },
  { value: "warning", label: "Warning", tone: "text-status-warning" },
  { value: "critical", label: "Critical", tone: "text-status-critical" },
  { value: "offline", label: "Offline", tone: "text-status-offline" },
  { value: "stale", label: "Stale", tone: "text-status-stale" },
];

export interface MachineFleetToolbarProps {
  search: string;
  onSearchChange: (value: string) => void;
  onOpenCommandPalette: () => void;

  statusFilter: StatusFilter;
  onStatusFilterChange: (value: StatusFilter) => void;

  sortBy: SortOption;
  onSortChange: (value: SortOption) => void;

  tags: string[];
  tagFilter: string | null;
  onTagFilterChange: (value: string | null) => void;

  savedFilterCount: number;
  onApplySavedFilter: (filter: {
    search?: unknown;
    statusFilter?: unknown;
    tagFilter?: unknown;
    sortBy?: unknown;
  }) => void;
  canSaveFilter: boolean;

  viewMode: ViewMode;
  onViewModeChange: (value: ViewMode) => void;

  onArrange: () => void;
  arrangeDisabled: boolean;

  resultCount: number;
}

export function MachineFleetToolbar({
  search,
  onSearchChange,
  onOpenCommandPalette,
  statusFilter,
  onStatusFilterChange,
  sortBy,
  onSortChange,
  tags,
  tagFilter,
  onTagFilterChange,
  savedFilterCount,
  onApplySavedFilter,
  canSaveFilter,
  viewMode,
  onViewModeChange,
  onArrange,
  arrangeDisabled,
  resultCount,
}: MachineFleetToolbarProps) {
  const filtersActive = Boolean(search) || statusFilter !== "all" || Boolean(tagFilter);
  const activeStatus = STATUS_OPTIONS.find((o) => o.value === statusFilter) ?? STATUS_OPTIONS[0];

  return (
    <div className="flex flex-wrap items-center gap-3">
      <div className="relative min-w-[200px] max-w-sm flex-1">
        <Search
          className="pointer-events-none absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-text-tertiary"
          aria-hidden="true"
        />
        <Input
          type="text"
          placeholder="Search machines..."
          value={search}
          onChange={(e) => onSearchChange(e.target.value)}
          aria-label="Search machines"
          className="h-9 border-border-subtle bg-surface-raised pl-9 pr-14 text-xs text-text-primary placeholder:text-text-disabled"
        />
        <button
          type="button"
          onClick={onOpenCommandPalette}
          className="mf-metric absolute right-2 top-1/2 hidden -translate-y-1/2 items-center gap-0.5 rounded border border-border-subtle bg-surface-base px-1.5 py-0.5 text-[10px] text-text-tertiary transition-colors duration-[var(--motion-fast)] hover:text-text-primary sm:flex"
          aria-label="Open command palette"
          title="Open command palette (⌘K)"
        >
          <span>⌘</span>
          <span>K</span>
        </button>
      </div>

      {savedFilterCount > 0 && <SavedFiltersDropdown onApply={onApplySavedFilter} />}

      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant="outline"
              size="sm"
              className="gap-1.5 border-border-subtle text-xs text-text-primary"
            >
              <Filter className="h-3 w-3" aria-hidden="true" />
              {activeStatus.label}
              <ChevronDown className="h-3 w-3 text-text-tertiary" aria-hidden="true" />
            </Button>
          }
        />
        <DropdownMenuContent align="start" className="border-border-subtle bg-surface-raised">
          {STATUS_OPTIONS.map((option) => (
            <DropdownMenuItem
              key={option.value}
              onClick={() => onStatusFilterChange(option.value)}
              className={`text-xs ${option.tone}`}
            >
              {option.label}
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>

      <DropdownMenu>
        <DropdownMenuTrigger
          render={
            <Button
              variant="outline"
              size="sm"
              className="gap-1.5 border-border-subtle text-xs text-text-primary"
            >
              <ArrowUpDown className="h-3 w-3" aria-hidden="true" />
              {SORT_LABELS[sortBy]}
              <ChevronDown className="h-3 w-3 text-text-tertiary" aria-hidden="true" />
            </Button>
          }
        />
        <DropdownMenuContent align="start" className="border-border-subtle bg-surface-raised">
          <DropdownMenuLabel className="text-text-tertiary">Sort by</DropdownMenuLabel>
          <DropdownMenuSeparator className="bg-border-subtle" />
          {(Object.keys(SORT_LABELS) as SortOption[]).map((key) => (
            <DropdownMenuItem
              key={key}
              onClick={() => onSortChange(key)}
              className="text-xs text-text-primary"
            >
              {SORT_LABELS[key]}
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>

      {tags.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          {tagFilter && (
            <button
              type="button"
              onClick={() => onTagFilterChange(null)}
              className="rounded-full border border-accent/30 bg-accent-subtle px-2 py-1 text-[10px] font-medium text-accent transition-colors duration-[var(--motion-fast)]"
            >
              Clear tag
            </button>
          )}
          {tags.map((tag) => (
            <button
              key={tag}
              type="button"
              onClick={() => onTagFilterChange(tagFilter === tag ? null : tag)}
              aria-pressed={tagFilter === tag}
              className={`rounded-full border px-2 py-1 text-[10px] font-medium transition-colors duration-[var(--motion-fast)] ${
                tagFilter === tag
                  ? "border-accent/40 bg-accent-subtle text-accent"
                  : "border-border-subtle bg-surface-raised text-text-tertiary hover:border-border-strong"
              }`}
            >
              {tag}
            </button>
          ))}
        </div>
      )}

      <div className="flex-1" />

      {filtersActive && (
        <SaveFilterButton
          currentFilter={{ search, statusFilter, tagFilter, sortBy }}
          disabled={!canSaveFilter}
        />
      )}

      {/* Table is the primary surface; cards stay as the compact alternative. */}
      <div
        className="flex items-center overflow-hidden rounded-[10px] border border-border-subtle"
        role="group"
        aria-label="Machine fleet view"
      >
        <ViewButton
          active={viewMode === "list"}
          onClick={() => onViewModeChange("list")}
          label="Table view"
        >
          <Rows3 className="h-3.5 w-3.5" aria-hidden="true" />
        </ViewButton>
        <ViewButton
          active={viewMode === "grid"}
          onClick={() => onViewModeChange("grid")}
          label="Card view"
        >
          <LayoutGrid className="h-3.5 w-3.5" aria-hidden="true" />
        </ViewButton>
      </div>

      <Button
        variant="outline"
        size="sm"
        onClick={onArrange}
        disabled={arrangeDisabled}
        className="border-border-subtle text-xs text-text-primary"
      >
        Arrange machines
      </Button>

      <span className="mf-metric text-[10px] text-text-tertiary">
        {resultCount} machine{resultCount !== 1 ? "s" : ""}
      </span>
    </div>
  );
}

function ViewButton({
  active,
  onClick,
  label,
  children,
}: {
  active: boolean;
  onClick: () => void;
  label: string;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-pressed={active}
      title={label}
      aria-label={label}
      className={`p-2 transition-colors duration-[var(--motion-fast)] ${
        active ? "bg-accent-subtle text-accent" : "text-text-tertiary hover:text-text-primary"
      }`}
    >
      {children}
    </button>
  );
}
