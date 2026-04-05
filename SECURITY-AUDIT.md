# Security Assessment: BloxOS
Generated: 2026-04-05

## Executive Summary
- **Risk Level:** HIGH
- **Findings:** 1 critical, 3 high, 5 medium, 3 low
- **Immediate Actions Required:** Yes

## Threat Model
- **Attacker profiles:** Malicious user in a multi-tenant deployment; compromised mining rig agent; external attacker targeting the API
- **Assets:** SSH credentials (stored encrypted), mining wallet addresses, rig control (reboot/shutdown), JWT secrets
- **Attack surface:** REST API, WebSocket endpoints (dashboard + agent), SSH proxy/terminal

---

## Findings

### SEC-001 CRITICAL: Open Registration Allows Unauthorized Account Creation
**Location:** `apps/api/src/routes/auth.ts:58` and `apps/api/src/index.ts:53`
**Vulnerability:** Unrestricted user registration
**Risk:** Anyone with network access to the API can register an account. The `/api/auth/register` endpoint is in the `publicPaths` array and has no restriction after the first admin user is created. In a multi-user deployment, this means any attacker can create an account and gain access to the system (with USER role). Even in single-user self-hosted scenarios, an exposed API allows unauthorized account creation.

**Evidence:**
```typescript
// index.ts:50-57 - register is public
const publicPaths = [
  '/api/health',
  '/api/auth/login',
  '/api/auth/register',   // <-- No restriction
  '/api/auth/setup-required',
  '/api/auth/logout',
  '/api/auth/refresh',
];
```
The `authService.register()` at `auth-service.ts:113` creates the first user as ADMIN and subsequent users as USER, but there is no mechanism to disable registration after initial setup.

**Remediation:**
1. Add a system setting (in database or env var) to control whether open registration is allowed.
2. After the first admin is created, require either: (a) admin invitation/approval for new accounts, or (b) set `ALLOW_REGISTRATION=false` by default.
3. Quick fix: Check `hasUsers()` before allowing registration and reject if users already exist (unless admin creates via `/api/users` endpoint).

---

### SEC-002 HIGH: SSH Rig Routes Missing Authorization Check (IDOR)
**Location:** `apps/api/src/routes/ssh.ts:364,409,426`
**Vulnerability:** Insecure Direct Object Reference (IDOR) - Missing farm ownership verification
**Risk:** Any authenticated user can execute SSH commands on, refresh info for, or query system info of ANY rig by ID, regardless of whether they own the farm that rig belongs to. The three rig-specific SSH endpoints (`/ssh/rig/:rigId/exec`, `/ssh/rig/:rigId/refresh`, `/ssh/rig/:rigId/system-info`) only validate the rigId UUID format but never verify that the requesting user owns the rig.

**Evidence:**
```typescript
// ssh.ts:364 - No authorization middleware, no ownership check
app.post('/rig/:rigId/exec', async (request, reply) => {
    const { rigId } = request.params;
    // Only validates UUID format, then executes command
    if (!/^[0-9a-f]{8}-...$/i.test(rigId)) { ... }
    // ... directly calls sshManager.executeCommandOnRig(rigId, command)
    // NO ownership check!
});
```
Compare this to `routes/rigs.ts` which correctly uses `{ preHandler: [requireRigAccess] }` on every endpoint.

**Remediation:**
1. Add `requireRigAccess` middleware to all three endpoints: `/ssh/rig/:rigId/exec`, `/ssh/rig/:rigId/refresh`, `/ssh/rig/:rigId/system-info`.
2. Alternatively, add inline ownership verification by calling `getUserRigFilter` and filtering the rig query.

---

### SEC-003 HIGH: SSH System-Info Endpoint Bypasses Command Validation
**Location:** `apps/api/src/routes/ssh.ts:437-498`
**Vulnerability:** Internal commands use pipes, redirects, and shell features that would fail the `isCommandSafe()` check
**Risk:** The `GET /ssh/rig/:rigId/system-info` endpoint defines a set of hardcoded commands (lines 437-488) that include pipes (`|`), redirects (`2>/dev/null`), and shell expansion. These commands are executed via `sshManager.executeCommandOnRig()` which calls `validateUserCommand()` at `ssh-manager.ts:304`. However, `validateUserCommand` calls `sanitizeCommand` which rejects pipes and redirects (security.ts:100-101). This means the system-info endpoint will throw errors for most of its hardcoded commands.

Looking more carefully at the code flow: `executeCommandOnRig` at ssh-manager.ts:304 calls `this.validateUserCommand(command)` at line 310, and `validateUserCommand` calls `sanitizeCommand` which rejects `|` and `>`. The hardcoded commands at ssh.ts:439 contain `|` and `>` characters extensively.

**Wait** -- re-reading ssh-manager.ts:304-310 more carefully: `executeCommandOnRig` calls `this.validateUserCommand(command)` then `this.executeInternalCommand(credentials, command)`. The hardcoded system-info commands at ssh.ts:439-488 contain pipes and redirects. This means either: (a) these commands will fail validation and the endpoint is broken, or (b) there's a path where validation is bypassed.

Actually, looking at `ssh.ts:493`: `results[key] = await sshManager.executeCommandOnRig(rigId, cmd);` -- this IS calling the validated path. The `DANGEROUS_CHARS` regex at security.ts:78 checks for `|` which IS present in the hardcoded commands. This means the endpoint is functionally broken (commands will throw), which is actually a security *good* outcome but a functionality bug.

**Revised assessment:** This is a **design issue** rather than an exploitable vulnerability. The system-info endpoint should use `executeInternalCommand` (private method) rather than the validated `executeCommandOnRig`. Currently it's broken, not exploitable. However, if someone "fixes" it by removing validation, it becomes a vulnerability.

**Remediation:**
1. Create a public method `executeInternalCommandOnRig(rigId, command)` that uses the internal path (no user command validation) but restricts to a whitelist of known-safe info-gathering commands.
2. Use this method in the system-info endpoint.
3. Add authorization (SEC-002) before addressing this.

---

### SEC-004 HIGH: WebSocket Token in URL Query Parameter (Token Leakage)
**Location:** `apps/api/src/routes/agent-websocket.ts:153`, `apps/api/src/routes/websocket.ts:57`, `apps/api/src/routes/terminal.ts:63`, `apps/agent/internal/ws/client.go:185`
**Vulnerability:** JWT/agent tokens passed in URL query parameters
**Risk:** Tokens in URLs are logged by web servers, proxies, load balancers, and browser history. The Go agent at `client.go:185` sets `q.Set("token", c.token)` directly in the URL. All three WebSocket endpoints accept `?token=...` as a query parameter for authentication. If the API is behind Nginx or any reverse proxy, these tokens will appear in access logs.

**Evidence:**
```go
// client.go:183-186
q := u.Query()
q.Set("token", c.token)
u.RawQuery = q.Encode()
```
```typescript
// agent-websocket.ts:153
const queryToken = (request.query as { token?: string })?.token;
```

**Remediation:**
1. Prefer sending the token as the first WebSocket message after connection (the `auth` message type already exists and works).
2. If query parameter auth is needed for compatibility, strip the token from logs via Nginx config (`$request_uri` -> sanitized version).
3. In the Go agent, connect without the token in the URL and send an auth message immediately after connection.

---

### SEC-005 MEDIUM: CSRF Protection Disabled in Development
**Location:** `apps/api/src/index.ts:231-235`
**Vulnerability:** CSRF protection is only enabled when `NODE_ENV=production`
**Risk:** If the application is deployed without explicitly setting `NODE_ENV=production`, all state-changing requests (POST/PUT/PATCH/DELETE) are unprotected against CSRF attacks. Given this is a self-hosted application, users may run it in "development" mode in production without realizing the security implications.

**Evidence:**
```typescript
// index.ts:231-235
if (isProduction) {
    app.addHook('preHandler', async (request, reply) => {
      await csrfValidate(request, reply);
    });
}
```

**Remediation:**
1. Enable CSRF protection by default and only disable it if an explicit `DISABLE_CSRF=true` env var is set.
2. Log a warning at startup if CSRF is disabled.
3. Document in the deployment guide that `NODE_ENV=production` is required.

---

### SEC-006 MEDIUM: WebSocket Broadcast Leaks Data Across Tenants
**Location:** `apps/api/src/routes/websocket.ts:30-37` and `apps/api/src/routes/agent-websocket.ts:125-131`
**Vulnerability:** Rig updates broadcast to ALL subscribed dashboard clients without tenant filtering
**Risk:** In a multi-user deployment, when a rig sends stats, `broadcastRigUpdate()` calls `broadcastToSubscribers('rigs', ...)` which sends the data to ALL clients subscribed to the 'rigs' channel, regardless of whether they own that rig's farm. User A can see User B's rig status updates, GPU temperatures, hashrates, and miner status in real-time.

**Evidence:**
```typescript
// websocket.ts:30-37
export function broadcastToSubscribers(channel: string, event: string, data: unknown) {
  const message = createSafeWSMessage('broadcast', { event, channel, data: sanitizeOutput(data) });
  clients.forEach((client) => {
    // No ownership check! Sends to ALL clients on the channel
    if (client.subscriptions.has(channel) && client.socket.readyState === 1) {
      client.socket.send(message);
    }
  });
}
```

Note: The initial data load at `websocket.ts:195` correctly uses `getUserRigFilter`, but real-time updates bypass this.

**Remediation:**
1. Store the `farmId` with the rig update broadcast data.
2. Before sending to each client, check if the client's user has access to the rig's farm (either via stored farm IDs on the client object, or by checking `getUserFarmIds`).
3. Add a `farmIds` field to the `WebSocketClient` interface and populate it on authentication.

---

### SEC-007 MEDIUM: Static Salt in PBKDF2 Key Derivation
**Location:** `apps/api/src/utils/encryption.ts:32`
**Vulnerability:** Hardcoded static salt for PBKDF2 key derivation
**Risk:** The encryption key derivation uses a static salt (`'bloxos-encryption-salt-v1'`). While the code comments note "Static salt is OK here since key should be random," this weakens the key derivation. If the `ENCRYPTION_KEY` env var has low entropy (e.g., a human-chosen passphrase), the static salt means all BloxOS instances with the same key produce the same derived key, enabling rainbow table attacks.

**Evidence:**
```typescript
// encryption.ts:32
return crypto.pbkdf2Sync(
    key,
    'bloxos-encryption-salt-v1', // Static salt
    ITERATIONS,
    KEY_LENGTH,
    'sha512'
);
```

**Remediation:**
1. Generate a random salt on first startup and persist it (e.g., in database or a file).
2. Alternatively, document that `ENCRYPTION_KEY` must be a cryptographically random string (not a passphrase) and increase the minimum required length.

---

### SEC-008 MEDIUM: In-Memory Token Blacklist and Rate Limiting (Not Persistent)
**Location:** `apps/api/src/services/session-store.ts`, `apps/api/src/utils/security.ts:313`
**Vulnerability:** Token blacklist and rate limits stored in memory only
**Risk:** On server restart, all blacklisted tokens become valid again. An attacker who obtains a compromised token can simply wait for or cause a server restart to bypass the blacklist. Similarly, rate limiting and account lockout are reset on restart. The code has TODO comments acknowledging this ("In production, should use Redis").

**Evidence:**
```typescript
// session-store.ts:24
const tokenBlacklist = new Map<string, number>(); // In-memory only

// security.ts:313
const rateLimitStore = new Map<string, { count: number; resetTime: number }>();
```

**Remediation:**
1. Use Redis (already available in the stack at port 6380) for token blacklisting, rate limiting, and account lockout.
2. The `REDIS_URL` env var is already defined in `.env.example` but unused.
3. Priority: Token blacklist is most critical since it's the logout mechanism.

---

### SEC-009 MEDIUM: Rig Token Stored as Plaintext in Database
**Location:** `apps/api/src/routes/rigs.ts:98`, `apps/api/src/services/ssh-manager.ts:231`, `apps/api/src/middleware/authorization.ts:411`
**Vulnerability:** Agent authentication tokens stored in plaintext
**Risk:** The rig token (used by the Go agent to authenticate) is stored as a plaintext field in the `rig` table. Database access (SQL injection, backup exposure, admin panel leak) exposes all agent tokens. These tokens allow full control of the rig (start/stop miners, reboot, shutdown).

**Evidence:**
```typescript
// rigs.ts:98
const token = nanoid(32);
const rig = await prisma.rig.create({
    data: { ...fields, token } // stored as-is
});

// authorization.ts:411 - token lookup is by plaintext match
const rig = await prisma.rig.findUnique({
    where: { token }, // Direct plaintext comparison
});
```

**Remediation:**
1. Store only a hash of the token (SHA-256). Show the full token to the user once during rig creation.
2. On agent authentication, hash the incoming token and compare against the stored hash.
3. This follows the same pattern as API keys in services like GitHub/Stripe.

---

### SEC-010 LOW: Agent Token Passed in `X-API-Key` Header - Weak Validation
**Location:** `apps/api/src/index.ts:207-222`
**Vulnerability:** Agent auth hook validates format but defers actual validation
**Risk:** The global auth hook for agent paths checks that a token exists and has minimum length, but the actual token validation happens in the route handler. If a route handler forgets to validate, the request proceeds with an unchecked token. Currently all agent route handlers do validate, so this is a defense-in-depth concern, not an active vulnerability.

**Evidence:**
```typescript
// index.ts:216-222
if (typeof token !== 'string' || token.length < 10) {
    return reply.status(401).send({ error: 'Invalid API key format' });
}
// Token validation happens in the route handler
return;
```

**Remediation:**
1. Move token validation into the global auth hook for agent paths so that invalid tokens are rejected before reaching the route handler.

---

### SEC-011 LOW: Miner Config Saved with World-Readable Permissions
**Location:** `apps/agent/internal/executor/executor.go:578`
**Vulnerability:** Miner config file (containing wallet address, pool URL) written with 0644 permissions
**Risk:** On a shared system, any local user can read the miner configuration, which includes wallet addresses and pool credentials.

**Evidence:**
```go
// executor.go:578
return os.WriteFile(filepath.Join(e.configPath, "miner.json"), data, 0644)
```

**Remediation:**
1. Use `0600` permissions: `os.WriteFile(path, data, 0600)`
2. Also fix the config directory creation at line 569: use `0700` instead of `0755`.

---

### SEC-012 LOW: Audit Log Only In-Memory (Not Persistent)
**Location:** `apps/api/src/utils/security.ts:480-518`
**Vulnerability:** Audit logs stored in memory array, capped at 10,000 entries
**Risk:** Security-relevant events (login failures, unauthorized access attempts, SSH commands, admin actions) are lost on restart and when the 10K cap is reached. No forensic trail exists for security incidents.

**Evidence:**
```typescript
// security.ts:480
const auditLogs: AuditLogEntry[] = [];
// ...
if (auditLogs.length > 10000) {
    auditLogs.shift();
}
```

**Remediation:**
1. Write audit logs to a database table (PostgreSQL is already available).
2. Alternatively, write to a log file that can be shipped to a SIEM.
3. At minimum, ensure console.log output (which happens in dev mode) also happens in production for critical events.

---

## Dependency Vulnerabilities

Not checked (no `npm audit` run as project services aren't running). Recommend running:
```bash
pnpm audit
```

## Secrets Exposure Check
- `.env` files: In `.gitignore` -- YES (`.env`, `.env.local`, `.env.*.local`)
- No `.env` files found committed to git -- VERIFIED
- `.env.example` contains placeholder values only -- VERIFIED
- Hardcoded dev-only secrets: Present in `auth-service.ts:23` and `encryption.ts:20` -- guarded by `isProduction` checks
- JWT_SECRET, ENCRYPTION_KEY, COOKIE_SECRET: Required in production (validated at startup) -- GOOD
- SSH credentials: Encrypted with AES-256-GCM before storage -- GOOD

## Recommendations

### Immediate (Critical/High)
1. **SEC-001**: Disable open registration after first user is created. This is the highest priority -- any network-accessible instance allows unauthorized account creation.
2. **SEC-002**: Add `requireRigAccess` to all SSH rig endpoints (`/ssh/rig/:rigId/*`). Any authenticated user can currently SSH-exec commands on any rig.
3. **SEC-004**: Stop sending tokens in WebSocket URL query parameters. Use message-based auth instead.

### Short-term (Medium)
4. **SEC-005**: Enable CSRF by default, not just in production mode.
5. **SEC-006**: Add tenant filtering to WebSocket broadcasts. Currently leaks rig data across users.
6. **SEC-008**: Migrate token blacklist and rate limiting to Redis (already in stack).
7. **SEC-009**: Hash rig agent tokens before storage. Show plaintext token only once.
8. **SEC-007**: Use a random per-instance salt for PBKDF2.

### Long-term (Hardening)
9. **SEC-010**: Move agent token validation into the global auth hook.
10. **SEC-011**: Tighten file permissions in Go agent.
11. **SEC-012**: Persist audit logs to database.
12. Add `npm audit` / dependency scanning to CI pipeline.
13. Add rate limiting specifically to the `/api/auth/register` endpoint.
14. Consider adding 2FA for admin accounts.
15. Add HTTPS enforcement by default (not gated behind `FORCE_HTTPS=true`).
