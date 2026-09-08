export const DESIGN_LAYOUTS = ['classic', 'wall', 'grove', 'console'];
export const DESIGN_COLORS = ['original', 'bright', 'dark'];
export function normalizeDesign(value) {
  return {
    layout: DESIGN_LAYOUTS.includes(value?.layout) ? value.layout : 'classic',
    colors: Object.fromEntries(['wall', 'grove', 'console'].map(key =>
      [key, DESIGN_COLORS.includes(value?.colors?.[key]) ? value.colors[key] : 'original'])),
  };
}
export function designCacheKey(userID) { return `bloxos-design:${userID || 'guest'}`; }
export function readDesign(storage, userID) {
  try { return normalizeDesign(JSON.parse(storage.getItem(designCacheKey(userID)))); }
  catch { return normalizeDesign(null); }
}
export function writeDesign(storage, userID, value) {
  try { storage.setItem(designCacheKey(userID), JSON.stringify(normalizeDesign(value))); }
  catch { /* Appearance remains usable with blocked/full storage. */ }
}
