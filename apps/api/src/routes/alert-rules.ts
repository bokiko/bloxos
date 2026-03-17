import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';
import { auditLog } from '../utils/security.ts';

const AlertConditionEnum = z.enum([
  'TEMP_ABOVE',
  'TEMP_BELOW',
  'HASHRATE_BELOW',
  'HASHRATE_DROP_PERCENT',
  'RIG_OFFLINE',
  'GPU_ERROR',
  'REJECTED_PERCENT',
]);

const CreateAlertRuleSchema = z.object({
  name: z.string().min(1).max(200),
  enabled: z.boolean().optional(),
  condition: AlertConditionEnum,
  threshold: z.number(),
  duration: z.number().int().positive().nullable().optional(),
  cooldown: z.number().int().min(0).optional(),
  notify: z.array(z.enum(['telegram', 'discord', 'email'])).min(1),
});

const UpdateAlertRuleSchema = CreateAlertRuleSchema.partial();

function requireAdmin(request: FastifyRequest, reply: FastifyReply): boolean {
  const user = request.user;
  if (!user) {
    reply.status(401).send({ error: 'Authentication required' });
    return false;
  }
  if (user.role !== 'ADMIN') {
    auditLog({
      userId: user.userId,
      action: 'unauthorized_alert_rule_access',
      resource: 'alertRule',
      ip: request.ip,
      success: false,
    });
    reply.status(403).send({ error: 'Admin access required' });
    return false;
  }
  return true;
}

export async function alertRulesRoutes(app: FastifyInstance) {
  // List all alert rules (admin only)
  app.get('/', async (request: FastifyRequest, reply: FastifyReply) => {
    if (!requireAdmin(request, reply)) return;

    const rules = await prisma.alertRule.findMany({
      orderBy: { createdAt: 'desc' },
    });

    return reply.send(rules);
  });

  // Create alert rule
  app.post('/', async (request: FastifyRequest, reply: FastifyReply) => {
    if (!requireAdmin(request, reply)) return;

    const result = CreateAlertRuleSchema.safeParse(request.body);
    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const rule = await prisma.alertRule.create({
      data: result.data,
    });

    auditLog({
      userId: request.user!.userId,
      action: 'create_alert_rule',
      resource: 'alertRule',
      resourceId: rule.id,
      ip: request.ip,
      success: true,
    });

    return reply.status(201).send(rule);
  });

  // Update alert rule
  app.put('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    if (!requireAdmin(request, reply)) return;

    const { id } = request.params;
    const result = UpdateAlertRuleSchema.safeParse(request.body);
    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const existing = await prisma.alertRule.findUnique({ where: { id } });
    if (!existing) {
      return reply.status(404).send({ error: 'Alert rule not found' });
    }

    const rule = await prisma.alertRule.update({
      where: { id },
      data: result.data,
    });

    auditLog({
      userId: request.user!.userId,
      action: 'update_alert_rule',
      resource: 'alertRule',
      resourceId: id,
      ip: request.ip,
      success: true,
    });

    return reply.send(rule);
  });

  // Delete alert rule
  app.delete('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    if (!requireAdmin(request, reply)) return;

    const { id } = request.params;

    const existing = await prisma.alertRule.findUnique({ where: { id } });
    if (!existing) {
      return reply.status(404).send({ error: 'Alert rule not found' });
    }

    await prisma.alertRule.delete({ where: { id } });

    auditLog({
      userId: request.user!.userId,
      action: 'delete_alert_rule',
      resource: 'alertRule',
      resourceId: id,
      ip: request.ip,
      success: true,
    });

    return reply.status(204).send();
  });

  // Toggle enabled/disabled
  app.patch('/:id/toggle', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    if (!requireAdmin(request, reply)) return;

    const { id } = request.params;

    const existing = await prisma.alertRule.findUnique({ where: { id } });
    if (!existing) {
      return reply.status(404).send({ error: 'Alert rule not found' });
    }

    const rule = await prisma.alertRule.update({
      where: { id },
      data: { enabled: !existing.enabled },
    });

    auditLog({
      userId: request.user!.userId,
      action: existing.enabled ? 'disable_alert_rule' : 'enable_alert_rule',
      resource: 'alertRule',
      resourceId: id,
      ip: request.ip,
      success: true,
    });

    return reply.send(rule);
  });
}
