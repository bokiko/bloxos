import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { authService } from '../services/auth-service.ts';

// Validation schemas
const RegisterSchema = z.object({
  email: z.string().email(),
  password: z.string().min(6),
  name: z.string().min(1).optional(),
});

const LoginSchema = z.object({
  email: z.string().email(),
  password: z.string().min(1),
});

const ChangePasswordSchema = z.object({
  currentPassword: z.string().min(1),
  newPassword: z.string().min(6),
});

export async function authRoutes(app: FastifyInstance) {
  // Check if setup is needed (no users exist)
  app.get('/setup-required', async (request: FastifyRequest, reply: FastifyReply) => {
    const hasUsers = await authService.hasUsers();
    return reply.send({ setupRequired: !hasUsers });
  });

  // Register (first user becomes admin)
  app.post('/register', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = RegisterSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    try {
      const { user, token } = await authService.register(
        result.data.email,
        result.data.password,
        result.data.name
      );

      // Set cookie
      reply.setCookie('token', token, {
        httpOnly: true,
        secure: process.env.NODE_ENV === 'production',
        sameSite: 'lax',
        path: '/',
        maxAge: 7 * 24 * 60 * 60, // 7 days
      });

      return reply.status(201).send({ user, token });
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Registration failed';
      return reply.status(400).send({ error: message });
    }
  });

  // Login
  app.post('/login', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = LoginSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    try {
      const { user, token } = await authService.login(result.data.email, result.data.password);

      // Set cookie
      reply.setCookie('token', token, {
        httpOnly: true,
        secure: process.env.NODE_ENV === 'production',
        sameSite: 'lax',
        path: '/',
        maxAge: 7 * 24 * 60 * 60, // 7 days
      });

      return reply.send({ user, token });
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Login failed';
      return reply.status(401).send({ error: message });
    }
  });

  // Logout
  app.post('/logout', async (request: FastifyRequest, reply: FastifyReply) => {
    reply.clearCookie('token', { path: '/' });
    return reply.send({ success: true });
  });

  // Get current user (requires auth)
  app.get('/me', async (request: FastifyRequest, reply: FastifyReply) => {
    // Get token from cookie or header
    const token = request.cookies.token || request.headers.authorization?.replace('Bearer ', '');

    if (!token) {
      return reply.status(401).send({ error: 'Not authenticated' });
    }

    const payload = authService.verifyToken(token);
    if (!payload) {
      return reply.status(401).send({ error: 'Invalid token' });
    }

    const user = await authService.getUserById(payload.userId);
    if (!user) {
      return reply.status(401).send({ error: 'User not found' });
    }

    return reply.send({ user });
  });

  // Update profile
  app.patch('/me', async (request: FastifyRequest, reply: FastifyReply) => {
    const token = request.cookies.token || request.headers.authorization?.replace('Bearer ', '');

    if (!token) {
      return reply.status(401).send({ error: 'Not authenticated' });
    }

    const payload = authService.verifyToken(token);
    if (!payload) {
      return reply.status(401).send({ error: 'Invalid token' });
    }

    const { name, email } = request.body as { name?: string; email?: string };

    try {
      const user = await authService.updateUser(payload.userId, { name, email });
      return reply.send({ user });
    } catch (error) {
      return reply.status(400).send({ error: 'Failed to update profile' });
    }
  });

  // Change password
  app.post('/change-password', async (request: FastifyRequest, reply: FastifyReply) => {
    const token = request.cookies.token || request.headers.authorization?.replace('Bearer ', '');

    if (!token) {
      return reply.status(401).send({ error: 'Not authenticated' });
    }

    const payload = authService.verifyToken(token);
    if (!payload) {
      return reply.status(401).send({ error: 'Invalid token' });
    }

    const result = ChangePasswordSchema.safeParse(request.body);
    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    try {
      await authService.changePassword(
        payload.userId,
        result.data.currentPassword,
        result.data.newPassword
      );
      return reply.send({ success: true });
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to change password';
      return reply.status(400).send({ error: message });
    }
  });
}
