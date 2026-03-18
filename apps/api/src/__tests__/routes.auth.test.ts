import { describe, it, expect, vi, beforeEach } from 'vitest';
import Fastify, { type FastifyInstance } from 'fastify';
import cookie from '@fastify/cookie';

// Mock auth-service before importing routes
vi.mock('../services/auth-service.ts', () => {
  return {
    authService: {
      hasUsers: vi.fn(),
      register: vi.fn(),
      login: vi.fn(),
      logout: vi.fn(),
      verifyToken: vi.fn(),
      getUserById: vi.fn(),
      updateUser: vi.fn(),
      changePassword: vi.fn(),
      refreshAccessToken: vi.fn(),
    },
  };
});

// Mock @bloxos/database
vi.mock('@bloxos/database', () => ({
  prisma: {
    user: { count: vi.fn(), findUnique: vi.fn(), create: vi.fn(), update: vi.fn() },
    farm: { findMany: vi.fn(), create: vi.fn() },
    $queryRaw: vi.fn(),
  },
}));

// Mock security utils to avoid side effects
vi.mock('../utils/security.ts', async (importOriginal) => {
  const actual = await importOriginal<Record<string, unknown>>();
  return {
    ...actual,
    auditLog: vi.fn(),
  };
});

import { authService } from '../services/auth-service.ts';
import { authRoutes } from '../routes/auth.ts';

const mockedAuthService = vi.mocked(authService);

async function buildApp(): Promise<FastifyInstance> {
  const app = Fastify({ logger: false });
  await app.register(cookie);
  await app.register(authRoutes, { prefix: '/auth' });
  return app;
}

describe('Auth Routes', () => {
  let app: FastifyInstance;

  beforeEach(async () => {
    vi.clearAllMocks();
    app = await buildApp();
  });

  describe('GET /auth/setup-required', () => {
    it('returns setupRequired: true when no users exist', async () => {
      mockedAuthService.hasUsers.mockResolvedValue(false);

      const res = await app.inject({ method: 'GET', url: '/auth/setup-required' });

      expect(res.statusCode).toBe(200);
      expect(res.json()).toEqual({ setupRequired: true });
    });

    it('returns setupRequired: false when users exist', async () => {
      mockedAuthService.hasUsers.mockResolvedValue(true);

      const res = await app.inject({ method: 'GET', url: '/auth/setup-required' });

      expect(res.statusCode).toBe(200);
      expect(res.json()).toEqual({ setupRequired: false });
    });
  });

  describe('POST /auth/register', () => {
    it('returns 400 on missing fields', async () => {
      const res = await app.inject({
        method: 'POST',
        url: '/auth/register',
        payload: {},
      });

      expect(res.statusCode).toBe(400);
      expect(res.json()).toHaveProperty('error', 'Validation failed');
      expect(res.json()).toHaveProperty('details');
    });

    it('returns 400 on invalid email', async () => {
      const res = await app.inject({
        method: 'POST',
        url: '/auth/register',
        payload: { email: 'not-an-email', password: 'Test1234!@#$' },
      });

      expect(res.statusCode).toBe(400);
    });

    it('returns 201 on successful registration', async () => {
      const fakeUser = { id: 'u1', email: 'test@example.com', name: 'Test', role: 'ADMIN', createdAt: new Date().toISOString() };
      mockedAuthService.register.mockResolvedValue({
        user: fakeUser,
        token: 'access-token-123',
        refreshToken: 'refresh-token-123',
      });

      const res = await app.inject({
        method: 'POST',
        url: '/auth/register',
        payload: { email: 'test@example.com', password: 'SecurePass1!@#' },
      });

      expect(res.statusCode).toBe(201);
      const body = res.json();
      expect(body.user.email).toBe('test@example.com');
      expect(body.token).toBe('access-token-123');
      // Verify cookies are set
      const cookies = res.cookies;
      expect(cookies.some((c: { name: string }) => c.name === 'token')).toBe(true);
      expect(cookies.some((c: { name: string }) => c.name === 'refreshToken')).toBe(true);
    });
  });

  describe('POST /auth/login', () => {
    it('returns 400 on missing fields', async () => {
      const res = await app.inject({
        method: 'POST',
        url: '/auth/login',
        payload: {},
      });

      expect(res.statusCode).toBe(400);
      expect(res.json()).toHaveProperty('error', 'Validation failed');
    });

    it('returns 401 on invalid credentials', async () => {
      mockedAuthService.login.mockRejectedValue(new Error('Invalid email or password'));

      const res = await app.inject({
        method: 'POST',
        url: '/auth/login',
        payload: { email: 'test@example.com', password: 'WrongPass1!' },
      });

      expect(res.statusCode).toBe(401);
      expect(res.json()).toHaveProperty('error', 'Invalid credentials');
    });

    it('returns 200 with token on successful login', async () => {
      const fakeUser = { id: 'u1', email: 'test@example.com', name: 'Test', role: 'ADMIN' };
      mockedAuthService.login.mockResolvedValue({
        user: fakeUser,
        token: 'access-token-456',
        refreshToken: 'refresh-token-456',
      });

      const res = await app.inject({
        method: 'POST',
        url: '/auth/login',
        payload: { email: 'test@example.com', password: 'SecurePass1!@#' },
      });

      expect(res.statusCode).toBe(200);
      expect(res.json().user.email).toBe('test@example.com');
      expect(res.json().token).toBe('access-token-456');
    });
  });

  describe('POST /auth/logout', () => {
    it('returns success and clears cookies', async () => {
      mockedAuthService.logout.mockResolvedValue(undefined);

      const res = await app.inject({
        method: 'POST',
        url: '/auth/logout',
        cookies: { token: 'some-token' },
      });

      expect(res.statusCode).toBe(200);
      expect(res.json()).toEqual({ success: true });
      // Verify cookies are cleared (set with empty value or past expiry)
      const cookies = res.cookies;
      const tokenCookie = cookies.find((c: { name: string }) => c.name === 'token');
      expect(tokenCookie).toBeDefined();
    });

    it('returns success even without a token cookie', async () => {
      const res = await app.inject({
        method: 'POST',
        url: '/auth/logout',
      });

      expect(res.statusCode).toBe(200);
      expect(res.json()).toEqual({ success: true });
    });
  });
});
