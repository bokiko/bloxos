import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';
import { validateAgentToken } from '../middleware/authorization.ts';
import { auditLog, checkRateLimit } from '../utils/security.ts';

// Validation schemas
const RegisterSchema = z.object({
  token: z.string().min(10).max(100),
  hostname: z.string().max(255).optional(),
  os: z.string().max(100).optional(),
  osVersion: z.string().max(50).optional(),
});

const GPUStatsSchema = z.object({
  index: z.number(),
  name: z.string().optional(),
  temperature: z.number().nullable().optional(),
  memTemp: z.number().nullable().optional(),
  fanSpeed: z.number().nullable().optional(),
  powerDraw: z.number().nullable().optional(),
  coreClock: z.number().nullable().optional(),
  memoryClock: z.number().nullable().optional(),
  utilization: z.number().nullable().optional(),
  vram: z.number().optional(),
  busId: z.string().optional(),
});

const CPUStatsSchema = z.object({
  model: z.string().optional(),
  vendor: z.string().optional(),
  cores: z.number().optional(),
  threads: z.number().optional(),
  temperature: z.number().nullable().optional(),
  usage: z.number().nullable().optional(),
  frequency: z.number().nullable().optional(),
  powerDraw: z.number().nullable().optional(),
});

const ReportSchema = z.object({
  token: z.string(),
  gpus: z.array(GPUStatsSchema).optional(),
  cpu: CPUStatsSchema.optional(),
  timestamp: z.string().optional(),
});

const HeartbeatSchema = z.object({
  token: z.string(),
});

export async function agentRoutes(app: FastifyInstance) {
  // Rate limit agent endpoints by IP
  app.addHook('onRequest', async (request, reply) => {
    const ip = request.ip || 'unknown';
    const rateLimit = checkRateLimit(`agent:${ip}`, 60, 60000); // 60 requests per minute
    
    if (!rateLimit.allowed) {
      auditLog({
        action: 'agent_rate_limited',
        resource: 'agent',
        ip,
        success: false,
        error: 'Rate limit exceeded',
      });
      return reply.status(429).send({ 
        error: 'Too many requests', 
        retryAfter: Math.ceil(rateLimit.resetIn / 1000) 
      });
    }
  });

  // Register/update rig
  app.post('/register', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = RegisterSchema.safeParse(request.body);
    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const { token, hostname, os, osVersion } = result.data;

    // Validate token against database
    const validation = await validateAgentToken(token);
    if (!validation.valid || !validation.rig) {
      auditLog({
        action: 'agent_register_failed',
        resource: 'agent',
        ip: request.ip,
        success: false,
        error: validation.error || 'Invalid token',
      });
      return reply.status(401).send({ error: 'Invalid token' });
    }

    const rig = await prisma.rig.findUnique({
      where: { id: validation.rig.id },
    });

    if (!rig) {
      return reply.status(401).send({ error: 'Invalid token' });
    }

    // Update rig info
    await prisma.rig.update({
      where: { id: rig.id },
      data: {
        hostname: hostname || rig.hostname,
        os: os || rig.os,
        osVersion: osVersion || rig.osVersion,
        agentVersion: '0.1.0',
        status: 'ONLINE',
        lastSeen: new Date(),
      },
    });

    auditLog({
      action: 'agent_registered',
      resource: 'rig',
      resourceId: rig.id,
      ip: request.ip,
      success: true,
    });

    return reply.send({ success: true, rigId: rig.id });
  });

  // Report stats
  app.post('/report', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = ReportSchema.safeParse(request.body);
    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const { token, gpus, cpu } = result.data;

    // Validate token against database
    const validation = await validateAgentToken(token);
    if (!validation.valid || !validation.rig) {
      return reply.status(401).send({ error: 'Invalid token' });
    }

    const rig = await prisma.rig.findUnique({
      where: { id: validation.rig.id },
      include: { flightSheet: true },
    });

    if (!rig) {
      return reply.status(401).send({ error: 'Invalid token' });
    }

    // Update rig status
    await prisma.rig.update({
      where: { id: rig.id },
      data: {
        status: 'ONLINE',
        lastSeen: new Date(),
      },
    });

    // Update GPU stats
    if (gpus && gpus.length > 0) {
      for (const gpu of gpus) {
        // Find or create GPU
        const existingGpu = await prisma.gPU.findFirst({
          where: { rigId: rig.id, index: gpu.index },
        });

        if (existingGpu) {
          await prisma.gPU.update({
            where: { id: existingGpu.id },
            data: {
              name: gpu.name || existingGpu.name,
              temperature: gpu.temperature ?? existingGpu.temperature,
              memTemp: gpu.memTemp ?? existingGpu.memTemp,
              fanSpeed: gpu.fanSpeed ?? existingGpu.fanSpeed,
              powerDraw: gpu.powerDraw ?? existingGpu.powerDraw,
              coreClock: gpu.coreClock ?? existingGpu.coreClock,
              memoryClock: gpu.memoryClock ?? existingGpu.memoryClock,
              vram: gpu.vram || existingGpu.vram,
              busId: gpu.busId || existingGpu.busId,
            },
          });
        } else if (gpu.name) {
          // Create new GPU
          await prisma.gPU.create({
            data: {
              rigId: rig.id,
              index: gpu.index,
              name: gpu.name,
              vendor: gpu.name.toLowerCase().includes('nvidia') ? 'NVIDIA' :
                      gpu.name.toLowerCase().includes('amd') ? 'AMD' : 'INTEL',
              vram: gpu.vram || 0,
              busId: gpu.busId,
              temperature: gpu.temperature,
              memTemp: gpu.memTemp,
              fanSpeed: gpu.fanSpeed,
              powerDraw: gpu.powerDraw,
              coreClock: gpu.coreClock,
              memoryClock: gpu.memoryClock,
            },
          });
        }
      }
    }

    // Update CPU stats
    if (cpu) {
      const existingCpu = await prisma.cPU.findUnique({
        where: { rigId: rig.id },
      });

      if (existingCpu) {
        await prisma.cPU.update({
          where: { id: existingCpu.id },
          data: {
            model: cpu.model || existingCpu.model,
            vendor: cpu.vendor || existingCpu.vendor,
            cores: cpu.cores || existingCpu.cores,
            threads: cpu.threads || existingCpu.threads,
            temperature: cpu.temperature ?? existingCpu.temperature,
            usage: cpu.usage ?? existingCpu.usage,
            frequency: cpu.frequency ?? existingCpu.frequency,
            powerDraw: cpu.powerDraw ?? existingCpu.powerDraw,
          },
        });
      } else if (cpu.model) {
        // Create new CPU
        await prisma.cPU.create({
          data: {
            rigId: rig.id,
            model: cpu.model,
            vendor: cpu.vendor || 'Unknown',
            cores: cpu.cores || 1,
            threads: cpu.threads || 1,
            temperature: cpu.temperature,
            usage: cpu.usage,
            frequency: cpu.frequency,
            powerDraw: cpu.powerDraw,
          },
        });
      }
    }

    // Return response with any pending commands
    return reply.send({
      success: true,
      command: '', // TODO: Implement command queue
      config: {
        flightSheetId: rig.flightSheetId,
        gpuEnabled: rig.gpuMiningEnabled,
        cpuEnabled: rig.cpuMiningEnabled,
      },
    });
  });

  // Heartbeat
  app.post('/heartbeat', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = HeartbeatSchema.safeParse(request.body);
    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed' });
    }

    const { token } = result.data;

    // Validate token against database
    const validation = await validateAgentToken(token);
    if (!validation.valid || !validation.rig) {
      return reply.status(401).send({ error: 'Invalid token' });
    }

    const rig = await prisma.rig.findUnique({
      where: { id: validation.rig.id },
    });

    if (!rig) {
      return reply.status(401).send({ error: 'Invalid token' });
    }

    await prisma.rig.update({
      where: { id: rig.id },
      data: {
        status: 'ONLINE',
        lastSeen: new Date(),
      },
    });

    return reply.send({ success: true });
  });
}
