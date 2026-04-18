# Web Interface Design — 1pass-vaultwarden-sync

**Date:** 2026-04-16  
**Status:** Approved  
**Branch:** master

---

## Context

The existing tool is a CLI/TUI application that syncs secrets from 1Password to a self-hosted Vaultwarden instance. Each run requires credentials to be provided via environment variables, making it difficult for multiple users to manage their own sync configurations.

This spec describes a multi-tenant web service that wraps the existing sync engine, adds user accounts, and stores encrypted credentials per user — turning the tool into a hosted service accessible via browser.

---

## Goals

- Allow multiple users to register and manage their own 1Password + Vaultwarden credentials
- Let users manually trigger a sync and watch real-time progress in the browser
- Store all sensitive credentials encrypted at rest
- Deploy as a single Docker image alongside PostgreSQL

---

## Architecture Overview

A new `cmd/server` binary is added to the existing repo alongside `cmd/sync`. It shares all `internal/` packages unchanged — the sync engine, 1Password client, Vaultwarden client, and crypto packages are reused as-is.

```
cmd/
  sync/main.go        ← unchanged CLI tool
  server/main.go      ← new HTTP server entry point

internal/
  web/
    handlers/         ← HTTP handlers (auth, credentials, sync)
    middleware/       ← JWT extraction, tenant context, RLS activation
    templates/        ← templ components with Datastar attributes
  store/
    db.go             ← pgx connection pool setup
    users.go          ← user CRUD
    credentials.go    ← encrypted credential CRUD (pgcrypto)
    jobs.go           ← sync job state + event log
  jobrunner/
    runner.go         ← goroutine manager + SSE broadcast registry
  (existing — unchanged)
    sync/             ← Plan() + Apply()
    onepassword/      ← 1Password SDK wrapper
    vaultwarden/      ← Vaultwarden REST client
    crypto/           ← Bitwarden E2E encryption
    config/
    logger/

migrations/
  001_initial.sql

e2e/
  playwright.config.ts
  tests/
  fixtures/

docker/
  Dockerfile
  docker-compose.yml
  docker-compose.test.yml
```

**Frontend:** [Datastar](https://data-star.dev) — SSE-driven reactivity, ~14kb JS, no build pipeline. Server renders templ components; Datastar handles live updates via SSE signals.

**HTTP router:** `go-chi/chi` — lightweight, idiomatic middleware chaining.

**Migrations:** `golang-migrate/migrate` — applied automatically at server startup before accepting requests.

**Single binary** — templates embedded via `go:embed`, static assets served inline, migrations run at startup.

---

## Database Schema

```sql
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email         TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,  -- bcrypt cost=12
  created_at    TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE credentials (
  id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id                     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  op_service_account_token    BYTEA NOT NULL,  -- pgp_sym_encrypt
  vaultwarden_url             TEXT NOT NULL,   -- not sensitive
  vaultwarden_client_id       BYTEA NOT NULL,  -- pgp_sym_encrypt
  vaultwarden_client_secret   BYTEA NOT NULL,  -- pgp_sym_encrypt
  vaultwarden_master_password BYTEA NOT NULL,  -- pgp_sym_encrypt
  created_at TIMESTAMPTZ DEFAULT now(),
  updated_at TIMESTAMPTZ DEFAULT now(),
  UNIQUE (user_id)  -- one credential set per user in v1
);

CREATE TABLE sync_jobs (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status      TEXT NOT NULL DEFAULT 'pending',  -- pending/running/done/failed
  started_at  TIMESTAMPTZ,
  finished_at TIMESTAMPTZ,
  error       TEXT,
  created_at  TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE sync_job_events (
  id         BIGSERIAL PRIMARY KEY,
  job_id     UUID NOT NULL REFERENCES sync_jobs(id) ON DELETE CASCADE,
  sequence   INT NOT NULL,
  event_type TEXT NOT NULL,
  payload    JSONB NOT NULL,
  created_at TIMESTAMPTZ DEFAULT now()
);

-- Row-Level Security — tenant isolation enforced at DB layer
ALTER TABLE credentials     ENABLE ROW LEVEL SECURITY;
ALTER TABLE sync_jobs       ENABLE ROW LEVEL SECURITY;
ALTER TABLE sync_job_events ENABLE ROW LEVEL SECURITY;

CREATE POLICY tenant_credentials ON credentials
  USING (user_id = current_setting('app.user_id', true)::UUID);

CREATE POLICY tenant_jobs ON sync_jobs
  USING (user_id = current_setting('app.user_id', true)::UUID);

CREATE POLICY tenant_job_events ON sync_job_events
  USING (job_id IN (
    SELECT id FROM sync_jobs
    WHERE user_id = current_setting('app.user_id', true)::UUID
  ));
```

### Credential encryption

Sensitive fields are stored as `BYTEA` via `pgp_sym_encrypt(value, $ENCRYPTION_KEY)`. The `ENCRYPTION_KEY` is a 32-byte hex-encoded env var. Decryption happens only inside the JobRunner goroutine immediately before a sync run — credentials are never returned to the frontend.

### Password hashing

`bcrypt` with cost factor 12. Salt is generated automatically by the bcrypt library and embedded in the stored hash — no separate env var or salt management required.

---

## Auth Flow

```
POST /auth/register  →  validate → bcrypt(cost=12) → INSERT users → set JWT cookie → redirect /
POST /auth/login     →  bcrypt.Compare → issue JWT(user_id, exp=24h) → httpOnly cookie → redirect /
POST /auth/logout    →  clear cookie → redirect /auth/login
```

- JWT signed with `JWT_SECRET` env var
- Cookie flags: `httpOnly; Secure; SameSite=Strict`
- Auth middleware extracts `user_id` from JWT, injects into `context.Context`
- Before each DB query: `SET LOCAL app.user_id = '<id>'` activates RLS policies

---

## Sync Trigger + SSE Progress Flow

```
Browser                    Handler              JobRunner           Sync Engine
  │                           │                     │                    │
  ├─POST /sync/trigger────────►│                     │                    │
  │                           ├─INSERT sync_jobs────►│                   │
  │                           ├─Enqueue(jobID)───────►│                  │
  │◄─302 /sync/jobs/{id}──────┤                     │                    │
  │                           │                     │                    │
  ├─GET /sync/jobs/{id}/stream►│                     │                    │
  │◄──SSE: replay DB events───┤                     │                    │
  │                           │                     ├─decrypt credentials │
  │                           │                     ├─Plan()+Apply()──────►
  │                           │                     │◄─ProgressEvent chan─┤
  │                           │                     ├─INSERT event row    │
  │◄──SSE: live event─────────┤◄────broadcast───────┤                    │
  │  (Datastar updates UI)    │                     │                    │
```

`JobRunner` maintains a `map[jobID]chan ProgressEvent`. The SSE endpoint first replays persisted `sync_job_events` rows (so page refresh mid-sync works), then switches to the live channel. When the job finishes the channel closes and the SSE stream ends.

---

## Routes

| Method | Route | Description |
|---|---|---|
| GET | `/auth/login` | Login form |
| POST | `/auth/login` | Authenticate user |
| GET | `/auth/register` | Register form |
| POST | `/auth/register` | Create account |
| POST | `/auth/logout` | Clear session |
| GET | `/` | Dashboard — sync job history |
| GET | `/credentials` | View/edit credentials form |
| POST | `/credentials` | Save credentials |
| POST | `/sync/trigger` | Trigger a sync run |
| GET | `/sync/jobs/{id}` | Job detail page |
| GET | `/sync/jobs/{id}/stream` | SSE stream (Datastar target) |

---

## Environment Variables

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | PostgreSQL connection string |
| `ENCRYPTION_KEY` | 32-byte hex key for pgp_sym_encrypt |
| `JWT_SECRET` | HMAC secret for signing JWT tokens |
| `PORT` | HTTP listen port (default `8080`) |

---

## Docker

### Dockerfile (multi-stage)

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o server ./cmd/server

FROM alpine:3.19
COPY --from=builder /app/server /usr/local/bin/server
ENTRYPOINT ["server"]
```

### docker-compose.yml

```yaml
services:
  server:
    build: .
    ports: ["8080:8080"]
    environment:
      - DATABASE_URL=postgres://sync:${POSTGRES_PASSWORD}@postgres:5432/sync?sslmode=disable
      - ENCRYPTION_KEY=${ENCRYPTION_KEY}
      - JWT_SECRET=${JWT_SECRET}
    depends_on:
      postgres:
        condition: service_healthy

  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: sync
      POSTGRES_USER: sync
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
    volumes: [pgdata:/var/lib/postgresql/data]
    healthcheck:
      test: ["CMD", "pg_isready", "-U", "sync"]
      interval: 5s
      retries: 5

volumes:
  pgdata:
```

---

## E2E Testing

**Playwright** runs against the full stack (server + real PostgreSQL) via `docker-compose.test.yml`. No mocks — all tests hit real HTTP endpoints with a real database wiped between runs.

### Test matrix

| Test file | Coverage |
|---|---|
| `auth.spec.ts` | Register, login, logout, invalid credentials, cookie presence |
| `credentials.spec.ts` | Save credentials, update, form validation |
| `sync.spec.ts` | Trigger sync, SSE progress events appear in UI, job reaches `done` |
| `isolation.spec.ts` | User A cannot see User B's jobs or credentials |

### Structure

```
e2e/
  playwright.config.ts
  tests/
    auth.spec.ts
    credentials.spec.ts
    sync.spec.ts
    isolation.spec.ts
  fixtures/
    users.ts        ← test user setup/teardown helpers
```

### Taskfile task

```yaml
e2e:
  desc: Run E2E tests against full stack
  cmds:
    - docker compose -f docker-compose.test.yml up -d --wait
    - npx playwright test
    - docker compose -f docker-compose.test.yml down -v
```

---

## Build Order

1. `migrations/001_initial.sql` — schema + RLS
2. `internal/store/` — db, users, credentials, jobs
3. `internal/jobrunner/` — goroutine pool + SSE broadcast
4. `internal/web/templates/` — templ components
5. `internal/web/middleware/` — JWT + RLS activation
6. `internal/web/handlers/` — auth, credentials, sync
7. `cmd/server/main.go` — wire everything, run migrations on start
8. `docker/` — Dockerfile + compose files
9. `e2e/` — Playwright tests

---

## Out of Scope (v1)

- Scheduled/automatic sync (manual trigger only)
- OAuth / third-party auth (email+password only)
- Vaultwarden vault selection (uses default vault)
- Sync conflict resolution UI
