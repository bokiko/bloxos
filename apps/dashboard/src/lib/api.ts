export function getApiUrl(): string {
  if (typeof window === "undefined") return "http://localhost:3001";
  return "http://" + window.location.hostname + ":3001";
}

export function getCsrfToken(): string {
  const match = document.cookie.match(/(?:^|;\s*)csrf_token=([^;]*)/);
  return match ? decodeURIComponent(match[1]) : "";
}
