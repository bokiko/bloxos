import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';
import { getUserRigFilter } from '../middleware/authorization.ts';
import { auditLog } from '../utils/security.ts';

// Validation schemas
const UpdateAlertConfigSchema = z.object({
  tempAlertEnabled: z.boolean().optional(),
  offlineAlertEnabled: z.boolean().optional(),
  hashrateAlertEnabled: z.boolean().optional(),
  gpuTempThreshold: z.number().min(50).max(100).optional(),
  cpuTempThreshold: z.number().min(50).max(100).optional(),
  offlineThreshold: z.number().min(60).max(3600).optional(),
  hashrateDropPercent: z.number().min(5).max(50).optional(),
});

const MarkAlertsReadSchema = z.object({
  alertIds: z.array(z.string()).optional(),
  all: z.boolean().optional(),
});

export async function alertRoutes(app: FastifyInstance) {
  // Get all alerts (with filters) - filtered by user's rigs
  app.get('/', async (request: FastifyRequest<{
    Querystring: { 
      unreadOnly?: string; 
      rigId?: string;
      limit?: string;
    }
  }>, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const { unreadOnly, rigId, limit } = request.query;

    // Get filter for user's rigs
    const rigFilter = getUserRigFilter(user);

    // If specific rigId is provided, verify user has access to it
    if (rigId) {
      const rig = await prisma.rig.findUnique({
        where: { id: rigId },
        include: { farm: { select: { ownerId: true } } },
      });

      if (!rig) {
        return reply.status(404).send({ error: 'Rig not found' });
      }

      if (user.role !== 'ADMIN' && rig.farm.ownerId !== user.userId) {
        return reply.status(403).send({ error: 'Access denied to this rig' });
      }
    }
    
    const alerts = await prisma.alert.findMany({
      where: {
        ...(unreadOnly === 'true' && { read: false, dismissed: false }),
        ...(rigId ? { rigId } : { rig: rigFilter }),
      },
      include: {
        rig: {
          select: { id: true, name: true },
        },
      },
      orderBy: { triggeredAt: 'desc' },
      take: limit ? parseInt(limit) : 100,
    });

    return reply.send(alerts);
  });

  // Get unread alert count - filtered by user's rigs
  app.get('/count', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const rigFilter = getUserRigFilter(user);

    const count = await prisma.alert.count({
      where: {
        read: false,
        dismissed: false,
        rig: rigFilter,
      },
    });

    return reply.send({ count });
  });

  // Get single alert
  app.get('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const user = request.user;

    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const alert = await prisma.alert.findUnique({
      where: { id },
      include: {
        rig: {
          select: { id: true, name: true, ipAddress: true, farm: { select: { ownerId: true } } },
        },
      },
    });

    if (!alert) {
      return reply.status(404).send({ error: 'Alert not found' });
    }

    // Authorization check
    if (user.role !== 'ADMIN' && alert.rig.farm.ownerId !== user.userId) {
      auditLog({
        userId: user.userId,
        action: 'unauthorized_alert_access',
        resource: 'alert',
        resourceId: id,
        ip: request.ip,
        success: false,
      });
      return reply.status(403).send({ error: 'Access denied' });
    }

    return reply.send(alert);
  });

  // Mark alerts as read - only user's alerts
  app.post('/read', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const result = MarkAlertsReadSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const { alertIds, all } = result.data;
    const rigFilter = getUserRigFilter(user);

    if (all) {
      await prisma.alert.updateMany({
        where: {
          read: false,
          rig: rigFilter,
        },
        data: { read: true, readAt: new Date() },
      });

      auditLog({
        userId: user.userId,
        action: 'mark_all_alerts_read',
        resource: 'alert',
        ip: request.ip,
        success: true,
      });
    } else if (alertIds && alertIds.length > 0) {
      // Verify user has access to these alerts
      const userRigIds = await prisma.rig.findMany({
        where: rigFilter,
        select: { id: true },
      });
      const ownedRigIds = userRigIds.map(r => r.id);

      // Only update alerts for rigs the user owns
      await prisma.alert.updateMany({
        where: {
          id: { in: alertIds },
          rigId: { in: ownedRigIds },
        },
        data: { read: true, readAt: new Date() },
      });

      auditLog({
        userId: user.userId,
        action: 'mark_alerts_read',
        resource: 'alert',
        ip: request.ip,
        success: true,
        details: { alertIds },
      });
    }

    return reply.send({ success: true });
  });

  // Dismiss alert
  app.post('/:id/dismiss', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const user = request.user;

    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    // Verify user has access to this alert
    const existing = await prisma.alert.findUnique({
      where: { id },
      include: { rig: { include: { farm: { select: { ownerId: true } } } } },
    });

    if (!existing) {
      return reply.status(404).send({ error: 'Alert not found' });
    }

    if (user.role !== 'ADMIN' && existing.rig.farm.ownerId !== user.userId) {
      auditLog({
        userId: user.userId,
        action: 'unauthorized_alert_dismiss',
        resource: 'alert',
        resourceId: id,
        ip: request.ip,
        success: false,
      });
      return reply.status(403).send({ error: 'Access denied' });
    }

    const alert = await prisma.alert.update({
      where: { id },
      data: { dismissed: true, read: true, readAt: new Date() },
    });

    auditLog({
      userId: user.userId,
      action: 'dismiss_alert',
      resource: 'alert',
      resourceId: id,
      ip: request.ip,
      success: true,
    });

    return reply.send(alert);
  });

  // Dismiss all alerts - only user's alerts
  app.post('/dismiss-all', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const rigFilter = getUserRigFilter(user);

    await prisma.alert.updateMany({
      where: {
        dismissed: false,
        rig: rigFilter,
      },
      data: { dismissed: true, read: true, readAt: new Date() },
    });

    auditLog({
      userId: user.userId,
      action: 'dismiss_all_alerts',
      resource: 'alert',
      ip: request.ip,
      success: true,
    });

    return reply.send({ success: true });
  });

  // Delete old alerts (cleanup) - only user's alerts (or admin only)
  app.delete('/cleanup', async (request: FastifyRequest<{
    Querystring: { days?: string }
  }>, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const days = parseInt(request.query.days || '30');
    const cutoff = new Date();
    cutoff.setDate(cutoff.getDate() - days);

    const rigFilter = getUserRigFilter(user);

    const result = await prisma.alert.deleteMany({
      where: {
        triggeredAt: { lt: cutoff },
        dismissed: true,
        rig: rigFilter,
      },
    });

    auditLog({
      userId: user.userId,
      action: 'cleanup_alerts',
      resource: 'alert',
      ip: request.ip,
      success: true,
      details: { days, deleted: result.count },
    });

    return reply.send({ deleted: result.count });
  });

  // ===========================================
  // Alert Config Routes (per rig)
  // ===========================================

  // Get alert config for a rig
  app.get('/config/:rigId', async (request: FastifyRequest<{ Params: { rigId: string } }>, reply: FastifyReply) => {
    const { rigId } = request.params;
    const user = request.user;

    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    // Verify user has access to this rig
    const rig = await prisma.rig.findUnique({
      where: { id: rigId },
      include: { farm: { select: { ownerId: true } } },
    });

    if (!rig) {
      return reply.status(404).send({ error: 'Rig not found' });
    }

    if (user.role !== 'ADMIN' && rig.farm.ownerId !== user.userId) {
      return reply.status(403).send({ error: 'Access denied to this rig' });
    }

    let config = await prisma.alertConfig.findUnique({
      where: { rigId },
    });

    // Return defaults if no config exists
    if (!config) {
      config = {
        id: '',
        rigId,
        tempAlertEnabled: true,
        offlineAlertEnabled: true,
        hashrateAlertEnabled: true,
        gpuTempThreshold: 80,
        cpuTempThreshold: 85,
        offlineThreshold: 300,
        hashrateDropPercent: 20,
        createdAt: new Date(),
        updatedAt: new Date(),
      };
    }

    return reply.send(config);
  });

  // Update alert config for a rig
  app.patch('/config/:rigId', async (request: FastifyRequest<{ Params: { rigId: string } }>, reply: FastifyReply) => {
    const { rigId } = request.params;
    const user = request.user;

    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const result = UpdateAlertConfigSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Check if rig exists and user has access
    const rig = await prisma.rig.findUnique({
      where: { id: rigId },
      include: { farm: { select: { ownerId: true } } },
    });

    if (!rig) {
      return reply.status(404).send({ error: 'Rig not found' });
    }

    if (user.role !== 'ADMIN' && rig.farm.ownerId !== user.userId) {
      auditLog({
        userId: user.userId,
        action: 'unauthorized_alertconfig_update',
        resource: 'alertConfig',
        resourceId: rigId,
        ip: request.ip,
        success: false,
      });
      return reply.status(403).send({ error: 'Access denied to this rig' });
    }

    const config = await prisma.alertConfig.upsert({
      where: { rigId },
      update: result.data,
      create: {
        rigId,
        ...result.data,
      },
    });

    auditLog({
      userId: user.userId,
      action: 'update_alertconfig',
      resource: 'alertConfig',
      resourceId: rigId,
      ip: request.ip,
      success: true,
    });

    return reply.send(config);
  });
}
