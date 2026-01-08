import { PrismaClient } from '@prisma/client';

const prisma = new PrismaClient();

const defaultMiners = [
  // NVIDIA miners
  { name: 'T-Rex', version: '0.26.8', algo: 'ethash', supportedGpus: ['NVIDIA'], apiPort: 4067, apiType: 'http', defaultArgs: '--no-watchdog' },
  { name: 'T-Rex', version: '0.26.8', algo: 'kawpow', supportedGpus: ['NVIDIA'], apiPort: 4067, apiType: 'http', defaultArgs: '--no-watchdog' },
  { name: 'T-Rex', version: '0.26.8', algo: 'autolykos2', supportedGpus: ['NVIDIA'], apiPort: 4067, apiType: 'http', defaultArgs: '--no-watchdog' },
  { name: 'lolMiner', version: '1.76', algo: 'ethash', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4068, apiType: 'http', defaultArgs: '' },
  { name: 'lolMiner', version: '1.76', algo: 'etchash', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4068, apiType: 'http', defaultArgs: '' },
  { name: 'lolMiner', version: '1.76', algo: 'autolykos2', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4068, apiType: 'http', defaultArgs: '' },
  { name: 'Gminer', version: '3.44', algo: 'ethash', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4069, apiType: 'http', defaultArgs: '' },
  { name: 'Gminer', version: '3.44', algo: 'kawpow', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4069, apiType: 'http', defaultArgs: '' },
  { name: 'NBMiner', version: '42.3', algo: 'ethash', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4070, apiType: 'http', defaultArgs: '' },
  { name: 'NBMiner', version: '42.3', algo: 'kawpow', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4070, apiType: 'http', defaultArgs: '' },
  // AMD miners
  { name: 'TeamRedMiner', version: '0.10.14', algo: 'ethash', supportedGpus: ['AMD'], apiPort: 4071, apiType: 'http', defaultArgs: '' },
  { name: 'TeamRedMiner', version: '0.10.14', algo: 'kawpow', supportedGpus: ['AMD'], apiPort: 4071, apiType: 'http', defaultArgs: '' },
  { name: 'TeamRedMiner', version: '0.10.14', algo: 'autolykos2', supportedGpus: ['AMD'], apiPort: 4071, apiType: 'http', defaultArgs: '' },
  // CPU miners
  { name: 'XMRig', version: '6.21.0', algo: 'randomx', supportedGpus: ['NVIDIA', 'AMD', 'INTEL'], apiPort: 4072, apiType: 'http', defaultArgs: '' },
  { name: 'BloxMiner', version: '1.0.0', algo: 'verushash', supportedGpus: ['NVIDIA', 'AMD', 'INTEL'], apiPort: 4074, apiType: 'http', installUrl: 'https://raw.githubusercontent.com/bokiko/bloxminer/master/install.sh', defaultArgs: '' },
  // KAS miners
  { name: 'lolMiner', version: '1.76', algo: 'kaspa', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4068, apiType: 'http', defaultArgs: '' },
  { name: 'BzMiner', version: '19.3.0', algo: 'kaspa', supportedGpus: ['NVIDIA', 'AMD'], apiPort: 4073, apiType: 'http', defaultArgs: '' },
];

async function seedMiners() {
  let created = 0;
  let skipped = 0;

  for (const miner of defaultMiners) {
    try {
      await prisma.minerSoftware.create({ data: miner as any });
      created++;
      console.log(`Created: ${miner.name} ${miner.version} (${miner.algo})`);
    } catch {
      skipped++;
      console.log(`Skipped: ${miner.name} ${miner.version} (${miner.algo}) - already exists`);
    }
  }

  console.log(`\nSeeded ${created} miners, ${skipped} already existed`);
  await prisma.$disconnect();
}

seedMiners().catch(console.error);
