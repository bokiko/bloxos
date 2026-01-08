import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';
import { MinerControl } from '../services/miner-control.ts';
import { ocService } from '../services/oc-service.ts';
import { filterOwnedRigIds, getUserFarmIds } from '../middleware/authorization.ts';
import { auditLog } from '../utils/security.ts';

const minerControl = new MinerControl();

// Constants for bulk operations
const MAX_BULK_RIGS = 100; // Maximum rigs per bulk operation
// const BULK_OPERATION_TIMEOUT = 120000; // 2 minutes timeout for bulk operations (reserved for future use)

// Validation schemas with limits
const BulkRigIdsSchema = z.object({
  rigIds: z.array(z.string().max(50)).min(1).max(MAX_BULK_RIGS),
});

const BulkFlightSheetSchema = z.object({
  rigIds: z.array(z.string().max(50)).min(1).max(MAX_BULK_RIGS),
  flightSheetId: z.string().max(50).nullable(),
});

const BulkOCProfileSchema = z.object({
  rigIds: z.array(z.string().max(50)).min(1).max(MAX_BULK_RIGS),
  ocProfileId: z.string().max(50).nullable(),
});

const BulkGroupSchema = z.object({
  rigIds: z.array(z.string().max(50)).min(1).max(MAX_BULK_RIGS),
  groupIds: z.array(z.string().max(50)).max(20),  // Max 20 groups
});

// Helper for timeout wrapper (reserved for future use with long-running bulk operations)
// async function withTimeout<T>(promise: Promise<T>, ms: number, operation: string): Promise<T> {
//   const timeout = new Promise<never>((_, reject) => {
//     setTimeout(() => reject(new Error(`${operation} timed out after ${ms}ms`)), ms);
//   });
//   return Promise.race([promise, timeout]);
// }

export async function bulkActionsRoutes(app: FastifyInstance) {
  // Bulk start miners
  app.post('/start-miners', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const result = BulkRigIdsSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Filter to only rigs the user owns
    const ownedRigIds = await filterOwnedRigIds(result.data.rigIds, user.userId, user.role);

    if (ownedRigIds.length === 0) {
      return reply.status(403).send({ error: 'No access to any of the specified rigs' });
    }

    const results: { rigId: string; success: boolean; message: string }[] = [];

    for (const rigId of ownedRigIds) {
      try {
        const response = await minerControl.startMiner(rigId);
        results.push({ rigId, success: response.success, message: response.message });
      } catch (error) {
        const message = error instanceof Error ? error.message : 'Unknown error';
        results.push({ rigId, success: false, message });
      }
    }

    const successCount = results.filter(r => r.success).length;

    auditLog({
      userId: user.userId,
      action: 'bulk_start_miners',
      resource: 'rig',
      ip: request.ip,
      success: successCount > 0,
      details: { rigIds: ownedRigIds, successCount },
    });

    return reply.send({
      success: successCount > 0,
      message: `Started miners on ${successCount}/${ownedRigIds.length} rigs`,
      results,
    });
  });

  // Bulk stop miners
  app.post('/stop-miners', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const result = BulkRigIdsSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Filter to only rigs the user owns
    const ownedRigIds = await filterOwnedRigIds(result.data.rigIds, user.userId, user.role);

    if (ownedRigIds.length === 0) {
      return reply.status(403).send({ error: 'No access to any of the specified rigs' });
    }

    const results: { rigId: string; success: boolean; message: string }[] = [];

    for (const rigId of ownedRigIds) {
      try {
        const response = await minerControl.stopMiner(rigId);
        results.push({ rigId, success: response.success, message: response.message });
      } catch (error) {
        const message = error instanceof Error ? error.message : 'Unknown error';
        results.push({ rigId, success: false, message });
      }
    }

    const successCount = results.filter(r => r.success).length;

    auditLog({
      userId: user.userId,
      action: 'bulk_stop_miners',
      resource: 'rig',
      ip: request.ip,
      success: successCount > 0,
      details: { rigIds: ownedRigIds, successCount },
    });

    return reply.send({
      success: successCount > 0,
      message: `Stopped miners on ${successCount}/${ownedRigIds.length} rigs`,
      results,
    });
  });

  // Bulk apply OC profiles
  app.post('/apply-oc', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const result = BulkRigIdsSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Filter to only rigs the user owns
    const ownedRigIds = await filterOwnedRigIds(result.data.rigIds, user.userId, user.role);

    if (ownedRigIds.length === 0) {
      return reply.status(403).send({ error: 'No access to any of the specified rigs' });
    }

    const results: { rigId: string; success: boolean; message: string }[] = [];

    for (const rigId of ownedRigIds) {
      try {
        const response = await ocService.applyOCProfile(rigId);
        results.push({ rigId, success: response.success, message: response.message });
      } catch (error) {
        const message = error instanceof Error ? error.message : 'Unknown error';
        results.push({ rigId, success: false, message });
      }
    }

    const successCount = results.filter(r => r.success).length;

    auditLog({
      userId: user.userId,
      action: 'bulk_apply_oc',
      resource: 'rig',
      ip: request.ip,
      success: successCount > 0,
      details: { rigIds: ownedRigIds, successCount },
    });

    return reply.send({
      success: successCount > 0,
      message: `Applied OC to ${successCount}/${ownedRigIds.length} rigs`,
      results,
    });
  });

  // Bulk reset OC
  app.post('/reset-oc', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const result = BulkRigIdsSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Filter to only rigs the user owns
    const ownedRigIds = await filterOwnedRigIds(result.data.rigIds, user.userId, user.role);

    if (ownedRigIds.length === 0) {
      return reply.status(403).send({ error: 'No access to any of the specified rigs' });
    }

    const results: { rigId: string; success: boolean; message: string }[] = [];

    for (const rigId of ownedRigIds) {
      try {
        const response = await ocService.resetOC(rigId);
        results.push({ rigId, success: response.success, message: response.message });
      } catch (error) {
        const message = error instanceof Error ? error.message : 'Unknown error';
        results.push({ rigId, success: false, message });
      }
    }

    const successCount = results.filter(r => r.success).length;

    auditLog({
      userId: user.userId,
      action: 'bulk_reset_oc',
      resource: 'rig',
      ip: request.ip,
      success: successCount > 0,
      details: { rigIds: ownedRigIds, successCount },
    });

    return reply.send({
      success: successCount > 0,
      message: `Reset OC on ${successCount}/${ownedRigIds.length} rigs`,
      results,
    });
  });

  // Bulk assign flight sheet
  app.post('/assign-flight-sheet', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const result = BulkFlightSheetSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Filter to only rigs the user owns
    const ownedRigIds = await filterOwnedRigIds(result.data.rigIds, user.userId, user.role);

    if (ownedRigIds.length === 0) {
      return reply.status(403).send({ error: 'No access to any of the specified rigs' });
    }

    // Validate flight sheet exists and user has access if provided
    if (result.data.flightSheetId) {
      const farmIds = await getUserFarmIds(user.userId, user.role);
      const flightSheet = await prisma.flightSheet.findUnique({
        where: { id: result.data.flightSheetId },
      });
      if (!flightSheet) {
        return reply.status(404).send({ error: 'Flight sheet not found' });
      }
      if (!farmIds.includes(flightSheet.farmId)) {
        return reply.status(403).send({ error: 'Access denied to this flight sheet' });
      }
    }

    const updated = await prisma.rig.updateMany({
      where: { id: { in: ownedRigIds } },
      data: { flightSheetId: result.data.flightSheetId },
    });

    auditLog({
      userId: user.userId,
      action: 'bulk_assign_flight_sheet',
      resource: 'rig',
      ip: request.ip,
      success: true,
      details: { rigIds: ownedRigIds, flightSheetId: result.data.flightSheetId },
    });

    return reply.send({
      success: true,
      message: `Assigned flight sheet to ${updated.count} rigs`,
      count: updated.count,
    });
  });

  // Bulk assign OC profile
  app.post('/assign-oc-profile', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const result = BulkOCProfileSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Filter to only rigs the user owns
    const ownedRigIds = await filterOwnedRigIds(result.data.rigIds, user.userId, user.role);

    if (ownedRigIds.length === 0) {
      return reply.status(403).send({ error: 'No access to any of the specified rigs' });
    }

    // Validate OC profile exists and user has access if provided
    if (result.data.ocProfileId) {
      const farmIds = await getUserFarmIds(user.userId, user.role);
      const ocProfile = await prisma.oCProfile.findUnique({
        where: { id: result.data.ocProfileId },
      });
      if (!ocProfile) {
        return reply.status(404).send({ error: 'OC profile not found' });
      }
      if (!farmIds.includes(ocProfile.farmId)) {
        return reply.status(403).send({ error: 'Access denied to this OC profile' });
      }
    }

    const updated = await prisma.rig.updateMany({
      where: { id: { in: ownedRigIds } },
      data: { ocProfileId: result.data.ocProfileId },
    });

    auditLog({
      userId: user.userId,
      action: 'bulk_assign_oc_profile',
      resource: 'rig',
      ip: request.ip,
      success: true,
      details: { rigIds: ownedRigIds, ocProfileId: result.data.ocProfileId },
    });

    return reply.send({
      success: true,
      message: `Assigned OC profile to ${updated.count} rigs`,
      count: updated.count,
    });
  });

  // Bulk assign to groups (many-to-many: set groups for rigs)
  app.post('/assign-groups', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const result = BulkGroupSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Filter to only rigs the user owns
    const ownedRigIds = await filterOwnedRigIds(result.data.rigIds, user.userId, user.role);

    if (ownedRigIds.length === 0) {
      return reply.status(403).send({ error: 'No access to any of the specified rigs' });
    }

    // Validate groups exist and user has access
    const farmIds = await getUserFarmIds(user.userId, user.role);
    const groups = await prisma.rigGroup.findMany({
      where: { id: { in: result.data.groupIds } },
    });

    const ownedGroupIds = groups
      .filter(g => farmIds.includes(g.farmId))
      .map(g => g.id);

    // Update each rig to set its groups
    let count = 0;
    for (const rigId of ownedRigIds) {
      try {
        await prisma.rig.update({
          where: { id: rigId },
          data: {
            groups: {
              set: ownedGroupIds.map(id => ({ id })),
            },
          },
        });
        count++;
      } catch (error) {
        console.error(`Failed to update rig ${rigId}:`, error);
      }
    }

    auditLog({
      userId: user.userId,
      action: 'bulk_assign_groups',
      resource: 'rig',
      ip: request.ip,
      success: true,
      details: { rigIds: ownedRigIds, groupIds: ownedGroupIds },
    });

    return reply.send({
      success: true,
      message: `Updated groups for ${count} rigs`,
      count,
    });
  });

  // Bulk add to group (many-to-many: add to existing groups)
  app.post('/add-to-group', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const schema = z.object({
      rigIds: z.array(z.string()).min(1),
      groupId: z.string(),
    });
    const result = schema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Filter to only rigs the user owns
    const ownedRigIds = await filterOwnedRigIds(result.data.rigIds, user.userId, user.role);

    if (ownedRigIds.length === 0) {
      return reply.status(403).send({ error: 'No access to any of the specified rigs' });
    }

    // Validate group exists and user has access
    const farmIds = await getUserFarmIds(user.userId, user.role);
    const group = await prisma.rigGroup.findUnique({
      where: { id: result.data.groupId },
    });

    if (!group) {
      return reply.status(404).send({ error: 'Rig group not found' });
    }

    if (!farmIds.includes(group.farmId)) {
      return reply.status(403).send({ error: 'Access denied to this group' });
    }

    // Connect rigs to group
    await prisma.rigGroup.update({
      where: { id: result.data.groupId },
      data: {
        rigs: {
          connect: ownedRigIds.map(id => ({ id })),
        },
      },
    });

    auditLog({
      userId: user.userId,
      action: 'bulk_add_to_group',
      resource: 'rigGroup',
      resourceId: result.data.groupId,
      ip: request.ip,
      success: true,
      details: { rigIds: ownedRigIds },
    });

    return reply.send({
      success: true,
      message: `Added ${ownedRigIds.length} rigs to group`,
    });
  });

  // Bulk remove from group
  app.post('/remove-from-group', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const schema = z.object({
      rigIds: z.array(z.string()).min(1),
      groupId: z.string(),
    });
    const result = schema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Filter to only rigs the user owns
    const ownedRigIds = await filterOwnedRigIds(result.data.rigIds, user.userId, user.role);

    if (ownedRigIds.length === 0) {
      return reply.status(403).send({ error: 'No access to any of the specified rigs' });
    }

    // Validate group exists and user has access
    const farmIds = await getUserFarmIds(user.userId, user.role);
    const group = await prisma.rigGroup.findUnique({
      where: { id: result.data.groupId },
    });

    if (!group) {
      return reply.status(404).send({ error: 'Rig group not found' });
    }

    if (!farmIds.includes(group.farmId)) {
      return reply.status(403).send({ error: 'Access denied to this group' });
    }

    // Disconnect rigs from group
    await prisma.rigGroup.update({
      where: { id: result.data.groupId },
      data: {
        rigs: {
          disconnect: ownedRigIds.map(id => ({ id })),
        },
      },
    });

    auditLog({
      userId: user.userId,
      action: 'bulk_remove_from_group',
      resource: 'rigGroup',
      resourceId: result.data.groupId,
      ip: request.ip,
      success: true,
      details: { rigIds: ownedRigIds },
    });

    return reply.send({
      success: true,
      message: `Removed ${ownedRigIds.length} rigs from group`,
    });
  });

  // Bulk reboot rigs
  app.post('/reboot', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const result = BulkRigIdsSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Filter to only rigs the user owns
    const ownedRigIds = await filterOwnedRigIds(result.data.rigIds, user.userId, user.role);

    if (ownedRigIds.length === 0) {
      return reply.status(403).send({ error: 'No access to any of the specified rigs' });
    }

    const results: { rigId: string; success: boolean; message: string }[] = [];

    for (const rigId of ownedRigIds) {
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

    auditLog({
      userId: user.userId,
      action: 'bulk_reboot',
      resource: 'rig',
      ip: request.ip,
      success: successCount > 0,
      details: { rigIds: ownedRigIds, successCount },
    });

    return reply.send({
      success: successCount > 0,
      message: `Sent reboot command to ${successCount}/${ownedRigIds.length} rigs`,
      results,
    });
  });
}
