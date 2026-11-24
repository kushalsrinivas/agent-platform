#!/usr/bin/env bash
# Apply every migration to a scratch database, verify the core schemas, then drop it.
set -euo pipefail
cd "$(dirname "$0")/.."

DB="agent_platform_migrate_check_$$"
POSTGRES_PASSWORD="${POSTGRES_PASSWORD:-postgres}"
cleanup() {
  docker compose exec -T postgres psql -q -U postgres -d postgres \
    -c "DROP DATABASE IF EXISTS ${DB} WITH (FORCE);" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker compose up -d postgres >/dev/null
for _ in $(seq 1 30); do
  docker compose exec -T postgres pg_isready -U postgres >/dev/null 2>&1 && break
  sleep 1
done

docker compose exec -T postgres psql -q -U postgres -d postgres -c "CREATE DATABASE ${DB};"

docker compose run --rm \
  -e CONTROL_DATABASE_URL="postgres://postgres:${POSTGRES_PASSWORD}@postgres:5432/${DB}?sslmode=disable&x-migrations-table=control_schema_migrations_check" \
  -e EXECUTION_DATABASE_URL="postgres://postgres:${POSTGRES_PASSWORD}@postgres:5432/${DB}?sslmode=disable&x-migrations-table=execution_schema_migrations_check" \
  migrate >/dev/null

for relation in control.organizations control.projects control.agents execution.executions \
  execution.execution_tasks execution.task_attempts execution.checkpoints execution.outbox; do
  present=$(docker compose exec -T postgres psql -At -U postgres -d "${DB}" \
    -c "SELECT to_regclass('${relation}') IS NOT NULL;")
  if [ "${present}" != "t" ]; then
    echo "migration check failed: ${relation} is missing" >&2
    exit 1
  fi
done

control_version=$(docker compose exec -T postgres psql -At -U postgres -d "${DB}" \
  -c "SELECT version FROM control_schema_migrations_check;" | tr -d '[:space:]')
execution_version=$(docker compose exec -T postgres psql -At -U postgres -d "${DB}" \
  -c "SELECT version FROM execution_schema_migrations_check;" | tr -d '[:space:]')
echo "migrations applied and verified on ${DB} (control=${control_version:-none} execution=${execution_version:-none})"
