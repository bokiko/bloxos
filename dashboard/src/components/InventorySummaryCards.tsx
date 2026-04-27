"use client";

import { motion } from "framer-motion";
import {
  MemoryStick,
  Cpu,
  HardDrive,
  Gpu,
  Server,
} from "lucide-react";
import type { InventoryTotals } from "@/contexts/InventoryContext";
import { formatBytes } from "@/lib/inventory-utils";

interface InventorySummaryCardsProps {
  totals: InventoryTotals;
}

export function InventorySummaryCards({ totals }: InventorySummaryCardsProps) {
  return (
    <motion.div
      initial={{ opacity: 0, y: -8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.25, delay: 0.05 }}
      className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-5 gap-px bg-blox-border rounded-lg overflow-hidden border border-blox-border"
    >
      <SummaryCell
        label="Total RAM"
        icon={<MemoryStick className="w-3 h-3" />}
        value={formatBytes(totals.total_ram_bytes)}
        sub={`across ${totals.machine_count} machine${totals.machine_count === 1 ? "" : "s"}`}
      />
      <SummaryCell
        label="Total Cores"
        icon={<Cpu className="w-3 h-3" />}
        value={totals.total_cpu_cores.toString()}
        sub={
          totals.total_cpu_threads > 0
            ? `${totals.total_cpu_threads.toLocaleString()} threads · ${totals.unique_cpu_models} unique`
            : `${totals.unique_cpu_models} unique`
        }
      />
      <SummaryCell
        label="Total Storage"
        icon={<HardDrive className="w-3 h-3" />}
        value={formatBytes(totals.total_disk_bytes)}
        sub={
          totals.total_nvme_bytes > 0 || totals.total_ssd_bytes > 0 || totals.total_hdd_bytes > 0
            ? [
                totals.total_nvme_bytes > 0 ? `${formatBytes(totals.total_nvme_bytes)} NVMe` : "",
                totals.total_ssd_bytes > 0 ? `${formatBytes(totals.total_ssd_bytes)} SSD` : "",
                totals.total_hdd_bytes > 0 ? `${formatBytes(totals.total_hdd_bytes)} HDD` : "",
              ]
                .filter(Boolean)
                .join(" · ")
            : "—"
        }
      />
      <SummaryCell
        label="GPUs"
        icon={<Gpu className="w-3 h-3" />}
        value={totals.total_gpu_count.toString()}
        sub={
          totals.total_gpu_count === 0
            ? "no GPUs"
            : totals.unique_gpu_models === 1
            ? "1 unique model"
            : `${totals.unique_gpu_models} unique models`
        }
      />
      <SummaryCell
        label="Motherboards"
        icon={<Server className="w-3 h-3" />}
        value={totals.unique_motherboards.toString()}
        sub={
          totals.unique_motherboards === 0
            ? "not detected"
            : totals.unique_motherboards === 1
            ? "1 unique board"
            : "unique boards"
        }
      />
    </motion.div>
  );
}

function SummaryCell({
  label,
  icon,
  value,
  sub,
}: {
  label: string;
  icon: React.ReactNode;
  value: string;
  sub: string;
}) {
  return (
    <div className="bg-blox-card p-3 relative">
      <div className="flex items-center gap-1.5 mb-1">
        <span className="text-blox-muted shrink-0">{icon}</span>
        <span className="text-[10px] uppercase tracking-wider font-medium text-blox-muted">
          {label}
        </span>
      </div>
      <div className="text-xl font-semibold text-blox-text tabular-nums leading-none">
        {value}
      </div>
      <div className="text-[10px] text-blox-muted mt-1.5 tabular-nums truncate" title={sub}>
        {sub}
      </div>
    </div>
  );
}
