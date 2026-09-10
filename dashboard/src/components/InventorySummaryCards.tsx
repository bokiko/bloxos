"use client";

// Fleet hardware totals.
//
// Monoform: this used to be a five-cell bento of equal KPI cards. It is now a
// typographic summary — the headline totals on one mono line, their breakdown
// on a quieter second line — so hierarchy comes from type and whitespace
// rather than from five identical boxes. (The filename is kept so no other
// phase's checkout loses a file mid-flight.)

import type { InventoryTotals } from "@/contexts/InventoryContext";
import { formatBytes } from "@/lib/inventory-utils";

interface InventorySummaryProps {
  totals: InventoryTotals;
}

export function InventorySummary({ totals }: InventorySummaryProps) {
  const storageSplit = [
    totals.total_nvme_bytes > 0 ? `${formatBytes(totals.total_nvme_bytes)} NVMe` : "",
    totals.total_ssd_bytes > 0 ? `${formatBytes(totals.total_ssd_bytes)} SSD` : "",
    totals.total_hdd_bytes > 0 ? `${formatBytes(totals.total_hdd_bytes)} HDD` : "",
  ]
    .filter(Boolean)
    .join(" · ");

  const detail = [
    totals.total_cpu_threads > 0
      ? `${totals.total_cpu_threads.toLocaleString()} threads · ${totals.unique_cpu_models} unique CPU model${totals.unique_cpu_models === 1 ? "" : "s"}`
      : `${totals.unique_cpu_models} unique CPU model${totals.unique_cpu_models === 1 ? "" : "s"}`,
    storageSplit,
    totals.total_gpu_count === 0
      ? "no GPUs"
      : `${totals.unique_gpu_models} unique GPU model${totals.unique_gpu_models === 1 ? "" : "s"}`,
    totals.unique_motherboards === 0
      ? "motherboard not detected"
      : `${totals.unique_motherboards} unique motherboard${totals.unique_motherboards === 1 ? "" : "s"}`,
  ].filter(Boolean);

  return (
    <div>
      <dl className="flex flex-wrap items-baseline gap-x-8 gap-y-3">
        <Total label="Machines" value={totals.machine_count.toLocaleString()} />
        <Total label="Cores" value={totals.total_cpu_cores.toLocaleString()} />
        <Total label="RAM" value={formatBytes(totals.total_ram_bytes)} />
        <Total label="Storage" value={formatBytes(totals.total_disk_bytes)} />
        <Total label="GPUs" value={totals.total_gpu_count.toLocaleString()} />
      </dl>
      <p className="mt-3 max-w-3xl text-[11px] leading-[1.7] text-text-tertiary">
        {detail.join(" — ")}
      </p>
    </div>
  );
}

function Total({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline gap-2">
      <dt className="mf-kicker">{label}</dt>
      <dd className="mf-metric text-[19px] leading-none text-text-primary">{value}</dd>
    </div>
  );
}
