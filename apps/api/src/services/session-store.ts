/**
 * Session Store - Manages token blacklisting and session data
 * 
 * In development: Uses in-memory storage
 * In production: Should use Redis for distributed session management
 * 
 * This abstraction allows easy switching between storage backends.
 */

import crypto from 'node:crypto';

// Session data structure
export interface SessionData {
  userId: string;
  email: string;
  role: string;
  createdAt: number;
  expiresAt: number;
  ip?: string;
  userAgent?: string;
}

// In-memory store (for development)
const tokenBlacklist = new Map<string, number>(); // token hash -> expiry timestamp
const activeSessions = new Map<string, SessionData>(); // session id -> session data

// Configuration
const SESSION_CLEANUP_INTERVAL = 5 * 60 * 1000; // 5 minutes
const MAX_BLACKLIST_SIZE = 50000;
const MAX_SESSIONS_SIZE = 10000;

/**
 * Hash a token for storage (we don't store raw tokens)
 */
function hashToken(token: string): string {
  return crypto.createHash('sha256').update(token).digest('hex');
}

/**
 * Add a token to the blacklist
 * @param token - The JWT token to blacklist
 * @param expiresInMs - How long the token was valid for (we only need to blacklist until it naturally expires)
 */
export async function blacklistToken(token: string, expiresInMs: number = 4 * 60 * 60 * 1000): Promise<void> {
  const hash = hashToken(token);
  const expiresAt = Date.now() + expiresInMs;
  
  tokenBlacklist.set(hash, expiresAt);

  // Cleanup if too large
  if (tokenBlacklist.size > MAX_BLACKLIST_SIZE) {
    cleanupBlacklist();
  }
}

/**
 * Check if a token is blacklisted
 */
export async function isTokenBlacklisted(token: string): Promise<boolean> {
  const hash = hashToken(token);
  const expiresAt = tokenBlacklist.get(hash);
  
  if (!expiresAt) {
    return false;
  }

  // Check if still valid in blacklist
  if (Date.now() > expiresAt) {
    tokenBlacklist.delete(hash);
    return false;
  }

  return true;
}

/**
 * Create a new session
 */
export async function createSession(data: Omit<SessionData, 'createdAt'>): Promise<string> {
  const sessionId = crypto.randomBytes(32).toString('hex');
  
  activeSessions.set(sessionId, {
    ...data,
    createdAt: Date.now(),
  });

  // Cleanup if too large
  if (activeSessions.size > MAX_SESSIONS_SIZE) {
    cleanupSessions();
  }

  return sessionId;
}

/**
 * Get session data
 */
export async function getSession(sessionId: string): Promise<SessionData | null> {
  const session = activeSessions.get(sessionId);
  
  if (!session) {
    return null;
  }

  // Check if expired
  if (Date.now() > session.expiresAt) {
    activeSessions.delete(sessionId);
    return null;
  }

  return session;
}

/**
 * Delete a session (logout)
 */
export async function deleteSession(sessionId: string): Promise<void> {
  activeSessions.delete(sessionId);
}

/**
 * Delete all sessions for a user (force logout everywhere)
 */
export async function deleteUserSessions(userId: string): Promise<number> {
  let count = 0;
  
  for (const [id, session] of activeSessions.entries()) {
    if (session.userId === userId) {
      activeSessions.delete(id);
      count++;
    }
  }

  return count;
}

/**
 * Get all active sessions for a user
 */
export async function getUserSessions(userId: string): Promise<Array<{ id: string; data: SessionData }>> {
  const sessions: Array<{ id: string; data: SessionData }> = [];
  
  for (const [id, session] of activeSessions.entries()) {
    if (session.userId === userId && Date.now() <= session.expiresAt) {
      sessions.push({ id, data: session });
    }
  }

  return sessions;
}

/**
 * Cleanup expired blacklist entries
 */
function cleanupBlacklist(): void {
  const now = Date.now();
  
  for (const [hash, expiresAt] of tokenBlacklist.entries()) {
    if (now > expiresAt) {
      tokenBlacklist.delete(hash);
    }
  }
}

/**
 * Cleanup expired sessions
 */
function cleanupSessions(): void {
  const now = Date.now();
  
  for (const [id, session] of activeSessions.entries()) {
    if (now > session.expiresAt) {
      activeSessions.delete(id);
    }
  }
}

/**
 * Get stats about the session store
 */
export function getSessionStoreStats(): {
  blacklistSize: number;
  activeSessionsCount: number;
} {
  return {
    blacklistSize: tokenBlacklist.size,
    activeSessionsCount: activeSessions.size,
  };
}

// Periodic cleanup
setInterval(() => {
  cleanupBlacklist();
  cleanupSessions();
}, SESSION_CLEANUP_INTERVAL);

// Export for testing
export const __test__ = {
  clearAll: () => {
    tokenBlacklist.clear();
    activeSessions.clear();
  },
};
