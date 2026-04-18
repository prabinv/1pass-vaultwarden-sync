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

	// Replay persisted events so page refresh mid-sync works.
	if h.pool != nil {
		var past []store.SyncJobEvent
		store.WithUserID(r.Context(), h.pool, userID, func(tx pgx.Tx) error { //nolint:errcheck
			past, _ = h.jobStore.ListEvents(r.Context(), tx, jobID)
			return nil
		})
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
