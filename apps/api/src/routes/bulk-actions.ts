import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';
import { MinerControl } from '../services/miner-control.ts';
import { ocService } from '../services/oc-service.ts';

const minerControl = new MinerControl();

// Validation schemas
const BulkRigIdsSchema = z.object({
  rigIds: z.array(z.string()).min(1),
});

const BulkFlightSheetSchema = z.object({
  rigIds: z.array(z.string()).min(1),
  flightSheetId: z.string().nullable(),
});

const BulkOCProfileSchema = z.object({
  rigIds: z.array(z.string()).min(1),
  ocProfileId: z.string().nullable(),
});

const BulkGroupSchema = z.object({
  rigIds: z.array(z.string()).min(1),
  groupIds: z.array(z.string()),  // Array for many-to-many
});

export async function bulkActionsRoutes(app: FastifyInstance) {
  // Bulk start miners
  app.post('/start-miners', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = BulkRigIdsSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const results: { rigId: string; success: boolean; message: string }[] = [];

    for (const rigId of result.data.rigIds) {
      try {
        const response = await minerControl.startMiner(rigId);
        results.push({ rigId, success: response.success, message: response.message });
      } catch (error) {
        const message = error instanceof Error ? error.message : 'Unknown error';
        results.push({ rigId, success: false, message });
      }
    }

    const successCount = results.filter(r => r.success).length;
    return reply.send({
      success: successCount > 0,
      message: `Started miners on ${successCount}/${result.data.rigIds.length} rigs`,
      results,
    });
  });

  // Bulk stop miners
  app.post('/stop-miners', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = BulkRigIdsSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const results: { rigId: string; success: boolean; message: string }[] = [];

    for (const rigId of result.data.rigIds) {
      try {
        const response = await minerControl.stopMiner(rigId);
        results.push({ rigId, success: response.success, message: response.message });
      } catch (error) {
        const message = error instanceof Error ? error.message : 'Unknown error';
        results.push({ rigId, success: false, message });
      }
    }

    const successCount = results.filter(r => r.success).length;
    return reply.send({
      success: successCount > 0,
      message: `Stopped miners on ${successCount}/${result.data.rigIds.length} rigs`,
      results,
    });
  });

  // Bulk apply OC profiles
  app.post('/apply-oc', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = BulkRigIdsSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const results: { rigId: string; success: boolean; message: string }[] = [];

    for (const rigId of result.data.rigIds) {
      try {
        const response = await ocService.applyOCProfile(rigId);
        results.push({ rigId, success: response.success, message: response.message });
      } catch (error) {
        const message = error instanceof Error ? error.message : 'Unknown error';
        results.push({ rigId, success: false, message });
      }
    }

    const successCount = results.filter(r => r.success).length;
    return reply.send({
      success: successCount > 0,
      message: `Applied OC to ${successCount}/${result.data.rigIds.length} rigs`,
      results,
    });
  });

  // Bulk reset OC
  app.post('/reset-oc', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = BulkRigIdsSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const results: { rigId: string; success: boolean; message: string }[] = [];

    for (const rigId of result.data.rigIds) {
      try {
        const response = await ocService.resetOC(rigId);
        results.push({ rigId, success: response.success, message: response.message });
      } catch (error) {
        const message = error instanceof Error ? error.message : 'Unknown error';
        results.push({ rigId, success: false, message });
      }
    }

    const successCount = results.filter(r => r.success).length;
    return reply.send({
      success: successCount > 0,
      message: `Reset OC on ${successCount}/${result.data.rigIds.length} rigs`,
      results,
    });
  });

  // Bulk assign flight sheet
  app.post('/assign-flight-sheet', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = BulkFlightSheetSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Validate flight sheet exists if provided
    if (result.data.flightSheetId) {
      const flightSheet = await prisma.flightSheet.findUnique({
        where: { id: result.data.flightSheetId },
      });
      if (!flightSheet) {
        return reply.status(404).send({ error: 'Flight sheet not found' });
      }
    }

    const updated = await prisma.rig.updateMany({
      where: { id: { in: result.data.rigIds } },
      data: { flightSheetId: result.data.flightSheetId },
    });

    return reply.send({
      success: true,
      message: `Assigned flight sheet to ${updated.count} rigs`,
      count: updated.count,
    });
  });

  // Bulk assign OC profile
  app.post('/assign-oc-profile', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = BulkOCProfileSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Validate OC profile exists if provided
    if (result.data.ocProfileId) {
      const ocProfile = await prisma.oCProfile.findUnique({
        where: { id: result.data.ocProfileId },
      });
      if (!ocProfile) {
        return reply.status(404).send({ error: 'OC profile not found' });
      }
    }

    const updated = await prisma.rig.updateMany({
      where: { id: { in: result.data.rigIds } },
      data: { ocProfileId: result.data.ocProfileId },
    });

    return reply.send({
      success: true,
      message: `Assigned OC profile to ${updated.count} rigs`,
      count: updated.count,
    });
  });

  // Bulk assign to groups (many-to-many: set groups for rigs)
  app.post('/assign-groups', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = BulkGroupSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Update each rig to set its groups
    let count = 0;
    for (const rigId of result.data.rigIds) {
      try {
        await prisma.rig.update({
          where: { id: rigId },
          data: {
            groups: {
              set: result.data.groupIds.map(id => ({ id })),
            },
          },
        });
        count++;
      } catch (error) {
        console.error(`Failed to update rig ${rigId}:`, error);
      }
    }

    return reply.send({
      success: true,
      message: `Updated groups for ${count} rigs`,
      count,
    });
  });

  // Bulk add to group (many-to-many: add to existing groups)
  app.post('/add-to-group', async (request: FastifyRequest, reply: FastifyReply) => {
    const schema = z.object({
      rigIds: z.array(z.string()).min(1),
      groupId: z.string(),
    });
    const result = schema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Connect rigs to group
    await prisma.rigGroup.update({
      where: { id: result.data.groupId },
      data: {
        rigs: {
          connect: result.data.rigIds.map(id => ({ id })),
        },
      },
    });

    return reply.send({
      success: true,
      message: `Added ${result.data.rigIds.length} rigs to group`,
    });
  });

  // Bulk remove from group
  app.post('/remove-from-group', async (request: FastifyRequest, reply: FastifyReply) => {
    const schema = z.object({
      rigIds: z.array(z.string()).min(1),
      groupId: z.string(),
    });
    const result = schema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Disconnect rigs from group
    await prisma.rigGroup.update({
      where: { id: result.data.groupId },
      data: {
        rigs: {
          disconnect: result.data.rigIds.map(id => ({ id })),
        },
      },
    });

    return reply.send({
      success: true,
      message: `Removed ${result.data.rigIds.length} rigs from group`,
    });
  });

  // Bulk reboot rigs
  app.post('/reboot', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = BulkRigIdsSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const results: { rigId: string; success: boolean; message: string }[] = [];

    for (const rigId of result.data.rigIds) {
      try {
        // Get SSH manager and execute reboot
        const { SSHManager } = await import('../services/ssh-manager.ts');
        const sshManager = new SSHManager();
        await sshManager.executeSudoCommandOnRig(rigId, 'reboot');
        
        // Update rig status
        await prisma.rig.update({
          where: { id: rigId },
          data: { status: 'REBOOTING' },
        });

        results.push({ rigId, success: true, message: 'Reboot command sent' });
      } catch (error) {
        const message = error instanceof Error ? error.message : 'Unknown error';
        results.push({ rigId, success: false, message });
      }
    }

    const successCount = results.filter(r => r.success).length;
    return reply.send({
      success: successCount > 0,
      message: `Sent reboot command to ${successCount}/${result.data.rigIds.length} rigs`,
      results,
    });
  });
}
