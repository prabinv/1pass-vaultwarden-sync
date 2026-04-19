package handlers

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
	webmw "github.com/prabinv/1pass-vaultwarden-sync/internal/web/middleware"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/templates"
)

type DashboardHandler struct {
	pool      *pgxpool.Pool
	jobStore  *store.JobStore
	credStore *store.CredentialsStore
}

func NewDashboardHandler(pool *pgxpool.Pool, jobStore *store.JobStore, credStore *store.CredentialsStore) *DashboardHandler {
	return &DashboardHandler{pool: pool, jobStore: jobStore, credStore: credStore}
}

func (h *DashboardHandler) Show(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := webmw.UserIDFromContext(ctx)

	var jobs []store.SyncJob
	var hasCredentials bool

	store.WithUserID(ctx, h.pool, userID, func(tx pgx.Tx) error { //nolint:errcheck
		var e error
		jobs, e = h.jobStore.ListJobs(ctx, tx, userID)
		if e != nil {
			return e
		}
		creds, e := h.credStore.Get(ctx, tx, userID)
		if errors.Is(e, store.ErrNotFound) {
			return nil
		}
		hasCredentials = creds != nil
		return e
	})

	templates.Dashboard(jobs, hasCredentials).Render(ctx, w) //nolint:errcheck
}
