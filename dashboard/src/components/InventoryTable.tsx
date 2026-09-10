"use client";

import { useMemo, useState } from "react";
import {
  ArrowUpDown,
  ArrowUp,
  ArrowDown,
  Eye,
  Layers,
  Search as SearchIcon,
  X,
} from "lucide-react";
import { Input } from "@/components/ui/input";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuTrigger,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuCheckboxItem,
  DropdownMenuItem,
  DropdownMenuGroup,
} from "@/components/ui/dropdown-menu";
import {
  type ColumnDef,
  type SortDir,
  sortRows,
  filterRows,
  groupRows,
} from "@/lib/inventory-utils";
import { MF_BUTTON, MF_INPUT, MF_MENU, MF_MENU_ITEM } from "@/lib/monoform-classes";

interface InventoryTableProps<R> {
  rows: R[];
  cols: ColumnDef<R>[];
}

// State reset on view change is handled by the parent passing a fresh
// `key={activeView}` to <InventoryTable>, which forces React to unmount
// and remount this component — the useState initialisers below run fresh,
// so no view-watching effect is needed (and avoids the React 19
// set-state-in-effect rule).
export function InventoryTable<R>({ rows, cols }: InventoryTableProps<R>) {
  const [search, setSearch] = useState("");
  const [sortKey, setSortKey] = useState<string>(cols[0]?.key ?? "");
  const [sortDir, setSortDir] = useState<SortDir>("asc");
  const [groupKey, setGroupKey] = useState<string | null>(null);
  const [hidden, setHidden] = useState<Set<string>>(
    () => new Set(cols.filter((c) => c.defaultVisible === false).map((c) => c.key))
  );

  const visibleCols = useMemo(
    () => cols.filter((c) => !hidden.has(c.key)),
    [cols, hidden]
  );

  const filteredRows = useMemo(() => filterRows(rows, cols, search), [rows, cols, search]);
  const sortedRows = useMemo(
    () => sortRows(filteredRows, cols, sortKey, sortDir),
    [filteredRows, cols, sortKey, sortDir]
  );
  const groupedRows = useMemo(
    () => (groupKey ? groupRows(sortedRows, cols, groupKey) : null),
    [sortedRows, cols, groupKey]
  );

  const toggleSort = (key: string) => {
    if (sortKey === key) {
      setSortDir((d) => (d === "asc" ? "desc" : "asc"));
    } else {
      setSortKey(key);
      setSortDir("asc");
    }
  };

  const toggleHidden = (key: string) => {
    setHidden((prev) => {
      const next = new Set(prev);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });
  };

  return (
    <div className="space-y-4">
      {/* Toolbar */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-[220px] max-w-md">
          <SearchIcon
            className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-text-tertiary"
            aria-hidden
          />
          <Input
            type="text"
            placeholder="Filter rows…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className={`${MF_INPUT} pl-9 pr-8 w-full`}
            aria-label="Filter rows"
          />
          {search && (
            <button
              type="button"
              onClick={() => setSearch("")}
              className="absolute right-2.5 top-1/2 -translate-y-1/2 p-0.5 text-text-tertiary hover:text-text-primary"
              aria-label="Clear filter"
            >
              <X className="w-3.5 h-3.5" />
            </button>
          )}
        </div>

        {/* Group-by dropdown */}
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <button type="button" className={MF_BUTTON}>
                <Layers className="w-3.5 h-3.5" aria-hidden />
                {groupKey ? `Group: ${cols.find((c) => c.key === groupKey)?.label}` : "Group by"}
              </button>
            }
          />
          <DropdownMenuContent align="start" className={`${MF_MENU} min-w-[170px]`}>
            <DropdownMenuGroup>
              <DropdownMenuLabel className="mf-kicker uppercase">Group rows by</DropdownMenuLabel>
            </DropdownMenuGroup>
            <DropdownMenuSeparator className="bg-border-subtle" />
            <DropdownMenuItem onClick={() => setGroupKey(null)} className={MF_MENU_ITEM}>
              No grouping
            </DropdownMenuItem>
            {cols.map((c) => (
              <DropdownMenuItem
                key={c.key}
                onClick={() => setGroupKey(c.key)}
                className={MF_MENU_ITEM}
              >
                {c.label}
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>

        {/* Column visibility */}
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <button type="button" className={MF_BUTTON}>
                <Eye className="w-3.5 h-3.5" aria-hidden />
                Columns
              </button>
            }
          />
          <DropdownMenuContent align="end" className={`${MF_MENU} min-w-[190px]`}>
            <DropdownMenuGroup>
              <DropdownMenuLabel className="mf-kicker uppercase">Visible columns</DropdownMenuLabel>
            </DropdownMenuGroup>
            <DropdownMenuSeparator className="bg-border-subtle" />
            {cols.map((c) => (
              <DropdownMenuCheckboxItem
                key={c.key}
                checked={!hidden.has(c.key)}
                onCheckedChange={() => toggleHidden(c.key)}
                className={MF_MENU_ITEM}
              >
                {c.label}
              </DropdownMenuCheckboxItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>

        <span className="mf-kicker ml-auto">
          {filteredRows.length} of {rows.length} row{rows.length === 1 ? "" : "s"}
        </span>
      </div>

      {/* Table */}
      <div className="mf-panel overflow-hidden">
        <div className="mf-table-wrap overflow-x-auto">
          <table className="mf-table">
            <thead>
              <tr>
                {visibleCols.map((c) => (
                  <th key={c.key} className={c.numeric ? "text-right" : undefined}>
                    <button
                      type="button"
                      onClick={() => toggleSort(c.key)}
                      className={`inline-flex items-center gap-1.5 transition-colors hover:text-text-primary ${
                        c.numeric ? "ml-auto" : ""
                      }`}
                      aria-label={`Sort by ${c.label}`}
                    >
                      <span>{c.label}</span>
                      {sortKey === c.key ? (
                        sortDir === "asc" ? (
                          <ArrowUp className="w-2.5 h-2.5 text-accent" aria-hidden />
                        ) : (
                          <ArrowDown className="w-2.5 h-2.5 text-accent" aria-hidden />
                        )
                      ) : (
                        <ArrowUpDown className="w-2.5 h-2.5 opacity-30" aria-hidden />
                      )}
                    </button>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {groupedRows ? (
                groupedRows.map((g) => (
                  <GroupedRows key={g.groupValue} group={g} visibleCols={visibleCols} />
                ))
              ) : sortedRows.length === 0 ? (
                <tr>
                  <td colSpan={visibleCols.length} className="text-center text-[13px] text-text-tertiary">
                    {search ? "No rows match the filter" : "No data"}
                  </td>
                </tr>
              ) : (
                sortedRows.map((row, i) => <DataRow key={i} row={row} cols={visibleCols} />)
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  );
}

function DataRow<R>({ row, cols }: { row: R; cols: ColumnDef<R>[] }) {
  return (
    <tr>
      {cols.map((c) => {
        const text = c.render ? c.render(row) : String(c.value(row) ?? "—");
        return (
          <td
            key={c.key}
            className={`text-[13px] text-text-primary ${
              c.numeric ? "mf-metric text-right" : ""
            }`}
          >
            {text}
          </td>
        );
      })}
    </tr>
  );
}

function GroupedRows<R>({
  group,
  visibleCols,
}: {
  group: { groupValue: string; rows: R[] };
  visibleCols: ColumnDef<R>[];
}) {
  return (
    <>
      <tr className="bg-surface-sunken hover:bg-surface-sunken!">
        <td
          colSpan={visibleCols.length}
          className="h-auto! py-2.5! text-[11px] font-mono uppercase tracking-[0.05em] text-accent"
        >
          {group.groupValue}
          <span className="ml-2 normal-case text-text-tertiary">
            {group.rows.length} row{group.rows.length === 1 ? "" : "s"}
          </span>
        </td>
      </tr>
      {group.rows.map((row, i) => (
        <DataRow key={i} row={row} cols={visibleCols} />
      ))}
    </>
  );
}
