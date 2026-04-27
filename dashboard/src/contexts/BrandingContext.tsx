"use client";

// Phase 10 — instance branding (title, subtitle, logo, favicon).
//
// Branding is fetched from the public `/api/branding` endpoint on mount;
// no auth required so the login screen and pre-auth pages also see custom
// branding. Image bytes are served from `/api/branding/logo?v=<sha>` and
// `/api/branding/favicon?v=<sha>` and cached aggressively when the SHA
// matches.

import {
  createContext,
  useContext,
  useEffect,
  useState,
  useCallback,
  ReactNode,
} from "react";
import { HUB_URL } from "@/lib/session";

export interface Branding {
  title: string;
  subtitle: string;
  hasLogo: boolean;
  logoSha: string;
  hasFavicon: boolean;
  faviconSha: string;
}

interface BrandingContextValue {
  branding: Branding;
  loading: boolean;
  refresh: () => Promise<void>;
  /** URL of the logo image, with cache-busting SHA when present. */
  logoUrl: string | null;
  /** URL of the favicon image, with cache-busting SHA when present. */
  faviconUrl: string | null;
}

const EMPTY: Branding = {
  title: "",
  subtitle: "",
  hasLogo: false,
  logoSha: "",
  hasFavicon: false,
  faviconSha: "",
};

const BrandingContext = createContext<BrandingContextValue | null>(null);

function buildImageUrl(path: string, sha: string): string {
  // sha is short (16 chars) and safe to inline; encodeURIComponent for safety.
  const base = `${HUB_URL}${path}`;
  return sha ? `${base}?v=${encodeURIComponent(sha)}` : base;
}

function updateFaviconLink(href: string | null) {
  if (typeof document === "undefined") return;
  let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
  if (!link) {
    link = document.createElement("link");
    link.rel = "icon";
    document.head.appendChild(link);
  }
  if (href) {
    link.href = href;
    link.type = "image/png";
  }
}

export function BrandingProvider({ children }: { children: ReactNode }) {
  const [branding, setBranding] = useState<Branding>(EMPTY);
  const [loading, setLoading] = useState(true);

  const refresh = useCallback(async () => {
    try {
      const res = await fetch(`${HUB_URL}/api/branding`, { method: "GET" });
      if (!res.ok) {
        setLoading(false);
        return;
      }
      const data = await res.json();
      setBranding({
        title: typeof data.title === "string" ? data.title : "",
        subtitle: typeof data.subtitle === "string" ? data.subtitle : "",
        hasLogo: !!data.has_logo,
        logoSha: typeof data.logo_sha === "string" ? data.logo_sha : "",
        hasFavicon: !!data.has_favicon,
        faviconSha: typeof data.favicon_sha === "string" ? data.favicon_sha : "",
      });
    } catch {
      // Stay on EMPTY — the default BloxOS branding renders.
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    // Initial fetch synchronizes UI state with the network — a legitimate
    // use of useEffect (external system → React). The setState calls inside
    // refresh() are reactions to fetch completion, which is an external
    // event, so the React 19 set-state-in-effect lint can be suppressed.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    void refresh();
  }, [refresh]);

  // Mirror title to document.title and favicon to <link rel="icon">.
  useEffect(() => {
    if (typeof document === "undefined") return;
    document.title = branding.title || "BloxOS";
    if (branding.hasFavicon) {
      updateFaviconLink(buildImageUrl("/api/branding/favicon", branding.faviconSha));
    }
    // No "else" — we intentionally don't reset to the default favicon on
    // re-render, to avoid a flash. Browsers cache the previous icon until
    // the next navigation.
  }, [branding.title, branding.hasFavicon, branding.faviconSha]);

  const logoUrl = branding.hasLogo
    ? buildImageUrl("/api/branding/logo", branding.logoSha)
    : null;
  const faviconUrl = branding.hasFavicon
    ? buildImageUrl("/api/branding/favicon", branding.faviconSha)
    : null;

  return (
    <BrandingContext.Provider value={{ branding, loading, refresh, logoUrl, faviconUrl }}>
      {children}
    </BrandingContext.Provider>
  );
}

export function useBranding(): BrandingContextValue {
  const ctx = useContext(BrandingContext);
  if (!ctx) throw new Error("useBranding must be used within BrandingProvider");
  return ctx;
}
