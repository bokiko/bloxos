"use client";

export const HUB_URL = process.env.NEXT_PUBLIC_HUB_URL || "";
export const DEMO_MODE = process.env.NEXT_PUBLIC_DEMO_MODE === "true";
export const AUTH_CHANGED_EVENT = "bloxos-auth-changed";

export function getStoredToken(): string | null {
  if (typeof window === "undefined") return null;
  return localStorage.getItem("bloxos_token");
}

export function getAuthHeaders(extra: Record<string, string> = {}): Record<string, string> {
  const token = getStoredToken();
  return token ? { ...extra, Authorization: `Bearer ${token}` } : { ...extra };
}

export function dispatchAuthChanged() {
  if (typeof window !== "undefined") {
    window.dispatchEvent(new Event(AUTH_CHANGED_EVENT));
  }
}

export function getHubWsBaseUrl(): string {
  const base =
    HUB_URL ||
    (typeof window !== "undefined" ? window.location.origin : "");
  return base.replace(/^http/, "ws");
}
