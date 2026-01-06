import { prisma } from '@bloxos/database';
import { SSHManager } from './ssh-manager.ts';
import {
  validateMinerName,
  validatePoolUrl,
  validateWalletAddress,
  validateExtraArgs,
  escapeShellArg,
  auditLog,
} from '../utils/security.ts';

export interface MinerConfig {
  minerName: string;
  algo: string;
  poolUrl: string;
  walletAddress: string;
  workerName: string;
  extraArgs?: string;
}

// Miner binary paths - must be exact matches
const MINER_PATHS: Record<string, string> = {
  't-rex': '/opt/miners/t-rex/t-rex',
  'lolminer': '/opt/miners/lolminer/lolMiner',
  'gminer': '/opt/miners/gminer/miner',
  'nbminer': '/opt/miners/nbminer/nbminer',
  'teamredminer': '/opt/miners/teamredminer/teamredminer',
  'xmrig': '/opt/miners/xmrig/xmrig',
  'bzminer': '/opt/miners/bzminer/bzminer',
};

// Allowed algorithms (whitelist)
const ALLOWED_ALGOS = new Set([
  'ethash', 'etchash', 'kawpow', 'autolykos2', 'kheavyhash',
  'sha256', 'scrypt', 'x11', 'equihash', 'randomx', 'blake3',
  'octopus', 'ergo', 'flux', 'nexa', 'karlsen', 'pyrinhash',
]);

export class MinerControl {
  private sshManager: SSHManager;

  constructor() {
    this.sshManager = new SSHManager();
  }

  // Build miner command safely with validated inputs
  buildMinerCommand(config: MinerConfig): string {
    const { minerName, algo, poolUrl, walletAddress, workerName, extraArgs } = config;
    
    // Validate all inputs
    validateMinerName(minerName);
    validatePoolUrl(poolUrl);
    validateWalletAddress(walletAddress);
    
    // Validate algo
    if (!ALLOWED_ALGOS.has(algo.toLowerCase())) {
      throw new Error(`Invalid algorithm: ${algo}`);
    }
    
    // Validate worker name (alphanumeric and underscore only)
    const safeWorkerName = workerName.replace(/[^a-zA-Z0-9_-]/g, '_').slice(0, 32);
    
    // Validate and sanitize extra args
    const safeExtraArgs = extraArgs ? validateExtraArgs(extraArgs) : '';
    
    // Get miner path
    const minerPath = MINER_PATHS[minerName.toLowerCase()];
    if (!minerPath) {
      throw new Error(`Unknown miner: ${minerName}`);
    }
    
    // Build wallet string
    const wallet = `${walletAddress}.${safeWorkerName}`;
    
    // Build command using positional args (no shell interpolation)
    switch (minerName.toLowerCase()) {
      case 't-rex':
        return `${minerPath} -a ${algo} -o ${poolUrl} -u ${wallet} -p x ${safeExtraArgs} --api-bind-http 0.0.0.0:4067`.trim();
      
      case 'lolminer':
        return `${minerPath} -a ${algo} -p ${poolUrl} -u ${wallet} ${safeExtraArgs} --apiport 4068`.trim();
      
      case 'gminer':
        return `${minerPath} -a ${algo} -s ${poolUrl} -u ${wallet} ${safeExtraArgs} --api 4069`.trim();
      
      case 'nbminer':
        return `${minerPath} -a ${algo} -o ${poolUrl} -u ${wallet} ${safeExtraArgs} --api 0.0.0.0:4070`.trim();
      
      case 'teamredminer':
        return `${minerPath} -a ${algo} -o ${poolUrl} -u ${wallet} -p x ${safeExtraArgs} --api_listen=0.0.0.0:4071`.trim();
      
      case 'xmrig':
        return `${minerPath} -a ${algo} -o ${poolUrl} -u ${wallet} -p x ${safeExtraArgs} --http-host=0.0.0.0 --http-port=4072`.trim();
      
      case 'bzminer':
        return `${minerPath} -a ${algo} -p ${poolUrl} -w ${wallet} ${safeExtraArgs} --http_port 4073`.trim();
      
      default:
        throw new Error(`Unknown miner: ${minerName}`);
    }
  }

  // Start miner on a rig
  async startMiner(rigId: string): Promise<{ success: boolean; message: string }> {
    try {
      // Get rig with flight sheet info
      const rig = await prisma.rig.findUnique({
        where: { id: rigId },
        include: {
          flightSheet: {
            include: {
              wallet: true,
              pool: true,
              miner: true,
            },
          },
        },
      });

      if (!rig) {
        return { success: false, message: 'Rig not found' };
      }

      if (!rig.flightSheet) {
        return { success: false, message: 'No flight sheet assigned to this rig' };
      }

      const { flightSheet } = rig;
      const { wallet, pool, miner } = flightSheet;

      // Build the miner command (validates inputs internally)
      const minerConfig: MinerConfig = {
        minerName: miner.name,
        algo: miner.algo,
        poolUrl: pool.url,
        walletAddress: wallet.address,
        workerName: rig.name.replace(/[^a-zA-Z0-9_-]/g, '_'),
        extraArgs: flightSheet.extraArgs || miner.defaultArgs || '',
      };

      const minerCommand = this.buildMinerCommand(minerConfig);

      // Check if miner is already running
      const minerPath = MINER_PATHS[miner.name.toLowerCase()];
      const checkCmd = `pgrep -f ${escapeShellArg(minerPath)} || echo "not_running"`;
      const checkResult = await this.sshManager.executeCommandOnRig(rigId, checkCmd);
      
      if (checkResult && checkResult !== 'not_running') {
        return { success: false, message: 'Miner is already running' };
      }

      // Start miner in background with nohup
      // Use /opt/miners/logs for miner output
      const startCmd = `mkdir -p /opt/miners/logs && cd /opt/miners && nohup ${minerCommand} > /opt/miners/logs/miner.log 2>&1 & echo $!`;
      const pid = await this.sshManager.executeCommandOnRig(rigId, startCmd);

      // Create or update miner instance record
      await prisma.minerInstance.upsert({
        where: { 
          id: `${rigId}-${miner.name}`,
        },
        create: {
          id: `${rigId}-${miner.name}`,
          rigId,
          minerName: miner.name,
          algo: miner.algo,
          pool: pool.url,
          wallet: wallet.address,
          status: 'RUNNING',
          pid: parseInt(pid.trim(), 10) || null,
          startedAt: new Date(),
        },
        update: {
          status: 'RUNNING',
          pid: parseInt(pid.trim(), 10) || null,
          startedAt: new Date(),
        },
      });

      // Log event
      await prisma.rigEvent.create({
        data: {
          rigId,
          type: 'MINER_STARTED',
          severity: 'INFO',
          message: `Started ${miner.name} mining ${flightSheet.coin} on ${pool.name}`,
        },
      });

      auditLog({
        action: 'start_miner',
        resource: 'rig',
        resourceId: rigId,
        details: { miner: miner.name, algo: miner.algo, pool: pool.name },
        success: true,
      });

      return { success: true, message: `Miner started with PID ${pid.trim()}` };

    } catch (error) {
      console.error('[MinerControl] Start error:', error);
      auditLog({
        action: 'start_miner',
        resource: 'rig',
        resourceId: rigId,
        success: false,
        error: error instanceof Error ? error.message : 'Unknown error',
      });
      return { 
        success: false, 
        message: error instanceof Error ? error.message : 'Failed to start miner' 
      };
    }
  }

  // Stop miner on a rig
  async stopMiner(rigId: string): Promise<{ success: boolean; message: string }> {
    try {
      // Get active miner instance
      const minerInstance = await prisma.minerInstance.findFirst({
        where: { rigId, status: 'RUNNING' },
      });

      if (!minerInstance) {
        return { success: false, message: 'No running miner found' };
      }

      // Kill miner process by PID (safer than pkill with user input)
      if (minerInstance.pid) {
        const killCmd = `kill ${minerInstance.pid} 2>/dev/null || echo "process_not_found"`;
        await this.sshManager.executeCommandOnRig(rigId, killCmd);
      }
      
      // Also kill by miner path as fallback
      const minerPath = MINER_PATHS[minerInstance.minerName.toLowerCase()];
      if (minerPath) {
        const pkillCmd = `pkill -f ${escapeShellArg(minerPath)} 2>/dev/null || echo "killed"`;
        await this.sshManager.executeCommandOnRig(rigId, pkillCmd);
      }

      // Update miner instance
      await prisma.minerInstance.update({
        where: { id: minerInstance.id },
        data: {
          status: 'STOPPED',
          pid: null,
        },
      });

      // Log event
      await prisma.rigEvent.create({
        data: {
          rigId,
          type: 'MINER_STOPPED',
          severity: 'INFO',
          message: `Stopped ${minerInstance.minerName}`,
        },
      });

      auditLog({
        action: 'stop_miner',
        resource: 'rig',
        resourceId: rigId,
        details: { miner: minerInstance.minerName },
        success: true,
      });

      return { success: true, message: 'Miner stopped' };

    } catch (error) {
      console.error('[MinerControl] Stop error:', error);
      return { 
        success: false, 
        message: error instanceof Error ? error.message : 'Failed to stop miner' 
      };
    }
  }

  // Get miner status
  async getMinerStatus(rigId: string): Promise<{ running: boolean; pid?: number; minerName?: string }> {
    try {
      const minerInstance = await prisma.minerInstance.findFirst({
        where: { rigId, status: 'RUNNING' },
      });

      if (!minerInstance) {
        return { running: false };
      }

      // Verify process is actually running
      if (minerInstance.pid) {
        const checkCmd = `ps -p ${minerInstance.pid} -o pid= 2>/dev/null || echo "dead"`;
        const result = await this.sshManager.executeCommandOnRig(rigId, checkCmd);
        
        if (result.trim() === 'dead') {
          // Process died, update record
          await prisma.minerInstance.update({
            where: { id: minerInstance.id },
            data: { status: 'STOPPED', pid: null },
          });
          return { running: false };
        }
      }

      return { 
        running: true, 
        pid: minerInstance.pid || undefined,
        minerName: minerInstance.minerName,
      };

    } catch {
      return { running: false };
    }
  }
}

export const minerControl = new MinerControl();
