"use client";

// Phase 11 — avatar primitive.
//
// Renders an uploaded image when one is available, or generated initials
// over a deterministic hashed-hue background otherwise. The hue is stable
// per-name so the same user always lands on the same colour, even before
// they upload an image.

import { cn } from "@/lib/utils";

interface AvatarProps {
  url: string | null;
  name: string;
  size?: number;
  className?: string;
}

function getInitials(name: string): string {
  const trimmed = (name ?? "").trim();
  if (!trimmed) return "?";
  const parts = trimmed.split(/\s+/);
  if (parts.length === 1) {
    return (parts[0][0] || "?").toUpperCase();
  }
  return ((parts[0][0] ?? "") + (parts[parts.length - 1][0] ?? "")).toUpperCase() || "?";
}

function hashHue(name: string): number {
  // djb2 — small, fast, good enough for hue dispersion on short inputs.
  let h = 5381;
  for (let i = 0; i < name.length; i++) {
    h = (h * 33) ^ name.charCodeAt(i);
  }
  return Math.abs(h) % 360;
}

export function Avatar({ url, name, size = 32, className }: AvatarProps) {
  const dim = `${size}px`;
  if (url) {
    return (
      // eslint-disable-next-line @next/next/no-img-element -- user-uploaded blob from hub
      <img
        src={url}
        alt={name || "avatar"}
        width={size}
        height={size}
        className={cn("rounded-full object-cover", className)}
        style={{ width: dim, height: dim }}
      />
    );
  }
  const initials = getInitials(name);
  const hue = hashHue(name || "?");
  const fontSize = Math.max(10, Math.floor(size * 0.4));
  return (
    <span
      className={cn(
        "inline-flex items-center justify-center rounded-full text-white font-bold select-none",
        className,
      )}
      style={{
        width: dim,
        height: dim,
        backgroundColor: `hsl(${hue}, 50%, 45%)`,
        fontSize: `${fontSize}px`,
        lineHeight: 1,
      }}
      aria-label={`${name || "user"} avatar`}
    >
      {initials}
    </span>
  );
}
