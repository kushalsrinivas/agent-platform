# Single-host deployment (VPS)

This describes a **test or initial deployment** on one host. It is the same images and configuration as local
development, with datastore ports closed and credentials rotated.

It is not a full production hardening guide: TLS termination, key management, backups and observability are still the
operator's responsibility. The API authenticates OIDC bearer tokens, but it has no browser session flow, API keys or
fine-grained project ACLs yet. Do not expose a development-auth deployment to untrusted users.

## What gets deployed

| Container | Image | Runtime user | Published in base compose |
|---|---|---|---|
| `api` | Go, static binary on Alpine | `app` | 8080 |
| `orchestrator` | Go, static binary on Alpine | `app` | 9090, 8081 |
| `worker` | Go, static binary on Alpine | `app` | 8082 |
| `dashboard` | Next.js standalone | `nextjs` (uid 1001) | 3000 |
| `postgres`, `nats`, `redis`, object storage | pinned upstream images | image defaults | data ports |
| `migrate`, `minio-init` | one-shot jobs | image defaults | none |

All application images are multi-stage builds of this repository and run as non-root. Nothing is pulled from a
private registry, and no secret is baked into an image.

## 1. Host prerequisites

- Linux x86_64 or arm64 with Docker Engine 24+ and Docker Compose **v2.24+** (`docker compose version`).
- 2 vCPU / 4 GB RAM / 20 GB disk for this stack with no workload. Worker sandboxes need more memory later.
- A domain (recommended) and a reverse proxy for TLS.

```bash
git clone <your-repo-url> agent-platform
cd agent-platform
```

## 2. Configure credentials before the first start

```bash
cp .env.example .env
# edit .env
```

Change at minimum:

- `POSTGRES_PASSWORD` — used by the Postgres image **only when the volume is first created**.
- `MINIO_ROOT_USER` / `MINIO_ROOT_PASSWORD`.
- `PUBLIC_DASHBOARD_ORIGIN` (used by the VPS override's CORS setting), for example `https://app.example.com`.
- `AUTH_MODE=oidc`, `OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`.

The VPS override sets `AUTH_MODE=oidc` and clears the dev secret. Without a valid OIDC issuer/client the API refuses
to start, which is deliberate: a development token endpoint on a public host lets anyone mint a token for any
principal. For a temporary non-OIDC test host, use `AUTH_MODE=dev` only behind a firewall and with a rotated
`AUTH_DEV_SECRET`; never leave it on an exposed interface.

Application role passwords (`app_api`, `app_orchestrator`) are created by the bootstrap migration with
development defaults. Rotate them **after the first start** with [the steps below](#5-credentials) and restart the
services with an override that carries the new URLs.

> Changing `POSTGRES_PASSWORD` after the `postgres-data` volume exists has no effect on the database; the
> initialized cluster keeps the old password. Either set it before first boot or run `ALTER ROLE postgres`.

## 3. Start the stack

```bash
cp infrastructure/compose/vps.override.example.yml infrastructure/compose/vps.override.yaml
# edit PUBLIC_DASHBOARD_ORIGIN / DASHBOARD_PORT if needed

docker compose \
  -f docker-compose.yml \
  -f infrastructure/compose/vps.override.yaml \
  up -d --build
```

The override uses Compose's `!reset` to remove host port mappings for Postgres, NATS, Redis, object storage, the
orchestrator and the worker. With the default `.env` ports, only the dashboard (`3000`) and API (`8080`) remain
published. This matters because Docker-published ports bypass UFW; closing ports with a firewall alone is not
enough.

Verify:

```bash
docker compose -f docker-compose.yml -f infrastructure/compose/vps.override.yaml ps
curl -fsS http://localhost:8080/healthz
curl -fsS http://localhost:8080/readyz
curl -fsS http://localhost:8080/v1/meta
docker compose logs --tail=100 api orchestrator worker
```

The `migrate` service must show `Exited (0)`. The API and orchestrator wait for it. The worker waits for the
orchestrator's health check.

`make images` builds every image without starting anything; `docker compose build` is equivalent for the base file.

## 4. TLS and access

Run Caddy, nginx or Traefik on the host and proxy the two public ports:

```caddyfile
app.example.com {
    reverse_proxy 127.0.0.1:3000
}

api.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Set `PUBLIC_DASHBOARD_ORIGIN=https://app.example.com` in `.env` before starting, or update
`API_CORS_ORIGINS` in the override and restart `api`.

The API accepts `Authorization: Bearer <OIDC JWT>` only. The dashboard does not yet perform an OIDC login or send
cookies; browser session cookies and CSRF are deferred. Test API calls with a token from your IdP:

```bash
curl -fsS https://api.example.com/v1/orgs \
  -H "Authorization: Bearer $OIDC_ACCESS_TOKEN"
```

Restrict inbound traffic at the provider firewall and on the host. Keep SSH open, and expose only the proxy's
`80`/`443`; the two container ports can be bound to loopback in the override if the proxy runs on the same host.

## 5. Credentials

### Rotate application role passwords

```bash
docker compose exec -T postgres psql -U postgres -d agent_platform <<'SQL'
ALTER ROLE app_api PASSWORD 'replace-with-a-long-random-value';
ALTER ROLE app_orchestrator PASSWORD 'replace-with-a-long-random-value';
SQL
```

Then add an override that points the services at the new passwords and restart:

```yaml
# infrastructure/compose/vps.override.yaml
services:
  api:
    environment:
      API_DATABASE_URL: postgres://app_api:NEW_PASSWORD@postgres:5432/agent_platform?sslmode=disable
  orchestrator:
    environment:
      ORCHESTRATOR_DATABASE_URL: postgres://app_orchestrator:NEW_PASSWORD@postgres:5432/agent_platform?sslmode=disable
```

URL-encode characters that are special in a URI (`@`, `:`, `/`, `%`).

### Rotate the Postgres superuser password

```bash
docker compose exec -T postgres psql -U postgres -c "ALTER ROLE postgres PASSWORD 'new-value';"
# update POSTGRES_PASSWORD in .env so the migrate job can still connect
```

The migration job connects as the superuser locally. In a production deployment, DDL runs under dedicated
migration roles; see [data-ownership.md](data-ownership.md).

## 6. Migrations on upgrade

```bash
git pull
docker compose -f docker-compose.yml -f infrastructure/compose/vps.override.yaml build
docker compose -f docker-compose.yml -f infrastructure/compose/vps.override.yaml run --rm migrate
docker compose -f docker-compose.yml -f infrastructure/compose/vps.override.yaml up -d
```

Run migrations before starting new application replicas. Future migrations must be expand/contract so the
previous version keeps working during the transition.

## 7. Backups

PostgreSQL is the source of truth. Back it up even in a test environment you care about:

```bash
docker compose exec -T postgres pg_dump -U postgres -Fc agent_platform > "agent_platform-$(date -u +%Y%m%dT%H%M%SZ).dump"
```

- Restore with `pg_restore` into an empty database.
- Large checkpoints and artifacts live in the `checkpoint-artifacts` volume (or an S3/MinIO bucket when the
  artifact backend is replaced). Snapshot the volume with the host or mirror the bucket; losing it reduces resume
  granularity but cannot change a committed logical state because PostgreSQL remains authoritative.
- NATS and Redis hold no authoritative state; do not back them up. `TASK_DISPATCH` is a work-queue stream,
  `EVENTS` retains 72 hours and `TASK_DLQ` retains dead letters for 7 days. Setting
  `ORCHESTRATOR_STREAM_REPLICAS=3` requires a three-node NATS cluster; do not raise it on a single node.
- Set `ORCHESTRATOR_ADMIN_TOKEN` to a strong random value if operator status/redrive is needed. The orchestrator
  port is removed from the host by the VPS override, so the surface is reachable only inside the Compose network;
  leave the token empty to disable both routes. Worker IDs must match `^wrk_[A-Za-z0-9_-]{8,64}$`.
- The worker image includes the Python agent runtime. `WORKER_RUNTIME_COMMAND` defaults to
  `python -m agent_runtime serve --socket {socket}`; setting it empty uses the offline echo executor. Each attempt
  is a separate Python process in its own process group; `WORKER_RUNTIME_STARTUP_SECONDS` and
  `WORKER_RUNTIME_KILL_SECONDS` bound startup and termination. The image is larger than a Go-only worker because it
  carries the Python venv and LangGraph. The orchestrator writes large checkpoints to
  `ORCHESTRATOR_ARTIFACT_DIR`; Compose mounts the `checkpoint-artifacts` volume, and the directory is created with
  the service user's ownership in the image.
- API keys are bearer-like secrets. Create them through an owner/admin endpoint, scope them to a project for CI,
  and revoke them immediately when a device or pipeline is retired. Store them in a secret manager, not in shell
  profiles or source control. The API hashes keys at rest and shows the plaintext once.
- MCP servers are launched by the runtime (stdio) or reached over Streamable HTTP. In production keep
  `AGENT_MCP_ALLOW_STDIO=false`; use HTTPS servers and `AGENT_MCP_ALLOW_PRIVATE_NETWORKS=false` behind an egress
  proxy. Store MCP credentials under an `auth_credential_ref` in the encrypted secret store. Do not put tokens in
  the agent spec or MCP `env`.
- A supervisor holds one worker slot while it waits for children. Size each supervisor pool for
  `1 + orchestration.max_parallel` or run supervisors and children in separate pools. Approval and delay nodes do
  not occupy worker slots.
- Workers advertise `WORKER_RUNTIME`, `WORKER_CAPABILITIES`, CPU cores and memory. The scheduler only assigns a
  node to a worker whose pool, capability, runtime ABI/digest and resource requirements match. Scale workers by
  adding replicas with distinct `WORKER_ID` values; a shared worker ID intentionally replaces the previous boot.
- Subworkflow/loop child executions use the same database and worker pool; no extra service is required. A child
  chain is bounded by loop `max_iterations` and parent cancellation propagates to children.
- `docker compose stop worker` sends `SIGTERM`, so the worker drains for up to `WORKER_SHUTDOWN_GRACE_SECONDS`
  before exiting. `docker compose kill worker` bypasses that grace; leases expire and the reaper retries the work.
- Set `ORCHESTRATOR_MAX_ACTIVE_TASKS_PER_TENANT`, `ORCHESTRATOR_PROVIDER_CONCURRENCY` and
  `ORCHESTRATOR_TOOL_CONCURRENCY` to bound noisy-neighbour and upstream-provider load. They are advisory
  admission caps derived from committed active task rows.
- Worker control-plane and API-to-orchestrator gRPC require shared `x-worker-token` / `x-internal-token`
  identities. The transport is still plaintext inside the private Compose network; enable TLS or mTLS before
  running workers across hosts or trust boundaries. The worker is on the `work` network only and cannot route to
  PostgreSQL or object storage.

## 8. Updating images and base images

- Application images are built on the host from the checkout.
- Base images are pinned by tag. Before a real production deployment, pin them by digest and add an image
  scanner (Trivy/Grype) to CI.
- There is no registry push step. To deploy from CI instead, push the three images and the dashboard image, then
  change the compose `build:` entries to `image:` references.

## 9. Operational checks

| Check | Command |
|---|---|
| Liveness | `curl -fsS localhost:8080/healthz` |
| Readiness (database) | `curl -fsS localhost:8080/readyz` |
| Orchestrator dependencies | `curl -fsS localhost:8081/readyz` |
| Worker NATS and control session | `curl -fsS localhost:8082/readyz` and `/v1/consumer` |
| Service state | `docker compose ps` |
| Logs | `docker compose logs -f --tail=100` |

Readiness returns `503` with a dependency error; liveness only proves the process is running.

## Known limits of this deployment

- Single host, single Postgres, no high availability. Losing the host loses availability; restores come from
  backups.
- OIDC is the only production authentication path. There are no API keys, browser sessions or CSRF handling yet.
- The dashboard renders fixtures until the Phase 05 API wiring.
- No TLS, rate limiting or WAF inside the stack; the reverse proxy owns those.
- Development credentials exist in the bootstrap migration as a fallback. Rotate them as above.
