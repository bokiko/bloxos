import { prisma } from '@bloxos/database';
import { SSHManager } from './ssh-manager.ts';

export interface MinerConfig {
  minerName: string;
  algo: string;
  poolUrl: string;
  walletAddress: string;
  workerName: string;
  extraArgs?: string;
}

export class MinerControl {
  private sshManager: SSHManager;

  constructor() {
    this.sshManager = new SSHManager();
  }

  // Build miner command based on miner type
  buildMinerCommand(config: MinerConfig): string {
    const { minerName, algo, poolUrl, walletAddress, workerName, extraArgs } = config;
    
    const wallet = `${walletAddress}.${workerName}`;
    
    switch (minerName.toLowerCase()) {
      case 't-rex':
        return `t-rex -a ${algo} -o ${poolUrl} -u ${wallet} -p x ${extraArgs || ''} --api-bind-http 0.0.0.0:4067`;
      
      case 'lolminer':
        return `lolMiner -a ${algo} -p ${poolUrl} -u ${wallet} ${extraArgs || ''} --apiport 4068`;
      
      case 'gminer':
        return `miner -a ${algo} -s ${poolUrl} -u ${wallet} ${extraArgs || ''} --api 4069`;
      
      case 'nbminer':
        return `nbminer -a ${algo} -o ${poolUrl} -u ${wallet} ${extraArgs || ''} --api 0.0.0.0:4070`;
      
      case 'teamredminer':
        return `teamredminer -a ${algo} -o ${poolUrl} -u ${wallet} -p x ${extraArgs || ''} --api_listen=0.0.0.0:4071`;
      
      case 'xmrig':
        return `xmrig -a ${algo} -o ${poolUrl} -u ${wallet} -p x ${extraArgs || ''} --http-host=0.0.0.0 --http-port=4072`;
      
      case 'bzminer':
        return `bzminer -a ${algo} -p ${poolUrl} -w ${wallet} ${extraArgs || ''} --http_port 4073`;
      
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

      // Build the miner command
      const minerConfig: MinerConfig = {
        minerName: miner.name,
        algo: miner.algo,
        poolUrl: pool.url,
        walletAddress: wallet.address,
        workerName: rig.name.replace(/\s+/g, '_'),
        extraArgs: flightSheet.extraArgs || miner.defaultArgs || '',
      };

      const minerCommand = this.buildMinerCommand(minerConfig);

      // Check if miner is already running
      const checkCmd = `pgrep -f "${miner.name.toLowerCase()}" || echo "not_running"`;
      const checkResult = await this.sshManager.executeCommandOnRig(rigId, checkCmd);
      
      if (checkResult && checkResult !== 'not_running') {
        return { success: false, message: 'Miner is already running' };
      }

      // Start miner in background with nohup
      const startCmd = `cd /tmp && nohup ${minerCommand} > /tmp/miner.log 2>&1 & echo $!`;
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

      return { success: true, message: `Miner started with PID ${pid.trim()}` };

    } catch (error) {
      console.error('[MinerControl] Start error:', error);
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

      // Kill miner process
      const killCmd = minerInstance.pid 
        ? `kill ${minerInstance.pid} 2>/dev/null || pkill -f "${minerInstance.minerName.toLowerCase()}" || echo "killed"`
        : `pkill -f "${minerInstance.minerName.toLowerCase()}" || echo "killed"`;
      
      await this.sshManager.executeCommandOnRig(rigId, killCmd);

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

    } catch (error) {
      return { running: false };
    }
  }
}

export const minerControl = new MinerControl();
