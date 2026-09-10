"use client";

// Monoform — the one disclosure used by every collapsible Overview section.
//
// A real disclosure, not a CSS trick: a native <button> carrying
// `aria-expanded` and `aria-controls`, and a labelled region it owns. Native
// buttons are keyboard-operable for free, so there is no key handling here to
// get wrong.
//
// Collapsed does not mean gone. The header keeps its name and gains a compact
// summary — the count, the reading, whatever the section's one number is — so
// a folded section still tells the operator something. The region is animated
// by `.mf-region` in monoform.css (grid-template-rows, 200ms) and is hidden
// with `visibility` at the end of that transition, which also takes its
// contents out of the tab order and the accessibility tree.

import type { ReactNode } from "react";
import { ChevronDown } from "lucide-react";

export interface DisclosureProps {
  /** Stable, page-unique section id; also the persistence key. */
  id: string;
  /** The section's name. Rendered as the kicker. */
  label: string;
  /** Shown next to the name ONLY while collapsed. */
  summary?: ReactNode;
  open: boolean;
  onToggle: () => void;
  /** Header-row content to the right of the toggle. */
  actions?: ReactNode;
  /** Wraps the label in a heading when the section needs a real one. */
  headingID?: string;
  children: ReactNode;
}

export function Disclosure({
  id,
  label,
  summary,
  open,
  onToggle,
  actions,
  headingID,
  children,
}: DisclosureProps) {
  const buttonID = `mf-disclosure-${id}`;
  const regionID = `mf-region-${id}`;

  const toggle = (
    <button
      type="button"
      id={buttonID}
      className="mf-disclosure-toggle"
      aria-expanded={open}
      aria-controls={regionID}
      onClick={onToggle}
    >
      <ChevronDown className="mf-disclosure-chevron" aria-hidden="true" />
      <span className="mf-kicker">{label}</span>
      {!open && summary !== undefined && summary !== null && (
        <span className="mf-disclosure-summary mf-metric">{summary}</span>
      )}
    </button>
  );

  return (
    <>
      <div className="mf-disclosure-head">
        {headingID ? (
          <h2 id={headingID} className="m-0 min-w-0 text-[inherit] font-normal">
            {toggle}
          </h2>
        ) : (
          toggle
        )}
        {actions}
      </div>
      <div
        id={regionID}
        role="region"
        aria-labelledby={buttonID}
        className="mf-region"
        data-collapsed={open ? undefined : "true"}
      >
        <div className="mf-region-inner">{children}</div>
      </div>
    </>
  );
}
