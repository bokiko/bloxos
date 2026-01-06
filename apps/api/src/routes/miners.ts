import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';

// Validation schemas
const CreateMinerSchema = z.object({
  name: z.string().min(1).max(100),
  version: z.string().min(1).max(50),
  algo: z.string().min(1).max(50),
  supportedGpus: z.array(z.enum(['NVIDIA', 'AMD', 'INTEL'])),
  apiPort: z.number().min(1).max(65535),
  apiType: z.string().min(1).max(20),
  installUrl: z.string().max(500).optional(),
  defaultArgs: z.string().max(1000).optional(),
});

const UpdateMinerSchema = z.object({
  name: z.string().min(1).max(100).optional(),
  version: z.string().min(1).max(50).optional(),
  algo: z.string().min(1).max(50).optional(),
  supportedGpus: z.array(z.enum(['NVIDIA', 'AMD', 'INTEL'])).optional(),
  apiPort: z.number().min(1).max(65535).optional(),
  apiType: z.string().min(1).max(20).optional(),
  installUrl: z.string().max(500).nullable().optional(),
  defaultArgs: z.string().max(1000).nullable().optional(),
});

export async function minerRoutes(app: FastifyInstance) {
  // List all miners
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
        version: data.version,
        algo: data.algo.toLowerCase(),
        supportedGpus: data.supportedGpus,
        apiPort: data.apiPort,
        apiType: data.apiType,
        installUrl: data.installUrl || null,
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
        algo: data.algo?.toLowerCase(),
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

  // Seed default miners
  app.post('/seed', async (request: FastifyRequest, reply: FastifyReply) => {
    const defaultMiners = [
      // NVIDIA miners
      { name: 'T-Rex', version: '0.26.8', algo: 'ethash', supportedGpus: ['NVIDIA'], apiPort: 4067, apiType: 'http', defaultArgs: '--no-watchdog' },
      { name: 'T-Rex', version: '0.26.8', algo: 'kawpow', supportedGpus: ['NVIDIA'], apiPort: 4067, apiType: 'http', defaultArgs: '--no-watchdog' },
      { name: 'T-Rex', version: '0.26.8', algo: 'autolykos2', supportedGpus: ['NVIDIA'], apiPort: 4067, apiType: 'http', defaultArgs: '--no-watchdog' },
      { name: 'lolMiner', version: '1.76', algo: 'ethash', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4068, apiType: 'http', defaultArgs: '' },
      { name: 'lolMiner', version: '1.76', algo: 'etchash', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4068, apiType: 'http', defaultArgs: '' },
      { name: 'lolMiner', version: '1.76', algo: 'autolykos2', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4068, apiType: 'http', defaultArgs: '' },
      { name: 'Gminer', version: '3.44', algo: 'ethash', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4069, apiType: 'http', defaultArgs: '' },
      { name: 'Gminer', version: '3.44', algo: 'kawpow', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4069, apiType: 'http', defaultArgs: '' },
      { name: 'NBMiner', version: '42.3', algo: 'ethash', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4070, apiType: 'http', defaultArgs: '' },
      { name: 'NBMiner', version: '42.3', algo: 'kawpow', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4070, apiType: 'http', defaultArgs: '' },
      // AMD miners
      { name: 'TeamRedMiner', version: '0.10.14', algo: 'ethash', supportedGpus: ['AMD'], apiPort: 4071, apiType: 'http', defaultArgs: '' },
      { name: 'TeamRedMiner', version: '0.10.14', algo: 'kawpow', supportedGpus: ['AMD'], apiPort: 4071, apiType: 'http', defaultArgs: '' },
      { name: 'TeamRedMiner', version: '0.10.14', algo: 'autolykos2', supportedGpus: ['AMD'], apiPort: 4071, apiType: 'http', defaultArgs: '' },
      // CPU miners
      { name: 'XMRig', version: '6.21.0', algo: 'randomx', supportedGpus: ['NVIDIA', 'AMD', 'INTEL'], apiPort: 4072, apiType: 'http', defaultArgs: '' },
      // KAS miners
      { name: 'lolMiner', version: '1.76', algo: 'kaspa', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4068, apiType: 'http', defaultArgs: '' },
      { name: 'BzMiner', version: '19.3.0', algo: 'kaspa', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4073, apiType: 'http', defaultArgs: '' },
    ];

    let created = 0;
    let skipped = 0;

    for (const miner of defaultMiners) {
      try {
        await prisma.minerSoftware.create({
          data: miner as any,
        });
        created++;
      } catch {
        // Unique constraint violation - already exists
        skipped++;
      }
    }

    return reply.send({
      success: true,
      message: `Seeded ${created} miners, ${skipped} already existed`,
    });
  });
}
