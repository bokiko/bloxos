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

// Filter for farm-based resources (wallets, pools, OC profiles, flight sheets, rig groups)
export function getUserFarmResourceFilter(user: { userId: string; role: string }) {
  // Admins see all resources
  if (user.role === 'ADMIN') {
    return {};
  }

  // Regular users only see resources in their farms
  return {
    farm: {
      ownerId: user.userId,
    },
  };
}

// Get user's farm IDs for queries
export async function getUserFarmIds(userId: string, role: string): Promise<string[]> {
  // Admins have access to all farms
  if (role === 'ADMIN') {
    const allFarms = await prisma.farm.findMany({ select: { id: true } });
    return allFarms.map(f => f.id);
  }

  const farms = await prisma.farm.findMany({
    where: { ownerId: userId },
    select: { id: true },
  });

  return farms.map(f => f.id);
}

// Check if user owns a specific resource by farmId
export async function userOwnsFarm(userId: string, role: string, farmId: string): Promise<boolean> {
  if (role === 'ADMIN') return true;

  const farm = await prisma.farm.findUnique({
    where: { id: farmId },
    select: { ownerId: true },
  });

  return farm?.ownerId === userId;
}

// Verify user has access to wallet
export async function requireWalletAccess(
  request: FastifyRequest,
  reply: FastifyReply
) {
  const params = request.params as { id?: string };
  const walletId = params.id;
  const user = request.user;

  if (!user) {
    return reply.status(401).send({ error: 'Authentication required' });
  }

  if (!walletId) {
    return reply.status(400).send({ error: 'Wallet ID required' });
  }

  if (user.role === 'ADMIN') return;

  const wallet = await prisma.wallet.findUnique({
    where: { id: walletId },
    include: { farm: { select: { ownerId: true } } },
  });

  if (!wallet) {
    return reply.status(404).send({ error: 'Wallet not found' });
  }

  if (wallet.farm.ownerId !== user.userId) {
    auditLog({
      userId: user.userId,
      action: 'unauthorized_wallet_access',
      resource: 'wallet',
      resourceId: walletId,
      ip: request.ip,
      success: false,
    });
    return reply.status(403).send({ error: 'Access denied' });
  }
}

// Verify user has access to pool
export async function requirePoolAccess(
  request: FastifyRequest,
  reply: FastifyReply
) {
  const params = request.params as { id?: string };
  const poolId = params.id;
  const user = request.user;

  if (!user) {
    return reply.status(401).send({ error: 'Authentication required' });
  }

  if (!poolId) {
    return reply.status(400).send({ error: 'Pool ID required' });
  }

  if (user.role === 'ADMIN') return;

  const pool = await prisma.pool.findUnique({
    where: { id: poolId },
    include: { farm: { select: { ownerId: true } } },
  });

  if (!pool) {
    return reply.status(404).send({ error: 'Pool not found' });
  }

  if (pool.farm.ownerId !== user.userId) {
    auditLog({
      userId: user.userId,
      action: 'unauthorized_pool_access',
      resource: 'pool',
      resourceId: poolId,
      ip: request.ip,
      success: false,
    });
    return reply.status(403).send({ error: 'Access denied' });
  }
}

// Verify user has access to flight sheet
export async function requireFlightSheetAccess(
  request: FastifyRequest,
  reply: FastifyReply
) {
  const params = request.params as { id?: string };
  const flightSheetId = params.id;
  const user = request.user;

  if (!user) {
    return reply.status(401).send({ error: 'Authentication required' });
  }

  if (!flightSheetId) {
    return reply.status(400).send({ error: 'Flight sheet ID required' });
  }

  if (user.role === 'ADMIN') return;

  const flightSheet = await prisma.flightSheet.findUnique({
    where: { id: flightSheetId },
    include: { farm: { select: { ownerId: true } } },
  });

  if (!flightSheet) {
    return reply.status(404).send({ error: 'Flight sheet not found' });
  }

  if (flightSheet.farm.ownerId !== user.userId) {
    auditLog({
      userId: user.userId,
      action: 'unauthorized_flightsheet_access',
      resource: 'flightSheet',
      resourceId: flightSheetId,
      ip: request.ip,
      success: false,
    });
    return reply.status(403).send({ error: 'Access denied' });
  }
}

// Verify user has access to OC profile
export async function requireOCProfileAccess(
  request: FastifyRequest,
  reply: FastifyReply
) {
  const params = request.params as { id?: string };
  const profileId = params.id;
  const user = request.user;

  if (!user) {
    return reply.status(401).send({ error: 'Authentication required' });
  }

  if (!profileId) {
    return reply.status(400).send({ error: 'OC profile ID required' });
  }

  if (user.role === 'ADMIN') return;

  const profile = await prisma.oCProfile.findUnique({
    where: { id: profileId },
    include: { farm: { select: { ownerId: true } } },
  });

  if (!profile) {
    return reply.status(404).send({ error: 'OC profile not found' });
  }

  if (profile.farm.ownerId !== user.userId) {
    auditLog({
      userId: user.userId,
      action: 'unauthorized_ocprofile_access',
      resource: 'ocProfile',
      resourceId: profileId,
      ip: request.ip,
      success: false,
    });
    return reply.status(403).send({ error: 'Access denied' });
  }
}

// Verify user has access to rig group
export async function requireRigGroupAccess(
  request: FastifyRequest,
  reply: FastifyReply
) {
  const params = request.params as { id?: string };
  const groupId = params.id;
  const user = request.user;

  if (!user) {
    return reply.status(401).send({ error: 'Authentication required' });
  }

  if (!groupId) {
    return reply.status(400).send({ error: 'Rig group ID required' });
  }

  if (user.role === 'ADMIN') return;

  const group = await prisma.rigGroup.findUnique({
    where: { id: groupId },
    include: { farm: { select: { ownerId: true } } },
  });

  if (!group) {
    return reply.status(404).send({ error: 'Rig group not found' });
  }

  if (group.farm.ownerId !== user.userId) {
    auditLog({
      userId: user.userId,
      action: 'unauthorized_riggroup_access',
      resource: 'rigGroup',
      resourceId: groupId,
      ip: request.ip,
      success: false,
    });
    return reply.status(403).send({ error: 'Access denied' });
  }
}

// Filter rig IDs to only those the user owns
export async function filterOwnedRigIds(
  rigIds: string[],
  userId: string,
  role: string
): Promise<string[]> {
  if (role === 'ADMIN') return rigIds;

  const ownedRigs = await prisma.rig.findMany({
    where: {
      id: { in: rigIds },
      farm: { ownerId: userId },
    },
    select: { id: true },
  });

  return ownedRigs.map(r => r.id);
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
