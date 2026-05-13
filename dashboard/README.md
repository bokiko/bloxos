# BloxOS Dashboard

Next.js dashboard for the BloxOS hub.

## Development

```bash
pnpm install
pnpm dev
```

Open `http://localhost:3000`.

By default `NEXT_PUBLIC_HUB_URL` is empty, so API calls use the browser's current
origin. This matches production behind Caddy, where `/api/*`, `/ws/*`,
`/install.sh`, `/install.ps1`, and `/download/*` are routed to the hub.

For a split local setup with the hub running directly on port `4000`:

```bash
NEXT_PUBLIC_HUB_URL=http://localhost:4000 pnpm dev
```

## Production

Build and run the dashboard as a local service:

```bash
pnpm install --frozen-lockfile
pnpm build
pnpm start -- -H 127.0.0.1 -p 3000
```

The sample systemd unit lives at
[`scripts/systemd/bloxos-dashboard.service`](../scripts/systemd/bloxos-dashboard.service).

## Runtime Contract

- Auth uses the hub's `/api/auth/login` endpoint and stores the JWT in browser
  storage.
- Live fleet updates arrive over SSE from `/api/events`.
- Terminal sessions use `/api/machines/:id/terminal` to create a session and
  `/ws/terminal/:session_id` for the browser side of the relay.
- Branding, theme, user preferences, pinned machines, saved filters, and avatars
  are all loaded from hub APIs.

## Checks

```bash
pnpm lint
pnpm build
```
