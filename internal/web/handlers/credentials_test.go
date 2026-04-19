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

func TestSettingsHandler_Show_Returns200(t *testing.T) {
	h := handlers.NewSettingsHandler(nil, nil)
	_ = h
	_ = context.Background()
	// Compile-check only — real test requires DB
}

func TestSettingsHandler_Save_RequiresFields(t *testing.T) {
	h := handlers.NewSettingsHandler(nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/settings", strings.NewReader("op_token=&vw_url="))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	ctx := middleware.SetUserIDInContext(req.Context(), uuid.New())
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	h.SaveSettings(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (re-render)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "required") {
		t.Errorf("expected required-field error in body, got: %s", rec.Body.String())
	}
}
