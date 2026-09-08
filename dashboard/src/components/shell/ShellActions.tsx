"use client";

// Global actions for the non-classic layouts, mounted once by AppShell so
// Add Machine / Add API machine / the command palette are reachable from
// every authenticated route (and on mobile). Modal ownership lives here; the
// action paths are the same ones the classic dashboard uses. Cross-route
// side effects are broadcast as window events so whichever page owns the
// affected list refreshes consistently no matter where the action fired.

import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { usePathname, useRouter } from "next/navigation";
import { useAuth } from "@/contexts/AuthContext";
import { AddMachineModal } from "@/components/AddMachineModal";
import { AddAPIMachineModal } from "@/components/AddAPIMachineModal";
import { CommandPalette, useCommandPaletteHotkey } from "@/components/CommandPalette";

export const API_MACHINES_CHANGED = "bloxos:api-machines-changed";
export const OPEN_ALERTS = "bloxos:open-alerts";
export const OPEN_COMMAND = "bloxos:open-command";

interface ShellActionsValue {
  canCreateInstallTokens: boolean;
  canManageAPIMachines: boolean;
  openAddMachine?: () => void;
  openAddAPIMachine?: () => void;
  openCommandPalette: () => void;
}

const Context = createContext<ShellActionsValue | null>(null);

export function useShellActions(): ShellActionsValue {
  const ctx = useContext(Context);
  if (!ctx) throw new Error("useShellActions must be used within ShellActionsProvider");
  return ctx;
}

export function ShellActionsProvider({ children }: { children: ReactNode }) {
  const { hasScope } = useAuth();
  const router = useRouter();
  const pathname = usePathname();
  const canCreateInstallTokens = hasScope("install_tokens.admin");
  const canManageAPIMachines = hasScope("api_machines.admin");

  const [addMachineOpen, setAddMachineOpen] = useState(false);
  const [addAPIMachineOpen, setAddAPIMachineOpen] = useState(false);
  const [commandOpen, setCommandOpen] = useState(false);
  useCommandPaletteHotkey(setCommandOpen);

  const openAddMachine = useCallback(() => setAddMachineOpen(true), []);
  const openAddAPIMachine = useCallback(() => setAddAPIMachineOpen(true), []);
  const openCommandPalette = useCallback(() => setCommandOpen(true), []);

  // The dashboard manager's inline ⌘K button (rendered in the page, not the
  // chrome) asks the shell-owned palette to open via this event.
  useEffect(() => {
    const open = () => setCommandOpen(true);
    window.addEventListener(OPEN_COMMAND, open);
    return () => window.removeEventListener(OPEN_COMMAND, open);
  }, []);

  const openAlerts = useCallback(() => {
    // Alerts live on the fleet dashboard. If we are there, open in place;
    // otherwise navigate to it with a flag the dashboard reads on mount.
    if (pathname === "/") window.dispatchEvent(new CustomEvent(OPEN_ALERTS));
    else router.push("/?panel=alerts");
  }, [pathname, router]);

  const onAPIMachineSaved = useCallback(() => {
    if (typeof window !== "undefined") window.dispatchEvent(new CustomEvent(API_MACHINES_CHANGED));
  }, []);

  const value: ShellActionsValue = {
    canCreateInstallTokens,
    canManageAPIMachines,
    openAddMachine: canCreateInstallTokens ? openAddMachine : undefined,
    openAddAPIMachine: canManageAPIMachines ? openAddAPIMachine : undefined,
    openCommandPalette,
  };

  return (
    <Context.Provider value={value}>
      {children}

      <AddMachineModal open={addMachineOpen} onClose={() => setAddMachineOpen(false)} />
      {addAPIMachineOpen && (
        <AddAPIMachineModal
          open={addAPIMachineOpen}
          onClose={() => setAddAPIMachineOpen(false)}
          onSaved={onAPIMachineSaved}
        />
      )}
      <CommandPalette
        open={commandOpen}
        onOpenChange={setCommandOpen}
        onAddMachine={value.openAddMachine}
        onAddAPIMachine={value.openAddAPIMachine}
        onOpenAlerts={openAlerts}
      />
    </Context.Provider>
  );
}
