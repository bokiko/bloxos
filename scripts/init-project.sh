#!/bin/bash

#############################################
# BloxOs Project Initialization Script
# Run this after setup-vm.sh to scaffold the project
# 
# Usage: chmod +x init-project.sh && ./init-project.sh
#############################################

set -e

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

print_status() { echo -e "${BLUE}[*]${NC} $1"; }
print_success() { echo -e "${GREEN}[+]${NC} $1"; }
print_warning() { echo -e "${YELLOW}[!]${NC} $1"; }
print_error() { echo -e "${RED}[-]${NC} $1"; }

PROJECT_DIR="$HOME/projects/bloxos"

#############################################
# Check prerequisites
#############################################
print_status "Checking prerequisites..."

if ! command -v node &> /dev/null; then
    print_error "Node.js not found. Run setup-vm.sh first."
    exit 1
fi

if ! command -v pnpm &> /dev/null; then
    print_error "pnpm not found. Run setup-vm.sh first."
    exit 1
fi

if ! command -v go &> /dev/null; then
    print_error "Go not found. Run setup-vm.sh first."
    exit 1
fi

print_success "All prerequisites met"

#############################################
# Create directory structure
#############################################
print_status "Creating directory structure..."

cd "$PROJECT_DIR"

# Root directories
mkdir -p apps/dashboard/src/{app,components,lib,hooks}
mkdir -p apps/api/src/{routes,services,socket,utils}
mkdir -p apps/agent/cmd/agent
mkdir -p apps/agent/internal/{collector,executor,miner,websocket,config}

# Packages
mkdir -p packages/shared/src/{types,constants}
mkdir -p packages/database/prisma/migrations
mkdir -p packages/ui/src/components

# Other
mkdir -p docker
mkdir -p docs
mkdir -p scripts
mkdir -p .github/workflows

print_success "Directory structure created"

#############################################
# Create root package.json
#############################################
print_status "Creating root package.json..."

cat > package.json << 'EOF'
{
  "name": "bloxos",
  "version": "0.1.0",
  "private": true,
  "description": "Open-source mining rig management system",
  "repository": {
    "type": "git",
    "url": "https://github.com/bokiko/bloxos"
  },
  "author": "Bokiko",
  "license": "MIT",
  "scripts": {
    "dev": "turbo run dev",
    "build": "turbo run build",
    "start": "turbo run start",
    "lint": "turbo run lint",
    "test": "turbo run test",
    "clean": "turbo run clean && rm -rf node_modules",
    "db:push": "pnpm --filter database db:push",
    "db:migrate": "pnpm --filter database db:migrate",
    "db:studio": "pnpm --filter database db:studio",
    "db:generate": "pnpm --filter database db:generate"
  },
  "devDependencies": {
    "turbo": "^2.0.0",
    "typescript": "^5.4.0",
    "@types/node": "^20.0.0"
  },
  "packageManager": "pnpm@9.0.0",
  "engines": {
    "node": ">=22.0.0",
    "pnpm": ">=9.0.0"
  }
}
EOF

print_success "Root package.json created"

#############################################
# Create pnpm-workspace.yaml
#############################################
print_status "Creating pnpm workspace config..."

cat > pnpm-workspace.yaml << 'EOF'
packages:
  - 'apps/*'
  - 'packages/*'
EOF

print_success "pnpm-workspace.yaml created"

#############################################
# Create turbo.json
#############################################
print_status "Creating turbo.json..."

cat > turbo.json << 'EOF'
{
  "$schema": "https://turbo.build/schema.json",
  "globalDependencies": [".env"],
  "tasks": {
    "build": {
      "dependsOn": ["^build"],
      "outputs": [".next/**", "!.next/cache/**", "dist/**"]
    },
    "dev": {
      "cache": false,
      "persistent": true
    },
    "start": {
      "dependsOn": ["build"]
    },
    "lint": {},
    "test": {},
    "clean": {
      "cache": false
    },
    "db:push": {
      "cache": false
    },
    "db:migrate": {
      "cache": false
    },
    "db:generate": {
      "cache": false
    }
  }
}
EOF

print_success "turbo.json created"

#############################################
# Create .env.example
#############################################
print_status "Creating .env.example..."

cat > .env.example << 'EOF'
# ===========================================
# BloxOs Environment Configuration
# ===========================================
# Copy this file to .env and update values

# Database (PostgreSQL)
DATABASE_URL=postgresql://bloxos:bloxos_dev_password@localhost:5432/bloxos

# Redis
REDIS_URL=redis://localhost:6379

# API Server
API_PORT=3001
API_HOST=0.0.0.0
JWT_SECRET=change-this-to-a-secure-random-string
API_KEY_SALT=change-this-to-another-secure-string

# Dashboard
NEXT_PUBLIC_API_URL=http://localhost:3001
NEXT_PUBLIC_WS_URL=ws://localhost:3001

# Agent Configuration (set on mining rigs)
# BLOXOS_SERVER_URL=ws://your-server-ip:3001
# BLOXOS_RIG_TOKEN=generate-unique-token-per-rig

# Notifications (optional)
TELEGRAM_BOT_TOKEN=
TELEGRAM_CHAT_ID=
DISCORD_WEBHOOK_URL=

# Production
NODE_ENV=development
EOF

# Copy to .env for development
cp .env.example .env

print_success ".env.example created and copied to .env"

#############################################
# Create .gitignore
#############################################
print_status "Creating .gitignore..."

cat > .gitignore << 'EOF'
# Dependencies
node_modules/
.pnpm-store/

# Build outputs
.next/
dist/
build/
out/

# Environment files
.env
.env.local
.env.*.local
!.env.example

# IDE
.idea/
.vscode/
*.swp
*.swo

# OS
.DS_Store
Thumbs.db

# Logs
logs/
*.log
npm-debug.log*
pnpm-debug.log*

# Testing
coverage/
.nyc_output/

# Turbo
.turbo/

# Prisma
packages/database/prisma/*.db
packages/database/prisma/*.db-journal

# Go
apps/agent/bloxos-agent
apps/agent/bloxos-agent.exe
*.exe

# Docker
docker/data/

# Misc
*.bak
*.tmp
.cache/
EOF

print_success ".gitignore created"

#############################################
# Create packages/database/package.json
#############################################
print_status "Creating database package..."

cat > packages/database/package.json << 'EOF'
{
  "name": "@bloxos/database",
  "version": "0.1.0",
  "private": true,
  "main": "./src/index.ts",
  "types": "./src/index.ts",
  "scripts": {
    "db:generate": "prisma generate",
    "db:push": "prisma db push",
    "db:migrate": "prisma migrate dev",
    "db:studio": "prisma studio",
    "db:seed": "tsx prisma/seed.ts"
  },
  "dependencies": {
    "@prisma/client": "^5.15.0"
  },
  "devDependencies": {
    "prisma": "^5.15.0",
    "tsx": "^4.15.0"
  }
}
EOF

print_success "Database package.json created"

#############################################
# Create Prisma schema
#############################################
print_status "Creating Prisma schema..."

cat > packages/database/prisma/schema.prisma << 'EOF'
// BloxOs Database Schema
// Run: pnpm db:push to sync with database

generator client {
  provider = "prisma-client-js"
}

datasource db {
  provider = "postgresql"
  url      = env("DATABASE_URL")
}

// ===========================================
// Core Entities
// ===========================================

model User {
  id        String   @id @default(cuid())
  email     String   @unique
  name      String?
  password  String   // Hashed
  role      Role     @default(USER)
  apiKeys   ApiKey[]
  farms     Farm[]
  createdAt DateTime @default(now())
  updatedAt DateTime @updatedAt
}

enum Role {
  ADMIN
  USER
  MONITOR
}

model ApiKey {
  id        String   @id @default(cuid())
  name      String
  key       String   @unique // Hashed
  prefix    String   // First 8 chars for identification
  userId    String
  user      User     @relation(fields: [userId], references: [id], onDelete: Cascade)
  lastUsed  DateTime?
  expiresAt DateTime?
  createdAt DateTime @default(now())
}

model Farm {
  id          String   @id @default(cuid())
  name        String
  description String?
  ownerId     String
  owner       User     @relation(fields: [ownerId], references: [id], onDelete: Cascade)
  rigs        Rig[]
  createdAt   DateTime @default(now())
  updatedAt   DateTime @updatedAt
}

// ===========================================
// Rig & Hardware
// ===========================================

model Rig {
  id             String        @id @default(cuid())
  name           String
  hostname       String
  ipAddress      String?
  macAddress     String?
  os             String?
  osVersion      String?
  agentVersion   String?
  token          String        @unique // Auth token for agent
  status         RigStatus     @default(OFFLINE)
  lastSeen       DateTime?
  farmId         String
  farm           Farm          @relation(fields: [farmId], references: [id], onDelete: Cascade)
  flightSheetId  String?
  flightSheet    FlightSheet?  @relation(fields: [flightSheetId], references: [id])
  ocProfileId    String?
  ocProfile      OCProfile?    @relation(fields: [ocProfileId], references: [id])
  gpus           GPU[]
  minerInstances MinerInstance[]
  events         RigEvent[]
  stats          RigStats[]
  createdAt      DateTime      @default(now())
  updatedAt      DateTime      @updatedAt

  @@index([farmId])
  @@index([status])
}

enum RigStatus {
  ONLINE
  OFFLINE
  WARNING
  ERROR
  REBOOTING
}

model GPU {
  id           String   @id @default(cuid())
  index        Int      // GPU index on the rig
  name         String
  vendor       GPUVendor
  vram         Int      // MB
  busId        String?  // PCI bus ID
  uuid         String?  // GPU UUID
  rigId        String
  rig          Rig      @relation(fields: [rigId], references: [id], onDelete: Cascade)
  
  // Current stats (updated frequently)
  temperature  Int?     // Celsius
  memTemp      Int?     // Memory temp
  fanSpeed     Int?     // Percent
  powerDraw    Int?     // Watts
  coreClock    Int?     // MHz
  memoryClock  Int?     // MHz
  hashrate     Float?   // MH/s
  
  createdAt    DateTime @default(now())
  updatedAt    DateTime @updatedAt

  @@unique([rigId, index])
  @@index([rigId])
}

enum GPUVendor {
  NVIDIA
  AMD
  INTEL
}

// ===========================================
// Mining Configuration
// ===========================================

model Wallet {
  id           String        @id @default(cuid())
  name         String
  coin         String        // BTC, ETH, KAS, etc.
  address      String
  source       String?       // Exchange name or "personal"
  flightSheets FlightSheet[]
  createdAt    DateTime      @default(now())
  updatedAt    DateTime      @updatedAt

  @@index([coin])
}

model Pool {
  id           String        @id @default(cuid())
  name         String
  coin         String
  url          String        // stratum+tcp://pool:port
  url2         String?       // Backup
  url3         String?       // Backup 2
  user         String?       // Username template (use %WALLET%)
  pass         String?       // Usually "x"
  flightSheets FlightSheet[]
  createdAt    DateTime      @default(now())
  updatedAt    DateTime      @updatedAt

  @@index([coin])
}

model MinerSoftware {
  id             String        @id @default(cuid())
  name           String        // T-Rex, TeamRedMiner, etc.
  version        String
  algo           String        // ethash, kawpow, etc.
  supportedGpus  GPUVendor[]
  apiPort        Int
  apiType        String        // http, tcp
  installUrl     String?       // Download URL
  defaultArgs    String?       // Default command line args
  flightSheets   FlightSheet[]
  createdAt      DateTime      @default(now())
  updatedAt      DateTime      @updatedAt

  @@unique([name, version, algo])
}

model FlightSheet {
  id            String         @id @default(cuid())
  name          String
  coin          String
  walletId      String
  wallet        Wallet         @relation(fields: [walletId], references: [id])
  poolId        String
  pool          Pool           @relation(fields: [poolId], references: [id])
  minerId       String
  miner         MinerSoftware  @relation(fields: [minerId], references: [id])
  extraArgs     String?        // Additional miner arguments
  rigs          Rig[]
  createdAt     DateTime       @default(now())
  updatedAt     DateTime       @updatedAt
}

model OCProfile {
  id          String   @id @default(cuid())
  name        String
  vendor      GPUVendor
  
  // NVIDIA settings
  powerLimit  Int?     // Watts or percent
  coreOffset  Int?     // MHz offset
  memOffset   Int?     // MHz offset
  coreLock    Int?     // Lock core clock MHz
  memLock     Int?     // Lock mem clock MHz
  fanSpeed    Int?     // Percent (0 = auto)
  
  // AMD settings
  coreVddc    Int?     // Core voltage mV
  memVddc     Int?     // Memory voltage mV
  coreDpm     Int?     // Core DPM state
  memDpm      Int?     // Memory DPM state
  
  rigs        Rig[]
  createdAt   DateTime @default(now())
  updatedAt   DateTime @updatedAt
}

// ===========================================
// Miner Instances & Stats
// ===========================================

model MinerInstance {
  id          String   @id @default(cuid())
  rigId       String
  rig         Rig      @relation(fields: [rigId], references: [id], onDelete: Cascade)
  minerName   String   // T-Rex, etc.
  algo        String
  pool        String
  wallet      String
  status      MinerStatus @default(STOPPED)
  pid         Int?     // Process ID
  hashrate    Float?   // Total MH/s
  accepted    Int      @default(0)
  rejected    Int      @default(0)
  startedAt   DateTime?
  createdAt   DateTime @default(now())
  updatedAt   DateTime @updatedAt

  @@index([rigId])
}

enum MinerStatus {
  RUNNING
  STOPPED
  ERROR
  STARTING
}

model RigStats {
  id          String   @id @default(cuid())
  rigId       String
  rig         Rig      @relation(fields: [rigId], references: [id], onDelete: Cascade)
  hashrate    Float    // Total MH/s
  power       Int      // Total watts
  accepted    Int
  rejected    Int
  gpuData     Json     // Array of per-GPU stats
  timestamp   DateTime @default(now())

  @@index([rigId, timestamp])
}

// ===========================================
// Events & Alerts
// ===========================================

model RigEvent {
  id        String    @id @default(cuid())
  rigId     String
  rig       Rig       @relation(fields: [rigId], references: [id], onDelete: Cascade)
  type      EventType
  severity  Severity
  message   String
  data      Json?     // Additional event data
  timestamp DateTime  @default(now())

  @@index([rigId, timestamp])
  @@index([type])
}

enum EventType {
  RIG_ONLINE
  RIG_OFFLINE
  MINER_STARTED
  MINER_STOPPED
  MINER_ERROR
  GPU_TEMP_HIGH
  GPU_ERROR
  HASHRATE_DROP
  COMMAND_EXECUTED
  CONFIG_CHANGED
}

enum Severity {
  INFO
  WARNING
  ERROR
  CRITICAL
}

model AlertRule {
  id          String      @id @default(cuid())
  name        String
  enabled     Boolean     @default(true)
  condition   AlertCondition
  threshold   Float
  duration    Int?        // Seconds before triggering
  cooldown    Int         @default(300) // Seconds between alerts
  notify      String[]    // ["telegram", "discord", "email"]
  lastFired   DateTime?
  createdAt   DateTime    @default(now())
  updatedAt   DateTime    @updatedAt
}

enum AlertCondition {
  TEMP_ABOVE
  TEMP_BELOW
  HASHRATE_BELOW
  HASHRATE_DROP_PERCENT
  RIG_OFFLINE
  GPU_ERROR
  REJECTED_PERCENT
}
EOF

print_success "Prisma schema created"

#############################################
# Create packages/database/src/index.ts
#############################################
cat > packages/database/src/index.ts << 'EOF'
import { PrismaClient } from '@prisma/client';

const globalForPrisma = globalThis as unknown as {
  prisma: PrismaClient | undefined;
};

export const prisma =
  globalForPrisma.prisma ??
  new PrismaClient({
    log: process.env.NODE_ENV === 'development' ? ['query', 'error', 'warn'] : ['error'],
  });

if (process.env.NODE_ENV !== 'production') globalForPrisma.prisma = prisma;

export * from '@prisma/client';
EOF

print_success "Database client export created"

#############################################
# Create packages/shared/package.json
#############################################
print_status "Creating shared package..."

cat > packages/shared/package.json << 'EOF'
{
  "name": "@bloxos/shared",
  "version": "0.1.0",
  "private": true,
  "main": "./src/index.ts",
  "types": "./src/index.ts",
  "scripts": {
    "lint": "eslint src/",
    "typecheck": "tsc --noEmit"
  },
  "devDependencies": {
    "typescript": "^5.4.0"
  }
}
EOF

#############################################
# Create shared types
#############################################
cat > packages/shared/src/types/index.ts << 'EOF'
// ===========================================
// BloxOs Shared Types
// ===========================================

// Rig types
export interface RigInfo {
  id: string;
  name: string;
  hostname: string;
  ipAddress: string;
  os: string;
  osVersion: string;
  agentVersion: string;
  status: RigStatus;
  lastSeen: Date;
  gpus: GPUInfo[];
  miners: MinerInfo[];
}

export type RigStatus = 'online' | 'offline' | 'warning' | 'error' | 'rebooting';

// GPU types
export interface GPUInfo {
  index: number;
  name: string;
  vendor: 'nvidia' | 'amd' | 'intel';
  vram: number;
  temperature: number;
  memTemp?: number;
  fanSpeed: number;
  powerDraw: number;
  coreClock: number;
  memoryClock: number;
  hashrate?: number;
}

// Miner types
export interface MinerInfo {
  name: string;
  algo: string;
  pool: string;
  status: MinerStatus;
  hashrate: number;
  accepted: number;
  rejected: number;
  uptime: number;
}

export type MinerStatus = 'running' | 'stopped' | 'error' | 'starting';

// WebSocket message types
export type WSMessageType =
  | 'heartbeat'
  | 'stats'
  | 'event'
  | 'command'
  | 'config'
  | 'rig_update'
  | 'alert';

export interface WSMessage<T = unknown> {
  type: WSMessageType;
  payload: T;
  timestamp: number;
}

// Agent -> Server messages
export interface HeartbeatMessage {
  rigId: string;
  agentVersion: string;
}

export interface StatsMessage {
  rigId: string;
  gpus: GPUInfo[];
  miners: MinerInfo[];
  uptime: number;
  loadAvg: number[];
  memUsed: number;
  memTotal: number;
}

export interface EventMessage {
  rigId: string;
  event: EventType;
  severity: 'info' | 'warning' | 'error' | 'critical';
  message: string;
  data?: Record<string, unknown>;
}

export type EventType =
  | 'rig_online'
  | 'rig_offline'
  | 'miner_started'
  | 'miner_stopped'
  | 'miner_error'
  | 'gpu_temp_high'
  | 'gpu_error'
  | 'hashrate_drop'
  | 'command_executed'
  | 'config_changed';

// Server -> Agent messages
export interface CommandMessage {
  id: string;
  action: CommandAction;
  payload?: Record<string, unknown>;
}

export type CommandAction =
  | 'reboot'
  | 'shutdown'
  | 'exec'
  | 'miner_start'
  | 'miner_stop'
  | 'miner_restart'
  | 'apply_flight_sheet'
  | 'apply_oc'
  | 'update_agent';

export interface ConfigMessage {
  flightSheet?: FlightSheetConfig;
  ocProfile?: OCProfileConfig;
}

export interface FlightSheetConfig {
  coin: string;
  wallet: string;
  pool: string;
  poolBackup?: string;
  miner: string;
  algo: string;
  extraArgs?: string;
}

export interface OCProfileConfig {
  vendor: 'nvidia' | 'amd';
  powerLimit?: number;
  coreOffset?: number;
  memOffset?: number;
  coreLock?: number;
  memLock?: number;
  fanSpeed?: number;
}

// API Response types
export interface ApiResponse<T = unknown> {
  success: boolean;
  data?: T;
  error?: string;
  message?: string;
}

export interface PaginatedResponse<T> {
  items: T[];
  total: number;
  page: number;
  pageSize: number;
  totalPages: number;
}
EOF

cat > packages/shared/src/constants/index.ts << 'EOF'
// ===========================================
// BloxOs Constants
// ===========================================

export const AGENT_VERSION = '0.1.0';
export const API_VERSION = 'v1';

// Timing
export const HEARTBEAT_INTERVAL_MS = 10000; // 10 seconds
export const STATS_INTERVAL_MS = 30000; // 30 seconds
export const RECONNECT_DELAY_MS = 5000; // 5 seconds
export const COMMAND_TIMEOUT_MS = 60000; // 60 seconds

// Thresholds
export const DEFAULT_TEMP_WARNING = 75; // Celsius
export const DEFAULT_TEMP_CRITICAL = 85;
export const DEFAULT_HASHRATE_DROP_PERCENT = 20;

// Miner API ports
export const MINER_PORTS: Record<string, number> = {
  'trex': 4067,
  'teamredminer': 4028,
  'xmrig': 8080,
  'lolminer': 10400,
  'nbminer': 22333,
  'phoenixminer': 3333,
  'gminer': 3333,
  'bzminer': 4014,
};

// Supported miners
export const SUPPORTED_MINERS = [
  'trex',
  'teamredminer',
  'xmrig',
  'lolminer',
  'nbminer',
  'phoenixminer',
  'gminer',
  'bzminer',
] as const;

export type SupportedMiner = typeof SUPPORTED_MINERS[number];

// Supported algorithms
export const ALGORITHMS = [
  'ethash',
  'etchash',
  'kawpow',
  'autolykos2',
  'kheavyhash',
  'blake3',
  'randomx',
  'octopus',
  'firopow',
] as const;

export type Algorithm = typeof ALGORITHMS[number];
EOF

cat > packages/shared/src/index.ts << 'EOF'
export * from './types';
export * from './constants';
EOF

print_success "Shared package created"

#############################################
# Create apps/api package.json
#############################################
print_status "Creating API package..."

cat > apps/api/package.json << 'EOF'
{
  "name": "@bloxos/api",
  "version": "0.1.0",
  "private": true,
  "scripts": {
    "dev": "tsx watch src/index.ts",
    "build": "tsc",
    "start": "node dist/index.js",
    "lint": "eslint src/"
  },
  "dependencies": {
    "@bloxos/database": "workspace:*",
    "@bloxos/shared": "workspace:*",
    "fastify": "^4.28.0",
    "@fastify/cors": "^9.0.0",
    "@fastify/jwt": "^8.0.0",
    "@fastify/websocket": "^10.0.0",
    "socket.io": "^4.7.0",
    "zod": "^3.23.0",
    "bcryptjs": "^2.4.3",
    "ioredis": "^5.4.0",
    "dotenv": "^16.4.0"
  },
  "devDependencies": {
    "@types/bcryptjs": "^2.4.6",
    "@types/node": "^20.0.0",
    "tsx": "^4.15.0",
    "typescript": "^5.4.0"
  }
}
EOF

# Create basic API entry point
cat > apps/api/src/index.ts << 'EOF'
import 'dotenv/config';
import Fastify from 'fastify';
import cors from '@fastify/cors';
import { Server } from 'socket.io';

const PORT = parseInt(process.env.API_PORT || '3001');
const HOST = process.env.API_HOST || '0.0.0.0';

const fastify = Fastify({
  logger: true,
});

// Register plugins
fastify.register(cors, {
  origin: true,
  credentials: true,
});

// Health check
fastify.get('/health', async () => {
  return { status: 'ok', timestamp: new Date().toISOString() };
});

// API info
fastify.get('/api/v1', async () => {
  return {
    name: 'BloxOs API',
    version: '0.1.0',
    docs: '/api/v1/docs',
  };
});

// Start server
const start = async () => {
  try {
    // Create HTTP server
    await fastify.listen({ port: PORT, host: HOST });
    
    // Setup Socket.IO
    const io = new Server(fastify.server, {
      cors: {
        origin: '*',
        methods: ['GET', 'POST'],
      },
    });

    io.on('connection', (socket) => {
      console.log(`Client connected: ${socket.id}`);

      socket.on('disconnect', () => {
        console.log(`Client disconnected: ${socket.id}`);
      });

      // Agent heartbeat
      socket.on('heartbeat', (data) => {
        console.log('Heartbeat received:', data);
        socket.emit('heartbeat_ack', { timestamp: Date.now() });
      });

      // Agent stats
      socket.on('stats', (data) => {
        console.log('Stats received:', data);
        // TODO: Store in database, broadcast to dashboard
      });
    });

    console.log(`Server listening on http://${HOST}:${PORT}`);
    console.log(`WebSocket ready on ws://${HOST}:${PORT}`);
  } catch (err) {
    fastify.log.error(err);
    process.exit(1);
  }
};

start();
EOF

cat > apps/api/tsconfig.json << 'EOF'
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "NodeNext",
    "moduleResolution": "NodeNext",
    "lib": ["ES2022"],
    "outDir": "./dist",
    "rootDir": "./src",
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "forceConsistentCasingInFileNames": true,
    "resolveJsonModule": true,
    "declaration": true,
    "declarationMap": true,
    "sourceMap": true
  },
  "include": ["src/**/*"],
  "exclude": ["node_modules", "dist"]
}
EOF

print_success "API package created"

#############################################
# Create apps/dashboard package.json
#############################################
print_status "Creating Dashboard package..."

cat > apps/dashboard/package.json << 'EOF'
{
  "name": "@bloxos/dashboard",
  "version": "0.1.0",
  "private": true,
  "scripts": {
    "dev": "next dev --port 3000",
    "build": "next build",
    "start": "next start",
    "lint": "next lint"
  },
  "dependencies": {
    "@bloxos/shared": "workspace:*",
    "next": "^15.0.0",
    "react": "^19.0.0",
    "react-dom": "^19.0.0",
    "socket.io-client": "^4.7.0",
    "zustand": "^4.5.0",
    "recharts": "^2.12.0",
    "lucide-react": "^0.400.0",
    "clsx": "^2.1.0",
    "tailwind-merge": "^2.3.0"
  },
  "devDependencies": {
    "@types/node": "^20.0.0",
    "@types/react": "^18.3.0",
    "@types/react-dom": "^18.3.0",
    "autoprefixer": "^10.4.0",
    "postcss": "^8.4.0",
    "tailwindcss": "^3.4.0",
    "typescript": "^5.4.0"
  }
}
EOF

# Create Next.js config
cat > apps/dashboard/next.config.ts << 'EOF'
import type { NextConfig } from 'next';

const nextConfig: NextConfig = {
  reactStrictMode: true,
  transpilePackages: ['@bloxos/shared'],
};

export default nextConfig;
EOF

# Create Tailwind config
cat > apps/dashboard/tailwind.config.ts << 'EOF'
import type { Config } from 'tailwindcss';

const config: Config = {
  content: [
    './src/**/*.{js,ts,jsx,tsx,mdx}',
  ],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        primary: {
          50: '#f0fdf4',
          100: '#dcfce7',
          200: '#bbf7d0',
          300: '#86efac',
          400: '#4ade80',
          500: '#22c55e',
          600: '#16a34a',
          700: '#15803d',
          800: '#166534',
          900: '#14532d',
        },
      },
    },
  },
  plugins: [],
};

export default config;
EOF

# Create PostCSS config
cat > apps/dashboard/postcss.config.js << 'EOF'
module.exports = {
  plugins: {
    tailwindcss: {},
    autoprefixer: {},
  },
};
EOF

# Create tsconfig
cat > apps/dashboard/tsconfig.json << 'EOF'
{
  "compilerOptions": {
    "target": "ES2017",
    "lib": ["dom", "dom.iterable", "esnext"],
    "allowJs": true,
    "skipLibCheck": true,
    "strict": true,
    "noEmit": true,
    "esModuleInterop": true,
    "module": "esnext",
    "moduleResolution": "bundler",
    "resolveJsonModule": true,
    "isolatedModules": true,
    "jsx": "preserve",
    "incremental": true,
    "plugins": [{ "name": "next" }],
    "paths": {
      "@/*": ["./src/*"]
    }
  },
  "include": ["next-env.d.ts", "**/*.ts", "**/*.tsx", ".next/types/**/*.ts"],
  "exclude": ["node_modules"]
}
EOF

# Create basic layout and page
mkdir -p apps/dashboard/src/app

cat > apps/dashboard/src/app/globals.css << 'EOF'
@tailwind base;
@tailwind components;
@tailwind utilities;

:root {
  --background: #0a0a0a;
  --foreground: #ededed;
}

body {
  background: var(--background);
  color: var(--foreground);
  font-family: system-ui, -apple-system, sans-serif;
}
EOF

cat > apps/dashboard/src/app/layout.tsx << 'EOF'
import type { Metadata } from 'next';
import './globals.css';

export const metadata: Metadata = {
  title: 'BloxOs - Mining Rig Management',
  description: 'Open-source mining rig management system',
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en" className="dark">
      <body className="min-h-screen bg-zinc-950 text-zinc-100">
        {children}
      </body>
    </html>
  );
}
EOF

cat > apps/dashboard/src/app/page.tsx << 'EOF'
export default function Home() {
  return (
    <main className="min-h-screen p-8">
      <div className="max-w-6xl mx-auto">
        <header className="mb-8">
          <h1 className="text-4xl font-bold text-green-500">BloxOs</h1>
          <p className="text-zinc-400 mt-2">
            Open-source mining rig management system
          </p>
        </header>

        <div className="grid gap-6 md:grid-cols-2 lg:grid-cols-3">
          {/* Stats cards placeholder */}
          <div className="bg-zinc-900 border border-zinc-800 rounded-lg p-6">
            <h3 className="text-zinc-400 text-sm font-medium">Total Rigs</h3>
            <p className="text-3xl font-bold mt-2">0</p>
          </div>
          
          <div className="bg-zinc-900 border border-zinc-800 rounded-lg p-6">
            <h3 className="text-zinc-400 text-sm font-medium">Online</h3>
            <p className="text-3xl font-bold mt-2 text-green-500">0</p>
          </div>
          
          <div className="bg-zinc-900 border border-zinc-800 rounded-lg p-6">
            <h3 className="text-zinc-400 text-sm font-medium">Total Hashrate</h3>
            <p className="text-3xl font-bold mt-2">0 MH/s</p>
          </div>
        </div>

        <div className="mt-8 bg-zinc-900 border border-zinc-800 rounded-lg p-6">
          <h2 className="text-xl font-semibold mb-4">Rigs</h2>
          <p className="text-zinc-500">No rigs connected yet.</p>
          <p className="text-zinc-600 text-sm mt-2">
            Install the BloxOs agent on your mining rig to get started.
          </p>
        </div>
      </div>
    </main>
  );
}
EOF

print_success "Dashboard package created"

#############################################
# Create Go agent skeleton
#############################################
print_status "Creating Agent skeleton..."

cat > apps/agent/go.mod << 'EOF'
module github.com/bokiko/bloxos/agent

go 1.22

require (
	github.com/gorilla/websocket v1.5.1
	github.com/spf13/cobra v1.8.0
	gopkg.in/yaml.v3 v3.0.1
)
EOF

cat > apps/agent/cmd/agent/main.go << 'EOF'
package main

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

var (
	version   = "0.1.0"
	serverURL string
	rigToken  string
)

func main() {
	rootCmd := &cobra.Command{
		Use:   "bloxos-agent",
		Short: "BloxOs Mining Rig Agent",
		Long:  `BloxOs agent collects stats and receives commands from the BloxOs server.`,
		Run:   runAgent,
	}

	rootCmd.Flags().StringVarP(&serverURL, "server", "s", "", "BloxOs server URL (ws://host:port)")
	rootCmd.Flags().StringVarP(&rigToken, "token", "t", "", "Rig authentication token")
	rootCmd.MarkFlagRequired("server")
	rootCmd.MarkFlagRequired("token")

	versionCmd := &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("BloxOs Agent v%s\n", version)
		},
	}
	rootCmd.AddCommand(versionCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runAgent(cmd *cobra.Command, args []string) {
	fmt.Printf("BloxOs Agent v%s starting...\n", version)
	fmt.Printf("Server: %s\n", serverURL)

	// TODO: Initialize collectors
	// TODO: Connect to WebSocket
	// TODO: Start heartbeat loop
	// TODO: Start stats collection loop

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	fmt.Println("Agent running. Press Ctrl+C to stop.")
	<-sigCh
	fmt.Println("\nShutting down...")
}
EOF

# Create collector skeleton
cat > apps/agent/internal/collector/gpu.go << 'EOF'
package collector

import (
	"os/exec"
	"strings"
)

// GPUInfo holds information about a single GPU
type GPUInfo struct {
	Index       int     `json:"index"`
	Name        string  `json:"name"`
	Vendor      string  `json:"vendor"`
	VRAM        int     `json:"vram"`
	Temperature int     `json:"temperature"`
	FanSpeed    int     `json:"fanSpeed"`
	PowerDraw   int     `json:"powerDraw"`
	CoreClock   int     `json:"coreClock"`
	MemoryClock int     `json:"memoryClock"`
	Hashrate    float64 `json:"hashrate,omitempty"`
}

// CollectNvidiaGPUs collects stats from NVIDIA GPUs using nvidia-smi
func CollectNvidiaGPUs() ([]GPUInfo, error) {
	// Check if nvidia-smi exists
	_, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return nil, nil // No NVIDIA GPUs
	}

	// Query GPU stats
	cmd := exec.Command("nvidia-smi",
		"--query-gpu=index,name,memory.total,temperature.gpu,fan.speed,power.draw,clocks.gr,clocks.mem",
		"--format=csv,noheader,nounits")

	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var gpus []GPUInfo
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")

	for _, line := range lines {
		if line == "" {
			continue
		}
		// TODO: Parse CSV line into GPUInfo
		// This is a skeleton - full implementation needed
	}

	return gpus, nil
}

// CollectAMDGPUs collects stats from AMD GPUs using rocm-smi
func CollectAMDGPUs() ([]GPUInfo, error) {
	// Check if rocm-smi exists
	_, err := exec.LookPath("rocm-smi")
	if err != nil {
		return nil, nil // No AMD GPUs with ROCm
	}

	// TODO: Implement AMD GPU collection
	return nil, nil
}
EOF

cat > apps/agent/internal/collector/system.go << 'EOF'
package collector

import (
	"os"
	"runtime"
)

// SystemInfo holds system information
type SystemInfo struct {
	Hostname  string   `json:"hostname"`
	OS        string   `json:"os"`
	Arch      string   `json:"arch"`
	CPUs      int      `json:"cpus"`
	MemTotal  uint64   `json:"memTotal"`
	MemUsed   uint64   `json:"memUsed"`
	LoadAvg   []float64 `json:"loadAvg"`
}

// CollectSystemInfo gathers basic system information
func CollectSystemInfo() (*SystemInfo, error) {
	hostname, _ := os.Hostname()

	return &SystemInfo{
		Hostname: hostname,
		OS:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		CPUs:     runtime.NumCPU(),
		// TODO: Add memory and load average collection
	}, nil
}
EOF

print_success "Agent skeleton created"

#############################################
# Create Docker Compose
#############################################
print_status "Creating Docker configuration..."

cat > docker/docker-compose.yml << 'EOF'
version: '3.8'

services:
  # PostgreSQL Database
  postgres:
    image: postgres:16-alpine
    container_name: bloxos-postgres
    restart: unless-stopped
    environment:
      POSTGRES_USER: bloxos
      POSTGRES_PASSWORD: bloxos_dev_password
      POSTGRES_DB: bloxos
    volumes:
      - postgres_data:/var/lib/postgresql/data
    ports:
      - "5432:5432"
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U bloxos"]
      interval: 10s
      timeout: 5s
      retries: 5

  # Redis Cache
  redis:
    image: redis:7-alpine
    container_name: bloxos-redis
    restart: unless-stopped
    volumes:
      - redis_data:/data
    ports:
      - "6379:6379"
    healthcheck:
      test: ["CMD", "redis-cli", "ping"]
      interval: 10s
      timeout: 5s
      retries: 5

  # API Server
  api:
    build:
      context: ..
      dockerfile: docker/Dockerfile.api
    container_name: bloxos-api
    restart: unless-stopped
    environment:
      DATABASE_URL: postgresql://bloxos:bloxos_dev_password@postgres:5432/bloxos
      REDIS_URL: redis://redis:6379
      API_PORT: 3001
      API_HOST: 0.0.0.0
      JWT_SECRET: change-in-production
    ports:
      - "3001:3001"
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy

  # Dashboard
  dashboard:
    build:
      context: ..
      dockerfile: docker/Dockerfile.dashboard
    container_name: bloxos-dashboard
    restart: unless-stopped
    environment:
      NEXT_PUBLIC_API_URL: http://localhost:3001
      NEXT_PUBLIC_WS_URL: ws://localhost:3001
    ports:
      - "3000:3000"
    depends_on:
      - api

volumes:
  postgres_data:
  redis_data:
EOF

cat > docker/docker-compose.dev.yml << 'EOF'
version: '3.8'

# Development override - only runs databases
# Use this with: docker compose -f docker-compose.yml -f docker-compose.dev.yml up

services:
  api:
    profiles:
      - disabled
  
  dashboard:
    profiles:
      - disabled
EOF

cat > docker/Dockerfile.api << 'EOF'
FROM node:22-alpine AS builder

WORKDIR /app

# Install pnpm
RUN corepack enable && corepack prepare pnpm@9.0.0 --activate

# Copy workspace files
COPY package.json pnpm-workspace.yaml turbo.json ./
COPY apps/api/package.json ./apps/api/
COPY packages/shared/package.json ./packages/shared/
COPY packages/database/package.json ./packages/database/

# Install dependencies
RUN pnpm install --frozen-lockfile

# Copy source
COPY apps/api ./apps/api
COPY packages/shared ./packages/shared
COPY packages/database ./packages/database

# Generate Prisma client
RUN pnpm --filter database db:generate

# Build
RUN pnpm --filter api build

# Production stage
FROM node:22-alpine

WORKDIR /app

RUN corepack enable && corepack prepare pnpm@9.0.0 --activate

COPY --from=builder /app/node_modules ./node_modules
COPY --from=builder /app/apps/api/dist ./dist
COPY --from=builder /app/apps/api/package.json ./

EXPOSE 3001

CMD ["node", "dist/index.js"]
EOF

cat > docker/Dockerfile.dashboard << 'EOF'
FROM node:22-alpine AS builder

WORKDIR /app

RUN corepack enable && corepack prepare pnpm@9.0.0 --activate

COPY package.json pnpm-workspace.yaml turbo.json ./
COPY apps/dashboard/package.json ./apps/dashboard/
COPY packages/shared/package.json ./packages/shared/

RUN pnpm install --frozen-lockfile

COPY apps/dashboard ./apps/dashboard
COPY packages/shared ./packages/shared

RUN pnpm --filter dashboard build

# Production stage
FROM node:22-alpine

WORKDIR /app

RUN corepack enable && corepack prepare pnpm@9.0.0 --activate

COPY --from=builder /app/apps/dashboard/.next/standalone ./
COPY --from=builder /app/apps/dashboard/.next/static ./.next/static
COPY --from=builder /app/apps/dashboard/public ./public

EXPOSE 3000

CMD ["node", "server.js"]
EOF

print_success "Docker configuration created"

#############################################
# Initialize Git repository
#############################################
print_status "Initializing Git repository..."

git init
git add .
git commit -m "Initial commit: BloxOs project structure"

print_success "Git repository initialized"

#############################################
# Install dependencies
#############################################
print_status "Installing dependencies..."

# Source updated PATH for pnpm
export PNPM_HOME="$HOME/.local/share/pnpm"
export PATH="$PNPM_HOME:$PATH"

pnpm install

print_success "Dependencies installed"

#############################################
# Summary
#############################################
echo ""
echo "============================================"
echo -e "${GREEN}BloxOs Project Initialized!${NC}"
echo "============================================"
echo ""
echo "Project structure created at: $PROJECT_DIR"
echo ""
echo "Next steps:"
echo "  1. cd $PROJECT_DIR"
echo "  2. Start databases: docker compose -f docker/docker-compose.yml -f docker/docker-compose.dev.yml up -d"
echo "  3. Push database schema: pnpm db:push"
echo "  4. Start API: pnpm --filter api dev"
echo "  5. Start Dashboard: pnpm --filter dashboard dev"
echo ""
echo "URLs:"
echo "  - Dashboard: http://localhost:3000"
echo "  - API: http://localhost:3001"
echo "  - API Health: http://localhost:3001/health"
echo ""
echo "Happy mining!"
echo ""
