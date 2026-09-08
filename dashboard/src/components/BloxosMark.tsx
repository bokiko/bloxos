import type { SVGProps } from "react";

/** BloxOS: square enclosure, compute core, connected node. Inherits text color. */
export function BloxosMark(props: SVGProps<SVGSVGElement>) {
  return (
    <svg viewBox="0 0 64 64" fill="none" aria-hidden="true" {...props}>
      <path d="M38 8H18A10 10 0 0 0 8 18v28a10 10 0 0 0 10 10h28a10 10 0 0 0 10-10V26" stroke="currentColor" strokeWidth="5" strokeLinecap="round" />
      <rect x="22" y="22" width="20" height="20" rx="4" fill="currentColor" />
      <rect x="48" y="5" width="11" height="11" rx="3" fill="currentColor" />
    </svg>
  );
}
