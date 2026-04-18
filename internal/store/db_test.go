package store_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := store.NewPool(context.Background(), dsn)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func mustParseUUID(s string) uuid.UUID {
	id, err := uuid.Parse(s)
	if err != nil {
		panic(err)
	}
	return id
}

func TestNewPool_Ping(t *testing.T) {
	pool := testPool(t)
	if err := pool.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestWithUserID_SetsLocalConfig(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id := "11111111-1111-1111-1111-111111111111"

	var got string
	err := store.WithUserID(ctx, pool, mustParseUUID(id), func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT current_setting('app.user_id', true)").Scan(&got)
	})
	if err != nil {
		t.Fatalf("WithUserID: %v", err)
	}
	if got != id {
		t.Fatalf("expected app.user_id=%q, got %q", id, got)
	}
}

func TestWithUserID_RollsBackOnError(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	id := "22222222-2222-2222-2222-222222222222"
	sentinelErr := errors.New("fn error")

	err := store.WithUserID(ctx, pool, mustParseUUID(id), func(tx pgx.Tx) error {
		return sentinelErr
	})
	if err != sentinelErr {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}
