import { FastifyRequest, FastifyReply } from 'fastify';
import { prisma } from '@bloxos/database';
import { auditLog } from '../utils/security.ts';

/**
 * Authorization middleware for farm-based access control
 * Users can only access rigs that belong to farms they own
 */

// Check if user has access to a specific rig
export async function requireRigAccess(
  request: FastifyRequest,
  reply: FastifyReply
) {
  const params = request.params as { id?: string; rigId?: string };
  const rigId = params.id || params.rigId;
  const user = request.user;

  if (!user) {
    return reply.status(401).send({ error: 'Authentication required' });
  }

  if (!rigId) {
    return reply.status(400).send({ error: 'Rig ID required' });
  }

  // Admins have access to all rigs
  if (user.role === 'ADMIN') {
    return;
  }

  // Check if rig belongs to a farm owned by this user
  const rig = await prisma.rig.findUnique({
    where: { id: rigId },
    include: {
      farm: {
        select: { ownerId: true },
      },
    },
  });

  if (!rig) {
    return reply.status(404).send({ error: 'Rig not found' });
  }

  if (rig.farm.ownerId !== user.userId) {
    auditLog({
      userId: user.userId,
      action: 'unauthorized_rig_access',
      resource: 'rig',
      resourceId: rigId,
      ip: request.ip,
      success: false,
      error: 'User does not own the farm containing this rig',
    });
    return reply.status(403).send({ error: 'Access denied to this rig' });
  }
}

// Check if user has access to a specific farm
export async function requireFarmAccess(
  request: FastifyRequest,
  reply: FastifyReply
) {
  const params = request.params as { farmId?: string };
  const body = request.body as { farmId?: string } | undefined;
  const farmId = params.farmId || body?.farmId;
  const user = request.user;

  if (!user) {
    return reply.status(401).send({ error: 'Authentication required' });
  }

  if (!farmId) {
    return reply.status(400).send({ error: 'Farm ID required' });
  }

  // Admins have access to all farms
  if (user.role === 'ADMIN') {
    return;
  }

  const farm = await prisma.farm.findUnique({
    where: { id: farmId },
    select: { ownerId: true },
  });

  if (!farm) {
    return reply.status(404).send({ error: 'Farm not found' });
  }

  if (farm.ownerId !== user.userId) {
    auditLog({
      userId: user.userId,
      action: 'unauthorized_farm_access',
      resource: 'farm',
      resourceId: farmId,
      ip: request.ip,
      success: false,
      error: 'User does not own this farm',
    });
    return reply.status(403).send({ error: 'Access denied to this farm' });
  }
}

// Filter rigs query to only show rigs the user has access to
export function getUserRigFilter(user: { userId: string; role: string }) {
  // Admins see all rigs
  if (user.role === 'ADMIN') {
    return {};
  }

  // Regular users only see rigs in their farms
  return {
    farm: {
      ownerId: user.userId,
    },
  };
}

// Filter farms query to only show farms the user has access to
export function getUserFarmFilter(user: { userId: string; role: string }) {
  // Admins see all farms
  if (user.role === 'ADMIN') {
    return {};
  }

  // Regular users only see their own farms
  return {
    ownerId: user.userId,
  };
}

// Validate agent token and return rig if valid
export async function validateAgentToken(token: string): Promise<{
  valid: boolean;
  rig?: { id: string; farmId: string; name: string };
  error?: string;
}> {
  if (!token || typeof token !== 'string' || token.length < 10) {
    return { valid: false, error: 'Invalid token format' };
  }

  const rig = await prisma.rig.findUnique({
    where: { token },
    select: {
      id: true,
      farmId: true,
      name: true,
    },
  });

  if (!rig) {
    return { valid: false, error: 'Invalid token' };
  }

  return { valid: true, rig };
}
