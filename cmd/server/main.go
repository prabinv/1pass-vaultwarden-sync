package main

import (
	"context"
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
	migrations "github.com/prabinv/1pass-vaultwarden-sync/migrations"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/handlers"
	webmw "github.com/prabinv/1pass-vaultwarden-sync/internal/web/middleware"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/templates"
)

func main() {
	ctx := context.Background()

	dbURL  := mustEnv("DATABASE_URL")
	encKey := mustEnv("ENCRYPTION_KEY")
	jwtSec := mustEnv("JWT_SECRET")
	port   := envOr("PORT", "8080")

	// Run migrations at startup.
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		slog.Error("migration source", "err", err)
		os.Exit(1)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dbURL)
	if err != nil {
		slog.Error("migrate init", "err", err)
		os.Exit(1)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		slog.Error("migrate up", "err", err)
		os.Exit(1)
	}

	pool, err := store.NewPool(ctx, dbURL)
	if err != nil {
		slog.Error("db pool", "err", err)
		os.Exit(1)
	}
	defer pool.Close()

	userStore := store.NewUserStore(pool)
	credStore := store.NewCredentialsStore(pool, encKey)
	jobStore  := store.NewJobStore(pool)
	runner    := jobrunner.New(pool, credStore, jobStore)

	authH := handlers.NewAuthHandler(userStore, jwtSec)
	credH := handlers.NewCredentialsHandler(pool, credStore)
	syncH := handlers.NewSyncHandler(pool, jobStore, runner)

	r := chi.NewRouter()
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)

	// Public routes.
	r.Get("/auth/login",     authH.ShowLogin)
	r.Post("/auth/login",    authH.Login)
	r.Get("/auth/register",  authH.ShowRegister)
	r.Post("/auth/register", authH.Register)
	r.Post("/auth/logout",   authH.Logout)

	// Protected routes.
	r.Group(func(r chi.Router) {
		r.Use(webmw.Auth(jwtSec))

		r.Get("/", func(w http.ResponseWriter, req *http.Request) {
			userID := webmw.UserIDFromContext(req.Context())
			var jobs []store.SyncJob
			store.WithUserID(req.Context(), pool, userID, func(tx pgx.Tx) error { //nolint:errcheck
				var e error
				jobs, e = jobStore.ListJobs(req.Context(), tx, userID)
				return e
			})
			templates.Dashboard(jobs).Render(req.Context(), w) //nolint:errcheck
		})

		r.Get("/credentials",  credH.Show)
		r.Post("/credentials", credH.Save)

		r.Post("/sync/trigger",         syncH.Trigger)
		r.Get("/sync/jobs/{id}",        syncH.JobDetail)
		r.Get("/sync/jobs/{id}/stream", syncH.Stream)
	})

	slog.Info("server starting", "port", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		slog.Error("server", "err", err)
		os.Exit(1)
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
