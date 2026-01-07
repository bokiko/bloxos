import { describe, it, expect } from 'vitest';
import {
  sanitizeOutput,
  validateWSMessage,
  createSafeWSMessage,
  validatePassword,
  validateEmail,
  checkRateLimit,
  recordFailedLogin,
  isAccountLocked,
  generateSecureToken,
  generateApiKey,
  sanitizeString,
  validateHostname,
  validateIPAddress,
} from '../utils/security.ts';

describe('Security Utils', () => {
  describe('sanitizeOutput', () => {
    it('should escape HTML special characters in strings', () => {
      const input = '<script>alert("xss")</script>';
      const result = sanitizeOutput(input);
      expect(result).toBe('&lt;script&gt;alert(&quot;xss&quot;)&lt;&#x2F;script&gt;');
    });

    it('should sanitize strings in objects', () => {
      const input = { name: '<b>test</b>' };
      const result = sanitizeOutput(input) as typeof input;
      expect(result.name).toBe('&lt;b&gt;test&lt;&#x2F;b&gt;');
    });

    it('should handle arrays', () => {
      const input = ['<script>', 'normal'];
      const result = sanitizeOutput(input) as string[];
      expect(result[0]).toBe('&lt;script&gt;');
      expect(result[1]).toBe('normal');
    });

    it('should handle nested objects', () => {
      const input = {
        user: {
          name: '<script>evil</script>',
        },
      };
      const result = sanitizeOutput(input) as typeof input;
      expect(result.user.name).toContain('&lt;script&gt;');
    });

    it('should handle null and undefined', () => {
      expect(sanitizeOutput(null)).toBeNull();
      expect(sanitizeOutput(undefined)).toBeUndefined();
    });

    it('should handle primitive types', () => {
      expect(sanitizeOutput(123)).toBe(123);
      expect(sanitizeOutput(true)).toBe(true);
    });

    it('should preserve special fields like id and timestamp', () => {
      const input = { id: '123', timestamp: '2026-01-01' };
      const result = sanitizeOutput(input) as typeof input;
      expect(result.id).toBe('123');
      expect(result.timestamp).toBe('2026-01-01');
    });
  });

  describe('validateWSMessage', () => {
    it('should validate valid JSON messages', () => {
      const result = validateWSMessage('{"type":"ping"}');
      expect(result.valid).toBe(true);
      expect(result.message).toEqual({ type: 'ping' });
    });

    it('should reject invalid JSON', () => {
      const result = validateWSMessage('not json');
      expect(result.valid).toBe(false);
      expect(result.error).toBeDefined();
    });

    it('should reject non-object messages', () => {
      const result = validateWSMessage('"just a string"');
      expect(result.valid).toBe(false);
      expect(result.error).toBe('Message must be an object');
    });

    it('should reject messages without type field', () => {
      const result = validateWSMessage('{"data": "test"}');
      expect(result.valid).toBe(false);
      expect(result.error).toContain('type');
    });
  });

  describe('createSafeWSMessage', () => {
    it('should create valid JSON message', () => {
      const message = createSafeWSMessage('test', { foo: 'bar' });
      const parsed = JSON.parse(message);
      expect(parsed.type).toBe('test');
      expect(parsed.data.foo).toBe('bar');
    });

    it('should sanitize data in message', () => {
      const message = createSafeWSMessage('test', { html: '<script>' });
      const parsed = JSON.parse(message);
      expect(parsed.data.html).toBe('&lt;script&gt;');
    });

    it('should work without data', () => {
      const message = createSafeWSMessage('ping');
      const parsed = JSON.parse(message);
      expect(parsed.type).toBe('ping');
      expect(parsed.data).toBeUndefined();
    });
  });

  describe('validatePassword', () => {
    it('should accept strong passwords', () => {
      const result = validatePassword('SecurePass123!@#');
      expect(result.valid).toBe(true);
      expect(result.errors).toHaveLength(0);
    });

    it('should reject short passwords', () => {
      const result = validatePassword('Short1!');
      expect(result.valid).toBe(false);
      expect(result.errors.join(' ')).toContain('12 characters');
    });

    it('should reject passwords without uppercase', () => {
      const result = validatePassword('lowercase123!@#');
      expect(result.valid).toBe(false);
      expect(result.errors.join(' ')).toContain('uppercase');
    });

    it('should reject passwords without lowercase', () => {
      const result = validatePassword('UPPERCASE123!@#');
      expect(result.valid).toBe(false);
      expect(result.errors.join(' ')).toContain('lowercase');
    });

    it('should reject passwords without numbers', () => {
      const result = validatePassword('NoNumbersHere!@#');
      expect(result.valid).toBe(false);
      expect(result.errors.join(' ')).toContain('number');
    });

    it('should reject passwords without special characters', () => {
      const result = validatePassword('NoSpecial12345Ab');
      expect(result.valid).toBe(false);
      expect(result.errors.join(' ')).toContain('special');
    });
  });

  describe('validateEmail', () => {
    it('should accept valid emails', () => {
      expect(validateEmail('user@example.com')).toBe(true);
      expect(validateEmail('test.user@domain.co.uk')).toBe(true);
      expect(validateEmail('user+tag@example.org')).toBe(true);
    });

    it('should reject invalid emails', () => {
      expect(validateEmail('notanemail')).toBe(false);
      expect(validateEmail('missing@domain')).toBe(false);
      expect(validateEmail('@nodomain.com')).toBe(false);
    });
  });

  describe('validateHostname', () => {
    it('should accept valid hostnames', () => {
      expect(validateHostname('example.com')).toBe(true);
      expect(validateHostname('sub.example.com')).toBe(true);
      expect(validateHostname('rig-01')).toBe(true);
    });

    it('should reject invalid hostnames', () => {
      expect(validateHostname('-invalid')).toBe(false);
      expect(validateHostname('has spaces')).toBe(false);
    });
  });

  describe('validateIPAddress', () => {
    it('should accept valid IPv4 addresses', () => {
      expect(validateIPAddress('192.168.1.1')).toBe(true);
      expect(validateIPAddress('10.0.0.1')).toBe(true);
      expect(validateIPAddress('0.0.0.0')).toBe(true);
    });

    it('should reject invalid IPv4 addresses', () => {
      expect(validateIPAddress('256.256.256.256')).toBe(false);
      expect(validateIPAddress('not.an.ip')).toBe(false);
    });
  });

  describe('Rate Limiting', () => {
    it('should allow requests under limit', () => {
      const key = `test-${Date.now()}-${Math.random()}`;
      const result = checkRateLimit(key, 10, 60000);
      expect(result.allowed).toBe(true);
      expect(result.remaining).toBe(9);
    });

    it('should block requests over limit', () => {
      const key = `test-block-${Date.now()}-${Math.random()}`;
      // Exhaust the limit
      for (let i = 0; i < 5; i++) {
        checkRateLimit(key, 5, 60000);
      }
      const result = checkRateLimit(key, 5, 60000);
      expect(result.allowed).toBe(false);
      expect(result.remaining).toBe(0);
    });
  });

  describe('Account Lockout', () => {
    it('should lock account after max failed attempts', () => {
      const key = `lockout-test-${Date.now()}-${Math.random()}`;
      
      // Record 5 failed attempts
      for (let i = 0; i < 5; i++) {
        recordFailedLogin(key);
      }
      
      const lockStatus = isAccountLocked(key);
      expect(lockStatus.locked).toBe(true);
    });

    it('should not lock account under max attempts', () => {
      const key = `no-lockout-${Date.now()}-${Math.random()}`;
      
      // Record 4 failed attempts (under threshold)
      for (let i = 0; i < 4; i++) {
        recordFailedLogin(key);
      }
      
      const lockStatus = isAccountLocked(key);
      expect(lockStatus.locked).toBe(false);
    });

    it('should return remaining attempts', () => {
      const key = `attempts-${Date.now()}-${Math.random()}`;
      const result = recordFailedLogin(key);
      expect(result.attemptsRemaining).toBe(4); // 5 max - 1 attempt
    });
  });

  describe('Token Generation', () => {
    it('should generate secure tokens of specified length', () => {
      const token = generateSecureToken(32);
      expect(token).toBeDefined();
      expect(token.length).toBeGreaterThan(0);
    });

    it('should generate unique tokens', () => {
      const token1 = generateSecureToken();
      const token2 = generateSecureToken();
      expect(token1).not.toBe(token2);
    });

    it('should generate API keys with prefix', () => {
      const apiKey = generateApiKey();
      expect(apiKey.startsWith('blx_')).toBe(true);
    });
  });

  describe('sanitizeString', () => {
    it('should trim and limit string length', () => {
      const input = '  test  ';
      expect(sanitizeString(input)).toBe('test');
    });

    it('should remove HTML tags', () => {
      const input = '<script>alert(1)</script>test';
      const result = sanitizeString(input);
      expect(result).not.toContain('<');
      expect(result).not.toContain('>');
    });

    it('should respect max length', () => {
      const input = 'a'.repeat(300);
      const result = sanitizeString(input, 100);
      expect(result.length).toBe(100);
    });
  });
});
