import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';
import { authService } from '../services/auth-service.ts';

// Validation schemas
const CreateUserSchema = z.object({
  email: z.string().email(),
  password: z.string().min(6),
  name: z.string().min(1).optional(),
  role: z.enum(['ADMIN', 'USER', 'MONITOR']).optional(),
});

const UpdateUserSchema = z.object({
  email: z.string().email().optional(),
  name: z.string().min(1).optional(),
  role: z.enum(['ADMIN', 'USER', 'MONITOR']).optional(),
});

// Helper to verify admin
async function requireAdmin(request: FastifyRequest, reply: FastifyReply) {
  const token = request.cookies.token || request.headers.authorization?.replace('Bearer ', '');

  if (!token) {
    return reply.status(401).send({ error: 'Not authenticated' });
  }

  const payload = await authService.verifyToken(token);
  if (!payload) {
    return reply.status(401).send({ error: 'Invalid token' });
  }

  if (payload.role !== 'ADMIN') {
    return reply.status(403).send({ error: 'Admin access required' });
  }

  return payload;
}

export async function userRoutes(app: FastifyInstance) {
  // List all users (admin only)
  app.get('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const admin = await requireAdmin(request, reply);
    if (!admin) return;

    const users = await prisma.user.findMany({
      select: {
        id: true,
        email: true,
        name: true,
        role: true,
        createdAt: true,
        updatedAt: true,
      },
      orderBy: { createdAt: 'desc' },
    });

    return reply.send(users);
  });

  // Get single user (admin only)
  app.get('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const admin = await requireAdmin(request, reply);
    if (!admin) return;

    const user = await prisma.user.findUnique({
      where: { id: request.params.id },
      select: {
        id: true,
        email: true,
        name: true,
        role: true,
        createdAt: true,
        updatedAt: true,
      },
    });

    if (!user) {
      return reply.status(404).send({ error: 'User not found' });
    }

    return reply.send(user);
  });

  // Create user (admin only)
  app.post('/', async (request: FastifyRequest, reply: FastifyReply) => {
    const admin = await requireAdmin(request, reply);
    if (!admin) return;

    const result = CreateUserSchema.safeParse(request.body);
    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Check if email exists
    const existing = await prisma.user.findUnique({ where: { email: result.data.email } });
    if (existing) {
      return reply.status(400).send({ error: 'Email already registered' });
    }

    // Hash password
    const hashedPassword = await authService.hashPassword(result.data.password);

    const user = await prisma.user.create({
      data: {
        email: result.data.email,
        password: hashedPassword,
        name: result.data.name,
        role: result.data.role || 'USER',
      },
      select: {
        id: true,
        email: true,
        name: true,
        role: true,
        createdAt: true,
      },
    });

    return reply.status(201).send(user);
  });

  // Update user (admin only)
  app.patch('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const admin = await requireAdmin(request, reply);
    if (!admin) return;

    const result = UpdateUserSchema.safeParse(request.body);
    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Check if user exists
    const existing = await prisma.user.findUnique({ where: { id: request.params.id } });
    if (!existing) {
      return reply.status(404).send({ error: 'User not found' });
    }

    // If email is being changed, check it's not taken
    if (result.data.email && result.data.email !== existing.email) {
      const emailTaken = await prisma.user.findUnique({ where: { email: result.data.email } });
      if (emailTaken) {
        return reply.status(400).send({ error: 'Email already in use' });
      }
    }

    const user = await prisma.user.update({
      where: { id: request.params.id },
      data: result.data,
      select: {
        id: true,
        email: true,
        name: true,
        role: true,
        createdAt: true,
        updatedAt: true,
      },
    });

    return reply.send(user);
  });

  // Reset user password (admin only)
  app.post('/:id/reset-password', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const admin = await requireAdmin(request, reply);
    if (!admin) return;

    const { password } = request.body as { password: string };

    if (!password || password.length < 6) {
      return reply.status(400).send({ error: 'Password must be at least 6 characters' });
    }

    const existing = await prisma.user.findUnique({ where: { id: request.params.id } });
    if (!existing) {
      return reply.status(404).send({ error: 'User not found' });
    }

    const hashedPassword = await authService.hashPassword(password);

    await prisma.user.update({
      where: { id: request.params.id },
      data: { password: hashedPassword },
    });

    return reply.send({ success: true });
  });

  // Delete user (admin only)
  app.delete('/:id', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const admin = await requireAdmin(request, reply);
    if (!admin) return;

    // Prevent deleting yourself
    if (request.params.id === admin.userId) {
      return reply.status(400).send({ error: 'Cannot delete your own account' });
    }

    const existing = await prisma.user.findUnique({ where: { id: request.params.id } });
    if (!existing) {
      return reply.status(404).send({ error: 'User not found' });
    }

    await prisma.user.delete({ where: { id: request.params.id } });

    return reply.send({ success: true });
  });
}
