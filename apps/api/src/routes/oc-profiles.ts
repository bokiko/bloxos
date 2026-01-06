import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';

// Validation schemas
const CreateOCProfileSchema = z.object({
  name: z.string().min(1).max(100),
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

const UpdateOCProfileSchema = CreateOCProfileSchema.partial();

export async function ocProfileRoutes(app: FastifyInstance) {
  // List all OC profiles
  app.get('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const profiles = await prisma.oCProfile.findMany({
      include: {
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

    const profile = await prisma.oCProfile.findUnique({
      where: { id },
      include: {
        rigs: {
          select: { id: true, name: true },
        },
      },
    });

    if (!profile) {
      return reply.status(404).send({ error: 'OC Profile not found' });
    }

    return reply.send(profile);
  });

  // Create OC profile
  app.post('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = CreateOCProfileSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const profile = await prisma.oCProfile.create({
      data: result.data,
    });

    return reply.status(201).send(profile);
  });

  // Update OC profile
  app.patch('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const result = UpdateOCProfileSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    try {
      const profile = await prisma.oCProfile.update({
        where: { id },
        data: result.data,
      });

      return reply.send(profile);
    } catch (error) {
      return reply.status(404).send({ error: 'OC Profile not found' });
    }
  });

  // Delete OC profile
  app.delete('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;

    try {
      // First, unassign from all rigs
      await prisma.rig.updateMany({
        where: { ocProfileId: id },
        data: { ocProfileId: null },
      });

      await prisma.oCProfile.delete({ where: { id } });
      return reply.status(204).send();
    } catch (error) {
      return reply.status(404).send({ error: 'OC Profile not found' });
    }
  });

  // Get preset profiles for common GPUs
  app.get('/presets/:gpu', async (request: FastifyRequest<{ Params: { gpu: string } }>, reply: FastifyReply) => {
    const { gpu } = request.params;
    const gpuLower = gpu.toLowerCase();

    // Common presets based on GPU model
    const presets: Record<string, any> = {
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
