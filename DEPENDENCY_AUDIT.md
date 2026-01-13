# Dependency Audit Report

**Date:** 2026-01-13
**Project:** BloxOS

---

## Summary

| Category | Count |
|----------|-------|
| Security Vulnerabilities | 1 (moderate) |
| Outdated Packages | 10+ |
| Redundant/Bloated Dependencies | 2 |
| Recommendations | 8 |

---

## 1. Security Vulnerabilities

### Node.js (pnpm audit)

| Severity | Package | Issue | Fix |
|----------|---------|-------|-----|
| **Moderate** | `esbuild` (transitive) | CORS vulnerability allowing any website to send requests to dev server | Upgrade to `>=0.25.0` |

**Advisory:** https://github.com/advisories/GHSA-67mh-4wv8-2f99

This vulnerability affects the development server only (not production), but should still be addressed.

### Go (govulncheck)

Unable to run full scan due to a code error in `apps/agent/internal/executor/executor.go:293` (missing `strconv` import). After fixing that issue, run:

```bash
cd apps/agent && go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

---

## 2. Outdated Packages

### Node.js

| Package | Current | Latest | Location |
|---------|---------|--------|----------|
| `typescript` | ^5.4.0 | 5.9.3 | Root, all packages |
| `turbo` | ^2.0.0 | 2.7.4 | Root |
| `@types/node` | ^20.0.0 | 25.0.8 | Root, all packages |
| `@prisma/client` | ^5.15.0 | 6.x | packages/database |
| `prisma` | ^5.15.0 | 6.x | packages/database |

### Go Modules

| Module | Current | Latest |
|--------|---------|--------|
| `github.com/shirou/gopsutil/v3` | v3.24.1 | v3.24.5 |
| `github.com/go-ole/go-ole` | v1.2.6 | v1.3.0 |
| `golang.org/x/sys` | v0.16.0 | v0.40.0 |
| `github.com/tklauser/go-sysconf` | v0.3.12 | v0.3.16 |
| `github.com/tklauser/numcpus` | v0.6.1 | v0.11.0 |
| `github.com/yusufpapurcu/wmi` | v1.2.3 | v1.2.4 |
| `github.com/shoenig/go-m1cpu` | v0.1.6 | v0.1.7 |

---

## 3. Redundant/Bloated Dependencies

### Critical: Duplicate xterm packages (apps/dashboard)

**Issue:** Both `xterm@^5.3.0` and `@xterm/xterm@^6.0.0` are installed, but only `@xterm/xterm` is used.

**Evidence:** `apps/dashboard/src/components/Terminal.tsx:29` imports from `@xterm/xterm`:
```typescript
const { Terminal } = await import('@xterm/xterm');
```

**Fix:** Remove `xterm` from dependencies.

### pino-pretty in production dependencies (apps/api)

**Issue:** `pino-pretty` is listed as a production dependency but is only used in development mode.

**Evidence:** `apps/api/src/index.ts:83-88`:
```typescript
transport: isProduction ? undefined : {
  target: 'pino-pretty',
  ...
}
```

**Fix:** Move `pino-pretty` to `devDependencies`.

---

## 4. Recommendations

### High Priority

1. **Remove duplicate `xterm` package**
   ```bash
   cd apps/dashboard && pnpm remove xterm
   ```

2. **Move `pino-pretty` to devDependencies**
   ```bash
   cd apps/api && pnpm remove pino-pretty && pnpm add -D pino-pretty
   ```

3. **Update esbuild** (via transitive dependency update)
   ```bash
   pnpm update tsup  # tsup depends on esbuild
   ```

### Medium Priority

4. **Update TypeScript across all packages**
   ```bash
   pnpm update typescript -r
   ```

5. **Update Go dependencies**
   ```bash
   cd apps/agent && go get -u ./... && go mod tidy
   ```

6. **Fix Go code issue** - Add missing `strconv` import in `apps/agent/internal/executor/executor.go`

### Low Priority

7. **Consider Prisma 6.x upgrade** - Major version upgrade, requires migration testing

8. **Update @types/node to v22** - Match the Node.js engine requirement (>=22.0.0)

---

## 5. Commands to Apply Fixes

```bash
# 1. Remove duplicate xterm
cd apps/dashboard && pnpm remove xterm

# 2. Move pino-pretty to devDependencies
cd apps/api && pnpm remove pino-pretty && pnpm add -D pino-pretty

# 3. Update all dependencies
pnpm update -r

# 4. Update Go dependencies
cd apps/agent && go get -u ./... && go mod tidy

# 5. Verify no vulnerabilities remain
pnpm audit
```

---

## 6. Estimated Impact

| Fix | Bundle Size | Security | Maintenance |
|-----|-------------|----------|-------------|
| Remove xterm duplicate | -500KB+ | - | Improved |
| Move pino-pretty | -2MB+ (prod) | - | Improved |
| Update esbuild | - | Fixed | - |
| Update all deps | Varies | Improved | Improved |

---

## Notes

- This audit was performed on 2026-01-13
- All version numbers are approximate based on version ranges in package.json
- Production bundle sizes are estimates and should be verified with actual builds
