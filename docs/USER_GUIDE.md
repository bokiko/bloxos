# BloxOS User Guide

> Complete guide for using the BloxOS mining management platform

---

## Table of Contents

1. [Getting Started](#getting-started)
2. [Dashboard Overview](#dashboard-overview)
3. [Managing Rigs](#managing-rigs)
4. [Flight Sheets](#flight-sheets)
5. [Overclocking](#overclocking)
6. [Alerts](#alerts)
7. [User Management](#user-management)
8. [Tips & Best Practices](#tips--best-practices)

---

## Getting Started

### First Login

1. Navigate to your BloxOS dashboard URL
2. If this is a fresh install, you'll see the **Setup** page
3. Create your admin account with a strong password
4. You're now logged in as admin

### Dashboard Tour

After login, you'll see:

- **Top Bar**: Navigation, user menu, notifications
- **Sidebar**: Quick links to main sections
- **Main Area**: Current page content
- **Status Bar**: Live connection indicator

---

## Dashboard Overview

### Home Page

The home page shows a summary of your mining operation:

- **Total Rigs**: Online/Offline count
- **Total Hashrate**: Combined hashrate across all rigs
- **Total Power**: Combined power consumption
- **Recent Alerts**: Latest issues requiring attention

### Live Updates

The dashboard updates in real-time:

- **Green dot "Live"**: WebSocket connected, instant updates
- **Yellow dot "Polling"**: Fallback mode, updates every 30 seconds
- **Gray dot "Paused"**: Updates paused (click to resume)

---

## Managing Rigs

### Adding a New Rig

1. Go to **Rigs** > **Add Rig**
2. Enter rig details:
   - **Name**: Descriptive name (e.g., "GPU-Rig-01")
   - **Hostname/IP**: Network address (optional for agent-based rigs)
3. Click **Create Rig**
4. Copy the **Rig Token** - you'll need this for the agent
5. Install the agent on your rig using the token

### Rig Status Indicators

| Status | Color | Meaning |
|--------|-------|---------|
| Online | Green | Rig connected and reporting |
| Offline | Red | No connection for 60+ seconds |
| Warning | Yellow | High temp or low hashrate |
| Error | Red | Critical issue detected |

### Rig Detail Page

Click any rig to see detailed information:

- **System Info**: Hostname, IP, OS, uptime
- **GPU Cards**: Per-GPU stats with temperature, hashrate, power
- **Active Miners**: Running mining software with shares
- **Controls**: Start/stop miner, apply settings, reboot

### GPU Stats Explained

| Stat | Description | Good Range |
|------|-------------|------------|
| Temp | Core temperature | < 75°C |
| Mem Temp | Memory temperature | < 95°C |
| Fan | Fan speed percentage | Auto or 60-80% |
| Power | Power consumption in watts | Varies by GPU |
| Core | Core clock in MHz | Depends on OC |
| Memory | Memory clock in MHz | Depends on OC |
| Hashrate | Mining speed | Depends on algo |

### Rig Actions

From the rig detail page, you can:

- **Start Miner**: Start mining with current flight sheet
- **Stop Miner**: Stop all mining processes
- **Restart Miner**: Quick restart
- **Apply Flight Sheet**: Change mining configuration
- **Apply OC Profile**: Change overclocking settings
- **Reboot**: Restart the entire rig
- **Shutdown**: Power off the rig

---

## Flight Sheets

Flight sheets combine wallet, pool, and miner settings into one configuration.

### Creating a Wallet

1. Go to **Wallets**
2. Click **Add Wallet**
3. Enter:
   - **Name**: Descriptive name
   - **Coin**: Cryptocurrency (ETH, RVN, etc.)
   - **Address**: Your wallet address
4. Click **Save**

### Creating a Pool

1. Go to **Pools**
2. Click **Add Pool**
3. Enter:
   - **Name**: Pool name (e.g., "2Miners ETH")
   - **Coin**: Matching coin
   - **URL**: Stratum URL (e.g., `stratum+tcp://eth.2miners.com:2020`)
   - **SSL URL**: Optional secure URL
4. Click **Save**

### Creating a Flight Sheet

1. Go to **Flight Sheets**
2. Click **Add Flight Sheet**
3. Select:
   - **Name**: Configuration name
   - **Wallet**: Previously created wallet
   - **Pool**: Previously created pool
   - **Miner**: Mining software (T-Rex, lolMiner, etc.)
   - **Algorithm**: Mining algorithm
   - **Extra Arguments**: Optional miner flags
4. Click **Save**

### Applying a Flight Sheet

1. Go to rig detail page
2. Click **Apply Flight Sheet**
3. Select the flight sheet
4. Click **Apply**

The agent will:
1. Stop current miner
2. Configure new settings
3. Start miner with new configuration

---

## Overclocking

### Creating an OC Profile

1. Go to **OC Profiles**
2. Click **Add Profile**
3. Configure:
   - **Name**: Profile name (e.g., "ETH Optimal")
   - **Vendor**: NVIDIA or AMD
   - **Power Limit**: Watts or percentage
   - **Core Offset**: Core clock adjustment
   - **Memory Offset**: Memory clock adjustment
   - **Fan Speed**: Fixed percentage or "Auto"
4. Click **Save**

### Recommended OC Settings

**NVIDIA RTX 3080 (ETH):**
- Power Limit: 230W
- Core Offset: -200
- Memory Offset: +1200
- Fan: 70%

**AMD RX 6800 XT (ETH):**
- Power Limit: 140W
- Core Clock: 1300 MHz
- Memory Clock: 2100 MHz
- Fan: Auto

### Applying OC Profile

1. Go to rig detail page
2. Click **Apply OC Profile**
3. Select profile
4. Click **Apply**

Settings apply immediately to all GPUs.

### Per-GPU Overrides

For mixed GPU rigs:
1. Edit the OC profile
2. Add per-GPU overrides
3. Specify different settings for each GPU index

---

## Alerts

### Alert Types

| Type | Trigger | Default Threshold |
|------|---------|-------------------|
| GPU Temp High | GPU exceeds threshold | 80°C |
| CPU Temp High | CPU exceeds threshold | 85°C |
| Rig Offline | No heartbeat received | 5 minutes |
| Hashrate Drop | Hashrate drops significantly | 20% |

### Configuring Alerts

1. Go to rig detail page
2. Click **Alert Settings**
3. Configure thresholds:
   - GPU temperature threshold
   - CPU temperature threshold
   - Offline timeout (seconds)
   - Hashrate drop percentage
4. Enable/disable alert types
5. Click **Save**

### Viewing Alerts

1. Go to **Alerts** page
2. See all active and recent alerts
3. Click alert for details
4. Actions:
   - **Acknowledge**: Mark as seen
   - **Resolve**: Mark as fixed

### Alert Notifications

Configure external notifications:

1. Go to **Settings** > **Notifications**
2. Add notification channels:
   - **Telegram**: Enter bot token and chat ID
   - **Discord**: Enter webhook URL
3. Test notifications
4. Enable for alert types

---

## User Management

### User Roles

| Role | Permissions |
|------|-------------|
| Admin | Full access, manage users |
| User | Manage own farms and rigs |
| Monitor | Read-only access |

### Adding Users (Admin)

1. Go to **Users**
2. Click **Add User**
3. Enter:
   - Email address
   - Name
   - Role
   - Initial password
4. Click **Create**

User will be prompted to change password on first login.

### Rig Groups

Organize rigs into groups:

1. Go to **Rig Groups**
2. Click **Add Group**
3. Enter name and color
4. Add rigs to group

Groups help with:
- Visual organization
- Bulk actions
- Filtering

---

## Tips & Best Practices

### Performance

1. **Stable internet**: Rigs need reliable connection to server
2. **Local time sync**: Keep rig clocks synchronized
3. **Sufficient power**: Don't overload circuits

### Security

1. **Strong passwords**: Use unique, complex passwords
2. **Regular updates**: Keep BloxOS and miners updated
3. **Firewall**: Only expose necessary ports
4. **HTTPS**: Use SSL in production

### Monitoring

1. **Check daily**: Review dashboard for issues
2. **Set alerts**: Configure thresholds for your hardware
3. **Track efficiency**: Monitor power/hashrate ratio

### Troubleshooting

**Rig shows offline but is running:**
- Check network connectivity
- Verify agent is running: `systemctl status bloxos-agent`
- Check agent logs: `journalctl -u bloxos-agent -f`

**Miner won't start:**
- Check flight sheet configuration
- Verify miner is installed
- Check rig logs for errors

**OC not applying:**
- Verify GPU detected correctly
- Check if running as root/sudo
- AMD: Ensure proper drivers installed

**High temperatures:**
- Check fan settings
- Clean dust from GPUs
- Improve case airflow
- Reduce power limit

---

## Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `R` | Refresh current page |
| `G` then `R` | Go to Rigs |
| `G` then `F` | Go to Flight Sheets |
| `G` then `A` | Go to Alerts |
| `/` | Focus search |
| `?` | Show shortcuts help |

---

## Support

- **Documentation**: https://github.com/bokiko/bloxos/docs
- **Issues**: https://github.com/bokiko/bloxos/issues
- **Discussions**: https://github.com/bokiko/bloxos/discussions

---

*Last Updated: January 2026*
