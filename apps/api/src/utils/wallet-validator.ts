import { prisma } from '@bloxos/database';

interface ValidationResult {
  valid: boolean;
  error?: string;
}

/**
 * Validates a wallet address for a specific coin
 * Uses regex patterns stored in the database
 */
export async function validateWalletAddress(
  ticker: string,
  address: string
): Promise<ValidationResult> {
  if (!address || address.trim() === '') {
    return { valid: false, error: 'Address is required' };
  }

  const coin = await prisma.coin.findUnique({
    where: { ticker: ticker.toUpperCase() },
    select: { addressRegex: true, name: true },
  });

  if (!coin) {
    return { valid: false, error: `Unknown coin: ${ticker}` };
  }

  if (!coin.addressRegex) {
    // No regex defined, accept any address
    return { valid: true };
  }

  try {
    const regex = new RegExp(coin.addressRegex);
    if (regex.test(address.trim())) {
      return { valid: true };
    }
    return {
      valid: false,
      error: `Invalid ${coin.name} address format`,
    };
  } catch {
    // Invalid regex in database, log and accept
    console.error(`Invalid regex for ${ticker}: ${coin.addressRegex}`);
    return { valid: true };
  }
}

/**
 * Get validation pattern for a coin (for client-side validation)
 */
export async function getWalletPattern(ticker: string): Promise<string | null> {
  const coin = await prisma.coin.findUnique({
    where: { ticker: ticker.toUpperCase() },
    select: { addressRegex: true },
  });

  return coin?.addressRegex ?? null;
}

/**
 * Batch validate multiple wallet addresses
 */
export async function validateWalletAddresses(
  wallets: { ticker: string; address: string }[]
): Promise<Map<string, ValidationResult>> {
  const results = new Map<string, ValidationResult>();

  for (const wallet of wallets) {
    const result = await validateWalletAddress(wallet.ticker, wallet.address);
    results.set(`${wallet.ticker}:${wallet.address}`, result);
  }

  return results;
}
