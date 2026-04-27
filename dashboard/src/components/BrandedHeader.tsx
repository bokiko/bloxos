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

import { useBranding } from "@/contexts/BrandingContext";

interface BrandedHeaderProps {
  /** Compact mode is used in dashboard headers; expanded mode is used on login. */
  size?: "compact" | "expanded";
}

export function BrandedHeader({ size = "compact" }: BrandedHeaderProps) {
  const { branding, logoUrl } = useBranding();

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

  // Default BloxOS wordmark.
  if (size === "expanded") {
    return (
      <div className="text-center">
        <h1 className="text-3xl font-bold tracking-tight">
          <span className="text-blox-blue">Blox</span>
          <span className="text-blox-text">OS</span>
        </h1>
        <p className="text-sm text-blox-muted mt-2">Fleet Management Dashboard</p>
      </div>
    );
  }
  return (
    <h1 className="text-base font-bold tracking-tight">
      <span className="text-blox-blue">Blox</span>
      <span className="text-blox-text">OS</span>
    </h1>
  );
}

