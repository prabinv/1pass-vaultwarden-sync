package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
)

const testEncKey = "test-encryption-key-32-bytes!!!"

func TestCredentialsStore_UpsertAndGet(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	us := store.NewUserStore(pool)
	user, err := us.Create(ctx, "creds-test@example.com", "$2a$12$fakehash")
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id = $1", user.ID) })

	cs := store.NewCredentialsStore(pool, testEncKey)

	input := &store.Credentials{
		OPServiceAccountToken:     "ops_test_token",
		VaultwardenURL:            "https://vault.example.com",
		VaultwardenClientID:       "client-id",
		VaultwardenClientSecret:   "client-secret",
		VaultwardenMasterPassword: "master-password",
	}

	err = store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error {
		return cs.Upsert(ctx, tx, user.ID, input)
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	var got *store.Credentials
	err = store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error {
		var e error
		got, e = cs.Get(ctx, tx, user.ID)
		return e
	})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	if got.OPServiceAccountToken != input.OPServiceAccountToken {
		t.Errorf("token = %q, want %q", got.OPServiceAccountToken, input.OPServiceAccountToken)
	}
	if got.VaultwardenURL != input.VaultwardenURL {
		t.Errorf("url = %q, want %q", got.VaultwardenURL, input.VaultwardenURL)
	}
}

func TestCredentialsStore_Get_NotFound(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	us := store.NewUserStore(pool)
	user, _ := us.Create(ctx, "creds-notfound@example.com", "hash")
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id = $1", user.ID) })

	cs := store.NewCredentialsStore(pool, testEncKey)
	var err error
	store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error {
		_, err = cs.Get(ctx, tx, user.ID)
		return nil
	})
	if err != store.ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestCredentialsStore_Upsert_UpdatesExisting(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	us := store.NewUserStore(pool)
	user, _ := us.Create(ctx, "creds-update@example.com", "hash")
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id = $1", user.ID) })

	cs := store.NewCredentialsStore(pool, testEncKey)

	first := &store.Credentials{OPServiceAccountToken: "v1", VaultwardenURL: "https://v1.example.com", VaultwardenClientID: "cid", VaultwardenClientSecret: "cs", VaultwardenMasterPassword: "mp"}
	second := &store.Credentials{OPServiceAccountToken: "v2", VaultwardenURL: "https://v2.example.com", VaultwardenClientID: "cid2", VaultwardenClientSecret: "cs2", VaultwardenMasterPassword: "mp2"}

	store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error { return cs.Upsert(ctx, tx, user.ID, first) })
	store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error { return cs.Upsert(ctx, tx, user.ID, second) })

	var got *store.Credentials
	store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error {
		got, _ = cs.Get(ctx, tx, user.ID)
		return nil
	})
	if got.OPServiceAccountToken != "v2" {
		t.Errorf("token = %q, want v2", got.OPServiceAccountToken)
	}
}
