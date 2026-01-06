import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';

// Validation schemas
const CreateWalletSchema = z.object({
  name: z.string().min(1).max(100),
  coin: z.string().min(1).max(20),
  address: z.string().min(1).max(200),
  source: z.string().max(100).optional(),
});

const UpdateWalletSchema = z.object({
  name: z.string().min(1).max(100).optional(),
  coin: z.string().min(1).max(20).optional(),
  address: z.string().min(1).max(200).optional(),
  source: z.string().max(100).nullable().optional(),
});

export async function walletRoutes(app: FastifyInstance) {
  // List all wallets
  app.get('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const wallets = await prisma.wallet.findMany({
      include: {
        _count: {
          select: { flightSheets: true },
        },
      },
      orderBy: { createdAt: 'desc' },
    });

    return reply.send(wallets);
  });

  // Get single wallet
  app.get('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;

    const wallet = await prisma.wallet.findUnique({
      where: { id },
      include: {
        flightSheets: true,
      },
    });

    if (!wallet) {
      return reply.status(404).send({ message: 'Wallet not found' });
    }

    return reply.send(wallet);
  });

  // Create wallet
  app.post('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const data = CreateWalletSchema.parse(request.body);

    const wallet = await prisma.wallet.create({
      data: {
        name: data.name,
        coin: data.coin.toUpperCase(),
        address: data.address,
        source: data.source || null,
      },
    });

    return reply.status(201).send(wallet);
  });

  // Update wallet
  app.patch('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const data = UpdateWalletSchema.parse(request.body);

    const existing = await prisma.wallet.findUnique({ where: { id } });
    if (!existing) {
      return reply.status(404).send({ message: 'Wallet not found' });
    }

    const wallet = await prisma.wallet.update({
      where: { id },
      data: {
        ...data,
        coin: data.coin?.toUpperCase(),
      },
    });

    return reply.send(wallet);
  });

  // Delete wallet
  app.delete('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;

    const existing = await prisma.wallet.findUnique({
      where: { id },
      include: { _count: { select: { flightSheets: true } } },
    });

    if (!existing) {
      return reply.status(404).send({ message: 'Wallet not found' });
    }

    if (existing._count.flightSheets > 0) {
      return reply.status(400).send({
        message: `Cannot delete wallet: used by ${existing._count.flightSheets} flight sheet(s)`,
      });
    }

    await prisma.wallet.delete({ where: { id } });

    return reply.send({ success: true });
  });
}
