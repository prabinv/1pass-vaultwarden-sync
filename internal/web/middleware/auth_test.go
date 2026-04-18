package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/web/middleware"
)

const testSecret = "test-jwt-secret-at-least-32-chars!!"

func TestAuth_NoCookie_RedirectsToLogin(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware.Auth(testSecret)(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if rec.Header().Get("Location") != "/auth/login" {
		t.Errorf("Location = %q, want /auth/login", rec.Header().Get("Location"))
	}
}

func TestAuth_ValidToken_InjectsUserID(t *testing.T) {
	userID := uuid.New()
	token, err := middleware.IssueJWT(testSecret, userID)
	if err != nil {
		t.Fatalf("IssueJWT: %v", err)
	}

	var gotID uuid.UUID
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotID = middleware.UserIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware.Auth(testSecret)(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "token", Value: token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if gotID != userID {
		t.Errorf("userID = %v, want %v", gotID, userID)
	}
}

func TestAuth_ExpiredToken_RedirectsToLogin(t *testing.T) {
	token, _ := middleware.IssueJWTWithExpiry(testSecret, uuid.New(), -3600)

	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := middleware.Auth(testSecret)(next)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "token", Value: token})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Errorf("status = %d, want redirect", rec.Code)
	}
}

func TestSetUserIDInContext_RoundTrip(t *testing.T) {
	id := uuid.New()
	ctx := middleware.SetUserIDInContext(context.Background(), id)
	got := middleware.UserIDFromContext(ctx)
	if got != id {
		t.Errorf("got %v, want %v", got, id)
	}
}
