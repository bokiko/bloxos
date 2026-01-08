import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';
import { auditLog } from '../utils/security.ts';
import { sendCommandToRig } from './agent-websocket.ts';

// Validation schemas
const CreateMinerSchema = z.object({
  name: z.string().min(1).max(100),
  displayName: z.string().min(1).max(100),
  version: z.string().min(1).max(50),
  algorithms: z.array(z.string().min(1).max(50)),
  supportsNvidia: z.boolean().default(false),
  supportsAmd: z.boolean().default(false),
  supportsCpu: z.boolean().default(false),
  supportedGpus: z.array(z.enum(['NVIDIA', 'AMD', 'INTEL'])).optional(),
  apiPort: z.number().min(1).max(65535),
  apiType: z.string().min(1).max(20),
  githubRepo: z.string().max(200).optional(),
  binaryName: z.string().max(100).optional(),
  linuxAssetPattern: z.string().max(200).optional(),
  installPath: z.string().max(500).optional(),
  defaultArgs: z.string().max(1000).optional(),
});

const UpdateMinerSchema = z.object({
  name: z.string().min(1).max(100).optional(),
  displayName: z.string().min(1).max(100).optional(),
  version: z.string().min(1).max(50).optional(),
  algorithms: z.array(z.string().min(1).max(50)).optional(),
  supportsNvidia: z.boolean().optional(),
  supportsAmd: z.boolean().optional(),
  supportsCpu: z.boolean().optional(),
  supportedGpus: z.array(z.enum(['NVIDIA', 'AMD', 'INTEL'])).optional(),
  apiPort: z.number().min(1).max(65535).optional(),
  apiType: z.string().min(1).max(20).optional(),
  githubRepo: z.string().max(200).nullable().optional(),
  binaryName: z.string().max(100).nullable().optional(),
  linuxAssetPattern: z.string().max(200).nullable().optional(),
  installPath: z.string().max(500).nullable().optional(),
  defaultArgs: z.string().max(1000).nullable().optional(),
});

export async function minerRoutes(app: FastifyInstance) {
  // List all miners with install info
  app.get('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const miners = await prisma.minerSoftware.findMany({
      include: {
        _count: {
          select: { flightSheets: true },
        },
      },
      orderBy: [{ name: 'asc' }, { version: 'desc' }],
    });

    return reply.send(miners);
  });

  // Get miners available for installation (with GitHub info)
  app.get('/available', async (request: FastifyRequest, reply: FastifyReply) => {
    const miners = await prisma.minerSoftware.findMany({
      where: {
        githubRepo: { not: null },
      },
      select: {
        id: true,
        name: true,
        displayName: true,
        version: true,
        algorithms: true,
        supportsNvidia: true,
        supportsAmd: true,
        supportsCpu: true,
        githubRepo: true,
        binaryName: true,
        linuxAssetPattern: true,
        apiPort: true,
        defaultArgs: true,
      },
      orderBy: { displayName: 'asc' },
    });

    return reply.send(miners);
  });

  // Install miner on a rig
  app.post('/install', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const schema = z.object({
      rigId: z.string(),
      minerName: z.string(),
    });

    const { rigId, minerName } = schema.parse(request.body);

    // Verify rig exists and user has access
    const rig = await prisma.rig.findUnique({
      where: { id: rigId },
      include: { farm: { select: { ownerId: true } } },
    });

    if (!rig) {
      return reply.status(404).send({ error: 'Rig not found' });
    }

    if (user.role !== 'ADMIN' && rig.farm.ownerId !== user.userId) {
      return reply.status(403).send({ error: 'Access denied' });
    }

    // Send install command to rig via WebSocket
    const commandId = sendCommandToRig(rigId, { type: 'install_miner', payload: { minerName } });

    if (!commandId) {
      return reply.status(503).send({ 
        error: 'Rig is offline or command failed',
        details: 'The rig must be online to install miners',
      });
    }

    auditLog({
      userId: user.userId,
      action: 'install_miner',
      resource: 'rig',
      resourceId: rigId,
      ip: request.ip,
      success: true,
      details: { minerName },
    });

    return reply.send({ 
      success: true, 
      message: `Installing ${minerName} on ${rig.name}...`,
    });
  });

  // Uninstall miner from a rig
  app.post('/uninstall', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    const schema = z.object({
      rigId: z.string(),
      minerName: z.string(),
    });

    const { rigId, minerName } = schema.parse(request.body);

    // Verify rig exists and user has access
    const rig = await prisma.rig.findUnique({
      where: { id: rigId },
      include: { farm: { select: { ownerId: true } } },
    });

    if (!rig) {
      return reply.status(404).send({ error: 'Rig not found' });
    }

    if (user.role !== 'ADMIN' && rig.farm.ownerId !== user.userId) {
      return reply.status(403).send({ error: 'Access denied' });
    }

    // Send uninstall command to rig via WebSocket
    const commandId = sendCommandToRig(rigId, { type: 'uninstall_miner', payload: { minerName } });

    if (!commandId) {
      return reply.status(503).send({ 
        error: 'Rig is offline or command failed',
      });
    }

    auditLog({
      userId: user.userId,
      action: 'uninstall_miner',
      resource: 'rig',
      resourceId: rigId,
      ip: request.ip,
      success: true,
      details: { minerName },
    });

    return reply.send({ 
      success: true, 
      message: `Uninstalling ${minerName} from ${rig.name}...`,
    });
  });

  // Get single miner
  app.get('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;

    const miner = await prisma.minerSoftware.findUnique({
      where: { id },
      include: {
        flightSheets: true,
      },
    });

    if (!miner) {
      return reply.status(404).send({ message: 'Miner not found' });
    }

    return reply.send(miner);
  });

  // Create miner
  app.post('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const data = CreateMinerSchema.parse(request.body);

    const miner = await prisma.minerSoftware.create({
      data: {
        name: data.name,
        displayName: data.displayName,
        version: data.version,
        algorithms: data.algorithms.map(a => a.toLowerCase()),
        supportsNvidia: data.supportsNvidia,
        supportsAmd: data.supportsAmd,
        supportsCpu: data.supportsCpu,
        supportedGpus: data.supportedGpus || [],
        apiPort: data.apiPort,
        apiType: data.apiType,
        githubRepo: data.githubRepo || null,
        binaryName: data.binaryName || null,
        linuxAssetPattern: data.linuxAssetPattern || null,
        installPath: data.installPath || null,
        defaultArgs: data.defaultArgs || null,
      },
    });

    return reply.status(201).send(miner);
  });

  // Update miner
  app.patch('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;
    const data = UpdateMinerSchema.parse(request.body);

    const existing = await prisma.minerSoftware.findUnique({ where: { id } });
    if (!existing) {
      return reply.status(404).send({ message: 'Miner not found' });
    }

    const miner = await prisma.minerSoftware.update({
      where: { id },
      data: {
        ...data,
        algorithms: data.algorithms?.map(a => a.toLowerCase()),
      },
    });

    return reply.send(miner);
  });

  // Delete miner
  app.delete('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const { id } = request.params;

    const existing = await prisma.minerSoftware.findUnique({
      where: { id },
      include: { _count: { select: { flightSheets: true } } },
    });

    if (!existing) {
      return reply.status(404).send({ message: 'Miner not found' });
    }

    if (existing._count.flightSheets > 0) {
      return reply.status(400).send({
        message: `Cannot delete miner: used by ${existing._count.flightSheets} flight sheet(s)`,
      });
    }

    await prisma.minerSoftware.delete({ where: { id } });

    return reply.send({ success: true });
  });

  // Seed default miners (admin only)
  app.post('/seed', async (request: FastifyRequest, reply: FastifyReply) => {
    const user = request.user;
    if (!user) {
      return reply.status(401).send({ error: 'Authentication required' });
    }

    // Only admins can seed miners
    if (user.role !== 'ADMIN') {
      auditLog({
        userId: user.userId,
        action: 'unauthorized_seed_miners',
        resource: 'minerSoftware',
        ip: request.ip,
        success: false,
      });
      return reply.status(403).send({ error: 'Admin access required' });
    }

    // Note: Miners are now seeded via packages/database/seed/miners.json
    // This endpoint is deprecated - use pnpm db:seed instead
    auditLog({
      userId: user.userId,
      action: 'seed_miners_deprecated',
      resource: 'minerSoftware',
      ip: request.ip,
      success: true,
    });

    return reply.send({
      success: true,
      message: 'Miners are now seeded via pnpm db:seed. Run that command instead.',
    });
  });
}
