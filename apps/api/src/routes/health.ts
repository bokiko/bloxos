import { FastifyInstance } from 'fastify';
import { prisma } from '@bloxos/database';

export async function healthRoutes(app: FastifyInstance) {
  // Health check endpoint
  app.get('/health', async (_request, reply) => {
    try {
      // Test database connection
      await prisma.$queryRaw`SELECT 1`;
      
      return reply.send({
        status: 'ok',
        timestamp: new Date().toISOString(),
        services: {
          api: 'healthy',
          database: 'healthy',
        },
      });
    } catch (error) {
      return reply.status(503).send({
        status: 'error',
        timestamp: new Date().toISOString(),
        services: {
          api: 'healthy',
          database: 'unhealthy',
        },
      });
    }
  });

  // Version info
  app.get('/version', async (_request, reply) => {
    return reply.send({
      name: 'BloxOs API',
      version: '0.1.0',
    });
  });
}
