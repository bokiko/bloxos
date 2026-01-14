<div align="center">

# BloxOS

<strong>Free, self-hosted mining rig management - your HiveOS alternative</strong>

<p>
  <a href="https://github.com/bokiko/bloxos"><img src="https://img.shields.io/badge/GitHub-bloxos-181717?style=for-the-badge&logo=github" alt="GitHub"></a>
</p>

<p>
  <img src="https://img.shields.io/badge/Status-Alpha-orange?style=flat-square" alt="Status">
  <img src="https://img.shields.io/badge/TypeScript-007ACC?style=flat-square&logo=typescript&logoColor=white" alt="TypeScript">
  <img src="https://img.shields.io/badge/Next.js-black?style=flat-square&logo=next.js" alt="Next.js">
  <img src="https://img.shields.io/badge/Go-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/Docker-Ready-2496ED?style=flat-square&logo=docker&logoColor=white" alt="Docker">
  <img src="https://img.shields.io/badge/License-MIT-green?style=flat-square" alt="License">
</p>

</div>

---

## Table of Contents

- [What is BloxOS?](#what-is-bloxos)
- [Features](#features)
- [Quick Start](#quick-start)
- [Adding Your First Rig](#adding-your-first-rig)
- [Setting Up Mining](#setting-up-mining)
- [Supported Coins](#supported-coins)
- [System Requirements](#system-requirements)
- [Troubleshooting](#troubleshooting)
- [Updating](#updating)
- [Tech Stack](#tech-stack)
- [Project Structure](#project-structure)
- [Roadmap](#roadmap)
- [Contributing](#contributing)
- [For Developers](#for-developers)
- [License](#license)

---

<details>
<summary><h2>What is BloxOS?</h2></summary>

BloxOS lets you **manage all your mining rigs from one place** - just like HiveOS, but completely free and running on your own computer.

| BloxOS | HiveOS |
|--------|--------|
| Free forever | $3/month per rig |
| Your server, your data | Their servers |
| Unlimited rigs | Extra fees for more rigs |
| Open source | Closed source |

</details>

---

<details>
<summary><h2>Features</h2></summary>

<table>
<tr>
<td width="50%">

### Monitor Your Rigs
- See all your rigs on one dashboard
- Watch GPU temperatures in real-time
- Track hashrates and power usage
- Get alerts when something goes wrong

</td>
<td width="50%">

### Control Mining
- Start and stop miners remotely
- Change what coin you're mining
- Adjust GPU settings (overclocking)
- Reboot rigs without physical access

</td>
</tr>
</table>

</details>

---

<details open>
<summary><h2>Quick Start</h2></summary>

### What You Need

**BloxOS Server (the dashboard)** - runs on **any OS**:
- Windows, Mac, or Linux - whatever you have
- Can be an old PC, laptop, or even Raspberry Pi

**Your Mining Rigs** - must run **Linux**:
- [Ubuntu](https://ubuntu.com/download/desktop), HiveOS, or any Linux distro
- Windows is NOT supported on rigs (no GPU monitoring support)

**Other requirements:**
- Mining rigs connected to the same network as the server
- Basic ability to copy/paste commands

### Step 1: Install Docker

Docker is a program that makes installing BloxOS super easy.

**Windows:**
1. Download [Docker Desktop](https://www.docker.com/products/docker-desktop/)
2. Run the installer and follow the prompts
3. Restart your computer when asked

**Mac:**
1. Download [Docker Desktop for Mac](https://www.docker.com/products/docker-desktop/)
2. Drag to Applications folder
3. Open Docker from Applications

**Linux (Ubuntu/Debian):**
```bash
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER
```
Then log out and log back in.

### Step 2: Download BloxOS

**Option A: Using Git** (if you have Git installed)
```bash
git clone https://github.com/bokiko/bloxos.git
cd bloxos
```

**Option B: Download ZIP** (no Git needed)
1. Go to https://github.com/bokiko/bloxos
2. Click the green **"Code"** button → **"Download ZIP"**
3. Extract the ZIP file:
   - **Windows/Mac:** Double-click the ZIP file
   - **Linux:** `unzip bloxos-main.zip` (install with `sudo apt install unzip` if needed)
4. Open a terminal and navigate to the extracted folder:
   ```bash
   cd Downloads/bloxos-main
   ```

### Step 3: Configure Environment

**Mac/Linux:**
```bash
cp .env.example .env
```

**Windows (Command Prompt):**
```cmd
copy .env.example .env
```

Now open the `.env` file to review settings:

**Windows (choose one):**
- Open File Explorer, go to the `bloxos` folder, right-click `.env` → "Open with" → Notepad
- Or in Command Prompt: `notepad .env`

**Mac:**
- In Terminal: `open -e .env` (opens in TextEdit)

**Linux:**
- In Terminal: `nano .env` (press Ctrl+X to exit, Y to save)

The defaults work for testing. For production, change these passwords in the file:
- `POSTGRES_PASSWORD` - database password
- `JWT_SECRET` - already generated, keep as is

### Step 4: Start BloxOS

Copy and paste this entire command (it's all one line):

```bash
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --build
```

> **What this does:** Starts BloxOS with the database (PostgreSQL), cache (Redis), API server, and dashboard - all in the background. The `-d` means "detached" so it runs without blocking your terminal.

The first start takes 2-5 minutes to download and build everything. After that, starts are instant.

**Verify it's running:**
```bash
docker compose ps
```
You should see 4 services with "running" status.

### Step 5: Open the Dashboard

Open your web browser and go to:
```
http://localhost:3000
```

### Step 6: Create Your Account

The first time you visit, you'll create an admin account. Remember your password!

</details>

---

<details>
<summary><h2>Adding Your First Rig</h2></summary>

Once you're logged in:

1. **Click "Add Rig"** in the top right corner

2. **Fill in the rig details:**
   - **Name:** Give it a friendly name (like "Living Room Rig")
   - **IP Address:** Your rig's local IP (like 192.168.1.100)
   - **SSH Username:** Usually `root` or your username
   - **SSH Password:** Your rig's password

   > **How to find your rig's IP address:**
   > On your mining rig, open a terminal and run: `ip addr | grep inet`
   > Look for an address like `192.168.x.x` or `10.0.x.x` (not `127.0.0.1`).
   > Or check your router's admin page for connected devices.

3. **Click "Add Rig"** - BloxOS will connect and gather info automatically

</details>

---

<details>
<summary><h2>Setting Up Mining</h2></summary>

### Step 1: Add Your Wallet

Go to **Wallets** and click **Add Wallet**:
- **Name:** "My KAS Wallet" (or whatever helps you remember)
- **Coin:** Select the coin (like KAS for Kaspa)
- **Address:** Paste your wallet address from your exchange or personal wallet

### Step 2: Add a Pool

Go to **Pools** and click **Browse Presets**:
- Pick your coin
- Pick a region close to you (US, EU, or Asia)
- Click **Add** on the pool you want

Or add a custom pool if yours isn't listed.

### Step 3: Create a Flight Sheet

A flight sheet tells your rig what to mine. Go to **Flight Sheets** and click **Add Flight Sheet**:
- **Name:** "KAS Mining" (or whatever)
- **Wallet:** Pick the wallet you just added
- **Pool:** Pick the pool you just added  
- **Miner:** Pick the mining software (lolMiner works great for most coins)

### Step 4: Start Mining

1. Go to **Rigs** and click on your rig
2. Under "Flight Sheet", select the one you just created
3. Click **Apply**
4. Click **Start Miner**

That's it! Your rig should start mining within a minute.

</details>

---

<details>
<summary><h2>Supported Coins</h2></summary>

| Coin | Algorithm | GPU | CPU |
|------|-----------|-----|-----|
| Quai (QUAI) | ProgPoW | Yes | No |
| Ravencoin (RVN) | KawPow | Yes | No |
| Ethereum Classic (ETC) | Etchash | Yes | No |
| Ergo (ERG) | Autolykos2 | Yes | No |
| Monero (XMR) | RandomX | No | Yes |
| Verus (VRSC) | VerusHash | No | Yes |
| And 15+ more... | | | |

</details>

---

<details>
<summary><h2>System Requirements</h2></summary>

### For the BloxOS Server

The server is where you run the dashboard.

| Spec | Minimum | Optimal |
|------|---------|---------|
| RAM | 2GB | 4GB+ |
| Storage | 10GB | 20GB+ |
| CPU | 2 cores | 4 cores |

**Example devices that work great:**

| Device | Works? | Notes |
|--------|--------|-------|
| Old laptop | Yes | Perfect use for old hardware |
| Desktop PC | Yes | Works great |
| Raspberry Pi 4/5 | Yes | 4GB RAM model recommended |
| Cloud server | Yes | Any VPS works |

### For Mining Rigs

Your mining rigs need:
- **Linux operating system** (Ubuntu 20.04+, HiveOS, Debian, etc.)
- NVIDIA or AMD GPU with proper drivers installed
- SSH enabled (usually already is)
- Network connection to your server

> **Windows users:** Mining rigs must run Linux, not Windows. If you're currently mining on Windows, you'll need to install Ubuntu or another Linux distro on your mining rigs. The BloxOS *server* (dashboard) can run on Windows via Docker, but the *rigs* themselves need Linux.
>
> **WSL2 is not recommended** for mining rigs - it lacks proper GPU passthrough and systemd support. Install Ubuntu directly on your mining hardware for best results.

</details>

---

<details>
<summary><h2>Troubleshooting</h2></summary>

### "docker: command not found"

1. Make sure Docker Desktop is installed (Step 1 in Quick Start)
2. **Windows:** Restart your computer after installing Docker
3. **Mac:** Open Docker from Applications at least once
4. **Linux:** Log out and back in after running `usermod` command

### "Can't connect to rig"

1. Make sure your rig is turned on and connected to your network
2. Check that SSH is enabled on your rig
3. Verify the IP address is correct
4. Try pinging your rig: `ping 192.168.1.100` (use your rig's IP)

### "Dashboard won't load"

1. Make sure Docker Desktop is running (check your system tray/menu bar)
2. Check if BloxOS is running: `docker compose ps`
3. If not running, start it:
   ```bash
   docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --build
   ```
4. Wait 2 minutes and try again
5. Check logs for errors: `docker compose logs dashboard`

### "Miner won't start"

1. Make sure you've assigned a flight sheet to the rig
2. Check that the wallet address is correct
3. Go to the rig page and look at the Events section for error messages

### Getting More Help

- Check [GitHub Issues](https://github.com/bokiko/bloxos/issues) for known problems
- View logs: `docker compose logs -f`

</details>

---

<details>
<summary><h2>Updating</h2></summary>

To get the latest version:

**If you used Git:**
```bash
cd bloxos
git pull
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --build
```

**If you downloaded ZIP:**
1. Download the new ZIP from https://github.com/bokiko/bloxos
2. Extract it (keep your `.env` file!)
3. Copy your `.env` file into the new folder
4. Run the docker compose command above

</details>

---

<details>
<summary><h2>Tech Stack</h2></summary>

| Part | Technology |
|------|------------|
| Dashboard | Next.js 15, React, TailwindCSS |
| API | Node.js, Fastify |
| Database | PostgreSQL |
| Rig Agent | Go |
| Containers | Docker |

</details>

---

<details>
<summary><h2>Project Structure</h2></summary>

```
bloxos/
├── apps/
│   ├── dashboard/        # The web interface you see
│   ├── api/              # Backend that talks to rigs
│   └── agent/            # Software that runs on each rig
├── packages/
│   └── database/         # Database structure
├── docker/               # Docker configuration
└── docs/                 # Documentation
```

</details>

---

<details>
<summary><h2>Roadmap</h2></summary>

- [x] Dashboard with real-time monitoring
- [x] Rig management via SSH
- [x] Flight sheets (mining configs)
- [x] Overclock profiles
- [x] Alert system
- [x] Multi-user support
- [x] 22 coins with 91 pool presets
- [x] Email & Telegram notifications
- [ ] Mobile app
- [x] Profit tracking
- [ ] Auto-switching (mine most profitable coin)

</details>

---

<details>
<summary><h2>Contributing</h2></summary>

Want to help? Great! Here's how:

1. Fork this repository
2. Create a branch for your changes
3. Make your changes
4. Submit a pull request

See [AGENTS.md](AGENTS.md) for coding guidelines.

</details>

---

<details>
<summary><h2>For Developers</h2></summary>

Want to run BloxOS locally for development? Here's how:

### Prerequisites

- Node.js 22 or higher
- pnpm 9 or higher

**Install Node.js (using nvm):**
```bash
curl -o- https://raw.githubusercontent.com/nvm-sh/nvm/v0.40.0/install.sh | bash
source ~/.bashrc
nvm install 22
```

**Install pnpm:**
```bash
npm install -g pnpm@9
```

### Setup

```bash
# Clone and enter directory
git clone https://github.com/bokiko/bloxos.git
cd bloxos

# Copy environment file
cp .env.example .env

# Start database services
docker compose up -d

# Install dependencies
pnpm install

# Setup database schema
pnpm db:push

# Seed initial data (optional)
pnpm db:seed

# Start development server
pnpm dev
```

The dashboard runs at http://localhost:3000 and the API at http://localhost:3001.

### Useful Commands

| Command | Description |
|---------|-------------|
| `pnpm dev` | Start all apps in development mode |
| `pnpm build` | Build all apps for production |
| `pnpm db:studio` | Open Prisma Studio (database GUI) |
| `pnpm db:push` | Push schema changes to database |
| `pnpm lint` | Run linting |
| `pnpm test` | Run tests |

</details>

---

<details>
<summary><h2>License</h2></summary>

MIT License - do whatever you want with it, just don't blame us if something goes wrong.

</details>

---

<p align="center">
  <a href="https://github.com/bokiko/bloxos">GitHub</a> •
  <a href="https://github.com/bokiko/bloxos/issues">Report Bug</a> •
  <a href="https://github.com/bokiko/bloxos/issues">Request Feature</a>
</p>

<p align="center">
  Made by <a href="https://x.com/bokiko">@bokiko</a>
</p>
