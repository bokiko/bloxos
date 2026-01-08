import { PrismaClient, CoinType, PoolRegion, HardwareType } from '@prisma/client';
import { hash } from 'bcrypt';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

const prisma = new PrismaClient();

// Type definitions for JSON data
interface CoinData {
  ticker: string;
  name: string;
  algorithm: string;
  type: 'GPU' | 'CPU';
  logoPath: string;
  coingeckoId: string;
  addressRegex: string;
}

interface PoolData {
  name: string;
  region: 'US' | 'EU' | 'ASIA' | 'GLOBAL';
  host: string;
  port: number;
  sslPort?: number;
  fee: number;
  minPayout?: number;
  website?: string;
}

interface MinerData {
  name: string;
  displayName: string;
  version: string;
  algorithms: string[];
  supportsNvidia: boolean;
  supportsAmd: boolean;
  supportsCpu: boolean;
  githubRepo: string;
  binaryName: string;
  linuxAssetPattern: string;
  apiPort: number;
  apiType: string;
  defaultArgs: string;
}

interface TemplateData {
  name: string;
  minerName: string;
  poolName: string;
  gpuType: 'NVIDIA' | 'AMD' | 'CPU';
  extraArgs: string;
  recommended?: boolean;
}

function loadJson<T>(filename: string): T {
  const path = join(__dirname, '..', 'seed', filename);
  const data = readFileSync(path, 'utf-8');
  return JSON.parse(data);
}

async function seedCoins(): Promise<Map<string, string>> {
  console.log('Seeding coins...');
  const { coins } = loadJson<{ coins: CoinData[] }>('coins.json');
  const coinMap = new Map<string, string>();

  for (const coin of coins) {
    const created = await prisma.coin.upsert({
      where: { ticker: coin.ticker },
      update: {
        name: coin.name,
        algorithm: coin.algorithm,
        type: coin.type as CoinType,
        logoPath: coin.logoPath,
        coingeckoId: coin.coingeckoId,
        addressRegex: coin.addressRegex,
      },
      create: {
        ticker: coin.ticker,
        name: coin.name,
        algorithm: coin.algorithm,
        type: coin.type as CoinType,
        logoPath: coin.logoPath,
        coingeckoId: coin.coingeckoId,
        addressRegex: coin.addressRegex,
        enabled: true,
      },
    });
    coinMap.set(coin.ticker, created.id);
  }

  console.log(`  Created ${coins.length} coins`);
  return coinMap;
}

async function seedPools(coinMap: Map<string, string>): Promise<Map<string, string>> {
  console.log('Seeding pool presets...');
  const { pools: poolsData } = loadJson<{ pools: { coin: string; pools: PoolData[] }[] }>('pools.json');
  const poolMap = new Map<string, string>();
  let totalPools = 0;

  for (const coinPools of poolsData) {
    const coinId = coinMap.get(coinPools.coin);
    if (!coinId) {
      console.warn(`  Warning: Coin ${coinPools.coin} not found, skipping pools`);
      continue;
    }

    for (const pool of coinPools.pools) {
      const poolKey = `${coinPools.coin}-${pool.name}-${pool.region}`;
      const created = await prisma.poolPreset.upsert({
        where: {
          coinId_name_region: {
            coinId,
            name: pool.name,
            region: pool.region as PoolRegion,
          },
        },
        update: {
          host: pool.host,
          port: pool.port,
          sslPort: pool.sslPort,
          fee: pool.fee,
          minPayout: pool.minPayout,
          website: pool.website,
        },
        create: {
          coinId,
          name: pool.name,
          region: pool.region as PoolRegion,
          host: pool.host,
          port: pool.port,
          sslPort: pool.sslPort,
          fee: pool.fee,
          minPayout: pool.minPayout,
          website: pool.website,
        },
      });
      poolMap.set(poolKey, created.id);
      totalPools++;
    }
  }

  console.log(`  Created ${totalPools} pool presets`);
  return poolMap;
}

async function seedMiners(): Promise<void> {
  console.log('Seeding miner software...');
  const { miners } = loadJson<{ miners: MinerData[] }>('miners.json');

  for (const miner of miners) {
    // Map hardware support to GPUVendor enum
    const supportedGpus: ('NVIDIA' | 'AMD' | 'INTEL')[] = [];
    if (miner.supportsNvidia) supportedGpus.push('NVIDIA');
    if (miner.supportsAmd) supportedGpus.push('AMD');

    await prisma.minerSoftware.upsert({
      where: { name: miner.name },
      update: {
        displayName: miner.displayName,
        version: miner.version,
        algorithms: miner.algorithms,
        supportsNvidia: miner.supportsNvidia,
        supportsAmd: miner.supportsAmd,
        supportsCpu: miner.supportsCpu,
        githubRepo: miner.githubRepo,
        binaryName: miner.binaryName,
        linuxAssetPattern: miner.linuxAssetPattern,
        apiPort: miner.apiPort,
        apiType: miner.apiType,
        defaultArgs: miner.defaultArgs,
        supportedGpus,
      },
      create: {
        name: miner.name,
        displayName: miner.displayName,
        version: miner.version,
        algorithms: miner.algorithms,
        supportsNvidia: miner.supportsNvidia,
        supportsAmd: miner.supportsAmd,
        supportsCpu: miner.supportsCpu,
        githubRepo: miner.githubRepo,
        binaryName: miner.binaryName,
        linuxAssetPattern: miner.linuxAssetPattern,
        apiPort: miner.apiPort,
        apiType: miner.apiType,
        defaultArgs: miner.defaultArgs,
        supportedGpus,
      },
    });
  }

  console.log(`  Created ${miners.length} miners`);
}

async function seedTemplates(coinMap: Map<string, string>, poolMap: Map<string, string>): Promise<void> {
  console.log('Seeding flight sheet templates...');
  const { templates: templatesData } = loadJson<{ templates: { coin: string; templates: TemplateData[] }[] }>('templates.json');
  let totalTemplates = 0;

  for (const coinTemplates of templatesData) {
    const coinId = coinMap.get(coinTemplates.coin);
    if (!coinId) {
      console.warn(`  Warning: Coin ${coinTemplates.coin} not found, skipping templates`);
      continue;
    }

    // Delete existing templates for this coin (to handle updates cleanly)
    await prisma.flightSheetTemplate.deleteMany({
      where: { coinId },
    });

    for (const template of coinTemplates.templates) {
      // Find matching pool (first region available)
      let poolPresetId: string | undefined;
      for (const region of ['EU', 'US', 'GLOBAL', 'ASIA']) {
        const poolKey = `${coinTemplates.coin}-${template.poolName}-${region}`;
        if (poolMap.has(poolKey)) {
          poolPresetId = poolMap.get(poolKey);
          break;
        }
      }

      await prisma.flightSheetTemplate.create({
        data: {
          name: template.name,
          coinId,
          poolPresetId,
          minerName: template.minerName,
          gpuType: template.gpuType as HardwareType,
          extraArgs: template.extraArgs,
          recommended: template.recommended ?? false,
        },
      });
      totalTemplates++;
    }
  }

  console.log(`  Created ${totalTemplates} flight sheet templates`);
}

async function seedAdminUser(): Promise<string> {
  console.log('Seeding admin user...');
  const hashedPassword = await hash('admin123', 10);

  const adminUser = await prisma.user.upsert({
    where: { email: 'admin@blox.local' },
    update: {},
    create: {
      email: 'admin@blox.local',
      name: 'Admin',
      password: hashedPassword,
      role: 'ADMIN',
    },
  });

  console.log(`  Created admin user: ${adminUser.email}`);
  return adminUser.id;
}

async function seedDefaultFarm(userId: string): Promise<void> {
  console.log('Seeding default farm...');
  const defaultFarm = await prisma.farm.upsert({
    where: { id: 'default-farm' },
    update: {},
    create: {
      id: 'default-farm',
      name: 'My Farm',
      description: 'Default mining farm',
      ownerId: userId,
    },
  });

  console.log(`  Created default farm: ${defaultFarm.name}`);
}

async function main() {
  console.log('='.repeat(50));
  console.log('BloxOS Database Seeding');
  console.log('='.repeat(50));

  // Seed system data
  const coinMap = await seedCoins();
  const poolMap = await seedPools(coinMap);
  await seedMiners();
  await seedTemplates(coinMap, poolMap);

  // Seed user data
  const userId = await seedAdminUser();
  await seedDefaultFarm(userId);

  console.log('='.repeat(50));
  console.log('Seeding complete!');
  console.log('='.repeat(50));
}

main()
  .catch((e) => {
    console.error('Seeding failed:', e);
    process.exit(1);
  })
  .finally(async () => {
    await prisma.$disconnect();
  });
