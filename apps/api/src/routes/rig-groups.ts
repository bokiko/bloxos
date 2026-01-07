import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';
import { getUserFarmIds, filterOwnedRigIds } from '../middleware/authorization.ts';
import { auditLog } from '../utils/security.ts';

// Validation schemas
const CreateRigGroupSchema = z.object({
  name: z.string().min(1).max(100),
  farmId: z.string().min(1),
  color: z.string().regex(/^#[0-9A-Fa-f]{6}$/).optional(),
  description: z.string().max(500).nullable().optional(),
});

const UpdateRigGroupSchema = CreateRigGroupSchema.omit({ farmId: true }).partial();

const AssignRigsSchema = z.object({
  rigIds: z.array(z.string()),
});

export async function rigGroupRoutes(app: FastifyInstance) {
  // List all rig groups (filtered by user's farms)
  app.get('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const farmIds = await getUserFarmIds(user.userId, user.role);

    const groups = await prisma.rigGroup.findMany({
      where: {
        farmId: { in: farmIds },
      },
      include: {
        farm: {
          select: { id: true, name: true },
        },
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
    const user = request.user;

    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const group = await prisma.rigGroup.findUnique({
      where: { id },
      include: {
        farm: {
          select: { id: true, name: true, ownerId: true },
        },
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

    // Authorization check
    if (user.role !== 'ADMIN' && group.farm.ownerId !== user.userId) {
      auditLog({
        userId: user.userId,
        action: 'unauthorized_riggroup_access',
        resource: 'rigGroup',
        resourceId: id,
        ip: request.ip,
        success: false,
      });
      return reply.status(403).send({ error: 'Access denied' });
    }

    return reply.send(group);
  });

  // Create rig group
  app.post('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const result = CreateRigGroupSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Authorization check - verify user owns the farm
    const farmIds = await getUserFarmIds(user.userId, user.role);
    if (!farmIds.includes(result.data.farmId)) {
      auditLog({
        userId: user.userId,
        action: 'unauthorized_riggroup_create',
        resource: 'rigGroup',
        ip: request.ip,
        success: false,
        details: { farmId: result.data.farmId },
      });
      return reply.status(403).send({ error: 'Access denied to this farm' });
    }

    const group = await prisma.rigGroup.create({
      data: result.data,
      include: {
        farm: { select: { id: true, name: true } },
      },
    });

    auditLog({
      userId: user.userId,
      action: 'create_riggroup',
      resource: 'rigGroup',
      resourceId: group.id,
      ip: request.ip,
      success: true,
    });

    return reply.status(201).send(group);
  });

  // Update rig group
  app.patch('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const user = request.user;

    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const result = UpdateRigGroupSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Check if group exists and user has access
    const existing = await prisma.rigGroup.findUnique({
      where: { id },
      include: { farm: { select: { ownerId: true } } },
    });

    if (!existing) {
      return reply.status(404).send({ error: 'Rig group not found' });
    }

    if (user.role !== 'ADMIN' && existing.farm.ownerId !== user.userId) {
      auditLog({
        userId: user.userId,
        action: 'unauthorized_riggroup_update',
        resource: 'rigGroup',
        resourceId: id,
        ip: request.ip,
        success: false,
      });
      return reply.status(403).send({ error: 'Access denied' });
    }

    const group = await prisma.rigGroup.update({
      where: { id },
      data: result.data,
      include: {
        farm: { select: { id: true, name: true } },
      },
    });

    auditLog({
      userId: user.userId,
      action: 'update_riggroup',
      resource: 'rigGroup',
      resourceId: id,
      ip: request.ip,
      success: true,
    });

    return reply.send(group);
  });

  // Delete rig group
  app.delete('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const user = request.user;

    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    // Check if group exists and user has access
    const existing = await prisma.rigGroup.findUnique({
      where: { id },
      include: { farm: { select: { ownerId: true } } },
    });

    if (!existing) {
      return reply.status(404).send({ error: 'Rig group not found' });
    }

    if (user.role !== 'ADMIN' && existing.farm.ownerId !== user.userId) {
      auditLog({
        userId: user.userId,
        action: 'unauthorized_riggroup_delete',
        resource: 'rigGroup',
        resourceId: id,
        ip: request.ip,
        success: false,
      });
      return reply.status(403).send({ error: 'Access denied' });
    }

    await prisma.rigGroup.delete({ where: { id } });

    auditLog({
      userId: user.userId,
      action: 'delete_riggroup',
      resource: 'rigGroup',
      resourceId: id,
      ip: request.ip,
      success: true,
    });

    return reply.status(204).send();
  });

  // Add rigs to group (many-to-many: connect)
  app.post('/:id/rigs', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const user = request.user;

    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const result = AssignRigsSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Check if group exists and user has access
    const group = await prisma.rigGroup.findUnique({
      where: { id },
      include: { farm: { select: { ownerId: true } } },
    });

    if (!group) {
      return reply.status(404).send({ error: 'Rig group not found' });
    }

    if (user.role !== 'ADMIN' && group.farm.ownerId !== user.userId) {
      auditLog({
        userId: user.userId,
        action: 'unauthorized_riggroup_add_rigs',
        resource: 'rigGroup',
        resourceId: id,
        ip: request.ip,
        success: false,
      });
      return reply.status(403).send({ error: 'Access denied' });
    }

    // Filter rigs to only those the user owns
    const ownedRigIds = await filterOwnedRigIds(result.data.rigIds, user.userId, user.role);

    if (ownedRigIds.length === 0) {
      return reply.status(400).send({ error: 'No valid rigs to add' });
    }

    // Connect rigs to group (many-to-many)
    await prisma.rigGroup.update({
      where: { id },
      data: {
        rigs: {
          connect: ownedRigIds.map(rigId => ({ id: rigId })),
        },
      },
    });

    auditLog({
      userId: user.userId,
      action: 'add_rigs_to_group',
      resource: 'rigGroup',
      resourceId: id,
      ip: request.ip,
      success: true,
      details: { rigIds: ownedRigIds },
    });

    return reply.send({ success: true, added: ownedRigIds.length });
  });

  // Remove rigs from group (many-to-many: disconnect)
  app.delete('/:id/rigs', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const user = request.user;

    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const result = AssignRigsSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Check if group exists and user has access
    const group = await prisma.rigGroup.findUnique({
      where: { id },
      include: { farm: { select: { ownerId: true } } },
    });

    if (!group) {
      return reply.status(404).send({ error: 'Rig group not found' });
    }

    if (user.role !== 'ADMIN' && group.farm.ownerId !== user.userId) {
      auditLog({
        userId: user.userId,
        action: 'unauthorized_riggroup_remove_rigs',
        resource: 'rigGroup',
        resourceId: id,
        ip: request.ip,
        success: false,
      });
      return reply.status(403).send({ error: 'Access denied' });
    }

    // Filter rigs to only those the user owns
    const ownedRigIds = await filterOwnedRigIds(result.data.rigIds, user.userId, user.role);

    // Disconnect rigs from group
    await prisma.rigGroup.update({
      where: { id },
      data: {
        rigs: {
          disconnect: ownedRigIds.map(rigId => ({ id: rigId })),
        },
      },
    });

    auditLog({
      userId: user.userId,
      action: 'remove_rigs_from_group',
      resource: 'rigGroup',
      resourceId: id,
      ip: request.ip,
      success: true,
      details: { rigIds: ownedRigIds },
    });

    return reply.send({ success: true, removed: ownedRigIds.length });
  });
}
