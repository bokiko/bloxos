import bcrypt from 'bcrypt';
import jwt from 'jsonwebtoken';
import { prisma } from '@bloxos/database';
import { validatePassword, validateEmail, auditLog, checkRateLimit } from '../utils/security.ts';

// JWT Configuration
const JWT_SECRET = process.env.JWT_SECRET;
const JWT_EXPIRES_IN = '4h'; // Reduced from 7d to 4h for security
const REFRESH_TOKEN_EXPIRES_IN = '7d';
const SALT_ROUNDS = 12; // Increased from 10

// Validate JWT_SECRET in production
if (process.env.NODE_ENV === 'production' && !JWT_SECRET) {
  throw new Error('JWT_SECRET environment variable is required in production');
}

// Use secure default only in development
const getJwtSecret = (): string => {
  if (JWT_SECRET) return JWT_SECRET;
  if (process.env.NODE_ENV !== 'production') {
    console.warn('[Security] Warning: Using insecure default JWT secret for development');
    return 'bloxos-dev-secret-change-in-production';
  }
  throw new Error('JWT_SECRET is required');
};

export interface JWTPayload {
  userId: string;
  email: string;
  role: string;
  type: 'access' | 'refresh';
}

// Token blacklist for logout (in production, use Redis)
const tokenBlacklist = new Set<string>();

export class AuthService {
  // Hash password with increased rounds
  async hashPassword(password: string): Promise<string> {
    return bcrypt.hash(password, SALT_ROUNDS);
  }

  // Verify password
  async verifyPassword(password: string, hash: string): Promise<boolean> {
    return bcrypt.compare(password, hash);
  }

  // Generate access JWT token
  generateToken(payload: Omit<JWTPayload, 'type'>): string {
    return jwt.sign(
      { ...payload, type: 'access' as const },
      getJwtSecret(),
      { expiresIn: JWT_EXPIRES_IN }
    );
  }

  // Generate refresh token
  generateRefreshToken(payload: Omit<JWTPayload, 'type'>): string {
    return jwt.sign(
      { ...payload, type: 'refresh' as const },
      getJwtSecret(),
      { expiresIn: REFRESH_TOKEN_EXPIRES_IN }
    );
  }

  // Verify JWT token
  verifyToken(token: string): JWTPayload | null {
    try {
      // Check if token is blacklisted
      if (tokenBlacklist.has(token)) {
        return null;
      }
      
      const payload = jwt.verify(token, getJwtSecret()) as JWTPayload;
      
      // Ensure it's an access token
      if (payload.type !== 'access') {
        return null;
      }
      
      return payload;
    } catch {
      return null;
    }
  }

  // Verify refresh token
  verifyRefreshToken(token: string): JWTPayload | null {
    try {
      if (tokenBlacklist.has(token)) {
        return null;
      }
      
      const payload = jwt.verify(token, getJwtSecret()) as JWTPayload;
      
      if (payload.type !== 'refresh') {
        return null;
      }
      
      return payload;
    } catch {
      return null;
    }
  }

  // Blacklist token (for logout)
  blacklistToken(token: string): void {
    tokenBlacklist.add(token);
    
    // Clean up old tokens periodically (tokens expire anyway)
    // In production, use Redis with TTL
    if (tokenBlacklist.size > 10000) {
      const oldTokens = Array.from(tokenBlacklist).slice(0, 5000);
      oldTokens.forEach(t => tokenBlacklist.delete(t));
    }
  }

  // Register new user with strong password validation
  async register(email: string, password: string, name?: string, ip?: string) {
    // Validate email format
    if (!validateEmail(email)) {
      throw new Error('Invalid email format');
    }
    
    // Validate password strength
    const passwordValidation = validatePassword(password);
    if (!passwordValidation.valid) {
      throw new Error(passwordValidation.errors.join('. '));
    }

    // Check if user exists
    const existing = await prisma.user.findUnique({ where: { email: email.toLowerCase() } });
    if (existing) {
      auditLog({
        action: 'register',
        resource: 'user',
        details: { email: email.toLowerCase(), reason: 'email_exists' },
        ip,
        success: false,
        error: 'Email already registered',
      });
      throw new Error('Email already registered');
    }

    // Hash password
    const hashedPassword = await this.hashPassword(password);

    // Check if this is the first user (make them admin)
    const userCount = await prisma.user.count();
    const role = userCount === 0 ? 'ADMIN' : 'USER';

    // Create user
    const user = await prisma.user.create({
      data: {
        email: email.toLowerCase(),
        password: hashedPassword,
        name: name?.trim(),
        role: role as 'ADMIN' | 'USER' | 'MONITOR',
      },
      select: {
        id: true,
        email: true,
        name: true,
        role: true,
        createdAt: true,
      },
    });

    // Generate tokens
    const tokenPayload = {
      userId: user.id,
      email: user.email,
      role: user.role,
    };
    const token = this.generateToken(tokenPayload);
    const refreshToken = this.generateRefreshToken(tokenPayload);

    auditLog({
      userId: user.id,
      action: 'register',
      resource: 'user',
      resourceId: user.id,
      details: { email: user.email, role: user.role },
      ip,
      success: true,
    });

    return { user, token, refreshToken };
  }

  // Login user with rate limiting
  async login(email: string, password: string, ip?: string) {
    // Rate limiting per IP
    if (ip) {
      const rateLimit = checkRateLimit(`login:${ip}`, 5, 60000); // 5 attempts per minute
      if (!rateLimit.allowed) {
        auditLog({
          action: 'login',
          resource: 'user',
          details: { email: email.toLowerCase(), reason: 'rate_limited' },
          ip,
          success: false,
          error: 'Too many login attempts',
        });
        throw new Error(`Too many login attempts. Try again in ${Math.ceil(rateLimit.resetIn / 1000)} seconds`);
      }
    }

    // Find user
    const user = await prisma.user.findUnique({ where: { email: email.toLowerCase() } });
    if (!user) {
      auditLog({
        action: 'login',
        resource: 'user',
        details: { email: email.toLowerCase(), reason: 'user_not_found' },
        ip,
        success: false,
        error: 'Invalid credentials',
      });
      throw new Error('Invalid email or password');
    }

    // Verify password
    const valid = await this.verifyPassword(password, user.password);
    if (!valid) {
      auditLog({
        userId: user.id,
        action: 'login',
        resource: 'user',
        resourceId: user.id,
        details: { reason: 'invalid_password' },
        ip,
        success: false,
        error: 'Invalid credentials',
      });
      throw new Error('Invalid email or password');
    }

    // Generate tokens
    const tokenPayload = {
      userId: user.id,
      email: user.email,
      role: user.role,
    };
    const token = this.generateToken(tokenPayload);
    const refreshToken = this.generateRefreshToken(tokenPayload);

    auditLog({
      userId: user.id,
      action: 'login',
      resource: 'user',
      resourceId: user.id,
      ip,
      success: true,
    });

    return {
      user: {
        id: user.id,
        email: user.email,
        name: user.name,
        role: user.role,
      },
      token,
      refreshToken,
    };
  }

  // Logout - blacklist tokens
  logout(token: string, refreshToken?: string): void {
    this.blacklistToken(token);
    if (refreshToken) {
      this.blacklistToken(refreshToken);
    }
  }

  // Refresh access token
  async refreshAccessToken(refreshToken: string): Promise<{ token: string } | null> {
    const payload = this.verifyRefreshToken(refreshToken);
    if (!payload) {
      return null;
    }

    // Verify user still exists
    const user = await prisma.user.findUnique({ where: { id: payload.userId } });
    if (!user) {
      return null;
    }

    // Generate new access token
    const newToken = this.generateToken({
      userId: user.id,
      email: user.email,
      role: user.role,
    });

    return { token: newToken };
  }

  // Get user by ID
  async getUserById(userId: string) {
    return prisma.user.findUnique({
      where: { id: userId },
      select: {
        id: true,
        email: true,
        name: true,
        role: true,
        createdAt: true,
      },
    });
  }

  // Update user
  async updateUser(userId: string, data: { name?: string; email?: string }) {
    if (data.email) {
      if (!validateEmail(data.email)) {
        throw new Error('Invalid email format');
      }
      data.email = data.email.toLowerCase();
    }
    
    return prisma.user.update({
      where: { id: userId },
      data,
      select: {
        id: true,
        email: true,
        name: true,
        role: true,
      },
    });
  }

  // Change password with validation
  async changePassword(userId: string, currentPassword: string, newPassword: string, ip?: string) {
    const user = await prisma.user.findUnique({ where: { id: userId } });
    if (!user) {
      throw new Error('User not found');
    }

    // Verify current password
    const valid = await this.verifyPassword(currentPassword, user.password);
    if (!valid) {
      auditLog({
        userId,
        action: 'change_password',
        resource: 'user',
        resourceId: userId,
        ip,
        success: false,
        error: 'Invalid current password',
      });
      throw new Error('Current password is incorrect');
    }

    // Validate new password strength
    const passwordValidation = validatePassword(newPassword);
    if (!passwordValidation.valid) {
      throw new Error(passwordValidation.errors.join('. '));
    }

    // Ensure new password is different
    const samePassword = await this.verifyPassword(newPassword, user.password);
    if (samePassword) {
      throw new Error('New password must be different from current password');
    }

    // Hash new password
    const hashedPassword = await this.hashPassword(newPassword);

    // Update password
    await prisma.user.update({
      where: { id: userId },
      data: { password: hashedPassword },
    });

    auditLog({
      userId,
      action: 'change_password',
      resource: 'user',
      resourceId: userId,
      ip,
      success: true,
    });

    return { success: true };
  }

  // Check if any users exist (for initial setup)
  async hasUsers(): Promise<boolean> {
    const count = await prisma.user.count();
    return count > 0;
  }
}

export const authService = new AuthService();
