# Docker

Docker packaging is not shipped yet.

BloxOS currently runs as:

- a Go hub process on `127.0.0.1:4000`
- a Next.js dashboard process on `127.0.0.1:3000`
- native Linux and Windows agent binaries
- Caddy in front for LAN HTTPS and routing

The previous `docker-compose.yml` referenced Dockerfiles that are not present in
the repository, so it was removed to avoid advertising a broken install path.
