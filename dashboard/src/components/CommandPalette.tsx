"use client";

import { useEffect, useMemo, useState, useCallback } from "react";
import { useRouter } from "next/navigation";
import { Command } from "cmdk";
import {
  Dialog,
  DialogContent,
  DialogTitle,
  DialogDescription,
} from "@/components/ui/dialog";
import { useAuth } from "@/contexts/AuthContext";
import { useSSE } from "@/contexts/SSEContext";
import { useTheme } from "@/contexts/ThemeContext";
import {
  Monitor,
  Server,
  Sun,
  Moon,
  MonitorSmartphone,
  Plus,
  Bell,
  Users,
  LogOut,
  ArrowRight,
  GitBranch,
  Boxes,
  Settings,
} from "lucide-react";

interface CommandPaletteProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onAddMachine?: () => void;
  onAddAPIMachine?: () => void;
  onOpenAlerts?: () => void;
}

export function CommandPalette({
  open,
  onOpenChange,
  onAddMachine,
  onAddAPIMachine,
  onOpenAlerts,
}: CommandPaletteProps) {
  const router = useRouter();
  const { isAuthenticated, hasScope, logout } = useAuth();
  const { machines } = useSSE();
  const { setMode } = useTheme();
  const [search, setSearch] = useState("");

  // Clear search in the close event handler instead of a useEffect — avoids
  // the React 19 set-state-in-effect anti-pattern.
  const handleOpenChange = useCallback(
    (next: boolean) => {
      if (!next) setSearch("");
      onOpenChange(next);
    },
    [onOpenChange]
  );

  const runCommand = useCallback(
    (action: () => void) => {
      handleOpenChange(false);
      // Defer the action a tick so the dialog close animation doesn't
      // collide with route changes or other dialogs opening.
      setTimeout(action, 50);
    },
    [handleOpenChange]
  );

  const sortedMachines = useMemo(
    () =>
      [...machines]
        .filter((m) => m.hostname)
        .sort((a, b) => (a.hostname ?? "").localeCompare(b.hostname ?? "")),
    [machines]
  );

  if (!isAuthenticated) return null;

  return (
    <Dialog open={open} onOpenChange={handleOpenChange}>
      <DialogContent
        className="bg-blox-card border-blox-border ring-0 sm:max-w-[560px] p-0 gap-0 overflow-hidden"
        showCloseButton={false}
      >
        <DialogTitle className="sr-only">Command palette</DialogTitle>
        <DialogDescription className="sr-only">
          Search and run commands
        </DialogDescription>
        <Command label="Command palette" loop>
          <Command.Input
            value={search}
            onValueChange={setSearch}
            placeholder="Search machines, run commands…"
            autoFocus
          />
          <Command.List>
            <Command.Empty>No results found.</Command.Empty>

            {/* Navigation */}
            <Command.Group heading="Navigate">
              <Command.Item
                value="fleet dashboard home"
                onSelect={() => runCommand(() => router.push("/"))}
              >
                <Monitor />
                <span>Fleet dashboard</span>
                <ArrowRight className="ml-auto opacity-50" />
              </Command.Item>
              <Command.Item
                value="inventory hardware spec"
                onSelect={() => runCommand(() => router.push("/inventory"))}
              >
                <Boxes />
                <span>Hardware inventory</span>
              </Command.Item>
              <Command.Item
                value="versions agent rollout updates"
                onSelect={() => runCommand(() => router.push("/versions"))}
              >
                <GitBranch />
                <span>Agent versions</span>
              </Command.Item>
              {onOpenAlerts && (
                <Command.Item
                  value="alerts panel"
                  onSelect={() => runCommand(onOpenAlerts)}
                >
                  <Bell />
                  <span>Open alerts</span>
                </Command.Item>
              )}
              {hasScope("users.admin") && (
                <Command.Item
                  value="users user management"
                  onSelect={() => runCommand(() => router.push("/users"))}
                >
                  <Users />
                  <span>User management</span>
                </Command.Item>
              )}
              <Command.Item
                value="settings preferences theme branding"
                onSelect={() => runCommand(() => router.push("/settings"))}
              >
                <Settings />
                <span>Settings</span>
              </Command.Item>
            </Command.Group>

            {/* Machines — dynamic list */}
            {sortedMachines.length > 0 && (
              <Command.Group heading="Machines">
                {sortedMachines.map((m) => (
                  <Command.Item
                    key={m.machine_id}
                    value={`machine ${m.hostname} ${m.ip ?? ""} ${m.machine_id}`}
                    onSelect={() =>
                      runCommand(() => router.push(`/machine/${m.machine_id}`))
                    }
                  >
                    <Server />
                    <span>{m.hostname}</span>
                    {m.ip && (
                      <span className="ml-auto text-[10px] font-mono text-blox-muted">
                        {m.ip}
                      </span>
                    )}
                  </Command.Item>
                ))}
              </Command.Group>
            )}

            {/* Actions — gated by scope */}
            {(hasScope("install_tokens.admin") || hasScope("api_machines.admin")) && (
              <Command.Group heading="Actions">
                {hasScope("install_tokens.admin") && onAddMachine && (
                  <Command.Item
                    value="add machine install token"
                    onSelect={() => runCommand(onAddMachine)}
                  >
                    <Plus />
                    <span>Add machine (install token)</span>
                  </Command.Item>
                )}
                {hasScope("api_machines.admin") && onAddAPIMachine && (
                  <Command.Item
                    value="add api machine proxmox synology"
                    onSelect={() => runCommand(onAddAPIMachine)}
                  >
                    <Plus />
                    <span>Add API machine</span>
                  </Command.Item>
                )}
              </Command.Group>
            )}

            {/* Theme */}
            <Command.Group heading="Theme">
              <Command.Item
                value="theme light mode"
                onSelect={() => runCommand(() => setMode("light"))}
              >
                <Sun />
                <span>Switch to light mode</span>
              </Command.Item>
              <Command.Item
                value="theme dark mode"
                onSelect={() => runCommand(() => setMode("dark"))}
              >
                <Moon />
                <span>Switch to dark mode</span>
              </Command.Item>
              <Command.Item
                value="theme system auto mode"
                onSelect={() => runCommand(() => setMode("system"))}
              >
                <MonitorSmartphone />
                <span>Use system mode</span>
              </Command.Item>
            </Command.Group>

            {/* Account */}
            <Command.Group heading="Account">
              <Command.Item
                value="logout sign out"
                onSelect={() => runCommand(logout)}
              >
                <LogOut />
                <span>Logout</span>
              </Command.Item>
            </Command.Group>
          </Command.List>
        </Command>
      </DialogContent>
    </Dialog>
  );
}

/**
 * Hook that wires up Cmd+K (or Ctrl+K) globally.
 * Use it once at the page level to open the palette.
 */
export function useCommandPaletteHotkey(setOpen: (open: boolean) => void) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        setOpen(true);
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [setOpen]);
}
