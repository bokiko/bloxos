import { prisma } from '@bloxos/database';
import { notificationService } from './notification-service.ts';

// Store previous hashrates for comparison
const previousHashrates: Map<string, number> = new Map();
// Track last alert times to prevent spam
const lastAlertTimes: Map<string, number> = new Map();
const ALERT_COOLDOWN_MS = 5 * 60 * 1000; // 5 minutes between same alerts

export class AlertService {
  // Get or create alert config for a rig
  async getAlertConfig(rigId: string) {
    const config = await prisma.alertConfig.findUnique({
      where: { rigId },
    });

    // Return defaults if no config exists
    if (!config) {
      return {
        tempAlertEnabled: true,
        offlineAlertEnabled: true,
        hashrateAlertEnabled: true,
        gpuTempThreshold: 80,
        cpuTempThreshold: 85,
        offlineThreshold: 300,
        hashrateDropPercent: 20,
      };
    }

    return config;
  }

  // Check if we should send an alert (cooldown check)
  private shouldAlert(alertKey: string): boolean {
    const lastTime = lastAlertTimes.get(alertKey);
    const now = Date.now();
    
    if (lastTime && (now - lastTime) < ALERT_COOLDOWN_MS) {
      return false;
    }
    
    lastAlertTimes.set(alertKey, now);
    return true;
  }

  // Map alert types to notification event types
  private getNotificationEventType(
    type: 'GPU_TEMP_HIGH' | 'CPU_TEMP_HIGH' | 'RIG_OFFLINE' | 'HASHRATE_DROP' | 'MINER_ERROR' | 'GPU_ERROR'
  ): 'offline' | 'high_temp' | 'low_hashrate' | 'miner_error' {
    switch (type) {
      case 'RIG_OFFLINE':
        return 'offline';
      case 'GPU_TEMP_HIGH':
      case 'CPU_TEMP_HIGH':
        return 'high_temp';
      case 'HASHRATE_DROP':
        return 'low_hashrate';
      case 'MINER_ERROR':
      case 'GPU_ERROR':
        return 'miner_error';
    }
  }

  // Create an alert and send notification
  async createAlert(
    rigId: string,
    type: 'GPU_TEMP_HIGH' | 'CPU_TEMP_HIGH' | 'RIG_OFFLINE' | 'HASHRATE_DROP' | 'MINER_ERROR' | 'GPU_ERROR',
    severity: 'INFO' | 'WARNING' | 'ERROR' | 'CRITICAL',
    title: string,
    message: string,
    data?: object
  ): Promise<void> {
    try {
      await prisma.alert.create({
        data: {
          rigId,
          type,
          severity,
          title,
          message,
          data: data || {},
        },
      });
      console.log(`[AlertService] Created ${severity} alert: ${title}`);

      // Send notification to rig owner
      const eventType = this.getNotificationEventType(type);
      await notificationService.notifyRigEvent(rigId, eventType, {
        title,
        message,
      });
    } catch (error) {
      console.error('[AlertService] Failed to create alert:', error);
    }
  }

  // Check GPU temperatures
  async checkGpuTemperatures(rigId: string, rigName: string, gpus: { index: number; temperature: number | null }[]): Promise<void> {
    const config = await this.getAlertConfig(rigId);
    
    if (!config.tempAlertEnabled) return;

    for (const gpu of gpus) {
      if (gpu.temperature === null) continue;
      
      if (gpu.temperature >= config.gpuTempThreshold) {
        const alertKey = `gpu_temp_${rigId}_${gpu.index}`;
        
        if (this.shouldAlert(alertKey)) {
          const severity = gpu.temperature >= 90 ? 'CRITICAL' : 'WARNING';
          await this.createAlert(
            rigId,
            'GPU_TEMP_HIGH',
            severity,
            `High GPU Temperature on ${rigName}`,
            `GPU ${gpu.index} is at ${gpu.temperature}°C (threshold: ${config.gpuTempThreshold}°C)`,
            { gpuIndex: gpu.index, temperature: gpu.temperature, threshold: config.gpuTempThreshold }
          );
        }
      }
    }
  }

  // Check CPU temperature
  async checkCpuTemperature(rigId: string, rigName: string, cpuTemp: number | null): Promise<void> {
    if (cpuTemp === null) return;
    
    const config = await this.getAlertConfig(rigId);
    
    if (!config.tempAlertEnabled) return;

    if (cpuTemp >= config.cpuTempThreshold) {
      const alertKey = `cpu_temp_${rigId}`;
      
      if (this.shouldAlert(alertKey)) {
        const severity = cpuTemp >= 95 ? 'CRITICAL' : 'WARNING';
        await this.createAlert(
          rigId,
          'CPU_TEMP_HIGH',
          severity,
          `High CPU Temperature on ${rigName}`,
          `CPU is at ${cpuTemp}°C (threshold: ${config.cpuTempThreshold}°C)`,
          { temperature: cpuTemp, threshold: config.cpuTempThreshold }
        );
      }
    }
  }

  // Check for hashrate drop
  async checkHashrateDrop(rigId: string, rigName: string, currentHashrate: number): Promise<void> {
    const config = await this.getAlertConfig(rigId);
    
    if (!config.hashrateAlertEnabled) return;

    const previousHashrate = previousHashrates.get(rigId);
    
    // Store current for next comparison
    previousHashrates.set(rigId, currentHashrate);

    // Skip if no previous value or current is 0 (miner might be starting)
    if (previousHashrate === undefined || previousHashrate === 0 || currentHashrate === 0) {
      return;
    }

    const dropPercent = ((previousHashrate - currentHashrate) / previousHashrate) * 100;

    if (dropPercent >= config.hashrateDropPercent) {
      const alertKey = `hashrate_drop_${rigId}`;
      
      if (this.shouldAlert(alertKey)) {
        await this.createAlert(
          rigId,
          'HASHRATE_DROP',
          'WARNING',
          `Hashrate Drop on ${rigName}`,
          `Hashrate dropped by ${dropPercent.toFixed(1)}% (${previousHashrate.toFixed(2)} -> ${currentHashrate.toFixed(2)} MH/s)`,
          { previousHashrate, currentHashrate, dropPercent }
        );
      }
    }
  }

  // Check for rig going offline
  async checkRigOffline(rigId: string, rigName: string, lastSeen: Date | null): Promise<void> {
    const config = await this.getAlertConfig(rigId);
    
    if (!config.offlineAlertEnabled) return;

    if (!lastSeen) return;

    const offlineSeconds = (Date.now() - lastSeen.getTime()) / 1000;

    if (offlineSeconds >= config.offlineThreshold) {
      const alertKey = `offline_${rigId}`;
      
      if (this.shouldAlert(alertKey)) {
        const offlineMinutes = Math.round(offlineSeconds / 60);
        await this.createAlert(
          rigId,
          'RIG_OFFLINE',
          'ERROR',
          `Rig Offline: ${rigName}`,
          `Rig has been offline for ${offlineMinutes} minutes`,
          { offlineSeconds, lastSeen: lastSeen.toISOString() }
        );
      }
    }
  }

  // Run all checks for a rig after polling
  async checkRigAlerts(
    rigId: string,
    rigName: string,
    gpus: { index: number; temperature: number | null }[],
    cpuTemp: number | null,
    totalHashrate: number
  ): Promise<void> {
    try {
      await Promise.all([
        this.checkGpuTemperatures(rigId, rigName, gpus),
        this.checkCpuTemperature(rigId, rigName, cpuTemp),
        this.checkHashrateDrop(rigId, rigName, totalHashrate),
      ]);
    } catch (error) {
      console.error(`[AlertService] Error checking alerts for ${rigName}:`, error);
    }
  }

  // Check offline rigs (run separately on interval)
  async checkOfflineRigs(): Promise<void> {
    try {
      const rigs = await prisma.rig.findMany({
        where: {
          sshCredential: { isNot: null },
        },
        select: {
          id: true,
          name: true,
          lastSeen: true,
          status: true,
        },
      });

      for (const rig of rigs) {
        if (rig.status === 'OFFLINE' || (rig.lastSeen && (Date.now() - rig.lastSeen.getTime()) > 5 * 60 * 1000)) {
          await this.checkRigOffline(rig.id, rig.name, rig.lastSeen);
        }
      }
    } catch (error) {
      console.error('[AlertService] Error checking offline rigs:', error);
    }
  }
}

// Singleton instance
export const alertService = new AlertService();
