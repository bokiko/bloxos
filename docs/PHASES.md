# BloxOs Development Phases

> Incremental development plan with clear milestones and acceptance criteria

---

## Overview

Each phase must be **completed, tested, and approved** before moving to the next. This ensures stability and prevents scope creep.

| Phase | Name | Duration | Priority |
|-------|------|----------|----------|
| 0 | Foundation | 1-2 days | Critical |
| 1 | Agent MVP | 3-5 days | Critical |
| 2 | Dashboard MVP | 3-5 days | Critical |
| 3 | Miner Integration | 5-7 days | High |
| 4 | Flight Sheets | 3-5 days | High |
| 5 | Overclocking | 5-7 days | High |
| 6 | Alerts & Notifications | 3-5 days | Medium |
| 7 | Multi-User & Farms | 5-7 days | Medium |
| 8 | Production Ready | 5-7 days | Medium |

**Total Estimated Time:** 4-8 weeks

---

## Phase 0: Foundation

**Goal:** Set up development environment and verify all tools work together.

### Tasks

- [ ] **0.1** Set up VM with Ubuntu 24.04 LTS
  - Run `setup-vm.sh`
  - Verify all tools installed (Node, Go, Docker, PostgreSQL, Redis)
  
- [ ] **0.2** Initialize project structure
  - Run `init-project.sh`
  - Verify directory structure created
  
- [ ] **0.3** Start development databases
  ```bash
  cd docker
  docker compose -f docker-compose.yml -f docker-compose.dev.yml up -d
  ```
  - Verify PostgreSQL accessible on port 5432
  - Verify Redis accessible on port 6379
  
- [ ] **0.4** Push database schema
  ```bash
  pnpm db:push
  pnpm db:studio  # Open Prisma Studio to verify
  ```
  
- [ ] **0.5** Start API server
  ```bash
  pnpm --filter api dev
  ```
  - Verify http://localhost:3001/health returns `{"status":"ok"}`
  
- [ ] **0.6** Start Dashboard
  ```bash
  pnpm --filter dashboard dev
  ```
  - Verify http://localhost:3000 shows BloxOs page

### Acceptance Criteria

- [ ] All services start without errors
- [ ] Database tables created (visible in Prisma Studio)
- [ ] API responds to health check
- [ ] Dashboard renders in browser
- [ ] WebSocket connection established (check browser console)

### Deliverables

- Working development environment
- Empty but functional dashboard
- API with health endpoint
- Database with schema

---

## Phase 1: Agent MVP

**Goal:** Create a working agent that connects to the server and sends basic stats.

### Tasks

- [ ] **1.1** Agent WebSocket connection
  - Connect to API server
  - Handle reconnection on disconnect
  - Implement exponential backoff
  
- [ ] **1.2** Agent authentication
  - Send rig token on connect
  - Server validates token against database
  - Reject unauthorized connections
  
- [ ] **1.3** System info collection
  - Hostname, IP address
  - OS type and version
  - CPU count, memory
  
- [ ] **1.4** NVIDIA GPU detection
  - Parse `nvidia-smi` output
  - Collect: name, VRAM, temp, fan, power, clocks
  - Handle no GPUs gracefully
  
- [ ] **1.5** AMD GPU detection (optional for MVP)
  - Parse `rocm-smi` output
  - Same stats as NVIDIA
  
- [ ] **1.6** Heartbeat loop
  - Send heartbeat every 10 seconds
  - Include agent version, uptime
  
- [ ] **1.7** Stats collection loop
  - Collect GPU stats every 30 seconds
  - Send to server via WebSocket
  
- [ ] **1.8** API: Store rig data
  - Create/update rig record on connect
  - Store GPU data in database
  - Update `lastSeen` timestamp
  
- [ ] **1.9** API: Rig endpoints
  - `GET /api/v1/rigs` - List all rigs
  - `GET /api/v1/rigs/:id` - Get single rig with GPUs
  - `POST /api/v1/rigs` - Register new rig (get token)

### Acceptance Criteria

- [ ] Agent compiles to single binary
- [ ] Agent connects to server with token
- [ ] Server stores rig info in database
- [ ] GPU stats visible in Prisma Studio
- [ ] Rig shows as "online" when agent running
- [ ] Rig shows as "offline" when agent stopped (within 30s)

### Deliverables

- `bloxos-agent` binary (Linux amd64)
- API endpoints for rigs
- WebSocket message handlers
- Database records created

### Test Procedure

```bash
# On server
pnpm --filter api dev

# Create a rig token via API or database
# INSERT INTO "Rig" (id, name, hostname, token, "farmId") VALUES (...)

# On rig (or same machine for testing)
cd apps/agent
go build -o bloxos-agent ./cmd/agent
./bloxos-agent --server ws://localhost:3001 --token YOUR_TOKEN

# Verify in Prisma Studio that rig appears with GPU data
pnpm db:studio
```

---

## Phase 2: Dashboard MVP

**Goal:** Display connected rigs with real-time stats in the browser.

### Tasks

- [ ] **2.1** Dashboard layout
  - Header with logo, navigation
  - Sidebar (optional) or top nav
  - Main content area
  - Dark theme by default
  
- [ ] **2.2** Authentication (simple)
  - API key based for MVP
  - Store in localStorage
  - Protected routes
  
- [ ] **2.3** Rigs list page
  - Table/grid of all rigs
  - Show: name, status, GPUs count, total hashrate
  - Status indicator (online/offline/warning)
  - Click to view details
  
- [ ] **2.4** Rig detail page
  - System info section
  - GPU cards with all stats
  - Temperature gauges/bars
  - Power consumption
  
- [ ] **2.5** Real-time updates
  - Connect to WebSocket
  - Subscribe to rig updates
  - Update UI without page refresh
  
- [ ] **2.6** Stats visualization
  - Temperature bars (color coded)
  - Hashrate numbers
  - Power consumption
  
- [ ] **2.7** Responsive design
  - Works on desktop
  - Usable on tablet/mobile

### Acceptance Criteria

- [ ] Can log in with API key
- [ ] Rigs list shows all connected rigs
- [ ] Clicking rig shows detail page
- [ ] Stats update in real-time (no refresh)
- [ ] Offline rigs show correct status
- [ ] No console errors

### Deliverables

- Login page
- Rigs list page
- Rig detail page
- WebSocket integration
- Basic styling

---

## Phase 3: Miner Integration

**Goal:** Detect and control mining software running on rigs.

### Tasks

- [ ] **3.1** Process detection
  - Detect running miner processes
  - Match known miner names
  
- [ ] **3.2** Miner API clients
  - T-Rex HTTP API client
  - TeamRedMiner sgminer API client
  - XMRig API client
  - lolMiner API client
  
- [ ] **3.3** Parse miner stats
  - Hashrate (total and per-GPU)
  - Accepted/rejected shares
  - Pool connection status
  - Algorithm
  
- [ ] **3.4** Send miner stats to server
  - Include in stats message
  - Store in database
  
- [ ] **3.5** Display miner info in dashboard
  - Current miner name/version
  - Hashrate per GPU
  - Share statistics
  - Pool connected to
  
- [ ] **3.6** Miner control commands
  - Start miner
  - Stop miner
  - Restart miner
  
- [ ] **3.7** Command execution on agent
  - Receive command via WebSocket
  - Execute command
  - Report result

### Acceptance Criteria

- [ ] Running miner detected automatically
- [ ] Miner stats shown in dashboard
- [ ] Can start/stop miner from dashboard
- [ ] Commands execute within 5 seconds
- [ ] Error handling for failed commands

### Deliverables

- Miner detection module
- API clients for 4+ miners
- Command system
- UI controls

---

## Phase 4: Flight Sheets

**Goal:** Configure mining with presets (wallet + pool + miner).

### Tasks

- [ ] **4.1** Wallet management
  - CRUD for wallets
  - Coin selection
  - Address validation (basic)
  
- [ ] **4.2** Pool management
  - CRUD for pools
  - Pool URL templates
  - Common pools presets
  
- [ ] **4.3** Miner software registry
  - Store supported miners
  - Version management
  - Default arguments
  
- [ ] **4.4** Flight sheet creation
  - Combine wallet + pool + miner
  - Extra arguments field
  - Preview command line
  
- [ ] **4.5** Apply flight sheet
  - Select rig(s)
  - Send configuration
  - Agent applies and starts miner
  
- [ ] **4.6** Flight sheet UI
  - List view
  - Create/edit forms
  - Apply to rig modal

### Acceptance Criteria

- [ ] Can create wallet, pool, miner entries
- [ ] Can create flight sheet combining them
- [ ] Can apply flight sheet to rig
- [ ] Miner starts with correct config
- [ ] Configuration persists across agent restart

### Deliverables

- Wallet CRUD API + UI
- Pool CRUD API + UI
- Flight Sheet CRUD API + UI
- Apply flight sheet functionality

---

## Phase 5: Overclocking

**Goal:** Control GPU clock speeds, power limits, and fan speeds.

### Tasks

- [ ] **5.1** Read current OC settings
  - NVIDIA via nvidia-smi
  - AMD via rocm-smi
  
- [ ] **5.2** OC profile schema
  - Power limit
  - Core clock offset/lock
  - Memory clock offset/lock
  - Fan speed (or auto)
  
- [ ] **5.3** Apply OC on agent
  - Execute nvidia-smi commands
  - Execute rocm-smi commands
  - Handle errors gracefully
  
- [ ] **5.4** OC profile CRUD
  - Create profiles per vendor
  - Link to flight sheets
  
- [ ] **5.5** Per-GPU overrides
  - Different settings per GPU index
  - Inherit from profile with overrides
  
- [ ] **5.6** Persist OC
  - Apply on agent start
  - Apply after flight sheet change
  
- [ ] **5.7** OC UI
  - Profile editor
  - Visual sliders
  - Apply to rig

### Acceptance Criteria

- [ ] Can read current GPU settings
- [ ] Can apply OC profile
- [ ] Settings persist across miner restart
- [ ] Different profiles for NVIDIA/AMD
- [ ] Error shown if OC fails

### Deliverables

- OC profile management
- nvidia-smi/rocm-smi integration
- OC persistence
- UI for OC profiles

---

## Phase 6: Alerts & Notifications

**Goal:** Get notified when something goes wrong.

### Tasks

- [ ] **6.1** Alert rules system
  - Define conditions (temp > X, hashrate < Y)
  - Threshold values
  - Duration before firing
  
- [ ] **6.2** Alert evaluation
  - Check conditions on stats update
  - Track alert state (firing/resolved)
  - Cooldown period
  
- [ ] **6.3** Telegram integration
  - Bot setup instructions
  - Send messages via API
  - Format messages nicely
  
- [ ] **6.4** Discord integration
  - Webhook setup
  - Send embeds
  
- [ ] **6.5** Alert history
  - Store fired alerts
  - Show in dashboard
  
- [ ] **6.6** Alert UI
  - Create/edit rules
  - Test notifications
  - View history

### Acceptance Criteria

- [ ] Alert fires when condition met
- [ ] Telegram message received
- [ ] Discord message received
- [ ] Alert history shows past alerts
- [ ] Cooldown prevents spam

### Deliverables

- Alert rules engine
- Telegram bot integration
- Discord webhook integration
- Alert management UI

---

## Phase 7: Multi-User & Farms

**Goal:** Support multiple users with organized rig groups.

### Tasks

- [ ] **7.1** User authentication
  - Registration
  - Login (email/password)
  - JWT tokens
  
- [ ] **7.2** User roles
  - Admin: full access
  - User: manage own farms
  - Monitor: read-only
  
- [ ] **7.3** Farm organization
  - Create farms
  - Assign rigs to farms
  - Farm-level stats
  
- [ ] **7.4** Access control
  - Users own farms
  - Share farms with others
  - Permission levels
  
- [ ] **7.5** API key management
  - Generate API keys
  - Revoke keys
  - Key permissions
  
- [ ] **7.6** Activity logs
  - Track user actions
  - Track rig events
  - Audit trail

### Acceptance Criteria

- [ ] Users can register/login
- [ ] Users only see their farms
- [ ] Can share farm with other user
- [ ] API keys work for auth
- [ ] Activity log captures events

### Deliverables

- User authentication system
- Farm management
- Access control
- Activity logging

---

## Phase 8: Production Ready

**Goal:** Polish, secure, document, and prepare for public release.

### Tasks

- [ ] **8.1** Security audit
  - Input validation everywhere
  - SQL injection prevention
  - XSS prevention
  - Rate limiting
  
- [ ] **8.2** Performance optimization
  - Database indexes
  - Query optimization
  - Caching where needed
  
- [ ] **8.3** Error handling
  - Graceful degradation
  - User-friendly error messages
  - Error tracking (Sentry optional)
  
- [ ] **8.4** Documentation
  - Installation guide
  - User guide
  - API documentation
  - Agent deployment guide
  
- [ ] **8.5** One-line installer
  - Bash script for agent
  - Docker compose for server
  
- [ ] **8.6** Branding
  - Logo
  - Consistent styling
  - Landing page
  
- [ ] **8.7** Testing
  - Unit tests for critical paths
  - Integration tests
  - Load testing

### Acceptance Criteria

- [ ] Security checklist passed
- [ ] Documentation complete
- [ ] Can install with single command
- [ ] No known critical bugs
- [ ] Performance acceptable (100+ rigs)

### Deliverables

- Production-ready codebase
- Complete documentation
- Installer scripts
- Public release

---

## Notes

### Phase Dependencies

```
Phase 0 (Foundation)
    ↓
Phase 1 (Agent MVP)
    ↓
Phase 2 (Dashboard MVP)
    ↓
Phase 3 (Miner Integration) ──→ Phase 4 (Flight Sheets)
    ↓                               ↓
Phase 5 (Overclocking) ←───────────┘
    ↓
Phase 6 (Alerts)
    ↓
Phase 7 (Multi-User)
    ↓
Phase 8 (Production)
```

### Testing Each Phase

Before marking a phase complete:

1. **Functionality Test:** Does it do what it's supposed to?
2. **Error Test:** What happens when things go wrong?
3. **Edge Cases:** Empty states, max values, special characters
4. **UI Test:** Does it look right? Is it usable?
5. **Documentation:** Is it clear how to use this feature?

### Approval Process

1. Developer marks phase complete
2. Demo the functionality
3. Sir reviews and approves
4. Move to next phase

---

*Last Updated: January 4, 2026*
