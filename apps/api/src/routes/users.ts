import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';
import { authService } from '../services/auth-service.ts';
import { generateSecureToken, auditLog } from '../utils/security.ts';

// Validation schemas
const CreateUserSchema = z.object({
  email: z.string().email().max(254), // Max email length per RFC
  password: z.string().min(12).max(128), // Stronger min password requirement
  name: z.string().min(1).max(100).optional(),
  role: z.enum(['ADMIN', 'USER', 'MONITOR']).optional(),
});

const UpdateUserSchema = z.object({
  email: z.string().email().max(254).optional(),
  name: z.string().min(1).max(100).optional(),
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

  // Reset user password (admin only) - generates temporary password
  // User must change password on next login
  app.post('/:id/reset-password', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const admin = await requireAdmin(request, reply);
    if (!admin) return;

    const { id } = request.params;

    // Prevent admins from resetting their own password this way
    if (id === admin.userId) {
      return reply.status(400).send({ 
        error: 'Cannot reset your own password. Use the change password feature instead.' 
      });
    }

    const existing = await prisma.user.findUnique({ where: { id } });
    if (!existing) {
      return reply.status(404).send({ error: 'User not found' });
    }

    // Generate a secure temporary password
    const tempPassword = generateSecureToken(16);
    const hashedPassword = await authService.hashPassword(tempPassword);

    await prisma.user.update({
      where: { id },
      data: { 
        password: hashedPassword,
        // Note: In a full implementation, you'd set a flag like `mustChangePassword: true`
        // and check it on login to force password change
      },
    });

    // Log the password reset action
    auditLog({
      userId: admin.userId,
      action: 'admin_password_reset',
      resource: 'user',
      resourceId: id,
      details: { targetUserEmail: existing.email },
      ip: request.ip,
      success: true,
    });

    // Return temporary password - in production, this would be sent via email
    // For now, return it to the admin to communicate to the user securely
    return reply.send({ 
      success: true, 
      message: 'Password reset successfully. Provide this temporary password to the user securely.',
      temporaryPassword: tempPassword,
      note: 'The user should change this password immediately after logging in.',
    });
  });

  // Force logout all sessions for a user (admin only)
  app.post('/:id/logout-all', async (request: FastifyRequest<{ Params: { id: string } }>, reply: FastifyReply) => {
    const admin = await requireAdmin(request, reply);
    if (!admin) return;

    const { id } = request.params;

    const existing = await prisma.user.findUnique({ where: { id } });
    if (!existing) {
      return reply.status(404).send({ error: 'User not found' });
    }

    // Force logout all sessions
    const count = await authService.logoutAllSessions(id);

    auditLog({
      userId: admin.userId,
      action: 'admin_force_logout',
      resource: 'user',
      resourceId: id,
      details: { targetUserEmail: existing.email, sessionsTerminated: count },
      ip: request.ip,
      success: true,
    });

    return reply.send({ 
      success: true, 
      message: `Logged out ${count} session(s) for user ${existing.email}`,
    });
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
