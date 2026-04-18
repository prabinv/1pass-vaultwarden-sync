package sync

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"golang.org/x/sync/semaphore"
)

// maxConcurrentWrites caps simultaneous CreateItem/UpdateItem calls to avoid
// overwhelming the Vaultwarden API.
const maxConcurrentWrites = 5

// Engine orchestrates the sync between a source and a sink.
type Engine struct {
	source ItemSource
	sink   ItemSink
}

// NewEngine constructs a new Engine.
func NewEngine(source ItemSource, sink ItemSink) *Engine {
	return &Engine{source: source, sink: sink}
}

// Plan fetches all items from the source, diffs them against the sink, and
// returns a SyncPlan describing what needs to be created, updated, or skipped.
// No writes are performed.
func (e *Engine) Plan(ctx context.Context) (SyncPlan, error) {
	slog.InfoContext(ctx, "fetching items from source")

	items, err := e.source.ListItems(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list source items", slog.Any("error", err))
		return SyncPlan{}, fmt.Errorf("listing source items: %w", err)
	}
	slog.InfoContext(ctx, "source items fetched", slog.Int("count", len(items)))

	plan := SyncPlan{Items: make([]PlanItem, 0, len(items))}

	for _, src := range items {
		existing, err := e.sink.GetItem(ctx, src.Name)
		if err != nil {
			slog.ErrorContext(ctx, "failed to get sink item",
				slog.String("name", src.Name),
				slog.Any("error", err))
			return SyncPlan{}, fmt.Errorf("getting sink item %s: %w", src.Name, err)
		}

		var (
			action         Action
			sinkExternalID string
		)
		switch {
		case existing == nil:
			action = ActionCreate
		case src.UpdatedAt.After(existing.UpdatedAt):
			action = ActionUpdate
			sinkExternalID = existing.ExternalID
		default:
			action = ActionSkip
		}

		slog.DebugContext(ctx, "item planned",
			slog.String("name", src.Name),
			slog.String("action", action.String()))
		plan.Items = append(plan.Items, PlanItem{Item: src, SinkExternalID: sinkExternalID, Action: action})
	}

	creates, updates, skips := plan.Counts()
	slog.InfoContext(ctx, "sync plan ready",
		slog.Int("create", creates),
		slog.Int("update", updates),
		slog.Int("skip", skips))

	return plan, nil
}

// Apply executes a SyncPlan, sending a ProgressEvent per item to the provided
// channel. Callers must drain the channel; Apply does not close it.
// Skips are emitted immediately; creates and updates run concurrently bounded
// by maxConcurrentWrites. Apply returns early if ctx is cancelled.
func (e *Engine) Apply(ctx context.Context, plan SyncPlan, events chan<- ProgressEvent) {
	// Emit skips first — they require no network I/O.
	var toProcess []PlanItem
	for _, pi := range plan.Items {
		if pi.Action == ActionSkip {
			if !sendEvent(ctx, events, ProgressEvent{Item: pi.Item, Action: ActionSkip}) {
				return
			}
			continue
		}
		toProcess = append(toProcess, pi)
	}

	if len(toProcess) == 0 {
		return
	}

	sem := semaphore.NewWeighted(maxConcurrentWrites)
	var wg sync.WaitGroup
	for _, pi := range toProcess {
		pi := pi
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := sem.Acquire(ctx, 1); err != nil {
				return // context cancelled
			}
			defer sem.Release(1)

			slog.InfoContext(ctx, "applying item",
				slog.String("name", pi.Item.Name),
				slog.String("action", pi.Action.String()))

			var err error
			switch pi.Action {
			case ActionCreate:
				err = e.sink.CreateItem(ctx, pi.Item)
			case ActionUpdate:
				// Use the sink's native ID so the correct record is addressed.
				itemToUpdate := pi.Item
				itemToUpdate.ExternalID = pi.SinkExternalID
				err = e.sink.UpdateItem(ctx, itemToUpdate)
			}

			if err != nil {
				slog.ErrorContext(ctx, "failed to apply item",
					slog.String("name", pi.Item.Name),
					slog.String("action", pi.Action.String()),
					slog.Any("error", err))
			}

			sendEvent(ctx, events, ProgressEvent{Item: pi.Item, Action: pi.Action, Err: err})
		}()
	}
	wg.Wait()
}

// sendEvent sends ev to ch, returning false immediately if ctx is cancelled.
func sendEvent(ctx context.Context, ch chan<- ProgressEvent, ev ProgressEvent) bool {
	select {
	case ch <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}
