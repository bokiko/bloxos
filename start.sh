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

# Load environment variables for script use (docker-compose reads .env automatically)
export $(grep -v '^#' .env | grep -v '^$' | xargs) 2>/dev/null || true

# Clean up orphan containers
echo "Cleaning up old containers..."
docker compose -f docker-compose.yml -f docker-compose.prod.yml down --remove-orphans 2>/dev/null || true

# Pull latest images
echo "Pulling latest images..."
docker compose -f docker-compose.yml -f docker-compose.prod.yml pull 2>/dev/null || true

# Build images first
echo "Building BloxOS images..."
docker compose -f docker-compose.yml -f docker-compose.prod.yml build

# Start database services first
echo "Starting database services..."
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d postgres redis

# Wait for database to be ready
echo "Waiting for database to be ready..."
sleep 5
until docker compose -f docker-compose.yml -f docker-compose.prod.yml exec -T postgres pg_isready -U "${POSTGRES_USER:-bloxos}" >/dev/null 2>&1; do
    echo "  Waiting for PostgreSQL..."
    sleep 2
done
echo "  PostgreSQL is ready!"

# Run database migrations
echo "Running database migrations..."
docker run --rm --network bloxos-network \
    -e DATABASE_URL="postgresql://${POSTGRES_USER:-bloxos}:${POSTGRES_PASSWORD}@postgres:5432/${POSTGRES_DB:-bloxos}" \
    -w /app/packages/database \
    bloxos-api pnpm exec prisma db push

# Start all services
echo "Starting all services..."
docker compose -f docker-compose.yml -f docker-compose.prod.yml up -d

# Wait for services to be healthy
echo "Waiting for services to start..."
sleep 10

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
