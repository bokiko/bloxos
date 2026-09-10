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
//
// NOTES ARE THEIR OWN COLUMN, not a second tenant of "Workload". Workload
// carries the machine's tags: short, structured, fleet-reported classification
// that the toolbar can filter on. A note is unstructured operator prose about
// one machine. Putting prose in among the tag chips would make neither
// scannable, and "—" already means "no tags" there, so an empty note would
// have had to invent a second meaning for the same dash.

import Link from "next/link";
import {
  AlertTriangle,
  CheckSquare,
  Gauge,
  Pencil,
  Plus,
  RefreshCw,
  Square,
  StickyNote,
  Trash2,
} from "lucide-react";
import type { MachineMetrics } from "@/lib/demo-data";
import { STATUS_VIS, type MachineStatus } from "@/components/StatusBadge";
import {
  classifyWith,
  formatBytes,
  machineGpuUtil,
  ramPct,
  timeSince,
  type LoadBaselines,
} from "@/components/fleet/fleetModel";
import { noteSummary } from "@/lib/machine-notes";
import type { NoteState } from "./useMachineNotes";

const API_ADAPTER_TAGS = ["synology", "proxmox"];

/** How much of a note the hover tooltip carries before it is cut. */
const NOTE_TITLE_MAX = 400;

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
  /** Machines the operator has marked "expected high load". */
  baselines: LoadBaselines;
  /** Marking a machine is a per-reader view preference, not a fleet command,
   * so it needs no scope — every reader can quiet their own dashboard. */
  onToggleBaseline: (machineId: string) => void;
  /** This machine's note, in whatever state it is in. */
  noteOf: (machineId: string) => NoteState;
  /** Open the note editor for this machine. */
  onOpenNote: (machineId: string) => void;
  /** fleet.metadata — whether "Add" is offered on a machine with no note. */
  canEditNotes: boolean;
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
  baselines,
  onToggleBaseline,
  noteOf,
  onOpenNote,
  canEditNotes,
}: MachineFleetTableProps) {
  const allSelected = machines.length > 0 && selected.size === machines.length;

  return (
    // An inset frame, not a second `.mf-panel`: the Machine fleet section is
    // itself a panel now, so the table draws a border inside it rather than a
    // duplicate box around it.
    <div className="mf-table-wrap mf-table-frame overflow-x-auto">
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
            <th scope="col">Notes</th>
            <th scope="col">Heartbeat</th>
            {/* Always present: the expected-high-load toggle is a per-reader
                view preference, so this column no longer depends on scope. */}
            <th scope="col">
              <span className="sr-only">Actions</span>
            </th>
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
              baselines={baselines}
              onToggleBaseline={onToggleBaseline}
              note={noteOf(machine.machine_id)}
              onOpenNote={onOpenNote}
              canEditNotes={canEditNotes}
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
  baselines,
  onToggleBaseline,
  note,
  onOpenNote,
  canEditNotes,
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
  baselines: LoadBaselines;
  onToggleBaseline: (machineId: string) => void;
  note: NoteState;
  onOpenNote: (machineId: string) => void;
  canEditNotes: boolean;
}) {
  const baselined = baselines.has(machine.machine_id);
  const { status, suppressed } = classifyWith(machine, baselines);
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
        <StateCell status={status} suppressed={suppressed} baselined={baselined} />
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
        <NoteCell
          note={note}
          hostname={hostname}
          canEdit={canEditNotes}
          onOpen={() => onOpenNote(machine.machine_id)}
        />
      </td>

      <td>
        <span className="mf-metric text-[12px] text-text-secondary">
          {machine.last_seen ? timeSince(machine.last_seen) : "never"}
        </span>
      </td>

      <td>
        <div className="flex items-center justify-end gap-1.5">
          {/* Needs no scope: it changes what THIS reader's dashboard shouts
              about, not what the machine does.

              It carries its state as a word. This used to be a bare gauge icon
              at the far right of the row — the single control that decides
              what the dashboard escalates, rendered as the least legible thing
              on the page, and the operator could not find it. */}
          <button
            type="button"
            className="mf-inline-action"
            aria-pressed={baselined}
            onClick={() => onToggleBaseline(machine.machine_id)}
            title={
              baselined
                ? `High CPU on ${hostname} is treated as expected. Click to alert on it again.`
                : `Treat high CPU on ${hostname} as its normal working state and stop alerting on it.`
            }
          >
            <Gauge className="h-3 w-3" aria-hidden="true" />
            {baselined ? "Load expected" : "Expect load"}
          </button>
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
    </tr>
  );
}

/**
 * Dot plus the state's name — the label is never dropped.
 *
 * A machine marked "expected high load" also prints why its state reads the
 * way it does: `suppressed` names the CPU figure the flag swallowed, and the
 * flag itself is shown even when the machine was not busy enough for it to
 * matter. The state is never quietly softened without saying so.
 */
function StateCell({
  status,
  suppressed,
  baselined,
}: {
  status: MachineStatus;
  suppressed?: string;
  baselined: boolean;
}) {
  const vis = STATUS_VIS[status];
  return (
    <span className="inline-flex flex-wrap items-center gap-x-2 gap-y-1">
      <span className={`mf-status-dot mf-status-${status}`} aria-hidden="true" />
      <span className={`text-[13px] ${vis.textClass}`}>{vis.label}</span>
      {baselined && (
        <span
          className="mf-suppressed"
          title={
            suppressed
              ? `Expected high load — ${suppressed} is not escalated on this machine`
              : "Expected high load — CPU saturation is not escalated on this machine"
          }
        >
          {suppressed ? `${suppressed} expected` : "load expected"}
        </span>
      )}
    </span>
  );
}

/**
 * One machine's note, in one table cell.
 *
 * Four states, all distinguishable, none pretending to be another:
 *   loading   the read is still in flight — NOT "no note"
 *   error     the read failed — NOT "no note" either; the machine may well
 *             have one, and the cell says so rather than showing a dash
 *   empty     the machine genuinely has no note
 *   has note  the first line, truncated, with the rest on hover and all of it
 *             in the dialog
 *
 * The summary is rendered as a string child. Note text is operator free text
 * from the hub and is never treated as HTML.
 */
function NoteCell({
  note,
  hostname,
  canEdit,
  onOpen,
}: {
  note: NoteState;
  hostname: string;
  canEdit: boolean;
  onOpen: () => void;
}) {
  if (note.status === "loading") {
    return (
      <span className="text-[12px] text-text-disabled" aria-busy="true">
        loading…
      </span>
    );
  }

  if (note.status === "error") {
    return (
      <button type="button" onClick={onOpen} className="mf-inline-action" title={note.error}>
        <AlertTriangle className="h-3 w-3 text-status-warning" aria-hidden="true" />
        <span className="text-text-secondary">Notes unavailable</span>
      </button>
    );
  }

  const summary = noteSummary(note.text);

  if (!summary) {
    // Nothing written here. A reader who cannot write one is told the truth
    // and offered nothing to click.
    return canEdit ? (
      <button
        type="button"
        onClick={onOpen}
        className="mf-inline-action"
        aria-label={`Add a note to ${hostname}`}
      >
        <Plus className="h-3 w-3" aria-hidden="true" />
        Add
      </button>
    ) : (
      <span className="text-[13px] text-text-disabled">—</span>
    );
  }

  return (
    <button
      type="button"
      onClick={onOpen}
      className="flex max-w-[240px] items-center gap-1.5 text-left text-[12px] text-text-secondary transition-colors duration-[var(--motion-fast)] hover:text-text-primary"
      title={note.text.slice(0, NOTE_TITLE_MAX)}
      aria-label={`${canEdit ? "Edit notes" : "Read notes"} for ${hostname}`}
    >
      <StickyNote className="h-3 w-3 shrink-0 text-text-tertiary" aria-hidden="true" />
      <span className="truncate">{summary}</span>
    </button>
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
  pressed,
  children,
}: {
  label: string;
  onClick: () => void;
  destructive?: boolean;
  /** Toggle actions carry aria-pressed, so the state is never the tint alone. */
  pressed?: boolean;
  children: React.ReactNode;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      title={label}
      aria-label={label}
      aria-pressed={pressed}
      className={`grid h-8 w-8 place-items-center rounded-[10px] border transition-colors duration-[var(--motion-fast)] ${
        pressed
          ? "border-border-strong bg-surface-elevated text-text-primary"
          : "border-transparent text-text-tertiary hover:border-border-subtle"
      } ${destructive ? "hover:text-status-critical" : "hover:text-text-primary"}`}
    >
      {children}
    </button>
  );
}
