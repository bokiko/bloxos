import { FastifyInstance } from 'fastify';
import { prisma } from '@bloxos/database';
import { z } from 'zod';
import { validateWalletAddress, getWalletPattern } from '../utils/wallet-validator.js';

const CoinTypeEnum = z.enum(['GPU', 'CPU']);

export async function coinsRoutes(app: FastifyInstance) {
  // Get all enabled coins
  app.get('/coins', async (request, reply) => {
    const coins = await prisma.coin.findMany({
      where: { enabled: true },
      orderBy: [{ type: 'asc' }, { ticker: 'asc' }],
      select: {
        id: true,
        ticker: true,
        name: true,
        algorithm: true,
        type: true,
        logoPath: true,
        coingeckoId: true,
      },
    });

    return reply.send(coins);
  });

  // Get coin by ticker
  app.get<{ Params: { ticker: string } }>(
    '/coins/:ticker',
    async (request, reply) => {
      const { ticker } = request.params;

      const coin = await prisma.coin.findUnique({
        where: { ticker: ticker.toUpperCase() },
        include: {
          poolPresets: {
            orderBy: [{ region: 'asc' }, { name: 'asc' }],
          },
          flightSheetTemplates: {
            where: { recommended: true },
            orderBy: { name: 'asc' },
          },
        },
      });

      if (!coin) {
        return reply.status(404).send({ error: 'Coin not found' });
      }

      return reply.send(coin);
    }
  );

  // Get coins by type (GPU or CPU)
  app.get<{ Params: { type: string } }>(
    '/coins/type/:type',
    async (request, reply) => {
      const { type } = request.params;
      const coinType = type.toUpperCase();

      const parseResult = CoinTypeEnum.safeParse(coinType);
      if (!parseResult.success) {
        return reply.status(400).send({ error: 'Invalid coin type. Must be GPU or CPU' });
      }

      const coins = await prisma.coin.findMany({
        where: {
          type: coinType as 'GPU' | 'CPU',
          enabled: true,
        },
        orderBy: { ticker: 'asc' },
        select: {
          id: true,
          ticker: true,
          name: true,
          algorithm: true,
          type: true,
          logoPath: true,
        },
      });

      return reply.send(coins);
    }
  );

  // Get pool presets for a coin
  app.get<{ Params: { ticker: string } }>(
    '/coins/:ticker/pools',
    async (request, reply) => {
      const { ticker } = request.params;

      const coin = await prisma.coin.findUnique({
        where: { ticker: ticker.toUpperCase() },
        include: {
          poolPresets: {
            orderBy: [{ region: 'asc' }, { name: 'asc' }],
          },
        },
      });

      if (!coin) {
        return reply.status(404).send({ error: 'Coin not found' });
      }

      return reply.send(coin.poolPresets);
    }
  );

  // Validate wallet address
  app.post<{ Body: { ticker: string; address: string } }>(
    '/coins/validate-wallet',
    async (request, reply) => {
      const { ticker, address } = request.body;

      if (!ticker || !address) {
        return reply.status(400).send({ error: 'ticker and address are required' });
      }

      const result = await validateWalletAddress(ticker, address);
      return reply.send(result);
    }
  );

  // Get wallet validation pattern for client-side validation
  app.get<{ Params: { ticker: string } }>(
    '/coins/:ticker/wallet-pattern',
    async (request, reply) => {
      const { ticker } = request.params;
      const pattern = await getWalletPattern(ticker);

      if (pattern === null) {
        const coin = await prisma.coin.findUnique({
          where: { ticker: ticker.toUpperCase() },
        });
        if (!coin) {
          return reply.status(404).send({ error: 'Coin not found' });
        }
      }

      return reply.send({ pattern });
    }
  );
}
