import { prisma } from '@bloxos/database';
import { SSHManager } from './ssh-manager.ts';
import { validateOCValue, auditLog } from '../utils/security.ts';

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

  // Validate OC profile values
  private validateProfile(profile: OCProfile): void {
    const vendor = profile.vendor;
    
    if (profile.powerLimit !== null) {
      validateOCValue(vendor, 'powerLimit', profile.powerLimit);
    }
    if (profile.coreOffset !== null) {
      validateOCValue(vendor, 'coreOffset', profile.coreOffset);
    }
    if (profile.memOffset !== null) {
      validateOCValue(vendor, 'memOffset', profile.memOffset);
    }
    if (profile.coreLock !== null) {
      validateOCValue(vendor, 'coreLock', profile.coreLock);
    }
    if (profile.memLock !== null) {
      validateOCValue(vendor, 'memLock', profile.memLock);
    }
    if (profile.fanSpeed !== null) {
      validateOCValue(vendor, 'fanSpeed', profile.fanSpeed);
    }
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

      // Validate all OC values before applying
      try {
        this.validateProfile(profile);
      } catch (error) {
        return { 
          success: false, 
          message: error instanceof Error ? error.message : 'Invalid OC profile values' 
        };
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

      auditLog({
        action: 'apply_oc',
        resource: 'rig',
        resourceId: rigId,
        details: { profile: profile.name, vendor: profile.vendor },
        success: true,
      });

      return { 
        success: true, 
        message: `OC profile "${profile.name}" applied successfully`,
        details 
      };
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Unknown error';
      auditLog({
        action: 'apply_oc',
        resource: 'rig',
        resourceId: rigId,
        success: false,
        error: message,
      });
      return { success: false, message: `Failed to apply OC: ${message}` };
    }
  }

  // Apply NVIDIA OC settings with validated numeric values
  private async applyNvidiaOC(
    rigId: string, 
    profile: OCProfile, 
    gpuCount: number,
    details: string[]
  ): Promise<void> {
    // Enable persistence mode first
    try {
      await this.sshManager.executeSudoCommandOnRig(rigId, 'nvidia-smi -pm 1');
    } catch (error) {
      console.error('[OCService] Failed to enable persistence mode:', error);
    }

    // Apply settings to all GPUs
    for (let i = 0; i < gpuCount; i++) {
      // Power limit (in watts) - validated to be within safe range
      if (profile.powerLimit !== null) {
        const pl = Math.round(profile.powerLimit);
        try {
          await this.sshManager.executeSudoCommandOnRig(rigId, `nvidia-smi -i ${i} -pl ${pl}`);
          details.push(`GPU ${i}: Power limit set to ${pl}W`);
        } catch (error) {
          console.error(`[OCService] Failed to set power limit for GPU ${i}:`, error);
        }
      }

      // Fan speed (requires X server or coolbits)
      if (profile.fanSpeed !== null) {
        const fan = Math.round(profile.fanSpeed);
        try {
          if (fan > 0) {
            await this.sshManager.executeSudoCommandOnRig(rigId, `nvidia-settings -a "[gpu:${i}]/GPUFanControlState=1"`);
            await this.sshManager.executeSudoCommandOnRig(rigId, `nvidia-settings -a "[fan:${i}]/GPUTargetFanSpeed=${fan}"`);
            details.push(`GPU ${i}: Fan speed set to ${fan}%`);
          } else {
            await this.sshManager.executeSudoCommandOnRig(rigId, `nvidia-settings -a "[gpu:${i}]/GPUFanControlState=0"`);
            details.push(`GPU ${i}: Fan set to auto`);
          }
        } catch (error) {
          console.error(`[OCService] Failed to set fan speed for GPU ${i}:`, error);
        }
      }

      // Core clock offset (requires coolbits)
      if (profile.coreOffset !== null) {
        const offset = Math.round(profile.coreOffset);
        try {
          await this.sshManager.executeSudoCommandOnRig(rigId, `nvidia-settings -a "[gpu:${i}]/GPUGraphicsClockOffsetAllPerformanceLevels=${offset}"`);
          details.push(`GPU ${i}: Core offset set to ${offset} MHz`);
        } catch (error) {
          console.error(`[OCService] Failed to set core offset for GPU ${i}:`, error);
        }
      }

      // Memory clock offset (requires coolbits)
      if (profile.memOffset !== null) {
        const offset = Math.round(profile.memOffset);
        try {
          await this.sshManager.executeSudoCommandOnRig(rigId, `nvidia-settings -a "[gpu:${i}]/GPUMemoryTransferRateOffsetAllPerformanceLevels=${offset}"`);
          details.push(`GPU ${i}: Memory offset set to ${offset} MHz`);
        } catch (error) {
          console.error(`[OCService] Failed to set memory offset for GPU ${i}:`, error);
        }
      }

      // Lock core clock (newer GPUs)
      if (profile.coreLock !== null) {
        const lock = Math.round(profile.coreLock);
        try {
          await this.sshManager.executeSudoCommandOnRig(rigId, `nvidia-smi -i ${i} -lgc ${lock}`);
          details.push(`GPU ${i}: Core clock locked to ${lock} MHz`);
        } catch (error) {
          console.error(`[OCService] Failed to lock core clock for GPU ${i}:`, error);
        }
      }

      // Lock memory clock (newer GPUs)
      if (profile.memLock !== null) {
        const lock = Math.round(profile.memLock);
        try {
          await this.sshManager.executeSudoCommandOnRig(rigId, `nvidia-smi -i ${i} -lmc ${lock}`);
          details.push(`GPU ${i}: Memory clock locked to ${lock} MHz`);
        } catch (error) {
          console.error(`[OCService] Failed to lock memory clock for GPU ${i}:`, error);
        }
      }
    }
  }

  // Apply AMD OC settings with validated numeric values
  private async applyAmdOC(
    rigId: string, 
    profile: OCProfile, 
    gpuCount: number,
    details: string[]
  ): Promise<void> {
    for (let i = 0; i < gpuCount; i++) {
      // Power limit (as percentage)
      if (profile.powerLimit !== null) {
        const pl = Math.round(profile.powerLimit);
        try {
          await this.sshManager.executeSudoCommandOnRig(rigId, `rocm-smi -d ${i} --setpoweroverdrive ${pl}`);
          details.push(`GPU ${i}: Power limit set to ${pl}W`);
        } catch (error) {
          console.error(`[OCService] Failed to set power limit for GPU ${i}:`, error);
        }
      }

      // Fan speed
      if (profile.fanSpeed !== null) {
        const fan = Math.round(profile.fanSpeed);
        try {
          if (fan > 0) {
            await this.sshManager.executeSudoCommandOnRig(rigId, `rocm-smi -d ${i} --setfan ${fan}`);
            details.push(`GPU ${i}: Fan speed set to ${fan}%`);
          } else {
            await this.sshManager.executeSudoCommandOnRig(rigId, `rocm-smi -d ${i} --resetfan`);
            details.push(`GPU ${i}: Fan set to auto`);
          }
        } catch (error) {
          console.error(`[OCService] Failed to set fan speed for GPU ${i}:`, error);
        }
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
        try {
          await this.sshManager.executeSudoCommandOnRig(rigId, 'nvidia-smi -rgc'); // Reset GPU clocks
        } catch { /* ignore */ }
        try {
          await this.sshManager.executeSudoCommandOnRig(rigId, 'nvidia-smi -rmc'); // Reset memory clocks
        } catch { /* ignore */ }
      } else if (gpuVendor === 'AMD') {
        try {
          await this.sshManager.executeSudoCommandOnRig(rigId, 'rocm-smi --resetclocks');
        } catch { /* ignore */ }
        try {
          await this.sshManager.executeSudoCommandOnRig(rigId, 'rocm-smi --resetfan');
        } catch { /* ignore */ }
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

      auditLog({
        action: 'reset_oc',
        resource: 'rig',
        resourceId: rigId,
        success: true,
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
