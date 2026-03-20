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

## 2026-03-19 — Testing: Comprehensive tests for mining security validators

The mining-specific security utility functions in apps/api/src/utils/security.ts had zero test coverage despite being critical guards against shell injection, invalid OC values, and malformed miner configuration. Added 74 new unit tests covering all eight untested functions.

Functions covered: sanitizeCommand (chaining/redirection/dangerous chars), isCommandAllowed (whitelist enforcement), escapeShellArg (shell metacharacter containment), validateNumericRange (boundary + NaN guards), validateMinerName (all 7 valid miners, case-insensitive), validatePoolUrl (all stratum variants, injection schemes), validateWalletAddress (ETH/BTC formats, metacharacters), validateExtraArgs (safe flags vs injection chars), validateOCValue (NVIDIA/AMD boundaries, null pass-through, unknown setting).

Total suite: 132 passing tests (74 new + 58 existing). Zero new dependencies.

**Files changed:** apps/api/src/__tests__/security.mining.test.ts (new)
**Lines:** +369 / -0
**PR:** https://github.com/bokiko/bloxos/pull/8

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

## 2026-03-19 — Performance: useCachedFetch hook with stale-while-revalidate

Every dashboard page called fetch() in useEffect with no caching. Navigating away and back triggered a full round-trip even when data was seconds old. Added a zero-dependency module-level cache that returns stale data instantly while revalidating in the background, and deduplicates concurrent requests to the same URL.

New hook (useCachedFetch.ts) features: configurable TTL (default 30s), stale-while-revalidate semantics, in-flight Promise deduplication, optional revalidateOnFocus, full TypeScript generics with no any types.

Integrated into profit/page.tsx as the first consumer: replaced the manual useEffect/useCallback/Promise.all chain with four useCachedFetch calls, TTLs tuned per endpoint (summary/rigs 60s, prices 120s, settings 300s). All existing UI and data structures preserved exactly.

**Files changed:** apps/dashboard/src/hooks/useCachedFetch.ts (new), apps/dashboard/src/app/profit/page.tsx
**Lines:** +250 / -116
**PR:** https://github.com/bokiko/bloxos/pull/7

## 2026-03-20 — UI/UX: Search and coin filter on wallets page

The wallets page had no way to find a specific wallet in a long list. With multiple coins and many addresses, users had to scroll manually to locate what they needed.

Added a search bar (filters by name or address) and a coin filter dropdown (auto-populated from existing wallets) that render together in a styled toolbar row above the list. The clear (X) button removes the query instantly. When filters are active and nothing matches, the empty state shows "No wallets match your filters" instead of the onboarding prompt. Filter UI is hidden during loading and when no wallets exist.

UI style matches the existing pools page search bar (slate-800/50 bg, rounded-xl, blox-500 focus ring).

**Files changed:** apps/dashboard/src/app/wallets/page.tsx
**Lines:** +65 / -9
**PR:** https://github.com/bokiko/bloxos/pull/10

## 2026-03-20 — Bug Fix: Unified API URL resolution and NEXT_PUBLIC_API_URL support

Three separate files each maintained their own URL-building logic hardcoded to http://, silently breaking any deployment served over HTTPS or behind a reverse proxy. A fourth call in rigs/[id]/page.tsx bypassed getApiUrl() entirely with a bare http:// string.

lib/api.ts now exports getApiUrl() that respects a NEXT_PUBLIC_API_URL env override first, then infers from window.location.protocol so the scheme always matches the page. A new getWsUrl() helper derives ws/wss from getApiUrl() — single source of truth for WebSocket URLs. useWebSocket.ts and Terminal.tsx now import the shared getWsUrl() instead of duplicating it. The stray http:// string in rigs/[id]/page.tsx is replaced with getApiUrl(). Added .env.example documenting the new env var.

**Files changed:** apps/dashboard/src/lib/api.ts, apps/dashboard/src/hooks/useWebSocket.ts, apps/dashboard/src/components/Terminal.tsx, apps/dashboard/src/app/rigs/[id]/page.tsx, apps/dashboard/.env.example (new)
**Lines:** +32 / -16
**PR:** https://github.com/bokiko/bloxos/pull/11

## 2026-03-20 — Code Quality: Centralize farms in AuthContext

Every page that needed farms data (pools, wallets, flight-sheets, rig-groups, oc-profiles) was independently fetching /api/auth/me on mount, firing 5 extra network round-trips that duplicated what AuthContext already did during initialization.

Extended AuthContext with a Farm type and farms state, populated from the existing /api/auth/me call in initAuth() and refreshUser(). All five pages now call useAuth() to get farms instead of fetching the endpoint again. Removed the now-redundant local Farm interface definitions from four pages.

Result: 5 fewer network calls per page navigation, single source of truth for farms data, and a clear pattern for any future farm-aware pages.

**Files changed:** apps/dashboard/src/context/auth.tsx, apps/dashboard/src/app/pools/page.tsx, apps/dashboard/src/app/wallets/page.tsx, apps/dashboard/src/app/flight-sheets/page.tsx, apps/dashboard/src/app/rig-groups/page.tsx, apps/dashboard/src/app/oc-profiles/page.tsx
**Lines:** +29 / -88
**PR:** https://github.com/bokiko/bloxos/pull/12
