import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { prisma } from '@bloxos/database';

interface EventsQuery {
  limit?: string;
  offset?: string;
  rigId?: string;
  type?: string;
  severity?: string;
  since?: string;
}

export async function eventsRoutes(app: FastifyInstance) {
  // GET /api/events — paginated rig event log across the whole farm
  app.get<{ Querystring: EventsQuery }>(
    '/',
    async (request: FastifyRequest<{ Querystring: EventsQuery }>, reply: FastifyReply) => {
      const userId = request.user!.userId;

      const limit = Math.min(parseInt(request.query.limit ?? '50', 10), 200);
      const offset = parseInt(request.query.offset ?? '0', 10);

      // Collect farm IDs the user belongs to
      const farms = await prisma.farm.findMany({
        where: { userId },
        select: { id: true },
      });
      const farmIds = farms.map((f) => f.id);

      if (farmIds.length === 0) {
        return reply.send({ events: [], total: 0, limit, offset });
      }

      const where: Parameters<typeof prisma.rigEvent.findMany>[0]['where'] = {
        rig: { farmId: { in: farmIds } },
      };

      if (request.query.rigId) {
        where.rigId = request.query.rigId;
      }

      if (request.query.type) {
        // Allow comma-separated list
        const types = request.query.type.split(',').map((t) => t.trim().toUpperCase());
        (where as Record<string, unknown>).type = { in: types };
      }

      if (request.query.severity) {
        const severities = request.query.severity.split(',').map((s) => s.trim().toUpperCase());
        (where as Record<string, unknown>).severity = { in: severities };
      }

      if (request.query.since) {
        const since = new Date(request.query.since);
        if (!isNaN(since.getTime())) {
          (where as Record<string, unknown>).timestamp = { gte: since };
        }
      }

      const [events, total] = await Promise.all([
        prisma.rigEvent.findMany({
          where,
          orderBy: { timestamp: 'desc' },
          skip: offset,
          take: limit,
          select: {
            id: true,
            type: true,
            severity: true,
            message: true,
            timestamp: true,
            rigId: true,
            rig: {
              select: {
                id: true,
                name: true,
                hostname: true,
              },
            },
          },
        }),
        prisma.rigEvent.count({ where }),
      ]);

      return reply.send({ events, total, limit, offset });
    }
  );

  // GET /api/events/rigs — list of rigs that have events (for filter dropdown)
  app.get(
    '/rigs',
    async (request: FastifyRequest, reply: FastifyReply) => {
      const userId = request.user!.userId;

      const farms = await prisma.farm.findMany({
        where: { userId },
        select: { id: true },
      });
      const farmIds = farms.map((f) => f.id);

      if (farmIds.length === 0) {
        return reply.send([]);
      }

      const rigs = await prisma.rig.findMany({
        where: { farmId: { in: farmIds } },
        select: { id: true, name: true, hostname: true },
        orderBy: { name: 'asc' },
      });

      return reply.send(rigs);
    }
  );
}
