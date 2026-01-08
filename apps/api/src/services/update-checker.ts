import { prisma } from '@bloxos/database';

interface GitHubRelease {
  tag_name: string;
  name: string;
  published_at: string;
  html_url: string;
  assets: Array<{
    name: string;
    browser_download_url: string;
    size: number;
  }>;
}

interface MinerUpdate {
  minerName: string;
  displayName: string;
  currentVersion: string;
  latestVersion: string;
  releaseUrl: string;
  publishedAt: string;
  hasUpdate: boolean;
}

// Cache for rate limiting GitHub API calls
const versionCache = new Map<string, { version: string; checkedAt: Date; releaseUrl: string }>();
const CACHE_TTL_MS = 6 * 60 * 60 * 1000; // 6 hours

/**
 * Fetches the latest release version from GitHub
 */
async function getLatestGitHubRelease(repo: string): Promise<GitHubRelease | null> {
  try {
    const response = await fetch(`https://api.github.com/repos/${repo}/releases/latest`, {
      headers: {
        'Accept': 'application/vnd.github.v3+json',
        'User-Agent': 'BloxOS-UpdateChecker',
      },
    });

    if (!response.ok) {
      if (response.status === 404) {
        console.warn(`No releases found for ${repo}`);
        return null;
      }
      if (response.status === 403) {
        console.warn(`GitHub API rate limit reached for ${repo}`);
        return null;
      }
      throw new Error(`GitHub API error: ${response.status}`);
    }

    return await response.json() as GitHubRelease;
  } catch (error) {
    console.error(`Failed to fetch release for ${repo}:`, error);
    return null;
  }
}

/**
 * Normalizes version strings for comparison
 * Removes 'v' prefix and handles various formats
 */
function normalizeVersion(version: string): string {
  return version.replace(/^v/i, '').trim();
}

/**
 * Compares two version strings
 * Returns: -1 if a < b, 0 if a == b, 1 if a > b
 */
function compareVersions(a: string, b: string): number {
  const partsA = normalizeVersion(a).split(/[.-]/).map(p => parseInt(p, 10) || 0);
  const partsB = normalizeVersion(b).split(/[.-]/).map(p => parseInt(p, 10) || 0);
  
  const maxLen = Math.max(partsA.length, partsB.length);
  
  for (let i = 0; i < maxLen; i++) {
    const numA = partsA[i] || 0;
    const numB = partsB[i] || 0;
    
    if (numA < numB) return -1;
    if (numA > numB) return 1;
  }
  
  return 0;
}

/**
 * Checks for updates for a single miner
 */
async function checkMinerUpdate(miner: {
  name: string;
  displayName: string | null;
  version: string;
  githubRepo: string | null;
}): Promise<MinerUpdate | null> {
  if (!miner.githubRepo) {
    return null;
  }

  // Check cache first
  const cached = versionCache.get(miner.githubRepo);
  if (cached && (Date.now() - cached.checkedAt.getTime()) < CACHE_TTL_MS) {
    const hasUpdate = compareVersions(cached.version, miner.version) > 0;
    return {
      minerName: miner.name,
      displayName: miner.displayName || miner.name,
      currentVersion: miner.version,
      latestVersion: cached.version,
      releaseUrl: cached.releaseUrl,
      publishedAt: cached.checkedAt.toISOString(),
      hasUpdate,
    };
  }

  // Fetch from GitHub
  const release = await getLatestGitHubRelease(miner.githubRepo);
  if (!release) {
    return null;
  }

  const latestVersion = normalizeVersion(release.tag_name);
  
  // Update cache
  versionCache.set(miner.githubRepo, {
    version: latestVersion,
    checkedAt: new Date(),
    releaseUrl: release.html_url,
  });

  const hasUpdate = compareVersions(latestVersion, miner.version) > 0;

  return {
    minerName: miner.name,
    displayName: miner.displayName || miner.name,
    currentVersion: miner.version,
    latestVersion,
    releaseUrl: release.html_url,
    publishedAt: release.published_at,
    hasUpdate,
  };
}

/**
 * Checks for updates for all miners with GitHub repos
 */
export async function checkAllMinerUpdates(): Promise<MinerUpdate[]> {
  const miners = await prisma.minerSoftware.findMany({
    where: {
      githubRepo: { not: null },
    },
    select: {
      name: true,
      displayName: true,
      version: true,
      githubRepo: true,
    },
  });

  const updates: MinerUpdate[] = [];

  for (const miner of miners) {
    // Add small delay between requests to avoid rate limiting
    await new Promise(resolve => setTimeout(resolve, 100));
    
    const update = await checkMinerUpdate(miner);
    if (update) {
      updates.push(update);
    }
  }

  return updates;
}

/**
 * Gets only miners with available updates
 */
export async function getAvailableUpdates(): Promise<MinerUpdate[]> {
  const allUpdates = await checkAllMinerUpdates();
  return allUpdates.filter(u => u.hasUpdate);
}

/**
 * Updates the stored version for a miner after successful update
 */
export async function updateMinerVersion(minerName: string, newVersion: string): Promise<void> {
  await prisma.minerSoftware.update({
    where: { name: minerName },
    data: { version: newVersion },
  });
}

/**
 * Clears the version cache (useful for forcing refresh)
 */
export function clearVersionCache(): void {
  versionCache.clear();
}

// ============================================
// SCHEDULED UPDATE CHECKER
// ============================================

let updateCheckInterval: NodeJS.Timeout | null = null;
let lastCheckTime: Date | null = null;
let lastCheckResults: MinerUpdate[] = [];

/**
 * Starts the scheduled update checker
 * Runs twice daily (every 12 hours)
 */
export function startUpdateChecker(): void {
  if (updateCheckInterval) {
    return; // Already running
  }

  const CHECK_INTERVAL_MS = 12 * 60 * 60 * 1000; // 12 hours

  // Initial check after 1 minute (give server time to start)
  setTimeout(async () => {
    await runUpdateCheck();
  }, 60 * 1000);

  // Schedule recurring checks
  updateCheckInterval = setInterval(async () => {
    await runUpdateCheck();
  }, CHECK_INTERVAL_MS);

  console.log('[UpdateChecker] Started - checking every 12 hours');
}

/**
 * Stops the scheduled update checker
 */
export function stopUpdateChecker(): void {
  if (updateCheckInterval) {
    clearInterval(updateCheckInterval);
    updateCheckInterval = null;
    console.log('[UpdateChecker] Stopped');
  }
}

/**
 * Runs an update check and logs results
 */
async function runUpdateCheck(): Promise<void> {
  console.log('[UpdateChecker] Checking for miner updates...');
  
  try {
    const updates = await checkAllMinerUpdates();
    lastCheckTime = new Date();
    lastCheckResults = updates;

    const availableUpdates = updates.filter(u => u.hasUpdate);
    
    if (availableUpdates.length > 0) {
      console.log(`[UpdateChecker] Found ${availableUpdates.length} miner update(s):`);
      for (const update of availableUpdates) {
        console.log(`  - ${update.displayName}: ${update.currentVersion} -> ${update.latestVersion}`);
      }
    } else {
      console.log('[UpdateChecker] All miners are up to date');
    }
  } catch (error) {
    console.error('[UpdateChecker] Error checking updates:', error);
  }
}

/**
 * Gets the last check results without making new API calls
 */
export function getLastCheckResults(): { time: Date | null; updates: MinerUpdate[] } {
  return {
    time: lastCheckTime,
    updates: lastCheckResults,
  };
}

/**
 * Forces an immediate update check
 */
export async function forceUpdateCheck(): Promise<MinerUpdate[]> {
  clearVersionCache();
  await runUpdateCheck();
  return lastCheckResults;
}
