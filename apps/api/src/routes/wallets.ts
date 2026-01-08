import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';
import { getUserFarmIds } from '../middleware/authorization.ts';
import { auditLog } from '../utils/security.ts';
import { validateWalletAddress } from '../utils/wallet-validator.js';

// Validation schemas
const CreateWalletSchema = z.object({
  name: z.string().min(1).max(100),
  coin: z.string().min(1).max(20),
  address: z.string().min(10).max(200),
  source: z.string().max(100).optional(),
  farmId: z.string().min(1).max(50),
});

const UpdateWalletSchema = z.object({
  name: z.string().min(1).max(100).optional(),
  coin: z.string().min(1).max(20).optional(),
  address: z.string().min(10).max(200).optional(),
  source: z.string().max(100).nullable().optional(),
});

export async function walletRoutes(app: FastifyInstance) {
  // List wallets - filtered by user's farms
  app.get('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const farmIds = await getUserFarmIds(user.userId, user.role);

    const wallets = await prisma.wallet.findMany({
      where: { farmId: { in: farmIds } },
      include: {
        farm: { select: { id: true, name: true } },
        _count: {
          select: { flightSheets: true },
        },
      },
      orderBy: { createdAt: 'desc' },
    });

    return reply.send(wallets);
  });

  // Get single wallet - with authorization check
  app.get('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const wallet = await prisma.wallet.findUnique({
      where: { id },
      include: {
        farm: { select: { id: true, name: true, ownerId: true } },
        flightSheets: true,
      },
    });

    if (!wallet) {
      return reply.status(404).send({ error: 'Wallet not found' });
    }

    // Check authorization
    if (user.role !== 'ADMIN' && wallet.farm.ownerId !== user.userId) {
      return reply.status(403).send({ error: 'Access denied' });
    }

    return reply.send(wallet);
  });

  // Create wallet - verify user owns the farm
  app.post('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const data = CreateWalletSchema.parse(request.body);

    // Verify user owns the farm (or is admin)
    const farmIds = await getUserFarmIds(user.userId, user.role);
    if (!farmIds.includes(data.farmId)) {
      return reply.status(403).send({ error: 'Access denied to this farm' });
    }

    // Validate wallet address format
    const validation = await validateWalletAddress(data.coin, data.address);
    if (!validation.valid) {
      return reply.status(400).send({ 
        error: 'Invalid wallet address',
        details: validation.error,
        field: 'address',
      });
    }

    const wallet = await prisma.wallet.create({
      data: {
        name: data.name,
        coin: data.coin.toUpperCase(),
        address: data.address,
        source: data.source || null,
        farmId: data.farmId,
      },
    });

    auditLog({
      userId: user.userId,
      action: 'wallet_create',
      resource: 'wallet',
      resourceId: wallet.id,
      ip: request.ip,
      success: true,
    });

    return reply.status(201).send(wallet);
  });

  // Update wallet - with authorization check
  app.patch('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const data = UpdateWalletSchema.parse(request.body);

    // Check ownership
    const existing = await prisma.wallet.findUnique({
      where: { id },
      include: { farm: { select: { ownerId: true } } },
    });

    if (!existing) {
      return reply.status(404).send({ error: 'Wallet not found' });
    }

    if (user.role !== 'ADMIN' && existing.farm.ownerId !== user.userId) {
      return reply.status(403).send({ error: 'Access denied' });
    }

    // If address or coin is being updated, validate the new address
    const coinToValidate = data.coin?.toUpperCase() || existing.coin;
    const addressToValidate = data.address || existing.address;
    
    if (data.address || data.coin) {
      const validation = await validateWalletAddress(coinToValidate, addressToValidate);
      if (!validation.valid) {
        return reply.status(400).send({ 
          error: 'Invalid wallet address',
          details: validation.error,
          field: 'address',
        });
      }
    }

    const wallet = await prisma.wallet.update({
      where: { id },
      data: {
        ...data,
        coin: data.coin?.toUpperCase(),
      },
    });

    auditLog({
      userId: user.userId,
      action: 'wallet_update',
      resource: 'wallet',
      resourceId: id,
      ip: request.ip,
      success: true,
    });

    return reply.send(wallet);
  });

  // Delete wallet - with authorization check
  app.delete('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const existing = await prisma.wallet.findUnique({
      where: { id },
      include: {
        farm: { select: { ownerId: true } },
        _count: { select: { flightSheets: true } },
      },
    });

    if (!existing) {
      return reply.status(404).send({ error: 'Wallet not found' });
    }

    if (user.role !== 'ADMIN' && existing.farm.ownerId !== user.userId) {
      return reply.status(403).send({ error: 'Access denied' });
    }

    if (existing._count.flightSheets > 0) {
      return reply.status(400).send({
        error: `Cannot delete wallet: used by ${existing._count.flightSheets} flight sheet(s)`,
      });
    }

    await prisma.wallet.delete({ where: { id } });

    auditLog({
      userId: user.userId,
      action: 'wallet_delete',
      resource: 'wallet',
      resourceId: id,
      ip: request.ip,
      success: true,
    });

    return reply.send({ success: true });
  });
}
