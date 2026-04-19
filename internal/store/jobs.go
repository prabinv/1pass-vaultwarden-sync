package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type SyncJob struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Status     string
	StartedAt  *time.Time
	FinishedAt *time.Time
	Error      *string
	CreatedAt  time.Time
}

type SyncJobEvent struct {
	ID        int64
	JobID     uuid.UUID
	Sequence  int
	EventType string
	Payload   json.RawMessage
	CreatedAt time.Time
}

type JobStore struct {
	pool *pgxpool.Pool
}

func NewJobStore(pool *pgxpool.Pool) *JobStore {
	return &JobStore{pool: pool}
}

// Create inserts a new sync_job. Must run inside WithUserID (RLS).
func (s *JobStore) Create(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (*SyncJob, error) {
	var j SyncJob
	row := tx.QueryRow(ctx,
		`INSERT INTO sync_jobs (user_id)
		 VALUES ($1)
		 RETURNING id, user_id, status, started_at, finished_at, error, created_at`,
		userID,
	)
	if err := row.Scan(&j.ID, &j.UserID, &j.Status, &j.StartedAt, &j.FinishedAt, &j.Error, &j.CreatedAt); err != nil {
		return nil, fmt.Errorf("create job: %w", err)
	}
	return &j, nil
}

// UpdateStatus transitions a job's status. Runs inside WithUserID to enforce RLS.
func (s *JobStore) UpdateStatus(ctx context.Context, userID, jobID uuid.UUID, status string, errMsg *string) error {
	return WithUserID(ctx, s.pool, userID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE sync_jobs SET
				status      = $1,
				error       = $2,
				started_at  = CASE WHEN $1 = 'running'           THEN now() ELSE started_at  END,
				finished_at = CASE WHEN $1 IN ('done', 'failed') THEN now() ELSE finished_at END
			WHERE id = $3`,
			status, errMsg, jobID,
		)
		if err != nil {
			return fmt.Errorf("update job status: %w", err)
		}
		return nil
	})
}

// AppendEvent persists one progress event. Runs inside WithUserID to enforce RLS.
func (s *JobStore) AppendEvent(ctx context.Context, userID, jobID uuid.UUID, seq int, eventType string, payload json.RawMessage) error {
	return WithUserID(ctx, s.pool, userID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO sync_job_events (job_id, sequence, event_type, payload)
			 VALUES ($1, $2, $3, $4)`,
			jobID, seq, eventType, payload,
		)
		if err != nil {
			return fmt.Errorf("append event: %w", err)
		}
		return nil
	})
}

// ListEvents returns all events for jobID ordered by sequence. Must run inside WithUserID (RLS).
func (s *JobStore) ListEvents(ctx context.Context, tx pgx.Tx, jobID uuid.UUID) ([]SyncJobEvent, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, job_id, sequence, event_type, payload, created_at
		 FROM sync_job_events WHERE job_id = $1 ORDER BY sequence`,
		jobID,
	)
	if err != nil {
		return nil, fmt.Errorf("list events: %w", err)
	}
	defer rows.Close()

	var events []SyncJobEvent
	for rows.Next() {
		var e SyncJobEvent
		if err := rows.Scan(&e.ID, &e.JobID, &e.Sequence, &e.EventType, &e.Payload, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// GetJob returns a single job by ID. Must run inside WithUserID (RLS).
func (s *JobStore) GetJob(ctx context.Context, tx pgx.Tx, jobID uuid.UUID) (*SyncJob, error) {
	var j SyncJob
	row := tx.QueryRow(ctx,
		`SELECT id, user_id, status, started_at, finished_at, error, created_at
		 FROM sync_jobs WHERE id = $1`,
		jobID,
	)
	if err := row.Scan(&j.ID, &j.UserID, &j.Status, &j.StartedAt, &j.FinishedAt, &j.Error, &j.CreatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get job: %w", err)
	}
	return &j, nil
}

// HasActiveJob returns true if the user has a job currently in 'running' status.
// Must run inside WithUserID (RLS).
func (s *JobStore) HasActiveJob(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM sync_jobs WHERE user_id = current_setting('app.user_id')::uuid AND status = 'running')`,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("has active job: %w", err)
	}
	return exists, nil
}

// ListJobs returns the 50 most recent jobs for userID. Must run inside WithUserID (RLS).
func (s *JobStore) ListJobs(ctx context.Context, tx pgx.Tx, userID uuid.UUID) ([]SyncJob, error) {
	rows, err := tx.Query(ctx,
		`SELECT id, user_id, status, started_at, finished_at, error, created_at
		 FROM sync_jobs WHERE user_id = $1 ORDER BY created_at DESC LIMIT 50`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	var jobs []SyncJob
	for rows.Next() {
		var j SyncJob
		if err := rows.Scan(&j.ID, &j.UserID, &j.Status, &j.StartedAt, &j.FinishedAt, &j.Error, &j.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}
