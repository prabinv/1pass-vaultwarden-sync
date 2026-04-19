package jobrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/crypto"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/onepassword"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/store"
	syncengine "github.com/prabinv/1pass-vaultwarden-sync/internal/sync"
	"github.com/prabinv/1pass-vaultwarden-sync/internal/vaultwarden"
)

// Event is a single broadcast message sent to SSE subscribers.
type Event struct {
	Type    string
	Payload json.RawMessage
}

// Runner manages in-process sync goroutines and SSE subscriptions.
type Runner struct {
	pool      *pgxpool.Pool
	credStore *store.CredentialsStore
	jobStore  *store.JobStore

	mu   sync.RWMutex
	subs map[uuid.UUID][]chan Event
}

// New creates a new Runner. All arguments may be nil in tests.
func New(pool *pgxpool.Pool, credStore *store.CredentialsStore, jobStore *store.JobStore) *Runner {
	return &Runner{
		pool:      pool,
		credStore: credStore,
		jobStore:  jobStore,
		subs:      make(map[uuid.UUID][]chan Event),
	}
}

// Subscribe returns a channel that receives events for jobID and an unsubscribe
// function that removes the channel and closes it.
func (r *Runner) Subscribe(jobID uuid.UUID) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	r.mu.Lock()
	r.subs[jobID] = append(r.subs[jobID], ch)
	r.mu.Unlock()

	return ch, func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		subs := r.subs[jobID]
		for i, c := range subs {
			if c == ch {
				r.subs[jobID] = append(subs[:i], subs[i+1:]...)
				close(ch)
				return
			}
		}
	}
}

// Broadcast sends e to all current subscribers of jobID without blocking.
func (r *Runner) Broadcast(jobID uuid.UUID, e Event) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, ch := range r.subs[jobID] {
		select {
		case ch <- e:
		default:
		}
	}
}

// Enqueue launches the sync job in a new goroutine, detached from the request context.
// selectedIDs restricts the sync to items with those ExternalIDs; empty means all items.
func (r *Runner) Enqueue(ctx context.Context, jobID, userID uuid.UUID, selectedIDs []string) {
	go r.run(context.WithoutCancel(ctx), jobID, userID, selectedIDs)
}

func (r *Runner) run(ctx context.Context, jobID, userID uuid.UUID, selectedIDs []string) {
	fail := func(msg string) {
		slog.ErrorContext(ctx, "jobrunner: job failed", "job_id", jobID, "error", msg)
		r.jobStore.UpdateStatus(ctx, userID, jobID, "failed", &msg) //nolint:errcheck
		payload, _ := json.Marshal(map[string]string{"error": msg})
		r.Broadcast(jobID, Event{Type: "done", Payload: payload})
	}

	if err := r.jobStore.UpdateStatus(ctx, userID, jobID, "running", nil); err != nil {
		slog.ErrorContext(ctx, "jobrunner: mark running", "err", err)
		return
	}

	// Fetch credentials inside an RLS-protected transaction.
	var creds *store.Credentials
	if err := store.WithUserID(ctx, r.pool, userID, func(tx pgx.Tx) error {
		var e error
		creds, e = r.credStore.Get(ctx, tx, userID)
		return e
	}); err != nil {
		fail(fmt.Sprintf("fetch credentials: %v", err))
		return
	}

	// Perform the Vaultwarden identity exchange to obtain the access token and
	// the decrypted user symmetric key required for E2E field encryption.
	accessToken, userKey, err := crypto.Exchange(
		ctx,
		&http.Client{},
		creds.VaultwardenURL,
		creds.VaultwardenClientID,
		creds.VaultwardenClientSecret,
		creds.VaultwardenMasterPassword,
	)
	if err != nil {
		fail(fmt.Sprintf("vaultwarden identity exchange: %v", err))
		return
	}

	// Build 1Password client.
	opClient, err := onepassword.New(ctx, creds.OPServiceAccountToken)
	if err != nil {
		fail(fmt.Sprintf("1password client: %v", err))
		return
	}

	// Build Vaultwarden client with the bearer token and decrypted user key.
	vwClient := vaultwarden.New(creds.VaultwardenURL, accessToken, userKey)

	// Warm the Vaultwarden cipher cache before planning.
	if err := vwClient.WarmCache(ctx); err != nil {
		fail(fmt.Sprintf("warm vaultwarden cache: %v", err))
		return
	}

	// Plan the sync.
	engine := syncengine.NewEngine(opClient, vwClient)
	plan, err := engine.Plan(ctx)
	if err != nil {
		fail(fmt.Sprintf("plan: %v", err))
		return
	}

	if len(selectedIDs) > 0 {
		keep := make(map[string]bool, len(selectedIDs))
		for _, id := range selectedIDs {
			keep[id] = true
		}
		filtered := plan.Items[:0]
		for _, item := range plan.Items {
			if keep[item.Item.ExternalID] {
				filtered = append(filtered, item)
			}
		}
		plan.Items = filtered
	}

	// Apply the plan and forward progress events.
	progressCh := make(chan syncengine.ProgressEvent, 32)
	go func() {
		engine.Apply(ctx, plan, progressCh)
		close(progressCh)
	}()

	seq := 0
	for pe := range progressCh {
		payload, _ := json.Marshal(pe)
		e := Event{Type: pe.Action.String(), Payload: payload}
		r.Broadcast(jobID, e)
		if err := r.jobStore.AppendEvent(ctx, userID, jobID, seq, e.Type, e.Payload); err != nil {
			slog.ErrorContext(ctx, "jobrunner: append event", "err", err)
		}
		seq++
	}

	if err := r.jobStore.UpdateStatus(ctx, userID, jobID, "done", nil); err != nil {
		slog.ErrorContext(ctx, "jobrunner: mark done", "err", err)
	}
	donePayload, _ := json.Marshal(map[string]string{"error": ""})
	r.Broadcast(jobID, Event{Type: "done", Payload: donePayload})
}
