# Environment Variables Reference

> Complete list of all environment variables for BloxOS

---

## Quick Setup

Generate secure secrets:

```bash
# Generate all required secrets at once
cat << EOF
JWT_SECRET=$(openssl rand -base64 32)
COOKIE_SECRET=$(openssl rand -base64 32)
ENCRYPTION_KEY=$(openssl rand -hex 32)
EOF
```

---

## Required Variables

These must be set for the application to run.

### Database

| Variable | Description | Example |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | `postgresql://bloxos:password@localhost:5432/bloxos` |

### Security

| Variable | Description | Example |
|----------|-------------|---------|
| `JWT_SECRET` | Secret for signing JWT tokens. Must be 32+ characters. | `openssl rand -base64 32` |
| `COOKIE_SECRET` | Secret for signing cookies. Must be 32+ characters. | `openssl rand -base64 32` |
| `ENCRYPTION_KEY` | 256-bit key for encrypting SSH credentials. Must be 64 hex chars. | `openssl rand -hex 32` |

---

## Optional Variables

### Server Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `NODE_ENV` | `development` | Environment: `development` or `production` |
| `API_PORT` | `3001` | Port for API server |
| `API_HOST` | `0.0.0.0` | Host to bind API server |
| `DASHBOARD_PORT` | `3000` | Port for dashboard (Next.js) |

### Security Options

| Variable | Default | Description |
|----------|---------|-------------|
| `FORCE_HTTPS` | `false` | Redirect HTTP to HTTPS (production only) |
| `CORS_ORIGINS` | `*` (dev) / `false` (prod) | Comma-separated allowed origins |
| `RATE_LIMIT_MAX` | `100` | Max requests per minute per IP |
| `SESSION_TIMEOUT` | `4h` | JWT token expiration |
| `REFRESH_TOKEN_EXPIRY` | `7d` | Refresh token expiration |

### Redis (Optional)

| Variable | Default | Description |
|----------|---------|-------------|
| `REDIS_URL` | none | Redis connection URL for sessions |
| `REDIS_HOST` | `localhost` | Redis host (if not using URL) |
| `REDIS_PORT` | `6379` | Redis port |
| `REDIS_PASSWORD` | none | Redis password |

### Logging

| Variable | Default | Description |
|----------|---------|-------------|
| `LOG_LEVEL` | `info` (prod) / `debug` (dev) | Log level: `debug`, `info`, `warn`, `error` |
| `LOG_FORMAT` | `json` (prod) / `pretty` (dev) | Log format |

### Agent Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `STATS_INTERVAL` | `30` | Seconds between stats collection |
| `HEARTBEAT_INTERVAL` | `10` | Seconds between heartbeats |
| `HEARTBEAT_TIMEOUT` | `60` | Seconds before marking rig offline |
| `RECONNECT_MAX_DELAY` | `300` | Max seconds between reconnect attempts |

---

## Production Configuration

Recommended `.env` for production:

```bash
# Environment
NODE_ENV=production

# Database
DATABASE_URL=postgresql://bloxos:SECURE_PASSWORD@localhost:5432/bloxos

# Security - CHANGE THESE!
JWT_SECRET=your-very-long-random-jwt-secret-at-least-32-chars
COOKIE_SECRET=your-very-long-random-cookie-secret-at-least-32-chars
ENCRYPTION_KEY=64-character-hex-string-for-aes-256-encryption-key

# HTTPS
FORCE_HTTPS=true

# CORS - set to your domain
CORS_ORIGINS=https://bloxos.example.com

# Redis for sessions (recommended for multi-instance)
REDIS_URL=redis://localhost:6379

# Rate limiting
RATE_LIMIT_MAX=100

# Logging
LOG_LEVEL=info
LOG_FORMAT=json
```

---

## Development Configuration

Recommended `.env` for development:

```bash
# Environment
NODE_ENV=development

# Database (use different port to avoid conflicts)
DATABASE_URL=postgresql://bloxos:bloxos@localhost:5433/bloxos

# Security (can use simpler values in dev)
JWT_SECRET=dev-jwt-secret-not-for-production
COOKIE_SECRET=dev-cookie-secret-not-for-production
ENCRYPTION_KEY=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef

# Logging
LOG_LEVEL=debug
LOG_FORMAT=pretty
```

---

## Docker Compose Variables

When using Docker Compose, these are set in `docker-compose.yml`:

| Variable | Description |
|----------|-------------|
| `POSTGRES_USER` | PostgreSQL username |
| `POSTGRES_PASSWORD` | PostgreSQL password |
| `POSTGRES_DB` | PostgreSQL database name |

---

## Agent Environment Variables

These are set on the mining rig in `/etc/bloxos/agent.conf`:

| Variable | Required | Description |
|----------|----------|-------------|
| `SERVER_URL` | Yes | WebSocket URL to server (e.g., `ws://server:3001`) |
| `RIG_TOKEN` | Yes | Authentication token for this rig |
| `STATS_INTERVAL` | No | Override stats interval (default: 30s) |
| `HEARTBEAT_INTERVAL` | No | Override heartbeat interval (default: 10s) |
| `LOG_LEVEL` | No | Agent log level (default: info) |

---

## Security Notes

1. **Never commit `.env` to git** - It's in `.gitignore`
2. **Use strong secrets** - Generate with `openssl rand`
3. **Rotate secrets periodically** - Especially after team changes
4. **Different secrets per environment** - Dev, staging, production should differ
5. **Restrict file permissions** - `chmod 600 .env`

---

## Validation

The server validates required variables on startup:

```bash
# If missing required variables, you'll see:
[Security] Fatal: Error: Missing required secrets: JWT_SECRET, ENCRYPTION_KEY

# To test your configuration:
node -e "require('./apps/api/src/utils/security.ts').validateSecrets()"
```

---

*Last Updated: January 2026*
