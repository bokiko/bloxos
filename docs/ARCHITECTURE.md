# BloxOs Architecture

> Technical design document for the BloxOs mining rig management system

---

## System Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              BLOXOS ARCHITECTURE                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│   ┌──────────────┐    ┌──────────────┐    ┌──────────────┐                 │
│   │   Mining     │    │   Mining     │    │   Mining     │                 │
│   │   Rig #1     │    │   Rig #2     │    │   Rig #N     │                 │
│   │              │    │              │    │              │                 │
│   │ ┌──────────┐ │    │ ┌──────────┐ │    │ ┌──────────┐ │                 │
│   │ │  Agent   │ │    │ │  Agent   │ │    │ │  Agent   │ │                 │
│   │ └────┬─────┘ │    │ └────┬─────┘ │    │ └────┬─────┘ │                 │
│   └──────┼───────┘    └──────┼───────┘    └──────┼───────┘                 │
│          │                   │                   │                          │
│          └───────────────────┴───────────────────┘                          │
│                              │                                               │
│                      WebSocket (wss://)                                      │
│                              │                                               │
│   ┌──────────────────────────┴────────────────────────────┐                │
│   │                     API SERVER                         │                │
│   │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐   │                │
│   │  │   REST API  │  │  WebSocket  │  │   Workers   │   │                │
│   │  │  Endpoints  │  │   Handler   │  │  (Alerts)   │   │                │
│   │  └─────────────┘  └─────────────┘  └─────────────┘   │                │
│   └───────────────────────────┬───────────────────────────┘                │
│                               │                                             │
│          ┌────────────────────┼────────────────────┐                       │
│          │                    │                    │                       │
│   ┌──────┴──────┐      ┌──────┴──────┐      ┌──────┴──────┐               │
│   │  PostgreSQL │      │    Redis    │      │   Caddy     │               │
│   │  (Storage)  │      │   (Cache)   │      │  (Reverse   │               │
│   │             │      │   (Pub/Sub) │      │   Proxy)    │               │
│   └─────────────┘      └─────────────┘      └──────┬──────┘               │
│                                                     │                       │
│                                              ┌──────┴──────┐               │
│                                              │  Dashboard  │               │
│                                              │  (Next.js)  │               │
│                                              └─────────────┘               │
│                                                     │                       │
│                                              ┌──────┴──────┐               │
│                                              │   Browser   │               │
│                                              │   (User)    │               │
│                                              └─────────────┘               │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Components

### 1. Agent (Go)

**Purpose:** Runs on each mining rig, collects data, executes commands.

**Responsibilities:**
- Collect system information (hostname, IP, OS)
- Detect and query GPUs (nvidia-smi, rocm-smi)
- Detect running miners and query their APIs
- Maintain WebSocket connection to API server
- Send heartbeats every 10 seconds
- Send stats every 30 seconds
- Execute commands from server (start/stop miners, apply OC)
- Persist configuration locally

**Technology:**
- Language: Go 1.22+
- Single static binary (no dependencies)
- ~10MB compiled size
- Minimal resource usage (<50MB RAM, <1% CPU)

**Communication Protocol:**
```
Agent -> Server:
  - heartbeat: { rigId, agentVersion, uptime }
  - stats: { rigId, gpus[], miners[], system }
  - event: { rigId, type, severity, message }
  - command_result: { commandId, success, output }

Server -> Agent:
  - heartbeat_ack: { timestamp }
  - command: { id, action, payload }
  - config: { flightSheet, ocProfile }
```

### 2. API Server (Node.js/Fastify)

**Purpose:** Central hub that manages all rigs and serves the dashboard.

**Responsibilities:**
- Authenticate agents and users
- Store rig data in PostgreSQL
- Handle WebSocket connections from agents
- Broadcast updates to dashboards
- Process commands and route to agents
- Evaluate alert conditions
- Send notifications (Telegram, Discord)

**Technology:**
- Runtime: Node.js 22 LTS
- Framework: Fastify 4.x
- WebSocket: Socket.io 4.x
- Validation: Zod
- Database: Prisma 6.x

**API Structure:**
```
/api/v1
├── /auth
│   ├── POST /login
│   ├── POST /register
│   └── POST /refresh
├── /rigs
│   ├── GET /              # List all rigs
│   ├── GET /:id           # Get single rig
│   ├── POST /             # Register new rig
│   ├── PATCH /:id         # Update rig
│   ├── DELETE /:id        # Remove rig
│   └── POST /:id/command  # Send command
├── /farms
│   ├── GET /
│   ├── POST /
│   ├── PATCH /:id
│   └── DELETE /:id
├── /wallets
│   ├── GET /
│   ├── POST /
│   ├── PATCH /:id
│   └── DELETE /:id
├── /pools
│   ├── GET /
│   ├── POST /
│   ├── PATCH /:id
│   └── DELETE /:id
├── /flight-sheets
│   ├── GET /
│   ├── POST /
│   ├── PATCH /:id
│   ├── DELETE /:id
│   └── POST /:id/apply
├── /oc-profiles
│   ├── GET /
│   ├── POST /
│   ├── PATCH /:id
│   └── DELETE /:id
├── /alerts
│   ├── GET /rules
│   ├── POST /rules
│   ├── PATCH /rules/:id
│   ├── DELETE /rules/:id
│   └── GET /history
└── /users
    ├── GET /me
    ├── PATCH /me
    └── GET /api-keys
```

### 3. Dashboard (Next.js)

**Purpose:** Web interface for monitoring and controlling rigs.

**Responsibilities:**
- Display rig status and stats
- Real-time updates via WebSocket
- Manage wallets, pools, flight sheets
- Send commands to rigs
- Configure alerts
- User management

**Technology:**
- Framework: Next.js 15 (App Router)
- Styling: TailwindCSS
- State: Zustand
- Charts: Recharts
- Real-time: Socket.io client

**Pages:**
```
/
├── / (Dashboard overview)
├── /login
├── /rigs
│   ├── / (Rig list)
│   └── /[id] (Rig detail)
├── /flight-sheets
│   ├── / (List)
│   └── /create
├── /wallets
├── /pools
├── /oc-profiles
├── /alerts
└── /settings
```

### 4. Database (PostgreSQL)

**Purpose:** Persistent storage for all application data.

**Schema Overview:**
```
Users
  ├── ApiKeys
  └── Farms
        └── Rigs
              ├── GPUs
              ├── MinerInstances
              ├── RigStats (time-series)
              └── RigEvents (logs)

Wallets
Pools
MinerSoftware
FlightSheets
OCProfiles
AlertRules
```

See `packages/database/prisma/schema.prisma` for full schema.

### 5. Redis

**Purpose:** Caching and real-time features.

**Use Cases:**
- Session storage
- Rate limiting counters
- Pub/Sub for WebSocket scaling
- Command queue
- Temporary rig status cache

**Key Patterns:**
```
rig:{rigId}:status      # Current status (online/offline)
rig:{rigId}:stats       # Latest stats (expires 60s)
command:{commandId}     # Pending command (expires 120s)
rate:{ip}:{endpoint}    # Rate limit counter
session:{sessionId}     # User session
```

### 6. Caddy (Reverse Proxy)

**Purpose:** SSL termination, routing, and production deployment.

**Configuration:**
```caddyfile
bloxos.yourdomain.com {
    # Dashboard
    handle {
        reverse_proxy dashboard:3000
    }
}

api.bloxos.yourdomain.com {
    # API and WebSocket
    handle /socket.io/* {
        reverse_proxy api:3001
    }
    handle {
        reverse_proxy api:3001
    }
}
```

---

## Data Flow

### 1. Agent Registration

```
1. Admin creates rig entry in dashboard
2. Server generates unique token
3. Token displayed to admin
4. Admin installs agent on rig with token
5. Agent connects to server
6. Server validates token
7. Agent sends system info
8. Server updates rig record
9. Rig appears as "online" in dashboard
```

### 2. Stats Collection

```
1. Agent collects GPU stats (nvidia-smi)
2. Agent queries miner API
3. Agent sends stats via WebSocket
4. Server receives stats
5. Server updates database
6. Server updates Redis cache
7. Server broadcasts to dashboard clients
8. Dashboard updates UI
```

### 3. Command Execution

```
1. User clicks "Restart Miner" in dashboard
2. Dashboard sends POST /api/v1/rigs/:id/command
3. Server creates command record
4. Server sends command via WebSocket to agent
5. Agent receives command
6. Agent executes command
7. Agent sends result
8. Server updates command record
9. Server notifies dashboard
10. User sees success message
```

### 4. Alert Flow

```
1. Server receives stats from agent
2. Alert worker evaluates rules against stats
3. If condition met and not in cooldown:
   a. Create alert record
   b. Send Telegram message
   c. Send Discord message
   d. Broadcast to dashboard
4. Update cooldown timer
```

---

## Security

### Authentication Layers

1. **Agent → Server:**
   - Unique token per rig
   - Token validated on WebSocket connect
   - Token stored hashed in database

2. **User → API:**
   - JWT tokens (access + refresh)
   - Short-lived access tokens (15 min)
   - Long-lived refresh tokens (7 days)
   - Secure HTTP-only cookies

3. **API Keys:**
   - For external integrations
   - Hashed storage (bcrypt)
   - Prefix for identification
   - Revocable

### Input Validation

- All inputs validated with Zod schemas
- Parameterized queries (Prisma)
- No raw SQL execution
- HTML sanitization for user content

### Network Security

- HTTPS everywhere (Caddy auto-SSL)
- WebSocket over TLS (wss://)
- CORS configured for dashboard origin only
- Rate limiting on all endpoints

### Command Security

- Commands validated before execution
- Dangerous commands require confirmation
- Commands logged with user attribution
- No arbitrary shell execution

---

## Scaling Considerations

### Current Design (Small Scale)

- Single server, single database
- Up to ~100 rigs
- Up to ~10 concurrent users

### Future Scaling

1. **Horizontal API Scaling:**
   - Stateless API servers
   - Redis for session sharing
   - Load balancer (Caddy/Nginx)

2. **Database Scaling:**
   - Read replicas
   - Connection pooling (PgBouncer)
   - Time-series data archival

3. **WebSocket Scaling:**
   - Redis Pub/Sub for cross-instance messaging
   - Sticky sessions or Redis adapter

4. **Stats Optimization:**
   - TimescaleDB for time-series
   - Automatic data retention policies
   - Aggregation for historical data

---

## Directory Structure

```
bloxos/
├── apps/
│   ├── dashboard/              # Next.js frontend
│   │   ├── src/
│   │   │   ├── app/            # Pages (App Router)
│   │   │   ├── components/     # React components
│   │   │   │   ├── ui/         # Basic UI components
│   │   │   │   ├── rigs/       # Rig-related components
│   │   │   │   └── layout/     # Layout components
│   │   │   ├── hooks/          # Custom React hooks
│   │   │   ├── lib/            # Utilities
│   │   │   │   ├── api.ts      # API client
│   │   │   │   ├── socket.ts   # WebSocket client
│   │   │   │   └── utils.ts    # Helper functions
│   │   │   └── stores/         # Zustand stores
│   │   └── public/             # Static assets
│   │
│   ├── api/                    # Fastify backend
│   │   └── src/
│   │       ├── routes/         # API route handlers
│   │       │   ├── auth.ts
│   │       │   ├── rigs.ts
│   │       │   ├── farms.ts
│   │       │   └── ...
│   │       ├── services/       # Business logic
│   │       │   ├── rig.service.ts
│   │       │   ├── alert.service.ts
│   │       │   └── ...
│   │       ├── socket/         # WebSocket handlers
│   │       │   ├── agent.handler.ts
│   │       │   └── dashboard.handler.ts
│   │       ├── workers/        # Background jobs
│   │       │   └── alert.worker.ts
│   │       └── utils/          # Helpers
│   │
│   └── agent/                  # Go agent
│       ├── cmd/agent/          # Entry point
│       └── internal/
│           ├── collector/      # Data collection
│           │   ├── gpu.go
│           │   ├── system.go
│           │   └── miner.go
│           ├── executor/       # Command execution
│           ├── miner/          # Miner API clients
│           │   ├── trex.go
│           │   ├── teamred.go
│           │   └── ...
│           ├── websocket/      # Server communication
│           └── config/         # Configuration
│
├── packages/
│   ├── database/               # Prisma schema
│   ├── shared/                 # Shared types
│   └── ui/                     # Shared UI (optional)
│
└── docker/                     # Docker configs
```

---

## Technology Decisions

### Why Go for Agent?

- **Single binary:** No runtime dependencies
- **Cross-compilation:** Easy Linux/Windows builds
- **Performance:** Low resource usage
- **Concurrency:** Goroutines for parallel collection
- **System access:** Good stdlib for system calls

### Why Fastify over Express?

- **Performance:** Significantly faster
- **Schema validation:** Built-in JSON schema support
- **TypeScript:** Better type support
- **Plugins:** Clean plugin architecture
- **Logging:** Built-in Pino logger

### Why Next.js?

- **Server components:** Better performance
- **App Router:** Modern routing
- **TypeScript:** First-class support
- **Ecosystem:** Large community
- **Deployment:** Easy Vercel/Docker deploy

### Why PostgreSQL?

- **Reliability:** Battle-tested
- **Features:** JSON, arrays, full-text search
- **Prisma support:** Excellent ORM integration
- **Extensions:** TimescaleDB for future scaling

### Why Socket.io?

- **Reliability:** Auto-reconnection, fallbacks
- **Rooms:** Easy broadcasting
- **Scaling:** Redis adapter available
- **TypeScript:** Good type definitions

---

## Monitoring (Future)

### Metrics to Track

- **System:**
  - CPU usage
  - Memory usage
  - Disk usage
  - Network I/O

- **Application:**
  - Request latency
  - WebSocket connections
  - Database query time
  - Error rates

- **Business:**
  - Active rigs
  - Total hashrate
  - Alert frequency
  - Command success rate

### Tools (Optional)

- **Prometheus:** Metrics collection
- **Grafana:** Visualization
- **Loki:** Log aggregation
- **Sentry:** Error tracking

---

*Last Updated: January 4, 2026*
