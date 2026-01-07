import { FastifyRequest, FastifyReply } from 'fastify';
import crypto from 'node:crypto';
import { auditLog } from '../utils/security.ts';

/**
 * CSRF Protection Middleware
 * 
 * Uses double-submit cookie pattern:
 * 1. Server sets a CSRF token in a cookie
 * 2. Client must include the same token in a header (X-CSRF-Token)
 * 3. Server verifies both match
 * 
 * Combined with SameSite=strict cookies, this provides robust CSRF protection.
 */

const CSRF_COOKIE_NAME = 'csrf_token';
const CSRF_HEADER_NAME = 'x-csrf-token';
const TOKEN_LENGTH = 32;

// Methods that require CSRF protection (state-changing)
const PROTECTED_METHODS = ['POST', 'PUT', 'PATCH', 'DELETE'];

// Paths that are exempt from CSRF (API keys, agent routes)
const CSRF_EXEMPT_PATHS = [
  '/api/agent/',      // Agent uses token auth
  '/api/auth/login',  // Login creates session
  '/api/auth/register', // Registration
  '/api/auth/refresh', // Token refresh
  '/api/health',      // Health check
];

/**
 * Generate a cryptographically secure CSRF token
 */
export function generateCSRFToken(): string {
  return crypto.randomBytes(TOKEN_LENGTH).toString('hex');
}

/**
 * CSRF token generation hook - sets cookie on responses
 */
export async function csrfSetToken(request: FastifyRequest, reply: FastifyReply) {
  // Skip OPTIONS requests (CORS preflight)
  if (request.method === 'OPTIONS') {
    return;
  }

  // Only set token if not already present
  const existingToken = request.cookies?.[CSRF_COOKIE_NAME];
  
  if (!existingToken) {
    const token = generateCSRFToken();
    
    reply.setCookie(CSRF_COOKIE_NAME, token, {
      httpOnly: false, // Must be readable by JS to include in header
      secure: process.env.NODE_ENV === 'production',
      sameSite: 'strict',
      path: '/',
      maxAge: 24 * 60 * 60, // 24 hours
    });
  }
}

/**
 * CSRF validation hook - validates token on state-changing requests
 */
export async function csrfValidate(request: FastifyRequest, reply: FastifyReply) {
  const method = request.method;
  const path = request.url.split('?')[0];

  // Skip OPTIONS requests (CORS preflight)
  if (method === 'OPTIONS') {
    return;
  }

  // Skip non-protected methods
  if (!PROTECTED_METHODS.includes(method)) {
    return;
  }

  // Skip exempt paths
  if (CSRF_EXEMPT_PATHS.some(exempt => path.startsWith(exempt))) {
    return;
  }

  // Skip if request has API key header (machine-to-machine)
  if (request.headers['x-api-key']) {
    return;
  }

  // Get token from cookie and header
  const cookieToken = request.cookies[CSRF_COOKIE_NAME];
  const headerToken = request.headers[CSRF_HEADER_NAME] as string | undefined;

  // Validate tokens exist
  if (!cookieToken || !headerToken) {
    auditLog({
      userId: request.user?.userId,
      action: 'csrf_validation_failed',
      resource: 'security',
      details: { 
        path, 
        method,
        reason: !cookieToken ? 'missing_cookie' : 'missing_header',
      },
      ip: request.ip,
      success: false,
      error: 'CSRF token missing',
    });
    
    return reply.status(403).send({ 
      error: 'CSRF validation failed',
      message: 'Missing CSRF token. Include the csrf_token cookie value in the X-CSRF-Token header.',
    });
  }

  // Validate tokens match (timing-safe comparison)
  const tokensMatch = crypto.timingSafeEqual(
    Buffer.from(cookieToken),
    Buffer.from(headerToken)
  );

  if (!tokensMatch) {
    auditLog({
      userId: request.user?.userId,
      action: 'csrf_validation_failed',
      resource: 'security',
      details: { path, method, reason: 'token_mismatch' },
      ip: request.ip,
      success: false,
      error: 'CSRF token mismatch',
    });
    
    return reply.status(403).send({ 
      error: 'CSRF validation failed',
      message: 'Invalid CSRF token.',
    });
  }
}

/**
 * Endpoint to get a fresh CSRF token
 * Useful for SPAs that need to initialize CSRF protection
 */
export async function csrfTokenEndpoint(request: FastifyRequest, reply: FastifyReply) {
  const token = generateCSRFToken();
  
  reply.setCookie(CSRF_COOKIE_NAME, token, {
    httpOnly: false,
    secure: process.env.NODE_ENV === 'production',
    sameSite: 'strict',
    path: '/',
    maxAge: 24 * 60 * 60,
  });

  return reply.send({ 
    token,
    message: 'Include this token in the X-CSRF-Token header for state-changing requests.',
  });
}
