# BloxOS

Fleet management dashboard for AI compute machines.

## Architecture

```
┌─────────────┐     WebSocket      ┌──────────┐     SSE      ┌────────────┐
│   Agent(s)  │ ──────────────────► │   Hub    │ ──────────► │  Dashboard │
│  (per node) │   metrics + pty    │ :4000    │  live data  │  Next.js   │
└─────────────┘                    └──────────┘             └────────────┘
     gopsutil                     Echo + SQLite            React + Tailwind
     go-nvml                      gorilla/ws                xterm.js
```

## Quick Start

```bash
# Hub
cd hub && go run .

# Agent (on each machine)
cd agent && go run . --hub ws://<hub-ip>:4000/ws/agent --token <token>

# Dashboard
cd dashboard && pnpm install && pnpm dev
```

## Project Structure

| Directory    | Purpose                           |
|-------------|-----------------------------------|
| `hub/`      | Go API server (Echo + WebSocket)  |
| `agent/`    | Go agent binary (metrics + pty)   |
| `dashboard/`| Next.js 15 frontend               |
| `proto/`    | Shared protocol definitions       |
| `scripts/`  | Install and deploy scripts        |
| `docs/`     | Plans and documentation           |

## License

Private.
