#!/bin/bash
# BloxOS Stop Script
cd "$(dirname "$0")"
echo "Stopping BloxOS..."
docker compose -f docker-compose.yml -f docker-compose.prod.yml down
echo "BloxOS stopped."
