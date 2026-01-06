# BloxOs

**Open-source mining rig management system**

A self-hosted alternative to HiveOS for managing cryptocurrency mining rigs. Monitor, control, and optimize your mining operation from a single dashboard.

---

## Features

- **Real-time Monitoring** - GPU temps, hashrates, power consumption
- **Remote Control** - Start/stop miners, reboot rigs, apply configs
- **Flight Sheets** - Preset configurations (wallet + pool + miner)
- **Overclocking** - GPU tuning profiles for NVIDIA and AMD
- **Alerts** - Telegram/Discord notifications for issues
- **Multi-User** - Organize rigs into farms with access control

---

## Quick Start

### Prerequisites

- Ubuntu 24.04 LTS (or similar Linux)
- 4+ GB RAM
- Docker installed

### 1. Clone Repository

```bash
git clone https://github.com/bokiko/bloxos.git
cd bloxos
```

### 2. Setup Environment

```bash
cp .env.example .env
# Edit .env with your settings
nano .env
```

### 3. Start Services

```bash
# Start databases
docker compose up -d postgres redis

# Push database schema
pnpm db:push

# Start API
pnpm --filter api dev

# Start Dashboard (new terminal)
pnpm --filter dashboard dev
```

### 4. Open Dashboard

Visit http://localhost:3000

---

## Installing the Agent

On your mining rig:

```bash
# Download agent
wget https://github.com/bokiko/bloxos/releases/latest/download/bloxos-agent-linux-amd64

# Make executable
chmod +x bloxos-agent-linux-amd64

# Run (get token from dashboard)
./bloxos-agent-linux-amd64 --server ws://YOUR_SERVER:3001 --token YOUR_RIG_TOKEN
```

---

## Development

### Project Structure

```
bloxos/
├── apps/
│   ├── dashboard/    # Next.js web UI
│   ├── api/          # Fastify API server
│   └── agent/        # Go agent for rigs
├── packages/
│   ├── database/     # Prisma schema
│   └── shared/       # Shared types
└── docker/           # Docker configs
```

### Commands

```bash
# Install dependencies
pnpm install

# Start all apps in development
pnpm dev

# Build all apps
pnpm build

# Database commands
pnpm db:push      # Push schema changes
pnpm db:studio    # Open Prisma Studio
pnpm db:migrate   # Run migrations

# Build agent
cd apps/agent
go build -o bloxos-agent ./cmd/agent
```

---

## Tech Stack

| Component | Technology |
|-----------|------------|
| Dashboard | Next.js 15, React, TailwindCSS |
| API | Node.js, Fastify, Socket.io |
| Agent | Go 1.22 |
| Database | PostgreSQL 16, Prisma |
| Cache | Redis |
| Monorepo | Turborepo, pnpm |

---

## Documentation

- [Architecture](docs/ARCHITECTURE.md) - System design
- [Development Phases](docs/PHASES.md) - Roadmap
- [API Reference](docs/API.md) - API documentation
- [Agent Protocol](docs/AGENT.md) - Agent communication

---

## Supported Hardware

### GPUs

| Vendor | Series | Support |
|--------|--------|---------|
| NVIDIA | GTX 10xx, 16xx, RTX 20xx, 30xx, 40xx | Full |
| AMD | RX 400/500, Vega, RX 5xxx, 6xxx, 7xxx | Full |

### Miners

- T-Rex (NVIDIA)
- TeamRedMiner (AMD)
- lolMiner (NVIDIA/AMD)
- NBMiner (NVIDIA/AMD)
- XMRig (CPU/GPU)
- PhoenixMiner (NVIDIA/AMD)
- GMiner (NVIDIA/AMD)

---

## Contributing

Contributions are welcome! Please read our contributing guidelines first.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing`)
5. Open a Pull Request

---

## License

MIT License - see [LICENSE](LICENSE) for details.

---

## Acknowledgments

- Inspired by [HiveOS](https://hiveos.farm)
- Built with help from the mining community

---

**BloxOs** - Take control of your mining operation.
