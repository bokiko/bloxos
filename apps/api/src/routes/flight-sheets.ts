import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';
import { getUserFarmIds } from '../middleware/authorization.ts';
import { auditLog, validateExtraArgs } from '../utils/security.ts';

// Validation schemas
const CreateFlightSheetSchema = z.object({
  name: z.string().min(1).max(100),
  coin: z.string().min(1).max(20),
  walletId: z.string().cuid(),
  poolId: z.string().cuid(),
  minerId: z.string().cuid(),
  extraArgs: z.string().max(1000).optional(),
  farmId: z.string().cuid(),
});

const UpdateFlightSheetSchema = z.object({
  name: z.string().min(1).max(100).optional(),
  coin: z.string().min(1).max(20).optional(),
  walletId: z.string().cuid().optional(),
  poolId: z.string().cuid().optional(),
  minerId: z.string().cuid().optional(),
  extraArgs: z.string().max(1000).nullable().optional(),
});

export async function flightSheetRoutes(app: FastifyInstance) {
  // List flight sheets - filtered by user's farms
  app.get('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const farmIds = await getUserFarmIds(user.userId, user.role);

    const flightSheets = await prisma.flightSheet.findMany({
      where: { farmId: { in: farmIds } },
      include: {
        farm: { select: { id: true, name: true } },
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

  // Get single flight sheet - with authorization check
  app.get('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const flightSheet = await prisma.flightSheet.findUnique({
      where: { id },
      include: {
        farm: { select: { id: true, name: true, ownerId: true } },
        wallet: true,
        pool: true,
        miner: true,
        rigs: {
          select: { id: true, name: true, status: true },
        },
      },
    });

    if (!flightSheet) {
      return reply.status(404).send({ error: 'Flight sheet not found' });
    }

    if (user.role !== 'ADMIN' && flightSheet.farm.ownerId !== user.userId) {
      return reply.status(403).send({ error: 'Access denied' });
    }

    return reply.send(flightSheet);
  });

  // Create flight sheet - verify ownership of farm, wallet, and pool
  app.post('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const data = CreateFlightSheetSchema.parse(request.body);

    // Validate extraArgs if provided (MED-006)
    if (data.extraArgs) {
      try {
        validateExtraArgs(data.extraArgs);
      } catch {
        return reply.status(400).send({ error: 'Invalid extra arguments' });
      }
    }

    const farmIds = await getUserFarmIds(user.userId, user.role);
    if (!farmIds.includes(data.farmId)) {
      return reply.status(403).send({ error: 'Access denied to this farm' });
    }

    // Verify wallet exists AND belongs to user's farm (HIGH-006)
    const wallet = await prisma.wallet.findUnique({
      where: { id: data.walletId },
      include: { farm: { select: { ownerId: true } } },
    });
    if (!wallet) {
      return reply.status(400).send({ error: 'Invalid configuration' });
    }
    if (user.role !== 'ADMIN' && wallet.farm.ownerId !== user.userId) {
      return reply.status(403).send({ error: 'Access denied to wallet' });
    }

    // Verify pool exists AND belongs to user's farm
    const pool = await prisma.pool.findUnique({
      where: { id: data.poolId },
      include: { farm: { select: { ownerId: true } } },
    });
    if (!pool) {
      return reply.status(400).send({ error: 'Invalid configuration' });
    }
    if (user.role !== 'ADMIN' && pool.farm.ownerId !== user.userId) {
      return reply.status(403).send({ error: 'Access denied to pool' });
    }

    // Verify miner exists (miners are global)
    const miner = await prisma.minerSoftware.findUnique({ where: { id: data.minerId } });
    if (!miner) {
      return reply.status(400).send({ error: 'Invalid configuration' });
    }

    const flightSheet = await prisma.flightSheet.create({
      data: {
        name: data.name,
        coin: data.coin.toUpperCase(),
        walletId: data.walletId,
        poolId: data.poolId,
        minerId: data.minerId,
        extraArgs: data.extraArgs || null,
        farmId: data.farmId,
      },
      include: {
        wallet: true,
        pool: true,
        miner: true,
      },
    });

    auditLog({
      userId: user.userId,
      action: 'flightsheet_create',
      resource: 'flightSheet',
      resourceId: flightSheet.id,
      ip: request.ip,
      success: true,
    });

    return reply.status(201).send(flightSheet);
  });

  // Update flight sheet - with authorization check
  app.patch('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const data = UpdateFlightSheetSchema.parse(request.body);

    // Validate extraArgs if provided
    if (data.extraArgs) {
      try {
        validateExtraArgs(data.extraArgs);
      } catch {
        return reply.status(400).send({ error: 'Invalid extra arguments' });
      }
    }

    const existing = await prisma.flightSheet.findUnique({
      where: { id },
      include: { farm: { select: { ownerId: true } } },
    });

    if (!existing) {
      return reply.status(404).send({ error: 'Flight sheet not found' });
    }

    if (user.role !== 'ADMIN' && existing.farm.ownerId !== user.userId) {
      return reply.status(403).send({ error: 'Access denied' });
    }

    // If changing wallet/pool, verify ownership
    if (data.walletId) {
      const wallet = await prisma.wallet.findUnique({
        where: { id: data.walletId },
        include: { farm: { select: { ownerId: true } } },
      });
      if (!wallet || (user.role !== 'ADMIN' && wallet.farm.ownerId !== user.userId)) {
        return reply.status(403).send({ error: 'Access denied to wallet' });
      }
    }

    if (data.poolId) {
      const pool = await prisma.pool.findUnique({
        where: { id: data.poolId },
        include: { farm: { select: { ownerId: true } } },
      });
      if (!pool || (user.role !== 'ADMIN' && pool.farm.ownerId !== user.userId)) {
        return reply.status(403).send({ error: 'Access denied to pool' });
      }
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

    auditLog({
      userId: user.userId,
      action: 'flightsheet_update',
      resource: 'flightSheet',
      resourceId: id,
      ip: request.ip,
      success: true,
    });

    return reply.send(flightSheet);
  });

  // Delete flight sheet - with authorization check
  app.delete('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const existing = await prisma.flightSheet.findUnique({
      where: { id },
      include: {
        farm: { select: { ownerId: true } },
        _count: { select: { rigs: true } },
      },
    });

    if (!existing) {
      return reply.status(404).send({ error: 'Flight sheet not found' });
    }

    if (user.role !== 'ADMIN' && existing.farm.ownerId !== user.userId) {
      return reply.status(403).send({ error: 'Access denied' });
    }

    if (existing._count.rigs > 0) {
      return reply.status(400).send({
        error: `Cannot delete flight sheet: assigned to ${existing._count.rigs} rig(s)`,
      });
    }

    await prisma.flightSheet.delete({ where: { id } });

    auditLog({
      userId: user.userId,
      action: 'flightsheet_delete',
      resource: 'flightSheet',
      resourceId: id,
      ip: request.ip,
      success: true,
    });

    return reply.send({ success: true });
  });
}
