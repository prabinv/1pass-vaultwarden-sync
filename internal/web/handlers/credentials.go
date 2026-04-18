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
	store.WithUserID(r.Context(), h.pool, userID, func(tx pgx.Tx) error { //nolint:errcheck
		var err error
		creds, err = h.credStore.Get(r.Context(), tx, userID)
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	})

	templates.CredentialsForm(creds, false, "").Render(r.Context(), w) //nolint:errcheck
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
		templates.CredentialsForm(creds, false, "All fields are required.").Render(r.Context(), w) //nolint:errcheck
		return
	}

	err := store.WithUserID(r.Context(), h.pool, userID, func(tx pgx.Tx) error {
		return h.credStore.Upsert(r.Context(), tx, userID, creds)
	})
	if err != nil {
		templates.CredentialsForm(creds, false, "Failed to save credentials.").Render(r.Context(), w) //nolint:errcheck
		return
	}

	templates.CredentialsForm(creds, true, "").Render(r.Context(), w) //nolint:errcheck
}
