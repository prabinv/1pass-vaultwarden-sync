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
	h := handlers.NewSyncHandler(nil, nil, nil, runner)

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
