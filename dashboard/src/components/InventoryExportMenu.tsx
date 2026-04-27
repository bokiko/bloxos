"use client";

import { Download, FileText, FileJson, FileType } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuGroup,
} from "@/components/ui/dropdown-menu";
import {
  rowsToCSV,
  rowsToJSON,
  rowsToMarkdown,
  downloadAsFile,
  type ColumnDef,
  type InventoryView,
} from "@/lib/inventory-utils";
import { useToast } from "@/components/Toast";

interface InventoryExportMenuProps<R> {
  view: InventoryView;
  rows: R[];
  cols: ColumnDef<R>[];
}

export function InventoryExportMenu<R>({ view, rows, cols }: InventoryExportMenuProps<R>) {
  const { addToast } = useToast();

  const timestamp = new Date().toISOString().slice(0, 10);

  const exportCSV = () => {
    const content = rowsToCSV(rows, cols);
    downloadAsFile(content, `bloxos-inventory-${view}-${timestamp}.csv`, "text/csv");
    addToast("success", `Exported ${rows.length} rows as CSV`);
  };

  const exportJSON = () => {
    const content = rowsToJSON(rows as Record<string, unknown>[]);
    downloadAsFile(
      content,
      `bloxos-inventory-${view}-${timestamp}.json`,
      "application/json"
    );
    addToast("success", `Exported ${rows.length} rows as JSON`);
  };

  const exportMarkdown = () => {
    const content = rowsToMarkdown(rows, cols);
    downloadAsFile(
      content,
      `bloxos-inventory-${view}-${timestamp}.md`,
      "text/markdown"
    );
    addToast("success", `Exported ${rows.length} rows as Markdown`);
  };

  const copyMarkdown = async () => {
    const content = rowsToMarkdown(rows, cols);
    try {
      await navigator.clipboard.writeText(content);
      addToast("success", `Markdown table copied to clipboard`);
    } catch {
      addToast("error", "Clipboard write failed — use Download instead");
    }
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        render={
          <Button
            variant="outline"
            size="sm"
            className="text-xs border-blox-border text-blox-text gap-1.5 h-8"
            title="Export current view"
          >
            <Download className="w-3 h-3" />
            Export
          </Button>
        }
      />
      <DropdownMenuContent align="end" className="bg-blox-card border-blox-border min-w-[200px]">
        <DropdownMenuGroup>
          <DropdownMenuLabel className="text-blox-muted text-[10px] uppercase tracking-wider">
            Export {rows.length} row{rows.length === 1 ? "" : "s"}
          </DropdownMenuLabel>
        </DropdownMenuGroup>
        <DropdownMenuSeparator className="bg-blox-border" />
        <DropdownMenuItem onClick={exportCSV} className="text-xs gap-2 text-blox-text">
          <FileText className="w-3.5 h-3.5" />
          Download CSV
        </DropdownMenuItem>
        <DropdownMenuItem onClick={exportJSON} className="text-xs gap-2 text-blox-text">
          <FileJson className="w-3.5 h-3.5" />
          Download JSON
        </DropdownMenuItem>
        <DropdownMenuItem onClick={exportMarkdown} className="text-xs gap-2 text-blox-text">
          <FileType className="w-3.5 h-3.5" />
          Download Markdown
        </DropdownMenuItem>
        <DropdownMenuSeparator className="bg-blox-border" />
        <DropdownMenuItem onClick={copyMarkdown} className="text-xs gap-2 text-blox-blue">
          Copy Markdown to clipboard
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
