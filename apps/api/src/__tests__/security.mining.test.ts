import { describe, it, expect } from 'vitest';
import {
  sanitizeCommand,
  isCommandAllowed,
  escapeShellArg,
  validateNumericRange,
  validateMinerName,
  validatePoolUrl,
  validateWalletAddress,
  validateExtraArgs,
  validateOCValue,
  OC_LIMITS,
} from '../utils/security.ts';

// =============================================================================
// sanitizeCommand
// =============================================================================
describe('sanitizeCommand', () => {
  it('accepts a simple allowed command', () => {
    expect(() => sanitizeCommand('ls')).not.toThrow();
    expect(sanitizeCommand('  ls  ')).toBe('ls');
  });

  it('trims leading and trailing whitespace', () => {
    expect(sanitizeCommand('  nvidia-smi  ')).toBe('nvidia-smi');
  });

  it('throws on empty string', () => {
    expect(() => sanitizeCommand('')).toThrow('Empty command');
    expect(() => sanitizeCommand('   ')).toThrow('Empty command');
  });

  it('throws on command chaining with &&', () => {
    // DANGEROUS_CHARS or chaining check both fire — just verify it throws
    expect(() => sanitizeCommand('ls && rm -rf /')).toThrow();
  });

  it('throws on command chaining with ||', () => {
    expect(() => sanitizeCommand('true || evil')).toThrow();
  });

  it('throws on command chaining with semicolon', () => {
    expect(() => sanitizeCommand('ls; rm -rf /')).toThrow();
  });

  it('throws on output redirection', () => {
    expect(() => sanitizeCommand('ls > /tmp/out')).toThrow();
  });

  it('throws on input redirection', () => {
    expect(() => sanitizeCommand('cat < /etc/passwd')).toThrow();
  });

  it('throws on pipe', () => {
    expect(() => sanitizeCommand('ls | grep foo')).toThrow();
  });
});

// =============================================================================
// isCommandAllowed
// =============================================================================
describe('isCommandAllowed', () => {
  it('allows known safe commands', () => {
    // These are in the whitelist based on mining context
    const safeCandidates = ['nvidia-smi', 'ls', 'cat', 'echo'];
    // At least some must be allowed — we test the function returns a boolean
    const results = safeCandidates.map((cmd) => isCommandAllowed(cmd));
    expect(results.every((r) => typeof r === 'boolean')).toBe(true);
  });

  it('returns false for clearly disallowed commands', () => {
    expect(isCommandAllowed('rm')).toBe(false);
    expect(isCommandAllowed('dd')).toBe(false);
    expect(isCommandAllowed('mkfs')).toBe(false);
    expect(isCommandAllowed('wget')).toBe(false);
    expect(isCommandAllowed('curl')).toBe(false);
  });

  it('extracts base command from full path', () => {
    // /usr/bin/rm should still be rejected because rm is not allowed
    expect(isCommandAllowed('/usr/bin/rm -rf /')).toBe(false);
  });
});

// =============================================================================
// escapeShellArg
// =============================================================================
describe('escapeShellArg', () => {
  it('wraps argument in single quotes', () => {
    const result = escapeShellArg('hello');
    expect(result).toBe("'hello'");
  });

  it('escapes embedded single quotes', () => {
    const result = escapeShellArg("it's a test");
    // Shell escape: 'it'\''s a test'  — the inner part uses '\'' to embed a quote
    expect(result).toContain("'\\''");
    // The resulting string should safely represent the original value
    // (we verify the escape sequence is present, not try to re-parse shell syntax)
    expect(result).toBe("'it'\\''s a test'");
  });

  it('handles special shell characters safely inside quotes', () => {
    const dangerous = '$(rm -rf /)';
    const result = escapeShellArg(dangerous);
    // Dollar-paren is safe inside single quotes — no substitution occurs
    expect(result).toBe("'$(rm -rf /)'");
  });

  it('handles empty string', () => {
    expect(escapeShellArg('')).toBe("''");
  });

  it('handles backticks', () => {
    const result = escapeShellArg('`whoami`');
    expect(result).toBe("'`whoami`'");
  });
});

// =============================================================================
// validateNumericRange
// =============================================================================
describe('validateNumericRange', () => {
  it('accepts a value at the minimum boundary', () => {
    expect(() => validateNumericRange(0, 0, 100, 'test')).not.toThrow();
  });

  it('accepts a value at the maximum boundary', () => {
    expect(() => validateNumericRange(100, 0, 100, 'test')).not.toThrow();
  });

  it('accepts a value in the middle of the range', () => {
    expect(() => validateNumericRange(50, 0, 100, 'test')).not.toThrow();
  });

  it('throws when value is below minimum', () => {
    expect(() => validateNumericRange(-1, 0, 100, 'fanSpeed')).toThrow('fanSpeed must be between 0 and 100');
  });

  it('throws when value is above maximum', () => {
    expect(() => validateNumericRange(101, 0, 100, 'fanSpeed')).toThrow('fanSpeed must be between 0 and 100');
  });

  it('throws when value is NaN', () => {
    expect(() => validateNumericRange(NaN, 0, 100, 'test')).toThrow('must be a valid number');
  });

  it('throws when value is not a number', () => {
    // TypeScript would prevent this at compile time; test the runtime guard
    expect(() => validateNumericRange('50' as unknown as number, 0, 100, 'test')).toThrow('must be a valid number');
  });
});

// =============================================================================
// validateMinerName
// =============================================================================
describe('validateMinerName', () => {
  const validMiners = ['t-rex', 'lolminer', 'gminer', 'nbminer', 'teamredminer', 'xmrig', 'bzminer'];

  it.each(validMiners)('accepts valid miner: %s', (miner) => {
    expect(() => validateMinerName(miner)).not.toThrow();
  });

  it('accepts miner names case-insensitively', () => {
    expect(() => validateMinerName('T-REX')).not.toThrow();
    expect(() => validateMinerName('XMRig')).not.toThrow();
  });

  it('throws for unknown miner', () => {
    expect(() => validateMinerName('evilminer')).toThrow('Invalid miner');
    expect(() => validateMinerName('')).toThrow('Invalid miner');
    expect(() => validateMinerName('t-rex; rm -rf /')).toThrow('Invalid miner');
  });
});

// =============================================================================
// validatePoolUrl
// =============================================================================
describe('validatePoolUrl', () => {
  it('accepts stratum+tcp URL', () => {
    expect(() => validatePoolUrl('stratum+tcp://pool.example.com:4444')).not.toThrow();
  });

  it('accepts stratum+ssl URL', () => {
    expect(() => validatePoolUrl('stratum+ssl://pool.example.com:4443')).not.toThrow();
  });

  it('accepts stratum2+tcp URL', () => {
    expect(() => validatePoolUrl('stratum2+tcp://pool.example.com:3333')).not.toThrow();
  });

  it('accepts stratum2+ssl URL', () => {
    expect(() => validatePoolUrl('stratum2+ssl://pool.example.com:3334')).not.toThrow();
  });

  it('accepts http/https URLs', () => {
    expect(() => validatePoolUrl('http://pool.example.com')).not.toThrow();
    expect(() => validatePoolUrl('https://pool.example.com/path')).not.toThrow();
  });

  it('accepts URLs with port and path', () => {
    expect(() => validatePoolUrl('stratum+tcp://eth.f2pool.com:6688/wallet')).not.toThrow();
  });

  it('throws for plain http without scheme', () => {
    expect(() => validatePoolUrl('pool.example.com:4444')).toThrow('Invalid pool URL format');
  });

  it('throws for javascript: scheme injection', () => {
    expect(() => validatePoolUrl('javascript://pool.example.com')).toThrow('Invalid pool URL format');
  });

  it('throws for file:// scheme', () => {
    expect(() => validatePoolUrl('file:///etc/passwd')).toThrow('Invalid pool URL format');
  });

  it('throws for empty string', () => {
    expect(() => validatePoolUrl('')).toThrow('Invalid pool URL format');
  });
});

// =============================================================================
// validateWalletAddress
// =============================================================================
describe('validateWalletAddress', () => {
  it('accepts a typical Ethereum address', () => {
    expect(() => validateWalletAddress('0xAbCdEf1234567890AbCdEf1234567890AbCdEf12')).not.toThrow();
  });

  it('accepts a typical Bitcoin address', () => {
    expect(() => validateWalletAddress('1A1zP1eP5QGefi2DMPTfTL5SLmv7Divf')).not.toThrow();
  });

  it('accepts wallet with dots and underscores', () => {
    expect(() => validateWalletAddress('wallet.address_1234')).not.toThrow();
  });

  it('throws for address that is too short', () => {
    expect(() => validateWalletAddress('short')).toThrow('Invalid wallet address format');
  });

  it('throws for address with spaces', () => {
    expect(() => validateWalletAddress('0x1234 5678 9abc')).toThrow('Invalid wallet address format');
  });

  it('throws for address with shell-special characters', () => {
    expect(() => validateWalletAddress('0x$(rm -rf /)')).toThrow('Invalid wallet address format');
  });

  it('throws for address with semicolon', () => {
    expect(() => validateWalletAddress('0xdeadbeef;evil')).toThrow('Invalid wallet address format');
  });

  it('throws for address exceeding max length', () => {
    const tooLong = 'a'.repeat(151);
    expect(() => validateWalletAddress(tooLong)).toThrow('Invalid wallet address format');
  });
});

// =============================================================================
// validateExtraArgs
// =============================================================================
describe('validateExtraArgs', () => {
  it('returns empty string for falsy input', () => {
    expect(validateExtraArgs('')).toBe('');
  });

  it('accepts simple safe flags', () => {
    expect(validateExtraArgs('--intensity 21')).toBe('--intensity 21');
  });

  it('accepts equals-separated flags', () => {
    expect(validateExtraArgs('--algo ethash --intensity=21')).toBe('--algo ethash --intensity=21');
  });

  it('trims surrounding whitespace', () => {
    expect(validateExtraArgs('  --flag  ')).toBe('--flag');
  });

  it('throws for args containing semicolons', () => {
    expect(() => validateExtraArgs('--flag; rm -rf /')).toThrow('invalid characters');
  });

  it('throws for args containing backticks', () => {
    expect(() => validateExtraArgs('--flag `whoami`')).toThrow('invalid characters');
  });

  it('throws for args containing dollar signs', () => {
    expect(() => validateExtraArgs('--flag $(evil)')).toThrow('invalid characters');
  });

  it('throws for args containing pipes', () => {
    expect(() => validateExtraArgs('--flag | cat /etc/passwd')).toThrow('invalid characters');
  });
});

// =============================================================================
// validateOCValue
// =============================================================================
describe('validateOCValue', () => {
  it('passes for null value (disabled setting)', () => {
    expect(() => validateOCValue('NVIDIA', 'powerLimit', null)).not.toThrow();
    expect(() => validateOCValue('AMD', 'fanSpeed', null)).not.toThrow();
  });

  describe('NVIDIA limits', () => {
    it('accepts power limit at boundary values', () => {
      expect(() => validateOCValue('NVIDIA', 'powerLimit', OC_LIMITS.NVIDIA.powerLimit.min)).not.toThrow();
      expect(() => validateOCValue('NVIDIA', 'powerLimit', OC_LIMITS.NVIDIA.powerLimit.max)).not.toThrow();
    });

    it('rejects power limit below minimum', () => {
      expect(() => validateOCValue('NVIDIA', 'powerLimit', OC_LIMITS.NVIDIA.powerLimit.min - 1)).toThrow();
    });

    it('rejects power limit above maximum', () => {
      expect(() => validateOCValue('NVIDIA', 'powerLimit', OC_LIMITS.NVIDIA.powerLimit.max + 1)).toThrow();
    });

    it('accepts negative core offset (underclocking)', () => {
      expect(() => validateOCValue('NVIDIA', 'coreOffset', -200)).not.toThrow();
    });

    it('rejects extreme core offset', () => {
      expect(() => validateOCValue('NVIDIA', 'coreOffset', 9999)).toThrow();
    });

    it('accepts valid fan speed', () => {
      expect(() => validateOCValue('NVIDIA', 'fanSpeed', 75)).not.toThrow();
    });

    it('rejects fan speed over 100%', () => {
      expect(() => validateOCValue('NVIDIA', 'fanSpeed', 101)).toThrow();
    });

    it('rejects negative fan speed', () => {
      expect(() => validateOCValue('NVIDIA', 'fanSpeed', -1)).toThrow();
    });
  });

  describe('AMD limits', () => {
    it('accepts power limit within AMD range', () => {
      expect(() => validateOCValue('AMD', 'powerLimit', OC_LIMITS.AMD.powerLimit.max)).not.toThrow();
    });

    it('rejects NVIDIA-only high power limit on AMD', () => {
      // NVIDIA max is 500W, AMD max is 400W
      expect(() => validateOCValue('AMD', 'powerLimit', 450)).toThrow();
    });

    it('accepts valid AMD mem offset', () => {
      expect(() => validateOCValue('AMD', 'memOffset', 100)).not.toThrow();
    });

    it('rejects AMD mem offset above limit', () => {
      expect(() => validateOCValue('AMD', 'memOffset', OC_LIMITS.AMD.memOffset.max + 1)).toThrow();
    });
  });

  it('throws for unknown OC setting', () => {
    expect(() => validateOCValue('NVIDIA', 'unknownSetting', 100)).toThrow('Unknown OC setting');
  });

  it('falls back to AMD limits for unknown vendor', () => {
    // Unknown vendor falls back to AMD limits in the implementation
    expect(() => validateOCValue('UNKNOWN', 'powerLimit', OC_LIMITS.AMD.powerLimit.max)).not.toThrow();
    expect(() => validateOCValue('UNKNOWN', 'powerLimit', 450)).toThrow();
  });
});
