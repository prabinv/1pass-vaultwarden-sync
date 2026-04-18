package store_test

import (
	"context"
	"testing"

	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
)

func TestUserStore_CreateAndGetByEmail(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	us := store.NewUserStore(pool)

	email := "user-store-test@example.com"
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM users WHERE email = $1", email)
	})

	user, err := us.Create(ctx, email, "$2a$12$fakehashvalue")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if user.Email != email {
		t.Errorf("email = %q, want %q", user.Email, email)
	}
	if user.ID.String() == "" {
		t.Error("ID should not be empty")
	}

	got, err := us.GetByEmail(ctx, email)
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	if got.ID != user.ID {
		t.Errorf("ID = %v, want %v", got.ID, user.ID)
	}
}

func TestUserStore_GetByEmail_NotFound(t *testing.T) {
	pool := testPool(t)
	us := store.NewUserStore(pool)

	_, err := us.GetByEmail(context.Background(), "nobody@example.com")
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestUserStore_Create_DuplicateEmail(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	us := store.NewUserStore(pool)

	email := "duplicate@example.com"
	t.Cleanup(func() {
		pool.Exec(ctx, "DELETE FROM users WHERE email = $1", email)
	})

	if _, err := us.Create(ctx, email, "hash"); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	if _, err := us.Create(ctx, email, "hash"); err == nil {
		t.Error("expected error on duplicate email, got nil")
	}
}
