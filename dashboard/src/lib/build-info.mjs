import { randomUUID } from 'node:crypto';

// Correlation only, not an authentication credential. A restart gets a new ID.
const instanceID = randomUUID();

export function buildInfo(version, revision) {
  return {
    component: 'dashboard',
    version: version || 'development',
    revision: revision || 'unknown',
    instance_id: instanceID,
  };
}
