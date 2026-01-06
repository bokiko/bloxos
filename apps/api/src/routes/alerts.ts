import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';

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
  // Get all alerts (with filters)
  app.get('/', async (request: FastifyRequest<{
    Querystring: { 
      unreadOnly?: string; 
      rigId?: string;
      limit?: string;
    }
  }>, reply: FastifyReply) => {
    const { unreadOnly, rigId, limit } = request.query;
    
    const alerts = await prisma.alert.findMany({
      where: {
        ...(unreadOnly === 'true' && { read: false, dismissed: false }),
        ...(rigId && { rigId }),
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

  // Get unread alert count
  app.get('/count', async (request: FastifyRequest, reply: FastifyReply) => {
    const count = await prisma.alert.count({
      where: { read: false, dismissed: false },
    });

    return reply.send({ count });
  });

  // Get single alert
  app.get('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;

    const alert = await prisma.alert.findUnique({
      where: { id },
      include: {
        rig: {
          select: { id: true, name: true, ipAddress: true },
        },
      },
    });

    if (!alert) {
      return reply.status(404).send({ error: 'Alert not found' });
    }

    return reply.send(alert);
  });

  // Mark alerts as read
  app.post('/read', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = MarkAlertsReadSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const { alertIds, all } = result.data;

    if (all) {
      await prisma.alert.updateMany({
        where: { read: false },
        data: { read: true, readAt: new Date() },
      });
    } else if (alertIds && alertIds.length > 0) {
      await prisma.alert.updateMany({
        where: { id: { in: alertIds } },
        data: { read: true, readAt: new Date() },
      });
    }

    return reply.send({ success: true });
  });

  // Dismiss alert
  app.post('/:id/dismiss', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;

    try {
      const alert = await prisma.alert.update({
        where: { id },
        data: { dismissed: true, read: true, readAt: new Date() },
      });

      return reply.send(alert);
    } catch (error) {
      return reply.status(404).send({ error: 'Alert not found' });
    }
  });

  // Dismiss all alerts
  app.post('/dismiss-all', async (request: FastifyRequest, reply: FastifyReply) => {
    await prisma.alert.updateMany({
      where: { dismissed: false },
      data: { dismissed: true, read: true, readAt: new Date() },
    });

    return reply.send({ success: true });
  });

  // Delete old alerts (cleanup)
  app.delete('/cleanup', async (request: FastifyRequest<{
    Querystring: { days?: string }
  }>, reply: FastifyReply) => {
    const days = parseInt(request.query.days || '30');
    const cutoff = new Date();
    cutoff.setDate(cutoff.getDate() - days);

    const result = await prisma.alert.deleteMany({
      where: {
        triggeredAt: { lt: cutoff },
        dismissed: true,
      },
    });

    return reply.send({ deleted: result.count });
  });

  // ===========================================
  // Alert Config Routes (per rig)
  // ===========================================

  // Get alert config for a rig
  app.get('/config/:rigId', async (request: FastifyRequest<{ Params: { rigId: string } }>, reply: FastifyReply) => {
    const { rigId } = request.params;

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
    const result = UpdateAlertConfigSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Check if rig exists
    const rig = await prisma.rig.findUnique({ where: { id: rigId } });
    if (!rig) {
      return reply.status(404).send({ error: 'Rig not found' });
    }

    const config = await prisma.alertConfig.upsert({
      where: { rigId },
      update: result.data,
      create: {
        rigId,
        ...result.data,
      },
    });

    return reply.send(config);
  });
}
