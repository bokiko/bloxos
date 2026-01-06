import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';

// Validation schemas
const CreateFlightSheetSchema = z.object({
  name: z.string().min(1).max(100),
  coin: z.string().min(1).max(20),
  walletId: z.string(),
  poolId: z.string(),
  minerId: z.string(),
  extraArgs: z.string().max(1000).optional(),
});

const UpdateFlightSheetSchema = z.object({
  name: z.string().min(1).max(100).optional(),
  coin: z.string().min(1).max(20).optional(),
  walletId: z.string().optional(),
  poolId: z.string().optional(),
  minerId: z.string().optional(),
  extraArgs: z.string().max(1000).nullable().optional(),
});

export async function flightSheetRoutes(app: FastifyInstance) {
  // List all flight sheets
  app.get('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const flightSheets = await prisma.flightSheet.findMany({
      include: {
        wallet: true,
        pool: true,
        miner: true,
        _count: {
          select: { rigs: true },
        },
      },
      orderBy: { createdAt: 'desc' },
    });

    return reply.send(flightSheets);
  });

  // Get single flight sheet
  app.get('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;

    const flightSheet = await prisma.flightSheet.findUnique({
      where: { id },
      include: {
        wallet: true,
        pool: true,
        miner: true,
        rigs: {
          select: { id: true, name: true, status: true },
        },
      },
    });

    if (!flightSheet) {
      return reply.status(404).send({ message: 'Flight sheet not found' });
    }

    return reply.send(flightSheet);
  });

  // Create flight sheet
  app.post('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const data = CreateFlightSheetSchema.parse(request.body);

    // Verify wallet exists
    const wallet = await prisma.wallet.findUnique({ where: { id: data.walletId } });
    if (!wallet) {
      return reply.status(400).send({ message: 'Wallet not found' });
    }

    // Verify pool exists
    const pool = await prisma.pool.findUnique({ where: { id: data.poolId } });
    if (!pool) {
      return reply.status(400).send({ message: 'Pool not found' });
    }

    // Verify miner exists
    const miner = await prisma.minerSoftware.findUnique({ where: { id: data.minerId } });
    if (!miner) {
      return reply.status(400).send({ message: 'Miner not found' });
    }

    const flightSheet = await prisma.flightSheet.create({
      data: {
        name: data.name,
        coin: data.coin.toUpperCase(),
        walletId: data.walletId,
        poolId: data.poolId,
        minerId: data.minerId,
        extraArgs: data.extraArgs || null,
      },
      include: {
        wallet: true,
        pool: true,
        miner: true,
      },
    });

    return reply.status(201).send(flightSheet);
  });

  // Update flight sheet
  app.patch('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const data = UpdateFlightSheetSchema.parse(request.body);

    const existing = await prisma.flightSheet.findUnique({ where: { id } });
    if (!existing) {
      return reply.status(404).send({ message: 'Flight sheet not found' });
    }

    const flightSheet = await prisma.flightSheet.update({
      where: { id },
      data: {
        ...data,
        coin: data.coin?.toUpperCase(),
      },
      include: {
        wallet: true,
        pool: true,
        miner: true,
      },
    });

    return reply.send(flightSheet);
  });

  // Delete flight sheet
  app.delete('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;

    const existing = await prisma.flightSheet.findUnique({
      where: { id },
      include: { _count: { select: { rigs: true } } },
    });

    if (!existing) {
      return reply.status(404).send({ message: 'Flight sheet not found' });
    }

    if (existing._count.rigs > 0) {
      return reply.status(400).send({
        message: `Cannot delete flight sheet: assigned to ${existing._count.rigs} rig(s)`,
      });
    }

    await prisma.flightSheet.delete({ where: { id } });

    return reply.send({ success: true });
  });
}
