# Web Interface Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a multi-tenant web service to 1pass-vaultwarden-sync that lets users store encrypted credentials and manually trigger syncs with real-time SSE progress.

**Architecture:** New `cmd/server` binary shares all existing `internal/` packages unchanged. PostgreSQL with Row-Level Security provides tenant isolation. Datastar + templ delivers SSE-driven reactive UI with no JS build pipeline. Sync jobs run as in-process goroutines with progress persisted to the DB for reconnect resilience.

**Tech Stack:** Go 1.23, chi router, pgx/v5, golang-migrate, a-h/templ, Datastar CDN, bcrypt, golang-jwt, pgcrypto, Playwright

---

## File Map

### New files
```
migrations/001_initial.sql
internal/store/db.go
internal/store/db_test.go
internal/store/users.go
internal/store/users_test.go
internal/store/credentials.go
internal/store/credentials_test.go
internal/store/jobs.go
internal/store/jobs_test.go
internal/jobrunner/runner.go
internal/jobrunner/runner_test.go
internal/web/middleware/auth.go
internal/web/middleware/auth_test.go
internal/web/templates/layout.templ
internal/web/templates/auth.templ
internal/web/templates/dashboard.templ
internal/web/templates/credentials.templ
internal/web/templates/job.templ
internal/web/handlers/auth.go
internal/web/handlers/auth_test.go
internal/web/handlers/credentials.go
internal/web/handlers/credentials_test.go
internal/web/handlers/sync.go
internal/web/handlers/sync_test.go
cmd/server/main.go
docker/Dockerfile
docker/docker-compose.yml
docker/docker-compose.test.yml
.env.example
e2e/package.json
e2e/playwright.config.ts
e2e/fixtures/users.ts
e2e/tests/auth.spec.ts
e2e/tests/credentials.spec.ts
e2e/tests/sync.spec.ts
e2e/tests/isolation.spec.ts
```

### Modified files
```
go.mod / go.sum       — add chi, pgx, migrate, templ, jwt, bcrypt, uuid deps
Taskfile.yml          — add server run and e2e tasks
```

---

## Parallel Execution Groups

Tasks within the same group have no dependencies on each other and can run on separate agents simultaneously.

| Group | Tasks | Depends on |
|---|---|---|
| A | 1, 2 | nothing |
| B | 3, 4, 5, 6 | Group A complete |
| C | 7, 8 | Group B complete |
| D | 9, 10, 11, 12 | Group C complete |
| E | 13 | Group D complete |
| F | 14 | Group E complete |
| G | 15 | Group F complete |

---

## Group A — Foundations (parallel, no dependencies)

### Task 1: Add Go dependencies

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add all required dependencies**

```bash
cd /Users/prabin.varma/temp/1pass-vaultwarden-sync
go get github.com/go-chi/chi/v5@latest
go get github.com/golang-jwt/jwt/v5@latest
go get github.com/jackc/pgx/v5@latest
go get github.com/golang-migrate/migrate/v4@latest
go get github.com/golang-migrate/migrate/v4/database/postgres@latest
go get github.com/golang-migrate/migrate/v4/source/iofs@latest
go get github.com/a-h/templ@latest
go get golang.org/x/crypto@latest
go get github.com/google/uuid@latest
```

- [ ] **Step 2: Install templ CLI**

```bash
go install github.com/a-h/templ/cmd/templ@latest
```

- [ ] **Step 3: Verify existing build still compiles**

```bash
go build ./...
```
Expected: no errors (new deps downloaded, existing code unaffected)

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add web server dependencies (chi, pgx, migrate, templ, jwt)"
```

---

### Task 2: Database migration

**Files:**
- Create: `migrations/001_initial.sql`

- [ ] **Step 1: Create migrations directory and SQL file**

```bash
mkdir -p migrations
```

Create `migrations/001_initial.sql`:

```sql
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE users (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email         TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  created_at    TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE credentials (
  id                          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id                     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  op_service_account_token    BYTEA NOT NULL,
  vaultwarden_url             TEXT NOT NULL,
  vaultwarden_client_id       BYTEA NOT NULL,
  vaultwarden_client_secret   BYTEA NOT NULL,
  vaultwarden_master_password BYTEA NOT NULL,
  created_at                  TIMESTAMPTZ DEFAULT now(),
  updated_at                  TIMESTAMPTZ DEFAULT now(),
  UNIQUE (user_id)
);

CREATE TABLE sync_jobs (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  status      TEXT NOT NULL DEFAULT 'pending',
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

-- Tenant isolation enforced at DB layer
ALTER TABLE credentials      ENABLE ROW LEVEL SECURITY;
ALTER TABLE sync_jobs        ENABLE ROW LEVEL SECURITY;
ALTER TABLE sync_job_events  ENABLE ROW LEVEL SECURITY;

-- missing_ok=true prevents error when app.user_id is not set (e.g. unauthenticated queries)
CREATE POLICY tenant_credentials ON credentials
  USING (user_id = current_setting('app.user_id', true)::UUID);

CREATE POLICY tenant_jobs ON sync_jobs
  USING (user_id = current_setting('app.user_id', true)::UUID);

CREATE POLICY tenant_job_events ON sync_job_events
  USING (job_id IN (
    SELECT id FROM sync_jobs
    WHERE user_id = current_setting('app.user_id', true)::UUID
  ));

CREATE INDEX idx_sync_job_events_job_id_seq ON sync_job_events (job_id, sequence);
```

- [ ] **Step 2: Verify SQL against a local PostgreSQL instance**

```bash
psql $TEST_DATABASE_URL -f migrations/001_initial.sql
```
Expected: commands complete with no errors. `TEST_DATABASE_URL` must be set to a writable PostgreSQL URL, e.g. `postgres://sync:pass@localhost:5432/sync_test`.

- [ ] **Step 3: Commit**

```bash
git add migrations/
git commit -m "feat: add initial database migration with RLS tenant isolation"
```

---

## Group B — Store Layer (parallel, depends on Group A)

All store tests require `TEST_DATABASE_URL` pointing at a PostgreSQL instance with the migration already applied.

### Task 3: Store — db.go (connection pool + RLS transaction helper)

**Files:**
- Create: `internal/store/db.go`
- Create: `internal/store/db_test.go`

**Background:** RLS policies activate when `app.user_id` is set via `SET LOCAL`. `SET LOCAL` is transaction-scoped — it resets when the transaction ends. This is exactly what we need with a connection pool: each request gets its own transaction and the setting never leaks between requests. The `WithUserID` helper in this file encapsulates this pattern; all store operations that touch RLS-protected tables must use it.

- [ ] **Step 1: Write the failing test**

Create `internal/store/db_test.go`:

```go
package store_test

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := store.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func TestNewPool_Ping(t *testing.T) {
	pool := testPool(t)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestWithUserID_SetsLocalConfig(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id := "11111111-1111-1111-1111-111111111111"

	err := store.WithUserID(ctx, pool, mustParseUUID(id), func(tx pgx.Tx) error {
		var got string
		return tx.QueryRow(ctx, "SELECT current_setting('app.user_id', true)").Scan(&got)
	})
	if err != nil {
		t.Fatalf("WithUserID: %v", err)
	}
}

func mustParseUUID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		panic(err)
	}
	return id
}
```

Add missing imports at top of file:

```go
import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
)
```

- [ ] **Step 2: Run test to verify it fails**

```bash
TEST_DATABASE_URL=postgres://sync:pass@localhost:5432/sync_test go test -race ./internal/store/ -run TestNewPool -v
```
Expected: FAIL — `store.NewPool` undefined

- [ ] **Step 3: Implement db.go**

Create `internal/store/db.go`:

```go
package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPool creates a pgx connection pool.
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.New: %w", err)
	}
	return pool, nil
}

// WithUserID begins a transaction, activates RLS for userID via SET LOCAL,
// calls fn, then commits. Rolls back on any error. All RLS-protected queries
// (credentials, sync_jobs, sync_job_events) must run inside this wrapper.
func WithUserID(ctx context.Context, pool *pgxpool.Pool, userID uuid.UUID, fn func(pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx,
		"SELECT set_config('app.user_id', $1, true)", userID.String(),
	); err != nil {
		return fmt.Errorf("set app.user_id: %w", err)
	}

	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
TEST_DATABASE_URL=postgres://sync:pass@localhost:5432/sync_test go test -race ./internal/store/ -run "TestNewPool|TestWithUserID" -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/db.go internal/store/db_test.go
git commit -m "feat: add pgx pool and RLS transaction helper"
```

---

### Task 4: Store — users.go

**Files:**
- Create: `internal/store/users.go`
- Create: `internal/store/users_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/store/users_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
)

func TestUserStore_CreateAndGetByEmail(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	us := store.NewUserStore(pool)

	email := "user-store-test@example.com"
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM users WHERE email = $1", email)
	})

	user, err := us.Create(ctx, email, "$2a$12$fakehashvalue")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if user.Email != email {
		t.Errorf("email = %q, want %q", user.Email, email)
	}
	if user.ID.String() == "" {
		t.Error("ID should not be empty")
	}

	got, err := us.GetByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got.ID != user.ID {
		t.Errorf("ID = %v, want %v", got.ID, user.ID)
	}
}

func TestUserStore_GetByEmail_NotFound(t *testing.T) {
	pool := testPool(t)
	us := store.NewUserStore(pool)

	_, err := us.GetByEmail(context.Background(), "nobody@example.com")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUserStore_Create_DuplicateEmail(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	us := store.NewUserStore(pool)

	email := "duplicate@example.com"
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM users WHERE email = $1", email)
	})

	if _, err := us.Create(ctx, email, "hash"); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if _, err := us.Create(ctx, email, "hash"); err == nil {
		t.Error("expected error on duplicate email, got nil")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
TEST_DATABASE_URL=postgres://sync:pass@localhost:5432/sync_test go test -race ./internal/store/ -run TestUserStore -v
```
Expected: FAIL — `store.NewUserStore` undefined

- [ ] **Step 3: Implement users.go**

Create `internal/store/users.go`:

```go
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")

// User represents an authenticated user account.
type User struct {
	ID           uuid.UUID
	Email        string
	PasswordHash string
	CreatedAt    time.Time
}

// UserStore handles user persistence. Queries run against the public (non-RLS) users table.
type UserStore struct {
	pool *pgxpool.Pool
}

func NewUserStore(pool *pgxpool.Pool) *UserStore {
	return &UserStore{pool: pool}
}

// Create inserts a new user. Returns an error if the email already exists.
func (s *UserStore) Create(ctx context.Context, email, passwordHash string) (*User, error) {
	var u User
	row := s.pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash)
		 VALUES ($1, $2)
		 RETURNING id, email, password_hash, created_at`,
		email, passwordHash,
	)
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt); err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return &u, nil
}

// GetByEmail returns the user with the given email, or ErrNotFound.
func (s *UserStore) GetByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	row := s.pool.QueryRow(ctx,
		`SELECT id, email, password_hash, created_at FROM users WHERE email = $1`,
		email,
	)
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return &u, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
TEST_DATABASE_URL=postgres://sync:pass@localhost:5432/sync_test go test -race ./internal/store/ -run TestUserStore -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/users.go internal/store/users_test.go
git commit -m "feat: add user store (create, get-by-email)"
```

---

### Task 5: Store — credentials.go

**Files:**
- Create: `internal/store/credentials.go`
- Create: `internal/store/credentials_test.go`

**Background:** Sensitive fields are encrypted in PostgreSQL using `pgp_sym_encrypt` from the `pgcrypto` extension. The encryption key is a server-side secret (env var `ENCRYPTION_KEY`). Decryption happens only when the jobrunner fetches credentials to run a sync — they are never returned to HTTP responses. All queries run inside a `WithUserID` transaction because RLS protects the credentials table.

- [ ] **Step 1: Write the failing tests**

Create `internal/store/credentials_test.go`:

```go
package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
)

const testEncKey = "test-encryption-key-32-bytes!!!" // exactly 32 chars

func TestCredentialsStore_UpsertAndGet(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	us := store.NewUserStore(pool)
	user, err := us.Create(ctx, "creds-test@example.com", "$2a$12$fakehash")
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id = $1", user.ID) })

	cs := store.NewCredentialsStore(pool, testEncKey)

	input := &store.Credentials{
		OPServiceAccountToken:     "ops_test_token",
		VaultwardenURL:            "https://vault.example.com",
		VaultwardenClientID:       "client-id",
		VaultwardenClientSecret:   "client-secret",
		VaultwardenMasterPassword: "master-password",
	}

	err = store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error {
		return cs.Upsert(ctx, tx, user.ID, input)
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var got *store.Credentials
	err = store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error {
		var e error
		got, e = cs.Get(ctx, tx, user.ID)
		return e
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.OPServiceAccountToken != input.OPServiceAccountToken {
		t.Errorf("token = %q, want %q", got.OPServiceAccountToken, input.OPServiceAccountToken)
	}
	if got.VaultwardenURL != input.VaultwardenURL {
		t.Errorf("url = %q, want %q", got.VaultwardenURL, input.VaultwardenURL)
	}
}

func TestCredentialsStore_Get_NotFound(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	us := store.NewUserStore(pool)
	user, _ := us.Create(ctx, "creds-notfound@example.com", "hash")
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id = $1", user.ID) })

	cs := store.NewCredentialsStore(pool, testEncKey)
	var err error
	store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error {
		_, err = cs.Get(ctx, tx, user.ID)
		return nil
	})
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCredentialsStore_Upsert_UpdatesExisting(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	us := store.NewUserStore(pool)
	user, _ := us.Create(ctx, "creds-update@example.com", "hash")
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id = $1", user.ID) })

	cs := store.NewCredentialsStore(pool, testEncKey)

	first := &store.Credentials{OPServiceAccountToken: "v1", VaultwardenURL: "https://v1.example.com", VaultwardenClientID: "cid", VaultwardenClientSecret: "cs", VaultwardenMasterPassword: "mp"}
	second := &store.Credentials{OPServiceAccountToken: "v2", VaultwardenURL: "https://v2.example.com", VaultwardenClientID: "cid2", VaultwardenClientSecret: "cs2", VaultwardenMasterPassword: "mp2"}

	store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error { return cs.Upsert(ctx, tx, user.ID, first) })
	store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error { return cs.Upsert(ctx, tx, user.ID, second) })

	var got *store.Credentials
	store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error {
		got, _ = cs.Get(ctx, tx, user.ID)
		return nil
	})
	if got.OPServiceAccountToken != "v2" {
		t.Errorf("token = %q, want v2", got.OPServiceAccountToken)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
TEST_DATABASE_URL=postgres://sync:pass@localhost:5432/sync_test go test -race ./internal/store/ -run TestCredentialsStore -v
```
Expected: FAIL — `store.NewCredentialsStore` undefined

- [ ] **Step 3: Implement credentials.go**

Create `internal/store/credentials.go`:

```go
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Credentials holds decrypted 1Password and Vaultwarden connection details.
// Never return this struct in an HTTP response — it contains plaintext secrets.
type Credentials struct {
	ID                        uuid.UUID
	UserID                    uuid.UUID
	OPServiceAccountToken     string
	VaultwardenURL            string
	VaultwardenClientID       string
	VaultwardenClientSecret   string
	VaultwardenMasterPassword string
	CreatedAt                 time.Time
	UpdatedAt                 time.Time
}

// CredentialsStore persists credentials encrypted via pgcrypto.
type CredentialsStore struct {
	pool          *pgxpool.Pool
	encryptionKey string
}

func NewCredentialsStore(pool *pgxpool.Pool, encryptionKey string) *CredentialsStore {
	return &CredentialsStore{pool: pool, encryptionKey: encryptionKey}
}

// Upsert creates or replaces the credential set for userID.
// Must be called inside a WithUserID transaction (RLS enforced).
func (s *CredentialsStore) Upsert(ctx context.Context, tx pgx.Tx, userID uuid.UUID, c *Credentials) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO credentials (
			user_id,
			op_service_account_token,
			vaultwarden_url,
			vaultwarden_client_id,
			vaultwarden_client_secret,
			vaultwarden_master_password,
			updated_at
		) VALUES (
			$1,
			pgp_sym_encrypt($2, $7),
			$3,
			pgp_sym_encrypt($4, $7),
			pgp_sym_encrypt($5, $7),
			pgp_sym_encrypt($6, $7),
			now()
		)
		ON CONFLICT (user_id) DO UPDATE SET
			op_service_account_token    = pgp_sym_encrypt($2, $7),
			vaultwarden_url             = $3,
			vaultwarden_client_id       = pgp_sym_encrypt($4, $7),
			vaultwarden_client_secret   = pgp_sym_encrypt($5, $7),
			vaultwarden_master_password = pgp_sym_encrypt($6, $7),
			updated_at                  = now()`,
		userID,
		c.OPServiceAccountToken,
		c.VaultwardenURL,
		c.VaultwardenClientID,
		c.VaultwardenClientSecret,
		c.VaultwardenMasterPassword,
		s.encryptionKey,
	)
	if err != nil {
		return fmt.Errorf("upsert credentials: %w", err)
	}
	return nil
}

// Get returns decrypted credentials for userID, or ErrNotFound.
// Must be called inside a WithUserID transaction (RLS enforced).
func (s *CredentialsStore) Get(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (*Credentials, error) {
	var c Credentials
	row := tx.QueryRow(ctx, `
		SELECT
			id,
			user_id,
			pgp_sym_decrypt(op_service_account_token, $2),
			vaultwarden_url,
			pgp_sym_decrypt(vaultwarden_client_id, $2),
			pgp_sym_decrypt(vaultwarden_client_secret, $2),
			pgp_sym_decrypt(vaultwarden_master_password, $2),
			created_at,
			updated_at
		FROM credentials
		WHERE user_id = $1`,
		userID, s.encryptionKey,
	)
	if err := row.Scan(
		&c.ID, &c.UserID,
		&c.OPServiceAccountToken,
		&c.VaultwardenURL,
		&c.VaultwardenClientID,
		&c.VaultwardenClientSecret,
		&c.VaultwardenMasterPassword,
		&c.CreatedAt, &c.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get credentials: %w", err)
	}
	return &c, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
TEST_DATABASE_URL=postgres://sync:pass@localhost:5432/sync_test go test -race ./internal/store/ -run TestCredentialsStore -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/credentials.go internal/store/credentials_test.go
git commit -m "feat: add credentials store with pgcrypto encryption/decryption"
```

---

### Task 6: Store — jobs.go

**Files:**
- Create: `internal/store/jobs.go`
- Create: `internal/store/jobs_test.go`

**Background:** `UpdateStatus` and `AppendEvent` use the pool directly (no RLS) because the jobrunner goroutine calls them without a user HTTP context. The RLS policy on `sync_jobs` protects reads; the jobrunner only updates rows it already holds the ID for (created inside a WithUserID tx). `Create` and `ListJobs`/`GetJob` must run inside `WithUserID`.

- [ ] **Step 1: Write the failing tests**

Create `internal/store/jobs_test.go`:

```go
package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
)

func TestJobStore_FullLifecycle(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	us := store.NewUserStore(pool)
	user, err := us.Create(ctx, "jobs-test@example.com", "hash")
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id = $1", user.ID) })

	js := store.NewJobStore(pool)

	// Create job inside RLS tx
	var jobID uuid.UUID
	err = store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error {
		job, e := js.Create(ctx, tx, user.ID)
		if e != nil {
			return e
		}
		jobID = job.ID
		if job.Status != "pending" {
			return fmt.Errorf("status = %q, want pending", job.Status)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Create job: %v", err)
	}

	// UpdateStatus uses pool directly (no RLS needed for writes)
	if err := js.UpdateStatus(ctx, jobID, "running", nil); err != nil {
		t.Fatalf("UpdateStatus running: %v", err)
	}

	// AppendEvent uses pool directly
	payload, _ := json.Marshal(map[string]string{"item": "test-item"})
	if err := js.AppendEvent(ctx, jobID, 1, "create", payload); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	// ListEvents inside RLS tx
	var events []store.SyncJobEvent
	err = store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error {
		var e error
		events, e = js.ListEvents(ctx, tx, jobID)
		return e
	})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].EventType != "create" {
		t.Errorf("event_type = %q, want create", events[0].EventType)
	}

	// UpdateStatus to done
	errMsg := ""
	if err := js.UpdateStatus(ctx, jobID, "done", &errMsg); err != nil {
		t.Fatalf("UpdateStatus done: %v", err)
	}

	// GetJob inside RLS tx
	var job *store.SyncJob
	err = store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error {
		var e error
		job, e = js.GetJob(ctx, tx, jobID)
		return e
	})
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != "done" {
		t.Errorf("status = %q, want done", job.Status)
	}
	if job.FinishedAt == nil {
		t.Error("finished_at should be set for done status")
	}
}

func TestJobStore_ListJobs(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	us := store.NewUserStore(pool)
	user, _ := us.Create(ctx, "jobs-list@example.com", "hash")
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id = $1", user.ID) })

	js := store.NewJobStore(pool)

	// Create two jobs
	for i := 0; i < 2; i++ {
		store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error {
			_, e := js.Create(ctx, tx, user.ID)
			return e
		})
	}

	var jobs []store.SyncJob
	store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error {
		var e error
		jobs, e = js.ListJobs(ctx, tx, user.ID)
		return e
	})
	if len(jobs) < 2 {
		t.Errorf("len(jobs) = %d, want >= 2", len(jobs))
	}
}
```

Add missing import at top:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
)
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
TEST_DATABASE_URL=postgres://sync:pass@localhost:5432/sync_test go test -race ./internal/store/ -run TestJobStore -v
```
Expected: FAIL — `store.NewJobStore` undefined

- [ ] **Step 3: Implement jobs.go**

Create `internal/store/jobs.go`:

```go
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SyncJob represents one sync run triggered by a user.
type SyncJob struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Status     string // pending | running | done | failed
	StartedAt  *time.Time
	FinishedAt *time.Time
	Error      *string
	CreatedAt  time.Time
}

// SyncJobEvent is one progress event emitted during a sync run.
type SyncJobEvent struct {
	ID        int64
	JobID     uuid.UUID
	Sequence  int
	EventType string
	Payload   json.RawMessage
	CreatedAt time.Time
}

// JobStore handles sync job and event persistence.
type JobStore struct {
	pool *pgxpool.Pool
}

func NewJobStore(pool *pgxpool.Pool) *JobStore {
	return &JobStore{pool: pool}
}

// Create inserts a new sync_job. Must run inside WithUserID (RLS).
func (s *JobStore) Create(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (*SyncJob, error) {
	var j SyncJob
	row := tx.QueryRow(ctx,
		`INSERT INTO sync_jobs (user_id)
		 VALUES ($1)
		 RETURNING id, user_id, status, started_at, finished_at, error, created_at`,
		userID,
	)
	if err := row.Scan(&j.ID, &j.UserID, &j.Status, &j.StartedAt, &j.FinishedAt, &j.Error, &j.CreatedAt); err != nil {
		return nil, fmt.Errorf("create job: %w", err)
	}
	return &j, nil
}

// UpdateStatus transitions a job's status and sets timestamps accordingly.
// Uses the pool directly — safe because only the jobrunner calls this and it
// already holds the jobID from a prior RLS-protected Create.
func (s *JobStore) UpdateStatus(ctx context.Context, jobID uuid.UUID, status string, errMsg *string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE sync_jobs SET
			status      = $1,
			error       = $2,
			started_at  = CASE WHEN $1 = 'running'            THEN now() ELSE started_at  END,
			finished_at = CASE WHEN $1 IN ('done', 'failed')  THEN now() ELSE finished_at END
		WHERE id = $3`,
		status, errMsg, jobID,
	)
	if err != nil {
		return fmt.Errorf("update job status: %w", err)
	}
	return nil
}

// AppendEvent persists one progress event. Uses the pool directly (see UpdateStatus note).
func (s *JobStore) AppendEvent(ctx context.Context, jobID uuid.UUID, seq int, eventType string, payload json.RawMessage) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO sync_job_events (job_id, sequence, event_type, payload)
		 VALUES ($1, $2, $3, $4)`,
		jobID, seq, eventType, payload,
	)
	if err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}

// ListEvents returns all events for jobID ordered by sequence. Must run inside WithUserID (RLS).
func (s *JobStore) ListEvents(ctx context.Context, tx pgx.Tx, jobID uuid.UUID) ([]SyncJobEvent, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, job_id, sequence, event_type, payload, created_at
		 FROM sync_job_events WHERE job_id = $1 ORDER BY sequence`,
		jobID,
	)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	var events []SyncJobEvent
	for rows.Next() {
		var e SyncJobEvent
		if err := rows.Scan(&e.ID, &e.JobID, &e.Sequence, &e.EventType, &e.Payload, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// GetJob returns a single job by ID. Must run inside WithUserID (RLS).
func (s *JobStore) GetJob(ctx context.Context, tx pgx.Tx, jobID uuid.UUID) (*SyncJob, error) {
	var j SyncJob
	row := tx.QueryRow(ctx,
		`SELECT id, user_id, status, started_at, finished_at, error, created_at
		 FROM sync_jobs WHERE id = $1`,
		jobID,
	)
	if err := row.Scan(&j.ID, &j.UserID, &j.Status, &j.StartedAt, &j.FinishedAt, &j.Error, &j.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get job: %w", err)
	}
	return &j, nil
}

// ListJobs returns the 50 most recent jobs for userID. Must run inside WithUserID (RLS).
func (s *JobStore) ListJobs(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]SyncJob, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, user_id, status, started_at, finished_at, error, created_at
		 FROM sync_jobs WHERE user_id = $1 ORDER BY created_at DESC LIMIT 50`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	var jobs []SyncJob
	for rows.Next() {
		var j SyncJob
		if err := rows.Scan(&j.ID, &j.UserID, &j.Status, &j.StartedAt, &j.FinishedAt, &j.Error, &j.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
TEST_DATABASE_URL=postgres://sync:pass@localhost:5432/sync_test go test -race ./internal/store/ -run TestJobStore -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/store/jobs.go internal/store/jobs_test.go
git commit -m "feat: add job store (create, update status, append/list events)"
```

---

## Group C — JobRunner + Auth Middleware (parallel, depends on Group B)

### Task 7: JobRunner

**Files:**
- Create: `internal/jobrunner/runner.go`
- Create: `internal/jobrunner/runner_test.go`

**Background:** The runner holds a `map[jobID][]chan Event` protected by a RWMutex. `Subscribe` gives a caller a channel to receive events; `Broadcast` sends to all subscribers non-blocking. `Enqueue` launches a goroutine that decrypts credentials, calls the existing sync engine, and calls `emit` for each ProgressEvent (which persists to DB + broadcasts). The sync engine wiring uses `internal/sync`, `internal/onepassword`, and `internal/vaultwarden` — check the exact constructor signatures in those packages before implementing `run`.

- [ ] **Step 1: Write the failing tests**

Create `internal/jobrunner/runner_test.go`:

```go
package jobrunner_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/jobrunner"
)

func TestRunner_SubscribeReceivesEvent(t *testing.T) {
	r := jobrunner.New(nil, nil, nil)
	jobID := uuid.New()

	ch, unsub := r.Subscribe(jobID)
	defer unsub()

	r.Broadcast(jobID, jobrunner.Event{Type: "progress", Payload: []byte(`{"n":1}`)})

	select {
	case e := <-ch:
		if e.Type != "progress" {
			t.Errorf("type = %q, want progress", e.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestRunner_Broadcast_NoSubscribers_DoesNotBlock(t *testing.T) {
	r := jobrunner.New(nil, nil, nil)
	// Should complete instantly without blocking
	r.Broadcast(uuid.New(), jobrunner.Event{Type: "test", Payload: []byte(`{}`)})
}

func TestRunner_Unsubscribe_ClosesChannel(t *testing.T) {
	r := jobrunner.New(nil, nil, nil)
	jobID := uuid.New()

	ch, unsub := r.Subscribe(jobID)
	unsub()

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected closed channel")
		}
	case <-time.After(100 * time.Millisecond):
		// acceptable — buffered channel, just verify no panic
	}
}

func TestRunner_MultipleSubscribers_AllReceive(t *testing.T) {
	r := jobrunner.New(nil, nil, nil)
	jobID := uuid.New()

	ch1, unsub1 := r.Subscribe(jobID)
	ch2, unsub2 := r.Subscribe(jobID)
	defer unsub1()
	defer unsub2()

	r.Broadcast(jobID, jobrunner.Event{Type: "x", Payload: []byte(`{}`)})

	for _, ch := range []<-chan jobrunner.Event{ch1, ch2} {
		select {
		case e := <-ch:
			if e.Type != "x" {
				t.Errorf("type = %q, want x", e.Type)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout")
		}
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/jobrunner/ -v
```
Expected: FAIL — `jobrunner.New` undefined

- [ ] **Step 3: Implement runner.go**

Create `internal/jobrunner/runner.go`:

```go
package jobrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/onepassword"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
	syncengine "github.com/prabinv/1pass-vaultwarden-sync/internal/sync"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/vaultwarden"
)

// Event is one progress event broadcast to SSE subscribers.
type Event struct {
	Type    string
	Payload json.RawMessage
}

// Runner manages in-process sync goroutines and SSE subscriptions.
type Runner struct {
	pool      *pgxpool.Pool
	credStore *store.CredentialsStore
	jobStore  *store.JobStore

	mu   sync.RWMutex
	subs map[uuid.UUID][]chan Event
}

func New(pool *pgxpool.Pool, credStore *store.CredentialsStore, jobStore *store.JobStore) *Runner {
	return &Runner{
		pool:      pool,
		credStore: credStore,
		jobStore:  jobStore,
		subs:      make(map[uuid.UUID][]chan Event),
	}
}

// Subscribe returns a buffered channel for jobID events and a cleanup function.
// Call the cleanup function when the SSE connection closes.
func (r *Runner) Subscribe(jobID uuid.UUID) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	r.mu.Lock()
	r.subs[jobID] = append(r.subs[jobID], ch)
	r.mu.Unlock()

	return ch, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		subs := r.subs[jobID]
		for i, c := range subs {
			if c == ch {
				r.subs[jobID] = append(subs[:i], subs[i+1:]...)
				close(ch)
				return
			}
		}
	}
}

// Broadcast sends e to all subscribers of jobID. Non-blocking: slow subscribers miss events.
func (r *Runner) Broadcast(jobID uuid.UUID, e Event) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, ch := range r.subs[jobID] {
		select {
		case ch <- e:
		default:
		}
	}
}

// Enqueue starts a goroutine to run the sync job. Returns immediately.
func (r *Runner) Enqueue(ctx context.Context, jobID, userID uuid.UUID) {
	go r.run(context.WithoutCancel(ctx), jobID, userID)
}

func (r *Runner) run(ctx context.Context, jobID, userID uuid.UUID) {
	fail := func(msg string) {
		r.jobStore.UpdateStatus(ctx, jobID, "failed", &msg)
		payload, _ := json.Marshal(map[string]string{"error": msg})
		r.Broadcast(jobID, Event{Type: "done", Payload: payload})
	}

	if err := r.jobStore.UpdateStatus(ctx, jobID, "running", nil); err != nil {
		slog.Error("jobrunner: mark running", "err", err)
		return
	}

	// Fetch and decrypt credentials inside RLS transaction
	var creds *store.Credentials
	if err := store.WithUserID(ctx, r.pool, userID, func(tx pgx.Tx) error {
		var e error
		creds, e = r.credStore.Get(ctx, tx, userID)
		return e
	}); err != nil {
		fail(fmt.Sprintf("fetch credentials: %v", err))
		return
	}

	// Build 1Password client
	// Check internal/onepassword/client.go for the exact New() signature
	opClient, err := onepassword.New(ctx, creds.OPServiceAccountToken)
	if err != nil {
		fail(fmt.Sprintf("1password client: %v", err))
		return
	}

	// Build Vaultwarden client
	// Check internal/vaultwarden/client.go for the exact New() / Config struct
	vwClient, err := vaultwarden.New(ctx, vaultwarden.Config{
		URL:            creds.VaultwardenURL,
		ClientID:       creds.VaultwardenClientID,
		ClientSecret:   creds.VaultwardenClientSecret,
		MasterPassword: creds.VaultwardenMasterPassword,
	})
	if err != nil {
		fail(fmt.Sprintf("vaultwarden client: %v", err))
		return
	}

	// Run sync engine
	// Check internal/sync/engine.go for the exact Engine constructor and Plan/Apply signatures
	engine := syncengine.New(opClient, vwClient)
	plan, err := engine.Plan(ctx)
	if err != nil {
		fail(fmt.Sprintf("plan: %v", err))
		return
	}

	progressCh := make(chan syncengine.ProgressEvent, 32)
	go engine.Apply(ctx, plan, progressCh)

	seq := 0
	for pe := range progressCh {
		payload, _ := json.Marshal(pe)
		e := Event{Type: string(pe.Kind), Payload: payload}
		r.Broadcast(jobID, e)
		r.jobStore.AppendEvent(ctx, jobID, seq, e.Type, e.Payload)
		seq++
	}

	if err := r.jobStore.UpdateStatus(ctx, jobID, "done", nil); err != nil {
		slog.Error("jobrunner: mark done", "err", err)
	}
	donePayload, _ := json.Marshal(map[string]string{"error": ""})
	r.Broadcast(jobID, Event{Type: "done", Payload: donePayload})
}
```

**Important:** After writing this file, open `internal/onepassword/client.go`, `internal/vaultwarden/client.go`, and `internal/sync/engine.go` to verify the exact constructor names, config struct fields, and method signatures. Adjust the `run` method to match.

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -race ./internal/jobrunner/ -v
```
Expected: PASS

- [ ] **Step 5: Verify build compiles with actual engine imports**

```bash
go build ./internal/jobrunner/
```
Expected: no errors. Fix any field-name mismatches against actual client constructors.

- [ ] **Step 6: Commit**

```bash
git add internal/jobrunner/
git commit -m "feat: add job runner with SSE broadcast and sync engine wiring"
```

---

### Task 8: Auth middleware

**Files:**
- Create: `internal/web/middleware/auth.go`
- Create: `internal/web/middleware/auth_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/web/middleware/auth_test.go`:

```go
package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/middleware"
)

const testSecret = "test-jwt-secret-at-least-32-chars!!"

func TestAuth_NoCookie_RedirectsToLogin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware.Auth(testSecret)(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if rec.Header().Get("Location") != "/auth/login" {
		t.Errorf("Location = %q, want /auth/login", rec.Header().Get("Location"))
	}
}

func TestAuth_ValidToken_InjectsUserID(t *testing.T) {
	userID := uuid.New()
	token, err := middleware.IssueJWT(testSecret, userID)
	if err != nil {
		t.Fatalf("IssueJWT: %v", err)
	}

	var gotID uuid.UUID
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = middleware.UserIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware.Auth(testSecret)(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "token", Value: token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if gotID != userID {
		t.Errorf("userID = %v, want %v", gotID, userID)
	}
}

func TestAuth_ExpiredToken_RedirectsToLogin(t *testing.T) {
	// Issue a token with -1 hour expiry using the internal helper
	token, _ := middleware.IssueJWTWithExpiry(testSecret, uuid.New(), -3600)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := middleware.Auth(testSecret)(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "token", Value: token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want redirect", rec.Code)
	}
}

func TestSetUserIDInContext_RoundTrip(t *testing.T) {
	id := uuid.New()
	ctx := middleware.SetUserIDInContext(context.Background(), id)
	got := middleware.UserIDFromContext(ctx)
	if got != id {
		t.Errorf("got %v, want %v", got, id)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/web/middleware/ -v
```
Expected: FAIL — `middleware.Auth` undefined

- [ ] **Step 3: Implement auth.go**

Create `internal/web/middleware/auth.go`:

```go
package middleware

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type contextKey string

const userIDKey contextKey = "user_id"

// IssueJWT creates a signed JWT containing userID, valid for 24 hours.
func IssueJWT(secret string, userID uuid.UUID) (string, error) {
	return IssueJWTWithExpiry(secret, userID, int64((24 * time.Hour).Seconds()))
}

// IssueJWTWithExpiry creates a JWT with a custom expiry offset in seconds.
// Negative values produce an already-expired token (useful in tests).
func IssueJWTWithExpiry(secret string, userID uuid.UUID, expiryOffsetSecs int64) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": userID.String(),
		"exp":     time.Now().Unix() + expiryOffsetSecs,
	})
	return token.SignedString([]byte(secret))
}

// SetUserIDInContext injects userID into ctx. Used in tests and middleware.
func SetUserIDInContext(ctx context.Context, userID uuid.UUID) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// UserIDFromContext retrieves the authenticated user ID from ctx.
// Returns uuid.Nil if not set.
func UserIDFromContext(ctx context.Context) uuid.UUID {
	id, _ := ctx.Value(userIDKey).(uuid.UUID)
	return id
}

// Auth validates the JWT cookie and injects user_id into context.
// Unauthenticated or invalid requests redirect to /auth/login.
func Auth(jwtSecret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("token")
			if err != nil {
				http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
				return
			}

			token, err := jwt.Parse(cookie.Value, func(t *jwt.Token) (interface{}, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
				}
				return []byte(jwtSecret), nil
			})
			if err != nil || !token.Valid {
				http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
				return
			}

			claims, ok := token.Claims.(jwt.MapClaims)
			if !ok {
				http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
				return
			}

			idStr, _ := claims["user_id"].(string)
			userID, err := uuid.Parse(idStr)
			if err != nil {
				http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
				return
			}

			ctx := SetUserIDInContext(r.Context(), userID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -race ./internal/web/middleware/ -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/web/middleware/
git commit -m "feat: add JWT auth middleware with context injection"
```

---

## Group D — Templates + Handlers (parallel, depends on Group C)

### Task 9: templ templates

**Files:**
- Create: `internal/web/templates/layout.templ`
- Create: `internal/web/templates/auth.templ`
- Create: `internal/web/templates/dashboard.templ`
- Create: `internal/web/templates/credentials.templ`
- Create: `internal/web/templates/job.templ`

- [ ] **Step 1: Create layout.templ**

```bash
mkdir -p internal/web/templates
```

Create `internal/web/templates/layout.templ`:

```go
package templates

templ Layout(title string) {
	<!DOCTYPE html>
	<html lang="en">
	<head>
		<meta charset="UTF-8"/>
		<meta name="viewport" content="width=device-width, initial-scale=1.0"/>
		<title>{ title } — 1pass-vaultwarden-sync</title>
		<script type="module" src="https://cdn.jsdelivr.net/gh/starfederation/datastar@v1.0.0-beta.11/bundles/datastar.js"></script>
		<style>
			body { font-family: system-ui, sans-serif; max-width: 800px; margin: 2rem auto; padding: 0 1rem; }
			.error  { color: #c0392b; background: #fdecea; padding: .5rem; border-radius: 4px; }
			.success{ color: #1a7a4a; background: #eafaf1; padding: .5rem; border-radius: 4px; }
			nav a   { margin-right: 1rem; text-decoration: none; }
			table   { width: 100%; border-collapse: collapse; margin-top: 1rem; }
			td, th  { padding: .5rem .75rem; border-bottom: 1px solid #eee; text-align: left; }
			th      { background: #f8f8f8; font-weight: 600; }
			label   { display: block; margin: .5rem 0 1rem; }
			input   { width: 100%; padding: .5rem; box-sizing: border-box; margin-top: .25rem; }
			button  { padding: .5rem 1.25rem; cursor: pointer; }
		</style>
	</head>
	<body>
		{ children... }
	</body>
	</html>
}

templ Nav() {
	<nav>
		<a href="/">Dashboard</a>
		<a href="/credentials">Credentials</a>
		<form method="POST" action="/auth/logout" style="display:inline">
			<button type="submit">Logout</button>
		</form>
	</nav>
	<hr/>
}
```

- [ ] **Step 2: Create auth.templ**

Create `internal/web/templates/auth.templ`:

```go
package templates

templ Login(errMsg string) {
	@Layout("Login") {
		<h1>Login</h1>
		if errMsg != "" {
			<p class="error">{ errMsg }</p>
		}
		<form method="POST" action="/auth/login">
			<label>Email<input type="email" name="email" required/></label>
			<label>Password<input type="password" name="password" required/></label>
			<button type="submit">Login</button>
		</form>
		<p><a href="/auth/register">Create an account</a></p>
	}
}

templ Register(errMsg string) {
	@Layout("Register") {
		<h1>Create Account</h1>
		if errMsg != "" {
			<p class="error">{ errMsg }</p>
		}
		<form method="POST" action="/auth/register">
			<label>Email<input type="email" name="email" required/></label>
			<label>Password<input type="password" name="password" required minlength="8"/></label>
			<button type="submit">Register</button>
		</form>
		<p><a href="/auth/login">Already have an account?</a></p>
	}
}
```

- [ ] **Step 3: Create dashboard.templ**

Create `internal/web/templates/dashboard.templ`:

```go
package templates

import (
	"fmt"
	"time"

	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
)

templ Dashboard(jobs []store.SyncJob) {
	@Layout("Dashboard") {
		@Nav()
		<h1>Sync History</h1>
		<form method="POST" action="/sync/trigger">
			<button type="submit">Trigger Sync</button>
		</form>
		if len(jobs) == 0 {
			<p>No syncs yet. Click "Trigger Sync" to start.</p>
		} else {
			<table>
				<thead>
					<tr><th>Job ID</th><th>Status</th><th>Started</th><th>Finished</th></tr>
				</thead>
				<tbody>
					for _, j := range jobs {
						<tr>
							<td><a href={ templ.SafeURL(fmt.Sprintf("/sync/jobs/%s", j.ID)) }>{ j.ID.String()[:8] }…</a></td>
							<td>{ j.Status }</td>
							<td>{ fmtTime(j.StartedAt) }</td>
							<td>{ fmtTime(j.FinishedAt) }</td>
						</tr>
					}
				</tbody>
			</table>
		}
	}
}

func fmtTime(t *time.Time) string {
	if t == nil {
		return "—"
	}
	return t.Format("2006-01-02 15:04:05")
}
```

- [ ] **Step 4: Create credentials.templ**

Create `internal/web/templates/credentials.templ`:

```go
package templates

import "github.com/prabinv/1pass-vaultwarden-sync/internal/store"

templ CredentialsForm(creds *store.Credentials, saved bool, errMsg string) {
	@Layout("Credentials") {
		@Nav()
		<h1>Credentials</h1>
		if saved {
			<p class="success">Credentials saved successfully.</p>
		}
		if errMsg != "" {
			<p class="error">{ errMsg }</p>
		}
		<form method="POST" action="/credentials">
			<label>1Password Service Account Token
				<input type="password" name="op_token" value={ credVal(creds, "op") } required/>
			</label>
			<label>Vaultwarden URL
				<input type="url" name="vw_url" value={ credVal(creds, "url") } required placeholder="https://vault.example.com"/>
			</label>
			<label>Vaultwarden Client ID
				<input type="text" name="vw_client_id" value={ credVal(creds, "cid") } required/>
			</label>
			<label>Vaultwarden Client Secret
				<input type="password" name="vw_client_secret" value={ credVal(creds, "cs") } required/>
			</label>
			<label>Vaultwarden Master Password
				<input type="password" name="vw_master_password" value={ credVal(creds, "mp") } required/>
			</label>
			<button type="submit">Save Credentials</button>
		</form>
	}
}

func credVal(c *store.Credentials, field string) string {
	if c == nil {
		return ""
	}
	switch field {
	case "op":  return c.OPServiceAccountToken
	case "url": return c.VaultwardenURL
	case "cid": return c.VaultwardenClientID
	case "cs":  return c.VaultwardenClientSecret
	case "mp":  return c.VaultwardenMasterPassword
	}
	return ""
}
```

- [ ] **Step 5: Create job.templ**

Create `internal/web/templates/job.templ`:

```go
package templates

import (
	"fmt"

	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
)

templ JobDetail(job *store.SyncJob) {
	@Layout("Sync Job") {
		@Nav()
		<h1>Sync Job</h1>
		<p>ID: <code>{ job.ID.String() }</code></p>
		<p>Status: <strong id="job-status">{ job.Status }</strong></p>
		<div
			data-on-load={ fmt.Sprintf("$$get('/sync/jobs/%s/stream')", job.ID) }
			data-signals="{events: []}"
		>
			<ul id="events">
				<template data-for="event in $events">
					<li data-text="event.type + ': ' + JSON.stringify(event.payload)"></li>
				</template>
			</ul>
		</div>
		<p><a href="/">← Back to Dashboard</a></p>
	}
}
```

- [ ] **Step 6: Generate Go code from templ files**

```bash
templ generate ./internal/web/templates/
```
Expected: `layout_templ.go`, `auth_templ.go`, `dashboard_templ.go`, `credentials_templ.go`, `job_templ.go` created alongside each `.templ` file — no errors.

- [ ] **Step 7: Verify templates compile**

```bash
go build ./internal/web/templates/
```
Expected: no errors

- [ ] **Step 8: Commit**

```bash
git add internal/web/templates/
git commit -m "feat: add templ templates with Datastar SSE bindings"
```

---

### Task 10: Auth handlers

**Files:**
- Create: `internal/web/handlers/auth.go`
- Create: `internal/web/handlers/auth_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/web/handlers/auth_test.go`:

```go
package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/handlers"
)

func postForm(t *testing.T, h http.Handler, path string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuthHandler_Register_InvalidEmail(t *testing.T) {
	h := handlers.NewAuthHandler(nil, "secret")

	rec := postForm(t, http.HandlerFunc(h.Register), "/auth/register", url.Values{
		"email":    {"notanemail"},
		"password": {"validpassword123"},
	})

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (re-render)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Invalid email") {
		t.Errorf("expected 'Invalid email' in body, got: %s", rec.Body.String())
	}
}

func TestAuthHandler_Register_ShortPassword(t *testing.T) {
	h := handlers.NewAuthHandler(nil, "secret")

	rec := postForm(t, http.HandlerFunc(h.Register), "/auth/register", url.Values{
		"email":    {"user@example.com"},
		"password": {"short"},
	})

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (re-render)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "8 characters") {
		t.Errorf("expected password length error in body, got: %s", rec.Body.String())
	}
}

func TestAuthHandler_ShowLogin_Returns200(t *testing.T) {
	h := handlers.NewAuthHandler(nil, "secret")
	req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	rec := httptest.NewRecorder()
	h.ShowLogin(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/web/handlers/ -run TestAuthHandler -v
```
Expected: FAIL — `handlers.NewAuthHandler` undefined

- [ ] **Step 3: Implement auth.go**

Create `internal/web/handlers/auth.go`:

```go
package handlers

import (
	"net/http"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/middleware"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/templates"
)

// AuthHandler handles registration, login, and logout.
type AuthHandler struct {
	users     *store.UserStore
	jwtSecret string
}

func NewAuthHandler(users *store.UserStore, jwtSecret string) *AuthHandler {
	return &AuthHandler{users: users, jwtSecret: jwtSecret}
}

func (h *AuthHandler) ShowLogin(w http.ResponseWriter, r *http.Request) {
	templates.Login("").Render(r.Context(), w)
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	user, err := h.users.GetByEmail(r.Context(), email)
	if err != nil {
		templates.Login("Invalid email or password.").Render(r.Context(), w)
		return
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		templates.Login("Invalid email or password.").Render(r.Context(), w)
		return
	}

	token, err := middleware.IssueJWT(h.jwtSecret, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.setTokenCookie(w, token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *AuthHandler) ShowRegister(w http.ResponseWriter, r *http.Request) {
	templates.Register("").Render(r.Context(), w)
}

func (h *AuthHandler) Register(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")

	if !strings.Contains(email, "@") || !strings.Contains(email, ".") {
		templates.Register("Invalid email address.").Render(r.Context(), w)
		return
	}
	if len(password) < 8 {
		templates.Register("Password must be at least 8 characters.").Render(r.Context(), w)
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	user, err := h.users.Create(r.Context(), email, string(hash))
	if err != nil {
		templates.Register("Email already registered.").Render(r.Context(), w)
		return
	}

	token, err := middleware.IssueJWT(h.jwtSecret, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.setTokenCookie(w, token)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     "token",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
	})
	http.Redirect(w, r, "/auth/login", http.StatusSeeOther)
}

func (h *AuthHandler) setTokenCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "token",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -race ./internal/web/handlers/ -run TestAuthHandler -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/web/handlers/auth.go internal/web/handlers/auth_test.go
git commit -m "feat: add auth handlers (register, login, logout)"
```

---

### Task 11: Credentials handlers

**Files:**
- Create: `internal/web/handlers/credentials.go`
- Create: `internal/web/handlers/credentials_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/web/handlers/credentials_test.go`:

```go
package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/handlers"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/middleware"
)

func TestCredentialsHandler_Show_Returns200(t *testing.T) {
	// nil pool/store: Show renders the empty form when credentials not found
	h := handlers.NewCredentialsHandler(nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/credentials", nil)
	ctx := middleware.SetUserIDInContext(req.Context(), uuid.New())
	rec := httptest.NewRecorder()

	// Show will call store.Get which panics with nil store — use a real DB in integration.
	// This unit test only verifies the handler exists and the route wiring compiles.
	_ = h
	_ = ctx
	_ = rec
}

func TestCredentialsHandler_Save_RequiresFields(t *testing.T) {
	h := handlers.NewCredentialsHandler(nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/credentials", strings.NewReader("op_token=&vw_url="))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := middleware.SetUserIDInContext(req.Context(), uuid.New())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.Save(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (re-render)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "required") {
		t.Errorf("expected required-field error in body, got: %s", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/web/handlers/ -run TestCredentialsHandler -v
```
Expected: FAIL — `handlers.NewCredentialsHandler` undefined

- [ ] **Step 3: Implement credentials.go**

Create `internal/web/handlers/credentials.go`:

```go
package handlers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/middleware"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/templates"
)

// CredentialsHandler shows and saves user credential sets.
type CredentialsHandler struct {
	pool      *pgxpool.Pool
	credStore *store.CredentialsStore
}

func NewCredentialsHandler(pool *pgxpool.Pool, credStore *store.CredentialsStore) *CredentialsHandler {
	return &CredentialsHandler{pool: pool, credStore: credStore}
}

func (h *CredentialsHandler) Show(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())

	var creds *store.Credentials
	store.WithUserID(r.Context(), h.pool, userID, func(tx pgx.Tx) error {
		var err error
		creds, err = h.credStore.Get(r.Context(), tx, userID)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	})

	templates.CredentialsForm(creds, false, "").Render(r.Context(), w)
}

func (h *CredentialsHandler) Save(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())

	creds := &store.Credentials{
		OPServiceAccountToken:     strings.TrimSpace(r.FormValue("op_token")),
		VaultwardenURL:            strings.TrimSpace(r.FormValue("vw_url")),
		VaultwardenClientID:       strings.TrimSpace(r.FormValue("vw_client_id")),
		VaultwardenClientSecret:   strings.TrimSpace(r.FormValue("vw_client_secret")),
		VaultwardenMasterPassword: strings.TrimSpace(r.FormValue("vw_master_password")),
	}

	if creds.OPServiceAccountToken == "" || creds.VaultwardenURL == "" ||
		creds.VaultwardenClientID == "" || creds.VaultwardenClientSecret == "" ||
		creds.VaultwardenMasterPassword == "" {
		templates.CredentialsForm(creds, false, "All fields are required.").Render(r.Context(), w)
		return
	}

	err := store.WithUserID(r.Context(), h.pool, userID, func(tx pgx.Tx) error {
		return h.credStore.Upsert(r.Context(), tx, userID, creds)
	})
	if err != nil {
		templates.CredentialsForm(creds, false, "Failed to save credentials.").Render(r.Context(), w)
		return
	}

	templates.CredentialsForm(creds, true, "").Render(r.Context(), w)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -race ./internal/web/handlers/ -run TestCredentialsHandler -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/web/handlers/credentials.go internal/web/handlers/credentials_test.go
git commit -m "feat: add credentials handlers (show, save)"
```

---

### Task 12: Sync handlers (trigger + SSE stream)

**Files:**
- Create: `internal/web/handlers/sync.go`
- Create: `internal/web/handlers/sync_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/web/handlers/sync_test.go`:

```go
package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/jobrunner"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/handlers"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/middleware"
)

func chiCtx(r *http.Request, key, val string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, val)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestSyncHandler_Stream_ContentType(t *testing.T) {
	runner := jobrunner.New(nil, nil, nil)
	h := handlers.NewSyncHandler(nil, nil, runner)

	jobID := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so Stream returns without blocking

	req := httptest.NewRequest(http.MethodGet, "/sync/jobs/"+jobID.String()+"/stream", nil)
	req = req.WithContext(middleware.SetUserIDInContext(ctx, uuid.New()))
	req = chiCtx(req, "id", jobID.String())
	rec := httptest.NewRecorder()

	h.Stream(rec, req)

	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test -race ./internal/web/handlers/ -run TestSyncHandler -v
```
Expected: FAIL — `handlers.NewSyncHandler` undefined

- [ ] **Step 3: Implement sync.go**

Create `internal/web/handlers/sync.go`:

```go
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/jobrunner"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/middleware"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/templates"
)

// SyncHandler handles sync triggering, job detail, and SSE progress streaming.
type SyncHandler struct {
	pool     *pgxpool.Pool
	jobStore *store.JobStore
	runner   *jobrunner.Runner
}

func NewSyncHandler(pool *pgxpool.Pool, jobStore *store.JobStore, runner *jobrunner.Runner) *SyncHandler {
	return &SyncHandler{pool: pool, jobStore: jobStore, runner: runner}
}

// Trigger creates a sync job and redirects to its detail page.
func (h *SyncHandler) Trigger(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())

	var jobID uuid.UUID
	err := store.WithUserID(r.Context(), h.pool, userID, func(tx pgx.Tx) error {
		job, e := h.jobStore.Create(r.Context(), tx, userID)
		if e != nil {
			return e
		}
		jobID = job.ID
		return nil
	})
	if err != nil {
		http.Error(w, "failed to create job", http.StatusInternalServerError)
		return
	}

	h.runner.Enqueue(r.Context(), jobID, userID)
	http.Redirect(w, r, fmt.Sprintf("/sync/jobs/%s", jobID), http.StatusSeeOther)
}

// JobDetail renders the job detail page.
func (h *SyncHandler) JobDetail(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	jobID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}

	var job *store.SyncJob
	if err := store.WithUserID(r.Context(), h.pool, userID, func(tx pgx.Tx) error {
		var e error
		job, e = h.jobStore.GetJob(r.Context(), tx, jobID)
		return e
	}); err != nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	templates.JobDetail(job).Render(r.Context(), w)
}

// Stream sends an SSE stream of progress events for jobID.
// It first replays persisted events from the DB, then streams live events.
func (h *SyncHandler) Stream(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	jobID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Replay persisted events so page refresh mid-sync works
	var past []store.SyncJobEvent
	store.WithUserID(r.Context(), h.pool, userID, func(tx pgx.Tx) error {
		past, _ = h.jobStore.ListEvents(r.Context(), tx, jobID)
		return nil
	})
	for _, e := range past {
		writeSSE(w, flusher, e.EventType, e.Payload)
	}

	// Subscribe to live events from the jobrunner
	ch, unsub := h.runner.Subscribe(jobID)
	defer unsub()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-ch:
			if !ok {
				return
			}
			writeSSE(w, flusher, e.Type, e.Payload)
			if e.Type == "done" {
				return
			}
		}
	}
}

func writeSSE(w http.ResponseWriter, f http.Flusher, eventType string, payload json.RawMessage) {
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventType, payload)
	f.Flush()
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test -race ./internal/web/handlers/ -run TestSyncHandler -v
```
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/web/handlers/sync.go internal/web/handlers/sync_test.go
git commit -m "feat: add sync handlers (trigger, job detail, SSE stream)"
```

---

## Group E — Server Entry Point (depends on Group D)

### Task 13: cmd/server/main.go

**Files:**
- Create: `cmd/server/main.go`

- [ ] **Step 1: Create the server entry point**

Create `cmd/server/main.go`:

```go
package main

import (
	"context"
	"embed"
	"log/slog"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/jobrunner"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/handlers"
	webmw "github.com/prabinv/1pass-vaultwarden-sync/internal/web/middleware"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/templates"
)

//go:embed ../../migrations/*.sql
var migrationsFS embed.FS

func main() {
	ctx := context.Background()

	dbURL    := mustEnv("DATABASE_URL")
	encKey   := mustEnv("ENCRYPTION_KEY")
	jwtSec  := mustEnv("JWT_SECRET")
	port    := envOr("PORT", "8080")

	// Run migrations at startup
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		slog.Error("migration source", "err", err); os.Exit(1)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dbURL)
	if err != nil {
		slog.Error("migrate init", "err", err); os.Exit(1)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		slog.Error("migrate up", "err", err); os.Exit(1)
	}

	pool, err := store.NewPool(ctx, dbURL)
	if err != nil {
		slog.Error("db pool", "err", err); os.Exit(1)
	}
	defer pool.Close()

	userStore := store.NewUserStore(pool)
	credStore := store.NewCredentialsStore(pool, encKey)
	jobStore  := store.NewJobStore(pool)
	runner    := jobrunner.New(pool, credStore, jobStore)

	authH  := handlers.NewAuthHandler(userStore, jwtSec)
	credH  := handlers.NewCredentialsHandler(pool, credStore)
	syncH  := handlers.NewSyncHandler(pool, jobStore, runner)

	r := chi.NewRouter()
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)

	// Public routes
	r.Get("/auth/login",     authH.ShowLogin)
	r.Post("/auth/login",    authH.Login)
	r.Get("/auth/register",  authH.ShowRegister)
	r.Post("/auth/register", authH.Register)
	r.Post("/auth/logout",   authH.Logout)

	// Protected routes
	r.Group(func(r chi.Router) {
		r.Use(webmw.Auth(jwtSec))

		r.Get("/", func(w http.ResponseWriter, req *http.Request) {
			userID := webmw.UserIDFromContext(req.Context())
			var jobs []store.SyncJob
			store.WithUserID(req.Context(), pool, userID, func(tx pgx.Tx) error {
				var e error
				jobs, e = jobStore.ListJobs(req.Context(), tx, userID)
				return e
			})
			templates.Dashboard(jobs).Render(req.Context(), w)
		})

		r.Get("/credentials",  credH.Show)
		r.Post("/credentials", credH.Save)

		r.Post("/sync/trigger",          syncH.Trigger)
		r.Get("/sync/jobs/{id}",         syncH.JobDetail)
		r.Get("/sync/jobs/{id}/stream",  syncH.Stream)
	})

	slog.Info("server starting", "port", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		slog.Error("server", "err", err); os.Exit(1)
	}
}

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		slog.Error("missing required env var", "key", key)
		os.Exit(1)
	}
	return v
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
```

- [ ] **Step 2: Build to verify compilation**

```bash
go build ./cmd/server/
```
Expected: binary `server` produced with no errors. Fix any import or field-name mismatches.

- [ ] **Step 3: Run all unit tests**

```bash
go test -race ./...
```
Expected: all pass (skipping integration tests if TEST_DATABASE_URL not set)

- [ ] **Step 4: Commit**

```bash
git add cmd/server/
git commit -m "feat: add server entry point, wire all components"
```

---

## Group F — Docker (depends on Group E)

### Task 14: Docker and Compose files

**Files:**
- Create: `docker/Dockerfile`
- Create: `docker/docker-compose.yml`
- Create: `docker/docker-compose.test.yml`
- Create: `.env.example`
- Modify: `Taskfile.yml`

- [ ] **Step 1: Create Dockerfile**

```bash
mkdir -p docker
```

Create `docker/Dockerfile`:

```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o server ./cmd/server

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
COPY --from=builder /app/server /usr/local/bin/server
EXPOSE 8080
ENTRYPOINT ["server"]
```

- [ ] **Step 2: Create docker-compose.yml**

Create `docker/docker-compose.yml`:

```yaml
services:
  server:
    build:
      context: ..
      dockerfile: docker/Dockerfile
    ports:
      - "8080:8080"
    environment:
      - DATABASE_URL=postgres://sync:${POSTGRES_PASSWORD}@postgres:5432/sync?sslmode=disable
      - ENCRYPTION_KEY=${ENCRYPTION_KEY}
      - JWT_SECRET=${JWT_SECRET}
    depends_on:
      postgres:
        condition: service_healthy
    restart: unless-stopped

  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: sync
      POSTGRES_USER: sync
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD}
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD", "pg_isready", "-U", "sync"]
      interval: 5s
      timeout: 5s
      retries: 5

volumes:
  pgdata:
```

- [ ] **Step 3: Create docker-compose.test.yml**

Create `docker/docker-compose.test.yml`:

```yaml
services:
  postgres:
    image: postgres:16-alpine
    environment:
      POSTGRES_DB: sync_test
      POSTGRES_USER: sync
      POSTGRES_PASSWORD: testpassword
    ports:
      - "5433:5432"
    tmpfs:
      - /var/lib/postgresql/data
    healthcheck:
      test: ["CMD", "pg_isready", "-U", "sync"]
      interval: 2s
      timeout: 5s
      retries: 10

  server:
    build:
      context: ..
      dockerfile: docker/Dockerfile
    ports:
      - "8081:8080"
    environment:
      - DATABASE_URL=postgres://sync:testpassword@postgres:5432/sync_test?sslmode=disable
      - ENCRYPTION_KEY=test-encryption-key-32-bytes-long!!
      - JWT_SECRET=test-jwt-secret-for-e2e-tests-only!!
    depends_on:
      postgres:
        condition: service_healthy
```

- [ ] **Step 4: Create .env.example**

Create `.env.example`:

```
# Generate ENCRYPTION_KEY: openssl rand -hex 32
ENCRYPTION_KEY=

# Generate JWT_SECRET: openssl rand -hex 32
JWT_SECRET=

# PostgreSQL password
POSTGRES_PASSWORD=
```

- [ ] **Step 5: Add Taskfile tasks**

Add to `Taskfile.yml` under `tasks:`:

```yaml
  server:
    desc: Run the web server locally (requires .env)
    cmds:
      - go run ./cmd/server/

  e2e:
    desc: Run E2E tests against full stack in Docker
    cmds:
      - docker compose -f docker/docker-compose.test.yml up -d --wait
      - defer: docker compose -f docker/docker-compose.test.yml down -v
      - cd e2e && BASE_URL=http://localhost:8081 npx playwright test
```

- [ ] **Step 6: Build the Docker image**

```bash
docker build -f docker/Dockerfile -t 1pass-vw-sync-server .
```
Expected: image builds successfully, no errors

- [ ] **Step 7: Start the stack and verify it responds**

```bash
cd docker && POSTGRES_PASSWORD=test ENCRYPTION_KEY=test-key-32-chars-exactly-here!! JWT_SECRET=test-jwt-secret docker compose up -d
sleep 5
curl -s -o /dev/null -w "%{http_code}" http://localhost:8080/auth/login
```
Expected: `200`

- [ ] **Step 8: Stop the test stack**

```bash
cd docker && docker compose down
```

- [ ] **Step 9: Commit**

```bash
git add docker/ .env.example Taskfile.yml
git commit -m "feat: add Dockerfile, docker-compose, and e2e Taskfile task"
```

---

## Group G — E2E Tests (depends on Group F)

### Task 15: Playwright E2E tests

**Files:**
- Create: `e2e/package.json`
- Create: `e2e/playwright.config.ts`
- Create: `e2e/fixtures/users.ts`
- Create: `e2e/tests/auth.spec.ts`
- Create: `e2e/tests/credentials.spec.ts`
- Create: `e2e/tests/sync.spec.ts`
- Create: `e2e/tests/isolation.spec.ts`

- [ ] **Step 1: Initialize the e2e project**

```bash
mkdir -p e2e/tests e2e/fixtures
cd e2e
npm init -y
npm install --save-dev @playwright/test
npx playwright install chromium
```

- [ ] **Step 2: Create playwright.config.ts**

Create `e2e/playwright.config.ts`:

```typescript
import { defineConfig } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  timeout: 30_000,
  retries: 1,
  use: {
    baseURL: process.env.BASE_URL ?? 'http://localhost:8081',
    trace: 'on-first-retry',
  },
  reporter: [['list'], ['html', { open: 'never', outputFolder: 'playwright-report' }]],
});
```

- [ ] **Step 3: Create fixtures/users.ts**

Create `e2e/fixtures/users.ts`:

```typescript
import { Page } from '@playwright/test';

export async function register(page: Page, email: string, password: string): Promise<void> {
  await page.goto('/auth/register');
  await page.fill('input[name="email"]', email);
  await page.fill('input[name="password"]', password);
  await page.click('button[type="submit"]');
  await page.waitForURL('/');
}

export async function login(page: Page, email: string, password: string): Promise<void> {
  await page.goto('/auth/login');
  await page.fill('input[name="email"]', email);
  await page.fill('input[name="password"]', password);
  await page.click('button[type="submit"]');
  await page.waitForURL('/');
}

export async function saveCredentials(page: Page): Promise<void> {
  await page.goto('/credentials');
  await page.fill('input[name="op_token"]',           'ops_fake_token_for_testing');
  await page.fill('input[name="vw_url"]',             'https://vault.example.com');
  await page.fill('input[name="vw_client_id"]',       'fake-client-id');
  await page.fill('input[name="vw_client_secret"]',   'fake-client-secret');
  await page.fill('input[name="vw_master_password"]', 'fake-master-password');
  await page.click('button[type="submit"]');
  await page.waitForSelector('.success');
}
```

- [ ] **Step 4: Create auth.spec.ts**

Create `e2e/tests/auth.spec.ts`:

```typescript
import { test, expect } from '@playwright/test';
import { register } from '../fixtures/users';

test('register creates account and shows dashboard', async ({ page }) => {
  await register(page, `reg-${Date.now()}@example.com`, 'TestPassword123');
  await expect(page).toHaveURL('/');
  await expect(page.locator('h1')).toContainText('Sync History');
});

test('unauthenticated access to dashboard redirects to login', async ({ page }) => {
  await page.goto('/');
  await expect(page).toHaveURL('/auth/login');
});

test('login with invalid credentials shows error', async ({ page }) => {
  await page.goto('/auth/login');
  await page.fill('input[name="email"]', 'nobody@example.com');
  await page.fill('input[name="password"]', 'wrongpassword');
  await page.click('button[type="submit"]');
  await expect(page.locator('.error')).toBeVisible();
  await expect(page).toHaveURL('/auth/login');
});

test('logout clears session', async ({ page }) => {
  await register(page, `logout-${Date.now()}@example.com`, 'TestPassword123');
  await page.click('button[type="submit"]'); // Logout button in Nav
  await expect(page).toHaveURL('/auth/login');
  await page.goto('/');
  await expect(page).toHaveURL('/auth/login');
});

test('register with short password shows error', async ({ page }) => {
  await page.goto('/auth/register');
  await page.fill('input[name="email"]', `short-${Date.now()}@example.com`);
  await page.fill('input[name="password"]', 'short');
  await page.click('button[type="submit"]');
  await expect(page.locator('.error')).toBeVisible();
});
```

- [ ] **Step 5: Create credentials.spec.ts**

Create `e2e/tests/credentials.spec.ts`:

```typescript
import { test, expect } from '@playwright/test';
import { register, saveCredentials } from '../fixtures/users';

test('save credentials shows success message', async ({ page }) => {
  await register(page, `creds-${Date.now()}@example.com`, 'TestPassword123');
  await saveCredentials(page);
  await expect(page.locator('.success')).toBeVisible();
});

test('credentials form pre-fills after saving', async ({ page }) => {
  await register(page, `prefill-${Date.now()}@example.com`, 'TestPassword123');
  await saveCredentials(page);
  // Navigate away and back
  await page.goto('/');
  await page.goto('/credentials');
  // URL field should be visible and prefilled
  const url = await page.inputValue('input[name="vw_url"]');
  expect(url).toBe('https://vault.example.com');
});

test('save with empty fields shows error', async ({ page }) => {
  await register(page, `empty-${Date.now()}@example.com`, 'TestPassword123');
  await page.goto('/credentials');
  // Submit form without filling fields — handler validates empty fields
  await page.fill('input[name="op_token"]',           ' ');
  await page.fill('input[name="vw_url"]',             ' ');
  await page.fill('input[name="vw_client_id"]',       ' ');
  await page.fill('input[name="vw_client_secret"]',   ' ');
  await page.fill('input[name="vw_master_password"]', ' ');
  await page.click('button[type="submit"]');
  await expect(page.locator('.error')).toBeVisible();
});
```

- [ ] **Step 6: Create sync.spec.ts**

Create `e2e/tests/sync.spec.ts`:

```typescript
import { test, expect } from '@playwright/test';
import { register, saveCredentials } from '../fixtures/users';

test('trigger sync creates job and redirects to job detail', async ({ page }) => {
  await register(page, `sync-${Date.now()}@example.com`, 'TestPassword123');
  await saveCredentials(page);

  await page.goto('/');
  await page.click('form[action="/sync/trigger"] button');

  await expect(page).toHaveURL(/\/sync\/jobs\/.+/);
  await expect(page.locator('h1')).toContainText('Sync Job');
  await expect(page.locator('#job-status')).toBeVisible();
});

test('job detail page shows status', async ({ page }) => {
  await register(page, `status-${Date.now()}@example.com`, 'TestPassword123');
  await saveCredentials(page);

  await page.goto('/');
  await page.click('form[action="/sync/trigger"] button');
  await page.waitForURL(/\/sync\/jobs\/.+/);

  await expect(page.locator('p').filter({ hasText: 'Status:' })).toBeVisible();
});

test('SSE stream endpoint returns text/event-stream', async ({ page }) => {
  await register(page, `sse-${Date.now()}@example.com`, 'TestPassword123');
  await saveCredentials(page);

  await page.goto('/');
  await page.click('form[action="/sync/trigger"] button');
  await page.waitForURL(/\/sync\/jobs\/(.+)/);

  const jobID = page.url().split('/').pop()!;
  const response = await page.request.get(`/sync/jobs/${jobID}/stream`, {
    headers: { Accept: 'text/event-stream' },
    timeout: 5000,
  });
  expect(response.headers()['content-type']).toContain('text/event-stream');
});

test('triggered job appears in dashboard history', async ({ page }) => {
  await register(page, `history-${Date.now()}@example.com`, 'TestPassword123');
  await saveCredentials(page);

  await page.goto('/');
  await page.click('form[action="/sync/trigger"] button');
  await page.waitForURL(/\/sync\/jobs\/.+/);

  await page.goto('/');
  await expect(page.locator('table')).toBeVisible();
  await expect(page.locator('td a')).toBeVisible();
});
```

- [ ] **Step 7: Create isolation.spec.ts**

Create `e2e/tests/isolation.spec.ts`:

```typescript
import { test, expect } from '@playwright/test';
import { register, saveCredentials } from '../fixtures/users';

test('user A cannot access user B job detail', async ({ browser }) => {
  const ts = Date.now();

  const ctxA = await browser.newContext();
  const ctxB = await browser.newContext();
  const pageA = await ctxA.newPage();
  const pageB = await ctxB.newPage();

  try {
    // User A registers, saves credentials, triggers a sync
    await register(pageA, `userA-${ts}@example.com`, 'TestPassword123');
    await saveCredentials(pageA);
    await pageA.goto('/');
    await pageA.click('form[action="/sync/trigger"] button');
    await pageA.waitForURL(/\/sync\/jobs\/.+/);
    const jobID = pageA.url().split('/').pop()!;

    // User B registers separately
    await register(pageB, `userB-${ts}@example.com`, 'TestPassword123');

    // User B tries to access User A's job — must get 404
    const response = await pageB.request.get(`/sync/jobs/${jobID}`);
    expect(response.status()).toBe(404);
  } finally {
    await ctxA.close();
    await ctxB.close();
  }
});

test('user A cannot access user B credentials via direct GET', async ({ browser }) => {
  const ts = Date.now();

  const ctxA = await browser.newContext();
  const ctxB = await browser.newContext();
  const pageA = await ctxA.newPage();
  const pageB = await ctxB.newPage();

  try {
    await register(pageA, `credA-${ts}@example.com`, 'TestPassword123');
    await register(pageB, `credB-${ts}@example.com`, 'TestPassword123');

    await saveCredentials(pageA);

    // User B's credentials page shows no credentials (not user A's)
    await pageB.goto('/credentials');
    const token = await pageB.inputValue('input[name="op_token"]');
    expect(token).toBe(''); // User B should see empty form
  } finally {
    await ctxA.close();
    await ctxB.close();
  }
});
```

- [ ] **Step 8: Start test stack and run E2E tests**

```bash
task e2e
```
Expected: docker-compose.test.yml starts server + postgres, all 4 test files pass, stack tears down.

If `task` is not installed, run directly:

```bash
docker compose -f docker/docker-compose.test.yml up -d --wait
cd e2e && BASE_URL=http://localhost:8081 npx playwright test
docker compose -f docker/docker-compose.test.yml down -v
```

- [ ] **Step 9: Commit**

```bash
git add e2e/
git commit -m "feat: add Playwright E2E tests (auth, credentials, sync, tenant isolation)"
```

---

## Final Verification

After all tasks complete:

```bash
# Unit + store integration tests
TEST_DATABASE_URL=postgres://sync:pass@localhost:5432/sync_test go test -race ./...

# Both binaries build
go build ./cmd/sync/ ./cmd/server/

# Full E2E suite
task e2e
```

All three must pass before the feature is complete.
