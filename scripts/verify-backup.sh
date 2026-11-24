#!/usr/bin/env bash
# Dump the local database, restore it into a scratch database, and verify core table counts.
set -euo pipefail
cd "$(dirname "$0")/.."

DB="agent_platform_backup_check_$$"
DUMP="$(mktemp -t agent-platform-backup.XXXXXX.sql.gz)"
cleanup() {
  docker compose exec -T postgres psql -q -U postgres -d postgres \
    -c "DROP DATABASE IF EXISTS ${DB} WITH (FORCE);" >/dev/null 2>&1 || true
  rm -f "${DUMP}"
}
trap cleanup EXIT

docker compose up -d postgres >/dev/null
for _ in $(seq 1 30); do
  docker compose exec -T postgres pg_isready -U postgres >/dev/null 2>&1 && break
  sleep 1
done

docker compose exec -T postgres pg_dump -U postgres --no-owner --no-privileges agent_platform | gzip >"${DUMP}"
if [ ! -s "${DUMP}" ]; then
  echo "backup verification failed: dump is empty" >&2
  exit 1
fi

docker compose exec -T postgres psql -q -U postgres -d postgres -c "CREATE DATABASE ${DB};"
gunzip -c "${DUMP}" | docker compose exec -T postgres psql -q -U postgres -d "${DB}" >/dev/null

for relation in control.organizations execution.executions execution.execution_tasks execution.outbox; do
  source_count=$(docker compose exec -T postgres psql -At -U postgres -d agent_platform -c "SELECT count(*) FROM ${relation};")
  restored_count=$(docker compose exec -T postgres psql -At -U postgres -d "${DB}" -c "SELECT count(*) FROM ${relation};")
  if [ "${source_count}" != "${restored_count}" ]; then
    echo "backup verification failed: ${relation} source=${source_count} restored=${restored_count}" >&2
    exit 1
  fi
  echo "verified ${relation}: ${restored_count} rows"
done

echo "backup restored into ${DB} from $(du -h "${DUMP}" | cut -f1) dump"
