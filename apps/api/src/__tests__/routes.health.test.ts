import { describe, it, expect, vi, beforeEach } from 'vitest';
import Fastify, { type FastifyInstance } from 'fastify';

// Mock @bloxos/database
vi.mock('@bloxos/database', () => ({
  prisma: {
    $queryRaw: vi.fn(),
  },
}));

import { prisma } from '@bloxos/database';
import { healthRoutes } from '../routes/health.ts';

const mockedPrisma = vi.mocked(prisma);

async function buildApp(): Promise<FastifyInstance> {
  const app = Fastify({ logger: false });
  await app.register(healthRoutes, { prefix: '/api' });
  return app;
}

describe('Health Routes', () => {
  let app: FastifyInstance;

  beforeEach(async () => {
    vi.clearAllMocks();
    app = await buildApp();
  });

  describe('GET /api/health', () => {
    it('returns 200 with status ok when database is healthy', async () => {
      mockedPrisma.$queryRaw.mockResolvedValue([{ '?column?': 1 }]);

      const res = await app.inject({ method: 'GET', url: '/api/health' });

      expect(res.statusCode).toBe(200);
      const body = res.json();
      expect(body.status).toBe('ok');
      expect(body.services.api).toBe('healthy');
      expect(body.services.database).toBe('healthy');
      expect(body).toHaveProperty('timestamp');
    });

    it('returns 503 when database is unhealthy', async () => {
      mockedPrisma.$queryRaw.mockRejectedValue(new Error('Connection refused'));

      const res = await app.inject({ method: 'GET', url: '/api/health' });

      expect(res.statusCode).toBe(503);
      const body = res.json();
      expect(body.status).toBe('error');
      expect(body.services.api).toBe('healthy');
      expect(body.services.database).toBe('unhealthy');
    });
  });

  describe('GET /api/version', () => {
    it('returns version info', async () => {
      const res = await app.inject({ method: 'GET', url: '/api/version' });

      expect(res.statusCode).toBe(200);
      const body = res.json();
      expect(body.name).toBe('BloxOs API');
      expect(body.version).toBe('0.1.0');
    });
  });
});
