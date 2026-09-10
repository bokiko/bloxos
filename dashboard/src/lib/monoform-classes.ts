// Monoform — the class strings the product's routes share.
//
// `.mf-*` in monoform.css covers the panel, the table, the primary action and
// the status marks. The pieces below are the ones that are pure Tailwind
// compositions: secondary controls, inputs, panel headers, tab triggers,
// menus and dialogs. They live here rather than being retyped per route so
// that /inventory, /machine, /sessions, /versions and /users cannot drift into
// five slightly different products.
//
// Every value resolves to a Monoform token — no raw palette colours, no
// gradients, no shadows outside the single overlay shadow.

/** Secondary control: 36px, hairline border, graphite fill. Pairs with `.mf-action`. */
export const MF_BUTTON =
  "inline-flex h-9 shrink-0 items-center justify-center gap-2 rounded-[10px] border border-border-default " +
  "bg-surface-raised px-3 text-[13px] font-medium text-text-primary transition-colors " +
  "hover:bg-surface-elevated disabled:pointer-events-none disabled:opacity-50";

/** The same control, quieter — for tertiary actions inside a panel. */
export const MF_BUTTON_QUIET =
  "inline-flex h-9 shrink-0 items-center justify-center gap-2 rounded-[10px] border border-transparent " +
  "px-3 text-[13px] font-medium text-text-tertiary transition-colors hover:bg-surface-elevated " +
  "hover:text-text-primary disabled:pointer-events-none disabled:opacity-50";

/** Destructive confirm. Red is carried by text and border, never by a fill. */
export const MF_BUTTON_DANGER =
  "inline-flex h-9 shrink-0 items-center justify-center gap-2 rounded-[10px] border border-status-critical/40 " +
  "bg-status-critical-tint px-3 text-[13px] font-medium text-status-critical transition-colors " +
  "hover:bg-status-critical/20 disabled:pointer-events-none disabled:opacity-50";

/** Text field. Matches the 36px control height. */
export const MF_INPUT =
  "h-9 rounded-[10px] border-border-default bg-surface-sunken text-[13px] text-text-primary " +
  "placeholder:text-text-disabled";

/** Small caps label above a field. */
export const MF_LABEL = "mb-1.5 block text-[10px] uppercase tracking-[0.05em] text-text-tertiary font-mono";

/** A panel's header strip: title on the left, metadata or actions on the right. */
export const MF_PANEL_HEAD =
  "flex flex-wrap items-center justify-between gap-3 border-b border-border-subtle px-6 py-3.5";

/** The heading inside `MF_PANEL_HEAD`. */
export const MF_PANEL_TITLE = "text-[13px] font-semibold text-text-primary";

/**
 * Tab trigger. Base UI's `line` variant paints its indicator with
 * `after:bg-foreground`; Monoform's selection colour is the one blue, and the
 * `!` is what makes that override land regardless of stylesheet order.
 */
export const MF_TAB =
  "px-3.5 py-1.5 gap-1.5 text-[13px] flex-none data-active:text-text-primary after:bg-accent!";

/** Dropdown / popover surface. */
export const MF_MENU = "border-border-default bg-surface-elevated";

/** Dropdown item. */
export const MF_MENU_ITEM = "gap-2 text-[13px] text-text-primary";

/** Dialog surface — the one place Monoform uses its overlay shadow. */
export const MF_DIALOG = "border-border-default bg-surface-raised text-text-primary ring-0";
