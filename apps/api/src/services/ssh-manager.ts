import { Client, type ConnectConfig, type ClientChannel } from 'ssh2';
import { prisma } from '@bloxos/database';
import { encrypt, decrypt } from '../utils/encryption.ts';
import { 
  sanitizeCommand, 
  auditLog,
  generateSecureToken,
} from '../utils/security.ts';

export interface SSHCredentials {
  host: string;
  port: number;
  username: string;
  password?: string;
  privateKey?: string;
}

export interface SystemInfo {
  hostname: string;
  os: string;
  osVersion: string;
  kernel: string;
  cpuModel: string;
  cpuCores: number;
  ramTotal: number;
  ipAddresses: string[];
  gpus: GPUInfo[];
}

export interface GPUInfo {
  index: number;
  name: string;
  vendor: 'NVIDIA' | 'AMD' | 'INTEL';
  vram: number;
  busId?: string;
  uuid?: string;
}

// Whitelist of safe system commands for gathering info
const SAFE_INFO_COMMANDS = new Map<string, string>([
  ['hostname', 'hostname'],
  ['os', 'cat /etc/os-release | grep "^NAME=" | cut -d= -f2 | tr -d \'"\''],
  ['osVersion', 'cat /etc/os-release | grep "^VERSION_ID=" | cut -d= -f2 | tr -d \'"\''],
  ['kernel', 'uname -r'],
  ['cpuModel', 'cat /proc/cpuinfo | grep "model name" | head -1 | cut -d: -f2 | xargs'],
  ['cpuCores', 'nproc'],
  ['ramTotal', 'free -b | grep Mem | awk \'{print $2}\''],
  ['ipAddresses', 'hostname -I'],
  ['nvidiaGpus', 'nvidia-smi --query-gpu=index,name,memory.total,pci.bus_id,uuid --format=csv,noheader,nounits 2>/dev/null || echo ""'],
  ['amdGpus', 'rocm-smi --showid --showproductname --showmeminfo vram 2>/dev/null | grep -E "GPU\\[|Card series|VRAM Total" || echo ""'],
]);

// Commands that are never allowed
const BLOCKED_COMMANDS = [
  'rm ', 'rm -', 'rmdir',
  'dd ', 'mkfs',
  'wget ', 'curl ',
  'chmod ', 'chown ',
  'useradd', 'userdel', 'passwd',
  'shutdown', 'reboot', 'poweroff', 'halt',
  'iptables', 'ufw',
  'systemctl disable', 'systemctl mask',
  '> /dev/', 'cat /dev/zero', 'cat /dev/random',
  'fork', 'bomb',
  'base64 -d', 'eval ', 'exec ',
  'python -c', 'perl -e', 'ruby -e',
  '/bin/sh', '/bin/bash -c',
];

export class SSHManager {
  private getConnectConfig(credentials: SSHCredentials): ConnectConfig {
    const config: ConnectConfig = {
      host: credentials.host,
      port: credentials.port,
      username: credentials.username,
      readyTimeout: 10000,
    };

    if (credentials.privateKey) {
      config.privateKey = credentials.privateKey;
    } else if (credentials.password) {
      config.password = credentials.password;
    }

    return config;
  }

  async testConnection(credentials: SSHCredentials): Promise<boolean> {
    return new Promise((resolve) => {
      const conn = new Client();

      conn.on('ready', () => {
        conn.end();
        resolve(true);
      });

      conn.on('error', () => {
        resolve(false);
      });

      conn.connect(this.getConnectConfig(credentials));
    });
  }

  // Internal command execution - NO user input allowed
  private async executeInternalCommand(credentials: SSHCredentials, command: string): Promise<string> {
    return new Promise((resolve, reject) => {
      const conn = new Client();

      conn.on('ready', () => {
        conn.exec(command, (err: Error | undefined, stream: ClientChannel) => {
          if (err) {
            conn.end();
            reject(err);
            return;
          }

          let output = '';
          let errorOutput = '';

          stream.on('data', (data: Buffer) => {
            output += data.toString();
          });

          stream.stderr.on('data', (data: Buffer) => {
            errorOutput += data.toString();
          });

          stream.on('close', () => {
            conn.end();
            if (errorOutput && !output) {
              reject(new Error(errorOutput));
            } else {
              resolve(output.trim());
            }
          });
        });
      });

      conn.on('error', (err: Error) => {
        reject(err);
      });

      conn.connect(this.getConnectConfig(credentials));
    });
  }

  // Validate user command before execution
  private validateUserCommand(command: string): void {
    // Check for blocked commands
    const lowerCommand = command.toLowerCase();
    for (const blocked of BLOCKED_COMMANDS) {
      if (lowerCommand.includes(blocked.toLowerCase())) {
        throw new Error(`Command contains blocked operation: ${blocked.split(' ')[0]}`);
      }
    }

    // Sanitize the command
    sanitizeCommand(command);
  }

  // Execute user command with validation
  async executeCommand(credentials: SSHCredentials, command: string): Promise<string> {
    this.validateUserCommand(command);
    return this.executeInternalCommand(credentials, command);
  }

  async getSystemInfo(credentials: SSHCredentials): Promise<SystemInfo> {
    const results: Record<string, string> = {};

    // Only execute whitelisted commands
    for (const [key, cmd] of SAFE_INFO_COMMANDS) {
      try {
        results[key] = await this.executeInternalCommand(credentials, cmd);
      } catch {
        results[key] = '';
      }
    }

    // Parse GPU info
    const gpus: GPUInfo[] = [];

    // Parse NVIDIA GPUs
    if (results.nvidiaGpus) {
      const lines = results.nvidiaGpus.split('\n').filter(Boolean);
      for (const line of lines) {
        const parts = line.split(',').map((p) => p.trim());
        if (parts.length >= 4) {
          gpus.push({
            index: parseInt(parts[0], 10),
            name: parts[1],
            vendor: 'NVIDIA',
            vram: parseInt(parts[2], 10), // MB
            busId: parts[3],
            uuid: parts[4],
          });
        }
      }
    }

    return {
      hostname: results.hostname || 'unknown',
      os: results.os || 'Linux',
      osVersion: results.osVersion || 'unknown',
      kernel: results.kernel || 'unknown',
      cpuModel: results.cpuModel || 'unknown',
      cpuCores: parseInt(results.cpuCores, 10) || 1,
      ramTotal: parseInt(results.ramTotal, 10) || 0,
      ipAddresses: results.ipAddresses ? results.ipAddresses.split(' ').filter(Boolean) : [],
      gpus,
    };
  }

  async setupRig(options: {
    name: string;
    farmId: string;
    credentials: SSHCredentials;
  }): Promise<{ id: string; name: string; hostname: string; token: string }> {
    const { name, farmId, credentials } = options;

    // Step 1: Test connection
    const connected = await this.testConnection(credentials);
    if (!connected) {
      throw new Error('Cannot connect to host via SSH');
    }

    // Step 2: Gather system info
    const systemInfo = await this.getSystemInfo(credentials);

    // Step 3: Generate secure token for this rig
    const token = generateSecureToken(32);

    // Step 4: Create rig in database with SSH credentials
    const rig = await prisma.rig.create({
      data: {
        name,
        hostname: systemInfo.hostname,
        ipAddress: credentials.host,
        os: systemInfo.os,
        osVersion: systemInfo.osVersion,
        token,
        farmId,
        status: 'OFFLINE',
        gpus: {
          create: systemInfo.gpus.map((gpu) => ({
            index: gpu.index,
            name: gpu.name,
            vendor: gpu.vendor,
            vram: gpu.vram,
            busId: gpu.busId,
            uuid: gpu.uuid,
          })),
        },
        sshCredential: {
          create: {
            host: credentials.host,
            port: credentials.port,
            username: credentials.username,
            authType: credentials.privateKey ? 'KEY' : 'PASSWORD',
            encryptedPassword: credentials.password ? encrypt(credentials.password) : null,
            encryptedPrivateKey: credentials.privateKey ? encrypt(credentials.privateKey) : null,
          },
        },
      },
      include: {
        gpus: true,
        sshCredential: true,
      },
    });

    auditLog({
      action: 'setup_rig',
      resource: 'rig',
      resourceId: rig.id,
      details: { 
        name, 
        host: credentials.host,
        gpuCount: systemInfo.gpus.length,
      },
      success: true,
    });

    return rig;
  }

  // Get credentials for a rig from database
  async getCredentialsForRig(rigId: string): Promise<SSHCredentials | null> {
    const cred = await prisma.sSHCredential.findUnique({
      where: { rigId },
    });

    if (!cred) return null;

    return {
      host: cred.host,
      port: cred.port,
      username: cred.username,
      password: cred.encryptedPassword ? decrypt(cred.encryptedPassword) : undefined,
      privateKey: cred.encryptedPrivateKey ? decrypt(cred.encryptedPrivateKey) : undefined,
    };
  }

  // Execute validated command on a rig using stored credentials
  async executeCommandOnRig(rigId: string, command: string): Promise<string> {
    const credentials = await this.getCredentialsForRig(rigId);
    if (!credentials) {
      throw new Error('No SSH credentials found for this rig');
    }

    // Validate command
    this.validateUserCommand(command);

    auditLog({
      action: 'ssh_command',
      resource: 'rig',
      resourceId: rigId,
      details: { command: command.substring(0, 100) }, // Truncate for logging
      success: true,
    });

    return this.executeInternalCommand(credentials, command);
  }

  // Execute sudo command using SSH pseudo-terminal for secure password entry
  // This avoids having the password visible in process lists or command history
  async executeSudoCommandOnRig(rigId: string, command: string): Promise<string> {
    const credentials = await this.getCredentialsForRig(rigId);
    if (!credentials) {
      throw new Error('No SSH credentials found for this rig');
    }
    
    // Validate the command part
    this.validateUserCommand(command);

    // If we have a private key, try sudo without password first (assumes NOPASSWD or ssh-agent)
    if (credentials.privateKey) {
      try {
        return await this.executeInternalCommand(credentials, `sudo -n ${command}`);
      } catch {
        // -n flag failed, need password
      }
    }

    // Try passwordless sudo first (NOPASSWD configured)
    try {
      return await this.executeInternalCommand(credentials, `sudo -n ${command}`);
    } catch {
      // NOPASSWD not configured, need to use password
    }

    if (!credentials.password) {
      throw new Error('Sudo requires password but none provided. Configure NOPASSWD or provide password.');
    }

    // Use sudo with password via stdin using a here-string approach
    // This is more secure as the password doesn't appear in the command line
    // The password is sent via stdin to sudo -S
    return new Promise((resolve, reject) => {
      const conn = new Client();

      conn.on('ready', () => {
        // Request a PTY (pseudo-terminal) for sudo
        conn.exec(`sudo -S ${command}`, { pty: true }, (err, stream) => {
          if (err) {
            conn.end();
            reject(err);
            return;
          }

          let output = '';
          let errorOutput = '';
          let passwordSent = false;

          stream.on('data', (data: Buffer) => {
            const text = data.toString();
            
            // Detect password prompt and send password via stdin
            if (!passwordSent && (text.includes('[sudo]') || text.includes('password'))) {
              stream.write(credentials.password + '\n');
              passwordSent = true;
            } else {
              output += text;
            }
          });

          stream.stderr.on('data', (data: Buffer) => {
            const text = data.toString();
            // Filter out password prompts from error output
            if (!text.includes('[sudo]') && !text.includes('password')) {
              errorOutput += text;
            }
          });

          stream.on('close', () => {
            conn.end();
            
            // Clean up output (remove any password prompt echoes)
            const cleanOutput = output
              .replace(/\[sudo\].*password.*:/gi, '')
              .replace(/Password:/gi, '')
              .trim();

            if (errorOutput && !cleanOutput) {
              reject(new Error(errorOutput));
            } else {
              resolve(cleanOutput);
            }
          });
        });
      });

      conn.on('error', (err: Error) => {
        reject(err);
      });

      conn.connect(this.getConnectConfig(credentials));
    }).then((result) => {
      auditLog({
        action: 'ssh_sudo_command',
        resource: 'rig',
        resourceId: rigId,
        details: { command: command.substring(0, 100) },
        success: true,
      });
      return result as string;
    });
  }

  // Refresh system info for a rig
  async refreshRigInfo(rigId: string): Promise<void> {
    const credentials = await this.getCredentialsForRig(rigId);
    if (!credentials) {
      throw new Error('No SSH credentials found for this rig');
    }

    const systemInfo = await this.getSystemInfo(credentials);

    // Update rig info
    await prisma.rig.update({
      where: { id: rigId },
      data: {
        hostname: systemInfo.hostname,
        os: systemInfo.os,
        osVersion: systemInfo.osVersion,
        lastSeen: new Date(),
        status: 'ONLINE',
      },
    });

    // Update GPU info - delete old and create new
    await prisma.gPU.deleteMany({ where: { rigId } });
    await prisma.gPU.createMany({
      data: systemInfo.gpus.map((gpu) => ({
        rigId,
        index: gpu.index,
        name: gpu.name,
        vendor: gpu.vendor,
        vram: gpu.vram,
        busId: gpu.busId,
        uuid: gpu.uuid,
      })),
    });
  }
}
