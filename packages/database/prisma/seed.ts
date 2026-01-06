import { PrismaClient } from '@prisma/client';
import { hash } from 'bcrypt';

const prisma = new PrismaClient();

async function main() {
  console.log('Seeding database...');

  // Create default admin user
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

  console.log('Created admin user:', adminUser.email);

  // Create default farm
  const defaultFarm = await prisma.farm.upsert({
    where: { id: 'default-farm' },
    update: {},
    create: {
      id: 'default-farm',
      name: 'My Farm',
      description: 'Default mining farm',
      ownerId: adminUser.id,
    },
  });

  console.log('Created default farm:', defaultFarm.name);

  // Create some common miner software entries
  const miners = [
    { name: 'T-Rex', version: '0.26.8', algo: 'ethash', supportedGpus: ['NVIDIA'], apiPort: 4067, apiType: 'http' },
    { name: 'T-Rex', version: '0.26.8', algo: 'kawpow', supportedGpus: ['NVIDIA'], apiPort: 4067, apiType: 'http' },
    { name: 'T-Rex', version: '0.26.8', algo: 'autolykos2', supportedGpus: ['NVIDIA'], apiPort: 4067, apiType: 'http' },
    { name: 'TeamRedMiner', version: '0.10.21', algo: 'ethash', supportedGpus: ['AMD'], apiPort: 4028, apiType: 'tcp' },
    { name: 'TeamRedMiner', version: '0.10.21', algo: 'kawpow', supportedGpus: ['AMD'], apiPort: 4028, apiType: 'tcp' },
    { name: 'lolMiner', version: '1.88', algo: 'ethash', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4068, apiType: 'http' },
    { name: 'lolMiner', version: '1.88', algo: 'autolykos2', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4068, apiType: 'http' },
    { name: 'XMRig', version: '6.21.0', algo: 'randomx', supportedGpus: ['NVIDIA', 'AMD', 'INTEL'], apiPort: 8080, apiType: 'http' },
    { name: 'SRBMiner', version: '2.6.1', algo: 'autolykos2', supportedGpus: ['AMD'], apiPort: 21550, apiType: 'http' },
    { name: 'Rigel', version: '1.18.1', algo: 'ethash', supportedGpus: ['NVIDIA'], apiPort: 4067, apiType: 'http' },
    { name: 'Rigel', version: '1.18.1', algo: 'nexapow', supportedGpus: ['NVIDIA'], apiPort: 4067, apiType: 'http' },
    { name: 'BzMiner', version: '21.4.0', algo: 'ethash', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4014, apiType: 'http' },
    { name: 'BzMiner', version: '21.4.0', algo: 'kawpow', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4014, apiType: 'http' },
    { name: 'Nanominer', version: '3.9.1', algo: 'ethash', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 9090, apiType: 'http' },
    { name: 'CCMiner', version: '3.8.3', algo: 'verus', supportedGpus: ['NVIDIA'], apiPort: 4068, apiType: 'http' },
    { name: 'OneZeroMiner', version: '1.3.0', algo: 'dynex', supportedGpus: ['NVIDIA'], apiPort: 4067, apiType: 'http' },
  ];

  for (const miner of miners) {
    await prisma.minerSoftware.upsert({
      where: {
        name_version_algo: {
          name: miner.name,
          version: miner.version,
          algo: miner.algo,
        },
      },
      update: {},
      create: {
        name: miner.name,
        version: miner.version,
        algo: miner.algo,
        supportedGpus: miner.supportedGpus as any,
        apiPort: miner.apiPort,
        apiType: miner.apiType,
      },
    });
  }

  console.log('Created miner software entries:', miners.length);

  console.log('Seeding complete!');
}

main()
  .catch((e) => {
    console.error(e);
    process.exit(1);
  })
  .finally(async () => {
    await prisma.$disconnect();
  });
