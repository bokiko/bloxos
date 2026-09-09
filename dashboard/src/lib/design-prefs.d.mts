export type Layout = 'classic' | 'wall' | 'grove' | 'console' | 'ledger';
export type DesignColor = 'original' | 'bright' | 'dark';
/** Only the layouts with a colour column carry a colour. Classic and Ledger
 * each ship one fixed palette, so neither stores nor offers a choice. */
export type ColorfulLayout = Exclude<Layout, 'classic' | 'ledger'>;
export interface DesignPreferences { layout: Layout; colors: Record<ColorfulLayout, DesignColor>; }
export const DESIGN_LAYOUTS: readonly Layout[];
export const DESIGN_COLORS: readonly DesignColor[];
export function normalizeDesign(value: unknown): DesignPreferences;
export function designCacheKey(userID: string | null): string;
export function hasDesignCache(storage: Pick<Storage, 'getItem'>, userID: string | null): boolean;
export function readDesign(storage: Pick<Storage, 'getItem'>, userID: string | null): DesignPreferences;
export function hasPendingDesign(storage: Pick<Storage, 'getItem'>, userID: string | null): boolean;
export function writeDesign(storage: Pick<Storage, 'setItem'>, userID: string | null, value: DesignPreferences, pendingSync?: boolean): void;
export function markDesignSynced(storage: Pick<Storage, 'getItem' | 'setItem'>, userID: string | null, value: DesignPreferences): void;
