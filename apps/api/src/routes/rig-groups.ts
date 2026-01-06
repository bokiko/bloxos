import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';

// Validation schemas
const CreateRigGroupSchema = z.object({
  name: z.string().min(1).max(100),
  color: z.string().regex(/^#[0-9A-Fa-f]{6}$/).optional(),
  description: z.string().max(500).nullable().optional(),
});

const UpdateRigGroupSchema = CreateRigGroupSchema.partial();

const AssignRigsSchema = z.object({
  rigIds: z.array(z.string()),
});

export async function rigGroupRoutes(app: FastifyInstance) {
  // List all rig groups
  app.get('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const groups = await prisma.rigGroup.findMany({
      include: {
        _count: {
          select: { rigs: true },
        },
        rigs: {
          select: {
            id: true,
            name: true,
            status: true,
          },
        },
      },
      orderBy: { name: 'asc' },
    });

    return reply.send(groups);
  });

  // Get single rig group with full rig details
  app.get('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;

    const group = await prisma.rigGroup.findUnique({
      where: { id },
      include: {
        rigs: {
          include: {
            gpus: true,
            cpu: true,
            flightSheet: true,
            ocProfile: true,
            groups: true,
          },
        },
      },
    });

    if (!group) {
      return reply.status(404).send({ error: 'Rig group not found' });
    }

    return reply.send(group);
  });

  // Create rig group
  app.post('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = CreateRigGroupSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const group = await prisma.rigGroup.create({
      data: result.data,
    });

    return reply.status(201).send(group);
  });

  // Update rig group
  app.patch('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const result = UpdateRigGroupSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    try {
      const group = await prisma.rigGroup.update({
        where: { id },
        data: result.data,
      });

      return reply.send(group);
    } catch (error) {
      return reply.status(404).send({ error: 'Rig group not found' });
    }
  });

  // Delete rig group
  app.delete('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;

    try {
      await prisma.rigGroup.delete({ where: { id } });
      return reply.status(204).send();
    } catch (error) {
      return reply.status(404).send({ error: 'Rig group not found' });
    }
  });

  // Add rigs to group (many-to-many: connect)
  app.post('/:id/rigs', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const result = AssignRigsSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Check if group exists
    const group = await prisma.rigGroup.findUnique({ where: { id } });
    if (!group) {
      return reply.status(404).send({ error: 'Rig group not found' });
    }

    // Connect rigs to group (many-to-many)
    await prisma.rigGroup.update({
      where: { id },
      data: {
        rigs: {
          connect: result.data.rigIds.map(rigId => ({ id: rigId })),
        },
      },
    });

    return reply.send({ success: true, added: result.data.rigIds.length });
  });

  // Remove rigs from group (many-to-many: disconnect)
  app.delete('/:id/rigs', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const result = AssignRigsSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Disconnect rigs from group
    await prisma.rigGroup.update({
      where: { id },
      data: {
        rigs: {
          disconnect: result.data.rigIds.map(rigId => ({ id: rigId })),
        },
      },
    });

    return reply.send({ success: true, removed: result.data.rigIds.length });
  });
}
