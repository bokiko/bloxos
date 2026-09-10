"use client";

import { Download, FileText, FileJson, FileType } from "lucide-react";
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
import { MF_BUTTON, MF_MENU, MF_MENU_ITEM } from "@/lib/monoform-classes";

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
          <button type="button" className={MF_BUTTON} title="Export current view">
            <Download className="w-3.5 h-3.5" aria-hidden />
            Export
          </button>
        }
      />
      <DropdownMenuContent align="end" className={`${MF_MENU} min-w-[210px]`}>
        <DropdownMenuGroup>
          <DropdownMenuLabel className="mf-kicker uppercase">
            Export {rows.length} row{rows.length === 1 ? "" : "s"}
          </DropdownMenuLabel>
        </DropdownMenuGroup>
        <DropdownMenuSeparator className="bg-border-subtle" />
        <DropdownMenuItem onClick={exportCSV} className={MF_MENU_ITEM}>
          <FileText className="w-3.5 h-3.5" aria-hidden />
          Download CSV
        </DropdownMenuItem>
        <DropdownMenuItem onClick={exportJSON} className={MF_MENU_ITEM}>
          <FileJson className="w-3.5 h-3.5" aria-hidden />
          Download JSON
        </DropdownMenuItem>
        <DropdownMenuItem onClick={exportMarkdown} className={MF_MENU_ITEM}>
          <FileType className="w-3.5 h-3.5" aria-hidden />
          Download Markdown
        </DropdownMenuItem>
        <DropdownMenuSeparator className="bg-border-subtle" />
        <DropdownMenuItem onClick={copyMarkdown} className={`${MF_MENU_ITEM} text-accent!`}>
          Copy Markdown to clipboard
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
