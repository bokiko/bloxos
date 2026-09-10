"use client";

// Phase 10 — branded header.
//
// Renders one of three states based on `useBranding()`:
//   1. Custom logo URL  → <img> at the configured size.
//   2. Custom title only → big text in the active accent.
//   3. Default          → original "BloxOS" wordmark.
//
// Used in the fleet header and on the login page so both surfaces follow
// the same branding configuration.
//
// `tone="rail"` is the Monoform app rail: the ground there is near-black
// (--mf-void), so the brand inherits currentColor from `.mf-brand` instead of
// carrying its own palette, and it renders as plain spans so the top bar's
// `.mf-page-title` stays the page's only <h1>. It is a fragment on purpose —
// `.mf-brand` is the flex container, and `.mf-brand-word` is the part the CSS
// collapses when the rail narrows into a strip.

import { BloxosMark } from "@/components/BloxosMark";
import { useBranding } from "@/contexts/BrandingContext";

interface BrandedHeaderProps {
  /** Compact mode is used in dashboard headers; expanded mode is used on login. */
  size?: "compact" | "expanded";
  /** "rail" is the inverse presentation for the Monoform navigation rail. */
  tone?: "default" | "rail";
}

export function BrandedHeader({ size = "compact", tone = "default" }: BrandedHeaderProps) {
  const { branding, logoUrl } = useBranding();

  if (tone === "rail") {
    if (logoUrl) {
      return (
        // Branding logo is a user-uploaded blob served from the hub;
        // next/image's loader cannot inspect or resize it.
        // eslint-disable-next-line @next/next/no-img-element
        <img
          src={logoUrl}
          alt={branding.title || "BloxOS"}
          width={110}
          height={28}
          // An uploaded logo has no wordmark to collapse, so it has to survive
          // the rail narrowing to an icon strip on its own.
          className="mf-brand-mark h-7 w-auto max-w-full object-contain"
        />
      );
    }
    return (
      <>
        {!branding.title && <BloxosMark className="mf-brand-mark h-7 w-7 shrink-0" />}
        <span className="mf-brand-word text-base font-bold tracking-tight">
          {branding.title || "BloxOS"}
        </span>
      </>
    );
  }

  if (logoUrl) {
    const dimensions =
      size === "expanded"
        ? { width: 220, height: 64, className: "h-16 w-auto" }
        : { width: 110, height: 28, className: "h-7 w-auto" };
    return (
      <div className="flex items-center" aria-label={branding.title || "BloxOS"}>
        {/* eslint-disable-next-line @next/next/no-img-element -- branding logo
            is a user-uploaded blob served from the hub; next/image's loader
            cannot inspect or resize it. */}
        <img
          src={logoUrl}
          alt={branding.title || "BloxOS"}
          width={dimensions.width}
          height={dimensions.height}
          className={dimensions.className}
        />
      </div>
    );
  }

  if (branding.title) {
    if (size === "expanded") {
      return (
        <div className="text-center">
          <h1 className="text-3xl font-bold tracking-tight text-blox-text">
            {branding.title}
          </h1>
          {branding.subtitle && (
            <p className="text-sm text-blox-muted mt-2">{branding.subtitle}</p>
          )}
        </div>
      );
    }
    return (
      <h1 className="text-base font-bold tracking-tight text-blox-text">
        {branding.title}
      </h1>
    );
  }

  // Default BloxOS identity. Custom instance titles and uploaded logos keep priority.
  if (size === "expanded") {
    return (
      <div className="text-center">
        <h1 className="flex items-center justify-center gap-3 text-3xl font-bold tracking-tight">
          <BloxosMark className="h-11 w-11 shrink-0 text-blox-blue" />
          <span><span className="text-blox-blue">Blox</span><span className="text-blox-text">OS</span></span>
        </h1>
        <p className="text-sm text-blox-muted mt-2">Fleet Management Dashboard</p>
      </div>
    );
  }
  return (
    <h1 className="flex items-center gap-2 text-base font-bold tracking-tight">
      <BloxosMark className="h-7 w-7 shrink-0 text-blox-blue" />
      <span><span className="text-blox-blue">Blox</span><span className="text-blox-text">OS</span></span>
    </h1>
  );
}

