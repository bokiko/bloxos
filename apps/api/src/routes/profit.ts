import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';
import { authService } from '../services/auth-service.ts';
import { priceService } from '../services/price-service.ts';

// Schemas
const ElectricitySettingsSchema = z.object({
  rate: z.number().min(0).max(1), // $/kWh, typical range 0.01-0.50
  currency: z.string().length(3).default('USD'),
});

const ProfitQuerySchema = z.object({
  period: z.enum(['day', 'week', 'month', 'year']).default('month'),
  startDate: z.string().datetime().optional(),
  endDate: z.string().datetime().optional(),
});

// Helper to get user and their farm
async function getUserFarm(request: FastifyRequest): Promise<{ userId: string; farmId: string } | null> {
  const token = request.cookies.token || request.headers.authorization?.replace('Bearer ', '');
  if (!token) return null;

  const payload = await authService.verifyToken(token);
  if (!payload?.userId) return null;

  // Get user's first farm (users typically have one farm)
  const farm = await prisma.farm.findFirst({
    where: { ownerId: payload.userId },
    select: { id: true },
  });

  if (!farm) return null;

  return { userId: payload.userId, farmId: farm.id };
}

// Calculate date range from period
function getDateRange(period: string): { start: Date; end: Date } {
  const end = new Date();
  const start = new Date();

  switch (period) {
    case 'day':
      start.setDate(start.getDate() - 1);
      break;
    case 'week':
      start.setDate(start.getDate() - 7);
      break;
    case 'month':
      start.setMonth(start.getMonth() - 1);
      break;
    case 'year':
      start.setFullYear(start.getFullYear() - 1);
      break;
  }

  return { start, end };
}

export async function profitRoutes(app: FastifyInstance) {
  // =========================================
  // ELECTRICITY SETTINGS
  // =========================================

  // Get electricity settings for user's farm
  app.get('/settings', async (request: FastifyRequest, reply: FastifyReply) => {
    const userFarm = await getUserFarm(request);
    if (!userFarm) {
      return reply.status(401).send({ error: 'Not authenticated' });
    }

    const settings = await prisma.electricitySettings.findUnique({
      where: { farmId: userFarm.farmId },
    });

    // Return defaults if no settings exist
    if (!settings) {
      return reply.send({
        rate: 0.10, // Default $0.10/kWh
        currency: 'USD',
      });
    }

    return reply.send({
      rate: settings.rate,
      currency: settings.currency,
    });
  });

  // Update electricity settings
  app.put('/settings', async (request: FastifyRequest, reply: FastifyReply) => {
    const userFarm = await getUserFarm(request);
    if (!userFarm) {
      return reply.status(401).send({ error: 'Not authenticated' });
    }

    const result = ElectricitySettingsSchema.safeParse(request.body);
    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const settings = await prisma.electricitySettings.upsert({
      where: { farmId: userFarm.farmId },
      update: result.data,
      create: {
        farmId: userFarm.farmId,
        ...result.data,
      },
    });

    return reply.send({
      rate: settings.rate,
      currency: settings.currency,
    });
  });

  // =========================================
  // COIN PRICES
  // =========================================

  // Get current prices for all coins
  app.get('/prices', async (_request: FastifyRequest, reply: FastifyReply) => {
    const prices = await priceService.getCurrentPrices();

    const priceList = Array.from(prices.values()).map((p) => ({
      ticker: p.ticker,
      priceUsd: p.priceUsd,
      priceChange24h: p.priceChange24h,
      fetchedAt: p.fetchedAt,
    }));

    return reply.send(priceList);
  });

  // Get price for a specific coin
  app.get('/prices/:ticker', async (request: FastifyRequest<{ Params: { ticker: string } }>, reply: FastifyReply) => {
    const { ticker } = request.params;
    const price = await priceService.getPriceByTicker(ticker.toUpperCase());

    if (!price) {
      return reply.status(404).send({ error: 'Coin not found or no price data' });
    }

    return reply.send(price);
  });

  // Get price history for a coin
  app.get(
    '/prices/:ticker/history',
    async (request: FastifyRequest<{ Params: { ticker: string }; Querystring: { days?: string } }>, reply: FastifyReply) => {
      const { ticker } = request.params;
      const days = parseInt(request.query.days || '7', 10);

      const coin = await prisma.coin.findUnique({
        where: { ticker: ticker.toUpperCase() },
      });

      if (!coin) {
        return reply.status(404).send({ error: 'Coin not found' });
      }

      const history = await priceService.getPriceHistory(coin.id, days);
      return reply.send(history);
    }
  );

  // Force refresh prices (admin)
  app.post('/prices/refresh', async (_request: FastifyRequest, reply: FastifyReply) => {
    const result = await priceService.refresh();
    return reply.send(result);
  });

  // =========================================
  // PROFIT SUMMARY
  // =========================================

  // Get profit summary across all rigs
  app.get('/summary', async (request: FastifyRequest<{ Querystring: { period?: string } }>, reply: FastifyReply) => {
    const userFarm = await getUserFarm(request);
    if (!userFarm) {
      return reply.status(401).send({ error: 'Not authenticated' });
    }

    const queryResult = ProfitQuerySchema.safeParse(request.query);
    const period = queryResult.success ? queryResult.data.period : 'month';
    const { start, end } = getDateRange(period);

    // Get all rigs for the user's farm
    const rigs = await prisma.rig.findMany({
      where: { farmId: userFarm.farmId },
      select: { id: true },
    });

    const rigIds = rigs.map((r) => r.id);

    // Aggregate profit snapshots
    const snapshots = await prisma.profitSnapshot.findMany({
      where: {
        rigId: { in: rigIds },
        date: { gte: start, lte: end },
      },
    });

    // Calculate totals
    let totalRevenue = 0;
    let totalElectricityCost = 0;
    let totalProfit = 0;
    let totalPowerKwh = 0;

    for (const snapshot of snapshots) {
      totalRevenue += snapshot.revenueUsd;
      totalElectricityCost += snapshot.electricityCost;
      totalProfit += snapshot.profitUsd;
      totalPowerKwh += snapshot.powerKwh;
    }

    // Get daily breakdown for charts
    const dailyData = new Map<string, { revenue: number; cost: number; profit: number }>();

    for (const snapshot of snapshots) {
      const dateKey = snapshot.date.toISOString().split('T')[0];
      const existing = dailyData.get(dateKey) || { revenue: 0, cost: 0, profit: 0 };
      existing.revenue += snapshot.revenueUsd;
      existing.cost += snapshot.electricityCost;
      existing.profit += snapshot.profitUsd;
      dailyData.set(dateKey, existing);
    }

    const daily = Array.from(dailyData.entries())
      .map(([date, data]) => ({ date, ...data }))
      .sort((a, b) => a.date.localeCompare(b.date));

    return reply.send({
      period,
      startDate: start.toISOString(),
      endDate: end.toISOString(),
      totals: {
        revenue: Math.round(totalRevenue * 100) / 100,
        electricityCost: Math.round(totalElectricityCost * 100) / 100,
        profit: Math.round(totalProfit * 100) / 100,
        powerKwh: Math.round(totalPowerKwh * 100) / 100,
      },
      rigsCount: rigIds.length,
      snapshotsCount: snapshots.length,
      daily,
    });
  });

  // =========================================
  // PER-RIG PROFIT
  // =========================================

  // Get profit for a specific rig
  app.get('/rig/:id', async (request: FastifyRequest<{ Params: { id: string }; Querystring: { period?: string } }>, reply: FastifyReply) => {
    const userFarm = await getUserFarm(request);
    if (!userFarm) {
      return reply.status(401).send({ error: 'Not authenticated' });
    }

    const { id: rigId } = request.params;

    // Verify rig belongs to user's farm
    const rig = await prisma.rig.findFirst({
      where: { id: rigId, farmId: userFarm.farmId },
      select: { id: true, name: true },
    });

    if (!rig) {
      return reply.status(404).send({ error: 'Rig not found' });
    }

    const queryResult = ProfitQuerySchema.safeParse(request.query);
    const period = queryResult.success ? queryResult.data.period : 'month';
    const { start, end } = getDateRange(period);

    const snapshots = await prisma.profitSnapshot.findMany({
      where: {
        rigId,
        date: { gte: start, lte: end },
      },
      orderBy: { date: 'asc' },
    });

    // Calculate totals
    let totalRevenue = 0;
    let totalElectricityCost = 0;
    let totalProfit = 0;
    let totalPowerKwh = 0;
    let avgHashrate = 0;

    for (const snapshot of snapshots) {
      totalRevenue += snapshot.revenueUsd;
      totalElectricityCost += snapshot.electricityCost;
      totalProfit += snapshot.profitUsd;
      totalPowerKwh += snapshot.powerKwh;
      avgHashrate += snapshot.avgHashrate;
    }

    if (snapshots.length > 0) {
      avgHashrate = avgHashrate / snapshots.length;
    }

    return reply.send({
      rigId: rig.id,
      rigName: rig.name,
      period,
      startDate: start.toISOString(),
      endDate: end.toISOString(),
      totals: {
        revenue: Math.round(totalRevenue * 100) / 100,
        electricityCost: Math.round(totalElectricityCost * 100) / 100,
        profit: Math.round(totalProfit * 100) / 100,
        powerKwh: Math.round(totalPowerKwh * 100) / 100,
        avgHashrate: Math.round(avgHashrate * 100) / 100,
      },
      snapshots: snapshots.map((s) => ({
        date: s.date.toISOString().split('T')[0],
        coinTicker: s.coinTicker,
        revenue: s.revenueUsd,
        electricityCost: s.electricityCost,
        profit: s.profitUsd,
        powerKwh: s.powerKwh,
        avgHashrate: s.avgHashrate,
      })),
    });
  });

  // Get profit breakdown by rig (for comparison table)
  app.get('/by-rig', async (request: FastifyRequest<{ Querystring: { period?: string } }>, reply: FastifyReply) => {
    const userFarm = await getUserFarm(request);
    if (!userFarm) {
      return reply.status(401).send({ error: 'Not authenticated' });
    }

    const queryResult = ProfitQuerySchema.safeParse(request.query);
    const period = queryResult.success ? queryResult.data.period : 'month';
    const { start, end } = getDateRange(period);

    // Get all rigs with their profit snapshots
    const rigs = await prisma.rig.findMany({
      where: { farmId: userFarm.farmId },
      select: {
        id: true,
        name: true,
        status: true,
        flightSheet: {
          select: { coin: true },
        },
        profitSnapshots: {
          where: {
            date: { gte: start, lte: end },
          },
        },
      },
    });

    const rigProfits = rigs.map((rig) => {
      let totalRevenue = 0;
      let totalCost = 0;
      let totalProfit = 0;
      let avgHashrate = 0;

      for (const snapshot of rig.profitSnapshots) {
        totalRevenue += snapshot.revenueUsd;
        totalCost += snapshot.electricityCost;
        totalProfit += snapshot.profitUsd;
        avgHashrate += snapshot.avgHashrate;
      }

      if (rig.profitSnapshots.length > 0) {
        avgHashrate = avgHashrate / rig.profitSnapshots.length;
      }

      return {
        rigId: rig.id,
        rigName: rig.name,
        status: rig.status,
        coin: rig.flightSheet?.coin || null,
        revenue: Math.round(totalRevenue * 100) / 100,
        electricityCost: Math.round(totalCost * 100) / 100,
        profit: Math.round(totalProfit * 100) / 100,
        avgHashrate: Math.round(avgHashrate * 100) / 100,
        daysTracked: rig.profitSnapshots.length,
      };
    });

    // Sort by profit descending
    rigProfits.sort((a, b) => b.profit - a.profit);

    return reply.send({
      period,
      rigs: rigProfits,
    });
  });
}
