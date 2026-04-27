"use client";

import {
  createContext,
  useContext,
  useState,
  useCallback,
  useEffect,
  ReactNode,
} from "react";
import { useAuth } from "./AuthContext";
import { HUB_URL } from "@/lib/session";

/* ============================================================================
 * Types — mirror the InventoryResponse shape from hub/inventory.go.
 * Keep these in sync with the Go types if the schema evolves.
 * ============================================================================ */

export interface InventoryTotals {
  machine_count: number;
  online_count: number;
  total_ram_bytes: number;
  total_disk_bytes: number;
  total_nvme_bytes: number;
  total_ssd_bytes: number;
  total_hdd_bytes: number;
  total_cpu_cores: number;
  total_cpu_threads: number;
  total_gpu_count: number;
  unique_cpu_models: number;
  unique_gpu_models: number;
  unique_motherboards: number;
}

export interface InventoryMachine {
  machine_id: string;
  hostname: string;
  status: string;
  ip?: string;
  os?: string;
  cpu_model?: string;
  cpu_family?: string;
  cpu_cores?: number;
  cpu_threads?: number;
  cpu_sockets?: number;
  ram_bytes?: number;
  ram_slots?: number;
  ram_slots_used?: number;
  board_vendor?: string;
  board_product?: string;
  system_vendor?: string;
  system_product?: string;
  chassis_type?: string;
  disk_count?: number;
  total_disk_bytes?: number;
  gpu_count?: number;
  gpu_summary?: string;
  nic_count?: number;
}

export interface InventoryCPUGroup {
  model: string;
  vendor?: string;
  family?: string;
  model_num?: string;
  count: number;
  machine_ids: string[];
  cores?: number;
  threads?: number;
}

export interface InventoryMemoryGroup {
  size_bytes: number;
  type?: string;
  speed_mts?: number;
  manufacturer?: string;
  count: number;
  total_bytes: number;
  machine_ids: string[];
}

export interface InventoryDiskRow {
  machine_id: string;
  hostname: string;
  device: string;
  model?: string;
  serial?: string;
  size_bytes?: number;
  type?: string;
  interface?: string;
  firmware?: string;
}

export interface InventoryGPUGroup {
  vendor?: string;
  model: string;
  count: number;
  machine_ids: string[];
}

export interface InventoryNICRow {
  machine_id: string;
  hostname: string;
  name: string;
  ipv4?: string;
  mac?: string;
  speed_mbps?: number;
}

export interface InventoryResponse {
  generated_at: string;
  totals: InventoryTotals;
  machines: InventoryMachine[];
  cpus: InventoryCPUGroup[];
  memory: InventoryMemoryGroup[];
  disks: InventoryDiskRow[];
  gpus: InventoryGPUGroup[];
  nics: InventoryNICRow[];
}

/* ============================================================================
 * Context
 * ============================================================================ */

type Status = "idle" | "loading" | "ready" | "error";

interface InventoryContextValue {
  data: InventoryResponse | null;
  status: Status;
  error: string | null;
  refresh: () => Promise<void>;
}

const InventoryContext = createContext<InventoryContextValue | null>(null);

export function InventoryProvider({ children }: { children: ReactNode }) {
  const { authFetch, isAuthenticated } = useAuth();
  const [data, setData] = useState<InventoryResponse | null>(null);
  const [status, setStatus] = useState<Status>("idle");
  const [error, setError] = useState<string | null>(null);

  const refresh = useCallback(async () => {
    if (!isAuthenticated) return;
    setStatus("loading");
    setError(null);
    try {
      const res = await authFetch(`${HUB_URL}/api/inventory`);
      if (res.status === 404) {
        setStatus("error");
        setError(
          "Inventory endpoint not available — the hub may need to be updated to Phase 6 Unit A."
        );
        return;
      }
      if (!res.ok) {
        setStatus("error");
        setError(`Hub returned ${res.status}`);
        return;
      }
      const json: InventoryResponse = await res.json();
      setData(json);
      setStatus("ready");
    } catch (err) {
      setStatus("error");
      setError(err instanceof Error ? err.message : "Cannot reach hub");
    }
  }, [authFetch, isAuthenticated]);

  // Reset cached data when auth changes (logout, user switch). Deferred
  // to next tick so React 19's set-state-in-effect rule isn't tripped by
  // these synchronous setStates.
  useEffect(() => {
    if (isAuthenticated) return;
    const t = setTimeout(() => {
      setData(null);
      setStatus("idle");
      setError(null);
    }, 0);
    return () => clearTimeout(t);
  }, [isAuthenticated]);

  return (
    <InventoryContext.Provider value={{ data, status, error, refresh }}>
      {children}
    </InventoryContext.Provider>
  );
}

export function useInventory(): InventoryContextValue {
  const ctx = useContext(InventoryContext);
  if (!ctx) throw new Error("useInventory must be used within InventoryProvider");
  return ctx;
}
