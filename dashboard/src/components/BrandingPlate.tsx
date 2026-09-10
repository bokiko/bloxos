"use client";

// Contrast plate for a tenant's uploaded logo.
//
// Monoform's unauthenticated surfaces (/login, /setup) sit on the near-black
// `--mf-void`. Everything else drawn there is ours and uses product tokens, so
// it is legible by construction — but `branding.logo_url` is an arbitrary
// image we have never seen. Uploaded wordmarks are overwhelmingly dark ink
// meant for a white page, and those disappear on this ground.
//
// So a custom logo — and only a custom logo — gets a light, hairline-bordered
// plate to sit on. A logo already drawn for a dark ground loses nothing that
// matters (it keeps its own field), while a dark one stays readable instead of
// vanishing. The default BloxOS mark and text-only titles use product colours
// and render bare.

import { useBranding } from "@/contexts/BrandingContext";

export function BrandingPlate({ children }: { children: React.ReactNode }) {
  const { logoUrl } = useBranding();
  if (!logoUrl) return <>{children}</>;
  return (
    <div className="inline-flex rounded-[14px] border border-border-default bg-[#f4f4f2] px-6 py-4">
      {children}
    </div>
  );
}
