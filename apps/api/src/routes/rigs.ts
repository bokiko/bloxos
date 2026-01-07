import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';
import { nanoid } from 'nanoid';
import { gpuPoller } from '../services/gpu-poller.ts';
import { minerControl } from '../services/miner-control.ts';
import { ocService } from '../services/oc-service.ts';
import { requireRigAccess, requireFarmAccess, getUserRigFilter } from '../middleware/authorization.ts';
import { auditLog } from '../utils/security.ts';
import { sendCommandToRig, isAgentConnected } from './agent-websocket.ts';

// Validation schemas
const CreateRigSchema = z.object({
  name: z.string().min(1).max(100),
  farmId: z.string().max(50),
  hostname: z.string().max(253).optional(), // Max DNS hostname length
  ipAddress: z.string().max(45).optional(),  // Max IPv6 length
});

const UpdateRigSchema = z.object({
  name: z.string().min(1).max(100).optional(),
  hostname: z.string().max(253).optional(),
  ipAddress: z.string().max(45).optional(),
  flightSheetId: z.string().max(50).nullable().optional(),
  ocProfileId: z.string().max(50).nullable().optional(),
});

export async function rigRoutes(app: FastifyInstance) {
  // List all rigs (filtered by user's farm ownership)
  app.get('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const filter = getUserRigFilter(user);
    
    const rigs = await prisma.rig.findMany({
      where: filter,
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

  // Get single rig (with authorization check)
  app.get<{ Params: { id: string } }>('/:id', { preHandler: [requireRigAccess] }, async (request, reply) => {
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

  // Create rig (with farm authorization check)
  app.post('/', { preHandler: [requireFarmAccess] }, async (request: FastifyRequest, reply: FastifyReply) => {
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

    auditLog({
      userId: request.user?.userId,
      action: 'create_rig',
      resource: 'rig',
      resourceId: rig.id,
      details: { name, farmId },
      ip: request.ip,
      success: true,
    });

    return reply.status(201).send(rig);
  });

  // Update rig (with authorization check)
  app.patch<{ Params: { id: string } }>('/:id', { preHandler: [requireRigAccess] }, async (request, reply) => {
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

      auditLog({
        userId: request.user?.userId,
        action: 'update_rig',
        resource: 'rig',
        resourceId: id,
        ip: request.ip,
        success: true,
      });

      return reply.send(rig);
    } catch {
      return reply.status(404).send({ error: 'Rig not found' });
    }
  });

  // Delete rig (with authorization check)
  app.delete<{ Params: { id: string } }>('/:id', { preHandler: [requireRigAccess] }, async (request, reply) => {
    const { id } = request.params;

    try {
      await prisma.rig.delete({ where: { id } });
      
      auditLog({
        userId: request.user?.userId,
        action: 'delete_rig',
        resource: 'rig',
        resourceId: id,
        ip: request.ip,
        success: true,
      });

      return reply.status(204).send();
    } catch {
      return reply.status(404).send({ error: 'Rig not found' });
    }
  });

  // Get rig stats history (with authorization check)
  app.get<{ Params: { id: string } }>('/:id/stats', { preHandler: [requireRigAccess] }, async (request, reply) => {
    const { id } = request.params;

    const stats = await prisma.rigStats.findMany({
      where: { rigId: id },
      orderBy: { timestamp: 'desc' },
      take: 100,
    });

    return reply.send(stats);
  });

  // Get live GPU stats for a rig (with authorization check)
  app.get<{ Params: { id: string } }>('/:id/gpu-stats', { preHandler: [requireRigAccess] }, async (request, reply) => {
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

  // Manually trigger GPU poll for a rig (with authorization check)
  app.post<{ Params: { id: string } }>('/:id/poll', { preHandler: [requireRigAccess] }, async (request, reply) => {
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

  // Toggle CPU/GPU monitoring for a rig (with authorization check)
  app.patch<{ Params: { id: string } }>('/:id/monitoring', { preHandler: [requireRigAccess] }, async (request, reply) => {
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

  // Assign flight sheet to rig (with authorization check)
  app.patch<{ Params: { id: string } }>('/:id/flight-sheet', { preHandler: [requireRigAccess] }, async (request, reply) => {
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

  // Start miner on rig (with authorization check)
  app.post<{ Params: { id: string } }>('/:id/miner/start', { preHandler: [requireRigAccess] }, async (request, reply) => {
    const { id } = request.params;
    const result = await minerControl.startMiner(id);
    
    if (!result.success) {
      return reply.status(400).send(result);
    }
    
    return reply.send(result);
  });

  // Stop miner on rig (with authorization check)
  app.post<{ Params: { id: string } }>('/:id/miner/stop', { preHandler: [requireRigAccess] }, async (request, reply) => {
    const { id } = request.params;
    const result = await minerControl.stopMiner(id);
    
    if (!result.success) {
      return reply.status(400).send(result);
    }
    
    return reply.send(result);
  });

  // Get miner status (with authorization check)
  app.get<{ Params: { id: string } }>('/:id/miner/status', { preHandler: [requireRigAccess] }, async (request, reply) => {
    const { id } = request.params;
    const status = await minerControl.getMinerStatus(id);
    return reply.send(status);
  });

  // Assign OC profile to rig (with authorization check)
  app.patch<{ Params: { id: string } }>('/:id/oc-profile', { preHandler: [requireRigAccess] }, async (request, reply) => {
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

  // Apply OC profile to rig (with authorization check)
  app.post<{ Params: { id: string } }>('/:id/oc-profile/apply', { preHandler: [requireRigAccess] }, async (request, reply) => {
    const { id } = request.params;
    const result = await ocService.applyOCProfile(id);
    
    if (!result.success) {
      return reply.status(400).send(result);
    }
    
    return reply.send(result);
  });

  // Reset OC settings on rig (with authorization check)
  app.post<{ Params: { id: string } }>('/:id/oc-profile/reset', { preHandler: [requireRigAccess] }, async (request, reply) => {
    const { id } = request.params;
    const result = await ocService.resetOC(id);
    
    if (!result.success) {
      return reply.status(400).send(result);
    }
    
    return reply.send(result);
  });

  // Set rig groups (with authorization check)
  app.patch<{ Params: { id: string } }>('/:id/groups', { preHandler: [requireRigAccess] }, async (request, reply) => {
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

  // Add rig to a group (with authorization check)
  app.post<{ Params: { id: string; groupId: string } }>('/:id/groups/:groupId', { preHandler: [requireRigAccess] }, async (request, reply) => {
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

  // Remove rig from a group (with authorization check)
  app.delete<{ Params: { id: string; groupId: string } }>('/:id/groups/:groupId', { preHandler: [requireRigAccess] }, async (request, reply) => {
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

  // ============================================
  // WebSocket-based Agent Commands
  // ============================================

  // Check if agent is connected
  app.get<{ Params: { id: string } }>('/:id/agent/status', { preHandler: [requireRigAccess] }, async (request, reply) => {
    const { id } = request.params;
    const connected = isAgentConnected(id);
    return reply.send({ connected });
  });

  // Start miner via WebSocket agent
  app.post<{ Params: { id: string } }>('/:id/command/start-miner', { preHandler: [requireRigAccess] }, async (request, reply) => {
    const { id } = request.params;

    // Get rig with flight sheet
    const rig = await prisma.rig.findUnique({
      where: { id },
      include: {
        flightSheet: {
          include: { wallet: true, pool: true, miner: true },
        },
      },
    });

    if (!rig) {
      return reply.status(404).send({ error: 'Rig not found' });
    }

    if (!rig.flightSheet) {
      return reply.status(400).send({ error: 'No flight sheet assigned' });
    }

    const { flightSheet } = rig;

    // Build miner config for agent
    const minerConfig = {
      name: flightSheet.miner.name,
      algorithm: flightSheet.miner.algo,
      pool: flightSheet.pool.url,
      wallet: flightSheet.wallet.address,
      worker: rig.name.replace(/[^a-zA-Z0-9_-]/g, '_'),
      extraArgs: flightSheet.extraArgs?.split(/\s+/).filter(Boolean) || [],
    };

    const commandId = sendCommandToRig(id, {
      type: 'start_miner',
      payload: minerConfig,
    });

    auditLog({
      userId: request.user?.userId,
      action: 'command_start_miner',
      resource: 'rig',
      resourceId: id,
      details: { commandId, miner: minerConfig.name },
      ip: request.ip,
      success: true,
    });

    return reply.send({
      success: true,
      commandId,
      queued: !isAgentConnected(id),
      message: isAgentConnected(id) ? 'Command sent to agent' : 'Command queued (agent offline)',
    });
  });

  // Stop miner via WebSocket agent
  app.post<{ Params: { id: string } }>('/:id/command/stop-miner', { preHandler: [requireRigAccess] }, async (request, reply) => {
    const { id } = request.params;

    const commandId = sendCommandToRig(id, { type: 'stop_miner' });

    auditLog({
      userId: request.user?.userId,
      action: 'command_stop_miner',
      resource: 'rig',
      resourceId: id,
      details: { commandId },
      ip: request.ip,
      success: true,
    });

    return reply.send({
      success: true,
      commandId,
      queued: !isAgentConnected(id),
    });
  });

  // Restart miner via WebSocket agent
  app.post<{ Params: { id: string } }>('/:id/command/restart-miner', { preHandler: [requireRigAccess] }, async (request, reply) => {
    const { id } = request.params;

    const commandId = sendCommandToRig(id, { type: 'restart_miner' });

    auditLog({
      userId: request.user?.userId,
      action: 'command_restart_miner',
      resource: 'rig',
      resourceId: id,
      details: { commandId },
      ip: request.ip,
      success: true,
    });

    return reply.send({
      success: true,
      commandId,
      queued: !isAgentConnected(id),
    });
  });

  // Apply OC via WebSocket agent
  app.post<{ Params: { id: string } }>('/:id/command/apply-oc', { preHandler: [requireRigAccess] }, async (request, reply) => {
    const { id } = request.params;

    // Get rig with OC profile
    const rig = await prisma.rig.findUnique({
      where: { id },
      include: { ocProfile: true },
    });

    if (!rig) {
      return reply.status(404).send({ error: 'Rig not found' });
    }

    if (!rig.ocProfile) {
      return reply.status(400).send({ error: 'No OC profile assigned' });
    }

    const { ocProfile } = rig;

    // Build OC config for agent
    const ocConfig = {
      gpuIndex: -1, // Apply to all GPUs
      powerLimit: ocProfile.powerLimit,
      coreOffset: ocProfile.coreOffset,
      memOffset: ocProfile.memOffset,
      coreLock: ocProfile.coreLock,
      memLock: ocProfile.memLock,
      fanSpeed: ocProfile.fanSpeed,
    };

    const commandId = sendCommandToRig(id, {
      type: 'apply_oc',
      payload: ocConfig,
    });

    auditLog({
      userId: request.user?.userId,
      action: 'command_apply_oc',
      resource: 'rig',
      resourceId: id,
      details: { commandId, profile: ocProfile.name },
      ip: request.ip,
      success: true,
    });

    return reply.send({
      success: true,
      commandId,
      queued: !isAgentConnected(id),
    });
  });

  // Reboot rig via WebSocket agent
  app.post<{ Params: { id: string } }>('/:id/command/reboot', { preHandler: [requireRigAccess] }, async (request, reply) => {
    const { id } = request.params;

    const commandId = sendCommandToRig(id, { type: 'reboot' });

    auditLog({
      userId: request.user?.userId,
      action: 'command_reboot',
      resource: 'rig',
      resourceId: id,
      details: { commandId },
      ip: request.ip,
      success: true,
    });

    return reply.send({
      success: true,
      commandId,
      queued: !isAgentConnected(id),
    });
  });

  // Shutdown rig via WebSocket agent
  app.post<{ Params: { id: string } }>('/:id/command/shutdown', { preHandler: [requireRigAccess] }, async (request, reply) => {
    const { id } = request.params;

    const commandId = sendCommandToRig(id, { type: 'shutdown' });

    auditLog({
      userId: request.user?.userId,
      action: 'command_shutdown',
      resource: 'rig',
      resourceId: id,
      details: { commandId },
      ip: request.ip,
      success: true,
    });

    return reply.send({
      success: true,
      commandId,
      queued: !isAgentConnected(id),
    });
  });
}
