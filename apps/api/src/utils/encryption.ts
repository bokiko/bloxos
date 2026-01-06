import crypto from 'node:crypto';

const ALGORITHM = 'aes-256-gcm';
const IV_LENGTH = 16;
const AUTH_TAG_LENGTH = 16;
const KEY_LENGTH = 32;
const ITERATIONS = 100000; // PBKDF2 iterations

// Get encryption key from environment with proper key derivation
function getEncryptionKey(): Buffer {
  const key = process.env.ENCRYPTION_KEY;
  const isProduction = process.env.NODE_ENV === 'production';
  
  if (!key) {
    if (isProduction) {
      throw new Error('ENCRYPTION_KEY environment variable is required in production');
    }
    // Development fallback - NOT SECURE FOR PRODUCTION
    console.warn('[Security] Warning: Using insecure default encryption key for development');
    return crypto.pbkdf2Sync(
      'bloxos-dev-key-change-in-production',
      'bloxos-dev-salt',
      ITERATIONS,
      KEY_LENGTH,
      'sha512'
    );
  }
  
  // Use PBKDF2 for proper key derivation
  // In production, the ENCRYPTION_KEY should be a strong, randomly generated string
  return crypto.pbkdf2Sync(
    key,
    'bloxos-encryption-salt-v1', // Static salt is OK here since key should be random
    ITERATIONS,
    KEY_LENGTH,
    'sha512'
  );
}

// Cached key to avoid repeated derivation
let cachedKey: Buffer | null = null;

function getKey(): Buffer {
  if (!cachedKey) {
    cachedKey = getEncryptionKey();
  }
  return cachedKey;
}

// Clear cached key (useful for testing or key rotation)
export function clearKeyCache(): void {
  cachedKey = null;
}

export function encrypt(text: string): string {
  const key = getKey();
  const iv = crypto.randomBytes(IV_LENGTH);
  const cipher = crypto.createCipheriv(ALGORITHM, key, iv);
  
  let encrypted = cipher.update(text, 'utf8', 'hex');
  encrypted += cipher.final('hex');
  
  const authTag = cipher.getAuthTag();
  
  // Return iv:authTag:encryptedData
  return `${iv.toString('hex')}:${authTag.toString('hex')}:${encrypted}`;
}

export function decrypt(encryptedText: string): string {
  const key = getKey();
  const parts = encryptedText.split(':');
  
  if (parts.length !== 3) {
    throw new Error('Invalid encrypted text format');
  }
  
  const iv = Buffer.from(parts[0], 'hex');
  const authTag = Buffer.from(parts[1], 'hex');
  const encrypted = parts[2];
  
  // Validate lengths
  if (iv.length !== IV_LENGTH) {
    throw new Error('Invalid IV length');
  }
  if (authTag.length !== AUTH_TAG_LENGTH) {
    throw new Error('Invalid auth tag length');
  }
  
  const decipher = crypto.createDecipheriv(ALGORITHM, key, iv);
  decipher.setAuthTag(authTag);
  
  let decrypted = decipher.update(encrypted, 'hex', 'utf8');
  decrypted += decipher.final('utf8');
  
  return decrypted;
}

// Hash sensitive data for logging (one-way)
export function hashForLogging(text: string): string {
  return crypto.createHash('sha256').update(text).digest('hex').slice(0, 16);
}

// Generate a random encryption key (for setup)
export function generateEncryptionKey(): string {
  return crypto.randomBytes(32).toString('base64');
}
