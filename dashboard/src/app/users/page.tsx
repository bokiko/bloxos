"use client";
import { AppShell } from "@/components/shell/AppShell";

// User management. Gated on `users.admin` twice over: the rail hides the item
// for anyone without the scope, and this page redirects them away.
//
// Monoform: the shell owns the title, the rail and the global actions, so the
// page starts at its lead and owns only the table, the add dialog and the
// delete dialog.

import { FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import { Plus, Trash2, ShieldCheck, ShieldAlert, Eye, AlertTriangle } from "lucide-react";
import { useAuth, type UserRole } from "@/contexts/AuthContext";
import { HUB_URL } from "@/lib/session";
import { Input } from "@/components/ui/input";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { StatusCell } from "@/components/MonoformStatus";
import {
  MF_BUTTON_DANGER,
  MF_BUTTON_QUIET,
  MF_DIALOG,
  MF_INPUT,
  MF_LABEL,
  MF_MENU,
  MF_MENU_ITEM,
  MF_PANEL_HEAD,
  MF_PANEL_TITLE,
} from "@/lib/monoform-classes";

interface UserRecord {
  id: string;
  username: string;
  role: UserRole;
  created_at: string;
  password_changed: boolean;
  pin_changed: boolean;
}

// Role is an attribute, not a health reading, so it is drawn in the one blue
// (admin), the text colour (operator) or muted (viewer) — never in the
// green/amber/red that this product reserves for real machine state.
const ROLE_BADGE: Record<UserRole, { label: string; cls: string; icon: React.ReactNode }> = {
  admin:    { label: "Admin",    cls: "border-accent/40 bg-accent-subtle text-accent",              icon: <ShieldCheck className="w-3 h-3" /> },
  operator: { label: "Operator", cls: "border-border-strong bg-surface-elevated text-text-primary", icon: <ShieldAlert className="w-3 h-3" /> },
  viewer:   { label: "Viewer",   cls: "border-border-default bg-surface-elevated text-text-tertiary", icon: <Eye className="w-3 h-3" /> },
};

function timeSince(iso: string): string {
  const ms = new Date(iso).getTime();
  if (!isFinite(ms)) return "";
  const sec = Math.floor((Date.now() - ms) / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  const d = Math.floor(hr / 24);
  return `${d}d ago`;
}

export default function UsersPage() {
  return <AppShell><UsersContent /></AppShell>;
}

function UsersContent() {
  const router = useRouter();
  const { hasScope, authFetch, isAuthenticated, role: myRole } = useAuth();
  const canManage = isAuthenticated && hasScope("users.admin");

  const [users, setUsers] = useState<UserRecord[]>([]);
  const [loading, setLoading] = useState(true);
  const [pageError, setPageError] = useState<string | null>(null);

  const [addOpen, setAddOpen] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<UserRecord | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  // Boot the user back to / if they're authenticated but not admin.
  useEffect(() => {
    if (isAuthenticated && !hasScope("users.admin")) {
      router.replace("/");
    }
  }, [isAuthenticated, hasScope, router]);

  const loadUsers = useCallback(async () => {
    if (!canManage) return;
    setLoading(true);
    try {
      const res = await authFetch(`${HUB_URL}/api/users`);
      if (!res.ok) {
        setPageError(`Failed to load users (${res.status})`);
        setUsers([]);
        return;
      }
      const data = await res.json();
      setUsers(Array.isArray(data) ? data : []);
      setPageError(null);
    } catch {
      setPageError("Cannot reach hub");
    } finally {
      setLoading(false);
    }
  }, [authFetch, canManage]);

  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void loadUsers();
  }, [loadUsers]);

  const adminCount = useMemo(() => users.filter((u) => u.role === "admin").length, [users]);

  const handleRoleChange = useCallback(async (target: UserRecord, next: UserRole) => {
    if (target.role === next) return;
    if (target.role === "admin" && next !== "admin" && adminCount <= 1) {
      setPageError("Cannot demote the last admin.");
      return;
    }
    setBusyId(target.id);
    setPageError(null);
    try {
      const res = await authFetch(`${HUB_URL}/api/users/${target.id}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ role: next }),
      });
      const body = await res.json().catch(() => null);
      if (!res.ok) {
        setPageError(body?.error || `Update failed (${res.status})`);
      } else {
        setUsers((prev) => prev.map((u) => (u.id === target.id ? { ...u, role: next } : u)));
      }
    } catch {
      setPageError("Cannot reach hub");
    } finally {
      setBusyId(null);
    }
  }, [authFetch, adminCount]);

  const handleDelete = useCallback(async () => {
    if (!deleteTarget) return;
    setBusyId(deleteTarget.id);
    setPageError(null);
    try {
      const res = await authFetch(`${HUB_URL}/api/users/${deleteTarget.id}`, { method: "DELETE" });
      const body = await res.json().catch(() => null);
      if (!res.ok) {
        setPageError(body?.error || `Delete failed (${res.status})`);
      } else {
        setUsers((prev) => prev.filter((u) => u.id !== deleteTarget.id));
        setDeleteTarget(null);
      }
    } catch {
      setPageError("Cannot reach hub");
    } finally {
      setBusyId(null);
    }
  }, [authFetch, deleteTarget]);

  if (!canManage) {
    // Render a thin scaffold while the redirect runs so we don't flash
    // a fully empty page.
    return (
      <div className="flex items-center gap-2.5 py-20 text-[13px] text-text-tertiary">
        <span className="w-3.5 h-3.5 border-2 border-accent/40 border-t-accent rounded-full animate-spin" />
        Loading…
      </div>
    );
  }

  return (
    <>
      <div className="mf-intro">
        <dl className="flex flex-wrap items-baseline gap-x-8 gap-y-3">
          <div className="flex items-baseline gap-2">
            <dt className="mf-kicker">Accounts</dt>
            <dd className="mf-metric text-[19px] leading-none text-text-primary">{users.length}</dd>
          </div>
          <div className="flex items-baseline gap-2">
            <dt className="mf-kicker">Admins</dt>
            <dd className="mf-metric text-[19px] leading-none text-text-primary">{adminCount}</dd>
          </div>
        </dl>
        <div className="mf-intro-actions">
          <button type="button" onClick={() => setAddOpen(true)} className="mf-action inline-flex items-center gap-2">
            <Plus className="w-3.5 h-3.5" aria-hidden />
            Add User
          </button>
        </div>
        <p>
          Signed in as <span className="text-text-primary">{myRole}</span>. New accounts get a
          temporary password and PIN, both rotated at first sign-in.
        </p>
      </div>

      <div className="space-y-4">
        {pageError && (
          <div
            role="alert"
            className="mf-panel flex items-start gap-3 border-status-critical/40 px-5 py-4"
          >
            <AlertTriangle className="w-4 h-4 text-status-critical mt-0.5 shrink-0" aria-hidden />
            <p className="text-[13px] text-text-primary">{pageError}</p>
          </div>
        )}

        <div className="mf-panel overflow-hidden">
          <div className={MF_PANEL_HEAD}>
            <h2 className={MF_PANEL_TITLE}>Accounts</h2>
            <span className="mf-kicker">
              {users.length} total · {adminCount} admin
            </span>
          </div>
          <div className="mf-table-wrap overflow-x-auto">
            <table className="mf-table">
              <thead>
                <tr>
                  <th>Username</th>
                  <th>Role</th>
                  <th className="hidden md:table-cell">Created</th>
                  <th className="hidden lg:table-cell">Credentials</th>
                  <th className="text-right">Actions</th>
                </tr>
              </thead>
              <tbody>
                {loading ? (
                  Array.from({ length: 3 }).map((_, i) => (
                    <tr key={`user-skel-${i}`}>
                      <td><div className="h-3 w-24 rounded bg-border-default/60 animate-shimmer" /></td>
                      <td><div className="h-4 w-16 rounded bg-border-default/60 animate-shimmer" /></td>
                      <td className="hidden md:table-cell"><div className="h-3 w-12 rounded bg-border-default/60 animate-shimmer" /></td>
                      <td className="hidden lg:table-cell"><div className="h-3 w-32 rounded bg-border-default/60 animate-shimmer" /></td>
                      <td><div className="ml-auto h-4 w-4 rounded bg-border-default/60 animate-shimmer" /></td>
                    </tr>
                  ))
                ) : users.length === 0 ? (
                  <tr>
                    <td colSpan={5} className="text-center text-[13px] text-text-tertiary">
                      No users yet.
                    </td>
                  </tr>
                ) : (
                  users.map((u) => {
                    const badge = ROLE_BADGE[u.role];
                    const rowBusy = busyId === u.id;
                    const rotationPending = !u.password_changed || !u.pin_changed;
                    return (
                      <tr key={u.id}>
                        <td className="text-[13px] font-medium text-text-primary">{u.username}</td>
                        <td>
                          <DropdownMenu>
                            <DropdownMenuTrigger
                              render={
                                <button
                                  disabled={rowBusy}
                                  className={`inline-flex items-center gap-1.5 rounded-md border px-2 py-1 text-[11px] font-medium transition-colors ${badge.cls} ${rowBusy ? "opacity-60" : "hover:bg-surface-elevated"}`}
                                  aria-label={`Change role for ${u.username}`}
                                >
                                  {badge.icon}
                                  {badge.label}
                                </button>
                              }
                            />
                            <DropdownMenuContent align="start" className={MF_MENU}>
                              {(["admin", "operator", "viewer"] as UserRole[]).map((r) => (
                                <DropdownMenuItem
                                  key={r}
                                  onClick={() => handleRoleChange(u, r)}
                                  className={MF_MENU_ITEM}
                                >
                                  {ROLE_BADGE[r].label}
                                </DropdownMenuItem>
                              ))}
                            </DropdownMenuContent>
                          </DropdownMenu>
                        </td>
                        <td className="mf-metric text-[12px] text-text-secondary hidden md:table-cell">
                          {timeSince(u.created_at)}
                        </td>
                        <td className="hidden lg:table-cell">
                          {rotationPending ? (
                            <StatusCell
                              tone="warning"
                              label={
                                !u.password_changed && !u.pin_changed
                                  ? "password + PIN rotation pending"
                                  : !u.password_changed
                                    ? "password rotation pending"
                                    : "PIN rotation pending"
                              }
                            />
                          ) : (
                            <StatusCell tone="ok" label="credentials current" />
                          )}
                        </td>
                        <td className="text-right">
                          <button
                            type="button"
                            disabled={rowBusy}
                            onClick={() => setDeleteTarget(u)}
                            className="ml-auto inline-grid h-8 w-8 place-items-center rounded-lg text-text-tertiary transition-colors hover:bg-surface-elevated hover:text-status-critical disabled:opacity-50"
                            title={`Delete ${u.username}`}
                            aria-label={`Delete ${u.username}`}
                          >
                            <Trash2 className="w-3.5 h-3.5" />
                          </button>
                        </td>
                      </tr>
                    );
                  })
                )}
              </tbody>
            </table>
          </div>
        </div>
      </div>

      <AddUserDialog
        open={addOpen}
        onClose={() => setAddOpen(false)}
        onCreated={(u) => {
          setUsers((prev) => [...prev, u]);
        }}
      />

      <Dialog open={!!deleteTarget} onOpenChange={(o) => { if (!o) setDeleteTarget(null); }}>
        <DialogContent className={`${MF_DIALOG} sm:max-w-md`} showCloseButton={false}>
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2.5">
              <Trash2 className="w-4 h-4 text-status-critical" aria-hidden />
              Delete user
            </DialogTitle>
            <DialogDescription className="mt-2 text-[13px] leading-6 text-text-tertiary">
              Remove <span className="text-text-primary font-medium">{deleteTarget?.username}</span>?
              This revokes their access immediately.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <button
              type="button"
              onClick={() => setDeleteTarget(null)}
              disabled={!!busyId}
              className={MF_BUTTON_QUIET}
            >
              Cancel
            </button>
            <button type="button" onClick={handleDelete} disabled={!!busyId} className={MF_BUTTON_DANGER}>
              {busyId ? "Deleting…" : "Delete user"}
            </button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}

function AddUserDialog({
  open,
  onClose,
  onCreated,
}: {
  open: boolean;
  onClose: () => void;
  onCreated: (u: UserRecord) => void;
}) {
  const { authFetch } = useAuth();
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [pin, setPin] = useState("");
  const [role, setRole] = useState<UserRole>("operator");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  // Reset form when reopened.
  useEffect(() => {
    if (open) {
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setUsername("");
      setPassword("");
      setPin("");
      setRole("operator");
      setError(null);
    }
  }, [open]);

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!username.trim() || password.length < 8 || !/^\d{4,}$/.test(pin)) {
      setError("Username, 8+ char password, and 4+ digit PIN are required.");
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      const res = await authFetch(`${HUB_URL}/api/users`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ username: username.trim(), password, pin, role }),
      });
      const body = await res.json().catch(() => null);
      if (!res.ok) {
        setError(body?.error || `Create failed (${res.status})`);
        return;
      }
      onCreated(body as UserRecord);
      onClose();
    } catch {
      setError("Cannot reach hub");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(o) => { if (!o) onClose(); }}>
      <DialogContent className={`${MF_DIALOG} sm:max-w-md`} showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>Add user</DialogTitle>
          <DialogDescription className="text-[13px] leading-6 text-text-tertiary">
            They&apos;ll have to rotate this password and PIN the first time they sign in.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-4">
          <div>
            <label className={MF_LABEL} htmlFor="add-user-username">Username</label>
            <Input
              id="add-user-username"
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoFocus
              autoComplete="off"
              className={`${MF_INPUT} w-full`}
              placeholder="alice"
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className={MF_LABEL} htmlFor="add-user-password">Temp password</label>
              <Input
                id="add-user-password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password"
                className={`${MF_INPUT} w-full`}
                placeholder="8+ chars"
              />
            </div>
            <div>
              <label className={MF_LABEL} htmlFor="add-user-pin">Temp PIN</label>
              <Input
                id="add-user-pin"
                type="password"
                inputMode="numeric"
                value={pin}
                onChange={(e) => setPin(e.target.value)}
                autoComplete="off"
                className={`${MF_INPUT} w-full font-mono`}
                placeholder="4+ digits"
              />
            </div>
          </div>
          <div>
            <span className={MF_LABEL}>Role</span>
            <div className="flex gap-2">
              {(["admin", "operator", "viewer"] as UserRole[]).map((r) => {
                const active = role === r;
                const badge = ROLE_BADGE[r];
                return (
                  <button
                    key={r}
                    type="button"
                    onClick={() => setRole(r)}
                    aria-pressed={active}
                    className={`flex-1 inline-flex items-center justify-center gap-1.5 rounded-lg border px-3 py-2 text-xs transition-colors ${
                      active
                        ? "border-accent bg-accent-subtle text-accent"
                        : "border-border-default text-text-tertiary hover:text-text-primary"
                    }`}
                  >
                    {badge.icon}
                    {badge.label}
                  </button>
                );
              })}
            </div>
          </div>
          {error && (
            <p
              role="alert"
              className="rounded-lg border border-status-critical/40 bg-status-critical-tint px-3 py-2 text-xs text-status-critical"
            >
              {error}
            </p>
          )}
          <DialogFooter>
            <button type="button" onClick={onClose} disabled={submitting} className={MF_BUTTON_QUIET}>
              Cancel
            </button>
            <button type="submit" disabled={submitting} className="mf-action">
              {submitting ? "Creating…" : "Create user"}
            </button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
