import crypto from 'node:crypto';

// ============================================
// SECURITY CONFIGURATION
// ============================================

// Require all secrets to be set in production
export function validateSecrets(): void {
  const requiredSecrets = [
    'JWT_SECRET',
    'ENCRYPTION_KEY',
    'COOKIE_SECRET',
  ];

  const isProduction = process.env.NODE_ENV === 'production';
  const missing: string[] = [];

  for (const secret of requiredSecrets) {
    if (!process.env[secret]) {
      if (isProduction) {
        missing.push(secret);
      } else {
        console.warn(`[Security] Warning: ${secret} not set, using insecure default for development`);
      }
    }
  }

  if (missing.length > 0) {
    throw new Error(`Missing required secrets in production: ${missing.join(', ')}`);
  }
}

// ============================================
// COMMAND INJECTION PREVENTION
// ============================================

// Whitelist of allowed commands for SSH execution
const ALLOWED_COMMANDS = new Set([
  // System info commands
  'hostname',
  'uname',
  'uptime',
  'whoami',
  'date',
  'cat /etc/os-release',
  'cat /proc/cpuinfo',
  'cat /proc/meminfo',
  'free',
  'df',
  'lsblk',
  'lscpu',
  'nproc',
  'ip',
  'hostname -I',
  
  // GPU commands
  'nvidia-smi',
  'rocm-smi',
  'lspci',
  
  // Process commands
  'ps',
  'top',
  'pgrep',
  'pkill',
  
  // Miner control (restricted)
  't-rex',
  'lolminer',
  'gminer',
  'nbminer',
  'teamredminer',
  'xmrig',
  'bzminer',
]);

// Characters that should never appear in commands
const DANGEROUS_CHARS = /[;&|`$(){}[\]<>!\\'"]/g;

// Validate and sanitize a command
export function sanitizeCommand(command: string): string {
  // Remove leading/trailing whitespace
  command = command.trim();
  
  // Check for empty command
  if (!command) {
    throw new Error('Empty command');
  }
  
  // Check for dangerous characters
  if (DANGEROUS_CHARS.test(command)) {
    throw new Error('Command contains disallowed characters');
  }
  
  // Check for command chaining attempts
  if (command.includes('&&') || command.includes('||') || command.includes(';')) {
    throw new Error('Command chaining is not allowed');
  }
  
  // Check for redirection
  if (command.includes('>') || command.includes('<') || command.includes('|')) {
    throw new Error('Redirection and pipes are not allowed');
  }
  
  return command;
}

// Check if a command is in the whitelist (for strict mode)
export function isCommandAllowed(command: string): boolean {
  const baseCommand = command.split(' ')[0].split('/').pop() || '';
  
  // Check if base command or full command is allowed
  return ALLOWED_COMMANDS.has(baseCommand) || 
         Array.from(ALLOWED_COMMANDS).some(allowed => command.startsWith(allowed));
}

// Escape shell arguments safely
export function escapeShellArg(arg: string): string {
  // Replace single quotes with escaped version
  return `'${arg.replace(/'/g, "'\\''")}'`;
}

// Validate numeric value is within safe range
export function validateNumericRange(value: number, min: number, max: number, name: string): void {
  if (typeof value !== 'number' || isNaN(value)) {
    throw new Error(`${name} must be a valid number`);
  }
  if (value < min || value > max) {
    throw new Error(`${name} must be between ${min} and ${max}`);
  }
}

// ============================================
// MINER COMMAND BUILDING (SAFE)
// ============================================

// Validate miner name
const VALID_MINERS = ['t-rex', 'lolminer', 'gminer', 'nbminer', 'teamredminer', 'xmrig', 'bzminer'];

export function validateMinerName(name: string): void {
  if (!VALID_MINERS.includes(name.toLowerCase())) {
    throw new Error(`Invalid miner: ${name}`);
  }
}

// Validate pool URL format
export function validatePoolUrl(url: string): void {
  // Basic URL pattern for mining pools
  const poolPattern = /^(stratum\+tcp|stratum\+ssl|stratum2\+tcp|stratum2\+ssl|http|https):\/\/[a-zA-Z0-9.-]+(:\d+)?(\/.*)?$/;
  if (!poolPattern.test(url)) {
    throw new Error('Invalid pool URL format');
  }
}

// Validate wallet address (basic alphanumeric check)
export function validateWalletAddress(address: string): void {
  // Allow alphanumeric, dots, and underscores (common in wallet addresses)
  const walletPattern = /^[a-zA-Z0-9._-]+$/;
  if (!walletPattern.test(address) || address.length < 10 || address.length > 150) {
    throw new Error('Invalid wallet address format');
  }
}

// Validate extra arguments (very strict)
export function validateExtraArgs(args: string): string {
  if (!args) return '';
  
  // Only allow alphanumeric, hyphens, equals, dots, and spaces
  const safePattern = /^[a-zA-Z0-9\-=.\s]+$/;
  if (!safePattern.test(args)) {
    throw new Error('Extra arguments contain invalid characters');
  }
  
  return args.trim();
}

// ============================================
// OVERCLOCK VALIDATION
// ============================================

// Safe ranges for OC settings
export const OC_LIMITS = {
  NVIDIA: {
    powerLimit: { min: 50, max: 500 },      // Watts
    coreOffset: { min: -500, max: 500 },    // MHz
    memOffset: { min: -2000, max: 3000 },   // MHz
    coreLock: { min: 200, max: 3000 },      // MHz
    memLock: { min: 400, max: 12000 },      // MHz
    fanSpeed: { min: 0, max: 100 },         // Percentage
  },
  AMD: {
    powerLimit: { min: 50, max: 400 },
    coreOffset: { min: -500, max: 500 },
    memOffset: { min: -500, max: 500 },
    coreLock: { min: 200, max: 2500 },
    memLock: { min: 400, max: 2500 },
    fanSpeed: { min: 0, max: 100 },
  },
};

export function validateOCValue(vendor: string, setting: string, value: number | null): void {
  if (value === null) return;
  
  const limits = vendor === 'NVIDIA' ? OC_LIMITS.NVIDIA : OC_LIMITS.AMD;
  const range = limits[setting as keyof typeof limits];
  
  if (!range) {
    throw new Error(`Unknown OC setting: ${setting}`);
  }
  
  validateNumericRange(value, range.min, range.max, setting);
}

// ============================================
// INPUT SANITIZATION
// ============================================

// Sanitize string for database storage (prevent XSS when displayed)
export function sanitizeString(input: string, maxLength = 255): string {
  if (typeof input !== 'string') {
    throw new Error('Input must be a string');
  }
  
  return input
    .trim()
    .slice(0, maxLength)
    .replace(/[<>]/g, ''); // Remove potential HTML tags
}

// Validate email format strictly (returns boolean for use in conditionals)
export function validateEmail(email: string): boolean {
  const emailPattern = /^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$/;
  return emailPattern.test(email) && email.length <= 254;
}

// Validate IP address format (returns boolean)
export function validateIPAddress(ip: string): boolean {
  const ipv4Pattern = /^(\d{1,3}\.){3}\d{1,3}$/;
  const ipv6Pattern = /^([0-9a-fA-F]{0,4}:){2,7}[0-9a-fA-F]{0,4}$/;
  
  if (!ipv4Pattern.test(ip) && !ipv6Pattern.test(ip)) {
    return false;
  }
  
  // Additional IPv4 validation
  if (ipv4Pattern.test(ip)) {
    const parts = ip.split('.').map(Number);
    if (parts.some(p => p < 0 || p > 255)) {
      return false;
    }
  }
  
  return true;
}

// Validate hostname format (returns boolean)
export function validateHostname(hostname: string): boolean {
  const hostnamePattern = /^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)*$/;
  return hostnamePattern.test(hostname) && hostname.length <= 253;
}

// ============================================
// PASSWORD VALIDATION
// ============================================

export interface PasswordRequirements {
  minLength: number;
  requireUppercase: boolean;
  requireLowercase: boolean;
  requireNumbers: boolean;
  requireSpecial: boolean;
}

export const PASSWORD_REQUIREMENTS: PasswordRequirements = {
  minLength: 12,
  requireUppercase: true,
  requireLowercase: true,
  requireNumbers: true,
  requireSpecial: true,
};

export function validatePassword(password: string): { valid: boolean; errors: string[] } {
  const errors: string[] = [];
  
  if (password.length < PASSWORD_REQUIREMENTS.minLength) {
    errors.push(`Password must be at least ${PASSWORD_REQUIREMENTS.minLength} characters`);
  }
  
  if (PASSWORD_REQUIREMENTS.requireUppercase && !/[A-Z]/.test(password)) {
    errors.push('Password must contain at least one uppercase letter');
  }
  
  if (PASSWORD_REQUIREMENTS.requireLowercase && !/[a-z]/.test(password)) {
    errors.push('Password must contain at least one lowercase letter');
  }
  
  if (PASSWORD_REQUIREMENTS.requireNumbers && !/\d/.test(password)) {
    errors.push('Password must contain at least one number');
  }
  
  if (PASSWORD_REQUIREMENTS.requireSpecial && !/[!@#$%^&*()_+\-=[\]{};:'",.<>?/\\|`~]/.test(password)) {
    errors.push('Password must contain at least one special character');
  }
  
  return { valid: errors.length === 0, errors };
}

// ============================================
// RATE LIMITING HELPERS
// ============================================

// In-memory rate limiter (for single instance, use Redis for distributed)
const rateLimitStore = new Map<string, { count: number; resetTime: number }>();

export function checkRateLimit(
  key: string, 
  maxRequests: number, 
  windowMs: number
): { allowed: boolean; remaining: number; resetIn: number } {
  const now = Date.now();
  const record = rateLimitStore.get(key);
  
  if (!record || now > record.resetTime) {
    rateLimitStore.set(key, { count: 1, resetTime: now + windowMs });
    return { allowed: true, remaining: maxRequests - 1, resetIn: windowMs };
  }
  
  if (record.count >= maxRequests) {
    return { allowed: false, remaining: 0, resetIn: record.resetTime - now };
  }
  
  record.count++;
  return { allowed: true, remaining: maxRequests - record.count, resetIn: record.resetTime - now };
}

// Clean up old rate limit entries periodically
setInterval(() => {
  const now = Date.now();
  for (const [key, record] of rateLimitStore.entries()) {
    if (now > record.resetTime) {
      rateLimitStore.delete(key);
    }
  }
}, 60000); // Clean up every minute

// ============================================
// ACCOUNT LOCKOUT
// ============================================

// Account lockout configuration
const LOCKOUT_CONFIG = {
  maxAttempts: 5,           // Number of failed attempts before lockout
  lockoutDuration: 15 * 60 * 1000, // 15 minutes lockout
  attemptWindow: 15 * 60 * 1000,   // Window to count attempts (15 minutes)
};

// In-memory store for failed login attempts (use Redis in production)
const failedAttempts = new Map<string, { count: number; firstAttempt: number; lockedUntil?: number }>();

/**
 * Record a failed login attempt for an account
 * @param identifier - Email or user ID
 * @returns Object indicating if account is now locked
 */
export function recordFailedLogin(identifier: string): { locked: boolean; attemptsRemaining: number; lockoutEndsAt?: Date } {
  const now = Date.now();
  const key = `login:${identifier.toLowerCase()}`;
  const record = failedAttempts.get(key);
  
  // Check if currently locked
  if (record?.lockedUntil && now < record.lockedUntil) {
    return {
      locked: true,
      attemptsRemaining: 0,
      lockoutEndsAt: new Date(record.lockedUntil),
    };
  }
  
  // If no record or window expired, start fresh
  if (!record || now > record.firstAttempt + LOCKOUT_CONFIG.attemptWindow) {
    failedAttempts.set(key, { count: 1, firstAttempt: now });
    return {
      locked: false,
      attemptsRemaining: LOCKOUT_CONFIG.maxAttempts - 1,
    };
  }
  
  // Increment count
  record.count++;
  
  // Check if should lock
  if (record.count >= LOCKOUT_CONFIG.maxAttempts) {
    record.lockedUntil = now + LOCKOUT_CONFIG.lockoutDuration;
    return {
      locked: true,
      attemptsRemaining: 0,
      lockoutEndsAt: new Date(record.lockedUntil),
    };
  }
  
  return {
    locked: false,
    attemptsRemaining: LOCKOUT_CONFIG.maxAttempts - record.count,
  };
}

/**
 * Check if an account is currently locked
 */
export function isAccountLocked(identifier: string): { locked: boolean; lockoutEndsAt?: Date } {
  const now = Date.now();
  const key = `login:${identifier.toLowerCase()}`;
  const record = failedAttempts.get(key);
  
  if (!record?.lockedUntil) {
    return { locked: false };
  }
  
  if (now >= record.lockedUntil) {
    // Lockout expired, clear it
    failedAttempts.delete(key);
    return { locked: false };
  }
  
  return {
    locked: true,
    lockoutEndsAt: new Date(record.lockedUntil),
  };
}

/**
 * Clear failed login attempts after successful login
 */
export function clearFailedLogins(identifier: string): void {
  const key = `login:${identifier.toLowerCase()}`;
  failedAttempts.delete(key);
}

/**
 * Manually unlock an account (admin function)
 */
export function unlockAccount(identifier: string): boolean {
  const key = `login:${identifier.toLowerCase()}`;
  return failedAttempts.delete(key);
}

// Clean up expired lockouts periodically
setInterval(() => {
  const now = Date.now();
  for (const [key, record] of failedAttempts.entries()) {
    // Remove if no lockout and window expired
    if (!record.lockedUntil && now > record.firstAttempt + LOCKOUT_CONFIG.attemptWindow) {
      failedAttempts.delete(key);
    }
    // Remove if lockout expired
    if (record.lockedUntil && now >= record.lockedUntil) {
      failedAttempts.delete(key);
    }
  }
}, 60000); // Clean up every minute

// ============================================
// AUDIT LOGGING
// ============================================

export interface AuditLogEntry {
  timestamp: Date;
  userId?: string;
  action: string;
  resource: string;
  resourceId?: string;
  details?: Record<string, unknown>;
  ip?: string;
  userAgent?: string;
  success: boolean;
  error?: string;
}

// In production, this should write to a persistent store
const auditLogs: AuditLogEntry[] = [];

// Audit log function - accepts either string action or full entry object
export function auditLog(
  actionOrEntry: string | Omit<AuditLogEntry, 'timestamp'>, 
  details?: Record<string, unknown>
): void {
  let logEntry: AuditLogEntry;
  
  if (typeof actionOrEntry === 'string') {
    // Simple format: auditLog('ACTION', { details })
    logEntry = {
      timestamp: new Date(),
      action: actionOrEntry,
      resource: 'system',
      details,
      success: true,
    };
  } else {
    // Full format: auditLog({ action, resource, ... })
    logEntry = {
      ...actionOrEntry,
      timestamp: new Date(),
    };
  }
  
  // Log to console in development
  if (process.env.NODE_ENV !== 'production') {
    console.log('[AUDIT]', JSON.stringify(logEntry));
  }
  
  // Store in memory (in production, write to database or external service)
  auditLogs.push(logEntry);
  
  // Keep only last 10000 entries in memory
  if (auditLogs.length > 10000) {
    auditLogs.shift();
  }
}

export function getAuditLogs(limit = 100): AuditLogEntry[] {
  return auditLogs.slice(-limit).reverse();
}

// ============================================
// TOKEN GENERATION
// ============================================

// Generate cryptographically secure token
export function generateSecureToken(length = 64): string {
  return crypto.randomBytes(length).toString('base64url');
}

// Generate API key
export function generateApiKey(): string {
  return `blx_${generateSecureToken(32)}`;
}

// ===========================================
// OUTPUT SANITIZATION (XSS Prevention)
// ===========================================

/**
 * Sanitize output for WebSocket/HTML to prevent XSS
 * Escapes HTML special characters
 */
export function sanitizeOutput(input: unknown): unknown {
  if (typeof input === 'string') {
    return input
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#x27;')
      .replace(/\//g, '&#x2F;');
  }
  
  if (Array.isArray(input)) {
    return input.map(sanitizeOutput);
  }
  
  if (input !== null && typeof input === 'object') {
    const sanitized: Record<string, unknown> = {};
    for (const [key, value] of Object.entries(input)) {
      // Don't sanitize certain fields that are used internally
      if (['id', 'createdAt', 'updatedAt', 'timestamp'].includes(key)) {
        sanitized[key] = value;
      } else {
        sanitized[key] = sanitizeOutput(value);
      }
    }
    return sanitized;
  }
  
  return input;
}

/**
 * Create a safe JSON message for WebSocket
 * Sanitizes all string values to prevent XSS
 */
export function createSafeWSMessage(type: string, data?: unknown): string {
  const message: Record<string, unknown> = {
    type,
  };
  if (data !== undefined) {
    message.data = sanitizeOutput(data);
  }
  return JSON.stringify(message);
}

/**
 * Validate WebSocket message structure
 */
export function validateWSMessage(rawMessage: string): { valid: boolean; message?: Record<string, unknown>; error?: string } {
  try {
    const message = JSON.parse(rawMessage);
    
    if (typeof message !== 'object' || message === null) {
      return { valid: false, error: 'Message must be an object' };
    }
    
    if (!message.type || typeof message.type !== 'string') {
      return { valid: false, error: 'Message must have a type field' };
    }
    
    // Validate type is alphanumeric/underscore only
    if (!/^[a-zA-Z_][a-zA-Z0-9_]*$/.test(message.type)) {
      return { valid: false, error: 'Invalid message type format' };
    }
    
    return { valid: true, message };
  } catch {
    return { valid: false, error: 'Invalid JSON' };
  }
}
