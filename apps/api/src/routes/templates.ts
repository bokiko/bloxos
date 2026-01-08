import { FastifyInstance } from 'fastify';
import { prisma } from '@bloxos/database';

export async function templatesRoutes(app: FastifyInstance) {
  // Get all flight sheet templates
  app.get('/templates', async (request, reply) => {
    const templates = await prisma.flightSheetTemplate.findMany({
      orderBy: [{ recommended: 'desc' }, { name: 'asc' }],
      include: {
        coin: {
          select: {
            ticker: true,
            name: true,
            algorithm: true,
            logoPath: true,
          },
        },
        poolPreset: {
          select: {
            name: true,
            region: true,
            host: true,
            port: true,
          },
        },
      },
    });

    return reply.send(templates);
  });

  // Get templates for a specific coin
  app.get<{ Params: { ticker: string } }>(
    '/templates/coin/:ticker',
    async (request, reply) => {
      const { ticker } = request.params;

      const coin = await prisma.coin.findUnique({
        where: { ticker: ticker.toUpperCase() },
      });

      if (!coin) {
        return reply.status(404).send({ error: 'Coin not found' });
      }

      const templates = await prisma.flightSheetTemplate.findMany({
        where: { coinId: coin.id },
        orderBy: [{ recommended: 'desc' }, { name: 'asc' }],
        include: {
          coin: {
            select: {
              ticker: true,
              name: true,
              algorithm: true,
              logoPath: true,
            },
          },
          poolPreset: {
            select: {
              name: true,
              region: true,
              host: true,
              port: true,
              sslPort: true,
              fee: true,
            },
          },
        },
      });

      return reply.send(templates);
    }
  );

  // Get recommended templates only
  app.get('/templates/recommended', async (request, reply) => {
    const templates = await prisma.flightSheetTemplate.findMany({
      where: { recommended: true },
      orderBy: { name: 'asc' },
      include: {
        coin: {
          select: {
            ticker: true,
            name: true,
            algorithm: true,
            type: true,
            logoPath: true,
          },
        },
        poolPreset: {
          select: {
            name: true,
            region: true,
            host: true,
            port: true,
          },
        },
      },
    });

    return reply.send(templates);
  });

  // Get templates by hardware type (NVIDIA, AMD, CPU)
  app.get<{ Params: { gpuType: string } }>(
    '/templates/hardware/:gpuType',
    async (request, reply) => {
      const { gpuType } = request.params;
      const hwType = gpuType.toUpperCase();

      if (!['NVIDIA', 'AMD', 'CPU'].includes(hwType)) {
        return reply.status(400).send({
          error: 'Invalid hardware type. Must be NVIDIA, AMD, or CPU',
        });
      }

      const templates = await prisma.flightSheetTemplate.findMany({
        where: { gpuType: hwType as 'NVIDIA' | 'AMD' | 'CPU' },
        orderBy: [{ recommended: 'desc' }, { name: 'asc' }],
        include: {
          coin: {
            select: {
              ticker: true,
              name: true,
              algorithm: true,
              logoPath: true,
            },
          },
          poolPreset: {
            select: {
              name: true,
              region: true,
              host: true,
              port: true,
            },
          },
        },
      });

      return reply.send(templates);
    }
  );

  // Get single template by ID
  app.get<{ Params: { id: string } }>(
    '/templates/:id',
    async (request, reply) => {
      const { id } = request.params;

      const template = await prisma.flightSheetTemplate.findUnique({
        where: { id },
        include: {
          coin: true,
          poolPreset: true,
        },
      });

      if (!template) {
        return reply.status(404).send({ error: 'Template not found' });
      }

      return reply.send(template);
    }
  );
}
