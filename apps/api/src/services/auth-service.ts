import bcrypt from 'bcrypt';
import jwt from 'jsonwebtoken';
import { prisma } from '@bloxos/database';

const JWT_SECRET = process.env.JWT_SECRET || 'bloxos-dev-secret-change-in-production';
const JWT_EXPIRES_IN = '7d';
const SALT_ROUNDS = 10;

export interface JWTPayload {
  userId: string;
  email: string;
  role: string;
}

export class AuthService {
  // Hash password
  async hashPassword(password: string): Promise<string> {
    return bcrypt.hash(password, SALT_ROUNDS);
  }

  // Verify password
  async verifyPassword(password: string, hash: string): Promise<boolean> {
    return bcrypt.compare(password, hash);
  }

  // Generate JWT token
  generateToken(payload: JWTPayload): string {
    return jwt.sign(payload, JWT_SECRET, { expiresIn: JWT_EXPIRES_IN });
  }

  // Verify JWT token
  verifyToken(token: string): JWTPayload | null {
    try {
      return jwt.verify(token, JWT_SECRET) as JWTPayload;
    } catch {
      return null;
    }
  }

  // Register new user
  async register(email: string, password: string, name?: string) {
    // Check if user exists
    const existing = await prisma.user.findUnique({ where: { email } });
    if (existing) {
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
        email,
        password: hashedPassword,
        name,
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

    // Generate token
    const token = this.generateToken({
      userId: user.id,
      email: user.email,
      role: user.role,
    });

    return { user, token };
  }

  // Login user
  async login(email: string, password: string) {
    // Find user
    const user = await prisma.user.findUnique({ where: { email } });
    if (!user) {
      throw new Error('Invalid email or password');
    }

    // Verify password
    const valid = await this.verifyPassword(password, user.password);
    if (!valid) {
      throw new Error('Invalid email or password');
    }

    // Generate token
    const token = this.generateToken({
      userId: user.id,
      email: user.email,
      role: user.role,
    });

    return {
      user: {
        id: user.id,
        email: user.email,
        name: user.name,
        role: user.role,
      },
      token,
    };
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

  // Change password
  async changePassword(userId: string, currentPassword: string, newPassword: string) {
    const user = await prisma.user.findUnique({ where: { id: userId } });
    if (!user) {
      throw new Error('User not found');
    }

    // Verify current password
    const valid = await this.verifyPassword(currentPassword, user.password);
    if (!valid) {
      throw new Error('Current password is incorrect');
    }

    // Hash new password
    const hashedPassword = await this.hashPassword(newPassword);

    // Update password
    await prisma.user.update({
      where: { id: userId },
      data: { password: hashedPassword },
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
