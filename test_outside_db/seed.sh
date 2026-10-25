#!/usr/bin/env bash
# Helper script to execute/re-apply seed.sql against leafygo-outside-db
set -euo pipefail

CONTAINER_NAME="leafygo-outside-db"
DB_USER="outside_admin"
DB_NAME="outside_corp"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

echo "Re-applying seed.sql to outside-db..."

if [ -z "$(docker ps -q -f name=${CONTAINER_NAME})" ]; then
    echo "Container ${CONTAINER_NAME} is not running. Starting with docker compose up -d..."
    docker compose up -d
    sleep 3
fi

docker exec -i "${CONTAINER_NAME}" psql -U "${DB_USER}" -d "${DB_NAME}" < "${SCRIPT_DIR}/seed.sql"

echo "Seeding complete!"
