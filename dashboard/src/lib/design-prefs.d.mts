export type Layout = 'classic' | 'wall' | 'grove' | 'console';
export type DesignColor = 'original' | 'bright' | 'dark';
export interface DesignPreferences { layout: Layout; colors: Record<Exclude<Layout, 'classic'>, DesignColor>; }
export const DESIGN_LAYOUTS: readonly Layout[];
export const DESIGN_COLORS: readonly DesignColor[];
export function normalizeDesign(value: unknown): DesignPreferences;
export function designCacheKey(userID: string | null): string;
export function hasDesignCache(storage: Pick<Storage, 'getItem'>, userID: string | null): boolean;
export function readDesign(storage: Pick<Storage, 'getItem'>, userID: string | null): DesignPreferences;
export function hasPendingDesign(storage: Pick<Storage, 'getItem'>, userID: string | null): boolean;
export function writeDesign(storage: Pick<Storage, 'setItem'>, userID: string | null, value: DesignPreferences, pendingSync?: boolean): void;
export function markDesignSynced(storage: Pick<Storage, 'getItem' | 'setItem'>, userID: string | null, value: DesignPreferences): void;
