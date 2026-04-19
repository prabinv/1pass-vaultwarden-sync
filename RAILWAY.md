# Deploying to Railway

## What already works

- `PORT` is read via `envOr("PORT", "8080")` — Railway's injected `$PORT` works automatically
- Migrations run at startup — no manual step needed
- `docker/Dockerfile` is multi-stage and ready to use

## Steps

### 1. Create Railway project

```
railway login
railway init
```

### 2. Add PostgreSQL

In Railway dashboard: **New** → **Database** → **PostgreSQL**

Railway auto-creates the DB and exposes `DATABASE_URL` as a linked variable.

### 3. Add the Go service

In Railway dashboard: **New** → **GitHub Repo** → select this repo

Railway detects `docker/Dockerfile` automatically.

### 4. Set environment variables

In the service settings → **Variables**:

| Variable | Value |
|---|---|
| `DATABASE_URL` | Link to Railway Postgres (click **+ Add Reference**) |
| `ENCRYPTION_KEY` | Random string, min 32 chars |
| `JWT_SECRET` | Random string, min 32 chars |

`PORT` is injected automatically — do not set it manually.

### 5. Deploy

```
railway up
```

Or push to your linked GitHub branch.

## SSL

Railway Postgres requires SSL. The Railway-provided `DATABASE_URL` already includes the correct SSL parameters — do not append `?sslmode=disable`.

For local docker-compose, `sslmode=disable` remains in `docker/docker-compose.yml` as-is.

## Local dev

Keep using `docker/docker-compose.dev.yml` locally. Point `.env` at your local Postgres:

```
DATABASE_URL=postgres://sync:password@localhost:5432/sync?sslmode=disable
```

## Verify deployment

1. Check Railway deploy logs for `migrate up` success
2. `POST /auth/register` → should return 200
3. Run a sync job → check SSE stream works
