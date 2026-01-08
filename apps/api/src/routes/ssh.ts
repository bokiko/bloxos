import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { SSHManager } from '../services/ssh-manager.ts';
import { validateHostname, validateIPAddress, auditLog } from '../utils/security.ts';

// List of allowed commands that users can execute
// These are safe, read-only commands for system monitoring
const ALLOWED_USER_COMMANDS = [
  // System info
  'uname',
  'hostname',
  'uptime',
  'whoami',
  'date',
  'id',
  
  // Process monitoring
  'ps aux',
  'ps -ef',
  'top -bn1',
  'htop -t',
  
  // Memory/CPU
  'free',
  'free -h',
  'free -m',
  'cat /proc/cpuinfo',
  'cat /proc/meminfo',
  'lscpu',
  'nproc',
  
  // Disk
  'df',
  'df -h',
  'lsblk',
  
  // Network
  'ip addr',
  'ip link',
  'ifconfig',
  'hostname -I',
  
  // GPU/Mining
  'nvidia-smi',
  'nvidia-smi -q',
  'nvidia-smi --query-gpu',
  'rocm-smi',
  'clinfo',
  
  // Miner status
  'screen -ls',
  'tmux list-sessions',
  'pgrep -a',
  'pidof',
  
  // Logs (read-only)
  'tail',
  'head',
  'cat /var/log',
  'journalctl',
  'dmesg',
  
  // System
  'systemctl status',
  'service status',
];

// Patterns that are NEVER allowed - security risks
const BLOCKED_PATTERNS = [
  // Destructive commands
  /\brm\s+-rf?\s/i,
  /\brmdir\b/i,
  /\bmkfs\b/i,
  /\bdd\s+if=/i,
  /\bformat\b/i,
  /\bfdisk\b/i,
  /\bparted\b/i,
  
  // Privilege escalation
  /\bsudo\s/i,
  /\bsu\s+-?\s*$/i,
  /\bsu\s+root/i,
  /\bchmod\s+[0-7]*[sS]/i, // setuid/setgid
  /\bchown\b/i,
  
  // Persistence/backdoors
  /\bcrontab\b/i,
  /\b\/etc\/cron/i,
  /\bsshd\b/i,
  /authorized_keys/i,
  /\.ssh\//i,
  
  // Network exfiltration
  /\bwget\s/i,
  /\bcurl\s.*-[oO]/i, // curl with output
  /\bnc\s+-[lp]/i, // netcat listeners
  /\bnetcat\b/i,
  /\bsocat\b/i,
  
  // Code execution
  /\beval\b/i,
  /\bexec\b/i,
  /\bpython\s+-c/i,
  /\bperl\s+-e/i,
  /\bruby\s+-e/i,
  /\bnode\s+-e/i,
  /\bbash\s+-c/i,
  /\bsh\s+-c/i,
  
  // File modifications
  /[>]{1,2}/i, // redirects
  /\bsed\s+-i/i, // in-place edit
  /\bawk\s.*system\(/i,
  /\btee\b/i,
  /\bmv\s/i,
  /\bcp\s.*--/i, // cp with flags
  
  // System modifications
  /\bsystemctl\s+(start|stop|restart|enable|disable)/i,
  /\bservice\s+\w+\s+(start|stop|restart)/i,
  /\bshutdown\b/i,
  /\breboot\b/i,
  /\bhalt\b/i,
  /\bpoweroff\b/i,
  /\binit\s+[0-6]/i,
  
  // Package management
  /\bapt\b/i,
  /\bapt-get\b/i,
  /\byum\b/i,
  /\bdnf\b/i,
  /\bpacman\b/i,
  /\bpip\s+install/i,
  /\bnpm\s+install/i,
  
  // Kill signals that could affect system
  /\bkill\s+-9\s+1\b/i, // kill init
  /\bkillall\b/i,
  /\bpkill\b/i,
  
  // Environment manipulation
  /\bexport\s/i,
  /\bunset\s/i,
  /\bsource\s/i,
  /\b\.\s+\//i, // source with dot
];

/**
 * Validate if a command is safe to execute
 */
function isCommandSafe(command: string): { safe: boolean; reason?: string } {
  const trimmedCommand = command.trim();
  
  // Check for empty command
  if (!trimmedCommand) {
    return { safe: false, reason: 'Empty command' };
  }
  
  // Check for blocked patterns
  for (const pattern of BLOCKED_PATTERNS) {
    if (pattern.test(trimmedCommand)) {
      return { safe: false, reason: 'Command contains blocked pattern' };
    }
  }
  
  // Check if command starts with an allowed prefix
  const commandBase = trimmedCommand.split(/\s+/)[0].toLowerCase();
  const isAllowed = ALLOWED_USER_COMMANDS.some(allowed => {
    const allowedBase = allowed.split(/\s+/)[0].toLowerCase();
    return commandBase === allowedBase || trimmedCommand.toLowerCase().startsWith(allowed.toLowerCase());
  });
  
  if (!isAllowed) {
    return { safe: false, reason: `Command '${commandBase}' is not in the allowed list` };
  }
  
  // Check for command chaining (could bypass validation)
  if (/[;&|`$()]/.test(trimmedCommand)) {
    return { safe: false, reason: 'Command chaining or shell expansion not allowed' };
  }
  
  return { safe: true };
}

/**
 * Validate host/IP format
 */
function isValidHost(host: string): boolean {
  return validateIPAddress(host) || validateHostname(host);
}

// Validation schemas
const SSHConnectSchema = z.object({
  host: z.string().min(1).max(253).refine(isValidHost, { message: 'Invalid host format' }),
  port: z.number().min(1).max(65535).default(22),
  username: z.string().min(1).max(32).regex(/^[a-z_][a-z0-9_-]*$/i, 'Invalid username format'),
  password: z.string().max(256).optional(),
  privateKey: z.string().max(16384).optional(), // Max ~16KB for private keys
}).refine(data => data.password || data.privateKey, {
  message: 'Either password or privateKey is required',
});

const SSHCommandSchema = z.object({
  host: z.string().min(1).max(253).refine(isValidHost, { message: 'Invalid host format' }),
  port: z.number().min(1).max(65535).default(22),
  username: z.string().min(1).max(32).regex(/^[a-z_][a-z0-9_-]*$/i, 'Invalid username format'),
  password: z.string().max(256).optional(),
  privateKey: z.string().max(16384).optional(),
  command: z.string().min(1).max(1000),
}).refine(data => data.password || data.privateKey, {
  message: 'Either password or privateKey is required',
});

const AddRigViaSSHSchema = z.object({
  name: z.string().min(1).max(100).regex(/^[\w\s-]+$/, 'Name can only contain letters, numbers, spaces, underscores, and hyphens'),
  farmId: z.string().max(50),
  host: z.string().min(1).max(253).refine(isValidHost, { message: 'Invalid host format' }),
  port: z.number().min(1).max(65535).default(22),
  username: z.string().min(1).max(32).regex(/^[a-z_][a-z0-9_-]*$/i, 'Invalid username format'),
  password: z.string().max(256).optional(),
  privateKey: z.string().max(16384).optional(),
}).refine(data => data.password || data.privateKey, {
  message: 'Either password or privateKey is required',
});

const RigCommandSchema = z.object({
  command: z.string().min(1).max(1000),
});

export async function sshRoutes(app: FastifyInstance) {
  const sshManager = new SSHManager();

  // Test SSH connection
  app.post('/test', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = SSHConnectSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    try {
      const connected = await sshManager.testConnection(result.data);
      
      auditLog('SSH_CONNECTION_TEST', { 
        host: result.data.host, 
        success: connected,
        userId: request.user?.userId || 'unknown'
      });

      if (connected) {
        return reply.send({ success: true, message: 'SSH connection successful' });
      } else {
        return reply.status(400).send({ success: false, message: 'SSH connection failed' });
      }
    } catch {
      return reply.status(500).send({ success: false, message: 'Connection failed' });
    }
  });

  // Execute command via SSH (with validation)
  app.post('/exec', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = SSHCommandSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const { command, ...credentials } = result.data;

    // Validate command safety
    const validation = isCommandSafe(command);
    if (!validation.safe) {
      auditLog('SSH_COMMAND_BLOCKED', {
        host: credentials.host,
        command: command.substring(0, 100),
        reason: validation.reason,
        userId: request.user?.userId || 'unknown'
      });
      return reply.status(403).send({ 
        success: false, 
        message: `Command not allowed: ${validation.reason}` 
      });
    }

    try {
      auditLog('SSH_COMMAND_EXECUTED', {
        host: credentials.host,
        command: command.substring(0, 100),
        userId: request.user?.userId || 'unknown'
      });

      const output = await sshManager.executeCommand(credentials, command);
      return reply.send({ success: true, output });
    } catch {
      return reply.status(500).send({ success: false, message: 'Command execution failed' });
    }
  });

  // Add rig via SSH (auto-setup)
  app.post('/add-rig', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = AddRigViaSSHSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const { name, farmId, ...credentials } = result.data;

    try {
      auditLog('RIG_SETUP_STARTED', {
        name,
        farmId,
        host: credentials.host,
        userId: request.user?.userId || 'unknown'
      });

      // This will:
      // 1. Connect to the rig
      // 2. Gather system info
      // 3. Install the agent
      // 4. Create rig in database
      const rig = await sshManager.setupRig({ name, farmId, credentials });
      
      auditLog('RIG_SETUP_COMPLETED', {
        rigId: rig.id,
        name,
        host: credentials.host,
        userId: request.user?.userId || 'unknown'
      });

      return reply.status(201).send(rig);
    } catch (error) {
      const errorMessage = error instanceof Error ? error.message : 'Unknown error';
      request.log.error({ error, host: credentials.host }, 'Failed to setup rig via SSH');
      
      auditLog('RIG_SETUP_FAILED', {
        name,
        host: credentials.host,
        error: errorMessage,
        userId: request.user?.userId || 'unknown'
      });

      return reply.status(500).send({ success: false, message: 'Rig setup failed' });
    }
  });

  // Get system info from a host
  app.post('/system-info', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = SSHConnectSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    try {
      const systemInfo = await sshManager.getSystemInfo(result.data);
      return reply.send(systemInfo);
    } catch {
      return reply.status(500).send({ success: false, message: 'Failed to get system info' });
    }
  });

  // Execute command on a rig using stored credentials
  app.post('/rig/:rigId/exec', async (request: FastifyRequest<{ Params: { rigId: string } }>, reply: FastifyReply) => {
    const { rigId } = request.params;
    
    // Validate rigId format (UUID)
    if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(rigId)) {
      return reply.status(400).send({ error: 'Invalid rig ID format' });
    }

    const result = RigCommandSchema.safeParse(request.body);
    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const { command } = result.data;

    // Validate command safety
    const validation = isCommandSafe(command);
    if (!validation.safe) {
      auditLog('SSH_RIG_COMMAND_BLOCKED', {
        rigId,
        command: command.substring(0, 100),
        reason: validation.reason,
        userId: request.user?.userId || 'unknown'
      });
      return reply.status(403).send({ 
        success: false, 
        message: `Command not allowed: ${validation.reason}` 
      });
    }

    try {
      auditLog('SSH_RIG_COMMAND_EXECUTED', {
        rigId,
        command: command.substring(0, 100),
        userId: request.user?.userId || 'unknown'
      });

      const output = await sshManager.executeCommandOnRig(rigId, command);
      return reply.send({ success: true, output });
    } catch {
      return reply.status(500).send({ success: false, message: 'Command execution failed' });
    }
  });

  // Refresh rig info using stored credentials
  app.post('/rig/:rigId/refresh', async (request: FastifyRequest<{ Params: { rigId: string } }>, reply: FastifyReply) => {
    const { rigId } = request.params;

    // Validate rigId format (UUID)
    if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(rigId)) {
      return reply.status(400).send({ error: 'Invalid rig ID format' });
    }

    try {
      await sshManager.refreshRigInfo(rigId);
      return reply.send({ success: true, message: 'Rig info refreshed' });
    } catch {
      return reply.status(500).send({ success: false, message: 'Failed to refresh rig info' });
    }
  });

  // Get detailed system info for a rig (uses internal safe commands only)
  app.get('/rig/:rigId/system-info', async (request: FastifyRequest<{ Params: { rigId: string } }>, reply: FastifyReply) => {
    const { rigId } = request.params;

    // Validate rigId format (UUID)
    if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(rigId)) {
      return reply.status(400).send({ error: 'Invalid rig ID format' });
    }

    try {
      // These are internal commands - safe and read-only
      const commands = {
        // OS Info
        hostname: 'hostname',
        os: 'cat /etc/os-release | grep "^PRETTY_NAME=" | cut -d= -f2 | tr -d \'"\'',
        kernel: 'uname -r',
        arch: 'uname -m',
        uptime: 'uptime -p 2>/dev/null || uptime',
        
        // CPU Info
        cpuModel: 'cat /proc/cpuinfo | grep "model name" | head -1 | cut -d: -f2 | xargs',
        cpuCores: 'grep -c "^processor" /proc/cpuinfo',
        cpuThreads: 'nproc',
        cpuFreqMax: 'cat /sys/devices/system/cpu/cpu0/cpufreq/cpuinfo_max_freq 2>/dev/null || echo "0"',
        cpuFreqCurrent: 'cat /proc/cpuinfo | grep "cpu MHz" | head -1 | cut -d: -f2 | xargs',
        cpuCache: 'cat /proc/cpuinfo | grep "cache size" | head -1 | cut -d: -f2 | xargs',
        
        // Memory Info
        memTotal: 'free -b | grep Mem | awk \'{print $2}\'',
        memUsed: 'free -b | grep Mem | awk \'{print $3}\'',
        memFree: 'free -b | grep Mem | awk \'{print $4}\'',
        swapTotal: 'free -b | grep Swap | awk \'{print $2}\'',
        swapUsed: 'free -b | grep Swap | awk \'{print $3}\'',
        
        // Storage Info
        diskInfo: 'df -B1 / | tail -1 | awk \'{print $2","$3","$4","$5}\'',
        disks: 'lsblk -d -b -o NAME,SIZE,TYPE,MODEL 2>/dev/null | tail -n +2',
        
        // Motherboard Info
        mbVendor: 'cat /sys/class/dmi/id/board_vendor 2>/dev/null || echo "Unknown"',
        mbName: 'cat /sys/class/dmi/id/board_name 2>/dev/null || echo "Unknown"',
        mbVersion: 'cat /sys/class/dmi/id/board_version 2>/dev/null || echo ""',
        biosVendor: 'cat /sys/class/dmi/id/bios_vendor 2>/dev/null || echo "Unknown"',
        biosVersion: 'cat /sys/class/dmi/id/bios_version 2>/dev/null || echo "Unknown"',
        biosDate: 'cat /sys/class/dmi/id/bios_date 2>/dev/null || echo "Unknown"',
        
        // System Info
        systemVendor: 'cat /sys/class/dmi/id/sys_vendor 2>/dev/null || echo "Unknown"',
        productName: 'cat /sys/class/dmi/id/product_name 2>/dev/null || echo "Unknown"',
        
        // Network Info
        ipAddresses: 'hostname -I',
        macAddresses: 'ip link show | grep -E "link/ether" | awk \'{print $2}\' | head -5 | tr "\\n" ","',
        defaultGateway: 'ip route | grep default | awk \'{print $3}\' | head -1',
        dnsServers: 'cat /etc/resolv.conf | grep nameserver | awk \'{print $2}\' | tr "\\n" ","',
        
        // GPU Info (NVIDIA)
        nvidiaDriver: 'nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -1 || echo ""',
        nvidiaGpus: 'nvidia-smi --query-gpu=index,name,memory.total,pci.bus_id,uuid,power.limit --format=csv,noheader,nounits 2>/dev/null || echo ""',
        cudaVersion: 'nvidia-smi --query-gpu=driver_version --format=csv,noheader 2>/dev/null | head -1 || echo ""',
        
        // PCI Devices
        pciDevices: 'lspci -nn 2>/dev/null | grep -E "VGA|3D|Display" || echo ""',
      };

      const results: Record<string, string> = {};

      // Use internal command execution (bypasses user command validation)
      for (const [key, cmd] of Object.entries(commands)) {
        try {
          results[key] = await sshManager.executeCommandOnRig(rigId, cmd);
        } catch {
          results[key] = '';
        }
      }

      // Parse disk info
      const diskParts = results.diskInfo?.split(',') || [];
      const rootDisk = {
        total: parseInt(diskParts[0], 10) || 0,
        used: parseInt(diskParts[1], 10) || 0,
        free: parseInt(diskParts[2], 10) || 0,
        usedPercent: diskParts[3] || '0%',
      };

      // Parse disks list
      const disks = results.disks?.split('\n').filter(Boolean).map(line => {
        const parts = line.trim().split(/\s+/);
        return {
          name: parts[0] || '',
          size: parseInt(parts[1], 10) || 0,
          type: parts[2] || '',
          model: parts.slice(3).join(' ') || 'Unknown',
        };
      }) || [];

      // Parse GPUs
      const gpus = results.nvidiaGpus?.split('\n').filter(Boolean).map(line => {
        const parts = line.split(',').map(p => p.trim());
        return {
          index: parseInt(parts[0], 10),
          name: parts[1] || 'Unknown',
          vram: parseInt(parts[2], 10) || 0,
          busId: parts[3] || '',
          uuid: parts[4] || '',
          powerLimit: parseInt(parts[5], 10) || 0,
        };
      }) || [];

      // Format response
      const systemInfo = {
        os: {
          name: results.os || 'Unknown',
          kernel: results.kernel || 'Unknown',
          arch: results.arch || 'Unknown',
          hostname: results.hostname || 'Unknown',
          uptime: results.uptime || 'Unknown',
        },
        cpu: {
          model: results.cpuModel || 'Unknown',
          cores: parseInt(results.cpuCores, 10) || 0,
          threads: parseInt(results.cpuThreads, 10) || 0,
          maxFrequency: Math.round(parseInt(results.cpuFreqMax, 10) / 1000) || 0,
          currentFrequency: Math.round(parseFloat(results.cpuFreqCurrent) || 0),
          cache: results.cpuCache || 'Unknown',
        },
        memory: {
          total: parseInt(results.memTotal, 10) || 0,
          used: parseInt(results.memUsed, 10) || 0,
          free: parseInt(results.memFree, 10) || 0,
          swapTotal: parseInt(results.swapTotal, 10) || 0,
          swapUsed: parseInt(results.swapUsed, 10) || 0,
        },
        storage: {
          root: rootDisk,
          disks,
        },
        motherboard: {
          vendor: results.mbVendor?.trim() || 'Unknown',
          name: results.mbName?.trim() || 'Unknown',
          version: results.mbVersion?.trim() || '',
        },
        bios: {
          vendor: results.biosVendor?.trim() || 'Unknown',
          version: results.biosVersion?.trim() || 'Unknown',
          date: results.biosDate?.trim() || 'Unknown',
        },
        system: {
          vendor: results.systemVendor?.trim() || 'Unknown',
          product: results.productName?.trim() || 'Unknown',
        },
        network: {
          ipAddresses: results.ipAddresses?.trim().split(' ').filter(Boolean) || [],
          macAddresses: results.macAddresses?.trim().split(',').filter(Boolean) || [],
          gateway: results.defaultGateway?.trim() || 'Unknown',
          dns: results.dnsServers?.trim().split(',').filter(Boolean) || [],
        },
        gpu: {
          driver: results.nvidiaDriver?.trim() || 'Not installed',
          devices: gpus,
          pciDevices: results.pciDevices?.trim().split('\n').filter(Boolean) || [],
        },
      };

      return reply.send(systemInfo);
    } catch {
      return reply.status(500).send({ success: false, message: 'Failed to get system info' });
    }
  });
}
