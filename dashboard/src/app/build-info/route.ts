import { buildInfo } from '@/lib/build-info.mjs';

// /api/* is forwarded to the hub by existing proxies. Keep this dashboard
// endpoint outside that prefix so old proxy configurations need no change.
export const dynamic = 'force-dynamic';
export const runtime = 'nodejs';

export function GET() {
  return Response.json(
    buildInfo(process.env.BLOXOS_BUILD_VERSION, process.env.BLOXOS_BUILD_REVISION),
    { headers: { 'Cache-Control': 'no-store, max-age=0' } },
  );
}
