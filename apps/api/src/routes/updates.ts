import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import {
  checkAllMinerUpdates,
  getAvailableUpdates,
  getLastCheckResults,
  forceUpdateCheck,
  clearVersionCache,
} from '../services/update-checker.js';

export async function updatesRoutes(app: FastifyInstance) {
  // Get all miner update status
  app.get('/miners', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const updates = await checkAllMinerUpdates();
    return reply.send({
      checkedAt: new Date().toISOString(),
      miners: updates,
      hasUpdates: updates.some(u => u.hasUpdate),
      updateCount: updates.filter(u => u.hasUpdate).length,
    });
  });

  // Get only miners with available updates
  app.get('/miners/available', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const updates = await getAvailableUpdates();
    return reply.send({
      checkedAt: new Date().toISOString(),
      updates,
      count: updates.length,
    });
  });

  // Get cached results (fast, no API calls)
  app.get('/miners/cached', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const { time, updates } = getLastCheckResults();
    return reply.send({
      lastCheckedAt: time?.toISOString() || null,
      miners: updates,
      hasUpdates: updates.some(u => u.hasUpdate),
      updateCount: updates.filter(u => u.hasUpdate).length,
    });
  });

  // Force refresh update check (admin only)
  app.post('/miners/refresh', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    if (user.role !== 'ADMIN') {
      return reply.status(403).send({ error: 'Admin access required' });
    }

    clearVersionCache();
    const updates = await forceUpdateCheck();
    
    return reply.send({
      checkedAt: new Date().toISOString(),
      miners: updates,
      hasUpdates: updates.some(u => u.hasUpdate),
      updateCount: updates.filter(u => u.hasUpdate).length,
    });
  });
}
