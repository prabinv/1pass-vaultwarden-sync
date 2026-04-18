package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/handlers"
)

func postForm(t *testing.T, h http.Handler, path string, values url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(values.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestAuthHandler_Register_InvalidEmail(t *testing.T) {
	h := handlers.NewAuthHandler(nil, "secret")

	rec := postForm(t, http.HandlerFunc(h.Register), "/auth/register", url.Values{
		"email":    {"notanemail"},
		"password": {"validpassword123"},
	})

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (re-render)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Invalid email") {
		t.Errorf("expected 'Invalid email' in body, got: %s", rec.Body.String())
	}
}

func TestAuthHandler_Register_ShortPassword(t *testing.T) {
	h := handlers.NewAuthHandler(nil, "secret")

	rec := postForm(t, http.HandlerFunc(h.Register), "/auth/register", url.Values{
		"email":    {"user@example.com"},
		"password": {"short"},
	})

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (re-render)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "8 characters") {
		t.Errorf("expected password length error in body, got: %s", rec.Body.String())
	}
}

func TestAuthHandler_ShowLogin_Returns200(t *testing.T) {
	h := handlers.NewAuthHandler(nil, "secret")
	req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	rec := httptest.NewRecorder()
	h.ShowLogin(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}
