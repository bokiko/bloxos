import { describe, it, expect } from 'vitest';
import { encrypt, decrypt } from '../utils/encryption.ts';

describe('Encryption Utils', () => {
  describe('encrypt/decrypt', () => {
    it('should encrypt and decrypt text correctly', () => {
      const plaintext = 'Hello, World!';
      const encrypted = encrypt(plaintext);
      const decrypted = decrypt(encrypted);
      expect(decrypted).toBe(plaintext);
    });

    it('should produce different ciphertext for same plaintext', () => {
      const plaintext = 'Test message';
      const encrypted1 = encrypt(plaintext);
      const encrypted2 = encrypt(plaintext);
      expect(encrypted1).not.toBe(encrypted2);
    });

    it('should handle special characters', () => {
      const plaintext = '!@#$%^&*()_+-=[]{}|;:,.<>?`~';
      const encrypted = encrypt(plaintext);
      const decrypted = decrypt(encrypted);
      expect(decrypted).toBe(plaintext);
    });

    it('should handle unicode characters', () => {
      const plaintext = '你好世界 🌍 مرحبا';
      const encrypted = encrypt(plaintext);
      const decrypted = decrypt(encrypted);
      expect(decrypted).toBe(plaintext);
    });

    it('should handle long strings', () => {
      const plaintext = 'a'.repeat(10000);
      const encrypted = encrypt(plaintext);
      const decrypted = decrypt(encrypted);
      expect(decrypted).toBe(plaintext);
    });

    it('should handle empty string', () => {
      const plaintext = '';
      const encrypted = encrypt(plaintext);
      const decrypted = decrypt(encrypted);
      expect(decrypted).toBe(plaintext);
    });

    it('should throw on invalid ciphertext', () => {
      expect(() => decrypt('invalid')).toThrow();
    });

    it('should throw on tampered ciphertext', () => {
      const plaintext = 'Test';
      const encrypted = encrypt(plaintext);
      // Tamper with the ciphertext
      const tampered = encrypted.slice(0, -5) + 'XXXXX';
      expect(() => decrypt(tampered)).toThrow();
    });
  });
});
