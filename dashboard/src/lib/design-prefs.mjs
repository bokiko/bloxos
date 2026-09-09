export const DESIGN_LAYOUTS = ['classic', 'wall', 'grove', 'console', 'ledger'];
export const DESIGN_COLORS = ['original', 'bright', 'dark'];
export function normalizeDesign(value) {
  return {
    layout: DESIGN_LAYOUTS.includes(value?.layout) ? value.layout : 'classic',
    colors: Object.fromEntries(['wall', 'grove', 'console'].map(key =>
      [key, DESIGN_COLORS.includes(value?.colors?.[key]) ? value.colors[key] : 'original'])),
  };
}
export function designCacheKey(userID) { return `bloxos-design:${userID || 'guest'}`; }
export function hasDesignCache(storage, userID) {
  try { return DESIGN_LAYOUTS.includes(JSON.parse(storage.getItem(designCacheKey(userID)))?.layout); }
  catch { return false; }
}
export function readDesign(storage, userID) {
  try { return normalizeDesign(JSON.parse(storage.getItem(designCacheKey(userID)))); }
  catch { return normalizeDesign(null); }
}
export function hasPendingDesign(storage, userID) {
  try { return hasDesignCache(storage, userID) && JSON.parse(storage.getItem(designCacheKey(userID)))?.pendingSync === true; }
  catch { return false; }
}
export function writeDesign(storage, userID, value, pendingSync = false) {
  try { storage.setItem(designCacheKey(userID), JSON.stringify({ ...normalizeDesign(value), ...(pendingSync ? {pendingSync: true} : {}) })); }
  catch { /* Appearance remains usable with blocked/full storage. */ }
}
export function markDesignSynced(storage, userID, sent) {
  // A different tab may have saved a newer local choice while this request ran.
  if (JSON.stringify(readDesign(storage, userID)) === JSON.stringify(normalizeDesign(sent))) {
    writeDesign(storage, userID, sent);
  }
}
