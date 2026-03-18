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

## 2026-03-18 — Testing: Route integration tests for auth and health endpoints

Added Fastify injection-based integration tests for the auth and health API routes — the first route-level test coverage in the codebase. Tests use `vi.mock()` to stub Prisma and authService so no real database is required. Covers setup-required check, register/login validation and success flows, logout cookie clearing, and health check 200/503 responses.

**Files changed:**
- `apps/api/src/__tests__/routes.auth.test.ts` (new — 10 tests)
- `apps/api/src/__tests__/routes.health.test.ts` (new — 3 tests)

**Lines:** +268 / -0
**PR:** https://github.com/bokiko/bloxos/pull/2

## 2026-03-18 — Code Quality: Extract shared API utilities to lib/api.ts

Centralized `getApiUrl()` and `getCsrfToken()` — two utility functions copy-pasted verbatim across 17 files in the dashboard. Created `apps/dashboard/src/lib/api.ts` as a single source of truth and updated all consumers to import from it.

**Files changed:** apps/dashboard/src/lib/api.ts (new), + 17 files updated
**Lines:** +26 / -129 (net -103)
**PR:** https://github.com/bokiko/bloxos/pull/4

## 2026-03-18 — Security: Consolidate auth to global middleware

Three route files (profit.ts, settings.ts, users.ts) were bypassing the centralized `requireAuth` middleware by re-implementing JWT extraction and verification inline. Despite the global `onRequest` hook already calling `requireAuth` and populating `request.user` for every protected route, these files ignored `request.user` and re-verified the JWT themselves — duplicating security logic that could drift from the canonical implementation.

Removed `getUserFarm()` from profit.ts, `getUserId()` from settings.ts, and the local `requireAdmin()` from users.ts. Replaced all usages with direct `request.user!.userId` reads. Converted users.ts routes to use `preHandler: [requireAdmin]` from the centralized middleware.

**Files changed:** apps/api/src/routes/profit.ts, apps/api/src/routes/settings.ts, apps/api/src/routes/users.ts
**Lines:** +40 / -106 (net -66)
**PR:** https://github.com/bokiko/bloxos/pull/5

## 2026-03-18 — Accessibility: ARIA markup across core dashboard components

The dashboard had essentially zero accessibility support — one aria-* attribute across 50+ component files. Added comprehensive WCAG-compliant ARIA markup across the five most impactful files.

layout.tsx: skip-to-main-content link, role=banner on mobile header, aria-label on hamburger/close buttons, role=navigation + aria-label on nav, aria-current=page on active links, aria-label on alert/update count badges, aria-label on command palette + logout buttons, id=main-content + tabIndex=-1 on main, role=complementary + aria-label on aside, role=presentation + aria-hidden on mobile overlay, role=status + aria-label on loading spinner.

Toast.tsx: role=alert/status per toast severity, aria-live=assertive for errors and polite for info/success.

CommandPalette.tsx: role=dialog + aria-modal + aria-label on modal, role=combobox + aria-autocomplete + aria-activedescendant on search input, role=listbox on results list, role=option + aria-selected on each result item.

login/page.tsx: role=alert + id on error div, aria-describedby linking inputs to the error element.

Skeleton.tsx: role=status + aria-label + aria-busy=true on all 10 skeleton components.

**Files changed:** apps/dashboard/src/app/layout.tsx, apps/dashboard/src/app/login/page.tsx, apps/dashboard/src/components/CommandPalette.tsx, apps/dashboard/src/components/Skeleton.tsx, apps/dashboard/src/components/Toast.tsx
**Lines:** +44 / -20
**PR:** https://github.com/bokiko/bloxos/pull/6
