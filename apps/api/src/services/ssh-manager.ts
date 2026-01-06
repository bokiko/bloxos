import { Client, type ConnectConfig, type ClientChannel } from 'ssh2';
import { prisma } from '@bloxos/database';
import { nanoid } from 'nanoid';
import { encrypt, decrypt } from '../utils/encryption.ts';

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

  async executeCommand(credentials: SSHCredentials, command: string): Promise<string> {
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

  async getSystemInfo(credentials: SSHCredentials): Promise<SystemInfo> {
    // Gather system information via SSH commands
    const commands = {
      hostname: 'hostname',
      os: 'cat /etc/os-release | grep "^NAME=" | cut -d= -f2 | tr -d \'"\'',
      osVersion: 'cat /etc/os-release | grep "^VERSION_ID=" | cut -d= -f2 | tr -d \'"\'',
      kernel: 'uname -r',
      cpuModel: 'cat /proc/cpuinfo | grep "model name" | head -1 | cut -d: -f2 | xargs',
      cpuCores: 'nproc',
      ramTotal: 'free -b | grep Mem | awk \'{print $2}\'',
      ipAddresses: 'hostname -I',
      // Check for NVIDIA GPUs
      nvidiaGpus: 'nvidia-smi --query-gpu=index,name,memory.total,pci.bus_id,uuid --format=csv,noheader,nounits 2>/dev/null || echo ""',
      // Check for AMD GPUs
      amdGpus: 'rocm-smi --showid --showproductname --showmeminfo vram 2>/dev/null | grep -E "GPU\\[|Card series|VRAM Total" || echo ""',
    };

    const results: Record<string, string> = {};

    for (const [key, cmd] of Object.entries(commands)) {
      try {
        results[key] = await this.executeCommand(credentials, cmd);
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

    // Parse AMD GPUs (simplified - rocm-smi output is more complex)
    // TODO: Better AMD parsing

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
  }): Promise<unknown> {
    const { name, farmId, credentials } = options;

    // Step 1: Test connection
    const connected = await this.testConnection(credentials);
    if (!connected) {
      throw new Error('Cannot connect to host via SSH');
    }

    // Step 2: Gather system info
    const systemInfo = await this.getSystemInfo(credentials);

    // Step 3: Generate token for this rig
    const token = nanoid(32);

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

    // Step 5: Install agent on the rig (TODO: implement agent)
    // For now, we just return the rig info

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

  // Execute command on a rig using stored credentials
  async executeCommandOnRig(rigId: string, command: string): Promise<string> {
    const credentials = await this.getCredentialsForRig(rigId);
    if (!credentials) {
      throw new Error('No SSH credentials found for this rig');
    }
    return this.executeCommand(credentials, command);
  }

  // Execute command with sudo using the SSH password
  async executeSudoCommandOnRig(rigId: string, command: string): Promise<string> {
    const credentials = await this.getCredentialsForRig(rigId);
    if (!credentials) {
      throw new Error('No SSH credentials found for this rig');
    }
    
    if (!credentials.password) {
      // No password, try without sudo -S
      return this.executeCommand(credentials, command);
    }

    // Use echo PASSWORD | sudo -S to pipe password to sudo
    const sudoCommand = `echo '${credentials.password}' | sudo -S ${command} 2>/dev/null`;
    return this.executeCommand(credentials, sudoCommand);
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
