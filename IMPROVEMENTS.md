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
