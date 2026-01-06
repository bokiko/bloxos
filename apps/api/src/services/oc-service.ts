import { prisma } from '@bloxos/database';
import { SSHManager } from './ssh-manager.ts';

interface OCProfile {
  id: string;
  name: string;
  vendor: string;
  powerLimit: number | null;
  coreOffset: number | null;
  memOffset: number | null;
  coreLock: number | null;
  memLock: number | null;
  fanSpeed: number | null;
}

export class OCService {
  private sshManager: SSHManager;

  constructor() {
    this.sshManager = new SSHManager();
  }

  // Apply OC profile to a rig
  async applyOCProfile(rigId: string): Promise<{ success: boolean; message: string; details?: string[] }> {
    try {
      // Get rig with OC profile
      const rig = await prisma.rig.findUnique({
        where: { id: rigId },
        include: {
          ocProfile: true,
          gpus: true,
        },
      });

      if (!rig) {
        return { success: false, message: 'Rig not found' };
      }

      if (!rig.ocProfile) {
        return { success: false, message: 'No OC profile assigned to this rig' };
      }

      const profile = rig.ocProfile as OCProfile;
      const gpuCount = rig.gpus.length;

      if (gpuCount === 0) {
        return { success: false, message: 'No GPUs detected on this rig' };
      }

      const details: string[] = [];

      // Apply settings based on vendor
      if (profile.vendor === 'NVIDIA') {
        await this.applyNvidiaOC(rigId, profile, gpuCount, details);
      } else if (profile.vendor === 'AMD') {
        await this.applyAmdOC(rigId, profile, gpuCount, details);
      } else {
        return { success: false, message: `Unsupported GPU vendor: ${profile.vendor}` };
      }

      // Create event for the OC apply
      await prisma.rigEvent.create({
        data: {
          rigId,
          type: 'CONFIG_CHANGED',
          severity: 'INFO',
          message: `Applied OC profile: ${profile.name}`,
          data: { profileId: profile.id, settings: JSON.parse(JSON.stringify(profile)) },
        },
      });

      return { 
        success: true, 
        message: `OC profile "${profile.name}" applied successfully`,
        details 
      };
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Unknown error';
      return { success: false, message: `Failed to apply OC: ${message}` };
    }
  }

  // Apply NVIDIA OC settings
  private async applyNvidiaOC(
    rigId: string, 
    profile: OCProfile, 
    gpuCount: number,
    details: string[]
  ): Promise<void> {
    const commands: string[] = [];

    // Enable persistence mode
    commands.push('nvidia-smi -pm 1');

    // Apply settings to all GPUs
    for (let i = 0; i < gpuCount; i++) {
      // Power limit (in watts)
      if (profile.powerLimit !== null) {
        commands.push(`nvidia-smi -i ${i} -pl ${profile.powerLimit}`);
        details.push(`GPU ${i}: Power limit set to ${profile.powerLimit}W`);
      }

      // Fan speed (requires X server or coolbits)
      if (profile.fanSpeed !== null && profile.fanSpeed > 0) {
        // Use nvidia-settings for fan control
        commands.push(`nvidia-settings -a "[gpu:${i}]/GPUFanControlState=1"`);
        commands.push(`nvidia-settings -a "[fan:${i}]/GPUTargetFanSpeed=${profile.fanSpeed}"`);
        details.push(`GPU ${i}: Fan speed set to ${profile.fanSpeed}%`);
      } else if (profile.fanSpeed === 0) {
        commands.push(`nvidia-settings -a "[gpu:${i}]/GPUFanControlState=0"`);
        details.push(`GPU ${i}: Fan set to auto`);
      }

      // Core clock offset (requires coolbits)
      if (profile.coreOffset !== null) {
        commands.push(`nvidia-settings -a "[gpu:${i}]/GPUGraphicsClockOffsetAllPerformanceLevels=${profile.coreOffset}"`);
        details.push(`GPU ${i}: Core offset set to ${profile.coreOffset} MHz`);
      }

      // Memory clock offset (requires coolbits)
      if (profile.memOffset !== null) {
        commands.push(`nvidia-settings -a "[gpu:${i}]/GPUMemoryTransferRateOffsetAllPerformanceLevels=${profile.memOffset}"`);
        details.push(`GPU ${i}: Memory offset set to ${profile.memOffset} MHz`);
      }

      // Lock core clock (newer GPUs)
      if (profile.coreLock !== null) {
        commands.push(`nvidia-smi -i ${i} -lgc ${profile.coreLock}`);
        details.push(`GPU ${i}: Core clock locked to ${profile.coreLock} MHz`);
      }

      // Lock memory clock (newer GPUs)
      if (profile.memLock !== null) {
        commands.push(`nvidia-smi -i ${i} -lmc ${profile.memLock}`);
        details.push(`GPU ${i}: Memory clock locked to ${profile.memLock} MHz`);
      }
    }

    // Execute all commands
    for (const cmd of commands) {
      try {
        await this.sshManager.executeSudoCommandOnRig(rigId, cmd);
      } catch (error) {
        console.error(`[OCService] Command failed: ${cmd}`, error);
        // Continue with other commands even if one fails
      }
    }
  }

  // Apply AMD OC settings (using rocm-smi or amdgpu-pro)
  private async applyAmdOC(
    rigId: string, 
    profile: OCProfile, 
    gpuCount: number,
    details: string[]
  ): Promise<void> {
    const commands: string[] = [];

    for (let i = 0; i < gpuCount; i++) {
      // Power limit (as percentage)
      if (profile.powerLimit !== null) {
        commands.push(`rocm-smi -d ${i} --setpoweroverdrive ${profile.powerLimit}`);
        details.push(`GPU ${i}: Power limit set to ${profile.powerLimit}W`);
      }

      // Fan speed
      if (profile.fanSpeed !== null && profile.fanSpeed > 0) {
        commands.push(`rocm-smi -d ${i} --setfan ${profile.fanSpeed}`);
        details.push(`GPU ${i}: Fan speed set to ${profile.fanSpeed}%`);
      } else if (profile.fanSpeed === 0) {
        commands.push(`rocm-smi -d ${i} --resetfan`);
        details.push(`GPU ${i}: Fan set to auto`);
      }

      // Core voltage
      if ((profile as any).coreVddc !== null) {
        commands.push(`rocm-smi -d ${i} --setsclkdpm ${(profile as any).coreDpm || 7} --setvddgfx ${(profile as any).coreVddc}`);
        details.push(`GPU ${i}: Core voltage set to ${(profile as any).coreVddc} mV`);
      }
    }

    // Execute all commands
    for (const cmd of commands) {
      try {
        await this.sshManager.executeSudoCommandOnRig(rigId, cmd);
      } catch (error) {
        console.error(`[OCService] Command failed: ${cmd}`, error);
      }
    }
  }

  // Reset OC settings to default
  async resetOC(rigId: string): Promise<{ success: boolean; message: string }> {
    try {
      const rig = await prisma.rig.findUnique({
        where: { id: rigId },
        include: { gpus: true },
      });

      if (!rig || rig.gpus.length === 0) {
        return { success: false, message: 'Rig or GPUs not found' };
      }

      const gpuVendor = rig.gpus[0].vendor;

      if (gpuVendor === 'NVIDIA') {
        // Reset NVIDIA
        await this.sshManager.executeSudoCommandOnRig(rigId, 'nvidia-smi -rgc'); // Reset GPU clocks
        await this.sshManager.executeSudoCommandOnRig(rigId, 'nvidia-smi -rmc'); // Reset memory clocks
        // Reset power limit to default
        await this.sshManager.executeSudoCommandOnRig(rigId, 'nvidia-smi -pl 0'); // 0 = default
      } else if (gpuVendor === 'AMD') {
        await this.sshManager.executeSudoCommandOnRig(rigId, 'rocm-smi --resetclocks');
        await this.sshManager.executeSudoCommandOnRig(rigId, 'rocm-smi --resetfan');
      }

      // Create event
      await prisma.rigEvent.create({
        data: {
          rigId,
          type: 'CONFIG_CHANGED',
          severity: 'INFO',
          message: 'OC settings reset to default',
        },
      });

      return { success: true, message: 'OC settings reset to default' };
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Unknown error';
      return { success: false, message: `Failed to reset OC: ${message}` };
    }
  }
}

// Singleton instance
export const ocService = new OCService();
