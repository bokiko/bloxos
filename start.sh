#!/bin/bash
# BloxOS Start Script - One command to run everything
set -e

echo "========================================"
echo "  BloxOS - Starting..."
echo "========================================"

cd "$(dirname "$0")"

# Check if Docker is running
if ! docker info >/dev/null 2>&1; then
    echo "ERROR: Docker is not running!"
    echo "Start Docker first: sudo systemctl start docker"
    exit 1
fi

# Check if .env exists, create from example if not
if [ ! -f .env ]; then
    if [ -f .env.example ]; then
        echo "Creating .env from .env.example..."
        cp .env.example .env
        echo "IMPORTANT: Edit .env with your settings before continuing!"
        echo "Run: nano .env"
        exit 1
    else
        echo "ERROR: No .env file found!"
        echo "Create one with at least: POSTGRES_PASSWORD=yourpassword"
        exit 1
    fi
fi

# Clean up orphan containers
echo "Cleaning up old containers..."
docker compose -f docker-compose.yml -f docker-compose.prod.yml down --remove-orphans 2>/dev/null || true

# Pull latest images
echo "Pulling latest images..."
docker compose -f docker-compose.yml -f docker-compose.prod.yml pull 2>/dev/null || true

# Build and start everything
echo "Building and starting BloxOS..."
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d --build

# Wait for services to be healthy
echo "Waiting for services to start..."
sleep 5

# Show status
echo ""
echo "========================================"
echo "  BloxOS Status"
echo "========================================"
docker compose -f docker-compose.yml -f docker-compose.prod.yml ps

echo ""
echo "========================================"
echo "  BloxOS is ready!"
echo "========================================"
echo "Dashboard: http://localhost:3000"
echo "API:       http://localhost:3001"
echo ""
echo "To view logs:  docker compose -f docker-compose.yml -f docker-compose.prod.yml logs -f"
echo "To stop:       docker compose -f docker-compose.yml -f docker-compose.prod.yml down"
