import Fastify from 'fastify';
import cors from '@fastify/cors';
import cookie from '@fastify/cookie';
import websocket from '@fastify/websocket';
import { prisma } from '@bloxos/database';

import { rigRoutes } from './routes/rigs.ts';
import { healthRoutes } from './routes/health.ts';
import { sshRoutes } from './routes/ssh.ts';
import { walletRoutes } from './routes/wallets.ts';
import { poolRoutes } from './routes/pools.ts';
import { minerRoutes } from './routes/miners.ts';
import { flightSheetRoutes } from './routes/flight-sheets.ts';
import { alertRoutes } from './routes/alerts.ts';
import { agentRoutes } from './routes/agent.ts';
import { ocProfileRoutes } from './routes/oc-profiles.ts';
import { rigGroupRoutes } from './routes/rig-groups.ts';
import { bulkActionsRoutes } from './routes/bulk-actions.ts';
import { authRoutes } from './routes/auth.ts';
import { userRoutes } from './routes/users.ts';
import { websocketRoutes } from './routes/websocket.ts';
import { terminalRoutes } from './routes/terminal.ts';
import { gpuPoller } from './services/gpu-poller.ts';
import { requireAuth } from './middleware/auth.ts';

// Routes that don't require authentication
const publicPaths = [
  '/api/health',
  '/api/auth/login',
  '/api/auth/register',
  '/api/auth/setup-required',
  '/api/auth/logout',
  '/api/agent/register',  // Agent registration uses API key
  '/api/agent/heartbeat', // Agent heartbeat uses API key
  '/api/ws',              // WebSocket handles its own auth
  '/api/terminal/ws',     // Terminal WebSocket handles its own auth
];

const PORT = parseInt(process.env.API_PORT || '3001', 10);
const HOST = process.env.API_HOST || '0.0.0.0';

async function main() {
  const app = Fastify({
    logger: {
      level: process.env.NODE_ENV === 'production' ? 'info' : 'debug',
      transport: {
        target: 'pino-pretty',
        options: {
          colorize: true,
        },
      },
    },
  });

  // Register plugins
  await app.register(cors, {
    origin: true,
    credentials: true,
  });

  await app.register(cookie, {
    secret: process.env.COOKIE_SECRET || 'bloxos-cookie-secret-change-in-production',
  });

  await app.register(websocket);

  // Global auth hook - protect all routes except public ones
  app.addHook('onRequest', async (request, reply) => {
    const path = request.url.split('?')[0]; // Remove query params
    
    // Skip auth for public paths
    if (publicPaths.some(p => path.startsWith(p))) {
      return;
    }

    // Skip auth for agent routes with valid API key
    if (path.startsWith('/api/agent/')) {
      const apiKey = request.headers['x-api-key'];
      if (apiKey) {
        return; // Agent routes handle their own auth via API key
      }
    }

    // Require auth for all other routes
    await requireAuth(request, reply);
  });

  // Register routes
  await app.register(healthRoutes, { prefix: '/api' });
  await app.register(authRoutes, { prefix: '/api/auth' });
  await app.register(userRoutes, { prefix: '/api/users' });
  await app.register(rigRoutes, { prefix: '/api/rigs' });
  await app.register(sshRoutes, { prefix: '/api/ssh' });
  await app.register(walletRoutes, { prefix: '/api/wallets' });
  await app.register(poolRoutes, { prefix: '/api/pools' });
  await app.register(minerRoutes, { prefix: '/api/miners' });
  await app.register(flightSheetRoutes, { prefix: '/api/flight-sheets' });
  await app.register(alertRoutes, { prefix: '/api/alerts' });
  await app.register(agentRoutes, { prefix: '/api/agent' });
  await app.register(ocProfileRoutes, { prefix: '/api/oc-profiles' });
  await app.register(rigGroupRoutes, { prefix: '/api/rig-groups' });
  await app.register(bulkActionsRoutes, { prefix: '/api/bulk' });
  await app.register(websocketRoutes, { prefix: '/api' });
  await app.register(terminalRoutes, { prefix: '/api/terminal' });

  // Graceful shutdown
  const signals = ['SIGINT', 'SIGTERM'];
  signals.forEach((signal) => {
    process.on(signal, async () => {
      app.log.info(`Received ${signal}, shutting down...`);
      gpuPoller.stop();
      await app.close();
      await prisma.$disconnect();
      process.exit(0);
    });
  });

  // Start server
  try {
    await app.listen({ port: PORT, host: HOST });
    app.log.info(`BloxOs API running on http://${HOST}:${PORT}`);

    // Start GPU polling service
    gpuPoller.start();
    app.log.info('GPU polling service started');
  } catch (err) {
    app.log.error(err);
    process.exit(1);
  }
}

main();
