# BloxOS Improvement Log

## 2026-03-17 — UI/UX: Skeleton loading states across dashboard pages

Replaced all basic spinner/text loading states with proper skeleton UI across the six main dashboard pages. The skeleton components match the rough shape of actual content (rig cards, stat cards, table rows, profile cards) using animated pulse effects, making the app feel more polished and reducing perceived load time.

**Files changed:**
- `apps/dashboard/src/components/Skeleton.tsx` (new — 10 reusable skeleton variants)
- `apps/dashboard/src/app/rigs/page.tsx`
- `apps/dashboard/src/app/profit/page.tsx`
- `apps/dashboard/src/app/alerts/page.tsx`
- `apps/dashboard/src/app/oc-profiles/page.tsx`
- `apps/dashboard/src/app/pools/page.tsx`
- `apps/dashboard/src/app/wallets/page.tsx`

**PR:** https://github.com/bokiko/bloxos/pull/1
