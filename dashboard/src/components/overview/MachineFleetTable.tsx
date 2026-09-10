"use client";

// Monoform — the Machine fleet table.
//
// This is the ONE machine table on the Overview. It replaces both the old
// classic list view and the compact registers the retired layouts drew, so
// everything an operator could reach from either has to be reachable here:
// selection, per-row refresh, API-machine edit, delete, and the link into the
// machine.
//
// Every state cell prints its name next to its dot — colour never carries the
// meaning on its own. Values are mono with tabular numerals via `.mf-metric`;
// the thin `.mf-meter` bars are proportion, not decoration (blue = CPU and
// memory, violet = GPU, amber when a reading is in its warning band).

import Link from "next/link";
import { CheckSquare, Pencil, RefreshCw, Square, Trash2 } from "lucide-react";
import type { MachineMetrics } from "@/lib/demo-data";
import { STATUS_VIS, type MachineStatus } from "@/components/StatusBadge";
import { formatBytes, machineGpuUtil, ramPct, statusOf, timeSince } from "@/components/fleet/fleetModel";

const API_ADAPTER_TAGS = ["synology", "proxmox"];

export interface MachineFleetTableProps {
  machines: MachineMetrics[];
  selected: Set<string>;
  onToggleSelect: (machineId: string) => void;
  onToggleSelectAll: () => void;
  canControlFleet: boolean;
  canDeleteMachines: boolean;
  canManageAPIMachines: boolean;
  onRefresh?: (machineId: string) => void;
  onEditAPIMachine?: (machineId: string) => void;
  onDelete?: (machineId: string, hostname: string) => void;
}

function splitTags(machine: MachineMetrics): string[] {
  return machine.tags
    ? machine.tags
        .split(",")
        .map((t) => t.trim())
        .filter(Boolean)
    : [];
}

export function MachineFleetTable({
  machines,
  selected,
  onToggleSelect,
  onToggleSelectAll,
  canControlFleet,
  canDeleteMachines,
  canManageAPIMachines,
  onRefresh,
  onEditAPIMachine,
  onDelete,
}: MachineFleetTableProps) {
  const allSelected = machines.length > 0 && selected.size === machines.length;
  const showActions = Boolean(onRefresh || onDelete);

  return (
    <div className="mf-table-wrap mf-panel overflow-x-auto">
      <table className="mf-table">
        <thead>
          <tr>
            {canControlFleet && (
              <th scope="col" className="w-8">
                <button
                  type="button"
                  onClick={onToggleSelectAll}
                  className="text-text-tertiary transition-colors duration-[var(--motion-fast)] hover:text-text-primary"
                  aria-label={allSelected ? "Clear selection" : "Select all machines"}
                >
                  {allSelected ? (
                    <CheckSquare className="h-3.5 w-3.5 text-accent" />
                  ) : (
                    <Square className="h-3.5 w-3.5" />
                  )}
                </button>
              </th>
            )}
            <th scope="col">Machine</th>
            <th scope="col">State</th>
            <th scope="col">CPU</th>
            <th scope="col">GPU</th>
            <th scope="col">Memory</th>
            <th scope="col">Workload</th>
            <th scope="col">Heartbeat</th>
            {showActions && (
              <th scope="col">
                <span className="sr-only">Actions</span>
              </th>
            )}
          </tr>
        </thead>
        <tbody>
          {machines.map((machine) => (
            <MachineRow
              key={machine.machine_id}
              machine={machine}
              selected={selected.has(machine.machine_id)}
              onToggleSelect={onToggleSelect}
              canControlFleet={canControlFleet}
              canDeleteMachines={canDeleteMachines}
              canManageAPIMachines={canManageAPIMachines}
              onRefresh={onRefresh}
              onEditAPIMachine={onEditAPIMachine}
              onDelete={onDelete}
              showActions={showActions}
            />
          ))}
        </tbody>
      </table>
      {machines.length === 0 && (
        <p className="px-7 py-12 text-center text-sm text-text-tertiary">
          No machines match the current filters.
        </p>
      )}
    </div>
  );
}

function MachineRow({
  machine,
  selected,
  onToggleSelect,
  canControlFleet,
  canDeleteMachines,
  canManageAPIMachines,
  onRefresh,
  onEditAPIMachine,
  onDelete,
  showActions,
}: {
  machine: MachineMetrics;
  selected: boolean;
  onToggleSelect: (machineId: string) => void;
  canControlFleet: boolean;
  canDeleteMachines: boolean;
  canManageAPIMachines: boolean;
  onRefresh?: (machineId: string) => void;
  onEditAPIMachine?: (machineId: string) => void;
  onDelete?: (machineId: string, hostname: string) => void;
  showActions: boolean;
}) {
  const status = statusOf(machine);
  const tags = splitTags(machine);
  const isAPIMachine = tags.some((tag) => API_ADAPTER_TAGS.includes(tag.toLowerCase()));
  const cpu = Number.isFinite(machine.cpu_percent) ? machine.cpu_percent : null;
  const gpu = machineGpuUtil(machine);
  const ram = ramPct(machine);
  const hostname = machine.hostname || machine.machine_id;

  return (
    <tr data-selected={selected ? "true" : undefined}>
      {canControlFleet && (
        <td>
          <button
            type="button"
            onClick={() => onToggleSelect(machine.machine_id)}
            className="text-text-tertiary transition-colors duration-[var(--motion-fast)] hover:text-text-primary"
            aria-label={selected ? `Deselect ${hostname}` : `Select ${hostname}`}
          >
            {selected ? (
              <CheckSquare className="h-3.5 w-3.5 text-accent" />
            ) : (
              <Square className="h-3.5 w-3.5" />
            )}
          </button>
        </td>
      )}

      <td>
        <div className="flex items-center gap-2">
          <Link
            href={`/machine/${machine.machine_id}`}
            className="text-[13px] font-medium text-text-primary transition-colors duration-[var(--motion-fast)] hover:text-accent"
          >
            {hostname}
          </Link>
          {isAPIMachine && canManageAPIMachines && onEditAPIMachine && (
            <button
              type="button"
              onClick={() => onEditAPIMachine(machine.machine_id)}
              className="text-text-tertiary transition-colors duration-[var(--motion-fast)] hover:text-accent"
              title="Edit API machine"
              aria-label={`Edit ${hostname}`}
            >
              <Pencil className="h-3.5 w-3.5" />
            </button>
          )}
        </div>
        {machine.ip && (
          <div className="mf-metric mt-0.5 text-[11px] text-text-tertiary">{machine.ip}</div>
        )}
      </td>

      <td>
        <StateCell status={status} />
      </td>

      <td>
        <MeterCell value={cpu} warnAt={60} critAt={85} label={`CPU on ${hostname}`} />
      </td>

      <td>
        <MeterCell value={gpu} tone="gpu" label={`GPU utilisation on ${hostname}`} />
      </td>

      <td>
        <MeterCell value={ram} warnAt={80} critAt={92} label={`Memory on ${hostname}`} />
        {(machine.ram_total_bytes ?? 0) > 0 && (
          <div className="mf-metric mt-0.5 text-[11px] text-text-tertiary">
            {formatBytes(machine.ram_used_bytes)} / {formatBytes(machine.ram_total_bytes)}
          </div>
        )}
      </td>

      {/* No work-item data exists in the model, so this column carries the
          machine's tags — its real classification — and a dash when it has none. */}
      <td>
        {tags.length === 0 ? (
          <span className="text-[13px] text-text-disabled">—</span>
        ) : (
          <div className="flex max-w-[220px] flex-wrap gap-1">
            {tags.map((tag) => (
              <span
                key={tag}
                className="mf-metric rounded border border-border-subtle px-1.5 py-0.5 text-[10px] text-text-tertiary"
              >
                {tag}
              </span>
            ))}
          </div>
        )}
      </td>

      <td>
        <span className="mf-metric text-[12px] text-text-secondary">
          {machine.last_seen ? timeSince(machine.last_seen) : "never"}
        </span>
      </td>

      {showActions && (
        <td>
          <div className="flex items-center justify-end gap-1">
            {onRefresh && (
              <RowAction
                label={`Refresh ${hostname}`}
                onClick={() => onRefresh(machine.machine_id)}
              >
                <RefreshCw className="h-3.5 w-3.5" />
              </RowAction>
            )}
            {canDeleteMachines && onDelete && (
              <RowAction
                label={`Delete ${hostname}`}
                destructive
                onClick={() => onDelete(machine.machine_id, hostname)}
              >
                <Trash2 className="h-3.5 w-3.5" />
              </RowAction>
            )}
          </div>
        </td>
      )}
    </tr>
  );
}

/** Dot plus the state's name — the label is never dropped. */
function StateCell({ status }: { status: MachineStatus }) {
  const vis = STATUS_VIS[status];
  return (
    <span className="inline-flex items-center gap-2">
      <span className={`mf-status-dot mf-status-${status}`} aria-hidden="true" />
      <span className={`text-[13px] ${vis.textClass}`}>{vis.label}</span>
    </span>
  );
}

/**
 * A percentage with its proportion bar. `null` prints "N/A" and draws no bar:
 * a machine with no GPU is unknown here, not idle at zero.
 */
function MeterCell({
  value,
  tone = "cpu",
  warnAt,
  critAt,
  label,
}: {
  value: number | null;
  tone?: "cpu" | "gpu";
  warnAt?: number;
  critAt?: number;
  label: string;
}) {
  if (value === null) {
    return <span className="mf-metric text-[13px] text-text-disabled">N/A</span>;
  }
  const rounded = Math.round(value);
  const critical = critAt !== undefined && value > critAt;
  const warning = !critical && warnAt !== undefined && value > warnAt;
  const textTone = critical
    ? "text-status-critical"
    : warning
      ? "text-status-warning"
      : "text-text-primary";
  const meterTone = warning || critical ? "mf-meter mf-meter--warning" : tone === "gpu" ? "mf-meter mf-meter--gpu" : "mf-meter";

  return (
    <span className="inline-flex items-center whitespace-nowrap">
      <span className={`mf-metric w-9 text-right text-[13px] ${textTone}`}>{rounded}%</span>
      <span className={meterTone} role="img" aria-label={`${label}: ${rounded} percent`}>
        <i style={{ width: `${Math.min(100, Math.max(0, value))}%` }} />
      </span>
    </span>
  );
}

function RowAction({
  label,
  onClick,
  destructive,
  children,
}: {
  label: string;
  onClick: () => void;
  destructive?: boolean;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={label}
      aria-label={label}
      className={`grid h-8 w-8 place-items-center rounded-[10px] border border-transparent text-text-tertiary transition-colors duration-[var(--motion-fast)] hover:border-border-subtle ${
        destructive ? "hover:text-status-critical" : "hover:text-text-primary"
      }`}
    >
      {children}
    </button>
  );
}
