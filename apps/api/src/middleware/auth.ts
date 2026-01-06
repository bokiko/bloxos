import { FastifyRequest, FastifyReply } from 'fastify';
import { authService, JWTPayload } from '../services/auth-service.ts';

// Extend FastifyRequest to include user
declare module 'fastify' {
  interface FastifyRequest {
    user?: JWTPayload;
  }
}

// Auth middleware - requires valid token
export async function requireAuth(request: FastifyRequest, reply: FastifyReply) {
  const token = request.cookies.token || request.headers.authorization?.replace('Bearer ', '');

  if (!token) {
    return reply.status(401).send({ error: 'Authentication required' });
  }

  const payload = authService.verifyToken(token);
  if (!payload) {
    return reply.status(401).send({ error: 'Invalid or expired token' });
  }

  // Attach user to request
  request.user = payload;
}

// Admin middleware - requires admin role
export async function requireAdmin(request: FastifyRequest, reply: FastifyReply) {
  // First check auth
  await requireAuth(request, reply);
  
  if (reply.sent) return; // Auth failed

  if (request.user?.role !== 'ADMIN') {
    return reply.status(403).send({ error: 'Admin access required' });
  }
}

// Optional auth - attaches user if token present, but doesn't require it
export async function optionalAuth(request: FastifyRequest, reply: FastifyReply) {
  const token = request.cookies.token || request.headers.authorization?.replace('Bearer ', '');

  if (token) {
    const payload = authService.verifyToken(token);
    if (payload) {
      request.user = payload;
    }
  }
}
