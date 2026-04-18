package store_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
)

func TestJobStore_FullLifecycle(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	us := store.NewUserStore(pool)
	user, err := us.Create(ctx, "jobs-test@example.com", "hash")
	if err != nil {
		t.Fatalf("Create user: %v", err)
	}
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id = $1", user.ID) })

	js := store.NewJobStore(pool)

	var jobID uuid.UUID
	err = store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error {
		job, e := js.Create(ctx, tx, user.ID)
		if e != nil {
			return e
		}
		jobID = job.ID
		if job.Status != "pending" {
			return fmt.Errorf("status = %q, want pending", job.Status)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Create job: %v", err)
	}

	if err := js.UpdateStatus(ctx, jobID, "running", nil); err != nil {
		t.Fatalf("UpdateStatus running: %v", err)
	}

	payload, _ := json.Marshal(map[string]string{"item": "test-item"})
	if err := js.AppendEvent(ctx, jobID, 1, "create", payload); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	var events []store.SyncJobEvent
	err = store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error {
		var e error
		events, e = js.ListEvents(ctx, tx, jobID)
		return e
	})
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("len(events) = %d, want 1", len(events))
	}
	if events[0].EventType != "create" {
		t.Errorf("event_type = %q, want create", events[0].EventType)
	}

	errMsg := ""
	if err := js.UpdateStatus(ctx, jobID, "done", &errMsg); err != nil {
		t.Fatalf("UpdateStatus done: %v", err)
	}

	var job *store.SyncJob
	err = store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error {
		var e error
		job, e = js.GetJob(ctx, tx, jobID)
		return e
	})
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if job.Status != "done" {
		t.Errorf("status = %q, want done", job.Status)
	}
	if job.FinishedAt == nil {
		t.Error("finished_at should be set for done status")
	}
}

func TestJobStore_ListJobs(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()

	us := store.NewUserStore(pool)
	user, _ := us.Create(ctx, "jobs-list@example.com", "hash")
	t.Cleanup(func() { pool.Exec(ctx, "DELETE FROM users WHERE id = $1", user.ID) })

	js := store.NewJobStore(pool)

	for i := 0; i < 2; i++ {
		store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error {
			_, e := js.Create(ctx, tx, user.ID)
			return e
		})
	}

	var jobs []store.SyncJob
	store.WithUserID(ctx, pool, user.ID, func(tx pgx.Tx) error {
		var e error
		jobs, e = js.ListJobs(ctx, tx, user.ID)
		return e
	})
	if len(jobs) < 2 {
		t.Errorf("len(jobs) = %d, want >= 2", len(jobs))
	}
}
