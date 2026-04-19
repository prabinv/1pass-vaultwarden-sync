package handlers

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/crypto"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/jobrunner"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/onepassword"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
	syncengine "github.com/prabinv/1pass-vaultwarden-sync/internal/sync"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/vaultwarden"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/middleware"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/templates"
)

type SyncHandler struct {
	pool      *pgxpool.Pool
	jobStore  *store.JobStore
	credStore *store.CredentialsStore
	runner    *jobrunner.Runner
}

func NewSyncHandler(pool *pgxpool.Pool, jobStore *store.JobStore, credStore *store.CredentialsStore, runner *jobrunner.Runner) *SyncHandler {
	return &SyncHandler{pool: pool, jobStore: jobStore, credStore: credStore, runner: runner}
}

// ShowPlan renders the sync plan page (full page with loading spinner).
func (h *SyncHandler) ShowPlan(w http.ResponseWriter, r *http.Request) {
	templates.SyncPlanPage().Render(r.Context(), w) //nolint:errcheck
}

// Plan runs the sync engine plan and returns an HTMX fragment with the diff table.
func (h *SyncHandler) Plan(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := middleware.UserIDFromContext(ctx)

	var creds *store.Credentials
	if err := store.WithUserID(ctx, h.pool, userID, func(tx pgx.Tx) error {
		var e error
		creds, e = h.credStore.Get(ctx, tx, userID)
		if errors.Is(e, store.ErrNotFound) {
			return nil
		}
		return e
	}); err != nil {
		http.Error(w, "failed to fetch credentials", http.StatusInternalServerError)
		return
	}

	if creds == nil {
		templates.SyncPlanNoCredentials().Render(ctx, w) //nolint:errcheck
		return
	}

	accessToken, userKey, err := crypto.Exchange(
		ctx,
		&http.Client{},
		creds.VaultwardenURL,
		creds.VaultwardenClientID,
		creds.VaultwardenClientSecret,
		creds.VaultwardenMasterPassword,
	)
	if err != nil {
		templates.SyncPlanError(fmt.Sprintf("Vaultwarden authentication failed: %v", err)).Render(ctx, w) //nolint:errcheck
		return
	}

	opClient, err := onepassword.New(ctx, creds.OPServiceAccountToken)
	if err != nil {
		templates.SyncPlanError(fmt.Sprintf("1Password client error: %v", err)).Render(ctx, w) //nolint:errcheck
		return
	}

	vwClient := vaultwarden.New(creds.VaultwardenURL, accessToken, userKey)
	if err := vwClient.WarmCache(ctx); err != nil {
		templates.SyncPlanError(fmt.Sprintf("Vaultwarden cache error: %v", err)).Render(ctx, w) //nolint:errcheck
		return
	}

	engine := syncengine.NewEngine(opClient, vwClient)
	plan, err := engine.Plan(ctx)
	if err != nil {
		templates.SyncPlanError(fmt.Sprintf("Plan failed: %v", err)).Render(ctx, w) //nolint:errcheck
		return
	}

	templates.SyncPlanFragment(plan).Render(ctx, w) //nolint:errcheck
}

// Trigger creates a sync job and redirects to its detail page.
func (h *SyncHandler) Trigger(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := middleware.UserIDFromContext(ctx)

	// Parse selected IDs from form (multi-value or comma-separated).
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	selectedIDs := r.Form["selected_id"]
	if len(selectedIDs) == 0 {
		if raw := r.FormValue("selected_ids"); raw != "" {
			selectedIDs = strings.Split(raw, ",")
		}
	}

	var jobID uuid.UUID
	err := store.WithUserID(ctx, h.pool, userID, func(tx pgx.Tx) error {
		active, e := h.jobStore.HasActiveJob(ctx, tx, userID)
		if e != nil {
			return e
		}
		if active {
			return errActiveJob
		}
		job, e := h.jobStore.Create(ctx, tx, userID)
		if e != nil {
			return e
		}
		jobID = job.ID
		return nil
	})
	if errors.Is(err, errActiveJob) {
		http.Error(w, "a sync job is already running", http.StatusConflict)
		return
	}
	if err != nil {
		http.Error(w, "failed to create job", http.StatusInternalServerError)
		return
	}

	h.runner.Enqueue(ctx, jobID, userID, selectedIDs)
	http.Redirect(w, r, fmt.Sprintf("/sync/jobs/%s", jobID), http.StatusSeeOther)
}

var errActiveJob = errors.New("active job exists")

// JobDetail renders the job detail page.
func (h *SyncHandler) JobDetail(w http.ResponseWriter, r *http.Request) {
	userID := middleware.UserIDFromContext(r.Context())
	jobID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid job id", http.StatusBadRequest)
		return
	}

	var job *store.SyncJob
	var events []store.SyncJobEvent
	if err := store.WithUserID(r.Context(), h.pool, userID, func(tx pgx.Tx) error {
		var e error
		job, e = h.jobStore.GetJob(r.Context(), tx, jobID)
		if e != nil {
			return e
		}
		if job.Status != "running" {
			events, e = h.jobStore.ListEvents(r.Context(), tx, jobID)
		}
		return e
	}); err != nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	templates.JobDetail(job, events).Render(r.Context(), w) //nolint:errcheck
}

// Stream sends an SSE stream of progress events for jobID.
// Replays persisted events first (reconnect resilience), then streams live.
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

	if h.pool != nil {
		var past []store.SyncJobEvent
		if err := store.WithUserID(r.Context(), h.pool, userID, func(tx pgx.Tx) error {
			var e error
			past, e = h.jobStore.ListEvents(r.Context(), tx, jobID)
			return e
		}); err != nil {
			slog.Warn("sync handler: replay events", "err", err)
		}
		for _, e := range past {
			writeSSE(w, flusher, e.EventType, e.Payload)
		}
	}

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
