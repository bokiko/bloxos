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

## What is BloxOS?

BloxOS lets you **manage all your mining rigs from one place** - just like HiveOS, but completely free and running on your own computer.

| BloxOS | HiveOS |
|--------|--------|
| Free forever | $3/month per rig |
| Your server, your data | Their servers |
| Unlimited rigs | Extra fees for more rigs |
| Open source | Closed source |

---

## What Can You Do With BloxOS?

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

---

## Quick Start (5 Minutes)

### What You Need

- A computer to run BloxOS (can be an old PC, laptop, or even Raspberry Pi)
- Your mining rigs connected to the same network
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

Open a terminal (Command Prompt on Windows, Terminal on Mac/Linux) and run:

```bash
git clone https://github.com/bokiko/bloxos.git
cd bloxos
```

### Step 3: Start BloxOS

```bash
docker compose up -d
```

Wait about 2 minutes for everything to start.

### Step 4: Open the Dashboard

Open your web browser and go to:
```
http://localhost:3000
```

### Step 5: Create Your Account

The first time you visit, you'll create an admin account. Remember your password!

---

## Adding Your First Rig

Once you're logged in:

1. **Click "Add Rig"** in the top right corner

2. **Fill in the rig details:**
   - **Name:** Give it a friendly name (like "Living Room Rig")
   - **IP Address:** Your rig's local IP (like 192.168.1.100)
   - **SSH Username:** Usually `root` or your username
   - **SSH Password:** Your rig's password

3. **Click "Add Rig"** - BloxOS will connect and gather info automatically

---

## Setting Up Mining

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

---

## Supported Coins

| Coin | Algorithm | GPU | CPU |
|------|-----------|-----|-----|
| Kaspa (KAS) | kHeavyHash | Yes | No |
| Ravencoin (RVN) | KawPow | Yes | No |
| Ethereum Classic (ETC) | Etchash | Yes | No |
| Ergo (ERG) | Autolykos2 | Yes | No |
| Flux (FLUX) | ZelHash | Yes | No |
| Monero (XMR) | RandomX | No | Yes |
| Verus (VRSC) | VerusHash | No | Yes |
| And 15+ more... | | | |

---

## System Requirements

### For the BloxOS Server

The server is where you run the dashboard. This can be:

| Device | Works? | Notes |
|--------|--------|-------|
| Old laptop | Yes | Perfect use for old hardware |
| Desktop PC | Yes | Works great |
| Raspberry Pi 4/5 | Yes | 4GB RAM minimum |
| Cloud server | Yes | Any VPS works |

**Minimum specs:**
- 2GB RAM
- 10GB storage
- Any modern processor

### For Mining Rigs

Your mining rigs need:
- Linux operating system (Ubuntu, HiveOS, etc.)
- SSH enabled (usually already is)
- Network connection to your server

---

## Troubleshooting

### "Can't connect to rig"

1. Make sure your rig is turned on and connected to your network
2. Check that SSH is enabled on your rig
3. Verify the IP address is correct
4. Try pinging your rig: `ping 192.168.1.100` (use your rig's IP)

### "Dashboard won't load"

1. Make sure Docker is running
2. Check if BloxOS is running: `docker compose ps`
3. If not running, start it: `docker compose up -d`
4. Wait 2 minutes and try again

### "Miner won't start"

1. Make sure you've assigned a flight sheet to the rig
2. Check that the wallet address is correct
3. Go to the rig page and look at the Events section for error messages

### Getting More Help

- Check [GitHub Issues](https://github.com/bokiko/bloxos/issues) for known problems
- View logs: `docker compose logs -f`

---

## Updating BloxOS

To get the latest version:

```bash
cd bloxos
git pull
docker compose up -d --build
```

---

## Tech Stack

| Part | Technology |
|------|------------|
| Dashboard | Next.js 15, React, TailwindCSS |
| API | Node.js, Fastify |
| Database | PostgreSQL |
| Rig Agent | Go |
| Containers | Docker |

---

## Project Structure

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

---

## Roadmap

- [x] Dashboard with real-time monitoring
- [x] Rig management via SSH
- [x] Flight sheets (mining configs)
- [x] Overclock profiles
- [x] Alert system
- [x] Multi-user support
- [x] 22 coins with 91 pool presets
- [x] Email & Telegram notifications
- [ ] Mobile app
- [ ] Profit tracking
- [ ] Auto-switching (mine most profitable coin)

---

## Contributing

Want to help? Great! Here's how:

1. Fork this repository
2. Create a branch for your changes
3. Make your changes
4. Submit a pull request

See [AGENTS.md](AGENTS.md) for coding guidelines.

---

## License

MIT License - do whatever you want with it, just don't blame us if something goes wrong.

---

<p align="center">
  <a href="https://github.com/bokiko/bloxos">GitHub</a> •
  <a href="https://github.com/bokiko/bloxos/issues">Report Bug</a> •
  <a href="https://github.com/bokiko/bloxos/issues">Request Feature</a>
</p>

<p align="center">
  Made by <a href="https://github.com/bokiko">@bokiko</a>
</p>
