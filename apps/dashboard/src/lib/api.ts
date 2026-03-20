// Resolve the base API URL.
// Priority: NEXT_PUBLIC_API_URL env var → infer from current page origin.
// Inferring from origin uses the same protocol (http/https) so the app works
// behind a reverse proxy without code changes.
export function getApiUrl(): string {
  if (process.env.NEXT_PUBLIC_API_URL) {
    return process.env.NEXT_PUBLIC_API_URL.replace(/\/$/, '');
  }
  if (typeof window === 'undefined') return 'http://localhost:3001';
  const protocol = window.location.protocol; // 'http:' or 'https:'
  return `${protocol}//${window.location.hostname}:3001`;
}

// Derive WebSocket URL from the API URL.
// Replaces http(s): with ws(s): so callers don't need to duplicate the logic.
export function getWsUrl(): string {
  const base = getApiUrl();
  return base.replace(/^http(s?):/, 'ws$1:');
}

export function getCsrfToken(): string {
  const match = document.cookie.match(/(?:^|;\s*)csrf_token=([^;]*)/);
  return match ? decodeURIComponent(match[1]) : '';
}
