import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { authService } from '../services/auth-service.ts';
import { validatePassword, validateEmail, PASSWORD_REQUIREMENTS, auditLog } from '../utils/security.ts';

// Password validation with security requirements
const passwordSchema = z.string().refine(
  (password) => validatePassword(password).valid,
  (password) => ({
    message: validatePassword(password).errors.join(', ') || 
      `Password must be at least ${PASSWORD_REQUIREMENTS.minLength} characters with uppercase, lowercase, number, and special character`
  })
);

// Validation schemas
const RegisterSchema = z.object({
  email: z.string().email(),
  password: passwordSchema,
  name: z.string().min(1).max(100).optional(),
});

const LoginSchema = z.object({
  email: z.string().email(),
  password: z.string().min(1),
});

const ChangePasswordSchema = z.object({
  currentPassword: z.string().min(1),
  newPassword: passwordSchema,
});

const UpdateProfileSchema = z.object({
  name: z.string().min(1).max(100).optional(),
  email: z.string().email().optional(),
});

// Token expiry aligned with auth-service (4 hours for access, 7 days for refresh)
const ACCESS_TOKEN_MAX_AGE = 4 * 60 * 60; // 4 hours in seconds
const REFRESH_TOKEN_MAX_AGE = 7 * 24 * 60 * 60; // 7 days in seconds

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
      return reply.status(400).send({ 
        error: 'Validation failed', 
        details: result.error.issues.map(i => ({ field: i.path.join('.'), message: i.message }))
      });
    }

    // Additional email validation
    if (!validateEmail(result.data.email)) {
      return reply.status(400).send({ error: 'Invalid email format' });
    }

    try {
      const { user, token: accessToken, refreshToken } = await authService.register(
        result.data.email,
        result.data.password,
        result.data.name
      );

      // Set access token cookie
      reply.setCookie('token', accessToken, {
        httpOnly: true,
        secure: process.env.NODE_ENV === 'production',
        sameSite: 'strict',
        path: '/',
        maxAge: ACCESS_TOKEN_MAX_AGE,
      });

      // Set refresh token cookie
      reply.setCookie('refreshToken', refreshToken, {
        httpOnly: true,
        secure: process.env.NODE_ENV === 'production',
        sameSite: 'strict',
        path: '/auth/refresh',
        maxAge: REFRESH_TOKEN_MAX_AGE,
      });

      auditLog('USER_REGISTERED', { userId: user.id, email: user.email });

      return reply.status(201).send({ user, token: accessToken });
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Registration failed';
      auditLog('REGISTRATION_FAILED', { email: result.data.email, error: message });
      return reply.status(400).send({ error: message });
    }
  });

  // Login
  app.post('/login', async (request: FastifyRequest, reply: FastifyReply) => {
    const result = LoginSchema.safeParse(request.body);

    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const clientIp = request.ip || request.headers['x-forwarded-for'] || 'unknown';

    try {
      const { user, token: accessToken, refreshToken } = await authService.login(
        result.data.email, 
        result.data.password,
        clientIp as string
      );

      // Set access token cookie
      reply.setCookie('token', accessToken, {
        httpOnly: true,
        secure: process.env.NODE_ENV === 'production',
        sameSite: 'strict',
        path: '/',
        maxAge: ACCESS_TOKEN_MAX_AGE,
      });

      // Set refresh token cookie
      reply.setCookie('refreshToken', refreshToken, {
        httpOnly: true,
        secure: process.env.NODE_ENV === 'production',
        sameSite: 'strict',
        path: '/auth/refresh',
        maxAge: REFRESH_TOKEN_MAX_AGE,
      });

      return reply.send({ user, token: accessToken });
    } catch {
      // Don't reveal whether email exists or password is wrong
      return reply.status(401).send({ error: 'Invalid credentials' });
    }
  });

  // Refresh token
  app.post('/refresh', async (request: FastifyRequest, reply: FastifyReply) => {
    const refreshToken = request.cookies.refreshToken;

    if (!refreshToken) {
      return reply.status(401).send({ error: 'No refresh token provided' });
    }

    try {
      const result = await authService.refreshAccessToken(refreshToken);
      
      if (!result) {
        reply.clearCookie('token', { path: '/' });
        reply.clearCookie('refreshToken', { path: '/auth/refresh' });
        return reply.status(401).send({ error: 'Invalid refresh token' });
      }

      // Set new access token cookie
      reply.setCookie('token', result.token, {
        httpOnly: true,
        secure: process.env.NODE_ENV === 'production',
        sameSite: 'strict',
        path: '/',
        maxAge: ACCESS_TOKEN_MAX_AGE,
      });

      return reply.send({ token: result.token });
    } catch {
      reply.clearCookie('token', { path: '/' });
      reply.clearCookie('refreshToken', { path: '/auth/refresh' });
      return reply.status(401).send({ error: 'Invalid refresh token' });
    }
  });

  // Logout
  app.post('/logout', async (request: FastifyRequest, reply: FastifyReply) => {
    const token = request.cookies.token || request.headers.authorization?.replace('Bearer ', '');
    
    if (token) {
      try {
        await authService.logout(token);
      } catch {
        // Ignore logout errors, still clear cookies
      }
    }

    reply.clearCookie('token', { path: '/' });
    reply.clearCookie('refreshToken', { path: '/auth/refresh' });
    return reply.send({ success: true });
  });

  // Get current user (requires auth)
  app.get('/me', async (request: FastifyRequest, reply: FastifyReply) => {
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

    const result = UpdateProfileSchema.safeParse(request.body);
    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    // Validate email if provided
    if (result.data.email && !validateEmail(result.data.email)) {
      return reply.status(400).send({ error: 'Invalid email format' });
    }

    try {
      const user = await authService.updateUser(payload.userId, result.data);
      auditLog('PROFILE_UPDATED', { userId: payload.userId });
      return reply.send({ user });
    } catch {
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
      return reply.status(400).send({ 
        error: 'Validation failed', 
        details: result.error.issues.map(i => ({ field: i.path.join('.'), message: i.message }))
      });
    }

    // Prevent reusing the same password
    if (result.data.currentPassword === result.data.newPassword) {
      return reply.status(400).send({ error: 'New password must be different from current password' });
    }

    try {
      await authService.changePassword(
        payload.userId,
        result.data.currentPassword,
        result.data.newPassword
      );

      auditLog('PASSWORD_CHANGED', { userId: payload.userId });

      // Invalidate current token after password change
      await authService.logout(token);
      reply.clearCookie('token', { path: '/' });
      reply.clearCookie('refreshToken', { path: '/auth/refresh' });

      return reply.send({ success: true, message: 'Password changed. Please log in again.' });
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Failed to change password';
      return reply.status(400).send({ error: message });
    }
  });

  // Get password requirements (for frontend validation)
  app.get('/password-requirements', async (request: FastifyRequest, reply: FastifyReply) => {
    return reply.send({
      minLength: PASSWORD_REQUIREMENTS.minLength,
      requireUppercase: PASSWORD_REQUIREMENTS.requireUppercase,
      requireLowercase: PASSWORD_REQUIREMENTS.requireLowercase,
      requireNumbers: PASSWORD_REQUIREMENTS.requireNumbers,
      requireSpecialChars: PASSWORD_REQUIREMENTS.requireSpecial,
    });
  });
}
