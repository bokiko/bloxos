import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';
import { getUserFarmIds } from '../middleware/authorization.ts';
import { auditLog } from '../utils/security.ts';

// Pool URL validation pattern (MED-001)
const poolUrlPattern = /^(stratum\+tcp|stratum\+ssl|stratum2\+tcp|stratum2\+ssl|http|https):\/\/[a-zA-Z0-9.-]+(:\d+)?(\/.*)?$/;

// Validation schemas
const CreatePoolSchema = z.object({
  name: z.string().min(1).max(100),
  coin: z.string().min(1).max(20),
  url: z.string().min(1).max(500).regex(poolUrlPattern, 'Invalid pool URL format'),
  url2: z.string().max(500).regex(poolUrlPattern, 'Invalid pool URL format').optional(),
  url3: z.string().max(500).regex(poolUrlPattern, 'Invalid pool URL format').optional(),
  user: z.string().max(200).optional(),
  pass: z.string().max(100).optional(),
  farmId: z.string().cuid(),
});

const UpdatePoolSchema = z.object({
  name: z.string().min(1).max(100).optional(),
  coin: z.string().min(1).max(20).optional(),
  url: z.string().min(1).max(500).regex(poolUrlPattern, 'Invalid pool URL format').optional(),
  url2: z.string().max(500).regex(poolUrlPattern, 'Invalid pool URL format').nullable().optional(),
  url3: z.string().max(500).regex(poolUrlPattern, 'Invalid pool URL format').nullable().optional(),
  user: z.string().max(200).nullable().optional(),
  pass: z.string().max(100).nullable().optional(),
});

export async function poolRoutes(app: FastifyInstance) {
  // List pools - filtered by user's farms
  app.get('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const farmIds = await getUserFarmIds(user.userId, user.role);

    const pools = await prisma.pool.findMany({
      where: { farmId: { in: farmIds } },
      include: {
        farm: { select: { id: true, name: true } },
        _count: {
          select: { flightSheets: true },
        },
      },
      orderBy: { createdAt: 'desc' },
    });

    return reply.send(pools);
  });

  // Get single pool - with authorization check
  app.get('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const pool = await prisma.pool.findUnique({
      where: { id },
      include: {
        farm: { select: { id: true, name: true, ownerId: true } },
        flightSheets: true,
      },
    });

    if (!pool) {
      return reply.status(404).send({ error: 'Pool not found' });
    }

    if (user.role !== 'ADMIN' && pool.farm.ownerId !== user.userId) {
      return reply.status(403).send({ error: 'Access denied' });
    }

    return reply.send(pool);
  });

  // Create pool - verify user owns the farm
  app.post('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const data = CreatePoolSchema.parse(request.body);

    const farmIds = await getUserFarmIds(user.userId, user.role);
    if (!farmIds.includes(data.farmId)) {
      return reply.status(403).send({ error: 'Access denied to this farm' });
    }

    const pool = await prisma.pool.create({
      data: {
        name: data.name,
        coin: data.coin.toUpperCase(),
        url: data.url,
        url2: data.url2 || null,
        url3: data.url3 || null,
        user: data.user || null,
        pass: data.pass || null,
        farmId: data.farmId,
      },
    });

    auditLog({
      userId: user.userId,
      action: 'pool_create',
      resource: 'pool',
      resourceId: pool.id,
      ip: request.ip,
      success: true,
    });

    return reply.status(201).send(pool);
  });

  // Update pool - with authorization check
  app.patch('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const data = UpdatePoolSchema.parse(request.body);

    const existing = await prisma.pool.findUnique({
      where: { id },
      include: { farm: { select: { ownerId: true } } },
    });

    if (!existing) {
      return reply.status(404).send({ error: 'Pool not found' });
    }

    if (user.role !== 'ADMIN' && existing.farm.ownerId !== user.userId) {
      return reply.status(403).send({ error: 'Access denied' });
    }

    const pool = await prisma.pool.update({
      where: { id },
      data: {
        ...data,
        coin: data.coin?.toUpperCase(),
      },
    });

    auditLog({
      userId: user.userId,
      action: 'pool_update',
      resource: 'pool',
      resourceId: id,
      ip: request.ip,
      success: true,
    });

    return reply.send(pool);
  });

  // Delete pool - with authorization check
  app.delete('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const existing = await prisma.pool.findUnique({
      where: { id },
      include: {
        farm: { select: { ownerId: true } },
        _count: { select: { flightSheets: true } },
      },
    });

    if (!existing) {
      return reply.status(404).send({ error: 'Pool not found' });
    }

    if (user.role !== 'ADMIN' && existing.farm.ownerId !== user.userId) {
      return reply.status(403).send({ error: 'Access denied' });
    }

    if (existing._count.flightSheets > 0) {
      return reply.status(400).send({
        error: `Cannot delete pool: used by ${existing._count.flightSheets} flight sheet(s)`,
      });
    }

    await prisma.pool.delete({ where: { id } });

    auditLog({
      userId: user.userId,
      action: 'pool_delete',
      resource: 'pool',
      resourceId: id,
      ip: request.ip,
      success: true,
    });

    return reply.send({ success: true });
  });
}
