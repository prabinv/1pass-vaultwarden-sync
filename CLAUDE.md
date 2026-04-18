# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project

Go tool to sync secrets between 1Password and Vaultwarden (self-hosted Bitwarden). Ships as two binaries:

- `cmd/sync` — interactive CLI with Bubble Tea TUI
- `cmd/server` — multi-tenant web service with PostgreSQL backend and SSE streaming

Module: `github.com/prabinv/1pass-vaultwarden-sync`

## Commands

```bash
# Build all binaries
go build ./...

# Run tests (always with race detector)
go test -race ./...

# Test single package
go test -race ./internal/sync/...

# Coverage
go test -cover ./...

# Format
gofmt -w .
goimports -w .

# Vet
go vet ./...

# Security scan
gosec ./...

# Run web server (requires DATABASE_URL, ENCRYPTION_KEY, JWT_SECRET)
task server

# Run E2E tests (requires Docker)
task e2e
```

## Architecture

```
cmd/sync/main.go                  — cobra CLI: --watch, --no-confirm, --debug, --log-file, --page-size
cmd/server/main.go                — chi router, DB migrations at startup, JWT+cookie auth
internal/
  config/        config.go        — Load() reads env vars into Config struct
  crypto/        cipherstring.go  — Bitwarden AES-256-CBC+HMAC CipherString (parse/encrypt/decrypt)
                 key.go           — UserKey derivation from master password (PBKDF2)
                 identity.go      — Exchange() — Vaultwarden identity token + user key derivation
  logger/        logger.go        — Setup() configures slog handler (level, writer)
  onepassword/   client.go        — 1Password SDK wrapper; implements sync.ItemSource
  vaultwarden/   client.go        — Vaultwarden REST client; implements sync.ItemSink
                                    (name-based cache, E2E encryption, HTTP/1.1 forced)
  sync/          engine.go        — Plan() + Apply() — diff + timestamp-compare sync engine
                 plan.go          — Item, SyncPlan, PlanItem, SyncResult, ProgressEvent types
  tui/           model.go         — Bubble Tea state machine (planning/preview/syncing/done)
                 list.go          — bubbles/list delegate + paginated renderer
                 progress.go      — ProgressEvent → tea.Cmd channel bridge
                 styles.go        — lipgloss style definitions
  store/         db.go            — WithUserID() RLS transaction helper (SET LOCAL app.user_id)
                 users.go         — UserStore: Create, GetByEmail
                 credentials.go   — CredentialsStore: Upsert, Get (pgcrypto-encrypted at rest)
                 jobs.go          — JobStore: Create, UpdateStatus, AppendEvent, ListEvents, GetJob, ListJobs
  jobrunner/     runner.go        — SSE broadcast hub + goroutine-per-job sync runner
  web/
    middleware/  auth.go          — JWT HS256 cookie middleware; injects userID into context
    handlers/    auth.go          — POST /auth/register, /auth/login, /auth/logout
                 credentials.go   — GET/POST /settings/credentials
                 sync.go          — POST /sync, GET /sync/:id, GET /sync/:id/stream (SSE), GET /sync/history
    templates/   *.templ          — a-h/templ type-safe HTML; Datastar CDN for SSE reactivity
migrations/
  up.sql                          — PostgreSQL schema: users, credentials (RLS+pgcrypto), sync_jobs, sync_job_events
  down.sql                        — DROP TABLE statements
docker/
  Dockerfile                      — Multi-stage: golang:1.23-alpine builder → alpine:3.19 runtime
  docker-compose.yml              — Production stack (server + postgres)
  docker-compose.test.yml         — E2E test stack (isolated postgres on 5433, server on 8081)
e2e/
  tests/                          — Playwright tests: auth, credentials, sync, tenant isolation
```

### Key design decisions

- Items matched by **name** (not UUID): 1Password ExternalID vs Vaultwarden cipher UUID are different namespaces; `PlanItem.SinkExternalID` carries the Vaultwarden UUID through to Apply.
- Vaultwarden cipher cache: one `GET /api/ciphers` fetch per sync run (warmed in `main.go` via `WarmCache`), invalidated after any write.
- HTTP/2 disabled on the Vaultwarden HTTP client (`TLSNextProto: make(...)`) to avoid RST_STREAM INTERNAL_ERROR from reverse-proxied deployments.
- `VAULTWARDEN_MASTER_PASSWORD` is **not** used for authentication — it decrypts the encrypted user key returned by the identity endpoint, enabling client-side E2E field encryption.
- **RLS enforcement**: every tenant-scoped DB write goes through `WithUserID(ctx, pool, userID, fn)` which runs `SET LOCAL app.user_id = $1` inside a transaction before executing the query. `JobStore.UpdateStatus` and `AppendEvent` accept `userID` and call `WithUserID` internally so callers cannot accidentally bypass RLS.
- **Credential encryption**: Vaultwarden credentials stored via `pgp_sym_encrypt` (pgcrypto); `ENCRYPTION_KEY` env var is the symmetric passphrase.
- **SSE replay**: on reconnect the handler replays all persisted `sync_job_events` before streaming live events, ensuring no progress is lost.

## Environment Variables

### CLI (`cmd/sync`)

| Variable | Purpose |
|---|---|
| `OP_SERVICE_ACCOUNT_TOKEN` | 1Password SDK service account token (starts with `ops_`) |
| `VAULTWARDEN_URL` | Base URL of Vaultwarden instance (e.g. `https://vault.example.com`) |
| `VAULTWARDEN_CLIENT_ID` | Vaultwarden API client ID |
| `VAULTWARDEN_CLIENT_SECRET` | Vaultwarden API client secret |
| `VAULTWARDEN_MASTER_PASSWORD` | Master password (for E2E key decryption only, not auth) |

### Web server (`cmd/server`)

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | PostgreSQL connection string (e.g. `postgres://user:pass@host:5432/db`) |
| `ENCRYPTION_KEY` | Symmetric passphrase for pgcrypto credential encryption (min 32 chars recommended) |
| `JWT_SECRET` | HMAC-SHA256 signing secret for JWT session tokens (min 32 chars recommended) |

## Go Conventions

- Accept interfaces, return structs
- Wrap errors: `fmt.Errorf("context: %w", err)`
- Use `context.Context` with timeouts for all external calls
- Table-driven tests
