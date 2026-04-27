import type {
  InventoryResponse,
  InventoryMachine,
  InventoryCPUGroup,
  InventoryMemoryGroup,
  InventoryDiskRow,
  InventoryGPUGroup,
  InventoryNICRow,
} from "@/contexts/InventoryContext";

/* ============================================================================
 * Formatting helpers
 * ============================================================================ */

export function formatBytes(bytes: number | undefined | null): string {
  if (!bytes || bytes <= 0) return "—";
  const tb = bytes / 1024 ** 4;
  if (tb >= 1) return `${tb.toFixed(tb >= 10 ? 0 : 1)} TB`;
  const gb = bytes / 1024 ** 3;
  if (gb >= 1) return `${gb.toFixed(gb >= 100 ? 0 : 1)} GB`;
  const mb = bytes / 1024 ** 2;
  return `${mb.toFixed(0)} MB`;
}

export function formatNICSpeed(mbps?: number): string {
  if (!mbps || mbps <= 0) return "—";
  if (mbps >= 1000) {
    const gbps = mbps / 1000;
    return `${gbps % 1 === 0 ? gbps.toFixed(0) : gbps.toFixed(1)} Gb/s`;
  }
  return `${mbps} Mb/s`;
}

export function formatRAMSpeed(mts?: number): string {
  if (!mts || mts <= 0) return "—";
  return `${mts.toLocaleString()} MT/s`;
}

export function emDash(value: unknown): string {
  if (value === null || value === undefined) return "—";
  if (typeof value === "string" && value.trim() === "") return "—";
  if (typeof value === "number" && value === 0) return "—";
  return String(value);
}

/* ============================================================================
 * View definitions — each tab in the inventory page is (rows, columns).
 * The InventoryTable component is generic over R; views differ only in shape.
 * ============================================================================ */

export type InventoryView = "machines" | "cpus" | "memory" | "disks" | "gpus" | "nics";

export interface ColumnDef<R> {
  key: string;
  label: string;
  value: (row: R) => string | number | undefined;
  render?: (row: R) => string;
  defaultVisible?: boolean;
  numeric?: boolean;
}

export const machineColumns: ColumnDef<InventoryMachine>[] = [
  { key: "hostname", label: "Hostname", value: (r) => r.hostname },
  { key: "status", label: "Status", value: (r) => r.status },
  { key: "ip", label: "IP", value: (r) => r.ip ?? "" },
  {
    key: "cpu",
    label: "CPU",
    value: (r) => r.cpu_family ?? r.cpu_model ?? "",
    render: (r) => r.cpu_family || r.cpu_model || "—",
  },
  {
    key: "cpu_cores",
    label: "Cores",
    numeric: true,
    value: (r) => r.cpu_cores ?? 0,
    render: (r) =>
      r.cpu_cores
        ? `${r.cpu_cores}c / ${r.cpu_threads ?? r.cpu_cores}t${
            r.cpu_sockets && r.cpu_sockets > 1 ? ` × ${r.cpu_sockets}` : ""
          }`
        : "—",
  },
  {
    key: "ram",
    label: "RAM",
    numeric: true,
    value: (r) => r.ram_bytes ?? 0,
    render: (r) => formatBytes(r.ram_bytes),
  },
  {
    key: "ram_slots",
    label: "DIMMs",
    numeric: true,
    defaultVisible: false,
    value: (r) => r.ram_slots_used ?? 0,
    render: (r) =>
      r.ram_slots_used && r.ram_slots ? `${r.ram_slots_used}/${r.ram_slots}` : "—",
  },
  {
    key: "disk",
    label: "Storage",
    numeric: true,
    value: (r) => r.total_disk_bytes ?? 0,
    render: (r) =>
      r.disk_count ? `${formatBytes(r.total_disk_bytes)} (${r.disk_count} disks)` : "—",
  },
  {
    key: "gpu",
    label: "GPU",
    value: (r) => r.gpu_summary ?? "",
    render: (r) => r.gpu_summary || "—",
  },
  {
    key: "board",
    label: "Motherboard",
    defaultVisible: false,
    value: (r) => `${r.board_vendor ?? ""} ${r.board_product ?? ""}`.trim(),
    render: (r) =>
      r.board_product ? `${r.board_vendor ? r.board_vendor + " " : ""}${r.board_product}` : "—",
  },
  {
    key: "system",
    label: "System",
    defaultVisible: false,
    value: (r) => `${r.system_vendor ?? ""} ${r.system_product ?? ""}`.trim(),
    render: (r) =>
      r.system_product ? `${r.system_vendor ? r.system_vendor + " " : ""}${r.system_product}` : "—",
  },
  {
    key: "chassis",
    label: "Chassis",
    defaultVisible: false,
    value: (r) => r.chassis_type ?? "",
    render: (r) => emDash(r.chassis_type),
  },
  { key: "os", label: "OS", value: (r) => r.os ?? "", render: (r) => emDash(r.os) },
];

export const cpuColumns: ColumnDef<InventoryCPUGroup>[] = [
  { key: "model", label: "Model", value: (r) => r.model },
  { key: "vendor", label: "Vendor", value: (r) => r.vendor ?? "" },
  { key: "family", label: "Family", value: (r) => r.family ?? "" },
  { key: "model_num", label: "Model #", value: (r) => r.model_num ?? "" },
  { key: "count", label: "Count", numeric: true, value: (r) => r.count },
  {
    key: "cores",
    label: "Cores",
    numeric: true,
    value: (r) => r.cores ?? 0,
    render: (r) => (r.cores ? `${r.cores}c / ${r.threads ?? r.cores}t` : "—"),
  },
  {
    key: "machines",
    label: "Machines",
    value: (r) => r.machine_ids.length,
    render: (r) =>
      r.machine_ids.length === 1 ? "1 machine" : `${r.machine_ids.length} machines`,
  },
];

export const memoryColumns: ColumnDef<InventoryMemoryGroup>[] = [
  {
    key: "size",
    label: "Module Size",
    numeric: true,
    value: (r) => r.size_bytes,
    render: (r) => formatBytes(r.size_bytes),
  },
  { key: "type", label: "Type", value: (r) => r.type ?? "" },
  {
    key: "speed",
    label: "Speed",
    numeric: true,
    value: (r) => r.speed_mts ?? 0,
    render: (r) => formatRAMSpeed(r.speed_mts),
  },
  {
    key: "manufacturer",
    label: "Manufacturer",
    value: (r) => r.manufacturer ?? "",
    render: (r) => emDash(r.manufacturer),
  },
  { key: "count", label: "DIMMs", numeric: true, value: (r) => r.count },
  {
    key: "total",
    label: "Total RAM",
    numeric: true,
    value: (r) => r.total_bytes,
    render: (r) => formatBytes(r.total_bytes),
  },
  {
    key: "machines",
    label: "Machines",
    value: (r) => r.machine_ids.length,
    render: (r) =>
      r.machine_ids.length === 1 ? "1 machine" : `${r.machine_ids.length} machines`,
  },
];

export const diskColumns: ColumnDef<InventoryDiskRow>[] = [
  { key: "hostname", label: "Hostname", value: (r) => r.hostname },
  { key: "device", label: "Device", value: (r) => r.device },
  { key: "model", label: "Model", value: (r) => r.model ?? "", render: (r) => emDash(r.model) },
  {
    key: "size",
    label: "Size",
    numeric: true,
    value: (r) => r.size_bytes ?? 0,
    render: (r) => formatBytes(r.size_bytes),
  },
  {
    key: "type",
    label: "Type",
    value: (r) => r.type ?? "",
    render: (r) => (r.type ? r.type.toUpperCase() : "—"),
  },
  {
    key: "interface",
    label: "Interface",
    value: (r) => r.interface ?? "",
    render: (r) => (r.interface ? r.interface.toUpperCase() : "—"),
  },
  {
    key: "serial",
    label: "Serial",
    defaultVisible: false,
    value: (r) => r.serial ?? "",
    render: (r) => emDash(r.serial),
  },
  {
    key: "firmware",
    label: "Firmware",
    defaultVisible: false,
    value: (r) => r.firmware ?? "",
    render: (r) => emDash(r.firmware),
  },
];

export const gpuColumns: ColumnDef<InventoryGPUGroup>[] = [
  { key: "model", label: "Model", value: (r) => r.model },
  { key: "vendor", label: "Vendor", value: (r) => r.vendor ?? "", render: (r) => emDash(r.vendor) },
  { key: "count", label: "Count", numeric: true, value: (r) => r.count },
  {
    key: "machines",
    label: "Machines",
    value: (r) => r.machine_ids.length,
    render: (r) =>
      r.machine_ids.length === 1 ? "1 machine" : `${r.machine_ids.length} machines`,
  },
];

export const nicColumns: ColumnDef<InventoryNICRow>[] = [
  { key: "hostname", label: "Hostname", value: (r) => r.hostname },
  { key: "name", label: "Interface", value: (r) => r.name },
  { key: "ipv4", label: "IPv4", value: (r) => r.ipv4 ?? "", render: (r) => emDash(r.ipv4) },
  {
    key: "mac",
    label: "MAC",
    defaultVisible: false,
    value: (r) => r.mac ?? "",
    render: (r) => emDash(r.mac),
  },
  {
    key: "speed",
    label: "Link Speed",
    numeric: true,
    value: (r) => r.speed_mbps ?? 0,
    render: (r) => formatNICSpeed(r.speed_mbps),
  },
];

/* ============================================================================
 * Generic table operations — sort, filter, group
 * ============================================================================ */

export type SortDir = "asc" | "desc";

export function sortRows<R>(
  rows: R[],
  cols: ColumnDef<R>[],
  sortKey: string,
  dir: SortDir
): R[] {
  const col = cols.find((c) => c.key === sortKey);
  if (!col) return rows;
  const sorted = [...rows].sort((a, b) => {
    const va = col.value(a);
    const vb = col.value(b);
    if (typeof va === "number" && typeof vb === "number") {
      return va - vb;
    }
    return String(va ?? "").localeCompare(String(vb ?? ""));
  });
  return dir === "desc" ? sorted.reverse() : sorted;
}

export function filterRows<R>(rows: R[], cols: ColumnDef<R>[], query: string): R[] {
  const q = query.trim().toLowerCase();
  if (!q) return rows;
  return rows.filter((row) => {
    for (const col of cols) {
      const text = String(col.value(row) ?? "").toLowerCase();
      if (text.includes(q)) return true;
    }
    return false;
  });
}

export function groupRows<R>(
  rows: R[],
  cols: ColumnDef<R>[],
  groupKey: string
): Array<{ groupValue: string; rows: R[] }> {
  const col = cols.find((c) => c.key === groupKey);
  if (!col) return [{ groupValue: "All", rows }];
  const map = new Map<string, R[]>();
  for (const r of rows) {
    const v = col.render ? col.render(r) : String(col.value(r) ?? "—");
    if (!map.has(v)) map.set(v, []);
    map.get(v)!.push(r);
  }
  return Array.from(map.entries())
    .map(([groupValue, rs]) => ({ groupValue, rows: rs }))
    .sort((a, b) => b.rows.length - a.rows.length);
}

/* ============================================================================
 * Export helpers — CSV, JSON, Markdown
 * ============================================================================ */

function getCellText<R>(col: ColumnDef<R>, row: R): string {
  if (col.render) return col.render(row);
  const v = col.value(row);
  if (v === undefined || v === null) return "";
  return String(v);
}

function csvEscape(s: string): string {
  if (s.includes(",") || s.includes('"') || s.includes("\n")) {
    return `"${s.replace(/"/g, '""')}"`;
  }
  return s;
}

export function rowsToCSV<R>(rows: R[], cols: ColumnDef<R>[]): string {
  const visibleCols = cols.filter((c) => c.defaultVisible !== false);
  const header = visibleCols.map((c) => csvEscape(c.label)).join(",");
  const body = rows
    .map((r) => visibleCols.map((c) => csvEscape(getCellText(c, r))).join(","))
    .join("\n");
  return `${header}\n${body}\n`;
}

export function rowsToJSON<R extends Record<string, unknown>>(rows: R[]): string {
  return JSON.stringify(rows, null, 2);
}

export function rowsToMarkdown<R>(rows: R[], cols: ColumnDef<R>[]): string {
  const visibleCols = cols.filter((c) => c.defaultVisible !== false);
  const header = `| ${visibleCols.map((c) => c.label).join(" | ")} |`;
  const sep = `| ${visibleCols.map(() => "---").join(" | ")} |`;
  const body = rows
    .map(
      (r) =>
        `| ${visibleCols
          .map((c) => getCellText(c, r).replace(/\|/g, "\\|"))
          .join(" | ")} |`
    )
    .join("\n");
  return `${header}\n${sep}\n${body}\n`;
}

export function downloadAsFile(content: string, filename: string, mime: string) {
  const blob = new Blob([content], { type: mime });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

/* ============================================================================
 * View resolver — given a view name and an InventoryResponse, return the
 * rows + column definitions that the table should render.
 * ============================================================================ */

export interface ResolvedView<R> {
  rows: R[];
  cols: ColumnDef<R>[];
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function resolveView(view: InventoryView, data: InventoryResponse): ResolvedView<any> {
  switch (view) {
    case "machines":
      return { rows: data.machines, cols: machineColumns };
    case "cpus":
      return { rows: data.cpus, cols: cpuColumns };
    case "memory":
      return { rows: data.memory, cols: memoryColumns };
    case "disks":
      return { rows: data.disks, cols: diskColumns };
    case "gpus":
      return { rows: data.gpus, cols: gpuColumns };
    case "nics":
      return { rows: data.nics, cols: nicColumns };
  }
}
