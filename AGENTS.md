# AGENTS.md - BloxOs Coding Agent Guidelines

> Instructions for AI coding agents operating in this repository

## Quick Reference

| Task | Command |
|------|---------|
| Install deps | `pnpm install` |
| Dev (all) | `pnpm dev` |
| Build (all) | `pnpm build` |
| Lint/Test | `pnpm lint` / `pnpm test` |
| Single app | `pnpm --filter <app> dev` |
| DB push/migrate | `pnpm db:push` / `pnpm db:migrate` |

## Build & Test Commands

```bash
# Monorepo (Turborepo)
pnpm dev                              # Start all apps
pnpm build                            # Build all
pnpm --filter api test -- file.test.ts  # Single test

# Database
pnpm db:push                          # Push schema (dev)
pnpm db:migrate                       # Create migration
pnpm db:studio                        # Prisma Studio

# Go Agent (from apps/agent)
go build -o bloxos-agent ./cmd/agent
go test ./...                         # All tests
go test -run TestName ./...           # Single test
```

## Project Structure

```
bloxos/
├── apps/
│   ├── dashboard/       # Next.js 15 (App Router)
│   ├── api/             # Fastify API server
│   └── agent/           # Go 1.22+ binary
├── packages/
│   ├── database/        # Prisma schema & client
│   ├── shared/          # Shared TypeScript types
│   └── ui/              # Shared React components
└── docker/              # Docker Compose files
```

## Code Style - TypeScript

**Imports Order:**
1. Node built-ins (`node:path`)
2. External (`react`, `zod`)
3. Internal (`@bloxos/database`)
4. Relative (`./utils`)

**Formatting:** 2 spaces, single quotes, semicolons, trailing commas

**Types:**
- `interface` for objects, `type` for unions
- Explicit return types on exports
- `unknown` over `any`

```typescript
interface RigStats {
  id: string;
  hashrate: number | null;
}

export function calculatePower(gpus: GPU[]): number {
  return gpus.reduce((sum, gpu) => sum + (gpu.powerDraw ?? 0), 0);
}
```

**Naming:**
- `PascalCase`: Components, Types, Interfaces
- `camelCase`: functions, variables
- `SCREAMING_SNAKE`: constants
- `kebab-case`: file names

**Error Handling:** try/catch for async, log with context, return error states

## Code Style - Go

**Formatting:** `gofmt` / `goimports`
**Naming:** Exported `PascalCase`, unexported `camelCase`, acronyms `HTTP`, `ID`
**Errors:** Check immediately, wrap: `fmt.Errorf("context: %w", err)`

## Validation & Database

- **Zod** for all API input validation
- Schema: `packages/database/prisma/schema.prisma`
- Import: `@bloxos/database`
- Use transactions for multi-step ops

## Environment & Security

- Never commit `.env` (use `.env.example`)
- Validate env vars at startup
- Validate input with Zod
- Rate limit API endpoints

## Git Conventions

```
<type>: <description>
Types: feat, fix, refactor, docs, test, chore

Branches: feature/add-dashboard, fix/websocket-reconnect
```

## Important Notes

- **Turborepo monorepo** with **pnpm**
- Node 22+, pnpm 9+ required
- Incremental development preferred
- Keep Go agent lightweight

---

## Testing Patterns

```typescript
// Unit test example (Vitest/Jest)
describe('calculateHashrate', () => {
  it('should sum GPU hashrates', () => {
    const gpus = [{ hashrate: 100 }, { hashrate: 50 }];
    expect(calculateHashrate(gpus)).toBe(150);
  });
});
```

```go
// Go test example
func TestCollector_GetGPUStats(t *testing.T) {
    c := NewCollector()
    stats, err := c.GetGPUStats()
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if len(stats) == 0 {
        t.Error("expected at least one GPU")
    }
}
```

---

## Common Patterns

**API Route (Fastify):**
```typescript
app.post('/rigs', async (request, reply) => {
  const data = CreateRigSchema.parse(request.body);
  const rig = await prisma.rig.create({ data });
  return reply.status(201).send(rig);
});
```

**React Component:**
```typescript
export const RigCard: FC<{ rig: Rig }> = ({ rig }) => {
  return (
    <div className="rig-card">
      <h3>{rig.name}</h3>
      <span>{rig.status}</span>
    </div>
  );
};
```

*Last Updated: January 2026*
