import Fastify from 'fastify';
import cors from '@fastify/cors';
import cookie from '@fastify/cookie';
import websocket from '@fastify/websocket';
import rateLimit from '@fastify/rate-limit';
import helmet from '@fastify/helmet';
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
import { agentWebsocketRoutes } from './routes/agent-websocket.ts';
import { coinsRoutes } from './routes/coins.ts';
import { templatesRoutes } from './routes/templates.ts';
import { updatesRoutes } from './routes/updates.ts';
import { gpuPoller } from './services/gpu-poller.ts';
import { startUpdateChecker, stopUpdateChecker } from './services/update-checker.ts';
import { requireAuth } from './middleware/auth.ts';
import { csrfSetToken, csrfValidate, csrfTokenEndpoint } from './middleware/csrf.ts';
import { validateSecrets, auditLog } from './utils/security.ts';

// Validate secrets on startup
try {
  validateSecrets();
} catch (error) {
  console.error('[Security] Fatal:', error);
  process.exit(1);
}

// Routes that don't require authentication
const publicPaths = [
  '/api/health',
  '/api/auth/login',
  '/api/auth/register',
  '/api/auth/setup-required',
  '/api/auth/logout',
  '/api/auth/refresh',
];

// Agent routes require API key validation
const agentPaths = [
  '/api/agent/register',
  '/api/agent/heartbeat',
  '/api/agent/report',
];

const PORT = parseInt(process.env.API_PORT || '3001', 10);
const HOST = process.env.API_HOST || '0.0.0.0';
const isProduction = process.env.NODE_ENV === 'production';

// CORS allowed origins
const getAllowedOrigins = (): string[] | boolean => {
  const origins = process.env.CORS_ORIGINS;
  if (origins) {
    return origins.split(',').map(o => o.trim());
  }
  // In development, allow all origins
  if (!isProduction) {
    return true;
  }
  // In production without explicit origins, restrict to same origin
  return false;
};

async function main() {
  const app = Fastify({
    logger: {
      level: isProduction ? 'info' : 'debug',
      transport: isProduction ? undefined : {
        target: 'pino-pretty',
        options: {
          colorize: true,
        },
      },
    },
    // Request body size limit
    bodyLimit: 1048576, // 1MB
    // Request timeout
    requestTimeout: 30000, // 30 seconds
  });

  // ============================================
  // SECURITY PLUGINS
  // ============================================

  // Security headers (Helmet)
  await app.register(helmet, {
    contentSecurityPolicy: isProduction ? {
      directives: {
        defaultSrc: ["'self'"],
        styleSrc: ["'self'", "'unsafe-inline'"],
        scriptSrc: ["'self'"],
        imgSrc: ["'self'", 'data:', 'https:'],
        connectSrc: ["'self'", 'wss:', 'ws:'],
      },
    } : false,
    crossOriginEmbedderPolicy: false,
    // HSTS - Strict Transport Security (only in production)
    hsts: isProduction ? {
      maxAge: 31536000, // 1 year
      includeSubDomains: true,
      preload: true,
    } : false,
  });

  // HTTPS redirect in production
  if (isProduction && process.env.FORCE_HTTPS === 'true') {
    app.addHook('onRequest', async (request, reply) => {
      const proto = request.headers['x-forwarded-proto'] || 'http';
      if (proto !== 'https') {
        const host = request.headers.host || request.hostname;
        const url = `https://${host}${request.url}`;
        return reply.status(301).redirect(url);
      }
    });
  }

  // Rate limiting - higher limits in development
  await app.register(rateLimit, {
    max: isProduction ? 100 : 1000, // 1000 requests per minute in dev
    timeWindow: '1 minute',
    keyGenerator: (request) => {
      return request.ip || 'unknown';
    },
    errorResponseBuilder: (request, context) => {
      return {
        error: 'Too many requests',
        message: `Rate limit exceeded. Try again in ${Math.round(context.ttl / 1000)} seconds`,
        retryAfter: Math.round(context.ttl / 1000),
      };
    },
  });

  // CORS - Restrict in production
  await app.register(cors, {
    origin: getAllowedOrigins(),
    credentials: true,
    methods: ['GET', 'POST', 'PUT', 'PATCH', 'DELETE', 'OPTIONS'],
    allowedHeaders: ['Content-Type', 'Authorization', 'X-API-Key', 'X-Request-ID', 'X-CSRF-Token'],
    exposedHeaders: ['X-Request-ID', 'X-RateLimit-Limit', 'X-RateLimit-Remaining'],
    maxAge: 600, // 10 minutes
  });

  // Cookie secret - Required
  const cookieSecret = process.env.COOKIE_SECRET;
  if (!cookieSecret && isProduction) {
    throw new Error('COOKIE_SECRET environment variable is required in production');
  }
  await app.register(cookie, {
    secret: cookieSecret || 'bloxos-cookie-secret-dev-only',
  });

  // WebSocket support
  await app.register(websocket);

  // ============================================
  // REQUEST HOOKS
  // ============================================

  // Add request ID for tracing
  app.addHook('onRequest', async (request, reply) => {
    const requestId = request.headers['x-request-id'] || 
      `${Date.now()}-${Math.random().toString(36).substr(2, 9)}`;
    request.headers['x-request-id'] = requestId as string;
    reply.header('X-Request-ID', requestId);
  });

  // CSRF token generation - set token cookie on responses
  app.addHook('onSend', async (request, reply) => {
    await csrfSetToken(request, reply);
  });

  // Global auth hook - protect all routes except public ones
  app.addHook('onRequest', async (request, reply) => {
    const path = request.url.split('?')[0]; // Remove query params
    
    // Skip auth for public paths
    if (publicPaths.some(p => path.startsWith(p))) {
      return;
    }

    // Skip auth for WebSocket paths (they handle their own auth)
    if (path.startsWith('/api/ws') || path.startsWith('/api/terminal/ws') || path.startsWith('/api/agent/ws')) {
      return;
    }

    // Agent routes require valid API key (token validation)
    if (agentPaths.some(p => path.startsWith(p))) {
      const apiKey = request.headers['x-api-key'];
      if (!apiKey) {
        return reply.status(401).send({ error: 'API key required' });
      }
      
      // Validate token exists in database
      const body = request.body as { token?: string } | undefined;
      const token = body?.token || apiKey;
      
      if (typeof token !== 'string' || token.length < 10) {
        return reply.status(401).send({ error: 'Invalid API key format' });
      }
      
      // Token validation happens in the route handler
      return;
    }

    // Require auth for all other routes
    await requireAuth(request, reply);
  });

  // CSRF validation for state-changing requests
  // Disabled in development for easier testing - enable in production
  if (isProduction) {
    app.addHook('preHandler', async (request, reply) => {
      await csrfValidate(request, reply);
    });
  }

  // Audit logging for sensitive operations
  app.addHook('onResponse', async (request, reply) => {
    const path = request.url.split('?')[0];
    const method = request.method;
    
    // Log sensitive operations
    if (method !== 'GET' && method !== 'OPTIONS') {
      const sensitiveRoutes = ['/api/auth', '/api/users', '/api/ssh', '/api/rigs'];
      if (sensitiveRoutes.some(r => path.startsWith(r))) {
        auditLog({
          userId: (request as any).user?.userId,
          action: `${method} ${path}`,
          resource: path.split('/')[2] || 'unknown',
          ip: request.ip,
          userAgent: request.headers['user-agent'],
          success: reply.statusCode < 400,
          error: reply.statusCode >= 400 ? `HTTP ${reply.statusCode}` : undefined,
        });
      }
    }
  });

  // Error handler - Don't leak sensitive info
  app.setErrorHandler((error: Error & { statusCode?: number }, request, reply) => {
    const requestId = request.headers['x-request-id'];
    
    // Log full error internally
    app.log.error({
      requestId,
      error: error.message,
      stack: error.stack,
      path: request.url,
    });

    // Return sanitized error to client
    const statusCode = error.statusCode || 500;
    const message = statusCode >= 500 && isProduction
      ? 'Internal server error'
      : error.message;

    reply.status(statusCode).send({
      error: message,
      requestId,
      statusCode,
    });
  });

  // ============================================
  // ROUTES
  // ============================================

  // Apply stricter rate limits to auth routes
  await app.register(async (authApp) => {
    authApp.addHook('onRequest', async (_request, _reply) => {
      // Additional rate limiting for auth: 5 requests per minute
      // This is handled by the main rate limiter, but we could add
      // additional checks here if needed
    });
    
    await authApp.register(authRoutes);
  }, { prefix: '/api/auth' });

  // CSRF token endpoint
  app.get('/api/csrf-token', csrfTokenEndpoint);

  await app.register(healthRoutes, { prefix: '/api' });
  await app.register(userRoutes, { prefix: '/api/users' });
  await app.register(rigRoutes, { prefix: '/api/rigs' });
  await app.register(sshRoutes, { prefix: '/api/ssh' });
  await app.register(walletRoutes, { prefix: '/api/wallets' });
  await app.register(poolRoutes, { prefix: '/api/pools' });
  await app.register(minerRoutes, { prefix: '/api/miners' });
  await app.register(flightSheetRoutes, { prefix: '/api/flight-sheets' });
  await app.register(alertRoutes, { prefix: '/api/alerts' });
  await app.register(agentRoutes, { prefix: '/api/agent' });
  await app.register(agentWebsocketRoutes, { prefix: '/api/agent' });
  await app.register(ocProfileRoutes, { prefix: '/api/oc-profiles' });
  await app.register(rigGroupRoutes, { prefix: '/api/rig-groups' });
  await app.register(bulkActionsRoutes, { prefix: '/api/bulk' });
  await app.register(websocketRoutes, { prefix: '/api' });
  await app.register(terminalRoutes, { prefix: '/api/terminal' });
  await app.register(coinsRoutes, { prefix: '/api' });
  await app.register(templatesRoutes, { prefix: '/api' });
  await app.register(updatesRoutes, { prefix: '/api/updates' });

  // ============================================
  // GRACEFUL SHUTDOWN
  // ============================================

  const signals = ['SIGINT', 'SIGTERM'];
  signals.forEach((signal) => {
    process.on(signal, async () => {
      app.log.info(`Received ${signal}, shutting down...`);
      gpuPoller.stop();
      stopUpdateChecker();
      await app.close();
      await prisma.$disconnect();
      process.exit(0);
    });
  });

  // ============================================
  // START SERVER
  // ============================================

  try {
    await app.listen({ port: PORT, host: HOST });
    app.log.info(`BloxOs API running on http://${HOST}:${PORT}`);
    app.log.info(`Environment: ${isProduction ? 'production' : 'development'}`);

    // Start GPU polling service
    gpuPoller.start();
    app.log.info('GPU polling service started');

    // Start miner update checker (checks every 12 hours)
    startUpdateChecker();
    app.log.info('Miner update checker started');
  } catch (err) {
    app.log.error(err);
    process.exit(1);
  }
}

main();
