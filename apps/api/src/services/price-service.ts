import { prisma } from '@bloxos/database';

interface CoinGeckoPrice {
  usd: number;
  usd_24h_change?: number;
}

interface CoinGeckoPriceResponse {
  [coingeckoId: string]: CoinGeckoPrice;
}

interface CoinPriceData {
  coinId: string;
  ticker: string;
  priceUsd: number;
  priceChange24h: number | null;
  fetchedAt: Date;
}

class PriceService {
  private interval: ReturnType<typeof setInterval> | null = null;
  private readonly FETCH_INTERVAL = 60 * 60 * 1000; // 1 hour
  private readonly COINGECKO_API = 'https://api.coingecko.com/api/v3';
  private isRunning = false;

  /**
   * Start the price fetching service
   */
  start(): void {
    if (this.isRunning) {
      console.log('[PriceService] Already running');
      return;
    }

    this.isRunning = true;
    console.log('[PriceService] Starting price fetching service');

    // Fetch immediately on startup
    this.fetchPrices().catch((err) => {
      console.error('[PriceService] Initial fetch failed:', err);
    });

    // Then fetch every hour
    this.interval = setInterval(() => {
      this.fetchPrices().catch((err) => {
        console.error('[PriceService] Scheduled fetch failed:', err);
      });
    }, this.FETCH_INTERVAL);
  }

  /**
   * Stop the price fetching service
   */
  stop(): void {
    if (this.interval) {
      clearInterval(this.interval);
      this.interval = null;
    }
    this.isRunning = false;
    console.log('[PriceService] Stopped');
  }

  /**
   * Fetch prices from CoinGecko for all enabled coins
   */
  async fetchPrices(): Promise<void> {
    try {
      // Get all coins with coingeckoId
      const coins = await prisma.coin.findMany({
        where: {
          enabled: true,
          coingeckoId: { not: null },
        },
        select: {
          id: true,
          ticker: true,
          coingeckoId: true,
        },
      });

      if (coins.length === 0) {
        console.log('[PriceService] No coins with CoinGecko IDs found');
        return;
      }

      // Build list of CoinGecko IDs
      const coingeckoIds = coins
        .map((c) => c.coingeckoId)
        .filter((id): id is string => id !== null);

      if (coingeckoIds.length === 0) {
        return;
      }

      // Fetch prices in a single batch request
      const idsParam = coingeckoIds.join(',');
      const url = `${this.COINGECKO_API}/simple/price?ids=${idsParam}&vs_currencies=usd&include_24hr_change=true`;

      console.log(`[PriceService] Fetching prices for ${coingeckoIds.length} coins`);

      const response = await fetch(url);

      if (!response.ok) {
        throw new Error(`CoinGecko API error: ${response.status} ${response.statusText}`);
      }

      const data = (await response.json()) as CoinGeckoPriceResponse;

      // Store prices in database
      const now = new Date();
      const priceRecords = [];

      for (const coin of coins) {
        if (!coin.coingeckoId) continue;

        const priceData = data[coin.coingeckoId];
        if (!priceData) {
          console.log(`[PriceService] No price data for ${coin.ticker} (${coin.coingeckoId})`);
          continue;
        }

        priceRecords.push({
          coinId: coin.id,
          priceUsd: priceData.usd,
          priceChange24h: priceData.usd_24h_change ?? null,
          fetchedAt: now,
        });
      }

      // Batch insert prices
      if (priceRecords.length > 0) {
        await prisma.coinPrice.createMany({
          data: priceRecords,
        });

        console.log(`[PriceService] Stored ${priceRecords.length} price records`);
      }

      // Clean up old prices (keep last 30 days)
      const thirtyDaysAgo = new Date();
      thirtyDaysAgo.setDate(thirtyDaysAgo.getDate() - 30);

      const deleted = await prisma.coinPrice.deleteMany({
        where: {
          fetchedAt: { lt: thirtyDaysAgo },
        },
      });

      if (deleted.count > 0) {
        console.log(`[PriceService] Cleaned up ${deleted.count} old price records`);
      }
    } catch (error) {
      console.error('[PriceService] Error fetching prices:', error);
      throw error;
    }
  }

  /**
   * Get current prices for all coins (most recent)
   */
  async getCurrentPrices(): Promise<Map<string, CoinPriceData>> {
    const prices = new Map<string, CoinPriceData>();

    // Get the most recent price for each coin
    const coins = await prisma.coin.findMany({
      where: { enabled: true },
      include: {
        prices: {
          orderBy: { fetchedAt: 'desc' },
          take: 1,
        },
      },
    });

    for (const coin of coins) {
      if (coin.prices.length > 0) {
        const price = coin.prices[0];
        prices.set(coin.ticker, {
          coinId: coin.id,
          ticker: coin.ticker,
          priceUsd: price.priceUsd,
          priceChange24h: price.priceChange24h,
          fetchedAt: price.fetchedAt,
        });
      }
    }

    return prices;
  }

  /**
   * Get current price for a specific coin by ticker
   */
  async getPriceByTicker(ticker: string): Promise<CoinPriceData | null> {
    const coin = await prisma.coin.findUnique({
      where: { ticker },
      include: {
        prices: {
          orderBy: { fetchedAt: 'desc' },
          take: 1,
        },
      },
    });

    if (!coin || coin.prices.length === 0) {
      return null;
    }

    const price = coin.prices[0];
    return {
      coinId: coin.id,
      ticker: coin.ticker,
      priceUsd: price.priceUsd,
      priceChange24h: price.priceChange24h,
      fetchedAt: price.fetchedAt,
    };
  }

  /**
   * Get price history for a coin
   */
  async getPriceHistory(
    coinId: string,
    days: number = 7
  ): Promise<Array<{ priceUsd: number; priceChange24h: number | null; fetchedAt: Date }>> {
    const since = new Date();
    since.setDate(since.getDate() - days);

    const prices = await prisma.coinPrice.findMany({
      where: {
        coinId,
        fetchedAt: { gte: since },
      },
      orderBy: { fetchedAt: 'asc' },
      select: {
        priceUsd: true,
        priceChange24h: true,
        fetchedAt: true,
      },
    });

    return prices;
  }

  /**
   * Force a price refresh (for API endpoint)
   */
  async refresh(): Promise<{ success: boolean; coinsUpdated: number }> {
    try {
      await this.fetchPrices();

      const count = await prisma.coinPrice.count({
        where: {
          fetchedAt: {
            gte: new Date(Date.now() - 5 * 60 * 1000), // Last 5 minutes
          },
        },
      });

      return { success: true, coinsUpdated: count };
    } catch {
      return { success: false, coinsUpdated: 0 };
    }
  }
}

export const priceService = new PriceService();
