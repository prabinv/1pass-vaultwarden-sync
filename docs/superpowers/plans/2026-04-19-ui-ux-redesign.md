# UI/UX Redesign — Implementation Plan

## Context

The web UI is bare-bones inline-styled HTML. The app is being open-sourced, so the UI needs to look
polished and production-ready. We're replacing Datastar with HTMX + Alpine.js + Tailwind CSS, adding
a preview/diff step before syncing (so users pick which items to sync), and improving the settings and
credentials-missing flows.

Approved spec: `docs/superpowers/specs/2026-04-19-ui-ux-redesign.md`

---

## Critical Files

| File | Change |
|---|---|
| `cmd/server/main.go` | Add routes: `/sync/new`, `/settings`, `/settings/test`; rename `/credentials`; extract dashboard handler; add 409 guard |
| `internal/jobrunner/runner.go` | Add `selectedIDs []string` param to `Enqueue()` and `run()`; filter `plan.Items` before `Apply()` |
| `internal/web/handlers/sync.go` | Add `Plan()` handler (GET /sync/plan fragment); modify `Trigger()` to read selected IDs |
| `internal/web/handlers/credentials.go` | Rename to `settings.go`; rename struct; add `Test()` method |
| `internal/web/handlers/dashboard.go` | New file — extract inline dashboard handler from `main.go` |
| `internal/web/templates/layout.templ` | Swap Datastar CDN → Tailwind + HTMX + HTMX SSE ext + Alpine |
| `internal/web/templates/auth.templ` | Full redesign — centered card, show/hide toggle, inline errors |
| `internal/web/templates/dashboard.templ` | Full redesign — table with badges, credentials banner, HTMX polling |
| `internal/web/templates/job.templ` | Replace Datastar SSE with HTMX SSE ext; progress list; completion summary |
| `internal/web/templates/credentials.templ` | Rename to `settings.templ`; full redesign; add test-connection panel |
| `internal/web/templates/syncplan.templ` | New — diff table with Alpine checkboxes |

---

## Implementation Steps

### Step 1 — Sync engine: add subset filtering in runner

**File:** `internal/jobrunner/runner.go`

- Change `Enqueue(ctx, jobID, userID)` → `Enqueue(ctx, jobID, userID, selectedIDs []string)`
- Change `run(ctx, jobID, userID)` → `run(ctx, jobID, userID, selectedIDs []string)`
- After `engine.Plan(ctx)` succeeds (line 143), add filter:
  ```go
  if len(selectedIDs) > 0 {
      keep := make(map[string]bool, len(selectedIDs))
      for _, id := range selectedIDs { keep[id] = true }
      filtered := plan.Items[:0]
      for _, item := range plan.Items {
          if keep[item.Item.ExternalID] { filtered = append(filtered, item) }
      }
      plan.Items = filtered
  }
  ```
- Filter by `item.Item.ExternalID` (the 1Password ExternalID, used as the unique key)

**Note:** `Apply()` in `engine.go` is unchanged — filtering happens pre-Apply in the runner. This keeps the TUI/CLI path unaffected.

### Step 2 — New handler: `GET /sync/plan` (HTML fragment)

**File:** `internal/web/handlers/sync.go`

Add `Plan(w http.ResponseWriter, r *http.Request)`:
1. Extract userID from context via `middleware.UserIDFromContext()`
2. Fetch credentials: `store.WithUserID(ctx, h.pool, userID, func(tx) { creds = credStore.Get(ctx, tx, userID) })`
3. If no credentials → return HTML fragment: credentials-missing message with link to `/settings`
4. Build engine (same pattern as `runner.go` lines 112-133): exchange identity, create 1P + VW clients, `syncp.NewEngine(opClient, vwClient)`
5. Call `engine.Plan(ctx)` — if error → return error fragment
6. Render `templates.SyncPlanFragment(plan)` — HTMX fragment (no layout wrapper)

**SyncHandler** needs `pool *pgxpool.Pool` and `credStore *store.CredentialsStore` added to its struct (currently only has `jobStore` and `runner`). Wire in `main.go`.

### Step 3 — Modify `POST /sync/trigger`

**File:** `internal/web/handlers/sync.go` — `Trigger()` handler

- Parse `r.FormValue("selected_ids")` (comma-separated ExternalIDs) or `r.Form["selected_id"]` (multiple values)
- Pass `selectedIDs` to `h.runner.Enqueue(ctx, jobID, userID, selectedIDs)`
- Add check: if active job exists → return 409 with inline HTMX error (or redirect with error param)

Active job check: add `JobStore.HasActiveJob(ctx, tx, userID) (bool, error)` method in `internal/store/jobs.go`:
```sql
SELECT EXISTS(SELECT 1 FROM sync_jobs WHERE user_id = current_setting('app.user_id')::uuid AND status = 'running')
```

### Step 4 — Settings handler (rename + Test method)

**File:** `internal/web/handlers/credentials.go` → `internal/web/handlers/settings.go`

- Rename `CredentialsHandler` → `SettingsHandler`
- Rename `Show()` → `ShowSettings()`, `Save()` → `SaveSettings()`
- Add `TestConnection(w, r)` handler:
  1. Fetch credentials (same RLS pattern)
  2. Test 1Password: create `onepassword.New(creds.OPServiceAccountToken)`, call `client.ListItems(ctx)` — count items/vaults
  3. Test Vaultwarden: call `crypto.Exchange(ctx, creds.VaultwardenURL, creds.VaultwardenClientID, creds.VaultwardenClientSecret, creds.VaultwardenMasterPassword)` — success = authenticated
  4. Run both tests in parallel with `errgroup` or two goroutines + channels
  5. Render `templates.TestConnectionResult(opResult, vwResult)` fragment (HTMX swap)

### Step 5 — Extract dashboard handler

**File:** `internal/web/handlers/dashboard.go` (new)

Move the inline handler from `main.go` lines 78-87 into `DashboardHandler.Show()`.
- Needs: `pool *pgxpool.Pool`, `jobStore *store.JobStore`, `credStore *store.CredentialsStore`
- Checks `credStore.Get()` to determine if credentials are configured (for banner)

### Step 6 — Template: layout.templ

**File:** `internal/web/templates/layout.templ`

Replace Datastar CDN with:
```html
<link href="https://cdn.jsdelivr.net/npm/tailwindcss@..." rel="stylesheet"/>
<script src="https://unpkg.com/htmx.org@2.0.0" ...></script>
<script src="https://unpkg.com/htmx-ext-sse@2.2.2" ...></script>
<script defer src="https://cdn.jsdelivr.net/npm/alpinejs@3.x.x/dist/cdn.min.js"></script>
```
Remove all inline `<style>` block. Body gets `class="bg-slate-50 dark:bg-slate-900 text-slate-900 dark:text-slate-100 font-inter"`.

Add `Nav()` update: links to `/` and `/settings`; remove `/credentials`.

### Step 7 — Template: auth.templ

**File:** `internal/web/templates/auth.templ`

Rewrite `Login(errMsg)` and `Register(errMsg)`:
- Centered full-viewport layout, `max-w-sm` card
- App logo icon + name + tagline above card
- Fields with Tailwind input styles
- Password field wrapped in Alpine `x-data="{ show: false }"` with eye toggle
- Error rendered as styled inline block below field
- HTMX form: `hx-post="/auth/login" hx-target="this" hx-swap="outerHTML"` for inline error swap on failure

### Step 8 — Template: dashboard.templ

**File:** `internal/web/templates/dashboard.templ`

Rewrite `Dashboard(jobs []store.SyncJob, hasCredentials bool)` — add `hasCredentials` param:
- Credentials warning banner (shown when `!hasCredentials`)
- Page header with "New Sync" button — `hx-get="/sync/new"` or regular link; disabled + tooltip when job running OR no credentials
- Table: status badge (colored), job ID (truncated), started, duration, items, view link
- HTMX polling: wrap table in `<div hx-get="/" hx-trigger="every 5s" hx-target="this" hx-swap="innerHTML">` — only when a running job exists
- Empty state component

Duration: compute from `StartedAt` and `FinishedAt` in template helper func.

### Step 9 — Template: syncplan.templ (new)

**File:** `internal/web/templates/syncplan.templ`

Two templates:
1. `SyncPlanPage()` — full page with Nav + loading spinner; HTMX `hx-get="/sync/plan" hx-trigger="load" hx-target="#plan-content" hx-swap="innerHTML"` on mount
2. `SyncPlanFragment(plan sync.SyncPlan)` — the diff table (no layout):
   - Alpine `x-data` with selected IDs array, computed count
   - Per-row checkboxes bound to Alpine model; `ActionSkip` rows disabled
   - Default selection: `ActionCreate` = checked, `ActionUpdate` = unchecked
   - "Sync N Selected" button: `hx-post="/sync/trigger" hx-include="[name='selected_id']"`
   - Select all / deselect all buttons (Alpine)

### Step 10 — Template: job.templ

**File:** `internal/web/templates/job.templ`

Rewrite `JobDetail(job *store.SyncJob)`:
- Replace `data-on-load` / Datastar SSE with HTMX SSE ext:
  ```html
  <div hx-ext="sse" sse-connect="/sync/jobs/{id}/stream" sse-swap="progress">
  ```
- Progress list: each SSE event of type `progress` appends a `<li>` with item name + status icon
- Status icons with Tailwind color classes
- Completion summary card: shown when job status = `completed`/`failed` (Alpine `x-show`)
- "Done" SSE event triggers Alpine to flip a `completed` flag that shows summary + stops polling

### Step 11 — Template: settings.templ (renamed from credentials.templ)

**File:** `internal/web/templates/settings.templ`

Rewrite `CredentialsForm` → `SettingsPage(creds *store.Credentials, saved bool, errMsg string)`:
- Two labeled sections (1Password / Vaultwarden)
- Alpine show/hide for each password field
- Save: HTMX `hx-post="/settings"` with inline success toast swap
- Test connection panel at bottom:
  - "Run Test" button: `hx-get="/settings/test" hx-target="#test-results" hx-swap="innerHTML" hx-indicator="#test-spinner"`
  - `<div id="test-results">` — swapped with `TestConnectionResult` fragment
- `TestConnectionResult(op, vw TestResult)` fragment template — two result rows

### Step 12 — Route wiring in main.go

**File:** `cmd/server/main.go`

```
GET  /                      → dashboardH.Show()
GET  /sync/new              → syncH.ShowPlan()
GET  /sync/plan             → syncH.Plan()         (HTMX fragment)
POST /sync/trigger          → syncH.Trigger()
GET  /sync/jobs/{id}        → syncH.JobDetail()
GET  /sync/jobs/{id}/stream → syncH.Stream()
GET  /settings              → settingsH.ShowSettings()
POST /settings              → settingsH.SaveSettings()
GET  /settings/test         → settingsH.TestConnection() (HTMX fragment)
```

Remove `/credentials` routes. Add `http.Redirect` from `/credentials` → `/settings` for backward compat.

Wire `pool` and `credStore` into `SyncHandler` constructor. Wire `credStore` into `DashboardHandler`.

### Step 13 — Regenerate templ files

```bash
go generate ./...
# or
templ generate
```

All `*_templ.go` files are auto-generated — do not edit them directly.

### Step 14 — Build and smoke test

```bash
go build ./...
go vet ./...
go test -race ./...
```

Then start the stack and manually verify each screen.

---

## Reused Existing Code

| Pattern | Source |
|---|---|
| Credential fetch in handler | Copy from `runner.go` lines 101-108 |
| Vaultwarden identity exchange | `crypto.Exchange()` in `internal/crypto/identity.go` |
| 1Password client creation | `onepassword.New(token)` in `internal/onepassword/client.go` |
| RLS transaction helper | `store.WithUserID()` in `internal/store/db.go` |
| UserID from context | `middleware.UserIDFromContext()` in `internal/web/middleware/auth.go` |
| SSE writer | `writeSSE()` helper in `internal/web/handlers/sync.go` line 128 |

---

## Verification

1. `go build ./...` — clean compile
2. `go test -race ./...` — all tests pass
3. Start stack: `task server` (or `docker compose up`)
4. Visit each screen and verify:
   - Login / register flow; post-login redirect to `/settings` if no credentials
   - Dashboard: credentials banner visible when none saved; New Sync disabled when job running
   - `/sync/new`: diff table loads via HTMX, checkboxes work, "Sync N" count reactive
   - `/sync/jobs/:id`: progress items appear live via SSE, summary shows on completion
   - `/settings`: save works inline; test connection shows per-system results
   - Old `/credentials` URL redirects to `/settings`
5. `task e2e` — existing Playwright tests pass (may need updates for renamed routes)
