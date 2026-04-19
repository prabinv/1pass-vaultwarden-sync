package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/crypto"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/onepassword"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/middleware"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/templates"
)

type SettingsHandler struct {
	pool      *pgxpool.Pool
	credStore *store.CredentialsStore
}

func NewSettingsHandler(pool *pgxpool.Pool, credStore *store.CredentialsStore) *SettingsHandler {
	return &SettingsHandler{pool: pool, credStore: credStore}
}

func (h *SettingsHandler) ShowSettings(w http.ResponseWriter, r *http.Request) {
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

	templates.SettingsPage(creds, false, "").Render(r.Context(), w) //nolint:errcheck
}

func (h *SettingsHandler) SaveSettings(w http.ResponseWriter, r *http.Request) {
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
		templates.SettingsForm(creds, false, "All fields are required.").Render(r.Context(), w) //nolint:errcheck
		return
	}

	err := store.WithUserID(r.Context(), h.pool, userID, func(tx pgx.Tx) error {
		return h.credStore.Upsert(r.Context(), tx, userID, creds)
	})
	if err != nil {
		templates.SettingsForm(creds, false, "Failed to save credentials.").Render(r.Context(), w) //nolint:errcheck
		return
	}

	templates.SettingsForm(creds, true, "").Render(r.Context(), w) //nolint:errcheck
}

func (h *SettingsHandler) TestConnection(w http.ResponseWriter, r *http.Request) {
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
		templates.TestConnectionResult(
			templates.TestResult{Name: "1Password", OK: false, Message: "No credentials saved"},
			templates.TestResult{Name: "Vaultwarden", OK: false, Message: "No credentials saved"},
		).Render(ctx, w) //nolint:errcheck
		return
	}

	type result struct {
		res templates.TestResult
		idx int
	}
	ch := make(chan result, 2)

	go func() {
		ch <- result{idx: 0, res: testOnePassword(ctx, creds.OPServiceAccountToken)}
	}()
	go func() {
		ch <- result{idx: 1, res: testVaultwarden(ctx, creds)}
	}()

	results := make([]templates.TestResult, 2)
	for range 2 {
		r := <-ch
		results[r.idx] = r.res
	}

	templates.TestConnectionResult(results[0], results[1]).Render(ctx, w) //nolint:errcheck
}

func testOnePassword(ctx context.Context, token string) templates.TestResult {
	client, err := onepassword.New(ctx, token)
	if err != nil {
		return templates.TestResult{Name: "1Password", OK: false, Message: err.Error()}
	}
	items, err := client.ListItems(ctx)
	if err != nil {
		return templates.TestResult{Name: "1Password", OK: false, Message: err.Error()}
	}
	return templates.TestResult{Name: "1Password", OK: true, Message: formatCount(len(items), "item")}
}

func testVaultwarden(ctx context.Context, creds *store.Credentials) templates.TestResult {
	_, _, err := crypto.Exchange(
		ctx,
		&http.Client{},
		creds.VaultwardenURL,
		creds.VaultwardenClientID,
		creds.VaultwardenClientSecret,
		creds.VaultwardenMasterPassword,
	)
	if err != nil {
		return templates.TestResult{Name: "Vaultwarden", OK: false, Message: err.Error()}
	}
	return templates.TestResult{Name: "Vaultwarden", OK: true, Message: "authenticated successfully"}
}

func formatCount(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
