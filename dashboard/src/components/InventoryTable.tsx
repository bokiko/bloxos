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
import { Button } from "@/components/ui/button";
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
  Table,
  TableHeader,
  TableBody,
  TableRow,
  TableHead,
  TableCell,
} from "@/components/ui/table";
import {
  type ColumnDef,
  type SortDir,
  sortRows,
  filterRows,
  groupRows,
} from "@/lib/inventory-utils";

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
    <div className="space-y-3">
      {/* Toolbar */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative flex-1 min-w-[220px] max-w-md">
          <SearchIcon className="absolute left-3 top-1/2 -translate-y-1/2 w-3.5 h-3.5 text-blox-muted" />
          <Input
            type="text"
            placeholder="Filter rows…"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            className="pl-9 h-8 text-xs bg-blox-card border-blox-border text-blox-text placeholder:text-blox-muted/50"
          />
          {search && (
            <button
              type="button"
              onClick={() => setSearch("")}
              className="absolute right-2 top-1/2 -translate-y-1/2 p-0.5 text-blox-muted hover:text-blox-text"
              aria-label="Clear filter"
            >
              <X className="w-3 h-3" />
            </button>
          )}
        </div>

        {/* Group-by dropdown */}
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <Button
                variant="outline"
                size="sm"
                className="text-xs border-blox-border text-blox-text gap-1.5 h-8"
              >
                <Layers className="w-3 h-3" />
                {groupKey ? `Group: ${cols.find((c) => c.key === groupKey)?.label}` : "Group by"}
              </Button>
            }
          />
          <DropdownMenuContent align="start" className="bg-blox-card border-blox-border min-w-[160px]">
            <DropdownMenuGroup>
              <DropdownMenuLabel className="text-blox-muted text-[10px] uppercase tracking-wider">
                Group rows by
              </DropdownMenuLabel>
            </DropdownMenuGroup>
            <DropdownMenuSeparator className="bg-blox-border" />
            <DropdownMenuItem onClick={() => setGroupKey(null)} className="text-xs text-blox-text">
              No grouping
            </DropdownMenuItem>
            {cols.map((c) => (
              <DropdownMenuItem
                key={c.key}
                onClick={() => setGroupKey(c.key)}
                className="text-xs text-blox-text"
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
              <Button
                variant="outline"
                size="sm"
                className="text-xs border-blox-border text-blox-text gap-1.5 h-8"
              >
                <Eye className="w-3 h-3" />
                Columns
              </Button>
            }
          />
          <DropdownMenuContent align="end" className="bg-blox-card border-blox-border min-w-[180px]">
            <DropdownMenuGroup>
              <DropdownMenuLabel className="text-blox-muted text-[10px] uppercase tracking-wider">
                Visible columns
              </DropdownMenuLabel>
            </DropdownMenuGroup>
            <DropdownMenuSeparator className="bg-blox-border" />
            {cols.map((c) => (
              <DropdownMenuCheckboxItem
                key={c.key}
                checked={!hidden.has(c.key)}
                onCheckedChange={() => toggleHidden(c.key)}
                className="text-xs text-blox-text"
              >
                {c.label}
              </DropdownMenuCheckboxItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>

        <span className="text-[10px] text-blox-muted font-mono tabular-nums ml-auto">
          {filteredRows.length} of {rows.length} row{rows.length === 1 ? "" : "s"}
        </span>
      </div>

      {/* Table */}
      <div className="bg-blox-card border border-blox-border rounded-xl overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow className="border-b-blox-border hover:bg-transparent">
              {visibleCols.map((c) => (
                <TableHead
                  key={c.key}
                  className={`text-blox-muted text-[11px] uppercase tracking-[0.06em] font-medium ${
                    c.numeric ? "text-right" : ""
                  }`}
                >
                  <button
                    type="button"
                    onClick={() => toggleSort(c.key)}
                    className={`inline-flex items-center gap-1 hover:text-blox-text transition-colors ${
                      c.numeric ? "ml-auto" : ""
                    }`}
                  >
                    <span>{c.label}</span>
                    {sortKey === c.key ? (
                      sortDir === "asc" ? (
                        <ArrowUp className="w-2.5 h-2.5 text-blox-blue" />
                      ) : (
                        <ArrowDown className="w-2.5 h-2.5 text-blox-blue" />
                      )
                    ) : (
                      <ArrowUpDown className="w-2.5 h-2.5 opacity-30" />
                    )}
                  </button>
                </TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {groupedRows ? (
              groupedRows.map((g) => (
                <GroupedRows key={g.groupValue} group={g} visibleCols={visibleCols} />
              ))
            ) : sortedRows.length === 0 ? (
              <TableRow>
                <TableCell
                  colSpan={visibleCols.length}
                  className="text-center py-8 text-blox-muted text-xs"
                >
                  {search ? "No rows match the filter" : "No data"}
                </TableCell>
              </TableRow>
            ) : (
              sortedRows.map((row, i) => (
                <DataRow key={i} row={row} cols={visibleCols} alt={i % 2 === 1} />
              ))
            )}
          </TableBody>
        </Table>
      </div>
    </div>
  );
}

function DataRow<R>({
  row,
  cols,
  alt,
}: {
  row: R;
  cols: ColumnDef<R>[];
  alt: boolean;
}) {
  return (
    <TableRow
      className={`border-b-blox-border/30 hover:bg-blox-border/10 transition-colors ${
        alt ? "bg-blox-bg/30" : ""
      }`}
    >
      {cols.map((c) => {
        const text = c.render ? c.render(row) : String(c.value(row) ?? "—");
        return (
          <TableCell
            key={c.key}
            className={`text-xs text-blox-text ${
              c.numeric ? "text-right tabular-nums font-mono" : ""
            }`}
          >
            {text}
          </TableCell>
        );
      })}
    </TableRow>
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
      <TableRow className="bg-blox-bg/40 hover:bg-blox-bg/40 border-b-blox-border/40">
        <TableCell
          colSpan={visibleCols.length}
          className="text-[11px] uppercase tracking-[0.06em] font-medium text-blox-blue py-2"
        >
          {group.groupValue}
          <span className="ml-2 text-blox-muted normal-case font-normal tabular-nums">
            {group.rows.length} row{group.rows.length === 1 ? "" : "s"}
          </span>
        </TableCell>
      </TableRow>
      {group.rows.map((row, i) => (
        <DataRow key={i} row={row} cols={visibleCols} alt={i % 2 === 1} />
      ))}
    </>
  );
}
