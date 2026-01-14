import { prisma } from '@bloxos/database';
import { priceService } from './price-service.ts';

// Approximate daily earnings per MH/s for common algorithms (in coins per day)
// These are rough estimates and should be updated or fetched from an API
const HASHRATE_EARNINGS: Record<string, number> = {
  // Etchash (ETC) - ~0.0003 ETC per MH/s per day
  Etchash: 0.0003,
  // Ethash - legacy, similar to Etchash
  Ethash: 0.0003,
  // KawPow (RVN) - ~0.5 RVN per MH/s per day
  KawPow: 0.5,
  // RandomX (XMR) - ~0.00001 XMR per H/s per day (note: H/s not MH/s)
  RandomX: 0.00001,
  // Autolykos2 (ERG) - ~0.02 ERG per MH/s per day
  Autolykos2: 0.02,
  // Kheavyhash (KAS - removed, but keeping for reference)
  Kheavyhash: 0,
  // ZelHash (FLUX - removed, but keeping for reference)
  ZelHash: 0,
  // ProgPow (ZANO, SERO)
  ProgPoW: 0.1,
  // Blake3 (ALPH)
  Blake3: 0.5,
  // Pyrinhash (PYRIN)
  Pyrinhash: 100,
  // SHA512256d (QUAI)
  SHA512256d: 0.1,
};

class ProfitCalculator {
  private interval: ReturnType<typeof setInterval> | null = null;
  private isRunning = false;
  // Run at midnight (calculate previous day)
  private readonly CALC_INTERVAL = 24 * 60 * 60 * 1000; // 24 hours

  /**
   * Start the profit calculator (runs daily)
   */
  start(): void {
    if (this.isRunning) {
      console.log('[ProfitCalculator] Already running');
      return;
    }

    this.isRunning = true;
    console.log('[ProfitCalculator] Starting profit calculation service');

    // Calculate for yesterday on startup if missing
    this.calculateYesterday().catch((err) => {
      console.error('[ProfitCalculator] Initial calculation failed:', err);
    });

    // Schedule daily calculation
    this.interval = setInterval(() => {
      this.calculateYesterday().catch((err) => {
        console.error('[ProfitCalculator] Scheduled calculation failed:', err);
      });
    }, this.CALC_INTERVAL);
  }

  /**
   * Stop the profit calculator
   */
  stop(): void {
    if (this.interval) {
      clearInterval(this.interval);
      this.interval = null;
    }
    this.isRunning = false;
    console.log('[ProfitCalculator] Stopped');
  }

  /**
   * Calculate profit for yesterday for all rigs
   */
  async calculateYesterday(): Promise<void> {
    const yesterday = new Date();
    yesterday.setDate(yesterday.getDate() - 1);
    yesterday.setHours(0, 0, 0, 0);

    await this.calculateForDate(yesterday);
  }

  /**
   * Calculate profit for a specific date for all rigs
   */
  async calculateForDate(date: Date): Promise<void> {
    const dateStr = date.toISOString().split('T')[0];
    console.log(`[ProfitCalculator] Calculating profit for ${dateStr}`);

    // Get all rigs with their flight sheets
    const rigs = await prisma.rig.findMany({
      where: { status: { not: 'OFFLINE' } },
      include: {
        flightSheet: {
          include: {
            wallet: true,
          },
        },
        farm: {
          include: {
            electricitySettings: true,
          },
        },
      },
    });

    // Get current coin prices
    const prices = await priceService.getCurrentPrices();

    let created = 0;
    let skipped = 0;

    for (const rig of rigs) {
      try {
        // Check if snapshot already exists
        const existing = await prisma.profitSnapshot.findUnique({
          where: {
            rigId_date: {
              rigId: rig.id,
              date,
            },
          },
        });

        if (existing) {
          skipped++;
          continue;
        }

        // Calculate profit for this rig
        const snapshot = await this.calculateRigProfit(rig, date, prices);

        if (snapshot) {
          await prisma.profitSnapshot.create({
            data: snapshot,
          });
          created++;
        }
      } catch (error) {
        console.error(`[ProfitCalculator] Error calculating for rig ${rig.id}:`, error);
      }
    }

    console.log(`[ProfitCalculator] Created ${created} snapshots, skipped ${skipped} existing`);
  }

  /**
   * Calculate profit for a single rig on a given date
   */
  private async calculateRigProfit(
    rig: {
      id: string;
      flightSheet: { coin: string; wallet: { coin: string } } | null;
      farm: { electricitySettings: { rate: number } | null };
    },
    date: Date,
    prices: Map<string, { priceUsd: number; ticker: string }>
  ): Promise<{
    rigId: string;
    date: Date;
    coinTicker: string;
    coinsEarned: number | null;
    revenueUsd: number;
    avgHashrate: number;
    powerKwh: number;
    electricityCost: number;
    profitUsd: number;
  } | null> {
    // Get the coin being mined
    const coinTicker = rig.flightSheet?.coin || 'ETH';

    // Get coin info
    const coin = await prisma.coin.findUnique({
      where: { ticker: coinTicker },
    });

    if (!coin) {
      return null;
    }

    // Get electricity rate (default to $0.10/kWh)
    const electricityRate = rig.farm?.electricitySettings?.rate || 0.1;

    // Get stats for the date range
    const dayStart = new Date(date);
    dayStart.setHours(0, 0, 0, 0);
    const dayEnd = new Date(date);
    dayEnd.setHours(23, 59, 59, 999);

    const stats = await prisma.rigStats.findMany({
      where: {
        rigId: rig.id,
        timestamp: {
          gte: dayStart,
          lte: dayEnd,
        },
      },
      orderBy: { timestamp: 'asc' },
    });

    if (stats.length === 0) {
      return null;
    }

    // Calculate power consumption (kWh)
    // Stats are collected every 30 seconds
    // kWh = (watts * seconds) / 3600 / 1000
    let totalWattSeconds = 0;
    let totalHashrate = 0;

    for (const stat of stats) {
      // Assuming 30 second intervals between stats
      totalWattSeconds += stat.power * 30;
      totalHashrate += stat.hashrate;
    }

    const powerKwh = totalWattSeconds / 3600 / 1000;
    const avgHashrate = totalHashrate / stats.length;
    const electricityCost = powerKwh * electricityRate;

    // Estimate coins earned based on hashrate
    const earningsRate = HASHRATE_EARNINGS[coin.algorithm] || 0;
    let coinsEarned: number | null = null;

    if (earningsRate > 0) {
      // For RandomX (CPU mining), hashrate is in H/s, not MH/s
      if (coin.algorithm === 'RandomX') {
        coinsEarned = avgHashrate * 1000000 * earningsRate; // Convert MH/s back to H/s
      } else {
        coinsEarned = avgHashrate * earningsRate;
      }
    }

    // Calculate revenue in USD
    const priceData = prices.get(coinTicker);
    const priceUsd = priceData?.priceUsd || 0;
    const revenueUsd = coinsEarned !== null ? coinsEarned * priceUsd : 0;

    // Calculate net profit
    const profitUsd = revenueUsd - electricityCost;

    return {
      rigId: rig.id,
      date,
      coinTicker,
      coinsEarned,
      revenueUsd: Math.round(revenueUsd * 100) / 100,
      avgHashrate: Math.round(avgHashrate * 100) / 100,
      powerKwh: Math.round(powerKwh * 100) / 100,
      electricityCost: Math.round(electricityCost * 100) / 100,
      profitUsd: Math.round(profitUsd * 100) / 100,
    };
  }

  /**
   * Backfill missing profit snapshots for a date range
   */
  async backfill(startDate: Date, endDate: Date): Promise<{ processed: number }> {
    let processed = 0;

    const current = new Date(startDate);
    while (current <= endDate) {
      await this.calculateForDate(new Date(current));
      processed++;
      current.setDate(current.getDate() + 1);
    }

    return { processed };
  }

  /**
   * Recalculate profit for a specific rig and date range
   */
  async recalculateRig(rigId: string, startDate: Date, endDate: Date): Promise<number> {
    // Delete existing snapshots in range
    await prisma.profitSnapshot.deleteMany({
      where: {
        rigId,
        date: {
          gte: startDate,
          lte: endDate,
        },
      },
    });

    // Recalculate
    let created = 0;
    const current = new Date(startDate);
    const prices = await priceService.getCurrentPrices();

    const rig = await prisma.rig.findUnique({
      where: { id: rigId },
      include: {
        flightSheet: {
          include: { wallet: true },
        },
        farm: {
          include: { electricitySettings: true },
        },
      },
    });

    if (!rig) return 0;

    while (current <= endDate) {
      const snapshot = await this.calculateRigProfit(rig, new Date(current), prices);
      if (snapshot) {
        await prisma.profitSnapshot.create({ data: snapshot });
        created++;
      }
      current.setDate(current.getDate() + 1);
    }

    return created;
  }
}

export const profitCalculator = new ProfitCalculator();
