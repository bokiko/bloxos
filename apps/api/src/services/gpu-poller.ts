import { prisma } from '@bloxos/database';
import { SSHManager } from './ssh-manager.ts';
import { alertService } from './alert-service.ts';

interface GPUStats {
  index: number;
  temperature: number | null;
  memTemp: number | null;
  fanSpeed: number | null;
  powerDraw: number | null;
  coreClock: number | null;
  memoryClock: number | null;
  utilization: number | null;
}

interface CPUInfo {
  model: string;
  vendor: string;
  cores: number;
  threads: number;
  maxFrequency: number | null;
}

interface CPUStats {
  temperature: number | null;
  usage: number | null;
  frequency: number | null;
  powerDraw: number | null;
}

export class GPUPoller {
  private sshManager: SSHManager;
  private intervalId: NodeJS.Timeout | null = null;
  private isPolling = false;
  private pollInterval: number;
  
  // Store previous CPU stats for usage and power calculation
  private prevCpuStats: Map<string, { idle: number; total: number }> = new Map();
  private prevCpuEnergy: Map<string, { energy: number; timestamp: number }> = new Map();

  constructor(pollIntervalMs = 30000) {
    this.sshManager = new SSHManager();
    this.pollInterval = pollIntervalMs;
  }

  // Parse nvidia-smi CSV output
  parseNvidiaSmiOutput(output: string): GPUStats[] {
    const gpus: GPUStats[] = [];
    const lines = output.trim().split('\n');

    for (const line of lines) {
      const parts = line.split(',').map((p) => p.trim());
      if (parts.length < 7) continue;

      const parseNum = (val: string): number | null => {
        if (val === 'N/A' || val === '[N/A]' || val === '') return null;
        const num = parseFloat(val);
        return isNaN(num) ? null : num;
      };

      gpus.push({
        index: gpus.length,
        temperature: parseNum(parts[0]),
        memTemp: parseNum(parts[1]),
        fanSpeed: parseNum(parts[2]),
        powerDraw: parseNum(parts[3]),
        coreClock: parseNum(parts[4]),
        memoryClock: parseNum(parts[5]),
        utilization: parseNum(parts[6]),
      });
    }

    return gpus;
  }

  // Parse CPU info from lscpu
  parseCPUInfo(output: string): CPUInfo | null {
    const lines = output.trim().split('\n');
    let model = '';
    let vendor = '';
    let cores = 0;
    let threads = 0;
    let maxFreq: number | null = null;

    for (const line of lines) {
      if (line.includes('Model name:')) {
        model = line.split(':')[1]?.trim() || '';
        // Extract vendor from model name
        if (model.toLowerCase().includes('amd')) vendor = 'AMD';
        else if (model.toLowerCase().includes('intel')) vendor = 'Intel';
        else vendor = 'Unknown';
      }
      if (line.includes('Core(s) per socket:')) {
        cores = parseInt(line.split(':')[1]?.trim() || '0', 10);
      }
      if (line.includes('Thread(s) per core:')) {
        const threadsPerCore = parseInt(line.split(':')[1]?.trim() || '1', 10);
        // Threads will be calculated after cores is set
        if (cores > 0) {
          threads = cores * threadsPerCore;
        }
      }
      // Also check for siblings (total threads)
      if (line.includes('CPU(s):') && !line.includes('NUMA') && !line.includes('On-line')) {
        threads = parseInt(line.split(':')[1]?.trim() || '0', 10);
      }
      if (line.includes('CPU max MHz:')) {
        maxFreq = Math.round(parseFloat(line.split(':')[1]?.trim() || '0'));
      }
    }

    if (!model) return null;

    return { model, vendor, cores, threads, maxFrequency: maxFreq };
  }

  // Parse CPU stats
  parseCPUStats(
    rigId: string,
    statOutput: string,
    freqOutput: string,
    tempOutput: string,
    energyOutput: string
  ): CPUStats {
    // Parse /proc/stat for CPU usage
    // Format: cpu user nice system idle iowait irq softirq steal guest guest_nice
    const statParts = statOutput.trim().split(/\s+/);
    let usage: number | null = null;

    if (statParts.length >= 5) {
      const user = parseInt(statParts[1], 10);
      const nice = parseInt(statParts[2], 10);
      const system = parseInt(statParts[3], 10);
      const idle = parseInt(statParts[4], 10);
      const iowait = parseInt(statParts[5] || '0', 10);
      const irq = parseInt(statParts[6] || '0', 10);
      const softirq = parseInt(statParts[7] || '0', 10);
      const steal = parseInt(statParts[8] || '0', 10);

      const total = user + nice + system + idle + iowait + irq + softirq + steal;
      const idleTotal = idle + iowait;

      const prev = this.prevCpuStats.get(rigId);
      if (prev) {
        const totalDiff = total - prev.total;
        const idleDiff = idleTotal - prev.idle;
        if (totalDiff > 0) {
          usage = Math.round(((totalDiff - idleDiff) / totalDiff) * 100 * 10) / 10;
        }
      }

      this.prevCpuStats.set(rigId, { idle: idleTotal, total });
    }

    // Parse frequency from /proc/cpuinfo
    let frequency: number | null = null;
    const freqMatch = freqOutput.match(/cpu MHz\s*:\s*([\d.]+)/);
    if (freqMatch) {
      frequency = Math.round(parseFloat(freqMatch[1]));
    }

    // Parse temperature from hwmon (in millidegrees)
    let temperature: number | null = null;
    const tempValue = parseInt(tempOutput.trim(), 10);
    if (!isNaN(tempValue)) {
      temperature = Math.round(tempValue / 1000);
    }

    // Parse power from RAPL energy_uj (microjoules)
    let powerDraw: number | null = null;
    const energyUj = parseInt(energyOutput.trim(), 10);
    if (!isNaN(energyUj)) {
      const now = Date.now();
      const prev = this.prevCpuEnergy.get(rigId);
      if (prev) {
        const timeDiffSec = (now - prev.timestamp) / 1000;
        const energyDiffUj = energyUj - prev.energy;
        if (timeDiffSec > 0 && energyDiffUj > 0) {
          // Convert microjoules to watts: W = J/s = uJ / (s * 1000000)
          powerDraw = Math.round(energyDiffUj / (timeDiffSec * 1000000));
        }
      }
      this.prevCpuEnergy.set(rigId, { energy: energyUj, timestamp: now });
    }

    return { temperature, usage, frequency, powerDraw };
  }

  // Get CPU info and create/update CPU record
  async pollCPUInfo(rigId: string): Promise<void> {
    try {
      const output = await this.sshManager.executeCommandOnRig(
        rigId,
        'lscpu | grep -E "Model name|Core|Thread|MHz"'
      );
      
      const cpuInfo = this.parseCPUInfo(output);
      if (!cpuInfo) return;

      // Check if CPU record exists
      const existingCpu = await prisma.cPU.findUnique({
        where: { rigId },
      });

      if (existingCpu) {
        // Update if model changed
        if (existingCpu.model !== cpuInfo.model) {
          await prisma.cPU.update({
            where: { rigId },
            data: cpuInfo,
          });
        }
      } else {
        // Create new CPU record
        await prisma.cPU.create({
          data: {
            rigId,
            ...cpuInfo,
          },
        });
      }
    } catch (error) {
      console.error(`[GPUPoller] Failed to get CPU info for rig ${rigId}:`, error);
    }
  }

  // Poll CPU stats
  async pollCPUStats(rigId: string): Promise<void> {
    try {
      // Run all commands in parallel
      const [statOutput, freqOutput, tempOutput, energyOutput] = await Promise.all([
        this.sshManager.executeCommandOnRig(rigId, 'grep "^cpu " /proc/stat'),
        this.sshManager.executeCommandOnRig(rigId, 'grep "cpu MHz" /proc/cpuinfo | head -1'),
        this.sshManager.executeCommandOnRig(rigId, 'cat /sys/class/hwmon/hwmon0/temp1_input 2>/dev/null || cat /sys/class/hwmon/hwmon1/temp1_input 2>/dev/null || echo "0"'),
        this.sshManager.executeSudoCommandOnRig(rigId, 'cat /sys/class/powercap/intel-rapl:0/energy_uj 2>/dev/null || echo "0"'),
      ]);

      const stats = this.parseCPUStats(rigId, statOutput, freqOutput, tempOutput, energyOutput);

      // Update CPU stats
      await prisma.cPU.update({
        where: { rigId },
        data: {
          temperature: stats.temperature,
          usage: stats.usage,
          frequency: stats.frequency,
          powerDraw: stats.powerDraw,
        },
      });
    } catch (error) {
      // Ignore errors - CPU might not exist yet
    }
  }

  // Poll a single rig for GPU stats
  async pollGPUStats(rigId: string): Promise<{ success: boolean; error?: string }> {
    try {
      const command = `nvidia-smi --query-gpu=temperature.gpu,temperature.memory,fan.speed,power.draw,clocks.gr,clocks.mem,utilization.gpu --format=csv,noheader,nounits`;
      
      const output = await this.sshManager.executeCommandOnRig(rigId, command);
      const stats = this.parseNvidiaSmiOutput(output);

      if (stats.length === 0) {
        return { success: false, error: 'No GPU stats parsed' };
      }

      // Get existing GPUs for this rig
      const existingGpus = await prisma.gPU.findMany({
        where: { rigId },
        orderBy: { index: 'asc' },
      });

      // Update each GPU with new stats
      for (const stat of stats) {
        const gpu = existingGpus.find((g) => g.index === stat.index);
        if (gpu) {
          await prisma.gPU.update({
            where: { id: gpu.id },
            data: {
              temperature: stat.temperature,
              memTemp: stat.memTemp,
              fanSpeed: stat.fanSpeed,
              powerDraw: stat.powerDraw,
              coreClock: stat.coreClock,
              memoryClock: stat.memoryClock,
            },
          });
        }
      }

      return { success: true };
    } catch (error) {
      return {
        success: false,
        error: error instanceof Error ? error.message : 'Unknown error',
      };
    }
  }

  // Poll a single rig
  async pollRig(rigId: string): Promise<{ success: boolean; error?: string }> {
    try {
      // Get rig settings
      const rig = await prisma.rig.findUnique({
        where: { id: rigId },
        select: { name: true, cpuMiningEnabled: true, gpuMiningEnabled: true },
      });

      if (!rig) {
        return { success: false, error: 'Rig not found' };
      }

      const results: { gpu?: boolean; cpu?: boolean } = {};

      // Poll GPU if enabled
      if (rig.gpuMiningEnabled) {
        const gpuResult = await this.pollGPUStats(rigId);
        results.gpu = gpuResult.success;
      }

      // Poll CPU if enabled
      if (rig.cpuMiningEnabled) {
        // First ensure CPU info exists
        await this.pollCPUInfo(rigId);
        await this.pollCPUStats(rigId);
        results.cpu = true;
      }

      // Update rig lastSeen and status
      await prisma.rig.update({
        where: { id: rigId },
        data: {
          lastSeen: new Date(),
          status: 'ONLINE',
        },
      });

      // Run alert checks after successful poll
      await this.runAlertChecks(rigId, rig.name);

      return { success: true };
    } catch (error) {
      // Mark rig as offline if we can't reach it
      try {
        await prisma.rig.update({
          where: { id: rigId },
          data: { status: 'OFFLINE' },
        });
      } catch {}

      return {
        success: false,
        error: error instanceof Error ? error.message : 'Unknown error',
      };
    }
  }

  // Run alert checks for a rig
  private async runAlertChecks(rigId: string, rigName: string): Promise<void> {
    try {
      // Get current GPU and CPU stats
      const gpus = await prisma.gPU.findMany({
        where: { rigId },
        select: { index: true, temperature: true, hashrate: true },
      });

      const cpu = await prisma.cPU.findUnique({
        where: { rigId },
        select: { temperature: true },
      });

      // Calculate total hashrate
      const totalHashrate = gpus.reduce((sum, gpu) => sum + (gpu.hashrate || 0), 0);

      // Run alert checks
      await alertService.checkRigAlerts(
        rigId,
        rigName,
        gpus.map(g => ({ index: g.index, temperature: g.temperature })),
        cpu?.temperature || null,
        totalHashrate
      );
    } catch (error) {
      console.error(`[Poller] Error running alert checks for ${rigName}:`, error);
    }
  }

  // Poll all online rigs
  async pollAllRigs(): Promise<void> {
    if (this.isPolling) {
      console.log('[Poller] Already polling, skipping...');
      return;
    }

    this.isPolling = true;
    console.log('[Poller] Starting poll cycle...');

    try {
      // Get all rigs that have SSH credentials
      const rigs = await prisma.rig.findMany({
        where: {
          sshCredential: { isNot: null },
        },
        select: { id: true, name: true, status: true, cpuMiningEnabled: true, gpuMiningEnabled: true },
      });

      console.log(`[Poller] Found ${rigs.length} rigs with SSH credentials`);

      // Poll each rig
      const results = await Promise.allSettled(
        rigs.map(async (rig) => {
          const result = await this.pollRig(rig.id);
          const mode = [
            rig.gpuMiningEnabled ? 'GPU' : '',
            rig.cpuMiningEnabled ? 'CPU' : '',
          ].filter(Boolean).join('+') || 'None';
          
          if (result.success) {
            console.log(`[Poller] ${rig.name} (${mode}): OK`);
          } else {
            console.log(`[Poller] ${rig.name} (${mode}): FAILED - ${result.error}`);
          }
          return { rigId: rig.id, ...result };
        })
      );

      const successful = results.filter(
        (r) => r.status === 'fulfilled' && r.value.success
      ).length;
      const failed = results.length - successful;

      console.log(`[Poller] Poll cycle complete: ${successful} OK, ${failed} failed`);

      // Check for offline rigs and create alerts
      await alertService.checkOfflineRigs();
    } catch (error) {
      console.error('[Poller] Poll cycle error:', error);
    } finally {
      this.isPolling = false;
    }
  }

  // Start the polling loop
  start(): void {
    if (this.intervalId) {
      console.log('[Poller] Already running');
      return;
    }

    console.log(`[Poller] Starting with ${this.pollInterval / 1000}s interval`);

    // Run immediately on start
    this.pollAllRigs();

    // Then run at interval
    this.intervalId = setInterval(() => {
      this.pollAllRigs();
    }, this.pollInterval);
  }

  // Stop the polling loop
  stop(): void {
    if (this.intervalId) {
      clearInterval(this.intervalId);
      this.intervalId = null;
      console.log('[Poller] Stopped');
    }
  }

  // Check if poller is running
  isRunning(): boolean {
    return this.intervalId !== null;
  }
}

// Singleton instance
export const gpuPoller = new GPUPoller(30000); // 30 seconds
