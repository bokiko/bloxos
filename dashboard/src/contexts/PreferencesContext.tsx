"use client";

// Phase 11 — per-user workflow personalization.
//
// Single round-trip GET /api/me/preferences hydrates everything the
// dashboard needs after login (display name, density, default view/sort,
// pinned machines, saved filters). A localStorage cache primes the
// initial render so reloads paint instantly with the right view/density,
// without waiting for the network.
//
// Optimistic updates: pin/unpin, saved-filter add/delete and the scalar
// PATCH all apply locally first then send to the server. Failures revert
// for pin/unpin and delete-filter; PATCH and saveFilter throw on the
// network so the caller can surface a toast.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  ReactNode,
} from "react";
import { HUB_URL, getStoredToken } from "@/lib/session";
import { useAuth } from "@/contexts/AuthContext";
import { userIDFromToken } from "@/lib/auth-session.mjs";
import { normalizeMachineOrder, acceptedMachineOrder, createPreferenceWriter } from "@/lib/machine-order.mjs";
import {
  purgeLegacyPreferencesCache,
  readPreferencesCache,
  writePreferencesCache,
} from "@/lib/preferences-cache.mjs";
import {
  DEFAULT_OVERVIEW_LAYOUT,
  DEFAULT_OVERVIEW_WIDGETS,
  isOverviewLayout,
  normalizeOverviewWidgets,
} from "@/lib/overview-layout.mjs";

/** One of the three named arrangements above the machine table. */
export type OverviewLayout = "machine-first" | "balanced" | "power-focus";
/** The compact context modules. Every key is always present. */
export interface OverviewWidgets {
  availability: boolean;
  attention: boolean;
  urgent_alert: boolean;
}

export type Density = "comfortable" | "compact";
export type DefaultView = "grid" | "list";
export type DefaultSort = "manual" | "name" | "status" | "cpu" | "gpu_temp";

export interface SavedFilter {
  id: string;
  name: string;
  filter: Record<string, unknown>;
  created_at: string;
}

export interface Preferences {
  display_name: string;
  has_avatar: boolean;
  avatar_sha: string;
  density: Density;
  default_view: DefaultView;
  default_sort: DefaultSort;
  pinned_machines: string[];
  machine_order: string[];
  saved_filters: SavedFilter[];
  /** Which arrangement sits above the machine table. */
  overview_layout: OverviewLayout;
  /** Which compact context modules are kept. */
  overview_widgets: OverviewWidgets;
}

const DEFAULT_PREFS: Preferences = {
  display_name: "",
  has_avatar: false,
  avatar_sha: "",
  density: "comfortable",
  default_view: "grid",
  default_sort: "name",
  pinned_machines: [],
  machine_order: [],
  saved_filters: [],
  overview_layout: DEFAULT_OVERVIEW_LAYOUT,
  overview_widgets: { ...DEFAULT_OVERVIEW_WIDGETS },
};

interface PreferencesContextValue {
  preferences: Preferences;
  loading: boolean;
  saveMachineOrder: (ids: string[]) => Promise<void>;
  /** Optimistic scalar update — applies locally then PATCHes. */
  updateScalar: (
    patch: Partial<
      Pick<
        Preferences,
        "display_name" | "density" | "default_view" | "default_sort" | "overview_layout" | "overview_widgets"
      >
    >,
  ) => Promise<void>;
  /**
   * Whether the hub that answered the last GET knows about the overview
   * preference. False against a hub older than the arrangement feature, and
   * before the first successful response. The overview controls read this and
   * disable themselves rather than appearing to save into a void.
   */
  hubSupportsOverview: boolean;
  uploadAvatar: (file: File) => Promise<void>;
  removeAvatar: () => Promise<void>;
  pinMachine: (machineID: string) => Promise<void>;
  unpinMachine: (machineID: string) => Promise<void>;
  isPinned: (machineID: string) => boolean;
  saveFilter: (name: string, filter: Record<string, unknown>) => Promise<SavedFilter>;
  deleteFilter: (id: string) => Promise<void>;
  /** Stable URL for the current user's avatar; null when none. */
  myAvatarURL: string | null;
}

const PreferencesContext = createContext<PreferencesContextValue | null>(null);

function normalizePreferences(raw: unknown): Preferences {
  const r = (raw && typeof raw === "object" ? raw : {}) as Record<string, unknown>;
  const density = r.density === "compact" ? "compact" : "comfortable";
  const defaultView = r.default_view === "list" ? "list" : "grid";
  const sortRaw = typeof r.default_sort === "string" ? r.default_sort : "name";
  const defaultSort: DefaultSort =
    sortRaw === "manual" || sortRaw === "status" || sortRaw === "cpu" || sortRaw === "gpu_temp"
      ? sortRaw
      : "name";
  const pinned = Array.isArray(r.pinned_machines)
    ? r.pinned_machines.filter((s): s is string => typeof s === "string")
    : [];
  const saved = Array.isArray(r.saved_filters)
    ? r.saved_filters
        .map((f) => normalizeSavedFilter(f))
        .filter((f): f is SavedFilter => f !== null)
    : [];
  return {
    display_name: typeof r.display_name === "string" ? r.display_name : "",
    has_avatar: !!r.has_avatar,
    avatar_sha: typeof r.avatar_sha === "string" ? r.avatar_sha : "",
    density,
    default_view: defaultView,
    default_sort: defaultSort,
    pinned_machines: pinned,
    machine_order: normalizeMachineOrder(r.machine_order),
    saved_filters: saved,
    overview_layout: isOverviewLayout(r.overview_layout)
      ? (r.overview_layout as OverviewLayout)
      : DEFAULT_OVERVIEW_LAYOUT,
    overview_widgets: normalizeOverviewWidgets(r.overview_widgets) as OverviewWidgets,
  };
}

function normalizeSavedFilter(raw: unknown): SavedFilter | null {
  if (!raw || typeof raw !== "object") return null;
  const r = raw as Record<string, unknown>;
  if (typeof r.id !== "string" || typeof r.name !== "string") return null;
  const filter =
    r.filter && typeof r.filter === "object" && !Array.isArray(r.filter)
      ? (r.filter as Record<string, unknown>)
      : {};
  const createdAt = typeof r.created_at === "string" ? r.created_at : "";
  return { id: r.id, name: r.name, filter, created_at: createdAt };
}

// userIDFromToken (lib/auth-session.mjs) is the single JWT user_id reader.

export function PreferencesProvider({ children }: { children: ReactNode }) {
  const { authFetch, token, logout } = useAuth();
  // Lazy initializer: read THIS user's cached prefs synchronously so the
  // first paint already has the right density/view/sort. Never reads the
  // legacy unscoped cache (cross-user leak).
  const [preferences, setPreferences] = useState<Preferences>(
    () => readPreferencesCache(userIDFromToken(getStoredToken()), normalizePreferences) ?? DEFAULT_PREFS,
  );
  const [loading, setLoading] = useState(true);
  const [hubSupportsOverview, setHubSupportsOverview] = useState(false);
  // userID tracks the actual token, not just authenticated-ness: a cross-tab
  // or same-tab user switch with the same boolean state must still re-key.
  const userID = useMemo<string | null>(() => userIDFromToken(token), [token]);
  const userIDRef = useRef<string | null>(userID);
  const preferencesRef = useRef(preferences);
  const writePreference = useMemo(() => createPreferenceWriter(), []);
  const patchPreferences = useCallback((patch: unknown) => {
    const capturedToken = token;
    return writePreference(async () => {
      if (!capturedToken || getStoredToken() !== capturedToken) throw new Error("Your session changed. Please retry.");
      const res = await fetch(`${HUB_URL}/api/me/preferences`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json", Authorization: `Bearer ${capturedToken}` },
        body: JSON.stringify(patch),
        signal: AbortSignal.timeout(15000),
      });
      const data = await res.json().catch(() => ({}));
      if (res.status === 401 && getStoredToken() === capturedToken) logout();
      if (!res.ok) throw new Error(typeof data.error === "string" ? data.error : "Could not save preferences. Please retry.");
      return data;
    });
  }, [token, logout, writePreference]);

  // Track in-flight refresh to avoid races between the post-login refresh
  // and any optimistic patch the user fires before it returns.
  const refreshSeqRef = useRef(0);

  const setAndCache = useCallback((p: Preferences) => {
    preferencesRef.current = p;
    setPreferences(p);
    writePreferencesCache(userIDRef.current, p);
  }, []);

  // Mutations only replace fields they own. A late pin/avatar/filter response
  // must not overwrite a machine order that was saved while it was in flight.
  const mergeAndCache = useCallback((patch: Partial<Preferences>) => {
    setAndCache({ ...preferencesRef.current, ...patch });
  }, [setAndCache]);

  const refresh = useCallback(async () => {
    const seq = ++refreshSeqRef.current;
    const uid = userIDRef.current;
    try {
      const res = await authFetch(`${HUB_URL}/api/me/preferences`);
      if (!res.ok) {
        // Unauthorized / 5xx — keep local cache; don't blow away state.
        return;
      }
      const data = await res.json();
      // A user switch (or a newer refresh) while this request was in flight
      // must not write the old user's preferences into the new session.
      if (seq !== refreshSeqRef.current || userIDRef.current !== uid) return;
      // An older hub answers without these keys. Recording that lets the
      // overview controls disable themselves instead of writing a preference
      // the hub will reject with "no fields to update".
      setHubSupportsOverview(
        typeof (data as Record<string, unknown>)?.overview_layout === "string",
      );
      setAndCache(normalizePreferences(data));
    } catch {
      // Offline — leave cached prefs intact.
    } finally {
      if (seq === refreshSeqRef.current && userIDRef.current === uid) {
        setLoading(false);
      }
    }
  }, [authFetch, setAndCache]);

  // Hydrate/sync on user change. Logout drops all state; a user switch
  // hydrates ONLY the new user's keyed cache (never the legacy unscoped
  // entry) before the server sync, so user A's preferences can never paint
  // for user B. setState in this effect is the canonical fetch-sync pattern
  // (same as BrandingContext), suppressed by the same lint rule.
  useEffect(() => {
    purgeLegacyPreferencesCache();
    userIDRef.current = userID;
    if (!userID) {
      preferencesRef.current = DEFAULT_PREFS;
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setPreferences(DEFAULT_PREFS);
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setLoading(false);
      return;
    }
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setAndCache(readPreferencesCache(userID, normalizePreferences) ?? DEFAULT_PREFS);
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLoading(true);
    void refresh();
  }, [userID, refresh, setAndCache]);

  const saveMachineOrder = useCallback(async (ids: string[]) => {
    const uid = userIDRef.current;
    if (!uid || loading) throw new Error("Wait for your preferences to load, then retry.");
    const order = normalizeMachineOrder(ids);
    ++refreshSeqRef.current;
    const data = await patchPreferences({ machine_order: order });
    if (userIDRef.current !== uid || userIDFromToken(getStoredToken()) !== uid) {
      throw new Error("Your session changed. Sign in and check your saved order.");
    }
    if (!acceptedMachineOrder(data, order)) throw new Error("The hub did not confirm this order. Update the hub and retry.");
    mergeAndCache({ machine_order: order, default_sort: "manual" });
  }, [patchPreferences, loading, mergeAndCache]);

  // Apply density-* class to <html> so CSS variables propagate to every
  // descendant. Plain DOM mutation, not setState — safe in an effect.
  useEffect(() => {
    if (typeof document === "undefined") return;
    const root = document.documentElement;
    const desired =
      preferences.density === "compact" ? "density-compact" : "density-comfortable";
    if (!root.classList.contains(desired)) {
      root.classList.remove("density-comfortable", "density-compact");
      root.classList.add(desired);
    }
  }, [preferences.density]);

  const updateScalar = useCallback(
    async (
      patch: Partial<
        Pick<
          Preferences,
          "display_name" | "density" | "default_view" | "default_sort" | "overview_layout" | "overview_widgets"
        >
      >,
    ) => {
      const uid = userIDRef.current;
      // Optimistic local update.
      mergeAndCache(patch);
      try {
        const data = await patchPreferences(patch);
        // Late response after a user switch must not write the old user's
        // preferences into the new session.
        if (userIDRef.current !== uid) return;
        const normalized = normalizePreferences(data);
        const owned: Partial<Preferences> = {};
        if (patch.display_name !== undefined) owned.display_name = normalized.display_name;
        if (patch.density !== undefined) owned.density = normalized.density;
        if (patch.default_view !== undefined) owned.default_view = normalized.default_view;
        if (patch.default_sort !== undefined) owned.default_sort = normalized.default_sort;
        if (patch.overview_layout !== undefined) owned.overview_layout = normalized.overview_layout;
        if (patch.overview_widgets !== undefined) owned.overview_widgets = normalized.overview_widgets;
        mergeAndCache(owned);
      } catch (e) {
        // Don't revert: we keep the optimistic state so the user isn't
        // jolted. The next refresh on reload reconciles with the server.
        throw e;
      }
    },
    [patchPreferences, mergeAndCache],
  );

  const uploadAvatar = useCallback(
    async (file: File) => {
      const uid = userIDRef.current;
      const fd = new FormData();
      fd.append("file", file);
      const res = await authFetch(`${HUB_URL}/api/me/avatar`, {
        method: "POST",
        body: fd,
      });
      if (!res.ok) {
        const err = await res.json().catch(() => ({}));
        throw new Error(typeof err.error === "string" ? err.error : "Failed to upload avatar");
      }
      const data = await res.json();
      // Late response after a user switch must not write the old user's state.
      if (userIDRef.current !== uid) return;
      const normalized = normalizePreferences(data);
      mergeAndCache({ has_avatar: normalized.has_avatar, avatar_sha: normalized.avatar_sha });
    },
    [authFetch, mergeAndCache],
  );

  const removeAvatar = useCallback(async () => {
    const uid = userIDRef.current;
    const res = await authFetch(`${HUB_URL}/api/me/avatar`, { method: "DELETE" });
    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      throw new Error(typeof err.error === "string" ? err.error : "Failed to remove avatar");
    }
    const data = await res.json();
    if (userIDRef.current !== uid) return;
    const normalized = normalizePreferences(data);
    mergeAndCache({ has_avatar: normalized.has_avatar, avatar_sha: normalized.avatar_sha });
  }, [authFetch, mergeAndCache]);

  const pinMachine = useCallback(
    async (machineID: string) => {
      if (preferences.pinned_machines.includes(machineID)) return;
      const uid = userIDRef.current;
      const next = { ...preferences, pinned_machines: [machineID, ...preferences.pinned_machines] };
      mergeAndCache({ pinned_machines: next.pinned_machines });
      try {
        const res = await authFetch(`${HUB_URL}/api/me/pinned/${encodeURIComponent(machineID)}`, {
          method: "POST",
        });
        if (!res.ok) throw new Error("pin failed");
      } catch {
        // Revert on failure — unless the user switched meanwhile; the new
        // user's session owns its own state.
        if (userIDRef.current !== uid) return;
        mergeAndCache({
          pinned_machines: preferencesRef.current.pinned_machines.filter((id) => id !== machineID),
        });
      }
    },
    [authFetch, preferences, mergeAndCache],
  );

  const unpinMachine = useCallback(
    async (machineID: string) => {
      if (!preferences.pinned_machines.includes(machineID)) return;
      const uid = userIDRef.current;
      const prev = preferences.pinned_machines;
      const next = { ...preferences, pinned_machines: prev.filter((id) => id !== machineID) };
      mergeAndCache({ pinned_machines: next.pinned_machines });
      try {
        const res = await authFetch(`${HUB_URL}/api/me/pinned/${encodeURIComponent(machineID)}`, {
          method: "DELETE",
        });
        if (!res.ok) throw new Error("unpin failed");
      } catch {
        if (userIDRef.current !== uid) return;
        mergeAndCache({ pinned_machines: [...new Set([...preferencesRef.current.pinned_machines, machineID])] });
      }
    },
    [authFetch, preferences, mergeAndCache],
  );

  const isPinned = useCallback(
    (machineID: string) => preferences.pinned_machines.includes(machineID),
    [preferences.pinned_machines],
  );

  const saveFilter = useCallback(
    async (name: string, filter: Record<string, unknown>): Promise<SavedFilter> => {
      const uid = userIDRef.current;
      const res = await authFetch(`${HUB_URL}/api/me/filters`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ name, filter }),
      });
      if (!res.ok) {
        const err = await res.json().catch(() => ({}));
        throw new Error(typeof err.error === "string" ? err.error : "Failed to save filter");
      }
      const created = await res.json();
      const normalized = normalizeSavedFilter(created);
      if (!normalized) throw new Error("Invalid server response");
      // Late response after a user switch must not write the old user's state.
      if (userIDRef.current !== uid) return normalized;
      mergeAndCache({
        saved_filters: [normalized, ...preferencesRef.current.saved_filters],
      });
      return normalized;
    },
    [authFetch, mergeAndCache],
  );

  const deleteFilter = useCallback(
    async (id: string) => {
      const uid = userIDRef.current;
      const prev = preferences.saved_filters;
      mergeAndCache({
        saved_filters: prev.filter((f) => f.id !== id),
      });
      try {
        const res = await authFetch(`${HUB_URL}/api/me/filters/${encodeURIComponent(id)}`, {
          method: "DELETE",
        });
        if (!res.ok && res.status !== 404) throw new Error("delete failed");
      } catch {
        if (userIDRef.current !== uid) return;
        mergeAndCache({ saved_filters: [...preferencesRef.current.saved_filters, ...prev.filter(f => f.id === id)] });
      }
    },
    [authFetch, preferences, mergeAndCache],
  );

  const myAvatarURL =
    preferences.has_avatar && userID
      ? `${HUB_URL}/api/users/${encodeURIComponent(userID)}/avatar?v=${encodeURIComponent(preferences.avatar_sha)}`
      : null;

  return (
    <PreferencesContext.Provider
      value={{
        preferences,
        loading,
        saveMachineOrder,
        updateScalar,
        uploadAvatar,
        removeAvatar,
        pinMachine,
        unpinMachine,
        isPinned,
        saveFilter,
        deleteFilter,
        myAvatarURL,
        hubSupportsOverview,
      }}
    >
      {children}
    </PreferencesContext.Provider>
  );
}

export function usePreferences(): PreferencesContextValue {
  const ctx = useContext(PreferencesContext);
  if (!ctx) throw new Error("usePreferences must be used within PreferencesProvider");
  return ctx;
}
