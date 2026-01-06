import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { SSHManager } from '../services/ssh-manager.ts';

// Validation schemas
const SSHConnectSchema = z.object({
  host: z.string().min(1),
  port: z.number().default(22),
  username: z.string().min(1),
  password: z.string().optional(),
  privateKey: z.string().optional(),
});

const SSHCommandSchema = z.object({
  host: z.string().min(1),
  port: z.number().default(22),
  username: z.string().min(1),
  password: z.string().optional(),
  privateKey: z.string().optional(),
  command: z.string().min(1),
});

const AddRigViaSSHSchema = z.object({
  name: z.string().min(1).max(100),
  farmId: z.string(),
  host: z.string().min(1),
  port: z.number().default(22),
  username: z.string().min(1),
  password: z.string().optional(),
  privateKey: z.string().optional(),
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
      
      if (connected) {
        return reply.send({ success: true, message: 'SSH connection successful' });
      } else {
        return reply.status(400).send({ success: false, message: 'SSH connection failed' });
      }
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Unknown error';
      return reply.status(500).send({ success: false, message });
    }
  });

  // Execute command via SSH
  app.post('/exec', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = SSHCommandSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const { command, ...credentials } = result.data;

    try {
      const output = await sshManager.executeCommand(credentials, command);
      return reply.send({ success: true, output });
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Unknown error';
      return reply.status(500).send({ success: false, message });
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
      // This will:
      // 1. Connect to the rig
      // 2. Gather system info
      // 3. Install the agent
      // 4. Create rig in database
      const rig = await sshManager.setupRig({ name, farmId, credentials });
      
      return reply.status(201).send(rig);
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Unknown error';
      request.log.error({ error, host: credentials.host }, 'Failed to setup rig via SSH');
      return reply.status(500).send({ success: false, message });
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
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Unknown error';
      return reply.status(500).send({ success: false, message });
    }
  });

  // Execute command on a rig using stored credentials
  app.post('/rig/:rigId/exec', async (request: FastifyRequest<{ Params: { rigId: string } }>, reply: FastifyReply) => {
    const { rigId } = request.params;
    const body = request.body as { command?: string };

    if (!body.command) {
      return reply.status(400).send({ error: 'Command is required' });
    }

    try {
      const output = await sshManager.executeCommandOnRig(rigId, body.command);
      return reply.send({ success: true, output });
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Unknown error';
      return reply.status(500).send({ success: false, message });
    }
  });

  // Refresh rig info using stored credentials
  app.post('/rig/:rigId/refresh', async (request: FastifyRequest<{ Params: { rigId: string } }>, reply: FastifyReply) => {
    const { rigId } = request.params;

    try {
      await sshManager.refreshRigInfo(rigId);
      return reply.send({ success: true, message: 'Rig info refreshed' });
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Unknown error';
      return reply.status(500).send({ success: false, message });
    }
  });

  // Get detailed system info for a rig
  app.get('/rig/:rigId/system-info', async (request: FastifyRequest<{ Params: { rigId: string } }>, reply: FastifyReply) => {
    const { rigId } = request.params;

    try {
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
          maxFrequency: Math.round(parseInt(results.cpuFreqMax, 10) / 1000) || 0, // Convert KHz to MHz
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
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Unknown error';
      return reply.status(500).send({ success: false, message });
    }
  });
}
