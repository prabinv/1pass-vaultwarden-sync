# UI/UX Redesign — Design Spec

**Date:** 2026-04-19
**Status:** Approved

## Overview

Redesign the web interface from bare-bones HTML to a polished, open-source-ready UI. The app is
intended for public release and self-hosting by others, so first impressions and UX quality matter.

## Goals

- Replace inline-styled templates with a consistent, modern design system
- Add a preview/diff step before syncing so users can choose what to sync
- Show real-time item-by-item progress during a sync job
- Improve the credentials workflow (Settings page, test connection, missing-credentials warnings)
- Support light and dark themes via system preference

---

## Tech Stack

| Concern | Choice | Reason |
|---|---|---|
| CSS | Tailwind CSS (CDN) | No build step; wide contributor familiarity |
| Interactivity | HTMX | Server-driven; replaces Datastar for all interactions |
| Client state | Alpine.js | Checkbox selection, show/hide toggles, reactive counters |
| Streaming | HTMX SSE extension (`htmx-ext-sse`) | Replaces Datastar SSE; same backend unchanged |
| Templates | `a-h/templ` (keep) | Go-native; no JS toolchain required |

Datastar is removed entirely. No other dependencies added.

---

## Design System

**Font:** Inter (Google Fonts, single weight subset — 400/500/600/700)

**Color tokens:**

| Token | Light | Dark |
|---|---|---|
| Background | `slate-50` | `slate-900` |
| Surface | `white` | `slate-800` |
| Surface-2 | `slate-100` | `slate-700` |
| Border | `slate-200` | `slate-700` |
| Text | `slate-900` | `slate-100` |
| Text-muted | `slate-500` | `slate-400` |
| Accent | `violet-600` | `violet-400` |
| Accent bg | `violet-50` | `violet-950` |

**Status colors:** `green-600` (success), `yellow-600` (running/warning), `red-600` (failed), `slate-400` (unchanged/skipped)

**Border radius:** `rounded-xl` cards, `rounded-lg` buttons/inputs

**Shadows:** `shadow-sm` on cards, `shadow-md` on page panels

---

## Pages & Routes

| Route | Page | Auth required |
|---|---|---|
| `/login` | Login | No |
| `/register` | Register | No |
| `/` | Dashboard | Yes |
| `/sync/new` | Sync Plan | Yes |
| `/sync/jobs/:id` | Job Progress | Yes |
| `/settings` | Settings | Yes |

`/credentials` is removed; replaced by `/settings`.

---

## Navigation

Top bar present on all authenticated pages:

- **Left:** App logo icon + "1pass-sync" wordmark
- **Center:** "Dashboard" and "Settings" nav links (active state: `violet` bg pill)
- **Right:** User email (muted), Logout button

---

## Screen 1 — Auth (Login / Register)

**Layout:** Full-viewport centered, single card (`max-w-sm`), app logo above card.

**Login card:**
- App icon + name + tagline ("Sync 1Password → Vaultwarden")
- Email field
- Password field with show/hide toggle (Alpine)
- "Sign in" button (full-width, `violet-600`)
- "Don't have an account? Register" footer link

**Register card:** Same layout — email, password, confirm password fields.

**Validation:** Inline field errors injected below each input via HTMX on submit failure. No page reload.

**Post-login redirect:**
- If credentials not configured → redirect to `/settings` with toast: *"Welcome! Set up your credentials to get started."*
- Otherwise → redirect to `/`

---

## Screen 2 — Dashboard (`/`)

**Page header:**
- "Sync History" title (left)
- "New Sync" button (right, `violet-600`)

**Credentials missing banner:** Yellow warning banner above table when no credentials are saved:
> "⚠ Credentials not configured — syncs will fail until you set them up. [Go to Settings →]"

**New Sync button disabled state:** Button is disabled (with tooltip: *"A sync is already running"*) when any job has `running` status. Re-enabled automatically via HTMX polling once job settles.

**Server-side guard:** `POST /sync/trigger` returns `409 Conflict` if an active job exists.

**Sync history table columns:** Status badge · Job ID (truncated monospace) · Started · Duration · Items synced · View link

**Status badges:**
- `● Running` — yellow, pulsing dot
- `✓ Completed` — green
- `✗ Failed` — red

**Empty state:** Centered icon + "No syncs yet. Configure credentials in Settings, then click New Sync."

**Live table refresh:** HTMX polling (`hx-trigger="every 5s"`) refreshes table rows while any job is `running`. Polling stops when all jobs are settled.

---

## Screen 3 — Sync Plan (`/sync/new`)

Two-step flow: plan loads → user selects → sync launches.

**Step 1 — Plan loading:**
- Card with spinner: *"Comparing 1Password and Vaultwarden…"*
- HTMX triggers `GET /sync/plan` on load; swaps in diff table when ready

**Step 2 — Diff table:**

Header row (above table):
- Left: item count summary — *"5 items — 2 new, 2 updated, 1 unchanged"*
- Right: *"N selected"* counter (Alpine reactive) + "Select all" / "Deselect all" buttons

Table columns: Checkbox · Item name + vault label · Vault/collection → destination · Change badge

**Change badges:** `New` (violet) · `Updated` (yellow) · `Unchanged` (slate, row dimmed, checkbox disabled)

**Default selection:**
- `New` items: checked by default
- `Updated` items: unchecked by default (safer — requires explicit opt-in)
- `Unchanged` items: disabled, never selectable

**Launch bar (below table):**
- "Sync N Selected Items →" button — disabled when 0 items selected
- On submit: `POST /sync/trigger` with selected item IDs → redirects to `/sync/jobs/:id`

---

## Screen 4 — Job Progress (`/sync/jobs/:id`)

**Header:**
- "Sync Job" + truncated monospace ID
- Status badge (pulsing yellow while running, green/red on completion)
- "← Dashboard" link (top-right)

**Progress list:**
- Items appear one-by-one via HTMX SSE as events arrive
- Each row: status icon + item name + detail text + status label (right-aligned)
- Status icons: spinning circle (running) · `✓` green (synced) · `✗` red (failed) · `–` slate (skipped)
- SSE reconnect: HTMX auto-reconnects; backend replays all persisted events (existing behavior, unchanged)

**Completion summary (inline, no redirect):**
- Green summary card slides in below list on job completion
- Contents: *"✓ Sync complete — 3 synced, 0 failed, 1 skipped"*
- Two buttons: "New Sync" (→ `/sync/new`) and "Back to Dashboard"
- User controls navigation; no auto-redirect

---

## Screen 5 — Settings (`/settings`)

**Page header:** "Settings" title + *"Configure your 1Password and Vaultwarden credentials"* subtitle + "Save Changes" button (top-right)

**Form layout — two labeled sections:**

**1Password section:**
- Service Account Token (password field, show/hide toggle)

**Vaultwarden section:**
- URL (text/url field)
- Client ID (text field)
- Client Secret (password, show/hide toggle)
- Master Password (password, show/hide toggle)

Show/hide toggles implemented with Alpine.js; eye icon button inlined in each password field.

**Save:** HTMX form submit; inline success toast *"Credentials saved"* replaces button briefly. No page reload.

**Test Connection section** (bottom, separated by border):

Title: "Test Connection"
Description: *"Verify credentials against each system without saving changes."*
Button: "▶ Run Test" / "↺ Re-run Test" after first run

Results displayed as two separate rows — one per system:

| System | Success state | Failure state |
|---|---|---|
| 1Password | `✓ OK` — "Connected — N items across N vaults" | `✗ Failed` — specific error |
| Vaultwarden | `✓ OK` — "Authenticated as user@example.com" | `✗ Failed` — specific error (e.g., "401 — check Client ID or Secret") |

Each row shows: icon (green ✓ or red ✗) · system name · detail message · status badge

Backend: `GET /settings/test` runs both checks in parallel, returns JSON with per-system results. HTMX swaps the results panel.

---

## Backend Changes Required

| Change | Details |
|---|---|
| New route: `GET /sync/plan` | Runs `sync.Plan()`, returns diff as JSON/HTML fragment |
| New route: `GET /settings/test` | Tests 1Password + Vaultwarden connectivity in parallel; per-system results |
| Modified: `POST /sync/trigger` | Accept optional list of selected item IDs; pass to `sync.Apply()` |
| Modified: `sync.Apply()` | Accept item ID filter; only apply selected items |
| Rename: `/credentials` → `/settings` | Handler rename + redirect from old path |
| `409` guard on `/sync/trigger` | Return conflict if active job exists |

---

## Mockups

Saved in `.superpowers/brainstorm/` (excluded from git via `.gitignore`).

- All 5 screens: login, dashboard (with data + credentials-missing states), sync plan, job progress, settings (3 test-connection states)
- Light/dark theme via `prefers-color-scheme`
