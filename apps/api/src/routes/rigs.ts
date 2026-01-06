import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';
import { nanoid } from 'nanoid';
import { gpuPoller } from '../services/gpu-poller.ts';
import { minerControl } from '../services/miner-control.ts';
import { ocService } from '../services/oc-service.ts';

// Validation schemas
const CreateRigSchema = z.object({
  name: z.string().min(1).max(100),
  farmId: z.string(),
  hostname: z.string().optional(),
  ipAddress: z.string().optional(),
});

const UpdateRigSchema = z.object({
  name: z.string().min(1).max(100).optional(),
  hostname: z.string().optional(),
  ipAddress: z.string().optional(),
  flightSheetId: z.string().nullable().optional(),
  ocProfileId: z.string().nullable().optional(),
});

export async function rigRoutes(app: FastifyInstance) {
  // List all rigs
  app.get('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const rigs = await prisma.rig.findMany({
      include: {
        gpus: true,
        cpu: true,
        farm: true,
        groups: true,
        minerInstances: true,
      },
      orderBy: { createdAt: 'desc' },
    });

    return reply.send(rigs);
  });

  // Get single rig
  app.get('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;

    const rig = await prisma.rig.findUnique({
      where: { id },
      include: {
        gpus: true,
        cpu: true,
        farm: true,
        groups: true,
        flightSheet: {
          include: {
            miner: true,
            pool: true,
            wallet: true,
          },
        },
        ocProfile: true,
        minerInstances: true,
        events: {
          take: 20,
          orderBy: { timestamp: 'desc' },
        },
      },
    });

    if (!rig) {
      return reply.status(404).send({ error: 'Rig not found' });
    }

    return reply.send(rig);
  });

  // Create rig
  app.post('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = CreateRigSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const { name, farmId, hostname, ipAddress } = result.data;

    // Generate unique token for agent authentication
    const token = nanoid(32);

    const rig = await prisma.rig.create({
      data: {
        name,
        hostname: hostname || name,
        ipAddress,
        token,
        farmId,
        status: 'OFFLINE',
      },
    });

    return reply.status(201).send(rig);
  });

  // Update rig
  app.patch('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const result = UpdateRigSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    try {
      const rig = await prisma.rig.update({
        where: { id },
        data: result.data,
      });

      return reply.send(rig);
    } catch (error) {
      return reply.status(404).send({ error: 'Rig not found' });
    }
  });

  // Delete rig
  app.delete('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;

    try {
      await prisma.rig.delete({ where: { id } });
      return reply.status(204).send();
    } catch (error) {
      return reply.status(404).send({ error: 'Rig not found' });
    }
  });

  // Get rig stats history
  app.get('/:id/stats', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;

    const stats = await prisma.rigStats.findMany({
      where: { rigId: id },
      orderBy: { timestamp: 'desc' },
      take: 100,
    });

    return reply.send(stats);
  });

  // Get live GPU stats for a rig
  app.get('/:id/gpu-stats', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;

    const rig = await prisma.rig.findUnique({
      where: { id },
      include: {
        gpus: {
          orderBy: { index: 'asc' },
        },
      },
    });

    if (!rig) {
      return reply.status(404).send({ error: 'Rig not found' });
    }

    return reply.send({
      rigId: rig.id,
      status: rig.status,
      lastSeen: rig.lastSeen,
      gpus: rig.gpus.map((gpu) => ({
        id: gpu.id,
        index: gpu.index,
        name: gpu.name,
        temperature: gpu.temperature,
        memTemp: gpu.memTemp,
        fanSpeed: gpu.fanSpeed,
        powerDraw: gpu.powerDraw,
        coreClock: gpu.coreClock,
        memoryClock: gpu.memoryClock,
        hashrate: gpu.hashrate,
      })),
    });
  });

  // Manually trigger GPU poll for a rig
  app.post('/:id/poll', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;

    const rig = await prisma.rig.findUnique({ where: { id } });
    if (!rig) {
      return reply.status(404).send({ error: 'Rig not found' });
    }

    const result = await gpuPoller.pollRig(id);

    if (result.success) {
      return reply.send({ success: true, message: 'GPU stats updated' });
    } else {
      return reply.status(500).send({ success: false, message: result.error });
    }
  });

  // Get poller status
  app.get('/poller/status', async (request: FastifyRequest, reply: FastifyReply) => {
    return reply.send({
      running: gpuPoller.isRunning(),
    });
  });

  // Toggle CPU/GPU monitoring for a rig
  app.patch('/:id/monitoring', async (request: FastifyRequest<{ 
    Params: { id: string };
    Body: { cpuMiningEnabled?: boolean; gpuMiningEnabled?: boolean };
  }>, reply: FastifyReply) => {
    const { id } = request.params;
    const { cpuMiningEnabled, gpuMiningEnabled } = request.body as { cpuMiningEnabled?: boolean; gpuMiningEnabled?: boolean };

    const rig = await prisma.rig.findUnique({ where: { id } });
    if (!rig) {
      return reply.status(404).send({ error: 'Rig not found' });
    }

    const updateData: { cpuMiningEnabled?: boolean; gpuMiningEnabled?: boolean } = {};
    if (typeof cpuMiningEnabled === 'boolean') updateData.cpuMiningEnabled = cpuMiningEnabled;
    if (typeof gpuMiningEnabled === 'boolean') updateData.gpuMiningEnabled = gpuMiningEnabled;

    const updated = await prisma.rig.update({
      where: { id },
      data: updateData,
    });

    return reply.send({
      success: true,
      cpuMiningEnabled: updated.cpuMiningEnabled,
      gpuMiningEnabled: updated.gpuMiningEnabled,
    });
  });

  // Assign flight sheet to rig
  app.patch('/:id/flight-sheet', async (request: FastifyRequest<{ 
    Params: { id: string };
    Body: { flightSheetId: string | null };
  }>, reply: FastifyReply) => {
    const { id } = request.params;
    const { flightSheetId } = request.body as { flightSheetId: string | null };

    const rig = await prisma.rig.findUnique({ where: { id } });
    if (!rig) {
      return reply.status(404).send({ error: 'Rig not found' });
    }

    // Verify flight sheet exists if assigning
    if (flightSheetId) {
      const fs = await prisma.flightSheet.findUnique({ where: { id: flightSheetId } });
      if (!fs) {
        return reply.status(400).send({ error: 'Flight sheet not found' });
      }
    }

    const updated = await prisma.rig.update({
      where: { id },
      data: { flightSheetId },
      include: {
        flightSheet: {
          include: { wallet: true, pool: true, miner: true },
        },
      },
    });

    return reply.send({
      success: true,
      flightSheet: updated.flightSheet,
    });
  });

  // Start miner on rig
  app.post('/:id/miner/start', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const result = await minerControl.startMiner(id);
    
    if (!result.success) {
      return reply.status(400).send(result);
    }
    
    return reply.send(result);
  });

  // Stop miner on rig
  app.post('/:id/miner/stop', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const result = await minerControl.stopMiner(id);
    
    if (!result.success) {
      return reply.status(400).send(result);
    }
    
    return reply.send(result);
  });

  // Get miner status
  app.get('/:id/miner/status', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const status = await minerControl.getMinerStatus(id);
    return reply.send(status);
  });

  // Assign OC profile to rig
  app.patch('/:id/oc-profile', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const { ocProfileId } = request.body as { ocProfileId: string | null };

    const rig = await prisma.rig.findUnique({ where: { id } });
    if (!rig) {
      return reply.status(404).send({ error: 'Rig not found' });
    }

    // Verify OC profile exists if assigning
    if (ocProfileId) {
      const profile = await prisma.oCProfile.findUnique({ where: { id: ocProfileId } });
      if (!profile) {
        return reply.status(400).send({ error: 'OC profile not found' });
      }
    }

    await prisma.rig.update({
      where: { id },
      data: { ocProfileId },
    });

    return reply.send({ success: true, ocProfileId });
  });

  // Apply OC profile to rig
  app.post('/:id/oc-profile/apply', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const result = await ocService.applyOCProfile(id);
    
    if (!result.success) {
      return reply.status(400).send(result);
    }
    
    return reply.send(result);
  });

  // Reset OC settings on rig
  app.post('/:id/oc-profile/reset', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const result = await ocService.resetOC(id);
    
    if (!result.success) {
      return reply.status(400).send(result);
    }
    
    return reply.send(result);
  });

  // Set rig groups (many-to-many)
  app.patch('/:id/groups', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const { groupIds } = request.body as { groupIds: string[] };

    const rig = await prisma.rig.findUnique({ where: { id } });
    if (!rig) {
      return reply.status(404).send({ error: 'Rig not found' });
    }

    // Update rig groups (set = replace all)
    await prisma.rig.update({
      where: { id },
      data: {
        groups: {
          set: groupIds.map(gid => ({ id: gid })),
        },
      },
    });

    // Return updated rig with groups
    const updated = await prisma.rig.findUnique({
      where: { id },
      include: { groups: true },
    });

    return reply.send({ success: true, groups: updated?.groups || [] });
  });

  // Add rig to a group
  app.post('/:id/groups/:groupId', async (request: FastifyRequest<{ Params: { id: string; groupId: string } }>, reply: FastifyReply) => {
    const { id, groupId } = request.params;

    await prisma.rig.update({
      where: { id },
      data: {
        groups: {
          connect: { id: groupId },
        },
      },
    });

    return reply.send({ success: true });
  });

  // Remove rig from a group
  app.delete('/:id/groups/:groupId', async (request: FastifyRequest<{ Params: { id: string; groupId: string } }>, reply: FastifyReply) => {
    const { id, groupId } = request.params;

    await prisma.rig.update({
      where: { id },
      data: {
        groups: {
          disconnect: { id: groupId },
        },
      },
    });

    return reply.send({ success: true });
  });
}
