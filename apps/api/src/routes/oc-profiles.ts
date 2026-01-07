import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';
import { getUserFarmIds } from '../middleware/authorization.ts';
import { auditLog } from '../utils/security.ts';

// Validation schemas
const CreateOCProfileSchema = z.object({
  name: z.string().min(1).max(100),
  farmId: z.string().min(1),
  vendor: z.enum(['NVIDIA', 'AMD', 'INTEL']),
  // NVIDIA settings
  powerLimit: z.number().min(50).max(500).nullable().optional(),
  coreOffset: z.number().min(-500).max(500).nullable().optional(),
  memOffset: z.number().min(-2000).max(2000).nullable().optional(),
  coreLock: z.number().min(200).max(3000).nullable().optional(),
  memLock: z.number().min(200).max(12000).nullable().optional(),
  fanSpeed: z.number().min(0).max(100).nullable().optional(),
  // AMD settings
  coreVddc: z.number().min(600).max(1200).nullable().optional(),
  memVddc: z.number().min(600).max(1200).nullable().optional(),
  coreDpm: z.number().min(0).max(7).nullable().optional(),
  memDpm: z.number().min(0).max(3).nullable().optional(),
});

const UpdateOCProfileSchema = CreateOCProfileSchema.omit({ farmId: true }).partial();

export async function ocProfileRoutes(app: FastifyInstance) {
  // List all OC profiles (filtered by user's farms)
  app.get('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const farmIds = await getUserFarmIds(user.userId, user.role);

    const profiles = await prisma.oCProfile.findMany({
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
      },
      orderBy: { name: 'asc' },
    });

    return reply.send(profiles);
  });

  // Get single OC profile
  app.get('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const user = request.user;

    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const profile = await prisma.oCProfile.findUnique({
      where: { id },
      include: {
        farm: {
          select: { id: true, name: true, ownerId: true },
        },
        rigs: {
          select: { id: true, name: true },
        },
      },
    });

    if (!profile) {
      return reply.status(404).send({ error: 'OC Profile not found' });
    }

    // Authorization check
    if (user.role !== 'ADMIN' && profile.farm.ownerId !== user.userId) {
      auditLog({
        userId: user.userId,
        action: 'unauthorized_ocprofile_access',
        resource: 'ocProfile',
        resourceId: id,
        ip: request.ip,
        success: false,
      });
      return reply.status(403).send({ error: 'Access denied' });
    }

    return reply.send(profile);
  });

  // Create OC profile
  app.post('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const result = CreateOCProfileSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Authorization check - verify user owns the farm
    const farmIds = await getUserFarmIds(user.userId, user.role);
    if (!farmIds.includes(result.data.farmId)) {
      auditLog({
        userId: user.userId,
        action: 'unauthorized_ocprofile_create',
        resource: 'ocProfile',
        ip: request.ip,
        success: false,
        details: { farmId: result.data.farmId },
      });
      return reply.status(403).send({ error: 'Access denied to this farm' });
    }

    const profile = await prisma.oCProfile.create({
      data: result.data,
      include: {
        farm: { select: { id: true, name: true } },
      },
    });

    auditLog({
      userId: user.userId,
      action: 'create_ocprofile',
      resource: 'ocProfile',
      resourceId: profile.id,
      ip: request.ip,
      success: true,
    });

    return reply.status(201).send(profile);
  });

  // Update OC profile
  app.patch('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const user = request.user;

    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const result = UpdateOCProfileSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Check if profile exists and user has access
    const existing = await prisma.oCProfile.findUnique({
      where: { id },
      include: { farm: { select: { ownerId: true } } },
    });

    if (!existing) {
      return reply.status(404).send({ error: 'OC Profile not found' });
    }

    if (user.role !== 'ADMIN' && existing.farm.ownerId !== user.userId) {
      auditLog({
        userId: user.userId,
        action: 'unauthorized_ocprofile_update',
        resource: 'ocProfile',
        resourceId: id,
        ip: request.ip,
        success: false,
      });
      return reply.status(403).send({ error: 'Access denied' });
    }

    const profile = await prisma.oCProfile.update({
      where: { id },
      data: result.data,
      include: {
        farm: { select: { id: true, name: true } },
      },
    });

    auditLog({
      userId: user.userId,
      action: 'update_ocprofile',
      resource: 'ocProfile',
      resourceId: id,
      ip: request.ip,
      success: true,
    });

    return reply.send(profile);
  });

  // Delete OC profile
  app.delete('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const user = request.user;

    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    // Check if profile exists and user has access
    const existing = await prisma.oCProfile.findUnique({
      where: { id },
      include: { farm: { select: { ownerId: true } } },
    });

    if (!existing) {
      return reply.status(404).send({ error: 'OC Profile not found' });
    }

    if (user.role !== 'ADMIN' && existing.farm.ownerId !== user.userId) {
      auditLog({
        userId: user.userId,
        action: 'unauthorized_ocprofile_delete',
        resource: 'ocProfile',
        resourceId: id,
        ip: request.ip,
        success: false,
      });
      return reply.status(403).send({ error: 'Access denied' });
    }

    // First, unassign from all rigs in user's farms
    const farmIds = await getUserFarmIds(user.userId, user.role);
    await prisma.rig.updateMany({
      where: {
        ocProfileId: id,
        farmId: { in: farmIds },
      },
      data: { ocProfileId: null },
    });

    await prisma.oCProfile.delete({ where: { id } });

    auditLog({
      userId: user.userId,
      action: 'delete_ocprofile',
      resource: 'ocProfile',
      resourceId: id,
      ip: request.ip,
      success: true,
    });

    return reply.status(204).send();
  });

  // Get preset profiles for common GPUs (public - no auth needed)
  app.get('/presets/:gpu', async (request: FastifyRequest<{ Params: { gpu: string } }>, reply: FastifyReply) => {
    const { gpu } = request.params;
    const gpuLower = gpu.toLowerCase();

    // Common presets based on GPU model
    const presets: Record<string, Record<string, { name: string; powerLimit: number; coreOffset: number; memOffset: number }>> = {
      // RTX 3080
      '3080': {
        efficiency: { name: 'RTX 3080 Efficiency', powerLimit: 220, coreOffset: -200, memOffset: 1200 },
        balanced: { name: 'RTX 3080 Balanced', powerLimit: 280, coreOffset: -100, memOffset: 1000 },
        performance: { name: 'RTX 3080 Performance', powerLimit: 320, coreOffset: 0, memOffset: 800 },
      },
      // RTX 3090
      '3090': {
        efficiency: { name: 'RTX 3090 Efficiency', powerLimit: 280, coreOffset: -200, memOffset: 1100 },
        balanced: { name: 'RTX 3090 Balanced', powerLimit: 320, coreOffset: -100, memOffset: 900 },
        performance: { name: 'RTX 3090 Performance', powerLimit: 370, coreOffset: 0, memOffset: 700 },
      },
      // RTX 4090
      '4090': {
        efficiency: { name: 'RTX 4090 Efficiency', powerLimit: 300, coreOffset: -200, memOffset: 1500 },
        balanced: { name: 'RTX 4090 Balanced', powerLimit: 380, coreOffset: -100, memOffset: 1200 },
        performance: { name: 'RTX 4090 Performance', powerLimit: 450, coreOffset: 0, memOffset: 1000 },
      },
    };

    // Find matching preset
    for (const [key, value] of Object.entries(presets)) {
      if (gpuLower.includes(key)) {
        return reply.send(value);
      }
    }

    return reply.send({});
  });
}
