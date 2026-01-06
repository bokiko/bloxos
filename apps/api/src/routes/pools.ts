import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';

// Validation schemas
const CreatePoolSchema = z.object({
  name: z.string().min(1).max(100),
  coin: z.string().min(1).max(20),
  url: z.string().min(1).max(500),
  url2: z.string().max(500).optional(),
  url3: z.string().max(500).optional(),
  user: z.string().max(200).optional(),
  pass: z.string().max(100).optional(),
});

const UpdatePoolSchema = z.object({
  name: z.string().min(1).max(100).optional(),
  coin: z.string().min(1).max(20).optional(),
  url: z.string().min(1).max(500).optional(),
  url2: z.string().max(500).nullable().optional(),
  url3: z.string().max(500).nullable().optional(),
  user: z.string().max(200).nullable().optional(),
  pass: z.string().max(100).nullable().optional(),
});

export async function poolRoutes(app: FastifyInstance) {
  // List all pools
  app.get('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const pools = await prisma.pool.findMany({
      include: {
        _count: {
          select: { flightSheets: true },
        },
      },
      orderBy: { createdAt: 'desc' },
    });

    return reply.send(pools);
  });

  // Get single pool
  app.get('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;

    const pool = await prisma.pool.findUnique({
      where: { id },
      include: {
        flightSheets: true,
      },
    });

    if (!pool) {
      return reply.status(404).send({ message: 'Pool not found' });
    }

    return reply.send(pool);
  });

  // Create pool
  app.post('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const data = CreatePoolSchema.parse(request.body);

    const pool = await prisma.pool.create({
      data: {
        name: data.name,
        coin: data.coin.toUpperCase(),
        url: data.url,
        url2: data.url2 || null,
        url3: data.url3 || null,
        user: data.user || null,
        pass: data.pass || null,
      },
    });

    return reply.status(201).send(pool);
  });

  // Update pool
  app.patch('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const data = UpdatePoolSchema.parse(request.body);

    const existing = await prisma.pool.findUnique({ where: { id } });
    if (!existing) {
      return reply.status(404).send({ message: 'Pool not found' });
    }

    const pool = await prisma.pool.update({
      where: { id },
      data: {
        ...data,
        coin: data.coin?.toUpperCase(),
      },
    });

    return reply.send(pool);
  });

  // Delete pool
  app.delete('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;

    const existing = await prisma.pool.findUnique({
      where: { id },
      include: { _count: { select: { flightSheets: true } } },
    });

    if (!existing) {
      return reply.status(404).send({ message: 'Pool not found' });
    }

    if (existing._count.flightSheets > 0) {
      return reply.status(400).send({
        message: `Cannot delete pool: used by ${existing._count.flightSheets} flight sheet(s)`,
      });
    }

    await prisma.pool.delete({ where: { id } });

    return reply.send({ success: true });
  });
}
