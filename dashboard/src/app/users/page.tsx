"use client";

import { FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { motion } from "framer-motion";
import { ArrowLeft, Plus, Trash2, ShieldCheck, ShieldAlert, Eye, AlertTriangle } from "lucide-react";
import { useAuth, type UserRole } from "@/contexts/AuthContext";
import { HUB_URL } from "@/lib/session";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import {
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow,
} from "@/components/ui/table";
import {
  Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";

interface UserRecord {
  id: string;
  username: string;
  role: UserRole;
  created_at: string;
  password_changed: boolean;
  pin_changed: boolean;
}

const ROLE_BADGE: Record<UserRole, { label: string; cls: string; icon: React.ReactNode }> = {
  admin:    { label: "Admin",    cls: "border-blox-blue/30 bg-blox-blue/10 text-blox-blue",       icon: <ShieldCheck className="w-3 h-3" /> },
  operator: { label: "Operator", cls: "border-amber-500/30 bg-amber-500/10 text-amber-400",       icon: <ShieldAlert className="w-3 h-3" /> },
  viewer:   { label: "Viewer",   cls: "border-blox-border bg-blox-border/30 text-blox-muted",     icon: <Eye className="w-3 h-3" /> },
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
      <div className="min-h-screen bg-blox-bg flex items-center justify-center">
        <div className="flex items-center gap-2 text-blox-muted text-sm">
          <span className="w-3.5 h-3.5 border-2 border-blox-blue/40 border-t-blox-blue rounded-full animate-spin" />
          Loading…
        </div>
      </div>
    );
  }

  return (
    <motion.div
      initial={{ opacity: 0, y: 8 }}
      animate={{ opacity: 1, y: 0 }}
      transition={{ duration: 0.25, ease: "easeOut" }}
      className="min-h-screen bg-blox-bg"
    >
      <header className="sticky top-0 z-50 bg-blox-bg/80 backdrop-blur-xl border-b border-blox-border/50">
        <div className="max-w-[1200px] mx-auto px-4 sm:px-6 h-14 flex items-center justify-between">
          <div className="flex items-center gap-3">
            <Link href="/" className="flex items-center gap-1.5 text-blox-muted hover:text-blox-text transition-colors text-sm">
              <ArrowLeft className="w-4 h-4" />
              Fleet
            </Link>
            <span className="text-blox-border/50">/</span>
            <h1 className="font-semibold text-blox-text tracking-tight">Users</h1>
            <Badge variant="outline" className="text-[10px] border-blox-border text-blox-muted">
              {users.length} total · {adminCount} admin
            </Badge>
          </div>
          <Button
            variant="outline"
            size="sm"
            onClick={() => setAddOpen(true)}
            className="text-xs text-blox-blue border-blox-blue/20 bg-blox-blue/5 hover:bg-blox-blue/10 gap-1.5"
          >
            <Plus className="w-3.5 h-3.5" />
            Add User
          </Button>
        </div>
      </header>

      <main className="max-w-[1200px] mx-auto px-4 sm:px-6 py-6 space-y-4">
        {pageError && (
          <div className="flex items-start gap-2 rounded-xl border border-red-500/20 bg-red-500/10 px-3 py-2 text-xs text-blox-red">
            <AlertTriangle className="w-3.5 h-3.5 mt-0.5 shrink-0" />
            <span>{pageError}</span>
          </div>
        )}

        <div className="bg-blox-card border border-blox-border rounded-xl overflow-hidden">
          <Table>
            <TableHeader>
              <TableRow className="border-b-blox-border hover:bg-transparent">
                <TableHead className="text-blox-muted text-xs">Username</TableHead>
                <TableHead className="text-blox-muted text-xs">Role</TableHead>
                <TableHead className="text-blox-muted text-xs hidden md:table-cell">Created</TableHead>
                <TableHead className="text-blox-muted text-xs hidden lg:table-cell">Status</TableHead>
                <TableHead className="text-blox-muted text-xs text-right">Actions</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading ? (
                <>
                  {Array.from({ length: 3 }).map((_, i) => (
                    <TableRow key={`user-skel-${i}`} className="border-b-blox-border/30 hover:bg-transparent">
                      <TableCell>
                        <div className="h-3 w-24 bg-blox-border/60 rounded animate-shimmer" />
                      </TableCell>
                      <TableCell>
                        <div className="h-4 w-16 bg-blox-border/60 rounded-md animate-shimmer" />
                      </TableCell>
                      <TableCell className="hidden md:table-cell">
                        <div className="h-3 w-12 bg-blox-border/60 rounded animate-shimmer" />
                      </TableCell>
                      <TableCell className="hidden lg:table-cell">
                        <div className="h-3 w-32 bg-blox-border/60 rounded animate-shimmer" />
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="h-4 w-4 bg-blox-border/60 rounded ml-auto animate-shimmer" />
                      </TableCell>
                    </TableRow>
                  ))}
                </>
              ) : users.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={5} className="text-center py-8 text-blox-muted text-xs">No users yet.</TableCell>
                </TableRow>
              ) : (
                users.map((u) => {
                  const badge = ROLE_BADGE[u.role];
                  const rowBusy = busyId === u.id;
                  return (
                    <TableRow key={u.id} className="border-b-blox-border/30 hover:bg-blox-border/10 transition-colors">
                      <TableCell className="text-xs font-medium text-blox-text">{u.username}</TableCell>
                      <TableCell className="text-xs">
                        <DropdownMenu>
                          <DropdownMenuTrigger
                            render={
                              <button
                                disabled={rowBusy}
                                className={`inline-flex items-center gap-1.5 px-2 py-0.5 rounded-md border text-[11px] font-medium transition-colors ${badge.cls} ${rowBusy ? "opacity-60" : "hover:bg-opacity-20"}`}
                              >
                                {badge.icon}
                                {badge.label}
                              </button>
                            }
                          />
                          <DropdownMenuContent align="start" className="bg-blox-card border-blox-border">
                            {(["admin", "operator", "viewer"] as UserRole[]).map((r) => (
                              <DropdownMenuItem
                                key={r}
                                onClick={() => handleRoleChange(u, r)}
                                className="text-xs text-blox-text"
                              >
                                {ROLE_BADGE[r].label}
                              </DropdownMenuItem>
                            ))}
                          </DropdownMenuContent>
                        </DropdownMenu>
                      </TableCell>
                      <TableCell className="text-xs text-blox-muted font-mono tabular-nums hidden md:table-cell">
                        {timeSince(u.created_at)}
                      </TableCell>
                      <TableCell className="text-xs hidden lg:table-cell">
                        {!u.password_changed && (
                          <span className="text-amber-400 text-[10px] mr-2">password rotation pending</span>
                        )}
                        {!u.pin_changed && (
                          <span className="text-amber-400 text-[10px]">PIN rotation pending</span>
                        )}
                        {u.password_changed && u.pin_changed && (
                          <span className="text-emerald-400 text-[10px]">credentials current</span>
                        )}
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          disabled={rowBusy}
                          onClick={() => setDeleteTarget(u)}
                          className="text-blox-muted hover:text-red-400"
                          title="Delete user"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  );
                })
              )}
            </TableBody>
          </Table>
        </div>

        <p className="text-[10px] text-blox-muted">
          Logged in as <span className="text-blox-text">{myRole}</span>. Newly created users are forced to rotate their temporary password and PIN on first login.
        </p>
      </main>

      <AddUserDialog
        open={addOpen}
        onClose={() => setAddOpen(false)}
        onCreated={(u) => {
          setUsers((prev) => [...prev, u]);
        }}
      />

      <Dialog open={!!deleteTarget} onOpenChange={(o) => { if (!o) setDeleteTarget(null); }}>
        <DialogContent className="bg-blox-card border-blox-border text-blox-text ring-0 sm:max-w-md" showCloseButton={false}>
          <DialogHeader>
            <div className="flex items-center gap-3">
              <div className="p-2 rounded-xl bg-red-500/10">
                <Trash2 className="w-5 h-5 text-red-400" />
              </div>
              <DialogTitle>Delete User</DialogTitle>
            </div>
            <DialogDescription className="text-blox-muted text-xs mt-2">
              Remove <span className="text-blox-text font-medium">{deleteTarget?.username}</span>? This will revoke their access immediately.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button
              variant="outline"
              size="sm"
              onClick={() => setDeleteTarget(null)}
              disabled={!!busyId}
              className="text-xs text-blox-muted border-blox-border"
            >
              Cancel
            </Button>
            <Button
              variant="destructive"
              size="sm"
              onClick={handleDelete}
              disabled={!!busyId}
              className="text-xs"
            >
              {busyId ? "Deleting…" : "Delete User"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </motion.div>
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
      <DialogContent className="bg-blox-card border-blox-border text-blox-text ring-0 sm:max-w-md" showCloseButton={false}>
        <DialogHeader>
          <DialogTitle>Add User</DialogTitle>
          <DialogDescription className="text-blox-muted text-xs">
            They&apos;ll have to rotate this password and PIN the first time they sign in.
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit} className="space-y-3">
          <div>
            <label className="block text-[11px] uppercase tracking-[0.18em] text-blox-muted mb-1">Username</label>
            <Input
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              autoFocus
              autoComplete="off"
              className="h-9 text-sm bg-blox-bg border-blox-border text-blox-text"
              placeholder="alice"
            />
          </div>
          <div className="grid grid-cols-2 gap-3">
            <div>
              <label className="block text-[11px] uppercase tracking-[0.18em] text-blox-muted mb-1">Temp password</label>
              <Input
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password"
                className="h-9 text-sm bg-blox-bg border-blox-border text-blox-text"
                placeholder="8+ chars"
              />
            </div>
            <div>
              <label className="block text-[11px] uppercase tracking-[0.18em] text-blox-muted mb-1">Temp PIN</label>
              <Input
                type="password"
                inputMode="numeric"
                value={pin}
                onChange={(e) => setPin(e.target.value)}
                autoComplete="off"
                className="h-9 text-sm bg-blox-bg border-blox-border text-blox-text font-mono"
                placeholder="4+ digits"
              />
            </div>
          </div>
          <div>
            <label className="block text-[11px] uppercase tracking-[0.18em] text-blox-muted mb-1">Role</label>
            <div className="flex gap-2">
              {(["admin", "operator", "viewer"] as UserRole[]).map((r) => {
                const active = role === r;
                const badge = ROLE_BADGE[r];
                return (
                  <button
                    key={r}
                    type="button"
                    onClick={() => setRole(r)}
                    className={`flex-1 inline-flex items-center justify-center gap-1.5 px-3 py-1.5 rounded-md border text-xs transition-colors ${
                      active ? badge.cls : "border-blox-border text-blox-muted hover:text-blox-text"
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
            <p className="text-xs text-blox-red bg-red-500/10 border border-red-500/20 rounded-md px-3 py-2">{error}</p>
          )}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              size="sm"
              onClick={onClose}
              disabled={submitting}
              className="text-xs text-blox-muted border-blox-border"
            >
              Cancel
            </Button>
            <Button
              type="submit"
              size="sm"
              disabled={submitting}
              className="text-xs bg-blox-blue hover:bg-blox-blue/90 text-white"
            >
              {submitting ? "Creating…" : "Create User"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
